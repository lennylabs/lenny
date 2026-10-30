// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 cases for the scope of the GET capabilities a derived or replayed
// session's first bind delivers to the pod adapter on FinalizeWorkspace. The
// gateway keys the restore on the session's own tenant, its own session id,
// and the checkpoint id its snapshot ref names, so every capability names an
// object under the session's own chunk prefix in its own tenant. A snapshot
// ref that names another session's checkpoint, in the same tenant or in
// another one, resolves no chunk under the session's prefix, and a derived
// chunk set with a missing index is not contiguous; each fails the bind
// closed with no capability minted. The capabilities are bearer URLs and
// reach no log line and no error string the gateway returns.
//
// Each case drives a gateway replica of the lifecycle harness on a
// concurrent-workspace pool, whose pods run the in-process adapter, against a
// live MinIO object store holding the source session's checkpoint, a
// co-tenant's checkpoint, and a second tenant's checkpoint. A recording
// object-store transport on the pod adapter observes every capability the
// adapter fetches, which is exactly the set FinalizeWorkspace carried.
//
// spec: §7.1 (Derive restore semantics); §13.1; §13.2.2; §10.1 (Reassembly on
// resume).
package tier9_security_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// restoreScopeEncoding is the chunk encoding every seeded checkpoint uses.
const restoreScopeEncoding = partialmanifeststore.ChunkEncodingTarGz

// restoreScopeChunks is the number of chunks every seeded checkpoint holds.
const restoreScopeChunks = 3

// recordingObjectTransport is the pod adapter's object-store transport with a
// record of every capability URL the adapter fetched.
type recordingObjectTransport struct {
	inner adapter.CheckpointTransport
	mu    sync.Mutex
	urls  []string
}

func (r *recordingObjectTransport) PutChunk(ctx context.Context, u string, headers map[string]string, n int64, body io.Reader) (int, string, error) {
	return r.inner.PutChunk(ctx, u, headers, n, body)
}

func (r *recordingObjectTransport) GetChunk(ctx context.Context, u string, headers map[string]string) (io.ReadCloser, error) {
	r.mu.Lock()
	r.urls = append(r.urls, u)
	r.mu.Unlock()
	return r.inner.GetChunk(ctx, u, headers)
}

func (r *recordingObjectTransport) fetched() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.urls...)
}

// restoreScopeFixture is the gateway replica, its pod, and the object store
// the cases share.
type restoreScopeFixture struct {
	h         *lifecycleharness.Harness
	r         *lifecycleharness.Replica
	store     *miniostore.Store
	mstore    *partialmanifeststore.MemoryStore
	transport *recordingObjectTransport
	logs      *lockedBuffer
	// bodies collects every gateway answer the cases read, so the bearer
	// check covers each error string the gateway returned.
	bodies []string
}

// lockedBuffer is a log sink safe for the gateway's concurrent writers.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newRestoreScopeFixture(t *testing.T) *restoreScopeFixture {
	t.Helper()
	mio := containers.StartMinIO(t, containers.MinIOOptions{})
	store, err := miniostore.New(miniostore.Config{
		Endpoint: mio.Endpoint, AccessKey: mio.AccessKey, SecretKey: mio.SecretKey, Bucket: mio.Bucket,
	})
	if err != nil {
		t.Fatalf("miniostore.New: %v", err)
	}
	mstore := partialmanifeststore.NewMemoryStore(nil)
	inner, err := adapter.NewHTTPCheckpointTransport("")
	if err != nil {
		t.Fatalf("object-store transport: %v", err)
	}
	transport := &recordingObjectTransport{inner: inner}
	logs := &lockedBuffer{}
	prev := log.Writer()
	log.SetOutput(io.MultiWriter(prev, logs))
	t.Cleanup(func() { log.SetOutput(prev) })
	// Every failed bind costs the pod two slot failures (the first attempt
	// and the §5.2 retry); the slot bound keeps the whole-pod replacement
	// threshold above the failures the cases cause.
	h := lifecycleharness.New(t, lifecycleharness.Options{
		Replicas: 1, Pods: 1, MaxConcurrentSessions: 16,
		ObjectStore: &lifecycleharness.ObjectStore{
			Blobs: store, Manifests: mstore, Transport: transport,
			Resolver: &resumechunks.Resolver{
				Manifests: mstore, Presigner: store, Lister: store,
				TTL: time.Minute, PartialRecoveryThresholdFraction: 0.5,
			},
		},
	})
	return &restoreScopeFixture{h: h, r: h.Replicas[0], store: store, mstore: mstore, transport: transport, logs: logs}
}

