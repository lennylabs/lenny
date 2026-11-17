// SPDX-License-Identifier: MIT

package journeys

import (
	"math"
	"strings"
	"testing"
)

// These tests exercise contributor tooling and carry no spec annotation.

// recordedStream is the usage-bearing part of a stream-json transcript
// Claude Code 2.1.326 wrote on 2026-11-17 for a two-response run with
// --include-partial-messages; its result message reported
// total_cost_usd 0.0122995.
const recordedStream = `{"type":"system","subtype":"init","model":"claude-sonnet-5-5"}
{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":2,"cache_creation_input_tokens":407,"cache_read_input_tokens":6094,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":407},"output_tokens":17}}}}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"x"}}}
{"type":"assistant","message":{"id":"msg_1","usage":{"input_tokens":2,"cache_creation_input_tokens":407,"cache_read_input_tokens":6094,"output_tokens":17},"content":[{"type":"tool_use","name":"Bash"}]}}
{"type":"assistant","message":{"id":"msg_1","usage":{"input_tokens":2,"cache_creation_input_tokens":407,"cache_read_input_tokens":6094,"output_tokens":17},"content":[{"type":"tool_use","name":"Read"}]}}
{"type":"stream_event","event":{"type":"message_delta","usage":{"input_tokens":2,"cache_creation_input_tokens":407,"cache_read_input_tokens":6094,"output_tokens":376}}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"ok"}]}}
{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_2","usage":{"input_tokens":2,"cache_creation_input_tokens":966,"cache_read_input_tokens":6501,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":966},"output_tokens":178}}}}
{"type":"stream_event","event":{"type":"message_delta","usage":{"input_tokens":2,"cache_creation_input_tokens":966,"cache_read_input_tokens":6501,"output_tokens":178}}}
`

const recordedResult = `{"type":"result","subtype":"error_max_budget_usd","is_error":true,"total_cost_usd":0.0122995,"num_turns":2,"errors":["Reached maximum budget ($0.01)"],"modelUsage":{"claude-sonnet-5-5":{"inputTokens":4,"outputTokens":554,"cacheReadInputTokens":12595,"cacheCreationInputTokens":1373,"costUSD":0.0122995}},"permission_denials":[{"tool_name":"Bash","tool_input":{"command":"ls /"}}]}`

func feed(m *Meter, stream string) []string {
	var types []string
	for _, line := range strings.Split(stream, "\n") {
		if line != "" {
			types = append(types, m.Observe([]byte(line)))
		}
	}
	return types
}

func TestMeterEstimateReproducesTheReportedCost(t *testing.T) {
	m := NewMeter(SonnetRates)
	feed(m, recordedStream)
	if got := m.Estimate(); math.Abs(got-0.0122995) > 1e-9 {
		t.Fatalf("estimate = %.7f, want the reported 0.0122995", got)
	}
	if cost, reported := m.Cost(); reported || math.Abs(cost-0.0122995) > 1e-9 {
		t.Fatalf("before the result the cost is the estimate: %.7f reported=%v", cost, reported)
	}
	tok := m.Tokens()
	if tok.Output != 376+178 || tok.CacheRead != 6094+6501 {
		t.Errorf("tokens from the responses = %+v", tok)
	}
}

func TestMeterPrefersTheReportedResult(t *testing.T) {
	m := NewMeter(SonnetRates)
	feed(m, recordedStream+recordedResult+"\n")
	cost, reported := m.Cost()
	if !reported || cost != 0.0122995 {
		t.Fatalf("cost = %v reported=%v, want the reported total", cost, reported)
	}
	if m.Result.Subtype != ResultSubtypeBudget || len(m.Result.PermissionDenials) != 1 {
		t.Errorf("result not read: %+v", m.Result)
	}
	if tok := m.Tokens(); tok.Output != 554 || tok.CacheWrite != 1373 {
		t.Errorf("tokens from the result's model usage = %+v", tok)
	}
}

func TestMeterCountsARepeatedResponseOnce(t *testing.T) {
	m := NewMeter(SonnetRates)
	line := `{"type":"assistant","message":{"id":"msg_x","usage":{"input_tokens":1000000,"output_tokens":0}}}`
	for i := 0; i < 5; i++ {
		m.Observe([]byte(line))
	}
	if got := m.Estimate(); math.Abs(got-2.0) > 1e-9 {
		t.Fatalf("five copies of one response cost %.4f, want 2.0", got)
	}
}

func TestMeterReturnsLineTypesAndIgnoresNonJSON(t *testing.T) {
	m := NewMeter(SonnetRates)
	types := feed(m, "not json\n"+`{"type":"stream_event","event":{"type":"message_delta","usage":{"output_tokens":5}}}`+"\n")
	if types[0] != "" || types[1] != StreamEventType {
		t.Fatalf("types = %q", types)
	}
	if m.Estimate() != 0 {
		t.Error("a delta before any message_start is not attributed")
	}
}

func TestRatesPriceUnknownCacheWritesAtTheOneHourRate(t *testing.T) {
	r := SonnetRates
	u := Usage{CacheCreationInputTokens: 1_000_000}
	if got := r.Cost(u); got != r.CacheWrite1h {
		t.Errorf("an unsplit cache write costs %.2f, want the one-hour rate %.2f", got, r.CacheWrite1h)
	}
	u.CacheCreation = &struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	}{Ephemeral5m: 1_000_000}
	if got := r.Cost(u); got != r.CacheWrite5m {
		t.Errorf("a five-minute cache write costs %.2f, want %.2f", got, r.CacheWrite5m)
	}
	if got := r.Cost(Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}); got != 12 {
		t.Errorf("a million input and output tokens cost %.2f, want $12", got)
	}
}
