package metrics

type Snapshot struct {
	Gauges   map[string]float64 `json:"gauges"`
	Counters map[string]int64   `json:"counters"`
}

type SnapshotSource interface {
	Snapshot(ctx Context) (Snapshot, error)
}

type Context interface {
	Done() <-chan struct{}
	Err() error
}
