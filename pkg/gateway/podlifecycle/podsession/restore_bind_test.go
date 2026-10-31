// SPDX-License-Identifier: MIT

package podsession_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// snapshotTransport is an adapter.CheckpointTransport that serves one
// starting-snapshot bundle per URL and records each fetch.
type snapshotTransport struct {
	mu      sync.Mutex
	bodies  map[string][]byte
	fetched []string
}

func (s *snapshotTransport) PutChunk(context.Context, string, map[string]string, int64, io.Reader) (int, string, error) {
	return 0, "", errors.New("snapshotTransport: PutChunk is not used by a restore")
}

func (s *snapshotTransport) GetChunk(_ context.Context, url string, _ map[string]string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fetched = append(s.fetched, url)
	body, ok := s.bodies[url]
	if !ok {
		return nil, fmt.Errorf("snapshotTransport: no chunk at %q", url)
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (s *snapshotTransport) fetchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.fetched)
}

// snapshotBundle builds a gzip-compressed tar checkpoint bundle from
// prefixed regular files, in the layout the derive copy writes.
func snapshotBundle(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(body))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %q: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("write body %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// restoreClosure returns a RestoreChunks closure that hands out grants and
// counts its calls.
func restoreClosure(grants []adapterclient.ChunkGrant, err error) (func(context.Context) ([]adapterclient.ChunkGrant, error), *int) {
	calls := 0
	return func(context.Context) ([]adapterclient.ChunkGrant, error) {
		calls++
		if err != nil {
			return nil, err
		}
		return grants, nil
	}, &calls
}

// A derived or replayed session's exclusive-pool bind invokes the restore
// closure once, carries its grants on FinalizeWorkspace, and promotes a
// workspace that is the snapshot's workspace tree overlaid with the plan's
// sources. Against the code that passed no restore on FinalizeWorkspace the
// restored file is absent from current and the transport is never fetched.
//
// spec: §7.1 (Derive restore semantics); §5.1
// diagnosis: a derived or replayed session would start with only the plan sources, or with the source session files, instead of the source snapshot overlaid by the plan.
func TestBindRestoresStartingSnapshotUnderPlanSources_spec_7_1(t *testing.T) {
	ns := testNamespace(t)
	transport := &snapshotTransport{bodies: map[string][]byte{
		"https://objstore.example/chunk-0": snapshotBundle(t, map[string]string{
			"workspace/restored.txt":    "from snapshot",
			"workspace/overwritten.txt": "from snapshot",
			"sessions/transcript.jsonl": "source session file",
			"workspace/sub/nested.txt":  "nested from snapshot",
		}),
	}}
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	root := t.TempDir()
	srv.WorkspaceBase = root
	srv.Runtime = rt
	srv.CheckpointTransport = transport

	c := k8sClient(t, idleSandbox(ns, "sbx-1", "10.244.1.7"))
	binder := newBinder(ns, c, adapterDialer(t, srv))
	restore, calls := restoreClosure([]adapterclient.ChunkGrant{
		{Index: 0, URL: "https://objstore.example/chunk-0"},
	}, nil)

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Plan: &adapterv1.WorkspacePlan{Sources: []*adapterv1.WorkspaceSource{
			{Type: "inlineFile", Path: "overwritten.txt", Content: "from source"},
		}},
		RestoreChunks: restore,
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer res.Adapter.Close()

	if *calls != 1 {
		t.Errorf("RestoreChunks called %d times, want exactly once per bind attempt", *calls)
	}
	if n := transport.fetchCount(); n != 1 {
		t.Errorf("adapter fetched %d chunks, want 1", n)
	}
	current := filepath.Join(root, "slots", "sess-1", "current")
	want := map[string]string{
		"restored.txt":    "from snapshot",
		"overwritten.txt": "from source",
		"sub/nested.txt":  "nested from snapshot",
	}
	for rel, body := range want {
		got, err := os.ReadFile(filepath.Join(current, rel))
		if err != nil {
			t.Errorf("read %s: %v", rel, err)
			continue
		}
		if string(got) != body {
			t.Errorf("%s = %q, want %q", rel, got, body)
		}
	}
	if _, err := os.Stat(filepath.Join(current, "transcript.jsonl")); !os.IsNotExist(err) {
		t.Errorf("session-file tree leaked into the workspace: stat err = %v", err)
	}
}

// A restore closure that fails fails the bind with no fallback: Bind
// returns the wrapped closure error, reclaims the claimed pod, never
// promotes a workspace, and never starts the runtime.
//
// spec: §7.1 (Derive restore semantics); §6.2 (pod state machine)
// diagnosis: a restore that cannot be minted would start the session with a different workspace or strand the claimed pod.
func TestBindFailsClosedWhenRestoreCannotBeMinted_spec_7_1(t *testing.T) {
	ns := testNamespace(t)
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	root := t.TempDir()
	srv.WorkspaceBase = root
	srv.Runtime = rt

	c := k8sClient(t, idleSandbox(ns, "sbx-1", "10.244.1.7"))
	binder := newBinder(ns, c, adapterDialer(t, srv))
	mintErr := errors.New("derived manifest has no chunk")
	restore, calls := restoreClosure(nil, mintErr)

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Plan:          inlinePlan("a.txt"),
		RestoreChunks: restore,
	})
	if !errors.Is(err, mintErr) {
		t.Fatalf("Bind error = %v, want it to wrap %v", err, mintErr)
	}
	if *calls != 1 {
		t.Errorf("RestoreChunks called %d times, want 1", *calls)
	}
	if rt.started != "" {
		t.Errorf("runtime started for %q after a failed restore", rt.started)
	}
	if _, serr := os.Stat(filepath.Join(root, "slots", "sess-1", "current")); !os.IsNotExist(serr) {
		t.Errorf("current was promoted after a failed restore: stat err = %v", serr)
	}
	if claimPresent(t, c) {
		t.Error("claimed pod was not reclaimed after a failed restore")
	}
}

