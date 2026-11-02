// SPDX-License-Identifier: MIT

package connectoroauth

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// TestNewStateIsRandomUnpaddedBase64URL pins the §9.3 `state` format: an
// unpadded base64url value of at least 128 random bits with no signature
// segment, distinct on every call.
//
// spec: §9.3 (Connector Definition and OAuth/OIDC) "`state` parameter (anti-CSRF)"
func TestNewStateIsRandomUnpaddedBase64URL_spec_9_3(t *testing.T) {
	seen := make(map[string]struct{}, 256)
	for range 256 {
		state := NewState()
		raw, err := base64.RawURLEncoding.DecodeString(state)
		if err != nil {
			t.Fatalf("NewState() = %q is not unpadded base64url: %v", state, err)
		}
		if len(raw)*8 < 128 {
			t.Fatalf("NewState() decodes to %d bits, want at least 128", len(raw)*8)
		}
		if strings.Contains(state, ".") {
			t.Fatalf("NewState() = %q carries a signature segment", state)
		}
		if _, dup := seen[state]; dup {
			t.Fatalf("NewState() repeated %q", state)
		}
		seen[state] = struct{}{}
	}
}

func sampleFlow(now time.Time) FlowContext {
	return FlowContext{
		ConnectorID:  "github",
		TenantID:     "acme",
		UserID:       "alice@acme.com",
		SessionID:    "sess_abc",
		CodeVerifier: "verifier-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		RedirectURI:  "https://gw.acme.com/v1/admin/connectors/oauth/callback",
		Scopes:       []string{"repo"},
		CreatedAt:    now,
	}
}

// TestStateStorePutConsumeRoundTrip stores a flow and consumes it once.
func TestStateStorePutConsumeRoundTrip(t *testing.T) {
	store := NewMemoryStateStore()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	flow := sampleFlow(now)
	if err := store.Put("state-1", flow, DefaultStateTTL); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := store.Consume("state-1", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got.ConnectorID != "github" || got.UserID != "alice@acme.com" || got.CodeVerifier != flow.CodeVerifier {
		t.Fatalf("Consume returned the wrong flow context: %+v", got)
	}
}

func TestStateStoreUnknownStateRejected(t *testing.T) {
	store := NewMemoryStateStore()
	if _, err := store.Consume("never-issued", time.Now()); err != ErrStateUnknown {
		t.Fatalf("Consume of an unknown state: got %v, want ErrStateUnknown", err)
	}
}

// TestStateStoreExpiredStateRejected covers the §9.3 10-minute TTL: a
// callback that arrives after the TTL must be rejected.
func TestStateStoreExpiredStateRejected(t *testing.T) {
	store := NewMemoryStateStore()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	if err := store.Put("state-1", sampleFlow(now), DefaultStateTTL); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// One second past the 10-minute TTL.
	late := now.Add(DefaultStateTTL + time.Second)
	if _, err := store.Consume("state-1", late); err != ErrStateExpired {
		t.Fatalf("Consume past the TTL: got %v, want ErrStateExpired", err)
	}
	// An expired state cannot then be redeemed even at an earlier
	// clock: it was marked consumed.
	if _, err := store.Consume("state-1", now.Add(time.Minute)); err != ErrStateConsumed {
		t.Fatalf("Consume after an expiry: got %v, want ErrStateConsumed", err)
	}
}

// TestStateStoreReplayedStateRejected covers a replayed callback: a
// state consumed once must fail a second Consume, so a captured
// callback cannot be replayed into a second token exchange.
func TestStateStoreReplayedStateRejected(t *testing.T) {
	store := NewMemoryStateStore()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	if err := store.Put("state-1", sampleFlow(now), DefaultStateTTL); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := store.Consume("state-1", now.Add(time.Minute)); err != nil {
		t.Fatalf("first Consume: %v", err)
	}
	if _, err := store.Consume("state-1", now.Add(2*time.Minute)); err != ErrStateConsumed {
		t.Fatalf("replayed Consume: got %v, want ErrStateConsumed", err)
	}
}

func TestStateStoreSweep(t *testing.T) {
	store := NewMemoryStateStore()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	_ = store.Put("live", sampleFlow(now), DefaultStateTTL)
	_ = store.Put("stale", sampleFlow(now), DefaultStateTTL)
	_ = store.Put("used", sampleFlow(now), DefaultStateTTL)
	if _, err := store.Consume("used", now.Add(time.Minute)); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	// Sweep at a time past the TTL: "stale" and "used" go, "live"
	// stays only if still inside its TTL — here it is not, so all are
	// reclaimed. Sweep just past one minute keeps "live".
	dropped := store.Sweep(now.Add(2 * time.Minute))
	if dropped != 1 {
		t.Fatalf("Sweep at now+2m dropped %d, want 1 (the consumed entry)", dropped)
	}
	if _, err := store.Consume("live", now.Add(3*time.Minute)); err != nil {
		t.Fatalf("live entry was swept prematurely: %v", err)
	}
}

// spec: §9.3 — pending state entries have TTL=10 min. The
// in-memory store grows monotonically without a periodic Sweep; the
// gateway main schedules one every minute under the watchdog. This
// test asserts that repeated periodic Sweep calls reclaim consumed and
// expired entries without affecting still-live entries. F-9.3.16.
func TestStateStorePeriodicSweepReclaimsExpired_spec_9_3_157(t *testing.T) {
	store := NewMemoryStateStore()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	if err := store.Put("live", sampleFlow(now), DefaultStateTTL); err != nil {
		t.Fatalf("Put live: %v", err)
	}
	if err := store.Put("expired", sampleFlow(now), DefaultStateTTL); err != nil {
		t.Fatalf("Put expired: %v", err)
	}
	// Sweep before expiry must keep both.
	if dropped := store.Sweep(now.Add(time.Minute)); dropped != 0 {
		t.Fatalf("premature Sweep dropped %d, want 0", dropped)
	}
	// After the TTL, the expired one goes; the live one is still live
	// per its own put time (same now).
	if dropped := store.Sweep(now.Add(DefaultStateTTL + time.Second)); dropped != 2 {
		t.Fatalf("post-TTL Sweep dropped %d, want 2", dropped)
	}
	// A subsequent Consume of an already-swept entry must return
	// ErrStateUnknown rather than ErrStateExpired or ErrStateConsumed,
	// since the entry no longer exists in the map.
	if _, err := store.Consume("expired", now.Add(DefaultStateTTL+2*time.Second)); err != ErrStateUnknown {
		t.Fatalf("Consume after Sweep: got %v, want ErrStateUnknown", err)
	}
}
