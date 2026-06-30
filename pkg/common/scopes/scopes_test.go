// SPDX-License-Identifier: MIT

package scopes_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/common/scopes"
)

// TestParseScope_Canonical covers the §25.1 happy path: a scope value
// in the tools:<domain>:<action> form lands as a Scope and round-trips
// through String.
func TestParseScope_Canonical(t *testing.T) {
	cases := []struct {
		in     string
		domain string
		action string
	}{
		{"tools:pool:scale", "pool", "scale"},
		{"tools:health:read", "health", "read"},
		{"tools:locks:*", "locks", "*"},
		{"tools:credential_pool:rotate", "credential_pool", "rotate"},
		{"tools:*:*", "*", "*"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := scopes.ParseScope(tc.in)
			if err != nil {
				t.Fatalf("ParseScope(%q) returned %v", tc.in, err)
			}
			if got.Domain != tc.domain || got.Action != tc.action {
				t.Errorf("ParseScope(%q) = %+v, want {%q %q}", tc.in, got, tc.domain, tc.action)
			}
			if got.String() != tc.in {
				t.Errorf("ParseScope(%q).String() = %q, want round-trip", tc.in, got.String())
			}
		})
	}
}

// taxonomyDomains is the closed §15.1 "Scope taxonomy" domain set
// (spec/15_external-api-surface.md §15.1, "Domains" bullet). The map in
// scopes.Domains MUST contain every one of these, so a renamed admin
// scope in tools:<domain>:<action> form parses through ParseScope and is
// not mis-denied by Set.Matches.
//
// spec:§15.1 closed scope taxonomy.
var taxonomyDomains = []string{
	"pool", "health", "diagnostics", "recommendations", "runbooks",
	"events", "audit", "drift", "backup", "restore", "upgrade", "locks",
	"escalation", "logs", "me", "operations", "tenant", "credential_pool",
	"credential", "runtime", "quota", "config", "circuit_breaker",
	"artifact_replication", "billing_correction", "bootstrap", "ca_rotation",
	"connector", "credential_rekey", "delegation_policy", "deployment",
	"environment", "erasure_job", "experiment", "external_adapter",
	"impersonation", "interceptor", "issued_token", "legal_hold", "platform",
	"preflight", "rbac", "schema", "sessions", "tree", "user",
}

// playgroundCeilingDomains are the §10.2 / §25.1 playground_allowed_scope
// ceiling domains that live in scopes.Domains but outside the §15.1 admin
// taxonomy bullet (pkg/gateway/playground/token.go uses tools:runtimes:read
// and tools:pools:read). They are the only legitimate map entries absent
// from taxonomyDomains.
//
// spec:§10.2 playground_allowed_scope ceiling.
var playgroundCeilingDomains = []string{"runtimes", "pools"}

// TestNewAdminDomains_Parse asserts every admin domain added by proposal
// 0019 C1 (finding 25.1-H1) parses through ParseScope in the canonical
// tools:<domain>:<action> form. Before the Domains-map expansion these
// 21 domains were absent, so ParseScope returned ErrInvalidScope and
// Set.Matches mis-denied every call to the matching admin endpoint
// family. This test fails against the pre-fix map.
//
// spec:§15.1 closed scope taxonomy; spec:§25.1 scope value syntax.
func TestNewAdminDomains_Parse(t *testing.T) {
	newDomains := []string{
		"artifact_replication", "billing_correction", "bootstrap",
		"ca_rotation", "connector", "credential_rekey", "delegation_policy",
		"deployment", "environment", "erasure_job", "external_adapter",
		"impersonation", "interceptor", "issued_token", "legal_hold",
		"platform", "preflight", "rbac", "schema", "tree", "user",
	}
	for _, d := range newDomains {
		t.Run(d, func(t *testing.T) {
			in := "tools:" + d + ":read"
			got, err := scopes.ParseScope(in)
			if err != nil {
				t.Fatalf("ParseScope(%q) returned %v, want the renamed admin scope to parse", in, err)
			}
			if got.Domain != d || got.Action != "read" {
				t.Errorf("ParseScope(%q) = %+v, want domain %q action read", in, got, d)
			}
			// The per-domain wildcard must parse too: x-lenny-scope values
			// use the * action for "all actions in the domain".
			if _, err := scopes.ParseScope("tools:" + d + ":*"); err != nil {
				t.Errorf("ParseScope(tools:%s:*) returned %v, want wildcard action to parse", d, err)
			}
		})
	}
}

