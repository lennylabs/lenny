// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"strings"
	"sync/atomic"
	"testing"
)

// spec: §15.4.1 (Basic fallback: a `message` with no platform MCP
// client produces a `response` echoing the input parts)
func TestHandleMessageBasicFallback(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var seq atomic.Uint64
	w := newWriter(&stdout)
	line := []byte(`{"type":"message","id":"m1","input":[{"type":"text","inline":"hello"}]}`)
	if err := handleMessage(w, line, &seq, nil, &stderr); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	if !strings.Contains(stdout.String(), `"response"`) {
		t.Errorf("stdout missing response frame:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `[elicitation-echo seq=1] hello`) {
		t.Errorf("stdout missing echoed input:\n%s", stdout.String())
	}
}

// spec: §15.4.1 (malformed envelope is a protocolError that maps to
// exit code 2)
func TestHandleMessageRejectsMalformedEnvelope(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var seq atomic.Uint64
	w := newWriter(&stdout)
	err := handleMessage(w, []byte(`not-json`), &seq, nil, &stderr)
	if err == nil {
		t.Fatal("expected a protocol error")
	}
	if _, ok := err.(protocolError); !ok {
		t.Errorf("err type = %T, want protocolError", err)
	}
}
