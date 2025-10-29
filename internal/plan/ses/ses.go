package ses

import "fmt"

type SES struct {
	Alpha   float64
	WarmupN int
	s       float64
	seen    int
	inited  bool
}

func New(alpha float64, warmupN int) (*SES, error) {
	if !(alpha > 0 && alpha <= 1) {
		return nil, fmt.Errorf("alpha must be in (0,1], got %v", alpha)
	}
	if warmupN < 1 {
		return nil, fmt.Errorf("warmupN must be >= 1, got %d", warmupN)
	}
	return &SES{Alpha: alpha, WarmupN: warmupN}, nil
}

func (m *SES) Reset() {
	m.s = 0
	m.seen = 0
	m.inited = false
}

func (m *SES) Prime(s0 float64) {
	m.s = clamp01(s0)
	m.inited = true
	if m.seen == 0 {
		m.seen = 1
	}
}

func (m *SES) Observe(x float64) float64 {
	x = clamp01(x)
	if !m.inited {
		m.s = x
		m.inited = true
		m.seen = 1
		return m.s
	}
	m.s = m.Alpha*x + (1.0-m.Alpha)*m.s
	if m.seen < m.WarmupN {
		m.seen++
	}
	m.s = clamp01(m.s)
	return m.s
}

func (m *SES) Value() (float64, bool) {
	return m.s, m.inited
}

func (m *SES) Seen() int {
	return m.seen
}

func (m *SES) Warmed() bool {
	return m.inited && m.seen >= m.WarmupN
}

func (m *SES) CloneState() SES {
	return SES{
		Alpha:   m.Alpha,
		WarmupN: m.WarmupN,
		s:       m.s,
		seen:    m.seen,
		inited:  m.inited,
	}
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 100 {
		return 100
	}
	return x
}
