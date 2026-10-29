// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the tree membership of a derived session.
// POST /v1/sessions/{id}/derive runs against a live MinIO chunk store, and the
// §8.10 orphan-cleanup sweeper runs on a short interval over the same session
// store. A derived session is the root of its own delegation tree: orphan
// cleanup never selects it, whatever the state of the source's tree, and the
// source's cancel_all cascade never reaches it.
//
// spec: §12.6 (StoreRouter), §8.10 (Orphan cleanup), §15.1

package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// deriveVia posts a derive of source and returns the derived session's id.
func deriveVia(t *testing.T, h http.Handler, tenant, source string, allowStale bool) string {
	t.Helper()
	buf, _ := json.Marshal(sessionserver.DeriveRequest{AllowStale: allowStale})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+source+"/derive", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("derive %s: status %d, body %s", source, rr.Code, rr.Body.String())
	}
	var resp sessionserver.DeriveResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || resp.ID == "" {
		t.Fatalf("derive %s: decode response %s: %v", source, rr.Body.String(), err)
	}
	return resp.ID
}

// TestDerivedSessionIsOutsideTheSourceTree_spec_12_6 derives one session from
// a completed delegated source whose tree root ended an hour before the
// sweeper's one-second cascade window, and one from a live cancel_all root
// with allowStale. The sweeper runs at least two passes: the first derived
// session stays non-terminal while a live delegated sibling of the source is
// expired, which shows the passes ran. Terminating the live source runs its
// cancel_all cascade, which leaves the second derived session non-terminal.
//
// spec: §12.6 (StoreRouter), §8.10 (Orphan cleanup), §15.1 (Session Replay Semantics)
// diagnosis: a failure means derive wrote the source as the derived session's
// delegation parent, so orphan cleanup expires a derived session once the
// source's tree root is past cascadeTimeoutSeconds, or the source's cancel_all
// cascade cancels a session the client derived from it.
func TestDerivedSessionIsOutsideTheSourceTree_spec_12_6(t *testing.T) {
	const tenant = "acme"
	ctx := context.Background()
	chunks := newLiveChunkStore(t)
	manifests := partialmanifeststore.NewMemoryStore(nil)
	seedCheckpoint(t, chunks, manifests, tenant, "sess_src_done", "ck_done", []int{1024, 512}, partialmanifeststore.ChunkEncodingTar)
	seedCheckpoint(t, chunks, manifests, tenant, "sess_src_live", "ck_live", []int{1024}, partialmanifeststore.ChunkEncodingTar)

	clock := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	sessions := memstore.New()
	var idMu sync.Mutex
	nextID := 0
	srv := sessionserver.New(sessions, sessionserver.Options{
		DevTenantHeader:          true,
		Blobs:                    chunks,
		CheckpointManifestReader: manifests,
		CheckpointManifestWriter: manifests,
		IDFunc: func() string {
			idMu.Lock()
			defer idMu.Unlock()
			nextID++
			return fmt.Sprintf("gen_%03d", nextID)
		},
		Clock: func() time.Time { return clock },
	})
	base := sessionstore.Session{
		TenantID: tenant, RuntimeRef: "claude-code", PoolRef: "default-pool",
		IsolationProfile: isolation.ProfileSandboxed, CreatedAt: clock, UpdatedAt: clock,
	}
	seed := func(mut func(*sessionstore.Session)) {
		row := base
		mut(&row)
		if err := sessions.Create(ctx, row); err != nil {
			t.Fatalf("seed %s: %v", row.ID, err)
		}
	}
	seed(func(r *sessionstore.Session) {
		r.ID, r.State, r.TerminatedAt = "sess_old_root", session.StateCompleted, clock.Add(-time.Hour)
		r.CascadeOnFailure = session.CascadeDetach
	})
	seed(func(r *sessionstore.Session) {
		r.ID, r.State, r.TerminatedAt = "sess_src_done", session.StateCompleted, clock.Add(-30*time.Minute)
		r.ParentSessionID, r.RootSessionID, r.DelegationDepth = "sess_old_root", "sess_old_root", 1
		r.WorkspaceSnapshot = &sessionstore.WorkspaceSnapshot{Ref: "ck_done", Source: sessionstore.WorkspaceSnapshotSealed}
	})
	seed(func(r *sessionstore.Session) {
		r.ID, r.State = "sess_sibling", session.StateRunning
		r.ParentSessionID, r.RootSessionID, r.DelegationDepth = "sess_old_root", "sess_old_root", 1
	})
	seed(func(r *sessionstore.Session) {
		r.ID, r.State, r.CascadeOnFailure = "sess_src_live", session.StateRunning, session.CascadeCancelAll
		r.WorkspaceSnapshot = &sessionstore.WorkspaceSnapshot{Ref: "ck_live", Source: sessionstore.WorkspaceSnapshotCheckpoint}
	})

	handler := srv.Handler()
	fromDone := deriveVia(t, handler, tenant, "sess_src_done", false)
	fromLive := deriveVia(t, handler, tenant, "sess_src_live", true)

	// Two orphan-cleanup intervals with a cascade window far shorter than the
	// hour since the source tree's root ended.
	sweeper := orphancleanup.New(sessions, orphancleanup.StaticTenants{tenant}, orphancleanup.Options{
		CascadeTimeout: time.Second,
		Interval:       50 * time.Millisecond,
		Terminal:       srv,
	})
	runCtx, stop := context.WithCancel(ctx)
	ticks := make(chan struct{}, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		sweeper.Run(runCtx, func(int, error) {
			select {
			case ticks <- struct{}{}:
			default:
			}
		})
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-ticks:
		case <-time.After(10 * time.Second):
			t.Fatalf("orphan cleanup ran %d passes within 10s, want 2", i)
		}
	}
	stop()
	<-done
	if err := srv.WaitTerminalWork(ctx); err != nil {
		t.Fatalf("WaitTerminalWork: %v", err)
	}

	if got := mustState(t, sessions, tenant, "sess_sibling"); got != session.StateExpired {
		t.Fatalf("control orphan sess_sibling state = %q, want expired (the sweeper passes did not run)", got)
	}
	if got := mustState(t, sessions, tenant, fromDone); session.IsTerminal(got) {
		t.Fatalf("session derived from a terminal source state = %q after two orphan-cleanup passes, want non-terminal", got)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_src_live/terminate", nil)
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("terminate live source: status %d, body %s", rr.Code, rr.Body.String())
	}
	if err := srv.WaitTerminalWork(ctx); err != nil {
		t.Fatalf("WaitTerminalWork: %v", err)
	}
	if got := mustState(t, sessions, tenant, "sess_src_live"); !session.IsTerminal(got) {
		t.Fatalf("live source state = %q after terminate, want terminal", got)
	}
	if got := mustState(t, sessions, tenant, fromLive); session.IsTerminal(got) {
		t.Fatalf("session derived from the live source state = %q after the source's cancel_all cascade, want non-terminal", got)
	}
}

func mustState(t *testing.T, store sessionstore.Store, tenant, id string) session.State {
	t.Helper()
	row, err := store.Get(context.Background(), tenant, id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return row.State
}
