// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §4.4 checkpoint-atomicity contract and
// the checkpoint-to-MinIO-then-resume round-trip, driven against real
// MinIO and real Postgres containers rather than in-memory fakes.
//
// The spec makes a checkpoint an atomic unit: the metadata record on the
// session row is written only after the workspace-and-session-file
// bundle is durably uploaded to MinIO, and a checkpoint whose upload
// fails is discarded with no metadata ever recorded. Existing coverage
// pins this ordering only with in-memory stubs (the checkpointer unit
// test's stubSink, the adapter unit test's fakeCheckpointSink); no test
// exercised the real MinIO upload and the real Postgres metadata write
// together, nor the resume that reads the bundle back from MinIO onto a
// fresh pod.
//
// The test composes real product surfaces end to end: the pod adapter's
// Checkpoint/Resume RPCs (pkg/adapter), the MinIO-backed §4.5 blob store
// (pkg/blobstore/miniostore) reached through the adapter's CheckpointSink
// seam, the gateway checkpointer that records the WorkspaceSnapshot
// (pkg/gateway/checkpoint/checkpointer), and the Postgres-backed session
// store (pkg/gateway/session/sessionstore/pgstore) with the production
// migrations applied. Only the thin CheckpointSink/Source bridge over the
// blob store is test-local; the ordering guarantee under test lives
// entirely in the product code.
//
// This crosses the gateway, the datastore (the Postgres session row),
// the artifact store (real MinIO), and the pod adapter, which is the
// multi-service surface the integration tier owns.

package tier4_integration_test

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// minioCheckpointBridge adapts a §4.5 blobstore.Store to the adapter's
// CheckpointSink and CheckpointSource seams. SaveCheckpoint streams the
// checkpoint bundle into a MinIO object keyed under the checkpoint
// object-type prefix and returns the canonical lenny-blob URI as the
// checkpoint id; LoadCheckpoint reads that same object back for a resume.
// This is the only test-local piece; the atomicity ordering it exercises
// is enforced by the adapter and the gateway checkpointer.
type minioCheckpointBridge struct {
	store  blobstore.Store
	tenant string
}

func (b *minioCheckpointBridge) SaveCheckpoint(_ context.Context, sessionID string, r io.Reader) (string, error) {
	u := blobstore.URI{
		TenantID:   b.tenant,
		ObjectType: blobstore.ObjectTypeCheckpoint,
		SessionID:  sessionID,
		PartID:     blobstore.NewPartID(),
		TTL:        time.Hour,
		Encoding:   blobstore.Encoding,
	}
	return b.store.Put(u, "application/gzip", r)
}

func (b *minioCheckpointBridge) LoadCheckpoint(_ context.Context, checkpointID string) (io.ReadCloser, error) {
	u, err := blobstore.ParseURI(checkpointID)
	if err != nil {
		return nil, err
	}
	_, rc, err := b.store.Get(u)
	return rc, err
}

// dialCheckpointAdapter serves srv over an in-memory connection and
// returns a connected adapter client, so the gateway checkpointer drives
// the real Checkpoint RPC path rather than calling the Server directly.
func dialCheckpointAdapter(t *testing.T, srv *adapter.Server) *adapterclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// seedRunningSession seeds store with one running session that carries no
// workspace snapshot yet, so the checkpoint under test is the first to
// record one.
func seedRunningSession(t *testing.T, store sessionstore.Store, tenantID, sessionID string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID:               sessionID,
		TenantID:         tenantID,
		UserID:           "alice@acme.com",
		State:            session.StateRunning,
		RuntimeRef:       "echo",
		IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt:        now,
		UpdatedAt:        now,
	}); err != nil {
		t.Fatalf("seed running session: %v", err)
	}
}

