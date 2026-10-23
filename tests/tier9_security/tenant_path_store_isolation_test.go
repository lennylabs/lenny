// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 cross-tenant isolation of the §12.8 tenant deletion Phase 4 entries
// over the production Postgres stores. The test seeds acme and globex rows in
// every tenant-path store a Phase 4 entry erases, plus audit rows on the
// platform chain, runs acme's entries in the Store table's order through each
// store's production method (the entry's List-then-Revoke or List-then-Delete
// sequence where the entry composes one), and asserts that every globex row
// and every platform-chain row is byte-for-byte unchanged and that acme keeps
// only what the §13.3 retention rule and the tenant tombstone keep.
package tier9_security_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/common/seqname"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorcredstore"
	connectorcredpg "github.com/lennylabs/lenny/pkg/gateway/connectors/connectorcredstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	connectorpg "github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease"
	coordleasepg "github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease/pgstore"
	credentialpg "github.com/lennylabs/lenny/pkg/gateway/credentials/credentialstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	customrolepg "github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	environmentpg "github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantaccessstore"
	tenantaccesspg "github.com/lennylabs/lenny/pkg/gateway/environment/tenantaccessstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	tenantpg "github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	userpg "github.com/lennylabs/lenny/pkg/gateway/environment/userstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore"
	experimentpg "github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationbudget"
	delegationbudgetpg "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationbudget/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// platformChain is the pseudo-tenant of the platform audit chain.
const platformChain = "platform"

// tenantPathStores are the production stores behind the tenant-path Phase 4
// entries this test runs.
type tenantPathStores struct {
	mirror      *coordleasepg.Store
	audit       *auditstore.Store
	budgets     *delegationbudgetpg.Store
	connTokens  *connectorcredpg.Store
	creds       *credentialpg.Store
	issued      *issuedtokenstore.Store
	access      *tenantaccesspg.Store
	users       *userpg.Store
	roles       *customrolepg.Store
	connectors  *connectorpg.Store
	envs        *environmentpg.Store
	experiments *experimentpg.Store
	tenants     *tenantpg.Store
}

func newTenantPathStores(t *testing.T, pg *containers.Postgres) tenantPathStores {
	t.Helper()
	provider, err := kms.NewLocalRandom()
	isoMust(t, "kms provider", err)
	connTokens, err := connectorcredpg.New(pg.Pool, provider, time.Now)
	isoMust(t, "connector token store", err)
	creds, err := credentialpg.New(pg.Pool, provider)
	isoMust(t, "credential store", err)
	return tenantPathStores{
		mirror:      coordleasepg.New(pg.Pool, nil),
		audit:       auditstore.New(pg.Router(t)),
		budgets:     delegationbudgetpg.New(pg.Pool),
		connTokens:  connTokens,
		creds:       creds,
		issued:      issuedtokenstore.New(pg.Pool),
		access:      tenantaccesspg.New(pg.Pool),
		users:       userpg.New(pg.Pool),
		roles:       customrolepg.New(pg.Pool),
		connectors:  connectorpg.New(pg.Pool),
		envs:        environmentpg.New(pg.Pool),
		experiments: experimentpg.New(pg.Pool),
		tenants:     tenantpg.New(pg.Pool, tenantpg.WithKMS(provider)),
	}
}

