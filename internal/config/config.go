// Package config loads and validates Regbot's strict YAML configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/restayway/regbot/pkg/policy"
	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

var scheduleParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

type Config struct {
	Version    string              `yaml:"version"`
	Apply      bool                `yaml:"apply"`
	Registries map[string]Registry `yaml:"registries"`
	Policies   map[string]Policy   `yaml:"policies"`
	Schedule   *Schedule           `yaml:"schedule,omitempty"`
	Hooks      Hooks               `yaml:"hooks,omitempty"`
}

type Registry struct {
	Provider     string      `yaml:"provider"`
	Endpoint     string      `yaml:"endpoint,omitempty"`
	Owner        string      `yaml:"owner,omitempty"`
	OwnerType    string      `yaml:"owner_type,omitempty"`
	TokenEnv     string      `yaml:"token_env,omitempty"`
	TokenFile    string      `yaml:"token_file,omitempty"`
	Repositories []string    `yaml:"repositories,omitempty"`
	Credentials  Credentials `yaml:"credentials,omitempty"`
	TLS          TLS         `yaml:"tls,omitempty"`
	Timeout      Duration    `yaml:"timeout,omitempty"`
}

type Credentials struct {
	UsernameEnv  string `yaml:"username_env,omitempty"`
	UsernameFile string `yaml:"username_file,omitempty"`
	PasswordEnv  string `yaml:"password_env,omitempty"`
	PasswordFile string `yaml:"password_file,omitempty"`
}

type TLS struct {
	CAFile             string `yaml:"ca_file,omitempty"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify,omitempty"`
}

type Policy struct {
	Targets   []Target  `yaml:"targets"`
	Tags      Tags      `yaml:"tags"`
	Retention Retention `yaml:"retention"`
	Protect   Protect   `yaml:"protect,omitempty"`
	Safety    Safety    `yaml:"safety"`
}

type Target struct {
	Registry     string     `yaml:"registry"`
	Repositories Repository `yaml:"repositories"`
}

type Repository struct {
	Include []string `yaml:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty"`
}

type Tags struct {
	Parser   string   `yaml:"parser"`
	Patterns []string `yaml:"patterns,omitempty"`
	Regex    string   `yaml:"regex,omitempty"`
}

type Retention struct {
	KeepNewerThan   Duration `yaml:"keep_newer_than,omitempty"`
	DeleteOlderThan Duration `yaml:"delete_older_than"`
	KeepLatest      int      `yaml:"keep_latest,omitempty"`
	KeepAtLeast     int      `yaml:"keep_at_least"`
	GroupBy         string   `yaml:"group_by,omitempty"`
}

type Protect struct {
	Tags         []string `yaml:"tags,omitempty"`
	TagPatterns  []string `yaml:"tag_patterns,omitempty"`
	Digests      []string `yaml:"digests,omitempty"`
	UnparsedTags *bool    `yaml:"unparsed_tags,omitempty"`
}

type Safety struct {
	MaxDeleteCount   int     `yaml:"max_delete_count"`
	MaxDeletePercent float64 `yaml:"max_delete_percent"`
	RequireTagged    *bool   `yaml:"require_tagged_artifact,omitempty"`
}

type Schedule struct {
	Cron       string   `yaml:"cron"`
	Timezone   string   `yaml:"timezone"`
	RunOnStart bool     `yaml:"run_on_start,omitempty"`
	Timeout    Duration `yaml:"timeout,omitempty"`
}

type Hooks struct {
	AfterApply *Webhook `yaml:"after_apply,omitempty"`
}

type Webhook struct {
	Type           string   `yaml:"type"`
	URLEnv         string   `yaml:"url_env"`
	BearerTokenEnv string   `yaml:"bearer_token_env,omitempty"`
	HMACSecretEnv  string   `yaml:"hmac_secret_env,omitempty"`
	Timeout        Duration `yaml:"timeout,omitempty"`
}

type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	value, err := time.ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", node.Value, err)
	}
	d.Duration = value
	return nil
}

func Load(path string) (*Config, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return nil, nil, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	return &cfg, data, nil
}

