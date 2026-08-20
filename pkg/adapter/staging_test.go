// SPDX-License-Identifier: MIT

package adapter

import (
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
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
	srv := &Server{WorkspaceBase: root}
	if _, err := srv.FinalizeWorkspace(context.Background(), finalizeReq("sess-1",
		wsSource("mkdir", "docs", "", "755"),
		wsSource("inlineFile", "docs/readme.md", "hello", "644"))); err != nil {
		t.Fatalf("FinalizeWorkspace: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(slotCurrent(srv, "sess-1"), "docs", "readme.md"))
	if err != nil {
		t.Fatalf("read materialized file: %v", err)
	}
	if string(b) != "hello" {
		t.Errorf("readme.md = %q, want hello", b)
	}
}

func TestFinalizeWorkspaceEmptyPlan(t *testing.T) {
	// A session with an empty workspace plan finalizes as a no-op.
	srv := &Server{WorkspaceBase: t.TempDir()}
	if _, err := srv.FinalizeWorkspace(context.Background(), finalizeReq("sess-1")); err != nil {
		t.Errorf("FinalizeWorkspace with an empty plan = %v, want nil", err)
	}
}

func TestFinalizeWorkspaceRequiresSessionID(t *testing.T) {
	srv := &Server{WorkspaceBase: t.TempDir()}
	_, err := srv.FinalizeWorkspace(context.Background(),
		finalizeReq("", wsSource("mkdir", "x", "", "")))
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("FinalizeWorkspace without a session id = %v, want InvalidArgument", err)
	}
}

func TestFinalizeWorkspaceRequiresWorkspaceBase(t *testing.T) {
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
	srv := &Server{WorkspaceBase: t.TempDir()}
	_, err := srv.FinalizeWorkspace(context.Background(),
		finalizeReq("sess-1", wsSource("inlineFile", "../escape.txt", "x", "644")))
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("FinalizeWorkspace with an escaping path = %v, want InvalidArgument", err)
	}
}

// TestFinalizeWorkspacePlumsArchivePolicy covers F-7.4.4: the
// FinalizeWorkspaceRequest.archive_policy.allow_symlinks toggle lifts
// the §7.4 default-deny on symlink sources. The gateway extracts
// uploadArchive sources and rewrites their symlink entries into `symlink`
// sources (§7.4 — the pod never decompresses); the adapter
// honors the same per-Runtime policy when it materializes them. With the
// gateway-supplied policy set to AllowSymlinks=true and a target inside
// the workspace the link is created; without it the same source is
// rejected. spec: §7.4; §13.4 — F-7.4.4, F-7.4.1.
func TestFinalizeWorkspacePlumsArchivePolicy(t *testing.T) {
	srv := &Server{WorkspaceBase: t.TempDir()}

	sources := []*adapterv1.WorkspaceSource{
		wsSource("inlineFile", "target.txt", "hi", "644"),
		{Type: "symlink", Path: "link", LinkTarget: "target.txt"},
	}
	reqWithSymlinkOptIn := &adapterv1.FinalizeWorkspaceRequest{
		SessionId:     &adapterv1.SessionId{Value: "sess-1"},
		WorkspacePlan: &adapterv1.WorkspacePlan{SchemaVersion: 1, Sources: sources},
		ArchivePolicy: &adapterv1.ArchivePolicy{AllowSymlinks: true, WorkspaceRoot: slotCurrent(srv, "sess-1")},
	}
	if _, err := srv.FinalizeWorkspace(context.Background(), reqWithSymlinkOptIn); err != nil {
		t.Fatalf("FinalizeWorkspace with AllowSymlinks=true: %v", err)
	}
	if info, err := os.Lstat(filepath.Join(slotCurrent(srv, "sess-1"), "link")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected symlink to be created; Lstat = %v %v", info, err)
	}

	// Default policy (nil) must reject the same symlink source with
	// InvalidArgument (the gRPC mapping of workspace.Materialize errors).
	srv2 := &Server{WorkspaceBase: t.TempDir()}
	reqDefault := &adapterv1.FinalizeWorkspaceRequest{
		SessionId:     &adapterv1.SessionId{Value: "sess-1"},
		WorkspacePlan: &adapterv1.WorkspacePlan{SchemaVersion: 1, Sources: sources},
	}
	_, err := srv2.FinalizeWorkspace(context.Background(), reqDefault)
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("FinalizeWorkspace with default policy and a symlink source = %v, want InvalidArgument", err)
	}
}

