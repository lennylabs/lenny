// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for §10.2 tenant-claim spoofing against a
// multi-tenant gateway. It runs the real cmd/lenny-gateway binary with
// --tenancy-mode=multi --multi-tenant and a trusted HMAC bearer key, then
// presents bearers that try to reach a tenant the caller does not hold: an
// unregistered tenant, a registered tenant under an untrusted signing key,
// a token whose tenant claim was rewritten after signing, a malformed
// claim aimed at another tenant's identifier, and a valid token paired
// with a dev header naming another tenant. Each attempt is refused or
// resolves to the tenant the signed claim names. T-10.2.11.
package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

const (
	spoofTrustKeyID  = "tenant-spoof-sec"
	spoofTrustSecret = "tenant-spoof-security-hmac-secret-32by"
)

// spoofMint signs a user bearer for alice under the dev.local audience
// with the given key, carrying tenant as its tenant_id claim.
func spoofMint(t *testing.T, keyID, secret, tenant string) string {
	t.Helper()
	now := time.Now()
	tok, err := jwt.NewHMACSigner(keyID, []byte(secret)).Sign(jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: tenant,
		Roles:    []auth.Role{auth.RoleUser},
		Typ:      auth.TokenUserBearer,
		Audience: []string{"dev.local"},
		IssuedAt: now.Unix(),
		Expiry:   now.Add(5 * time.Minute).Unix(),
		JWTID:    "spoof-" + tenant + "-" + now.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("sign bearer: %v", err)
	}
	return tok
}

// rewriteTenantClaim replaces the tenant_id claim in a signed token's
// payload and keeps the original signature, the way an attacker holding a
// token for one tenant would try to retarget it at another.
func rewriteTenantClaim(t *testing.T, token, tenant string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	payload["tenant_id"] = tenant
	out, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return parts[0] + "." + base64.RawURLEncoding.EncodeToString(out) + "." + parts[2]
}

// spoofCall issues one request against the gateway and returns the status
// and decoded body.
func spoofCall(t *testing.T, method, url, bearer string, headers map[string]string, body any) (int, map[string]any) {
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
	for k, v := range headers {
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

// spoofErrorCode extracts error.code from the canonical error envelope.
func spoofErrorCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

// spec: §10.2 (Authentication, tenant_id extraction from OIDC tokens)
// diagnosis: a failure means a caller can reach a tenant it does not hold
// on a multi-tenant gateway: the gateway admitted an unregistered tenant
// claim, accepted a tenant claim it cannot authenticate (untrusted key or
// rewritten payload), let a malformed claim through the format gate, or
// let a client header override the tenant the signed claim names.
func TestMultiTenantGatewayRefusesTenantClaimSpoofing(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	keyPath := filepath.Join(t.TempDir(), "bearer-trust-key.json")
	rawKey, err := json.Marshal(struct {
		KeyID  string `json:"keyId"`
		Secret []byte `json:"secret"`
	}{spoofTrustKeyID, []byte(spoofTrustSecret)})
	if err != nil {
		t.Fatalf("marshal trust key: %v", err)
	}
	if err := os.WriteFile(keyPath, rawKey, 0o600); err != nil {
		t.Fatalf("write trust key: %v", err)
	}
	gw := gateway.StartWith(t, "--dev-mode", "--tenancy-mode=multi", "--multi-tenant",
		"--bearer-trust-hmac-key-file="+keyPath)
	base := gw.BaseURL()
	me := base + "/v1/admin/me"

	admin := map[string]string{
		"X-Lenny-Tenant-ID": auth.DefaultTenantID,
		"X-Lenny-User-ID":   "ops@acme.com",
		"X-Lenny-Roles":     "platform-admin",
	}
	for _, id := range []string{"acme", "globex"} {
		if code, body := spoofCall(t, http.MethodPost, base+"/v1/admin/tenants", "", admin,
			map[string]any{"id": id, "displayName": id}); code != http.StatusCreated {
			t.Fatalf("create tenant %s: status %d (%v)\n%s", id, code, body, gw.Logs())
		}
	}
	globex := spoofMint(t, spoofTrustKeyID, spoofTrustSecret, "globex")

	refusals := []struct {
		name       string
		bearer     string
		headers    map[string]string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "unregistered tenant claim",
			bearer:     spoofMint(t, spoofTrustKeyID, spoofTrustSecret, "initech"),
			wantStatus: http.StatusForbidden, wantCode: "TENANT_NOT_FOUND",
		},
		{
			name:       "registered tenant under an untrusted key",
			bearer:     spoofMint(t, spoofTrustKeyID, "attacker-chosen-secret-of-32-bytes!!", "acme"),
			wantStatus: http.StatusUnauthorized, wantCode: "TOKEN_INVALID",
		},
		{
			name:       "tenant claim rewritten after signing",
			bearer:     rewriteTenantClaim(t, globex, "acme"),
			wantStatus: http.StatusUnauthorized, wantCode: "TOKEN_INVALID",
		},
		{
			name:       "malformed claim aimed at another tenant",
			bearer:     spoofMint(t, spoofTrustKeyID, spoofTrustSecret, "globex/../acme"),
			wantStatus: http.StatusUnauthorized, wantCode: "TENANT_CLAIM_INVALID_FORMAT",
		},
		{
			name:       "dev header naming an unregistered tenant",
			headers:    map[string]string{"X-Lenny-Tenant-ID": "initech", "X-Lenny-User-ID": "mallory"},
			wantStatus: http.StatusForbidden, wantCode: "TENANT_NOT_FOUND",
		},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			code, body := spoofCall(t, http.MethodGet, me, tc.bearer, tc.headers, nil)
			if code != tc.wantStatus || spoofErrorCode(body) != tc.wantCode {
				t.Fatalf("status %d code %q (%v), want %d %s", code, spoofErrorCode(body), body, tc.wantStatus, tc.wantCode)
			}
		})
	}

	t.Run("dev header cannot override the signed tenant", func(t *testing.T) {
		code, body := spoofCall(t, http.MethodGet, me, globex, map[string]string{"X-Lenny-Tenant-ID": "acme"}, nil)
		if code != http.StatusOK {
			t.Fatalf("status %d (%v), want 200", code, body)
		}
		if got, _ := body["tenantId"].(string); got != "globex" {
			t.Fatalf("tenantId = %q, want globex: a client header selected the tenant", got)
		}
	})
}
