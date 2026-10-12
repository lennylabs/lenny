// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointretention"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
)

// inMemoryEraserWiring returns a gateway wiring holding the in-memory
// backend of every store the tenant Phase 4 eraser reads, as a gateway
// started without Postgres or Redis holds them.
func inMemoryEraserWiring() *gatewayWiring {
	w := &gatewayWiring{}
	w.sessions = memstore.New()
	w.interactions = interactionstore.NewMemory()
	w.evals = evalstore.NewMemory(0, time.Now)
	w.memories = memorystore.NewInMemory(0, time.Now)
	w.treeArchive = treearchive.NewCached(treearchive.NewMemory(), 0)
	w.transcripts = transcriptstore.NewMemory()
	w.partialManifests = partialmanifeststore.NewMemoryStore(nil)
	w.checkpointRetention = checkpointretention.NewMemoryStore(nil)
	return w
}

// TestBuildTenantErasersOrder_spec_12_8 asserts the in-memory gateway's
// Phase 4 sequence names session_tree_archive and every other
// session-keyed store ahead of SessionStore, and that the sequence passes
// the startup order check.
//
// spec: §12.8 (Compliance Interfaces, tenant deletion Phase 4).
func TestBuildTenantErasersOrder_spec_12_8(t *testing.T) {
	w := inMemoryEraserWiring()
	names := tenantEraserNames(w.buildTenantErasers(nil, nil, credentialpoolstore.NewMemory()))
	got := strings.Join(names, ",")
	want := "checkpoint_manifests,checkpoint_retention,transcripts,interactions,memory," +
		"session_tree_archive,eval_results,sessions,credential_pool"
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

	eraser := &tenantEraser{erasers: w.buildTenantErasers(nil, nil, credentialpoolstore.NewMemory())}
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
