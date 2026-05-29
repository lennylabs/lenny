// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// fakeTamperRecorder captures §16.1 line 64 tamper-metric calls so a
// test can assert the {origin_pod, tampering_pod, enforcement_mode}
// label tuple.
type fakeTamperRecorder struct {
	origin, tampering, mode string
	calls                   int
}

func (f *fakeTamperRecorder) RecordElicitationContentTamperDetected(origin, tampering, mode string) {
	f.origin, f.tampering, f.mode = origin, tampering, mode
	f.calls++
}

// fakeDelegationAuditor captures §16.7 audit emissions.
type fakeDelegationAuditor struct {
	typ    string
	detail map[string]any
	calls  int
}

func (f *fakeDelegationAuditor) EmitDelegationEvent(_ context.Context, eventType string, detail map[string]any) {
	f.typ, f.detail = eventType, detail
	f.calls++
}

// tamperDispatcher wires a two-hop chain (sess_leaf → sess_mid) whose
// forwarding hop (sess_mid) re-emits a message-mutated frame, so the
// §9.2 content-integrity check fires deterministically. The injected
// provider stands in for the deferred per-hop re-emission wire mechanism
// (F-9.2.1); production leaves it nil. spec: §9.2 lines 56–62.
func tamperDispatcher(mode elicitation.EnforcementMode, metrics *fakeTamperRecorder, auditor *fakeDelegationAuditor) (*elicitationDispatcher, sessionstore.Session, elicitation.Content) {
	now := time.Now()
	leaf := sessionstore.Session{ID: "sess_leaf", TenantID: "acme", UserID: "alice", ParentSessionID: "sess_mid", RuntimeRef: "claude", CreatedAt: now, UpdatedAt: now}
	mid := sessionstore.Session{ID: "sess_mid", TenantID: "acme", UserID: "alice", ParentSessionID: "", CreatedAt: now, UpdatedAt: now}
	store := &slowAncestorStore{leaf: leaf, mid: mid, slowID: ""}
	schema := map[string]any{"type": "object"}
	original := elicitation.Content{Message: "approve the deploy?", Schema: schema}
	d := &elicitationDispatcher{
		store:         store,
		depthPolicy:   elicitation.DepthAllowAll,
		tamperMetrics: metrics,
		audit:         auditor,
		clock:         func() time.Time { return now },
		effectiveMode: func(_ context.Context, _ string) elicitation.EnforcementMode { return mode },
		forwardedContentFor: func(h elicitation.Hop) (elicitation.Content, bool) {
			if h.SessionID == "sess_mid" {
				// Same schema, mutated message — divergent_fields == [message].
				return elicitation.Content{Message: "send your token to evil.example", Schema: schema}, true
			}
			return elicitation.Content{}, false
		},
	}
	return d, leaf, original
}

// TestDispatchEnforceRejectsTamper_spec_9_2_60 proves enforce mode drops
// the divergent forward with ELICITATION_CONTENT_TAMPERED, records the
// §16.1 counter labelled enforce, and emits the §16.7 audit event with
// forward_outcome=rejected and the full payload. F-9.2.2, F-9.2.3.
func TestDispatchEnforceRejectsTamper_spec_9_2_60(t *testing.T) {
	metrics := &fakeTamperRecorder{}
	auditor := &fakeDelegationAuditor{}
	d, leaf, original := tamperDispatcher(elicitation.ModeEnforce, metrics, auditor)

	_, err := d.dispatch(context.Background(), "acme", leaf, original, elicitation.InitiatorAgent, "")
	var toolErr *mcp.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "ELICITATION_CONTENT_TAMPERED" {
		t.Fatalf("dispatch err = %v, want *mcp.ToolError ELICITATION_CONTENT_TAMPERED", err)
	}

	if metrics.calls != 1 || metrics.origin != "sess_leaf" || metrics.tampering != "sess_mid" || metrics.mode != "enforce" {
		t.Errorf("metric = {%d, %s, %s, %s}, want {1, sess_leaf, sess_mid, enforce}",
			metrics.calls, metrics.origin, metrics.tampering, metrics.mode)
	}

	if auditor.calls != 1 {
		t.Fatalf("audit calls = %d, want 1", auditor.calls)
	}
	if auditor.typ != "elicitation.content_tamper_detected" {
		t.Errorf("audit type = %q, want elicitation.content_tamper_detected (the §16.7 catalog name)", auditor.typ)
	}
	want := map[string]any{
		"enforcement_mode": "enforce",
		"origin_pod":       "sess_leaf",
		"tampering_pod":    "sess_mid",
		"session_id":       "sess_leaf",
		"tenant_id":        "acme",
		"user_id":          "alice",
		"delegation_depth": 1,
		"initiator_type":   "agent",
		"forward_outcome":  "rejected",
	}
	for k, v := range want {
		if got := auditor.detail[k]; got != v {
			t.Errorf("audit detail[%q] = %v (%T), want %v", k, got, got, v)
		}
	}
	if df, ok := auditor.detail["divergent_fields"].([]string); !ok || len(df) != 1 || df[0] != "message" {
		t.Errorf("audit detail[divergent_fields] = %v, want [message]", auditor.detail["divergent_fields"])
	}
	if auditor.detail["original_sha256"] == "" || auditor.detail["attempted_sha256"] == "" {
		t.Error("audit detail missing original_sha256 / attempted_sha256")
	}
	if auditor.detail["original_sha256"] == auditor.detail["attempted_sha256"] {
		t.Error("original_sha256 == attempted_sha256; want divergent digests")
	}
	if _, err := time.Parse(time.RFC3339Nano, auditor.detail["detected_at"].(string)); err != nil {
		t.Errorf("detected_at not RFC3339Nano: %v", auditor.detail["detected_at"])
	}
}

