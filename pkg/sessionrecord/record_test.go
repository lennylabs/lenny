// SPDX-License-Identifier: MIT

package sessionrecord

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// spec: §15.4.1 line 1889 — RuntimeCrash synthesizes a RUNTIME_CRASH
// error from a non-zero exit code and stderr; §8.8 lines 936-938 classify
// it TRANSIENT.
func TestRuntimeCrash_spec_15_4_1_1889(t *testing.T) {
	e := RuntimeCrash(7, "panic: nil deref\n")
	if e.Code != "RUNTIME_CRASH" {
		t.Errorf("code = %q, want RUNTIME_CRASH", e.Code)
	}
	if e.Category != "TRANSIENT" {
		t.Errorf("category = %q, want TRANSIENT", e.Category)
	}
	if !strings.Contains(e.Message, "code 7") {
		t.Errorf("message omits exit code: %q", e.Message)
	}
	if !strings.Contains(e.Message, "panic: nil deref") {
		t.Errorf("message omits stderr: %q", e.Message)
	}
	// The Error block is returnable through a Go error value.
	var asErr error = e
	var te *Error
	if !errors.As(asErr, &te) || te.Code != "RUNTIME_CRASH" {
		t.Errorf("errors.As did not recover the RUNTIME_CRASH block")
	}
}

// spec: §15.4.1 line 1889 — a runtime that emits no stderr still
// produces a RUNTIME_CRASH naming the exit code, and an oversized stderr
// dump is capped to its tail.
func TestRuntimeCrashBounds_spec_15_4_1_1889(t *testing.T) {
	bare := RuntimeCrash(2, "")
	if strings.Contains(bare.Message, ": ") && strings.HasSuffix(bare.Message, ": ") {
		t.Errorf("empty stderr should not leave a trailing colon: %q", bare.Message)
	}
	if !strings.Contains(bare.Message, "code 2") {
		t.Errorf("bare crash omits exit code: %q", bare.Message)
	}
	huge := strings.Repeat("x", maxCrashStderrBytes+5000) + "TAILMARK"
	capped := RuntimeCrash(1, huge)
	if len(capped.Message) > maxCrashStderrBytes+200 {
		t.Errorf("crash message not capped: %d bytes", len(capped.Message))
	}
	if !strings.Contains(capped.Message, "TAILMARK") {
		t.Error("capped crash dropped the diagnostic stderr tail")
	}
}

// spec: §15.4.1 lines 1530-1531 — a `text` OutputPart guarantees type,
// inline, mimeType (text/plain) and carries its own schemaVersion.
func TestTextPart_spec_15_4_1(t *testing.T) {
	p := TextPart("hello")
	if p.Type != "text" || p.MimeType != "text/plain" || p.Inline != "hello" {
		t.Fatalf("TextPart = %+v, want text/text-plain/hello", p)
	}
	if p.SchemaVersion != SchemaVersion {
		t.Errorf("part schemaVersion = %d, want %d", p.SchemaVersion, SchemaVersion)
	}
	if p.Status != "complete" {
		t.Errorf("part status = %q, want complete", p.Status)
	}
}

// spec: §8.8 lines 825-827; §15.5 item 7 — the envelope schemaVersion is
// immutable once the first writer sets it. ReconcileSchemaVersion keeps
// an existing non-zero version and only fills in the producer version
// for a brand-new record.
func TestReconcileSchemaVersion_spec_8_8_825(t *testing.T) {
	cases := []struct {
		existing, producer, want int
	}{
		{0, 1, 1}, // new record: producer version wins
		{1, 1, 1}, // same version: unchanged
		{1, 2, 1}, // immutable: a schema-2 writer must not bump a schema-1 record
		{2, 1, 2}, // a record already at 2 is preserved even by a v1 writer
	}
	for _, c := range cases {
		if got := ReconcileSchemaVersion(c.existing, c.producer); got != c.want {
			t.Errorf("ReconcileSchemaVersion(%d,%d) = %d, want %d", c.existing, c.producer, got, c.want)
		}
	}
}

