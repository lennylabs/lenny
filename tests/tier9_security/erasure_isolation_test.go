// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security tests for the §12.8 erasure job and creation fence
// isolation boundaries.
//
// The first test drives the admin router over the production Postgres
// erasure job store, with the §10.2 principal a verified bearer token
// yields injected as the admin_scope_enforce tests do: a globex
// tenant-admin cannot read, retry, or clear an acme erasure job, and the
// status handler reads the job under globex's tenant scope.
//
// The second test runs the real cmd/lenny-gateway binary over Postgres with
// a trusted HMAC bearer key and signs a bearer token per caller, so every
// request authenticates through the gateway's own §10.2 verifier. It pins
// the Article 18 refusal rows of §12.8 on every client creation surface,
// the source-owner check of a derive, the caller check and the triple of
// the MCP elicitation-resolution tools, and the removal of
// DELETE /v1/responses/{id}.
//
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)),
// §12.8 (Compliance Interfaces: Lifecycle fence and drain), §15.1 (REST
// API), §9.2 (respond_to_elicitation authorization).
package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	tenantpg "github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	userpg "github.com/lennylabs/lenny/pkg/gateway/environment/userstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
	erasurepg "github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// recordingJobs records the tenant scope of every Get.
type recordingJobs struct {
	erasurejob.Store
	mu     sync.Mutex
	scopes []string
}

func (r *recordingJobs) Get(ctx context.Context, tenantID, id string) (erasurejob.Job, error) {
	r.mu.Lock()
	r.scopes = append(r.scopes, tenantID)
	r.mu.Unlock()
	return r.Store.Get(ctx, tenantID, id)
}

func (r *recordingJobs) lastScope() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.scopes) == 0 {
		return ""
	}
	return r.scopes[len(r.scopes)-1]
}