// isoMust fails the test on err.
func isoMust(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// seedIsolationTenant inserts a tenants row with its audit sequence.
func seedIsolationTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id string) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce, display_name) VALUES ($1, $2, $3)`, id, []byte{0x01}, "Display "+id)
	isoMust(t, "seed tenant "+id, err)
	_, err = pool.Exec(ctx, "CREATE SEQUENCE IF NOT EXISTS "+seqname.AuditSequenceName(id)+" START WITH 1 INCREMENT BY 1 NO CYCLE")
	isoMust(t, "audit sequence of "+id, err)
}

// seedTenantPathRows writes one row or more for tenant into each store a
// tenant-path Phase 4 entry erases: an issued token past the §13.3 trailing
// hour and one inside it, among the rest.
func (s tenantPathStores) seedTenantPathRows(t *testing.T, ctx context.Context, tenant string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	isoMust(t, "coordination mirror", s.mirror.Upsert(ctx, coordlease.Lease{
		TenantID: tenant, SessionID: "s-" + tenant, CoordinatorReplica: "rep-1", CoordinationGeneration: 1,
	}))
	_, err := s.audit.Append(ctx, tenant, "session.created", json.RawMessage(`{"tenant":"`+tenant+`"}`), now)
	isoMust(t, "audit row", err)
	isoMust(t, "delegation budget", s.budgets.Write(ctx, []delegationbudget.Checkpoint{{
		TenantID: tenant, RootSessionID: "root-" + tenant, TreeSize: 2, TokenBudgetConsumed: 100,
	}}))
	isoMust(t, "connector token", s.connTokens.Put(ctx, connectorcredstore.ConnectorCredential{
		TenantID: tenant, ConnectorID: "github", UserID: "alice@acme.com",
		AccessToken: "gho_" + tenant, RefreshToken: "ghr_" + tenant, TokenType: "Bearer", ExpiresAt: now.Add(time.Hour),
	}))
	_, err = s.creds.Register(ctx, tenant, "alice", credential.ProviderAnthropicDirect, "", "sk-"+tenant)
	isoMust(t, "registered credential", err)
	for label, exp := range map[string]time.Duration{"live": time.Hour, "old": -2 * time.Hour} {
		isoMust(t, "issued token "+label, s.issued.Record(ctx, issuedtokenstore.IssuedToken{
			JTI: "jti-" + label + "-" + tenant, TenantID: tenant, Subject: "alice@acme.com",
			TokenHash: []byte(label + tenant), Scope: []string{"sessions:read"}, Audience: "lenny-gateway",
			IssuedAt: now.Add(-3 * time.Hour), ExpiresAt: now.Add(exp),
		}))
	}
	_, err = s.access.Grant(ctx, tenantaccessstore.KindRuntime, "runtime-shared", tenant, "carol@acme.com", now)
	isoMust(t, "runtime grant", err)
	_, err = s.access.Grant(ctx, tenantaccessstore.KindPool, "pool-shared", tenant, "carol@acme.com", now)
	isoMust(t, "pool grant", err)
	isoMust(t, "user", s.users.Create(ctx, userstore.User{Subject: "alice@" + tenant, TenantID: tenant, Email: "alice@acme.com"}))
	isoMust(t, "custom role", s.roles.Create(ctx, customrolestore.CustomRole{
		TenantID: tenant, Name: "session-manager", CreatedAt: now, UpdatedAt: now,
	}))
	isoMust(t, "connector", s.connectors.Create(ctx, connectorstore.Connector{
		TenantID: tenant, ID: "github", DisplayName: "GitHub", MCPServerURL: "https://mcp.github.com",
		Transport: "streamable_http", Visibility: "tenant",
	}))
	isoMust(t, "environment", s.envs.Create(ctx, environmentstore.Environment{
		Name: "security-team", TenantID: tenant,
		Members: []environmentstore.Member{{
			Identity: environmentstore.Identity{Type: "oidc-group", Value: "security-engineers"}, Role: environment.RoleAdmin,
		}},
	}))
	isoMust(t, "experiment", s.experiments.Create(ctx, experimentstore.Experiment{
		ID: "exp_" + tenant, TenantID: tenant, Status: experiment.StatusActive, BaseRuntime: "claude-worker",
		Variants:      []experimentstore.Variant{{ID: "treatment", Runtime: "claude-worker-v2", Pool: "cw-v2-pool", Weight: 0.1, InitialMinWarm: 2}},
		TargetingMode: experiment.TargetingPercentage, Sticky: experiment.StickyUser, Propagation: experiment.PropagationInherit,
	}))
}

// tenantPathEntry is one tenant-path Phase 4 entry over a production store.
type tenantPathEntry struct {
	name  string
	erase func(ctx context.Context, tenant string) (int, error)
}

// entries returns the tenant-path entries in the Store table's order. The
// audit entry is the store's DeleteByTenant, which the gateway skips for the
// platform chain; the test runs it for acme and asserts the platform chain
// is untouched.
func (s tenantPathStores) entries() []tenantPathEntry {
	return []tenantPathEntry{
		{"coordination_mirror", func(ctx context.Context, tenant string) (int, error) {
			return 0, s.mirror.DeleteByTenant(ctx, tenant)
		}},
		{"audit", s.audit.DeleteByTenant},
		{"delegation_budget", s.budgets.DeleteByTenant},
		{"tokens", func(ctx context.Context, tenant string) (int, error) {
			n, err := s.connTokens.DeleteByTenant(ctx, tenant)
			if err != nil {
				return n, err
			}
			m, err := s.creds.DeleteByTenant(ctx, tenant)
			return n + m, err
		}},
		{"issued_tokens", s.issued.DeleteByTenant},
		{"runtime_tenant_access", s.revokeGrants(tenantaccessstore.KindRuntime)},
		{"pool_tenant_access", s.revokeGrants(tenantaccessstore.KindPool)},
		{"user_roles", s.users.DeleteByTenant},
		{"custom_roles", s.roles.DeleteByTenant},
		{"tenant_definitions", s.eraseDefinitions},
		{"tenant_config", s.nullConfig},
	}
}

// revokeGrants is the access entry: list the tenant's grants of the kind and
// revoke each.
func (s tenantPathStores) revokeGrants(kind tenantaccessstore.ResourceKind) func(context.Context, string) (int, error) {
	return func(ctx context.Context, tenant string) (int, error) {
		resources, err := s.access.ListForTenant(ctx, kind, tenant)
		if err != nil {
			return 0, err
		}
		for i, r := range resources {
			if err := s.access.Revoke(ctx, kind, r, tenant); err != nil {
				return i, err
			}
		}
		return len(resources), nil
	}
}

// eraseDefinitions is the tenant_definitions entry: the connectors, each
// listed environment, and the experiments.
func (s tenantPathStores) eraseDefinitions(ctx context.Context, tenant string) (int, error) {
	n, err := s.connectors.DeleteByTenant(ctx, tenant)
	if err != nil {
		return n, err
	}
	envs, err := s.envs.List(ctx, tenant)
	if err != nil {
		return n, err
	}
	for _, e := range envs {
		if err := s.envs.Delete(ctx, tenant, e.Name); err != nil {
			return n, err
		}
		n++
	}
	m, err := s.experiments.DeleteByTenant(ctx, tenant)
	return n + m, err
}

// nullConfig is the tenant_config entry: the tenant row keeps its identity,
// state, tier, residency, and billing policy, and loses every other field.
func (s tenantPathStores) nullConfig(ctx context.Context, tenant string) (int, error) {
	_, err := s.tenants.Update(ctx, tenant, func(row *tenantstore.Tenant) error {
		*row = tenantstore.Tenant{
			ID: row.ID, State: row.State, DeletedAt: row.DeletedAt, WorkspaceTier: row.WorkspaceTier,
			DataResidencyRegion: row.DataResidencyRegion, BillingErasurePolicy: row.BillingErasurePolicy,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Version: row.Version,
		}
		return nil
	})
	return 1, err
}

// tenantTables returns every base table in the public schema with a
// tenant_id column.
func tenantTables(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT c.table_name FROM information_schema.columns c
		  JOIN information_schema.tables tb ON tb.table_schema = c.table_schema AND tb.table_name = c.table_name
		 WHERE c.table_schema = 'public' AND c.column_name = 'tenant_id' AND tb.table_type = 'BASE TABLE'
		 ORDER BY 1`)
	isoMust(t, "list tenant tables", err)
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	isoMust(t, "read tenant tables", err)
	return names
}