// A preConnect session that starts from a restored snapshot is demoted when
// the runtime declares any sdkWarmBlockingPaths, even though the plan places
// no matching path, because the restored paths are not known before
// materialization. The launch-only recomputation agrees. Against the
// path-only decision the pod stays SDK-warm and ConfigureWorkspace runs.
//
// spec: §6.1; §7.1 (Derive restore semantics)
// diagnosis: a restored preConnect session would keep a pre-connected SDK that never saw the restored blocking files.
func TestBindSDKWarmDemotesARestoredSession_spec_6_1(t *testing.T) {
	ns := testNamespace(t)
	transport := &snapshotTransport{bodies: map[string][]byte{
		"https://objstore.example/chunk-0": snapshotBundle(t, map[string]string{"workspace/CLAUDE.md": "restored"}),
	}}
	rt := &fakeSDKWarmRuntime{}
	srv := adapter.New("adapter-test")
	srv.WorkspaceBase = t.TempDir()
	srv.Runtime = rt
	srv.CheckpointTransport = transport

	c := k8sClient(t, idleSandbox(ns, "sbx-1", "10.244.1.7"))
	binder := newBinder(ns, c, adapterDialer(t, srv))
	restore, _ := restoreClosure([]adapterclient.ChunkGrant{{Index: 0, URL: "https://objstore.example/chunk-0"}}, nil)
	req := podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Plan:                 inlinePlan("src/main.go"),
		PreConnect:           true,
		SDKWarmBlockingPaths: []string{"CLAUDE.md"},
		RestoreChunks:        restore,
	}
	if !podsession.RequiresDemotion(req) {
		t.Error("RequiresDemotion = false for a restored preConnect session with blocking paths")
	}

	res, err := binder.Bind(context.Background(), req)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer res.Adapter.Close()

	if rt.demoted != 1 {
		t.Errorf("DemoteSDK called %d times, want 1", rt.demoted)
	}
	if len(rt.configured) != 0 {
		t.Errorf("ConfigureWorkspace ran on a demoted pod: %v", rt.configured)
	}
	if rt.started != "sess-1" {
		t.Errorf("demoted pod must StartSession pod-warm, started = %q", rt.started)
	}
}

