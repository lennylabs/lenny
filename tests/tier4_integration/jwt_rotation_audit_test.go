// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §10.3 JWT signing-key rotation audit
// pipeline. Each Rotate or RetireExpired call against the
// RotatingVerifier emits one `platform.jwt_signing_key_rotated`
// audit row through the gateway's jwtaudit Observer. The test wires
// the observer against an in-process pkg/audit chain set, exercises
// the full rotation lifecycle, and asserts the chain carries one
// row per transition with the canonical wire fields.

package tier4_integration_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/audit/jwtaudit"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
)

// spec: 10.3
// diagnosis: a Rotate against the RotatingVerifier did not commit a
// `platform.jwt_signing_key_rotated` row on the platform tenant
// chain, or the row's payload carried the wrong from/to kid or
// transition. The §13.3 audit pipeline depends on a one-to-one
// mapping between rotation lifecycle transitions and audit rows.
func TestJWTRotationEmitsPromoteCurrentAuditRow(t *testing.T) {
	t.Parallel()
	chains := audit.NewChainSet()
	appender := policy.NewChainSetAppender(chains, nil)
	obs := jwtaudit.NewObserver(appender)

	cur := jwt.NewHMACSigner("kid-rot-1", []byte("secret-1"))
	next := jwt.NewHMACSigner("kid-rot-2", []byte("secret-2"))
	v := jwt.NewRotatingVerifier(cur, 24*time.Hour)
	v.SetObserver(obs)

	if err := v.Rotate(next); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	rows := chains.Chain(jwtaudit.PlatformTenantID).Rows()
	if len(rows) != 1 {
		t.Fatalf("platform chain rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.EventType != jwtaudit.EventTypeJWTSigningKeyRotated {
		t.Errorf("event_type = %q, want %q", row.EventType, jwtaudit.EventTypeJWTSigningKeyRotated)
	}
	if row.TenantID != jwtaudit.PlatformTenantID {
		t.Errorf("tenant_id = %q, want %q", row.TenantID, jwtaudit.PlatformTenantID)
	}
	var payload jwtaudit.Payload
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Transition != jwt.TransitionPromoteCurrent {
		t.Errorf("payload.transition = %q, want %q",
			payload.Transition, jwt.TransitionPromoteCurrent)
	}
	if payload.FromKeyID != "kid-rot-1" {
		t.Errorf("payload.from_key_id = %q, want kid-rot-1", payload.FromKeyID)
	}
	if payload.ToKeyID != "kid-rot-2" {
		t.Errorf("payload.to_key_id = %q, want kid-rot-2", payload.ToKeyID)
	}
	if payload.At.IsZero() {
		t.Errorf("payload.at is the zero time; want the rotation instant")
	}
}

// spec: 10.3
// diagnosis: RetireExpired pruned a previous key without emitting a
// `platform.jwt_signing_key_rotated` row with transition=retire_previous,
// or the row carried a non-empty to_key_id. The §13.3 audit pipeline
// must record retire_previous transitions with the pruned kid as
// from_key_id and no successor in to_key_id.
func TestJWTRotationEmitsRetirePreviousAuditRow(t *testing.T) {
	t.Parallel()
	chains := audit.NewChainSet()
	appender := policy.NewChainSetAppender(chains, nil)
	obs := jwtaudit.NewObserver(appender)

	cur := jwt.NewHMACSigner("kid-retire-1", []byte("secret-1"))
	// Use a 1ns overlap window so the first RetireExpired sweep
	// prunes the previous key without sleeping.
	v := jwt.NewRotatingVerifier(cur, time.Nanosecond)
	v.SetObserver(obs)

	if err := v.Rotate(jwt.NewHMACSigner("kid-retire-2", []byte("secret-2"))); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	// Allow the 1ns overlap to lapse on the real wall clock.
	time.Sleep(time.Millisecond)
	if removed := v.RetireExpired(); removed != 1 {
		t.Fatalf("RetireExpired removed %d, want 1", removed)
	}

	rows := chains.Chain(jwtaudit.PlatformTenantID).Rows()
	if len(rows) != 2 {
		t.Fatalf("platform chain rows = %d, want 2 (promote + retire)", len(rows))
	}
	// Second row is the retire_previous event.
	row := rows[1]
	if row.EventType != jwtaudit.EventTypeJWTSigningKeyRotated {
		t.Errorf("retire row event_type = %q, want %q",
			row.EventType, jwtaudit.EventTypeJWTSigningKeyRotated)
	}
	var payload jwtaudit.Payload
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		t.Fatalf("unmarshal retire payload: %v", err)
	}
	if payload.Transition != jwt.TransitionRetirePrevious {
		t.Errorf("retire payload.transition = %q, want %q",
			payload.Transition, jwt.TransitionRetirePrevious)
	}
	if payload.FromKeyID != "kid-retire-1" {
		t.Errorf("retire payload.from_key_id = %q, want kid-retire-1", payload.FromKeyID)
	}
	if payload.ToKeyID != "" {
		t.Errorf("retire payload.to_key_id = %q, want empty (no successor)", payload.ToKeyID)
	}
}

// spec: 10.3
// diagnosis: the audit chain did not retain hash-chain integrity
// across two §10.3 rotation rows. Per §11.7 every audit append
// extends the chain; the verifier must report verified after both
// rows commit. A broken chain would mask a forged rotation row.
func TestJWTRotationAuditChainStaysVerified(t *testing.T) {
	t.Parallel()
	chains := audit.NewChainSet()
	appender := policy.NewChainSetAppender(chains, nil)
	obs := jwtaudit.NewObserver(appender)

	v := jwt.NewRotatingVerifier(jwt.NewHMACSigner("kid-chain-1", []byte("s1")), time.Nanosecond)
	v.SetObserver(obs)
	if err := v.Rotate(jwt.NewHMACSigner("kid-chain-2", []byte("s2"))); err != nil {
		t.Fatalf("Rotate 1: %v", err)
	}
	time.Sleep(time.Millisecond)
	if removed := v.RetireExpired(); removed != 1 {
		t.Fatalf("RetireExpired removed %d, want 1", removed)
	}

	chain := chains.Chain(jwtaudit.PlatformTenantID)
	res := chain.Verify()
	if res.Integrity != audit.ChainVerified {
		t.Errorf("chain integrity after rotation lifecycle = %q, want verified (%s)",
			res.Integrity, res.Detail)
	}
}
