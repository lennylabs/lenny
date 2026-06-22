// SPDX-License-Identifier: MIT

package stack

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
)

// TestBootstrapSeedRegistersReferenceFieldsThroughAdmin_spec_26_2 is a
// tier-3 contract test: it pushes the Embedded Mode bootstrap seed
// through the real gateway admin bootstrap handler and asserts the
// §26.2 shared coding-agent blocks and the §26.1 chat resource posture
// survive registration. This guards the embedded-stack catalog against
// dropping the §26 fields the chart install carries. F-26.2.3 / F-26.1.3.
func TestBootstrapSeedRegistersReferenceFieldsThroughAdmin_spec_26_2(t *testing.T) {
	tenants := tenantstore.NewMemory()
	runtimes := runtimestore.NewMemory()
	users := userstore.NewMemory()
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithRuntimes(runtimes).WithUsers(users)

	body, err := json.Marshal(buildBootstrapSeed())
	if err != nil {
		t.Fatalf("marshal seed: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap", bytes.NewReader(body))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "admin@acme.com",
		TenantID: "platform",
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("bootstrap status %d: %s", rr.Code, rr.Body.String())
	}

	var resp admin.BootstrapResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Runtimes.Errors) != 0 {
		t.Fatalf("runtime registration errors: %+v", resp.Runtimes.Errors)
	}
	// The §26 reference catalog plus the §15.4.4 echo exemplar register.
	if resp.Runtimes.CreatedCount != len(referenceRuntimes)+1 {
		t.Fatalf("created %d runtimes, want %d (the §26 catalog plus echo)", resp.Runtimes.CreatedCount, len(referenceRuntimes)+1)
	}

	// spec: §26.2 — claude-code carries the shared coding-agent blocks.
	cc, err := runtimes.Get(context.Background(), "claude-code")
	if err != nil {
		t.Fatalf("get claude-code: %v", err)
	}
	if cc.Limits == nil || cc.Limits.MaxSessionAgeSeconds != 14400 {
		t.Errorf("claude-code limits not stored: %+v", cc.Limits)
	}
	if cc.SetupCommandPolicy == nil || string(cc.SetupCommandPolicy.Mode) != "allowlist" {
		t.Errorf("claude-code setupCommandPolicy not stored: %+v", cc.SetupCommandPolicy)
	}
	if cc.DefaultPoolConfig == nil || cc.DefaultPoolConfig.EgressProfile != "restricted" {
		t.Errorf("claude-code defaultPoolConfig not stored: %+v", cc.DefaultPoolConfig)
	}
	if cc.CredentialCapabilities == nil || len(cc.CredentialCapabilities.ProxyDialect) != 1 || cc.CredentialCapabilities.ProxyDialect[0] != "anthropic" {
		t.Errorf("claude-code credentialCapabilities not stored: %+v", cc.CredentialCapabilities)
	}
	if cc.Capabilities == nil || string(cc.Capabilities.Interaction) != "multi_turn" {
		t.Errorf("claude-code capabilities not stored: %+v", cc.Capabilities)
	}
	if len(cc.AllowedResourceClasses) != 3 {
		t.Errorf("claude-code allowedResourceClasses not stored: %+v", cc.AllowedResourceClasses)
	}

	// spec: §26.1 line 22 / §26.7 — chat is Full (hotRotation: true
	// requires the Full-only lifecycle channel) and carries the small
	// resource class only.
	chat, err := runtimes.Get(context.Background(), "chat")
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if string(chat.IntegrationLevel) != "full" {
		t.Errorf("chat integrationLevel = %q, want full", chat.IntegrationLevel)
	}
	if len(chat.AllowedResourceClasses) != 1 || chat.AllowedResourceClasses[0] != "small" {
		t.Errorf("chat allowedResourceClasses = %v, want [small]", chat.AllowedResourceClasses)
	}
	if chat.Capabilities == nil || !chat.Capabilities.Injection.Supported || len(chat.Capabilities.Injection.Modes) != 1 {
		t.Errorf("chat capabilities not stored (expected immediate-only injection): %+v", chat.Capabilities)
	}
}
