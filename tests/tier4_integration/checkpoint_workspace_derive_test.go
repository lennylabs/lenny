// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §10.1 / §15.1
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
//   - A derived or replayed session's first bind on a concurrent-workspace
//     pool restores the copied snapshot through FinalizeWorkspace, so the
//     bound slot's workspace is the source snapshot overlaid with the plan's
//     sources, and a restore that cannot complete fails the start.
//
// spec: §10.1; §15.1; §7.1 derive copy semantics; §7.1 Derive restore
// semantics; §5.1; §5.2.

package tier4_integration_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// spec: §15.1 / §10.1 — the workspace download returns
// the concatenation of the manifest row's chunks in index order.
// diagnosis: the workspace download did not return the manifest chunks
// concatenated in index order, so a derived workspace is corrupt or
// misordered.
func TestWorkspaceDownloadStreamsConcatenatedChunks(t *testing.T) {
	t.Parallel()
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

// spec: §7.1 derive / §10.1.7 — deriving from a multi-chunk parent
// copies every chunk into the derived session's own prefix and writes a
// derived manifest row, so no object is shared with the parent.
//
// diagnosis: a failure here means the derived session shares the parent's
// chunk objects, so a parent-side GC or retention rotation would strip the
// derived session's workspace out from under it.
func TestDeriveCopiesEveryChunkIntoDerivedPrefix(t *testing.T) {
	t.Parallel()
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

	// The derived session's ref is the four-segment object-path form whose
	// last segment is the fresh derived checkpoint id.
	// spec: §10.1 / §4.5.
	derived, err := sessions.Get(ctx, tenant, "sess_derived")
	if err != nil {
		t.Fatalf("get derived session: %v", err)
	}
	const wantDerivedRef = "/acme/checkpoints/sess_derived/ck_derived"
	if derived.WorkspaceSnapshot == nil || derived.WorkspaceSnapshot.Ref != wantDerivedRef {
		t.Fatalf("derived ref = %+v, want %s", derived.WorkspaceSnapshot, wantDerivedRef)
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

// spec: §15.1 (Session Replay Semantics), §7.1 (Derive copy semantics),
// §10.1 — a replay in either mode copies every chunk of the source's
// checkpoint into the replayed session's own prefix with a complete
// derived manifest row, so the replayed session's stored ref names objects
// that exist, and its workspace download reassembles the source's archive.
//
// diagnosis: a failure here means a replayed session's WorkspaceSnapshot
// names no copied object (the synthetic derived-from ref F-7.1.34
// reported), so the replay starts from nothing even though §15.1 states it
// starts from the source's snapshot.
func TestReplayCopiesEveryChunkIntoReplayedPrefix(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"prompt_history", "workspace_derive"} {
		t.Run(mode, func(t *testing.T) {
			const (
				tenant   = "acme"
				parentID = "sess_source"
				parentCk = "ck_parent"
			)
			store := newLiveChunkStore(t)
			mstore := partialmanifeststore.NewMemoryStore(nil)
			archive := seedCheckpoint(t, store, mstore, tenant, parentID, parentCk,
				[]int{2048, 900}, partialmanifeststore.ChunkEncodingTar)
			ctx := context.Background()
			ids := []string{"sess_replay", "ck_replay"}
			handler, sessions := newChunkGateway(t, store, mstore, func() string {
				id := ids[0]
				ids = append(ids[1:], "unused")
				return id
			}, sessionstore.Session{
				ID: parentID, TenantID: tenant, State: session.StateCompleted,
				RuntimeRef: "claude-code", PoolRef: "default-pool", IsolationProfile: isolation.ProfileSandboxed,
				WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: parentCk, Source: sessionstore.WorkspaceSnapshotSealed},
			})

			buf, _ := json.Marshal(map[string]any{"replayMode": mode, "targetRuntime": "claude-code"})
			req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+parentID+"/replay", bytes.NewReader(buf))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Lenny-Tenant-ID", tenant)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusCreated {
				t.Fatalf("replay status = %d, want 201; body=%s", rr.Code, rr.Body.String())
			}
			replayed, err := sessions.Get(ctx, tenant, "sess_replay")
			if err != nil {
				t.Fatalf("get replayed session: %v", err)
			}
			const wantRef = "/acme/checkpoints/sess_replay/ck_replay"
			if replayed.WorkspaceSnapshot == nil || replayed.WorkspaceSnapshot.Ref != wantRef {
				t.Fatalf("replayed ref = %+v, want %s", replayed.WorkspaceSnapshot, wantRef)
			}
			objs, err := store.ListByPrefix(ctx, resumechunkPrefixURI(tenant, "sess_replay", "ck_replay"))
			if err != nil {
				t.Fatalf("list replayed prefix: %v", err)
			}
			if len(objs) != 2 {
				t.Fatalf("replayed chunk objects = %d, want 2", len(objs))
			}
			rrWs := getWorkspace(t, handler, tenant, "sess_replay")
			if rrWs.Code != http.StatusOK || rrWs.Body.String() != string(archive) {
				t.Fatalf("replayed workspace = %d (%d bytes), want 200 with the source archive (%d bytes)",
					rrWs.Code, rrWs.Body.Len(), len(archive))
			}
		})
	}
}

