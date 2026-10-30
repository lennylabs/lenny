// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// cleanupCall is one CleanupAfterResume invocation a recordingCleaner saw.
type cleanupCall struct {
	tenantID, sessionID string
	resumedGen          int64
}

// recordingCleaner is a PartialManifestCleaner that records its arguments.
type recordingCleaner struct {
	mu    sync.Mutex
	calls []cleanupCall
}

func (c *recordingCleaner) CleanupAfterResume(_ context.Context, tenantID, sessionID string, resumedGen int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, cleanupCall{tenantID: tenantID, sessionID: sessionID, resumedGen: resumedGen})
	return nil
}

// TestResumeCleanupScopedToCommittedGeneration_spec_10_1_7 drives a resume
// through its commit and asserts that the partial-manifest cleanup runs once,
// for the resumed session's tenant and id, scoped to the committed resume
// row's coordination generation. An unbounded generation would delete a live
// seal or checkpoint intent row written by the resumed incarnation, and a zero
// generation would leave every abandoned attempt's manifest in place.
//
// spec: §10.1.7 (Cleanup.), §7.3 (Resume driver)
func TestResumeCleanupScopedToCommittedGeneration_spec_10_1_7(t *testing.T) {
	const committedGen = 7
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s", TenantID: "acme", RuntimeRef: "echo", State: session.StateResumePending,
		CoordinationGeneration: committedGen,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	listed, err := store.Get(context.Background(), "acme", "s")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	cleaner := &recordingCleaner{}
	srv := New(store, Options{DevTenantHeader: true, Events: sessionevents.NewBus(64), PartialManifestCleaner: cleaner})
	defer srv.Close()

	if exit := srv.completeResume(listed, 0, resumeOutcome{}); exit != resumeExitCommitLanded {
		t.Fatalf("exit = %v, want %v", exit, resumeExitCommitLanded)
	}
	committed, err := store.Get(context.Background(), "acme", "s")
	if err != nil {
		t.Fatalf("get committed: %v", err)
	}
	if committed.State != session.StateRunning || committed.CoordinationGeneration != committedGen {
		t.Fatalf("committed row state %q gen %d, want %q gen %d", committed.State, committed.CoordinationGeneration, session.StateRunning, committedGen)
	}
	cleaner.mu.Lock()
	defer cleaner.mu.Unlock()
	want := cleanupCall{tenantID: "acme", sessionID: "s", resumedGen: committedGen}
	if len(cleaner.calls) != 1 || cleaner.calls[0] != want {
		t.Fatalf("CleanupAfterResume calls = %+v, want exactly [%+v]", cleaner.calls, want)
	}
}

// TestResumeNotLandedRunsNoCleanup_spec_10_1_7 asserts that a commit that does
// not land runs no partial-manifest cleanup, so a resume that lost its commit
// deletes nothing.
//
// spec: §10.1.7 (Cleanup.), §7.3 (Resume driver)
func TestResumeNotLandedRunsNoCleanup_spec_10_1_7(t *testing.T) {
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s", TenantID: "acme", RuntimeRef: "echo", State: session.StateRunning, CoordinationGeneration: 3,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	listed, _ := store.Get(context.Background(), "acme", "s")
	cleaner := &recordingCleaner{}
	srv := New(store, Options{DevTenantHeader: true, Events: sessionevents.NewBus(64), PartialManifestCleaner: cleaner})
	defer srv.Close()

	if exit := srv.completeResume(listed, 0, resumeOutcome{}); exit != resumeExitCommitNotLanded {
		t.Fatalf("exit = %v, want %v", exit, resumeExitCommitNotLanded)
	}
	cleaner.mu.Lock()
	defer cleaner.mu.Unlock()
	if len(cleaner.calls) != 0 {
		t.Fatalf("CleanupAfterResume calls = %+v, want none", cleaner.calls)
	}
}
