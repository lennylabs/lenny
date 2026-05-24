// SPDX-License-Identifier: MIT

package adapter_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// fakeAborter records every AbortPartial invocation and optionally
// fails so the adapter exercises the §4.4 line 248 orphan counter.
type fakeAborter struct {
	calls []string
	err   error
}

func (f *fakeAborter) AbortPartial(_ context.Context, sessionID string) error {
	f.calls = append(f.calls, sessionID)
	return f.err
}

// fakeCheckpointMetrics records every metric increment so tests can
// assert the §4.4 line 248 / 254 / 262 counters fire on the expected paths.
type fakeCheckpointMetrics struct {
	orphaned       []string
	exceeded       []string
	storageFailure []string
}

func (f *fakeCheckpointMetrics) IncCheckpointOrphanedObjects(pool, trigger string) {
	f.orphaned = append(f.orphaned, pool+"|"+trigger)
}

func (f *fakeCheckpointMetrics) IncCheckpointSizeExceeded(pool, level string) {
	f.exceeded = append(f.exceeded, pool+"|"+level)
}

// spec: §4.4 line 262 — non-eviction MinIO upload failure.
func (f *fakeCheckpointMetrics) IncCheckpointStorageFailure(pool, level, trigger string) {
	f.storageFailure = append(f.storageFailure, pool+"|"+level+"|"+trigger)
}

// fakeCheckpointEvents records every checkpoint.skipped publish so
// tests can assert the §4.4 line 254 event-emit contract.
type fakeCheckpointEvents struct {
	sessionID string
	reason    string
	fields    map[string]any
	calls     int32
}

func (f *fakeCheckpointEvents) EmitCheckpointSkipped(_ context.Context, sessionID, reason string, fields map[string]any) {
	atomic.AddInt32(&f.calls, 1)
	f.sessionID = sessionID
	f.reason = reason
	f.fields = fields
}

// spec: §4.4 / §4.7 — the adapter Checkpoint RPC snapshots the session
// workspace and stores it in the artifact store.

// fakeCheckpointSink records what SaveCheckpoint received.
type fakeCheckpointSink struct {
	id         string
	err        error
	gotSession string
	received   []byte
}

func (f *fakeCheckpointSink) SaveCheckpoint(_ context.Context, sessionID string, r io.Reader) (string, error) {
	f.gotSession = sessionID
	b, readErr := io.ReadAll(r) // drain to EOF before reporting any error
	f.received = b
	if readErr != nil {
		return "", readErr
	}
	if f.err != nil {
		return "", f.err
	}
	return f.id, nil
}

func checkpointReq(sessionID string) *adapterv1.CheckpointRequest {
	return &adapterv1.CheckpointRequest{SessionId: &adapterv1.SessionId{Value: sessionID}}
}

// startedServer returns an adapter Server with an assigned session and
// one file written into its workspace.
func startedServer(t *testing.T) (*adapter.Server, string) {
	t.Helper()
	s, _, root := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("agent state"), 0o644); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	return s, root
}

func TestCheckpointArchivesAndStoresTheWorkspace(t *testing.T) {
	s, _ := startedServer(t)
	sink := &fakeCheckpointSink{id: "ckpt-1"}
	s.Checkpoints = sink

	resp, err := s.Checkpoint(context.Background(), checkpointReq("sess-1"))
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if resp.GetCheckpointId() != "ckpt-1" {
		t.Errorf("checkpoint id = %q, want ckpt-1", resp.GetCheckpointId())
	}
	if resp.GetSizeBytes() != int64(len(sink.received)) {
		t.Errorf("size = %d, want the archived byte count %d", resp.GetSizeBytes(), len(sink.received))
	}
	if sink.gotSession != "sess-1" {
		t.Errorf("sink saw session %q, want sess-1", sink.gotSession)
	}

	// The stored archive is a valid gzip-tar carrying the workspace.
	if content := tarEntry(t, sink.received, "notes.txt"); content != "agent state" {
		t.Errorf("archived notes.txt = %q, want the workspace content", content)
	}
}

