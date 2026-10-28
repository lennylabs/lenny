//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test: §12.8 tenant deletion against two replicas of the
// real multi-tenant cmd/lenny-gateway binary over a migrated Postgres and a
// Redis. It seeds a tenant whose sessions hold a delegated tree with
// archived child results, checkpoints, and eval results, plus live
// sessions, sticky, billing-stream, delegation-budget, and session queue
// keys, and a legal hold, deletes the tenant through the admin API, and
// asserts that Phase 2 drains every live session before Phase 4 deletes it,
// that the tenant's bearers are refused on every replica from Phase 3 and
// through the Phase 3.5 pause, and that once the hold clears the deletion
// completes and leaves none of the tenant's rows, keys, or non-gdpr audit
// rows behind while a second tenant's rows, keys, and live sessions
// survive. Every run logs how long the deletion took to get through its
// Phase 2 drain.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointretention"
	checkpointretentionpg "github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointretention/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	partialmanifestpg "github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore"
	evalstorepg "github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentsticky"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	treearchivepg "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioncheckpointmeta"
	sessioncheckpointmetapg "github.com/lennylabs/lenny/pkg/gateway/session/sessioncheckpointmeta/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionstorepg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
	"github.com/lennylabs/lenny/pkg/gateway/storage/rediskeys"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/stackkey"
	tokensvc "github.com/lennylabs/lenny/tests/testinfra/tokenservice"
)

// phase4Tables are the session-keyed tables the seed populates. Phase 4
// must leave none of the deleted tenant's rows in any of them.
var phase4Tables = []string{
	"sessions",
	"session_tree_archive",
	"eval_results",
	"checkpoint_manifest",
	"session_checkpoints",
	"session_checkpoint_meta",
}

// seededTenant records the identifiers seeded for one tenant.
type seededTenant struct {
	root, child, grandchild string
}

// seedTenantTree writes a three-level delegation tree for tenant: a root
// session, a delegated child, and a delegated grandchild, with the
// settled child and grandchild archived under the root, a checkpoint
// manifest, a retained checkpoint, checkpoint metadata, and an eval
// result per session.
func seedTenantTree(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant string) seededTenant {
	t.Helper()
	ids := seededTenant{root: uuid.NewString(), child: uuid.NewString(), grandchild: uuid.NewString()}
	now := time.Now().UTC()
	sessions := sessionstorepg.New(pool)
	for _, s := range []sessionstore.Session{
		{ID: ids.root, TenantID: tenant, UserID: "alice@" + tenant + ".com", State: session.StateRunning, CreatedAt: now, UpdatedAt: now},
		{ID: ids.child, TenantID: tenant, UserID: "alice@" + tenant + ".com", ParentSessionID: ids.root, RootSessionID: ids.root, State: session.StateCompleted, CreatedAt: now, UpdatedAt: now},
		{ID: ids.grandchild, TenantID: tenant, UserID: "alice@" + tenant + ".com", ParentSessionID: ids.child, RootSessionID: ids.root, State: session.StateFailed, FailureReason: string(session.FailureDeadlockTimeout), CreatedAt: now, UpdatedAt: now},
	} {
		if err := sessions.Create(ctx, s); err != nil {
			t.Fatalf("seed session %s/%s: %v", tenant, s.ID, err)
		}
	}

	archive := treearchivepg.New(pool, nil)
	for _, n := range []treearchive.ArchivedNode{
		{TenantID: tenant, RootSessionID: ids.root, NodeSessionID: ids.child, ParentSessionID: ids.root, State: "completed", Result: `{"taskId":"` + ids.child + `"}`, SettledAt: now},
		{TenantID: tenant, RootSessionID: ids.root, NodeSessionID: ids.grandchild, ParentSessionID: ids.child, State: "failed", Result: `{"taskId":"` + ids.grandchild + `"}`, SettledAt: now},
	} {
		if err := archive.Archive(ctx, n); err != nil {
			t.Fatalf("seed archive %s/%s: %v", tenant, n.NodeSessionID, err)
		}
	}

	manifests := partialmanifestpg.New(pool, nil)
	retention := checkpointretentionpg.New(pool, nil)
	meta := sessioncheckpointmetapg.New(pool, nil)
	evals := evalstorepg.New(pool)
	for _, sid := range []string{ids.root, ids.child} {
		cp := uuid.NewString()
		if err := manifests.Put(ctx, partialmanifeststore.Record{
			TenantID: tenant, CheckpointID: cp, SessionID: sid,
			ChunkObjectKeyPrefix: fmt.Sprintf("/%s/checkpoints/%s/%s/", tenant, sid, cp),
			CheckpointStartedAt:  now, CheckpointTimeoutAt: now.Add(time.Minute),
		}); err != nil {
			t.Fatalf("seed checkpoint manifest %s/%s: %v", tenant, sid, err)
		}
		if err := retention.Insert(ctx, checkpointretention.Record{
			TenantID: tenant, SessionID: sid, Ref: "checkpoints/" + cp, Retained: true,
		}); err != nil {
			t.Fatalf("seed retained checkpoint %s/%s: %v", tenant, sid, err)
		}
		if err := meta.Upsert(ctx, sessioncheckpointmeta.Record{
			TenantID: tenant, SessionID: sid, BarrierID: "b-1", CheckpointRef: "checkpoints/" + cp,
		}); err != nil {
			t.Fatalf("seed checkpoint meta %s/%s: %v", tenant, sid, err)
		}
		score := 0.5
		if _, err := evals.Put(ctx, evalstore.EvalResult{
			TenantID: tenant, SessionID: sid, Scorer: "llm-judge", Score: &score,
		}); err != nil {
			t.Fatalf("seed eval result %s/%s: %v", tenant, sid, err)
		}
	}
	return ids
}

