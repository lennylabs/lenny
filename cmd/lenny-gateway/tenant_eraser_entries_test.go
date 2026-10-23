// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/audit/jwtaudit"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore/failover"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// billingFixture is an in-memory billing ledger and stream wired into a
// gateway wiring as the Phase 4 billing entries read them.
type billingFixture struct {
	w      *gatewayWiring
	ledger *billingstore.Memory
	stream *failover.MemStream
}

// newBillingFixture builds the in-memory eraser wiring with a billing
// ledger and stream it can inspect, and seeds one durable and one staged
// billing event for each tenant.
func newBillingFixture(t *testing.T, tenants ...string) billingFixture {
	t.Helper()
	w := inMemoryEraserWiring()
	ledger := billingstore.NewMemory()
	stream := failover.NewMemStream()
	w.billingStream = stream
	w.billingPipeline = failover.New(failover.Options{Primary: ledger, Stream: stream})
	ctx := context.Background()
	for _, id := range tenants {
		if _, err := ledger.Append(ctx, billingstore.Event{TenantID: id, UserID: "alice", EventType: "session.completed"}); err != nil {
			t.Fatalf("seed durable billing event: %v", err)
		}
		if err := stream.Publish(ctx, billingstore.Event{TenantID: id, UserID: "alice", EventType: "session.completed"}); err != nil {
			t.Fatalf("seed staged billing event: %v", err)
		}
	}
	return billingFixture{w: w, ledger: ledger, stream: stream}
}

// durable returns the tenant's durable billing event count.
func (f billingFixture) durable(t *testing.T, tenantID string) int {
	t.Helper()
	evs, err := f.ledger.Since(context.Background(), tenantID, 0, 1000)
	if err != nil {
		t.Fatalf("billing Since: %v", err)
	}
	return len(evs)
}

// staged returns the stream's staged event count across tenants.
func (f billingFixture) staged(t *testing.T) int {
	t.Helper()
	n, err := f.stream.Pending(context.Background())
	if err != nil {
		t.Fatalf("stream Pending: %v", err)
	}
	return n
}

// entry returns the named tenant Phase 4 entry's function.
func entry(t *testing.T, erasers []namedTenantEraser, name string) tenantEraseFunc {
	t.Helper()
	for _, e := range erasers {
		if e.name == name {
			if e.fn == nil {
				t.Fatalf("entry %q has no function", name)
			}
			return e.fn
		}
	}
	t.Fatalf("no entry %q", name)
	return nil
}

// failingGetTenants is a tenant store whose Get fails.
type failingGetTenants struct {
	tenantstore.Store
	err error
}

func (f failingGetTenants) Get(context.Context, string) (tenantstore.Tenant, error) {
	return tenantstore.Tenant{}, f.err
}