// spec: §10.1 / §4.5 — the stored WorkspaceSnapshot.Ref
// is the four-segment object-path form /{tenant}/checkpoints/{session}/
// {checkpoint_id} the snapshot parsers accept; the workspace download
// resolves the manifest row from its trailing checkpoint_id segment and
// streams the chunk set.
//
// diagnosis: a failure here means the download handler treats the whole
// four-segment ref as a bare checkpoint_id, so the manifest lookup misses,
// the handler falls through to the legacy single-object path, and an
// N-chunk checkpoint cannot be served.
func TestWorkspaceDownloadResolvesFourSegmentRef(t *testing.T) {
	t.Parallel()
	const (
		tenant = "acme"
		sessID = "sess_fs"
		ck     = "ck_fs"
	)
	store := newLiveChunkStore(t)
	mstore := partialmanifeststore.NewMemoryStore(nil)
	archive := seedCheckpoint(t, store, mstore, tenant, sessID, ck,
		[]int{2500, 2500, 640}, partialmanifeststore.ChunkEncodingTarGz)

	fourSegmentRef := "/" + tenant + "/checkpoints/" + sessID + "/" + ck
	handler, _ := newChunkGateway(t, store, mstore, func() string { return "unused" },
		sessionstore.Session{
			ID: sessID, TenantID: tenant, State: session.StateCompleted,
			WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: fourSegmentRef},
		})

	rr := getWorkspace(t, handler, tenant, sessID)
	if rr.Code != http.StatusOK {
		t.Fatalf("workspace status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != string(archive) {
		t.Fatalf("workspace body (%d bytes) does not match concatenated archive (%d bytes)", rr.Body.Len(), len(archive))
	}
}

// spec: §7.1 derive / §10.1.7 — deriving from a parent whose
// WorkspaceSnapshot.Ref is the four-segment object-path form resolves the
// parent manifest from the trailing checkpoint_id segment and copies every
// chunk into the derived session's own prefix.
//
// diagnosis: a failure here means derive treats the whole four-segment
// parent ref as a bare checkpoint_id, misses the parent manifest, and
// answers 503 DERIVE_SNAPSHOT_UNAVAILABLE for a checkpoint that exists.
func TestDeriveFromFourSegmentParentRefCopiesChunks(t *testing.T) {
	t.Parallel()
	const (
		tenant   = "acme"
		parentID = "sess_fs_parent"
		parentCk = "ck_fs_parent"
	)
	store := newLiveChunkStore(t)
	mstore := partialmanifeststore.NewMemoryStore(nil)
	archive := seedCheckpoint(t, store, mstore, tenant, parentID, parentCk,
		[]int{2048, 2048, 512}, partialmanifeststore.ChunkEncodingTar)
	ctx := context.Background()

	fourSegmentRef := "/" + tenant + "/checkpoints/" + parentID + "/" + parentCk
	ids := []string{"sess_fs_derived", "ck_fs_derived"}
	handler, sessions := newChunkGateway(t, store, mstore, func() string {
		id := ids[0]
		ids = ids[1:]
		return id
	}, sessionstore.Session{
		ID: parentID, TenantID: tenant, State: session.StateCompleted,
		RuntimeRef: "claude-code", PoolRef: "default-pool", IsolationProfile: isolation.ProfileSandboxed,
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: fourSegmentRef, Source: sessionstore.WorkspaceSnapshotSealed},
	})

	buf, _ := json.Marshal(map[string]any{})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+parentID+"/derive", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("derive status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	derived, err := sessions.Get(ctx, tenant, "sess_fs_derived")
	if err != nil {
		t.Fatalf("get derived session: %v", err)
	}
	const wantDerivedRef = "/acme/checkpoints/sess_fs_derived/ck_fs_derived"
	if derived.WorkspaceSnapshot == nil || derived.WorkspaceSnapshot.Ref != wantDerivedRef {
		t.Fatalf("derived ref = %+v, want %s", derived.WorkspaceSnapshot, wantDerivedRef)
	}

	derivedObjs, err := store.ListByPrefix(ctx, resumechunkPrefixURI(tenant, "sess_fs_derived", "ck_fs_derived"))
	if err != nil {
		t.Fatalf("list derived prefix: %v", err)
	}
	if len(derivedObjs) != 3 {
		t.Fatalf("derived chunk objects = %d, want 3", len(derivedObjs))
	}

	// The derived session's four-segment ref round-trips through the
	// workspace download and reassembles to the parent's archive.
	rrWs := getWorkspace(t, handler, tenant, "sess_fs_derived")
	if rrWs.Code != http.StatusOK {
		t.Fatalf("derived workspace status = %d, want 200; body=%s", rrWs.Code, rrWs.Body.String())
	}
	if rrWs.Body.String() != string(archive) {
		t.Fatalf("derived workspace bytes do not match the parent archive")
	}
}

