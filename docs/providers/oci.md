# OCI provider

The OCI provider targets registries implementing the OCI/Docker Distribution
HTTP API, including CNCF Distribution 3.x.

It supports basic authentication and standard bearer-token challenges. Catalog
and tag pagination follow `Link` headers. Manifest negotiation includes OCI
manifests/indexes and Docker schema 2 manifests/lists.

Regbot queries the OCI Referrers API before considering a manifest deletable.
If the registry returns `404`, Regbot follows the OCI Distribution
Specification and reads the digest-derived referrers tag. A missing fallback
tag means no known referrers. Malformed indexes, invalid descriptor digests,
unexpected media types, and unexpected status codes fail closed.

Deletion is always performed with:

```text
DELETE /v2/<repository>/manifests/<digest>
```

Regbot never deletes blobs directly. Manifest deletion must be enabled in the
registry configuration.

If catalog access is disabled, configure `registries.<name>.repositories`.
Regbot fails closed rather than assuming an incomplete catalog is complete.

## R2-backed Distribution

R2 is an S3-compatible storage backend from the registry's perspective. A
manifest deletion does not guarantee immediate physical space reclamation.
Unreferenced layers are removed only by Distribution garbage collection.

Run garbage collection through external operational automation and follow the
Distribution version's read-only/maintenance guidance. Do not configure bucket
lifecycle rules that delete live registry metadata or blobs by age.
