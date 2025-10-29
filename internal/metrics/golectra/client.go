package golectra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/vshulcz/cpu-autoscaler/internal/metrics"
)

type Client struct {
	base         *url.URL
	snapshotPath string
	hc           *http.Client
	timeout      time.Duration

	backoffs []time.Duration
}

type Options struct {
	BaseURL      string
	SnapshotPath string
	Timeout      time.Duration
	HTTPClient   *http.Client
}

func New(opts Options) (*Client, error) {
	if strings.TrimSpace(opts.BaseURL) == "" {
		return nil, fmt.Errorf("baseURL required")
	}
	u, err := url.Parse(normalizeBase(opts.BaseURL))
	if err != nil {
		return nil, fmt.Errorf("parse baseURL: %w", err)
	}
	if opts.SnapshotPath == "" {
		opts.SnapshotPath = "/api/v1/snapshot"
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 800 * time.Millisecond
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: opts.Timeout}
	}

	return &Client{
		base:         u,
		snapshotPath: opts.SnapshotPath,
		hc:           hc,
		timeout:      opts.Timeout,
		backoffs:     []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond},
	}, nil
}

func normalizeBase(s string) string {
	s = strings.TrimRight(s, "/")
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return s
	}
	return "http://" + s
}

func (c *Client) endpoint(p string) string {
	u := *c.base
	u.Path = path.Join(strings.TrimRight(c.base.Path, "/"), strings.TrimLeft(p, "/"))
	return u.String()
}

func (c *Client) Snapshot(ctx context.Context) (metrics.Snapshot, error) {
	var lastErr error
	for attempt := range 3 {
		if attempt > 0 {
			bo := c.backoffs[attempt-1]
			timer := time.NewTimer(bo)
			select {
			case <-ctx.Done():
				timer.Stop()
				return metrics.Snapshot{}, &metrics.MetricsFetchError{Err: ctx.Err()}
			case <-timer.C:
			}
		}

		snap, err := c.doOnce(ctx)
		if err == nil {
			return snap, nil
		}
		if !isRetryable(err) || attempt == 2 {
			return metrics.Snapshot{}, &metrics.MetricsFetchError{Err: err}
		}
		lastErr = err
	}
	return metrics.Snapshot{}, &metrics.MetricsFetchError{Err: lastErr}
}

func (c *Client) doOnce(ctx context.Context) (metrics.Snapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint(c.snapshotPath), nil)
	if err != nil {
		return metrics.Snapshot{}, err
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return metrics.Snapshot{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return metrics.Snapshot{}, fmt.Errorf("http status: %s", resp.Status)
	}

	dec := json.NewDecoder(resp.Body)
	var tmp struct {
		Gauges   map[string]float64 `json:"gauges"`
		Counters map[string]int64   `json:"counters"`
	}
	if err := dec.Decode(&tmp); err != nil {
		return metrics.Snapshot{}, err
	}
	if tmp.Gauges == nil {
		tmp.Gauges = map[string]float64{}
	}
	if tmp.Counters == nil {
		tmp.Counters = map[string]int64{}
	}
	return metrics.Snapshot{Gauges: tmp.Gauges, Counters: tmp.Counters}, nil
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "http status: 502") ||
		strings.Contains(msg, "http status: 503") ||
		strings.Contains(msg, "http status: 504") ||
		strings.Contains(msg, "http status: 429")
}
