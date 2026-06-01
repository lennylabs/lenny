// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	corr "github.com/lennylabs/lenny/pkg/observability/correlation"
)

// spec: §11.7 lines 347-348 — every audit event carries the optional
// operation_id (X-Lenny-Operation-ID) and caller_kind (OIDC
// caller_type) fields when available; the OCSF mapping projects them
// onto metadata.correlation_uid and actor.user.type. F-11.7.13.

// firstPayload decodes the first row of the named chain into a map.
func firstPayload(t *testing.T, chains *audit.ChainSet, tenant string) map[string]any {
	t.Helper()
	chain := chains.Chain(tenant)
	if chain == nil || chain.Len() == 0 {
		t.Fatalf("chain %q is empty", tenant)
	}
	var m map[string]any
	if err := json.Unmarshal(chain.Rows()[0].Payload, &m); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return m
}

func TestEmitAdminEventCarriesExplicitCorrelationFields_F11713(t *testing.T) {
	chains := audit.NewChainSet()
	sink := admin.NewChainAuditSink(chains, nil)
	sink.EmitAdminEvent(context.Background(), admin.AuditEvent{
		Type:          "admin.tenant.created",
		ActorTenantID: "acme",
		OperationID:   "op-123",
		CallerKind:    "human",
		At:            time.Unix(0, 0).UTC(),
	})
	m := firstPayload(t, chains, "acme")
	if m["operation_id"] != "op-123" {
		t.Errorf("operation_id = %v, want op-123", m["operation_id"])
	}
	if m["caller_kind"] != "human" {
		t.Errorf("caller_kind = %v, want human", m["caller_kind"])
	}
}

// When the emitter leaves the fields empty (the credential, delegation,
// playground, and auth-failure auditors construct events directly),
// EmitAdminEvent recovers them from the request context.
func TestEmitAdminEventFallsBackToContext_F11713(t *testing.T) {
	chains := audit.NewChainSet()
	sink := admin.NewChainAuditSink(chains, nil)
	ctx := corr.With(context.Background(), corr.Fields{OperationID: "op-ctx"})
	ctx = authmw.WithPrincipal(ctx, authmw.Principal{TenantID: "acme", CallerType: "agent"})
	sink.EmitAdminEvent(ctx, admin.AuditEvent{
		Type:          "admin.user.created",
		ActorTenantID: "acme",
		At:            time.Unix(0, 0).UTC(),
	})
	m := firstPayload(t, chains, "acme")
	if m["operation_id"] != "op-ctx" {
		t.Errorf("operation_id = %v, want op-ctx", m["operation_id"])
	}
	if m["caller_kind"] != "agent" {
		t.Errorf("caller_kind = %v, want agent", m["caller_kind"])
	}
}

// Explicit event fields take precedence over the context fallback.
func TestEmitAdminEventExplicitBeatsContext_F11713(t *testing.T) {
	chains := audit.NewChainSet()
	sink := admin.NewChainAuditSink(chains, nil)
	ctx := corr.With(context.Background(), corr.Fields{OperationID: "op-ctx"})
	ctx = authmw.WithPrincipal(ctx, authmw.Principal{TenantID: "acme", CallerType: "agent"})
	sink.EmitAdminEvent(ctx, admin.AuditEvent{
		Type:          "admin.user.created",
		ActorTenantID: "acme",
		OperationID:   "op-explicit",
		CallerKind:    "human",
		At:            time.Unix(0, 0).UTC(),
	})
	m := firstPayload(t, chains, "acme")
	if m["operation_id"] != "op-explicit" {
		t.Errorf("operation_id = %v, want op-explicit", m["operation_id"])
	}
	if m["caller_kind"] != "human" {
		t.Errorf("caller_kind = %v, want human", m["caller_kind"])
	}
}

// Both fields are optional: absent on the event and absent on the
// context, the payload omits the keys rather than emitting empties.
func TestEmitAdminEventOmitsEmptyCorrelationFields_F11713(t *testing.T) {
	chains := audit.NewChainSet()
	sink := admin.NewChainAuditSink(chains, nil)
	sink.EmitAdminEvent(context.Background(), admin.AuditEvent{
		Type:          "admin.tenant.created",
		ActorTenantID: "acme",
		At:            time.Unix(0, 0).UTC(),
	})
	m := firstPayload(t, chains, "acme")
	if _, ok := m["operation_id"]; ok {
		t.Errorf("operation_id should be omitted, got %v", m["operation_id"])
	}
	if _, ok := m["caller_kind"]; ok {
		t.Errorf("caller_kind should be omitted, got %v", m["caller_kind"])
	}
}

