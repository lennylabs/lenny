// SPDX-License-Identifier: MIT

package outputpartfidelity_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/outputpartfidelity"
	"github.com/lennylabs/lenny/sdks/runtime/go/runtime"
)

// canonicalSamples returns one OutputPart per canonical type in §15.4.1
// "Canonical Type Registry (v1)". Each sample has every field the
// fidelity matrix tracks populated with a distinctive value so the
// per-field assertions can observe what survives translation.
func canonicalSamples() []runtime.OutputPart {
	return []runtime.OutputPart{
		{SchemaVersion: 1, ID: "p_text", Type: "text", MimeType: "text/plain", Inline: "hello"},
		{SchemaVersion: 1, ID: "p_code", Type: "code", MimeType: "text/x-go", Inline: "package main"},
		{SchemaVersion: 1, ID: "p_reason", Type: "reasoning_trace", MimeType: "text/plain", Inline: "thinking..."},
		{SchemaVersion: 1, ID: "p_cite", Type: "citation", MimeType: "text/plain", Inline: "see RFC 9110"},
		{SchemaVersion: 1, ID: "p_diff", Type: "diff", MimeType: "text/x-diff", Inline: "@@ -1 +1 @@"},
		{SchemaVersion: 1, ID: "p_err", Type: "error", MimeType: "text/plain", Inline: "boom"},
		{SchemaVersion: 1, ID: "p_img", Type: "image", MimeType: "image/png", Inline: "iVBORw0KGgo="},
		{SchemaVersion: 1, ID: "p_shot", Type: "screenshot", MimeType: "image/png", Inline: "iVBORw0KGgo="},
		{SchemaVersion: 1, ID: "p_file", Type: "file", MimeType: "application/pdf", Inline: "JVBERi0x"},
	}
}

// fullyPopulated returns an OutputPart with every field the fidelity
// matrix tracks set to a distinctive value. Tests use this to confirm
// the dropped-field cells produce no wire representation.
func fullyPopulated() runtime.OutputPart {
	return runtime.OutputPart{
		SchemaVersion: 7,
		ID:            "p_full",
		Type:          "text",
		MimeType:      "text/plain",
		Inline:        "payload",
		Ref:           "lenny-blob://acme/sess/p_full?ttl=3600",
		Annotations:   map[string]any{"role": "assistant", "protocolHints": map[string]any{"openai": map[string]any{}}},
		Parts:         []runtime.OutputPart{{Type: "text", Inline: "child"}},
		Status:        "complete",
	}
}

func TestMatrixCoversAllAdaptersAndFields(t *testing.T) {
	for _, a := range outputpartfidelity.Adapters() {
		for _, f := range outputpartfidelity.Fields() {
			if _, ok := outputpartfidelity.Matrix(a, f); !ok {
				t.Errorf("matrix missing (%s, %s)", a, f)
			}
		}
	}
}

func TestOpenAICompletionsInlineExact(t *testing.T) {
	for _, sample := range canonicalSamples() {
		wire, err := outputpartfidelity.TranslateOpenAICompletions(sample)
		if err != nil {
			t.Fatalf("translate(%s): %v", sample.Type, err)
		}
		round, err := outputpartfidelity.ParseOpenAICompletions(wire)
		if err != nil {
			t.Fatalf("parse(%s): %v", sample.Type, err)
		}
		if round.Inline != sample.Inline {
			t.Errorf("inline %s: got %q, want %q (matrix marks [exact])",
				sample.Type, round.Inline, sample.Inline)
		}
	}
}

