// SPDX-License-Identifier: MIT

package llmproxy_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
)

// spec: §4.9 — the LLM proxy SSE relay: stream the upstream
// Server-Sent Events response to the agent pod and extract the
// authoritative token usage.

// anthropicSSEStream is a representative Anthropic Messages streaming
// response: message_start carries the input count, message_delta the
// cumulative output count.
const anthropicSSEStream = `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":12,"output_tokens":1}}}

event: content_block_delta
data: {"type":"content_block_delta","delta":{"text":"hel"}}

event: content_block_delta
data: {"type":"content_block_delta","delta":{"text":"lo"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":34}}

event: message_stop
data: {"type":"message_stop"}

`

func TestRelayStreamCopiesVerbatimAndExtractsUsage(t *testing.T) {
	var dst bytes.Buffer
	flushes := 0
	usage, err := llmproxy.RelayStream(&dst, strings.NewReader(anthropicSSEStream), func() { flushes++ })
	if err != nil {
		t.Fatalf("RelayStream: %v", err)
	}
	if dst.String() != anthropicSSEStream {
		t.Errorf("relayed stream is not byte-exact:\n got %q\nwant %q", dst.String(), anthropicSSEStream)
	}
	if usage.InputTokens != 12 || usage.OutputTokens != 34 {
		t.Errorf("usage = %+v, want 12 in / 34 out", usage)
	}
	if flushes == 0 {
		t.Error("flush was never called — the relay buffered the stream")
	}
}

func TestRelayStreamEmptyStream(t *testing.T) {
	var dst bytes.Buffer
	usage, err := llmproxy.RelayStream(&dst, strings.NewReader(""), nil)
	if err != nil {
		t.Fatalf("RelayStream: %v", err)
	}
	if usage.InputTokens != 0 || usage.OutputTokens != 0 {
		t.Errorf("usage = %+v, want zero for an empty stream", usage)
	}
}

func TestRelayStreamToleratesNonUsageLines(t *testing.T) {
	// SSE comments and non-JSON data lines must relay without error and
	// without affecting the usage tally.
	const stream = ": ping\n\ndata: not-json\n\ndata: {\"type\":\"content_block_delta\"}\n\n"
	var dst bytes.Buffer
	usage, err := llmproxy.RelayStream(&dst, strings.NewReader(stream), nil)
	if err != nil {
		t.Fatalf("RelayStream: %v", err)
	}
	if dst.String() != stream {
		t.Errorf("relayed stream = %q, want %q", dst.String(), stream)
	}
	if usage.InputTokens != 0 || usage.OutputTokens != 0 {
		t.Errorf("usage = %+v, want zero", usage)
	}
}

// failingReader yields its payload once, then a read error.
type failingReader struct {
	payload []byte
	done    bool
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, errors.New("upstream connection reset")
	}
	r.done = true
	n := copy(p, r.payload)
	return n, nil
}

func TestRelayStreamReportsMidStreamReadFailure(t *testing.T) {
	var dst bytes.Buffer
	src := &failingReader{payload: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":9}}}\n\n")}
	usage, err := llmproxy.RelayStream(&dst, src, nil)
	if err == nil {
		t.Fatal("RelayStream returned nil error for a mid-stream read failure")
	}
	var te *llmproxy.TranslationError
	if !errors.As(err, &te) || te.Type != llmproxy.ErrStreamingInterrupted {
		t.Errorf("error = %v, want a streaming_interrupted TranslationError", err)
	}
	// The bytes read before the failure were still relayed, and their
	// usage was still extracted.
	if usage.InputTokens != 9 {
		t.Errorf("usage.InputTokens = %d, want 9 from the relayed prefix", usage.InputTokens)
	}
}

// failingWriter errors on the first Write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("client gone") }

func TestRelayStreamReportsClientWriteFailure(t *testing.T) {
	_, err := llmproxy.RelayStream(failingWriter{}, strings.NewReader(anthropicSSEStream), nil)
	var te *llmproxy.TranslationError
	if !errors.As(err, &te) || te.Type != llmproxy.ErrStreamingInterrupted {
		t.Errorf("error = %v, want a streaming_interrupted TranslationError", err)
	}
}