// TestDomainsEqualsTaxonomyUnion asserts scopes.Domains equals the union
// of the §15.1 admin taxonomy and the §10.2 playground ceiling domains:
// every taxonomy domain is present, and the only entries outside the
// taxonomy are the two documented playground plurals. A taxonomy domain
// missing from the map would mis-deny that endpoint family; a stray map
// entry outside both sets would accept a scope the taxonomy never granted.
//
// spec:§15.1 closed scope taxonomy; spec:§10.2 playground ceiling.
func TestDomainsEqualsTaxonomyUnion(t *testing.T) {
	want := make(map[string]struct{}, len(taxonomyDomains)+len(playgroundCeilingDomains))
	for _, d := range taxonomyDomains {
		want[d] = struct{}{}
	}
	for _, d := range playgroundCeilingDomains {
		want[d] = struct{}{}
	}
	got := scopes.Domains

	for d := range want {
		if _, ok := got[d]; !ok {
			t.Errorf("scopes.Domains is missing taxonomy/ceiling domain %q", d)
		}
	}
	for d := range got {
		if _, ok := want[d]; !ok {
			t.Errorf("scopes.Domains carries %q, which is in neither the §15.1 taxonomy nor the §10.2 playground ceiling", d)
		}
	}
	if len(got) != len(want) {
		t.Errorf("len(scopes.Domains)=%d, want %d (taxonomy union with playground ceiling)", len(got), len(want))
	}
}

// TestParseScope_Malformed enumerates the §25.1 malformed-value
// rejections: empty, missing prefix, wrong prefix, unknown domain,
// wildcard in the domain slot without wildcard action, surrounding
// whitespace, and wrong segment count.
func TestParseScope_Malformed(t *testing.T) {
	cases := []string{
		"",
		"tools",
		"tools:pool",
		"tools:pool:scale:extra",
		"admin:pool:scale",   // wrong prefix
		"tools::scale",       // empty domain
		"tools:pool:",        // empty action
		"tools:notadomain:*", // domain not in taxonomy
		"tools:*:read",       // wildcard domain requires wildcard action
		" tools:pool:scale",  // leading whitespace
		"tools:pool:scale ",  // trailing whitespace
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			_, err := scopes.ParseScope(in)
			if !errors.Is(err, scopes.ErrInvalidScope) {
				t.Errorf("ParseScope(%q) err = %v, want ErrInvalidScope", in, err)
			}
		})
	}
}

// TestParse_Absent covers the §25.1 absent-claim semantics: an empty
// or whitespace-only claim parses as a Set that reports !Present and
// matches every required scope.
func TestParse_Absent(t *testing.T) {
	for _, in := range []string{"", "   ", "\t\n"} {
		set, err := scopes.Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q) returned %v", in, err)
		}
		if set.Present() {
			t.Errorf("Parse(%q).Present() = true, want false for absent claim", in)
		}
		if !set.Matches("tools:pool:scale") {
			t.Errorf("absent claim should match every required scope (spec §25.1)")
		}
	}
}

// TestParse_MultiValue confirms a §25.1 space-separated claim parses
// every value, preserves order, and de-duplicates.
func TestParse_MultiValue(t *testing.T) {
	set, err := scopes.Parse("tools:health:read tools:pool:* tools:health:read")
	if err != nil {
		t.Fatalf("Parse returned %v", err)
	}
	if !set.Present() {
		t.Fatalf("Present() = false, want true after non-empty parse")
	}
	got := set.Scopes()
	if len(got) != 2 {
		t.Fatalf("Scopes()=%v, want 2 unique entries (de-dup)", got)
	}
	if got[0].String() != "tools:health:read" || got[1].String() != "tools:pool:*" {
		t.Errorf("Scopes order = %v, want preserved insertion order", got)
	}
}

// TestParse_RejectsMalformedValue ensures a single bad token fails the
// whole claim, so the §25.1 enforcement boundary is deterministic.
func TestParse_RejectsMalformedValue(t *testing.T) {
	_, err := scopes.Parse("tools:health:read garbage tools:pool:*")
	if !errors.Is(err, scopes.ErrInvalidScope) {
		t.Errorf("Parse with one malformed token err = %v, want ErrInvalidScope", err)
	}
}

// TestParse_TabSeparator confirms the §25.1 claim accepts any Unicode
// whitespace as the separator, mirroring strings.Fields.
func TestParse_TabSeparator(t *testing.T) {
	set, err := scopes.Parse("tools:health:read\ttools:pool:*")
	if err != nil {
		t.Fatalf("Parse returned %v", err)
	}
	if len(set.Scopes()) != 2 {
		t.Errorf("Scopes()=%v, want both values across tab separator", set.Scopes())
	}
}

