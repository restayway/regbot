package scheduler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/restayway/regbot/internal/metrics"
	"github.com/restayway/regbot/pkg/plan"
)

func TestHealthHandlerDoesNotExposeRun(t *testing.T) {
	t.Parallel()
	handler := healthHandler(prometheus.NewRegistry())
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Errorf("GET %s status = %d", path, response.Code)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/run", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("POST /run status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestRunOnStart(t *testing.T) {
	t.Parallel()
	schedule, err := scheduleParser.Parse("17 3 * * *")
	if err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewRegistry()
	runMetrics := metrics.New(registry)
	scheduleMetrics := metrics.NewScheduler(registry)
	ctx, cancel := context.WithCancel(context.Background())
	called := make(chan struct{}, 1)
	instance := &Scheduler{
		Location: time.UTC, RunOnStart: true, Timeout: time.Second,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Run: func(context.Context) (plan.Result, error) {
			called <- struct{}{}
			return plan.Result{Planned: 2}, nil
		},
	}
	done := make(chan struct{})
	go func() {
		instance.loop(ctx, schedule, runMetrics, scheduleMetrics)
		close(done)
	}()
	select {
	case <-called:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("run-on-start did not execute")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
}

func TestListenAndServeRunsOnStartAndShutsDown(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	called := make(chan struct{}, 1)
	instance := &Scheduler{
		Address: "127.0.0.1:0", Expression: "17 3 * * *", Location: time.UTC,
		RunOnStart: true, Timeout: time.Second,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Run: func(context.Context) (plan.Result, error) {
			called <- struct{}{}
			return plan.Result{Planned: 1}, nil
		},
	}
	done := make(chan error, 1)
	go func() {
		done <- instance.ListenAndServe(ctx)
	}()
	select {
	case <-called:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("scheduler did not execute run-on-start")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduler did not shut down")
	}
}

func TestSkippedOccurrences(t *testing.T) {
	t.Parallel()
	schedule, err := scheduleParser.Parse("* * * * *")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	finished := started.Add(2*time.Minute + 30*time.Second)
	if got := skippedOccurrences(schedule, started, finished); got != 2 {
		t.Fatalf("skippedOccurrences() = %d, want 2", got)
	}
}

func TestRunTimeoutCancelsExecution(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	runMetrics := metrics.New(registry)
	scheduleMetrics := metrics.NewScheduler(registry)
	canceled := make(chan error, 1)
	instance := &Scheduler{
		Timeout: 10 * time.Millisecond,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Run: func(ctx context.Context) (plan.Result, error) {
			<-ctx.Done()
			canceled <- ctx.Err()
			return plan.Result{}, ctx.Err()
		},
	}
	instance.execute(context.Background(), time.Now(), runMetrics, scheduleMetrics)
	if err := <-canceled; err != context.DeadlineExceeded {
		t.Fatalf("run context error = %v, want %v", err, context.DeadlineExceeded)
	}
}

func TestScheduleUsesConfiguredTimezone(t *testing.T) {
	t.Parallel()
	location, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := scheduleParser.Parse("CRON_TZ=Europe/Istanbul 17 3 * * *")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	want := time.Date(2026, 7, 20, 3, 17, 0, 0, location)
	if got := schedule.Next(from); !got.Equal(want) {
		t.Fatalf("next = %s, want %s", got, want)
	}
}