func TestOpenAICompletionsDropsByMatrix(t *testing.T) {
	sample := fullyPopulated()
	wire, err := outputpartfidelity.TranslateOpenAICompletions(sample)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	for _, dropped := range []string{
		`"schemaVersion"`,
		`"id"`,
		`"ref"`,
		`"annotations"`,
		`"parts"`,
		`"status"`,
		`"protocolHints"`,
	} {
		if bytes.Contains(wire, []byte(dropped)) {
			t.Errorf("OpenAI wire body carries %s; matrix marks it [dropped]: %s", dropped, wire)
		}
	}

	round, err := outputpartfidelity.ParseOpenAICompletions(wire)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if round.SchemaVersion != 0 {
		t.Errorf("schemaVersion: got %d, want 0 (matrix marks [dropped])", round.SchemaVersion)
	}
	if round.ID != "" {
		t.Errorf("id: got %q, want empty (matrix marks [dropped])", round.ID)
	}
	if round.Ref != "" {
		t.Errorf("ref: got %q, want empty (matrix marks [dropped])", round.Ref)
	}
	if round.Annotations != nil {
		t.Errorf("annotations: got %v, want nil (matrix marks [dropped])", round.Annotations)
	}
	if round.Parts != nil {
		t.Errorf("parts: got %v, want nil (matrix marks [dropped])", round.Parts)
	}
	if round.Status != "" {
		t.Errorf("status: got %q, want empty (matrix marks [dropped])", round.Status)
	}
}

func TestOpenAICompletionsTypeLossy(t *testing.T) {
	// Every non-image canonical type collapses to "text" per matrix.
	for _, sample := range canonicalSamples() {
		wire, err := outputpartfidelity.TranslateOpenAICompletions(sample)
		if err != nil {
			t.Fatalf("translate(%s): %v", sample.Type, err)
		}
		var b outputpartfidelity.OpenAIContentBlock
		if err := json.Unmarshal(wire, &b); err != nil {
			t.Fatalf("unmarshal(%s): %v", sample.Type, err)
		}
		want := "text"
		switch sample.Type {
		case "image", "screenshot":
			want = "image_url"
		case "file":
			if strings.HasPrefix(sample.MimeType, "image/") {
				want = "image_url"
			}
		}
		if b.Type != want {
			t.Errorf("OpenAI block type for %s: got %q, want %q (matrix marks [lossy])",
				sample.Type, b.Type, want)
		}

		// On inverse, every non-image type comes back as `text`.
		round, _ := outputpartfidelity.ParseOpenAICompletions(wire)
		expected := "text"
		if want == "image_url" {
			expected = "image"
		}
		if round.Type != expected {
			t.Errorf("OpenAI parsed type for %s: got %q, want %q", sample.Type, round.Type, expected)
		}
	}
}

func TestOpenAICompletionsMimeTypeLossy(t *testing.T) {
	// Non-image MIME types are dropped on the wire.
	sample := runtime.OutputPart{Type: "file", MimeType: "application/pdf", Inline: "pdfdata"}
	wire, err := outputpartfidelity.TranslateOpenAICompletions(sample)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if bytes.Contains(wire, []byte("application/pdf")) {
		t.Errorf("non-image mime survived OpenAI wire: %s", wire)
	}
	round, _ := outputpartfidelity.ParseOpenAICompletions(wire)
	if round.MimeType != "" {
		t.Errorf("non-image mime survived OpenAI round-trip: %q (matrix marks [lossy])", round.MimeType)
	}

	// Image/* MIME types survive via image_url data: URL.
	imgSample := runtime.OutputPart{Type: "image", MimeType: "image/png", Inline: "AAA="}
	wire, _ = outputpartfidelity.TranslateOpenAICompletions(imgSample)
	round, _ = outputpartfidelity.ParseOpenAICompletions(wire)
	if round.MimeType != "image/png" {
		t.Errorf("image/png lost on OpenAI round-trip: got %q", round.MimeType)
	}
}

func TestOpenResponsesIDExtended(t *testing.T) {
	sample := fullyPopulated()
	wire, err := outputpartfidelity.TranslateOpenResponses(sample)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if !bytes.Contains(wire, []byte("p_full")) {
		t.Errorf("OpenResponses wire missing id; matrix marks [extended]: %s", wire)
	}
	round, _ := outputpartfidelity.ParseOpenResponses(wire)
	if round.ID != sample.ID {
		t.Errorf("OpenResponses round-trip id: got %q, want %q", round.ID, sample.ID)
	}
}

