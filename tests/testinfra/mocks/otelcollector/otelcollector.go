// SPDX-License-Identifier: MIT

// Package otelcollector is the in-process span sink test helper. It
// constructs an OTel TracerProvider backed by a tracetest.SpanRecorder
// and returns the recorder so tests can assert which spans were emitted,
// with which attributes, in which order.
//
// Future phases that ship binaries emitting OTLP over gRPC will add an
// OTLP receiver to this package (the public API stays Collector). Phase
// 2.5 has no such binaries — the gateway, controllers, and runtime
// adapter are all later phases — so this collector is the in-process
// recorder only. The collector reference in TESTING.md §13.4 is satisfied
// by the recorder for purposes of the Phase 2.5 gate.
package otelcollector

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Collector is an in-process span sink. Use New to construct one in a test;
// Shutdown when finished. The zero value is not usable.
type Collector struct {
	provider *trace.TracerProvider
	recorder *tracetest.SpanRecorder
}

// New returns a Collector with a synchronous span processor. Spans
// recorded against tracers created from c.TracerProvider() are visible
// to c.Spans() immediately after the originating Span.End() returns —
// callers do not need to flush.
//
// New registers a t.Cleanup so the underlying TracerProvider shuts down
// at the end of the test even when the test fails before calling
// Shutdown explicitly.
func New(t testing.TB) *Collector {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := trace.NewTracerProvider(trace.WithSpanProcessor(rec))
	c := &Collector{provider: tp, recorder: rec}
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	return c
}

// TracerProvider returns the OTel TracerProvider that records to this
// collector. Pass it to code under test that needs a TracerProvider.
func (c *Collector) TracerProvider() oteltrace.TracerProvider {
	return c.provider
}

// Tracer is the most common way to drive code under test: it returns a
// trace.Tracer rooted at this collector's provider.
func (c *Collector) Tracer(name string) oteltrace.Tracer {
	return c.provider.Tracer(name)
}

// Spans returns the spans that have ended on this collector, in the
// order they ended. The returned slice is a snapshot; later End calls
// do not mutate it.
func (c *Collector) Spans() []trace.ReadOnlySpan {
	return c.recorder.Ended()
}

// FindByName returns the first ended span whose name equals name. The
// boolean is false when no matching span has ended yet. Useful for
// tests that check a single span on a simple code path.
func (c *Collector) FindByName(name string) (trace.ReadOnlySpan, bool) {
	for _, s := range c.recorder.Ended() {
		if s.Name() == name {
			return s, true
		}
	}
	return nil, false
}

// Reset discards all recorded spans without tearing down the underlying
// provider. Useful for tests that exercise multiple scenarios against
// one collector.
func (c *Collector) Reset() {
	c.recorder.Reset()
}

// Shutdown flushes the provider and disables further recording. It is
// safe to call multiple times. The cleanup registered by New calls it
// automatically.
func (c *Collector) Shutdown(ctx context.Context) error {
	return c.provider.Shutdown(ctx)
}
