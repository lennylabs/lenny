// SPDX-License-Identifier: MIT

// Package idempotency encodes the §11.5 idempotency primitives that
// are pure: the 128-character key length cap, the SHA-256 body-hash
// canonicalisation, the same-key-different-body reuse detection that
// produces the 422 IDEMPOTENCY_KEY_REUSED envelope, the 24-hour TTL
// constant, and the tenant-scoped Key value.
//
// The Postgres-backed `idempotency_keys` table, the GC job that
// drops expired rows, and the HTTP/MCP transport handlers that read
// the `Idempotency-Key` header / `idempotencyKey` field all live in
// later phases that import this package.
package idempotency

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// MaxKeyLength is the §11.5 cap on the Idempotency-Key string. Keys
// longer than this are rejected at the request boundary.
const MaxKeyLength = 128

// TTL is the §11.5 retention window for an idempotency record. After
// this window the GC job drops the row and the key may be reused.
const TTL = 24 * time.Hour

// Key is a tenant-scoped idempotency key. The tenant scope is part of
// the key per §11.5: "the same key string used by different tenants
// is treated independently."
type Key struct {
	TenantID string
	Value    string
}

// Validate reports the §11.5 format errors. Returns nil when the key
// is admissible at the request boundary.
func (k Key) Validate() error {
	if k.TenantID == "" {
		return fmt.Errorf("idempotency: TenantID is required")
	}
	if k.Value == "" {
		return fmt.Errorf("idempotency: key value is required")
	}
	if len(k.Value) > MaxKeyLength {
		return &KeyTooLongError{Length: len(k.Value), Max: MaxKeyLength}
	}
	return nil
}

// KeyTooLongError is the typed error Validate returns when the key
// length exceeds §11.5's 128-character cap. The gateway maps this to
// 400 INVALID_IDEMPOTENCY_KEY at the HTTP boundary.
type KeyTooLongError struct {
	Length int
	Max    int
}

func (e *KeyTooLongError) Error() string {
	return fmt.Sprintf("idempotency: key length %d exceeds maximum %d (§11.5)", e.Length, e.Max)
}

// HashBody computes the SHA-256 hex digest of body. The result is the
// canonical body hash stored alongside the idempotency key. §11.5
// uses this hash to detect same-key-different-body reuse.
func HashBody(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// Record captures one stored idempotency entry, mirroring the
// `idempotency_keys` Postgres row.
type Record struct {
	Key      Key
	BodyHash string
	// Response captures the cached response payload (status code +
	// body) the gateway replays for duplicate requests.
	Response Response
	// StoredAt is the wall-clock time the record was first written.
	// The GC job uses this to enforce the §11.5 24-hour TTL.
	StoredAt time.Time
}

// Response is the cached HTTP response payload the gateway returns
// for an idempotent duplicate. The exact shape mirrors the §15.1
// envelope: status + headers + body.
type Response struct {
	StatusCode int
	Headers    map[string]string
	Body       []byte
}

// IsExpired reports whether the record has aged out of the §11.5 TTL
// window. spec: §11.5 line 277.
//
// MUST-NOT-RELAX: this method returns false for the zero Record
// (StoredAt.IsZero()) by design. Every store implementation
// (MemoryStore.Get, pgstore.Store.Get) layers a "not found" check
// *before* calling IsExpired, so the zero-StoredAt branch is never
// reached in production. A future refactor that drops the not-found
// check would silently fall through this branch and treat a missing
// record as "fresh", admitting a same-key request as a Replay against
// an empty body hash. Keep the not-found gate at the callsite.
func (r Record) IsExpired(now time.Time) bool {
	return !r.StoredAt.IsZero() && now.Sub(r.StoredAt) > TTL
}

// DetectReuse implements the §11.5 same-key-different-body check. The
// caller queries the idempotency-key store and passes the previously
// stored Record (or zero value when no prior request exists) and the
// hash of the inbound body.
//
// Returns:
//   - (StoreNew, "")               — no prior record; the caller
//     should persist the new record before executing the operation.
//   - (Replay, "")                 — prior record exists and the body
//     hash matches; the caller MUST replay the cached Response without
//     re-executing the operation.
//   - (Reject, <reason>)           — prior record exists with a
//     different body hash; the caller returns *ReuseError → 422
//     IDEMPOTENCY_KEY_REUSED.
//   - (StoreNew, "")               — prior record exists but is
//     expired; the caller treats it as if it does not exist.
type Action string

const (
	ActionStoreNew Action = "store_new"
	ActionReplay   Action = "replay"
	ActionReject   Action = "reject"
)

// DetectReuse runs the §11.5 same-key-different-body check. The
// `stored` argument is the previously-stored Record for the inbound
// key (zero value when no prior record exists). The `inboundHash` is
// the SHA-256 digest of the new request body. `now` is the current
// wall-clock time.
func DetectReuse(stored Record, inboundHash string, now time.Time) (Action, error) {
	if stored.Key.Value == "" || stored.StoredAt.IsZero() {
		return ActionStoreNew, nil
	}
	if stored.IsExpired(now) {
		return ActionStoreNew, nil
	}
	if stored.BodyHash == inboundHash {
		return ActionReplay, nil
	}
	return ActionReject, &ReuseError{
		Key:         stored.Key,
		StoredHash:  stored.BodyHash,
		InboundHash: inboundHash,
	}
}

// ReuseError is the typed error DetectReuse returns when a key is
// reused with a different body. The gateway maps it to the §11.5
// 422 IDEMPOTENCY_KEY_REUSED envelope.
type ReuseError struct {
	Key         Key
	StoredHash  string
	InboundHash string
}

func (e *ReuseError) Error() string {
	return fmt.Sprintf("idempotency: key %s/%s was previously used with a different body (stored hash %s ≠ inbound hash %s) — §11.5 IDEMPOTENCY_KEY_REUSED", e.Key.TenantID, e.Key.Value, e.StoredHash, e.InboundHash)
}

// Code returns the HTTP status code the gateway surfaces for this
// error per §11.5.
func (e *ReuseError) Code() int { return 422 }

// ErrorCode returns the spec-mandated error-code string for the
// §15.1 error envelope.
func (e *ReuseError) ErrorCode() string { return "IDEMPOTENCY_KEY_REUSED" }
