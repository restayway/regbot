package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validConfig = `version: v1
apply: false
registries:
  local:
    provider: oci
    endpoint: https://registry.example.com
    repositories: [example-org/api]
policies:
  default:
    targets:
      - registry: local
        repositories:
          include: ["example-org/*"]
    tags:
      parser: calendar
    retention:
      keep_newer_than: 168h
      delete_older_than: 720h
      keep_latest: 5
      keep_at_least: 1
      group_by: app
    safety:
      max_delete_count: 10
      max_delete_percent: 25
`

func TestLoadStrictConfig(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "regbot.yaml")
	if err := os.WriteFile(path, []byte(validConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != "v1" || cfg.Policies["default"].Retention.KeepLatest != 5 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "regbot.yaml")
	data := strings.Replace(validConfig, "apply: false", "apply: false\nunknown: true", 1)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(path); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestValidateRejectsUnsafeLimits(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "regbot.yaml")
	data := strings.Replace(validConfig, "max_delete_percent: 25", "max_delete_percent: 101", 1)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(path); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestExampleConfigurationLoads(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		filepath.Join("..", "..", "examples", "regbot.yaml"),
		filepath.Join("..", "..", "examples", "docker", "regbot.yaml"),
		filepath.Join("..", "..", "examples", "docker", "ghcr-regbot.yaml"),
		filepath.Join("..", "..", "examples", "docker", "swarm-regbot.yaml"),
	} {
		if _, _, err := Load(path); err != nil {
			t.Errorf("Load(%q): %v", path, err)
		}
	}
}