func TestCheckpointViaLifecycleChannelQuiescesAndResumes(t *testing.T) {
	s, _ := startedServer(t)
	sink := &fakeCheckpointSink{id: "ckpt-full"}
	s.Checkpoints = sink
	lc := startLifecycle(t)
	s.Lifecycle = lc
	lr := dialLifecycle(t, lc)

	respc := make(chan *adapterv1.CheckpointResponse, 1)
	go func() {
		resp, err := s.Checkpoint(context.Background(), checkpointReq("sess-1"))
		if err != nil {
			t.Errorf("Checkpoint: %v", err)
			respc <- nil
			return
		}
		respc <- resp
	}()

	// The adapter asks the runtime to quiesce, then waits for ready.
	req := lr.recv()
	if req["type"] != "checkpoint_request" {
		t.Fatalf("runtime saw %v, want checkpoint_request", req["type"])
	}
	corrID, _ := req["checkpointId"].(string)
	if corrID == "" {
		t.Fatal("checkpoint_request carried no checkpointId")
	}
	lr.send(map[string]any{"type": "checkpoint_ready", "checkpointId": corrID})

	// Once the snapshot is stored the adapter resumes the runtime.
	done := lr.recv()
	if done["type"] != "checkpoint_complete" || done["status"] != "ok" {
		t.Fatalf("runtime saw %v, want checkpoint_complete status ok", done)
	}
	if done["checkpointId"] != corrID {
		t.Errorf("checkpoint_complete checkpointId = %v, want %v", done["checkpointId"], corrID)
	}

	resp := <-respc
	if resp == nil {
		return
	}
	if resp.GetCheckpointId() != "ckpt-full" {
		t.Errorf("checkpoint id = %q, want ckpt-full", resp.GetCheckpointId())
	}
	if content := tarEntry(t, sink.received, "notes.txt"); content != "agent state" {
		t.Errorf("archived notes.txt = %q, want the workspace content", content)
	}
}

