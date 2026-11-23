//go:build component

// SPDX-License-Identifier: MIT

package tokenproperties_test

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/common/scopes"
)

// playgroundCeiling is the playground_allowed_scope set the playground
// mint intersects a pasted subject's scope with.
const playgroundCeiling = "tools:sessions:* tools:me:read tools:runtimes:read tools:pools:read tools:operations:read tools:events:read"

// futureAction stands for an action a domain declares after a token was
// issued. A domain wildcard admits it and a list of today's actions does
// not, so reach over the universe below tells the two apart.
const futureAction = "purge"

// reachUniverse is every tool the reach comparisons read: each domain of
// the closed taxonomy with the generic actions, one specific tool action,
// and the future action.
var reachUniverse = func() []string {
	domains := make([]string, 0, len(scopes.Domains))
	for d := range scopes.Domains {
		domains = append(domains, d)
	}
	sort.Strings(domains)
	var out []string
	for _, d := range domains {
		for _, a := range []string{"read", "write", "scale", futureAction} {
			out = append(out, "tools:"+d+":"+a)
		}
	}
	return out
}()

// reach is the set of tools of reachUniverse a scope claim admits: every
// tool for an absent claim and tools:*, every action of a domain for its
// wildcard, and no tool for a claim that does not parse.
func reach(claim string) map[string]bool {
	out := map[string]bool{}
	set, err := scopes.Parse(claim)
	if err != nil {
		return out
	}
	for _, tool := range reachUniverse {
		if set.Matches(tool) {
			out[tool] = true
		}
	}
	return out
}

// within reports whether every tool of a is in b.
func within(a, b map[string]bool) bool {
	for tool := range a {
		if !b[tool] {
			return false
		}
	}
	return true
}

// strictlyNarrower reports whether issued admits fewer tools than subject,
// counting the future action, so a list of a domain's actions is narrower
// than the domain's wildcard and a list of every domain wildcard is not
// narrower than tools:*.
func strictlyNarrower(issued, subject string) bool {
	ri, rs := reach(issued), reach(subject)
	return within(ri, rs) && len(ri) < len(rs)
}

// grantedBy reports whether value is canonical and the subject claim
// grants it.
func grantedBy(subject, value string) bool {
	if _, err := scopes.ParseScope(value); err != nil {
		return false
	}
	set, err := scopes.Parse(subject)
	return err == nil && set.Matches(value)
}

// label names the token a jti belongs to, or the jti when the sequence
// never issued it.
func (w *world) label(jti string) string {
	if k := w.byJTI[jti]; k != nil {
		return k.name()
	}
	return jti
}

func (w *world) fail(t tb, property, format string, args ...any) {
	t.Helper()
	t.Fatalf("token rule property %q broken at step %d: %s\nsequence:\n%s",
		property, len(w.log), fmt.Sprintf(format, args...), w.sequence())
}

// checkStep checks every property against the step just applied.
func checkStep(t tb, w *world, o op, out outcome, before map[string]rowState) {
	t.Helper()
	if k := out.issued; k != nil && k.kind != kindSignIn {
		checkNarrowing(t, w, k)
		checkTenant(t, w, k)
		checkLineageRecorded(t, w, k)
		checkIdentity(t, w, o, k)
		checkPlaygroundOrigin(t, w, k)
		checkDepthAndAct(t, w, k)
		checkRevokedSources(t, w, k, before)
		checkLifetime(t, w, k)
	}
	checkRevocationReach(t, w, o, out, before)
	checkWhoMayRevoke(t, w, o, out, before)
	checkFullRevoke(t, w, o, out, before)
}

// lineageParent returns the token the issued token's row names in
// parent_jti, or nil when the row names none.
func (w *world) lineageParent(t tb, k *token) *token {
	t.Helper()
	parent := w.rows[k.c.JWTID].parent
	if parent == "" {
		return nil
	}
	p := w.byJTI[parent]
	if p == nil {
		w.fail(t, "lineage", "%s names parent %s, which the sequence never issued", k.name(), parent)
	}
	return p
}

