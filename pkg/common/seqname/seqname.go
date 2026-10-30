// SPDX-License-Identifier: MIT

// Package seqname derives the length-bounded, injection-safe identifier
// for a per-tenant Postgres sequence object from a tenant_id and a
// per-ledger prefix.
//
// The §10.2 tenant_id format (`^[a-zA-Z0-9_-]{1,128}$`) is free of
// injection and path-traversal hazards but does not bound identifier
// length: a 128-character tenant_id exceeds the 63-byte Postgres
// identifier limit (NAMEDATALEN-1), and Postgres silently truncates any
// longer identifier, so two tenants sharing a 51-character prefix would
// collapse onto the same truncated sequence object. Interpolating the
// raw tenant_id into `billing_seq_{tenant_id}` therefore imports a
// cross-tenant sequence-corruption hazard.
//
// The derivation defined here is the canonical §10.2 name: a per-ledger
// literal prefix (`billing_seq_` for the billing ledger, `audit_seq_`
// for the audit ledger) followed by the lowercase hexadecimal encoding
// of the first 20 bytes (160 bits) of SHA-256(tenant_id), a fixed
// 40-hexadecimal-character digest. `billing_seq_<40hex>` is 52 bytes
// and `audit_seq_<40hex>` is 50 bytes, both ≤ 63 bytes for every
// tenant_id the format admits. The two prefixes give the billing and
// audit ledgers distinct per-tenant sequence objects on the shared
// billing/audit instance, so each ledger's sequence_number advances
// independently and neither ledger consumes the other's values.
//
// The output is injection-safe by construction: a fixed literal prefix
// followed by a hex digest is a valid Postgres identifier for any input
// string, so call sites that interpolate the result into runtime DDL
// (CREATE SEQUENCE, nextval, setval) need no separate identifier
// allowlisting.
//
// Consumers: the billing store (pkg/gateway/billing/billingstore),
// the audit store (pkg/gateway/audit/auditstore), and the admin
// tenant-provisioning helper (pkg/gateway/externalapi/admin) all call
// this package so the four write and provisioning paths agree on one
// derivation.
//
// The package is a leaf utility with no imports outside the standard
// library.
//
// spec: §10.2
package seqname

import (
	"crypto/sha256"
	"encoding/hex"
)

// digestBytes is the number of leading SHA-256 bytes the §10.2
// derivation encodes as hex: 20 bytes (160 bits) yield a fixed
// 40-hex-character digest.
//
// spec: §10.2
const digestBytes = 20

// Ledger identifies which per-tenant sequence a name derives. The two
// ledgers carry distinct literal prefixes so billing and audit resolve
// to separate sequence objects on the shared §12.3 billing/audit
// instance.
//
// spec: §10.2
type Ledger struct {
	// prefix is the per-ledger literal prefix the §10.2 derivation
	// prepends to the hex digest. It is a compile-time constant valid
	// Postgres identifier fragment.
	prefix string
}

// Billing is the §11.2.1 billing ledger. Its per-tenant sequence name
// is `billing_seq_` followed by the 40-hex tenant digest (52 bytes).
//
// spec: §10.2, §11.2.1
var Billing = Ledger{prefix: "billing_seq_"}

// Audit is the §11.7 audit ledger. Its per-tenant sequence name is
// `audit_seq_` followed by the 40-hex tenant digest (50 bytes), a
// distinct object from the billing sequence.
//
// spec: §10.2, §11.7
var Audit = Ledger{prefix: "audit_seq_"}

// SequenceName returns the derived per-tenant Postgres sequence name for
// the given tenant_id on this ledger. The result is the ledger's prefix
// followed by the lowercase hex encoding of the first 20 bytes of
// SHA-256(tenantID), a fixed 40-hex-character digest, and is a valid,
// injection-safe Postgres identifier ≤ 63 bytes for any input.
//
// spec: §10.2
func (l Ledger) SequenceName(tenantID string) string {
	sum := sha256.Sum256([]byte(tenantID))
	return l.prefix + hex.EncodeToString(sum[:digestBytes])
}

// BillingSequenceName returns the §11.2.1 per-tenant billing sequence
// name for tenantID (`billing_seq_<40hex>`).
//
// spec: §10.2, §11.2.1
func BillingSequenceName(tenantID string) string {
	return Billing.SequenceName(tenantID)
}

// AuditSequenceName returns the §11.7 per-tenant audit sequence name for
// tenantID (`audit_seq_<40hex>`), a distinct object from the billing
// sequence.
//
// spec: §10.2, §11.7
func AuditSequenceName(tenantID string) string {
	return Audit.SequenceName(tenantID)
}
