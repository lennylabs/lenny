// SPDX-License-Identifier: MIT

// Package drainreadiness serves the §12.5 GET /internal/drain-readiness
// endpoint. The lenny-drain-readiness ValidatingAdmissionWebhook
// queries this endpoint before admitting a node-drain pod eviction: it
// runs a MinIO liveness probe so a planned drain cannot evict agent
// pods into a degraded artifact store and lose un-checkpointed
// workspace state.
//
// The package also serves the §12.5 POST
// /internal/audit/node-drain-forced endpoint, the durable hop the
// webhook uses to commit the §16.7 `node.drain.forced` audit event into
// the gateway's per-tenant §11.7 audit hash chain.
//
// spec: GWDRAIN
package drainreadiness
