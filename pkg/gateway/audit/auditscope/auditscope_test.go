// SPDX-License-Identifier: MIT

package auditscope_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditscope"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	obsaudit "github.com/lennylabs/lenny/pkg/observability/audit"
)

// spec: §11.7 line 428 — write-time tenant validation rejects an audit
// row whose tenant differs from the authenticated caller's scope.
// F-11.7.6.

type appendCall struct {
	tenant    string
	eventType string
	payload   json.RawMessage
}

// recordingChain captures Append calls and serves canned Rows/Verify.
type recordingChain struct {
	appends   []appendCall
	rows      []audit.Row
	verify    audit.VerifyResult
	appendErr error
}

func (c *recordingChain) Append(_ context.Context, tenant, eventType string, payload json.RawMessage, _ time.Time) (audit.Row, error) {
	c.appends = append(c.appends, appendCall{tenant, eventType, payload})
	return audit.Row{TenantID: tenant, EventType: eventType}, c.appendErr
}

func (c *recordingChain) Rows(_ context.Context, tenant string) ([]audit.Row, error) {
	return c.rows, nil
}

func (c *recordingChain) Verify(_ context.Context, _ string) (audit.VerifyResult, error) {
	return c.verify, nil
}

func ctxWith(p authmw.Principal) context.Context {
	return authmw.WithPrincipal(context.Background(), p)
}

func TestValidatorAllowsMatchingTenant_F1176(t *testing.T) {
	inner := &recordingChain{}
	v := auditscope.New(inner, nil)
	ctx := ctxWith(authmw.Principal{TenantID: "acme", Subject: "u@acme"})
	if _, err := v.Append(ctx, "acme", "admin.tenant.updated", json.RawMessage(`{}`), time.Time{}); err != nil {
		t.Fatalf("matching tenant should pass: %v", err)
	}
	if len(inner.appends) != 1 || inner.appends[0].tenant != "acme" {
		t.Fatalf("expected one acme append, got %+v", inner.appends)
	}
}

func TestValidatorRejectsForeignTenant_F1176(t *testing.T) {
	inner := &recordingChain{}
	v := auditscope.New(inner, nil)
	ctx := ctxWith(authmw.Principal{TenantID: "acme", Subject: "u@acme", CallerType: "agent"})

	row, err := v.Append(ctx, "globex", "admin.tenant.updated", json.RawMessage(`{}`), time.Time{})
	if err == nil {
		t.Fatal("foreign tenant write must be rejected")
	}
	var scopeErr *auditscope.TenantScopeError
	if !errors.As(err, &scopeErr) {
		t.Fatalf("want *TenantScopeError, got %T", err)
	}
	if scopeErr.Code() != auditscope.CodeTenantScopeMismatch {
		t.Errorf("code = %q, want %q", scopeErr.Code(), auditscope.CodeTenantScopeMismatch)
	}
	if scopeErr.Attempted != "globex" || scopeErr.Authenticated != "acme" {
		t.Errorf("error fields = %+v", scopeErr)
	}
	if row.EventType != "" {
		t.Errorf("rejected write must return a zero row, got %+v", row)
	}

	// The forged-tenant event must NOT be committed; exactly one
	// security.audit_write_rejected row must land on the platform chain.
	if len(inner.appends) != 1 {
		t.Fatalf("expected exactly one (rejection) append, got %d: %+v", len(inner.appends), inner.appends)
	}
	got := inner.appends[0]
	if got.tenant != "platform" {
		t.Errorf("rejection tenant = %q, want platform", got.tenant)
	}
	if got.eventType != obsaudit.EventSecurityAuditWriteRejected.String() {
		t.Errorf("rejection event = %q, want %q", got.eventType, obsaudit.EventSecurityAuditWriteRejected.String())
	}
	var fields map[string]any
	if err := json.Unmarshal(got.payload, &fields); err != nil {
		t.Fatalf("unmarshal rejection payload: %v", err)
	}
	if fields["error_code"] != auditscope.CodeTenantScopeMismatch {
		t.Errorf("payload error_code = %v", fields["error_code"])
	}
	if fields["attempted_tenant_id"] != "globex" {
		t.Errorf("payload attempted_tenant_id = %v", fields["attempted_tenant_id"])
	}
	if fields["authenticated_tenant"] != "acme" {
		t.Errorf("payload authenticated_tenant = %v", fields["authenticated_tenant"])
	}
	if fields["actor_subject"] != "u@acme" {
		t.Errorf("payload actor_subject = %v", fields["actor_subject"])
	}
	if fields["caller_kind"] != "agent" {
		t.Errorf("payload caller_kind = %v, want agent", fields["caller_kind"])
	}
}

