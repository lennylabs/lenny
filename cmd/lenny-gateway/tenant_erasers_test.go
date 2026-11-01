// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore/failover"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointretention"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/tests/testinfra/credsecretfake"
)

// inMemoryEraserWiring returns a gateway wiring holding the in-memory
// backend of every store the tenant Phase 4 eraser reads, as a gateway
// started without Postgres or Redis holds them.
func inMemoryEraserWiring() *gatewayWiring {
	w := &gatewayWiring{}
	w.tenants = tenantstore.NewMemory()
	w.sessions = memstore.New()
	w.interactions = interactionstore.NewMemory()
	w.evals = evalstore.NewMemory(0, time.Now)
	w.memories = memorystore.NewInMemory(0, time.Now)
	w.treeArchive = treearchive.NewCached(treearchive.NewMemory(), 0)
	w.transcripts = transcriptstore.NewMemory()
	w.partialManifests = partialmanifeststore.NewMemoryStore(nil)
	w.checkpointRetention = checkpointretention.NewMemoryStore(nil)
	w.blobs = blobstore.NewMemoryStore(nil)
	w.billingStream = failover.NewMemStream()
	w.billingPipeline = failover.New(failover.Options{Primary: billingstore.NewMemory(), Stream: w.billingStream})
	w.connectorCreds = connectorcredstore.NewMemory(nil)
	w.credentials = credentialstore.NewMemory(nil)
	w.tenantAccess = tenantaccessstore.NewMemory()
	w.users = userstore.NewMemory()
	w.customRoles = customrolestore.NewMemory()
	w.connectors = connectorstore.NewMemory()
	w.environments = environmentstore.NewMemory()
	w.experiments = experimentstore.NewMemory()
	return w
}

// TestBuildTenantErasersOrder_spec_12_8 asserts the in-memory gateway's
// Phase 4 sequence lists every store the tenant path wires, in
// rank order with every session-keyed store ahead of SessionStore, and
// that the sequence passes the startup order and completeness check.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle)
func TestBuildTenantErasersOrder_spec_12_8(t *testing.T) {
	w := inMemoryEraserWiring()
	names := tenantEraserNames(w.buildTenantErasers(nil, nil, credentialpoolstore.NewMemory(), nil))
	got := strings.Join(names, ",")
	want := "leases,coordination_mirror,semantic_cache,experiment_sticky,billing_buffer,quota,artifacts," +
		"checkpoint_manifests,checkpoint_retention,checkpoint_meta,transcripts,interactions,memory," +
		"session_message_queues,session_tree_archive,audit,billing,eval_results,delegation_budget," +
		"sessions,tokens,issued_tokens,credential_pool,runtime_tenant_access,pool_tenant_access," +
		"user_roles,custom_roles,tenant_definitions,tenant_config"
	if got != want {
		t.Fatalf("tenant erasers = %s, want %s", got, want)
	}
	if err := erasure.ValidateTenantOrder(names); err != nil {
		t.Fatalf("gateway tenant eraser order rejected: %v", err)
	}
}

// TestTenantEraserClearsArchivedTreesBeforeSessions_spec_12_8 runs the
// in-memory Phase 4 sequence over a tenant whose root session holds an
// archived delegation tree, and asserts the archive rows and the
// sessions are gone, the receipt tallies the archive step, and another
// tenant's rows survive.
//
// spec: §12.8 (Compliance Interfaces, tenant deletion Phase 4).
func TestTenantEraserClearsArchivedTreesBeforeSessions_spec_12_8(t *testing.T) {
	ctx := context.Background()
	w := inMemoryEraserWiring()
	now := time.Now()
	seedTenants(t, w.tenants, "acme", "globex")
	for _, s := range []sessionstore.Session{
		{ID: "root-a", TenantID: "acme", State: session.StateRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "child-a", TenantID: "acme", ParentSessionID: "root-a", State: session.StateCompleted, CreatedAt: now, UpdatedAt: now},
		{ID: "root-g", TenantID: "globex", State: session.StateRunning, CreatedAt: now, UpdatedAt: now},
	} {
		if err := w.sessions.Create(ctx, s); err != nil {
			t.Fatalf("seed session %s: %v", s.ID, err)
		}
	}
	for _, n := range []treearchive.ArchivedNode{
		{TenantID: "acme", RootSessionID: "root-a", NodeSessionID: "child-a", State: "completed", SettledAt: now},
		{TenantID: "globex", RootSessionID: "root-g", NodeSessionID: "child-g", State: "completed", SettledAt: now},
	} {
		if err := w.treeArchive.Archive(ctx, n); err != nil {
			t.Fatalf("seed archive: %v", err)
		}
	}

	eraser := &tenantEraser{erasers: w.buildTenantErasers(nil, nil, credentialpoolstore.NewMemory(), nil)}
	counts, err := eraser.DeleteByTenant(ctx, "acme")
	if err != nil {
		t.Fatalf("Phase 4 DeleteByTenant: %v", err)
	}
	if counts["session_tree_archive"] != 1 || counts["sessions"] != 2 {
		t.Fatalf("receipt counts = %v, want session_tree_archive=1 sessions=2", counts)
	}
	if _, err := w.treeArchive.GetByNode(ctx, "acme", "child-a"); !errors.Is(err, treearchive.ErrNotFound) {
		t.Fatalf("acme archive row survived Phase 4: %v", err)
	}
	if _, err := w.sessions.Get(ctx, "acme", "root-a"); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Fatalf("acme root session survived Phase 4: %v", err)
	}
	if _, err := w.treeArchive.GetByNode(ctx, "globex", "child-g"); err != nil {
		t.Fatalf("globex archive row must survive: %v", err)
	}
	if _, err := w.sessions.Get(ctx, "globex", "root-g"); err != nil {
		t.Fatalf("globex session must survive: %v", err)
	}
}

