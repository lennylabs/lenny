// SPDX-License-Identifier: MIT

package adapter_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// spec: §4.7 / §7.1 — the adapter Resume RPC restores a session
// workspace from the gateway-minted presigned GET capabilities on
// ResumeRequest.chunks, fetched in index order and concatenated into one
// tar (or tar.gz) byte stream (§10.1 line 155).

// fakeCheckpointTransport serves fixed chunk bodies keyed by presigned
// URL, standing in for the object store the production restore path
// fetches from. getErr models a failed object-store GET.
type fakeCheckpointTransport struct {
	chunks map[string][]byte
	getErr error
}

func (f fakeCheckpointTransport) PutChunk(context.Context, string, map[string]string, int64, io.Reader) (int, string, error) {
	return 200, "", nil
}

func (f fakeCheckpointTransport) GetChunk(_ context.Context, url string, _ map[string]string) (io.ReadCloser, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	b, ok := f.chunks[url]
	if !ok {
		return nil, fmt.Errorf("fake transport: no chunk for %q", url)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// archiveOf builds a §4.4 checkpoint bundle holding the given workspace
// files under the workspace prefix — the format the Checkpoint stream now
// produces via workspace.ArchiveTree (F-7.3.14).
func archiveOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	return bundleOf(t, files, nil)
}

// bundleOf builds a §4.4 checkpoint bundle carrying the given workspace
// files and, when sessionFiles is non-nil, a /sessions tree under the
// session prefix. spec: §7.3 line 408-409 (steps e and f).
func bundleOf(t *testing.T, workspaceFiles, sessionFiles map[string]string) []byte {
	t.Helper()
	wsDir := writeTree(t, workspaceFiles)
	roots := []workspace.NamedRoot{{Prefix: workspace.WorkspacePrefix, Root: wsDir}}
	if sessionFiles != nil {
		roots = append(roots, workspace.NamedRoot{
			Prefix: workspace.SessionsPrefix, Root: writeTree(t, sessionFiles),
		})
	}
	var buf bytes.Buffer
	if _, err := workspace.ArchiveTree(roots, &buf); err != nil {
		t.Fatalf("ArchiveTree: %v", err)
	}
	return buf.Bytes()
}

// writeTree materializes files into a fresh temp dir and returns it.
func writeTree(t *testing.T, files map[string]string) string {
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
	return dir
}

// serveArchive wires s with a fake transport that serves the archive as a
// single presigned chunk and returns the ChunkGrant set for the request.
func serveArchive(s *adapter.Server, archive []byte) []*adapterv1.ChunkGrant {
	const url = "https://objectstore.example/chunk-0"
	s.CheckpointTransport = fakeCheckpointTransport{chunks: map[string][]byte{url: archive}}
	return []*adapterv1.ChunkGrant{{Index: 0, Url: url, Length: int64(len(archive))}}
}

func resumeReq(sessionID, checkpointID string) *adapterv1.ResumeRequest {
	return &adapterv1.ResumeRequest{
		SessionId:    &adapterv1.SessionId{Value: sessionID},
		Runtime:      "echo",
		CheckpointId: checkpointID,
	}
}

// resumeReqChunks is resumeReq plus a chunk set.
func resumeReqChunks(sessionID, checkpointID string, chunks []*adapterv1.ChunkGrant) *adapterv1.ResumeRequest {
	req := resumeReq(sessionID, checkpointID)
	req.Chunks = chunks
	return req
}

func TestResumeRestoresTheWorkspaceAndStartsTheRuntime(t *testing.T) {
	s, rt, root := sessionServer(t)
	chunks := serveArchive(s, archiveOf(t, map[string]string{
		"restored.txt": "from checkpoint",
	}))

	resp, err := s.Resume(context.Background(), resumeReqChunks("sess-1", "ckpt-1", chunks))
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

// spec: §10.1 line 155 — a multi-chunk checkpoint is restored by fetching
// each presigned GET capability in ascending index order and feeding the
// concatenation into one decompress→untar pipeline. This pins that the
// adapter reassembles across chunk boundaries rather than treating each
// chunk as an independent archive.
func TestResumeConcatenatesChunksInIndexOrder_spec_10_1_155(t *testing.T) {
	s, _, root := sessionServer(t)
	archive := archiveOf(t, map[string]string{"multi.txt": "reassembled from two chunks"})
	// Split the single archive at an arbitrary byte offset into two chunks,
	// and present them out of order to prove the adapter sorts by index.
	mid := len(archive) / 2
	const url0, url1 = "https://objectstore.example/chunk-0", "https://objectstore.example/chunk-1"
	s.CheckpointTransport = fakeCheckpointTransport{chunks: map[string][]byte{
		url0: archive[:mid],
		url1: archive[mid:],
	}}
	chunks := []*adapterv1.ChunkGrant{
		{Index: 1, Url: url1, Length: int64(len(archive) - mid)},
		{Index: 0, Url: url0, Length: int64(mid)},
	}

	if _, err := s.Resume(context.Background(), resumeReqChunks("sess-1", "ckpt-1", chunks)); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "multi.txt"))
	if err != nil {
		t.Fatalf("the concatenated checkpoint workspace was not restored: %v", err)
	}
	if string(got) != "reassembled from two chunks" {
		t.Errorf("restored file = %q, want the concatenated content", got)
	}
}

