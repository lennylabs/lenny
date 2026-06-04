// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// installSpanRecorder swaps the process-global OTel TracerProvider for an
// SDK-backed recorder so a test can read every span the adapter emitted,
// then restores the prior provider when the test ends. The adapter
// resolves NewTracer(nil) against this global provider, exactly as it
// resolves the provider cmd/lenny-adapter installs in production. spec:
// §16.3 / F-16.3.6.
func installSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return rec
}

// findSpan returns the first ended span with the given name, or nil.
func findSpan(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// spanStringAttr returns the string value of attribute key, or ("", false).
func spanStringAttr(s sdktrace.ReadOnlySpan, key string) (string, bool) {
	for _, a := range s.Attributes() {
		if string(a.Key) == key {
			return a.Value.AsString(), true
		}
	}
	return "", false
}

// TestStartSessionEmitsSessionStartSpan_spec_16_3 asserts the §16.3
// line 341 Pod-emitted `session.start` span fires on a clean StartSession.
// This is the Go-side adapter OTel emitter F-16.3.6 was missing.
func TestStartSessionEmitsSessionStartSpan_spec_16_3(t *testing.T) {
	rec := installSpanRecorder(t)
	s, rt, _ := sessionServer(t)

	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if len(rt.started) != 1 {
		t.Fatalf("runtime not started; harness changed")
	}

	span := findSpan(rec.Ended(), "session.start")
	if span == nil {
		t.Fatalf("session.start span not recorded; got %d spans", len(rec.Ended()))
	}
	if span.Status().Code != codes.Unset {
		t.Errorf("clean StartSession span status = %v, want Unset", span.Status().Code)
	}
}

// TestStartSessionSpanRecordsRuntimeStartError_spec_16_3 asserts the
// error path: a runtime-start crash records the error on the
// `session.start` span (the §16.3 TRANSIENT category).
func TestStartSessionSpanRecordsRuntimeStartError_spec_16_3(t *testing.T) {
	rec := installSpanRecorder(t)
	root := t.TempDir()
	s := adapter.New("test")
	s.WorkspaceRoot = root
	s.Runtime = &fakeRuntime{startErr: errors.New("runtime crashed")}

	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err == nil {
		t.Fatal("StartSession should fail when the runtime crashes")
	}

	span := findSpan(rec.Ended(), "session.start")
	if span == nil {
		t.Fatal("session.start span not recorded on the runtime-start failure path")
	}
	if span.Status().Code != codes.Error {
		t.Errorf("span status = %v, want codes.Error on a runtime-start crash", span.Status().Code)
	}
}

// TestAttachEmitsToolCallSpanPerInvocation_spec_16_3 asserts the §16.3
// line 343 Pod-emitted `session.tool_call` span fires once per
// adapter-local tool invocation, carrying the tool name attribute.
func TestAttachEmitsToolCallSpanPerInvocation_spec_16_3(t *testing.T) {
	rec := installSpanRecorder(t)
	s, rt, root := sessionServer(t)
	rt.output = make(chan []byte, 4)
	rt.echoInput = true
	if err := os.WriteFile(filepath.Join(root, "data.txt"), []byte("file-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	client, _ := adapterClient(t, s)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := stream.Send(&adapterv1.AttachRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Two distinct adapter-local tool_calls must produce two spans.
	rt.output <- toolCallFrame(t, "tc_read", "read_file", map[string]string{"path": "data.txt"})
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("Recv read result: %v", err)
	}
	rt.output <- toolCallFrame(t, "tc_write", "write_file", map[string]string{"path": "out.txt", "content": "x"})
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("Recv write result: %v", err)
	}

	var toolSpans int
	var sawRead bool
	for _, sp := range rec.Ended() {
		if sp.Name() != "session.tool_call" {
			continue
		}
		toolSpans++
		if name, ok := spanStringAttr(sp, "tool.name"); ok && name == "read_file" {
			sawRead = true
		}
	}
	if toolSpans != 2 {
		t.Errorf("session.tool_call spans = %d, want 2 (one per invocation)", toolSpans)
	}
	if !sawRead {
		t.Error("no session.tool_call span carried tool.name=read_file")
	}
}

// TestAttachToolCallSpanRecordsToolError_spec_16_3 asserts the error
// path: an adapter-local tool that returns an error result records the
// error on its `session.tool_call` span. read_file of a missing file is
// handled (the tool runs) but reports isError.
func TestAttachToolCallSpanRecordsToolError_spec_16_3(t *testing.T) {
	rec := installSpanRecorder(t)
	s, rt, _ := sessionServer(t)
	rt.output = make(chan []byte, 4)
	rt.echoInput = true
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	client, _ := adapterClient(t, s)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := stream.Send(&adapterv1.AttachRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	rt.output <- toolCallFrame(t, "tc_miss", "read_file", map[string]string{"path": "absent"})
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("Recv: %v", err)
	}

	span := findSpan(rec.Ended(), "session.tool_call")
	if span == nil {
		t.Fatal("session.tool_call span not recorded for the failing-tool path")
	}
	if span.Status().Code != codes.Error {
		t.Errorf("span status = %v, want codes.Error when the tool reports isError", span.Status().Code)
	}
}

// TestPrepareWorkspaceEmitsUploadSpan_spec_16_3 asserts the Pod half of
// the §16.3 line 338 Gateway + Pod `session.upload` span: the adapter's
// staging-stream RPC runs under a `session.upload` span carrying the
// staged byte/file counts.
func TestPrepareWorkspaceEmitsUploadSpan_spec_16_3(t *testing.T) {
	rec := installSpanRecorder(t)
	s := adapter.New("test")
	s.StagingDir = t.TempDir()
	client, _ := adapterClient(t, s)

	stream, err := client.PrepareWorkspace(context.Background())
	if err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}
	if err := stream.Send(&adapterv1.PrepareWorkspaceRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		UploadRef: "u1",
		Chunk:     []byte("hello upload"),
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := stream.CloseAndRecv(); err != nil && err != io.EOF {
		t.Fatalf("CloseAndRecv: %v", err)
	}

	span := findSpan(rec.Ended(), "session.upload")
	if span == nil {
		t.Fatalf("session.upload span not recorded; got %d spans", len(rec.Ended()))
	}
	if span.Status().Code != codes.Unset {
		t.Errorf("clean upload span status = %v, want Unset", span.Status().Code)
	}
}

// TestCheckpointEmitsCheckpointSpan_spec_16_3 asserts the Pod half of the
// §16.3 line 355 Gateway + Pod `session.checkpoint` span: the adapter's
// workspace-snapshot RPC runs under a `session.checkpoint` span.
func TestCheckpointEmitsCheckpointSpan_spec_16_3(t *testing.T) {
	rec := installSpanRecorder(t)
	s, _ := startedServer(t)
	s.Checkpoints = &fakeCheckpointSink{id: "ckpt-1"}

	if _, err := s.Checkpoint(context.Background(), checkpointReq("sess-1")); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	span := findSpan(rec.Ended(), "session.checkpoint")
	if span == nil {
		t.Fatalf("session.checkpoint span not recorded; got %d spans", len(rec.Ended()))
	}
	if span.Status().Code != codes.Unset {
		t.Errorf("clean checkpoint span status = %v, want Unset", span.Status().Code)
	}
}

// TestCheckpointSpanRecordsSinkError_spec_16_3 asserts the error path: a
// failing checkpoint sink records the error on the `session.checkpoint`
// span.
func TestCheckpointSpanRecordsSinkError_spec_16_3(t *testing.T) {
	rec := installSpanRecorder(t)
	s, _ := startedServer(t)
	s.Checkpoints = &fakeCheckpointSink{err: errors.New("minio unreachable")}

	if _, err := s.Checkpoint(context.Background(), checkpointReq("sess-1")); err == nil {
		t.Fatal("Checkpoint should fail when the sink errors")
	}

	span := findSpan(rec.Ended(), "session.checkpoint")
	if span == nil {
		t.Fatal("session.checkpoint span not recorded on the sink-error path")
	}
	if span.Status().Code != codes.Error {
		t.Errorf("span status = %v, want codes.Error on a sink failure", span.Status().Code)
	}
}