// tenantFingerprint returns, per table holding rows of the tenant, the row
// count and a digest of the rows' text, with the tenants row under
// "tenants". A rewritten row changes its digest as a deleted row changes the
// count.
func tenantFingerprint(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tables []string, tenant string) map[string]string {
	t.Helper()
	out := map[string]string{}
	read := func(name, query string) {
		var n int
		var digest string
		isoMust(t, "fingerprint "+name, pool.QueryRow(ctx, query, tenant).Scan(&n, &digest))
		if n > 0 {
			out[name] = fmt.Sprintf("%d:%s", n, digest)
		}
	}
	for _, tbl := range tables {
		read(tbl, `SELECT count(*), coalesce(md5(string_agg(r::text, '|' ORDER BY r::text)), '') FROM `+
			pgx.Identifier{tbl}.Sanitize()+` r WHERE tenant_id = $1`)
	}
	read("tenants", `SELECT count(*), coalesce(md5(string_agg(r::text, '|' ORDER BY r::text)), '') FROM tenants r WHERE id = $1`)
	return out
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle), 13.3 (Credential Flow: Authoritative durability for revocation), 11.7 (Audit Logging)
// diagnosis: a globex or platform-chain fingerprint change means a
// tenant-path Phase 4 entry's production store method is not scoped to the
// tenant it is given, so deleting acme deleted, revoked, or rewrote another
// tenant's coordination mirror rows, audit rows, delegation budget rows,
// TokenStore rows, issued tokens, access grants, users, roles, definitions,
// or tenant configuration; an acme table other than issued_tokens and the
// tenants row still holding rows means the entry missed the deleted tenant.
func TestTenantPathEntriesNeverTouchAnotherTenant_spec_12_8(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: schematest.RepoRoot(t) + "/migrations"})
	ctx := context.Background()
	for _, id := range []string{"acme", "globex"} {
		seedIsolationTenant(t, ctx, pg.Pool, id)
	}
	_, err := pg.Pool.Exec(ctx, `INSERT INTO runtime_definitions (name, type, image, execution_mode, isolation_profile, integration_level)
		VALUES ('runtime-shared', 'agent', 'example/img:latest', 'session', 'standard', 'standard')`)
	isoMust(t, "seed runtime", err)
	_, err = pg.Pool.Exec(ctx, `INSERT INTO sandbox_warm_pools (name) VALUES ('pool-shared')`)
	isoMust(t, "seed pool", err)

	s := newTenantPathStores(t, pg)
	for _, id := range []string{"acme", "globex"} {
		s.seedTenantPathRows(t, ctx, id)
	}
	_, err = s.audit.Append(ctx, platformChain, "tenant.created", json.RawMessage(`{"target_tenant_id":"acme"}`), time.Now().UTC())
	isoMust(t, "platform audit row", err)

	tables := tenantTables(t, ctx, pg.Pool)
	acmeBefore := tenantFingerprint(t, ctx, pg.Pool, tables, "acme")
	globexBefore := tenantFingerprint(t, ctx, pg.Pool, tables, "globex")
	platformBefore := tenantFingerprint(t, ctx, pg.Pool, tables, platformChain)
	if len(globexBefore) < 10 || len(platformBefore) == 0 {
		t.Fatalf("seed reached too few tables: globex %v, platform %v", globexBefore, platformBefore)
	}

	for run := 1; run <= 2; run++ {
		for _, e := range s.entries() {
			if _, err := e.erase(ctx, "acme"); err != nil {
				t.Fatalf("run %d: entry %s for acme: %v", run, e.name, err)
			}
		}
	}

	for name, want := range map[string]map[string]string{"globex": globexBefore, platformChain: platformBefore} {
		got := tenantFingerprint(t, ctx, pg.Pool, tables, name)
		for tbl, before := range want {
			if got[tbl] != before {
				t.Errorf("%s %s rows changed from %s to %s after deleting acme", name, tbl, before, got[tbl])
			}
		}
		for tbl := range got {
			if _, ok := want[tbl]; !ok {
				t.Errorf("%s gained %s rows after deleting acme", name, tbl)
			}
		}
	}
	acmeAfter := tenantFingerprint(t, ctx, pg.Pool, tables, "acme")
	var left []string
	for tbl := range acmeAfter {
		if tbl != "issued_tokens" && tbl != "tenants" {
			left = append(left, tbl)
		}
	}
	if len(left) > 0 {
		t.Errorf("acme still holds rows in %s after its entries ran (before: %v)", strings.Join(left, ", "), acmeBefore)
	}
	if !strings.HasPrefix(acmeAfter["issued_tokens"], "1:") {
		t.Errorf("acme issued_tokens = %q, want the one row inside the §13.3 trailing hour", acmeAfter["issued_tokens"])
	}
}
