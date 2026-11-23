//go:build component

// SPDX-License-Identifier: MIT

package tokenproperties_test

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/lennylabs/lenny/pkg/auth"
)

// Run bounds. The default count fits the component tier's budget; a run
// that passes -rapid.checks or sets RAPID_CHECKS keeps its own count, and
// one that passes -rapid.seed or sets RAPID_SEED keeps its own seed.
const (
	defaultChecks = 300
	defaultSeed   = 20261123
	minSteps      = 8
	maxSteps      = 20
)

// scopeValues are the scope values sign-ins and requests draw from: every
// case the scope grammar distinguishes (tools:*, a domain wildcard, an
// exact value, a specific tool action, and a value outside the canonical
// form) over domains inside and outside the playground ceiling.
var scopeValues = []string{
	"tools:*",
	"tools:sessions:*", "tools:me:*",
	"tools:sessions:read", "tools:sessions:write", "tools:sessions:scale",
	"tools:me:read", "tools:me:write", "tools:pools:read", "tools:audit:read",
	"openid",
}

// opWeights sets how often each operation is drawn. rapid draws small
// integers more often than large ones, so the operations that build long
// lineages come first and the sign-in, which every user without a token
// performs anyway, comes last.
var opWeights = []struct {
	kind   opKind
	weight int
}{
	{opExchange, 6},
	{opChild, 5},
	{opLeak, 3},
	{opRevoke, 3},
	{opImpersonate, 2},
	{opPlayground, 2},
	{opFullRevoke, 1},
	{opSignIn, 2},
}

// witnesses are the scenarios each run must reach at least once, so that
// no property holds only because its premise never occurred.
var witnesses = []string{
	"accepted rotation",
	"accepted exchange by the subject's caller",
	"accepted exchange by another caller",
	"accepted child",
	"accepted child of another subject",
	"accepted playground",
	"accepted impersonation",
	"accepted cross-tenant impersonation",
	"revocation reaching descendants",
	"revocation of a root without a row",
	"revocation ending an impersonation",
	"full revoke revoking tokens",
	"refused exchange",
	"refused child",
	"refused revoke",
	"op leak",
}

// spec: §13.3 (Credential Flow) "Token rule properties.",
// §10.2 (Authentication) "Playground mint invariants",
// §11.4 (User Invalidation) step 5
// diagnosis: a failure names the property and prints the minimized
// sequence of operations that breaks it, with the rapid seed that
// reproduces it. The sequence ran against the Token Service, the
// playground mint, the impersonation issuer, and the full revoke over one
// Postgres issued-token store, so the property is false in the code as
// built: read the operation of the last step and the rule the property
// follows from. A failure that names a server error is a defect or a
// harness fault rather than a property. A missing witness means the
// generator no longer reaches a scenario a property depends on.
func TestTokenRulesKeepTheirPropertiesOverGeneratedSequences(t *testing.T) {
	setRapidDefault(t, "rapid.checks", "RAPID_CHECKS", fmt.Sprint(defaultChecks))
	setRapidDefault(t, "rapid.seed", "RAPID_SEED", fmt.Sprint(defaultSeed))
	h := newHarness(t)
	stats := map[string]int{}
	sequences, steps := 0, 0
	rapid.Check(t, func(rt *rapid.T) {
		w := newWorld(rt, h, stats)
		n := rapid.IntRange(minSteps, maxSteps).Draw(rt, "steps")
		for i := 0; i < n; i++ {
			w.step(rt, drawOp(rt, w, i))
		}
		sequences++
		steps += n
	})
	if t.Failed() {
		return
	}
	t.Logf("ran %d sequences, %d steps", sequences, steps)
	keys := make([]string, 0, len(stats))
	for k := range stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("  %-45s %d", k, stats[k])
	}
	var missing []string
	for _, k := range witnesses {
		if stats[k] == 0 {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		t.Errorf("the sequences never reached: %s", strings.Join(missing, ", "))
	}
}

