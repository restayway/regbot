# GitHub provider

The GitHub provider supports GHCR container packages owned by an organization
or user. It enumerates packages and versions through GitHub's REST API and
deletes a package version by its immutable numeric ID.

The authenticated principal needs package read and administration/deletion
permission. Token capabilities differ between classic personal access tokens,
GitHub Apps, and workflow `GITHUB_TOKEN` access. Confirm the current GitHub
documentation and connect the package to the workflow repository when using
Actions.

Regbot handles REST pagination, `Retry-After`, transient server errors, and
secondary-rate-limit responses with bounded exponential backoff and jitter.
Permission failures are errors and are never silently skipped.

Only `package_type=container` is supported in v1.
