// SPDX-License-Identifier: MIT

package escalation_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/escalation"
)

// recordingEmitter is a test Emitter that records calls and can be told
// to fail the publish.
type recordingEmitter struct {
	calls int
	fail  bool
}

func (e *recordingEmitter) EmitEscalationCreated(escalation.Escalation) bool {
	e.calls++
	return !e.fail
}

// create is a test helper that creates an escalation.
func create(t *testing.T, s *escalation.Service, sev, summary string) *escalation.Escalation {
	t.Helper()
	esc, err := s.Create(context.Background(), escalation.CreateRequest{
		Severity: sev, Summary: summary, Source: "prod-watchdog",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return esc
}

func TestCreatePopulatesRecordAndEmits(t *testing.T) {
	em := &recordingEmitter{}
	s := escalation.NewService(em)
	esc := create(t, s, escalation.SeverityCritical, "warm pool exhausted, scaling failed")
	if esc.ID == "" {
		t.Error("escalation id is empty")
	}
	if esc.Status != escalation.StatusOpen {
		t.Errorf("status = %q, want open on a fresh escalation", esc.Status)
	}
	if esc.Persistence != escalation.PersistenceBufferedMemory {
		t.Errorf("persistence = %q, want buffered-memory for the Tier 3 store", esc.Persistence)
	}
	// §25.4 emission: the escalation_created event is published and the
	// emitted flag is set.
	if em.calls != 1 {
		t.Errorf("emitter calls = %d, want 1", em.calls)
	}
	if !esc.Emitted {
		t.Error("emitted = false after a successful publish")
	}
}

func TestCreateLeavesEmittedFalseWhenEmissionFails(t *testing.T) {
	em := &recordingEmitter{fail: true}
	s := escalation.NewService(em)
	esc := create(t, s, escalation.SeverityWarning, "credential pool degraded")
	// §25.4: when emission fails, emitted stays false for the background
	// retry — the record is still stored.
	if esc.Emitted {
		t.Error("emitted = true despite a failed publish")
	}
}

func TestRetryEmissionPublishesUnemittedEscalations(t *testing.T) {
	em := &recordingEmitter{fail: true}
	s := escalation.NewService(em)
	create(t, s, escalation.SeverityCritical, "a")
	create(t, s, escalation.SeverityCritical, "b")
	// Emission now succeeds; the background retry publishes both.
	em.fail = false
	if n := s.RetryEmission(context.Background()); n != 2 {
		t.Errorf("RetryEmission emitted %d, want 2", n)
	}
	// A second pass has nothing left to emit.
	if n := s.RetryEmission(context.Background()); n != 0 {
		t.Errorf("RetryEmission emitted %d on the second pass, want 0", n)
	}
}

func TestCreateRejectsInvalidSeverityAndEmptySummary(t *testing.T) {
	s := escalation.NewService(nil)
	if _, err := s.Create(context.Background(), escalation.CreateRequest{
		Severity: "catastrophic", Summary: "x",
	}); escalation.CodeOf(err) != escalation.ErrCodeInvalid {
		t.Errorf("err code = %q, want ESCALATION_INVALID for a bad severity", escalation.CodeOf(err))
	}
	if _, err := s.Create(context.Background(), escalation.CreateRequest{
		Severity: escalation.SeverityInfo,
	}); escalation.CodeOf(err) != escalation.ErrCodeInvalid {
		t.Errorf("err code = %q, want ESCALATION_INVALID for an empty summary", escalation.CodeOf(err))
	}
}

func TestUpdateMovesStatusAndStampsTimestamps(t *testing.T) {
	s := escalation.NewService(nil)
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })
	esc := create(t, s, escalation.SeverityCritical, "x")

	ack, err := s.Update(context.Background(), esc.ID, escalation.UpdateRequest{Status: escalation.StatusAcknowledged})
	if err != nil {
		t.Fatalf("acknowledge: %v", err)
	}
	if ack.Status != escalation.StatusAcknowledged || ack.AcknowledgedAt == nil {
		t.Errorf("status=%q acknowledgedAt=%v, want acknowledged with a timestamp", ack.Status, ack.AcknowledgedAt)
	}
	resolved, err := s.Update(context.Background(), esc.ID, escalation.UpdateRequest{Status: escalation.StatusResolved})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Status != escalation.StatusResolved || resolved.ResolvedAt == nil {
		t.Errorf("status=%q resolvedAt=%v, want resolved with a timestamp", resolved.Status, resolved.ResolvedAt)
	}
}

func TestUpdateUnknownEscalationIsNotFound(t *testing.T) {
	s := escalation.NewService(nil)
	_, err := s.Update(context.Background(), "esc-nonexistent", escalation.UpdateRequest{Status: escalation.StatusResolved})
	if escalation.CodeOf(err) != escalation.ErrCodeNotFound {
		t.Errorf("err code = %q, want ESCALATION_NOT_FOUND", escalation.CodeOf(err))
	}
}

func TestListFiltersByStatusAndSeverity(t *testing.T) {
	s := escalation.NewService(nil)
	a := create(t, s, escalation.SeverityCritical, "critical-open")
	create(t, s, escalation.SeverityWarning, "warning-open")
	if _, err := s.Update(context.Background(), a.ID, escalation.UpdateRequest{Status: escalation.StatusResolved}); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	openPage, err := s.List(context.Background(), escalation.Filter{Status: "open"}, "", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	open := openPage.Items
	if len(open) != 1 || open[0].Summary != "warning-open" {
		t.Errorf("status=open filter returned %v, want just warning-open", summaries(open))
	}
	critPage, _ := s.List(context.Background(), escalation.Filter{Severity: "critical"}, "", 0)
	crit := critPage.Items
	if len(crit) != 1 || crit[0].Summary != "critical-open" {
		t.Errorf("severity=critical filter returned %v, want just critical-open", summaries(crit))
	}
}

func TestListIsNewestFirstAndRespectsLimit(t *testing.T) {
	s := escalation.NewService(nil)
	base := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		s.SetClock(func() time.Time { return base.Add(time.Duration(i) * time.Minute) })
		create(t, s, escalation.SeverityInfo, "e")
	}
	all := listItems(t, s, escalation.Filter{}, 0)
	if len(all) != 3 {
		t.Fatalf("got %d escalations, want 3", len(all))
	}
	if !all[0].CreatedAt.After(all[2].CreatedAt) {
		t.Error("list is not newest-first")
	}
	limited := listItems(t, s, escalation.Filter{}, 2)
	if len(limited) != 2 {
		t.Errorf("limited list returned %d, want 2", len(limited))
	}
}

func TestBufferEvictsOldestBeyondCapacity(t *testing.T) {
	s := escalation.NewService(nil)
	// §25.4 caps the Tier 3 buffer at 100; the 101st create evicts the
	// oldest.
	for i := 0; i < 105; i++ {
		create(t, s, escalation.SeverityInfo, "e")
	}
	all := listItems(t, s, escalation.Filter{}, 0)
	if len(all) != 100 {
		t.Errorf("buffer holds %d escalations, want the 100-entry cap", len(all))
	}
}

func summaries(escs []escalation.Escalation) []string {
	out := make([]string, len(escs))
	for i, e := range escs {
		out[i] = e.Summary
	}
	return out
}

// listItems runs the query-path List from the head and returns the page's
// records, failing the test on error. It keeps the pagination-agnostic
// assertions terse.
func listItems(t *testing.T, s *escalation.Service, f escalation.Filter, limit int) []escalation.Escalation {
	t.Helper()
	page, err := s.List(context.Background(), f, "", limit)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return page.Items
}
