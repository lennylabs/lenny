// SPDX-License-Identifier: MIT

package otelcollector

import (
	"context"
	"testing"
)

func TestCollectorRecordsSpans(t *testing.T) {
	c := New(t)
	tr := c.Tracer("unit-test")

	_, span := tr.Start(context.Background(), "session.create")
	span.End()

	got := c.Spans()
	if len(got) != 1 {
		t.Fatalf("expected 1 span, got %d", len(got))
	}
	if got[0].Name() != "session.create" {
		t.Errorf("span name: want session.create, got %q", got[0].Name())
	}
}

func TestFindByNameMatchesEndedSpan(t *testing.T) {
	c := New(t)
	tr := c.Tracer("unit-test")

	_, parent := tr.Start(context.Background(), "session.create")
	_, child := tr.Start(context.Background(), "session.claim_pod")
	parent.End()
	child.End()

	if _, ok := c.FindByName("session.claim_pod"); !ok {
		t.Errorf("FindByName should locate ended span")
	}
	if _, ok := c.FindByName("session.never_started"); ok {
		t.Errorf("FindByName should return false for missing span")
	}
}

func TestResetClearsRecordedSpans(t *testing.T) {
	c := New(t)
	tr := c.Tracer("unit-test")
	_, span := tr.Start(context.Background(), "x")
	span.End()
	if len(c.Spans()) != 1 {
		t.Fatalf("setup: expected 1 span before reset")
	}
	c.Reset()
	if len(c.Spans()) != 0 {
		t.Errorf("Reset should clear recorded spans, got %d", len(c.Spans()))
	}
}

func TestShutdownIsIdempotent(t *testing.T) {
	c := New(t)
	if err := c.Shutdown(context.Background()); err != nil {
		t.Errorf("first Shutdown should succeed, got %v", err)
	}
	if err := c.Shutdown(context.Background()); err != nil {
		t.Errorf("second Shutdown should be a no-op, got %v", err)
	}
}
