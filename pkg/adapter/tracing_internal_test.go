// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// installInternalSpanRecorder swaps the process-global OTel
// TracerProvider for an SDK-backed recorder so an internal-package test
// can read the spans the adapter's lifecycle RPCs emitted. The adapter
// resolves tracing.NewTracer(nil) against this global provider, the same
// provider cmd/lenny-adapter installs in production. spec: §16.3 /
// F-16.3.6.
func installInternalSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return rec
}

func endedSpanNamed(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// TestFinalizeWorkspaceEmitsSpan_spec_16_3 asserts the §16.3 line 339
// Pod-emitted `session.finalize_workspace` span fires on a clean
// materialization. F-16.3.6.
func TestFinalizeWorkspaceEmitsSpan_spec_16_3(t *testing.T) {
	rec := installInternalSpanRecorder(t)
	srv := &Server{WorkspaceRoot: t.TempDir()}

	if _, err := srv.FinalizeWorkspace(context.Background(), finalizeReq("sess-1",
		wsSource("mkdir", "docs", "", "755"),
		wsSource("inlineFile", "docs/readme.md", "hello", "644"))); err != nil {
		t.Fatalf("FinalizeWorkspace: %v", err)
	}

	span := endedSpanNamed(rec.Ended(), "session.finalize_workspace")
	if span == nil {
		t.Fatalf("session.finalize_workspace span not recorded; got %d spans", len(rec.Ended()))
	}
	if span.Status().Code != codes.Unset {
		t.Errorf("clean finalize span status = %v, want Unset", span.Status().Code)
	}
}

// TestFinalizeWorkspaceSpanRecordsSchemaError_spec_16_3 asserts the
// error path: a workspace plan with an unsupported schemaVersion records
// the error on the span (the §16.3 PERMANENT category).
func TestFinalizeWorkspaceSpanRecordsSchemaError_spec_16_3(t *testing.T) {
	rec := installInternalSpanRecorder(t)
	srv := &Server{WorkspaceRoot: t.TempDir()}

	req := &adapterv1.FinalizeWorkspaceRequest{
		SessionId:     &adapterv1.SessionId{Value: "sess-1"},
		WorkspacePlan: &adapterv1.WorkspacePlan{SchemaVersion: 9999},
	}
	if _, err := srv.FinalizeWorkspace(context.Background(), req); err == nil {
		t.Fatal("FinalizeWorkspace should reject an unsupported plan schema version")
	}

	span := endedSpanNamed(rec.Ended(), "session.finalize_workspace")
	if span == nil {
		t.Fatal("session.finalize_workspace span not recorded on the schema-error path")
	}
	if span.Status().Code != codes.Error {
		t.Errorf("span status = %v, want codes.Error on an unsupported schema", span.Status().Code)
	}
}

// TestRunSetupEmitsSpan_spec_16_3 asserts the §16.3 line 340 Pod-emitted
// `session.run_setup` span fires on a clean setup run. F-16.3.6.
func TestRunSetupEmitsSpan_spec_16_3(t *testing.T) {
	rec := installInternalSpanRecorder(t)
	srv := &Server{WorkspaceRoot: t.TempDir()}

	if _, err := srv.RunSetup(context.Background(), runSetupReq("sess-1", nil, "true")); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}

	span := endedSpanNamed(rec.Ended(), "session.run_setup")
	if span == nil {
		t.Fatalf("session.run_setup span not recorded; got %d spans", len(rec.Ended()))
	}
	if span.Status().Code != codes.Unset {
		t.Errorf("clean run_setup span status = %v, want Unset", span.Status().Code)
	}
}

// TestRunSetupSpanRecordsCommandFailure_spec_16_3 asserts the error
// path: a setup command that exits non-zero records the error on the
// `session.run_setup` span (the §16.3 UPSTREAM category — the failure
// is in the runtime's own setup command).
func TestRunSetupSpanRecordsCommandFailure_spec_16_3(t *testing.T) {
	rec := installInternalSpanRecorder(t)
	srv := &Server{WorkspaceRoot: t.TempDir()}

	// `false` exits non-zero, which surfaces as a hard setup failure.
	_, err := srv.RunSetup(context.Background(), runSetupReq("sess-1", nil, "false"))
	if err == nil {
		t.Fatal("RunSetup should fail when a setup command exits non-zero")
	}

	span := endedSpanNamed(rec.Ended(), "session.run_setup")
	if span == nil {
		t.Fatal("session.run_setup span not recorded on the command-failure path")
	}
	if span.Status().Code != codes.Error {
		t.Errorf("span status = %v, want codes.Error on a failing setup command", span.Status().Code)
	}
}
