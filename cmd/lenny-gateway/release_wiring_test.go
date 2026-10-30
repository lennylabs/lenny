// SPDX-License-Identifier: MIT

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditscope"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/impersonation"
	revocationprop "github.com/lennylabs/lenny/pkg/gateway/credentials/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// assertWired fails the test for every seam whose wiring text is missing
// from its file, comparing with whitespace collapsed so formatting changes
// do not break the pin.
func assertWired(t *testing.T, seams []struct{ file, want, seam string }) {
	t.Helper()
	space := regexp.MustCompile(`\s+`)
	for _, tc := range seams {
		b, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatalf("read %s: %v", tc.file, err)
		}
		if !strings.Contains(space.ReplaceAllString(string(b), " "), tc.want) {
			t.Errorf("%s: %s is not wired (%q missing)", tc.file, tc.seam, tc.want)
		}
	}
}

// TestSlotSetRecoveryProbeRunsOnTheWatchdogContext_spec_12_4 pins the
// gateway's wiring of the slot-set recovery: the slot counter is built with
// the coordination-client probe and the operator-tunable probe interval, it
// is published on the wiring, and the reconciler workers start its recovery
// loop on the watchdog context. Counter.RunRecovery is tested on its own; a
// gateway that never starts it leaves a replica with no slot traffic unable
// to rebuild the slot sets when a Redis outage ends, which only this pin
// catches.
//
// spec: §12.4 (Redis HA and Failure Modes), §5.2 (Slot assignment atomicity)
func TestSlotSetRecoveryProbeRunsOnTheWatchdogContext_spec_12_4(t *testing.T) {
	assertWired(t, []struct{ file, want, seam string }{
		{"pod_lifecycle.go", "slotcounter.WithRecoveryProbeInterval(*slotCounterRecoveryProbeInterval),", "the recovery probe interval"},
		{"pod_lifecycle.go", "slotcounter.WithRecoveryProbe(func(context.Context) error { return redisconn.PingWithTimeout(slotRedis, slotCounterProbeTimeout) })", "the recovery probe"},
		{"pod_lifecycle.go", "w.slotCounter = slotCounter", "the slot counter's publication on the wiring"},
		{"reconciler_workers.go", "if w.slotCounter != nil { go w.slotCounter.RunRecovery(w.watchdogCtx) }", "the recovery loop on the watchdog context"},
	})
}

// TestFullRevokeFanOutReleasesThroughTheSessionServer_spec_11_4 pins the
// admin router's full_revoke fan-out wiring: each binding is released
// through the session server's terminal binding release, and the lease
// revoker returns each lease to its pool through the credential assigner
// and writes no deny-list entry. The fan-out and the
// revoker are unit-tested with injected fakes, so a production wiring that
// drops the releaser or passes no assigner is caught only here.
//
// spec: §11.4 (User Invalidation), §4.9 (Credential Leasing)
func TestFullRevokeFanOutReleasesThroughTheSessionServer_spec_11_4(t *testing.T) {
	assertWired(t, []struct{ file, want, seam string }{
		{"adminrouter.go", "fanOut := &bindReleaseFanOut{registry: w.podRegistry, releaser: sessionSrv}", "the binding release through the session server"},
		{"adminrouter.go", "userPods = fanOut", "the pod terminator handed to the admin router"},
		{"adminrouter.go", "leaseRevoker := leaserevoke.New(w.llmLeases, w.credAssign, nil)", "the lease revoker's credential assigner"},
		{"adminrouter.go", "userLeases = leaseRevoker", "the lease revoker handed to the admin router"},
	})
}

// TestLifecycleDrainsShareTheBindReleaseFanOut_spec_12_8 pins the gateway's
// wiring of the drain publish: the binding-release fan-out is built once, and
// the same terminator reaches the erasure drain, the tenant-deletion drain,
// and the admin router, whose tenant-suspension drain reads it. The drain and
// the terminator are unit-tested with injected fakes, so a wiring that hands
// a drain nil, or builds a second fan-out with a second subscriber, is caught
// only here.
//
// spec: §12.8 (Compliance Interfaces: Lifecycle fence and drain), §11.4 (User Invalidation), §28.5.7 (Gateway-to-store)
func TestLifecycleDrainsShareTheBindReleaseFanOut_spec_12_8(t *testing.T) {
	assertWired(t, []struct{ file, want, seam string }{
		{"adminrouter.go", "admin.DrainSessions(ctx, w.sessions, erasureTerm, userPods, tenantID, userID)", "the erasure drain's fan-out"},
		{"adminrouter.go", "beginTerminal: sessionSrv.BeginTerminal}, userPods, *lifecycleTickBudget, )", "the tenant-deletion drain's fan-out"},
		{"adminrouter.go", "adminRouter = adminRouter.WithUserRevocation(userPods, userLeases, userTokens)", "the admin router's fan-out"},
		{"tenantdeletion.go", "admin.DrainSessions(tctx, t.sessions, t.term, t.pods, tenantID, \"\")", "the tenant-deletion terminator's publish"},
	})
	b, err := os.ReadFile("adminrouter.go")
	if err != nil {
		t.Fatalf("read adminrouter.go: %v", err)
	}
	src := string(b)
	if n := strings.Count(src, "bindrelease.New("); n != 1 {
		t.Errorf("adminrouter.go builds %d binding-release propagators, want exactly one subscriber", n)
	}
	build := strings.Index(src, "fanOut := &bindReleaseFanOut{")
	erasure := strings.Index(src, "WithSessionDrain(")
	if build < 0 || erasure < 0 || build > erasure {
		t.Errorf("the binding-release fan-out is built at offset %d, after the erasure drain at %d", build, erasure)
	}
}