// spec: §8.8 lines 936-938 — retriesExhausted is the precise
// budget-consumed comparison when maxRetries is known, and the
// row-only "retried at least once" witness when it is not.
func TestRetriesExhausted_spec_8_8_936(t *testing.T) {
	cases := []struct {
		name       string
		retryCount int64
		maxRetries int
		want       bool
	}{
		{"budget consumed", 2, 2, true},
		{"budget left", 1, 2, false},
		{"over budget", 3, 2, true},
		{"unknown budget, retried", 1, 0, true},
		{"unknown budget, no retry", 0, 0, false},
		{"permanent failure, zero retries", 0, 3, false},
	}
	for _, c := range cases {
		if got := RetriesExhausted(c.retryCount, c.maxRetries); got != c.want {
			t.Errorf("%s: RetriesExhausted(%d,%d) = %v, want %v", c.name, c.retryCount, c.maxRetries, got, c.want)
		}
	}
}

// spec: §8.8 lines 806-823 — the TaskRecord envelope serializes with the
// canonical field names and a messages array carrying caller/agent
// entries with per-entry OutputParts.
func TestRecord_JSON_spec_8_8_806(t *testing.T) {
	rec := Record{
		SchemaVersion: SchemaVersion,
		TaskID:        "task_abc",
		SessionID:     "sess_xyz",
		State:         "completed",
		Messages: []Message{
			{Role: RoleCaller, Parts: []OutputPart{TextPart("do the thing")}},
			{Role: RoleAgent, Parts: []OutputPart{TextPart("done")}, State: "completed"},
		},
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"schemaVersion", "taskId", "sessionId", "state", "messages"} {
		if _, ok := got[k]; !ok {
			t.Errorf("record JSON missing %q: %s", k, b)
		}
	}
	// usage / treeUsage are absent (omitempty) when not populated — the
	// §8.8 "treeUsage null until descendants settle" contract.
	if _, ok := got["treeUsage"]; ok {
		t.Errorf("unpopulated treeUsage should be absent, got: %s", b)
	}
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages len = %d, want 2", len(msgs))
	}
	agent, _ := msgs[1].(map[string]any)
	if agent["role"] != RoleAgent || agent["state"] != "completed" {
		t.Errorf("agent message = %v, want role=agent state=completed", agent)
	}
}

// spec: §8.8 lines 904-917 — treeUsage flattens the usage fields and
// adds totalTasks; the embedded Usage promotes inline rather than
// nesting under a "usage" key.
func TestTreeUsage_JSON_spec_8_8_904(t *testing.T) {
	tu := TreeUsage{Usage: Usage{InputTokens: 45000, OutputTokens: 22000, WallClockSeconds: 450, PodMinutes: 12.5, CredentialLeaseMinutes: 10.2}, TotalTasks: 4}
	b, err := json.Marshal(tu)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	for _, k := range []string{"inputTokens", "outputTokens", "wallClockSeconds", "podMinutes", "credentialLeaseMinutes", "totalTasks"} {
		if _, ok := got[k]; !ok {
			t.Errorf("treeUsage JSON missing flattened field %q: %s", k, b)
		}
	}
	if _, ok := got["Usage"]; ok {
		t.Errorf("embedded Usage must promote inline, not nest: %s", b)
	}
}

// spec: §8.8 lines 922-940 — the failure example carries an output: null
// (absent) and a populated error block; the completed example carries
// output and a nil error.
func TestResult_JSON_outputErrorMutualExclusion_spec_8_8_922(t *testing.T) {
	failed := Result{
		SchemaVersion: SchemaVersion,
		TaskID:        "child_abc",
		State:         "failed",
		Error:         &Error{Code: "RUNTIME_CRASH", Category: "TRANSIENT", Message: "boom", RetriesExhausted: true},
	}
	b, _ := json.Marshal(failed)
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	if _, ok := got["output"]; ok {
		t.Errorf("failed result must omit output, got: %s", b)
	}
	errBlock, _ := got["error"].(map[string]any)
	if errBlock["category"] != "TRANSIENT" || errBlock["retriesExhausted"] != true {
		t.Errorf("error block = %v, want category=TRANSIENT retriesExhausted=true", errBlock)
	}

	completed := Result{
		SchemaVersion: SchemaVersion,
		TaskID:        "child_abc",
		State:         "completed",
		Output:        &Output{Parts: []OutputPart{TextPart("result")}, ArtifactRefs: []string{}},
	}
	b, _ = json.Marshal(completed)
	got = map[string]any{}
	_ = json.Unmarshal(b, &got)
	if _, ok := got["error"]; ok {
		t.Errorf("completed result must omit error, got: %s", b)
	}
	out, ok := got["output"].(map[string]any)
	if !ok {
		t.Fatalf("completed result must carry output, got: %s", b)
	}
	if _, ok := out["artifactRefs"]; !ok {
		t.Errorf("output must always carry artifactRefs (possibly empty): %s", b)
	}
}

