# Changelog

All notable changes will be documented in this file.

The format follows Keep a Changelog, and releases use Semantic Versioning.

## [Unreleased]

### Added

- Initial OCI and GitHub Container Registry retention implementation.
- Built-in cron scheduler with explicit timezones, run timeouts, overlap
  protection, health/readiness endpoints, and Prometheus metrics.
- `REGBOT_SCHEDULER_RUN_ON_START` deployment smoke-test override.