// TestLegalHoldEscrowRunsOverTheEscrowStore_spec_12_8 pins the gateway's
// legal-hold escrow wiring: one escrow store is built over the configured
// escrow bucket from the artifact store's provider options, the Phase 3.5
// migrator writes to it and reads the durable escrow records for its
// re-entry skip, and the Releaser deletes from it. The migrator and the
// Releaser are unit-tested over injected stores, so a production wiring
// that hands either the artifact store, whose tenant prefix delete would
// destroy the escrow copies, is caught only here.
//
// spec: 12.8 (Compliance Interfaces: --acknowledge-hold-override admin action (tenant scope)), 12.8 (Compliance Interfaces: Tenant deletion lifecycle Phase 3.5)
func TestLegalHoldEscrowRunsOverTheEscrowStore_spec_12_8(t *testing.T) {
	assertWired(t, []struct{ file, want, seam string }{
		{"adminrouter.go", "escrowBlobs, err := blobescrow.NewStore(context.Background(), objectStorageOptions(f), *legalHoldEscrowBucket, *legalHoldEscrowEndpoint)", "the escrow store over the escrow bucket"},
		{"adminrouter.go", "escrowBlobs: escrowBlobs, records: escrowRecords,", "the escrow store and escrow records in the tenantEscrowMigrator literal"},
		{"adminrouter.go", "Deleter: blobescrow.Deleter{Blobs: escrowBlobs},", "the escrow store in the Releaser's deleter"},
		{"legalholdescrow.go", "Source: blobescrow.Source{Blobs: m.blobs},", "the original reader over the artifact store"},
		{"legalholdescrow.go", "Escrow: blobescrow.Writer{Blobs: m.escrowBlobs, TenantID: req.TenantID},", "the escrow writer over the escrow store"},
		{"legalholdescrow.go", "Records: m.records,", "the escrow records on the Migrator"},
	})
}

// TestErasureListsAndTenantListersAreWired_spec_12_8 pins the gateway
// wiring the eraser and lister tests cannot observe: the semantic cache the
// tenant-deletion Phase 4 list erases is the one the user erasure and the
// LLM proxy share; the billing retention pruner, the revocation-cache
// rehydration, and the issued-token GC list tombstoned tenants; and the
// billing flusher and the idempotency-key GC keep listing live tenants
// only. The semantic cache is in-process, so no integration test can tell
// a nil argument from the shared instance.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle), 13.3 (Credential Flow: Authoritative durability for revocation), 12.2 (Storage Roles)
func TestErasureListsAndTenantListersAreWired_spec_12_8(t *testing.T) {
	assertWired(t, []struct{ file, want, seam string }{
		{"adminrouter.go", "tenantErasers := w.buildTenantErasers(quotaCounter, quotaFailOpenAccum, credentialPools, erasureSemanticCache)", "the shared semantic cache in the tenant Phase 4 list"},
		{"adminrouter.go", "userScoped := w.buildUserErasers(quotaCounter, quotaFailOpenAccum, credentialPools, erasureSemanticCache)", "the shared semantic cache in the user erasure list"},
		{"billing_security_workers.go", "billingretention.New(w.billing, allTenantsLister{w.tenants},", "the billing retention pruner over every tenant row"},
		{"billing_security_workers.go", "w.billingTier.NewFlusherManager(tenantsLister{w.tenants},", "the billing flushers over live tenants"},
		{"billing_security_workers.go", "lister := allTenantsLister{w.tenants} if err := w.revCache.Rehydrate(context.Background(), lister, issued)", "the revocation-cache rehydration over every tenant row"},
		{"billing_security_workers.go", "idemGC := idempgstore.New(w.pgPool) lister := tenantsLister{w.tenants}", "the idempotency-key GC over live tenants"},
		{"billing_security_workers.go", "w.startIssuedTokenGC(issuedtokenstore.New(w.pgPool), time.Duration(*issuedTokenGCIntervalSeconds)*time.Second)", "the issued-token GC start"},
		{"billing_security_workers.go", "lister := allTenantsLister{w.tenants} sweepIssuedTokens(context.Background(), gc, lister, clockinject.Now)", "the issued-token GC over every tenant row"},
	})
}