// TestTenantEraserStopsAtFirstFailure_spec_12_8 asserts that a failing
// store aborts Phase 4 with the store named in the error, and that the
// stores after it, SessionStore among them, are not run, so the phase is
// retried from the start on the next pass.
//
// spec: §12.8 (Compliance Interfaces, tenant deletion Phase 4,
// Idempotency and resumption).
func TestTenantEraserStopsAtFirstFailure_spec_12_8(t *testing.T) {
	var ran []string
	step := func(name string, err error) namedTenantEraser {
		return namedTenantEraser{name, func(context.Context, string) (int, error) {
			ran = append(ran, name)
			return 1, err
		}}
	}
	e := &tenantEraser{erasers: []namedTenantEraser{
		step("transcripts", nil),
		step("session_tree_archive", errors.New("archive delete failed")),
		step("sessions", nil),
	}}
	counts, err := e.DeleteByTenant(context.Background(), "acme")
	if err == nil || !strings.Contains(err.Error(), "session_tree_archive") {
		t.Fatalf("DeleteByTenant error = %v, want it to name session_tree_archive", err)
	}
	if strings.Join(ran, ",") != "transcripts,session_tree_archive" {
		t.Fatalf("ran = %v, want the sequence to stop at the failing store", ran)
	}
	if _, ok := counts["sessions"]; ok {
		t.Fatal("sessions counted although it never ran")
	}
}

// TestCountlessAdaptsErrorOnlyErasers_spec_12_8 covers the adapter for
// error-only DeleteByTenant stores: success records zero, and an error
// passes through unchanged.
//
// spec: §12.8 (Compliance Interfaces).
func TestCountlessAdaptsErrorOnlyErasers_spec_12_8(t *testing.T) {
	boom := errors.New("boom")
	if n, err := countless(func(context.Context, string) error { return nil })(context.Background(), "acme"); n != 0 || err != nil {
		t.Fatalf("countless success = (%d, %v), want (0, nil)", n, err)
	}
	if _, err := countless(func(context.Context, string) error { return boom })(context.Background(), "acme"); !errors.Is(err, boom) {
		t.Fatalf("countless error = %v, want boom", err)
	}
}