func (c *Config) Validate() error {
	var errs []error
	if c.Version != "v1" {
		errs = append(errs, fmt.Errorf("version must be v1"))
	}
	if len(c.Registries) == 0 {
		errs = append(errs, fmt.Errorf("at least one registry is required"))
	}
	for name, registry := range c.Registries {
		switch registry.Provider {
		case "oci":
			if registry.Endpoint == "" {
				errs = append(errs, fmt.Errorf("registry %q: endpoint is required", name))
			}
		case "github":
			if registry.Owner == "" || registry.TokenEnv == "" && registry.TokenFile == "" {
				errs = append(errs, fmt.Errorf("registry %q: owner and token_env or token_file are required", name))
			}
			if registry.OwnerType != "organization" && registry.OwnerType != "user" {
				errs = append(errs, fmt.Errorf("registry %q: owner_type must be organization or user", name))
			}
		default:
			errs = append(errs, fmt.Errorf("registry %q: unsupported provider %q", name, registry.Provider))
		}
	}
	for name, configured := range c.Policies {
		if len(configured.Targets) == 0 {
			errs = append(errs, fmt.Errorf("policy %q: at least one target is required", name))
		}
		switch configured.Tags.Parser {
		case "calendar":
			if configured.Tags.Regex != "" {
				errs = append(errs, fmt.Errorf("policy %q: regex is only valid with custom_regex parser", name))
			}
			for _, pattern := range configured.Tags.Patterns {
				if pattern != "vYYYY.MM.DD.x" && pattern != "vYYYY.MM.DD.x-{app}" {
					errs = append(errs, fmt.Errorf("policy %q: unsupported calendar pattern %q", name, pattern))
				}
			}
		case "custom_regex":
			if err := policy.ValidateRegexParser(configured.Tags.Regex); err != nil {
				errs = append(errs, fmt.Errorf("policy %q: %w", name, err))
			}
		default:
			errs = append(errs, fmt.Errorf("policy %q: parser must be calendar or custom_regex", name))
		}
		if configured.Retention.DeleteOlderThan.Duration <= 0 {
			errs = append(errs, fmt.Errorf("policy %q: delete_older_than must be positive", name))
		}
		if configured.Retention.KeepNewerThan.Duration > configured.Retention.DeleteOlderThan.Duration {
			errs = append(errs, fmt.Errorf("policy %q: keep_newer_than cannot exceed delete_older_than", name))
		}
		if configured.Retention.KeepAtLeast < 1 {
			errs = append(errs, fmt.Errorf("policy %q: keep_at_least must be at least 1", name))
		}
		if configured.Retention.KeepLatest < 0 {
			errs = append(errs, fmt.Errorf("policy %q: keep_latest cannot be negative", name))
		}
		if configured.Retention.GroupBy != "" && configured.Retention.GroupBy != "app" {
			errs = append(errs, fmt.Errorf("policy %q: group_by must be app", name))
		}
		if configured.Safety.MaxDeleteCount <= 0 {
			errs = append(errs, fmt.Errorf("policy %q: max_delete_count must be positive", name))
		}
		if configured.Safety.MaxDeletePercent <= 0 || configured.Safety.MaxDeletePercent > 100 {
			errs = append(errs, fmt.Errorf("policy %q: max_delete_percent must be in (0,100]", name))
		}
		for _, target := range configured.Targets {
			if _, ok := c.Registries[target.Registry]; !ok {
				errs = append(errs, fmt.Errorf("policy %q: unknown registry %q", name, target.Registry))
			}
		}
		for _, expression := range configured.Protect.TagPatterns {
			if _, err := regexp.Compile(expression); err != nil {
				errs = append(errs, fmt.Errorf("policy %q: invalid protected tag pattern: %w", name, err))
			}
		}
	}
	if c.Hooks.AfterApply != nil && c.Hooks.AfterApply.Type != "webhook" {
		errs = append(errs, fmt.Errorf("hooks.after_apply.type must be webhook"))
	}
	if c.Schedule != nil {
		if c.Schedule.Cron == "" {
			errs = append(errs, errors.New("schedule.cron is required"))
		} else if _, err := scheduleParser.Parse(c.Schedule.Cron); err != nil {
			errs = append(errs, fmt.Errorf("schedule.cron: %w", err))
		}
		if c.Schedule.Timezone == "" {
			errs = append(errs, errors.New("schedule.timezone is required"))
		} else if _, err := time.LoadLocation(c.Schedule.Timezone); err != nil {
			errs = append(errs, fmt.Errorf("schedule.timezone: %w", err))
		}
		if c.Schedule.Timeout.Duration < 0 {
			errs = append(errs, errors.New("schedule.timeout must be positive"))
		}
	}
	return errors.Join(errs...)
}

func (p Policy) Rule(name string) (policy.Rule, error) {
	patterns := make([]*regexp.Regexp, 0, len(p.Protect.TagPatterns))
	for _, expression := range p.Protect.TagPatterns {
		compiled, err := regexp.Compile(expression)
		if err != nil {
			return policy.Rule{}, err
		}
		patterns = append(patterns, compiled)
	}
	digests := make(map[string]struct{}, len(p.Protect.Digests))
	for _, digest := range p.Protect.Digests {
		digests[digest] = struct{}{}
	}
	unparsed := true
	if p.Protect.UnparsedTags != nil {
		unparsed = *p.Protect.UnparsedTags
	}
	tagged := true
	if p.Safety.RequireTagged != nil {
		tagged = *p.Safety.RequireTagged
	}
	return policy.Rule{
		Name: name, KeepNewerThan: p.Retention.KeepNewerThan.Duration,
		DeleteOlderThan: p.Retention.DeleteOlderThan.Duration,
		KeepLatest:      p.Retention.KeepLatest, KeepAtLeast: p.Retention.KeepAtLeast,
		GroupByApp: p.Retention.GroupBy == "app", ProtectedTags: p.Protect.Tags,
		ProtectedPatterns: patterns, ProtectedDigests: digests,
		ProtectUnparsed: unparsed, RequireTagged: tagged,
		MaxDeleteCount:   p.Safety.MaxDeleteCount,
		MaxDeletePercent: p.Safety.MaxDeletePercent,
	}, nil
}

func Secret(envName, file string) (string, error) {
	if envName != "" {
		value, ok := os.LookupEnv(envName)
		if !ok || value == "" {
			return "", fmt.Errorf("environment variable %s is not set", envName)
		}
		return value, nil
	}
	if file != "" {
		value, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return string(bytes.TrimSpace(value)), nil
	}
	return "", nil
}