// tenantRowCounts returns the row count of each phase4Tables table for
// tenant.
func tenantRowCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant string) map[string]int {
	t.Helper()
	out := make(map[string]int, len(phase4Tables))
	for _, table := range phase4Tables {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
			t.Fatalf("count %s for %s: %v", table, tenant, err)
		}
		out[table] = n
	}
	return out
}

// leftoverTenantTables reports every table with a tenant_id column that
// still holds rows for tenant, excluding the tombstoned tenants row and
// the audit_log, whose gdpr.* receipts §12.8 retains.
func leftoverTenantTables(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant string) map[string]int {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT c.table_name
		  FROM information_schema.columns c
		  JOIN information_schema.tables tb
		    ON tb.table_schema = c.table_schema AND tb.table_name = c.table_name
		 WHERE c.table_schema = 'public' AND c.column_name = 'tenant_id'
		   AND tb.table_type = 'BASE TABLE'`)
	if err != nil {
		t.Fatalf("list tenant tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	out := map[string]int{}
	for _, table := range tables {
		if table == "tenants" || table == "audit_log" {
			continue
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM "`+table+`" WHERE tenant_id::text = $1`, tenant).Scan(&n); err != nil {
			continue // a partitioned parent or a view-like table; the per-table assertions cover the seed
		}
		if n > 0 {
			out[table] = n
		}
	}
	return out
}