// spec: §15.4 (MessageEnvelope.input oneOf(string, OutputPart[])),
// §15.2.1 (REST/MCP parity).
//
// MessageContent is the §15.4 message-input union: a bare string is sugar
// for a single text OutputPart, a part array is the structured form, and
// the type round-trips the wire form it was unmarshalled from so a
// buffered message re-delivers identically.
func TestMessageContentUnion_spec_15_4(t *testing.T) {
	t.Run("bare string is a single text part", func(t *testing.T) {
		var mc MessageContent
		if err := json.Unmarshal([]byte(`"hello world"`), &mc); err != nil {
			t.Fatalf("unmarshal bare string: %v", err)
		}
		parts := mc.Parts()
		if len(parts) != 1 {
			t.Fatalf("bare string must become exactly one part, got %d", len(parts))
		}
		if parts[0].Type != "text" || parts[0].Inline != "hello world" {
			t.Errorf("bare string part = %+v, want text/inline=hello world", parts[0])
		}
		if mc.Text() != "hello world" {
			t.Errorf("Text() = %q, want %q", mc.Text(), "hello world")
		}
		// Round-trips back to the bare-string wire form.
		b, err := json.Marshal(mc)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(b) != `"hello world"` {
			t.Errorf("bare-string round-trip = %s, want %q", b, `"hello world"`)
		}
	})

	t.Run("OutputPart array decodes verbatim", func(t *testing.T) {
		raw := `[{"type":"text","inline":"a"},{"type":"image","ref":"lenny-blob://t/s/p","mimeType":"image/png"}]`
		var mc MessageContent
		if err := json.Unmarshal([]byte(raw), &mc); err != nil {
			t.Fatalf("unmarshal part array: %v", err)
		}
		parts := mc.Parts()
		if len(parts) != 2 {
			t.Fatalf("part array len = %d, want 2", len(parts))
		}
		if parts[1].Type != "image" || parts[1].Ref != "lenny-blob://t/s/p" {
			t.Errorf("second part = %+v, want image/ref", parts[1])
		}
		// Text() concatenates only the text parts; the image contributes none.
		if mc.Text() != "a" {
			t.Errorf("Text() = %q, want %q (image part contributes no text)", mc.Text(), "a")
		}
		// A part-array input round-trips to an array, never a bare string.
		b, err := json.Marshal(mc)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if b[0] != '[' {
			t.Errorf("part-array round-trip must stay an array, got %s", b)
		}
	})

	t.Run("null is empty content", func(t *testing.T) {
		// spec: §15.4 — a JSON null content is the empty value (no parts).
		var mc MessageContent
		if err := json.Unmarshal([]byte(`null`), &mc); err != nil {
			t.Fatalf("unmarshal null: %v", err)
		}
		if !mc.IsEmpty() {
			t.Errorf("null must be empty, got parts %+v", mc.Parts())
		}
		// An absent content field on an enclosing struct leaves the zero
		// value, which is also empty.
		var wrap struct {
			Content MessageContent `json:"content"`
		}
		if err := json.Unmarshal([]byte(`{}`), &wrap); err != nil {
			t.Fatalf("unmarshal absent content: %v", err)
		}
		if !wrap.Content.IsEmpty() {
			t.Errorf("absent content must be empty, got %+v", wrap.Content.Parts())
		}
	})

	t.Run("non-string non-array shapes are rejected", func(t *testing.T) {
		// spec: §15.4 — the union admits only a string or an array; an object,
		// number, or bool body is a validation error rather than silently
		// coerced.
		for _, in := range []string{`{"type":"text"}`, `42`, `true`} {
			var mc MessageContent
			if err := json.Unmarshal([]byte(in), &mc); err == nil {
				t.Errorf("input %q must be rejected, got %+v", in, mc.Parts())
			}
		}
	})

	t.Run("constructors", func(t *testing.T) {
		if got := MessageContentFromText("x").Text(); got != "x" {
			t.Errorf("MessageContentFromText(x).Text() = %q", got)
		}
		fromParts := MessageContentFromParts([]OutputPart{TextPart("p")})
		if len(fromParts.Parts()) != 1 || fromParts.Text() != "p" {
			t.Errorf("MessageContentFromParts = %+v", fromParts)
		}
		// A constructed-from-parts value marshals to an array, not a string.
		b, _ := json.Marshal(fromParts)
		if b[0] != '[' {
			t.Errorf("MessageContentFromParts must marshal as array, got %s", b)
		}
	})
}
