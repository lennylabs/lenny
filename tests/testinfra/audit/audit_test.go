// SPDX-License-Identifier: MIT

package audit

import (
	"bytes"
	"testing"
)

// spec: 11.7 (audit hash-chain genesis)
// diagnosis: A divergent sentinel between writer and verifier
//
//	would cause every chain to fail verification on the
//	first row.
func TestChainGenesis(t *testing.T) {
	t.Parallel()
	c := NewChain("acme")
	r := c.Append([]byte("first"))
	if !bytes.Equal(r.PrevHash, GenesisPrevHash) {
		t.Fatalf("first row prev_hash must be GenesisPrevHash")
	}
}

// spec: 11.7 (audit hash-chain link invariant)
// diagnosis: A drift between the writer's hash function and the
//
//	verifier's would mark every otherwise-valid chain as
//	broken.
func TestChainVerifiesHappyPath(t *testing.T) {
	t.Parallel()
	c := NewChain("acme")
	c.Append([]byte("first"))
	c.Append([]byte("second"))
	c.Append([]byte("third"))
	got := c.Verify()
	if got.State != ChainVerified {
		t.Fatalf("verify: state=%q badSeq=%d badKind=%q", got.State, got.BadSeq, got.BadKind)
	}
}

// spec: 11.7 (audit hash-chain tamper detection)
// diagnosis: A verifier that misses a single-byte flip cannot
//
//	meet the SOC2/FedRAMP non-repudiation control the chain
//	is supposed to provide.
func TestChainDetectsTamper(t *testing.T) {
	t.Parallel()
	c := NewChain("acme")
	c.Append([]byte("first"))
	c.Append([]byte("second"))
	c.Append([]byte("third"))
	if err := c.Tamper(1, []byte("FORGED")); err != nil {
		t.Fatalf("Tamper: %v", err)
	}
	got := c.Verify()
	if got.State != ChainBroken {
		t.Fatalf("expected ChainBroken; got %q", got.State)
	}
	if got.BadSeq != 1 || got.BadKind != "hash" {
		t.Errorf("expected broken at seq=1 kind=hash; got seq=%d kind=%q", got.BadSeq, got.BadKind)
	}
}

// spec: 11.7 (audit hash-chain per-tenant scope)
// diagnosis: A cross-tenant link would let one tenant's
//
//	redaction break another tenant's chain.
func TestChainRejectsCrossTenantRow(t *testing.T) {
	t.Parallel()
	c := NewChain("acme")
	r := c.Append([]byte("first"))
	r.Tenant = "globex" // simulate tampering
	got := c.Verify()
	if got.State != ChainBroken {
		t.Fatalf("expected ChainBroken on cross-tenant row; got %q", got.State)
	}
	if got.BadKind != "tenant" {
		t.Errorf("expected BadKind=tenant; got %q", got.BadKind)
	}
}

// spec: 11.7 (audit hash-chain empty case)
// diagnosis: An empty chain is "not yet written" rather than
//
//	"broken"; the verifier must distinguish.
func TestChainEmpty(t *testing.T) {
	t.Parallel()
	c := NewChain("acme")
	got := c.Verify()
	if got.State != ChainEmpty {
		t.Fatalf("empty chain should report ChainEmpty; got %q", got.State)
	}
}

// spec: 11.7 (audit hash-chain prev_hash break)
// diagnosis: Flipping the prev_hash on a non-genesis row must
//
//	be detected at that seq specifically.
func TestChainDetectsPrevHashBreak(t *testing.T) {
	t.Parallel()
	c := NewChain("acme")
	c.Append([]byte("first"))
	c.Append([]byte("second"))
	c.rows[1].PrevHash[0] ^= 0xff
	got := c.Verify()
	if got.State != ChainBroken {
		t.Fatalf("expected ChainBroken; got %q", got.State)
	}
	if got.BadSeq != 1 || got.BadKind != "prev_hash" {
		t.Errorf("expected broken at seq=1 kind=prev_hash; got seq=%d kind=%q", got.BadSeq, got.BadKind)
	}
}