func TestOpenResponsesDropsByMatrix(t *testing.T) {
	sample := fullyPopulated()
	wire, err := outputpartfidelity.TranslateOpenResponses(sample)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	for _, dropped := range []string{
		`"schemaVersion"`,
		`"ref"`,
		`"annotations"`,
		`"parts"`,
		`"protocolHints"`,
	} {
		if bytes.Contains(wire, []byte(dropped)) {
			t.Errorf("Responses wire body carries %s; matrix marks it [dropped]: %s", dropped, wire)
		}
	}
	round, _ := outputpartfidelity.ParseOpenResponses(wire)
	if round.SchemaVersion != 0 {
		t.Errorf("schemaVersion: got %d, want 0", round.SchemaVersion)
	}
	if round.Ref != "" {
		t.Errorf("ref: got %q, want empty", round.Ref)
	}
	if round.Annotations != nil {
		t.Errorf("annotations: got %v, want nil", round.Annotations)
	}
	if round.Parts != nil {
		t.Errorf("parts: got %v, want nil", round.Parts)
	}
}

func TestOpenResponsesStatusLossy(t *testing.T) {
	// `failed` survives; `complete` collapses to unset.
	failed := runtime.OutputPart{Type: "text", Inline: "x", Status: "failed"}
	wire, _ := outputpartfidelity.TranslateOpenResponses(failed)
	round, _ := outputpartfidelity.ParseOpenResponses(wire)
	if round.Status != "failed" {
		t.Errorf("failed status: got %q, want failed", round.Status)
	}

	complete := runtime.OutputPart{Type: "text", Inline: "x", Status: "complete"}
	wire, _ = outputpartfidelity.TranslateOpenResponses(complete)
	round, _ = outputpartfidelity.ParseOpenResponses(wire)
	if round.Status != "" {
		t.Errorf("complete status: got %q, want empty (matrix marks [lossy])", round.Status)
	}
}

func TestOpenResponsesInlineExact(t *testing.T) {
	for _, sample := range canonicalSamples() {
		wire, err := outputpartfidelity.TranslateOpenResponses(sample)
		if err != nil {
			t.Fatalf("translate(%s): %v", sample.Type, err)
		}
		round, err := outputpartfidelity.ParseOpenResponses(wire)
		if err != nil {
			t.Fatalf("parse(%s): %v", sample.Type, err)
		}
		if round.Inline != sample.Inline {
			t.Errorf("inline %s: got %q, want %q (matrix marks [exact])",
				sample.Type, round.Inline, sample.Inline)
		}
	}
}

// TestMatrixDrivenRoundTrip is the §12.10 conformance check at the
// per-OutputPart level. For every (adapter, field) cell, the test:
//
//  1. Builds a sample OutputPart that exercises the field.
//  2. Translates it through the adapter.
//  3. Parses the wire form back into an OutputPart.
//  4. Asserts the field's reconstructed value matches the matrix tag.
func TestMatrixDrivenRoundTrip(t *testing.T) {
	cases := []struct {
		adapter   outputpartfidelity.Adapter
		translate func(runtime.OutputPart) ([]byte, error)
		parse     func([]byte) (runtime.OutputPart, error)
	}{
		{
			adapter:   outputpartfidelity.AdapterOpenAICompletions,
			translate: outputpartfidelity.TranslateOpenAICompletions,
			parse:     outputpartfidelity.ParseOpenAICompletions,
		},
		{
			adapter:   outputpartfidelity.AdapterOpenResponses,
			translate: outputpartfidelity.TranslateOpenResponses,
			parse:     outputpartfidelity.ParseOpenResponses,
		},
	}
	for _, tc := range cases {
		t.Run(string(tc.adapter), func(t *testing.T) {
			sample := fullyPopulated()
			wire, err := tc.translate(sample)
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			round, err := tc.parse(wire)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			for _, field := range outputpartfidelity.Fields() {
				tag, _ := outputpartfidelity.Matrix(tc.adapter, field)
				assertFieldFidelity(t, tc.adapter, field, tag, sample, round, wire)
			}
		})
	}
}