// withoutRetainedIssuedTokens removes from left the tenant's issued_tokens
// rows that §13.3 keeps through tenant deletion: a row whose exp has not
// yet passed the trailing hour stays so that a replica that restarts still
// loads the token's revocation. Every Token Service bearer the probes mint
// is such a row. The rows past the trailing hour stay counted, because
// Phase 4 deletes them.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation",
// §12.8 (Compliance Interfaces) "Tenant deletion lifecycle".
func withoutRetainedIssuedTokens(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant string, left map[string]int) map[string]int {
	t.Helper()
	if left["issued_tokens"] == 0 {
		return left
	}
	var retained int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM issued_tokens WHERE tenant_id = $1 AND exp > now() - interval '1 hour'`, tenant).Scan(&retained); err != nil {
		t.Fatalf("count the retained issued_tokens rows of %s: %v", tenant, err)
	}
	out := make(map[string]int, len(left))
	for table, n := range left {
		if table == "issued_tokens" {
			n -= retained
		}
		if n > 0 {
			out[table] = n
		}
	}
	return out
}

// tenantDeletionLogLines returns the gateway log lines that mention the
// tenant-deletion lifecycle, capped so a failure message stays readable.
func tenantDeletionLogLines(logs string) string {
	var out []string
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "tenant-deletion") || strings.Contains(line, "tenant erase") {
			out = append(out, line)
		}
	}
	if len(out) > 40 {
		out = out[len(out)-40:]
	}
	return strings.Join(out, "\n")
}

// liveSessionsPerTenant is how many running sessions, beyond the seeded
// tree's running root, each tenant holds when the deletion starts.
const liveSessionsPerTenant = 3

// seedLiveSessions writes n running sessions for tenant, owned by bob, and
// returns their IDs.
func seedLiveSessions(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant string, n int) []string {
	t.Helper()
	sessions := sessionstorepg.New(pool)
	now := time.Now().UTC()
	ids := make([]string, n)
	for i := range ids {
		ids[i] = uuid.NewString()
		if err := sessions.Create(ctx, sessionstore.Session{
			ID: ids[i], TenantID: tenant, UserID: "bob@" + tenant + ".com", RuntimeRef: "echo",
			State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed live session %s/%s: %v", tenant, ids[i], err)
		}
	}
	return ids
}

// installSessionDeletionProbe records, for every sessions row any later
// statement deletes, the row's tenant, ID, state, and failure reason at
// the moment of deletion. Phase 4 deletes the rows, so the probe is the
// only witness of the state Phase 2 left them in. The probe table carries
// no tenant_id column, so the leftover-table report ignores it.
func installSessionDeletionProbe(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, stmt := range []string{
		`CREATE TABLE test_deleted_session_probe (deleted_tenant TEXT, session_id UUID, state TEXT, failure_reason TEXT)`,
		`CREATE FUNCTION test_record_deleted_session() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER AS $$
		 BEGIN
		   INSERT INTO test_deleted_session_probe VALUES (OLD.tenant_id, OLD.id, OLD.state, OLD.failure_reason);
		   RETURN OLD;
		 END $$`,
		`CREATE TRIGGER test_deleted_session_probe BEFORE DELETE ON sessions
		 FOR EACH ROW EXECUTE FUNCTION test_record_deleted_session()`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("install session deletion probe: %v", err)
		}
	}
}

// deletedSessionStates returns, per deleted session of tenant, its state
// and failure reason at deletion.
func deletedSessionStates(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant string) map[string][2]string {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT session_id::text, state, failure_reason FROM test_deleted_session_probe WHERE deleted_tenant = $1`, tenant)
	if err != nil {
		t.Fatalf("read session deletion probe: %v", err)
	}
	defer rows.Close()
	out := map[string][2]string{}
	for rows.Next() {
		var id, state, reason string
		if err := rows.Scan(&id, &state, &reason); err != nil {
			t.Fatalf("scan session deletion probe: %v", err)
		}
		out[id] = [2]string{state, reason}
	}
	return out
}

// assertDrainedBeforeDeletion asserts that every acme session was terminal
// when Phase 4 deleted it, and that each session live at the deletion's
// start carries the forced transition of the Phase 2 drain.
func assertDrainedBeforeDeletion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, live []string) {
	t.Helper()
	deleted := deletedSessionStates(t, ctx, pool, "acme")
	for id, st := range deleted {
		if !session.IsTerminal(session.State(st[0])) {
			t.Errorf("Phase 4 deleted acme session %s while it was %s; Phase 2 did not drain it", id, st[0])
		}
	}
	for _, id := range live {
		st, ok := deleted[id]
		switch {
		case !ok:
			t.Errorf("live acme session %s was never deleted", id)
		case st[0] != string(session.StateFailed) || st[1] != "FORCE_TERMINATED":
			t.Errorf("live acme session %s was deleted as %s/%q, want the drain's failed/FORCE_TERMINATED", id, st[0], st[1])
		}
	}
}

// drainPollInterval is how often awaitSessionsTerminal reads the session
// rows. It is well below the gateway's tenant-deletion interval, so the
// measured drain time overstates the drain by at most one poll.
const drainPollInterval = 25 * time.Millisecond

// awaitSessionsTerminal polls the primary until every session in ids is
// terminal or deleted and returns the time from start to the first poll
// that saw it. Every one of ids is live before the deletion, so the
// returned duration is the time the deletion took to get through its Phase
// 2 drain. The test logs it on every run, because a long Phase 2 drain
// delays the tenant-deletion tick and the run is where that delay shows.
func awaitSessionsTerminal(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant string, ids []string, start time.Time, timeout time.Duration) time.Duration {
	t.Helper()
	deadline := start.Add(timeout)
	for {
		live := liveSessionCount(t, ctx, pool, tenant, ids)
		if live == 0 {
			return time.Since(start)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d %s sessions still live %s after the DELETE request; Phase 2 never drained them", live, tenant, timeout)
		}
		time.Sleep(drainPollInterval)
	}
}

