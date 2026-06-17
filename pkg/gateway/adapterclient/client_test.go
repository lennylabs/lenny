// SPDX-License-Identifier: MIT

package adapterclient_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// fakeRuntime records what the adapter asks of the pod's runtime.
type fakeRuntime struct {
	started         string
	envelopes       [][]byte
	interrupted     bool
	interruptedHard bool
	closed          bool
	output          chan []byte // when set, Output returns it
}

func (f *fakeRuntime) Start(_ context.Context, sessionID string) error {
	f.started = sessionID
	return nil
}

func (f *fakeRuntime) WriteEnvelope(_ string, envelope []byte) error {
	f.envelopes = append(f.envelopes, envelope)
	return nil
}

func (f *fakeRuntime) Output(_ context.Context, _ string) (<-chan []byte, error) {
	if f.output != nil {
		return f.output, nil
	}
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func (f *fakeRuntime) Interrupt(_ context.Context, _ string, hard bool) error {
	f.interruptedHard = hard
	f.interrupted = true
	return nil
}

func (f *fakeRuntime) Close(_ context.Context, _ string) error {
	f.closed = true
	return nil
}

// dialAdapter serves srv over an in-memory connection and returns a
// Client wired to it.
func dialAdapter(t *testing.T, srv *adapter.Server) *adapterclient.Client {
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

func TestNegotiateVersionSelectsACommonVersion(t *testing.T) {
	cl := dialAdapter(t, adapter.New("adapter-test-build"))

	resp, err := cl.NegotiateVersion(context.Background(), []string{adapter.ProtocolVersionV1})
	if err != nil {
		t.Fatalf("NegotiateVersion: %v", err)
	}
	if resp.GetIncompatible() {
		t.Error("negotiation reported incompatible for a shared version")
	}
	if resp.GetSelectedProtocolVersion() != adapter.ProtocolVersionV1 {
		t.Errorf("selected version = %q, want %q", resp.GetSelectedProtocolVersion(), adapter.ProtocolVersionV1)
	}
	if resp.GetAdapterVersion() != "adapter-test-build" {
		t.Errorf("adapter version = %q, want adapter-test-build", resp.GetAdapterVersion())
	}
}

func TestNegotiateVersionReportsIncompatibleWhenNoVersionShared(t *testing.T) {
	cl := dialAdapter(t, adapter.New("adapter-test-build"))

	resp, err := cl.NegotiateVersion(context.Background(), []string{"9.9.9"})
	if err != nil {
		t.Fatalf("NegotiateVersion: %v", err)
	}
	if !resp.GetIncompatible() {
		t.Error("negotiation did not report incompatible for a disjoint version set")
	}
}

func TestStartSessionWritesManifest(t *testing.T) {
	// §15.4 / §8.3: StartSession writes the adapter manifest carrying
	// the session's experimentContext and tracingContext end to end.
	manifestDir := t.TempDir()
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.ManifestDir = manifestDir
	srv.Runtime = &fakeRuntime{}
	cl := dialAdapter(t, srv)

	err := cl.StartSession(context.Background(), adapterclient.StartSessionParams{
		SessionID:          "sess-m",
		Runtime:            "claude-code",
		ExperimentContext:  &adapterv1.ExperimentContext{ExperimentId: "exp_1", VariantId: "treatment", Inherited: true},
		TracingContext:     map[string]string{"langsmith_run_id": "run_9"},
		AgentInterface:     []byte(`{"description":"analyzes codebases"}`),
		MinPlatformVersion: "1.4.0",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(manifestDir, adapter.ManifestFilename))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m adapter.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if m.SessionID != "sess-m" {
		t.Errorf("manifest sessionId = %q, want sess-m", m.SessionID)
	}
	// §4.7: in session mode the manifest taskId defaults to the session id.
	if m.TaskID != "sess-m" {
		t.Errorf("manifest taskId = %q, want sess-m (session-mode default)", m.TaskID)
	}
	if m.ExperimentContext == nil || m.ExperimentContext.ExperimentID != "exp_1" ||
		m.ExperimentContext.VariantID != "treatment" || !m.ExperimentContext.Inherited {
		t.Errorf("manifest experimentContext = %+v, want exp_1/treatment inherited", m.ExperimentContext)
	}
	if m.TracingContext["langsmith_run_id"] != "run_9" {
		t.Errorf("manifest tracingContext = %v, want the langsmith run id", m.TracingContext)
	}
	// §4.7: agentInterface is carried through (the manifest is pretty-printed,
	// so compare the decoded value rather than the bytes).
	var ai struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(m.AgentInterface, &ai); err != nil || ai.Description != "analyzes codebases" {
		t.Errorf("manifest agentInterface = %s (err %v), want the runtime descriptor", m.AgentInterface, err)
	}
	if m.MinPlatformVersion != "1.4.0" {
		t.Errorf("manifest minPlatformVersion = %q, want 1.4.0", m.MinPlatformVersion)
	}
}

func TestPrepareWorkspaceStagesUploads(t *testing.T) {
	// §4.7: PrepareWorkspace streams uploaded files into the staging
	// area, keyed by upload_ref.
	stagingDir := t.TempDir()
	srv := adapter.New("adapter-test-build")
	srv.StagingDir = stagingDir
	cl := dialAdapter(t, srv)

	uploads := map[string][]byte{
		"upload-a": []byte("alpha content"),
		"upload-b": []byte("beta"),
	}
	resp, err := cl.PrepareWorkspace(context.Background(), "sess-1", uploads)
	if err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}
	if resp.GetStagedFiles() != 2 {
		t.Errorf("stagedFiles = %d, want 2", resp.GetStagedFiles())
	}
	wantBytes := int64(len("alpha content") + len("beta"))
	if resp.GetStagedBytes() != wantBytes {
		t.Errorf("stagedBytes = %d, want %d", resp.GetStagedBytes(), wantBytes)
	}
	for ref, want := range uploads {
		stagedAt, err := workspace.StagingPath(stagingDir, ref)
		if err != nil {
			t.Fatalf("StagingPath(%q): %v", ref, err)
		}
		got, err := os.ReadFile(stagedAt)
		if err != nil {
			t.Fatalf("read staged %s: %v", ref, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("staged %s = %q, want %q", ref, got, want)
		}
	}
}

func TestPrepareWorkspaceConcatenatesChunks(t *testing.T) {
	// An upload larger than the client chunk size arrives as multiple
	// frames; the adapter concatenates them in order into one file.
	stagingDir := t.TempDir()
	srv := adapter.New("adapter-test-build")
	srv.StagingDir = stagingDir
	cl := dialAdapter(t, srv)

	large := bytes.Repeat([]byte("lenny"), 40_000) // 200 KB, several frames
	resp, err := cl.PrepareWorkspace(context.Background(), "sess-1",
		map[string][]byte{"big": large})
	if err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}
	if resp.GetStagedBytes() != int64(len(large)) {
		t.Errorf("stagedBytes = %d, want %d", resp.GetStagedBytes(), len(large))
	}
	stagedAt, err := workspace.StagingPath(stagingDir, "big")
	if err != nil {
		t.Fatalf("StagingPath: %v", err)
	}
	got, err := os.ReadFile(stagedAt)
	if err != nil {
		t.Fatalf("read staged file: %v", err)
	}
	if !bytes.Equal(got, large) {
		t.Errorf("staged file is %d bytes, want %d (chunk reassembly mismatch)",
			len(got), len(large))
	}
}

func TestPrepareWorkspaceWithoutStagingDir(t *testing.T) {
	srv := adapter.New("adapter-test-build") // no StagingDir configured
	cl := dialAdapter(t, srv)
	_, err := cl.PrepareWorkspace(context.Background(), "sess-1",
		map[string][]byte{"x": []byte("y")})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("PrepareWorkspace without a staging dir = %v, want FailedPrecondition", err)
	}
}

func TestPrepareWorkspaceRejectsEmptyRef(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.StagingDir = t.TempDir()
	cl := dialAdapter(t, srv)
	_, err := cl.PrepareWorkspace(context.Background(), "sess-1",
		map[string][]byte{"": []byte("payload")})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("PrepareWorkspace with an empty upload ref = %v, want InvalidArgument", err)
	}
}

