package engine

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/restayway/regbot/internal/config"
	"github.com/restayway/regbot/pkg/plan"
	"github.com/restayway/regbot/pkg/provider"
)

type fakeProvider struct {
	artifacts []provider.Artifact
	deleted   []provider.DeleteRequest
	deleteErr error
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Preflight(context.Context) (provider.Capabilities, error) {
	return provider.Capabilities{Delete: true}, nil
}
func (f *fakeProvider) List(context.Context, provider.Target) ([]provider.Artifact, error) {
	return append([]provider.Artifact(nil), f.artifacts...), nil
}
func (f *fakeProvider) Delete(_ context.Context, request provider.DeleteRequest) error {
	f.deleted = append(f.deleted, request)
	return f.deleteErr
}

func TestPlanAndApplyConverges(t *testing.T) {
	now := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	fake := &fakeProvider{artifacts: []provider.Artifact{
		{Provider: "oci", Registry: "local", Repository: "api", ID: "new", Digest: "new", Tags: []string{"v2026.07.19.1-api"}},
		{Provider: "oci", Registry: "local", Repository: "api", ID: "old", Digest: "old", Tags: []string{"v2026.01.01.1-api"}},
	}}
	cfg := testConfig(100)
	runner := &Engine{
		Config: cfg, ConfigBytes: []byte("config"), Providers: map[string]provider.Provider{"local": fake},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }, Concurrency: 1,
	}
	proposal, err := runner.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(proposal.Actions) != 1 || proposal.Actions[0].Artifact.ID != "old" {
		t.Fatalf("unexpected plan: %+v", proposal)
	}
	result, err := runner.Apply(context.Background(), proposal)
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 1 || len(fake.deleted) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestPlanRejectsSafetyLimit(t *testing.T) {
	now := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	fake := &fakeProvider{artifacts: []provider.Artifact{
		{Registry: "local", Repository: "api", ID: "one", Tags: []string{"v2026.01.01.1-api"}},
		{Registry: "local", Repository: "api", ID: "two", Tags: []string{"v2026.01.02.1-api"}},
		{Registry: "local", Repository: "api", ID: "three", Tags: []string{"v2026.01.03.1-api"}},
	}}
	cfg := testConfig(10)
	runner := &Engine{Config: cfg, ConfigBytes: []byte("config"), Providers: map[string]provider.Provider{"local": fake}, Now: func() time.Time { return now }}
	_, err := runner.Plan(context.Background())
	if !errors.Is(err, ErrSafety) {
		t.Fatalf("expected safety error, got %v", err)
	}
}

func TestApplyRejectsExpiredOrChangedPlan(t *testing.T) {
	now := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	runner := &Engine{Config: testConfig(100), ConfigBytes: []byte("config"), Now: func() time.Time { return now }}
	_, err := runner.Apply(context.Background(), plan.Plan{Version: "v1", ConfigFingerprint: "wrong", ExpiresAt: now.Add(time.Hour)})
	if !errors.Is(err, ErrStalePlan) {
		t.Fatalf("expected stale plan, got %v", err)
	}
}

func testConfig(maxPercent float64) *config.Config {
	return &config.Config{
		Version: "v1", Registries: map[string]config.Registry{"local": {Provider: "oci", Endpoint: "https://example.com"}},
		Policies: map[string]config.Policy{"default": {
			Targets: []config.Target{{Registry: "local"}},
			Tags:    config.Tags{Parser: "calendar"},
			Retention: config.Retention{
				DeleteOlderThan: config.Duration{Duration: 30 * 24 * time.Hour},
				KeepAtLeast:     1, KeepLatest: 1, GroupBy: "app",
			},
			Safety: config.Safety{MaxDeleteCount: 10, MaxDeletePercent: maxPercent},
		}},
	}
}
