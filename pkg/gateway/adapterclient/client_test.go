// SPDX-License-Identifier: MIT

package adapterclient_test

import (
	"bytes"
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
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
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

func TestSessionRoundTrip(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt
	cl := dialAdapter(t, srv)
	ctx := context.Background()

	if err := cl.StartSession(ctx, "sess-x", "claude-code", nil); err != nil {
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

	if err := cl.StartSession(ctx, "sess-x", "claude-code", nil); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	ack, err := cl.Interrupt(ctx, "sess-x", true, 2*time.Second)
	if err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if !ack {
		t.Error("Interrupt was not acknowledged")
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

	if err := cl.StartSession(ctx, "sess-x", "claude-code", nil); err != nil {
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

	if err := cl.StartSession(ctx, "sess-x", "claude-code", nil); err != nil {
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
	// Build a one-file checkpoint archive to restore.
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

	n, err := cl.Resume(context.Background(), "sess-r", "echo", "ckpt-1")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if n != int64(len("restored")) {
		t.Errorf("restored bytes = %d, want %d", n, len("restored"))
	}
}

func TestResumeRejectsAMissingCheckpointSource(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	// No Restorer configured: the adapter cannot restore a checkpoint.
	cl := dialAdapter(t, srv)

	if _, err := cl.Resume(context.Background(), "sess-r", "echo", "ckpt-1"); err == nil {
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

	if err := cl.StartSession(ctx, "sess-x", "claude-code", nil); err != nil {
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

	if err := cl.StartSession(ctx, "sess-x", "claude-code", nil); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if _, err := cl.ReportUsage(ctx, "sess-x"); err == nil {
		t.Error("ReportUsage with no usage meter succeeded, want a failure")
	}
}
