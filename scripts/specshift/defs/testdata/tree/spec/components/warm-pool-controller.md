---
code: WPC
type: component
summary: The controller that creates agent pods.
---

# WarmPoolController

## Deployment

<a id="grant-pods-write"></a>

```lenny-def
kind: grant
name: pods write
api-groups: [""]
resources: [pods]
verbs: [create, delete, patch]
namespace-scope: agent namespace
requirement: POOL-R1
```

<!-- generated:rbac-grants -->
stale
<!-- /generated:rbac-grants -->

<!-- generated:network-rules -->
<!-- /generated:network-rules -->

## Settings read

<!-- generated:settings-read -->
<!-- /generated:settings-read -->

## Metrics and alerts

<!-- generated:metrics-emitted -->
<!-- /generated:metrics-emitted -->

Text after the sections stays.
