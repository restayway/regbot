// Package scheduler runs Regbot on a cron schedule and exposes health and metrics.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/restayway/regbot/internal/metrics"
	"github.com/restayway/regbot/pkg/plan"
	"github.com/robfig/cron/v3"
)

const defaultRunTimeout = time.Hour

var scheduleParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// RunSummary contains low-cardinality counts for one scheduled workflow.
type RunSummary struct {
	Result     plan.Result
	DryRun     bool
	Discovered int
	Protected  int
}

// RunFunc executes one complete scheduled plan/apply workflow.
type RunFunc func(context.Context) (RunSummary, error)

// Scheduler runs a workflow on a cron schedule and serves operational endpoints.
type Scheduler struct {
	Address    string
	Expression string
	Location   *time.Location
	RunOnStart bool
	Timeout    time.Duration
	Run        RunFunc
	Logger     *slog.Logger
}

func (s *Scheduler) ListenAndServe(ctx context.Context) error {
	if s.Run == nil {
		return errors.New("scheduler run function is required")
	}
	if s.Location == nil {
		return errors.New("scheduler location is required")
	}
	schedule, err := scheduleParser.Parse("CRON_TZ=" + s.Location.String() + " " + s.Expression)
	if err != nil {
		return fmt.Errorf("parse schedule: %w", err)
	}
	if s.Timeout == 0 {
		s.Timeout = defaultRunTimeout
	}
	if s.Timeout < 0 {
		return errors.New("scheduler timeout must be positive")
	}
	if s.Logger == nil {
		s.Logger = slog.Default()
	}

	registry := prometheus.NewRegistry()
	runMetrics := metrics.New(registry)
	scheduleMetrics := metrics.NewScheduler(registry)
	handler := healthHandler(registry)
	server := &http.Server{
		Addr: s.Address, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 60 * time.Second, WriteTimeout: 30 * time.Second,
	}
	listener, err := net.Listen("tcp", s.Address)
	if err != nil {
		return err
	}

	runtimeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Serve(listener)
	}()
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		s.loop(runtimeCtx, schedule, runMetrics, scheduleMetrics)
	}()

	s.Logger.Info("scheduler listening",
		"address", s.Address,
		"cron", s.Expression,
		"timezone", s.Location.String(),
		"run_on_start", s.RunOnStart,
	)

	select {
	case <-ctx.Done():
	case err = <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			cancel()
			return err
		}
	}

	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	select {
	case <-loopDone:
	case <-shutdownCtx.Done():
		if shutdownErr == nil {
			shutdownErr = shutdownCtx.Err()
		}
	}
	return shutdownErr
}

func (s *Scheduler) loop(
	ctx context.Context,
	schedule cron.Schedule,
	runMetrics *metrics.Metrics,
	scheduleMetrics *metrics.Scheduler,
) {
	if s.RunOnStart {
		s.execute(ctx, time.Now(), runMetrics, scheduleMetrics)
	}

	for {
		now := time.Now().In(s.Location)
		next := schedule.Next(now)
		scheduleMetrics.NextRunTimestamp.Set(float64(next.Unix()))
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}

		s.execute(ctx, next, runMetrics, scheduleMetrics)
		finished := time.Now().In(s.Location)
		skipped := skippedOccurrences(schedule, next, finished)
		if skipped > 0 {
			scheduleMetrics.SkippedOverlaps.Add(float64(skipped))
			s.Logger.Warn("scheduled occurrences skipped while previous run was active", "count", skipped)
		}
	}
}

func (s *Scheduler) execute(
	ctx context.Context,
	scheduledAt time.Time,
	runMetrics *metrics.Metrics,
	scheduleMetrics *metrics.Scheduler,
) {
	if ctx.Err() != nil {
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	scheduleMetrics.Running.Set(1)
	defer scheduleMetrics.Running.Set(0)
	s.Logger.Info("scheduled run started", "scheduled_at", scheduledAt)

	started := time.Now().UTC()
	summary, err := s.Run(runCtx)
	result := summary.Result
	if result.StartedAt.IsZero() {
		result.StartedAt = started
	}
	if result.FinishedAt.IsZero() {
		result.FinishedAt = time.Now().UTC()
	}
	runMetrics.Observe(result, err)
	scheduleMetrics.LastRunTimestamp.Set(float64(result.FinishedAt.Unix()))

	attributes := []any{
		"dry_run", summary.DryRun,
		"discovered", summary.Discovered,
		"protected", summary.Protected,
		"planned", result.Planned,
		"deleted", result.Deleted,
		"skipped", result.Skipped,
		"failed", result.Failed,
		"duration_seconds", result.FinishedAt.Sub(result.StartedAt).Seconds(),
	}
	if err != nil {
		attributes = append(attributes, "error", err)
		s.Logger.Error("scheduled run failed", attributes...)
		return
	}
	s.Logger.Info("scheduled run completed", attributes...)
}

func healthHandler(registry prometheus.Gatherer) http.Handler {
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
	return mux
}

func skippedOccurrences(schedule cron.Schedule, started, finished time.Time) int {
	skipped := 0
	for next := schedule.Next(started); !next.After(finished); next = schedule.Next(next) {
		skipped++
	}
	return skipped
}
