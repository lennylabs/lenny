// SPDX-License-Identifier: MIT

package pgaudit_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/audit/pgaudit"
)

// spec: §4.4 line 232 — pgaudit sink consumers in the OCSF egress
// targets list.

// recordingSink captures every Deliver call. The tail loop delivers from
// a background goroutine while a test polls the captured calls, so the
// recorded calls are guarded by a mutex to stay -race clean.
type recordingSink struct {
	mu    sync.Mutex
	calls []deliverCall
	err   error
}

type deliverCall struct {
	tenantID string
	topic    string
	rec      ocsf.Record
}

func (r *recordingSink) Deliver(_ context.Context, tenantID, topic string, rec ocsf.Record) error {
	if r.err != nil {
		return r.err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, deliverCall{tenantID, topic, rec})
	return nil
}

// snapshot returns a copy of the recorded calls under the lock so a
// concurrent reader does not race the tail-loop Deliver.
func (r *recordingSink) snapshot() []deliverCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]deliverCall(nil), r.calls...)
}

// recordingMetrics captures every counter increment.
type recordingMetrics struct {
	events    []pgaudit.Class
	deliv     []pgaudit.Class
	parseFail int
}

func (m *recordingMetrics) PgAuditEvent(class pgaudit.Class) {
	m.events = append(m.events, class)
}

func (m *recordingMetrics) PgAuditDeliveryFailed(class pgaudit.Class) {
	m.deliv = append(m.deliv, class)
}
func (m *recordingMetrics) PgAuditParseFailed() { m.parseFail++ }

// TestParseLineDDL covers the DDL-class line shape pgaudit emits for
// a CREATE TABLE statement.
func TestParseLineDDL(t *testing.T) {
	line := `2026-05-22 12:00:00.000 UTC [12345] AUDIT: SESSION,1,1,DDL,CREATE TABLE,TABLE,public.t,"CREATE TABLE t(id int);",<not logged>`
	got, ok := pgaudit.ParseLine(line)
	if !ok {
		t.Fatal("ParseLine returned ok=false on a valid DDL record")
	}
	if got.Class != pgaudit.ClassDDL {
		t.Errorf("Class = %q, want DDL", got.Class)
	}
	if got.Command != "CREATE TABLE" {
		t.Errorf("Command = %q, want CREATE TABLE", got.Command)
	}
	if got.ObjectType != "TABLE" {
		t.Errorf("ObjectType = %q, want TABLE", got.ObjectType)
	}
	if got.ObjectName != "public.t" {
		t.Errorf("ObjectName = %q, want public.t", got.ObjectName)
	}
	if got.Statement != "CREATE TABLE t(id int);" {
		t.Errorf("Statement = %q", got.Statement)
	}
}

// TestParseLineRole covers a GRANT statement.
func TestParseLineRole(t *testing.T) {
	line := `2026-05-22 12:00:00.000 UTC [12345] AUDIT: SESSION,1,1,ROLE,GRANT,ROLE,reader,"GRANT SELECT ON t TO reader;",<not logged>`
	got, ok := pgaudit.ParseLine(line)
	if !ok {
		t.Fatal("ParseLine ok=false on valid ROLE record")
	}
	if got.Class != pgaudit.ClassRole {
		t.Errorf("Class = %q, want ROLE", got.Class)
	}
	if got.Command != "GRANT" {
		t.Errorf("Command = %q", got.Command)
	}
}

// TestParseLineRejectsBadShape rejects a line without the AUDIT
// marker and a line with too few fields.
func TestParseLineRejectsBadShape(t *testing.T) {
	if _, ok := pgaudit.ParseLine("INFO: this is a regular Postgres log line"); ok {
		t.Error("ParseLine accepted a non-AUDIT line")
	}
	if _, ok := pgaudit.ParseLine("2026-05-22 AUDIT: too,few,fields"); ok {
		t.Error("ParseLine accepted a line with < 9 fields")
	}
}

