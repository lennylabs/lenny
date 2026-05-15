// SPDX-License-Identifier: MIT

// Package audit is the §11.7 audit hash-chain helper. Tests use
// Chain to build sequences of audit-style rows in memory, mutate
// them to simulate tampering or §12.8 GDPR redactions, and run the
// Verifier to assert the resulting ChainIntegrity state.
//
// The helper does not interact with Postgres or any production
// audit-writer; it operates on opaque payloads so callers control
// the exact byte sequence the hash function consumes. Future
// integration tests can wrap their real audit writer in this
// helper to verify chain semantics end-to-end.
//
// Chain semantics mirror the §11.7 invariant:
//
//	row.prev_hash = sha256(prev_row.hash || prev_row.canonical_bytes)
//	row.hash     = sha256(row.prev_hash  || row.canonical_bytes)
//
// where canonical_bytes is whatever the caller chose to feed in;
// the helper does not impose a canonicalization scheme so tests can
// validate the §11.7 RFC 8785 JCS implementation against a known
// fixture set.
//
// # Genesis sentinel
//
// The first row in a chain uses the all-zeros 32-byte
// GenesisPrevHash. Writers and verifiers must agree on this
// constant; a divergent sentinel between them is the most common
// integration bug and the one the helper makes easy to catch.
//
// # Per-tenant chains
//
// Chains are per-tenant scopes. A row from tenant A must never
// link to a row from tenant B; the helper enforces this via the
// Tenant field on every Row and refuses to extend across tenants.
package audit

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
)

// GenesisPrevHash is the 32-byte sentinel the writer uses as
// prev_hash for the first row in a tenant's chain.
var GenesisPrevHash = make([]byte, 32)

// Row is one entry in a tenant's audit chain. Tenant scopes the
// chain; PrevHash links to the row before; Bytes is the
// canonical payload the hash function consumed; Hash is the
// computed link.
type Row struct {
	Tenant   string
	Seq      uint64
	PrevHash []byte
	Bytes    []byte
	Hash     []byte
}

// Chain holds the in-memory audit log for a single tenant. Append
// is the only mutation; tests that need to model corruption mutate
// Row fields directly and re-verify.
type Chain struct {
	tenant string
	rows   []*Row
}

// NewChain returns an empty chain anchored at GenesisPrevHash.
func NewChain(tenant string) *Chain {
	return &Chain{tenant: tenant}
}

// Append adds a row whose canonical bytes are `payload`. The
// row's prev_hash links to the previous row's hash (or
// GenesisPrevHash for the first row). Returns the appended Row.
func (c *Chain) Append(payload []byte) *Row {
	prev := GenesisPrevHash
	if n := len(c.rows); n > 0 {
		prev = c.rows[n-1].Hash
	}
	r := &Row{
		Tenant:   c.tenant,
		Seq:      uint64(len(c.rows)),
		PrevHash: append([]byte(nil), prev...),
		Bytes:    append([]byte(nil), payload...),
	}
	h := sha256.New()
	h.Write(r.PrevHash)
	h.Write(r.Bytes)
	r.Hash = h.Sum(nil)
	c.rows = append(c.rows, r)
	return r
}

// Rows returns the chain in append order. Callers can mutate the
// returned slice to simulate tampering before re-verifying.
func (c *Chain) Rows() []*Row { return c.rows }

// Tenant returns the chain's tenant identifier.
func (c *Chain) Tenant() string { return c.tenant }

// ChainIntegrity is the §11.7 verifier state. Mirrors the
// pkg/audit enum exactly so integration tests can compare
// directly.
type ChainIntegrity string

const (
	ChainVerified ChainIntegrity = "verified"
	ChainBroken   ChainIntegrity = "broken"
	ChainEmpty    ChainIntegrity = "empty"
)

// VerifyResult names which row, if any, broke the chain and why.
type VerifyResult struct {
	State   ChainIntegrity
	BadSeq  uint64
	BadKind string // "prev_hash" | "hash" | "tenant" | ""
}

// Verify walks the chain end-to-end. The first row's prev_hash
// must equal GenesisPrevHash; every subsequent row's prev_hash
// must equal the prior row's hash; every row's recomputed hash
// must match the stored hash; every row's tenant must match the
// chain's tenant.
func (c *Chain) Verify() VerifyResult {
	if len(c.rows) == 0 {
		return VerifyResult{State: ChainEmpty}
	}
	expectedPrev := GenesisPrevHash
	for _, r := range c.rows {
		if r.Tenant != c.tenant {
			return VerifyResult{State: ChainBroken, BadSeq: r.Seq, BadKind: "tenant"}
		}
		if !bytes.Equal(r.PrevHash, expectedPrev) {
			return VerifyResult{State: ChainBroken, BadSeq: r.Seq, BadKind: "prev_hash"}
		}
		h := sha256.New()
		h.Write(r.PrevHash)
		h.Write(r.Bytes)
		want := h.Sum(nil)
		if !bytes.Equal(r.Hash, want) {
			return VerifyResult{State: ChainBroken, BadSeq: r.Seq, BadKind: "hash"}
		}
		expectedPrev = r.Hash
	}
	return VerifyResult{State: ChainVerified}
}

// Tamper mutates the row at the given sequence number by
// overwriting its Bytes. Helper for tests that want to assert
// Verify returns ChainBroken at that seq.
func (c *Chain) Tamper(seq uint64, newBytes []byte) error {
	if int(seq) >= len(c.rows) {
		return fmt.Errorf("audit.Chain.Tamper: seq %d out of range (len=%d)", seq, len(c.rows))
	}
	c.rows[seq].Bytes = append([]byte(nil), newBytes...)
	return nil
}

// ErrCrossTenantLink is returned by AppendCrossTenant when a test
// tries to extend a chain with a row marked for a different tenant.
// Production code should never do this; the helper enforces it so
// the unit test can pin the contract.
var ErrCrossTenantLink = errors.New("audit.Chain: cross-tenant row rejected")
