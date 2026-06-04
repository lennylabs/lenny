// SPDX-License-Identifier: MIT

// Package pgaudit is the §4.4 line 232 / §11.7 pgaudit sink consumer.
// It tails a Postgres-emitted pgaudit log file, parses each line,
// translates it into an OCSF v1.1.0 record (preserving the
// pgaudit-source classification under unmapped.lenny.pgaudit_class so
// downstream SIEM consumers can route DDL / ROLE events independently),
// and delivers the record to a configurable sink.
//
// pgaudit emits comma-separated records: AUDIT,<class>,<statement_id>,
// <substatement_id>,<command>,<object_type>,<object_name>,
// <statement>,<parameter>. v1 parses the eight fields the spec calls
// out for DDL / ROLE tracking; deployers can extend the parser by
// satisfying the LineParser interface.
//
// The shipper is intentionally narrow: it is a tail-and-translate
// pipeline, not a Postgres extension manager. Deployers run pgaudit
// against their Postgres instance and point the shipper at the log
// file; the shipper handles parsing, translation, and delivery. The
// §11.7 line 375 startup pre-flight (verifying the pgaudit extension is
// installed and `pgaudit.log` is configured with the DDL and ROLE
// classes) is implemented in preflight.go.
//
// spec: §4.4 line 232 — "pgaudit sink consumers" listed among OCSF
// egress targets.
package pgaudit

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
)

// Class is the pgaudit message-class enum. pgaudit emits one of these
// per record so the shipper can classify the OCSF record's
// activity_id and the routing label downstream.
type Class string

const (
	// ClassDDL: data-definition statements (CREATE, ALTER, DROP, etc.).
	ClassDDL Class = "DDL"
	// ClassRole: role-management statements (GRANT, REVOKE, etc.).
	ClassRole Class = "ROLE"
	// ClassRead: SELECT-class statements.
	ClassRead Class = "READ"
	// ClassWrite: INSERT/UPDATE/DELETE/MERGE statements.
	ClassWrite Class = "WRITE"
	// ClassFunction: function and procedure executions.
	ClassFunction Class = "FUNCTION"
	// ClassMisc: anything else pgaudit logs.
	ClassMisc Class = "MISC"
)

// Record is one parsed pgaudit log line. Every field is verbatim from
// the comma-separated pgaudit record; the translator builds an OCSF
// envelope around this on egress.
type Record struct {
	// Timestamp is the wall-clock instant the shipper observed the
	// line. The pgaudit-emitted line itself does not carry a per-line
	// timestamp; the surrounding log_line_prefix carries it but is
	// not parsed by v1 because operators can configure any prefix.
	Timestamp time.Time
	// Class is the pgaudit message class (DDL, ROLE, etc.).
	Class Class
	// StatementID is the per-session pgaudit statement counter.
	StatementID string
	// SubstatementID counts statements expanded from a single
	// top-level statement (e.g., a function call's nested SQL).
	SubstatementID string
	// Command is the SQL command (CREATE TABLE, GRANT, etc.).
	Command string
	// ObjectType is the object kind the command targets (TABLE, ROLE).
	ObjectType string
	// ObjectName is the object name the command targets.
	ObjectName string
	// Statement is the full SQL statement text.
	Statement string
	// Parameter is the substituted parameter list (empty when none).
	Parameter string
}

// Sink consumes a translated OCSF record (the pgaudit-source flavour).
// Implementations may deliver to file, HTTP webhook, or any other
// downstream — the contract is identical to pkg/audit/ocsf.Sink: the
// call returns nil on durable acceptance.
type Sink interface {
	// Deliver hands one OCSF record to the sink. tenantID is the
	// pgaudit owner tenant; for a platform-shared Postgres instance
	// the shipper stamps "platform" by default. The topic is the
	// EventTopic the record rides; for pgaudit-source records v1
	// uses "pgaudit".
	Deliver(ctx context.Context, tenantID, topic string, rec ocsf.Record) error
}

