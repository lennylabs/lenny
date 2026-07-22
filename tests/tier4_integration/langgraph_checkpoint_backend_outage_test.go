// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §26.8 langgraph checkpoint-backend
// outage behavior: what a langgraph session with
// runtimeOptions.checkpointBackend set to redis or postgres does when
// the backing datastore is unavailable mid-checkpoint.
//
// See the sibling tests/tier2_component/credential/checkpoint_scope_test.go
// and tests/tier9_security/checkpoint_tenant_isolation_test.go for the
// credential-issuance and tenant-isolation halves of the same §26.8
// checkpointing sentence; this test covers the outage/failover axis.
package tier4_integration_test

import "testing"

// spec: §26.8 ("Checkpointing (when runtimeOptions.checkpointBackend:
// postgres or redis) uses Lenny's platform Postgres/Redis, scoped to the
// session's tenant. The runtime's adapter connects via short-lived
// credentials issued by the credential leasing service with scope
// datastore.checkpoint.rw.") and spec/14_workspace-plan-schema.md:183
// (runtimeOptions.checkpointBackend enum ["memory", "postgres", "redis"]).
//
// diagnosis: once unskipped, a failure here means a langgraph session
// checkpointing to Redis does not follow the documented behavior when
// Redis becomes unavailable mid-write (whether that behavior turns out to
// be fail-open, degrade to a Postgres fallback, or fail-closed and block
// the graph step).
func TestLanggraphCheckpointRedisOutageFollowsDocumentedBehavior(t *testing.T) {
	// §26.8's checkpointing sentence says only that checkpointing "uses
	// Lenny's platform Postgres/Redis, scoped to the session's tenant" via
	// a datastore.checkpoint.rw-scoped credential. It does not say what
	// happens to an in-progress LangGraph checkpoint write (a
	// langgraph-checkpoint-postgres / langgraph-checkpoint-redis saver
	// call from inside the adapter) when the target backend becomes
	// unavailable partway through a run: whether the graph step blocks
	// until the datastore recovers (fail closed, mirroring the delegation
	// budget counters' Redis-outage behavior in
	// spec/12_storage-architecture.md §12.4's failure-behavior table), or
	// proceeds without a durable checkpoint (fail open, mirroring the
	// rate-limit and quota fail-open windows in the same table). §12.4's
	// per-key failure-behavior table enumerates specific Redis-backed
	// concerns (session slot counters, storage quota, delegation budgets,
	// billing events, rate limiting) with a documented failure mode for
	// each; the langgraph checkpoint-backend connection this test would
	// exercise is not one of the enumerated keys, and §4.9 (Credential
	// Leasing Service) defines no "datastore" provider or
	// datastore.checkpoint.rw scope grammar at all — matching the existing
	// non-blocking skips in
	// tests/tier2_component/credential/checkpoint_scope_test.go and
	// tests/tier9_security/checkpoint_tenant_isolation_test.go. There is
	// therefore no documented behavior to assert against, and no adapter
	// to drive: the langgraph reference runtime
	// (github.com/lennylabs/runtime-langgraph, cmd/runtimes/langgraph) is
	// not vendored in this repo and is deferred to Wave 6 / Phase 11 per
	// tests/spec-map-exceptions.yaml section 26.8. A repository-wide search
	// for "checkpointBackend" outside schema validation confirms no
	// package in pkg/ inspects or acts on this field.
	t.Skip("spec does not define fail-open/fail-closed behavior for a langgraph checkpointBackend outage, and the langgraph reference runtime that would connect to Redis/Postgres for checkpointing does not exist yet (§4.9 defines no datastore.checkpoint.rw scope; §26.8 langgraph is deferred to Phase 11) — needs a spec/product decision before this can be driven against real code")
}

// spec: §26.8 ("Checkpointing (when runtimeOptions.checkpointBackend:
// postgres or redis) uses Lenny's platform Postgres/Redis, scoped to the
// session's tenant.") read together with spec/12_storage-architecture.md
// §12.3 ("Behavior during Postgres failover: During the Postgres failover
// window (up to 30s), Redis remains the primary coordination mechanism
// and all Redis-backed roles continue normally... new sessions and writes
// requiring Postgres... are rejected or queued as specified below.").
//
// diagnosis: once unskipped, a failure here means a langgraph session
// checkpointing to Postgres does not survive (or does not correctly
// surface) a Postgres HA failover that occurs while a checkpoint write is
// in flight.
func TestLanggraphCheckpointPostgresFailoverMidWrite(t *testing.T) {
	// §12.3 documents Postgres HA failover behavior in general terms (RTO
	// < 30s, write-durability categories by write class), but its
	// "Write durability categories during failover" table does not name a
	// langgraph checkpoint write as one of its categories, and §26.8 does
	// not say whether a langgraph checkpoint write in flight during a
	// Postgres failover is retried, surfaced to the graph as an error, or
	// silently lost. As with the Redis-outage case above, this is a gap
	// between what §26.8 promises (checkpointing "uses Lenny's platform
	// Postgres... scoped to the session's tenant") and what §4.9 and §12.3
	// define for this specific credential-scoped write path, compounded by
	// the langgraph reference runtime itself not existing in this repo
	// (deferred to Wave 6 / Phase 11 per
	// tests/spec-map-exceptions.yaml section 26.8). There is no adapter,
	// no datastore.checkpoint.rw credential issuance, and no documented
	// per-write-class behavior to assert against.
	t.Skip("spec does not define langgraph checkpoint-write behavior across a Postgres HA failover, and the langgraph reference runtime that would perform such a write does not exist yet (§4.9 defines no datastore.checkpoint.rw scope; §26.8 langgraph is deferred to Phase 11) — needs a spec/product decision before this can be driven against real code")
}
