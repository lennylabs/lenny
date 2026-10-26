// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the lenny-ops revocation set (§25.4
// "Authentication", revocation set rules 1 to 7) against real
// cmd/lenny-gateway, cmd/lenny-token-service, and cmd/lenny-ops processes
// over one Postgres and one Redis, with the gateway serving the apiKey-mode
// playground. A Lenny-issued token revoked through
// rotation, through §11.4 user invalidation, or before its tenant is
// deleted must be refused on lenny-ops within one 35 s read bound, with
// lenny-ops's Redis link up and with it cut, and the deleted tenant's
// revocation must survive into a fresh lenny-ops process. A playground
// bearer the gateway mints for a tenant-admin user must be refused on
// lenny-ops (rule 7), and as a caller of
// POST /v1/oauth/token through a gateway replica (§13.3).
package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/stackkey"
	"github.com/lennylabs/lenny/tests/testinfra/tcpfault"
	tokensvc "github.com/lennylabs/lenny/tests/testinfra/tokenservice"
)

// opsRevBound is the §25.4 bound on lenny-ops refusing a revoked token: the
// 30 s read interval plus margin for the read.
const opsRevBound = 35 * time.Second

// opsRevAlias maps platform:jwt-signing on every process of the stack.
const opsRevAlias = "--kms-alias=" + kms.AliasJWTSigning + "=local-key-1"

// opsRevStack is a gateway, a Token Service, and lenny-ops over one
// Postgres and one Redis. lenny-ops reaches Redis through redisLink so a
// test can cut that link alone.
type opsRevStack struct {
	pg        *containers.Postgres
	gw        *gateway.Process
	ts        *tokensvc.Process
	ops       *opsprocess.Process
	opsOpts   opsprocess.Options
	redisLink *tcpfault.Proxy
	// owner is the top-level test. A lenny-ops process restarted inside a
	// subtest is stopped at owner's cleanup, so it serves every later
	// subtest of the same stack.
	owner testing.TB
}

// ownerScopedTB reports failures through the subtest it wraps and
// registers its cleanups and logs on the stack's top-level test, because
// a cleanup runs after the subtest has completed.
type ownerScopedTB struct {
	testing.TB
	owner testing.TB
}

// Cleanup registers f on the top-level test rather than on the subtest.
func (o ownerScopedTB) Cleanup(f func()) { o.owner.Cleanup(f) }

// Logf logs on the top-level test, which is still running when a cleanup
// registered through Cleanup logs.
func (o ownerScopedTB) Logf(format string, args ...any) { o.owner.Logf(format, args...) }

// restartOps stops the running lenny-ops and starts a fresh one whose
// lifetime is the stack's top-level test, so the restart a subtest makes
// does not end with that subtest.
func (s *opsRevStack) restartOps(t *testing.T) {
	t.Helper()
	s.ops.Stop(t)
	s.ops = opsprocess.StartOpts(ownerScopedTB{TB: t, owner: s.owner}, s.opsOpts)
}

// startOpsRevStack starts the stack and seeds acme, globex, and initech.
func startOpsRevStack(t *testing.T) *opsRevStack {
	t.Helper()
	stackkey.MasterKeyFile(t)
	s := &opsRevStack{owner: t, pg: containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})}
	rd := containers.StartRedis(t, containers.RedisOptions{})
	s.ts = tokensvc.StartWith(t, "--postgres-dsn="+s.pg.DSN, "--kms-provider=local", opsRevAlias)
	s.gw = gateway.StartWith(t,
		"--no-environment-policy", "allow-all", "--dev-mode",
		"--postgres-dsn="+s.pg.DSN, "--postgres-billing-audit-ddl-dsn="+s.pg.DSN,
		"--redis-url=redis://"+rd.Addr+"/0",
		"--playground-enabled", "--playground-auth-mode=apiKey",
		"--tenancy-mode=multi", "--multi-tenant=true",
		"--tenant-deletion-interval=300ms",
		"--token-service-http-url="+s.ts.BaseURL(),
		"--kms-provider=local", opsRevAlias)
	var opsRedis string
	s.redisLink, opsRedis = tcpfault.StartURL(t, "redis://"+rd.Addr+"/0")
	s.opsOpts = opsprocess.Options{
		Binary: opsprocess.Build(t),
		Args: append([]string{
			"--postgres-dsn=" + s.pg.DSN, "--redis-url=" + opsRedis,
			"--kms-provider=local", opsRevAlias,
		}, opsprocess.AuthArgs(t)...),
	}
	s.ops = opsprocess.StartOpts(t, s.opsOpts)
	s.admin(t, http.MethodPost, "/v1/admin/bootstrap", map[string]any{
		"tenants": []map[string]any{
			{"id": "acme", "displayName": "Acme Corp"},
			{"id": "globex", "displayName": "Globex"},
			{"id": "initech", "displayName": "Initech"},
		},
	}, http.StatusOK)
	return s
}