// Metrics is the shipper's metric surface. A nil Metrics is a valid
// no-op.
type Metrics interface {
	// PgAuditEvent counts one shipped record labeled by class
	// (DDL, ROLE, READ, etc.). This is the
	// lenny_pgaudit_grant_events_total counter per §4.4 / §11.7.
	PgAuditEvent(class Class)
	// PgAuditDeliveryFailed counts a sink-delivery failure so the
	// §16.5 PgAuditSinkDeliveryFailed alert can fire.
	PgAuditDeliveryFailed(class Class)
	// PgAuditParseFailed counts a line that did not parse (e.g.,
	// pgaudit emitted a record shape the shipper doesn't recognise).
	PgAuditParseFailed()
}

// noOpSink is the default Sink — used when the shipper is wired
// without an explicit sink so the parse + metric paths still run.
type noOpSink struct{}

// Deliver is a no-op.
func (noOpSink) Deliver(context.Context, string, string, ocsf.Record) error { return nil }

// NoOpSink returns a Sink that discards every record. Useful for
// dev-mode wiring where pgaudit is configured but no downstream
// consumer is.
func NoOpSink() Sink { return noOpSink{} }

// Config pins the shipper's runtime parameters.
type Config struct {
	// LogFile is the path to the pgaudit log file the shipper tails.
	// Required when Start is called.
	LogFile string
	// TenantID stamped onto every OCSF record. Defaults to "platform"
	// when empty (the regulated-Postgres-instance default).
	TenantID string
	// Sink consumes each translated record. nil selects NoOpSink.
	Sink Sink
	// Metrics receives per-record counters. nil is permitted.
	Metrics Metrics
	// Now overrides time.Now for the per-record timestamp. nil
	// selects time.Now.
	Now func() time.Time
}

// Shipper is the §4.4 line 232 pgaudit log shipper. It tails the
// configured log file, parses each line, translates to OCSF, and
// delivers to the sink. Construct with New.
type Shipper struct {
	cfg Config

	mu      sync.Mutex
	started bool
	stop    chan struct{}
	done    chan struct{}
}

// New returns a Shipper over cfg. The shipper is inert until Start is
// called.
func New(cfg Config) *Shipper {
	if cfg.TenantID == "" {
		cfg.TenantID = "platform"
	}
	if cfg.Sink == nil {
		cfg.Sink = NoOpSink()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Shipper{cfg: cfg}
}

// ProcessLine parses one pgaudit log line, translates it to OCSF, and
// delivers it to the configured sink. Exposed for unit tests so the
// translator + sink path can be exercised without a tempfile.
func (s *Shipper) ProcessLine(ctx context.Context, line string) error {
	rec, ok := ParseLine(line)
	if !ok {
		if s.cfg.Metrics != nil {
			s.cfg.Metrics.PgAuditParseFailed()
		}
		return nil
	}
	rec.Timestamp = s.cfg.Now().UTC()
	ocsfRec := Translate(rec, s.cfg.TenantID)
	if err := s.cfg.Sink.Deliver(ctx, s.cfg.TenantID, "pgaudit", ocsfRec); err != nil {
		if s.cfg.Metrics != nil {
			s.cfg.Metrics.PgAuditDeliveryFailed(rec.Class)
		}
		return err
	}
	if s.cfg.Metrics != nil {
		s.cfg.Metrics.PgAuditEvent(rec.Class)
	}
	return nil
}

// Start begins tailing the log file in a background goroutine. The
// shipper reads new lines as they are appended; when the file is
// rotated (truncated) the shipper continues reading from offset 0.
// Returns the first error opening the file; the goroutine surfaces
// runtime errors via the Metrics surface.
func (s *Shipper) Start(ctx context.Context) error {
	if s.cfg.LogFile == "" {
		return errors.New("pgaudit: LogFile is required to Start")
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("pgaudit: Shipper already started")
	}
	f, err := os.Open(s.cfg.LogFile)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("pgaudit: open %s: %w", s.cfg.LogFile, err)
	}
	s.started = true
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	s.mu.Unlock()

	go s.tailLoop(ctx, f)
	return nil
}