// TestProcessLineRejectsParseFailureMetric confirms the metric
// surface bumps on a malformed line.
func TestProcessLineRecordsParseFailure(t *testing.T) {
	metrics := &recordingMetrics{}
	s := pgaudit.New(pgaudit.Config{Metrics: metrics, Sink: pgaudit.NoOpSink()})
	if err := s.ProcessLine(context.Background(), "no marker here"); err != nil {
		t.Errorf("ProcessLine returned err on parse failure; want nil (logged via metric): %v", err)
	}
	if metrics.parseFail != 1 {
		t.Errorf("parseFail count = %d, want 1", metrics.parseFail)
	}
}

// TestProcessLineDeliversToOCSFSink covers the happy path: a valid
// pgaudit line is parsed, translated, and delivered to the sink.
func TestProcessLineDeliversToOCSFSink(t *testing.T) {
	sink := &recordingSink{}
	metrics := &recordingMetrics{}
	s := pgaudit.New(pgaudit.Config{
		TenantID: "acme",
		Sink:     sink,
		Metrics:  metrics,
		Now:      func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	line := `2026-05-22 12:00:00.000 UTC [12345] AUDIT: SESSION,1,1,ROLE,GRANT,ROLE,reader,"GRANT SELECT ON t TO reader;",<not logged>`
	if err := s.ProcessLine(context.Background(), line); err != nil {
		t.Fatalf("ProcessLine: %v", err)
	}
	if len(sink.calls) != 1 {
		t.Fatalf("sink calls = %d, want 1", len(sink.calls))
	}
	c := sink.calls[0]
	if c.tenantID != "acme" {
		t.Errorf("tenantID = %q, want acme", c.tenantID)
	}
	if c.topic != "pgaudit" {
		t.Errorf("topic = %q, want pgaudit", c.topic)
	}
	if c.rec.ClassUID != ocsf.ClassAccountChange {
		t.Errorf("ClassUID = %d, want %d (Account Change)", c.rec.ClassUID, ocsf.ClassAccountChange)
	}
	if c.rec.SeverityID != 3 {
		t.Errorf("SeverityID = %d, want 3 (ROLE bumped)", c.rec.SeverityID)
	}
	if c.rec.Unmapped.Lenny["pgaudit_class"] != "ROLE" {
		t.Errorf("unmapped.lenny.pgaudit_class = %v, want ROLE",
			c.rec.Unmapped.Lenny["pgaudit_class"])
	}
	if len(metrics.events) != 1 || metrics.events[0] != pgaudit.ClassRole {
		t.Errorf("metric events = %v, want [ROLE]", metrics.events)
	}
}

// TestProcessLineSurfacesSinkError confirms the shipper reports the
// sink error back to the caller and bumps the failure metric.
func TestProcessLineSurfacesSinkError(t *testing.T) {
	sink := &recordingSink{err: errors.New("sink unreachable")}
	metrics := &recordingMetrics{}
	s := pgaudit.New(pgaudit.Config{Sink: sink, Metrics: metrics})
	line := `2026-05-22 AUDIT: SESSION,1,1,DDL,CREATE TABLE,TABLE,t,"CREATE TABLE t(id int);",<not logged>`
	if err := s.ProcessLine(context.Background(), line); err == nil {
		t.Fatal("ProcessLine returned nil on sink error; want underlying error")
	}
	if len(metrics.deliv) != 1 || metrics.deliv[0] != pgaudit.ClassDDL {
		t.Errorf("delivery-failed metric = %v, want [DDL]", metrics.deliv)
	}
	if len(metrics.events) != 0 {
		t.Errorf("event metric should NOT fire on sink failure; got %v", metrics.events)
	}
}

// TestStartTailsLogFile drives the end-to-end shipper against a
// tempfile: the shipper picks up lines appended after Start and
// delivers them to the sink.
//
// spec: §4.4 line 232.
func TestStartTailsLogFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pgaudit.log")
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatalf("create logfile: %v", err)
	}
	sink := &recordingSink{}
	s := pgaudit.New(pgaudit.Config{LogFile: path, Sink: sink, TenantID: "acme"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	// Append a line after Start.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	line := "2026-05-22 12:00:00 UTC [1] AUDIT: SESSION,1,1,DDL,CREATE TABLE,TABLE,public.t,\"CREATE TABLE t(id int);\",<not logged>\n"
	if _, err := f.WriteString(line); err != nil {
		t.Fatalf("write line: %v", err)
	}
	_ = f.Close()

	// Poll the sink (the tail loop sleeps 100ms between EOF checks).
	var calls []deliverCall
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if calls = sink.snapshot(); len(calls) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(calls) == 0 {
		t.Fatal("shipper never delivered the appended line to the sink")
	}
	if calls[0].tenantID != "acme" {
		t.Errorf("tenantID = %q, want acme", calls[0].tenantID)
	}
}

// TestStartRejectsMissingLogFile confirms Start surfaces the
// missing-file error rather than starting the loop silently.
func TestStartRejectsMissingLogFile(t *testing.T) {
	s := pgaudit.New(pgaudit.Config{LogFile: "/nonexistent/pgaudit.log"})
	err := s.Start(context.Background())
	if err == nil {
		t.Error("Start on missing file = nil, want error")
	}
}

// TestStartRejectsDoubleStart confirms calling Start twice fails.
func TestStartRejectsDoubleStart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pgaudit.log")
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatalf("create logfile: %v", err)
	}
	s := pgaudit.New(pgaudit.Config{LogFile: path})
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()
	if err := s.Start(context.Background()); err == nil {
		t.Error("second Start = nil, want error")
	}
}

