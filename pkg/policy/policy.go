// Package policy contains the deterministic, provider-neutral retention
// policy evaluator.
package policy

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/restayway/regbot/pkg/provider"
)

// Reason is a stable machine-readable explanation for a decision.
type Reason string

const (
	ReasonRecent       Reason = "protected.recent"
	ReasonLatest       Reason = "protected.latest"
	ReasonMinimum      Reason = "protected.minimum"
	ReasonTag          Reason = "protected.tag"
	ReasonDigest       Reason = "protected.digest"
	ReasonSharedDigest Reason = "protected.shared_digest"
	ReasonUnparsed     Reason = "protected.unparsed"
	ReasonReferrer     Reason = "protected.referrer"
	ReasonTooYoung     Reason = "protected.too_young"
	ReasonOld          Reason = "eligible.older_than"
	ReasonNotSelected  Reason = "ignored.not_selected"
)

// Rule is a normalized retention rule.
type Rule struct {
	Name              string
	KeepNewerThan     time.Duration
	DeleteOlderThan   time.Duration
	KeepLatest        int
	KeepAtLeast       int
	GroupByApp        bool
	ProtectedTags     []string
	ProtectedPatterns []*regexp.Regexp
	ProtectedDigests  map[string]struct{}
	ProtectUnparsed   bool
	RequireTagged     bool
	MaxDeleteCount    int
	MaxDeletePercent  float64
}

// Decision describes whether an artifact is protected or eligible.
type Decision struct {
	Artifact provider.Artifact
	Delete   bool
	Reasons  []Reason
	Group    string
}

// ParseCalendar parses vYYYY.MM.DD.x and vYYYY.MM.DD.x-app tags.
func ParseCalendar(tag string) (provider.Version, error) {
	const prefix = "v"
	if !strings.HasPrefix(tag, prefix) {
		return provider.Version{}, fmt.Errorf("tag %q does not start with v", tag)
	}
	main, app, hasApp := strings.Cut(tag[1:], "-")
	parts := strings.Split(main, ".")
	if len(parts) != 4 {
		return provider.Version{}, fmt.Errorf("tag %q does not match vYYYY.MM.DD.x", tag)
	}
	year, err := strconv.Atoi(parts[0])
	if err != nil || len(parts[0]) != 4 {
		return provider.Version{}, fmt.Errorf("invalid year in tag %q", tag)
	}
	month, err := strconv.Atoi(parts[1])
	if err != nil || len(parts[1]) != 2 {
		return provider.Version{}, fmt.Errorf("invalid month in tag %q", tag)
	}
	day, err := strconv.Atoi(parts[2])
	if err != nil || len(parts[2]) != 2 {
		return provider.Version{}, fmt.Errorf("invalid day in tag %q", tag)
	}
	sequence, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil {
		return provider.Version{}, fmt.Errorf("invalid sequence in tag %q", tag)
	}
	date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if date.Year() != year || int(date.Month()) != month || date.Day() != day {
		return provider.Version{}, fmt.Errorf("invalid calendar date in tag %q", tag)
	}
	if hasApp {
		if app == "" {
			return provider.Version{}, fmt.Errorf("empty app in tag %q", tag)
		}
		for _, r := range app {
			if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-') {
				return provider.Version{}, fmt.Errorf("invalid app %q in tag %q", app, tag)
			}
		}
	}
	return provider.Version{Tag: tag, Date: date, Sequence: sequence, App: app}, nil
}

// ValidateRegexParser validates an anchored custom tag parser. The expression
// must define year, month, day, and sequence named captures; app is optional.
func ValidateRegexParser(expression string) error {
	if expression == "" {
		return fmt.Errorf("custom regex is required")
	}
	if !strings.HasPrefix(expression, "^") || !strings.HasSuffix(expression, "$") {
		return fmt.Errorf("custom regex must be anchored with ^ and $")
	}
	compiled, err := regexp.Compile(expression)
	if err != nil {
		return fmt.Errorf("invalid custom regex: %w", err)
	}
	names := map[string]bool{}
	for _, name := range compiled.SubexpNames() {
		names[name] = true
	}
	for _, required := range []string{"year", "month", "day", "sequence"} {
		if !names[required] {
			return fmt.Errorf("custom regex is missing named capture %q", required)
		}
	}
	return nil
}

// ParseRegex parses a tag using an anchored expression with named calendar
// captures. It applies the same date, sequence, and app validation as the
// built-in calendar parser.
func ParseRegex(tag, expression string) (provider.Version, error) {
	if err := ValidateRegexParser(expression); err != nil {
		return provider.Version{}, err
	}
	compiled := regexp.MustCompile(expression)
	match := compiled.FindStringSubmatch(tag)
	if match == nil {
		return provider.Version{}, fmt.Errorf("tag %q does not match custom regex", tag)
	}
	values := map[string]string{}
	for index, name := range compiled.SubexpNames() {
		if index > 0 && name != "" {
			values[name] = match[index]
		}
	}
	normalized := "v" + values["year"] + "." + values["month"] + "." + values["day"] + "." + values["sequence"]
	if app := values["app"]; app != "" {
		normalized += "-" + app
	}
	version, err := ParseCalendar(normalized)
	if err != nil {
		return provider.Version{}, fmt.Errorf("tag %q: %w", tag, err)
	}
	version.Tag = tag
	return version, nil
}

