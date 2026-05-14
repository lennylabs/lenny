// SPDX-License-Identifier: MIT

package audit

import (
	"testing"
)

// The §11.7 audit hash chain links each row to its predecessor via
// `prev_hash = H(prev_row.hash || canonical_bytes(prev_row))`. The
// writer that produces the chain and the verifier that walks it both
// live in a later phase. These tests pin down the invariants that
// every implementation must satisfy; they skip on Phase 0 because the
// chain primitive does not yet exist on the package surface.

// TestHashChainGenesisRow — the first row in a per-tenant chain has a
// well-known sentinel prev_hash. The writer must use the same
// sentinel the verifier walks back to so the chain is anchored at a
// reproducible point.
//
// spec: 11.7 (audit hash-chain genesis)
// diagnosis: A divergent sentinel between writer and verifier would
//
//	cause every chain to fail verification on the first row.
func TestHashChainGenesisRow(t *testing.T) {
	t.Parallel()
	t.Skip("not implemented: §12.1 unit-tier hash-chain primitive lives in a later phase")
}

// TestHashChainLinkInvariant — for any two consecutive rows R_n and
// R_{n+1} in a tenant's chain, R_{n+1}.prev_hash equals the hash of
// R_n. A row that fails this is reported as ChainBroken.
//
// spec: 11.7 (audit hash-chain link invariant)
// diagnosis: A drift between the writer's hash function and the
//
//	verifier's would mark every otherwise-valid chain as
//	broken and fire AuditChainGap on healthy systems.
func TestHashChainLinkInvariant(t *testing.T) {
	t.Parallel()
	t.Skip("not implemented: §12.1 hash-chain primitive lives in a later phase")
}

// TestHashChainDetectsTamper — flipping a single byte in any non-tail
// row must cause the verifier to report ChainBroken at or after the
// tampered row's sequence number.
//
// spec: 11.7 (audit hash-chain tamper detection)
// diagnosis: A verifier that misses a single-byte flip cannot meet
//
//	the SOC2/FedRAMP non-repudiation control the chain is
//	supposed to provide.
func TestHashChainDetectsTamper(t *testing.T) {
	t.Parallel()
	t.Skip("not implemented: §12.1 hash-chain primitive lives in a later phase")
}

// TestHashChainPerTenantIsolation — chains for different tenants are
// independent. A row from tenant A must never link to a row from
// tenant B, even when the row contents otherwise hash to the same
// value.
//
// spec: 11.7 (audit hash-chain per-tenant scope)
// diagnosis: A cross-tenant link would let one tenant's redaction
//
//	break another tenant's chain — a cross-tenant denial of
//	non-repudiation.
func TestHashChainPerTenantIsolation(t *testing.T) {
	t.Parallel()
	t.Skip("not implemented: §12.1 hash-chain primitive lives in a later phase")
}

// TestHashChainRechainAfterRedaction — a §12.8 GDPR redaction
// rewrites a row in place. The verifier observes a hash break and
// must report ChainRedactedGDPR (not ChainBroken) when a
// signature-verified RedactionReceipt is attached.
//
// spec: 11.7 (audit redaction receipt path)
// diagnosis: A verifier that conflates redacted_gdpr with broken
//
//	fires AuditChainGap on lawful erasure flows.
func TestHashChainRechainAfterRedaction(t *testing.T) {
	t.Parallel()
	t.Skip("not implemented: §11.7 redaction-receipt path lives in a later phase")
}
