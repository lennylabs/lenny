// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/ops/escalation"
)

// captureEmitter records the last event Emit received and optionally
// returns an error to exercise the failed-publish path.
type captureEmitter struct {
	last events.OperationalEvent
	err  error
}

func (c *captureEmitter) Emit(_ context.Context, e events.OperationalEvent) error {
	c.last = e
	return c.err
}

// spec: §25.17 lines 5266-5285 — escalation_created is emitted to the
// event stream so a webhook subscriber can route it to PagerDuty. The
// stream emitter publishes the escalation_created CloudEvent and reports
// the publish as delivered so the escalation's emitted flag flips.
func TestStreamEscalationEmitterPublishes_spec_25_17(t *testing.T) {
	cap := &captureEmitter{}
	em := newStreamEscalationEmitter(cap, "replica-1")
	delivered := em.EmitEscalationCreated(escalation.Escalation{
		ID:       "esc-7",
		Severity: escalation.SeverityCritical,
		Summary:  "warm pool exhausted, scaling failed",
	})
	if !delivered {
		t.Fatal("EmitEscalationCreated reported not delivered; want true")
	}
	if got := cap.last.Type; got != events.EventEscalationCreated.CloudEventsType() {
		t.Errorf("event type = %q, want %q", got, events.EventEscalationCreated.CloudEventsType())
	}
	if cap.last.Severity != "critical" {
		t.Errorf("severity = %q, want critical", cap.last.Severity)
	}
	if cap.last.Subject != "escalation/esc-7" {
		t.Errorf("subject = %q, want escalation/esc-7", cap.last.Subject)
	}
	var payload map[string]any
	if err := json.Unmarshal(cap.last.Data, &payload); err != nil {
		t.Fatalf("unmarshal event data: %v", err)
	}
	if payload["id"] != "esc-7" {
		t.Errorf("payload id = %v, want esc-7", payload["id"])
	}
}

// A failed stream write reports not-delivered so the escalation stays
// un-emitted for the §25.4 background retry.
func TestStreamEscalationEmitterReportsFailure_spec_25_17(t *testing.T) {
	cap := &captureEmitter{err: errors.New("redis down")}
	em := newStreamEscalationEmitter(cap, "replica-1")
	if em.EmitEscalationCreated(escalation.Escalation{ID: "esc-8", Severity: escalation.SeverityWarning}) {
		t.Fatal("EmitEscalationCreated reported delivered on emit error; want false")
	}
}

// An escalation with no severity defaults the CloudEvents severity to
// warning rather than emitting an empty severity extension.
func TestStreamEscalationEmitterDefaultsSeverity_spec_25_17(t *testing.T) {
	cap := &captureEmitter{}
	em := newStreamEscalationEmitter(cap, "replica-1")
	em.EmitEscalationCreated(escalation.Escalation{ID: "esc-9"})
	if cap.last.Severity != "warning" {
		t.Errorf("severity = %q, want warning (default)", cap.last.Severity)
	}
}
