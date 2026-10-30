// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §10.2 registered reading and the
// IdP-scoped tenant claim on a multi-tenant gateway backed by Postgres.
// It runs the real cmd/lenny-gateway binary with a trusted embedded HMAC
// key and a non-default --tenant-id-claim, and checks that an IdP-signed
// bearer cannot choose its tenant through `tenant_id`, that a bearer for
// a tenant in `deleting` is refused with TENANT_NOT_FOUND, that the
// refusal's auth_failure row lands on the platform audit chain rather
// than on the refused tenant's chain, and that a globex bearer stays
// admitted while acme is deleting and after acme's tombstone.
package tier9_security_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

const (
	lifecycleTrustKeyID  = "tenant-lifecycle-sec"
	lifecycleTrustSecret = "tenant-lifecycle-security-hmac-secret"
	lifecycleTenantClaim = "https://acme.example/tenant"
)

// lifecycleMint signs an embedded-IdP user bearer for alice carrying the
// given payload claims on top of the standard ones.
func lifecycleMint(t *testing.T, extra map[string]any) string {
	t.Helper()
	now := time.Now()
	payload := map[string]any{
		"sub":   "alice@acme.com",
		"roles": []string{string(auth.RoleUser)},
		"typ":   string(auth.TokenUserBearer),
		"aud":   []string{"dev.local"},
		"iat":   now.Unix(),
		"exp":   now.Add(5 * time.Minute).Unix(),
		"jti":   "lifecycle-" + now.Format(time.RFC3339Nano),
	}
	for k, v := range extra {
		payload[k] = v
	}
	return signRawHS256(t, lifecycleTrustKeyID, []byte(lifecycleTrustSecret), payload)
}

// signRawHS256 signs payload verbatim as an HS256 JWT under kid, so the
// token can carry the configured tenant claim, which jwt.Claims has no
// typed field for.
func signRawHS256(t *testing.T, kid string, secret []byte, payload map[string]any) string {
	t.Helper()
	hb, err := json.Marshal(map[string]any{"alg": "HS256", "typ": "JWT", "kid": kid})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	pb, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signing))
	return signing + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// spec: 10.2 (Authentication: `tenant_id` extraction), 12.8 (Compliance Interfaces: Tenant deletion lifecycle), 11.7 (Audit Logging)