// spec: §7.3 line 409 step (f) — a resume restores the runtime's
// session file (native SDK session state, conversation logs) from the
// /sessions tmpfs, not just the workspace. F-7.3.14.
func TestResumeRestoresSessionFileToExpectedPath_spec_7_3_14(t *testing.T) {
	s, _, root := sessionServer(t)
	sessionsRoot := t.TempDir()
	s.SessionsRoot = sessionsRoot
	chunks := serveArchive(s, bundleOf(
		t,
		map[string]string{"work/file.txt": "ws"},
		map[string]string{".session.json": `{"id":"sess-1","turns":3}`},
	))

	resp, err := s.Resume(context.Background(), resumeReqChunks("sess-1", "ckpt-1", chunks))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	// The workspace tree restored to WorkspaceRoot.
	if got, err := os.ReadFile(filepath.Join(root, "work/file.txt")); err != nil || string(got) != "ws" {
		t.Fatalf("workspace file = %q (err %v), want %q", got, err, "ws")
	}
	// The session file restored to SessionsRoot — the §7.3 step (f) path.
	got, err := os.ReadFile(filepath.Join(sessionsRoot, ".session.json"))
	if err != nil {
		t.Fatalf("the session file was not restored to /sessions: %v", err)
	}
	if string(got) != `{"id":"sess-1","turns":3}` {
		t.Errorf("session file = %q, want the checkpoint content", got)
	}
	wantBytes := int64(len("ws") + len(`{"id":"sess-1","turns":3}`))
	if resp.GetRestoredBytes() != wantBytes {
		t.Errorf("restored bytes = %d, want %d (workspace + session file)", resp.GetRestoredBytes(), wantBytes)
	}
}

// A checkpoint that carried no /sessions tree (a runtime without a
// session file, or a SessionsRoot-less adapter) restores workspace-only
// and leaves /sessions untouched. F-7.3.14.
func TestResumeWorkspaceOnlyBundleLeavesSessionsEmpty_spec_7_3_14(t *testing.T) {
	s, _, root := sessionServer(t)
	sessionsRoot := t.TempDir()
	s.SessionsRoot = sessionsRoot
	chunks := serveArchive(s, bundleOf(
		t,
		map[string]string{"only.txt": "ws"}, nil,
	))

	if _, err := s.Resume(context.Background(), resumeReqChunks("sess-1", "ckpt-1", chunks)); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "only.txt")); err != nil || string(got) != "ws" {
		t.Fatalf("workspace file = %q (err %v), want %q", got, err, "ws")
	}
	entries, err := os.ReadDir(sessionsRoot)
	if err != nil {
		t.Fatalf("read /sessions: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("/sessions has %d entries, want 0 (workspace-only bundle)", len(entries))
	}
}

// spec: §10.1 — a conversation-only resume carries no chunks; the adapter
// restores no workspace and starts the runtime fresh rather than failing.
func TestResumeWithNoChunksRestoresNothing(t *testing.T) {
	s, rt, root := sessionServer(t)
	// No transport and no chunks: the restore is a no-op.
	resp, err := s.Resume(context.Background(), resumeReq("sess-1", "ckpt-1"))
	if err != nil {
		t.Fatalf("Resume with no chunks: %v", err)
	}
	if resp.GetRestoredBytes() != 0 {
		t.Errorf("restored bytes = %d, want 0 for a chunk-less resume", resp.GetRestoredBytes())
	}
	if len(rt.started) != 1 || rt.started[0] != "sess-1" {
		t.Errorf("runtime started for %v, want [sess-1]", rt.started)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("workspace has %d entries, want 0 for a chunk-less resume", len(entries))
	}
}

