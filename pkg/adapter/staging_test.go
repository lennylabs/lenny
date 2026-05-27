// SPDX-License-Identifier: MIT

package adapter

import (
	"archive/tar"
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// captureLogOutput redirects the stdlib logger to a buffer for the
// duration of a test so a warning-line emit can be asserted.
func captureLogOutput(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := log.Writer()
	log.SetOutput(buf)
	return buf, func() { log.SetOutput(prev) }
}

func runSetupReq(sessionID string, policy *adapterv1.SetupPolicy, cmds ...string) *adapterv1.RunSetupRequest {
	req := &adapterv1.RunSetupRequest{
		SessionId:   &adapterv1.SessionId{Value: sessionID},
		SetupPolicy: policy,
	}
	for _, c := range cmds {
		req.SetupCommands = append(req.SetupCommands, &adapterv1.SetupCommand{Cmd: c})
	}
	return req
}

func wsSource(typ, path, content, mode string) *adapterv1.WorkspaceSource {
	return &adapterv1.WorkspaceSource{Type: typ, Path: path, Content: content, Mode: mode}
}

func finalizeReq(sessionID string, sources ...*adapterv1.WorkspaceSource) *adapterv1.FinalizeWorkspaceRequest {
	return &adapterv1.FinalizeWorkspaceRequest{
		SessionId:     &adapterv1.SessionId{Value: sessionID},
		WorkspacePlan: &adapterv1.WorkspacePlan{SchemaVersion: 1, Sources: sources},
	}
}

func TestFinalizeWorkspaceMaterializes(t *testing.T) {
	root := t.TempDir()
	srv := &Server{WorkspaceRoot: root}
	if _, err := srv.FinalizeWorkspace(context.Background(), finalizeReq("sess-1",
		wsSource("mkdir", "docs", "", "755"),
		wsSource("inlineFile", "docs/readme.md", "hello", "644"))); err != nil {
		t.Fatalf("FinalizeWorkspace: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, "docs", "readme.md"))
	if err != nil {
		t.Fatalf("read materialized file: %v", err)
	}
	if string(b) != "hello" {
		t.Errorf("readme.md = %q, want hello", b)
	}
}

func TestFinalizeWorkspaceEmptyPlan(t *testing.T) {
	// A session with an empty workspace plan finalizes as a no-op.
	srv := &Server{WorkspaceRoot: t.TempDir()}
	if _, err := srv.FinalizeWorkspace(context.Background(), finalizeReq("sess-1")); err != nil {
		t.Errorf("FinalizeWorkspace with an empty plan = %v, want nil", err)
	}
}

func TestFinalizeWorkspaceRequiresSessionID(t *testing.T) {
	srv := &Server{WorkspaceRoot: t.TempDir()}
	_, err := srv.FinalizeWorkspace(context.Background(),
		finalizeReq("", wsSource("mkdir", "x", "", "")))
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("FinalizeWorkspace without a session id = %v, want InvalidArgument", err)
	}
}

func TestFinalizeWorkspaceRequiresWorkspaceRoot(t *testing.T) {
	srv := &Server{}
	_, err := srv.FinalizeWorkspace(context.Background(),
		finalizeReq("sess-1", wsSource("mkdir", "x", "", "")))
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("FinalizeWorkspace without a workspace root = %v, want FailedPrecondition", err)
	}
}

func TestFinalizeWorkspaceRejectsEscapingPath(t *testing.T) {
	// The §14 path-containment guard in workspace.Materialize rejects a
	// source that escapes the workspace root; the RPC surfaces it as
	// InvalidArgument.
	srv := &Server{WorkspaceRoot: t.TempDir()}
	_, err := srv.FinalizeWorkspace(context.Background(),
		finalizeReq("sess-1", wsSource("inlineFile", "../escape.txt", "x", "644")))
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("FinalizeWorkspace with an escaping path = %v, want InvalidArgument", err)
	}
}

// TestFinalizeWorkspacePlumsArchivePolicy covers F-7.4.4: the
// FinalizeWorkspaceRequest.archive_policy.allow_symlinks toggle lifts
// the §7.4 line 458 default-deny on uploadArchive symlink entries. With
// the gateway-supplied policy set to AllowSymlinks=true and a target
// inside the workspace, extraction succeeds; without it, the same
// archive aborts. spec: §7.4 lines 458, 462; §13.4 — F-7.4.4.
func TestFinalizeWorkspacePlumsArchivePolicy(t *testing.T) {
	srv := &Server{WorkspaceRoot: t.TempDir(), StagingDir: t.TempDir()}

	// Build a one-entry tar archive with a symlink whose target is a
	// regular file at the workspace root.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, hdr := range []*tar.Header{
		{Name: "target.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 2},
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "target.txt", Mode: 0o644},
	} {
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte("hi")); err != nil {
				t.Fatalf("write tar body: %v", err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	stagedPath, err := workspace.StagingPath(srv.StagingDir, "arch")
	if err != nil {
		t.Fatalf("StagingPath: %v", err)
	}
	if err := os.WriteFile(stagedPath, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write staging file: %v", err)
	}

	src := &adapterv1.WorkspaceSource{
		Type: "uploadArchive", Format: "tar", UploadRef: "arch",
	}
	reqWithSymlinkOptIn := &adapterv1.FinalizeWorkspaceRequest{
		SessionId:     &adapterv1.SessionId{Value: "sess-1"},
		WorkspacePlan: &adapterv1.WorkspacePlan{SchemaVersion: 1, Sources: []*adapterv1.WorkspaceSource{src}},
		ArchivePolicy: &adapterv1.ArchivePolicy{AllowSymlinks: true, WorkspaceRoot: srv.WorkspaceRoot},
	}
	if _, err := srv.FinalizeWorkspace(context.Background(), reqWithSymlinkOptIn); err != nil {
		t.Fatalf("FinalizeWorkspace with AllowSymlinks=true: %v", err)
	}
	if info, err := os.Lstat(filepath.Join(srv.WorkspaceRoot, "link")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected symlink to be created; Lstat = %v %v", info, err)
	}

	// Default policy (nil) must reject the same archive with
	// InvalidArgument (the gRPC mapping of workspace.Materialize errors).
	srv2 := &Server{WorkspaceRoot: t.TempDir(), StagingDir: t.TempDir()}
	staged2, _ := workspace.StagingPath(srv2.StagingDir, "arch")
	if err := os.WriteFile(staged2, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("seed staging file: %v", err)
	}
	reqDefault := &adapterv1.FinalizeWorkspaceRequest{
		SessionId:     &adapterv1.SessionId{Value: "sess-1"},
		WorkspacePlan: &adapterv1.WorkspacePlan{SchemaVersion: 1, Sources: []*adapterv1.WorkspaceSource{src}},
	}
	_, err = srv2.FinalizeWorkspace(context.Background(), reqDefault)
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("FinalizeWorkspace with default policy and a symlink entry = %v, want InvalidArgument", err)
	}
}