// TestBillingEntriesHonorExemptPolicy_spec_12_8 asserts that the
// billing_buffer and billing Phase 4 entries erase nothing for a tenant
// whose billingErasurePolicy is exempt, purge a non-exempt tenant, return a
// tenant-read error without touching the stream or the durable rows, and
// that a Phase 4 re-run after the tenant_config entry leaves an exempt
// tenant's billing events in place, which only holds while the
// configuration record keeps the policy.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle), 11.2.1
func TestBillingEntriesHonorExemptPolicy_spec_12_8(t *testing.T) {
	ctx := context.Background()

	t.Run("exempt tenant keeps its billing events", func(t *testing.T) {
		f := newBillingFixture(t, "acme")
		if err := f.w.tenants.Create(ctx, tenantstore.Tenant{ID: "acme", BillingErasurePolicy: tenantstore.BillingErasureExempt}); err != nil {
			t.Fatal(err)
		}
		erasers := f.w.buildTenantErasers(nil, nil, credentialpoolstore.NewMemory(), nil)
		for _, name := range []string{"billing_buffer", "billing"} {
			if n, err := entry(t, erasers, name)(ctx, "acme"); n != 0 || err != nil {
				t.Fatalf("%s = (%d, %v), want (0, nil)", name, n, err)
			}
		}
		if f.durable(t, "acme") != 1 || f.staged(t) != 1 {
			t.Fatalf("exempt tenant lost billing events: durable %d staged %d", f.durable(t, "acme"), f.staged(t))
		}
	})

	t.Run("non-exempt tenant is purged and another tenant is untouched", func(t *testing.T) {
		f := newBillingFixture(t, "acme", "globex")
		seedTenants(t, f.w.tenants, "acme", "globex")
		erasers := f.w.buildTenantErasers(nil, nil, credentialpoolstore.NewMemory(), nil)
		if _, err := entry(t, erasers, "billing_buffer")(ctx, "acme"); err != nil {
			t.Fatalf("billing_buffer: %v", err)
		}
		if _, err := entry(t, erasers, "billing")(ctx, "acme"); err != nil {
			t.Fatalf("billing: %v", err)
		}
		if f.durable(t, "acme") != 0 || f.staged(t) != 1 || f.durable(t, "globex") != 1 {
			t.Fatalf("after purge: acme durable %d, staged %d, globex durable %d; want 0, 1, 1",
				f.durable(t, "acme"), f.staged(t), f.durable(t, "globex"))
		}
	})

	t.Run("tenant read error touches nothing", func(t *testing.T) {
		f := newBillingFixture(t, "acme")
		readErr := errors.New("tenant row unreadable")
		f.w.tenants = failingGetTenants{Store: f.w.tenants, err: readErr}
		erasers := f.w.buildTenantErasers(nil, nil, credentialpoolstore.NewMemory(), nil)
		for _, name := range []string{"billing_buffer", "billing"} {
			if _, err := entry(t, erasers, name)(ctx, "acme"); !errors.Is(err, readErr) {
				t.Fatalf("%s error = %v, want the tenant read error", name, err)
			}
		}
		if f.durable(t, "acme") != 1 || f.staged(t) != 1 {
			t.Fatalf("a failed tenant read touched billing: durable %d staged %d", f.durable(t, "acme"), f.staged(t))
		}
	})

	t.Run("re-run after tenant_config keeps an exempt tenant's events", func(t *testing.T) {
		f := newBillingFixture(t, "acme")
		if err := f.w.tenants.Create(ctx, tenantstore.Tenant{
			ID: "acme", DisplayName: "Acme Corp", BillingErasurePolicy: tenantstore.BillingErasureExempt,
		}); err != nil {
			t.Fatal(err)
		}
		phase4 := &tenantEraser{erasers: f.w.buildTenantErasers(nil, nil, credentialpoolstore.NewMemory(), nil)}
		for run := 1; run <= 2; run++ {
			if _, err := phase4.DeleteByTenant(ctx, "acme"); err != nil {
				t.Fatalf("Phase 4 run %d: %v", run, err)
			}
			if f.durable(t, "acme") != 1 {
				t.Fatalf("Phase 4 run %d deleted an exempt tenant's billing events", run)
			}
		}
	})
}

// knownTenantFields partitions every field of tenantstore.Tenant into the
// fields the tenant_config entry keeps and the ones it nulls. A new field
// fails the reflection check until it is placed in one of the two lists.
var (
	keptTenantFields = []string{
		"ID", "State", "DeletedAt", "WorkspaceTier", "DataResidencyRegion", "BillingErasurePolicy",
		"ForceDeleteHoldOverride", "ForceDeleteJustification", "ForceDeleteBy", "ForceDeleteAt",
		"CreatedAt", "UpdatedAt", "Version",
	}
	nulledTenantFields = []string{
		"DisplayName", "ComplianceProfile", "MaxConcurrentSessions", "StorageQuotaBytes",
		"TokenQuotaPerWindow", "QuotaResetPeriod", "GCPriority", "ElicitationContentIntegrity",
		"ElicitationContentIntegrityJustification", "ElicitationContentIntegrityChangedAt",
		"ElicitationContentIntegrityChangedBy", "MinIsolationProfile", "NoEnvironmentPolicy",
		"RBACConfig", "ExperimentTargeting", "CredentialPolicy", "ErasureSalt", "Suspended",
		"SuspendedReason", "SuspendedAt", "SuspendedBy",
	}
)

// populate sets every field of v to a non-zero value.
func populate(t *testing.T, v reflect.Value) {
	t.Helper()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString("x-" + v.Type().Field(i).Name)
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Int, reflect.Int64:
			f.SetInt(7)
		case reflect.Slice:
			f.Set(reflect.MakeSlice(f.Type(), 1, 1))
			if f.Type().Elem().Kind() == reflect.Uint8 {
				f.Index(0).SetUint(9)
			}
		case reflect.Struct:
			if f.Type() == reflect.TypeOf(time.Time{}) {
				f.Set(reflect.ValueOf(time.Unix(1_700_000_000, 0).UTC()))
				continue
			}
			populate(t, f)
		case reflect.Map:
			f.Set(reflect.MakeMapWithSize(f.Type(), 0))
		case reflect.Pointer:
			f.Set(reflect.New(f.Type().Elem()))
		}
	}
}

