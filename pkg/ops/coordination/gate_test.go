// SPDX-License-Identifier: MIT

package coordination_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/coordination"
)

// fakeReplicas is a fixed ReplicaCounter for the gate tests.
type fakeReplicas int

func (f fakeReplicas) ReplicaCount() int { return int(f) }

// spec: §25.4 line 2212 — the empty string and "single-replica-only"
// both resolve to the default; "always" and "never" are honored; a typo
// is rejected so the binary can fail fast.
func TestParseMemoryTier(t *testing.T) {
	cases := []struct {
		in   string
		want coordination.MemoryTier
		ok   bool
	}{
		{"", coordination.MemoryTierSingleReplicaOnly, true},
		{"single-replica-only", coordination.MemoryTierSingleReplicaOnly, true},
		{"always", coordination.MemoryTierAlways, true},
		{"never", coordination.MemoryTierNever, true},
		{"sometimes", "", false},
		{"Single-Replica-Only", "", false}, // case-sensitive
	}
	for _, c := range cases {
		got, ok := coordination.ParseMemoryTier(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("ParseMemoryTier(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// spec: §25.4 lines 2206-2212 — the single-replica-only default grants a
// Tier-3 acquire only when the deployment is a single replica.
func TestGateSingleReplicaOnly(t *testing.T) {
	t.Run("single replica grants without warning", func(t *testing.T) {
		g := coordination.NewCoordinationGate(coordination.MemoryTierSingleReplicaOnly, fakeReplicas(1))
		if dec := g.Evaluate(); dec.Reject || dec.Warning != "" {
			t.Errorf("decision = %+v, want grant with no warning", dec)
		}
	})
	t.Run("multi replica rejects", func(t *testing.T) {
		g := coordination.NewCoordinationGate(coordination.MemoryTierSingleReplicaOnly, fakeReplicas(3))
		if dec := g.Evaluate(); !dec.Reject {
			t.Errorf("decision = %+v, want reject in a multi-replica deployment", dec)
		}
	})
	t.Run("nil counter (single-process dev) grants", func(t *testing.T) {
		g := coordination.NewCoordinationGate(coordination.MemoryTierSingleReplicaOnly, nil)
		if dec := g.Evaluate(); dec.Reject {
			t.Errorf("decision = %+v, want grant when no replica counter is wired", dec)
		}
	})
}

// spec: §25.4 line 2209 — "always" grants every acquire but attaches a
// replica-local warning, regardless of replica count.
func TestGateAlwaysGrantsWithWarning(t *testing.T) {
	for _, replicas := range []fakeReplicas{1, 5} {
		g := coordination.NewCoordinationGate(coordination.MemoryTierAlways, replicas)
		dec := g.Evaluate()
		if dec.Reject {
			t.Errorf("replicas=%d: reject, want grant in always mode", replicas)
		}
		if dec.Warning == "" {
			t.Errorf("replicas=%d: missing degradation warning in always mode", replicas)
		}
	}
}

// spec: §25.4 line 2210 — "never" disables Tier 3, rejecting every
// acquire even on a single replica.
func TestGateNeverRejects(t *testing.T) {
	g := coordination.NewCoordinationGate(coordination.MemoryTierNever, fakeReplicas(1))
	if dec := g.Evaluate(); !dec.Reject {
		t.Errorf("decision = %+v, want reject in never mode", dec)
	}
}

func TestGateTierAccessor(t *testing.T) {
	g := coordination.NewCoordinationGate(coordination.MemoryTierNever, nil)
	if g.Tier() != coordination.MemoryTierNever {
		t.Errorf("Tier() = %q, want never", g.Tier())
	}
}
