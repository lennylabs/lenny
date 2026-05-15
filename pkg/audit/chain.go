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
)

// GenesisPrevHash is the §11.7 sentinel prev_hash stamped on the
// first row of every per-tenant chain. The verifier walks back to
// this anchor; writer and verifier MUST agree on the value, so it
// is a single exported constant.
const GenesisPrevHash = "0000000000000000000000000000000000000000000000000000000000000000"

// Row is one §11.7 audit hash-chain entry. Rows are append-only:
// once written, only the §12.8 GDPR redaction path may rewrite a
// row's Payload in place, and that path attaches a RedactionReceipt.
type Row struct {
	// Seq is the per-tenant monotonic sequence number, starting at 1.
	Seq uint64 `json:"seq"`

	// TenantID scopes the row to a single tenant's chain.
	TenantID string `json:"tenant_id"`

	// EventType is the §11.7 audit event type
	// (e.g., `admin.tenant.created`).
	EventType string `json:"event_type"`

	// Payload is the canonical event body. Redaction rewrites this
	// in place per §12.8.
	Payload json.RawMessage `json:"payload"`

	// Timestamp is the UTC instant the row was committed.
	Timestamp time.Time `json:"timestamp"`

	// PrevHash links this row to its predecessor:
	// `prev_hash = H(prev_row.Hash || canonical_bytes(prev_row))`.
	// The genesis row carries GenesisPrevHash.
	PrevHash string `json:"prev_hash"`

	// Hash is this row's content hash: `H(canonical_bytes(row))`
	// computed over every field except Hash itself.
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

// canonicalBytes returns the deterministic byte representation of a
// row over which the content hash is computed — every field except
// Hash. encoding/json with struct field order is deterministic for
// a fixed struct definition.
func canonicalBytes(r Row) []byte {
	type canonical struct {
		Seq       uint64          `json:"seq"`
		TenantID  string          `json:"tenant_id"`
		EventType string          `json:"event_type"`
		Payload   json.RawMessage `json:"payload"`
		Timestamp string          `json:"timestamp"`
		PrevHash  string          `json:"prev_hash"`
		Redacted  bool            `json:"redacted"`
	}
	b, _ := json.Marshal(canonical{
		Seq:       r.Seq,
		TenantID:  r.TenantID,
		EventType: r.EventType,
		Payload:   r.Payload,
		Timestamp: r.Timestamp.UTC().Format(time.RFC3339Nano),
		PrevHash:  r.PrevHash,
		Redacted:  r.Redacted,
	})
	return b
}

// computeHash returns the §11.7 content hash of a row.
func computeHash(r Row) string {
	sum := sha256.Sum256(canonicalBytes(r))
	return hex.EncodeToString(sum[:])
}

// linkHash returns the prev_hash a successor row must carry per
// §11.7: `H(prev_row.Hash || canonical_bytes(prev_row))`.
func linkHash(prev Row) string {
	h := sha256.New()
	h.Write([]byte(prev.Hash))
	h.Write(canonicalBytes(prev))
	return hex.EncodeToString(h.Sum(nil))
}

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

// TenantID returns the chain's tenant scope.
func (c *Chain) TenantID() string { return c.tenantID }

// Append commits a new row carrying eventType + payload. Returns the
// committed row. The chain assigns Seq, PrevHash, Hash, and the
// timestamp (using now).
func (c *Chain) Append(eventType string, payload json.RawMessage, now time.Time) Row {
	c.mu.Lock()
	defer c.mu.Unlock()
	row := Row{
		Seq:       uint64(len(c.rows)) + 1,
		TenantID:  c.tenantID,
		EventType: eventType,
		Payload:   payload,
		Timestamp: now.UTC(),
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
	if redactionSeen {
		return VerifyResult{Integrity: ChainRechainedPostOutage, Detail: "chain contains lawful GDPR redactions"}
	}
	return VerifyResult{Integrity: ChainVerified}
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
