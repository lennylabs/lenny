// SPDX-License-Identifier: MIT

package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/audit/jcs"
)

// DefaultEventSchemaVersion is the §11.7 item 3 line 365 schema version
// stamped on a row whose event type does not carry an explicit version.
// The audit_log.event_schema_version column defaults to the same value,
// so a row sealed in Go and a row scanned from Postgres hash identically.
const DefaultEventSchemaVersion = "v1"

// GenesisPrevHash is the §11.7 sentinel prev_hash stamped on the
// first row of every per-tenant chain. The verifier walks back to
// this anchor; writer and verifier MUST agree on the value, so it
// is a single exported constant.
const GenesisPrevHash = "0000000000000000000000000000000000000000000000000000000000000000"

// Row is one §11.7 audit hash-chain entry. Rows are append-only:
// once written, only the §12.8 GDPR redaction path may rewrite a
// row's Payload in place, and that path attaches a RedactionReceipt.
type Row struct {
	// ID is the row UUID (audit_log.id). It is part of the §11.7 item 3
	// hash input tuple, so it is generated at seal time rather than left
	// to a Postgres column default. A row scanned from Postgres carries
	// the stored UUID.
	ID string `json:"id"`

	// Seq is the per-tenant monotonic sequence number, starting at 1.
	Seq uint64 `json:"seq"`

	// TenantID scopes the row to a single tenant's chain.
	TenantID string `json:"tenant_id"`

	// EventType is the §11.7 audit event type
	// (e.g., `admin.tenant.created`).
	EventType string `json:"event_type"`

	// EventSchemaVersion is the §11.7 item 3 line 365 schema version of
	// EventType (e.g., `v1`). It is part of the hash input so a payload
	// shape change for the same event_type produces distinct hashes.
	EventSchemaVersion string `json:"event_schema_version"`

	// Payload is the canonical event body. Redaction rewrites this
	// in place per §12.8.
	Payload json.RawMessage `json:"payload"`

	// Timestamp is the UTC instant the row was committed.
	Timestamp time.Time `json:"timestamp"`

	// PrevHash links this row to its predecessor: it is the SHA-256 hash
	// of the predecessor's §11.7 item 3 tuple (the predecessor's Hash).
	// The genesis row carries GenesisPrevHash.
	PrevHash string `json:"prev_hash"`

	// Hash is this row's content hash: the SHA-256 of the §11.7 item 3
	// canonical tuple (id, prev_hash, tenant_id, sequence_number,
	// event_type, event_schema_version, payload_canonical_json,
	// created_at). The successor row's PrevHash equals this value.
	Hash string `json:"hash"`

	// Redacted, when true, marks the row as rewritten by a §12.8
	// GDPR erasure. A redacted row carries a non-nil
	// RedactionReceipt and the verifier reports ChainRedactedGDPR
	// rather than ChainBroken at the hash break it introduces.
	Redacted bool `json:"redacted,omitempty"`
}

// RedactionReceipt is the §12.8 KMS-signed record attached to a row
// rewritten by GDPR erasure. The minimal implementation models the
// signature as an opaque string; production wires KMS.
type RedactionReceipt struct {
	// RedactedSeq is the sequence number of the rewritten row.
	RedactedSeq uint64 `json:"redacted_seq"`

	// OriginalHash is the content hash the row carried before
	// redaction. The verifier uses it to confirm the receipt
	// matches the break it observes.
	OriginalHash string `json:"original_hash"`

	// Signature is the KMS signature over (RedactedSeq,
	// OriginalHash). The minimal implementation does not verify the
	// signature cryptographically; production wires KMS.
	Signature string `json:"signature"`
}

// CanonicalPayload returns the RFC 8785 JCS canonical form of a row's
// payload — the value stored in audit_log.payload_canonical_json and
// fed into the §11.7 hash input. A payload that does not parse as JSON
// falls back to its raw bytes (audit payloads are always JSON, so this
// is defensive); an empty payload canonicalizes to `null`.
//
// spec: §11.7 item 3 line 364 (payload_canonical_json is RFC 8785 JCS).
func CanonicalPayload(payload json.RawMessage) []byte {
	if len(payload) == 0 {
		return []byte("null")
	}
	canon, err := jcs.Canonicalize(payload)
	if err != nil {
		return []byte(payload)
	}
	return canon
}

