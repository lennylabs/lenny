//go:build component

// SPDX-License-Identifier: MIT

package tokenproperties_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
)

// Users of every world: alice and bob in acme, carol in globex.
const (
	alice = 0
	bob   = 1
	carol = 2
)

// signIn is a recorded human sign-in of u with scope and roles, valid for
// two hours, carrying the gateway audience.
func signIn(u int, scope string, roles ...auth.Role) op {
	return op{
		kind: opSignIn, user: u, caller: -1, subject: -1, actor: -1, other: -1,
		scope: scope, roles: roles, callerType: "human", tokenAud: []string{defaultAudience},
		expMinutes: 120, recorded: true,
	}
}

// rowless returns the sign-in without an issued_tokens row.
func (o op) rowless() op { o.recorded = false; return o }

// as returns the sign-in with another caller_type.
func (o op) as(callerType string) op { o.callerType = callerType; return o }

// lasting returns the sign-in with another lifetime in minutes.
func (o op) lasting(minutes int) op { o.expMinutes = minutes; return o }

// exchange is an exchange by user u with caller token c and subject token
// s; a scope argument names the requested scope, and none leaves it
// absent.
func exchange(u, c, s int, scope ...string) op {
	o := op{kind: opExchange, user: u, caller: c, subject: s, actor: -1, other: -1}
	if len(scope) > 0 {
		o.scopeSet, o.scope = true, scope[0]
	}
	return o
}

// child is a child mint by user u with caller c, subject s, and actor a.
func child(u, c, s, a int, scope ...string) op {
	o := exchange(u, c, s, scope...)
	o.kind, o.actor = opChild, a
	return o
}

func revoke(u, c, s int) op {
	return op{kind: opRevoke, user: u, caller: c, subject: s, actor: -1, other: -1}
}

func impersonate(u, c, target, minutes int) op {
	return op{kind: opImpersonate, user: u, caller: c, subject: -1, actor: -1, other: target, impersonationMinutes: minutes}
}

func playgroundOf(u, s int) op {
	return op{kind: opPlayground, user: u, caller: -1, subject: s, actor: -1, other: -1}
}

func fullRevoke(u, c, target int) op {
	return op{kind: opFullRevoke, user: u, caller: c, subject: -1, actor: -1, other: target}
}

func leak(u, tok, to int) op {
	return op{kind: opLeak, user: u, caller: -1, subject: tok, actor: -1, other: to}
}

// expect fails unless out has the status and its reason contains reason.
func expect(t *testing.T, w *world, out outcome, status int, reason string) {
	t.Helper()
	if out.status != status || !strings.Contains(out.reason, reason) {
		t.Fatalf("step %d: %d %q, want %d %q\nsequence:\n%s", len(w.log), out.status, out.reason, status, reason, w.sequence())
	}
}

// expectRevoked fails unless token i's revocation state is want.
func expectRevoked(t *testing.T, w *world, i int, want bool) {
	t.Helper()
	if got := w.revoked(w.rows, w.tok(i)); got != want {
		t.Fatalf("%s revoked = %t, want %t\nsequence:\n%s", w.tok(i).name(), got, want, w.sequence())
	}
}

