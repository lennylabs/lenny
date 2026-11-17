---
code: XC
type: index
summary: >-
  Lists the properties every capability and component upholds, with the pages that carry each.
---

# Cross-cutting

Lists the properties every capability and component upholds, with the pages that carry each.

## Pages

| Page | Code | Summary |
| :-- | :-- | :-- |
| [API conventions](api-conventions.md) | `APICONV` | Conventions shared by the client, admin, and ops APIs: idempotency, ETags, pagination, dry-run, correlation headers, and rate-limit headers. |
| [Availability and degradation](availability-and-degradation.md) | `AVAIL` | Availability principles, fail-open and fail-closed rules, dependency outage behavior, and total-outage recovery. |
| [Data classification and retention](data-classification-and-retention.md) | `DATACLASS` | Data classes, the controls each class requires, encryption at rest, and retention defaults. |
| [Error model](error-model.md) | `ERR` | The error envelope, the error codes shared across interfaces, validation errors, the degradation envelope, and the refusal of removed fields. |
| [mTLS and PKI](mtls-and-pki.md) | `PKI` | The certificate authority, certificate lifecycle, and the peer validation every mTLS link performs. |
| [Naming](naming.md) | `NAMING` | The channel naming law, resource name rules, and the naming table of retired spellings. |
| [Observability conventions](observability-conventions.md) | `OBSCONV` | Metric and attribute naming, labels, log format, trace propagation, and the single source of alert rules. |
| [Security model](security-model.md) | `SECMODEL` | Trust boundaries, the threat model, the adapter-to-runtime boundary, and image supply chain controls. |
| [Tenant isolation](tenant-isolation.md) | `TENISO` | The tenant isolation property: tenant identifiers on every record, row-level security, key and object prefixes, and the query rules that keep tenants apart. |
| [Upgrade and compatibility](upgrade-and-compatibility.md) | `COMPAT` | The compatibility policy: API versioning, expand-contract migrations, CRD versioning, wire-contract versions, and version skew. |