// The restore demotion clause is part of the pure decision both Prepare and
// the launch-only /start recomputation read.
//
// spec: §6.1; §7.1 (Derive restore semantics)
func TestRequiresDemotionForRestoredSnapshot_spec_6_1(t *testing.T) {
	restore, calls := restoreClosure(nil, nil)
	cases := []struct {
		name string
		req  podsession.BindRequest
		want bool
	}{
		{
			name: "restore with non-empty blocking paths demotes",
			req:  podsession.BindRequest{PreConnect: true, RestoreChunks: restore, SDKWarmBlockingPaths: []string{"CLAUDE.md"}},
			want: true,
		},
		{
			name: "restore with no blocking paths stays SDK-warm",
			req:  podsession.BindRequest{PreConnect: true, RestoreChunks: restore},
			want: false,
		},
		{
			name: "restore on a non-preConnect pod never demotes",
			req:  podsession.BindRequest{RestoreChunks: restore, SDKWarmBlockingPaths: []string{"CLAUDE.md"}},
			want: false,
		},
		{
			name: "no restore and no matching path stays SDK-warm",
			req:  podsession.BindRequest{PreConnect: true, Plan: inlinePlan("src/main.go"), SDKWarmBlockingPaths: []string{"CLAUDE.md"}},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := podsession.RequiresDemotion(tc.req); got != tc.want {
				t.Errorf("RequiresDemotion = %v, want %v", got, tc.want)
			}
		})
	}
	if *calls != 0 {
		t.Errorf("RequiresDemotion invoked the restore closure %d times; the decision must not mint grants", *calls)
	}
}

// A slot bind invokes the restore closure once and carries its grants on the
// slot's FinalizeWorkspace, in order.
//
// spec: §7.1 (Derive restore semantics); §5.2
// diagnosis: a derived or replayed session on a concurrent-session pool would start without its source snapshot.
func TestBindSlotCarriesRestoreGrantsOnFinalize_spec_7_1(t *testing.T) {
	ns := testNamespace(t)
	a := newConcurrentAdapter()
	c := k8sClient(t, concurrentIdleSandbox(ns, "sbx-1", "10.244.1.7"))
	binder := newSlotBinder(t, c, concurrentAdapterDialer(t, a))
	restore, calls := restoreClosure([]adapterclient.ChunkGrant{
		{Index: 0, URL: "https://objstore.example/chunk-0"},
		{Index: 1, URL: "https://objstore.example/chunk-1"},
	}, nil)

	res, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		MaxConcurrentSessions: 8,
		Plan:                  &adapterv1.WorkspacePlan{},
		RestoreChunks:         restore,
	})
	if err != nil {
		t.Fatalf("BindSlot: %v", err)
	}
	defer res.Adapter.Close()

	if *calls != 1 {
		t.Errorf("RestoreChunks called %d times, want 1", *calls)
	}
	a.mu.Lock()
	got := fmt.Sprint(a.restoreURLs["sess-1"])
	a.mu.Unlock()
	if want := fmt.Sprint([]string{"https://objstore.example/chunk-0", "https://objstore.example/chunk-1"}); got != want {
		t.Errorf("FinalizeWorkspace restore_chunks URLs = %s, want %s", got, want)
	}
}

// A slot bind whose restore closure fails records the workspace_finalize
// slot failure, returns a SlotBindError at the workspace_prep stage that
// classifies as transient, wraps the closure error, and never reaches
// FinalizeWorkspace.
//
// spec: §7.1 (Derive restore semantics); §5.2; §16.1
// diagnosis: a slot restore that cannot be minted would start the slot without its snapshot or be miscounted at the slot failure metric.
func TestBindSlotFailsClosedWhenRestoreCannotBeMinted_spec_7_1(t *testing.T) {
	ns := testNamespace(t)
	a := newConcurrentAdapter()
	c := k8sClient(t, concurrentIdleSandbox(ns, "sbx-1", "10.244.1.7"))
	binder := newSlotBinder(t, c, concurrentAdapterDialer(t, a))
	var failures []slotFailureCall
	binder.SlotFailure = func(errorType, pool, podName string) {
		failures = append(failures, slotFailureCall{errorType, pool, podName})
	}
	mintErr := errors.New("capability cannot be signed")
	restore, _ := restoreClosure(nil, mintErr)

	_, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		MaxConcurrentSessions: 8,
		Plan:                  &adapterv1.WorkspacePlan{},
		RestoreChunks:         restore,
	})
	if !errors.Is(err, mintErr) {
		t.Fatalf("BindSlot error = %v, want it to wrap %v", err, mintErr)
	}
	want := slotFailureCall{podsession.SlotFailureWorkspaceFinalizeForTest, testPool, "sbx-1"}
	if len(failures) != 1 || failures[0] != want {
		t.Errorf("slot failures = %+v, want [%+v]", failures, want)
	}
	var sbe *podsession.SlotBindError
	if !errors.As(err, &sbe) {
		t.Fatalf("BindSlot error = %v, want *SlotBindError", err)
	}
	if sbe.Stage != podsession.SlotFailureWorkspacePrepForTest {
		t.Errorf("SlotBindError.Stage = %q, want %q", sbe.Stage, podsession.SlotFailureWorkspacePrepForTest)
	}
	if sbe.Reason() != podsession.SlotReasonTransient {
		t.Errorf("SlotBindError.Reason = %q, want %q", sbe.Reason(), podsession.SlotReasonTransient)
	}
	if len(a.finalizedSet()) != 0 {
		t.Errorf("finalized = %v, want none", a.finalizedSet())
	}
}