// checkNarrowing checks that an issued token is no broader than the
// tokens it was issued from.
//
// spec: §13.3 (Credential Flow) "Token rule properties." item 1, §10.2
// (Authentication) "Playground mint invariants" invariant 2
func checkNarrowing(t tb, w *world, k *token) {
	t.Helper()
	const p = "1 narrowing"
	rk := reach(k.c.Scope)
	switch k.kind {
	case kindExchange, kindChild:
		issued, subject := strings.Fields(k.c.Scope), strings.Fields(k.subject.c.Scope)
		if !sameSet(issued, subject) {
			for _, v := range issued {
				if !grantedBy(k.subject.c.Scope, v) {
					w.fail(t, p, "%s carries %q, which %s's scope %q does not grant", k.name(), v, k.subject.name(), k.subject.c.Scope)
				}
			}
		}
		if !within(rk, reach(k.subject.c.Scope)) {
			w.fail(t, p, "%s (%q) is broader than its subject %s (%q)", k.name(), k.c.Scope, k.subject.name(), k.subject.c.Scope)
		}
		if k.actor != nil && !within(rk, reach(k.actor.c.Scope)) {
			w.fail(t, p, "child %s (%q) is broader than its actor %s (%q)", k.name(), k.c.Scope, k.actor.name(), k.actor.c.Scope)
		}
	case kindPlayground:
		if _, err := scopes.Parse(k.c.Scope); err != nil || strings.TrimSpace(k.c.Scope) == "" {
			w.fail(t, p, "playground bearer %s carries scope %q, which is absent or does not parse", k.name(), k.c.Scope)
		}
		if !within(rk, reach(playgroundCeiling)) || !within(rk, reach(k.subject.c.Scope)) {
			w.fail(t, p, "playground bearer %s (%q) is broader than the ceiling or its subject %s (%q)",
				k.name(), k.c.Scope, k.subject.name(), k.subject.c.Scope)
		}
	case kindImpersonation:
		if !within(rk, reach(k.call.c.Scope)) {
			w.fail(t, p, "impersonation bearer %s (%q) is broader than the caller's bearer %s (%q)",
				k.name(), k.c.Scope, k.call.name(), k.call.c.Scope)
		}
	}
	if parent := w.lineageParent(t, k); parent != nil && !within(rk, reach(parent.c.Scope)) {
		w.fail(t, p, "%s (%q) is broader than its lineage parent %s (%q)", k.name(), k.c.Scope, parent.name(), parent.c.Scope)
	}
}

// checkTenant checks the tenant of an issued token against the tokens it
// was issued from, and that only an impersonation crosses tenants.
//
// spec: §13.3 (Credential Flow) "Token rule properties." item 2
func checkTenant(t tb, w *world, k *token) {
	t.Helper()
	const p = "2 tenant"
	row := w.rows[k.c.JWTID]
	if row.tenant != k.c.TenantID {
		w.fail(t, p, "%s carries tenant %s and its row tenant %s", k.name(), k.c.TenantID, row.tenant)
	}
	if k.kind == kindImpersonation {
		c := k.call
		if !c.hasRole(auth.RolePlatformAdmin) || row.actSub != c.c.Subject ||
			row.impTenant != c.c.TenantID || row.impJTI != c.c.JWTID {
			w.fail(t, p, "impersonation bearer %s row %+v does not name its platform-admin caller %s", k.name(), row, c.name())
		}
		return
	}
	for _, src := range []*token{k.subject, k.actor, k.call} {
		if src != nil && src.c.TenantID != k.c.TenantID {
			w.fail(t, p, "%s in tenant %s was issued from %s in tenant %s", k.name(), k.c.TenantID, src.name(), src.c.TenantID)
		}
	}
	if k.c.Subject != k.subject.c.Subject {
		w.fail(t, p, "%s carries sub %s, its subject %s carries %s", k.name(), k.c.Subject, k.subject.name(), k.subject.c.Subject)
	}
}

// checkLineageRecorded checks the parent_jti and act_sub an issued token's
// row records, which the revocation reach of item 3 walks.
//
// spec: §13.3 (Credential Flow) "Token rule properties." item 3,
// "Lineage of derived tokens."
func checkLineageRecorded(t tb, w *world, k *token) {
	t.Helper()
	const p = "3 revocation reach (lineage)"
	row, ok := w.rows[k.c.JWTID]
	if !ok {
		w.fail(t, p, "%s has no issued_tokens row", k.name())
	}
	var parent, actSub string
	switch k.kind {
	case kindExchange, kindPlayground:
		parent = k.subject.c.JWTID
	case kindChild:
		parent, actSub = k.actor.c.JWTID, k.actor.c.Subject
	case kindImpersonation:
		actSub = k.call.c.Subject
	}
	if row.parent != parent || row.actSub != actSub {
		w.fail(t, p, "%s row records parent %q and act_sub %q, want %q and %q", k.name(), row.parent, row.actSub, parent, actSub)
	}
}

