package metrics

import "testing"

func TestAggregateCPUPercent_OK(t *testing.T) {
	g := map[string]float64{
		"CPUutilization1": 10,
		"CPUutilization2": 30,
		"CPUutilizationX": 50,
		"Other":           99,
	}
	got, err := AggregateCPUPercent(g, "CPUutilization")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got < 19.9 || got > 20.1 {
		t.Fatalf("avg want 20, got %v", got)
	}
}

func TestAggregateCPUPercent_InvalidValues(t *testing.T) {
	g := map[string]float64{
		"CPUutilization1": -1,
		"CPUutilization2": 200,
	}
	_, err := AggregateCPUPercent(g, "CPUutilization")
	if err != ErrNoValidCPU {
		t.Fatalf("want ErrNoValidCPU, got %v", err)
	}
}

func TestAggregateCPUPercent_NoKeys(t *testing.T) {
	g := map[string]float64{"Other": 10}
	_, err := AggregateCPUPercent(g, "CPUutilization")
	if err != ErrNoCPUKeys {
		t.Fatalf("want ErrNoCPUKeys, got %v", err)
	}
}