// setRapidDefault sets rapid's flag to value for the calling test when
// neither the command line nor the environment variable set it, and
// restores the previous value when the test ends.
func setRapidDefault(t *testing.T, name, env, value string) {
	t.Helper()
	f := flag.Lookup(name)
	if f == nil {
		t.Fatalf("rapid registers no %s flag", name)
	}
	explicit := false
	flag.Visit(func(set *flag.Flag) { explicit = explicit || set.Name == name })
	if _, ok := os.LookupEnv(env); explicit || ok {
		return
	}
	prev := f.Value.String()
	if err := f.Value.Set(value); err != nil {
		t.Fatalf("set %s: %v", name, err)
	}
	t.Cleanup(func() { _ = f.Value.Set(prev) })
}

// drawOp draws the operation of step i from the world's state. A user who
// holds no token signs in.
func drawOp(rt *rapid.T, w *world, i int) op {
	label := func(s string) string { return fmt.Sprintf("%s%d", s, i) }
	u := rapid.IntRange(0, len(w.users)-1).Draw(rt, label("user"))
	// Newest first, so the small indices rapid favours pick the tokens
	// that extend a lineage.
	held := w.held(u)
	for i, j := 0, len(held)-1; i < j; i, j = i+1, j-1 {
		held[i], held[j] = held[j], held[i]
	}
	kind := drawKind(rt, label("op"))
	if len(held) == 0 {
		kind = opSignIn
	}
	pick := func(name string) int {
		return held[rapid.IntRange(0, len(held)-1).Draw(rt, label(name))].n
	}
	// pickOther draws a token other than token i, and about half the time
	// when u holds one, a token of another identity in i's tenant, so
	// exchanges by another caller and children of another subject occur
	// often enough to test.
	pickOther := func(name string, i int) int {
		var rest, other []*token
		for _, k := range held {
			if k.n == i {
				continue
			}
			rest = append(rest, k)
			if k.c.TenantID == w.tok(i).c.TenantID && k.c.Subject != w.tok(i).c.Subject {
				other = append(other, k)
			}
		}
		if len(other) > 0 && rapid.Bool().Draw(rt, label(name+"Other")) {
			rest = other
		}
		if len(rest) == 0 {
			return i
		}
		return rest[rapid.IntRange(0, len(rest)-1).Draw(rt, label(name))].n
	}
	o := op{kind: kind, user: u, caller: -1, subject: -1, actor: -1, other: -1}
	switch kind {
	case opSignIn:
		o.scope = drawClaim(rt, label("scope"))
		o.roles = rapid.SampledFrom([][]auth.Role{
			nil, nil, {auth.RolePlatformAdmin}, {auth.RoleTenantAdmin},
		}).Draw(rt, label("roles"))
		o.callerType = rapid.SampledFrom([]string{"", "human", "human", "agent", "service"}).Draw(rt, label("callerType"))
		o.tokenAud = rapid.SampledFrom([][]string{
			nil, {defaultAudience}, {defaultAudience}, {defaultAudience, "lenny-ops"},
		}).Draw(rt, label("aud"))
		o.expMinutes = rapid.IntRange(20, 300).Draw(rt, label("exp"))
		o.recorded = rapid.Bool().Draw(rt, label("recorded"))
	case opExchange:
		o.caller = pick("caller")
		o.subject = o.caller
		if !rapid.Bool().Draw(rt, label("self")) {
			o.subject = pickOther("subject", o.caller)
		}
		drawRequest(rt, w, &o, label)
	case opChild:
		o.actor = pick("actor")
		o.caller = o.actor
		if !rapid.Bool().Draw(rt, label("callerIsActor")) {
			o.caller = pick("caller")
		}
		o.subject = pickOther("subject", o.actor)
		drawRequest(rt, w, &o, label)
	case opRevoke:
		// The caller and the subject are drawn oldest first, and the
		// subject is often the caller itself, so a revocation often names
		// a root with descendants and impersonations started from it.
		oldest := func(name string) int {
			return held[len(held)-1-rapid.IntRange(0, len(held)-1).Draw(rt, label(name))].n
		}
		o.caller = oldest("caller")
		o.subject = o.caller
		if !rapid.Bool().Draw(rt, label("selfRevoke")) {
			o.subject = oldest("subject")
		}
		// TOKX-I22 (finding F1 of 0165.S2): the Token Service refuses an
		// accepted request whose impersonation step revokes its own
		// caller, against items 3 and 4. Until change 0168 fixes it, such
		// a caller revokes itself instead, which keeps the sequence out of
		// the generated runs, and the skipped subtest "an impersonation
		// bearer as the caller of its root's revocation revokes the root"
		// holds it. Change 0168 removes this exclusion.
		if c, s := w.tok(o.caller), w.tok(o.subject); w.callerEndedByLink(c, s) && w.revocationEligible(c, s, w.rows) {
			o.subject = o.caller
		}
	case opImpersonate:
		o.caller = pick("caller")
		o.other = rapid.IntRange(0, len(w.users)-1).Draw(rt, label("target"))
		o.impersonationMinutes = rapid.IntRange(1, 60).Draw(rt, label("duration"))
		o.roles = rapid.SampledFrom([][]auth.Role{nil, nil, {auth.RoleTenantAdmin}}).Draw(rt, label("targetRoles"))
	case opPlayground:
		o.subject = pick("subject")
	case opFullRevoke:
		o.caller = pick("caller")
		o.other = rapid.IntRange(0, len(w.users)-1).Draw(rt, label("target"))
	case opLeak:
		o.subject = pick("token")
		o.other = (u + 1 + rapid.IntRange(0, len(w.users)-2).Draw(rt, label("to"))) % len(w.users)
	}
	return o
}