// canonicalBytes returns the deterministic byte representation of the
// §11.7 item 3 line 361 hash-input tuple: (id, prev_hash, tenant_id,
// sequence_number, event_type, event_schema_version,
// payload_canonical_json, created_at). The payload is canonicalized with
// RFC 8785 JCS so the hash is stable across the key-order and
// number-form changes a Postgres jsonb round trip introduces.
// encoding/json with a fixed struct field order is deterministic.
//
// spec: §11.7 item 3 lines 361-366.
func canonicalBytes(r Row) []byte {
	version := r.EventSchemaVersion
	if version == "" {
		version = DefaultEventSchemaVersion
	}
	type canonical struct {
		ID                 string          `json:"id"`
		PrevHash           string          `json:"prev_hash"`
		TenantID           string          `json:"tenant_id"`
		SequenceNumber     uint64          `json:"sequence_number"`
		EventType          string          `json:"event_type"`
		EventSchemaVersion string          `json:"event_schema_version"`
		PayloadCanonical   json.RawMessage `json:"payload_canonical_json"`
		CreatedAt          string          `json:"created_at"`
	}
	b, _ := json.Marshal(canonical{
		ID:                 r.ID,
		PrevHash:           r.PrevHash,
		TenantID:           r.TenantID,
		SequenceNumber:     r.Seq,
		EventType:          r.EventType,
		EventSchemaVersion: version,
		PayloadCanonical:   json.RawMessage(CanonicalPayload(r.Payload)),
		CreatedAt:          r.Timestamp.UTC().Format(time.RFC3339Nano),
	})
	return b
}

// computeHash returns the §11.7 item 3 content hash of a row: the
// SHA-256 of its canonical tuple. The successor row's prev_hash equals
// this value.
func computeHash(r Row) string {
	sum := sha256.Sum256(canonicalBytes(r))
	return hex.EncodeToString(sum[:])
}

// linkHash returns the prev_hash a successor row must carry per §11.7
// item 3: the SHA-256 hash of the predecessor's canonical tuple, which
// is exactly the predecessor's content hash. Because that tuple already
// folds in the predecessor's own prev_hash, this forms the tamper-
// evident chain the verifier walks.
func linkHash(prev Row) string {
	return prev.Hash
}

// ComputeHash returns a row's §11.7 content hash. A Postgres-backed
// audit store calls this to seal a row before persisting it, so the
// stored chain uses the same hash construction as the in-memory one.
func ComputeHash(r Row) string { return computeHash(r) }

// LinkHash returns the prev_hash a row's successor must carry, given
// that row. A Postgres-backed store calls this to chain a freshly
// appended row to the persisted tail.
func LinkHash(prev Row) string { return linkHash(prev) }

// Chain is a per-tenant append-only audit hash chain. The zero
// value is not usable; construct with NewChain.
type Chain struct {
	mu       sync.Mutex
	tenantID string
	rows     []Row
	receipts map[uint64]RedactionReceipt
}

// NewChain returns an empty Chain scoped to tenantID.
func NewChain(tenantID string) *Chain {
	return &Chain{
		tenantID: tenantID,
		receipts: map[uint64]RedactionReceipt{},
	}
}

// ChainFromRows builds a Chain over an already-committed, sequence-
// ordered set of rows. A Postgres-backed audit store loads a tenant's
// persisted rows and calls Verify on the result, so the durable chain
// is checked by exactly the same §11.7 walk as the in-memory one.
// The returned Chain is intended for verification only.
func ChainFromRows(tenantID string, rows []Row, receipts map[uint64]RedactionReceipt) *Chain {
	if receipts == nil {
		receipts = map[uint64]RedactionReceipt{}
	}
	return &Chain{tenantID: tenantID, rows: rows, receipts: receipts}
}

// TenantID returns the chain's tenant scope.
func (c *Chain) TenantID() string { return c.tenantID }

// Append commits a new row carrying eventType + payload. Returns the
// committed row. The chain assigns Seq, PrevHash, Hash, and the
// timestamp (using now).
func (c *Chain) Append(eventType string, payload json.RawMessage, now time.Time) Row {
	c.mu.Lock()
	defer c.mu.Unlock()
	row := Row{
		ID:                 uuid.NewString(),
		Seq:                uint64(len(c.rows)) + 1,
		TenantID:           c.tenantID,
		EventType:          eventType,
		EventSchemaVersion: DefaultEventSchemaVersion,
		Payload:            payload,
		Timestamp:          now.UTC(),
	}
	if len(c.rows) == 0 {
		row.PrevHash = GenesisPrevHash
	} else {
		row.PrevHash = linkHash(c.rows[len(c.rows)-1])
	}
	row.Hash = computeHash(row)
	c.rows = append(c.rows, row)
	return row
}

// Rows returns a copy of the committed rows in sequence order.
func (c *Chain) Rows() []Row {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Row, len(c.rows))
	copy(out, c.rows)
	return out
}

// Len returns the number of rows in the chain.
func (c *Chain) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.rows)
}

