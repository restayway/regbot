# Architecture

Regbot has a small provider-neutral core:

```text
strict YAML
    │
provider discovery ── OCI / GitHub
    │
normalized artifacts (digest, tags, timestamps, referrers)
    │
calendar parser and app grouping
    │
retention decisions and reason codes
    │
safety limits
    │
immutable JSON plan
    │
precondition check → provider delete → result → optional webhook
```

Public contracts live under `pkg/provider`, `pkg/policy`, and `pkg/plan`.
Runtime providers, configuration, orchestration, and CLI wiring remain under
`internal`.

A provider must enumerate the complete selected target, return stable immutable
IDs, expose a preflight capability check, and implement idempotent deletion
with explicit not-found and precondition errors. It must never silently convert
incomplete discovery into an empty successful result.

The policy engine has no network access and receives one evaluation timestamp.
This makes decisions deterministic and straightforward to fuzz and unit test.
