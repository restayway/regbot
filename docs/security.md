# Security model

Regbot is a destructive automation tool. Its primary threats are compromised
credentials, malicious or mistaken configuration, incomplete provider
discovery, tag races, credential leakage, and an exposed HTTP run endpoint.

Controls include:

- Dry-run by default
- Explicit immutable plans and one-hour expiration
- Configuration fingerprints and deletion preconditions
- Per-policy and per-repository count/percentage limits
- Mandatory minimum artifact retention
- Protection of unknown tags, shared digests, and OCI referrers
- Strict YAML decoding
- Environment/file secret references
- Bounded concurrency and provider retries
- Constant-time HTTP bearer-token comparison
- Low-cardinality metrics without repository or artifact identifiers

Use credentials dedicated to Regbot. Restrict them to the selected organization,
packages, and registry operations. Do not grant storage-bucket credentials:
Regbot does not need them.

Bind `serve` to loopback or a private network. Configure
`--run-token-env` or `--run-token-file` whenever another host can reach it,
terminate TLS at a trusted proxy, and apply network policy. The one-shot model
is safer when HTTP execution is unnecessary.

Do not enable `tls.insecure_skip_verify` outside disposable development
environments.