func TestValidatorAllowsPlatformTenantScope_F1176(t *testing.T) {
	inner := &recordingChain{}
	v := auditscope.New(inner, nil)
	// A platform-tenant caller may write any tenant chain.
	ctx := ctxWith(authmw.Principal{TenantID: "platform", Subject: "ops@platform"})
	if _, err := v.Append(ctx, "globex", "platform.bootstrap_applied", json.RawMessage(`{}`), time.Time{}); err != nil {
		t.Fatalf("platform-tenant caller should pass: %v", err)
	}
	if len(inner.appends) != 1 || inner.appends[0].tenant != "globex" {
		t.Fatalf("expected the globex append to pass through, got %+v", inner.appends)
	}
}

func TestValidatorAllowsPlatformAdminRole_F1176(t *testing.T) {
	inner := &recordingChain{}
	v := auditscope.New(inner, nil)
	// A platform-admin role bound to a tenant scope may still write
	// cross-tenant (operators administering a specific tenant).
	ctx := ctxWith(authmw.Principal{
		TenantID: "acme",
		Subject:  "ops@platform",
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
	})
	if _, err := v.Append(ctx, "globex", "admin.tenant.updated", json.RawMessage(`{}`), time.Time{}); err != nil {
		t.Fatalf("platform-admin role should pass: %v", err)
	}
	if len(inner.appends) != 1 || inner.appends[0].tenant != "globex" {
		t.Fatalf("expected the globex append, got %+v", inner.appends)
	}
}

func TestValidatorAllowsSystemWriteWithoutPrincipal_F1176(t *testing.T) {
	inner := &recordingChain{}
	v := auditscope.New(inner, nil)
	// No authenticated principal on ctx: a gateway-internal write
	// (background reconciler, key-rotation observer). It passes.
	if _, err := v.Append(context.Background(), "acme", "platform.jwt_signing_key_rotated", json.RawMessage(`{}`), time.Time{}); err != nil {
		t.Fatalf("system write should pass: %v", err)
	}
	if len(inner.appends) != 1 || inner.appends[0].tenant != "acme" {
		t.Fatalf("expected the acme append, got %+v", inner.appends)
	}
}

func TestValidatorRowsAndVerifyDelegate_F1176(t *testing.T) {
	inner := &recordingChain{
		rows:   []audit.Row{{Seq: 1, TenantID: "acme"}},
		verify: audit.VerifyResult{Integrity: audit.ChainVerified},
	}
	v := auditscope.New(inner, nil)
	rows, err := v.Rows(context.Background(), "acme")
	if err != nil || len(rows) != 1 || rows[0].Seq != 1 {
		t.Fatalf("Rows did not delegate: rows=%+v err=%v", rows, err)
	}
	res, err := v.Verify(context.Background(), "acme")
	if err != nil || res.Integrity != audit.ChainVerified {
		t.Fatalf("Verify did not delegate: %+v err=%v", res, err)
	}
	// Read-path delegation must not emit any write.
	if len(inner.appends) != 0 {
		t.Errorf("reads must not append, got %+v", inner.appends)
	}
}

// The in-memory ChainSet adapter satisfies Chain and round-trips a
// write, so the minimal gateway can sit a Validator in front of it.
func TestChainSetChainRoundTrip_F1176(t *testing.T) {
	chains := audit.NewChainSet()
	chain := auditscope.NewChainSetChain(chains, func() time.Time { return time.Unix(0, 0).UTC() })
	if _, err := chain.Append(context.Background(), "acme", "admin.tenant.created", json.RawMessage(`{"k":"v"}`), time.Time{}); err != nil {
		t.Fatalf("append: %v", err)
	}
	rows, err := chain.Rows(context.Background(), "acme")
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows: %+v err=%v", rows, err)
	}
	res, err := chain.Verify(context.Background(), "acme")
	if err != nil || res.Integrity != audit.ChainVerified {
		t.Fatalf("verify: %+v err=%v", res, err)
	}
	// An unknown tenant verifies as an empty (valid) chain.
	if res, _ := chain.Verify(context.Background(), "globex"); res.Integrity != audit.ChainVerified {
		t.Errorf("empty chain should verify, got %q", res.Integrity)
	}
}
