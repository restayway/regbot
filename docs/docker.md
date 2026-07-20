# Docker and Docker Swarm

Regbot publishes a distroless, non-root image at
`ghcr.io/restayway/regbot`. Pin a released version in every destructive or
scheduled deployment:

```sh
export REGBOT_IMAGE=ghcr.io/restayway/regbot:<version>
```

Do not use `latest` for automated deletion. The examples default to dry-run and
must be reviewed before changing `apply: false` to `apply: true`.
Release image tags retain the Git tag's `v` prefix, for example `v0.1.0`.

Regbot does not need the Docker socket, privileged mode, Cloudflare R2
credentials, or direct access to registry storage.

## Build locally

```sh
docker build --tag regbot:local .
docker run --rm regbot:local version
```

The image runs as UID/GID `65532` and contains no shell, curl, or package
manager. It intentionally has no image-wide `HEALTHCHECK`, because one-shot
`plan`, `apply`, and `run` containers do not expose HTTP. The Swarm `serve`
example adds an appropriate service-level health check.

## Configure Docker Run

Copy the provided files:

```sh
cp examples/docker/regbot.yaml ./regbot.yaml
cp examples/docker/regbot.env.example ./.env
chmod 600 .env
```

Edit `regbot.yaml` to select repositories and retention rules. Replace the
credential placeholders in `.env`. Environment-variable names must match the
`username_env`, `password_env`, or `token_env` fields in the YAML.

For GHCR instead of the custom OCI registry, start from:

```sh
cp examples/docker/ghcr-regbot.yaml ./regbot.yaml
```

Set `GITHUB_TOKEN` in `.env`, and change `owner`, `owner_type`, and repository
selectors for the target account.

### Validate connectivity

```sh
docker run --rm \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --env-file .env \
  --mount type=bind,src="$PWD/regbot.yaml",dst=/etc/regbot/regbot.yaml,readonly \
  "$REGBOT_IMAGE" \
  validate --config /etc/regbot/regbot.yaml
```

### Preview a plan

Table output:

```sh
docker run --rm \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --env-file .env \
  --mount type=bind,src="$PWD/regbot.yaml",dst=/etc/regbot/regbot.yaml,readonly \
  "$REGBOT_IMAGE" \
  plan --config /etc/regbot/regbot.yaml
```

JSON output:

```sh
docker run --rm \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --env-file .env \
  --mount type=bind,src="$PWD/regbot.yaml",dst=/etc/regbot/regbot.yaml,readonly \
  "$REGBOT_IMAGE" \
  plan --config /etc/regbot/regbot.yaml --output json
```

### Separate plan and apply

Create a host directory owned by the user running Docker. Override the
container UID/GID only for this workflow so the atomic plan writer can create a
file in the mounted directory:

```sh
mkdir -p output

docker run --rm \
  --user "$(id -u):$(id -g)" \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --env-file .env \
  --mount type=bind,src="$PWD/regbot.yaml",dst=/etc/regbot/regbot.yaml,readonly \
  --mount type=bind,src="$PWD/output",dst=/output \
  "$REGBOT_IMAGE" \
  plan --config /etc/regbot/regbot.yaml --out /output/plan.json
```

Review `output/plan.json`, then apply that exact plan within one hour:

```sh
docker run --rm \
  --user "$(id -u):$(id -g)" \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --env-file .env \
  --mount type=bind,src="$PWD/regbot.yaml",dst=/etc/regbot/regbot.yaml,readonly \
  --mount type=bind,src="$PWD/output",dst=/output \
  "$REGBOT_IMAGE" \
  apply --config /etc/regbot/regbot.yaml --plan /output/plan.json
```

The configuration fingerprint and provider preconditions must still match.

### One-shot scheduled run

`run` applies only when the mounted configuration contains `apply: true`.
Test the exact cron command with `apply: false` first:

```cron
17 3 * * * /usr/bin/docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges --env-file /etc/regbot/regbot.env --mount type=bind,src=/etc/regbot/regbot.yaml,dst=/etc/regbot/regbot.yaml,readonly ghcr.io/restayway/regbot:<version> run --config /etc/regbot/regbot.yaml --output json
```

Use absolute paths in cron and configure alerting for every non-zero exit code.
Prevent overlapping invocations with the host scheduler.

### Secret files and custom CAs

Instead of an environment file, mount credentials read-only and reference them
from YAML:

```yaml
credentials:
  username_file: /run/secrets/registry_username
  password_file: /run/secrets/registry_password
```

