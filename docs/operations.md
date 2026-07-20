# Operations

## Recommended execution model

Run Regbot as a one-shot process. The platform owns scheduling and prevents
overlap.

For container deployments, see the complete
[Docker Run and Docker Swarm guide](docker.md).

```cron
17 3 * * * /usr/local/bin/regbot run --config /etc/regbot/regbot.yaml
```

For Kubernetes, use a `CronJob` with `concurrencyPolicy: Forbid`, a read-only
root filesystem, a non-root user, and secrets mounted as files or environment
variables.

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: regbot
spec:
  schedule: "17 3 * * *"
  concurrencyPolicy: Forbid
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: Never
          containers:
            - name: regbot
              image: ghcr.io/restayway/regbot:latest
              args: ["run", "--config", "/etc/regbot/regbot.yaml", "--output", "json"]
              securityContext:
                runAsNonRoot: true
                readOnlyRootFilesystem: true
```

Start with `apply: false`, review several scheduled plans, and then enable
apply. Keep deletion limits substantially below the normal repository size.

## Plan/apply separation

For stronger review and audit:

```sh
regbot plan -c regbot.yaml --out plan.json
regbot apply -c regbot.yaml --plan plan.json
```

Plans expire after one hour and are rejected if configuration changes.

## Distribution garbage collection

The post-apply webhook should enqueue—not directly perform—a controlled
maintenance workflow:

1. Prevent new pushes or place Distribution into read-only mode.
2. Run the registry version's garbage-collection command with its real storage
   configuration.
3. Record actual reclaimed storage externally.
4. Restore writes and verify pull/push health.

Webhook delivery does not prove garbage collection succeeded. Regbot reports
manifest deletion only.

## Recovery

OCI manifest deletion is generally not reversible through the registry API.
Keep tested storage backups when recovery is required. GitHub may offer
time-limited package restoration, but operators must not rely on that as a
substitute for safe policy limits.

Exit codes are documented in the README and are suitable for alert routing.
Alert on any non-zero code, safety abort, partial apply, or webhook failure.
