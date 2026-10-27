// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	tokensvc "github.com/lennylabs/lenny/tests/testinfra/tokenservice"
)

// cascadeRefusalBound bounds a peer replica's refusal of a revoked bearer:
// the Redis propagation, or failing it the 30 s rehydrate, plus margin.
const cascadeRefusalBound = 35 * time.Second

// cascadeAdminDo issues an admin request on the default tenant as a
// platform-admin named user and returns the status and body.
func cascadeAdminDo(t *testing.T, base, user, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "default")
	req.Header.Set("X-Lenny-User-ID", user)
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// mintImpersonationAs mints, on base, an impersonation bearer for subject in
// tenant started by the platform-admin admin.
func mintImpersonationAs(t *testing.T, base, admin, tenant, subject string) string {
	t.Helper()
	code, raw := cascadeAdminDo(t, base, admin, http.MethodPost, "/v1/admin/impersonation", map[string]any{
		"targetTenantId": tenant, "targetUserId": subject,
		"reason": "impersonation cascade probe", "ticketId": "cascade", "durationSeconds": 600,
	})
	var out struct {
		BearerToken string `json:"bearerToken"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &out) != nil || out.BearerToken == "" {
		t.Fatalf("impersonate %s/%s as %s on %s: status %d: %s", tenant, subject, admin, base, code, raw)
	}
	return out.BearerToken
}

// awaitRefusedOnReplicas polls GET /v1/admin/me with bearer on every replica
// until each answers 401 or cascadeRefusalBound passes.
func awaitRefusedOnReplicas(t *testing.T, name, bearer string, replicas []string) {
	t.Helper()
	awaitRefusedOnReplicasWithin(t, name, bearer, replicas, cascadeRefusalBound)
}

// awaitRefusedOnReplicasWithin polls GET /v1/admin/me with bearer on every
// replica until each answers 401 or bound passes.
func awaitRefusedOnReplicasWithin(t *testing.T, name, bearer string, replicas []string, bound time.Duration) {
	t.Helper()
	for _, base := range replicas {
		deadline := time.Now().Add(bound)
		for {
			code, _ := tenancyCall(t, http.MethodGet, base+"/v1/admin/me", bearer, nil, nil)
			if code == http.StatusUnauthorized {
				break
			}
			if time.Now().After(deadline) {
				t.Errorf("%s on %s answered %d %s after its revocation, want 401", name, base, code, bound)
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
}

// spec: 13.3 (Platform-admin impersonation), 11.4 (User Invalidation)
// diagnosis: a bearer still admitted on either replica after the full
// revoke means the §11.4 step 5 cascade did not revoke the impersonation
// bearers the revoked platform-admin minted, or the revoking replica did not
// push the cascade's JTIs into the cross-replica revocation cache. A
// tokenRevocationError on the response means the cascade or its
// cross_tenant_read emit failed on a gateway that wires the audit store.
func TestFullRevokeOfPlatformAdminRevokesItsBearersOnEveryReplica_spec_13_3(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ts := tokensvc.StartWith(t, "--postgres-dsn="+pg.DSN)
	args := deletionGatewayArgs(pg.DSN, rd.Addr, writeTenancyTrustKey(t), ts.BaseURL())
	gwA := gateway.StartWith(t, args...)
	gwB := gateway.StartWith(t, args...)
	replicas := []string{gwA.BaseURL(), gwB.BaseURL()}

	code, raw := deletionAdminDo(t, gwA.BaseURL(), http.MethodPost, "/v1/admin/bootstrap", map[string]any{
		"tenants": []map[string]any{
			{"id": "acme", "displayName": "Acme Corp"},
			{"id": "globex", "displayName": "Globex"},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d: %s", code, raw)
	}
	const revokedAdmin = "dave@acme.com"
	createTenantUser(t, gwA.BaseURL(), "default", revokedAdmin)
	createTenantUser(t, gwA.BaseURL(), "acme", "bob@acme.com")
	createTenantUser(t, gwA.BaseURL(), "globex", "erin@globex.com")
	bearers := map[string]string{
		"acme impersonation":   mintImpersonationAs(t, gwA.BaseURL(), revokedAdmin, "acme", "bob@acme.com"),
		"globex impersonation": mintImpersonationAs(t, gwA.BaseURL(), revokedAdmin, "globex", "erin@globex.com"),
	}
	for name, tok := range bearers {
		for _, base := range replicas {
			if code, _ := tenancyCall(t, http.MethodGet, base+"/v1/admin/me", tok, nil, nil); code != http.StatusOK {
				t.Fatalf("%s on %s answered %d before the revoke, want 200", name, base, code)
			}
		}
	}

	code, raw = cascadeAdminDo(t, gwB.BaseURL(), "carol@acme.com", http.MethodPost,
		"/v1/admin/users/"+revokedAdmin+"/invalidate", map[string]any{
			"tenantId": "default", "mode": "full_revoke", "reason": "impersonation cascade probe",
		})
	if code != http.StatusOK {
		t.Fatalf("full revoke of %s: status %d: %s", revokedAdmin, code, raw)
	}
	var resp map[string]any
	_ = json.Unmarshal(raw, &resp)
	if v, ok := resp["tokenRevocationError"]; ok {
		t.Errorf("tokenRevocationError = %v, want none", v)
	}
	if n, _ := resp["tokensRevoked"].(float64); int(n) < len(bearers) {
		t.Errorf("tokensRevoked = %v, want at least %d", resp["tokensRevoked"], len(bearers))
	}
	for name, tok := range bearers {
		awaitRefusedOnReplicas(t, name, tok, replicas)
	}
}