func TestClientFinalizeWorkspace(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	root := t.TempDir()
	srv.WorkspaceRoot = root
	cl := dialAdapter(t, srv)

	if _, err := cl.FinalizeWorkspace(context.Background(), "sess-1", &adapterv1.WorkspacePlan{
		SchemaVersion: 1,
		Sources: []*adapterv1.WorkspaceSource{
			{Type: "inlineFile", Path: "CLAUDE.md", Content: "notes", Mode: "644"},
		},
	}, nil, false); err != nil {
		t.Fatalf("FinalizeWorkspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Errorf("FinalizeWorkspace did not materialize the workspace: %v", err)
	}
}

func TestClientFinalizeWorkspaceRejectsBadPlan(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	cl := dialAdapter(t, srv)

	_, err := cl.FinalizeWorkspace(context.Background(), "sess-1", &adapterv1.WorkspacePlan{
		SchemaVersion: 1,
		Sources: []*adapterv1.WorkspaceSource{
			{Type: "inlineFile", Path: "../escape", Content: "x", Mode: "644"},
		},
	}, nil, false)
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("FinalizeWorkspace with an escaping path = %v, want InvalidArgument", err)
	}
}

func TestClientRunSetup(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	root := t.TempDir()
	srv.WorkspaceRoot = root
	cl := dialAdapter(t, srv)

	outputs, err := cl.RunSetup(context.Background(), "sess-1", []*adapterv1.SetupCommand{
		{Cmd: "touch setup.done", TimeoutSeconds: 30},
	}, &adapterv1.SetupPolicy{Shell: true})
	if err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "setup.done")); err != nil {
		t.Errorf("RunSetup did not run the setup command: %v", err)
	}
	// spec: §7.5 line 475 — every executed command must surface a record
	// on RunSetupResponse.outputs so the gateway can persist the trail.
	// F-7.5.4.
	if len(outputs) != 1 || outputs[0].GetCmd() != "touch setup.done" {
		t.Errorf("RunSetup outputs = %+v, want one entry for touch setup.done", outputs)
	}
}