// diagnosis: a failure means a multi-tenant gateway lets a caller reach a
// tenant it should not: an IdP-signed bearer chose its tenant through its
// own `tenant_id` instead of the configured claim, a bearer for a tenant
// in `deleting` was still admitted (the registry read DeletedAt only), or
// the refusal was written to the refused tenant's audit chain instead of
// the platform chain with a top-level target_tenant_id. A globex bearer
// refused while acme is deleting or after acme's tombstone means the
// registry's refusal crossed tenants.
func TestDeletingTenantBearerRefusedAndAuditedOnPlatformChain(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations")})
	keyPath := filepath.Join(t.TempDir(), "bearer-trust-key.json")
	rawKey, err := json.Marshal(struct {
		KeyID  string `json:"keyId"`
		Secret []byte `json:"secret"`
	}{lifecycleTrustKeyID, []byte(lifecycleTrustSecret)})
	if err != nil {
		t.Fatalf("marshal trust key: %v", err)
	}
	if err := os.WriteFile(keyPath, rawKey, 0o600); err != nil {
		t.Fatalf("write trust key: %v", err)
	}
	gw := gateway.StartWith(t, "--dev-mode", "--tenancy-mode=multi", "--multi-tenant",
		"--no-environment-policy", "allow-all",
		"--postgres-dsn="+pg.DSN,
		"--bearer-trust-hmac-key-file="+keyPath,
		"--tenant-id-claim="+lifecycleTenantClaim)
	base := gw.BaseURL()
	me := base + "/v1/admin/me"

	admin := map[string]string{
		"X-Lenny-Tenant-ID": auth.DefaultTenantID,
		"X-Lenny-User-ID":   "ops@acme.com",
		"X-Lenny-Roles":     "platform-admin",
	}
	if code, body := spoofCall(t, http.MethodPost, base+"/v1/admin/tenants", "", admin,
		map[string]any{"id": "acme", "displayName": "Acme Corp"}); code != http.StatusCreated {
		t.Fatalf("create tenant acme: status %d (%v)\n%s", code, body, gw.Logs())
	}

	if code, body := spoofCall(t, http.MethodPost, base+"/v1/admin/tenants", "", admin,
		map[string]any{"id": "globex", "displayName": "Globex"}); code != http.StatusCreated {
		t.Fatalf("create tenant globex: status %d (%v)\n%s", code, body, gw.Logs())
	}
	globex := lifecycleMint(t, map[string]any{lifecycleTenantClaim: "globex"})
	requireAdmittedAs(t, me, globex, "globex", "before acme's deletion")

	acme := lifecycleMint(t, map[string]any{lifecycleTenantClaim: "acme"})
	if code, body := spoofCall(t, http.MethodGet, me, acme, nil, nil); code != http.StatusOK || body["tenantId"] != "acme" {
		t.Fatalf("configured claim: status %d (%v), want 200 as acme\n%s", code, body, gw.Logs())
	}

	onlyTenantID := lifecycleMint(t, map[string]any{"tenant_id": "acme"})
	if code, body := spoofCall(t, http.MethodGet, me, onlyTenantID, nil, nil); code != http.StatusUnauthorized ||
		spoofErrorCode(body) != "TENANT_CLAIM_MISSING" {
		t.Fatalf("IdP bearer with tenant_id only: status %d code %q (%v), want 401 TENANT_CLAIM_MISSING",
			code, spoofErrorCode(body), body)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, pg.DSN)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	defer pool.Close()
	// §12.8 Phase 3 commits `deleting`; DeletedAt stays NULL until the
	// Phase 6 tombstone, so only the State half of the registered
	// reading refuses the bearer here.
	if _, err := pool.Exec(ctx, `UPDATE tenants SET state = 'deleting' WHERE id = 'acme'`); err != nil {
		t.Fatalf("move acme to deleting: %v", err)
	}
	if code, body := spoofCall(t, http.MethodGet, me, acme, nil, nil); code != http.StatusForbidden ||
		spoofErrorCode(body) != "TENANT_NOT_FOUND" {
		t.Fatalf("bearer for a deleting tenant: status %d code %q (%v), want 403 TENANT_NOT_FOUND\n%s",
			code, spoofErrorCode(body), body, gw.Logs())
	}
	requireAdmittedAs(t, me, globex, "globex", "while acme is deleting")

	var platformRows, acmeRows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE tenant_id = 'platform' AND payload->>'target_tenant_id' = 'acme'
		                        AND payload->'detail'->>'reason' = 'TENANT_NOT_FOUND'),
		       count(*) FILTER (WHERE tenant_id = 'acme' AND payload->'detail'->>'reason' = 'TENANT_NOT_FOUND')
		  FROM audit_log WHERE event_type = 'auth_failure'`).Scan(&platformRows, &acmeRows); err != nil {
		t.Fatalf("read auth_failure rows: %v", err)
	}
	if platformRows != 1 {
		t.Errorf("platform-chain TENANT_NOT_FOUND rows targeting acme = %d, want 1\n%s", platformRows, gw.Logs())
	}
	if acmeRows != 0 {
		t.Errorf("TENANT_NOT_FOUND rows on acme's own chain = %d, want 0", acmeRows)
	}

	// §12.8 Phase 6 tombstone: the registry keeps refusing acme and keeps
	// admitting globex.
	if _, err := pool.Exec(ctx, `UPDATE tenants SET state = 'deleted', deleted_at = now() WHERE id = 'acme'`); err != nil {
		t.Fatalf("tombstone acme: %v", err)
	}
	if code, body := spoofCall(t, http.MethodGet, me, acme, nil, nil); code != http.StatusForbidden ||
		spoofErrorCode(body) != "TENANT_NOT_FOUND" {
		t.Errorf("bearer for a tombstoned tenant: status %d code %q, want 403 TENANT_NOT_FOUND", code, spoofErrorCode(body))
	}
	requireAdmittedAs(t, me, globex, "globex", "after acme's tombstone")
}

// requireAdmittedAs asserts the bearer is admitted as tenant.
func requireAdmittedAs(t *testing.T, url, bearer, tenant, when string) {
	t.Helper()
	if code, body := spoofCall(t, http.MethodGet, url, bearer, nil, nil); code != http.StatusOK || body["tenantId"] != tenant {
		t.Fatalf("%s bearer %s: status %d (%v), want 200 as %s", tenant, when, code, body, tenant)
	}
}