// restoreSnapshotFiles is the source session's workspace at its sealed
// checkpoint, and restoreSessionFile is the session-file entry the bundle
// also carries, which a restore must not place in the new workspace.
var (
	restoreSnapshotFiles = map[string]string{
		"a.txt":          "restored a",
		"keep.txt":       "restored keep",
		"sub/nested.txt": "restored nested",
	}
	restoreSessionFile = "sessions/transcript.jsonl"
)

// restorePlanSources is the plan bound at finalize: it overwrites one restored
// path and adds one new path.
var restorePlanSources = map[string]string{
	"a.txt":     "from plan",
	"added.txt": "from plan",
}

// restoreBundle builds the gzip-compressed tar checkpoint bundle the source's
// sealed checkpoint holds: workspace entries under the workspace prefix and
// one session-file entry.
func restoreBundle(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	write := func(name, body string) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	names := make([]string, 0, len(restoreSnapshotFiles))
	for name := range restoreSnapshotFiles {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		write("workspace/"+name, restoreSnapshotFiles[name])
	}
	write(restoreSessionFile, "source session file")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// seedRestoreSource writes the restore bundle as three chunk objects of a
// sealed checkpoint of source and records its complete manifest row.
func seedRestoreSource(t *testing.T, store *miniostore.Store, mstore *partialmanifeststore.MemoryStore, source, checkpointID string) {
	t.Helper()
	raw := restoreBundle(t)
	third := (len(raw) + 2) / 3
	for i := range 3 {
		lo, hi := min(i*third, len(raw)), min((i+1)*third, len(raw))
		putChunk(t, store, lifecycleharness.Tenant, source, checkpointID, uint32(i), partialmanifeststore.ChunkEncodingTarGz, raw[lo:hi])
	}
	seedCompleteManifest(t, mstore, lifecycleharness.Tenant, source, checkpointID, 3, int64(len(raw)), partialmanifeststore.ChunkEncodingTarGz)
}

// restoreFixture is a gateway replica on a concurrent-workspace pool whose
// pods run the in-process adapter, wired to a live MinIO checkpoint store.
type restoreFixture struct {
	h      *lifecycleharness.Harness
	r      *lifecycleharness.Replica
	store  *miniostore.Store
	mstore *partialmanifeststore.MemoryStore
	pod    *lifecycleharness.Pod
}

func newRestoreFixture(t *testing.T) *restoreFixture {
	t.Helper()
	store := newLiveChunkStore(t)
	mstore := partialmanifeststore.NewMemoryStore(nil)
	transport, err := adapter.NewHTTPCheckpointTransport("")
	if err != nil {
		t.Fatalf("object-store transport: %v", err)
	}
	h := lifecycleharness.New(t, lifecycleharness.Options{
		Replicas: 1, Pods: 1, MaxConcurrentSessions: 8,
		ObjectStore: &lifecycleharness.ObjectStore{
			Blobs: store, Manifests: mstore, Resolver: chunkResolver(store, mstore), Transport: transport,
		},
	})
	return &restoreFixture{h: h, r: h.Replicas[0], store: store, mstore: mstore, pod: h.Cluster.Pods()[0]}
}

// seedSource records a completed source session whose sealed snapshot is a
// seeded chunked checkpoint.
func (f *restoreFixture) seedSource(t *testing.T, source, checkpointID string) {
	t.Helper()
	seedRestoreSource(t, f.store, f.mstore, source, checkpointID)
	if err := f.h.Inner.Create(context.Background(), sessionstore.Session{
		ID: source, TenantID: lifecycleharness.Tenant, UserID: "alice@acme.com", State: session.StateCompleted,
		RuntimeRef: lifecycleharness.RuntimeRef, IsolationProfile: isolation.ProfileSandboxed,
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: checkpointID, Source: sessionstore.WorkspaceSnapshotSealed},
	}); err != nil {
		t.Fatalf("seed source session %s: %v", source, err)
	}
}