// restoreEventLog records, in order, the bind-sequence events a restore test
// asserts on: the PrepareWorkspace stream completing, each restore closure
// invocation, and each FinalizeWorkspace the adapter serves.
type restoreEventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *restoreEventLog) add(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *restoreEventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

// restoreRecordingAdapter is the concurrent-adapter fake with a recording
// PrepareWorkspace and FinalizeWorkspace. It logs both RPCs into the shared
// event log, keeps each FinalizeWorkspace's restore_chunks URLs in arrival
// order, and fails the first FinalizeWorkspace when failFirstFinalize is set
// so a test can drive a retried bind. It serves the exclusive and the slot
// bind paths alike.
type restoreRecordingAdapter struct {
	*concurrentAdapter
	log               *restoreEventLog
	failFirstFinalize bool

	mu        sync.Mutex
	finalizes int
	restores  [][]string
}

func newRestoreRecordingAdapter(log *restoreEventLog, failFirstFinalize bool) *restoreRecordingAdapter {
	return &restoreRecordingAdapter{concurrentAdapter: newConcurrentAdapter(), log: log, failFirstFinalize: failFirstFinalize}
}

func (a *restoreRecordingAdapter) PrepareWorkspace(stream grpc.ClientStreamingServer[adapterv1.PrepareWorkspaceRequest, adapterv1.PrepareWorkspaceResponse]) error {
	err := a.concurrentAdapter.PrepareWorkspace(stream)
	a.log.add("PrepareWorkspace")
	return err
}

func (a *restoreRecordingAdapter) FinalizeWorkspace(ctx context.Context, req *adapterv1.FinalizeWorkspaceRequest) (*adapterv1.FinalizeWorkspaceResponse, error) {
	a.log.add("FinalizeWorkspace")
	urls := []string{}
	for _, g := range req.GetRestoreChunks() {
		urls = append(urls, fmt.Sprintf("%d=%s", g.GetIndex(), g.GetUrl()))
	}
	a.mu.Lock()
	a.finalizes++
	fail := a.failFirstFinalize && a.finalizes == 1
	a.restores = append(a.restores, urls)
	a.mu.Unlock()
	if fail {
		return nil, status.Error(codes.Internal, "first finalize fails so the bind is retried")
	}
	return a.concurrentAdapter.FinalizeWorkspace(ctx, req)
}

func (a *restoreRecordingAdapter) recordedRestores() [][]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([][]string(nil), a.restores...)
}