// liveSessionCount returns how many of ids are still non-terminal rows of
// tenant. A deleted row counts as drained.
func liveSessionCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant string, ids []string) int {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT state FROM sessions WHERE tenant_id = $1 AND id::text = ANY($2)`, tenant, ids)
	if err != nil {
		t.Fatalf("read %s session states: %v", tenant, err)
	}
	defer rows.Close()
	live := 0
	for rows.Next() {
		var state string
		if err := rows.Scan(&state); err != nil {
			t.Fatalf("scan %s session state: %v", tenant, err)
		}
		if !session.IsTerminal(session.State(state)) {
			live++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read %s session states: %v", tenant, err)
	}
	return live
}

// assertGlobexLive asserts the other tenant's live sessions are untouched.
func assertGlobexLive(t *testing.T, ctx context.Context, pool *pgxpool.Pool, live []string) {
	t.Helper()
	sessions := sessionstorepg.New(pool)
	for _, id := range live {
		row, err := sessions.Get(ctx, "globex", id)
		if err != nil {
			t.Errorf("globex live session %s: %v", id, err)
			continue
		}
		if row.State != session.StateRunning {
			t.Errorf("globex live session %s is %s after deleting acme; the drain crossed tenants", id, row.State)
		}
	}
}

// deletionGatewayArgs are the flags of each gateway replica in the tenant
// deletion test. The T-10.2.11 startup gate requires --tenancy-mode=multi and
// --multi-tenant=true together. The trusted HMAC key lets the test sign an
// embedded-OIDC bearer every replica verifies. The dev-mode playground is
// bound to acme, because multi-tenant dev mode requires an explicit
// non-default dev tenant. Every replica proxies POST /v1/oauth/token to the
// Token Service at tsURL.
func deletionGatewayArgs(pgDSN, redisAddr, keyFile, tsURL string) []string {
	return []string{
		"--no-environment-policy", "allow-all", "--dev-mode",
		"--postgres-dsn=" + pgDSN,
		// The DDL login provisions each tenant's audit sequence at
		// bootstrap; the Phase 6 erasure receipt appends to it.
		"--postgres-billing-audit-ddl-dsn=" + pgDSN,
		"--redis-url=redis://" + redisAddr + "/0", "--messaging-durable-inbox",
		"--tenancy-mode=multi", "--multi-tenant=true",
		"--bearer-trust-hmac-key-file=" + keyFile,
		"--tenant-deletion-interval=300ms",
		"--playground-enabled", "--playground-auth-mode=dev",
		"--playground-dev-tenant-id=acme",
		"--token-service-http-url=" + tsURL,
	}
}

// deletionAdminDo issues an admin request as a platform-admin on the
// built-in default tenant. A multi-tenant gateway's registry refuses acme
// before the bootstrap seed creates it and again from Phase 3, so the admin
// calls name default.
func deletionAdminDo(t *testing.T, base, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "default")
	req.Header.Set("X-Lenny-User-ID", "carol@acme.com")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// createTenantUser creates a user row in tenant through the admin API, so
// the impersonation endpoint can resolve it as a target.
func createTenantUser(t *testing.T, base, tenant, subject string) {
	t.Helper()
	code, raw := deletionAdminDo(t, base, http.MethodPost, "/v1/admin/users", map[string]any{
		"tenantId": tenant, "subject": subject, "roles": []string{"user"},
	})
	if code != http.StatusCreated {
		t.Fatalf("create %s user %s: status %d: %s", tenant, subject, code, raw)
	}
}

// mintImpersonationBearer mints a Lenny-issued user bearer for subject in
// tenant through base's platform-admin impersonation endpoint. The replica
// signs it with the shared §10.2 platform:jwt-signing key, so every replica
// of the install verifies it.
func mintImpersonationBearer(t *testing.T, base, tenant, subject string) string {
	t.Helper()
	code, raw := deletionAdminDo(t, base, http.MethodPost, "/v1/admin/impersonation", map[string]any{
		"targetTenantId": tenant, "targetUserId": subject,
		"reason": "tenant deletion bearer probe", "ticketId": "tenant-deletion-bearer-probe", "durationSeconds": 600,
	})
	if code != http.StatusOK {
		t.Fatalf("impersonate %s/%s on %s: status %d: %s", tenant, subject, base, code, raw)
	}
	var out struct {
		BearerToken string `json:"bearerToken"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.BearerToken == "" {
		t.Fatalf("impersonate %s/%s on %s: no bearer in %s (%v)", tenant, subject, base, raw, err)
	}
	return out.BearerToken
}

