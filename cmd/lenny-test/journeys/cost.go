// SPDX-License-Identifier: MIT

package journeys

import (
	"encoding/json"
)

// Rates are the dollar prices per million tokens the cost meter uses to
// estimate the cost of a run while it proceeds. Claude Code reports the
// authoritative cost only in its final result message, so the meter
// estimates from the usage each model response reports and stops the
// run when the estimate reaches the cap.
type Rates struct {
	Input        float64 `json:"input"`
	Output       float64 `json:"output"`
	CacheRead    float64 `json:"cacheRead"`
	CacheWrite5m float64 `json:"cacheWrite5m"`
	CacheWrite1h float64 `json:"cacheWrite1h"`
}

// SonnetRates are Claude Sonnet 5.5's list prices: $2 per million input
// tokens and $10 per million output tokens (decision 5 of change 0157),
// $0.10 per million cache-read tokens, and cache writes at 1.25 times
// the input price for the five-minute cache and twice the input price
// for the one-hour cache. A probe run on 2026-11-17 reproduced Claude
// Code's reported total_cost_usd from these rates to the last digit.
var SonnetRates = Rates{Input: 2, Output: 10, CacheRead: 0.10, CacheWrite5m: 2.5, CacheWrite1h: 4}

// Usage is the token usage of one model response, in the field names of
// the Messages API.
type Usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheCreation            *struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

// Tokens totals the tokens of a run.
type Tokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
}

// Cost prices one response. Cache writes whose split between the
// five-minute and one-hour caches is unknown are priced at the one-hour
// rate, the higher of the two, so the estimate errs toward stopping
// early.
func (r Rates) Cost(u Usage) float64 {
	write5m, write1h := int64(0), u.CacheCreationInputTokens
	if c := u.CacheCreation; c != nil && c.Ephemeral5m+c.Ephemeral1h > 0 {
		write5m = c.Ephemeral5m
		write1h = u.CacheCreationInputTokens - write5m
		if write1h < 0 {
			write1h = 0
		}
	}
	return (float64(u.InputTokens)*r.Input +
		float64(u.OutputTokens)*r.Output +
		float64(u.CacheReadInputTokens)*r.CacheRead +
		float64(write5m)*r.CacheWrite5m +
		float64(write1h)*r.CacheWrite1h) / 1e6
}

// Meter reads Claude Code's stream-json output line by line and keeps a
// running cost estimate and the final reported result.
//
// A response's usage arrives in several places: a stream_event
// message_start carries the input side, each message_delta carries the
// running output count, and an assistant message repeats the usage of
// the response it belongs to once per content block. The meter keeps,
// per response, the largest value seen for each field, so repeated and
// partial reports never double count, and sums the responses.
type Meter struct {
	Rates Rates

	order   []string
	usage   map[string]Usage
	current string

	// Result is the final result message, nil until it arrives.
	Result *ResultMessage
}

// ResultMessage is the subset of Claude Code's final stream-json result
// message the harness records.
type ResultMessage struct {
	Subtype      string                `json:"subtype"`
	IsError      bool                  `json:"is_error"`
	TotalCostUSD *float64              `json:"total_cost_usd"`
	NumTurns     int                   `json:"num_turns"`
	DurationMS   int64                 `json:"duration_ms"`
	Result       string                `json:"result"`
	Errors       []string              `json:"errors"`
	ModelUsage   map[string]modelUsage `json:"modelUsage"`
	SessionID    string                `json:"session_id"`
	// PermissionDenials lists the tool uses the permission
	// configuration refused.
	PermissionDenials []PermissionDenial `json:"permission_denials"`
}

// PermissionDenial is one refused tool use.
type PermissionDenial struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
}