// TestTenantConfigEntryNullsAllButTheKeepList_spec_12_8 runs the
// tenant_config entry over a tenant row with every field set and asserts
// every kept field is unchanged and every other field is zero, the erasure
// salt included. It fails when tenantstore.Tenant gains a field that is in
// neither list.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle)
func TestTenantConfigEntryNullsAllButTheKeepList_spec_12_8(t *testing.T) {
	ctx := context.Background()
	typ := reflect.TypeOf(tenantstore.Tenant{})
	known := map[string]bool{}
	for _, n := range append(append([]string(nil), keptTenantFields...), nulledTenantFields...) {
		known[n] = true
	}
	for i := 0; i < typ.NumField(); i++ {
		if name := typ.Field(i).Name; !known[name] {
			t.Errorf("tenantstore.Tenant field %s is neither kept nor nulled by the tenant_config entry; place it in one list", name)
		}
	}

	var row tenantstore.Tenant
	populate(t, reflect.ValueOf(&row).Elem())
	row.ID = "acme"
	rv := reflect.ValueOf(row)
	for i := 0; i < typ.NumField(); i++ {
		if rv.Field(i).IsZero() {
			t.Fatalf("populate left field %s zero, so its nulling would not be observed", typ.Field(i).Name)
		}
	}
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before, _ := tenants.Get(ctx, "acme")
	if n, err := tenantConfigEraser(tenants)(ctx, "acme"); n != 1 || err != nil {
		t.Fatalf("tenant_config = (%d, %v), want (1, nil)", n, err)
	}
	after, err := tenants.Get(ctx, "acme")
	if err != nil {
		t.Fatalf("get after: %v", err)
	}
	bv, av := reflect.ValueOf(before), reflect.ValueOf(after)
	for _, name := range keptTenantFields {
		if name == "UpdatedAt" || name == "Version" {
			continue // the store advances both on every write
		}
		if !reflect.DeepEqual(bv.FieldByName(name).Interface(), av.FieldByName(name).Interface()) {
			t.Errorf("kept field %s changed: %v -> %v", name, bv.FieldByName(name), av.FieldByName(name))
		}
	}
	for _, name := range nulledTenantFields {
		if !av.FieldByName(name).IsZero() {
			t.Errorf("field %s not nulled: %v", name, av.FieldByName(name))
		}
	}
	if n, err := tenantConfigEraser(tenants)(ctx, "missing"); n != 0 || err != nil {
		t.Errorf("tenant_config on a missing row = (%d, %v), want (0, nil)", n, err)
	}
}

// recordingPurger records the root sessions PurgeRoot is called for.
type recordingPurger struct{ roots []string }

func (r *recordingPurger) PurgeRoot(_ context.Context, root string) (int, error) {
	r.roots = append(r.roots, root)
	return 1, nil
}

// recordingDeleter records DeleteByTenant calls.
type recordingDeleter struct {
	tenants []string
	n       int
	err     error
}

func (r *recordingDeleter) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	r.tenants = append(r.tenants, tenantID)
	return r.n, r.err
}

// goneEnvironments is an environment store whose Delete reports every
// environment already gone.
type goneEnvironments struct {
	environmentstore.Store
	deletes int
}

func (g *goneEnvironments) Delete(context.Context, string, string) error {
	g.deletes++
	return environmentstore.ErrNotFound
}

// failingListSessions is a session store whose List always fails.
type failingListSessions struct {
	sessionstore.Store
	err error
}

func (f failingListSessions) List(context.Context, string, sessionstore.ListFilter) ([]sessionstore.Session, error) {
	return nil, f.err
}