// adminCall serves one admin request as principal and returns the status
// and the error code of the body, if any.
func adminCall(t *testing.T, h http.Handler, p authmw.Principal, method, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(`{"justification":"operator review"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authmw.WithPrincipal(req.Context(), p))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	return rr.Code, env.Error.Code
}

// TestErasureJobCrossTenantIsolation_spec_12_8 seeds an acme erasure job in
// the Postgres store and calls the status, retry, and
// clear-processing-restriction endpoints as a globex tenant-admin.
//
// spec: 12.8 (User-level erasure invocation), 12.8 (Processing restriction during erasure (GDPR Article 18)), 15.1 (REST API)
// diagnosis: a failure means a tenant-admin of one tenant can see or act on
// another tenant's erasure job: the status handler read the job under a
// scope other than the caller's tenant, the store's tenant filter returned
// a foreign job, or retry or the restriction clear admitted a caller below
// platform-admin.
func TestErasureJobCrossTenantIsolation_spec_12_8(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: schematest.RepoRoot(t) + "/migrations"})
	ctx := context.Background()
	for _, id := range []string{"acme", "globex"} {
		if _, err := pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x01')`, id); err != nil {
			t.Fatalf("seed tenant %s: %v", id, err)
		}
	}
	users := userpg.New(pg.Pool)
	if err := users.Create(ctx, userstore.User{TenantID: "acme", Subject: "alice@acme.com"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	jobs := &recordingJobs{Store: erasurepg.New(pg.Pool)}
	now := time.Now().UTC()
	job, err := jobs.Start(ctx, erasurejob.Job{
		ID: erasurejob.NewID(), TenantID: "acme", UserID: "alice@acme.com",
		Phase: erasurejob.PhaseInitiated, StartedAt: now,
		PhaseLog: []erasurejob.PhaseTransition{{Phase: erasurejob.PhaseInitiated, At: now}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h := admin.NewRouter(tenantpg.New(pg.Pool), admin.Options{}).
		WithErasure(erasurejob.NewRunner(jobs, nil, nil), jobs).
		WithUsers(users).
		Handler()
	globex := authmw.Principal{TenantID: "globex", Subject: "bob@globex.com", Roles: []pkgauth.Role{pkgauth.RoleTenantAdmin}}
	acme := authmw.Principal{TenantID: "acme", Subject: "carol@acme.com", Roles: []pkgauth.Role{pkgauth.RoleTenantAdmin}}
	base := "/v1/admin/erasure-jobs/" + job.ID

	if code, ec := adminCall(t, h, globex, http.MethodGet, base); code != http.StatusNotFound {
		t.Errorf("globex GET of an acme job = %d %s, want 404", code, ec)
	}
	if got := jobs.lastScope(); got != "globex" {
		t.Errorf("the status handler read the job under scope %q, want globex", got)
	}
	if _, err := jobs.Store.Get(ctx, "globex", job.ID); !errors.Is(err, erasurejob.ErrNotFound) {
		t.Errorf("store Get(globex, acme job) = %v, want ErrNotFound", err)
	}
	for _, path := range []string{base + "/retry", base + "/clear-processing-restriction"} {
		if code, ec := adminCall(t, h, globex, http.MethodPost, path); code != http.StatusForbidden || ec != "FORBIDDEN" {
			t.Errorf("globex POST %s = %d %s, want 403 FORBIDDEN", path, code, ec)
		}
	}
	// The job's own tenant-admin reads it, so the 404 above is the tenant
	// boundary rather than a missing route.
	if code, ec := adminCall(t, h, acme, http.MethodGet, base); code != http.StatusOK {
		t.Errorf("acme GET of its own job = %d %s, want 200", code, ec)
	}
	if u, err := users.Get(ctx, "acme", "alice@acme.com"); err != nil || !u.ProcessingRestricted {
		t.Errorf("the refused calls changed the restriction: %+v, %v", u, err)
	}
}

// liveGateway is the real gateway binary over Postgres with a trusted
// HMAC bearer key, plus the test's own handles on the tenant and user
// stores for setting lifecycle markers.
type liveGateway struct {
	t       *testing.T
	base    string
	signer  *jwt.HMACSigner
	tenants tenantstore.Store
	users   userstore.Store
	pgExec  func(sql string, args ...any) int
	pool    *pgxpool.Pool
}

const liveTenant = "acme"

// startLiveGateway boots the gateway and bootstraps acme with the echo
// runtimes and the users alice, bob (users), and carol (tenant-admin).
func startLiveGateway(t *testing.T) *liveGateway {
	t.Helper()
	gateway.SkipUnlessAvailable(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations")})
	const keyID, secret = "tier9-erasure-isolation", "tier9-erasure-isolation-hmac-secret-0123456789"
	raw, err := json.Marshal(struct {
		KeyID  string `json:"keyId"`
		Secret []byte `json:"secret"`
	}{keyID, []byte(secret)})
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "bearer-trust-key.json")
	if err := os.WriteFile(keyPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// The tenant markers and the users live in acme, so the gateway runs
	// multi-tenant: a single-tenant gateway ignores the bearer's tenant
	// claim and serves every request as the built-in default tenant, where
	// no marker or user row exists (§10.2 tenant-claim extraction).
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all", "--dev-mode",
		"--tenancy-mode=multi", "--multi-tenant",
		"--postgres-dsn="+pg.DSN, "--bearer-trust-hmac-key-file="+keyPath)
	g := &liveGateway{
		t: t, base: gw.BaseURL(), signer: jwt.NewHMACSigner(keyID, []byte(secret)),
		tenants: tenantpg.New(pg.Pool), users: userpg.New(pg.Pool), pool: pg.Pool,
		pgExec: func(sql string, args ...any) int {
			var n int
			if err := pg.Pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
				t.Fatalf("query %q: %v", sql, err)
			}
			return n
		},
	}
	runtime := func(name string) map[string]any {
		return map[string]any{
			"name": name, "image": "lenny/echo@sha256:abc", "labels": map[string]string{"tier": "test"},
			"capabilities": map[string]any{"injection": map[string]any{"supported": true}},
		}
	}
	user := func(sub string, role string) map[string]any {
		return map[string]any{"subject": sub, "tenantId": liveTenant, "email": sub, "roles": []string{role}}
	}
	// A multi-tenant gateway refuses a bearer naming an unregistered tenant,
	// so the platform admin who registers acme authenticates in the built-in
	// default tenant.
	code, body := g.restIn(pkgauth.DefaultTenantID, "ops@acme.com", pkgauth.RolePlatformAdmin, http.MethodPost, "/v1/admin/bootstrap", map[string]any{
		"tenants":  []map[string]any{{"id": liveTenant, "displayName": "Acme Corp"}},
		"runtimes": []map[string]any{runtime("echo"), runtime("echo-child")},
		"users": []map[string]any{
			user("alice@acme.com", "user"), user("bob@acme.com", "user"), user("carol@acme.com", "tenant-admin"),
		},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: %d %v", code, body)
	}
	return g
}

// bearer signs a token for sub in tenant with role.
func (g *liveGateway) bearer(tenant, sub string, role pkgauth.Role) string {
	g.t.Helper()
	now := time.Now()
	tok, err := g.signer.Sign(jwt.Claims{
		Subject: sub, TenantID: tenant, Roles: []pkgauth.Role{role},
		Audience: []string{"dev.local"}, IssuedAt: now.Unix(), Expiry: now.Add(time.Hour).Unix(),
		JWTID: sub + "-" + now.Format(time.RFC3339Nano),
	})
	if err != nil {
		g.t.Fatalf("sign bearer: %v", err)
	}
	return tok
}

// rest issues a REST request as sub in acme and returns the status and the
// body.
func (g *liveGateway) rest(sub string, role pkgauth.Role, method, path string, body any) (int, map[string]any) {
	g.t.Helper()
	return g.restIn(liveTenant, sub, role, method, path, body)
}

// restIn is rest with the bearer's tenant claim set to tenant.
func (g *liveGateway) restIn(tenant, sub string, role pkgauth.Role, method, path string, body any) (int, map[string]any) {
	g.t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, g.base+path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.bearer(tenant, sub, role))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		g.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// tool calls an MCP tool on /mcp as sub and returns whether the result is
// an error, its lenny error code, and its first text block.
func (g *liveGateway) tool(sub string, role pkgauth.Role, name string, args map[string]any) (bool, string, string) {
	g.t.Helper()
	a, _ := json.Marshal(args)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + string(a) + `}}`
	req, _ := http.NewRequest(http.MethodPost, g.base+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.bearer(liveTenant, sub, role))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		g.t.Fatalf("POST /mcp %s: %v", name, err)
	}
	defer resp.Body.Close()
	var rpc struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Data struct {
				Code string `json:"code"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		g.t.Fatalf("decode /mcp %s response: %v", name, err)
	}
	if rpc.Error != nil {
		return true, rpc.Error.Data.Code, ""
	}
	var first, code string
	for i, c := range rpc.Result.Content {
		if i == 0 {
			first = c.Text
		}
		var env struct {
			Code string `json:"code"`
		}
		if json.Unmarshal([]byte(c.Text), &env) == nil && env.Code != "" {
			code = env.Code
		}
	}
	return rpc.Result.IsError, code, first
}

// errorCode returns the code of a REST error body.
func errorCode(body map[string]any) string {
	env, _ := body["error"].(map[string]any)
	code, _ := env["code"].(string)
	return code
}

// liveMarker is one lifecycle marker on acme or alice, with its write and
// its reset.
type liveMarker struct {
	name string
	set  func(ctx context.Context, g *liveGateway) error
	// want is the code each creation surface answers; delegateWant is the
	// delegated child's, which differs only for a soft-disabled owner
	// (§11.4: a soft-disabled user's in-flight work keeps delegating).
	want, delegateWant string
}

func setUser(sub string, mutate func(*userstore.User)) func(context.Context, *liveGateway) error {
	return func(ctx context.Context, g *liveGateway) error {
		_, err := g.users.Update(ctx, liveTenant, sub, func(u *userstore.User) error { mutate(u); return nil })
		return err
	}
}

func setTenant(mutate func(*tenantstore.Tenant)) func(context.Context, *liveGateway) error {
	return func(ctx context.Context, g *liveGateway) error {
		_, err := g.tenants.Update(ctx, liveTenant, func(tn *tenantstore.Tenant) error { mutate(tn); return nil })
		return err
	}
}

// restrictAlice sets the Article 18 restriction an erasure job sets.
var restrictAlice = setUser("alice@acme.com", func(u *userstore.User) {
	u.ProcessingRestricted, u.ErasureJobID = true, "erasure_tier9"
})

func liveMarkers() []liveMarker {
	return []liveMarker{
		{"owner restricted", restrictAlice, "ERASURE_IN_PROGRESS", "ERASURE_IN_PROGRESS"},
		{"owner soft-disabled", setUser("alice@acme.com", func(u *userstore.User) { u.Disabled = true }), "USER_INVALIDATED", ""},
		{"owner hard-disabled", setUser("alice@acme.com", func(u *userstore.User) { u.DeletedAt = time.Now().UTC() }), "USER_INVALIDATED", "USER_INVALIDATED"},
		{"tenant suspended", setTenant(func(tn *tenantstore.Tenant) { tn.Suspended = true }), "TENANT_SUSPENDED", "TENANT_SUSPENDED"},
		{"tenant disabling", setTenant(func(tn *tenantstore.Tenant) { tn.State = tenantstore.TenantStateDisabling }), "TENANT_NOT_ACTIVE", "TENANT_NOT_ACTIVE"},
	}
}

// clearMarkers resets every marker liveMarkers sets.
func (g *liveGateway) clearMarkers(ctx context.Context) {
	g.t.Helper()
	if err := setUser("alice@acme.com", func(u *userstore.User) {
		u.Disabled, u.DeletedAt, u.ProcessingRestricted, u.ErasureJobID = false, time.Time{}, false, ""
	})(ctx, g); err != nil {
		g.t.Fatalf("reset alice: %v", err)
	}
	if err := setTenant(func(tn *tenantstore.Tenant) {
		tn.Suspended, tn.State = false, tenantstore.TenantStateActive
	})(ctx, g); err != nil {
		g.t.Fatalf("reset tenant: %v", err)
	}
}

// startRunning creates and starts a session of alice and returns its ID.
func (g *liveGateway) startRunning(runtime string) string {
	g.t.Helper()
	code, body := g.rest("alice@acme.com", pkgauth.RoleUser, http.MethodPost, "/v1/sessions/start",
		map[string]any{"runtimeRef": runtime, "userId": "alice@acme.com"})
	if code != http.StatusCreated {
		g.t.Fatalf("start session: %d %v", code, body)
	}
	id, _ := body["id"].(string)
	return id
}

// deriveWriteTables are the tables a derive writes under the derived
// session ID: the reservation, the chunked-checkpoint manifest row, the
// artifact metadata of the snapshot copy, and the session row.
var deriveWriteTables = []string{"session_reservations", "checkpoint_manifest", "artifact_store", "sessions"}

// installDeriveWriteProbe records every row any later committed statement
// inserts into deriveWriteTables for acme, with the table and the session
// ID. A derive that reserved its ID or copied under it and then undid the
// write leaves nothing in the tables themselves, so the probe is the only
// witness that the write happened. The probe table carries no tenant_id
// column, so no tenant guard applies to the trigger's insert.
//
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18))
func (g *liveGateway) installDeriveWriteProbe(ctx context.Context) {
	g.t.Helper()
	stmts := []string{
		`CREATE TABLE test_derive_write_probe (written_tenant TEXT, table_name TEXT, session_id TEXT)`,
		`CREATE FUNCTION test_record_derive_write() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER AS $$
		 BEGIN
		   INSERT INTO test_derive_write_probe VALUES
		     (NEW.tenant_id, TG_TABLE_NAME, COALESCE(to_jsonb(NEW)->>'session_id', to_jsonb(NEW)->>'id'));
		   RETURN NEW;
		 END $$`,
	}
	for _, table := range deriveWriteTables {
		stmts = append(stmts, `CREATE TRIGGER test_derive_write_probe AFTER INSERT ON `+table+
			` FOR EACH ROW EXECUTE FUNCTION test_record_derive_write()`)
	}
	for _, stmt := range stmts {
		if _, err := g.pool.Exec(ctx, stmt); err != nil {
			g.t.Fatalf("install derive write probe: %v", err)
		}
	}
	g.checkProbeSeesUndoneWrite(ctx)
}

// checkProbeSeesUndoneWrite is the probe's control: it reserves a session
// ID and releases it again, as a derive that reserved and then undid would,
// and fails unless the probe recorded the reservation. It then empties the
// probe, so the derive's assertion starts from nothing.
func (g *liveGateway) checkProbeSeesUndoneWrite(ctx context.Context) {
	g.t.Helper()
	tx, err := g.pool.Begin(ctx)
	if err != nil {
		g.t.Fatalf("probe control: begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, stmt := range []string{
		`SELECT set_config('app.current_tenant', '` + liveTenant + `', true)`,
		`INSERT INTO session_reservations (tenant_id, session_id, user_id) VALUES ('` + liveTenant + `', '00000000-0000-4000-8000-000000000001', 'bob@acme.com')`,
		`DELETE FROM session_reservations WHERE session_id = '00000000-0000-4000-8000-000000000001'`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			g.t.Fatalf("probe control: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		g.t.Fatalf("probe control: commit: %v", err)
	}
	if got := g.deriveWrites(ctx, ""); len(got) != 1 {
		g.t.Fatalf("probe control: recorded %v, want the one undone reservation", got)
	}
	if _, err := g.pool.Exec(ctx, `DELETE FROM test_derive_write_probe`); err != nil {
		g.t.Fatalf("probe control: empty the probe: %v", err)
	}
}

// deriveWrites returns the probe's acme rows other than the parent's own,
// as "table/session" strings. The running parent may checkpoint, so its
// own rows are not a derive's.
func (g *liveGateway) deriveWrites(ctx context.Context, parent string) []string {
	g.t.Helper()
	rows, err := g.pool.Query(ctx, `SELECT table_name, session_id FROM test_derive_write_probe
		WHERE written_tenant = $1 AND session_id IS DISTINCT FROM $2`, liveTenant, parent)
	if err != nil {
		g.t.Fatalf("read derive write probe: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var table, id string
		if err := rows.Scan(&table, &id); err != nil {
			g.t.Fatalf("scan derive write probe: %v", err)
		}
		out = append(out, table+"/"+id)
	}
	if err := rows.Err(); err != nil {
		g.t.Fatalf("read derive write probe: %v", err)
	}
	return out
}

// TestCreationFenceAndResolutionOnLiveGateway_spec_12_8 drives the real
// gateway with signed bearer tokens.
//
// spec: 12.8 (Processing restriction during erasure (GDPR Article 18)), 12.8 (Compliance Interfaces: Lifecycle fence and drain), 15.1 (REST API), 9.2 (respond_to_elicitation authorization)
// diagnosis: a failure names the surface and the marker. A create admitted
// under a marker, or refused with another code, means that surface's gate
// or the fence's refusal mapping diverged from the §12.8 refusal rows; a
// derive that reserved, copied, or inserted anything under a new session ID,
// even a write it later undid, means the source-owner check runs after the
// reservation or the copy; bob resolving or seeing alice's elicitation
// means the MCP resolution tools key the triple on something other than
// the authenticated caller; a DELETE /v1/responses/{id} other than 405
// means the removed route is back.
func TestCreationFenceAndResolutionOnLiveGateway_spec_12_8(t *testing.T) {
	g := startLiveGateway(t)
	ctx := context.Background()
	parent := g.startRunning("echo")

	// The bearer is what authenticates every call below: a token signed
	// under a key the gateway does not trust is refused.
	t.Run("untrusted bearer is refused", func(t *testing.T) {
		trusted := g.signer
		g.signer = jwt.NewHMACSigner("tier9-untrusted", []byte("tier9-untrusted-hmac-secret-0123456789abcdef"))
		defer func() { g.signer = trusted }()
		g.t = t
		if code, _ := g.rest("alice@acme.com", pkgauth.RoleUser, http.MethodGet, "/v1/sessions/"+parent, nil); code != http.StatusUnauthorized {
			t.Errorf("GET with an untrusted bearer = %d, want 401", code)
		}
	})
	g.t = t

	for _, m := range liveMarkers() {
		t.Run(m.name, func(t *testing.T) {
			g.t = t
			if err := m.set(ctx, g); err != nil {
				t.Fatalf("set marker: %v", err)
			}
			defer g.clearMarkers(ctx)
			createBody := map[string]any{"runtimeRef": "echo", "userId": "alice@acme.com"}
			for _, path := range []string{"/v1/sessions", "/v1/sessions/start"} {
				code, body := g.rest("alice@acme.com", pkgauth.RoleUser, http.MethodPost, path, createBody)
				if code != http.StatusForbidden || errorCode(body) != m.want {
					t.Errorf("POST %s = %d %s, want 403 %s", path, code, errorCode(body), m.want)
				}
			}
			if isErr, code, text := g.tool("alice@acme.com", pkgauth.RoleUser, "lenny/create_session", createBody); !isErr || code != m.want {
				t.Errorf("MCP create_session = isError %v code %q (%s), want %s", isErr, code, text, m.want)
			}
			isErr, code, text := g.tool("alice@acme.com", pkgauth.RoleUser, "lenny/delegate_task",
				map[string]any{"parentSessionId": parent, "target": "echo-child"})
			switch {
			case m.delegateWant == "" && isErr:
				t.Errorf("delegate_task for a soft-disabled owner = %s (%s), want admitted", code, text)
			case m.delegateWant != "" && (!isErr || code != m.delegateWant):
				t.Errorf("delegate_task = isError %v code %q (%s), want %s", isErr, code, text, m.delegateWant)
			}
		})
	}
	g.t = t

	t.Run("derive of a restricted owner's session", func(t *testing.T) {
		g.t = t
		defer g.clearMarkers(ctx)
		g.installDeriveWriteProbe(ctx)
		before := g.pgExec(`SELECT count(*) FROM sessions WHERE tenant_id = $1`, liveTenant)
		if err := restrictAlice(ctx, g); err != nil {
			t.Fatal(err)
		}
		code, body := g.rest("carol@acme.com", pkgauth.RoleTenantAdmin, http.MethodPost,
			"/v1/sessions/"+parent+"/derive", map[string]any{"userId": "bob@acme.com"})
		if code != http.StatusForbidden || errorCode(body) != "ERASURE_IN_PROGRESS" {
			t.Errorf("derive = %d %s, want 403 ERASURE_IN_PROGRESS", code, errorCode(body))
		}
		if after := g.pgExec(`SELECT count(*) FROM sessions WHERE tenant_id = $1`, liveTenant); after != before {
			t.Errorf("the refused derive changed the session rows from %d to %d", before, after)
		}
		if n := g.pgExec(`SELECT count(*) FROM session_reservations WHERE tenant_id = $1`, liveTenant); n != 0 {
			t.Errorf("the refused derive left %d reservations", n)
		}
		if writes := g.deriveWrites(ctx, parent); len(writes) != 0 {
			t.Errorf("the refused derive wrote under a new session ID before its refusal, even if undone: %v", writes)
		}
	})

	t.Run("elicitation resolution keyed on the caller", func(t *testing.T) {
		g.t = t
		const elicitationID = "elic-tier9-erasure"
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _, _ = g.tool("alice@acme.com", pkgauth.RoleUser, "lenny/request_elicitation",
				map[string]any{"sessionId": parent, "message": "approve?", "schema": map[string]any{}, "elicitationId": elicitationID})
		}()
		pending := func() int {
			return g.pgExec(`SELECT count(*) FROM interactions WHERE id = $1 AND phase = 'pending'`, elicitationID)
		}
		deadline := time.Now().Add(8 * time.Second)
		for pending() == 0 && time.Now().Before(deadline) {
			time.Sleep(25 * time.Millisecond)
		}
		if pending() != 1 {
			t.Fatal("alice's elicitation was never recorded as pending")
		}
		args := map[string]any{"sessionId": parent, "elicitationId": elicitationID, "response": "approved"}
		for _, tool := range []string{"lenny/respond_to_elicitation", "lenny/dismiss_elicitation"} {
			if isErr, code, text := g.tool("bob@acme.com", pkgauth.RoleUser, tool, args); !isErr || code != "ELICITATION_NOT_FOUND" {
				t.Errorf("bob %s on alice's elicitation = isError %v code %q (%s), want ELICITATION_NOT_FOUND", tool, isErr, code, text)
			}
		}
		if pending() != 1 {
			t.Fatal("bob's calls resolved alice's elicitation")
		}
		if isErr, code, text := g.tool("alice@acme.com", pkgauth.RoleUser, "lenny/respond_to_elicitation", args); isErr {
			t.Fatalf("alice respond to her own elicitation = %s (%s)", code, text)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("request_elicitation did not return after alice resolved it")
		}
	})

	t.Run("DELETE /v1/responses/{id} is not routed", func(t *testing.T) {
		g.t = t
		code, _ := g.rest("alice@acme.com", pkgauth.RoleUser, http.MethodDelete, "/v1/responses/resp_tier9", nil)
		if code != http.StatusMethodNotAllowed {
			t.Errorf("DELETE /v1/responses/{id} = %d, want 405", code)
		}
	})
}