func TestRunSetupExecutesCommands(t *testing.T) {
	root := t.TempDir()
	srv := &Server{WorkspaceRoot: root}
	if _, err := srv.RunSetup(context.Background(),
		runSetupReq("sess-1", nil, "echo ok > result.txt", "mkdir sub")); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, "result.txt"))
	if err != nil {
		t.Fatalf("read result.txt: %v", err)
	}
	if strings.TrimSpace(string(b)) != "ok" {
		t.Errorf("result.txt = %q, want ok", b)
	}
	if fi, err := os.Stat(filepath.Join(root, "sub")); err != nil || !fi.IsDir() {
		t.Errorf("setup command did not create the sub directory: %v", err)
	}
}

func TestRunSetupNoCommands(t *testing.T) {
	// The §4.7 sequence calls RunSetup even for a plan with no setup
	// commands; an empty list completes the phase as a no-op.
	srv := &Server{WorkspaceRoot: t.TempDir()}
	if _, err := srv.RunSetup(context.Background(), runSetupReq("sess-1", nil)); err != nil {
		t.Errorf("RunSetup with no commands = %v, want nil", err)
	}
}

func TestRunSetupRequiresSessionID(t *testing.T) {
	srv := &Server{WorkspaceRoot: t.TempDir()}
	_, err := srv.RunSetup(context.Background(), runSetupReq("", nil, "true"))
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("RunSetup without a session id = %v, want InvalidArgument", err)
	}
}

func TestRunSetupRequiresWorkspaceRoot(t *testing.T) {
	srv := &Server{}
	_, err := srv.RunSetup(context.Background(), runSetupReq("sess-1", nil, "true"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("RunSetup without a workspace root = %v, want FailedPrecondition", err)
	}
}

func TestRunSetupFailingCommandIsRejected(t *testing.T) {
	srv := &Server{WorkspaceRoot: t.TempDir()}
	_, err := srv.RunSetup(context.Background(), runSetupReq("sess-1", nil, "exit 3"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("RunSetup with a failing command = %v, want FailedPrecondition", err)
	}
}

func TestRunSetupAggregateTimeoutFails(t *testing.T) {
	// §5.1: a setupPolicy with on_timeout "fail" aborts the phase when
	// the aggregate cap is exceeded. Threading the policy through the
	// RunSetup RPC is what this exercises.
	srv := &Server{WorkspaceRoot: t.TempDir()}
	policy := &adapterv1.SetupPolicy{TimeoutSeconds: 1, OnTimeout: "fail"}
	_, err := srv.RunSetup(context.Background(), runSetupReq("sess-1", policy, "sleep 30"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("RunSetup over the aggregate cap = %v, want FailedPrecondition", err)
	}
}

// spec: §5.1 lines 89-91 — onTimeout `warn` proceeds past the aggregate
// cap rather than failing pod startup, and the §7.5 observability
// contract requires a structured signal (operator-visible warning) so
// "setup succeeded" and "setup truncated under warn" do not look
// identical. The RPC reports success; the adapter logs a
// `setup_aggregate_timeout_warn` line carrying the session id, cap, and
// command count. F-7.5.13.
func TestRunSetupAggregateTimeoutWarnProceeds(t *testing.T) {
	srv := &Server{WorkspaceRoot: t.TempDir()}
	policy := &adapterv1.SetupPolicy{TimeoutSeconds: 1, OnTimeout: "warn"}

	logBuf, restore := captureLogOutput(t)
	defer restore()

	if _, err := srv.RunSetup(context.Background(), runSetupReq("sess-1", policy, "sleep 30")); err != nil {
		t.Errorf("RunSetup over the cap with the warn disposition = %v, want nil", err)
	}
	got := logBuf.String()
	if !strings.Contains(got, "setup_aggregate_timeout_warn") || !strings.Contains(got, "session=sess-1") {
		t.Errorf("warn disposition should emit the setup_aggregate_timeout_warn observability line; got log: %q", got)
	}
}
