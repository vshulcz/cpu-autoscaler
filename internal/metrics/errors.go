package metrics

import "errors"

var (
	ErrNoCPUKeys  = errors.New("no CPUutilization* keys found")
	ErrNoValidCPU = errors.New("no valid CPU values in [0,100]")
)

type MetricsFetchError struct {
	Err error
}

func (e *MetricsFetchError) Error() string {
	if e == nil || e.Err == nil {
		return "metrics fetch error"
	}
	return "metrics fetch error: " + e.Err.Error()
}

func (e *MetricsFetchError) Unwrap() error { return e.Err }