// spec: §14.1 — the adapter is a live consumer that MUST reject
// a plan whose schemaVersion exceeds the known revision before touching
// the filesystem, and surface the typed WORKSPACE_PLAN_SCHEMA_UNSUPPORTED
// code. A source that would write a file proves the reject happens before
// materialization. F-14.1.3.
func TestFinalizeWorkspaceRejectsUnsupportedSchemaVersion_spec_14_1_326(t *testing.T) {
	root := t.TempDir()
	srv := &Server{WorkspaceBase: root}
	req := &adapterv1.FinalizeWorkspaceRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		WorkspacePlan: &adapterv1.WorkspacePlan{
			SchemaVersion: workspace.MaxKnownSchemaVersion + 1,
			Sources:       []*adapterv1.WorkspaceSource{wsSource("inlineFile", "written.txt", "x", "644")},
		},
	}
	_, err := srv.FinalizeWorkspace(context.Background(), req)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("FinalizeWorkspace with a future schemaVersion = %v, want FailedPrecondition", err)
	}
	// The status carries the typed §15.1 error code so the gateway can
	// map it to the 422 envelope.
	st, _ := status.FromError(err)
	var code adapterv1.Error_ErrorCode
	for _, d := range st.Details() {
		if e, ok := d.(*adapterv1.Error); ok {
			code = e.GetCode()
		}
	}
	if code != adapterv1.Error_ERROR_CODE_WORKSPACE_PLAN_SCHEMA_UNSUPPORTED {
		t.Errorf("status error code detail = %v, want WORKSPACE_PLAN_SCHEMA_UNSUPPORTED", code)
	}
	// The reject must happen before any filesystem write.
	if _, statErr := os.Stat(filepath.Join(slotCurrent(srv, "sess-1"), "written.txt")); statErr == nil {
		t.Error("materialization wrote a file despite the schemaVersion reject; the gate ran too late")
	}
}

// spec: §14 — an unknown source.type is skipped with a
// workspace_plan_unknown_source_type warning the adapter returns on
// FinalizeWorkspaceResponse, carrying `unknownType` and the plan's
// `schemaVersion`. F-14.1.2.
func TestFinalizeWorkspaceSkipsUnknownSourceType_spec_14_334(t *testing.T) {
	srv := &Server{WorkspaceBase: t.TempDir()}
	resp, err := srv.FinalizeWorkspace(context.Background(),
		finalizeReq("sess-1", wsSource("teleport", "x", "", "")))
	if err != nil {
		t.Fatalf("FinalizeWorkspace must skip an unknown source type, got error: %v", err)
	}
	warns := resp.GetWorkspacePlanWarnings()
	if len(warns) != 1 {
		t.Fatalf("warnings: got %d, want 1: %+v", len(warns), warns)
	}
	w := warns[0]
	if w.GetCode() != "workspace_plan_unknown_source_type" {
		t.Errorf("warning code: got %q, want workspace_plan_unknown_source_type", w.GetCode())
	}
	if w.GetUnknownType() != "teleport" {
		t.Errorf("warning unknownType: got %q, want teleport", w.GetUnknownType())
	}
	if w.GetSchemaVersion() != 1 {
		t.Errorf("warning schemaVersion: got %d, want 1 (the plan's version)", w.GetSchemaVersion())
	}
}

// spec: §14 — when two sources resolve to the same workspace
// path the adapter raises a workspace_plan_path_collision warning on
// FinalizeWorkspaceResponse carrying `path`, `winningSourceIndex`, and
// `losingSourceIndex` so the gateway can republish them. F-14.1.9.
func TestFinalizeWorkspaceTranscribesPathCollision_spec_14_338(t *testing.T) {
	srv := &Server{WorkspaceBase: t.TempDir()}
	resp, err := srv.FinalizeWorkspace(context.Background(), finalizeReq("sess-1",
		wsSource("inlineFile", "conf/app.yaml", "first", "644"),
		wsSource("inlineFile", "conf/app.yaml", "second", "644")))
	if err != nil {
		t.Fatalf("FinalizeWorkspace: %v", err)
	}
	var coll *adapterv1.WorkspacePlanWarning
	for _, w := range resp.GetWorkspacePlanWarnings() {
		if w.GetCode() == "workspace_plan_path_collision" {
			coll = w
		}
	}
	if coll == nil {
		t.Fatalf("no path-collision warning transcribed: %+v", resp.GetWorkspacePlanWarnings())
	}
	if coll.GetPath() != "conf/app.yaml" {
		t.Errorf("path = %q, want conf/app.yaml", coll.GetPath())
	}
	if coll.GetWinningSourceIndex() != 1 || coll.GetLosingSourceIndex() != 0 {
		t.Errorf("winning/losing = %d/%d, want 1/0", coll.GetWinningSourceIndex(), coll.GetLosingSourceIndex())
	}
	// The proto source_index mirrors the winning index for back-compat
	// single-index consumers.
	if coll.GetSourceIndex() != coll.GetWinningSourceIndex() {
		t.Errorf("source_index = %d, want = winning_source_index %d", coll.GetSourceIndex(), coll.GetWinningSourceIndex())
	}
}

