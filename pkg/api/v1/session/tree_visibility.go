// SPDX-License-Identifier: MIT

package session

// TreeVisibility is the §8.3 / §8.5 visibility boundary carried on a
// delegation lease. In v1 the lease is realised by the child session
// row (§4.2 line 161 design clarification), so the value persists on
// sessions.tree_visibility. It controls the scope of the task tree a
// session observes via lenny/get_task_tree.
//
// The three values are ordered from broadest to narrowest. The ordering
// is strict: a child lease may narrow visibility at any delegation hop
// but may never widen it (§8.3 lines 313-317).
//
// spec: §8.5 line 540; §8.3 lines 311-319.
type TreeVisibility string

const (
	// VisibilityFull — the session sees the entire subtree rooted at the
	// tree root, including siblings and their descendants. This is the
	// §8.5 default and the only value compatible with a resolved
	// messagingScope of `siblings`. spec: §8.5 line 540.
	VisibilityFull TreeVisibility = "full"

	// VisibilityParentAndSelf — the session sees only its own node and
	// its direct parent's node. spec: §8.5 line 540.
	VisibilityParentAndSelf TreeVisibility = "parent-and-self"

	// VisibilitySelfOnly — the session sees only its own node. spec: §8.5
	// line 540.
	VisibilitySelfOnly TreeVisibility = "self-only"
)

// IsValid reports whether v is one of the three §8.5 enum values.
func (v TreeVisibility) IsValid() bool {
	switch v {
	case VisibilityFull, VisibilityParentAndSelf, VisibilitySelfOnly:
		return true
	default:
		return false
	}
}

// OrDefault returns v when it is a recognised value, otherwise
// VisibilityFull. The §8.5 default of `full` applies at the root session
// where no parent exists; below the root, an empty field means
// inheritance (resolved by the delegation Service before storage), so a
// persisted row is normally explicit. A blank or unrecognised stored
// value still resolves to the broadest, fail-open default rather than
// silently hiding a tree. spec: §8.5 line 540; §8.3 line 315.
func (v TreeVisibility) OrDefault() TreeVisibility {
	if v.IsValid() {
		return v
	}
	return VisibilityFull
}

// rank orders the enum from broadest (0) to narrowest (2) per the §8.3
// strict ordering `full → parent-and-self → self-only`. An unrecognised
// value ranks as the broadest so OrDefault and rank agree. spec: §8.3
// line 313.
func (v TreeVisibility) rank() int {
	switch v {
	case VisibilityParentAndSelf:
		return 1
	case VisibilitySelfOnly:
		return 2
	default:
		return 0 // VisibilityFull and any unrecognised value
	}
}

// AtLeastAsNarrow reports whether v is at least as narrow as parent. A
// child lease's treeVisibility must satisfy this against the parent's
// effective value: a child may equal or narrow the parent's visibility
// but may never widen it. spec: §8.3 lines 313-317.
func (v TreeVisibility) AtLeastAsNarrow(parent TreeVisibility) bool {
	return v.OrDefault().rank() >= parent.OrDefault().rank()
}

// MessagingScope is the §7.2 inter-session messaging reachability
// boundary. It is not a per-delegation lease field — the gateway
// resolves the effective value from the deployment/tenant/runtime
// configuration hierarchy at session-creation time (the narrowest of
// deployment maxScope, tenant scope, and the top-most parent runtime
// scope). It is compared against the lease's treeVisibility at
// delegation time: a resolved `siblings` scope requires `full`
// visibility so that children can discover one another via
// lenny/get_task_tree.
//
// spec: §7.2 lines 236-266; §8.3 lines 321-324.
type MessagingScope string

const (
	// MessagingScopeDirect — a session may message only its direct
	// parent and its direct children. The §7.2 default. spec: §7.2 line
	// 240.
	MessagingScopeDirect MessagingScope = "direct"

	// MessagingScopeSiblings — a session may additionally message
	// sibling tasks (children of the same parent). Requires
	// treeVisibility `full`. spec: §7.2 line 241.
	MessagingScopeSiblings MessagingScope = "siblings"
)

// OrDefault returns s when it is a recognised value, otherwise
// MessagingScopeDirect (the §7.2 default for sessions without an
// override). spec: §7.2 line 240, line 254 (`defaultScope: direct`).
func (s MessagingScope) OrDefault() MessagingScope {
	if s == MessagingScopeSiblings {
		return MessagingScopeSiblings
	}
	return MessagingScopeDirect
}

// restrictiveness orders the §7.2 scopes from most to least restrictive:
// `direct` (0) is narrower than `siblings` (1). An unrecognised value
// collapses to `direct` via OrDefault. spec: §7.2 line 266
// ("restrictiveness order is: direct < siblings").
func (s MessagingScope) restrictiveness() int {
	if s.OrDefault() == MessagingScopeSiblings {
		return 1
	}
	return 0
}

// narrowerMessagingScope returns the more restrictive of a and b, each
// normalised through OrDefault.
func narrowerMessagingScope(a, b MessagingScope) MessagingScope {
	if b.restrictiveness() < a.restrictiveness() {
		return b.OrDefault()
	}
	return a.OrDefault()
}

// ResolveEffectiveMessagingScope computes a session's effective §7.2
// messagingScope from the deployment/tenant/runtime configuration
// hierarchy. Per the §7.2 "Effective scope" rule the result is the
// narrowest of:
//
//   - the base scope, which is the tenant scope when set, otherwise the
//     deployment defaultScope,
//   - the top-most parent runtime scope when set, and
//   - the deployment maxScope ceiling (no tenant or runtime can widen
//     beyond it).
//
// tenantScope and runtimeScope are optional: an empty string means "no
// override at this level" and is skipped rather than read as `direct`.
// An empty deploymentDefault resolves to the §7.2 default `direct`
// (siblings is opt-in). An empty deploymentMax imposes no ceiling beyond
// the enum; only an explicit `direct` ceiling lowers the result. The
// restrictiveness order is `direct` < `siblings`.
//
// spec: §7.2 lines 250-266 (configuration hierarchy; "Effective scope"
// rule); §8.3 lines 321-324. F-7.2.6.
func ResolveEffectiveMessagingScope(deploymentDefault, deploymentMax, tenantScope, runtimeScope MessagingScope) MessagingScope {
	base := deploymentDefault.OrDefault()
	if tenantScope != "" {
		base = narrowerMessagingScope(base, tenantScope)
	}
	if runtimeScope != "" {
		base = narrowerMessagingScope(base, runtimeScope)
	}
	// An unset maxScope leaves the ceiling at the widest enum value;
	// only an explicit `direct` ceiling caps the resolved base.
	if deploymentMax == MessagingScopeDirect {
		return MessagingScopeDirect
	}
	return base
}