// seedCheckpoint writes a gzip-compressed tar bundle as chunk objects of
// tenant/session/checkpointID and records its complete manifest row.
func (f *restoreScopeFixture) seedCheckpoint(t *testing.T, tenant, sessionID, checkpointID, marker string) {
	t.Helper()
	raw := restoreScopeBundle(t, marker)
	part := (len(raw) + restoreScopeChunks - 1) / restoreScopeChunks
	for i := range restoreScopeChunks {
		lo, hi := min(i*part, len(raw)), min((i+1)*part, len(raw))
		u := resumechunks.ChunkObjectURI(tenant, sessionID, checkpointID, uint32(i), restoreScopeEncoding)
		u.TTL = time.Hour
		if _, err := f.store.Put(u, "application/octet-stream", bytes.NewReader(raw[lo:hi])); err != nil {
			t.Fatalf("put chunk %d of %s/%s: %v", i, tenant, checkpointID, err)
		}
	}
	ctx := context.Background()
	now := time.Now().UTC()
	if err := f.mstore.Put(ctx, partialmanifeststore.Record{
		TenantID: tenant, CheckpointID: checkpointID, SessionID: sessionID,
		ChunkObjectKeyPrefix: fmt.Sprintf("/%s/checkpoints/%s/%s/", tenant, sessionID, checkpointID),
		ChunkSizeBytes:       16 << 20, ChunkEncoding: restoreScopeEncoding,
		CheckpointStartedAt: now, CheckpointTimeoutAt: now.Add(90 * time.Second),
	}); err != nil {
		t.Fatalf("manifest Put %s: %v", checkpointID, err)
	}
	if err := f.mstore.ConfirmChunk(ctx, tenant, checkpointID, restoreScopeChunks-1, int64(len(raw))); err != nil {
		t.Fatalf("manifest ConfirmChunk %s: %v", checkpointID, err)
	}
	if err := f.mstore.Finalise(ctx, tenant, checkpointID, false, partialmanifeststore.ReasonComplete); err != nil {
		t.Fatalf("manifest Finalise %s: %v", checkpointID, err)
	}
}

// restoreScopeBundle is a checkpoint bundle whose workspace holds one file
// naming its owner.
func restoreScopeBundle(t *testing.T, marker string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := strings.Repeat(marker+"\n", 64)
	if err := tw.WriteHeader(&tar.Header{Name: "workspace/owner.txt", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// seedSource records the completed source session in the harness tenant.
func (f *restoreScopeFixture) seedSource(t *testing.T, id, checkpointID string) {
	t.Helper()
	f.seedCheckpoint(t, lifecycleharness.Tenant, id, checkpointID, id)
	if err := f.h.Inner.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: lifecycleharness.Tenant, UserID: "alice@acme.com", State: session.StateCompleted,
		RuntimeRef: lifecycleharness.RuntimeRef, IsolationProfile: isolation.ProfileSandboxed,
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: checkpointID, Source: sessionstore.WorkspaceSnapshotSealed},
	}); err != nil {
		t.Fatalf("seed source %s: %v", id, err)
	}
}

// do sends a request and keeps the answer for the bearer check.
func (f *restoreScopeFixture) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	code, resp := f.h.Do(f.r, method, path, body)
	raw, _ := json.Marshal(resp)
	f.bodies = append(f.bodies, string(raw))
	return code, resp
}

// copyFrom derives or replays source and returns the new session's id.
func (f *restoreScopeFixture) copyFrom(t *testing.T, source, route string, body map[string]any) string {
	t.Helper()
	code, resp := f.do(t, http.MethodPost, "/v1/sessions/"+source+"/"+route, body)
	id, _ := resp["id"].(string)
	if code != http.StatusCreated || id == "" {
		t.Fatalf("%s from %s: status %d body %v", route, source, code, resp)
	}
	f.h.Track(id)
	return id
}

// finalizeAndStart binds an empty plan and starts the session, returning the
// /start status.
func (f *restoreScopeFixture) finalizeAndStart(t *testing.T, id string) int {
	t.Helper()
	code, resp := f.do(t, http.MethodPost, "/v1/sessions/"+id+"/finalize", map[string]any{
		"workspacePlan": map[string]any{"schemaVersion": 1, "sources": []any{}},
	})
	if code != http.StatusOK {
		t.Fatalf("finalize %s: status %d body %v", id, code, resp)
	}
	code, _ = f.do(t, http.MethodPost, "/v1/sessions/"+id+"/start", nil)
	return code
}