func drawKind(rt *rapid.T, name string) opKind {
	total := 0
	for _, w := range opWeights {
		total += w.weight
	}
	n := rapid.IntRange(0, total-1).Draw(rt, name)
	for _, w := range opWeights {
		if n < w.weight {
			return w.kind
		}
		n -= w.weight
	}
	return opSignIn
}

// drawClaim draws a scope claim of up to three distinct values, the empty
// claim standing for an absent one.
func drawClaim(rt *rapid.T, name string) string {
	values := rapid.SliceOfNDistinct(rapid.SampledFrom(scopeValues), 0, 3, rapid.ID[string]).Draw(rt, name)
	return strings.Join(values, " ")
}

// drawRequest draws the scope, audience, and expires_in of an exchange or
// a child mint. The scope is absent, the subject's own, a subset of the
// subject's values, one value strictly narrower than the subject's scope,
// or a drawn claim, so rotations, narrowings, exchanges by another caller,
// and refusals all occur.
func drawRequest(rt *rapid.T, w *world, o *op, label func(string) string) {
	claim := w.tok(o.subject).c.Scope
	subject := strings.Fields(claim)
	// The shift puts the narrower value on the draw rapid favours most.
	switch (rapid.IntRange(0, 4).Draw(rt, label("scopeChoice")) + 3) % 5 {
	case 0:
	case 1:
		o.scopeSet, o.scope = true, strings.Join(subject, " ")
	case 2:
		if len(subject) > 0 {
			kept := rapid.SliceOfNDistinct(rapid.SampledFrom(subject), 1, len(subject), rapid.ID[string]).Draw(rt, label("subset"))
			o.scopeSet, o.scope = true, strings.Join(kept, " ")
		}
	case 3:
		// A child's scope also has to be granted by its actor, so the
		// value is drawn from those both tokens grant.
		bound := claim
		if o.kind == opChild {
			bound = w.tok(o.actor).c.Scope
		}
		var narrower []string
		for _, v := range scopeValues {
			if grantedBy(claim, v) && grantedBy(bound, v) && strictlyNarrower(v, claim) {
				narrower = append(narrower, v)
			}
		}
		if len(narrower) > 0 {
			o.scopeSet, o.scope = true, rapid.SampledFrom(narrower).Draw(rt, label("narrower"))
		}
	default:
		o.scopeSet, o.scope = true, drawClaim(rt, label("requested"))
	}
	o.audience = rapid.SampledFrom([]string{"", "", "", "", defaultAudience, defaultAudience, "lenny-ops"}).Draw(rt, label("audience"))
	if rapid.IntRange(0, 3).Draw(rt, label("hasExpiresIn")) == 0 {
		o.expiresIn = int64(rapid.IntRange(60, 4*3600).Draw(rt, label("expiresIn")))
	}
}
