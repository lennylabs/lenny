// SPDX-License-Identifier: MIT

package localcli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/embedded/devauth"
	"github.com/lennylabs/lenny/pkg/embedded/stack"
)

// spec: §22.7 (time-to-hello-world: lenny session new entry point)
// diagnosis: the quick-start documents `lenny session new --runtime
// <name>` as the entry point for starting a session against the
// embedded gateway. The CLI dispatch must wire `session` so an
// unknown-command error does not greet the operator on step 3 of the
// TTHW flow.
func TestSessionSubcommandWired(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cmdSession(nil, &stdout, &stderr)
	if code != 2 {
		t.Errorf("no-argument session: exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "new --runtime") {
		t.Errorf("usage text is missing: %q", stderr.String())
	}
}

func TestSessionNewRequiresRuntime(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cmdSession([]string{"new"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("session new with no runtime: exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--runtime") {
		t.Errorf("expected --runtime hint, got: %q", stderr.String())
	}
}

func TestSessionUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cmdSession([]string{"frobnicate"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("unknown session subcommand: exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown subcommand") {
		t.Errorf("expected unknown-subcommand diagnosis, got: %q", stderr.String())
	}
}

// TestMintEmbeddedBearerSignsFromPersistedDevKey_spec_17_4 covers the §17.4 dev
// bearer mint the CLI sends as Authorization: Bearer: mintEmbeddedBearer reads
// the persisted dev key under the Embedded Mode home (the same key lenny token
// print mints from and the in-cluster gateway trusts through
// --bearer-trust-hmac-key-file) and issues a token carrying the dev.local
// audience the gateway pins. Verifying the minted token against the same
// persisted key proves the CLI and the gateway share one signing key, and the
// dev.local audience proves the §17.4 foreign-audience rejection control still
// applies. A home with no persisted key fails closed with a message pointing at
// lenny up / --token rather than minting an unsigned bearer.
//
// spec: §17.4 (the CLI mints the dev bearer from a persisted dev key the
// in-cluster gateway trusts; the dev.local audience pin holds), §10.2 (the
// gateway loads the dev HMAC key as a verifier).
func TestMintEmbeddedBearerSignsFromPersistedDevKey_spec_17_4(t *testing.T) {
	t.Run("mints a verifiable dev.local bearer from the persisted key", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("LENNY_HOME", home)
		seedOIDCKey(t, home)

		token, err := mintEmbeddedBearer()
		if err != nil {
			t.Fatalf("mintEmbeddedBearer: %v", err)
		}
		if token == "" {
			t.Fatal("mintEmbeddedBearer returned an empty token")
		}

		// The bearer must verify against the same persisted key the gateway
		// trusts, with the §17.4 dev.local audience.
		paths := stack.NewPaths(home)
		verifier, err := devauth.NewWithPersistedKey(paths.OIDCKeyFile(), false)
		if err != nil {
			t.Fatalf("load persisted key for verify: %v", err)
		}
		// Verify enforces the §17.4 dev.local audience pin internally, so a
		// successful Verify already proves the audience holds; the explicit
		// check below guards against a future Verify that stops pinning it.
		claims, err := verifier.Verify(token)
		if err != nil {
			t.Fatalf("minted bearer does not verify against the shared persisted key: %v", err)
		}
		var devLocal bool
		for _, a := range claims.Audience {
			if a == devauth.Audience {
				devLocal = true
			}
		}
		if !devLocal {
			t.Errorf("minted bearer audience = %v, want it to carry %q", claims.Audience, devauth.Audience)
		}
	})

	t.Run("fails closed with no persisted key", func(t *testing.T) {
		t.Setenv("LENNY_HOME", t.TempDir())
		_, err := mintEmbeddedBearer()
		if err == nil {
			t.Fatal("mintEmbeddedBearer with no persisted key = nil, want a fail-closed error")
		}
		if !strings.Contains(err.Error(), "lenny up") {
			t.Errorf("error = %q, want it to point the operator at 'lenny up'/--token", err.Error())
		}
	})
}