// derivedCheckpoint is the checkpoint id the session's own snapshot ref names.
func (f *restoreScopeFixture) derivedCheckpoint(t *testing.T, id string) string {
	t.Helper()
	ref := f.h.Row(id).WorkspaceSnapshot
	if ref == nil || ref.Ref == "" {
		t.Fatalf("session %s has no snapshot ref", id)
	}
	return ref.Ref[strings.LastIndex(ref.Ref, "/")+1:]
}

// fetchesSince returns the capabilities the adapter fetched after the first n.
func (f *restoreScopeFixture) fetchesSince(n int) []string {
	return f.transport.fetched()[n:]
}

// requireNotStarted asserts the session did not reach running.
func (f *restoreScopeFixture) requireNotStarted(t *testing.T, id string, code int) {
	t.Helper()
	if code == http.StatusOK {
		t.Errorf("start of %s answered 200, want the bind to fail closed", id)
	}
	if st := f.h.Row(id).State; st == session.StateRunning || st == session.StateStarting {
		t.Errorf("session %s state = %q after a failed restore, want not started", id, st)
	}
}

// requireOwnPrefix asserts the capabilities name, in index order, exactly the
// chunk objects under tenant/session/checkpointID.
func requireOwnPrefix(t *testing.T, urls []string, tenant, sessionID, checkpointID string) {
	t.Helper()
	if len(urls) != restoreScopeChunks {
		t.Fatalf("adapter fetched %d capabilities, want %d: %v", len(urls), restoreScopeChunks, redactAll(urls))
	}
	for i, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("capability %d is not a URL: %v", i, err)
		}
		want := fmt.Sprintf("/%s/checkpoints/%s/%s/chunk-%05d.%s", tenant, sessionID, checkpointID, i, restoreScopeEncoding)
		if !strings.HasSuffix(u.Path, want) {
			t.Errorf("capability %d names %s, want an object ending %s", i, u.Path, want)
		}
		if u.Query().Get("X-Amz-Signature") == "" {
			t.Errorf("capability %d carries no signature, so it is not a presigned GET", i)
		}
	}
}

// redactAll drops the query of each URL, so a failure message never prints a
// signature.
func redactAll(urls []string) []string {
	out := make([]string, 0, len(urls))
	for _, raw := range urls {
		if i := strings.IndexByte(raw, '?'); i >= 0 {
			raw = raw[:i]
		}
		out = append(out, raw)
	}
	return out
}

// requireNoBearerLeak asserts no capability URL and no signature reached a
// gateway log line or a gateway answer.
func (f *restoreScopeFixture) requireNoBearerLeak(t *testing.T) {
	t.Helper()
	logs := f.logs.String()
	answers := strings.Join(f.bodies, "\n")
	for _, raw := range f.transport.fetched() {
		u, _ := url.Parse(raw)
		sig := u.Query().Get("X-Amz-Signature")
		if strings.Contains(logs, raw) || (sig != "" && strings.Contains(logs, sig)) {
			t.Errorf("a presigned GET for %s reached a log line", u.Path)
		}
		if strings.Contains(answers, raw) || (sig != "" && strings.Contains(answers, sig)) {
			t.Errorf("a presigned GET for %s reached a gateway answer", u.Path)
		}
	}
	for _, sink := range []struct{ name, text string }{{"log line", logs}, {"gateway answer", answers}} {
		if strings.Contains(sink.text, "X-Amz-Signature") || strings.Contains(sink.text, "X-Amz-Credential") {
			t.Errorf("a presigned query parameter reached a %s", sink.name)
		}
	}
}

