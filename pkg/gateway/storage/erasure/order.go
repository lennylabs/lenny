// SPDX-License-Identifier: MIT

package erasure

import (
	"fmt"
	"sort"
)

// canonicalRank names every §12.8 erasure store and fixes its rank. The
// ranks follow the §12.8 tenant-deletion Phase 4 order, so the tenant
// Phase 4 sequence runs in strictly increasing rank (ValidateTenantOrder).
// The user-erasure sequence orders a few steps differently from Phase 4
// (the audit no-op and EvalResultStore, for example), so ValidateOrder holds
// it only to the fkMustPrecede edges rather than to strict rank.
//
// Every name here must be wired on each path or recorded in that path's
// exemption map (userExempt, tenantExempt) with a reason; the validators
// refuse a wiring that does neither. A store wired under a name that is not
// listed here is rejected, so a new store is ranked rather than slotted in at
// an arbitrary position. The values are spaced so a store added between two
// existing ones can be ranked without renumbering.
//
// spec: §12.8 (Compliance Interfaces: Storage backends in erasure scope,
// DeleteByUser dependency-ordered deletion sequence, Tenant deletion
// lifecycle).
var canonicalRank = map[string]int{
	"leases":                 10,  // LeaseStore
	"coordination_mirror":    15,  // the §10.1.8 Postgres mirror of the LeaseStore coordination leases
	"semantic_cache":         20,  // SemanticCache
	"redis_caches":           30,  // Redis caches
	"experiment_sticky":      40,  // experiment sticky assignment cache
	"billing_buffer":         50,  // billing write-ahead buffer (Redis stream)
	"quota":                  60,  // QuotaStore
	"artifacts":              70,  // ArtifactStore
	"checkpoint_manifests":   71,  // ArtifactStore checkpoints: checkpoint manifests
	"checkpoint_retention":   72,  // ArtifactStore checkpoints: session_checkpoints
	"checkpoint_meta":        73,  // ArtifactStore checkpoints: session_checkpoint_meta
	"transcripts":            75,  // ArtifactStore: session transcripts
	"interactions":           78,  // session interaction records (FK child of sessions)
	"memory":                 80,  // MemoryStore
	"eviction_state":         90,  // EvictionStateStore
	"session_dlq_archive":    100, // session_dlq_archive
	"session_message_queues": 105, // session DLQ and durable inbox keys
	"session_tree_archive":   110, // session_tree_archive
	"audit":                  130, // EventStore (audit)
	"billing":                150, // EventStore (billing)
	"eval_results":           155, // EvalResultStore
	"delegation_budget":      160, // delegation budget keys
	"sessions":               170, // SessionStore, the FK parent
	"tokens":                 180, // TokenStore
	"issued_tokens":          185, // TokenIssuanceStore
	"credential_pool":        190, // CredentialPoolStore
	"runtime_tenant_access":  200, // runtime_tenant_access rows
	"pool_tenant_access":     205, // pool_tenant_access rows
	"user_roles":             210, // user role mappings
	"custom_roles":           220, // custom role definitions
	"tenant_definitions":     230, // connector, environment, and experiment definitions
	"tenant_config":          240, // tenant configuration record
}

// Exemption reasons shared by the two exemption maps. A "no production
// writer" reason is held honest by the tier-0 erasure exemption guard, which
// fails when a production writer appears so the exemption is revisited.
const (
	redisCachesExemption = "no production writer: the token service constructs tokencache.Cache and discards it, " +
		"and no production package imports routingcache"
	evictionStateExemption = "no production writer: evictionstatestore and its pgstore are imported only by " +
		"evictionfallback, which no production package imports"
	sessionDLQArchiveExemption = "erased by ON DELETE CASCADE from sessions when the SessionStore entry runs"
	tenantScopedExemption      = "tenant-scoped rows that hold no user's personal data; tenant deletion erases them"
)

// userExempt records, for each canonicalRank name the user-erasure path does
// not wire, why the path leaves it alone. A name is wired or exempt on the
// path, never both and never neither.
//
// spec: §12.8 (Compliance Interfaces: DeleteByUser dependency-ordered
// deletion sequence).
var userExempt = map[string]string{
	"redis_caches":        redisCachesExemption,
	"eviction_state":      evictionStateExemption,
	"session_dlq_archive": sessionDLQArchiveExemption,
	"billing": "the erasure job's billing phase pseudonymizes the user's billing events " +
		"(erasurejob.BillingEraser) rather than deleting them",
	"runtime_tenant_access": tenantScopedExemption,
	"pool_tenant_access":    tenantScopedExemption,
	"user_roles": "user erasure keeps the user row under processing restriction rather than " +
		"deleting it (Processing restriction during erasure)",
	"custom_roles":       tenantScopedExemption,
	"tenant_definitions": tenantScopedExemption,
	"tenant_config":      tenantScopedExemption,
}

// tenantExempt records, for each canonicalRank name the tenant-deletion
// Phase 4 path does not wire, why the path leaves it alone.
//
// spec: §12.8 (Compliance Interfaces: Tenant deletion lifecycle).
var tenantExempt = map[string]string{
	"redis_caches":        redisCachesExemption,
	"eviction_state":      evictionStateExemption,
	"session_dlq_archive": sessionDLQArchiveExemption,
}