func TestCheckpointRejectsMissingSessionID(t *testing.T) {
	s, _ := startedServer(t)
	s.Checkpoints = &fakeCheckpointSink{id: "x"}
	_, err := s.Checkpoint(context.Background(), checkpointReq(""))
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestCheckpointRejectsUnassignedSession(t *testing.T) {
	s, _, _ := sessionServer(t)
	s.Checkpoints = &fakeCheckpointSink{id: "x"}
	_, err := s.Checkpoint(context.Background(), checkpointReq("sess-1"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition for a pod with no session", status.Code(err))
	}
}

func TestCheckpointRejectsWrongSession(t *testing.T) {
	s, _ := startedServer(t)
	s.Checkpoints = &fakeCheckpointSink{id: "x"}
	_, err := s.Checkpoint(context.Background(), checkpointReq("other-session"))
	if status.Code(err) != codes.NotFound {
		t.Errorf("code = %v, want NotFound for a session not assigned to the pod", status.Code(err))
	}
}

func TestCheckpointUnimplementedWithoutSink(t *testing.T) {
	s, _ := startedServer(t) // s.Checkpoints left nil
	_, err := s.Checkpoint(context.Background(), checkpointReq("sess-1"))
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("code = %v, want Unimplemented when no checkpoint sink is configured", status.Code(err))
	}
}

func TestCheckpointSurfacesSinkError(t *testing.T) {
	s, _ := startedServer(t)
	s.Checkpoints = &fakeCheckpointSink{err: errors.New("minio unreachable")}
	_, err := s.Checkpoint(context.Background(), checkpointReq("sess-1"))
	if status.Code(err) != codes.Internal {
		t.Errorf("code = %v, want Internal when the checkpoint sink fails", status.Code(err))
	}
}

// TestCheckpointAbortCleanupRunsOnSinkFailure asserts the §4.4 line
// 248 abort-cleanup invocation: every failed checkpoint MUST trigger
// the configured CheckpointAborter so partially-uploaded MinIO
// objects are removed for the session.
func TestCheckpointAbortCleanupRunsOnSinkFailure(t *testing.T) {
	s, _ := startedServer(t)
	s.Checkpoints = &fakeCheckpointSink{err: errors.New("minio unreachable")}
	aborter := &fakeAborter{}
	s.CheckpointAborter = aborter

	_, err := s.Checkpoint(context.Background(), checkpointReq("sess-1"))
	if status.Code(err) != codes.Internal {
		t.Fatalf("Checkpoint code = %v, want Internal on sink failure", status.Code(err))
	}
	if len(aborter.calls) != 1 || aborter.calls[0] != "sess-1" {
		t.Errorf("AbortPartial calls = %v, want [sess-1]", aborter.calls)
	}
}

// TestCheckpointBumpsStorageFailureCounterOnSaveFailure asserts the
// §4.4 line 262 lenny_checkpoint_storage_failure_total counter fires
// when the non-eviction MinIO upload fails. Eviction triggers bypass
// this counter (see TestCheckpointSkipsStorageFailureForEviction).
//
// spec: §4.4 line 262 — "If all upload retries fail, the adapter
// resumes the agent immediately ... The adapter logs the failure and
// increments the lenny_checkpoint_storage_failure_total metric
// (counter, labeled by pool, level, and trigger)".
func TestCheckpointBumpsStorageFailureCounterOnSaveFailure(t *testing.T) {
	s, _ := startedServer(t)
	s.Checkpoints = &fakeCheckpointSink{err: errors.New("minio unreachable")}
	metrics := &fakeCheckpointMetrics{}
	s.CheckpointMetrics = metrics
	s.CheckpointPoolLabel = "default"
	s.CheckpointLevelLabel = "full"
	s.CheckpointTriggerLabel = "periodic"

	_, _ = s.Checkpoint(context.Background(), checkpointReq("sess-1"))
	if len(metrics.storageFailure) != 1 {
		t.Fatalf("storage_failure counter fired %d times, want 1", len(metrics.storageFailure))
	}
	want := "default|full|periodic"
	if metrics.storageFailure[0] != want {
		t.Fatalf("storage_failure label = %q, want %q", metrics.storageFailure[0], want)
	}
}

// spec: §4.4 line 262 — eviction triggers skip the storage_failure
// counter; their failures are accounted in the eviction-fallback
// writer's IncCheckpointEvictionFallback so every fallback attempt
// counts exactly once.
func TestCheckpointSkipsStorageFailureForEviction(t *testing.T) {
	s, _ := startedServer(t)
	s.Checkpoints = &fakeCheckpointSink{err: errors.New("minio unreachable")}
	metrics := &fakeCheckpointMetrics{}
	s.CheckpointMetrics = metrics
	s.CheckpointTriggerLabel = "eviction"

	_, _ = s.Checkpoint(context.Background(), checkpointReq("sess-1"))
	if len(metrics.storageFailure) != 0 {
		t.Fatalf("storage_failure counter fired on eviction trigger: %v", metrics.storageFailure)
	}
}

// TestCheckpointAbortCleanupBumpsOrphanedCounterOnDeleteFailure asserts
// the §4.4 line 248 orphan-counter increment when AbortPartial cannot
// remove the partial objects (e.g., MinIO is unreachable).
func TestCheckpointAbortCleanupBumpsOrphanedCounterOnDeleteFailure(t *testing.T) {
	s, _ := startedServer(t)
	s.Checkpoints = &fakeCheckpointSink{err: errors.New("minio unreachable")}
	s.CheckpointAborter = &fakeAborter{err: errors.New("delete refused")}
	metrics := &fakeCheckpointMetrics{}
	s.CheckpointMetrics = metrics
	s.CheckpointPoolLabel = "claude-code-pool"
	s.CheckpointTriggerLabel = "periodic"

	_, _ = s.Checkpoint(context.Background(), checkpointReq("sess-1"))
	if len(metrics.orphaned) != 1 || metrics.orphaned[0] != "claude-code-pool|periodic" {
		t.Errorf("orphaned counter = %v, want [claude-code-pool|periodic]", metrics.orphaned)
	}
}

// TestCheckpointAbortCleanupSkippedOnSuccess asserts the abort
// pathway only fires on failure — a successful checkpoint never
// invokes AbortPartial.
func TestCheckpointAbortCleanupSkippedOnSuccess(t *testing.T) {
	s, _ := startedServer(t)
	s.Checkpoints = &fakeCheckpointSink{id: "ckpt-ok"}
	aborter := &fakeAborter{}
	s.CheckpointAborter = aborter

	if _, err := s.Checkpoint(context.Background(), checkpointReq("sess-1")); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if len(aborter.calls) != 0 {
		t.Errorf("AbortPartial called %d times on success, want 0", len(aborter.calls))
	}
}

// TestCheckpointSizePreCheckRejectsOversizedWorkspace asserts the
// §4.4 line 254 pre-checkpoint workspace-size probe: an oversized
// workspace returns FailedPrecondition without quiescing the runtime.
func TestCheckpointSizePreCheckRejectsOversizedWorkspace(t *testing.T) {
	s, _ := startedServer(t)
	s.Checkpoints = &fakeCheckpointSink{id: "ckpt-1"}
	s.WorkspaceSizeLimitBytes = 100
	s.WorkspaceSize = func(string) (int64, error) { return 200, nil }
	metrics := &fakeCheckpointMetrics{}
	s.CheckpointMetrics = metrics
	events := &fakeCheckpointEvents{}
	s.CheckpointEvents = events
	s.CheckpointPoolLabel = "test-pool"
	s.CheckpointLevelLabel = "full"

	_, err := s.Checkpoint(context.Background(), checkpointReq("sess-1"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("Checkpoint code = %v, want FailedPrecondition for size_exceeded", status.Code(err))
	}
	if len(metrics.exceeded) != 1 || metrics.exceeded[0] != "test-pool|full" {
		t.Errorf("size_exceeded counter = %v, want [test-pool|full]", metrics.exceeded)
	}
	if events.reason != "workspace_size_limit" {
		t.Errorf("event reason = %q, want workspace_size_limit", events.reason)
	}
	if events.sessionID != "sess-1" {
		t.Errorf("event sessionID = %q, want sess-1", events.sessionID)
	}
	if events.fields["workspace_bytes"] != int64(200) || events.fields["limit_bytes"] != int64(100) {
		t.Errorf("event fields = %+v, want workspace_bytes=200, limit_bytes=100", events.fields)
	}
}

// TestCheckpointSizePreCheckPassesWithinLimit asserts that a
// workspace within the limit proceeds through the normal
// archive-and-store path.
func TestCheckpointSizePreCheckPassesWithinLimit(t *testing.T) {
	s, _ := startedServer(t)
	sink := &fakeCheckpointSink{id: "ckpt-ok"}
	s.Checkpoints = sink
	s.WorkspaceSizeLimitBytes = 1 << 30
	s.WorkspaceSize = func(string) (int64, error) { return 256, nil }
	metrics := &fakeCheckpointMetrics{}
	s.CheckpointMetrics = metrics
	events := &fakeCheckpointEvents{}
	s.CheckpointEvents = events

	resp, err := s.Checkpoint(context.Background(), checkpointReq("sess-1"))
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if resp.GetCheckpointId() != "ckpt-ok" {
		t.Errorf("checkpoint id = %q, want ckpt-ok", resp.GetCheckpointId())
	}
	if len(metrics.exceeded) != 0 {
		t.Errorf("size_exceeded counter fired %d times, want 0", len(metrics.exceeded))
	}
	if atomic.LoadInt32(&events.calls) != 0 {
		t.Errorf("checkpoint.skipped event emitted %d times, want 0", events.calls)
	}
}

// TestCheckpointSizePreCheckSkippedWhenUnconfigured asserts the probe
// is a no-op when no limit or no size function is set — the kubelet
// emptyDir.sizeLimit is the backstop in that case.
func TestCheckpointSizePreCheckSkippedWhenUnconfigured(t *testing.T) {
	s, _ := startedServer(t)
	s.Checkpoints = &fakeCheckpointSink{id: "ckpt-ok"}
	// No WorkspaceSize, no WorkspaceSizeLimitBytes — probe disabled.
	metrics := &fakeCheckpointMetrics{}
	s.CheckpointMetrics = metrics

	if _, err := s.Checkpoint(context.Background(), checkpointReq("sess-1")); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if len(metrics.exceeded) != 0 {
		t.Errorf("size_exceeded fired with no limit configured = %v", metrics.exceeded)
	}
}

// TestCheckpointSizePreCheckIgnoresStatError asserts a probe stat
// failure does not block the checkpoint — the workspace is checkpointed
// anyway, with the kubelet emptyDir.sizeLimit as the backstop.
func TestCheckpointSizePreCheckIgnoresStatError(t *testing.T) {
	s, _ := startedServer(t)
	s.Checkpoints = &fakeCheckpointSink{id: "ckpt-ok"}
	s.WorkspaceSizeLimitBytes = 100
	s.WorkspaceSize = func(string) (int64, error) {
		return 0, errors.New("stat refused")
	}
	if _, err := s.Checkpoint(context.Background(), checkpointReq("sess-1")); err != nil {
		t.Errorf("Checkpoint: %v, want stat error to be ignored", err)
	}
}

// TestCheckpointAbortNotInvokedOnSizeExceededReject asserts the abort
// path is NOT invoked on a size_exceeded reject — there were never
// any partial uploads to clean up because quiescence never ran.
func TestCheckpointAbortNotInvokedOnSizeExceededReject(t *testing.T) {
	s, _ := startedServer(t)
	s.Checkpoints = &fakeCheckpointSink{id: "ckpt-1"}
	s.WorkspaceSizeLimitBytes = 100
	s.WorkspaceSize = func(string) (int64, error) { return 200, nil }
	aborter := &fakeAborter{}
	s.CheckpointAborter = aborter
	s.CheckpointEvents = &fakeCheckpointEvents{}
	s.CheckpointMetrics = &fakeCheckpointMetrics{}

	_, _ = s.Checkpoint(context.Background(), checkpointReq("sess-1"))
	if len(aborter.calls) != 0 {
		t.Errorf("AbortPartial called %d times on size-reject, want 0 (no quiescence ran)", len(aborter.calls))
	}
}

// tarEntry decodes a gzip-tar and returns the named entry's content.
func tarEntry(t *testing.T, data []byte, name string) string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			t.Fatalf("entry %q not found in the archive", name)
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		if hdr.Name == name {
			body, err := io.ReadAll(tr)
			if err != nil {
				t.Fatalf("read %q: %v", name, err)
			}
			return string(body)
		}
	}
}
