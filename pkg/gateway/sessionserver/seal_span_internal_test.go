// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §16.3 line 356 — the gateway-side seal-and-export path must open a
// `session.seal_and_export` span. sealWorkspace is unexported, so these
// tests live in the internal package and drive it directly through the
// existing flakySealer / advancingClock harness from seal_internal_test.go.
// F-16.3.1.

// installSealSpanRecorder swaps the global OTel TracerProvider for an
// SDK-backed recorder and restores the prior provider when the test ends.
func installSealSpanRecorder(t *testing.T) (*tracetest.SpanRecorder, func()) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	return rec, func() { otel.SetTracerProvider(prev) }
}

func sealSpan(t *testing.T, rec *tracetest.SpanRecorder) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, s := range rec.Ended() {
		if s.Name() == "session.seal_and_export" {
			return s
		}
	}
	return nil
}

func TestSealWorkspaceStartsSealAndExportSpan_spec_16_3(t *testing.T) {
	rec, restore := installSealSpanRecorder(t)
	defer restore()

	srv := New(memstore.New(), Options{
		Sealer: &flakySealer{failUntil: 0}, // succeeds first try
		Clock:  func() time.Time { return time.Unix(0, 0).UTC() },
	})
	sess := sessionstore.Session{ID: "s1", TenantID: "acme", RuntimeRef: "claude-code", State: session.StateCompleted}
	if err := srv.sealWorkspace(context.Background(), sess); err != nil {
		t.Fatalf("sealWorkspace: %v", err)
	}

	span := sealSpan(t, rec)
	if span == nil {
		t.Fatalf("session.seal_and_export span not recorded; got %d spans", len(rec.Ended()))
	}
	if span.Status().Code != codes.Unset {
		t.Errorf("clean seal span status = %v, want Unset", span.Status().Code)
	}
}

func TestSealWorkspaceSpanRecordsTimeoutError_spec_16_3(t *testing.T) {
	rec, restore := installSealSpanRecorder(t)
	defer restore()

	clock, sleep, _ := advancingClock(time.Unix(0, 0).UTC())
	srv := New(memstore.New(), Options{
		Sealer:    &flakySealer{failUntil: -1, err: errors.New("minio: connection refused")}, // never succeeds
		SealSleep: sleep,
		Clock:     clock,
	})
	sess := sessionstore.Session{ID: "s1", TenantID: "acme", RuntimeRef: "claude-code", State: session.StateCompleted}
	if err := srv.sealWorkspace(context.Background(), sess); err == nil {
		t.Fatal("sealWorkspace must return the last error when the window is exhausted")
	}

	span := sealSpan(t, rec)
	if span == nil {
		t.Fatalf("session.seal_and_export span not recorded on the timeout path")
	}
	if span.Status().Code != codes.Error {
		t.Errorf("span status = %v, want codes.Error on a seal timeout", span.Status().Code)
	}
}