func TestClientRunSetupFailingCommand(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	cl := dialAdapter(t, srv)

	outputs, err := cl.RunSetup(context.Background(), "sess-1",
		[]*adapterv1.SetupCommand{{Cmd: "exit 7"}}, &adapterv1.SetupPolicy{Shell: true})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("RunSetup with a failing command = %v, want FailedPrecondition", err)
	}
	// spec: §7.5 line 475 / §7.5 line 488 — partial outputs survive the
	// failure so the gateway can persist them and surface the rejection
	// reason. F-7.5.4.
	if len(outputs) == 0 {
		t.Error("RunSetup with a failing command should surface partial outputs in status details")
	}
	if len(outputs) > 0 && outputs[0].GetExitCode() == 0 {
		t.Errorf("failing command should report a non-zero exit code, got %d", outputs[0].GetExitCode())
	}
}

func TestClientAssignCredentials(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	credDir := t.TempDir()
	srv.CredentialsDir = credDir
	cl := dialAdapter(t, srv)

	err := cl.AssignCredentials(context.Background(), "sess-1",
		map[string]*adapterv1.CredentialLease{
			"anthropic_direct": {
				LeaseId:  "cl-1",
				Provider: "anthropic_direct",
				Payload: []byte(`{"deliveryMode":"proxy",` +
					`"materializedConfig":{"proxyUrl":"https://p/v1","leaseToken":"lt-x"}}`),
			},
		})
	if err != nil {
		t.Fatalf("AssignCredentials: %v", err)
	}

	// The adapter materialized the lease into the runtime credential file.
	data, err := os.ReadFile(filepath.Join(credDir, "credentials.json"))
	if err != nil {
		t.Fatalf("read credential file: %v", err)
	}
	var doc struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode credential file: %v", err)
	}
	if len(doc.Providers) != 1 || doc.Providers[0]["leaseId"] != "cl-1" {
		t.Errorf("credential file providers = %v, want one entry for lease cl-1", doc.Providers)
	}
}

func TestClientAssignCredentialsEmptyMapIsAccepted(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.CredentialsDir = t.TempDir()
	cl := dialAdapter(t, srv)

	// A session that needs no upstream credentials assigns an empty set.
	if err := cl.AssignCredentials(context.Background(), "sess-1", nil); err != nil {
		t.Fatalf("AssignCredentials with no leases: %v", err)
	}
}

func TestClientAssignCredentialsRejectsEmptySessionID(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.CredentialsDir = t.TempDir()
	cl := dialAdapter(t, srv)

	err := cl.AssignCredentials(context.Background(), "", nil)
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("AssignCredentials with no session id = %v, want InvalidArgument", err)
	}
}

func TestClientAssignCredentialsRejectsUnconfiguredAdapter(t *testing.T) {
	srv := adapter.New("adapter-test-build") // no CredentialsDir
	cl := dialAdapter(t, srv)

	err := cl.AssignCredentials(context.Background(), "sess-1", nil)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("AssignCredentials on an adapter with no credentials dir = %v, want FailedPrecondition", err)
	}
}