// TestRestoreGrantsNameOnlyTheSessionsOwnPrefix_spec_7_1 derives and replays
// one source session while a co-tenant session and a session of a second
// tenant hold checkpoints of their own, and pins that each new session's
// FinalizeWorkspace carried capabilities for its own chunk prefix in its own
// tenant and nothing else, none of which reached a log line or an answer.
//
// spec: §7.1 (Derive restore semantics); §13.1; §13.2.2
// diagnosis: a derived or replayed session's restore minted a capability for an object outside its own prefix, such as the source session's or another tenant's chunks, or a presigned URL was written to a log line or an error string. Check workspaceRestore in pkg/gateway/sessionserver and the per-index presign in pkg/gateway/checkpoint/resumechunks.
func TestRestoreGrantsNameOnlyTheSessionsOwnPrefix_spec_7_1(t *testing.T) {
	f := newRestoreScopeFixture(t)
	f.seedSource(t, "sess_scope_source", "ck_scope_source")
	f.seedCheckpoint(t, lifecycleharness.Tenant, "sess_scope_cotenant", "ck_scope_cotenant", "co-tenant")
	f.seedCheckpoint(t, "globex", "sess_scope_globex", "ck_scope_globex", "second tenant")

	for _, tc := range []struct {
		name, route string
		body        map[string]any
	}{
		{name: "derive", route: "derive", body: map[string]any{}},
		{name: "replay", route: "replay", body: map[string]any{"replayMode": "workspace_derive", "targetRuntime": lifecycleharness.RuntimeRef}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := f.copyFrom(t, "sess_scope_source", tc.route, tc.body)
			before := len(f.transport.fetched())
			if code := f.finalizeAndStart(t, id); code != http.StatusOK {
				t.Fatalf("start %s: status %d", id, code)
			}
			requireOwnPrefix(t, f.fetchesSince(before), lifecycleharness.Tenant, id, f.derivedCheckpoint(t, id))
		})
	}
	f.requireNoBearerLeak(t)
}

// TestForgedOrMisorderedRestoreFailsClosed_spec_7_1 points a derived
// session's snapshot ref at a co-tenant's checkpoint and at a second tenant's
// checkpoint, and removes one index from a derived chunk set. Each start
// fails with no capability delivered to the adapter, the session does not
// start, and nothing the gateway logged or answered carries a presigned URL.
//
// spec: §7.1 (Derive restore semantics); §13.1; §13.2.2; §10.1 (Reassembly on
// resume)
// diagnosis: a forged or cross-tenant snapshot ref minted capabilities for another session's or another tenant's chunks, a chunk set with a missing index was restored partially, or a failed restore leaked a presigned URL. Check workspaceRestore in pkg/gateway/sessionserver and the contiguity check in pkg/gateway/checkpoint/resumechunks.
func TestForgedOrMisorderedRestoreFailsClosed_spec_7_1(t *testing.T) {
	f := newRestoreScopeFixture(t)
	f.seedSource(t, "sess_forge_source", "ck_forge_source")
	f.seedCheckpoint(t, lifecycleharness.Tenant, "sess_forge_cotenant", "ck_forge_cotenant", "co-tenant")
	f.seedCheckpoint(t, "globex", "sess_forge_globex", "ck_forge_globex", "second tenant")

	for _, tc := range []struct {
		name   string
		tamper func(t *testing.T, id string)
	}{
		{name: "ref forged to a co-tenant checkpoint", tamper: func(t *testing.T, id string) {
			f.forgeRef(t, id, "/"+lifecycleharness.Tenant+"/checkpoints/sess_forge_cotenant/ck_forge_cotenant")
		}},
		{name: "ref forged to a second tenant checkpoint", tamper: func(t *testing.T, id string) {
			f.forgeRef(t, id, "/globex/checkpoints/sess_forge_globex/ck_forge_globex")
		}},
		{name: "derived chunk set with a missing index", tamper: func(t *testing.T, id string) {
			gap := resumechunks.ChunkObjectURI(lifecycleharness.Tenant, id, f.derivedCheckpoint(t, id), 1, restoreScopeEncoding)
			if err := f.store.HardDeleteObject(gap); err != nil {
				t.Fatalf("delete derived chunk 1: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := f.copyFrom(t, "sess_forge_source", "derive", map[string]any{})
			tc.tamper(t, id)
			before := len(f.transport.fetched())
			code := f.finalizeAndStart(t, id)
			f.requireNotStarted(t, id, code)
			if got := f.fetchesSince(before); len(got) != 0 {
				t.Errorf("adapter fetched %d capabilities for a restore that must mint none: %v", len(got), redactAll(got))
			}
		})
	}
	f.requireNoBearerLeak(t)
}

// forgeRef rewrites a derived session's snapshot ref before its first bind.
func (f *restoreScopeFixture) forgeRef(t *testing.T, id, ref string) {
	t.Helper()
	if _, err := f.h.Inner.Update(context.Background(), lifecycleharness.Tenant, id, func(s *sessionstore.Session) error {
		s.WorkspaceSnapshot.Ref = ref
		return nil
	}); err != nil {
		t.Fatalf("forge the snapshot ref of %s: %v", id, err)
	}
}
