//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/blobstore/cataloging"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorcredstore"
	connectorcredpg "github.com/lennylabs/lenny/pkg/gateway/connectors/connectorcredstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	connectorpg "github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore/pgstore"
	credentialpg "github.com/lennylabs/lenny/pkg/gateway/credentials/credentialstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	customrolepg "github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore/pgstore"
	environmentpg "github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	tenantpg "github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	userpg "github.com/lennylabs/lenny/pkg/gateway/environment/userstore/pgstore"
	experimentpg "github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore/pgstore"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// twinCase is one Phase 4 store method checked against an acme row and a
// globex twin: seed writes one row for a tenant, erase runs the method the
// tenant entry calls, and count reads the tenant's remaining rows.
type twinCase struct {
	name  string
	seed  func(t *testing.T, tenant string)
	erase func(tenant string) (int, error)
	count func(t *testing.T, tenant string) int
}

// runTwinCase seeds both tenants, erases acme twice, and asserts acme has
// no rows left, globex keeps its row, and the second run succeeds.
func runTwinCase(t *testing.T, acme, globex string, tc twinCase) {
	t.Helper()
	tc.seed(t, acme)
	tc.seed(t, globex)
	for run := 1; run <= 2; run++ {
		if _, err := tc.erase(acme); err != nil {
			t.Fatalf("%s run %d: %v", tc.name, run, err)
		}
	}
	if n := tc.count(t, acme); n != 0 {
		t.Errorf("%s: acme rows left = %d, want 0", tc.name, n)
	}
	if n := tc.count(t, globex); n != 1 {
		t.Errorf("%s: globex rows = %d, want 1 (the acme delete reached another tenant)", tc.name, n)
	}
}

