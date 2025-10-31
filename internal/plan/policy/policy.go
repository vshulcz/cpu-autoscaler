package policy

import (
	"fmt"
	"math"
	"sync"
	"time"
)

const (
	ReasonForecast   = "forecast"
	ReasonStabilized = "stabilized"
	ReasonMaxStep    = "max_step"
	ReasonFreeze     = "freeze"
)

type Limits struct {
	MinReplicas         int32
	MaxReplicas         int32
	MaxStepUp           int32
	MaxStepDown         int32
	StabilizationWindow time.Duration
	ErrorFreeze         time.Duration
}

type Inputs struct {
	Now             time.Time
	CurrentReplicas int32

	ForecastCPUPercent float64
	TargetCPUPercent   float64

	ValidMetrics bool
	Warmed       bool
}

type Decision struct {
	CurrentReplicas int32
	DesiredRaw      int32
	Applied         int32

	Limits Limits

	Reason string
}

type Planner struct {
	mu    sync.Mutex
	cfg   Limits
	state map[string]*keyState
}

type keyState struct {
	freezeUntil time.Time
	history     []histEntry
}

type histEntry struct {
	at      time.Time
	desired int32
}

func NewPlanner(cfg Limits) (*Planner, error) {
	if cfg.MinReplicas < 1 {
		return nil, fmt.Errorf("minReplicas must be >=1")
	}
	if cfg.MaxReplicas < cfg.MinReplicas {
		return nil, fmt.Errorf("maxReplicas must be >= minReplicas")
	}
	return &Planner{
		cfg:   cfg,
		state: make(map[string]*keyState),
	}, nil
}

func (p *Planner) Plan(key string, in Inputs) Decision {
	p.mu.Lock()
	defer p.mu.Unlock()

	st := p.getState(key)

	if !in.ValidMetrics || !in.Warmed {
		if in.Now.After(st.freezeUntil) {
			st.freezeUntil = in.Now.Add(p.cfg.ErrorFreeze)
		} else {
			st.freezeUntil = maxTime(st.freezeUntil, in.Now.Add(p.cfg.ErrorFreeze))
		}
		return Decision{
			CurrentReplicas: in.CurrentReplicas,
			DesiredRaw:      clamp(in.CurrentReplicas, p.cfg.MinReplicas, p.cfg.MaxReplicas),
			Applied:         in.CurrentReplicas,
			Limits:          p.cfg,
			Reason:          ReasonFreeze,
		}
	}

	if in.Now.Before(st.freezeUntil) {
		return Decision{
			CurrentReplicas: in.CurrentReplicas,
			DesiredRaw:      clamp(in.CurrentReplicas, p.cfg.MinReplicas, p.cfg.MaxReplicas),
			Applied:         in.CurrentReplicas,
			Limits:          p.cfg,
			Reason:          ReasonFreeze,
		}
	}

	desired := computeDesired(in.CurrentReplicas, in.ForecastCPUPercent, in.TargetCPUPercent)
	desired = clamp(desired, p.cfg.MinReplicas, p.cfg.MaxReplicas)

	p.pushHistory(st, in.Now, desired)
	stabilized := desired
	if desired < in.CurrentReplicas && p.cfg.StabilizationWindow > 0 {
		maxInWindow := p.maxDesiredInWindow(st, in.Now)
		if maxInWindow > 0 {
			floor := max(maxInWindow-p.cfg.MaxStepDown, p.cfg.MinReplicas)
			if stabilized < floor {
				stabilized = floor
			}
		}
	}

	applied, reason := applyStepLimits(in.CurrentReplicas, stabilized, p.cfg.MaxStepUp, p.cfg.MaxStepDown)

	return Decision{
		CurrentReplicas: in.CurrentReplicas,
		DesiredRaw:      desired,
		Applied:         applied,
		Limits:          p.cfg,
		Reason:          reason,
	}
}
func (p *Planner) getState(key string) *keyState {
	st, ok := p.state[key]
	if !ok {
		st = &keyState{}
		p.state[key] = st
	}
	return st
}

func (p *Planner) pushHistory(st *keyState, now time.Time, desired int32) {
	st.history = append(st.history, histEntry{at: now, desired: desired})
	if p.cfg.StabilizationWindow <= 0 {
		return
	}
	cut := now.Add(-p.cfg.StabilizationWindow)
	i := 0
	for ; i < len(st.history); i++ {
		if st.history[i].at.After(cut) || st.history[i].at.Equal(cut) {
			break
		}
	}
	if i > 0 {
		st.history = append([]histEntry{}, st.history[i:]...)
	}
}

func (p *Planner) maxDesiredInWindow(st *keyState, now time.Time) int32 {
	if len(st.history) == 0 {
		return 0
	}
	if p.cfg.StabilizationWindow <= 0 {
		return st.history[len(st.history)-1].desired
	}
	cut := now.Add(-p.cfg.StabilizationWindow)
	var maxD int32
	var have bool
	for i := len(st.history) - 1; i >= 0; i-- {
		if st.history[i].at.Before(cut) {
			break
		}
		if !have || st.history[i].desired > maxD {
			maxD = st.history[i].desired
			have = true
		}
	}
	if !have {
		return 0
	}
	return maxD
}

func (p *Planner) Clone() *Planner {
	p.mu.Lock()
	defer p.mu.Unlock()
	np := &Planner{
		cfg:   p.cfg,
		state: make(map[string]*keyState, len(p.state)),
	}
	for k, st := range p.state {
		hh := make([]histEntry, len(st.history))
		copy(hh, st.history)
		np.state[k] = &keyState{
			freezeUntil: st.freezeUntil,
			history:     hh,
		}
	}
	return np
}

func computeDesired(current int32, forecastCPU, targetCPU float64) int32 {
	if targetCPU <= 0 {
		return current
	}
	raw := float64(current) * (forecastCPU / targetCPU)
	if raw <= 0 {
		return 0
	}
	return int32(math.Ceil(raw))
}

func applyStepLimits(current, desired int32, maxUp, maxDown int32) (applied int32, reason string) {
	if desired == current {
		return current, ReasonForecast
	}
	if desired > current {
		if maxUp > 0 {
			up := desired - current
			if up > maxUp {
				return current + maxUp, ReasonMaxStep
			}
		}
		return desired, ReasonForecast
	}
	if maxDown > 0 {
		down := current - desired
		if down > maxDown {
			return current - maxDown, ReasonMaxStep
		}
	}
	return desired, ReasonForecast
}

func clamp(x, lo, hi int32) int32 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