// lennyIssuedProbes mints, through each replica, an impersonation bearer
// and a Token Service bearer from POST /v1/oauth/token, and returns a probe
// per bearer over every replica: each Lenny-issued bearer verifies on every
// replica of the install, whichever process signed it. The exchange's
// subject_token is a user bearer for the same tenant and subject signed
// with the stack's platform:jwt-signing key, because step (1a) refuses an
// impersonation bearer as a subject_token; the impersonation bearer stays
// the caller.
//
// spec: §10.2 (Authentication) "Signing topology", §4.3 (Token Service), §13.3 (Credential Flow) "Platform-admin impersonation".
func lennyIssuedProbes(t *testing.T, tenant, subject string, replicas []string) []bearerProbe {
	t.Helper()
	_, signer := stackkey.Signer(t, stackkey.MasterKeyFile(t), nil)
	impersonation := make([]string, len(replicas))
	out := make([]bearerProbe, 0, 2*len(replicas))
	for i, base := range replicas {
		impersonation[i] = mintImpersonationBearer(t, base, tenant, subject)
		out = append(out, bearerProbe{
			name:     fmt.Sprintf("%s impersonation (minted on replica %d)", tenant, i),
			bearer:   impersonation[i],
			replicas: replicas,
		})
	}
	for i, base := range replicas {
		// The subject is a distinct bearer, so the exchange is not a
		// self-rotation and revokes nothing.
		subjectToken := stackkey.UserBearer(t, signer, tenant, subject, nil, auth.RoleUser)
		out = append(out, bearerProbe{
			name:     fmt.Sprintf("%s Token Service (requested through replica %d)", tenant, i),
			bearer:   mintExchanged(t, base, impersonation[i], subjectToken, ""),
			replicas: replicas,
		})
	}
	return out
}

// playgroundProbes mints one dev-mode playground bearer for the gateway's
// dev tenant on each replica through POST /v1/playground/token and returns
// a probe per bearer over every replica. The mint runs behind the
// dev-tenant Ready gate, so it needs the tenant seeded.
//
// spec: §27.3.1 (OIDC cookie-to-MCP-bearer exchange), §27.3 (Authentication).
func playgroundProbes(t *testing.T, tenant string, replicas []string) []bearerProbe {
	t.Helper()
	out := make([]bearerProbe, 0, len(replicas))
	for i, base := range replicas {
		out = append(out, bearerProbe{
			name:     fmt.Sprintf("%s playground (minted on replica %d)", tenant, i),
			bearer:   mintPlaygroundBearer(t, base),
			replicas: replicas,
		})
	}
	return out
}

