package ses

import (
	"math"
	"testing"
)

func TestNew_Args(t *testing.T) {
	if _, err := New(0, 1); err == nil {
		t.Fatalf("alpha=0 must be invalid")
	}
	if _, err := New(1.1, 1); err == nil {
		t.Fatalf("alpha>1 must be invalid")
	}
	if _, err := New(0.5, 0); err == nil {
		t.Fatalf("warmupN<1 must be invalid")
	}
	if _, err := New(1, 1); err != nil {
		t.Fatalf("alpha=1 should be ok, got %v", err)
	}
}

func TestSES_WarmupAndObserve(t *testing.T) {
	m, _ := New(0.5, 3)

	s := m.Observe(50)
	if s != 50 {
		t.Fatalf("S1 want 50, got %v", s)
	}
	if m.Warmed() {
		t.Fatalf("must not be warmed after 1st point")
	}

	s = m.Observe(70)
	if s != 60 {
		t.Fatalf("S2 want 60, got %v", s)
	}
	if m.Warmed() {
		t.Fatalf("must not be warmed after 2nd point")
	}

	s = m.Observe(90)
	if s != 75 {
		t.Fatalf("S3 want 75, got %v", s)
	}
	if !m.Warmed() {
		t.Fatalf("must be warmed after 3rd point")
	}
}

func TestSES_ConvergenceToConstant(t *testing.T) {
	m, _ := New(0.3, 2)
	m.Observe(0)
	target := 80.0
	s := 0.0
	for range 100 {
		s = m.Observe(target)
	}
	if math.Abs(s-target) > 0.001 {
		t.Fatalf("S_t must approach target; got %v", s)
	}
}

func TestSES_AlphaOne(t *testing.T) {
	m, _ := New(1, 2)
	m.Observe(20)
	s := m.Observe(80)
	if s != 80 {
		t.Fatalf("alpha=1 must follow x_t exactly; got %v", s)
	}
	if !m.Warmed() {
		t.Fatalf("must be warmed")
	}
}

func TestSES_Clamp(t *testing.T) {
	m, _ := New(0.5, 1)
	s := m.Observe(200)
	if s != 100 {
		t.Fatalf("clamp to 100 expected, got %v", s)
	}
	s = m.Observe(-10)
	if s != 50 {
		t.Fatalf("expected 50 after clamped -10, got %v", s)
	}
}

func TestSES_Prime(t *testing.T) {
	m, _ := New(0.4, 3)
	m.Prime(60)
	if v, ok := m.Value(); !ok || v != 60 {
		t.Fatalf("prime failed, got (%v,%v)", v, ok)
	}
	if m.Warmed() {
		t.Fatalf("should not be warmed yet")
	}
	m.Observe(60)
	m.Observe(60)
	if !m.Warmed() {
		t.Fatalf("must be warmed after 3rd effective point")
	}
}