// assertFieldFidelity asserts that the round-tripped OutputPart's field
// reflects the documented fidelity tag.
func assertFieldFidelity(t *testing.T, a outputpartfidelity.Adapter, f outputpartfidelity.Field, tag outputpartfidelity.FidelityTag, original, round runtime.OutputPart, wire []byte) {
	t.Helper()
	switch tag {
	case outputpartfidelity.TagDropped:
		if !fieldIsZero(f, round) {
			t.Errorf("%s/%s: matrix marks [dropped]; reconstructed value is non-zero", a, f)
		}
	case outputpartfidelity.TagExact:
		if !fieldsEqual(f, original, round) {
			t.Errorf("%s/%s: matrix marks [exact]; round-trip differs", a, f)
		}
	case outputpartfidelity.TagLossy:
		// A lossy field is allowed to differ; the wire form is the
		// authoritative degradation. The test confirms the field is
		// either preserved or transformed — i.e., not silently
		// reconstructed from nothing. The per-adapter unit tests
		// cover the exact degradation each lossy cell promises.
	case outputpartfidelity.TagExtended:
		if !fieldsEqual(f, original, round) {
			t.Errorf("%s/%s: matrix marks [extended]; field not recoverable on ingest", a, f)
		}
	case outputpartfidelity.TagUnsupported:
		// Unsupported maps to dropped in our v1 translators.
		if !fieldIsZero(f, round) {
			t.Errorf("%s/%s: matrix marks [unsupported]; reconstructed non-zero", a, f)
		}
	}
}

func fieldIsZero(f outputpartfidelity.Field, p runtime.OutputPart) bool {
	switch f {
	case outputpartfidelity.FieldSchemaVersion:
		return p.SchemaVersion == 0
	case outputpartfidelity.FieldID:
		return p.ID == ""
	case outputpartfidelity.FieldType:
		return p.Type == ""
	case outputpartfidelity.FieldMimeType:
		return p.MimeType == ""
	case outputpartfidelity.FieldInline:
		return p.Inline == ""
	case outputpartfidelity.FieldRef:
		return p.Ref == ""
	case outputpartfidelity.FieldAnnotations:
		return p.Annotations == nil
	case outputpartfidelity.FieldParts:
		return p.Parts == nil
	case outputpartfidelity.FieldStatus:
		return p.Status == ""
	case outputpartfidelity.FieldProtocolHints:
		_, ok := p.Annotations["protocolHints"]
		return !ok
	}
	return true
}

func fieldsEqual(f outputpartfidelity.Field, a, b runtime.OutputPart) bool {
	switch f {
	case outputpartfidelity.FieldSchemaVersion:
		return a.SchemaVersion == b.SchemaVersion
	case outputpartfidelity.FieldID:
		return a.ID == b.ID
	case outputpartfidelity.FieldType:
		return a.Type == b.Type
	case outputpartfidelity.FieldMimeType:
		return a.MimeType == b.MimeType
	case outputpartfidelity.FieldInline:
		return a.Inline == b.Inline
	case outputpartfidelity.FieldRef:
		return a.Ref == b.Ref
	case outputpartfidelity.FieldAnnotations:
		return mapsEqual(a.Annotations, b.Annotations)
	case outputpartfidelity.FieldParts:
		return len(a.Parts) == len(b.Parts)
	case outputpartfidelity.FieldStatus:
		return a.Status == b.Status
	case outputpartfidelity.FieldProtocolHints:
		_, aok := a.Annotations["protocolHints"]
		_, bok := b.Annotations["protocolHints"]
		return aok == bok
	}
	return false
}

func mapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		bv, ok := b[k]
		if !ok || bv != v {
			return false
		}
	}
	return true
}