// mintPlaygroundBearer mints a dev-mode playground bearer on base.
func mintPlaygroundBearer(t *testing.T, base string) string {
	t.Helper()
	resp, err := http.Post(base+"/v1/playground/token", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST /v1/playground/token on %s: %v", base, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/playground/token on %s: status %d: %s", base, resp.StatusCode, raw)
	}
	var out struct {
		BearerToken string `json:"bearerToken"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.BearerToken == "" {
		t.Fatalf("POST /v1/playground/token on %s: no bearer in %s (%v)", base, raw, err)
	}
	return out.BearerToken
}

// bearerProbe is one bearer and the replicas that verify its signature.
type bearerProbe struct {
	name     string
	bearer   string
	replicas []string
}

// requireBearersRefused asserts every probe's bearer is refused with 403
// TENANT_NOT_FOUND on every replica that verifies it.
func requireBearersRefused(t *testing.T, when string, probes []bearerProbe) {
	t.Helper()
	for _, p := range probes {
		for _, base := range p.replicas {
			code, body := tenancyCall(t, http.MethodGet, base+"/v1/admin/me", p.bearer, nil, nil)
			if code != http.StatusForbidden || tenancyErrorCode(body) != "TENANT_NOT_FOUND" {
				t.Errorf("%s: the %s bearer on %s answered %d %q, want 403 TENANT_NOT_FOUND", when, p.name, base, code, tenancyErrorCode(body))
			}
		}
	}
}

// requireBearersAdmitted asserts no probe's bearer is refused as an
// unregistered or unverifiable tenant at the moment named by when.
func requireBearersAdmitted(t *testing.T, when string, probes []bearerProbe) {
	t.Helper()
	for _, p := range probes {
		for _, base := range p.replicas {
			code, body := tenancyCall(t, http.MethodGet, base+"/v1/admin/me", p.bearer, nil, nil)
			if code == http.StatusUnauthorized || tenancyErrorCode(body) == "TENANT_NOT_FOUND" {
				t.Errorf("%s: the %s bearer on %s answered %d %q", when, p.name, base, code, tenancyErrorCode(body))
			}
		}
	}
}

// requireNoGlobexRowLost asserts no tenant-scoped table holds fewer globex
// rows than it did before acme's deletion. A gateway worker may add globex
// rows while the test runs, so only a loss is a crossing.
func requireNoGlobexRowLost(t *testing.T, before, after map[string]int) {
	t.Helper()
	for table, n := range before {
		if after[table] < n {
			t.Errorf("%s globex rows = %d after deleting acme, want at least %d", table, after[table], n)
		}
	}
}

// tenantRedisKeys returns the Redis keys the test seeds for tenant, whose
// delegation tree is rooted at root: its sticky assignment, its billing
// stream, its root's delegation-budget counter, and its root's session DLQ
// and durable inbox.
func tenantRedisKeys(tenant, root string) []string {
	return []string{
		rediskeys.TenantPrefix(tenant) + "exp:exp-1:sticky:alice",
		rediskeys.TenantPrefix(tenant) + "billing:stream",
		"{" + root + "}:dlg:tree_size",
		sessioninbox.DLQKey(tenant, root),
		sessioninbox.InboxKey(tenant, root),
	}
}

// seedTenantRedis writes the keys tenantRedisKeys names.
func seedTenantRedis(t *testing.T, ctx context.Context, rd *containers.Redis, tenant, root string) {
	t.Helper()
	if err := experimentsticky.NewRedis(rd.Client).Put(ctx, tenant, "exp-1", "alice", "treatment"); err != nil {
		t.Fatalf("seed sticky key of %s: %v", tenant, err)
	}
	keys := tenantRedisKeys(tenant, root)
	if err := rd.Client.XAdd(ctx, &redis.XAddArgs{Stream: keys[1], Values: map[string]any{"seed": "1"}}).Err(); err != nil {
		t.Fatalf("seed billing stream of %s: %v", tenant, err)
	}
	if err := rd.Client.Set(ctx, keys[2], 1, 0).Err(); err != nil {
		t.Fatalf("seed delegation budget of %s: %v", tenant, err)
	}
	if err := rd.Client.ZAdd(ctx, keys[3], redis.Z{Score: float64(time.Now().Add(time.Hour).Unix()), Member: `{"id":"m-dlq"}`}).Err(); err != nil {
		t.Fatalf("seed DLQ of %s: %v", tenant, err)
	}
	if err := rd.Client.RPush(ctx, keys[4], `{"id":"m-inbox"}`).Err(); err != nil {
		t.Fatalf("seed inbox of %s: %v", tenant, err)
	}
}

// requireRedisResidue asserts that none of acme's seeded keys remain and
// that every globex twin does.
func requireRedisResidue(t *testing.T, ctx context.Context, rd *containers.Redis, acmeRoot, globexRoot string) {
	t.Helper()
	for _, k := range tenantRedisKeys("acme", acmeRoot) {
		if n := rd.Client.Exists(ctx, k).Val(); n != 0 {
			t.Errorf("acme key %s survived the deletion", k)
		}
	}
	for _, k := range tenantRedisKeys("globex", globexRoot) {
		if n := rd.Client.Exists(ctx, k).Val(); n != 1 {
			t.Errorf("globex key %s is gone after deleting acme", k)
		}
	}
}

// setLegalHold sets or clears the session-level legal hold on one acme
// session, which pauses the deletion at Phase 3.5 while it is set.
func setLegalHold(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID string, on bool) {
	t.Helper()
	err := pgtenant.InTx(ctx, pool, "acme", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE sessions SET legal_hold = $2 WHERE tenant_id = 'acme' AND id = $1`, sessionID, on)
		return err
	})
	if err != nil {
		t.Fatalf("set legal hold %v on %s: %v", on, sessionID, err)
	}
}

// tenantState reads the persisted tenant state.
func tenantState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant string) string {
	t.Helper()
	var state string
	if err := pool.QueryRow(ctx, `SELECT coalesce(state, '') FROM tenants WHERE id = $1`, tenant).Scan(&state); err != nil {
		t.Fatalf("read %s tenant state: %v", tenant, err)
	}
	return state
}

