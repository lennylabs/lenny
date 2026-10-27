// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
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
