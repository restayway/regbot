package policy

import (
	"testing"
	"time"

	"github.com/restayway/regbot/pkg/provider"
)

func TestParseCalendar(t *testing.T) {
	t.Parallel()
	tests := []struct {
		tag      string
		app      string
		sequence uint64
		wantErr  bool
	}{
		{tag: "v2026.07.20.3", sequence: 3},
		{tag: "v2024.02.29.0-api-v2", app: "api-v2"},
		{tag: "v2023.02.29.1", wantErr: true},
		{tag: "2026.07.20.1", wantErr: true},
		{tag: "v2026.07.20.-1", wantErr: true},
		{tag: "v2026.07.20.1-API", wantErr: true},
		{tag: "v2026.07.20.1-", wantErr: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.tag, func(t *testing.T) {
			t.Parallel()
			got, err := ParseCalendar(test.tag)
			if (err != nil) != test.wantErr {
				t.Fatalf("ParseCalendar() error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && (got.App != test.app || got.Sequence != test.sequence) {
				t.Fatalf("ParseCalendar() = %+v", got)
			}
		})
	}
}

func TestEvaluateKeepsLatestAndMinimumByApp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	artifacts := []provider.Artifact{
		artifact("one", "v2026.07.01.1-api"),
		artifact("two", "v2026.06.01.1-api"),
		artifact("three", "v2026.05.01.1-api"),
		artifact("web", "v2026.01.01.1-web"),
	}
	rule := Rule{
		DeleteOlderThan: 7 * 24 * time.Hour, KeepLatest: 1, KeepAtLeast: 1,
		GroupByApp: true, ProtectUnparsed: true, RequireTagged: true,
		MaxDeleteCount: 10, MaxDeletePercent: 100,
	}
	decisions := Evaluate(artifacts, rule, now)
	assertDelete(t, decisions, "one", false)
	assertDelete(t, decisions, "two", true)
	assertDelete(t, decisions, "three", true)
	assertDelete(t, decisions, "web", false)
}

func TestEvaluateProtectsUnparsedSharedAndReferrers(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	shared := artifact("shared", "v2025.01.01.1-api")
	shared.Tags = append(shared.Tags, "v2025.01.01.1-web")
	shared.ParsedVersions = append(shared.ParsedVersions, mustVersion("v2025.01.01.1-web"))
	referrer := artifact("signed", "v2025.01.01.1-worker")
	referrer.ReferrerIDs = []string{"sha256:sig"}
	unparsed := provider.Artifact{ID: "latest", Tags: []string{"latest"}, CreatedAt: now.AddDate(-1, 0, 0)}
	rule := Rule{DeleteOlderThan: 24 * time.Hour, KeepAtLeast: 1, GroupByApp: true, ProtectUnparsed: true}
	for _, decision := range Evaluate([]provider.Artifact{shared, referrer, unparsed}, rule, now) {
		if decision.Delete {
			t.Fatalf("artifact %s should be protected: %v", decision.Artifact.ID, decision.Reasons)
		}
	}
}

func TestMatchRepository(t *testing.T) {
	t.Parallel()
	if !MatchRepository("example-org/api", []string{"example-org/*"}, nil) {
		t.Fatal("expected include match")
	}
	if MatchRepository("example-org/archive-api", []string{"example-org/*"}, []string{"example-org/archive-*"}) {
		t.Fatal("expected exclude match")
	}
}

func TestParseRegex(t *testing.T) {
	t.Parallel()
	expression := `^release-(?P<year>[0-9]{4})(?P<month>[0-9]{2})(?P<day>[0-9]{2})-(?P<sequence>[0-9]+)-(?P<app>[a-z0-9-]+)$`
	version, err := ParseRegex("release-20260720-12-api", expression)
	if err != nil {
		t.Fatal(err)
	}
	if version.Sequence != 12 || version.App != "api" || version.Tag != "release-20260720-12-api" {
		t.Fatalf("unexpected version: %+v", version)
	}
	if err := ValidateRegexParser(`(?P<year>[0-9]{4})`); err == nil {
		t.Fatal("expected unanchored/incomplete parser error")
	}
}

func FuzzParseCalendar(f *testing.F) {
	f.Add("v2026.07.20.1-api")
	f.Add("latest")
	f.Fuzz(func(t *testing.T, tag string) {
		_, _ = ParseCalendar(tag)
	})
}

func artifact(id, tag string) provider.Artifact {
	version := mustVersion(tag)
	return provider.Artifact{ID: id, Digest: "sha256:" + id, Tags: []string{tag}, ParsedVersions: []provider.Version{version}}
}

func mustVersion(tag string) provider.Version {
	version, err := ParseCalendar(tag)
	if err != nil {
		panic(err)
	}
	return version
}

func assertDelete(t *testing.T, decisions []Decision, id string, want bool) {
	t.Helper()
	for _, decision := range decisions {
		if decision.Artifact.ID == id {
			if decision.Delete != want {
				t.Fatalf("%s delete = %v, want %v; reasons=%v", id, decision.Delete, want, decision.Reasons)
			}
			return
		}
	}
	t.Fatalf("missing decision for %s", id)
}
