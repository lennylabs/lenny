// SPDX-License-Identifier: MIT

package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/observability/correlation"
)

func mustParse(t *testing.T, line []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("invalid JSON log line: %v\nline: %s", err, line)
	}
	return m
}

// Every JSON record must include slog's required keys.
func TestHandlerEmitsRequiredSlogKeys(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(NewJSONHandler(&buf, Options{DefaultComponent: "gateway"}))
	l.InfoContext(context.Background(), "request received")

	rec := mustParse(t, buf.Bytes())
	// spec: §16.4 line 372 — the timestamp field is `ts`, not slog's
	// default `time`.
	for _, key := range []string{"ts", "level", "msg"} {
		if _, ok := rec[key]; !ok {
			t.Errorf("required key %q missing from record", key)
		}
	}
	if _, ok := rec["time"]; ok {
		t.Errorf("record must not carry slog's default `time` key; got %v", rec["time"])
	}
}

// spec: §16.4 line 372 — `ts` is renamed from slog's `time` and rendered
// in RFC 3339 UTC regardless of the process-local zone.
func TestHandlerEmitsTimestampAsTSInUTC_spec_16_4_372(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(NewJSONHandler(&buf, Options{DefaultComponent: "gateway"}))
	l.InfoContext(context.Background(), "tick")

	rec := mustParse(t, buf.Bytes())
	tsRaw, ok := rec["ts"].(string)
	if !ok {
		t.Fatalf("ts missing or not a string; record: %v", rec)
	}
	parsed, err := time.Parse(time.RFC3339Nano, tsRaw)
	if err != nil {
		t.Fatalf("ts %q is not RFC 3339: %v", tsRaw, err)
	}
	if _, offset := parsed.Zone(); offset != 0 {
		t.Errorf("ts must be UTC (zero offset), got offset %d in %q", offset, tsRaw)
	}
	if !strings.HasSuffix(tsRaw, "Z") {
		t.Errorf("ts must render the UTC zone as Z, got %q", tsRaw)
	}
}

// The text handler also renames `time` to `ts`.
func TestTextHandlerRenamesTimeToTS_spec_16_4_372(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(NewTextHandler(&buf, Options{DefaultComponent: "gateway"}))
	l.InfoContext(context.Background(), "tick")
	out := buf.String()
	if !strings.Contains(out, "ts=") {
		t.Errorf("text handler must emit ts=, got %q", out)
	}
	if strings.Contains(out, "time=") {
		t.Errorf("text handler must not emit slog's default time=, got %q", out)
	}
}

// spec: §16.4 lines 370-372 — Setup installs the JSON handler as the slog
// default and bridges the stdlib log package so legacy log.Printf sites
// surface as structured records carrying ts, level, msg, and component.
func TestSetupBridgesStdlibLog_spec_16_4_370(t *testing.T) {
	var buf bytes.Buffer
	prevLogger := slog.Default()
	prevFlags := log.Flags()
	prevOut := log.Writer()
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		log.SetFlags(prevFlags)
		log.SetOutput(prevOut)
	})

	t.Setenv("LENNY_LOG_LEVEL", "")
	Setup(&buf, "gateway")
	log.Printf("lenny-gateway: %s", "legacy line")

	rec := mustParse(t, []byte(strings.TrimSpace(buf.String())))
	if rec["msg"] != "lenny-gateway: legacy line" {
		t.Errorf("msg = %v, want bridged Printf string", rec["msg"])
	}
	if rec["component"] != "gateway" {
		t.Errorf("component = %v, want gateway", rec["component"])
	}
	if rec["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", rec["level"])
	}
	if _, ok := rec["ts"]; !ok {
		t.Errorf("bridged record missing ts: %v", rec)
	}
}

// LENNY_LOG_LEVEL raises the floor so sub-threshold stdlib lines drop.
func TestSetupHonoursLogLevelEnv(t *testing.T) {
	var buf bytes.Buffer
	prevLogger := slog.Default()
	prevFlags := log.Flags()
	prevOut := log.Writer()
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		log.SetFlags(prevFlags)
		log.SetOutput(prevOut)
	})

	t.Setenv("LENNY_LOG_LEVEL", "WARN")
	Setup(&buf, "gateway")
	log.Printf("info-level bridged line")
	if buf.Len() != 0 {
		t.Fatalf("Info-level stdlib line should drop below WARN, got %q", buf.String())
	}
	slog.Default().Warn("warn-level line")
	if buf.Len() == 0 {
		t.Fatal("Warn-level line should be emitted at the WARN floor")
	}
}

// The stdlib bridge strips the trailing newline log.Output appends.
func TestStdlogBridgeDropsTrailingNewline(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewJSONHandler(&buf, Options{DefaultComponent: "gateway"}))
	b := stdlogBridge{logger: logger}
	n, err := b.Write([]byte("trailing\n"))
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if n != len("trailing\n") {
		t.Errorf("Write returned %d bytes, want %d", n, len("trailing\n"))
	}
	rec := mustParse(t, []byte(strings.TrimSpace(buf.String())))
	if rec["msg"] != "trailing" {
		t.Errorf("msg = %v, want newline stripped", rec["msg"])
	}
}

