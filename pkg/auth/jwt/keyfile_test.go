// SPDX-License-Identifier: MIT

package jwt

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeKeyFile writes an hmacKeyFile to a temp path in the same JSON
// format the embedded OIDC provider persists, and returns the path.
func writeKeyFile(t *testing.T, keyID string, secret []byte) string {
	t.Helper()
	raw, err := json.Marshal(hmacKeyFile{KeyID: keyID, Secret: secret})
	if err != nil {
		t.Fatalf("marshal key file: %v", err)
	}
	path := filepath.Join(t.TempDir(), "signing.key")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	return path
}

func TestLoadHMACKeyFileRoundTrip(t *testing.T) {
	secret := []byte("embedded-oidc-signing-secret-32b")
	path := writeKeyFile(t, "embedded-key-1", secret)

	loaded, err := LoadHMACKeyFile(path)
	if err != nil {
		t.Fatalf("LoadHMACKeyFile: %v", err)
	}
	if loaded.KeyID() != "embedded-key-1" {
		t.Errorf("KeyID = %q, want embedded-key-1", loaded.KeyID())
	}

	// A signer built from the same secret produces a token the loaded
	// verifier accepts: the load path recovers the exact key.
	signer := NewHMACSigner("embedded-key-1", secret)
	tok, err := signer.Sign(mvClaims("alice@acme.com"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	claims, err := loaded.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "alice@acme.com" {
		t.Errorf("subject = %q, want alice@acme.com", claims.Subject)
	}
}

func TestLoadHMACKeyFileMissing(t *testing.T) {
	_, err := LoadHMACKeyFile(filepath.Join(t.TempDir(), "absent.key"))
	if err == nil {
		t.Fatal("LoadHMACKeyFile accepted a missing file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error = %v, want it to wrap os.ErrNotExist", err)
	}
}

func TestLoadHMACKeyFileNotJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.key")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadHMACKeyFile(path); err == nil {
		t.Fatal("LoadHMACKeyFile accepted a non-JSON file")
	}
}

func TestLoadHMACKeyFileEmptyKeyID(t *testing.T) {
	path := writeKeyFile(t, "", []byte("some-secret"))
	if _, err := LoadHMACKeyFile(path); err == nil {
		t.Fatal("LoadHMACKeyFile accepted a file with an empty keyId")
	}
}

func TestLoadHMACKeyFileEmptySecret(t *testing.T) {
	path := writeKeyFile(t, "key-1", nil)
	if _, err := LoadHMACKeyFile(path); err == nil {
		t.Fatal("LoadHMACKeyFile accepted a file with an empty secret")
	}
}