// Stop signals the tail loop to exit and waits for it to drain. Safe
// to call multiple times.
func (s *Shipper) Stop() {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	close(s.stop)
	done := s.done
	s.started = false
	s.mu.Unlock()
	<-done
}

// tailLoop is the goroutine that reads new lines from the log file
// and dispatches each to ProcessLine. On EOF it sleeps briefly and
// resumes; on truncation (file size shrank below offset) it seeks to
// 0. Cancellation via ctx or Stop exits the loop.
func (s *Shipper) tailLoop(ctx context.Context, f *os.File) {
	defer close(s.done)
	defer func() { _ = f.Close() }()

	r := bufio.NewReader(f)
	var leftover string
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		default:
		}
		chunk, err := r.ReadString('\n')
		if err == nil {
			line := leftover + strings.TrimRight(chunk, "\n")
			leftover = ""
			if line == "" {
				continue
			}
			_ = s.ProcessLine(ctx, line)
			continue
		}
		if err == io.EOF {
			if chunk != "" {
				leftover += chunk
			}
			// Check for truncation: if the underlying file is shorter
			// than the current offset, the file was rotated.
			if rotated, _ := wasTruncated(f); rotated {
				if _, _ = f.Seek(0, io.SeekStart); true {
					r = bufio.NewReader(f)
					leftover = ""
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-s.stop:
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		// Any other read error — log via metric and exit the loop.
		if s.cfg.Metrics != nil {
			s.cfg.Metrics.PgAuditParseFailed()
		}
		return
	}
}

// wasTruncated reports whether the file's size is now smaller than
// the current read offset (i.e., the file was truncated or rotated
// under the shipper).
func wasTruncated(f *os.File) (bool, error) {
	stat, err := f.Stat()
	if err != nil {
		return false, err
	}
	offset, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return false, err
	}
	return stat.Size() < offset, nil
}

// ParseLine parses one pgaudit-emitted log line. pgaudit emits the
// record after a `AUDIT:` marker, with the eight fields
// comma-separated. The function returns ok=false when the line lacks
// the marker or has fewer than nine fields.
//
// pgaudit example:
//
//	2026-05-22 12:00:00.000 UTC [12345] AUDIT: SESSION,1,1,DDL,CREATE TABLE,TABLE,public.t,"CREATE TABLE t(id int);",<not logged>
//
// v1 splits on commas at the top level only; embedded commas inside
// the double-quoted statement / parameter fields are preserved by
// counting the quote balance. A line that does not start with AUDIT
// after a literal "AUDIT: " is rejected.
func ParseLine(line string) (Record, bool) {
	idx := strings.Index(line, "AUDIT:")
	if idx < 0 {
		return Record{}, false
	}
	body := strings.TrimSpace(line[idx+len("AUDIT:"):])
	fields := splitTopLevel(body)
	// pgaudit emits 9 fields after AUDIT: (audit_type, statement_id,
	// substatement_id, class, command, object_type, object_name,
	// statement, parameter).
	if len(fields) < 9 {
		return Record{}, false
	}
	cls := Class(strings.ToUpper(fields[3]))
	if !cls.IsValid() {
		cls = ClassMisc
	}
	return Record{
		Class:          cls,
		StatementID:    fields[1],
		SubstatementID: fields[2],
		Command:        fields[4],
		ObjectType:     fields[5],
		ObjectName:     fields[6],
		Statement:      stripQuotes(fields[7]),
		Parameter:      stripQuotes(fields[8]),
	}, true
}

// splitTopLevel splits s on commas at the top level only, preserving
// commas inside double-quoted segments. The result has every field
// trimmed of surrounding whitespace.
func splitTopLevel(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			inQuote = !inQuote
			cur.WriteByte(c)
			continue
		}
		if c == ',' && !inQuote {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	out = append(out, strings.TrimSpace(cur.String()))
	return out
}

