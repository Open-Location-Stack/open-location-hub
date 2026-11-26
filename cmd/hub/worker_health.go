package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

func serveWorker(ctx context.Context, run func(context.Context) error) error {
	address := os.Getenv("HUB_WORKER_HEALTH_ADDR")
	if address == "" {
		return run(ctx)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("bind worker health endpoint: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	return run(ctx)
}