// checkIdentity checks that a token issued to another principal is
// strictly narrower than its subject, that a child's actor is its caller,
// and that no operation other than an impersonation gives the presenting
// user more of another identity than a token of it the user held.
//
// spec: §13.3 (Credential Flow) "Token rule properties." item 5
func checkIdentity(t tb, w *world, o op, k *token) {
	t.Helper()
	const p = "5 identity"
	switch {
	case k.kind == kindExchange && differentCaller(k.call, k.subject):
		if !strictlyNarrower(k.c.Scope, k.subject.c.Scope) {
			w.fail(t, p, "%s issued to caller %s (%s) is not strictly narrower than subject %s (%q vs %q)",
				k.name(), k.call.name(), k.call.c.Subject, k.subject.name(), k.c.Scope, k.subject.c.Scope)
		}
	case k.kind == kindChild:
		if k.actor.c.Subject != k.call.c.Subject {
			w.fail(t, p, "child %s has actor %s of %s and caller %s of %s",
				k.name(), k.actor.name(), k.actor.c.Subject, k.call.name(), k.call.c.Subject)
		}
		if k.subject.c.Subject != k.actor.c.Subject && !strictlyNarrower(k.c.Scope, k.subject.c.Scope) {
			w.fail(t, p, "child %s of another subject %s is not strictly narrower (%q vs %q)",
				k.name(), k.subject.name(), k.c.Scope, k.subject.c.Scope)
		}
	}
	if k.kind == kindImpersonation {
		return
	}
	u := w.users[o.user]
	if k.c.Subject == u.sub && k.c.TenantID == w.tenants[u.tenant] {
		return
	}
	for _, held := range w.held(o.user) {
		if held != k && held.c.Subject == k.c.Subject && held.c.TenantID == k.c.TenantID &&
			within(reach(k.c.Scope), reach(held.c.Scope)) {
			return
		}
	}
	w.fail(t, p, "%s gives %s a token of %s (%q) broader than any token of %s that %s held",
		k.name(), u.name, k.c.Subject, k.c.Scope, k.c.Subject, u.name)
}

// checkPlaygroundOrigin checks that no token was issued from a playground
// bearer.
//
// spec: §13.3 (Credential Flow) "Token rule properties." item 6
func checkPlaygroundOrigin(t tb, w *world, k *token) {
	t.Helper()
	for _, src := range []*token{k.subject, k.actor, k.call} {
		if src != nil && src.isPlayground() {
			w.fail(t, "6 playground origin", "%s was issued from playground bearer %s", k.name(), src.name())
		}
	}
}

// checkDepthAndAct checks delegation_depth and the act claim of an issued
// token against its actor, its subject, and its lineage parent.
//
// spec: §13.3 (Credential Flow) "Token rule properties." item 7
func checkDepthAndAct(t tb, w *world, k *token) {
	t.Helper()
	const p = "7 delegation depth and act"
	switch k.kind {
	case kindChild:
		a := k.actor
		want := jwt.ActorClaim{Subject: a.c.Subject, TenantID: a.c.TenantID, SessionID: a.c.SessionID, DelegationDepth: a.c.DelegationDepth}
		if k.c.DelegationDepth != a.c.DelegationDepth+1 || k.c.Act == nil || *k.c.Act != want {
			w.fail(t, p, "child %s has depth %d and act %+v, actor %s has depth %d and sub %s",
				k.name(), k.c.DelegationDepth, k.c.Act, a.name(), a.c.DelegationDepth, a.c.Subject)
		}
	case kindExchange:
		s := k.subject
		sameAct := (k.c.Act == nil) == (s.c.Act == nil) && (k.c.Act == nil || *k.c.Act == *s.c.Act)
		if k.c.DelegationDepth != s.c.DelegationDepth || !sameAct {
			w.fail(t, p, "%s has depth %d and act %+v, its subject %s has depth %d and act %+v",
				k.name(), k.c.DelegationDepth, k.c.Act, s.name(), s.c.DelegationDepth, s.c.Act)
		}
	}
	if parent := w.lineageParent(t, k); parent != nil && k.c.DelegationDepth < parent.c.DelegationDepth {
		w.fail(t, p, "%s has depth %d below its lineage parent %s at %d", k.name(), k.c.DelegationDepth, parent.name(), parent.c.DelegationDepth)
	}
	if k.c.DelegationDepth > 0 && k.c.Act == nil {
		w.fail(t, p, "%s has depth %d and no act claim", k.name(), k.c.DelegationDepth)
	}
}