// restoreRecordingDialer serves a restoreRecordingAdapter over an in-memory
// connection and returns a DialAdapter func wired to it.
func restoreRecordingDialer(t *testing.T, a *restoreRecordingAdapter) func(string) (*adapterclient.Client, error) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, a)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return func(string) (*adapterclient.Client, error) {
		return adapterclient.Dial("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
}

// perAttemptRestore returns a restore closure that mints a distinct grant set
// on every call, logs each call into the event log, and reports the grants it
// minted per call in the "index=url" form restoreRecordingAdapter records.
func perAttemptRestore(log *restoreEventLog) (func(context.Context) ([]adapterclient.ChunkGrant, error), func() [][]string) {
	var mu sync.Mutex
	var minted [][]string
	restore := func(context.Context) ([]adapterclient.ChunkGrant, error) {
		mu.Lock()
		defer mu.Unlock()
		attempt := len(minted) + 1
		log.add(fmt.Sprintf("restore#%d", attempt))
		var grants []adapterclient.ChunkGrant
		var urls []string
		for i := range 3 {
			url := fmt.Sprintf("https://objstore.example/attempt-%d/chunk-%d", attempt, i)
			grants = append(grants, adapterclient.ChunkGrant{Index: uint32(i), URL: url})
			urls = append(urls, fmt.Sprintf("%d=%s", i, url))
		}
		minted = append(minted, urls)
		return grants, nil
	}
	return restore, func() [][]string {
		mu.Lock()
		defer mu.Unlock()
		return append([][]string(nil), minted...)
	}
}

// restoreBindPath drives one bind attempt on one of the two Binder sites that
// issue a bind-time FinalizeWorkspace: Prepare (through Bind on an exclusive
// pool) and materializeSlotStages (through BindSlot on a concurrent-workspace
// pool). Each attempt carries an uploadFile source so stageWorkspace sends a
// PrepareWorkspace the event log observes.
type restoreBindPath struct {
	name string
	bind func(t *testing.T, a *restoreRecordingAdapter, restore func(context.Context) ([]adapterclient.ChunkGrant, error)) (attempt func() error)
}

func restoreBindPaths() []restoreBindPath {
	return []restoreBindPath{
		{name: "exclusive Prepare", bind: exclusiveRestoreAttempt},
		{name: "slot materializeSlotStages", bind: slotRestoreAttempt},
	}
}

// uploadPlan returns a blob store holding one upload and the plan that
// names it.
func uploadPlan(t *testing.T) (*blobstore.MemoryStore, *adapterv1.WorkspacePlan) {
	t.Helper()
	blobs := blobstore.NewMemoryStore(nil)
	uri := blobstore.URI{TenantID: "acme", SessionID: "sess-1", PartID: "part-1", TTL: time.Hour, Encoding: blobstore.Encoding}
	if _, err := blobs.Put(uri, "application/octet-stream", bytes.NewReader([]byte("uploaded"))); err != nil {
		t.Fatalf("put blob: %v", err)
	}
	return blobs, &adapterv1.WorkspacePlan{SchemaVersion: 1, Sources: []*adapterv1.WorkspaceSource{
		{Type: "uploadFile", Path: "data/payload.bin", UploadRef: uri.String()},
	}}
}

func exclusiveRestoreAttempt(t *testing.T, a *restoreRecordingAdapter, restore func(context.Context) ([]adapterclient.ChunkGrant, error)) func() error {
	ns := testNamespace(t)
	c := k8sClient(t, idleSandbox(ns, "sbx-1", "10.244.1.7"), idleSandbox(ns, "sbx-2", "10.244.1.8"))
	binder := newBinder(ns, c, restoreRecordingDialer(t, a))
	blobs, plan := uploadPlan(t)
	binder.Blobs = blobs
	return func() error {
		res, err := binder.Bind(context.Background(), podsession.BindRequest{
			Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
			Plan: plan, RestoreChunks: restore,
		})
		if err == nil {
			res.Adapter.Close()
		}
		return err
	}
}

func slotRestoreAttempt(t *testing.T, a *restoreRecordingAdapter, restore func(context.Context) ([]adapterclient.ChunkGrant, error)) func() error {
	ns := testNamespace(t)
	c := k8sClient(t, concurrentIdleSandbox(ns, "sbx-1", "10.244.1.7"))
	binder := newSlotBinder(t, c, restoreRecordingDialer(t, a))
	blobs, plan := uploadPlan(t)
	binder.Blobs = blobs
	return func() error {
		res, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
			Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
			MaxConcurrentSessions: 8, Plan: plan, RestoreChunks: restore,
		})
		if err == nil {
			res.Adapter.Close()
		}
		return err
	}
}

