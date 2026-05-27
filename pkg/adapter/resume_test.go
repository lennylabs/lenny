// SPDX-License-Identifier: MIT

package adapter_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// spec: §4.7 / §7.1 — the adapter Resume RPC restores a session
// workspace from a checkpoint on a replacement pod.

// fakeCheckpointSource serves a fixed checkpoint archive.
type fakeCheckpointSource struct {
	archive []byte
	err     error
}

func (f fakeCheckpointSource) LoadCheckpoint(context.Context, string) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(bytes.NewReader(f.archive)), nil
}

// archiveOf builds a gzip-tar checkpoint archive of a workspace holding
// the given files.
func archiveOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	var buf bytes.Buffer
	if _, err := workspace.Archive(dir, &buf); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	return buf.Bytes()
}

func resumeReq(sessionID, checkpointID string) *adapterv1.ResumeRequest {
	return &adapterv1.ResumeRequest{
		SessionId:    &adapterv1.SessionId{Value: sessionID},
		Runtime:      "echo",
		CheckpointId: checkpointID,
	}
}

func TestResumeRestoresTheWorkspaceAndStartsTheRuntime(t *testing.T) {
	s, rt, root := sessionServer(t)
	s.Restorer = fakeCheckpointSource{archive: archiveOf(t, map[string]string{
		"restored.txt": "from checkpoint",
	})}

	resp, err := s.Resume(context.Background(), resumeReq("sess-1", "ckpt-1"))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resp.GetRestoredBytes() != int64(len("from checkpoint")) {
		t.Errorf("restored bytes = %d, want %d", resp.GetRestoredBytes(), len("from checkpoint"))
	}
	if len(rt.started) != 1 || rt.started[0] != "sess-1" {
		t.Errorf("runtime started for %v, want [sess-1]", rt.started)
	}
	got, err := os.ReadFile(filepath.Join(root, "restored.txt"))
	if err != nil {
		t.Fatalf("the checkpoint workspace was not restored: %v", err)
	}
	if string(got) != "from checkpoint" {
		t.Errorf("restored file = %q, want the checkpoint content", got)
	}
}

func TestResumeRejectsMissingIdentifiers(t *testing.T) {
	s, _, _ := sessionServer(t)
	s.Restorer = fakeCheckpointSource{archive: archiveOf(t, map[string]string{"f": "x"})}

	if _, err := s.Resume(context.Background(), resumeReq("", "ckpt-1")); status.Code(err) != codes.InvalidArgument {
		t.Errorf("missing session id: code = %v, want InvalidArgument", status.Code(err))
	}
	if _, err := s.Resume(context.Background(), resumeReq("sess-1", "")); status.Code(err) != codes.InvalidArgument {
		t.Errorf("missing checkpoint id: code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestResumeUnimplementedWithoutACheckpointSource(t *testing.T) {
	s, _, _ := sessionServer(t) // s.Restorer left nil
	_, err := s.Resume(context.Background(), resumeReq("sess-1", "ckpt-1"))
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("code = %v, want Unimplemented when no checkpoint source is configured", status.Code(err))
	}
}

func TestResumeRejectsANonIdlePod(t *testing.T) {
	s, _, _ := sessionServer(t)
	s.Restorer = fakeCheckpointSource{archive: archiveOf(t, map[string]string{"f": "x"})}
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	// The pod already holds sess-1; resuming another session is rejected.
	_, err := s.Resume(context.Background(), resumeReq("sess-2", "ckpt-1"))
	if status.Code(err) != codes.Unavailable {
		t.Errorf("code = %v, want Unavailable for a non-idle pod", status.Code(err))
	}
}

func TestResumeReleasesThePodWhenCheckpointLoadFails(t *testing.T) {
	s, _, _ := sessionServer(t)
	s.Restorer = fakeCheckpointSource{err: errors.New("artifact store unreachable")}

	if _, err := s.Resume(context.Background(), resumeReq("sess-1", "ckpt-1")); status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal when the checkpoint load fails", status.Code(err))
	}
	// The pod was returned to idle, so a retry can claim it.
	s.Restorer = fakeCheckpointSource{archive: archiveOf(t, map[string]string{"f": "x"})}
	if _, err := s.Resume(context.Background(), resumeReq("sess-1", "ckpt-2")); err != nil {
		t.Errorf("retry after a released pod failed: %v", err)
	}
}

// spec: §7.3 line 408 step (d) — "Recreate same absolute `cwd` path."
// The gateway carries the original session's WorkspaceRoot on
// ResumeRequest.expected_workspace_root; the adapter MUST refuse a
// Resume whose mount path disagrees with the adapter's configured
// WorkspaceRoot. F-7.3.15.
func TestResumeRejectsWorkspaceRootMismatch_spec_7_3_15(t *testing.T) {
	s, _, root := sessionServer(t)
	s.Restorer = fakeCheckpointSource{archive: archiveOf(t, map[string]string{"f": "x"})}
	req := resumeReq("sess-1", "ckpt-1")
	req.ExpectedWorkspaceRoot = root + "-different"
	_, err := s.Resume(context.Background(), req)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("F-7.3.15: code = %v, want FailedPrecondition for workspace_root mismatch", status.Code(err))
	}
}

// spec: §7.3 line 408 — a matching workspace_root passes the assertion
// and the resume proceeds. F-7.3.15.
func TestResumeAcceptsMatchingWorkspaceRoot_spec_7_3_15(t *testing.T) {
	s, _, root := sessionServer(t)
	s.Restorer = fakeCheckpointSource{archive: archiveOf(t, map[string]string{"f": "x"})}
	req := resumeReq("sess-1", "ckpt-1")
	req.ExpectedWorkspaceRoot = root
	if _, err := s.Resume(context.Background(), req); err != nil {
		t.Fatalf("F-7.3.15: matching workspace_root rejected: %v", err)
	}
}

// spec: an empty ExpectedWorkspaceRoot disables the assertion so a
// pre-F-7.3.15 client can resume without the hint. F-7.3.15.
func TestResumeAcceptsEmptyExpectedWorkspaceRoot_spec_7_3_15(t *testing.T) {
	s, _, _ := sessionServer(t)
	s.Restorer = fakeCheckpointSource{archive: archiveOf(t, map[string]string{"f": "x"})}
	req := resumeReq("sess-1", "ckpt-1")
	req.ExpectedWorkspaceRoot = ""
	if _, err := s.Resume(context.Background(), req); err != nil {
		t.Fatalf("F-7.3.15: empty workspace_root must be permissive, got: %v", err)
	}
}

// spec: §7.3 line 408 — a mismatch must release the session claim so a
// retry can land on a fresh pod without the original session being
// stuck claimed.
func TestResumeWorkspaceRootMismatchReleasesClaim_spec_7_3_15(t *testing.T) {
	s, _, root := sessionServer(t)
	s.Restorer = fakeCheckpointSource{archive: archiveOf(t, map[string]string{"f": "x"})}
	req := resumeReq("sess-1", "ckpt-1")
	req.ExpectedWorkspaceRoot = root + "-different"
	if _, err := s.Resume(context.Background(), req); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("Resume: %v", err)
	}
	// A subsequent Resume on a different session id must succeed — the
	// pod was released by the first failed Resume.
	req2 := resumeReq("sess-2", "ckpt-1")
	req2.ExpectedWorkspaceRoot = root
	if _, err := s.Resume(context.Background(), req2); err != nil {
		t.Errorf("F-7.3.15: pod was not released on mismatch: %v", err)
	}
}