// spec: §4.4 (spec/04_system-components.md, Event / Checkpoint Store) —
// "A checkpoint is an atomic unit comprising a workspace snapshot (tar of
// /workspace/current), a session file snapshot (copy of /sessions/
// contents), and a checkpoint metadata record ... The metadata record in
// Postgres references both artifacts and is written only after both are
// successfully uploaded to MinIO."
//
// diagnosis: a failure means the §4.4 checkpoint-to-MinIO write is not
// atomic against the real stack. If the WorkspaceSnapshot row is set but
// the referenced object is absent from MinIO, the gateway recorded
// metadata before (or without) the upload. If the object is present but
// the row is unset, the metadata write did not follow a successful
// upload. If the resume cannot reconstruct the workspace and session-file
// trees from the referenced object, the bundle uploaded to MinIO was not
// the durable checkpoint the metadata claims.
func TestCheckpointWritesMetadataOnlyAfterMinIOUpload(t *testing.T) {
	mio := containers.StartMinIO(t, containers.MinIOOptions{})
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	ctx := context.Background()
	const tenant = "acme"
	seedTenant(t, pg, tenant)

	store := sessionpg.New(pg.Pool)
	blob, err := miniostore.New(miniostore.Config{
		Endpoint:  mio.Endpoint,
		AccessKey: mio.AccessKey,
		SecretKey: mio.SecretKey,
		Bucket:    mio.Bucket,
		UseSSL:    false,
	})
	if err != nil {
		t.Fatalf("miniostore.New: %v", err)
	}
	bridge := &minioCheckpointBridge{store: blob, tenant: tenant}

	// The adapter checkpoints a real workspace tree AND a real /sessions
	// tree, so the bundle carries both §4.4 artifacts.
	const (
		wsFile     = "notes.txt"
		wsContent  = "agent workspace state"
		sesFile    = ".session.json"
		sesContent = `{"id":"sess","turns":3}`
	)
	srv := adapter.New("ckpt-atomicity")
	srv.WorkspaceRoot = t.TempDir()
	srv.SessionsRoot = t.TempDir()
	srv.Runtime = &eagerRuntime{}
	srv.Checkpoints = bridge
	if err := os.WriteFile(filepath.Join(srv.WorkspaceRoot, wsFile), []byte(wsContent), 0o644); err != nil {
		t.Fatalf("seed workspace file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srv.SessionsRoot, sesFile), []byte(sesContent), 0o644); err != nil {
		t.Fatalf("seed session file: %v", err)
	}

	sid := newSessionUUID(t)
	client := dialCheckpointAdapter(t, srv)
	if err := client.StartSession(ctx, adapterclient.StartSessionParams{SessionID: sid, Runtime: "echo"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: sid, TenantID: tenant, Adapter: client})
	seedRunningSession(t, store, tenant, sid)

	cp := &checkpointer.Checkpointer{Sessions: store, Registry: registry}

	// Baseline: the running session carries no workspace snapshot.
	if row, err := store.Get(ctx, tenant, sid); err != nil {
		t.Fatalf("Get baseline: %v", err)
	} else if row.WorkspaceSnapshot != nil {
		t.Fatalf("baseline WorkspaceSnapshot = %+v, want nil", row.WorkspaceSnapshot)
	}

	if err := cp.Checkpoint(ctx, tenant, sid); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	// The metadata record was written and names a checkpoint ref.
	row, err := store.Get(ctx, tenant, sid)
	if err != nil {
		t.Fatalf("Get after checkpoint: %v", err)
	}
	if row.WorkspaceSnapshot == nil {
		t.Fatal("after checkpoint, WorkspaceSnapshot is nil; the metadata record was not written")
	}
	if row.WorkspaceSnapshot.Source != sessionstore.WorkspaceSnapshotCheckpoint {
		t.Errorf("snapshot source = %q, want %q", row.WorkspaceSnapshot.Source, sessionstore.WorkspaceSnapshotCheckpoint)
	}
	ref := row.WorkspaceSnapshot.Ref
	if ref == "" {
		t.Fatal("WorkspaceSnapshot.Ref is empty; the metadata does not reference a MinIO object")
	}

	// The referenced artifact is durably in MinIO: read it back and
	// confirm the bundle carries both the workspace and the session file.
	// This is the §4.4 "written only after both are successfully uploaded"
	// guarantee — the metadata the row points at resolves to a real object.
	rc, err := bridge.LoadCheckpoint(ctx, ref)
	if err != nil {
		t.Fatalf("the checkpoint the metadata references is not in MinIO: %v", err)
	}
	wsOut := t.TempDir()
	sesOut := t.TempDir()
	if _, err := workspace.ExtractTree([]workspace.NamedRoot{
		{Prefix: workspace.WorkspacePrefix, Root: wsOut},
		{Prefix: workspace.SessionsPrefix, Root: sesOut},
	}, rc); err != nil {
		_ = rc.Close()
		t.Fatalf("extract checkpoint bundle from MinIO: %v", err)
	}
	_ = rc.Close()
	if got, err := os.ReadFile(filepath.Join(wsOut, wsFile)); err != nil || string(got) != wsContent {
		t.Fatalf("workspace artifact in MinIO = %q (err %v), want %q", got, err, wsContent)
	}
	if got, err := os.ReadFile(filepath.Join(sesOut, sesFile)); err != nil || string(got) != sesContent {
		t.Fatalf("session-file artifact in MinIO = %q (err %v), want %q", got, err, sesContent)
	}

	// The journey's second half: resume on a fresh pod restores the
	// workspace and the session file from the MinIO checkpoint.
	fresh := adapter.New("ckpt-resume")
	fresh.WorkspaceRoot = t.TempDir()
	fresh.SessionsRoot = t.TempDir()
	fresh.Runtime = &eagerRuntime{}
	fresh.Restorer = bridge
	resp, err := fresh.Resume(ctx, &adapterv1.ResumeRequest{
		SessionId:    &adapterv1.SessionId{Value: sid},
		Runtime:      "echo",
		CheckpointId: ref,
	})
	if err != nil {
		t.Fatalf("Resume on fresh pod: %v", err)
	}
	if resp.GetRestoredBytes() != int64(len(wsContent)+len(sesContent)) {
		t.Errorf("restored bytes = %d, want %d (workspace + session file)",
			resp.GetRestoredBytes(), len(wsContent)+len(sesContent))
	}
	if got, err := os.ReadFile(filepath.Join(fresh.WorkspaceRoot, wsFile)); err != nil || string(got) != wsContent {
		t.Fatalf("resumed workspace file = %q (err %v), want %q", got, err, wsContent)
	}
	if got, err := os.ReadFile(filepath.Join(fresh.SessionsRoot, sesFile)); err != nil || string(got) != sesContent {
		t.Fatalf("resumed session file = %q (err %v), want %q", got, err, sesContent)
	}
}

// spec: §4.4 (spec/04_system-components.md, Event / Checkpoint Store) —
// "If either snapshot fails, the entire checkpoint is discarded — partial
// checkpoints are never stored as valid checkpoints. The metadata record
// in Postgres ... is written only after both are successfully uploaded to
// MinIO."
//
// diagnosis: a failure means the checkpoint write is not fail-closed
// against a real MinIO upload failure. If the WorkspaceSnapshot row is
// set after the upload failed, the gateway recorded a metadata record for
// a checkpoint whose bytes never reached MinIO — a partial checkpoint
// masquerading as a valid one, exactly what §4.4 forbids.
func TestCheckpointDiscardedWhenMinIOUploadFails(t *testing.T) {
	mio := containers.StartMinIO(t, containers.MinIOOptions{})
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	ctx := context.Background()
	const tenant = "acme"
	seedTenant(t, pg, tenant)

	store := sessionpg.New(pg.Pool)
	// Point the sink at a bucket that does not exist on the running MinIO
	// so the real PutObject fails with a terminal NoSuchBucket error (not a
	// transport-class error, so the store fails fast without retrying).
	badBlob, err := miniostore.New(miniostore.Config{
		Endpoint:  mio.Endpoint,
		AccessKey: mio.AccessKey,
		SecretKey: mio.SecretKey,
		Bucket:    "no-such-checkpoint-bucket",
		UseSSL:    false,
	})
	if err != nil {
		t.Fatalf("miniostore.New: %v", err)
	}
	bridge := &minioCheckpointBridge{store: badBlob, tenant: tenant}

	srv := adapter.New("ckpt-discard")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &eagerRuntime{}
	srv.Checkpoints = bridge
	if err := os.WriteFile(filepath.Join(srv.WorkspaceRoot, "notes.txt"), []byte("state"), 0o644); err != nil {
		t.Fatalf("seed workspace file: %v", err)
	}

	sid := newSessionUUID(t)
	client := dialCheckpointAdapter(t, srv)
	if err := client.StartSession(ctx, adapterclient.StartSessionParams{SessionID: sid, Runtime: "echo"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: sid, TenantID: tenant, Adapter: client})
	seedRunningSession(t, store, tenant, sid)

	cp := &checkpointer.Checkpointer{Sessions: store, Registry: registry}
	if err := cp.Checkpoint(ctx, tenant, sid); err == nil {
		t.Fatal("Checkpoint succeeded despite the MinIO upload failing; want an error")
	}

	// The checkpoint was discarded: no metadata record was written, so a
	// subsequent recovery cannot resolve a checkpoint whose bytes are absent.
	row, err := store.Get(ctx, tenant, sid)
	if err != nil {
		t.Fatalf("Get after failed checkpoint: %v", err)
	}
	if row.WorkspaceSnapshot != nil {
		t.Errorf("after a failed MinIO upload, WorkspaceSnapshot = %+v, want nil (checkpoint must be discarded)",
			row.WorkspaceSnapshot)
	}
}
