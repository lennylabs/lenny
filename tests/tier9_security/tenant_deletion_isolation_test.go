// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 cross-tenant isolation of §12.8 tenant deletion. Deleting one
// tenant must never reach another tenant's rows, objects, keys, leases,
// TokenStore rows, or bearers. The first test runs a tenant deletion through
// Phase 4 over the production Postgres stores with a twin tenant sharing the
// blob store, the manifests, the lease pool, and the TokenStore, and asserts
// the twin is unchanged and still registered while the deleted tenant's
// registry entry refuses its bearers. The second refuses a playground apiKey
// mint for the deleted tenant after Phase 4 and checks the refusal is audited
// on the platform chain rather than on the deleted tenant's chain. The third runs the two Redis-resident
// Phase 4 primitives, the experiment sticky-cache sweep and the billing
// stream purge, against a real Redis holding a twin tenant and a tenant whose
// ID extends the deleted tenant's ID.
package tier9_security_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/common/seqname"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore/failover/redisstream"
	tenantpg "github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentsticky"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
	"github.com/lennylabs/lenny/pkg/gateway/storage/rediskeys"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/lifecyclefence"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle), 10.2 (Authentication)
// diagnosis: a twin change means a Phase 3 step or a Phase 4 entry is not
// tenant-scoped and deleted, returned, or rewrote another tenant's data; the
// deleted tenant still registered means its bearers are admitted after
// Phase 3; the twin unregistered means the registry refuses a live tenant.
func TestTenantDeletionNeverTouchesAnotherTenant_spec_12_8(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: schematest.RepoRoot(t) + "/migrations"})
	ctx := context.Background()
	s, err := lifecyclefence.PostgresStores(ctx, pg.Pool)
	if err != nil {
		t.Fatal(err)
	}
	w := lifecyclefence.NewWorld(t, s, 2)
	tw := w.NewTwin(t, 2)
	d := w.NewDeletion()
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.Complete(ctx, 12); err != nil {
		t.Fatal(err)
	}
	registry := tenantpg.New(pg.Pool)
	for tenant, want := range map[string]bool{w.Tenant: false, tw.Tenant: true} {
		got, err := registry.IsRegistered(tenant)
		if err != nil || got != want {
			t.Errorf("IsRegistered(%s) = %v, %v; want %v", tenant, got, err, want)
		}
	}
	w.Check(t)
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle, Phase 4), 11.7 (Audit Logging), 10.2 (Authentication: `tenant_id` extraction)
// diagnosis: a 2xx means the playground minted a bearer for a tenant whose
// deletion passed Phase 4; a non-gdpr row on the deleted tenant's chain means
// the refused mint's playground.bearer_mint_rejected row was written to that
// tenant's own chain, which after the Phase 4 audit entry holds only gdpr.*
// rows; no platform row targeting the tenant means the refusal went unaudited
// or lost its top-level target_tenant_id.
func TestRefusedPlaygroundMintAfterPhase4AuditsOnPlatformChain_spec_12_8(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: schematest.RepoRoot(t) + "/migrations"})
	ctx := context.Background()
	s, err := lifecyclefence.PostgresStores(ctx, pg.Pool)
	if err != nil {
		t.Fatal(err)
	}
	w := lifecyclefence.NewWorld(t, s, 1)
	d := w.NewDeletion()
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.Complete(ctx, 12); err != nil {
		t.Fatal(err)
	}
	seedPlatformAuditChain(ctx, t, pg)

	signer := jwt.NewHMACSigner("tenant-deletion-sec", []byte("tenant-deletion-playground-secret"))
	h := playground.New(playground.Config{Enabled: true, AuthMode: playground.AuthModeAPIKey, MultiTenant: true},
		playground.Options{Signer: signer, Tenants: tenantpg.New(pg.Pool)}).
		WithAuditEmitter(playground.ChainAuditEmitter{Sink: admin.NewAuditLogSink(auditstore.New(pg.Router(t)), nil)})
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()
	bearer, err := signer.Sign(jwt.Claims{
		Subject: "alice@acme.com", TenantID: w.Tenant, Typ: auth.TokenUserBearer,
		JWTID: "deleted-tenant-paste", Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/playground/token", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("apiKey mint for %s after Phase 4 = %d, want 403 TENANT_NOT_FOUND", w.Tenant, resp.StatusCode)
	}

	var own, platform int
	if err := pg.Pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE tenant_id = $1 AND event_type NOT LIKE 'gdpr.%'),
		       count(*) FILTER (WHERE tenant_id = 'platform' AND event_type = 'playground.bearer_mint_rejected'
		                        AND payload->>'target_tenant_id' = $1)
		  FROM audit_log`, w.Tenant).Scan(&own, &platform); err != nil {
		t.Fatalf("read audit rows: %v", err)
	}
	if own != 0 {
		t.Errorf("%s's chain holds %d non-gdpr rows after the refused mint, want 0", w.Tenant, own)
	}
	if platform != 1 {
		t.Errorf("platform-chain bearer_mint_rejected rows targeting %s = %d, want 1", w.Tenant, platform)
	}
}

// seedPlatformAuditChain provisions the platform tenant's §11.7 chain
// genesis row and audit sequence, which a platform-chain append needs.
func seedPlatformAuditChain(ctx context.Context, t *testing.T, pg *containers.Postgres) {
	t.Helper()
	if _, err := pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ('platform', '\x00') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed platform tenant: %v", err)
	}
	if _, err := pg.Pool.Exec(ctx, "CREATE SEQUENCE IF NOT EXISTS "+seqname.AuditSequenceName("platform")+" START WITH 1 INCREMENT BY 1 NO CYCLE"); err != nil {
		t.Fatalf("provision platform audit sequence: %v", err)
	}
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle, Phase 4), 11.2.1 (billing write-ahead stream)
// diagnosis: a globex or acme2 key gone after deleting acme means the sticky
// sweep's glob or the stream purge's key is not bounded by the tenant
// prefix's delimiter, so deleting one tenant erases another's sticky
// assignments or staged billing events; an acme key left means the sweep or
// the purge missed the deleted tenant.
func TestTenantRedisPurgesStayInsideTheTenantPrefix_spec_12_8(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()
	tenants := []string{"acme", "acme2", "globex"}

	cache := experimentsticky.NewRedis(rd.Client)
	tier, err := redisstream.New(redisstream.Options{Client: rd.Client, ConsumerName: "replica-1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tenant := range tenants {
		for i := 0; i < 3; i++ {
			if err := cache.Put(ctx, tenant, "exp-1", fmt.Sprintf("user-%d", i), "treatment"); err != nil {
				t.Fatalf("Put(%s): %v", tenant, err)
			}
			e := billingstore.Event{TenantID: tenant, UserID: "alice", SequenceNumber: uint64(i + 1), EventType: billingstore.EventSessionCreated}
			if err := tier.Publish(ctx, e); err != nil {
				t.Fatalf("Publish(%s): %v", tenant, err)
			}
		}
	}

	if n, err := cache.DeleteByTenant(ctx, "acme"); err != nil || n != 3 {
		t.Fatalf("sticky DeleteByTenant(acme) = (%d, %v), want (3, nil)", n, err)
	}
	if n, err := tier.PurgeTenant(ctx, "acme"); err != nil || n != 3 {
		t.Fatalf("PurgeTenant(acme) = (%d, %v), want (3, nil)", n, err)
	}
	for _, tenant := range tenants {
		want := 3
		if tenant == "acme" {
			want = 0
		}
		keys, err := rd.Client.Keys(ctx, rediskeys.TenantPrefix(tenant)+"exp:*:sticky:*").Result()
		if err != nil || len(keys) != want {
			t.Errorf("%s sticky keys = %v (%v), want %d", tenant, keys, err, want)
		}
		l, err := rd.Client.XLen(ctx, rediskeys.TenantPrefix(tenant)+"billing:stream").Result()
		if err != nil && want != 0 || int(l) != want {
			t.Errorf("%s billing stream length = %d (%v), want %d", tenant, l, err, want)
		}
	}
}
