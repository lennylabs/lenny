// SPDX-License-Identifier: MIT

package credential

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

const testCredNS = "lenny-credentials"

// spec: §4.9 (Credential Storage Topology)
func TestParseSecretRefAcceptsBareAndQualifiedForms(t *testing.T) {
	cases := []struct {
		name string
		ref  string
		want string
	}{
		{"bare name", "anthropic-key", "anthropic-key"},
		{"qualified in credentials namespace", testCredNS + "/anthropic-key", "anthropic-key"},
		{"dotted subdomain name", "team.anthropic-key", "team.anthropic-key"},
		{"reserved prefix parses; the stored-entry check is the caller's", ManagedSecretName("acme", "p", "c"), ManagedSecretName("acme", "p", "c")},
		{"qualified reserved prefix", testCredNS + "/lenny-cred-abc", "lenny-cred-abc"},
		{"maximum length name", strings.Repeat("a", 253), strings.Repeat("a", 253)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSecretRef(tc.ref, testCredNS)
			if err != nil {
				t.Fatalf("ParseSecretRef(%q) error = %v, want nil", tc.ref, err)
			}
			if got != tc.want {
				t.Fatalf("ParseSecretRef(%q) = %q, want %q", tc.ref, got, tc.want)
			}
		})
	}
}

// spec: §4.9 (Credential Storage Topology)
func TestParseSecretRefRefusesOtherForms(t *testing.T) {
	cases := []struct {
		name   string
		ref    string
		credNS string
	}{
		{"empty ref", "", testCredNS},
		{"foreign namespace", "lenny-system/anthropic-key", testCredNS},
		{"agent namespace", "lenny-agents/anthropic-key", testCredNS},
		{"three segments", testCredNS + "/anthropic-key/apiKey", testCredNS},
		{"empty namespace segment", "/anthropic-key", testCredNS},
		{"empty name segment", testCredNS + "/", testCredNS},
		{"lone slash", "/", testCredNS},
		{"uppercase name", "Anthropic-Key", testCredNS},
		{"underscore in name", "anthropic_key", testCredNS},
		{"leading hyphen", "-anthropic", testCredNS},
		{"trailing dot", "anthropic.", testCredNS},
		{"whitespace", " anthropic-key", testCredNS},
		{"over 253 characters", strings.Repeat("a", 254), testCredNS},
		{"namespace prefix differs by case", "Lenny-Credentials/anthropic-key", testCredNS},
		{"unconfigured credentials namespace refuses bare", "anthropic-key", ""},
		{"unconfigured credentials namespace refuses qualified", "/anthropic-key", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSecretRef(tc.ref, tc.credNS)
			if !errors.Is(err, ErrInvalidSecretRef) {
				t.Fatalf("ParseSecretRef(%q, %q) = (%q, %v), want ErrInvalidSecretRef", tc.ref, tc.credNS, got, err)
			}
			if got != "" {
				t.Fatalf("ParseSecretRef(%q) returned name %q alongside an error", tc.ref, got)
			}
		})
	}
}

// spec: §4.9 (Credential Storage Topology)
func TestCanonicalSecretRefRoundTrips(t *testing.T) {
	ref := CanonicalSecretRef(testCredNS, "anthropic-key")
	if ref != testCredNS+"/anthropic-key" {
		t.Fatalf("CanonicalSecretRef = %q", ref)
	}
	name, err := ParseSecretRef(ref, testCredNS)
	if err != nil || name != "anthropic-key" {
		t.Fatalf("ParseSecretRef(canonical) = (%q, %v)", name, err)
	}
}

// FuzzParseSecretRefCanonicalIdempotent asserts that a ref the parser
// accepts canonicalizes to a fixed point and never resolves to a Secret in
// another namespace.
//
// spec: §4.9 (Credential Storage Topology)
func FuzzParseSecretRefCanonicalIdempotent(f *testing.F) {
	for _, seed := range []string{
		"anthropic-key", testCredNS + "/anthropic-key", "lenny-system/x",
		"a/b/c", "/", "", "lenny-cred-0123", testCredNS + "/" + testCredNS,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, ref string) {
		name, err := ParseSecretRef(ref, testCredNS)
		if err != nil {
			if !errors.Is(err, ErrInvalidSecretRef) {
				t.Fatalf("ParseSecretRef(%q) error %v does not wrap ErrInvalidSecretRef", ref, err)
			}
			return
		}
		if strings.Contains(name, "/") {
			t.Fatalf("ParseSecretRef(%q) = %q, a name carrying a namespace", ref, name)
		}
		if ns, _, ok := strings.Cut(ref, "/"); ok && ns != testCredNS {
			t.Fatalf("ParseSecretRef(%q) accepted foreign namespace %q", ref, ns)
		}
		canonical := CanonicalSecretRef(testCredNS, name)
		again, err := ParseSecretRef(canonical, testCredNS)
		if err != nil {
			t.Fatalf("ParseSecretRef(canonical %q) error = %v", canonical, err)
		}
		if again != name || CanonicalSecretRef(testCredNS, again) != canonical {
			t.Fatalf("canonical form of %q is not a fixed point: %q then %q", ref, name, again)
		}
	})
}