// A retried bind invokes the restore closure again, after stageWorkspace and
// immediately before FinalizeWorkspace, and each attempt's FinalizeWorkspace
// carries exactly that attempt's freshly minted grants in index order. The
// pair of Binder sites that issue a bind-time FinalizeWorkspace behave the
// same. Against grants minted once and reused, the second attempt would carry
// the first attempt's (possibly lapsed) capabilities.
//
// spec: §7.1 (Derive restore semantics); §5.2; §4.7.1
// diagnosis: a retried bind would reuse expired restore capabilities, or mint them before stageWorkspace, and fail every retry of a derived session.
func TestRetriedBindMintsFreshRestoreGrantsImmediatelyBeforeFinalize_spec_7_1(t *testing.T) {
	for _, path := range restoreBindPaths() {
		t.Run(path.name, func(t *testing.T) {
			log := &restoreEventLog{}
			a := newRestoreRecordingAdapter(log, true)
			restore, minted := perAttemptRestore(log)
			attempt := path.bind(t, a, restore)

			if err := attempt(); err == nil {
				t.Fatal("first attempt succeeded, want the injected FinalizeWorkspace failure")
			}
			if err := attempt(); err != nil {
				t.Fatalf("retried attempt: %v", err)
			}

			want := []string{
				"PrepareWorkspace", "restore#1", "FinalizeWorkspace",
				"PrepareWorkspace", "restore#2", "FinalizeWorkspace",
			}
			if got := log.snapshot(); !slices.Equal(got, want) {
				t.Errorf("bind sequence = %v, want %v", got, want)
			}
			gotRestores, wantRestores := a.recordedRestores(), minted()
			if len(gotRestores) != 2 || len(wantRestores) != 2 {
				t.Fatalf("finalizes carried %d restores and the closure minted %d sets, want 2 each", len(gotRestores), len(wantRestores))
			}
			for i := range wantRestores {
				if !slices.Equal(gotRestores[i], wantRestores[i]) {
					t.Errorf("attempt %d restore_chunks = %v, want %v", i+1, gotRestores[i], wantRestores[i])
				}
			}
			if slices.Equal(gotRestores[0], gotRestores[1]) {
				t.Error("the retried attempt reused the first attempt's grants")
			}
		})
	}
}

// A bind whose request carries no restore closure sends FinalizeWorkspace
// with an empty restore_chunks on both Binder sites.
//
// spec: §7.1 (Derive restore semantics); §4.7.1
// diagnosis: an ordinary session would ask the adapter to seed its workspace from a snapshot it does not have.
func TestBindWithoutRestoreSendsEmptyRestoreChunks_spec_7_1(t *testing.T) {
	for _, path := range restoreBindPaths() {
		t.Run(path.name, func(t *testing.T) {
			log := &restoreEventLog{}
			a := newRestoreRecordingAdapter(log, false)
			if err := path.bind(t, a, nil)(); err != nil {
				t.Fatalf("bind: %v", err)
			}
			got := a.recordedRestores()
			if len(got) != 1 || len(got[0]) != 0 {
				t.Errorf("FinalizeWorkspace restore_chunks = %v, want one finalize with none", got)
			}
		})
	}
}

// The composers set the restore closure on every bind request, including the
// claim-only request at create and the launch-only request at /start. Only a
// bind that issues FinalizeWorkspace invokes it: Claim, ClaimSlot, and Launch
// never do, so no 30-second grant is minted for a call that cannot use it.
//
// spec: §7.1 (Derive restore semantics); §4.3; §4.4
// diagnosis: grants minted at claim or launch would lapse before the bind-time FinalizeWorkspace, or the closure would be invoked where no restore is sent.
func TestRestoreClosureIsNotInvokedOnClaimOrLaunch_spec_7_1(t *testing.T) {
	t.Run("exclusive Claim, Prepare, Launch", func(t *testing.T) {
		ns := testNamespace(t)
		transport := &snapshotTransport{bodies: map[string][]byte{
			"https://objstore.example/chunk-0": snapshotBundle(t, map[string]string{"workspace/restored.txt": "r"}),
		}}
		srv := adapter.New("adapter-test")
		srv.WorkspaceBase = t.TempDir()
		srv.Runtime = &fakeRuntime{}
		srv.CheckpointTransport = transport
		binder := newBinder(ns, k8sClient(t, idleSandbox(ns, "sbx-1", "10.244.1.7")), adapterDialer(t, srv))
		restore, calls := restoreClosure([]adapterclient.ChunkGrant{{Index: 0, URL: "https://objstore.example/chunk-0"}}, nil)
		req := podsession.BindRequest{
			Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
			Plan: inlinePlan("a.txt"), RestoreChunks: restore,
		}

		claim, err := binder.Claim(context.Background(), req)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if *calls != 0 {
			t.Errorf("Claim invoked the restore closure %d times, want 0", *calls)
		}
		req.SandboxName = claim.SandboxName
		prep, err := binder.Prepare(context.Background(), req)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if *calls != 1 {
			t.Errorf("Prepare invoked the restore closure %d times, want 1", *calls)
		}
		req.Demoted = prep.Demoted
		res, err := binder.Launch(context.Background(), req)
		if err != nil {
			t.Fatalf("Launch: %v", err)
		}
		defer res.Adapter.Close()
		if *calls != 1 {
			t.Errorf("Launch invoked the restore closure; calls = %d, want 1", *calls)
		}
	})
	t.Run("slot ClaimSlot then BindReservedSlot", func(t *testing.T) {
		ns := testNamespace(t)
		a := newConcurrentAdapter()
		binder := newSlotBinder(t, k8sClient(t, concurrentIdleSandbox(ns, "sbx-1", "10.244.1.7")), concurrentAdapterDialer(t, a))
		restore, calls := restoreClosure([]adapterclient.ChunkGrant{{Index: 0, URL: "https://objstore.example/chunk-0"}}, nil)
		req := podsession.SlotBindRequest{
			Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
			MaxConcurrentSessions: 8, Plan: &adapterv1.WorkspacePlan{}, RestoreChunks: restore,
		}
		claim, err := binder.ClaimSlot(context.Background(), req)
		if err != nil {
			t.Fatalf("ClaimSlot: %v", err)
		}
		if *calls != 0 {
			t.Errorf("ClaimSlot invoked the restore closure %d times, want 0", *calls)
		}
		res, err := binder.BindReservedSlot(context.Background(), req, claim.SandboxName, claim.SlotID)
		if err != nil {
			t.Fatalf("BindReservedSlot: %v", err)
		}
		defer res.Adapter.Close()
		if *calls != 1 {
			t.Errorf("BindReservedSlot invoked the restore closure %d times, want 1", *calls)
		}
	})
}

