// SPDX-License-Identifier: MIT

package conventions_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/observability/tracing"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// spec: §16.3 line 372 — the §25.2 ops error taxonomy (TRANSIENT, PERMANENT,
// POLICY, AUTH) maps onto the §16.3 span taxonomy (TRANSIENT, PERMANENT,
// POLICY, UPSTREAM). AUTH folds into UPSTREAM ("external dependency failure
// (MCP tool error, auth failure)"); the other three share names. A span's
// error.category must therefore never carry AUTH nor omit UPSTREAM. F-16.3.7.
func TestSpanCategoryMapsOpsTaxonomyOntoSpec163(t *testing.T) {
	cases := []struct {
		in   conventions.ErrorCategory
		want tracing.ErrorCategory
	}{
		{conventions.CategoryTransient, tracing.CategoryTransient},
		{conventions.CategoryPermanent, tracing.CategoryPermanent},
		{conventions.CategoryPolicy, tracing.CategoryPolicy},
		{conventions.CategoryAuth, tracing.CategoryUpstream},
		{conventions.ErrorCategory("UNKNOWN"), tracing.CategoryUpstream},
	}
	for _, c := range cases {
		if got := c.in.SpanCategory(); got != c.want {
			t.Errorf("%q.SpanCategory() = %q, want %q", c.in, got, c.want)
		}
	}

	// Every mapped value is one of the four §16.3 categories.
	valid := map[tracing.ErrorCategory]bool{
		tracing.CategoryTransient: true,
		tracing.CategoryPermanent: true,
		tracing.CategoryPolicy:    true,
		tracing.CategoryUpstream:  true,
	}
	for _, in := range []conventions.ErrorCategory{
		conventions.CategoryTransient, conventions.CategoryPermanent,
		conventions.CategoryPolicy, conventions.CategoryAuth,
	} {
		if !valid[in.SpanCategory()] {
			t.Errorf("%q.SpanCategory() = %q is outside the §16.3 taxonomy", in, in.SpanCategory())
		}
	}
}

func TestParsePageParamsDefaults(t *testing.T) {
	p, err := conventions.ParsePageParams(url.Values{}, "desc")
	if err != nil {
		t.Fatalf("ParsePageParams: %v", err)
	}
	if p.Limit != conventions.DefaultPageLimit {
		t.Errorf("limit = %d, want %d", p.Limit, conventions.DefaultPageLimit)
	}
	if p.SortOrder != "desc" {
		t.Errorf("sortOrder = %q, want desc (the supplied default)", p.SortOrder)
	}
	if p.Cursor != "" || !p.Since.IsZero() || !p.Until.IsZero() {
		t.Errorf("unset params should be zero, got %+v", p)
	}
}

func TestParsePageParamsLimit(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"50", 50},
		{"5000", conventions.MaxPageLimit}, // capped
		{"0", conventions.DefaultPageLimit},
		{"-3", conventions.DefaultPageLimit},
		{"1000", 1000},
	}
	for _, c := range cases {
		p, err := conventions.ParsePageParams(url.Values{"limit": {c.raw}}, "asc")
		if err != nil {
			t.Fatalf("ParsePageParams(limit=%s): %v", c.raw, err)
		}
		if p.Limit != c.want {
			t.Errorf("limit=%s → %d, want %d", c.raw, p.Limit, c.want)
		}
	}
}

func TestParsePageParamsRejectsMalformed(t *testing.T) {
	cases := map[string]url.Values{
		"non-integer limit":   {"limit": {"lots"}},
		"bad since timestamp": {"since": {"yesterday"}},
		"bad until timestamp": {"until": {"soon"}},
		"invalid sortOrder":   {"sortOrder": {"sideways"}},
	}
	for name, q := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := conventions.ParsePageParams(q, "asc"); err == nil {
				t.Errorf("ParsePageParams(%v) = nil error, want a parse error", q)
			}
		})
	}
}

func TestParsePageParamsTimeWindow(t *testing.T) {
	q := url.Values{
		"since": {"2026-04-16T10:00:00Z"},
		"until": {"2026-04-16T11:00:00Z"},
	}
	p, err := conventions.ParsePageParams(q, "asc")
	if err != nil {
		t.Fatalf("ParsePageParams: %v", err)
	}
	if !p.Since.Equal(time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("since = %v, want 2026-04-16T10:00:00Z", p.Since)
	}
	if !p.Until.Equal(time.Date(2026, 4, 16, 11, 0, 0, 0, time.UTC)) {
		t.Errorf("until = %v, want 2026-04-16T11:00:00Z", p.Until)
	}
}