type modelUsage struct {
	InputTokens              int64   `json:"inputTokens"`
	OutputTokens             int64   `json:"outputTokens"`
	CacheReadInputTokens     int64   `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64   `json:"cacheCreationInputTokens"`
	CostUSD                  float64 `json:"costUSD"`
}

// ResultSubtypeBudget is the result subtype Claude Code reports when its
// own --max-budget-usd limit stopped the run.
const ResultSubtypeBudget = "error_max_budget_usd"

type streamLine struct {
	Type    string `json:"type"`
	Message *struct {
		ID    string `json:"id"`
		Usage *Usage `json:"usage"`
	} `json:"message"`
	Event *struct {
		Type    string `json:"type"`
		Usage   *Usage `json:"usage"`
		Message *struct {
			ID    string `json:"id"`
			Usage *Usage `json:"usage"`
		} `json:"message"`
	} `json:"event"`
}

// NewMeter returns a meter pricing at rates.
func NewMeter(rates Rates) *Meter {
	return &Meter{Rates: rates, usage: map[string]Usage{}}
}

// Observe reads one line of stream-json output and returns the line's
// type. A line that is not JSON returns "" and changes nothing, because
// Claude Code's output stream carries only JSON lines and the
// transcript keeps the raw text either way.
func (m *Meter) Observe(line []byte) string {
	var l streamLine
	if err := json.Unmarshal(line, &l); err != nil {
		return ""
	}
	switch l.Type {
	case "assistant":
		if l.Message != nil && l.Message.Usage != nil {
			m.merge(l.Message.ID, *l.Message.Usage)
		}
	case "stream_event":
		m.observeEvent(l)
	case "result":
		var r ResultMessage
		if err := json.Unmarshal(line, &r); err == nil {
			m.Result = &r
		}
	}
	return l.Type
}

func (m *Meter) observeEvent(l streamLine) {
	e := l.Event
	if e == nil {
		return
	}
	switch e.Type {
	case "message_start":
		if e.Message != nil && e.Message.Usage != nil {
			m.current = e.Message.ID
			m.merge(e.Message.ID, *e.Message.Usage)
		}
	case "message_delta":
		if e.Usage != nil && m.current != "" {
			m.merge(m.current, *e.Usage)
		}
	}
}

func (m *Meter) merge(id string, u Usage) {
	if id == "" {
		return
	}
	old, seen := m.usage[id]
	if !seen {
		m.order = append(m.order, id)
		m.usage[id] = u
		return
	}
	old.InputTokens = max(old.InputTokens, u.InputTokens)
	old.OutputTokens = max(old.OutputTokens, u.OutputTokens)
	old.CacheReadInputTokens = max(old.CacheReadInputTokens, u.CacheReadInputTokens)
	old.CacheCreationInputTokens = max(old.CacheCreationInputTokens, u.CacheCreationInputTokens)
	if old.CacheCreation == nil {
		old.CacheCreation = u.CacheCreation
	}
	m.usage[id] = old
}

// Estimate is the running cost estimate in dollars.
func (m *Meter) Estimate() float64 {
	total := 0.0
	for _, id := range m.order {
		total += m.Rates.Cost(m.usage[id])
	}
	return total
}

// Cost returns the run's cost and whether Claude Code reported it. The
// reported total_cost_usd wins when the result message arrived; a run
// the harness stopped has no result message, and its cost is the
// estimate.
func (m *Meter) Cost() (float64, bool) {
	if m.Result != nil && m.Result.TotalCostUSD != nil {
		return *m.Result.TotalCostUSD, true
	}
	return m.Estimate(), false
}

// Tokens totals the run's tokens, from the result message when it
// arrived and from the observed responses otherwise.
func (m *Meter) Tokens() Tokens {
	var t Tokens
	if m.Result != nil && len(m.Result.ModelUsage) > 0 {
		for _, u := range m.Result.ModelUsage {
			t.Input += u.InputTokens
			t.Output += u.OutputTokens
			t.CacheRead += u.CacheReadInputTokens
			t.CacheWrite += u.CacheCreationInputTokens
		}
		return t
	}
	for _, id := range m.order {
		u := m.usage[id]
		t.Input += u.InputTokens
		t.Output += u.OutputTokens
		t.CacheRead += u.CacheReadInputTokens
		t.CacheWrite += u.CacheCreationInputTokens
	}
	return t
}

// StreamEventType is the type of a partial-message line. The transcript
// leaves those lines out: they repeat, token by token, the text the
// assistant messages carry whole, and the meter has already read their
// usage.
const StreamEventType = "stream_event"