// TestMatches covers the §25.1 matching table: exact domain+action,
// per-domain wildcard, the global tools:* wildcard, and a miss.
func TestMatches(t *testing.T) {
	cases := []struct {
		name    string
		claim   string
		req     string
		matches bool
	}{
		{"exact match", "tools:pool:scale", "tools:pool:scale", true},
		{"action mismatch", "tools:pool:scale", "tools:pool:read", false},
		{"domain mismatch", "tools:pool:scale", "tools:health:scale", false},
		{"per-domain wildcard", "tools:pool:*", "tools:pool:scale", true},
		{"per-domain wildcard miss", "tools:pool:*", "tools:health:read", false},
		{"global wildcard", "tools:*:*", "tools:pool:scale", true},
		{"multi-claim hits second", "tools:health:read tools:pool:scale", "tools:pool:scale", true},
		{"multi-claim miss", "tools:health:read tools:locks:write", "tools:pool:scale", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set, err := scopes.Parse(tc.claim)
			if err != nil {
				t.Fatalf("Parse(%q) returned %v", tc.claim, err)
			}
			if got := set.Matches(tc.req); got != tc.matches {
				t.Errorf("Matches(%q) on claim %q = %v, want %v",
					tc.req, tc.claim, got, tc.matches)
			}
		})
	}
}

// TestMatches_MalformedRequired guards the §25.1 "required" side: a
// caller that passes a malformed value cannot pass the check, even
// with a wildcard claim.
func TestMatches_MalformedRequired(t *testing.T) {
	set, _ := scopes.Parse("tools:*:*")
	if set.Matches("garbage") {
		t.Errorf("Matches(garbage) on wildcard claim = true, want false")
	}
}

// TestRawPreservedForResponse confirms Set.Raw round-trips the
// caller's claim text verbatim so §25.12 SCOPE_FORBIDDEN can echo
// activeScope as the caller sent it.
func TestRawPreservedForResponse(t *testing.T) {
	set, _ := scopes.Parse("  tools:pool:scale  tools:health:read ")
	if !strings.Contains(set.Raw, "tools:pool:scale") {
		t.Errorf("Raw=%q does not preserve the claim text", set.Raw)
	}
}

// TestIntersect covers the §10.2 playground-mint narrowing: the
// intersection of two scope sets matches what both permit and never
// elevates above the narrower side.
func TestIntersect(t *testing.T) {
	subject, _ := scopes.Parse("tools:pool:* tools:health:read tools:audit:read")
	allowed, _ := scopes.Parse("tools:pool:scale tools:health:read tools:operations:read")
	got := subject.Intersect(allowed)
	if !got.Present() {
		t.Fatalf("intersection.Present()=false, want overlap present")
	}
	if !got.Matches("tools:pool:scale") {
		t.Errorf("intersection should retain tools:pool:scale (lies in both)")
	}
	if got.Matches("tools:pool:read") {
		t.Errorf("intersection must not elevate to tools:pool:read (only allowed has the wildcard)")
	}
	if got.Matches("tools:operations:read") {
		t.Errorf("intersection must not include tools:operations:read (subject lacks it)")
	}
	if got.Matches("tools:audit:read") {
		t.Errorf("intersection must not include tools:audit:read (allowed lacks it)")
	}
}

// TestIntersect_OneAbsent confirms the absent-claim Set is the
// identity of Intersect: §25.1 absent-claim leaves the other side
// untouched.
func TestIntersect_OneAbsent(t *testing.T) {
	absent, _ := scopes.Parse("")
	present, _ := scopes.Parse("tools:pool:scale")
	if got := absent.Intersect(present); !got.Matches("tools:pool:scale") {
		t.Errorf("absent.Intersect(present) lost tools:pool:scale")
	}
	if got := present.Intersect(absent); !got.Matches("tools:pool:scale") {
		t.Errorf("present.Intersect(absent) lost tools:pool:scale")
	}
}

// TestIntersect_EmptyOverlap covers the §10.2 mint-with-no-overlap
// case: the intersection must report Present()==true with no matching
// scope so the minted token does NOT silently revert to the absent-
// claim semantics.
func TestIntersect_EmptyOverlap(t *testing.T) {
	a, _ := scopes.Parse("tools:pool:scale")
	b, _ := scopes.Parse("tools:health:read")
	got := a.Intersect(b)
	if !got.Present() {
		t.Errorf("empty overlap must remain Present (spec §10.2: never the union)")
	}
	if got.Matches("tools:pool:scale") || got.Matches("tools:health:read") {
		t.Errorf("empty overlap must not match either operand")
	}
}