// spec: §15.1 line 938 — X-Lenny-Agent-Name is propagated to audit
// records. The explicit AgentName field lands in the payload. F-15.1.10.
func TestEmitAdminEventCarriesAgentName_spec_15_1_938(t *testing.T) {
	chains := audit.NewChainSet()
	sink := admin.NewChainAuditSink(chains, nil)
	sink.EmitAdminEvent(context.Background(), admin.AuditEvent{
		Type:          "admin.tenant.created",
		ActorTenantID: "acme",
		AgentName:     "alice-remediation-bot",
		At:            time.Unix(0, 0).UTC(),
	})
	m := firstPayload(t, chains, "acme")
	if m["agent_name"] != "alice-remediation-bot" {
		t.Errorf("agent_name = %v, want alice-remediation-bot", m["agent_name"])
	}
}

// When the emitter leaves AgentName empty, EmitAdminEvent recovers it
// from the X-Lenny-Agent-Name correlation context. F-15.1.10.
func TestEmitAdminEventAgentNameFallsBackToContext_spec_15_1_938(t *testing.T) {
	chains := audit.NewChainSet()
	sink := admin.NewChainAuditSink(chains, nil)
	ctx := corr.With(context.Background(), corr.Fields{AgentName: "agent-ctx"})
	sink.EmitAdminEvent(ctx, admin.AuditEvent{
		Type:          "admin.user.created",
		ActorTenantID: "acme",
		At:            time.Unix(0, 0).UTC(),
	})
	m := firstPayload(t, chains, "acme")
	if m["agent_name"] != "agent-ctx" {
		t.Errorf("agent_name = %v, want agent-ctx", m["agent_name"])
	}
}

// agent_name is optional: absent on both event and context, the payload
// omits the key. F-15.1.10.
func TestEmitAdminEventOmitsEmptyAgentName_spec_15_1_938(t *testing.T) {
	chains := audit.NewChainSet()
	sink := admin.NewChainAuditSink(chains, nil)
	sink.EmitAdminEvent(context.Background(), admin.AuditEvent{
		Type:          "admin.tenant.created",
		ActorTenantID: "acme",
		At:            time.Unix(0, 0).UTC(),
	})
	m := firstPayload(t, chains, "acme")
	if _, ok := m["agent_name"]; ok {
		t.Errorf("agent_name should be omitted, got %v", m["agent_name"])
	}
}

// End-to-end through the admin router: the X-Lenny-Agent-Name carried on
// the correlation context lands on the committed hash-chain row alongside
// operation_id. spec: §15.1 lines 937-938. F-15.1.10.
func TestRouterEmitPopulatesAgentName_spec_15_1_938(t *testing.T) {
	chains := audit.NewChainSet()
	sink := admin.NewChainAuditSink(chains, nil)
	store := tenantstore.NewMemory()
	router := admin.NewRouter(store, admin.Options{
		Clock: func() time.Time { return time.Unix(0, 0).UTC() },
		Audit: sink,
	})

	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", DisplayName: "Acme"})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body))
	ctx := corr.With(req.Context(), corr.Fields{OperationID: "op-router", AgentName: "alice-agent"})
	ctx = authmw.WithPrincipal(ctx, authmw.Principal{
		Subject:    "admin@acme.com",
		TenantID:   "platform",
		CallerType: "human",
		Roles:      []pkgauth.Role{pkgauth.RolePlatformAdmin},
	})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req.WithContext(ctx))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d, body=%s", rr.Code, rr.Body.String())
	}

	m := firstPayload(t, chains, "platform")
	if m["agent_name"] != "alice-agent" {
		t.Errorf("agent_name = %v, want alice-agent", m["agent_name"])
	}
	if m["operation_id"] != "op-router" {
		t.Errorf("operation_id = %v, want op-router", m["operation_id"])
	}
}

// End-to-end through the admin router: emit populates the AuditEvent
// from the principal's caller_type and the correlation context, so the
// committed hash-chain row carries both fields.
func TestRouterEmitPopulatesCorrelationFields_F11713(t *testing.T) {
	chains := audit.NewChainSet()
	sink := admin.NewChainAuditSink(chains, nil)
	store := tenantstore.NewMemory()
	router := admin.NewRouter(store, admin.Options{
		Clock: func() time.Time { return time.Unix(0, 0).UTC() },
		Audit: sink,
	})

	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", DisplayName: "Acme"})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body))
	ctx := corr.With(req.Context(), corr.Fields{OperationID: "op-router"})
	ctx = authmw.WithPrincipal(ctx, authmw.Principal{
		Subject:    "admin@acme.com",
		TenantID:   "platform",
		CallerType: "human",
		Roles:      []pkgauth.Role{pkgauth.RolePlatformAdmin},
	})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req.WithContext(ctx))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d, body=%s", rr.Code, rr.Body.String())
	}

	m := firstPayload(t, chains, "platform")
	if m["operation_id"] != "op-router" {
		t.Errorf("operation_id = %v, want op-router", m["operation_id"])
	}
	if m["caller_kind"] != "human" {
		t.Errorf("caller_kind = %v, want human", m["caller_kind"])
	}
}
