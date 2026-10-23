// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §10.2 tenant-claim extraction mode of the
// real cmd/lenny-gateway binary. A gateway started with
// --tenancy-mode=multi --multi-tenant (what the chart renders for
// tenancy.mode: multi with auth.multiTenant: true) admits a Bearer whose
// tenant claim names a registered tenant and refuses an unregistered one
// with 403 TENANT_NOT_FOUND. A gateway started in single-tenant mode
// ignores the claim and serves the built-in `default` tenant. A gateway
// given a contradictory pair of tenancy flags refuses to start. T-10.2.11.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// tenancyTrustKeyID and tenancyTrustSecret are the §17.4 bearer-trust
// HMAC key the gateway under test trusts, so the test can mint Bearer
// tokens carrying any tenant claim.
const (
	tenancyTrustKeyID  = "tenancy-mode-it"
	tenancyTrustSecret = "tenancy-mode-integration-hmac-secret-32b"
)

// writeTenancyTrustKey writes the key file jwt.LoadHMACKeyFile parses and
// returns its path.
func writeTenancyTrustKey(t *testing.T) string {
	t.Helper()
	raw, err := json.Marshal(struct {
		KeyID  string `json:"keyId"`
		Secret []byte `json:"secret"`
	}{tenancyTrustKeyID, []byte(tenancyTrustSecret)})
	if err != nil {
		t.Fatalf("marshal trust key: %v", err)
	}
	p := filepath.Join(t.TempDir(), "bearer-trust-key.json")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatalf("write trust key: %v", err)
	}
	return p
}

// mintTenancyBearer signs a user bearer for alice carrying tenant as its
// tenant_id claim, under the dev.local audience the bearer-trust verifier
// pins.
func mintTenancyBearer(t *testing.T, tenant string) string {
	t.Helper()
	now := time.Now()
	tok, err := jwt.NewHMACSigner(tenancyTrustKeyID, []byte(tenancyTrustSecret)).Sign(jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: tenant,
		Roles:    []auth.Role{auth.RoleUser},
		Typ:      auth.TokenUserBearer,
		Audience: []string{"dev.local"},
		IssuedAt: now.Unix(),
		Expiry:   now.Add(5 * time.Minute).Unix(),
		JWTID:    "tenancy-" + tenant + "-" + now.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("sign bearer: %v", err)
	}
	return tok
}

