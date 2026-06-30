// SPDX-License-Identifier: MIT

package pgstore

import (
	"reflect"
	"testing"
)

// spec: §8.2 line 52 / §8.3 line 286 — the JSONB tracing_context arg
// encoder. An empty / nil map stores SQL NULL so the read path returns
// nil; a populated map stores the JSONB encoding. F-8.2.14.
func TestTracingContextArg_spec_8_2_52(t *testing.T) {
	if got := tracingContextArg(nil); got != nil {
		t.Errorf("tracingContextArg(nil) = %v, want nil", got)
	}
	if got := tracingContextArg(map[string]string{}); got != nil {
		t.Errorf("tracingContextArg({}) = %v, want nil", got)
	}
	got := tracingContextArg(map[string]string{"traceparent": "00-a-b-01"})
	s, ok := got.(string)
	if !ok {
		t.Fatalf("tracingContextArg returned %T, want string", got)
	}
	if s == "" || s[0] != '{' {
		t.Errorf("tracingContextArg = %q, want JSONB object literal", s)
	}
}

// spec: §8.2 line 52 — decodeTracingContext is the inverse: a valid
// JSONB document round-trips back into the in-memory map; a malformed
// payload errors. F-8.2.14.
func TestDecodeTracingContext_spec_8_2_52(t *testing.T) {
	want := map[string]string{"traceparent": "00-a-b-01", "tracestate": "acme=ok"}
	raw := []byte(`{"traceparent":"00-a-b-01","tracestate":"acme=ok"}`)
	got, err := decodeTracingContext(raw)
	if err != nil {
		t.Fatalf("decodeTracingContext: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decodeTracingContext = %#v, want %#v", got, want)
	}
	if _, err := decodeTracingContext([]byte(`not json`)); err == nil {
		t.Errorf("decodeTracingContext(non-json) = nil err, want error")
	}
}
