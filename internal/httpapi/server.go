package httpapi

import (
	"context"
	"errors"
	"io"
	"log"
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
		if _, err := w.Write([]byte("ok")); err != nil {
			log.Printf("health write failed: %v", err)
		}
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if s.readyCheck() {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("ready")); err != nil {
				log.Printf("ready write failed: %v", err)
			}
			return
		}
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	})

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("# metrics served by controller-runtime metrics server\n")); err != nil {
			log.Printf("metrics banner write failed: %v", err)
		}
	})

	mux.HandleFunc("/api/v1/preview-plan", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB
		defer func() { _ = r.Body.Close() }()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var mbErr *http.MaxBytesError
			if errors.As(err, &mbErr) {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
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
		if _, err := w.Write(resp); err != nil {
			log.Printf("preview-plan write failed: %v", err)
		}
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
		if err := s.httpServer.Shutdown(context.Background()); err != nil {
			log.Printf("http server shutdown: %v", err)
		}
	}()
	return s.httpServer.Serve(ln)
}
