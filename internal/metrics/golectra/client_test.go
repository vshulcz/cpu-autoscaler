package golectra

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_Snapshot_OK(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(map[string]any{
			"gauges": map[string]float64{
				"CPUutilization1": 12.3,
				"HeapAlloc":       123,
			},
			"counters": map[string]int64{"PollCount": 7},
		}); err != nil {
			t.Fatalf("encode failed: %v", err)
		}

	}))
	defer s.Close()

	cli, err := New(Options{BaseURL: s.URL, SnapshotPath: "/api/v1/snapshot", Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	snap, err := cli.Snapshot(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if snap.Gauges["CPUutilization1"] != 12.3 || snap.Counters["PollCount"] != 7 {
		t.Fatalf("bad payload: %+v", snap)
	}
}

func TestClient_Snapshot_RetryOn502(t *testing.T) {
	var calls int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"gauges":   map[string]float64{"CPUutilization1": 42},
			"counters": map[string]int64{},
		}); err != nil {
			t.Fatalf("encode failed: %v", err)
		}
	}))
	defer s.Close()

	cli, err := New(Options{BaseURL: s.URL, Timeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	snap, err := cli.Snapshot(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if snap.Gauges["CPUutilization1"] != 42 {
		t.Fatalf("want 42, got %v", snap.Gauges["CPUutilization1"])
	}
}