// stripQuotes removes one pair of surrounding double quotes when
// present. pgaudit double-quotes the statement and parameter fields
// when they contain commas; for v1 the quoted form is undone so the
// OCSF translator carries the canonical text.
func stripQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// IsValid reports whether c is one of the §4.4 pgaudit classes.
func (c Class) IsValid() bool {
	switch c {
	case ClassDDL, ClassRole, ClassRead, ClassWrite, ClassFunction, ClassMisc:
		return true
	}
	return false
}

// Translate renders one parsed pgaudit Record into an OCSF v1.1.0
// record. The class drives the OCSF class_uid / activity_id; the
// statement / parameter / object fields land in
// `unmapped.lenny.pgaudit_*` so downstream consumers see the original
// pgaudit payload without lossy re-mapping.
//
// spec: §4.4 line 232 — pgaudit sink consumers receive OCSF records.
func Translate(r Record, tenantID string) ocsf.Record {
	classUID, categoryUID, activityID := classifyOCSF(r.Class, r.Command)
	rec := ocsf.Record{
		ClassUID:    classUID,
		CategoryUID: categoryUID,
		ActivityID:  activityID,
		TypeUID:     classUID*100 + activityID,
		Time:        r.Timestamp.UTC().UnixMilli(),
		SeverityID:  1, // Informational baseline; bump as classifier-specific
		Metadata: ocsf.Metadata{
			UID:       tenantID + ":pgaudit:" + r.StatementID + ":" + r.SubstatementID,
			Sequence:  0, // pgaudit has no chain sequence
			TenantUID: tenantID,
			Version:   ocsf.Version,
			Product:   ocsf.Product{Name: "Lenny", VendorName: "Lenny"},
		},
		Resources: []ocsf.Resource{{Type: r.ObjectType, UID: r.ObjectName}},
		Unmapped: ocsf.Unmapped{
			Chain: ocsf.ChainExt{
				PrevHash:  "",
				Integrity: string(audit.ChainUnchecked),
			},
			Lenny: map[string]any{
				"pgaudit_class":           string(r.Class),
				"pgaudit_statement_id":    r.StatementID,
				"pgaudit_substatement_id": r.SubstatementID,
				"pgaudit_command":         r.Command,
				"pgaudit_statement":       r.Statement,
				"pgaudit_parameter":       r.Parameter,
			},
		},
	}
	// ROLE-class events (GRANT/REVOKE) are security-salient; bump
	// severity so SIEM dashboards highlight them.
	if r.Class == ClassRole {
		rec.SeverityID = 3 // Medium
	}
	return rec
}

// classifyOCSF maps a pgaudit (class, command) pair onto the OCSF
// (class_uid, category_uid, activity_id) triple. The map is narrow on
// purpose — pgaudit's vocabulary is much smaller than the full OCSF
// surface; v1 covers DDL / ROLE / READ / WRITE; everything else lands
// as API Activity / Other.
func classifyOCSF(cls Class, command string) (int, int, int) {
	cmd := strings.ToUpper(command)
	switch cls {
	case ClassDDL:
		// Entity create/update/delete (class 5001).
		activity := ocsf.ActivityCreate
		switch {
		case strings.HasPrefix(cmd, "DROP"):
			activity = ocsf.ActivityDelete
		case strings.HasPrefix(cmd, "ALTER"):
			activity = ocsf.ActivityUpdate
		}
		return ocsf.ClassEntityManagement, 5 /*categoryDiscovery*/, activity
	case ClassRole:
		// Account / role change (class 3006).
		return ocsf.ClassAccountChange, 3 /*categoryIAM*/, ocsf.ActivityUpdate
	case ClassRead:
		// API activity (class 6003), Read.
		return ocsf.ClassAPIActivity, 6 /*categoryApplication*/, ocsf.ActivityRead
	case ClassWrite:
		// API activity (class 6003), Update.
		return ocsf.ClassAPIActivity, 6, ocsf.ActivityUpdate
	}
	return ocsf.ClassAPIActivity, 6, ocsf.ActivityUnknown
}
