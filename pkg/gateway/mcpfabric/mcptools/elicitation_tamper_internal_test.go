// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
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
// §9.2 content-integrity check fires deterministically. In v1 the
// gateway resolves the chain server-internally and intermediate pods
// re-emit nothing, so production leaves the provider nil; the injected
// provider is the forward-compatible enforcement seam these tests use to
// exercise the enforce and detect-only branches. spec: §9.2
// (gateway-origin binding; v1 structural enforcement).
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

// urlModeDispatcher wires a single-session dispatcher (no ancestors) with
// a url-mode allowlist and the injected drop/audit recorders, so the §9.2
// DOMAIN_NOT_ALLOWLISTED drop path can be exercised directly. The url-mode
// provenance check runs before buildHops, so the store is never reached on
// the drop path; it returns the raising session for completeness.
func urlModeDispatcher(allowlist elicitation.URLModeAllowlist, drops *recordingDispatcherDrop, auditor *fakeDelegationAuditor) (*elicitationDispatcher, sessionstore.Session) {
	now := time.Now()
	raising := sessionstore.Session{
		ID: "sess_leaf", TenantID: "acme", UserID: "alice",
		DelegationDepth: 2, CreatedAt: now, UpdatedAt: now,
	}
	store := &slowAncestorStore{leaf: raising, mid: sessionstore.Session{}, slowID: ""}
	d := &elicitationDispatcher{
		store:            store,
		depthPolicy:      elicitation.DepthAllowAll,
		urlModeAllowlist: allowlist,
		dropMetrics:      drops,
		audit:            auditor,
		clock:            func() time.Time { return now },
	}
	return d, raising
}

// recordingDispatcherDrop captures §9.1 drop-counter calls for the
// internal dispatcher tests.
type recordingDispatcherDrop struct{ reasons []string }

func (r *recordingDispatcherDrop) RecordElicitationDrop(reason string) {
	r.reasons = append(r.reasons, reason)
}

// TestDispatchURLModeDropWritesAuditRow_spec_16_7 proves the §9.2 url-mode
// DOMAIN_NOT_ALLOWLISTED drop (F-9.2.11) writes the §16.7
// elicitation.url_mode_domain_rejected audit row (F-EL3) with the staged
// payload fields, alongside the existing drop metric and the per-hop tool
// error. The audit row is the SIEM-visible record of the policy-relevant
// drop; without it the rejection is observable only through the metric.
//
// diagnosis: a failure here means an agent-initiated url-mode elicitation
// blocked for a disallowed domain is not auditable through the §16.7 path,
// so a security review cannot reconstruct the drop from the audit log.
//
// spec: §16.7 (elicitation.url_mode_domain_rejected); §9.2 line 86. F-EL3,
// F-9.2.11.
func TestDispatchURLModeDropWritesAuditRow_spec_16_7(t *testing.T) {
	drops := &recordingDispatcherDrop{}
	auditor := &fakeDelegationAuditor{}
	d, raising := urlModeDispatcher(elicitation.URLModeAllowlist{
		Enabled:         true,
		DomainAllowlist: []string{"accounts.example.com"},
	}, drops, auditor)

	_, err := d.dispatch(context.Background(), "acme", raising,
		elicitation.Content{Message: "sign in", Schema: map[string]any{}},
		elicitation.InitiatorAgent, "https://phish.evil.test/login?token=secret")

	var toolErr *mcp.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "DOMAIN_NOT_ALLOWLISTED" {
		t.Fatalf("dispatch err = %v, want *mcp.ToolError DOMAIN_NOT_ALLOWLISTED", err)
	}
	// The drop metric still fires exactly once.
	if len(drops.reasons) != 1 || drops.reasons[0] != "domain_not_allowlisted" {
		t.Errorf("drop reasons = %v, want [domain_not_allowlisted]", drops.reasons)
	}
	// The §16.7 audit row is written exactly once with the catalog name.
	if auditor.calls != 1 {
		t.Fatalf("audit calls = %d, want 1", auditor.calls)
	}
	if auditor.typ != "elicitation.url_mode_domain_rejected" {
		t.Errorf("audit type = %q, want elicitation.url_mode_domain_rejected", auditor.typ)
	}
	want := map[string]any{
		"session_id":       "sess_leaf",
		"origin_pod":       "sess_leaf",
		"tenant_id":        "acme",
		"host":             "phish.evil.test",
		"reason":           "domain_not_allowlisted",
		"initiator_type":   "agent",
		"delegation_depth": 2,
	}
	for k, v := range want {
		if got := auditor.detail[k]; got != v {
			t.Errorf("audit detail[%q] = %v (%T), want %v", k, got, got, v)
		}
	}
	// The row carries the rejected host but never the full URL's query or
	// fragment (the staged §16.7 payload contract).
	for k, v := range auditor.detail {
		if s, ok := v.(string); ok && strings.Contains(s, "token=secret") {
			t.Errorf("audit detail[%q] leaked the URL query: %q", k, s)
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, auditor.detail["detected_at"].(string)); err != nil {
		t.Errorf("detected_at not RFC3339Nano: %v", auditor.detail["detected_at"])
	}
}

// TestDispatchURLModeDropNilAuditorNoPanic_spec_16_7 proves the drop path
// is safe when no auditor is wired (the metric and tool error still fire),
// so a binary without an audit sink degrades to the metric-only behavior
// rather than panicking. spec: §16.7; §9.2 line 86. F-EL3.
func TestDispatchURLModeDropNilAuditorNoPanic_spec_16_7(t *testing.T) {
	drops := &recordingDispatcherDrop{}
	d, raising := urlModeDispatcher(elicitation.URLModeAllowlist{
		Enabled:         true,
		DomainAllowlist: []string{"accounts.example.com"},
	}, drops, nil)
	d.audit = nil

	_, err := d.dispatch(context.Background(), "acme", raising,
		elicitation.Content{Message: "sign in", Schema: map[string]any{}},
		elicitation.InitiatorAgent, "https://phish.evil.test/login")
	var toolErr *mcp.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "DOMAIN_NOT_ALLOWLISTED" {
		t.Fatalf("dispatch err = %v, want DOMAIN_NOT_ALLOWLISTED", err)
	}
	if len(drops.reasons) != 1 {
		t.Errorf("drop reasons = %v, want one drop even with nil auditor", drops.reasons)
	}
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

// TestDispatchNoProviderNeverDetects_spec_9_2 pins the v1 production
// behavior: the gateway resolves the elicitation chain server-internally
// and no intermediate pod re-emits, so the dispatcher advances the chain
// unverified even under enforce and the gateway-origin binding holds by
// construction. With nothing re-emitted there is no divergence to catch,
// so the elicitation forwards normally and no metric or audit event is
// emitted. spec: §9.2 (gateway-origin binding; v1 structural enforcement).
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
