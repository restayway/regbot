# Retention policy semantics

Regbot evaluates all policies at one fixed UTC timestamp.

## Evaluation order

For each target, Regbot:

1. Discovers every selected repository and fails if discovery is empty.
2. Merges tags pointing to the same immutable digest.
3. Parses recognized tags into date, sequence, and optional app.
4. Groups artifacts by app when `group_by: app` is configured.
5. Sorts by date, numeric sequence, original tag, and immutable ID.
6. Adds all applicable protection reasons.
7. Selects old, unprotected artifacts for deletion.
8. Enforces count and percentage safety limits.

Protection always wins. If multiple policies target one artifact, a protection
from any policy prevents deletion.

## Calendar tags

The built-in parser accepts:

```text
vYYYY.MM.DD.x
vYYYY.MM.DD.x-{app}
```

Dates must be real Gregorian dates. `x` is a non-negative integer. App names
may contain lowercase ASCII letters, digits, `_`, `.`, and `-`.

An app-less version belongs to `_default`. A digest carrying tags for multiple
apps is protected because deleting it would affect more than one group.

Custom anchored Go regular expressions may map another tag shape to the same
typed fields through named captures. They receive the same calendar and app
validation as the built-in parser.

## Rule interaction

- `keep_newer_than` protects artifacts newer than its duration.
- `delete_older_than` makes artifacts at least that old eligible.
- `keep_latest` protects the first N artifacts after deterministic sorting.
- `keep_at_least` protects at least N artifacts in every group.
- Exact tag, tag-pattern, digest, referrer, and unparsed-tag protection are
  cumulative.

Example: with `keep_latest: 5`, `keep_at_least: 1`, and
`delete_older_than: 720h`, an app with three year-old versions still keeps its
latest version. An app with only one version always keeps it.

## Stable reason codes

Plans use reason codes including:

- `protected.recent`
- `protected.latest`
- `protected.minimum`
- `protected.tag`
- `protected.digest`
- `protected.shared_digest`
- `protected.unparsed`
- `protected.referrer`
- `protected.too_young`
- `eligible.older_than`
- `ignored.not_selected`

These strings are part of the v1 machine-readable output contract.