// fkMustPrecede lists the (child, parent) ordering constraints the §12.8
// sequence is required to honor because the child store holds rows with a
// foreign key into the parent. Erasing the parent first would either
// violate the constraint or orphan the child rows. The spec names the
// eval_results and session_tree_archive edges explicitly in the
// DeleteByUser dependency-ordered deletion sequence; the remaining
// session-keyed child stores carry the same `... → sessions.id` reference.
//
// spec: §12.8 (Compliance Interfaces: DeleteByUser dependency-ordered
// deletion sequence).
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

// userPath and tenantPath name the two erasure paths in validation errors.
const (
	userPath   = "user erasure"
	tenantPath = "tenant deletion Phase 4"
)

// ValidateOrder checks the user-erasure wiring before the Orchestrator runs a
// single deletion. The gateway calls it at startup and refuses to serve when
// it fails.
//
// The effective execution order is every session-scoped store (which the
// Orchestrator erases first, per session) followed by every user-scoped
// store. ValidateOrder rejects (a) a store name not present in
// canonicalRank, (b) a store wired into more than one slot, (c) a
// foreign-key child appearing after its parent in the effective order, and
// (d) an incomplete wiring: every canonicalRank name must be wired (a
// nil-function entry for an absent backend counts) or listed in userExempt
// with a non-empty reason, and not both. The user sequence orders some steps
// differently from the tenant Phase 4 order, so only the fkMustPrecede edges
// constrain it rather than strict rank.
//
// spec: §12.8 (Compliance Interfaces: DeleteByUser dependency-ordered
// deletion sequence, Storage backends in erasure scope).
func ValidateOrder(cfg Config) error {
	// Effective order: session-scoped first (they run before any
	// user-scoped store), then user-scoped, each in config order.
	order := make([]string, 0, len(cfg.SessionScoped)+len(cfg.UserScoped))
	for _, se := range cfg.SessionScoped {
		order = append(order, se.Name)
	}
	for _, e := range cfg.UserScoped {
		order = append(order, e.Name)
	}
	pos, err := rankedPositions(order, userPath)
	if err != nil {
		return err
	}
	for _, edge := range fkMustPrecede {
		child, parent := edge[0], edge[1]
		ci, childWired := pos[child]
		pi, parentWired := pos[parent]
		if childWired && parentWired && ci >= pi {
			return fmt.Errorf(
				"erasure: §12.8 FK order violation — %q (rank %d) must be erased before %q (rank %d) but is wired after it",
				child, canonicalRank[child], parent, canonicalRank[parent],
			)
		}
	}
	return checkComplete(pos, userExempt, userPath)
}

// ValidateTenantOrder checks that a §12.8 tenant-deletion Phase 4
// eraser sequence is complete and runs in dependency order before any store
// is erased. Phase 4 runs DeleteByTenant on each store in the order given and
// stops at the first error, so the names must be known to canonicalRank,
// appear once each, and run in strictly increasing rank. Strict rank order
// satisfies every fkMustPrecede edge, and it is what makes a Phase 4 re-run
// safe without saved progress: each entry runs after every entry it depends
// on. Every canonicalRank name must also be wired (a nil-function entry for
// an absent backend counts) or listed in tenantExempt with a non-empty
// reason, and not both.
//
// spec: §12.8 (Compliance Interfaces: Tenant deletion lifecycle).
func ValidateTenantOrder(names []string) error {
	pos, err := rankedPositions(names, tenantPath)
	if err != nil {
		return err
	}
	prev, prevName := -1, ""
	for _, name := range names {
		rank := canonicalRank[name]
		if rank <= prev {
			return fmt.Errorf(
				"erasure: §12.8 tenant Phase 4 order violation — %q (rank %d) is wired after %q (rank %d)",
				name, rank, prevName, prev,
			)
		}
		prev, prevName = rank, name
	}
	return checkComplete(pos, tenantExempt, tenantPath)
}

// rankedPositions maps each wired name to its position, rejecting a name
// with no canonicalRank entry and a name wired into more than one slot.
func rankedPositions(order []string, path string) (map[string]int, error) {
	pos := make(map[string]int, len(order))
	for i, name := range order {
		if _, ok := canonicalRank[name]; !ok {
			return nil, fmt.Errorf("erasure: %s store %q has no §12.8 dependency rank; add it to canonicalRank", path, name)
		}
		if _, dup := pos[name]; dup {
			return nil, fmt.Errorf("erasure: %s store %q wired into more than one slot", path, name)
		}
		pos[name] = i
	}
	return pos, nil
}

// checkComplete enforces that every canonicalRank name is either wired on the
// path or exempt on it with a reason, and never both. The names are checked
// in rank order so the first error is deterministic.
func checkComplete(wired map[string]int, exempt map[string]string, path string) error {
	for name := range exempt {
		if _, ok := canonicalRank[name]; !ok {
			return fmt.Errorf("erasure: %s exempts store %q, which has no §12.8 dependency rank", path, name)
		}
	}
	for _, name := range CanonicalOrder() {
		_, isWired := wired[name]
		reason, isExempt := exempt[name]
		switch {
		case isWired && isExempt:
			return fmt.Errorf("erasure: %s store %q is both wired and exempt; remove one", path, name)
		case isExempt && reason == "":
			return fmt.Errorf("erasure: %s store %q is exempt with an empty reason", path, name)
		case !isWired && !isExempt:
			return fmt.Errorf("erasure: %s store %q is neither wired nor exempt; wire it (a nil function for an absent backend) or record an exemption", path, name)
		}
	}
	return nil
}

// CanonicalOrder returns the §12.8 store names in rank order, which is the
// tenant-deletion Phase 4 order. ValidateTenantOrder pins a Phase 4 sequence
// against it, and tests use it as the reference sequence.
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