// checkRevokedSources checks that no mint accepted a token that was
// revoked before the step as its caller, subject, or actor.
//
// spec: §13.3 (Credential Flow) "Token rule properties." item 8
func checkRevokedSources(t tb, w *world, k *token, before map[string]rowState) {
	t.Helper()
	for _, src := range []*token{k.subject, k.actor, k.call} {
		if src != nil && w.revoked(before, src) {
			w.fail(t, "8 revoked tokens", "%s was issued from %s, which was revoked", k.name(), src.name())
		}
	}
}

// checkLifetime checks that an issued token expires no later than the
// tokens it was issued from and its lineage parent.
//
// spec: §13.3 (Credential Flow) "Token rule properties." item 9
func checkLifetime(t tb, w *world, k *token) {
	t.Helper()
	bounds := []*token{k.subject, k.actor, w.lineageParent(t, k)}
	if k.kind == kindImpersonation {
		bounds = append(bounds, k.call)
	}
	for _, b := range bounds {
		if b != nil && k.c.Expiry > b.c.Expiry {
			w.fail(t, "9 lifetime", "%s expires at %d, after %s at %d", k.name(), k.c.Expiry, b.name(), b.c.Expiry)
		}
	}
}

// descendants returns the jti values of roots and of every token derived
// from them by the derivations the lineage records: an exchange from its
// subject, a child from its actor, and a playground bearer from its
// subject, each within the parent's tenant. With impersonations set it
// adds the impersonation bearers whose caller is in that set.
func (w *world) descendants(roots []*token, impersonations bool) map[string]bool {
	in := map[string]bool{}
	for _, r := range roots {
		in[r.c.JWTID] = true
	}
	for changed := true; changed; {
		changed = false
		for _, k := range w.tokens {
			if in[k.c.JWTID] {
				continue
			}
			var parent *token
			switch k.kind {
			case kindExchange, kindPlayground:
				parent = k.subject
			case kindChild:
				parent = k.actor
			}
			if parent != nil && in[parent.c.JWTID] && parent.c.TenantID == k.c.TenantID {
				in[k.c.JWTID], changed = true, true
			}
		}
	}
	if impersonations {
		for _, k := range w.tokens {
			if k.kind == kindImpersonation && in[k.call.c.JWTID] {
				in[k.c.JWTID] = true
			}
		}
	}
	return in
}

// revocationEligible reports whether a recursive-revocation request meets
// every condition under which the rules accept it: a live caller that is
// not a playground bearer, in the subject's tenant, carrying the
// subject's sub or an administrator role.
func (w *world) revocationEligible(c, s *token, before map[string]rowState) bool {
	return !c.isPlayground() && !w.revoked(before, c) && c.c.TenantID == s.c.TenantID &&
		(c.c.Subject == s.c.Subject || c.hasRole(auth.RoleTenantAdmin, auth.RolePlatformAdmin))
}

