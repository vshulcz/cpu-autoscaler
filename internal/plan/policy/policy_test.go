package policy

import (
	"testing"
	"time"
)

func mustPlanner() *Planner {
	p, err := NewPlanner(Limits{
		MinReplicas:         1,
		MaxReplicas:         50,
		MaxStepUp:           5,
		MaxStepDown:         3,
		StabilizationWindow: 60 * time.Second,
		ErrorFreeze:         60 * time.Second,
	})
	if err != nil {
		panic(err)
	}
	return p
}

func TestComputeDesiredFormulaAndClamp(t *testing.T) {
	p := mustPlanner()
	now := time.Now()
	in := Inputs{
		Now:                now,
		CurrentReplicas:    6,
		ForecastCPUPercent: 68.2,
		TargetCPUPercent:   60,
		ValidMetrics:       true,
		Warmed:             true,
	}
	d := p.Plan("ns/app", in)
	if d.DesiredRaw != 7 {
		t.Fatalf("desired raw want 7, got %d", d.DesiredRaw)
	}
	if d.Applied != 7 {
		t.Fatalf("applied want 7, got %d", d.Applied)
	}
}

func TestClampMinMax(t *testing.T) {
	p := mustPlanner()
	now := time.Now()
	in := Inputs{
		Now:                now,
		CurrentReplicas:    1,
		ForecastCPUPercent: 1,
		TargetCPUPercent:   60,
		ValidMetrics:       true,
		Warmed:             true,
	}
	d := p.Plan("k", in)
	if d.DesiredRaw < 1 {
		t.Fatalf("desired must be clamped to min=1; got %d", d.DesiredRaw)
	}

	in2 := in
	in2.CurrentReplicas = 50
	in2.ForecastCPUPercent = 300
	in2.TargetCPUPercent = 10
	d2 := p.Plan("k2", in2)
	if d2.DesiredRaw > 50 {
		t.Fatalf("desired must be clamped to max=50; got %d", d2.DesiredRaw)
	}
}

func TestStepLimitsUpDown(t *testing.T) {
	p := mustPlanner()
	now := time.Now()

	in := Inputs{
		Now:                now,
		CurrentReplicas:    2,
		ForecastCPUPercent: 600,
		TargetCPUPercent:   60,
		ValidMetrics:       true,
		Warmed:             true,
	}
	d := p.Plan("up", in)
	if d.Applied != 2+5 || d.Reason != ReasonMaxStep {
		t.Fatalf("scale up step limit failed: %+v", d)
	}

	in2 := Inputs{
		Now:                now.Add(1 * time.Second),
		CurrentReplicas:    10,
		ForecastCPUPercent: 6,
		TargetCPUPercent:   60,
		ValidMetrics:       true,
		Warmed:             true,
	}
	d2 := p.Plan("down", in2)
	if d2.Applied != 10-3 || d2.Reason != ReasonMaxStep {
		t.Fatalf("scale down step limit failed: %+v", d2)
	}
}

func TestStabilizationWindowPreventsAggressiveDownscale(t *testing.T) {
	p := mustPlanner()
	key := "stable"
	now := time.Now()

	d1 := p.Plan(key, Inputs{
		Now:                now,
		CurrentReplicas:    10,
		ForecastCPUPercent: 120,
		TargetCPUPercent:   60,
		ValidMetrics:       true,
		Warmed:             true,
	})
	if d1.DesiredRaw != 20 {
		t.Fatalf("want desired=20, got %d", d1.DesiredRaw)
	}

	d2 := p.Plan(key, Inputs{
		Now:                now.Add(10 * time.Second),
		CurrentReplicas:    20,
		ForecastCPUPercent: 3,
		TargetCPUPercent:   60,
		ValidMetrics:       true,
		Warmed:             true,
	})
	if d2.DesiredRaw != 1 {
		t.Fatalf("raw desired should be 1; got %d", d2.DesiredRaw)
	}
	if d2.Applied != 20-3 {
		t.Fatalf("applied should respect stabilization+stepdown; got %d", d2.Applied)
	}
	if d2.Reason != ReasonMaxStep && d2.Reason != ReasonForecast {
		t.Fatalf("reason should be max_step or forecast, got %s", d2.Reason)
	}
}

func TestFreezeOnInvalidMetricsAndWarmup(t *testing.T) {
	p := mustPlanner()
	key := "freeze"
	now := time.Now()

	d1 := p.Plan(key, Inputs{
		Now:                now,
		CurrentReplicas:    6,
		ForecastCPUPercent: 0,
		TargetCPUPercent:   60,
		ValidMetrics:       false,
		Warmed:             true,
	})
	if d1.Reason != ReasonFreeze || d1.Applied != 6 {
		t.Fatalf("freeze expected on invalid metrics: %+v", d1)
	}

	d2 := p.Plan(key, Inputs{
		Now:                now.Add(10 * time.Second),
		CurrentReplicas:    6,
		ForecastCPUPercent: 90,
		TargetCPUPercent:   60,
		ValidMetrics:       true,
		Warmed:             true,
	})
	if d2.Reason != ReasonFreeze {
		t.Fatalf("still in freeze window, expected freeze, got %s", d2.Reason)
	}

	d3 := p.Plan(key, Inputs{
		Now:                now.Add(70 * time.Second),
		CurrentReplicas:    6,
		ForecastCPUPercent: 90,
		TargetCPUPercent:   60,
		ValidMetrics:       true,
		Warmed:             true,
	})
	if d3.Reason == ReasonFreeze {
		t.Fatalf("freeze should be over by now")
	}
}
