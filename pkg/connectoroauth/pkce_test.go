// SPDX-License-Identifier: MIT

package connectoroauth

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateCodeVerifierLengthAndAlphabet(t *testing.T) {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	for i := 0; i < 64; i++ {
		v, err := GenerateCodeVerifier()
		if err != nil {
			t.Fatalf("GenerateCodeVerifier: %v", err)
		}
		// RFC 7636 section 4.1: 43–128 characters.
		if len(v) < 43 || len(v) > 128 {
			t.Fatalf("verifier length %d outside RFC 7636 43..128", len(v))
		}
		for _, r := range v {
			if !strings.ContainsRune(unreserved, r) {
				t.Fatalf("verifier contains non-unreserved character %q", r)
			}
		}
	}
}

func TestGenerateCodeVerifierIsUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 256; i++ {
		v, err := GenerateCodeVerifier()
		if err != nil {
			t.Fatalf("GenerateCodeVerifier: %v", err)
		}
		if _, dup := seen[v]; dup {
			t.Fatalf("GenerateCodeVerifier returned a duplicate verifier")
		}
		seen[v] = struct{}{}
	}
}

// TestCodeChallengeS256DerivesFromVerifier is the core PKCE property:
// the challenge must equal base64url(SHA-256(verifier)) without
// padding, exactly the RFC 7636 S256 transform.
func TestCodeChallengeS256DerivesFromVerifier(t *testing.T) {
	v, err := GenerateCodeVerifier()
	if err != nil {
		t.Fatalf("GenerateCodeVerifier: %v", err)
	}
	got, err := CodeChallengeS256(v)
	if err != nil {
		t.Fatalf("CodeChallengeS256: %v", err)
	}
	sum := sha256.Sum256([]byte(v))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if got != want {
		t.Fatalf("challenge = %q, want base64url(SHA-256(verifier)) = %q", got, want)
	}
	// The challenge must not be the verifier itself (the `plain`
	// method, which §9.3 forbids).
	if got == v {
		t.Fatalf("challenge equals verifier; S256 transform was not applied")
	}
}

// TestCodeChallengeS256RFC7636Vector pins the transform against the
// worked example in RFC 7636 appendix B.
func TestCodeChallengeS256RFC7636Vector(t *testing.T) {
	const (
		verifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
		challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	)
	got, err := CodeChallengeS256(verifier)
	if err != nil {
		t.Fatalf("CodeChallengeS256: %v", err)
	}
	if got != challenge {
		t.Fatalf("RFC 7636 appendix B vector: got %q, want %q", got, challenge)
	}
}

func TestCodeChallengeS256RejectsShortVerifier(t *testing.T) {
	for _, v := range []string{"", "too-short", strings.Repeat("a", 42)} {
		if _, err := CodeChallengeS256(v); err != ErrInvalidVerifier {
			t.Fatalf("CodeChallengeS256(%q) error = %v, want ErrInvalidVerifier", v, err)
		}
	}
}

func TestGeneratePKCE(t *testing.T) {
	p, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE: %v", err)
	}
	if p.ChallengeMethod != PKCEMethodS256 {
		t.Fatalf("ChallengeMethod = %q, want %q", p.ChallengeMethod, PKCEMethodS256)
	}
	want, err := CodeChallengeS256(p.Verifier)
	if err != nil {
		t.Fatalf("CodeChallengeS256: %v", err)
	}
	if p.Challenge != want {
		t.Fatalf("GeneratePKCE challenge %q does not derive from its verifier", p.Challenge)
	}
}
