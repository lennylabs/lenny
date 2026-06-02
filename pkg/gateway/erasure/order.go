// SPDX-License-Identifier: MIT

package erasure

import (
	"fmt"
	"sort"
)

// canonicalRank assigns each §12.8 erasure store a position in the
// dependency-ordered DeleteByUser sequence (spec lines 792-836). The
// rank pins the intended order so ValidateOrder can reject a wiring that
// would erase a parent store before its foreign-key children. The values
// are spaced so a store added between two existing ones can be ranked
// without renumbering. A store wired into the Orchestrator that is not
// listed here is rejected, which forces a new store to be ranked rather
// than silently slotted in at an arbitrary position.
//
// spec: §12.8 lines 792-836 (DeleteByUser dependency-ordered sequence).
var canonicalRank = map[string]int{
	"leases":               10,  // step 1
	"semantic_cache":       20,  // step 2
	"redis_caches":         30,  // step 3
	"experiment_sticky":    40,  // step 4
	"billing_buffer":       50,  // step 5
	"quota":                60,  // step 6
	"artifacts":            70,  // step 7 (ArtifactStore, MinIO)
	"transcripts":          75,  // step 7 family (session transcripts)
	"interactions":         78,  // session interaction records (FK child of sessions)
	"memory":               80,  // step 8
	"eviction_state":       90,  // step 9
	"session_dlq_archive":  100, // step 10
	"session_tree_archive": 110, // step 11
	"eval_results":         120, // step 12
	"audit":                130, // step 13
	"billing":              150, // step 15
	"delegation_budget":    160, // step 16
	"sessions":             170, // step 17 (SessionStore, the FK parent)
	"tokens":               180, // step 18
	"credential_pool":      190, // step 19
}

// fkMustPrecede lists the (child, parent) ordering constraints the §12.8
// sequence is required to honor because the child store holds rows with a
// foreign key into the parent. Erasing the parent first would either
// violate the constraint or orphan the child rows. The spec names the
// eval_results and session_tree_archive edges explicitly (lines 807-808);
// the remaining session-keyed child stores carry the same
// `... → sessions.id` reference.
//
// spec: §12.8 lines 807-808 ("Must precede SessionStore deletion to
// satisfy the FK dependency").
var fkMustPrecede = [][2]string{
	{"eval_results", "sessions"},
	{"session_tree_archive", "sessions"},
	{"transcripts", "sessions"},
	{"artifacts", "sessions"},
	{"interactions", "sessions"},
	{"eviction_state", "sessions"},
	{"session_dlq_archive", "sessions"},
	{"memory", "sessions"},
}

// ValidateOrder checks that the erasure configuration honors the §12.8
// dependency order before the Orchestrator runs a single deletion. It is
// the runtime contract the spec's 20-step sequence implies: the gateway
// calls it at startup and refuses to serve if a future reorder of the
// store wiring would erase a foreign-key parent before its children.
//
// The effective execution order is every session-scoped store (which the
// Orchestrator erases first, per session) followed by every user-scoped
// store. ValidateOrder rejects (a) any store name not present in
// canonicalRank, (b) a store wired into more than one slot, and (c) a
// foreign-key child appearing after its parent in the effective order.
//
// spec: §12.8 lines 792-836.
func ValidateOrder(cfg Config) error {
	// Effective order: session-scoped first (they run before any
	// user-scoped store), then user-scoped, each in config order.
	var order []string
	seen := map[string]bool{}
	for _, se := range cfg.SessionScoped {
		if seen[se.Name] {
			return fmt.Errorf("erasure: store %q wired into more than one slot", se.Name)
		}
		seen[se.Name] = true
		order = append(order, se.Name)
	}
	for _, e := range cfg.UserScoped {
		if seen[e.Name] {
			return fmt.Errorf("erasure: store %q wired into more than one slot", e.Name)
		}
		seen[e.Name] = true
		order = append(order, e.Name)
	}

	pos := map[string]int{}
	for i, name := range order {
		if _, ok := canonicalRank[name]; !ok {
			return fmt.Errorf("erasure: store %q has no §12.8 dependency rank; add it to canonicalRank", name)
		}
		pos[name] = i
	}

	for _, edge := range fkMustPrecede {
		child, parent := edge[0], edge[1]
		ci, childWired := pos[child]
		pi, parentWired := pos[parent]
		if childWired && parentWired && ci >= pi {
			return fmt.Errorf(
				"erasure: §12.8 FK order violation — %q (rank %d) must be erased before %q (rank %d) but is wired after it",
				child, canonicalRank[child], parent, canonicalRank[parent])
		}
	}
	return nil
}

// CanonicalOrder returns the §12.8 store names in dependency order. It is
// the reference sequence ValidateOrder pins against, exposed for tests
// and operator documentation.
func CanonicalOrder() []string {
	names := make([]string, 0, len(canonicalRank))
	for name := range canonicalRank {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return canonicalRank[names[i]] < canonicalRank[names[j]]
	})
	return names
}
