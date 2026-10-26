// SPDX-License-Identifier: MIT

// Package stackkey gives every Lenny process one test starts the same
// local KMS master key, so the processes share one §10.2 key set as the
// replicas of one install do. The gateway, Token Service, and lenny-ops
// harnesses each pass MasterKeyFile through LENNY_KMS_MASTER_KEY_FILE, so
// a process started without --kms-master-key-file derives its
// platform:jwt-signing key from the stack's key rather than from a random
// per-process seed. A test that wants a process on another key passes
// --kms-master-key-file explicitly; the flag overrides the environment
// default.
//
// The stack is the top-level test: every subtest of one top-level test
// shares its key. A test that starts processes only inside subtests
// calls MasterKeyFile on the top-level test first, so a key a subtest
// creates is not removed when that subtest ends.
package stackkey

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
)

// EnvMasterKeyFile is the environment variable that supplies the default
// of --kms-master-key-file on all three binaries (pkg/kms/providerflags).
const EnvMasterKeyFile = "LENNY_KMS_MASTER_KEY_FILE"

var (
	mu    sync.Mutex
	files = map[string]string{}
)

// stackName returns the top-level test name of name.
func stackName(name string) string {
	root, _, _ := strings.Cut(name, "/")
	return root
}

// MasterKeyFile returns the path of the local KMS master key file of the
// stack t belongs to, creating it on first use. The file is created
// before any process reads it, so concurrent process starts cannot race
// to write two different keys to one path. The cleanup of the t that
// created it removes it.
func MasterKeyFile(t testing.TB) string {
	t.Helper()
	root := stackName(t.Name())
	mu.Lock()
	defer mu.Unlock()
	if p, ok := files[root]; ok {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	dir, err := os.MkdirTemp("", "lenny-stack-key-*")
	if err != nil {
		t.Fatalf("stackkey: mkdtemp: %v", err)
	}
	p := filepath.Join(dir, "master.key")
	if _, err := kms.LoadOrCreateMasterKey(p); err != nil {
		t.Fatalf("stackkey: create master key: %v", err)
	}
	files[root] = p
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		if files[root] == p {
			delete(files, root)
		}
		_ = os.RemoveAll(dir)
	})
	return p
}

// Env returns the environment entry that hands the stack's master key to
// a process.
func Env(t testing.TB) string {
	t.Helper()
	return EnvMasterKeyFile + "=" + MasterKeyFile(t)
}

// Signer derives the key set and signer a process on the local provider
// holds when it runs over the master key at keyFile with the
// platform:jwt-* references in refs, so a test can sign a token every
// such process verifies, or one no process verifies when keyFile is
// another stack's key. A nil refs derives each alias from its own name,
// as a process started without --kms-alias does.
func Signer(t testing.TB, keyFile string, refs map[string]string) (*jwt.KeySet, *jwt.RemoteSigner) {
	t.Helper()
	local, err := kms.NewLocalFromKeyFile(keyFile)
	if err != nil {
		t.Fatalf("stackkey: load master key %s: %v", keyFile, err)
	}
	for alias, ref := range refs {
		local.SetSigningReference(alias, ref)
	}
	ks, err := jwt.LoadKeySet(context.Background(), local, refs)
	if err != nil {
		t.Fatalf("stackkey: load key set: %v", err)
	}
	signer, err := jwt.NewRemoteSigner(local, ks.Signing())
	if err != nil {
		t.Fatalf("stackkey: remote signer: %v", err)
	}
	return ks, signer
}

// WriteBearerTrustKey writes a §17.4 dev bearer-trust HMAC key file with a
// random secret and returns its path. Passing it as
// --bearer-trust-hmac-key-file makes a non-production lenny-ops
// authenticate its requests, so a test can assert a token outcome on it.
func WriteBearerTrustKey(t testing.TB) string {
	t.Helper()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatalf("stackkey: random trust secret: %v", err)
	}
	raw, err := json.Marshal(struct {
		KeyID  string `json:"keyId"`
		Secret []byte `json:"secret"`
	}{"stack-dev-trust", secret})
	if err != nil {
		t.Fatalf("stackkey: marshal trust key: %v", err)
	}
	dir, err := os.MkdirTemp("", "lenny-stack-trust-*")
	if err != nil {
		t.Fatalf("stackkey: mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p := filepath.Join(dir, "bearer-trust-key.json")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatalf("stackkey: write trust key: %v", err)
	}
	return p
}