// Redact rewrites the payload of the row at seq in place per the
// §12.8 GDPR erasure path, marks the row Redacted, and stores the
// signed receipt. The row's Hash is left unchanged so the verifier
// observes a content-hash break that the receipt explains. Returns
// an error when seq is out of range.
func (c *Chain) Redact(seq uint64, newPayload json.RawMessage, receipt RedactionReceipt) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if seq == 0 || seq > uint64(len(c.rows)) {
		return fmt.Errorf("audit: redact seq %d out of range [1,%d]", seq, len(c.rows))
	}
	idx := seq - 1
	receipt.RedactedSeq = seq
	receipt.OriginalHash = c.rows[idx].Hash
	c.rows[idx].Payload = newPayload
	c.rows[idx].Redacted = true
	c.receipts[seq] = receipt
	return nil
}

// VerifyResult captures the outcome of a chain walk.
type VerifyResult struct {
	// Integrity is the overall chain state.
	Integrity ChainIntegrity

	// BreakSeq is the sequence number of the first row at which a
	// break was detected. Zero when Integrity is ChainVerified or
	// the chain is empty.
	BreakSeq uint64

	// Detail is a human-readable description of the break.
	Detail string
}

// Verify walks the chain and reports its §11.7 integrity state.
//
// The verifier checks, for each row:
//   - the content hash matches H(canonical_bytes(row)); a mismatch
//     on a non-redacted row is ChainBroken; a mismatch on a redacted
//     row carrying a valid receipt is ChainRedactedGDPR.
//   - the prev_hash links to the predecessor via linkHash; a
//     mismatch is ChainBroken.
//
// An empty chain is ChainVerified (nothing to break).
func (c *Chain) Verify() VerifyResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.rows) == 0 {
		return VerifyResult{Integrity: ChainVerified}
	}

	redactionSeen := false
	prevRedacted := false
	for i, row := range c.rows {
		// Cross-tenant rows must never appear in a tenant chain.
		if row.TenantID != c.tenantID {
			return VerifyResult{
				Integrity: ChainBroken,
				BreakSeq:  row.Seq,
				Detail:    fmt.Sprintf("row %d belongs to tenant %q, not %q", row.Seq, row.TenantID, c.tenantID),
			}
		}
		// Content hash check. A redacted row legitimately fails the
		// content-hash recomputation (its payload was rewritten in
		// place, but Hash was preserved as the pre-redaction value).
		// A valid signed receipt reclassifies the break as lawful.
		thisRedacted := false
		want := computeHash(row)
		if want != row.Hash {
			if row.Redacted {
				rcpt, ok := c.receipts[row.Seq]
				if ok && rcpt.OriginalHash == row.Hash && rcpt.Signature != "" {
					redactionSeen = true
					thisRedacted = true
				} else {
					return VerifyResult{
						Integrity: ChainBroken,
						BreakSeq:  row.Seq,
						Detail:    fmt.Sprintf("row %d redacted without a valid receipt", row.Seq),
					}
				}
			} else {
				return VerifyResult{
					Integrity: ChainBroken,
					BreakSeq:  row.Seq,
					Detail:    fmt.Sprintf("row %d content hash mismatch", row.Seq),
				}
			}
		}
		// Link check. The genesis row links to the sentinel. A row
		// whose predecessor was lawfully redacted carries a stale
		// prev_hash (the redaction rewrote the predecessor's
		// canonical bytes); that break is the lawful downstream
		// consequence of the erasure and is tolerated.
		if i == 0 {
			if row.PrevHash != GenesisPrevHash {
				return VerifyResult{
					Integrity: ChainBroken,
					BreakSeq:  row.Seq,
					Detail:    fmt.Sprintf("genesis row %d prev_hash is not the sentinel", row.Seq),
				}
			}
		} else if !prevRedacted {
			if row.PrevHash != linkHash(c.rows[i-1]) {
				return VerifyResult{
					Integrity: ChainBroken,
					BreakSeq:  row.Seq,
					Detail:    fmt.Sprintf("row %d prev_hash does not link to row %d", row.Seq, row.Seq-1),
				}
			}
		}
		prevRedacted = thisRedacted
	}
	// A lawfully-receipted §12.8 GDPR redaction is the redacted_gdpr
	// chainIntegrity bucket, distinct from rechained_post_outage (the
	// post-outage deferred-writes rechain). The collapsed verdict must
	// agree with the per-row classifyRow/VerifyRows classification so a
	// consumer tallying the §25.9 chainIntegrityReport does not miscount a
	// GDPR redaction as an outage rechain.
	// spec: §25.9 (redacted_gdpr is the authorized-discontinuity bucket).
	if redactionSeen {
		return VerifyResult{Integrity: ChainRedactedGDPR, Detail: "chain contains lawful GDPR redactions"}
	}
	return VerifyResult{Integrity: ChainVerified}
}