// copyFrom derives or replays source by the given route and body and returns
// the new session's id.
func (f *restoreFixture) copyFrom(t *testing.T, source, route string, body map[string]any) string {
	t.Helper()
	code, resp := f.h.Do(f.r, http.MethodPost, "/v1/sessions/"+source+"/"+route, body)
	if code != http.StatusCreated {
		t.Fatalf("%s from %s: status %d body %v", route, source, code, resp)
	}
	id, _ := resp["id"].(string)
	if id == "" {
		t.Fatalf("%s from %s answered no session id: %v", route, source, resp)
	}
	f.h.Track(id)
	return id
}

// finalizeAndStart binds the plan and starts the session, returning the
// /start status and body.
func (f *restoreFixture) finalizeAndStart(t *testing.T, id string) (int, map[string]any) {
	t.Helper()
	sources := make([]map[string]any, 0, len(restorePlanSources))
	for path, content := range restorePlanSources {
		sources = append(sources, map[string]any{"type": "inlineFile", "path": path, "content": content})
	}
	code, resp := f.h.Do(f.r, http.MethodPost, "/v1/sessions/"+id+"/finalize", map[string]any{
		"workspacePlan": map[string]any{"schemaVersion": 1, "sources": sources},
	})
	if code != http.StatusOK {
		t.Fatalf("finalize %s: status %d body %v", id, code, resp)
	}
	return f.h.Do(f.r, http.MethodPost, "/v1/sessions/"+id+"/start", nil)
}

// slotTree reads the session's promoted slot workspace as relative path to
// content.
func (f *restoreFixture) slotTree(t *testing.T, id string) map[string]string {
	t.Helper()
	current := filepath.Join(f.pod.Adapter.WorkspaceBase, "slots", id, "current")
	tree := map[string]string{}
	err := filepath.WalkDir(current, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(current, path)
		tree[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("read slot workspace of %s: %v", id, err)
	}
	return tree
}

// wantRestoredTree is the source snapshot overlaid with the plan's sources.
func wantRestoredTree() map[string]string {
	want := map[string]string{}
	for k, v := range restoreSnapshotFiles {
		want[k] = v
	}
	for k, v := range restorePlanSources {
		want[k] = v
	}
	return want
}

// A derived session and a session replayed in either mode start on a
// concurrent-workspace pool with the source snapshot overlaid by the plan's
// sources, read from the derived session's own copy: deleting every chunk
// object of the source session before /start changes nothing, and the
// bundle's session-file tree never reaches the workspace. Against the code
// that carried no restore on FinalizeWorkspace, the slot holds only the
// plan's sources.
//
// spec: §7.1 (Derive restore semantics); §7.1 (Derive copy semantics); §15.1
// (Session Replay Semantics); §5.1; §5.2; §14.1
// diagnosis: a derived or replayed session started without its source snapshot, restored it from the source session's objects instead of its own copy, restored the session-file tree, or let the snapshot overwrite a plan source. Check workspaceRestore in pkg/gateway/sessionserver, mintRestoreChunks in pkg/gateway/podlifecycle/podsession, and restoreSeed in pkg/adapter.
func TestDerivedAndReplayedSessionsStartFromTheirRestoredSnapshot_spec_7_1(t *testing.T) {
	f := newRestoreFixture(t)
	cases := []struct {
		name          string
		route         string
		body          map[string]any
		deleteSources bool
	}{
		{name: "derive", route: "derive", body: map[string]any{}},
		{name: "derive after the source chunks are deleted", route: "derive", body: map[string]any{}, deleteSources: true},
		{
			name: "replay prompt_history after the source chunks are deleted", route: "replay",
			body: map[string]any{"replayMode": "prompt_history", "targetRuntime": lifecycleharness.RuntimeRef}, deleteSources: true,
		},
		{
			name: "replay workspace_derive after the source chunks are deleted", route: "replay",
			body: map[string]any{"replayMode": "workspace_derive", "targetRuntime": lifecycleharness.RuntimeRef}, deleteSources: true,
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := fmt.Sprintf("sess_restore_source_%d", i)
			f.seedSource(t, source, fmt.Sprintf("ck_restore_%d", i))
			id := f.copyFrom(t, source, tc.route, tc.body)
			if tc.deleteSources {
				if _, err := f.store.DeleteBySession(context.Background(), lifecycleharness.Tenant, source); err != nil {
					t.Fatalf("delete the source chunk objects: %v", err)
				}
			}

			code, resp := f.finalizeAndStart(t, id)
			if code != http.StatusOK {
				t.Fatalf("start %s: status %d body %v", id, code, resp)
			}
			if st := f.h.Row(id).State; st != session.StateRunning {
				t.Errorf("session state after start = %q, want running", st)
			}
			got, want := f.slotTree(t, id), wantRestoredTree()
			if len(got) != len(want) {
				t.Errorf("slot workspace = %v, want %v", got, want)
			}
			for path, body := range want {
				if got[path] != body {
					t.Errorf("%s = %q, want %q", path, got[path], body)
				}
			}
			if _, ok := got["transcript.jsonl"]; ok {
				t.Error("the source session-file tree reached the workspace")
			}
		})
	}
}