func TestWantsConfirm(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"confirmed", `{"confirm": true}`, true},
		{"explicitly false", `{"confirm": false}`, false},
		{"absent — dry run", `{"reason": "scale down"}`, false},
		{"empty body", ``, false},
		{"malformed body", `{not json`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := conventions.WantsConfirm([]byte(c.body)); got != c.want {
				t.Errorf("WantsConfirm(%q) = %v, want %v", c.body, got, c.want)
			}
		})
	}
}

func TestNewErrorRetryability(t *testing.T) {
	cases := []struct {
		category      conventions.ErrorCategory
		wantRetryable bool
	}{
		{conventions.CategoryTransient, true},
		{conventions.CategoryPermanent, false},
		{conventions.CategoryPolicy, true},
		{conventions.CategoryAuth, true},
	}
	for _, c := range cases {
		t.Run(string(c.category), func(t *testing.T) {
			resp := conventions.NewError("SOME_CODE", c.category, "a message")
			if resp.Error.Retryable != c.wantRetryable {
				t.Errorf("retryable = %v, want %v", resp.Error.Retryable, c.wantRetryable)
			}
			if resp.Error.DocumentationURL != "https://docs.lenny.dev/errors/SOME_CODE" {
				t.Errorf("documentationUrl = %q, want the docs URL for the code", resp.Error.DocumentationURL)
			}
		})
	}
}

func TestProgressNullSemantics(t *testing.T) {
	// §25.2: a progress field with no meaningful value serializes as
	// JSON null, distinct from zero.
	raw, err := json.Marshal(conventions.Progress{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{"percent", "completedSteps", "totalSteps", "etaSeconds", "stalledForSeconds"} {
		v, present := generic[field]
		if !present {
			t.Errorf("%s is absent, want present as null", field)
		}
		if v != nil {
			t.Errorf("%s = %v, want null when unset", field, v)
		}
	}
}

func TestProgressCarriesValues(t *testing.T) {
	pct, steps := 47.0, 5
	raw, err := json.Marshal(conventions.Progress{
		Percent:        &pct,
		CompletedSteps: &steps,
		CurrentStep:    "migrating_shard_3",
		EtaMethod:      conventions.EtaHistoricalP50,
		RateMetric:     &conventions.RateMetric{Name: "shards_per_minute", Value: 0.5},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got conventions.Progress
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Percent == nil || *got.Percent != 47.0 {
		t.Errorf("percent round-trip = %v, want 47", got.Percent)
	}
	if got.EtaMethod != conventions.EtaHistoricalP50 {
		t.Errorf("etaMethod = %q, want historical_p50", got.EtaMethod)
	}
	if got.RateMetric == nil || got.RateMetric.Name != "shards_per_minute" {
		t.Errorf("rateMetric round-trip = %v", got.RateMetric)
	}
}

func TestUsesRankedActions(t *testing.T) {
	ranked := []string{
		"WARM_POOL_EXHAUSTED", "WARM_POOL_LOW",
		"CREDENTIAL_POOL_EXHAUSTED", "CIRCUIT_BREAKER_OPEN",
	}
	for _, issue := range ranked {
		if !conventions.UsesRankedActions(issue) {
			t.Errorf("UsesRankedActions(%q) = false, want true", issue)
		}
	}
	for _, issue := range []string{"SESSION_STORE_UNAVAILABLE", "MINIO_UNREACHABLE", ""} {
		if conventions.UsesRankedActions(issue) {
			t.Errorf("UsesRankedActions(%q) = true, want false (singular action)", issue)
		}
	}
}

func TestSortByConfidence(t *testing.T) {
	actions := []conventions.SuggestedAction{
		{Action: "INVESTIGATE_UPSTREAM", Confidence: 0.55},
		{Action: "SCALE_WARM_POOL", Confidence: 0.85},
		{Action: "WAIT", Confidence: 0.55},
	}
	conventions.SortByConfidence(actions)

	if actions[0].Action != "SCALE_WARM_POOL" {
		t.Errorf("highest-confidence action = %q, want SCALE_WARM_POOL", actions[0].Action)
	}
	// Stable: the two 0.55 entries keep their original relative order.
	if actions[1].Action != "INVESTIGATE_UPSTREAM" || actions[2].Action != "WAIT" {
		t.Errorf("equal-confidence order = %q,%q, want INVESTIGATE_UPSTREAM,WAIT",
			actions[1].Action, actions[2].Action)
	}
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	conventions.WriteError(rec, http.StatusConflict, "REMEDIATION_LOCK_CONFLICT",
		conventions.CategoryPolicy, "another agent holds the lock")

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var resp conventions.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.Error.Code != "REMEDIATION_LOCK_CONFLICT" || resp.Error.Category != conventions.CategoryPolicy {
		t.Errorf("error body = %+v, want the policy conflict", resp.Error)
	}
	if !resp.Error.Retryable {
		t.Error("a POLICY error should be retryable")
	}
}