// Prepare's demotion and the launch-only recomputation read one decision:
// for the same request, PrepareResult.Demoted equals RequiresDemotion, which
// is what launchPrepared assigns to BindRequest.Demoted. A restore with
// blocking paths demotes, a restore with an empty list does not, and a
// non-preConnect pod never does.
//
// spec: §6.1; §7.1 (Derive restore semantics); §4.4
// diagnosis: /start would launch a demoted pod through ConfigureWorkspace, or an SDK-warm pod through StartSession, because finalize and launch disagree about the restore demotion.
func TestPrepareAndLaunchRecomputationAgreeOnRestoreDemotion_spec_6_1(t *testing.T) {
	cases := []struct {
		name       string
		preConnect bool
		blocking   []string
		want       bool
	}{
		{name: "restore with blocking paths", preConnect: true, blocking: []string{"CLAUDE.md"}, want: true},
		{name: "restore with no blocking paths", preConnect: true, want: false},
		{name: "restore on a non-preConnect pod", preConnect: false, blocking: []string{"CLAUDE.md"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := testNamespace(t)
			transport := &snapshotTransport{bodies: map[string][]byte{
				"https://objstore.example/chunk-0": snapshotBundle(t, map[string]string{"workspace/CLAUDE.md": "restored"}),
			}}
			rt := &fakeSDKWarmRuntime{}
			srv := adapter.New("adapter-test")
			srv.WorkspaceBase = t.TempDir()
			srv.Runtime = rt
			srv.CheckpointTransport = transport
			binder := newBinder(ns, k8sClient(t, idleSandbox(ns, "sbx-1", "10.244.1.7")), adapterDialer(t, srv))
			restore, _ := restoreClosure([]adapterclient.ChunkGrant{{Index: 0, URL: "https://objstore.example/chunk-0"}}, nil)
			req := podsession.BindRequest{
				Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
				Plan: inlinePlan("src/main.go"), PreConnect: tc.preConnect,
				SDKWarmBlockingPaths: tc.blocking, RestoreChunks: restore,
			}
			claim, err := binder.Claim(context.Background(), req)
			if err != nil {
				t.Fatalf("Claim: %v", err)
			}
			req.SandboxName = claim.SandboxName
			prep, err := binder.Prepare(context.Background(), req)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if prep.Demoted != tc.want {
				t.Errorf("Prepare demoted = %v, want %v", prep.Demoted, tc.want)
			}
			if got := podsession.RequiresDemotion(req); got != prep.Demoted {
				t.Errorf("launch recomputation RequiresDemotion = %v, Prepare demoted = %v; they must agree", got, prep.Demoted)
			}
			wantDemoteCalls := 0
			if tc.want {
				wantDemoteCalls = 1
			}
			if rt.demoted != wantDemoteCalls {
				t.Errorf("DemoteSDK called %d times, want %d", rt.demoted, wantDemoteCalls)
			}
		})
	}
}