// must fails the test on err.
func must(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle)
// diagnosis: a store method that a tenant-deletion Phase 4 entry calls did
// not delete the deleted tenant's rows, deleted another tenant's rows, or
// failed on a re-run, against real Postgres. The entries are the
// TokenStore stores (connector tokens and registered credentials), the user
// role mappings, the custom roles, the connector, environment, and
// experiment definitions, and the tenant configuration record, whose nulled
// row must satisfy the tenants table constraints and drop the sealed salt.
func TestTenantPhase4StoreMethodsAgainstPostgres_spec_12_8(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	ctx := context.Background()
	provider, err := kms.NewLocalRandom()
	must(t, "kms provider", err)
	acme, globex := freshTenant(t, ctx, pg), freshTenant(t, ctx, pg)

	tokens, err := connectorcredpg.New(pg.Pool, provider, time.Now)
	must(t, "connector token store", err)
	creds, err := credentialpg.New(pg.Pool, provider)
	must(t, "credential store", err)
	users := userpg.New(pg.Pool)
	roles := customrolepg.New(pg.Pool)
	connectors := connectorpg.New(pg.Pool)
	envs := environmentpg.New(pg.Pool)
	experiments := experimentpg.New(pg.Pool)

	cases := []twinCase{
		{
			name: "connector tokens",
			seed: func(t *testing.T, tenant string) {
				must(t, "Put", tokens.Put(ctx, connectorcredstore.ConnectorCredential{
					TenantID: tenant, ConnectorID: "github", UserID: "alice@acme.com",
					AccessToken: "gho_" + tenant, RefreshToken: "ghr_" + tenant, TokenType: "Bearer",
					ExpiresAt: time.Now().Add(time.Hour),
				}))
			},
			erase: func(tenant string) (int, error) { return tokens.DeleteByTenant(ctx, tenant) },
			count: func(t *testing.T, tenant string) int {
				rows, err := tokens.ListByConnector(ctx, tenant, "github")
				must(t, "ListByConnector", err)
				return len(rows)
			},
		},
		{
			name: "registered credentials",
			seed: func(t *testing.T, tenant string) {
				_, err := creds.Register(ctx, tenant, "alice", credential.ProviderAnthropicDirect, "", "sk-"+tenant)
				must(t, "Register", err)
			},
			erase: func(tenant string) (int, error) { return creds.DeleteByTenant(ctx, tenant) },
			count: func(t *testing.T, tenant string) int {
				rows, err := creds.List(ctx, tenant, "alice")
				must(t, "List", err)
				return len(rows)
			},
		},
		{
			name: "user role mappings",
			seed: func(t *testing.T, tenant string) {
				must(t, "Create", users.Create(ctx, userstore.User{Subject: "alice@" + tenant, TenantID: tenant, Email: "alice@acme.com"}))
			},
			erase: func(tenant string) (int, error) { return users.DeleteByTenant(ctx, tenant) },
			count: func(t *testing.T, tenant string) int {
				rows, err := users.List(ctx, tenant, userstore.ListFilter{IncludeDeleted: true})
				must(t, "List", err)
				return len(rows)
			},
		},
		{
			name: "custom roles",
			seed: func(t *testing.T, tenant string) {
				must(t, "Create", roles.Create(ctx, customrolestore.CustomRole{
					TenantID: tenant, Name: "session-manager", Permissions: nil,
					CreatedAt: time.Now(), UpdatedAt: time.Now(),
				}))
			},
			erase: func(tenant string) (int, error) { return roles.DeleteByTenant(ctx, tenant) },
			count: func(t *testing.T, tenant string) int {
				rows, err := roles.List(ctx, tenant)
				must(t, "List", err)
				return len(rows)
			},
		},
		{
			name: "connector definitions",
			seed: func(t *testing.T, tenant string) {
				must(t, "Create", connectors.Create(ctx, connectorstore.Connector{
					TenantID: tenant, ID: "github", DisplayName: "GitHub", MCPServerURL: "https://mcp.github.com",
					Transport: "streamable_http", Visibility: "tenant",
				}))
			},
			erase: func(tenant string) (int, error) { return connectors.DeleteByTenant(ctx, tenant) },
			// List scopes by row-level security, which the superuser test
			// connection bypasses, so the count filters by tenant.
			count: func(t *testing.T, tenant string) int {
				rows, err := connectors.List(ctx, tenant, connectorstore.ListFilter{IncludeDeleted: true})
				must(t, "List", err)
				n := 0
				for _, c := range rows {
					if c.TenantID == tenant {
						n++
					}
				}
				return n
			},
		},
		{
			name: "environment definitions",
			seed: func(t *testing.T, tenant string) {
				must(t, "Create", envs.Create(ctx, sampleEnvironment(tenant, "security-team")))
			},
			// The entry lists the tenant's environments and deletes each,
			// treating one already gone as deleted.
			erase: func(tenant string) (int, error) {
				rows, err := envs.List(ctx, tenant)
				if err != nil {
					return 0, err
				}
				n := 0
				for _, e := range rows {
					if err := envs.Delete(ctx, tenant, e.Name); err != nil {
						return n, err
					}
					n++
				}
				return n, nil
			},
			count: func(t *testing.T, tenant string) int {
				rows, err := envs.List(ctx, tenant)
				must(t, "List", err)
				return len(rows)
			},
		},
		{
			name: "experiment definitions",
			seed: func(t *testing.T, tenant string) {
				must(t, "Create", experiments.Create(ctx, validExperiment(tenant, "exp_"+strings.ReplaceAll(tenant, "-", "_"))))
			},
			erase: func(tenant string) (int, error) { return experiments.DeleteByTenant(ctx, tenant) },
			count: func(t *testing.T, tenant string) int {
				rows, err := experiments.List(ctx, tenant)
				must(t, "List", err)
				return len(rows)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runTwinCase(t, acme, globex, tc) })
	}

	t.Run("tenant configuration record", func(t *testing.T) {
		tenants := tenantpg.New(pg.Pool, tenantpg.WithKMS(provider))
		for _, id := range []string{"cfg-acme", "cfg-globex"} {
			must(t, "Create", tenants.Create(ctx, tenantstore.Tenant{
				ID: id, DisplayName: "Acme Corp", ComplianceProfile: "soc2", WorkspaceTier: "T3",
				BillingErasurePolicy: tenantstore.BillingErasureExempt, GCPriority: "high",
				ErasureSalt: []byte("0123456789abcdef0123456789abcdef"), CreatedAt: time.Now(),
			}))
		}
		null := func(row *tenantstore.Tenant) error {
			*row = tenantstore.Tenant{
				ID: row.ID, State: row.State, DeletedAt: row.DeletedAt, WorkspaceTier: row.WorkspaceTier,
				DataResidencyRegion: row.DataResidencyRegion, BillingErasurePolicy: row.BillingErasurePolicy,
				CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Version: row.Version,
			}
			return nil
		}
		for run := 1; run <= 2; run++ {
			if _, err := tenants.Update(ctx, "cfg-acme", null); err != nil {
				t.Fatalf("null configuration run %d: %v", run, err)
			}
		}
		var saltNull bool
		must(t, "read salt", pg.Pool.QueryRow(ctx, `SELECT erasure_salt IS NULL FROM tenants WHERE id = 'cfg-acme'`).Scan(&saltNull))
		if !saltNull {
			t.Error("the sealed erasure salt survived the configuration record erasure")
		}
		got, err := tenants.Get(ctx, "cfg-acme")
		must(t, "Get", err)
		if got.DisplayName != "" || got.ComplianceProfile != "" || len(got.ErasureSalt) != 0 {
			t.Errorf("configuration fields survived: %+v", got)
		}
		if got.BillingErasurePolicy != tenantstore.BillingErasureExempt || got.WorkspaceTier != "T3" {
			t.Errorf("kept fields lost: policy %q tier %q", got.BillingErasurePolicy, got.WorkspaceTier)
		}
		twin, err := tenants.Get(ctx, "cfg-globex")
		must(t, "Get twin", err)
		if twin.DisplayName != "Acme Corp" || len(twin.ErasureSalt) == 0 {
			t.Errorf("the twin tenant's configuration changed: %+v", twin)
		}
	})
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle), 12.5
// diagnosis: the tenant-deletion artifacts entry, run through the
// cataloging wrapper over MinIO and the Postgres catalog, left a deleted
// tenant's object or catalog row in place, or reached another tenant's.
// Phase 3.5 is the hold gate, so a legal-held catalog row of the deleted
// tenant must be deleted too; a surviving held row means the catalog still
// guards held rows from the tenant delete.
func TestTenantArtifactsEntryDeletesHeldCatalogRows_spec_12_8(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cat, pg := newArtifactCatalog(t)
	mc := containers.StartMinIO(t, containers.MinIOOptions{})
	inner, err := minioStoreFor(mc)
	must(t, "miniostore", err)
	store := cataloging.New(inner, cat, cataloging.Options{})
	for _, id := range []string{"acme", "globex"} {
		seedArtifactTenant(t, ctx, pg, id)
	}
	put := func(tenant, session string) blobstore.URI {
		u := blobstore.URI{TenantID: tenant, ObjectType: blobstore.ObjectTypeWorkspace, SessionID: session, PartID: "p1", TTL: time.Hour, Encoding: blobstore.Encoding}
		_, err := store.Put(u, "application/gzip", strings.NewReader("workspace"))
		must(t, "Put", err)
		return u
	}
	held := put("acme", "s-held")
	put("acme", "s-free")
	twin := put("globex", "s-twin")
	must(t, "SetLegalHold", cat.SetLegalHold(ctx, held.String(), true, "alice@acme.com", time.Now(), "incident-42"))

	for run := 1; run <= 2; run++ {
		if _, err := store.DeleteByTenant(ctx, "acme"); err != nil {
			t.Fatalf("DeleteByTenant run %d: %v", run, err)
		}
	}
	var acmeRows, globexRows int
	must(t, "count acme", pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM artifact_store WHERE tenant_id = 'acme'`).Scan(&acmeRows))
	must(t, "count globex", pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM artifact_store WHERE tenant_id = 'globex'`).Scan(&globexRows))
	if acmeRows != 0 {
		t.Errorf("acme catalog rows left = %d, want 0 (the held row included)", acmeRows)
	}
	if globexRows != 1 {
		t.Errorf("globex catalog rows = %d, want 1", globexRows)
	}
	if _, err := inner.Stat(held); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("acme held object survived the prefix delete: %v", err)
	}
	if _, err := inner.Stat(twin); err != nil {
		t.Errorf("globex object lost: %v", err)
	}
	if _, err := cat.Get(ctx, held.String()); !errors.Is(err, artifactcatalog.ErrNotFound) {
		t.Errorf("held catalog row survived: %v", err)
	}
}