// VerifyRows walks the chain and returns the §11.7 ChainIntegrity of
// each row keyed by its sequence number. Unlike Verify, which collapses
// the chain to a single overall verdict and stops at the first break,
// this labels every row independently so the §25.9 query API can carry
// a per-event `chainIntegrity` field and tally the envelope
// `chainIntegrityReport`.
//
// Per-row classification:
//   - verified              — content hash matches and the row links to
//     its predecessor (or, for the genesis row, to the sentinel).
//   - redacted_gdpr         — content-hash break on a §12.8-redacted row
//     carrying a valid signed receipt; the discontinuity is lawful.
//   - gap_suspected         — the sequence number jumps relative to the
//     preceding row (e.g. #1000 then #1150), the §25.9 temporal-gap
//     signal for a degraded-mode write window.
//   - broken                — content-hash mismatch with no receipt, a
//     link mismatch with no sequence jump, or a cross-tenant row.
//
// The walk passes nil receipts when invoked on a chain built via
// ChainFromRows, matching Store.Verify; a redacted row without an
// in-memory receipt is therefore reported `broken`, which is the
// spec-correct verdict for an unauthenticated discontinuity.
//
// spec: §25.9 lines 3670-3679 (per-row chainIntegrity enum and temporal
// gap detection).
func (c *Chain) VerifyRows() map[uint64]ChainIntegrity {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make(map[uint64]ChainIntegrity, len(c.rows))
	for i, row := range c.rows {
		out[row.Seq] = c.classifyRow(i, row)
	}
	return out
}

// classifyRow returns the §11.7 ChainIntegrity for the row at index i.
// The caller holds c.mu.
func (c *Chain) classifyRow(i int, row Row) ChainIntegrity {
	if row.TenantID != c.tenantID {
		return ChainBroken
	}
	// Content-hash check. A redacted row legitimately fails the
	// recomputation (its payload was rewritten in place while Hash was
	// preserved); a valid signed receipt reclassifies the break as lawful.
	thisRedacted := false
	if computeHash(row) != row.Hash {
		if row.Redacted {
			rcpt, ok := c.receipts[row.Seq]
			if ok && rcpt.OriginalHash == row.Hash && rcpt.Signature != "" {
				thisRedacted = true
			} else {
				return ChainBroken
			}
		} else {
			return ChainBroken
		}
	}
	// Sequence-gap detection precedes the link check: a jump in the
	// sequence number is the §25.9 temporal-gap signal, distinct from a
	// tamper-broken link.
	if i > 0 && row.Seq != c.rows[i-1].Seq+1 {
		return ChainGapSuspected
	}
	// Link check. The genesis row links to the sentinel; a row whose
	// predecessor was lawfully redacted carries a stale prev_hash (the
	// redaction rewrote the predecessor's canonical bytes), tolerated as
	// the lawful downstream consequence of the erasure.
	if i == 0 {
		if row.PrevHash != GenesisPrevHash {
			return ChainBroken
		}
	} else {
		prev := c.rows[i-1]
		prevRedacted := false
		if prev.Redacted {
			rcpt, ok := c.receipts[prev.Seq]
			prevRedacted = ok && rcpt.OriginalHash == prev.Hash && rcpt.Signature != ""
		}
		if !prevRedacted && row.PrevHash != linkHash(prev) {
			return ChainBroken
		}
	}
	if thisRedacted {
		return ChainRedactedGDPR
	}
	return ChainVerified
}

// ChainSet is a goroutine-safe collection of per-tenant chains. It
// is the production AuditSink substrate: every tenant gets its own
// independent §11.7 chain so one tenant's redaction cannot break
// another tenant's chain.
type ChainSet struct {
	mu     sync.Mutex
	chains map[string]*Chain
}

// NewChainSet returns an empty ChainSet.
func NewChainSet() *ChainSet {
	return &ChainSet{chains: map[string]*Chain{}}
}

// Append commits a row to tenantID's chain, creating the chain on
// first use.
func (s *ChainSet) Append(tenantID, eventType string, payload json.RawMessage, now time.Time) Row {
	s.mu.Lock()
	chain, ok := s.chains[tenantID]
	if !ok {
		chain = NewChain(tenantID)
		s.chains[tenantID] = chain
	}
	s.mu.Unlock()
	return chain.Append(eventType, payload, now)
}

// Chain returns the chain for tenantID, or nil when no rows have
// been committed for that tenant.
func (s *ChainSet) Chain(tenantID string) *Chain {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chains[tenantID]
}

// Tenants returns the tenant ids with at least one committed row,
// sorted.
func (s *ChainSet) Tenants() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.chains))
	for t := range s.chains {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// ErrUnknownChain is returned when a chain lookup misses.
var ErrUnknownChain = errors.New("audit: no chain for tenant")

// keep strings import used even if the file is trimmed later.
var _ = strings.TrimSpace