func TestSessionRoundTrip(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt
	cl := dialAdapter(t, srv)
	ctx := context.Background()

	if err := cl.StartSession(ctx, adapterclient.StartSessionParams{SessionID: "sess-x", Runtime: "claude-code"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if rt.started != "sess-x" {
		t.Errorf("runtime started for %q, want sess-x", rt.started)
	}

	envelope := []byte(`{"type":"user","content":"hello"}`)
	if err := cl.SendMessage(ctx, "sess-x", envelope); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if len(rt.envelopes) != 1 || string(rt.envelopes[0]) != string(envelope) {
		t.Errorf("runtime received %v, want one copy of the envelope", rt.envelopes)
	}

	clean, err := cl.Shutdown(ctx, "sess-x")
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !clean {
		t.Error("Shutdown reported an unclean exit for a clean runtime close")
	}
	if !rt.closed {
		t.Error("the runtime was not closed on Shutdown")
	}
}

func TestInterruptDeliversTheSignalToTheRuntime(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt
	cl := dialAdapter(t, srv)
	ctx := context.Background()

	if err := cl.StartSession(ctx, adapterclient.StartSessionParams{SessionID: "sess-x", Runtime: "claude-code"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	status, err := cl.Interrupt(ctx, "sess-x", true, 2*time.Second)
	if err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if status != adapterclient.InterruptStatusAcknowledged {
		t.Errorf("Interrupt status = %v, want STATUS_ACKNOWLEDGED", status)
	}
	if !rt.interrupted || !rt.interruptedHard {
		t.Errorf("runtime interrupted=%t hard=%t, want a hard interrupt", rt.interrupted, rt.interruptedHard)
	}
}

func TestSendMessageRejectsAnUnassignedSession(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	cl := dialAdapter(t, srv)

	// No StartSession ran, so the pod holds no session.
	err := cl.SendMessage(context.Background(), "sess-absent", []byte(`{"type":"user"}`))
	if err == nil {
		t.Error("SendMessage to an unassigned session succeeded, want a failure")
	}
}

func TestAttachStreamsRuntimeOutput(t *testing.T) {
	rt := &fakeRuntime{output: make(chan []byte, 4)}
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt
	cl := dialAdapter(t, srv)
	ctx := context.Background()

	if err := cl.StartSession(ctx, adapterclient.StartSessionParams{SessionID: "sess-x", Runtime: "claude-code"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	stream, err := cl.Attach(ctx, "sess-x")
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer func() { _ = stream.CloseSend() }()

	rt.output <- []byte(`{"type":"response","text":"hi"}`)
	got, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if string(got) != `{"type":"response","text":"hi"}` {
		t.Errorf("Recv returned %s, want the runtime output envelope", got)
	}

	// Closing the runtime output ends the stream.
	close(rt.output)
	if _, err := stream.Recv(); err != io.EOF {
		t.Errorf("Recv after the runtime output closed = %v, want io.EOF", err)
	}
}

func TestShutdownRejectsAnUnassignedSession(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	cl := dialAdapter(t, srv)

	clean, err := cl.Shutdown(context.Background(), "sess-absent")
	if err == nil {
		t.Error("Shutdown of an unassigned session succeeded, want a failure")
	}
	if clean {
		t.Error("Shutdown reported a clean exit on an error")
	}
}

// spec: §4.7 Terminate RPC — graceful shutdown carrying a reason and a
// grace deadline. The §11.4 full_revoke fan-out uses it.

// recordingAdapter is a minimal Adapter gRPC server that captures the
// ShutdownRequest and RotateCredentialsRequest so a test can assert
// what reached the wire. Every other RPC stays unimplemented.
type recordingAdapter struct {
	adapterv1.UnimplementedAdapterServer
	gotShutdown       *adapterv1.ShutdownRequest
	gotRotate         *adapterv1.RotateCredentialsRequest
	rotateErr         error
	gotSignalDeadline *adapterv1.SignalDeadlineRequest
	signalDelivered   bool
	signalErr         error
}

func (r *recordingAdapter) SignalDeadline(_ context.Context, req *adapterv1.SignalDeadlineRequest) (*adapterv1.SignalDeadlineResponse, error) {
	if r.signalErr != nil {
		return nil, r.signalErr
	}
	r.gotSignalDeadline = req
	return &adapterv1.SignalDeadlineResponse{Delivered: r.signalDelivered}, nil
}

func (r *recordingAdapter) Shutdown(_ context.Context, req *adapterv1.ShutdownRequest) (*adapterv1.ShutdownResponse, error) {
	r.gotShutdown = req
	return &adapterv1.ShutdownResponse{ExitedCleanly: true}, nil
}

func (r *recordingAdapter) RotateCredentials(_ context.Context, req *adapterv1.RotateCredentialsRequest) (*adapterv1.RotateCredentialsResponse, error) {
	if r.rotateErr != nil {
		return nil, r.rotateErr
	}
	r.gotRotate = req
	return &adapterv1.RotateCredentialsResponse{}, nil
}

// dialRecordingAdapter serves rec over an in-memory connection and
// returns a Client wired to it.
func dialRecordingAdapter(t *testing.T, rec *recordingAdapter) *adapterclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, rec)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial recording adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// spec: §11.3 line 240 — the gateway client forwards remaining_ms and
// trigger to the adapter and surfaces whether the runtime was reached.
// F-11.3.5.
func TestSignalDeadlineForwardsAndReportsDelivery_spec_11_3_240(t *testing.T) {
	rec := &recordingAdapter{signalDelivered: true}
	cl := dialRecordingAdapter(t, rec)

	delivered, err := cl.SignalDeadline(context.Background(), "sess-d", 300000, "session_age")
	if err != nil {
		t.Fatalf("SignalDeadline: %v", err)
	}
	if !delivered {
		t.Error("delivered = false, want true")
	}
	if rec.gotSignalDeadline == nil {
		t.Fatal("the adapter received no SignalDeadline request")
	}
	if got := rec.gotSignalDeadline.GetSessionId().GetValue(); got != "sess-d" {
		t.Errorf("session id = %q, want sess-d", got)
	}
	if got := rec.gotSignalDeadline.GetRemainingMs(); got != 300000 {
		t.Errorf("remaining = %d ms, want 300000", got)
	}
	if got := rec.gotSignalDeadline.GetTrigger(); got != "session_age" {
		t.Errorf("trigger = %q, want session_age", got)
	}
}

// A runtime without a lifecycle channel returns delivered=false; the wrapper
// surfaces that without error so the watchdog's best-effort warning continues.
func TestSignalDeadlineReportsNotDelivered_spec_11_3_240(t *testing.T) {
	rec := &recordingAdapter{signalDelivered: false}
	cl := dialRecordingAdapter(t, rec)

	delivered, err := cl.SignalDeadline(context.Background(), "sess-d", 300000, "session_age")
	if err != nil {
		t.Fatalf("SignalDeadline: %v", err)
	}
	if delivered {
		t.Error("delivered = true, want false for a runtime without a lifecycle channel")
	}
}

func TestSignalDeadlineSurfacesRPCError_spec_11_3_240(t *testing.T) {
	rec := &recordingAdapter{signalErr: status.Error(codes.FailedPrecondition, "no session")}
	cl := dialRecordingAdapter(t, rec)

	if _, err := cl.SignalDeadline(context.Background(), "sess-d", 300000, "session_age"); err == nil {
		t.Error("SignalDeadline succeeded, want the adapter error surfaced")
	}
}

func TestTerminateSendsReasonAndDeadline(t *testing.T) {
	rec := &recordingAdapter{}
	cl := dialRecordingAdapter(t, rec)

	clean, err := cl.Terminate(context.Background(), "sess-t", "USER_REVOKED", 10*time.Second)
	if err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if !clean {
		t.Error("Terminate reported an unclean exit for a clean runtime close")
	}
	if rec.gotShutdown == nil {
		t.Fatal("the adapter received no Shutdown request")
	}
	if got := rec.gotShutdown.GetSessionId().GetValue(); got != "sess-t" {
		t.Errorf("Shutdown session id = %q, want sess-t", got)
	}
	if got := rec.gotShutdown.GetReason(); got != "USER_REVOKED" {
		t.Errorf("Shutdown reason = %q, want USER_REVOKED", got)
	}
	if got := rec.gotShutdown.GetDeadlineMs(); got != 10_000 {
		t.Errorf("Shutdown deadline = %d ms, want 10000", got)
	}
}

func TestTerminateZeroDeadlineSendsZero(t *testing.T) {
	rec := &recordingAdapter{}
	cl := dialRecordingAdapter(t, rec)

	if _, err := cl.Terminate(context.Background(), "sess-t", "USER_REVOKED", 0); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if rec.gotShutdown.GetDeadlineMs() != 0 {
		t.Errorf("a zero deadline sent %d ms, want 0 so the adapter applies its default",
			rec.gotShutdown.GetDeadlineMs())
	}
}

func TestTerminateEndsTheSessionOnThePod(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt
	cl := dialAdapter(t, srv)
	ctx := context.Background()

	if err := cl.StartSession(ctx, adapterclient.StartSessionParams{SessionID: "sess-x", Runtime: "claude-code"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	clean, err := cl.Terminate(ctx, "sess-x", "USER_REVOKED", 10*time.Second)
	if err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if !clean {
		t.Error("Terminate reported an unclean exit for a clean runtime close")
	}
	if !rt.closed {
		t.Error("the runtime was not closed on Terminate")
	}
}

func TestTerminateRejectsAnUnassignedSession(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	cl := dialAdapter(t, srv)

	clean, err := cl.Terminate(context.Background(), "sess-absent", "USER_REVOKED", 10*time.Second)
	if err == nil {
		t.Error("Terminate of an unassigned session succeeded, want a failure")
	}
	if clean {
		t.Error("Terminate reported a clean exit on an error")
	}
}

// countingSink is an adapter.CheckpointSink that tallies the archive
// bytes it receives and returns a fixed checkpoint id.
type countingSink struct {
	id   string
	size int64
}

func (s *countingSink) SaveCheckpoint(_ context.Context, _ string, r io.Reader) (string, error) {
	n, err := io.Copy(io.Discard, r)
	s.size = n
	if err != nil {
		return "", err
	}
	return s.id, nil
}

func TestCheckpointReturnsTheStoredCheckpoint(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	sink := &countingSink{id: "ckpt-9"}
	srv.Checkpoints = sink
	cl := dialAdapter(t, srv)
	ctx := context.Background()

	if err := cl.StartSession(ctx, adapterclient.StartSessionParams{SessionID: "sess-x", Runtime: "claude-code"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	res, err := cl.Checkpoint(ctx, "sess-x", 30*time.Second)
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if res.CheckpointID != "ckpt-9" {
		t.Errorf("checkpoint id = %q, want ckpt-9", res.CheckpointID)
	}
	if res.SizeBytes <= 0 || res.SizeBytes != sink.size {
		t.Errorf("size = %d, want the archived byte count %d", res.SizeBytes, sink.size)
	}
}

func TestCheckpointRejectsAnUnassignedSession(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	srv.Checkpoints = &countingSink{id: "x"}
	cl := dialAdapter(t, srv)

	if _, err := cl.Checkpoint(context.Background(), "sess-absent", 0); err == nil {
		t.Error("Checkpoint on an unassigned session succeeded, want a failure")
	}
}

// stubCheckpointSource is an adapter.CheckpointSource serving a fixed
// checkpoint archive.
type stubCheckpointSource struct{ archive []byte }

func (s stubCheckpointSource) LoadCheckpoint(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.archive)), nil
}

func TestResumeRestoresTheWorkspace(t *testing.T) {
	// Build a one-file checkpoint bundle to restore. The adapter Resume
	// replays the bundle through workspace.ExtractTree, which routes each
	// entry by its reserved prefix; a flat archive (no workspace/ prefix)
	// extracts nothing. Bundle the workspace tree under WorkspacePrefix so
	// the entry lands at "workspace/w.txt" and restores its 8 bytes.
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "w.txt"), []byte("restored"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var archived bytes.Buffer
	if _, err := workspace.ArchiveTree(
		[]workspace.NamedRoot{{Prefix: workspace.WorkspacePrefix, Root: src}},
		&archived,
	); err != nil {
		t.Fatalf("ArchiveTree: %v", err)
	}

	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	srv.Restorer = stubCheckpointSource{archive: archived.Bytes()}
	cl := dialAdapter(t, srv)

	res, err := cl.Resume(context.Background(), adapterclient.ResumeParams{
		SessionID:    "sess-r",
		Runtime:      "echo",
		CheckpointID: "ckpt-1",
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if res.RestoredBytes != int64(len("restored")) {
		t.Errorf("restored bytes = %d, want %d", res.RestoredBytes, len("restored"))
	}
	// spec: §4.4 / §7.2 — the adapter reports mode=full when it
	// restored the named checkpoint intact. F-7.3.22.
	if res.Mode != "full" {
		t.Errorf("Mode = %q, want %q", res.Mode, "full")
	}
}

// TestResumeEchoesRecoveryGenerationAndEnforcesSizePreCheck covers
// F-7.3.22 (adapter echoes recovery_generation) and F-7.3.26 (adapter
// honours the §7.3 line 397 symmetric pre-extraction size check before
// extracting the archive). spec: §4.4 / §7.3 line 397.
func TestResumeEchoesRecoveryGenerationAndEnforcesSizePreCheck(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "w.txt"), []byte("restored"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var archived bytes.Buffer
	if _, err := workspace.Archive(src, &archived); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	srv.Restorer = stubCheckpointSource{archive: archived.Bytes()}
	cl := dialAdapter(t, srv)

	// Healthy round-trip: a configured size limit above expected bytes
	// admits the resume, and the adapter echoes recovery_generation.
	res, err := cl.Resume(context.Background(), adapterclient.ResumeParams{
		SessionID:               "sess-rg",
		Runtime:                 "echo",
		CheckpointID:            "ckpt-1",
		RecoveryGeneration:      7,
		ExpectedWorkspaceBytes:  10,
		WorkspaceSizeLimitBytes: 1024,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if res.RecoveryGeneration != 7 {
		t.Errorf("RecoveryGeneration echo = %d, want 7", res.RecoveryGeneration)
	}

	// Pre-extraction refusal: expected_bytes above the limit must
	// fail with FailedPrecondition before any extraction work begins.
	srv2 := adapter.New("adapter-test-build")
	srv2.WorkspaceRoot = t.TempDir()
	srv2.Runtime = &fakeRuntime{}
	srv2.Restorer = stubCheckpointSource{archive: archived.Bytes()}
	cl2 := dialAdapter(t, srv2)
	if _, err := cl2.Resume(context.Background(), adapterclient.ResumeParams{
		SessionID:               "sess-too-big",
		Runtime:                 "echo",
		CheckpointID:            "ckpt-1",
		ExpectedWorkspaceBytes:  2048,
		WorkspaceSizeLimitBytes: 1024,
	}); err == nil {
		t.Error("Resume with oversize expected_bytes succeeded, want pre-extraction refusal")
	}
}

func TestResumeRejectsAMissingCheckpointSource(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	// No Restorer configured: the adapter cannot restore a checkpoint.
	cl := dialAdapter(t, srv)

	if _, err := cl.Resume(context.Background(), adapterclient.ResumeParams{
		SessionID: "sess-r", Runtime: "echo", CheckpointID: "ckpt-1",
	}); err == nil {
		t.Error("Resume succeeded with no checkpoint source, want a failure")
	}
}

// fakeUsageMeter is an adapter.UsageMeter returning a fixed accounting.
type fakeUsageMeter struct {
	usage adapter.Usage
	err   error
}

func (m fakeUsageMeter) Usage(context.Context, string) (adapter.Usage, error) {
	return m.usage, m.err
}

func TestReportUsageReturnsAccounting(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	srv.Usage = fakeUsageMeter{usage: adapter.Usage{InputTokens: 1200, OutputTokens: 340, WallClockMS: 5000}}
	cl := dialAdapter(t, srv)
	ctx := context.Background()

	if err := cl.StartSession(ctx, adapterclient.StartSessionParams{SessionID: "sess-x", Runtime: "claude-code"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	rep, err := cl.ReportUsage(ctx, "sess-x")
	if err != nil {
		t.Fatalf("ReportUsage: %v", err)
	}
	if rep.InputTokens != 1200 || rep.OutputTokens != 340 || rep.WallClockMS != 5000 {
		t.Errorf("usage = %+v, want input=1200 output=340 wall=5000", rep)
	}
}

func TestReportUsageRejectsAnUnassignedSession(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	srv.Usage = fakeUsageMeter{}
	cl := dialAdapter(t, srv)

	// No StartSession ran, so the pod holds no session.
	if _, err := cl.ReportUsage(context.Background(), "sess-absent"); err == nil {
		t.Error("ReportUsage on an unassigned session succeeded, want a failure")
	}
}

func TestReportUsageRejectsAMissingMeter(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	// No Usage meter configured: the adapter cannot report usage.
	cl := dialAdapter(t, srv)
	ctx := context.Background()

	if err := cl.StartSession(ctx, adapterclient.StartSessionParams{SessionID: "sess-x", Runtime: "claude-code"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if _, err := cl.ReportUsage(ctx, "sess-x"); err == nil {
		t.Error("ReportUsage with no usage meter succeeded, want a failure")
	}
}

// TestReportUsageForLeaseRejectsProxyMode_Spec4_9_1468 confirms the
// proxy-mode-safe wrapper refuses pod-reported counts; the §4.9 LLM
// proxy is the authoritative counter for these sessions.
// spec: spec/04_system-components.md line 1468.
func TestReportUsageForLeaseRejectsProxyMode_Spec4_9_1468(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	srv.Usage = fakeUsageMeter{usage: adapter.Usage{InputTokens: 1, OutputTokens: 1}}
	cl := dialAdapter(t, srv)
	ctx := context.Background()
	if err := cl.StartSession(ctx, adapterclient.StartSessionParams{SessionID: "sess-x", Runtime: "claude-code"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	if _, err := cl.ReportUsageForLease(ctx, "sess-x", credential.DeliveryProxy); !errors.Is(err, adapterclient.ErrUsageReportProxyMode) {
		t.Errorf("ReportUsageForLease proxy-mode err = %v, want ErrUsageReportProxyMode", err)
	}
}

// TestReportUsageForLeaseAcceptsDirectMode_Spec4_9_1468 confirms the
// wrapper falls through to the underlying RPC for direct-mode leases.
// spec: spec/04_system-components.md line 1468.
func TestReportUsageForLeaseAcceptsDirectMode_Spec4_9_1468(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	srv.Usage = fakeUsageMeter{usage: adapter.Usage{InputTokens: 11, OutputTokens: 22}}
	cl := dialAdapter(t, srv)
	ctx := context.Background()
	if err := cl.StartSession(ctx, adapterclient.StartSessionParams{SessionID: "sess-x", Runtime: "claude-code"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	rep, err := cl.ReportUsageForLease(ctx, "sess-x", credential.DeliveryDirect)
	if err != nil {
		t.Fatalf("ReportUsageForLease direct-mode: %v", err)
	}
	if rep.InputTokens != 11 || rep.OutputTokens != 22 {
		t.Errorf("ReportUsageForLease direct usage = %+v, want input=11 output=22", rep)
	}
}

// spec: §4.9 RotateCredentials RPC — the gateway-side driver for hot
// credential rotation. The §4.9 Proactive Lease Renewal loop, the
// Fallback Flow, and Emergency Credential Revocation all push a rotated
// lease to a pod through it.

func TestRotateCredentialsSendsSessionAndLeases(t *testing.T) {
	rec := &recordingAdapter{}
	cl := dialRecordingAdapter(t, rec)

	leases := map[string]*adapterv1.CredentialLease{
		"anthropic_direct": {
			LeaseId:  "cl-rotated",
			Provider: "anthropic_direct",
			Payload: []byte(`{"deliveryMode":"proxy",` +
				`"materializedConfig":{"proxyUrl":"https://p/v1","leaseToken":"lt-new"}}`),
		},
	}
	if err := cl.RotateCredentials(context.Background(), "sess-rot", leases, credential.TriggerProactiveRenewal); err != nil {
		t.Fatalf("RotateCredentials: %v", err)
	}
	if rec.gotRotate == nil {
		t.Fatal("the adapter received no RotateCredentials request")
	}
	if got := rec.gotRotate.GetSessionId().GetValue(); got != "sess-rot" {
		t.Errorf("RotateCredentials session id = %q, want sess-rot", got)
	}
	// spec: §4.9 line 1413 — the rotationTrigger rides the RPC. F-13.3.10.
	if got := rec.gotRotate.GetRotationTrigger(); got != "proactive_renewal" {
		t.Errorf("RotateCredentials rotation_trigger = %q, want proactive_renewal", got)
	}
	got, ok := rec.gotRotate.GetLeases()["anthropic_direct"]
	if !ok {
		t.Fatalf("RotateCredentials leases = %v, want an anthropic_direct entry", rec.gotRotate.GetLeases())
	}
	if got.GetLeaseId() != "cl-rotated" {
		t.Errorf("rotated lease id = %q, want cl-rotated", got.GetLeaseId())
	}
}

// spec: §4.9 line 1413 / §4.7 line 822 — a fault trigger rides the RPC so
// the adapter applies the 300s in-flight gate ceiling. F-13.3.10.
func TestRotateCredentialsForwardsFaultTrigger(t *testing.T) {
	rec := &recordingAdapter{}
	cl := dialRecordingAdapter(t, rec)

	if err := cl.RotateCredentials(context.Background(), "sess-rot", nil,
		credential.TriggerFaultRateLimited); err != nil {
		t.Fatalf("RotateCredentials: %v", err)
	}
	if rec.gotRotate == nil {
		t.Fatal("the adapter received no RotateCredentials request")
	}
	if got := rec.gotRotate.GetRotationTrigger(); got != "fault_rate_limited" {
		t.Errorf("RotateCredentials rotation_trigger = %q, want fault_rate_limited", got)
	}
}

func TestRotateCredentialsEmptyMapIsAccepted(t *testing.T) {
	rec := &recordingAdapter{}
	cl := dialRecordingAdapter(t, rec)

	// A nil lease map rotates nothing; the call still round-trips.
	if err := cl.RotateCredentials(context.Background(), "sess-rot", nil, credential.TriggerProactiveRenewal); err != nil {
		t.Fatalf("RotateCredentials with no leases: %v", err)
	}
	if rec.gotRotate == nil || len(rec.gotRotate.GetLeases()) != 0 {
		t.Errorf("RotateCredentials with a nil map sent %v leases, want none", rec.gotRotate.GetLeases())
	}
}

func TestRotateCredentialsPropagatesAdapterError(t *testing.T) {
	rec := &recordingAdapter{rotateErr: status.Error(codes.FailedPrecondition, "no session")}
	cl := dialRecordingAdapter(t, rec)

	err := cl.RotateCredentials(context.Background(), "sess-rot", nil, credential.TriggerFaultProviderUnavailable)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("RotateCredentials error = %v, want the adapter's FailedPrecondition", err)
	}
}

func TestRotateCredentialsRejectsEmptySessionID(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.CredentialsDir = t.TempDir()
	cl := dialAdapter(t, srv)

	// The adapter's RotateCredentials requires a session id.
	err := cl.RotateCredentials(context.Background(), "", nil, credential.TriggerProactiveRenewal)
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("RotateCredentials with no session id = %v, want InvalidArgument", err)
	}
}

func TestRotateCredentialsRewritesTheCredentialFile(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	credDir := t.TempDir()
	srv.CredentialsDir = credDir
	cl := dialAdapter(t, srv)
	ctx := context.Background()

	// Assign an initial lease, then rotate it onto a fresh one.
	if err := cl.AssignCredentials(ctx, "sess-rot",
		map[string]*adapterv1.CredentialLease{
			"anthropic_direct": {
				LeaseId:  "cl-initial",
				Provider: "anthropic_direct",
				Payload: []byte(`{"deliveryMode":"proxy",` +
					`"materializedConfig":{"proxyUrl":"https://p/v1","leaseToken":"lt-1"}}`),
			},
		}); err != nil {
		t.Fatalf("AssignCredentials: %v", err)
	}
	if err := cl.RotateCredentials(ctx, "sess-rot",
		map[string]*adapterv1.CredentialLease{
			"anthropic_direct": {
				LeaseId:  "cl-rotated",
				Provider: "anthropic_direct",
				Payload: []byte(`{"deliveryMode":"proxy",` +
					`"materializedConfig":{"proxyUrl":"https://p/v1","leaseToken":"lt-2"}}`),
			},
		}, credential.TriggerProactiveRenewal); err != nil {
		t.Fatalf("RotateCredentials: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(credDir, "credentials.json"))
	if err != nil {
		t.Fatalf("read credential file: %v", err)
	}
	var doc struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode credential file: %v", err)
	}
	if len(doc.Providers) != 1 || doc.Providers[0]["leaseId"] != "cl-rotated" {
		t.Errorf("credential file providers = %v, want one entry for the rotated lease cl-rotated", doc.Providers)
	}
}