// admin issues an admin request as a platform-admin on the default tenant
// and fails the test unless it answers want. It returns the decoded body.
func (s *opsRevStack) admin(t *testing.T, method, path string, body any, want int) map[string]any {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, s.gw.BaseURL()+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", auth.DefaultTenantID)
	req.Header.Set("X-Lenny-User-ID", "carol@acme.com")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s: status %d, want %d: %s\n%s", method, path, resp.StatusCode, want, out, s.gw.Logs())
	}
	var decoded map[string]any
	_ = json.Unmarshal(out, &decoded)
	return decoded
}

// tenantAdmin creates subject as a tenant-admin of tenant and returns two
// impersonation bearers for it, each with its own jti.
func (s *opsRevStack) tenantAdmin(t *testing.T, tenant, subject string) (string, string) {
	t.Helper()
	s.admin(t, http.MethodPost, "/v1/admin/users", map[string]any{
		"tenantId": tenant, "subject": subject, "roles": []string{"tenant-admin"},
	}, http.StatusCreated)
	mint := func() string {
		body := s.admin(t, http.MethodPost, "/v1/admin/impersonation", map[string]any{
			"targetTenantId": tenant, "targetUserId": subject,
			"reason": "lenny-ops revocation probe", "ticketId": "ops-revocation", "durationSeconds": 600,
		}, http.StatusOK)
		tok, _ := body["bearerToken"].(string)
		if tok == "" {
			t.Fatalf("impersonation of %s/%s returned no bearer: %v", tenant, subject, body)
		}
		return tok
	}
	return mint(), mint()
}

// exchange mints a Token Service token through the gateway's
// POST /v1/oauth/token, which records an issued_tokens row for it.
func (s *opsRevStack) exchange(t *testing.T, caller, subject string) string {
	t.Helper()
	code, body := forgeryExchange(t, s.gw.BaseURL(), caller, subject)
	tok, _ := body["access_token"].(string)
	if code != http.StatusOK || tok == "" {
		t.Fatalf("token exchange: status %d body %v\n%s", code, body, s.ts.Logs())
	}
	return tok
}

// rotate self-rotates tok, which revokes it in issued_tokens (§13.3).
func (s *opsRevStack) rotate(t *testing.T, tok string) {
	t.Helper()
	s.exchange(t, tok, tok)
}