// The default component falls through when no Fields is on the context.
func TestHandlerEmitsDefaultComponent(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(NewJSONHandler(&buf, Options{DefaultComponent: "gateway"}))
	l.InfoContext(context.Background(), "hi")

	rec := mustParse(t, buf.Bytes())
	if got, want := rec["component"], "gateway"; got != want {
		t.Errorf("component: want %q, got %v", want, got)
	}
}

// Fields on the context override the default component and add the spec
// correlation attributes.
func TestHandlerProjectsCorrelationFieldsFromContext(t *testing.T) {
	ctx := correlation.With(context.Background(), correlation.Fields{
		TraceID:     "0123456789abcdef0123456789abcdef",
		SpanID:      "0123456789abcdef",
		SessionID:   "sess_42",
		TenantID:    "acme",
		OperationID: "op_42",
		AgentName:   "alice-agent",
		Component:   "lenny-ops",
	})

	var buf bytes.Buffer
	l := slog.New(NewJSONHandler(&buf, Options{DefaultComponent: "gateway"}))
	l.InfoContext(ctx, "decision made")

	rec := mustParse(t, buf.Bytes())
	cases := map[string]string{
		"component":    "lenny-ops",
		"trace_id":     "0123456789abcdef0123456789abcdef",
		"span_id":      "0123456789abcdef",
		"session_id":   "sess_42",
		"tenant_id":    "acme",
		"operation_id": "op_42",
		"agent_name":   "alice-agent",
	}
	for k, want := range cases {
		got, ok := rec[k].(string)
		if !ok {
			t.Errorf("attribute %q missing or not a string; record: %v", k, rec)
			continue
		}
		if got != want {
			t.Errorf("attribute %q: want %q, got %q", k, want, got)
		}
	}
}

// Empty fields on the context must NOT appear in the record.
func TestHandlerSkipsEmptyCorrelationFields(t *testing.T) {
	ctx := correlation.With(context.Background(), correlation.Fields{
		Component: "gateway",
		TenantID:  "acme",
	})

	var buf bytes.Buffer
	l := slog.New(NewJSONHandler(&buf, Options{DefaultComponent: "gateway"}))
	l.InfoContext(ctx, "minimal")

	rec := mustParse(t, buf.Bytes())
	for _, missing := range []string{"trace_id", "span_id", "session_id", "task_id", "operation_id", "agent_name", "runtime_class", "pool", "request_id"} {
		if _, ok := rec[missing]; ok {
			t.Errorf("attribute %q should be absent for empty Fields value, got %v", missing, rec[missing])
		}
	}
}

// WithAttrs and WithGroup must produce handlers that still inject
// correlation attributes.
func TestHandlerWithAttrsPreservesCorrelation(t *testing.T) {
	ctx := correlation.With(context.Background(), correlation.Fields{
		Component: "gateway",
		TenantID:  "acme",
	})

	var buf bytes.Buffer
	base := slog.New(NewJSONHandler(&buf, Options{DefaultComponent: "gateway"}))
	l := base.With(slog.String("subsystem", "session_orchestrator"))
	l.InfoContext(ctx, "with attrs")

	rec := mustParse(t, buf.Bytes())
	if rec["subsystem"] != "session_orchestrator" {
		t.Errorf("With() attribute lost: %v", rec)
	}
	if rec["tenant_id"] != "acme" {
		t.Errorf("correlation lost through With(): %v", rec)
	}
}

func TestHandlerHonoursLevelGate(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(NewJSONHandler(&buf, Options{Level: slog.LevelWarn, DefaultComponent: "gateway"}))
	l.InfoContext(context.Background(), "below threshold")
	if buf.Len() != 0 {
		t.Fatalf("Info record below LevelWarn should be dropped, buf: %s", buf.String())
	}
	l.WarnContext(context.Background(), "above threshold")
	if buf.Len() == 0 {
		t.Fatal("Warn record at threshold should be emitted")
	}
}

func TestTextHandlerProjectsCorrelation(t *testing.T) {
	ctx := correlation.With(context.Background(), correlation.Fields{
		Component: "gateway",
		TenantID:  "acme",
	})
	var buf bytes.Buffer
	l := slog.New(NewTextHandler(&buf, Options{DefaultComponent: "gateway"}))
	l.InfoContext(ctx, "text")
	out := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte("tenant_id=acme")) {
		t.Errorf("text handler should include tenant_id=acme, got %q", out)
	}
	if !bytes.Contains(buf.Bytes(), []byte("component=gateway")) {
		t.Errorf("text handler should include component=gateway, got %q", out)
	}
}

func TestWrapPreservesDecoration(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	wrapped := Wrap(inner, "gateway")
	l := slog.New(wrapped)
	l.InfoContext(context.Background(), "wrapped")

	rec := mustParse(t, buf.Bytes())
	if rec["component"] != "gateway" {
		t.Errorf("Wrap() lost default component, record: %v", rec)
	}
}