// checkRevocationReach checks what each step revoked: a revocation
// request its subject's lineage and the impersonations started from it,
// a rotation the token it replaces, and the other operations nothing.
//
// spec: §13.3 (Credential Flow) "Token rule properties." item 3
func checkRevocationReach(t tb, w *world, o op, out outcome, before map[string]rowState) {
	t.Helper()
	const p = "3 revocation reach"
	newly := newlyRevoked(before, w.rows)
	switch {
	case o.kind == opFullRevoke:
		return
	case o.kind == opRevoke && out.status == http.StatusOK:
		root := w.tok(o.subject)
		reached := w.descendants([]*token{root}, true)
		for jti := range reached {
			if r, ok := w.rows[jti]; !ok || !r.revoked {
				w.fail(t, p, "revoking %s left %s live (row %t)", root.name(), w.label(jti), ok)
			}
		}
		for jti := range newly {
			if !reached[jti] {
				w.fail(t, p, "revoking %s revoked %s outside its lineage", root.name(), w.label(jti))
			}
		}
	case o.kind == opRevoke:
		c, s := w.tok(o.caller), w.tok(o.subject)
		if w.revocationEligible(c, s, before) {
			w.fail(t, p, "revoking %s with caller %s was refused (%d %s)", s.name(), c.name(), out.status, out.reason)
		}
		if len(newly) > 0 {
			w.fail(t, p, "a refused revocation revoked %v", sortedKeys(newly))
		}
	case out.issued != nil && out.issued.rotation:
		s := out.issued.subject
		for jti := range newly {
			if jti != s.c.JWTID {
				w.fail(t, p, "rotation %s of %s also revoked %s", out.issued.name(), s.name(), w.label(jti))
			}
		}
		if r, ok := before[s.c.JWTID]; ok && !r.revoked && !w.rows[s.c.JWTID].revoked {
			w.fail(t, p, "rotation %s left the replaced token %s live", out.issued.name(), s.name())
		}
	default:
		if len(newly) > 0 {
			w.fail(t, p, "%s revoked %v", o.kind, sortedKeys(newly))
		}
	}
}

// checkWhoMayRevoke checks that a revocation request revoked tokens only
// for a live caller of the subject's identity or with an administrator
// role, and only from a subject the presenting user held.
//
// spec: §13.3 (Credential Flow) "Token rule properties." item 4
func checkWhoMayRevoke(t tb, w *world, o op, out outcome, before map[string]rowState) {
	t.Helper()
	if o.kind != opRevoke || len(newlyRevoked(before, w.rows)) == 0 {
		return
	}
	c, s := w.tok(o.caller), w.tok(o.subject)
	if !w.revocationEligible(c, s, before) || !s.holders[o.user] || !c.holders[o.user] {
		w.fail(t, "4 who may revoke", "caller %s (sub %s, roles %v, revoked %t) revoked from %s (sub %s)",
			c.name(), c.c.Subject, c.c.Roles, w.revoked(before, c), s.name(), s.c.Subject)
	}
}

// checkFullRevoke checks that, in the caller's tenant, every recorded
// token of the user, every recorded token whose act claim is the user,
// and every recorded descendant of a token of the user is revoked after a
// full revoke, and that a platform-admin caller also ends the
// impersonation sessions the user started.
//
// spec: §13.3 (Credential Flow) "Token rule properties." item 10, §11.4
// (User Invalidation) step 5
func checkFullRevoke(t tb, w *world, o op, out outcome, before map[string]rowState) {
	t.Helper()
	const p = "10 full revoke"
	if o.kind != opFullRevoke || out.status != http.StatusOK {
		return
	}
	c, x := w.tok(o.caller), w.users[o.other].sub
	var seeds []*token
	for _, k := range w.tokens {
		if k.c.TenantID == c.c.TenantID && k.c.Subject == x {
			seeds = append(seeds, k)
		}
	}
	reached := w.descendants(seeds, false)
	platform := c.hasRole(auth.RolePlatformAdmin)
	for jti, r := range w.rows {
		k := w.byJTI[jti]
		// An impersonation row is a row with act_sub set and no parent.
		started := r.actSub == x && r.parent == ""
		if r.revoked {
			continue
		}
		switch {
		case r.tenant == c.c.TenantID && (r.sub == x || reached[jti]):
			w.fail(t, p, "full revoke of %s left %s live (sub %s)", x, w.label(jti), r.sub)
		case r.tenant == c.c.TenantID && k != nil && k.c.Act != nil && k.c.Act.Subject == x:
			w.fail(t, p, "full revoke of %s left %s live, whose act claim names the user", x, w.label(jti))
		case platform && started:
			w.fail(t, p, "full revoke of %s by a platform-admin left impersonation bearer %s live", x, w.label(jti))
		}
	}
	for jti := range newlyRevoked(before, w.rows) {
		r := w.rows[jti]
		inLineage := r.tenant == c.c.TenantID && reached[jti]
		if !inLineage && (!platform || r.actSub != x || r.parent != "") {
			w.fail(t, p, "full revoke of %s revoked %s (sub %s) outside the user's tokens and their lineage", x, w.label(jti), r.sub)
		}
	}
}
