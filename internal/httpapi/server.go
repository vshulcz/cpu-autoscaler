package httpapi

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"time"
)

type Previewer interface {
	PreviewPlan(ctx context.Context, in any) (out any, err error)
}

type PreviewFunc func(ctx context.Context, reqBody []byte) (status int, respBody []byte)

type Server struct {
	addr       string
	httpServer *http.Server
	onPreview  PreviewFunc
	readyCheck func() bool
}

type Options struct {
	Addr       string
	Preview    PreviewFunc
	ReadyCheck func() bool
}

func New(opts Options) *Server {
	if opts.Addr == "" {
		opts.Addr = ":8080"
	}
	if opts.ReadyCheck == nil {
		opts.ReadyCheck = func() bool { return true }
	}
	s := &Server{addr: opts.Addr, onPreview: opts.Preview, readyCheck: opts.ReadyCheck}
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if s.readyCheck() {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ready"))
			return
		}
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	})

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("# metrics served by controller-runtime metrics server\n"))
	})

	mux.HandleFunc("/api/v1/preview-plan", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := ioReadAllLimit(r.Body, 1<<20) // 1 MiB
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if s.onPreview == nil {
			http.Error(w, "preview not configured", http.StatusServiceUnavailable)
			return
		}
		status, resp := s.onPreview(r.Context(), body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write(resp)
	})

	s.httpServer = &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
	}
	return s
}

func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		s.httpServer.Shutdown(context.Background())
	}()
	return s.httpServer.Serve(ln)
}

func ioReadAllLimit(r io.Reader, limit int64) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(io.LimitReader(r, limit)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
