// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §10.1 line 155 / §15.1 line 671
// chunked workspace download and the §7.1 derive chunk-copy, driven against
// a live MinIO container.
//
//   - GET /v1/sessions/{id}/workspace resolves the manifest row and streams
//     the checkpoint's chunks in ascending index order as one continuous
//     archive; the gateway reads the bodies itself because the caller is the
//     API client.
//   - POST /v1/sessions/{id}/derive copies every chunk under the parent's
//     chunk_object_key_prefix into the derived session's own prefix and
//     writes a derived manifest row, so no object is shared with the parent
//     and the derived session survives the parent's GC.
//
// spec: §10.1 line 155; §15.1 line 671; §7.1 derive copy semantics.

package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// spec: §15.1 line 671 / §10.1 line 155 — the workspace download returns
// the concatenation of the manifest row's chunks in index order.
func TestWorkspaceDownloadStreamsConcatenatedChunks(t *testing.T) {
	const (
		tenant = "acme"
		sessID = "sess_ws"
		ck     = "ck_ws"
	)
	store := newLiveChunkStore(t)
	mstore := partialmanifeststore.NewMemoryStore(nil)
	archive := seedCheckpoint(t, store, mstore, tenant, sessID, ck,
		[]int{3000, 3000, 900}, partialmanifeststore.ChunkEncodingTarGz)

	handler, _ := newChunkGateway(t, store, mstore, func() string { return "unused" },
		sessionstore.Session{
			ID: sessID, TenantID: tenant, State: session.StateCompleted,
			WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: ck},
		})

	rr := getWorkspace(t, handler, tenant, sessID)
	if rr.Code != http.StatusOK {
		t.Fatalf("workspace status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != string(archive) {
		t.Fatalf("workspace body (%d bytes) does not match concatenated archive (%d bytes)", rr.Body.Len(), len(archive))
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("Content-Type = %q, want application/gzip", ct)
	}
}

// spec: §7.1 derive / §10.1 line 155 — deriving from a multi-chunk parent
// copies every chunk into the derived session's own prefix and writes a
// derived manifest row, so no object is shared with the parent.
//
// diagnosis: a failure here means the derived session shares the parent's
// chunk objects, so a parent-side GC or retention rotation would strip the
// derived session's workspace out from under it.
func TestDeriveCopiesEveryChunkIntoDerivedPrefix(t *testing.T) {
	const (
		tenant   = "acme"
		parentID = "sess_source"
		parentCk = "ck_parent"
	)
	store := newLiveChunkStore(t)
	mstore := partialmanifeststore.NewMemoryStore(nil)
	archive := seedCheckpoint(t, store, mstore, tenant, parentID, parentCk,
		[]int{2048, 2048, 700}, partialmanifeststore.ChunkEncodingTar)
	ctx := context.Background()

	// idFn returns the derived session id first, then the derived checkpoint id.
	ids := []string{"sess_derived", "ck_derived"}
	handler, sessions := newChunkGateway(t, store, mstore, func() string {
		id := ids[0]
		ids = ids[1:]
		return id
	}, sessionstore.Session{
		ID: parentID, TenantID: tenant, State: session.StateCompleted,
		RuntimeRef: "claude-code", PoolRef: "default-pool", IsolationProfile: isolation.ProfileSandboxed,
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: parentCk, Source: sessionstore.WorkspaceSnapshotSealed},
	})

	// POST /derive.
	buf, _ := json.Marshal(map[string]any{})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+parentID+"/derive", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("derive status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	// The derived session's ref names a fresh checkpoint id.
	derived, err := sessions.Get(ctx, tenant, "sess_derived")
	if err != nil {
		t.Fatalf("get derived session: %v", err)
	}
	if derived.WorkspaceSnapshot == nil || derived.WorkspaceSnapshot.Ref != "ck_derived" {
		t.Fatalf("derived ref = %+v, want ck_derived", derived.WorkspaceSnapshot)
	}

	// Every parent chunk is copied into the derived session's prefix.
	derivedObjs, err := store.ListByPrefix(ctx, resumechunkPrefixURI(tenant, "sess_derived", "ck_derived"))
	if err != nil {
		t.Fatalf("list derived prefix: %v", err)
	}
	if len(derivedObjs) != 3 {
		t.Fatalf("derived chunk objects = %d, want 3", len(derivedObjs))
	}
	// The parent's chunks are untouched (no object moved out from under it).
	parentObjs, err := store.ListByPrefix(ctx, resumechunkPrefixURI(tenant, parentID, parentCk))
	if err != nil {
		t.Fatalf("list parent prefix: %v", err)
	}
	if len(parentObjs) != 3 {
		t.Fatalf("parent chunk objects = %d, want 3 (untouched)", len(parentObjs))
	}
	// No object is shared: the derived and parent object keys are disjoint.
	parentKeys := map[string]bool{}
	for _, o := range parentObjs {
		parentKeys[o.URI.SessionID+"/"+o.URI.PartID] = true
	}
	for _, o := range derivedObjs {
		if parentKeys[o.URI.SessionID+"/"+o.URI.PartID] {
			t.Fatalf("derived object %q is shared with the parent", o.URI.PartID)
		}
	}

	// The derived manifest row is complete and reassembles to the same bytes
	// the parent held (the copy preserved every chunk).
	rec, err := mstore.Get(ctx, tenant, "ck_derived")
	if err != nil {
		t.Fatalf("get derived manifest: %v", err)
	}
	if rec.Partial || rec.ChunkCount != 3 {
		t.Fatalf("derived manifest: partial=%v chunk_count=%d, want partial=false chunk_count=3", rec.Partial, rec.ChunkCount)
	}
	rrWs := getWorkspace(t, handler, tenant, "sess_derived")
	if rrWs.Code != http.StatusOK {
		t.Fatalf("derived workspace status = %d, want 200; body=%s", rrWs.Code, rrWs.Body.String())
	}
	if rrWs.Body.String() != string(archive) {
		t.Fatalf("derived workspace bytes do not match the parent archive")
	}
}
