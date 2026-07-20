// Package server exposes optional stateless HTTP execution and metrics.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/restayway/regbot/internal/engine"
	"github.com/restayway/regbot/internal/metrics"
	"github.com/restayway/regbot/pkg/plan"
)

type Server struct {
	Address string
	Token   string
	Engine  *engine.Engine
	Logger  *slog.Logger
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	if err := validateBinding(s.Address, s.Token); err != nil {
		return err
	}
	registry := prometheus.NewRegistry()
	observability := metrics.New(registry)
	var running atomic.Bool
	var applyMu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ready\n"))
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("POST /run", func(writer http.ResponseWriter, request *http.Request) {
		if !authorized(request, s.Token) {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !running.CompareAndSwap(false, true) {
			http.Error(writer, "a run is already in progress", http.StatusConflict)
			return
		}
		defer running.Store(false)
		applyMu.Lock()
		defer applyMu.Unlock()
		proposal, err := s.Engine.Plan(request.Context())
		if err != nil {
			observability.Runs.WithLabelValues("failure").Inc()
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		result := plan.Result{Version: plan.FormatVersion, StartedAt: time.Now().UTC(), Planned: len(proposal.Actions)}
		if s.Engine.Config.Apply {
			result, err = s.Engine.Apply(request.Context(), proposal)
		} else {
			result.FinishedAt = time.Now().UTC()
		}
		observability.Observe(result, err)
		writer.Header().Set("Content-Type", "application/json")
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
		}
		_ = json.NewEncoder(writer).Encode(struct {
			Plan   plan.Plan   `json:"plan"`
			Result plan.Result `json:"result"`
			Error  string      `json:"error,omitempty"`
		}{Plan: proposal, Result: result, Error: errorString(err)})
	})
	httpServer := &http.Server{
		Addr: s.Address, Handler: mux, ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 60 * time.Second, WriteTimeout: 10 * time.Minute,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	s.Logger.Info("HTTP server listening", "address", s.Address)
	err := httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func validateBinding(address, token string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	if token != "" || host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	return errors.New("a run bearer token is required when listening on a non-loopback address")
}

func authorized(request *http.Request, token string) bool {
	if token == "" {
		return true
	}
	value := request.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(value) < len(prefix) || value[:len(prefix)] != prefix {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(value[len(prefix):]), []byte(token)) == 1
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
