// SPDX-License-Identifier: MIT

package audit

import (
	"encoding/json"
	"testing"
	"time"
)

// The §11.7 audit hash chain links each row to its predecessor via
// `prev_hash = H(prev_row.hash || canonical_bytes(prev_row))`.

func ts() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

// TestHashChainGenesisRow — the first row in a per-tenant chain has a
// well-known sentinel prev_hash.
//
// spec: 11.7 (audit hash-chain genesis)
func TestHashChainGenesisRow(t *testing.T) {
	t.Parallel()
	c := NewChain("acme")
	row := c.Append("admin.tenant.created", json.RawMessage(`{"id":"acme"}`), ts())
	if row.PrevHash != GenesisPrevHash {
		t.Errorf("genesis prev_hash: got %q, want %q", row.PrevHash, GenesisPrevHash)
	}
	if row.Seq != 1 {
		t.Errorf("genesis seq: got %d, want 1", row.Seq)
	}
	if row.Hash == "" {
		t.Error("genesis row hash must be set")
	}
}

// TestHashChainLinkInvariant — for any two consecutive rows R_n and
// R_{n+1}, R_{n+1}.prev_hash equals linkHash(R_n).
//
// spec: 11.7 (audit hash-chain link invariant)
func TestHashChainLinkInvariant(t *testing.T) {
	t.Parallel()
	c := NewChain("acme")
	r1 := c.Append("e1", json.RawMessage(`{"n":1}`), ts())
	r2 := c.Append("e2", json.RawMessage(`{"n":2}`), ts())
	if r2.PrevHash != linkHash(r1) {
		t.Errorf("link invariant: r2.prev_hash %q != linkHash(r1) %q", r2.PrevHash, linkHash(r1))
	}
	if res := c.Verify(); res.Integrity != ChainVerified {
		t.Errorf("healthy chain: got %q (%s)", res.Integrity, res.Detail)
	}
}

// TestHashChainDetectsTamper — flipping a byte in a non-tail row
// causes Verify to report ChainBroken at that row.
//
// spec: 11.7 (audit hash-chain tamper detection)
func TestHashChainDetectsTamper(t *testing.T) {
	t.Parallel()
	c := NewChain("acme")
	c.Append("e1", json.RawMessage(`{"n":1}`), ts())
	c.Append("e2", json.RawMessage(`{"n":2}`), ts())
	c.Append("e3", json.RawMessage(`{"n":3}`), ts())

	// Tamper with row 2's payload directly (bypassing Redact, so no
	// receipt is attached).
	c.rows[1].Payload = json.RawMessage(`{"n":999}`)

	res := c.Verify()
	if res.Integrity != ChainBroken {
		t.Fatalf("tamper: got %q, want broken", res.Integrity)
	}
	if res.BreakSeq != 2 {
		t.Errorf("break seq: got %d, want 2", res.BreakSeq)
	}
}

// TestHashChainPerTenantIsolation — chains for different tenants are
// independent; a foreign-tenant row poisons only its own chain.
//
// spec: 11.7 (audit hash-chain per-tenant scope)
func TestHashChainPerTenantIsolation(t *testing.T) {
	t.Parallel()
	set := NewChainSet()
	set.Append("acme", "e1", json.RawMessage(`{}`), ts())
	set.Append("globex", "e1", json.RawMessage(`{}`), ts())

	if res := set.Chain("acme").Verify(); res.Integrity != ChainVerified {
		t.Errorf("acme chain: %q", res.Integrity)
	}
	if res := set.Chain("globex").Verify(); res.Integrity != ChainVerified {
		t.Errorf("globex chain: %q", res.Integrity)
	}

	// Inject a foreign-tenant row into acme's chain — Verify must
	// catch the cross-tenant link.
	acme := set.Chain("acme")
	acme.rows[0].TenantID = "globex"
	if res := acme.Verify(); res.Integrity != ChainBroken {
		t.Errorf("cross-tenant row should break the chain: got %q", res.Integrity)
	}
}

// TestHashChainRechainAfterRedaction — a §12.8 GDPR redaction
// rewrites a row in place; the verifier reports ChainRechainedPostOutage
// (lawful) rather than ChainBroken when a valid receipt is attached.
//
// spec: 11.7 (audit redaction receipt path)
func TestHashChainRechainAfterRedaction(t *testing.T) {
	t.Parallel()
	c := NewChain("acme")
	c.Append("e1", json.RawMessage(`{"pii":"alice@acme.com"}`), ts())
	c.Append("e2", json.RawMessage(`{"n":2}`), ts())

	err := c.Redact(1, json.RawMessage(`{"pii":"[REDACTED]"}`), RedactionReceipt{
		Signature: "kms-sig-abc",
	})
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	res := c.Verify()
	if res.Integrity != ChainRechainedPostOutage {
		t.Errorf("lawful redaction: got %q (%s), want rechained", res.Integrity, res.Detail)
	}
}

func TestHashChainRedactionWithoutReceiptIsBroken(t *testing.T) {
	t.Parallel()
	c := NewChain("acme")
	c.Append("e1", json.RawMessage(`{"pii":"x"}`), ts())

	// Mark redacted + rewrite payload but attach NO valid receipt.
	c.rows[0].Redacted = true
	c.rows[0].Payload = json.RawMessage(`{"pii":"[REDACTED]"}`)

	if res := c.Verify(); res.Integrity != ChainBroken {
		t.Errorf("redaction w/o receipt: got %q, want broken", res.Integrity)
	}
}

func TestHashChainEmptyVerifies(t *testing.T) {
	t.Parallel()
	if res := NewChain("acme").Verify(); res.Integrity != ChainVerified {
		t.Errorf("empty chain: got %q", res.Integrity)
	}
}

func TestRedactOutOfRange(t *testing.T) {
	t.Parallel()
	c := NewChain("acme")
	c.Append("e1", json.RawMessage(`{}`), ts())
	if err := c.Redact(5, json.RawMessage(`{}`), RedactionReceipt{Signature: "s"}); err == nil {
		t.Error("Redact out-of-range seq should error")
	}
}

func TestChainSetTenants(t *testing.T) {
	t.Parallel()
	set := NewChainSet()
	set.Append("globex", "e", json.RawMessage(`{}`), ts())
	set.Append("acme", "e", json.RawMessage(`{}`), ts())
	got := set.Tenants()
	if len(got) != 2 || got[0] != "acme" || got[1] != "globex" {
		t.Errorf("Tenants: %v", got)
	}
}
