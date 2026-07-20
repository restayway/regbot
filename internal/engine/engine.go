// Package engine orchestrates discovery, planning, safety checks, and apply.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/restayway/regbot/internal/config"
	"github.com/restayway/regbot/pkg/plan"
	"github.com/restayway/regbot/pkg/policy"
	"github.com/restayway/regbot/pkg/provider"
)

var (
	ErrSafety       = errors.New("safety limit rejected the plan")
	ErrStalePlan    = errors.New("plan is stale")
	ErrPartialApply = errors.New("one or more deletions failed")
)

type Engine struct {
	Config      *config.Config
	ConfigBytes []byte
	Providers   map[string]provider.Provider
	Logger      *slog.Logger
	Now         func() time.Time
	Concurrency int
}

func (e *Engine) Validate(ctx context.Context) error {
	for name, target := range e.Providers {
		capabilities, err := target.Preflight(ctx)
		if err != nil {
			return fmt.Errorf("registry %s preflight: %w", name, err)
		}
		if !capabilities.Delete {
			return fmt.Errorf("registry %s does not support deletion", name)
		}
	}
	return nil
}

func (e *Engine) Plan(ctx context.Context) (plan.Plan, error) {
	now := e.now()
	type keyed struct {
		key    string
		action plan.Action
	}
	actions := map[string]keyed{}
	protected := map[string]struct{}{}
	discovered := map[string]struct{}{}
	limits := make([]safetyLimit, 0)

	policyNames := sortedKeys(e.Config.Policies)
	for _, policyName := range policyNames {
		configured := e.Config.Policies[policyName]
		rule, err := configured.Rule(policyName)
		if err != nil {
			return plan.Plan{}, err
		}
		for _, target := range configured.Targets {
			client := e.Providers[target.Registry]
			artifacts, err := client.List(ctx, provider.Target{
				Registry: target.Registry, Includes: target.Repositories.Include, Excludes: target.Repositories.Exclude,
			})
			if err != nil {
				return plan.Plan{}, fmt.Errorf("policy %s target %s discovery: %w", policyName, target.Registry, err)
			}
			if len(artifacts) == 0 {
				return plan.Plan{}, fmt.Errorf("policy %s target %s returned no artifacts; refusing incomplete discovery", policyName, target.Registry)
			}
			for i := range artifacts {
				if err := parseArtifact(&artifacts[i], configured.Tags); err != nil {
					return plan.Plan{}, fmt.Errorf("policy %s: %w", policyName, err)
				}
				discovered[artifactKey(artifacts[i])] = struct{}{}
			}
			decisions := policy.Evaluate(artifacts, rule, now)
			deleteCount := 0
			repositoryTotals := map[string]int{}
			repositoryDeletes := map[string]int{}
			for _, decision := range decisions {
				key := artifactKey(decision.Artifact)
				repositoryTotals[decision.Artifact.Repository]++
				if !decision.Delete {
					protected[key] = struct{}{}
					delete(actions, key)
					continue
				}
				deleteCount++
				repositoryDeletes[decision.Artifact.Repository]++
				reasons := make([]string, len(decision.Reasons))
				for i, reason := range decision.Reasons {
					reasons[i] = string(reason)
				}
				actions[key] = keyed{key: key, action: plan.Action{Policy: policyName, ReasonCodes: reasons, Artifact: decision.Artifact}}
			}
			limits = append(limits, safetyLimit{
				policy: policyName, total: len(decisions), deletes: deleteCount,
				maxCount: rule.MaxDeleteCount, maxPercent: rule.MaxDeletePercent,
				repositoryTotals: repositoryTotals, repositoryDeletes: repositoryDeletes,
			})
		}
	}
	for key := range protected {
		delete(actions, key)
	}
	for _, limit := range limits {
		if err := limit.validate(); err != nil {
			return plan.Plan{}, err
		}
	}
	result := plan.Plan{
		Version: plan.FormatVersion, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		ConfigFingerprint: plan.Fingerprint(e.ConfigBytes), DiscoveredCount: len(discovered),
		ProtectedCount: len(protected),
	}
	for _, item := range actions {
		result.Actions = append(result.Actions, item.action)
	}
	sort.Slice(result.Actions, func(i, j int) bool {
		a, b := result.Actions[i].Artifact, result.Actions[j].Artifact
		return artifactKey(a) < artifactKey(b)
	})
	return result, nil
}

