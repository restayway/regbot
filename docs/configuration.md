# Configuration

Regbot reads strict, versioned YAML. Unknown keys, unsupported providers, unsafe
limits, invalid durations, and missing target references are errors.

## Top-level fields

| Field | Required | Description |
| --- | --- | --- |
| `version` | yes | Must be `v1`. |
| `apply` | no | Defaults to `false`. Controls mutation in `run` and HTTP mode. |
| `registries` | yes | Named OCI or GitHub connections. |
| `policies` | yes | Named retention policies. |
| `hooks.after_apply` | no | Optional post-apply webhook. |

## OCI registry

```yaml
registries:
  internal:
    provider: oci
    endpoint: https://registry.example.com
    repositories: ["team/api", "team/web"] # optional catalog fallback
    credentials:
      username_env: REGISTRY_USERNAME
      password_env: REGISTRY_PASSWORD
    tls:
      ca_file: /etc/regbot/registry-ca.pem
      insecure_skip_verify: false
    timeout: 30s
```

If the registry disables `/v2/_catalog`, list repositories explicitly.
`insecure_skip_verify` exists for local development and should not be used in
production.

Credentials support `username_env`, `username_file`, `password_env`, and
`password_file`. File values are trimmed. Secrets are never interpolated into
arbitrary configuration strings.

## GitHub registry

```yaml
registries:
  github:
    provider: github
    endpoint: https://api.github.com # optional
    owner: example-org
    owner_type: organization
    token_env: GITHUB_TOKEN
    timeout: 30s
```

`owner_type` is either `organization` or `user`. Tokens can be supplied using
`token_env` or `token_file`.

## Policy fields

| Field | Default | Validation |
| --- | --- | --- |
| `tags.parser` | none | `calendar` or `custom_regex`. |
| `retention.keep_newer_than` | disabled | Go duration; cannot exceed `delete_older_than`. |
| `retention.delete_older_than` | none | Required positive Go duration. |
| `retention.keep_latest` | `0` | Non-negative. |
| `retention.keep_at_least` | none | Required and at least `1`. |
| `retention.group_by` | `_default` | Empty or `app`. |
| `protect.tags` | empty | Exact tags that always protect an artifact. |
| `protect.tag_patterns` | empty | Go regular expressions. |
| `protect.digests` | empty | Exact immutable digests. |
| `protect.unparsed_tags` | `true` | Protect artifacts without a recognized version. |
| `safety.max_delete_count` | none | Required positive integer. |
| `safety.max_delete_percent` | none | Required value in `(0, 100]`. |
| `safety.require_tagged_artifact` | `true` | Protect untagged artifacts. |

Durations use Go syntax such as `168h` and `720h`; `7d` is not valid.
Repository include/exclude values use path-style glob syntax.

The checked-in [JSON Schema](../schema/regbot.v1.schema.json) provides editor
validation.

Custom parsers use an anchored Go regular expression with required named
captures `year`, `month`, `day`, and `sequence`, plus optional `app`:

```yaml
tags:
  parser: custom_regex
  regex: "^release-(?P<year>[0-9]{4})(?P<month>[0-9]{2})(?P<day>[0-9]{2})-(?P<sequence>[0-9]+)-(?P<app>[a-z0-9-]+)$"
```

## Post-apply webhook

The webhook runs only after at least one deletion:

```yaml
hooks:
  after_apply:
    type: webhook
    url_env: REGBOT_GC_WEBHOOK_URL
    bearer_token_env: REGBOT_GC_WEBHOOK_TOKEN
    hmac_secret_env: REGBOT_GC_HMAC_SECRET
    timeout: 30s
```

When an HMAC secret is configured, the request includes
`X-Regbot-Signature-256: sha256=<hex digest>`. A webhook failure is logged and
does not roll back completed deletions.
