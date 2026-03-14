package mux

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/jotavich/xnullclaw/internal/logging"
)

// healthPort is the default port for the K8s liveness probe endpoint.
const healthPort = "8086"

// startHealthServer starts a minimal HTTP server exposing /healthz for K8s
// liveness/readiness probes. Only used in kubernetes runtime mode.
// Returns the server so the caller can shut it down.
func startHealthServer(logger *logging.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              "0.0.0.0:" + healthPort,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		ln, err := net.Listen("tcp", srv.Addr)
		if err != nil {
			logger.Error("health server listen failed", "addr", srv.Addr, "error", err)
			return
		}
		logger.Info("health server started", "addr", srv.Addr)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Error("health server error", "error", err)
		}
	}()

	return srv
}

// stopHealthServer gracefully shuts down the health server.
func stopHealthServer(srv *http.Server) {
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}