// awaitRowRevoked polls issued_tokens until tok's row carries revoked_at.
func (s *opsRevStack) awaitRowRevoked(t *testing.T, tok string) {
	t.Helper()
	jti := tokenJTI(t, tok)
	deadline := time.Now().Add(15 * time.Second)
	for {
		var revoked bool
		err := s.pg.Pool.QueryRow(context.Background(),
			`SELECT revoked_at IS NOT NULL FROM issued_tokens WHERE jti = $1`, jti).Scan(&revoked)
		if err == nil && revoked {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("issued_tokens row %s not revoked within 15 s (err %v)", jti, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// awaitTenantDeleted polls the tenant row until the deletion tombstone is
// written.
func (s *opsRevStack) awaitTenantDeleted(t *testing.T, tenant string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		var state string
		err := s.pg.Pool.QueryRow(context.Background(),
			`SELECT coalesce(state, '') FROM tenants WHERE id = $1`, tenant).Scan(&state)
		if err == nil && state == "deleted" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("tenant %s state %q 90 s after DELETE, want deleted (err %v)\n%s", tenant, state, err, s.gw.Logs())
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// requireRevokedOnOps asserts lenny-ops refuses tok with the gateway's
// revoked-token response within opsRevBound of its row being revoked.
func (s *opsRevStack) requireRevokedOnOps(t *testing.T, tok string) {
	t.Helper()
	s.awaitRowRevoked(t, tok)
	s.ops.WaitRefused(t, tok, http.StatusUnauthorized, opsprocess.CodeRevoked, opsRevBound)
}

// tokenJTI returns the jti claim of a compact JWS.
func tokenJTI(t *testing.T, tok string) string {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var c struct {
		JTI string `json:"jti"`
	}
	if err := json.Unmarshal(raw, &c); err != nil || c.JTI == "" {
		t.Fatalf("payload carries no jti: %s (%v)", raw, err)
	}
	return c.JTI
}

// revocationPaths revokes a token through each path and asserts lenny-ops
// refuses it. Each revoked token is a POST /v1/oauth/token token, which
// carries no roles claim, so before its revocation lenny-ops admits it
// through authentication and the revocation set and its role gate answers
// 403; afterwards the revocation set refuses it with the gateway's
// revoked-token response. users names the acme users this pass creates and tenant the
// tenant it deletes.
func (s *opsRevStack) revocationPaths(t *testing.T, rotUser, invalidUser, tenant, tenantUser string) {
	t.Helper()
	t.Run("rotation", func(t *testing.T) {
		a, b := s.tenantAdmin(t, "acme", rotUser)
		tok := s.exchange(t, a, b)
		s.ops.WaitAuthenticated(t, tok, opsRevBound)
		s.rotate(t, tok)
		s.requireRevokedOnOps(t, tok)
	})
	t.Run("user invalidation", func(t *testing.T) {
		a, b := s.tenantAdmin(t, "acme", invalidUser)
		tok := s.exchange(t, a, b)
		s.ops.WaitAuthenticated(t, tok, opsRevBound)
		s.admin(t, http.MethodPost, "/v1/admin/users/"+invalidUser+"/invalidate", map[string]any{
			"tenantId": "acme", "mode": "full_revoke", "reason": "lenny-ops revocation probe",
		}, http.StatusOK)
		s.requireRevokedOnOps(t, tok)
	})
	t.Run("tenant deletion", func(t *testing.T) {
		a, b := s.tenantAdmin(t, tenant, tenantUser)
		tok := s.exchange(t, a, b)
		s.rotate(t, tok)
		s.requireRevokedOnOps(t, tok)
		s.admin(t, http.MethodDelete, "/v1/admin/tenants/"+tenant, nil, http.StatusAccepted)
		s.awaitTenantDeleted(t, tenant)
		// A fresh process loads the deleted tenant's revocations from its
		// first read, because the read lists tombstoned tenants. The fresh
		// process outlives this subtest, so a later pass runs against it.
		s.restartOps(t)
		s.ops.WaitRefused(t, tok, http.StatusUnauthorized, opsprocess.CodeRevoked, opsRevBound)
	})
}

// spec: 25.4 (The lenny-ops Service), 13.3 (Credential Flow), 11.4 (User Invalidation), 12.8 (Compliance Interfaces)
// diagnosis: a revoked token admitted on lenny-ops past the 35 s bound
// means lenny-ops does not load the revoked issued_tokens rows on its 30 s
// read, so a Lenny-issued token revoked through rotation or §11.4 user
// invalidation stays usable on the operability surface. A failure only
// with the Redis link cut means lenny-ops relies on the event path and its
// read loop is not running. A deleted tenant's token admitted after the
// restart means the read omits tombstoned tenants.
func TestOpsRefusesRevokedLennyTokens_spec_25_4(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	s := startOpsRevStack(t)

	t.Run("Redis up", func(t *testing.T) {
		s.revocationPaths(t, "bob@acme.com", "dave@acme.com", "globex", "erin@globex.com")
	})
	s.redisLink.Cut()
	t.Run("Redis stopped", func(t *testing.T) {
		// The process the first pass restarted is still serving; a stopped
		// one fails here with a connection error rather than an outcome.
		resp, err := http.Get(s.ops.BaseURL() + "/readyz")
		if err != nil {
			t.Fatalf("lenny-ops restarted by the first pass is not serving: %v", err)
		}
		_ = resp.Body.Close()
		s.revocationPaths(t, "frank@acme.com", "grace@acme.com", "initech", "heidi@initech.com")
	})
}

// mintPlayground mints a playground bearer through the gateway's
// POST /v1/playground/token in apiKey mode from the user bearer pasted,
// so the gateway's own playground mint stamps every claim. It fails the
// test on any outcome but 200.
func (s *opsRevStack) mintPlayground(t *testing.T, pasted string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.gw.BaseURL()+"/v1/playground/token", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("new playground mint request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+pasted)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/playground/token: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		BearerToken string `json:"bearerToken"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &out) != nil || out.BearerToken == "" {
		t.Fatalf("POST /v1/playground/token: status %d: %s\n%s", resp.StatusCode, raw, s.gw.Logs())
	}
	return out.BearerToken
}

// requirePlaygroundAdmittedOnGateway asserts the gateway that minted
// playground admits it as a tenant-admin of acme, so a refusal elsewhere
// rests on the bearer's playground origin rather than on a malformed
// token.
func (s *opsRevStack) requirePlaygroundAdmittedOnGateway(t *testing.T, playground string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.gw.BaseURL()+"/v1/admin/me", nil)
	if err != nil {
		t.Fatalf("new /v1/admin/me request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+playground)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/admin/me: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var me struct {
		TenantID string      `json:"tenantId"`
		Typ      string      `json:"typ"`
		Roles    []auth.Role `json:"roles"`
	}
	_ = json.Unmarshal(raw, &me)
	if resp.StatusCode != http.StatusOK || me.TenantID != "acme" || !slices.Contains(me.Roles, auth.RoleTenantAdmin) {
		t.Fatalf("gateway /v1/admin/me with the playground bearer: status %d: %s, want 200 as an acme tenant-admin", resp.StatusCode, raw)
	}
}

// spec: 25.4 (The lenny-ops Service), 27.3.1 (OIDC cookie-to-MCP-bearer exchange), 13.3 (Credential Flow)
// diagnosis: a playground bearer admitted on lenny-ops means lenny-ops
// honours a bearer whose revocation it cannot check, because the playground
// revocation key lives in Redis and lenny-ops does not read it (rule 7),
// or the gateway's playground mint no longer stamps the origin claim that
// rule 7 keys on. An access_token returned through the gateway means the
// Token Service accepts a playground bearer as a caller credential, which
// mints a child that playground logout does not revoke.
func TestPlaygroundBearerRefusedOnOpsAndAsCaller_spec_25_4(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	s := startOpsRevStack(t)
	a, b := s.tenantAdmin(t, "acme", "alice@acme.com")
	s.ops.WaitAccepted(t, a, opsRevBound)

	// The gateway mints the playground bearer from alice's tenant-admin
	// user bearer, so it carries her tenant-admin role and the origin
	// claim the gateway's playground mint stamps. It has no issued_tokens
	// row, and its revocation key lives in Redis.
	playground := s.mintPlayground(t, a)
	s.requirePlaygroundAdmittedOnGateway(t, playground)

	got := s.ops.Me(t, playground)
	if got.Status != http.StatusUnauthorized || got.Code != opsprocess.CodeUnauthorized || got.Reason != "bearer_revoked" {
		t.Errorf("lenny-ops: %d %s, want 401 %s with reason bearer_revoked", got.Status, got.Body, opsprocess.CodeUnauthorized)
	}
	for name, pair := range map[string][2]string{
		"caller token":                   {playground, b},
		"caller token and subject_token": {playground, playground},
	} {
		code, body := forgeryExchange(t, s.gw.BaseURL(), pair[0], pair[1])
		if code != http.StatusUnauthorized || body["error"] != "invalid_client" {
			t.Errorf("playground bearer as the %s through the gateway: status %d (%v), want 401 invalid_client", name, code, body)
		}
		if _, ok := body["access_token"]; ok {
			t.Errorf("playground bearer as the %s through the gateway returned an access_token", name)
		}
	}
}
