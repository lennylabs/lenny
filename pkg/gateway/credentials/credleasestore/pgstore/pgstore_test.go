// SPDX-License-Identifier: MIT

package pgstore

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/kms"
)

// spec: §12.9 line 1048 — a credential lease is T4 — Restricted and must
// be stored under envelope encryption, so the Postgres-backed store must
// refuse to construct without a KEK provider.
func TestNewRequiresKMSProvider(t *testing.T) {
	t.Parallel()
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("NewLocalRandom: %v", err)
	}
	if _, err := New(nil, provider); err == nil {
		t.Error("New accepted a nil pool")
	}
	// A non-nil pool with a nil provider must be rejected: New stores the
	// pool only after the provider check, so the zero-value pool is never
	// dereferenced.
	if _, err := New(&pgxpool.Pool{}, nil); err == nil {
		t.Error("New accepted a nil kms provider for a T4 store")
	}
	if _, err := New(&pgxpool.Pool{}, provider); err != nil {
		t.Errorf("New with a provider failed: %v", err)
	}
}

// spec: §12.9 line 1048 — the bearer lease token must not be persisted in
// cleartext; GetByToken resolves it through a deterministic SHA-256 hash.
func TestLeaseTokenHash(t *testing.T) {
	t.Parallel()

	proxy := credential.Lease{
		LeaseID:      "cl-1",
		DeliveryMode: credential.DeliveryProxy,
		Proxy:        &credential.ProxyConfig{LeaseToken: "lt-secret-capability"},
	}
	got := leaseTokenHash(proxy)
	if got == nil {
		t.Fatal("leaseTokenHash returned nil for a proxy lease")
	}
	sum := sha256.Sum256([]byte("lt-secret-capability"))
	want := hex.EncodeToString(sum[:])
	if *got != want {
		t.Errorf("leaseTokenHash = %q, want %q", *got, want)
	}
	if *got == "lt-secret-capability" {
		t.Error("leaseTokenHash returned the plaintext token")
	}

	// A direct-mode lease carries no token, so the hash column is NULL.
	direct := credential.Lease{LeaseID: "cl-2", DeliveryMode: credential.DeliveryDirect}
	if leaseTokenHash(direct) != nil {
		t.Error("leaseTokenHash returned a non-nil hash for a direct-mode lease")
	}

	// GetByToken hashes the presented token the same way Put hashed it,
	// so the two derivations must agree for a round-trip to resolve.
	if tokenHash("lt-secret-capability") != *got {
		t.Error("tokenHash disagrees with leaseTokenHash for the same token")
	}
}