func TestRunSetupExecutesCommands(t *testing.T) {
	root := t.TempDir()
	srv := &Server{WorkspaceBase: root}
	if _, err := srv.RunSetup(context.Background(),
		runSetupReq("sess-1", nil, "echo ok > result.txt", "mkdir sub")); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(slotCurrent(srv, "sess-1"), "result.txt"))
	if err != nil {
		t.Fatalf("read result.txt: %v", err)
	}
	if strings.TrimSpace(string(b)) != "ok" {
		t.Errorf("result.txt = %q, want ok", b)
	}
	if fi, err := os.Stat(filepath.Join(slotCurrent(srv, "sess-1"), "sub")); err != nil || !fi.IsDir() {
		t.Errorf("setup command did not create the sub directory: %v", err)
	}
}

func TestRunSetupNoCommands(t *testing.T) {
	// The §4.7 sequence calls RunSetup even for a plan with no setup
	// commands; an empty list completes the phase as a no-op.
	srv := &Server{WorkspaceBase: t.TempDir()}
	if _, err := srv.RunSetup(context.Background(), runSetupReq("sess-1", nil)); err != nil {
		t.Errorf("RunSetup with no commands = %v, want nil", err)
	}
}

func TestRunSetupRequiresSessionID(t *testing.T) {
	srv := &Server{WorkspaceBase: t.TempDir()}
	_, err := srv.RunSetup(context.Background(), runSetupReq("", nil, "true"))
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("RunSetup without a session id = %v, want InvalidArgument", err)
	}
}