// tenancyCall issues one request and returns the status and decoded body.
// A non-empty bearer is sent as the Authorization header; otherwise the
// dev headers name tenant, user, and roles.
func tenancyCall(t *testing.T, method, url, bearer string, dev map[string]string, body any) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for k, v := range dev {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

// tenancyErrorCode extracts error.code from the canonical error envelope.
func tenancyErrorCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

// spec: §10.2 (Authentication, `tenant_id` extraction)
// diagnosis: a failure means the multi-tenant gateway binary does not
// consult the tenant registry on the Bearer path: either an unregistered
// tenant claim is admitted (the T-10.2.11 defect, where the single-tenant
// branch wired a registry that admitted every tenant), or a registered
// tenant is refused because the registry is not the tenantstore the admin
// API writes to.
func TestMultiTenantGatewayRefusesUnregisteredTenantClaim(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	gw := gateway.StartWith(t, "--dev-mode", "--tenancy-mode=multi", "--multi-tenant",
		"--bearer-trust-hmac-key-file="+writeTenancyTrustKey(t))
	base := gw.BaseURL()

	// Register acme through the admin API as a platform-admin on the
	// built-in default tenant, which the registry admits unconditionally.
	admin := map[string]string{
		"X-Lenny-Tenant-ID": auth.DefaultTenantID,
		"X-Lenny-User-ID":   "ops@acme.com",
		"X-Lenny-Roles":     "platform-admin",
	}
	if code, body := tenancyCall(t, http.MethodPost, base+"/v1/admin/tenants", "", admin,
		map[string]any{"id": "acme", "displayName": "Acme Corp"}); code != http.StatusCreated {
		t.Fatalf("create tenant acme: status %d (%v)\n%s", code, body, gw.Logs())
	}

	code, body := tenancyCall(t, http.MethodGet, base+"/v1/admin/me", mintTenancyBearer(t, "acme"), nil, nil)
	if code != http.StatusOK {
		t.Fatalf("registered tenant: status %d (%v), want 200", code, body)
	}
	if got, _ := body["tenantId"].(string); got != "acme" {
		t.Fatalf("registered tenant: tenantId = %q, want acme", got)
	}

	code, body = tenancyCall(t, http.MethodGet, base+"/v1/admin/me", mintTenancyBearer(t, "totally-bogus-tenant-xyz"), nil, nil)
	if code != http.StatusForbidden || tenancyErrorCode(body) != "TENANT_NOT_FOUND" {
		t.Fatalf("unregistered tenant: status %d code %q (%v), want 403 TENANT_NOT_FOUND", code, tenancyErrorCode(body), body)
	}

	code, body = tenancyCall(t, http.MethodGet, base+"/v1/admin/me", mintTenancyBearer(t, ""), nil, nil)
	if code != http.StatusUnauthorized || tenancyErrorCode(body) != "TENANT_CLAIM_MISSING" {
		t.Fatalf("absent claim: status %d code %q (%v), want 401 TENANT_CLAIM_MISSING", code, tenancyErrorCode(body), body)
	}
}

// spec: §10.2 (Authentication, `tenant_id` extraction)
// diagnosis: a failure means the single-tenant gateway binary lets the
// Bearer tenant claim select the tenant instead of serving every request
// as `default`, so a caller could reach another tenant's rows by naming it
// in the claim.
func TestSingleTenantGatewayIgnoresTenantClaim(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	gw := gateway.StartWith(t, "--dev-mode", "--tenancy-mode=single",
		"--bearer-trust-hmac-key-file="+writeTenancyTrustKey(t))
	for _, claim := range []string{"totally-bogus-tenant-xyz", "acme", ""} {
		code, body := tenancyCall(t, http.MethodGet, gw.BaseURL()+"/v1/admin/me", mintTenancyBearer(t, claim), nil, nil)
		if code != http.StatusOK {
			t.Fatalf("claim %q: status %d (%v), want 200", claim, code, body)
		}
		if got, _ := body["tenantId"].(string); got != auth.DefaultTenantID {
			t.Fatalf("claim %q: tenantId = %q, want %q", claim, got, auth.DefaultTenantID)
		}
	}
}

// spec: §10.2 (Authentication), §4.9 (tenancy.mode: multi), §10.3
// (gateway startup configuration validation)
// diagnosis: a failure means the gateway binary starts with tenancy flags
// that disagree, so its auth chain and its credential-delivery rejections
// would enforce different tenancy postures; a tenancy.mode: multi gateway
// without --multi-tenant would admit every tenant claim.
func TestGatewayRefusesContradictoryTenancyFlags(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	binary := filepath.Join(t.TempDir(), "lenny-gateway")
	build := exec.Command("go", "build", "-o", binary, "./cmd/lenny-gateway")
	build.Dir = schematest.RepoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build lenny-gateway: %v\n%s", err, out)
	}
	for _, flags := range [][]string{
		{"--tenancy-mode=multi"},
		{"--tenancy-mode=single", "--multi-tenant"},
		{"--tenancy-mode=shared"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, binary, append([]string{"--addr", "127.0.0.1:0", "--no-environment-policy", "deny-all"}, flags...)...)
		cmd.Env = append(os.Environ(), "LENNY_DEV_MODE=true", "LENNY_TLS_TERMINATED_UPSTREAM=true")
		out, err := cmd.CombinedOutput()
		cancel()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("flags %v: the gateway kept running; want a startup refusal\n%s", flags, out)
		}
		if err == nil {
			t.Fatalf("flags %v: the gateway exited 0; want a non-zero startup refusal\n%s", flags, out)
		}
		if !strings.Contains(string(out), "LENNY_CONFIG_INVALID") {
			t.Fatalf("flags %v: the gateway exited without naming LENNY_CONFIG_INVALID\n%s", flags, out)
		}
	}
}