func (e *Engine) Apply(ctx context.Context, proposal plan.Plan) (plan.Result, error) {
	started := e.now()
	result := plan.Result{Version: plan.FormatVersion, StartedAt: started, Planned: len(proposal.Actions)}
	if proposal.Version != plan.FormatVersion || proposal.ConfigFingerprint != plan.Fingerprint(e.ConfigBytes) || started.After(proposal.ExpiresAt) {
		return result, ErrStalePlan
	}
	concurrency := e.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan plan.Action)
	outcomes := make(chan plan.ActionOutcome)
	var workers sync.WaitGroup
	for range concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for action := range jobs {
				client := e.Providers[action.Artifact.Registry]
				if client == nil {
					outcomes <- plan.ActionOutcome{Action: action, Status: "failed", Error: "provider is not configured"}
					cancel()
					continue
				}
				err := client.Delete(ctx, provider.DeleteRequest{
					Repository: action.Artifact.Repository, ID: action.Artifact.ID,
					Digest: action.Artifact.Digest, Tags: action.Artifact.Tags, UpdatedAt: action.Artifact.UpdatedAt,
				})
				switch {
				case err == nil:
					outcomes <- plan.ActionOutcome{Action: action, Status: "deleted"}
				case errors.Is(err, provider.ErrNotFound):
					outcomes <- plan.ActionOutcome{Action: action, Status: "skipped"}
				default:
					outcomes <- plan.ActionOutcome{Action: action, Status: "failed", Error: err.Error()}
					if errors.Is(err, provider.ErrPrecondition) {
						cancel()
					}
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, action := range proposal.Actions {
			select {
			case jobs <- action:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(outcomes)
	}()
	for outcome := range outcomes {
		result.Outcomes = append(result.Outcomes, outcome)
		switch outcome.Status {
		case "deleted":
			result.Deleted++
		case "skipped":
			result.Skipped++
		case "failed":
			result.Failed++
		}
	}
	result.Skipped += result.Planned - result.Deleted - result.Skipped - result.Failed
	result.FinishedAt = e.now()
	sort.Slice(result.Outcomes, func(i, j int) bool {
		return artifactKey(result.Outcomes[i].Action.Artifact) < artifactKey(result.Outcomes[j].Action.Artifact)
	})
	if result.Failed > 0 {
		return result, ErrPartialApply
	}
	return result, nil
}

func parseArtifact(artifact *provider.Artifact, tags config.Tags) error {
	artifact.ParsedVersions = nil
	for _, tag := range artifact.Tags {
		var version provider.Version
		var err error
		switch tags.Parser {
		case "calendar":
			version, err = policy.ParseCalendar(tag)
		case "custom_regex":
			version, err = policy.ParseRegex(tag, tags.Regex)
		default:
			return fmt.Errorf("unsupported parser %q", tags.Parser)
		}
		if err == nil && patternAllows(version, tags.Patterns) {
			artifact.ParsedVersions = append(artifact.ParsedVersions, version)
		}
	}
	sort.Slice(artifact.ParsedVersions, func(i, j int) bool {
		a, b := artifact.ParsedVersions[i], artifact.ParsedVersions[j]
		if !a.Date.Equal(b.Date) {
			return a.Date.After(b.Date)
		}
		return a.Sequence > b.Sequence
	})
	return nil
}

func patternAllows(version provider.Version, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		if pattern == "vYYYY.MM.DD.x" && version.App == "" {
			return true
		}
		if pattern == "vYYYY.MM.DD.x-{app}" && version.App != "" {
			return true
		}
	}
	return false
}

type safetyLimit struct {
	policy            string
	total, deletes    int
	maxCount          int
	maxPercent        float64
	repositoryTotals  map[string]int
	repositoryDeletes map[string]int
}

func (s safetyLimit) validate() error {
	if s.deletes > s.maxCount {
		return fmt.Errorf("%w: policy %s planned %d deletions, maximum is %d", ErrSafety, s.policy, s.deletes, s.maxCount)
	}
	if percent(s.deletes, s.total) > s.maxPercent {
		return fmt.Errorf("%w: policy %s planned %.2f%% deletions, maximum is %.2f%%", ErrSafety, s.policy, percent(s.deletes, s.total), s.maxPercent)
	}
	for repository, deletes := range s.repositoryDeletes {
		if deletes > s.maxCount || percent(deletes, s.repositoryTotals[repository]) > s.maxPercent {
			return fmt.Errorf("%w: policy %s repository %s exceeds deletion limits", ErrSafety, s.policy, repository)
		}
	}
	return nil
}

func percent(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return math.Round(float64(part)*10000/float64(total)) / 100
}

func artifactKey(artifact provider.Artifact) string {
	return artifact.Registry + "\x00" + artifact.Repository + "\x00" + artifact.ID
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

func Marshal(value any) ([]byte, error) {
	return json.MarshalIndent(value, "", "  ")
}
