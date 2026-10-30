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
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
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
