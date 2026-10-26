// SPDX-License-Identifier: MIT

package stackkey

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
)

// spec: 10.2 (Authentication) "Verifier key set and rotation"
// diagnosis: two processes of one test would get different master keys, so
// peer replicas would hold different key sets and a token would verify
// only on its signer; or a subtest would not share its parent's key.
func TestMasterKeyFileIsSharedAcrossOneStack(t *testing.T) {
	first := MasterKeyFile(t)
	if again := MasterKeyFile(t); again != first {
		t.Fatalf("second call = %s, want %s", again, first)
	}
	t.Run("subtest", func(t *testing.T) {
		if got := MasterKeyFile(t); got != first {
			t.Errorf("subtest key = %s, want the stack's %s", got, first)
		}
	})
	if env := Env(t); env != EnvMasterKeyFile+"="+first {
		t.Errorf("Env = %q", env)
	}
	if st, err := os.Stat(first); err != nil || st.Size() < kms.DEKSize {
		t.Fatalf("master key file missing or short: %v", err)
	}
}

// spec: 10.2 (Authentication) "Verifier key set and rotation"
// diagnosis: the test-side signer does not derive the key a process over
// the same master key and references derives, so a test cannot sign a
// token the processes verify; or two different references derive one key,
// so a rotation test cannot tell old tokens from new ones.
func TestSignerDerivesTheProcessKey(t *testing.T) {
	keyFile := MasterKeyFile(t)
	refs := map[string]string{kms.AliasJWTSigning: "ref-a"}
	ksA, signerA := Signer(t, keyFile, refs)
	ksA2, _ := Signer(t, keyFile, refs)
	if ksA.Signing().KeyID != ksA2.Signing().KeyID || signerA.KeyID() != ksA.Signing().KeyID {
		t.Fatal("one master key and reference derived two kids")
	}
	ksB, _ := Signer(t, keyFile, map[string]string{kms.AliasJWTSigning: "ref-b"})
	if ksB.Signing().KeyID == ksA.Signing().KeyID {
		t.Fatal("two references derived one kid")
	}
	other := filepath.Join(t.TempDir(), "other.key")
	ksOther, _ := Signer(t, other, refs)
	if ksOther.Signing().KeyID == ksA.Signing().KeyID {
		t.Fatal("two master keys derived one kid")
	}
	both, _ := Signer(t, keyFile, map[string]string{kms.AliasJWTSigning: "ref-b", kms.AliasJWTVerify: "ref-a"})
	if both.Len() != 2 {
		t.Fatalf("key set with a verify alias holds %d keys, want 2", both.Len())
	}
}

// spec: 17.4 (Local Development Mode) "Dev mode guard rails"
// diagnosis: the trust key file is not in the format
// --bearer-trust-hmac-key-file loads, so a lenny-ops started with it exits.
func TestWriteBearerTrustKeyLoads(t *testing.T) {
	p := WriteBearerTrustKey(t)
	if _, err := jwt.LoadHMACKeyFile(p); err != nil {
		t.Fatalf("LoadHMACKeyFile: %v", err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var kf map[string]any
	if err := json.Unmarshal(raw, &kf); err != nil || !strings.HasPrefix(kf["keyId"].(string), "stack") {
		t.Fatalf("trust key %s: %v", raw, err)
	}
}
