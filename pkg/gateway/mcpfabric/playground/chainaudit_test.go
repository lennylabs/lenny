// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// recordingAuditSink captures the §11.7 events routed to it. It
// satisfies admin.AuditSink.
type recordingAuditSink struct {
	events []admin.AuditEvent
}

func (r *recordingAuditSink) EmitAdminEvent(_ context.Context, ev admin.AuditEvent) {
	r.events = append(r.events, ev)
}

// chainAppend is one row appended to a tenant's audit chain.
type chainAppend struct {
	tenantID  string
	eventType string
	payload   map[string]any
}

// recordingAuditLog is an admin.AuditLog that records the chain each
// append lands on and the decoded payload, so a test reads the chain
// placement the production admin.ChainAuditSink chooses.
type recordingAuditLog struct {
	appends []chainAppend
}

func (l *recordingAuditLog) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, _ time.Time) (audit.Row, error) {
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return audit.Row{}, err
	}
	l.appends = append(l.appends, chainAppend{tenantID: tenantID, eventType: eventType, payload: decoded})
	return audit.Row{}, nil
}

func (l *recordingAuditLog) Rows(context.Context, string) ([]audit.Row, error) { return nil, nil }

func (l *recordingAuditLog) Verify(context.Context, string) (audit.VerifyResult, error) {
	return audit.VerifyResult{}, nil
}

// spec: 27.3.1 (Playground authentication), 11.7 (Audit Logging)
func TestChainAuditEmitterRoutesMintToPrincipalChain_spec_27_3_1(t *testing.T) {
	sink := &recordingAuditSink{}
	e := ChainAuditEmitter{Sink: sink}
	at := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

	e.EmitPlaygroundEvent(context.Background(), AuditEvent{
		Type:             "playground.bearer_minted",
		UserID:           "alice@acme.com",
		TenantID:         "acme",
		SessionCookieID:  "cookie-1",
		BearerJTI:        "jti-1",
		BearerTTLSeconds: 900,
		Origin:           "playground",
		Labels:           map[string]string{"origin": "playground"},
		At:               at,
	})

	if len(sink.events) != 1 {
		t.Fatalf("emitted %d durable events, want 1", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Type != "playground.bearer_minted" {
		t.Errorf("Type = %q, want playground.bearer_minted", ev.Type)
	}
	if ev.ActorSubject != "alice@acme.com" || ev.ActorTenantID != "acme" || ev.TargetTenantID != "" {
		t.Errorf("actor = %q/%q target %q, want alice@acme.com/acme and no target", ev.ActorSubject, ev.ActorTenantID, ev.TargetTenantID)
	}
	if ev.TargetResource != "cookie-1" {
		t.Errorf("TargetResource = %q, want cookie-1", ev.TargetResource)
	}
	if ev.Detail["bearer_jti"] != "jti-1" || ev.Detail["bearer_ttl_seconds"] != int64(900) || ev.Detail["label_origin"] != "playground" {
		t.Errorf("detail = %v", ev.Detail)
	}
	if !ev.At.Equal(at) {
		t.Errorf("At = %v, want %v", ev.At, at)
	}
}

// TestRefusedMintForMissingTenantAuditsOnPlatformChain_spec_12_8 pins the
// chain placement of playground.bearer_mint_rejected rows through the
// production admin.ChainAuditSink. A tenant_not_found rejection is
// appended under the platform tenant with the refused tenant as the
// payload's top-level target_tenant_id, and nothing is appended to the
// refused tenant's chain. A tenant_claim_invalid_format or
// subject_typ_invalid rejection stays on the claimed tenant's chain, and
// a rejection with no tenant lands on the platform chain with no target.
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle), 11.7 (Audit Logging), 10.2 (Authentication: `tenant_id` extraction)
func TestRefusedMintForMissingTenantAuditsOnPlatformChain_spec_12_8(t *testing.T) {
	log := &recordingAuditLog{}
	e := ChainAuditEmitter{Sink: admin.NewAuditLogSink(log, time.Now)}
	ctx := context.Background()
	reject := func(tenant, invariant string) {
		e.EmitMintRejected(ctx, MintRejectedEvent{
			TenantID: tenant, SubjectJTI: "sj-" + invariant, SubjectTyp: "user_bearer",
			InvariantViolated: invariant, IngressPath: "/v1/playground/token",
		})
	}
	reject("acme", "tenant_not_found")
	reject("acme", "tenant_claim_invalid_format")
	reject("acme", "subject_typ_invalid")
	reject("", "tenant_claim_missing")

	if len(log.appends) != 4 {
		t.Fatalf("appends = %d, want 4", len(log.appends))
	}
	want := []struct {
		chain, target string
	}{
		{"platform", "acme"},
		{"acme", ""},
		{"acme", ""},
		{"platform", ""},
	}
	for i, w := range want {
		a := log.appends[i]
		inv := a.payload["detail"].(map[string]any)["invariant_violated"]
		if a.eventType != "playground.bearer_mint_rejected" {
			t.Errorf("%v: event type = %q", inv, a.eventType)
		}
		if a.tenantID != w.chain {
			t.Errorf("%v: chain = %q, want %q", inv, a.tenantID, w.chain)
		}
		got, ok := a.payload["target_tenant_id"]
		if w.target == "" && ok {
			t.Errorf("%v: carries target_tenant_id %v, want none", inv, got)
		}
		if w.target != "" && got != w.target {
			t.Errorf("%v: top-level target_tenant_id = %v, want %q", inv, got, w.target)
		}
	}
}

// A nil durable sink degrades to log-only without panicking, so a
// deployment without an admin audit sink still serves the playground.
// spec: 27.3.1 (Playground authentication)
func TestChainAuditEmitterNilSinkDoesNotPanic_spec_27_3_1(t *testing.T) {
	e := ChainAuditEmitter{}
	e.EmitPlaygroundEvent(context.Background(), AuditEvent{Type: "playground.bearer_minted", TenantID: "acme"})
	e.EmitMintRejected(context.Background(), MintRejectedEvent{InvariantViolated: "tenant_not_found", TenantID: "acme"})
}