// spec: §4.9 (Credential Storage Topology)
func TestManagedSecretNameIsDeterministicAndDistinct(t *testing.T) {
	base := ManagedSecretName("acme", "anthropic", "cred-1")
	if ManagedSecretName("acme", "anthropic", "cred-1") != base {
		t.Fatal("ManagedSecretName is not deterministic")
	}
	if !IsManaged(base) {
		t.Fatalf("ManagedSecretName %q lacks the reserved prefix", base)
	}
	if got := len(base); got != len(ManagedSecretPrefix)+40 {
		t.Fatalf("ManagedSecretName length = %d, want %d", got, len(ManagedSecretPrefix)+40)
	}
	if _, err := ParseSecretRef(base, testCredNS); err != nil {
		t.Fatalf("ManagedSecretName %q is not a valid Secret name: %v", base, err)
	}
	variants := map[string]string{
		"tenant differs":  ManagedSecretName("globex", "anthropic", "cred-1"),
		"pool differs":    ManagedSecretName("acme", "bedrock", "cred-1"),
		"cred id differs": ManagedSecretName("acme", "anthropic", "cred-2"),
	}
	for label, v := range variants {
		if v == base {
			t.Fatalf("%s: ManagedSecretName collided with the base tuple", label)
		}
	}
}

// spec: §4.9 (Credential Storage Topology)
func TestManagedSecretNameSeparatorNonCollision(t *testing.T) {
	pairs := [][2][3]string{
		{{"a", "b-c", "x"}, {"a-b", "c", "x"}},
		{{"a", "bc", "x"}, {"ab", "c", "x"}},
		{{"a", "b", "cx"}, {"a", "bc", "x"}},
		{{"a/b", "c", "x"}, {"a", "b/c", "x"}},
		{{"", "ab", "x"}, {"a", "b", "x"}},
	}
	for _, p := range pairs {
		l := ManagedSecretName(p[0][0], p[0][1], p[0][2])
		r := ManagedSecretName(p[1][0], p[1][1], p[1][2])
		if l == r {
			t.Fatalf("ManagedSecretName%v and ManagedSecretName%v collided: %q", p[0], p[1], l)
		}
	}
}

// spec: §4.9 (Credential Storage Topology), §12.8 (Compliance Interfaces)
func TestManagedSecretHashesMatchSHA256(t *testing.T) {
	nameSum := sha256.Sum256([]byte("acme\x00anthropic\x00cred-1"))
	wantName := "lenny-cred-" + hex.EncodeToString(nameSum[:])[:40]
	if got := ManagedSecretName("acme", "anthropic", "cred-1"); got != wantName {
		t.Fatalf("ManagedSecretName = %q, want %q", got, wantName)
	}
	tenantSum := sha256.Sum256([]byte("acme"))
	wantHash := hex.EncodeToString(tenantSum[:])[:40]
	if got := ManagedSecretTenantHash("acme"); got != wantHash {
		t.Fatalf("ManagedSecretTenantHash = %q, want %q", got, wantHash)
	}
	// The label value must fit the 63-character label limit for a
	// 128-character tenant id.
	if got := ManagedSecretTenantHash(strings.Repeat("t", 128)); len(got) != 40 {
		t.Fatalf("ManagedSecretTenantHash length = %d, want 40", len(got))
	}
}

// spec: §4.9 (Credential Storage Topology)
func TestIsManagedMatchesReservedPrefixOnly(t *testing.T) {
	cases := map[string]bool{
		"lenny-cred-abc": true,
		"lenny-cred-":    true,
		"lenny-cred":     false,
		"x-lenny-cred-a": false,
		"anthropic-key":  false,
		"":               false,
	}
	for name, want := range cases {
		if got := IsManaged(name); got != want {
			t.Fatalf("IsManaged(%q) = %v, want %v", name, got, want)
		}
	}
}
