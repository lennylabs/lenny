// SPDX-License-Identifier: MIT

package coordination

// MemoryTier is the §25.4 ops.locks.memoryTier policy governing whether
// the Tier-3 in-memory remediation-lock store may grant an acquisition.
// It is consulted only when the lock service is serving from the
// in-memory tier (both Postgres and Redis unreachable); in the v1
// deployment the in-memory store is the only tier, so the policy applies
// to every acquisition.
//
// spec: §25.4 lines 2206-2212 (Tier 3 behavior), line 1539
// (Multi-Replica Scaling trade-off), line 2326 (the error code).
type MemoryTier string

const (
	// MemoryTierSingleReplicaOnly is the §25.4 line 2212 default: a
	// Tier-3 acquisition succeeds only when lenny-ops runs as a single
	// replica (detected via the K8s Endpoints lookup). Multi-replica
	// deployments reject the acquisition so two replicas never grant
	// uncoordinated locks on the same scope.
	MemoryTierSingleReplicaOnly MemoryTier = "single-replica-only"
	// MemoryTierAlways always grants the Tier-3 acquisition and attaches a
	// degradation warning that coordination is replica-local. Safe for a
	// single-replica deployment; unsafe for multi-replica production.
	MemoryTierAlways MemoryTier = "always"
	// MemoryTierNever disables Tier 3: when both Postgres and Redis are
	// down, every acquisition is rejected. The most conservative option.
	MemoryTierNever MemoryTier = "never"
)

// ParseMemoryTier maps an operator-supplied ops.locks.memoryTier string
// to a MemoryTier. The empty string resolves to the §25.4 line 2212
// default (single-replica-only); an unrecognized value returns ok=false
// so the binary can reject a typo at startup rather than silently
// degrading to an unintended safety posture.
func ParseMemoryTier(s string) (MemoryTier, bool) {
	switch MemoryTier(s) {
	case "":
		return MemoryTierSingleReplicaOnly, true
	case MemoryTierSingleReplicaOnly:
		return MemoryTierSingleReplicaOnly, true
	case MemoryTierAlways:
		return MemoryTierAlways, true
	case MemoryTierNever:
		return MemoryTierNever, true
	default:
		return "", false
	}
}

// ReplicaCounter reports the current number of ready lenny-ops replicas.
// The §25.4 single-replica-only policy consults it to decide whether an
// in-memory (Tier-3) acquisition is safe.
//
// spec: §25.4 line 2208 ("detected via K8s Endpoints lookup at startup
// and re-checked every 30s").
type ReplicaCounter interface {
	ReplicaCount() int
}

// CoordinationGate applies the §25.4 ops.locks.memoryTier policy to a
// Tier-3 (in-memory) remediation-lock acquisition. The HTTP layer
// consults it on the acquire path before delegating to the store.
type CoordinationGate struct {
	tier     MemoryTier
	replicas ReplicaCounter
}

// NewCoordinationGate returns a gate enforcing tier. replicas may be nil
// for a single-process deployment with no cluster connection, in which
// case the single-replica-only policy treats the deployment as a single
// replica and grants the acquisition.
func NewCoordinationGate(tier MemoryTier, replicas ReplicaCounter) *CoordinationGate {
	return &CoordinationGate{tier: tier, replicas: replicas}
}

// Tier returns the gate's configured §25.4 memoryTier mode.
func (g *CoordinationGate) Tier() MemoryTier { return g.tier }

// CoordinationDecision is the gate's verdict for a Tier-3 acquisition.
type CoordinationDecision struct {
	// Reject is set when the acquisition must fail closed with
	// REMEDIATION_LOCK_NO_COORDINATION rather than grant an uncoordinated
	// in-memory lock.
	Reject bool
	// Warning, when non-empty, is the §25.2 degradation.warnings entry the
	// handler attaches to a granted lock so the agent learns the lock is
	// replica-local. Set only in the "always" mode.
	Warning string
}

// Evaluate returns the §25.4 decision for a Tier-3 in-memory acquisition.
//
//   - "never": always reject (Tier 3 disabled).
//   - "always": always grant, with a replica-local warning.
//   - "single-replica-only" (default): grant only when the deployment is
//     a single replica; reject otherwise.
//
// spec: §25.4 lines 2206-2212, 1539.
func (g *CoordinationGate) Evaluate() CoordinationDecision {
	switch g.tier {
	case MemoryTierNever:
		return CoordinationDecision{Reject: true}
	case MemoryTierAlways:
		return CoordinationDecision{Warning: "remediation-lock coordination is replica-local: " +
			"the in-memory lock tier provides no cross-replica coordination during a " +
			"dual Postgres and Redis outage (ops.locks.memoryTier=always)"}
	default: // single-replica-only
		if g.replicas != nil && g.replicas.ReplicaCount() > 1 {
			return CoordinationDecision{Reject: true}
		}
		return CoordinationDecision{}
	}
}