// fullImpersonationCollaborators returns one non-nil value of every
// collaborator the impersonation service needs. None is dialled: the
// wiring helper only stores them.
func fullImpersonationCollaborators() impersonationCollaborators {
	return impersonationCollaborators{
		pool:     &pgxpool.Pool{},
		signer:   &jwt.BreakerSigner{},
		cache:    &revocationprop.Propagator{},
		audit:    &auditstore.Store{},
		appender: &auditscope.Validator{},
	}
}

// impersonationListStatus serves GET /v1/admin/impersonation as a
// tenant-admin through an admin router wired with svc. A registered route
// answers 403 from the handler's platform-admin gate before any service
// call; an unregistered route answers 404 from the mux.
func impersonationListStatus(t *testing.T, svc admin.ImpersonationService) int {
	t.Helper()
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{}).WithImpersonation(svc)
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/impersonation", nil)
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "alice@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleTenantAdmin},
	}))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	return rr.Code
}

// TestImpersonationRoutesRequireEveryCollaborator_spec_13_3 asserts that a
// gateway missing any one of the Postgres pool, the signer, the revocation
// propagator, or the audit store builds no impersonation service, that the
// absent service is a true nil interface rather than a typed-nil
// *impersonation.Service, and that the admin router then registers no
// impersonation route. A typed nil would pass the router's nil check and
// register routes that panic on first use, and a service built without a
// collaborator would mint a bearer nobody can revoke.
//
// spec: 13.3 (Credential Flow: Platform-admin impersonation), 16.7 (Section 25 audit events), 12.3 (Postgres HA Requirements)
func TestImpersonationRoutesRequireEveryCollaborator_spec_13_3(t *testing.T) {
	cfg := impersonation.Config{NewID: func() string { return "imp-x" }}
	missing := map[string]func(*impersonationCollaborators){
		"postgres pool":         func(c *impersonationCollaborators) { c.pool = nil },
		"signer":                func(c *impersonationCollaborators) { c.signer = nil },
		"revocation propagator": func(c *impersonationCollaborators) { c.cache = nil },
		"audit store":           func(c *impersonationCollaborators) { c.audit = nil },
		"audit validator":       func(c *impersonationCollaborators) { c.appender = nil },
	}
	for name, drop := range missing {
		t.Run("without "+name, func(t *testing.T) {
			c := fullImpersonationCollaborators()
			drop(&c)
			svc := newImpersonationService(c, cfg)
			if svc != nil {
				t.Fatalf("service = %#v, want a nil interface", svc)
			}
			if code := impersonationListStatus(t, svc); code != http.StatusNotFound {
				t.Fatalf("GET /v1/admin/impersonation status = %d, want 404 (no route)", code)
			}
		})
	}
	t.Run("with every collaborator", func(t *testing.T) {
		svc := newImpersonationService(fullImpersonationCollaborators(), cfg)
		if svc == nil {
			t.Fatal("service is nil with every collaborator present")
		}
		if code := impersonationListStatus(t, svc); code != http.StatusForbidden {
			t.Fatalf("GET /v1/admin/impersonation status = %d, want 403 from the registered handler", code)
		}
	})
}

// TestNoImpersonationSweepRuns_spec_16_7 asserts that no impersonation
// expiry sweep exists to start: the service has no sweep method, and no
// gateway source file starts one. Only a winning DELETE writes the ended
// event, so a sweep would write a second, unsanctioned one.
//
// spec: 16.7 (Section 25 audit events), 13.3 (Credential Flow: Platform-admin impersonation)
func TestNoImpersonationSweepRuns_spec_16_7(t *testing.T) {
	if _, ok := reflect.TypeOf(&impersonation.Service{}).MethodByName("SweepExpired"); ok {
		t.Fatal("impersonation.Service still has a SweepExpired method")
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if strings.Contains(string(b), "SweepExpired") {
			t.Errorf("%s starts an impersonation sweep", f)
		}
	}
	assertWired(t, []struct{ file, want, seam string }{
		{"adminrouter.go", "adminRouter = adminRouter.WithImpersonation(newImpersonationService(impersonationCollaborators{", "the impersonation service through the collaborator guard"},
		{"adminrouter.go", "pool: w.pgPool, signer: w.jwtSigner, cache: revProp, audit: auditOpsStore, appender: auditValidator,", "every collaborator handed to the guard"},
	})
}