// TestTranslateDDL covers the OCSF mapping for a DDL CREATE.
func TestTranslateDDL(t *testing.T) {
	r := pgaudit.Record{
		Class:      pgaudit.ClassDDL,
		Command:    "CREATE TABLE",
		ObjectType: "TABLE",
		ObjectName: "public.t",
		Timestamp:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	rec := pgaudit.Translate(r, "acme")
	if rec.ClassUID != ocsf.ClassEntityManagement {
		t.Errorf("ClassUID = %d, want %d", rec.ClassUID, ocsf.ClassEntityManagement)
	}
	if rec.ActivityID != ocsf.ActivityCreate {
		t.Errorf("ActivityID = %d, want %d (Create)", rec.ActivityID, ocsf.ActivityCreate)
	}
	if rec.Metadata.Version != ocsf.Version {
		t.Errorf("Metadata.Version = %q, want %q", rec.Metadata.Version, ocsf.Version)
	}
}

// TestTranslateDDLDropMapsToDelete confirms DROP statements land on
// ActivityDelete.
func TestTranslateDDLDropMapsToDelete(t *testing.T) {
	r := pgaudit.Record{Class: pgaudit.ClassDDL, Command: "DROP TABLE", ObjectType: "TABLE", ObjectName: "t"}
	rec := pgaudit.Translate(r, "acme")
	if rec.ActivityID != ocsf.ActivityDelete {
		t.Errorf("ActivityID = %d, want %d (Delete)", rec.ActivityID, ocsf.ActivityDelete)
	}
}

// TestClassIsValid covers the closed-enum check.
func TestClassIsValid(t *testing.T) {
	for _, c := range []pgaudit.Class{pgaudit.ClassDDL, pgaudit.ClassRole, pgaudit.ClassRead, pgaudit.ClassWrite, pgaudit.ClassFunction, pgaudit.ClassMisc} {
		if !c.IsValid() {
			t.Errorf("%q is a valid pgaudit class but IsValid reports false", c)
		}
	}
	if pgaudit.Class("BOGUS").IsValid() {
		t.Error("BOGUS class IsValid returned true")
	}
}

// TestNoOpSinkDoesNotError confirms the default sink swallows every
// record.
func TestNoOpSinkDoesNotError(t *testing.T) {
	sink := pgaudit.NoOpSink()
	if err := sink.Deliver(context.Background(), "t", "topic", ocsf.Record{}); err != nil {
		t.Errorf("NoOpSink.Deliver = %v, want nil", err)
	}
}