func TestRunSetupRequiresWorkspaceBase(t *testing.T) {
	srv := &Server{}
	_, err := srv.RunSetup(context.Background(), runSetupReq("sess-1", nil, "true"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("RunSetup without a workspace root = %v, want FailedPrecondition", err)
	}
}

func TestRunSetupFailingCommandIsRejected(t *testing.T) {
	srv := &Server{WorkspaceBase: t.TempDir()}
	_, err := srv.RunSetup(context.Background(), runSetupReq("sess-1", nil, "exit 3"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("RunSetup with a failing command = %v, want FailedPrecondition", err)
	}
}

func TestRunSetupAggregateTimeoutFails(t *testing.T) {
	// §5.1: a setupPolicy with on_timeout "fail" aborts the phase when
	// the aggregate cap is exceeded. Threading the policy through the
	// RunSetup RPC is what this exercises.
	srv := &Server{WorkspaceBase: t.TempDir()}
	policy := &adapterv1.SetupPolicy{TimeoutSeconds: 1, OnTimeout: "fail"}
	_, err := srv.RunSetup(context.Background(), runSetupReq("sess-1", policy, "sleep 30"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("RunSetup over the aggregate cap = %v, want FailedPrecondition", err)
	}
}

// spec: §5.1 — onTimeout `warn` proceeds past the aggregate
// cap rather than failing pod startup, and the §7.5 observability
// contract requires a structured signal (operator-visible warning) so
// "setup succeeded" and "setup truncated under warn" do not look
// identical. The RPC reports success; the adapter logs a
// `setup_aggregate_timeout_warn` line carrying the session id, cap, and
// command count. F-7.5.13.
func TestRunSetupAggregateTimeoutWarnProceeds(t *testing.T) {
	srv := &Server{WorkspaceBase: t.TempDir()}
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

// slotCurrent is the session's own §6.4 cwd,
// `<base>/slots/{sessionId}/current`, which is the only workspace layout
// and the directory the workspace-prep RPCs materialize into.
func slotCurrent(srv *Server, sessionID string) string {
	return filepath.Join(srv.WorkspaceBase, "slots", sessionID, "current")
}

// prepareWorkspaceStreamStub feeds a fixed sequence of frames to the
// PrepareWorkspace handler in-process. The embedded grpc.ServerStream
// supplies the header/trailer methods the generated stream interface
// requires; the handler calls none of them.
type prepareWorkspaceStreamStub struct {
	grpc.ServerStream
	ctx    context.Context
	frames []*adapterv1.PrepareWorkspaceRequest
	next   int
	resp   *adapterv1.PrepareWorkspaceResponse
}

func (s *prepareWorkspaceStreamStub) Context() context.Context { return s.ctx }

func (s *prepareWorkspaceStreamStub) Recv() (*adapterv1.PrepareWorkspaceRequest, error) {
	if s.next >= len(s.frames) {
		return nil, io.EOF
	}
	f := s.frames[s.next]
	s.next++
	return f, nil
}

func (s *prepareWorkspaceStreamStub) SendAndClose(resp *adapterv1.PrepareWorkspaceResponse) error {
	s.resp = resp
	return nil
}

func uploadFrame(sessionID, ref, chunk string) *adapterv1.PrepareWorkspaceRequest {
	return &adapterv1.PrepareWorkspaceRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		UploadRef: ref,
		Chunk:     []byte(chunk),
	}
}

// TestPrepareWorkspaceStagesUnderTheSessionSlotTree asserts that an
// upload stages into the named session's
// /workspace/slots/{sessionId}/staging directory, which is the only
// staging layout: no upload lands in a pod-global /workspace/staging.
// spec: §6.4 (per-session workspace tree), §4.2 (session-addressed
// adapter requests).
func TestPrepareWorkspaceStagesUnderTheSessionSlotTree(t *testing.T) {
	base := t.TempDir()
	srv := &Server{WorkspaceBase: base}
	stream := &prepareWorkspaceStreamStub{
		ctx:    context.Background(),
		frames: []*adapterv1.PrepareWorkspaceRequest{uploadFrame("sess-1", "lenny-blob://t/a", "hello")},
	}
	if err := srv.PrepareWorkspace(stream); err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}
	if stream.resp.GetStagedBytes() != int64(len("hello")) {
		t.Errorf("stagedBytes = %d, want %d", stream.resp.GetStagedBytes(), len("hello"))
	}
	slotStaging := filepath.Join(base, "slots", "sess-1", "staging")
	entries, err := os.ReadDir(slotStaging)
	if err != nil {
		t.Fatalf("read per-session staging dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("per-session staging holds %d entries, want 1", len(entries))
	}
	if _, err := os.Stat(filepath.Join(base, "staging")); !os.IsNotExist(err) {
		t.Errorf("pod-global /workspace/staging exists (%v); the per-session tree is the only layout", err)
	}
}

// TestPrepareWorkspaceRefusesAnUnresolvableStagingPath asserts that an
// adapter with no workspace base refuses the upload with
// FailedPrecondition rather than writing the staged file into its own
// working directory. spec: §6.4.
func TestPrepareWorkspaceRefusesAnUnresolvableStagingPath(t *testing.T) {
	srv := &Server{}
	stream := &prepareWorkspaceStreamStub{
		ctx:    context.Background(),
		frames: []*adapterv1.PrepareWorkspaceRequest{uploadFrame("sess-1", "lenny-blob://t/a", "hello")},
	}
	err := srv.PrepareWorkspace(stream)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("PrepareWorkspace without a workspace base = %v, want FailedPrecondition", err)
	}
}

// TestPrepareWorkspaceRequiresASessionID asserts that a frame carrying no
// session id is refused: absence of the address is an error rather than a
// scope. spec: §4.2.
func TestPrepareWorkspaceRequiresASessionID(t *testing.T) {
	srv := &Server{WorkspaceBase: t.TempDir()}
	stream := &prepareWorkspaceStreamStub{
		ctx:    context.Background(),
		frames: []*adapterv1.PrepareWorkspaceRequest{uploadFrame("", "lenny-blob://t/a", "hello")},
	}
	err := srv.PrepareWorkspace(stream)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("PrepareWorkspace with an empty session id = %v, want InvalidArgument", err)
	}
}