func TestResumeRejectsMissingIdentifiers(t *testing.T) {
	s, _, _ := sessionServer(t)
	chunks := serveArchive(s, archiveOf(t, map[string]string{"f": "x"}))

	if _, err := s.Resume(context.Background(), resumeReqChunks("", "ckpt-1", chunks)); status.Code(err) != codes.InvalidArgument {
		t.Errorf("missing session id: code = %v, want InvalidArgument", status.Code(err))
	}
	if _, err := s.Resume(context.Background(), resumeReqChunks("sess-1", "", chunks)); status.Code(err) != codes.InvalidArgument {
		t.Errorf("missing checkpoint id: code = %v, want InvalidArgument", status.Code(err))
	}
}

// spec: §13.2 — a resume that carries chunks needs the checkpoint
// transport to fetch them; without one the adapter refuses the restore
// FailedPrecondition rather than silently restoring nothing.
func TestResumeRequiresTransportWhenChunksPresent(t *testing.T) {
	s, _, _ := sessionServer(t) // s.CheckpointTransport left nil
	req := resumeReqChunks("sess-1", "ckpt-1", []*adapterv1.ChunkGrant{
		{Index: 0, Url: "https://objectstore.example/chunk-0", Length: 1},
	})
	_, err := s.Resume(context.Background(), req)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition when chunks are present but no transport is configured", status.Code(err))
	}
}

func TestResumeRejectsANonIdlePod(t *testing.T) {
	s, _, _ := sessionServer(t)
	chunks := serveArchive(s, archiveOf(t, map[string]string{"f": "x"}))
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	// The pod already holds sess-1; resuming another session is rejected.
	_, err := s.Resume(context.Background(), resumeReqChunks("sess-2", "ckpt-1", chunks))
	if status.Code(err) != codes.Unavailable {
		t.Errorf("code = %v, want Unavailable for a non-idle pod", status.Code(err))
	}
}

func TestResumeReleasesThePodWhenChunkFetchFails(t *testing.T) {
	s, _, _ := sessionServer(t)
	s.CheckpointTransport = fakeCheckpointTransport{getErr: errors.New("object store unreachable")}
	req := resumeReqChunks("sess-1", "ckpt-1", []*adapterv1.ChunkGrant{
		{Index: 0, Url: "https://objectstore.example/chunk-0", Length: 1},
	})

	if _, err := s.Resume(context.Background(), req); status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal when the chunk fetch fails", status.Code(err))
	}
	// The pod was returned to idle, so a retry can claim it.
	chunks := serveArchive(s, archiveOf(t, map[string]string{"f": "x"}))
	if _, err := s.Resume(context.Background(), resumeReqChunks("sess-1", "ckpt-2", chunks)); err != nil {
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
	chunks := serveArchive(s, archiveOf(t, map[string]string{"f": "x"}))
	req := resumeReqChunks("sess-1", "ckpt-1", chunks)
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
	chunks := serveArchive(s, archiveOf(t, map[string]string{"f": "x"}))
	req := resumeReqChunks("sess-1", "ckpt-1", chunks)
	req.ExpectedWorkspaceRoot = root
	if _, err := s.Resume(context.Background(), req); err != nil {
		t.Fatalf("F-7.3.15: matching workspace_root rejected: %v", err)
	}
}

// spec: an empty ExpectedWorkspaceRoot disables the assertion so a
// pre-F-7.3.15 client can resume without the hint. F-7.3.15.
func TestResumeAcceptsEmptyExpectedWorkspaceRoot_spec_7_3_15(t *testing.T) {
	s, _, _ := sessionServer(t)
	chunks := serveArchive(s, archiveOf(t, map[string]string{"f": "x"}))
	req := resumeReqChunks("sess-1", "ckpt-1", chunks)
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
	chunks := serveArchive(s, archiveOf(t, map[string]string{"f": "x"}))
	req := resumeReqChunks("sess-1", "ckpt-1", chunks)
	req.ExpectedWorkspaceRoot = root + "-different"
	if _, err := s.Resume(context.Background(), req); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("Resume: %v", err)
	}
	// A subsequent Resume on a different session id must succeed — the
	// pod was released by the first failed Resume.
	req2 := resumeReqChunks("sess-2", "ckpt-1", chunks)
	req2.ExpectedWorkspaceRoot = root
	if _, err := s.Resume(context.Background(), req2); err != nil {
		t.Errorf("F-7.3.15: pod was not released on mismatch: %v", err)
	}
}