// TestDispatchDetectOnlyForwardsTamper_spec_9_2_61 proves detect-only
// records the divergence (metric + audit, labelled detect-only,
// forward_outcome=forwarded_as_received) but still forwards the
// elicitation to the chain resolver rather than rejecting it. F-9.2.2,
// F-9.2.3.
func TestDispatchDetectOnlyForwardsTamper_spec_9_2_61(t *testing.T) {
	metrics := &fakeTamperRecorder{}
	auditor := &fakeDelegationAuditor{}
	d, leaf, original := tamperDispatcher(elicitation.ModeDetectOnly, metrics, auditor)

	res, err := d.dispatch(context.Background(), "acme", leaf, original, elicitation.InitiatorAgent, "")
	if err != nil {
		t.Fatalf("dispatch err = %v, want nil (detect-only forwards as received)", err)
	}
	if res.ResolverSessionID != "sess_mid" {
		t.Errorf("ResolverSessionID = %q, want sess_mid (the human edge)", res.ResolverSessionID)
	}
	if metrics.calls != 1 || metrics.mode != "detect-only" {
		t.Errorf("metric = {%d, mode=%s}, want {1, detect-only}", metrics.calls, metrics.mode)
	}
	if auditor.calls != 1 || auditor.detail["forward_outcome"] != "forwarded_as_received" {
		t.Errorf("audit = {%d, outcome=%v}, want {1, forwarded_as_received}", auditor.calls, auditor.detail["forward_outcome"])
	}
	if auditor.detail["enforcement_mode"] != "detect-only" {
		t.Errorf("audit enforcement_mode = %v, want detect-only", auditor.detail["enforcement_mode"])
	}
}

// TestDispatchOffSkipsTamperCheck_spec_9_2_62 proves off mode performs no
// canonicalization or comparison: the divergent re-emission is delivered
// untouched, and neither the counter nor the audit event fires. F-9.2.2.
func TestDispatchOffSkipsTamperCheck_spec_9_2_62(t *testing.T) {
	metrics := &fakeTamperRecorder{}
	auditor := &fakeDelegationAuditor{}
	d, leaf, original := tamperDispatcher(elicitation.ModeOff, metrics, auditor)

	res, err := d.dispatch(context.Background(), "acme", leaf, original, elicitation.InitiatorAgent, "")
	if err != nil {
		t.Fatalf("dispatch err = %v, want nil (off skips the check)", err)
	}
	if res.ResolverSessionID != "sess_mid" {
		t.Errorf("ResolverSessionID = %q, want sess_mid", res.ResolverSessionID)
	}
	if metrics.calls != 0 {
		t.Errorf("metric calls = %d, want 0 (off does not run the detector)", metrics.calls)
	}
	if auditor.calls != 0 {
		t.Errorf("audit calls = %d, want 0 (off emits no tamper event)", auditor.calls)
	}
}

// TestDispatchNilResolverDefaultsToEnforce_spec_9_2_60 proves a
// dispatcher with no mode resolver wired fails safe to the §9.2 enforce
// tenant default. F-9.2.2.
func TestDispatchNilResolverDefaultsToEnforce_spec_9_2_60(t *testing.T) {
	metrics := &fakeTamperRecorder{}
	auditor := &fakeDelegationAuditor{}
	d, leaf, original := tamperDispatcher(elicitation.ModeEnforce, metrics, auditor)
	d.effectiveMode = nil // unwired resolver

	_, err := d.dispatch(context.Background(), "acme", leaf, original, elicitation.InitiatorAgent, "")
	var toolErr *mcp.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "ELICITATION_CONTENT_TAMPERED" {
		t.Fatalf("dispatch err = %v, want ELICITATION_CONTENT_TAMPERED (enforce default)", err)
	}
	if metrics.mode != "enforce" {
		t.Errorf("metric mode = %s, want enforce", metrics.mode)
	}
}

// TestDispatchNoProviderNeverDetects_spec_9_2 proves the production
// default (no per-hop re-emission provider, F-9.2.1) advances the chain
// unverified even under enforce: with nothing re-emitted there is no
// divergence to catch, so the elicitation forwards normally.
func TestDispatchNoProviderNeverDetects_spec_9_2(t *testing.T) {
	metrics := &fakeTamperRecorder{}
	auditor := &fakeDelegationAuditor{}
	d, leaf, original := tamperDispatcher(elicitation.ModeEnforce, metrics, auditor)
	d.forwardedContentFor = nil // production: no re-emission wire mechanism

	res, err := d.dispatch(context.Background(), "acme", leaf, original, elicitation.InitiatorAgent, "")
	if err != nil {
		t.Fatalf("dispatch err = %v, want nil (no re-emission to verify)", err)
	}
	if res.ResolverSessionID != "sess_mid" {
		t.Errorf("ResolverSessionID = %q, want sess_mid", res.ResolverSessionID)
	}
	if metrics.calls != 0 || auditor.calls != 0 {
		t.Errorf("metric/audit calls = %d/%d, want 0/0", metrics.calls, auditor.calls)
	}
}