// Evaluate applies a rule at a fixed evaluation time.
func Evaluate(artifacts []provider.Artifact, rule Rule, now time.Time) []Decision {
	decisions := make([]Decision, len(artifacts))
	groups := map[string][]int{}
	for i, artifact := range artifacts {
		decision := Decision{Artifact: artifact, Group: "_default"}
		if len(artifact.ParsedVersions) > 0 && rule.GroupByApp {
			decision.Group = artifact.ParsedVersions[0].App
			if decision.Group == "" {
				decision.Group = "_default"
			}
		}
		decisions[i] = decision
		groups[decision.Group] = append(groups[decision.Group], i)
	}

	for _, indices := range groups {
		sort.SliceStable(indices, func(i, j int) bool {
			return newer(decisions[indices[i]].Artifact, decisions[indices[j]].Artifact)
		})
		for rank, index := range indices {
			d := &decisions[index]
			a := d.Artifact
			protected := false
			if len(a.Tags) == 0 && rule.RequireTagged {
				d.Reasons = append(d.Reasons, ReasonUnparsed)
				protected = true
			}
			if len(a.ParsedVersions) == 0 && rule.ProtectUnparsed {
				d.Reasons = append(d.Reasons, ReasonUnparsed)
				protected = true
			}
			if protectedTag(a.Tags, rule) {
				d.Reasons = append(d.Reasons, ReasonTag)
				protected = true
			}
			if _, ok := rule.ProtectedDigests[a.Digest]; ok && a.Digest != "" {
				d.Reasons = append(d.Reasons, ReasonDigest)
				protected = true
			}
			if len(a.ReferrerIDs) > 0 {
				d.Reasons = append(d.Reasons, ReasonReferrer)
				protected = true
			}
			if hasMultipleApps(a.ParsedVersions) {
				d.Reasons = append(d.Reasons, ReasonSharedDigest)
				protected = true
			}
			age := artifactAge(a, now)
			if rule.KeepNewerThan > 0 && age < rule.KeepNewerThan {
				d.Reasons = append(d.Reasons, ReasonRecent)
				protected = true
			}
			if rank < rule.KeepLatest {
				d.Reasons = append(d.Reasons, ReasonLatest)
				protected = true
			}
			if rank < rule.KeepAtLeast {
				d.Reasons = append(d.Reasons, ReasonMinimum)
				protected = true
			}
			if rule.DeleteOlderThan > 0 && age < rule.DeleteOlderThan {
				d.Reasons = append(d.Reasons, ReasonTooYoung)
				protected = true
			}
			if !protected && rule.DeleteOlderThan > 0 && age >= rule.DeleteOlderThan {
				d.Delete = true
				d.Reasons = append(d.Reasons, ReasonOld)
			} else if !protected {
				d.Reasons = append(d.Reasons, ReasonNotSelected)
			}
		}
	}
	return decisions
}

func hasMultipleApps(versions []provider.Version) bool {
	apps := map[string]struct{}{}
	for _, version := range versions {
		app := version.App
		if app == "" {
			app = "_default"
		}
		apps[app] = struct{}{}
	}
	return len(apps) > 1
}

func protectedTag(tags []string, rule Rule) bool {
	for _, tag := range tags {
		for _, wanted := range rule.ProtectedTags {
			if tag == wanted {
				return true
			}
		}
		for _, pattern := range rule.ProtectedPatterns {
			if pattern.MatchString(tag) {
				return true
			}
		}
	}
	return false
}

func artifactAge(a provider.Artifact, now time.Time) time.Duration {
	if len(a.ParsedVersions) > 0 {
		return now.Sub(a.ParsedVersions[0].Date)
	}
	if !a.UpdatedAt.IsZero() {
		return now.Sub(a.UpdatedAt)
	}
	return now.Sub(a.CreatedAt)
}

func newer(a, b provider.Artifact) bool {
	if len(a.ParsedVersions) > 0 && len(b.ParsedVersions) > 0 {
		av, bv := a.ParsedVersions[0], b.ParsedVersions[0]
		if !av.Date.Equal(bv.Date) {
			return av.Date.After(bv.Date)
		}
		if av.Sequence != bv.Sequence {
			return av.Sequence > bv.Sequence
		}
		if av.Tag != bv.Tag {
			return av.Tag > bv.Tag
		}
	}
	return a.ID > b.ID
}

// MatchRepository reports whether a repository matches include/exclude globs.
func MatchRepository(repository string, includes, excludes []string) bool {
	included := len(includes) == 0
	for _, pattern := range includes {
		if ok, _ := path.Match(pattern, repository); ok {
			included = true
		}
	}
	for _, pattern := range excludes {
		if ok, _ := path.Match(pattern, repository); ok {
			return false
		}
	}
	return included
}