// spec: §13.3 (Credential Flow) "Token rule properties.", "Lineage of
// derived tokens.", "Rotation lineage and leaked tokens.", §10.2
// (Authentication) "Playground mint invariants" invariant 6, §11.4 (User
// Invalidation) step 5
// diagnosis: each subtest replays one counterexample change 0165's Alloy
// model found against the rules before change 0167 (named in its comment),
// with every property checked after every step. A failure in a subtest
// whose outcome the rules now refuse means the fix for that counterexample
// regressed; a failure in a subtest marked as an accepted outcome means the
// rule the owner accepted changed without a decision.
func TestTokenRuleScenariosOfTheModelKeepTheProperties(t *testing.T) {
	h := newHarness(t)
	run := func(name string, body func(t *testing.T, w *world)) {
		t.Run(name, func(t *testing.T) {
			body(t, newWorld(t, h, map[string]int{}))
		})
	}

	// CX-1: a narrowed token rotated by its holder stays under its
	// ancestor, so revoking the ancestor reaches the successor.
	run("a rotated derived token stays in its ancestor's lineage", func(t *testing.T, w *world) {
		w.step(t, signIn(alice, ""))
		w.step(t, exchange(alice, 0, 0, "tools:me:read"))
		expect(t, w, w.step(t, exchange(alice, 1, 1)), http.StatusOK, "")
		expect(t, w, w.step(t, revoke(alice, 0, 0)), http.StatusOK, "")
		expectRevoked(t, w, 2, true)
	})

	// CX-2, question (i), accepted (0164 decision 10): a holder of a token
	// alice rotated away from and of a live token of hers revokes her
	// current token through the rotation link.
	run("a holder of two leaked tokens revokes the owner's successor", func(t *testing.T, w *world) {
		w.step(t, signIn(alice, ""))
		w.step(t, signIn(alice, ""))
		w.step(t, leak(alice, 1, bob))
		expect(t, w, w.step(t, exchange(alice, 0, 0)), http.StatusOK, "")
		w.step(t, leak(alice, 0, bob))
		expect(t, w, w.step(t, revoke(bob, 1, 0)), http.StatusOK, "")
		expectRevoked(t, w, 2, true)
	})

	// CX-2, question (ii): a holder who rotates a leaked token loses the
	// successor when its owner revokes the leaked token.
	run("a holder's rotated successor dies with the leaked token", func(t *testing.T, w *world) {
		w.step(t, signIn(alice, ""))
		w.step(t, signIn(alice, ""))
		w.step(t, leak(alice, 0, bob))
		expect(t, w, w.step(t, exchange(bob, 0, 0)), http.StatusOK, "")
		expect(t, w, w.step(t, revoke(alice, 1, 0)), http.StatusOK, "")
		expectRevoked(t, w, 2, true)
	})

	// CX-3: revoking the pasted token revokes the playground bearer.
	run("a playground bearer dies with its pasted subject", func(t *testing.T, w *world) {
		w.step(t, signIn(alice, ""))
		expect(t, w, w.step(t, playgroundOf(alice, 0)), http.StatusOK, "")
		expect(t, w, w.step(t, revoke(alice, 0, 0)), http.StatusOK, "")
		expectRevoked(t, w, 1, true)
	})

	// CX-4: the impersonation bearer expires with the admin's bearer and
	// dies with it.
	run("an impersonation bearer is bounded by and dies with the admin's bearer", func(t *testing.T, w *world) {
		w.step(t, signIn(carol, "", auth.RolePlatformAdmin).lasting(30))
		expect(t, w, w.step(t, impersonate(carol, 0, alice, 60)), http.StatusOK, "")
		expect(t, w, w.step(t, revoke(carol, 0, 0)), http.StatusOK, "")
		expectRevoked(t, w, 1, true)
	})

	// CX-5: a token without a row, such as an upstream ID token, is a
	// revocation root, and the cascade reaches what was derived from it.
	run("a token without a row can be revoked with its descendants", func(t *testing.T, w *world) {
		w.step(t, signIn(alice, "").rowless())
		w.step(t, exchange(alice, 0, 0, "tools:me:read"))
		expect(t, w, w.step(t, revoke(alice, 0, 0)), http.StatusOK, "")
		expectRevoked(t, w, 0, true)
		expectRevoked(t, w, 1, true)
	})

	// CX-6: a child broader than its actor is refused.
	run("a child broader than its actor is refused", func(t *testing.T, w *world) {
		w.step(t, signIn(alice, ""))
		w.step(t, exchange(alice, 0, 0, "tools:me:read"))
		expect(t, w, w.step(t, child(alice, 1, 0, 1, "tools:sessions:scale")), http.StatusBadRequest, "scope_exceeds_actor")
	})

	// CX-7, accepted (0165 decision 7): a list of a domain's current
	// actions is strictly narrower than the domain's wildcard.
	run("a list of a domain's actions is narrower than its wildcard", func(t *testing.T, w *world) {
		w.step(t, signIn(alice, "tools:sessions:* tools:me:write"))
		w.step(t, signIn(alice, "").as("agent"))
		expect(t, w, w.step(t, exchange(alice, 1, 0,
			"tools:sessions:read tools:sessions:write tools:sessions:scale tools:me:write")), http.StatusOK, "")
	})

	// CX-8: a child's depth follows its actor, below its subject's.
	run("a child's depth follows its actor", func(t *testing.T, w *world) {
		w.step(t, signIn(alice, ""))
		expect(t, w, w.step(t, child(alice, 0, 0, 0)), http.StatusOK, "")
		expect(t, w, w.step(t, child(alice, 1, 1, 1)), http.StatusOK, "")
		expect(t, w, w.step(t, child(alice, 0, 2, 0)), http.StatusOK, "")
		if d := w.tok(3).c.DelegationDepth; d != 1 {
			t.Fatalf("child of a depth-2 subject with a depth-0 actor has depth %d, want 1", d)
		}
	})

	// CX-9: narrowing a delegated token keeps its act claim.
	run("a narrowed delegated token keeps its act claim", func(t *testing.T, w *world) {
		w.step(t, signIn(alice, ""))
		expect(t, w, w.step(t, child(alice, 0, 0, 0)), http.StatusOK, "")
		expect(t, w, w.step(t, exchange(alice, 1, 1, "tools:me:read")), http.StatusOK, "")
	})

	// CX-10: a token rotated away authenticates no mint.
	run("a revoked caller token authenticates no mint", func(t *testing.T, w *world) {
		w.step(t, signIn(alice, ""))
		expect(t, w, w.step(t, exchange(alice, 0, 0)), http.StatusOK, "")
		expect(t, w, w.step(t, exchange(alice, 0, 1, "tools:me:read")), http.StatusUnauthorized, "caller_token_revoked")
	})

	// CX-11: a full revoke of alice reaches the child bob minted with her
	// token as actor.
	run("a full revoke reaches the delegations the user made", func(t *testing.T, w *world) {
		w.step(t, signIn(bob, ""))
		w.step(t, signIn(alice, ""))
		w.step(t, leak(alice, 1, bob))
		expect(t, w, w.step(t, child(bob, 1, 0, 1, "tools:me:read")), http.StatusOK, "")
		w.step(t, signIn(bob, "", auth.RoleTenantAdmin))
		expect(t, w, w.step(t, fullRevoke(bob, 3, alice)), http.StatusOK, "")
		expectRevoked(t, w, 2, true)
	})

	// CX-12, accepted (0164 decision 8): a child records its actor as
	// parent, so revoking its subject leaves it live.
	run("a child does not die with its subject", func(t *testing.T, w *world) {
		w.step(t, signIn(alice, ""))
		w.step(t, signIn(alice, ""))
		expect(t, w, w.step(t, child(alice, 1, 0, 1)), http.StatusOK, "")
		expect(t, w, w.step(t, revoke(alice, 0, 0)), http.StatusOK, "")
		expectRevoked(t, w, 2, false)
	})

	// CX-13, accepted (0164 decision 11): a narrow token of alice's
	// revokes her unrestricted token.
	run("a narrow token of the user revokes the user's broader token", func(t *testing.T, w *world) {
		w.step(t, signIn(alice, ""))
		w.step(t, signIn(alice, "tools:me:read"))
		expect(t, w, w.step(t, revoke(alice, 1, 0)), http.StatusOK, "")
		expectRevoked(t, w, 0, true)
	})
}
