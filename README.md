# Regbot

Regbot safely applies explainable retention policies to OCI-compatible
registries and GitHub Container Registry (GHCR).

It is designed for calendar-versioned container images such as:

```text
v2026.07.20.1
v2026.07.20.1-api
v2026.07.20.1-web
```

Regbot understands the date, build sequence, and optional monorepo app name.
It can keep recent images, remove sufficiently old images, retain the latest
builds, and guarantee a minimum number of artifacts per app.

## Why Regbot?

Registry cleanup is deceptively dangerous. Tags may share one digest,
multi-platform images form graphs, and signatures or SBOMs may refer to an
image. Provider APIs also differ: OCI registries delete manifests by digest,
while GitHub deletes package versions by numeric ID.

Regbot uses a fail-closed workflow:

```text
discover → normalize → group → evaluate → enforce safety → plan → apply
```

- Dry-run is the default.
- Unknown tags and OCI referrers are protected by default.
- Every decision has a stable reason code.
- Deletion plans expire and include a configuration fingerprint.
- Apply rechecks provider preconditions.
- Count and percentage limits stop unexpectedly large deletions.
- Regbot never deletes Cloudflare R2 or S3 objects directly.

## Status

Regbot is an early v1 implementation. The configuration and plan formats are
versioned, but releases before `v1.0.0` may contain breaking changes.

Regbot releases use Semantic Versioning tags such as `v0.1.0`. Calendar
versions such as `v2026.07.20.1-api` describe container images managed by
Regbot; they are not Regbot release versions.

Supported:

- OCI Distribution-compatible registries, including CNCF Distribution 3.x
- GitHub Container Registry organization and user packages
- Calendar tags with optional app suffixes
- One-shot CLI, built-in scheduler, Kubernetes CronJob, GitHub Actions, and optional HTTP mode

Not supported in v1:

- npm, Maven, NuGet, RubyGems, or legacy GitHub Docker Packages
- Docker Hub-specific management APIs
- Direct R2/S3 lifecycle or object deletion
- Cascading deletion of signatures, SBOMs, or attestations
- A web UI or persistent database

## Install

Build from source:

```sh
go install github.com/restayway/regbot/cmd/regbot@latest
```

Or build the current checkout:

```sh
make build
./bin/regbot version
```

Container deployments are documented for both
[`docker run`](docs/docker.md#configure-docker-run) and
[Docker Swarm](docs/docker.md#docker-swarm), including mounted configuration,
secret files, scheduling, health checks, and metrics.

## Quick start

Copy the example:

```sh
cp examples/regbot.yaml regbot.yaml
export REGBOT_REGISTRY_USERNAME='...'
export REGBOT_REGISTRY_PASSWORD='...'
```

Validate configuration and connectivity:

```sh
regbot validate --config regbot.yaml
```

Create a human-readable plan and save its immutable JSON representation:

```sh
regbot plan --config regbot.yaml --out plan.json
```

Apply that exact plan:

```sh
regbot apply --config regbot.yaml --plan plan.json
```

For scheduled execution, `regbot run` plans and applies only when the
configuration explicitly contains `apply: true`. Otherwise it remains a dry
run.

For a self-scheduling container, add a top-level `schedule` block and run:

```sh
regbot scheduler --config regbot.yaml --listen 0.0.0.0:8080
```

The scheduler exposes health, readiness, and low-cardinality metrics but no
remote run endpoint. Use exactly one scheduler replica.

## Example policy

```yaml
version: v1
apply: false

registries:
  internal:
    provider: oci
    endpoint: https://registry.example.com
    credentials:
      username_env: REGBOT_REGISTRY_USERNAME
      password_env: REGBOT_REGISTRY_PASSWORD

policies:
  production:
    targets:
      - registry: internal
        repositories:
          include: ["example-org/*"]
          exclude: ["example-org/archive-*"]
    tags:
      parser: calendar
    retention:
      keep_newer_than: 168h
      delete_older_than: 720h
      keep_latest: 5
      keep_at_least: 1
      group_by: app
    protect:
      tags: ["latest", "stable", "prod"]
      unparsed_tags: true
    safety:
      max_delete_count: 100
      max_delete_percent: 25
      require_tagged_artifact: true
```

`keep_newer_than` and `delete_older_than` intentionally have different names:
one protects and the other creates deletion eligibility. `keep_latest` and
`keep_at_least` always add protection.

See [configuration](docs/configuration.md) and
[policy semantics](docs/policies.md) for the complete contract.

## Cloudflare R2 and Distribution garbage collection

When CNCF Distribution uses R2 through its S3-compatible storage driver,
deleting a manifest removes the registry reference. Shared layers remain, and
unreferenced blobs occupy storage until Distribution garbage collection runs.

Regbot deliberately does not inspect or delete registry bucket objects.
After successful OCI deletion it can send a signed webhook to external
automation. That automation should place the registry in its documented
maintenance/read-only state, run garbage collection, and restore normal
operation. See [operations](docs/operations.md).

## HTTP and metrics

One-shot execution is recommended. Optional HTTP mode is available for
platforms that need a long-running target:

```sh
export REGBOT_RUN_TOKEN='...'
regbot serve --config regbot.yaml \
  --listen 127.0.0.1:8080 \
  --run-token-env REGBOT_RUN_TOKEN
```

It exposes:

- `GET /healthz`
- `GET /readyz`
- `GET /metrics`
- `POST /run`

Only low-cardinality Prometheus labels are used. Repository names, tags,
digests, and credentials are never metric labels.

## Security

Use least-privilege credentials and review a plan before enabling automated
apply. For GHCR, the authenticated principal needs read access and package
administration/deletion permission. For OCI registries, manifest deletion must
be enabled.

See [security guidance](docs/security.md) and report vulnerabilities according
to [SECURITY.md](SECURITY.md).

## Development

```sh
make test
make race
make vet
make build
```

The public Go API is documented on `pkg.go.dev` through the packages under
`pkg/`. Provider implementations and runtime wiring intentionally remain
internal so their details can evolve without creating accidental compatibility
promises.

Builds use Go 1.26.5 or newer within the 1.26 release line. This minimum patch
level includes standard-library security fixes required by Regbot's network
surface.

## License

Apache License 2.0. See [LICENSE](LICENSE).
