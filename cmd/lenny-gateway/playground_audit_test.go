// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
)

// recordingAuditSink captures the §11.7 events routed to it. It
// satisfies admin.AuditSink.
type recordingAuditSink struct {
	events []admin.AuditEvent
}

func (r *recordingAuditSink) EmitAdminEvent(_ context.Context, ev admin.AuditEvent) {
	r.events = append(r.events, ev)
}

// spec: §27.3.1 step 6 (line 156) — playground.bearer_minted /
// playground.bearer_revoked share the §11.7 taxonomy and reach the
// durable audit sink, not just the log. F-27.3.5.
func TestPlaygroundAuditEmitterRoutesToDurableSink(t *testing.T) {
	sink := &recordingAuditSink{}
	e := playgroundAuditEmitter{sink: sink}
	at := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

	e.EmitPlaygroundEvent(context.Background(), playground.AuditEvent{
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
	// The event lands on the principal's tenant chain.
	if ev.ActorSubject != "alice@acme.com" || ev.ActorTenantID != "acme" {
		t.Errorf("actor = %q/%q, want alice@acme.com/acme", ev.ActorSubject, ev.ActorTenantID)
	}
	if ev.TargetResource != "cookie-1" {
		t.Errorf("TargetResource = %q, want cookie-1", ev.TargetResource)
	}
	if ev.Detail["bearer_jti"] != "jti-1" {
		t.Errorf("detail bearer_jti = %v, want jti-1", ev.Detail["bearer_jti"])
	}
	if ev.Detail["bearer_ttl_seconds"] != int64(900) {
		t.Errorf("detail bearer_ttl_seconds = %v, want 900", ev.Detail["bearer_ttl_seconds"])
	}
	if !ev.At.Equal(at) {
		t.Errorf("At = %v, want %v", ev.At, at)
	}
}

// spec: §10.2 line 243 — the bearer_mint_rejected event routes to the
// same §11.7 sink. F-27.3.5.
func TestPlaygroundAuditEmitterMintRejectedRoutesToDurableSink(t *testing.T) {
	sink := &recordingAuditSink{}
	e := playgroundAuditEmitter{sink: sink}

	e.EmitMintRejected(context.Background(), playground.MintRejectedEvent{
		TenantID:          "acme",
		SubjectJTI:        "sj-1",
		SubjectTyp:        "service_token",
		InvariantViolated: "subject_typ_invalid",
		IngressPath:       "/v1/playground/token",
	})

	if len(sink.events) != 1 || sink.events[0].Type != "playground.bearer_mint_rejected" {
		t.Fatalf("durable events = %+v, want one playground.bearer_mint_rejected", sink.events)
	}
	ev := sink.events[0]
	if ev.ActorTenantID != "acme" {
		t.Errorf("ActorTenantID = %q, want acme", ev.ActorTenantID)
	}
	if ev.Detail["invariant_violated"] != "subject_typ_invalid" {
		t.Errorf("detail invariant_violated = %v, want subject_typ_invalid", ev.Detail["invariant_violated"])
	}
	if ev.Detail["ingress_path"] != "/v1/playground/token" {
		t.Errorf("detail ingress_path = %v, want /v1/playground/token", ev.Detail["ingress_path"])
	}
}

// A nil durable sink degrades to log-only without panicking, so a
// deployment without an admin audit sink still serves the playground.
func TestPlaygroundAuditEmitterNilSinkDoesNotPanic(t *testing.T) {
	e := playgroundAuditEmitter{}
	e.EmitPlaygroundEvent(context.Background(), playground.AuditEvent{Type: "playground.bearer_minted", TenantID: "acme"})
	e.EmitMintRejected(context.Background(), playground.MintRejectedEvent{InvariantViolated: "subject_typ_invalid"})
}