// TestTenantPhase4DeletesLennyWrittenSecrets_spec_12_8 runs the gateway's
// Phase 4 sequence with the credential Secret writer wired and asserts the
// credential_pool entry issues the tenant's delete-collection after the pool
// rows are gone, removing an orphan no row names, while another tenant's
// Secret and rows stay. A failed delete-collection aborts Phase 4 at the
// credential_pool entry so the job retries the phase, and the retry
// completes.
//
// spec: §12.8 (Compliance Interfaces), §4.9 (Credential Storage Topology)
func TestTenantPhase4DeletesLennyWrittenSecrets_spec_12_8(t *testing.T) {
	ctx := context.Background()
	w := inMemoryEraserWiring()
	seedTenants(t, w.tenants, "acme", "globex")
	pools := credentialpoolstore.NewMemory()
	acmeName := credential.ManagedSecretName("acme", "p1", "key-1")
	globexName := credential.ManagedSecretName("globex", "p1", "key-1")
	orphan := credential.ManagedSecretName("acme", "removed", "key-1")
	for tenant, name := range map[string]string{"acme": acmeName, "globex": globexName} {
		if err := pools.Create(ctx, credentialpoolstore.CredentialPool{
			TenantID: tenant, Name: "p1", Provider: "anthropic_direct",
			Credentials: []credentialpoolstore.Credential{{ID: "key-1", SecretRef: credential.CanonicalSecretRef(credential.DefaultNamespace, name)}},
		}); err != nil {
			t.Fatalf("seed pool %s: %v", tenant, err)
		}
	}
	ns := credsecretfake.New()
	ns.Seed(acmeName, "acme")
	ns.Seed(orphan, "acme")
	ns.Seed(globexName, "globex")
	apiDown := errors.New("kube-apiserver unavailable")
	ns.BeforeDeleteTenant(func(context.Context, string) error { return apiDown })
	w.credentialSecretWriter = ns
	w.credentialsNamespace = credential.DefaultNamespace

	phase4 := &tenantEraser{erasers: w.buildTenantErasers(nil, nil, pools, nil)}
	counts, err := phase4.DeleteByTenant(ctx, "acme")
	if !errors.Is(err, apiDown) || !strings.Contains(err.Error(), "credential_pool") {
		t.Fatalf("Phase 4 error = %v, want the delete-collection failure named at credential_pool", err)
	}
	if _, ok := counts["runtime_tenant_access"]; ok {
		t.Fatal("Phase 4 ran past the failed credential_pool entry")
	}
	if !ns.Has(acmeName) || !ns.Has(orphan) {
		t.Fatal("a failed delete-collection removed a Secret")
	}

	ns.BeforeDeleteTenant(nil)
	counts, err = phase4.DeleteByTenant(ctx, "acme")
	if err != nil {
		t.Fatalf("retried Phase 4: %v", err)
	}
	if _, ok := counts["credential_pool"]; !ok {
		t.Fatalf("receipt counts = %v, want a credential_pool entry", counts)
	}
	if ns.Has(acmeName) || ns.Has(orphan) {
		t.Fatalf("acme Secrets left after Phase 4: %v", ns.Names())
	}
	if !ns.Has(globexName) {
		t.Fatal("globex Secret deleted by acme's Phase 4")
	}
	if _, err := pools.Get(ctx, "globex", "p1"); err != nil {
		t.Fatalf("globex pool row must survive: %v", err)
	}
	if got := ns.Calls(); len(got) != 2 || got[0] != "deleteTenant acme" || got[1] != "deleteTenant acme" {
		t.Fatalf("writer calls = %v, want two delete-collections for acme and no per-reference Delete", got)
	}
}

// TestTenantPhase4WithoutWriterRefusesSoftDeletedWrittenRef_spec_4_9 runs
// the gateway's Phase 4 sequence with no credential Secret writer over a
// tenant whose only Lenny-written reference sits on a soft-deleted pool,
// and asserts Phase 4 fails at the credential_pool entry with the row kept,
// while a tenant with operator references only completes.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 6; §12.8 (Compliance Interfaces)
func TestTenantPhase4WithoutWriterRefusesSoftDeletedWrittenRef_spec_4_9(t *testing.T) {
	ctx := context.Background()
	w := inMemoryEraserWiring()
	seedTenants(t, w.tenants, "acme", "globex")
	w.credentialsNamespace = credential.DefaultNamespace
	pools := credentialpoolstore.NewMemory()
	seed := func(tenant, ref string) {
		if err := pools.Create(ctx, credentialpoolstore.CredentialPool{
			TenantID: tenant, Name: "p1", Provider: "anthropic_direct",
			Credentials: []credentialpoolstore.Credential{{ID: "key-1", SecretRef: ref}},
		}); err != nil {
			t.Fatalf("seed pool %s: %v", tenant, err)
		}
	}
	seed("acme", credential.CanonicalSecretRef(credential.DefaultNamespace, credential.ManagedSecretName("acme", "p1", "key-1")))
	if err := pools.SoftDelete(ctx, "acme", "p1", time.Now()); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}
	seed("globex", credential.CanonicalSecretRef(credential.DefaultNamespace, "operator-made"))

	phase4 := &tenantEraser{erasers: w.buildTenantErasers(nil, nil, pools, nil)}
	_, err := phase4.DeleteByTenant(ctx, "acme")
	if !errors.Is(err, admin.ErrLennyWrittenRefWithoutWriter) || !strings.Contains(err.Error(), "credential_pool") {
		t.Fatalf("Phase 4 error = %v, want ErrLennyWrittenRefWithoutWriter at credential_pool", err)
	}
	rows, err := pools.List(ctx, "acme", credentialpoolstore.ListFilter{IncludeDeleted: true})
	if err != nil || len(rows) != 1 {
		t.Fatalf("acme rows after refusal = (%d, %v), want the soft-deleted row kept", len(rows), err)
	}
	if _, err := phase4.DeleteByTenant(ctx, "globex"); err != nil {
		t.Fatalf("Phase 4 for a tenant with operator references only: %v", err)
	}
}

// seedTenants creates an active tenant row for each id.
func seedTenants(t *testing.T, tenants tenantstore.Store, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := tenants.Create(context.Background(), tenantstore.Tenant{ID: id, DisplayName: id, CreatedAt: time.Now()}); err != nil {
			t.Fatalf("seed tenant %s: %v", id, err)
		}
	}
}