// TestTenantEntryAdapters_spec_12_8 covers the Phase 4 entry adapters:
// the delegation budget entry purges root sessions only and then the
// tenant's checkpoint rows, the tenant_definitions entry treats an
// environment already gone as deleted, forEachTenantSession stops at the
// first error, and the audit entry makes no call for the platform tenant.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle)
func TestTenantEntryAdapters_spec_12_8(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	sessions := memstore.New()
	for _, s := range []sessionstore.Session{
		{ID: "root-a", TenantID: "acme", State: session.StateRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "self-root", TenantID: "acme", RootSessionID: "self-root", State: session.StateRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "child-a", TenantID: "acme", ParentSessionID: "root-a", RootSessionID: "root-a", State: session.StateCompleted, CreatedAt: now, UpdatedAt: now},
		{ID: "root-g", TenantID: "globex", State: session.StateRunning, CreatedAt: now, UpdatedAt: now},
	} {
		if err := sessions.Create(ctx, s); err != nil {
			t.Fatalf("seed %s: %v", s.ID, err)
		}
	}

	t.Run("delegation budget purges root sessions only", func(t *testing.T) {
		purger := &recordingPurger{}
		checkpoints := &recordingDeleter{n: 2}
		n, err := delegationBudgetEraser(sessions, purger, checkpoints)(ctx, "acme")
		if err != nil {
			t.Fatalf("delegation_budget: %v", err)
		}
		got := strings.Join(sortedCopy(purger.roots), ",")
		if got != "root-a,self-root" {
			t.Fatalf("PurgeRoot roots = %s, want root-a,self-root", got)
		}
		if n != 4 || len(checkpoints.tenants) != 1 || checkpoints.tenants[0] != "acme" {
			t.Fatalf("count %d, checkpoint deletes %v; want 4 and one acme delete", n, checkpoints.tenants)
		}
	})

	t.Run("delegation budget without Postgres purges keys only", func(t *testing.T) {
		purger := &recordingPurger{}
		if _, err := delegationBudgetEraser(sessions, purger, nil)(ctx, "acme"); err != nil {
			t.Fatalf("delegation_budget: %v", err)
		}
		if len(purger.roots) != 2 {
			t.Fatalf("roots = %v, want two", purger.roots)
		}
	})

	t.Run("tenant definitions treat a gone environment as deleted", func(t *testing.T) {
		envs := environmentstore.NewMemory()
		if err := envs.Create(ctx, environmentstore.Environment{Name: "prod", TenantID: "acme"}); err != nil {
			t.Fatalf("seed environment: %v", err)
		}
		gone := &goneEnvironments{Store: envs}
		n, err := environmentTenantEraser(gone)(ctx, "acme")
		if err != nil || n != 0 || gone.deletes != 1 {
			t.Fatalf("environment entry = (%d, %v) with %d deletes, want (0, nil) with 1", n, err, gone.deletes)
		}
		n, err = environmentTenantEraser(envs)(ctx, "acme")
		if err != nil || n != 1 {
			t.Fatalf("environment entry = (%d, %v), want (1, nil)", n, err)
		}
		if rows, _ := envs.List(ctx, "acme"); len(rows) != 0 {
			t.Fatalf("environments left: %v", rows)
		}
	})

	t.Run("forEachTenantSession stops at the first error", func(t *testing.T) {
		boom := errors.New("erase failed")
		var visited []string
		_, err := forEachTenantSession(ctx, sessions, "acme", func(_ context.Context, s sessionstore.Session) (int, error) {
			visited = append(visited, s.ID)
			return 0, boom
		})
		if !errors.Is(err, boom) || len(visited) != 1 {
			t.Fatalf("error %v after %v, want boom after one session", err, visited)
		}
		listErr := errors.New("primary unreachable")
		_, err = forEachTenantSession(ctx, failingListSessions{Store: sessions, err: listErr}, "acme",
			func(context.Context, sessionstore.Session) (int, error) {
				t.Fatal("fn ran although the listing failed")
				return 0, nil
			})
		if !errors.Is(err, listErr) {
			t.Fatalf("listing error = %v, want %v", err, listErr)
		}
	})

	t.Run("audit erases nothing for the platform tenant", func(t *testing.T) {
		audit := &recordingDeleter{n: 3}
		if n, err := auditTenantEraser(audit)(ctx, jwtaudit.PlatformTenantID); n != 0 || err != nil {
			t.Fatalf("audit(platform) = (%d, %v), want (0, nil)", n, err)
		}
		if len(audit.tenants) != 0 {
			t.Fatalf("DeleteByTenant called for %v", audit.tenants)
		}
		if n, err := auditTenantEraser(audit)(ctx, "acme"); n != 3 || err != nil || len(audit.tenants) != 1 {
			t.Fatalf("audit(acme) = (%d, %v) calls %v, want (3, nil) and one call", n, err, audit.tenants)
		}
	})
}

// sortedCopy returns a sorted copy of s.
func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