```sh
docker run --rm \
  --read-only \
  --mount type=bind,src="$PWD/secrets",dst=/run/secrets,readonly \
  --mount type=bind,src="$PWD/regbot.yaml",dst=/etc/regbot/regbot.yaml,readonly \
  "$REGBOT_IMAGE" validate --config /etc/regbot/regbot.yaml
```

For a private certificate authority, mount the certificate and configure
`tls.ca_file`:

```yaml
tls:
  ca_file: /etc/regbot/certs/registry-ca.pem
```

Never bake credentials or private certificates into the image.

## Docker Swarm

Swarm has no native recurring cron scheduler. The recommended Swarm deployment
runs one authenticated Regbot HTTP service and lets an external scheduler call
`POST /run`. The service rejects overlapping runs with HTTP `409 Conflict`.

The provided stack:

- Runs exactly one replica
- Keeps the root filesystem read-only
- Drops every Linux capability
- Stores configuration in a Docker Config
- Stores credentials and the run token in Docker Secrets
- Exposes health and Prometheus metrics only on an internal, attachable overlay
  network
- Starts with `apply: false`

### Create secrets

Run these commands on a Swarm manager:

```sh
printf '%s' 'registry-user' | docker secret create registry_username -
printf '%s' 'registry-password' | docker secret create registry_password -
openssl rand -hex 32 | docker secret create regbot_run_token -
```

Do not add a trailing newline when creating username or password secrets.
Regbot trims mounted secret-file values, but exact secret creation makes manual
inspection and rotation clearer.

### Deploy the stack

```sh
cd examples/docker
export REGBOT_IMAGE=ghcr.io/restayway/regbot:<version>
docker stack config --compose-file swarm-stack.yaml
docker stack deploy --compose-file swarm-stack.yaml regbot
```

Inspect it:

```sh
docker stack services regbot
docker service ps regbot_regbot
docker service logs --follow regbot_regbot
```

### Trigger a run from the overlay network

The stack intentionally publishes no host port. From a manager node, attach a
short-lived curl container to the overlay network:

```sh
export REGBOT_RUN_TOKEN='<the-run-token>'
export CURL_IMAGE=curlimages/curl:latest

docker run --rm \
  --network regbot_regbot \
  "$CURL_IMAGE" \
  --fail-with-body \
  --request POST \
  --header "Authorization: Bearer ${REGBOT_RUN_TOKEN}" \
  http://regbot:8080/run
```

An external scheduler can run the same command. Protect its token and ensure it
does not start another invocation before the previous command finishes.

Health and metrics are reachable from the same network:

```sh
docker run --rm --network regbot_regbot "$CURL_IMAGE" \
  --fail http://regbot:8080/healthz

docker run --rm --network regbot_regbot "$CURL_IMAGE" \
  --fail http://regbot:8080/metrics
```

If a published port is required, add this to the service and protect it with a
firewall or private ingress:

```yaml
ports:
  - target: 8080
    published: 8080
    protocol: tcp
    mode: ingress
```

Never publish an unauthenticated `/run` endpoint. Regbot itself rejects
non-loopback binding when no run token is configured.

### One-shot Swarm job

Recent Docker Swarm releases support `replicated-job`, but still do not
schedule it. After the stack creates its config, secrets, and network, a manager
can launch a manual job:

```sh
docker service create \
  --name "regbot-run-$(date +%s)" \
  --mode replicated-job \
  --replicas 1 \
  --restart-condition none \
  --network regbot_regbot \
  --config source=regbot_regbot_config,target=/etc/regbot/regbot.yaml \
  --secret source=registry_username,target=registry_username \
  --secret source=registry_password,target=registry_password \
  "$REGBOT_IMAGE" \
  run --config /etc/regbot/regbot.yaml --output json
```

Use `docker service logs <job-name>` to collect the result and remove the
completed service after retaining the audit output. Manager cron may create
these jobs, but must enforce a non-overlap lock.

### Rotate secrets

Swarm secrets are immutable. Create versioned replacements, update the stack
secret source, deploy, verify the service, and then remove old secrets:

```sh
printf '%s' 'new-password' | docker secret create registry_password_v2 -
```

Keep the service-facing name and `/run/secrets/registry_password` path stable by
changing the stack declaration to:

```yaml
secrets:
  registry_password:
    external: true
    name: registry_password_v2
```

Then redeploy and remove the old secret:

```sh
docker stack deploy --compose-file swarm-stack.yaml regbot
docker secret rm registry_password
```

Do not remove an old secret until no service references it.

### Enable apply and remove the stack

Review multiple dry-run responses first. Then change only `apply: false` to
`apply: true` in `swarm-regbot.yaml`, inspect the rendered stack, and redeploy.

Remove the service without touching registry content:

```sh
docker stack rm regbot
```

Removing the stack does not remove external Docker Secrets.