// awaitTenantState polls until the tenant reaches want.
func awaitTenantState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, gw *gateway.Process, want string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if tenantState(t, ctx, pool, "acme") == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("acme tenant state = %q after %s, want %s; remaining rows: %v\ngateway tenant-deletion log:\n%s",
		tenantState(t, ctx, pool, "acme"), within, want, tenantRowCounts(t, ctx, pool, "acme"), tenantDeletionLogLines(gw.Logs()))
}

// requirePausedAtHold asserts the deletion stays paused before Phase 4 while
// the hold is set: across several runner intervals the tenant stays
// deleting and its session rows remain.
func requirePausedAtHold(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	time.Sleep(2 * time.Second)
	if state := tenantState(t, ctx, pool, "acme"); state != "deleting" {
		t.Fatalf("acme tenant state = %q during the legal hold, want deleting", state)
	}
	if n := tenantRowCounts(t, ctx, pool, "acme")["sessions"]; n == 0 {
		t.Fatal("Phase 4 deleted acme's sessions while a legal hold was set")
	}
}

// nonGDPRAuditRows counts tenant's audit rows outside the gdpr.* events
// §12.8 retains.
func nonGDPRAuditRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND event_type NOT LIKE 'gdpr.%'`, tenant).Scan(&n); err != nil {
		t.Fatalf("count %s audit rows: %v", tenant, err)
	}
	return n
}

// diagnosis: a failure means §12.8 tenant deletion did not finish Phase 4
// for a tenant with archived delegation trees and checkpoints, or left
// its rows behind. A tenant stuck in `deleting` with sessions still
// present usually means a session-keyed eraser (session_tree_archive
// first among them, because its foreign key into sessions has no ON
// DELETE action) is missing from the gateway's tenant eraser sequence or
// runs after SessionStore. Surviving rows for the second tenant mean a
// DeleteByTenant ignores its tenant scope. A session deleted while
// non-terminal, or a live session deleted without FORCE_TERMINATED, means
// Phase 2 did not drain the tenant before Phase 4; a globex session no
// longer running means the drain crossed tenants. A bearer admitted from
// Phase 3 or during the legal-hold pause, on either replica, means the
// tenant registry still admits a deleting tenant; a Lenny-issued bearer
// (impersonation-minted or Token Service-issued) admitted there means the
// key-set verifier path skips the registry check the embedded-OIDC path
// applies; a Lenny-issued bearer refused before the deletion on a replica
// other than its signer means the replicas do not share the §10.2 key set;
// an acme Redis key left
// means a Redis-backed eraser was wired nil at the gateway's call site; a
// globex key gone means one crossed tenants; a globex bearer refused on
// either replica, or a tenant-scoped table holding fewer globex rows, means
// the registry refusal or a Phase 4 entry crossed tenants; a non-gdpr acme
// audit row after
// the deletion means a refused bearer's TENANT_NOT_FOUND row was written to
// the deleted tenant's chain rather than the platform chain.
//
// spec: §12.8 (Compliance Interfaces, tenant deletion Phase 4), §12.8
// (Compliance Interfaces: Lifecycle fence and drain), §12.8 (Tenant
// deletion lifecycle), §8.10 (Delegation Tree Recovery), §10.2
// (Authentication), §27.3.1 (OIDC cookie-to-MCP-bearer exchange).
func TestTenantDeletionPhase4ClearsArchivedTreesAndCheckpoints_spec_12_8(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ts := tokensvc.StartWith(t, "--postgres-dsn="+pg.DSN)
	args := deletionGatewayArgs(pg.DSN, rd.Addr, writeTenancyTrustKey(t), ts.BaseURL())
	gwA := gateway.StartWith(t, args...)
	gwB := gateway.StartWith(t, args...)
	ctx := context.Background()

	code, raw := deletionAdminDo(t, gwA.BaseURL(), http.MethodPost, "/v1/admin/bootstrap", map[string]any{
		"tenants": []map[string]any{
			{"id": "acme", "displayName": "Acme Corp"},
			{"id": "globex", "displayName": "Globex"},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d: %s", code, raw)
	}

	acmeTree := seedTenantTree(t, ctx, pg.Pool, "acme")
	globexTree := seedTenantTree(t, ctx, pg.Pool, "globex")
	acmeLive := append(seedLiveSessions(t, ctx, pg.Pool, "acme", liveSessionsPerTenant), acmeTree.root)
	globexLive := seedLiveSessions(t, ctx, pg.Pool, "globex", liveSessionsPerTenant)
	seedTenantRedis(t, ctx, rd, "acme", acmeTree.root)
	seedTenantRedis(t, ctx, rd, "globex", globexTree.root)
	installSessionDeletionProbe(t, ctx, pg.Pool)
	globexBefore := tenantRowCounts(t, ctx, pg.Pool, "globex")
	for table, n := range tenantRowCounts(t, ctx, pg.Pool, "acme") {
		if n == 0 {
			t.Fatalf("seed wrote no %s rows for acme; the test would not exercise Phase 4", table)
		}
	}

	// Every bearer is probed on both replicas: the embedded-OIDC bearer,
	// and each Lenny-issued bearer (impersonation, Token Service, and
	// playground), which the replicas and the Token Service sign with one
	// shared key.
	replicas := []string{gwA.BaseURL(), gwB.BaseURL()}
	createTenantUser(t, gwA.BaseURL(), "acme", "alice@acme.com")
	createTenantUser(t, gwA.BaseURL(), "globex", "bob@globex.com")
	probes := append([]bearerProbe{{"embedded OIDC", mintTenancyBearer(t, "acme"), replicas}},
		lennyIssuedProbes(t, "acme", "alice@acme.com", replicas)...)
	probes = append(probes, playgroundProbes(t, "acme", replicas)...)
	globexProbes := append([]bearerProbe{{"globex embedded OIDC", mintTenancyBearer(t, "globex"), replicas}},
		lennyIssuedProbes(t, "globex", "bob@globex.com", replicas)...)
	requireBearersAdmitted(t, "before the deletion", append(probes, globexProbes...))
	globexTablesBefore := leftoverTenantTables(t, ctx, pg.Pool, "globex")

	setLegalHold(t, ctx, pg.Pool, acmeTree.child, true)
	deleteSent := time.Now()
	if code, raw := deletionAdminDo(t, gwA.BaseURL(), http.MethodDelete, "/v1/admin/tenants/acme", nil); code != http.StatusAccepted {
		t.Fatalf("DELETE /v1/admin/tenants/acme: status %d: %s", code, raw)
	}
	t.Logf("tenant deletion Phase 2 drain: %d acme sessions terminal %.3fs after the DELETE request",
		len(acmeLive), awaitSessionsTerminal(t, ctx, pg.Pool, "acme", acmeLive, deleteSent, 90*time.Second).Seconds())

	awaitTenantState(t, ctx, pg.Pool, gwA, "deleting", 30*time.Second)
	requirePausedAtHold(t, ctx, pg.Pool)
	requireBearersRefused(t, "during the Phase 3.5 pause", probes)
	requireBearersAdmitted(t, "during acme's Phase 3.5 pause", globexProbes)

	setLegalHold(t, ctx, pg.Pool, acmeTree.child, false)
	// The runner advances one phase per tick; the tombstone (`deleted`)
	// is written only after Phase 4 and the Phase 6 receipt succeed.
	awaitTenantState(t, ctx, pg.Pool, gwA, "deleted", 90*time.Second)
	requireBearersRefused(t, "after the tombstone", probes)
	requireBearersAdmitted(t, "after acme's tombstone", globexProbes)

	for table, n := range tenantRowCounts(t, ctx, pg.Pool, "acme") {
		if n != 0 {
			t.Errorf("%s still holds %d acme rows after tenant deletion", table, n)
		}
	}
	if n := nonGDPRAuditRows(t, ctx, pg.Pool, "acme"); n != 0 {
		t.Errorf("acme's audit chain holds %d non-gdpr rows after the deletion and the refused bearers", n)
	}
	requireRedisResidue(t, ctx, rd, acmeTree.root, globexTree.root)
	assertDrainedBeforeDeletion(t, ctx, pg.Pool, acmeLive)
	assertGlobexLive(t, ctx, pg.Pool, globexLive)
	globexAfter := tenantRowCounts(t, ctx, pg.Pool, "globex")
	for table, before := range globexBefore {
		if globexAfter[table] != before {
			t.Errorf("%s globex rows = %d after deleting acme, want %d", table, globexAfter[table], before)
		}
	}
	requireNoGlobexRowLost(t, globexTablesBefore, leftoverTenantTables(t, ctx, pg.Pool, "globex"))
	if left := withoutRetainedIssuedTokens(t, ctx, pg.Pool, "acme", leftoverTenantTables(t, ctx, pg.Pool, "acme")); len(left) > 0 {
		t.Errorf("tenant-scoped tables still holding acme rows after the deletion: %v", left)
	}
}