// A derived chunk overwritten in place with the same number of bytes that are
// not a gzip stream passes the gateway's chunk resolver, so the restore fails
// inside the adapter. Every attempt fails, the §5.2 slot retry runs a second
// FinalizeWorkspace with freshly minted grants, and /start answers 422
// SLOT_FAILED with the transient category and leaves no started session.
//
// spec: §7.1 (Derive restore semantics); §5.2; §4.7.1
// diagnosis: a restore that cannot complete started the session with a different workspace, was classified as a permanent slot failure and skipped the retry, or answered a status other than 422 SLOT_FAILED. Check the errRestore arm of FinalizeWorkspace in pkg/adapter and applySlotRetryPolicy in pkg/gateway/sessionserver.
func TestCorruptDerivedChunkFailsTheStartAfterTheSlotRetry_spec_7_1(t *testing.T) {
	f := newRestoreFixture(t)
	const source, checkpointID = "sess_restore_corrupt_source", "ck_restore_corrupt"
	f.seedSource(t, source, checkpointID)
	id := f.copyFrom(t, source, "derive", map[string]any{})

	ref := f.h.Row(id).WorkspaceSnapshot
	if ref == nil || ref.Ref == "" {
		t.Fatalf("derived session %s has no snapshot ref", id)
	}
	derivedCk := filepath.Base(ref.Ref)
	objs, err := f.store.ListByPrefix(context.Background(), resumechunkPrefixURI(lifecycleharness.Tenant, id, derivedCk))
	if err != nil || len(objs) != 3 {
		t.Fatalf("derived chunk objects = %d, %v; want 3", len(objs), err)
	}
	first := objs[0]
	for _, o := range objs[1:] {
		if o.URI.PartID < first.URI.PartID {
			first = o
		}
	}
	junk := bytes.Repeat([]byte{'x'}, int(first.Size))
	putChunk(t, f.store, lifecycleharness.Tenant, id, derivedCk, 0, partialmanifeststore.ChunkEncodingTarGz, junk)

	finalizesBefore := f.pod.UnaryCalls("FinalizeWorkspace")
	code, resp := f.finalizeAndStart(t, id)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("start status = %d, want 422; body %v", code, resp)
	}
	errBody, _ := resp["error"].(map[string]any)
	if errBody["code"] != "SLOT_FAILED" {
		t.Errorf("error.code = %v, want SLOT_FAILED", errBody["code"])
	}
	details, _ := errBody["details"].(map[string]any)
	if details["category"] != "transient" {
		t.Errorf("error.details.category = %v, want transient", details["category"])
	}
	if n := f.pod.UnaryCalls("FinalizeWorkspace") - finalizesBefore; n != 2 {
		t.Errorf("FinalizeWorkspace attempts = %d, want 2 (the first attempt and the §5.2 slot retry)", n)
	}
	if st := f.h.Row(id).State; st == session.StateRunning || st == session.StateStarting {
		t.Errorf("session state after the failed start = %q, want not started", st)
	}
	if slices.Contains(f.pod.Runtime.Started(), id) {
		t.Error("the runtime started the session after its restore failed")
	}
	if _, serr := os.Stat(filepath.Join(f.pod.Adapter.WorkspaceBase, "slots", id, "current")); !os.IsNotExist(serr) {
		t.Errorf("a workspace was promoted for the failed session: %v", serr)
	}
}