// TestPhase4TenantScopedEntriesIsolateTenants_spec_12_8 runs the in-memory
// Phase 4 sequence for acme over a store set seeded with an acme row and a
// globex twin in every tenant-scoped store the new entries erase, and
// asserts every acme row is gone, every globex row survives, and a second
// run succeeds.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle)
func TestPhase4TenantScopedEntriesIsolateTenants_spec_12_8(t *testing.T) {
	ctx := context.Background()
	w := inMemoryEraserWiring()
	seedTenants(t, w.tenants, "acme", "globex")
	now := time.Now()
	for _, tenant := range []string{"acme", "globex"} {
		mustDo(t, "grant runtime", func() error {
			_, err := w.tenantAccess.Grant(ctx, tenantaccessstore.KindRuntime, "echo", tenant, "admin", now)
			return err
		})
		mustDo(t, "grant pool", func() error {
			_, err := w.tenantAccess.Grant(ctx, tenantaccessstore.KindPool, "warm", tenant, "admin", now)
			return err
		})
		mustDo(t, "user", func() error {
			return w.users.Create(ctx, userstore.User{Subject: "alice@" + tenant + ".com", TenantID: tenant, Email: "alice@" + tenant + ".com"})
		})
		mustDo(t, "custom role", func() error {
			return w.customRoles.Create(ctx, customrolestore.CustomRole{TenantID: tenant, Name: "auditor", CreatedAt: now, UpdatedAt: now})
		})
		mustDo(t, "connector", func() error {
			return w.connectors.Create(ctx, connectorstore.Connector{TenantID: tenant, ID: "github", DisplayName: "GitHub", MCPServerURL: "https://mcp.example.com"})
		})
		mustDo(t, "environment", func() error {
			return w.environments.Create(ctx, environmentstore.Environment{Name: "prod", TenantID: tenant})
		})
		mustDo(t, "connector token", func() error {
			return w.connectorCreds.Put(ctx, connectorcredstore.ConnectorCredential{TenantID: tenant, ConnectorID: "github", UserID: "alice", AccessToken: "at", RefreshToken: "rt"})
		})
		mustDo(t, "registered credential", func() error {
			_, err := w.credentials.Register(ctx, tenant, "alice", credential.ProviderAnthropicDirect, "", "sk-test")
			return err
		})
	}
	phase4 := &tenantEraser{erasers: w.buildTenantErasers(nil, nil, credentialpoolstore.NewMemory(), nil)}
	for run := 1; run <= 2; run++ {
		if _, err := phase4.DeleteByTenant(ctx, "acme"); err != nil {
			t.Fatalf("Phase 4 run %d: %v", run, err)
		}
	}
	residue := func(tenant string) map[string]int {
		out := map[string]int{}
		rt, _ := w.tenantAccess.ListForTenant(ctx, tenantaccessstore.KindRuntime, tenant)
		pl, _ := w.tenantAccess.ListForTenant(ctx, tenantaccessstore.KindPool, tenant)
		users, _ := w.users.List(ctx, tenant, userstore.ListFilter{})
		roles, _ := w.customRoles.List(ctx, tenant)
		conns, _ := w.connectors.List(ctx, tenant, connectorstore.ListFilter{})
		envs, _ := w.environments.List(ctx, tenant)
		tokens, _ := w.connectorCreds.ListByConnector(ctx, tenant, "github")
		creds, _ := w.credentials.List(ctx, tenant, "alice")
		out["runtime_tenant_access"], out["pool_tenant_access"] = len(rt), len(pl)
		out["user_roles"], out["custom_roles"] = len(users), len(roles)
		out["connectors"], out["environments"] = len(conns), len(envs)
		out["connector_tokens"], out["registered_credentials"] = len(tokens), len(creds)
		return out
	}
	for store, n := range residue("acme") {
		if n != 0 {
			t.Errorf("acme %s rows left: %d", store, n)
		}
	}
	for store, n := range residue("globex") {
		if n != 1 {
			t.Errorf("globex %s rows = %d, want 1 (an acme deletion touched another tenant)", store, n)
		}
	}
	if row, err := w.tenants.Get(ctx, "globex"); err != nil || row.DisplayName != "globex" {
		t.Errorf("globex configuration record changed: %+v, %v", row, err)
	}
}

// mustDo fails the test when fn returns an error.
func mustDo(t *testing.T, what string, fn func() error) {
	t.Helper()
	if err := fn(); err != nil {
		t.Fatalf("seed %s: %v", what, err)
	}
}
