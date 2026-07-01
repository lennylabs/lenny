// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

// decodeErr decodes the §15.1 / §25.2 admin error envelope.
func decodeErr(t *testing.T, body []byte) struct {
	Error struct {
		Code                string         `json:"code"`
		Category            string         `json:"category"`
		Message             string         `json:"message"`
		Retryable           bool           `json:"retryable"`
		SuggestedRetryAfter string         `json:"suggestedRetryAfter"`
		Details             map[string]any `json:"details"`
	} `json:"error"`
} {
	t.Helper()
	var resp struct {
		Error struct {
			Code                string         `json:"code"`
			Category            string         `json:"category"`
			Message             string         `json:"message"`
			Retryable           bool           `json:"retryable"`
			SuggestedRetryAfter string         `json:"suggestedRetryAfter"`
			Details             map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, body)
	}
	return resp
}

// spec: §15.1 line 944, §25.2 lines 302-329 — every admin error carries
// the canonical envelope with code, category, message, and retryable.
// The category/retryable pair is sourced from the shared §15.2.1
// classifier. F-25.2.6.
func TestWriteErrorCanonicalEnvelope_spec_25_2_302(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		code      string
		wantCat   string
		wantRetry bool
	}{
		// Known catalog codes resolve to their authoritative pair.
		{"validation", 400, "VALIDATION_ERROR", "PERMANENT", false},
		{"not-found", 404, "RESOURCE_NOT_FOUND", "PERMANENT", false},
		{"forbidden", 403, "FORBIDDEN", "PERMANENT", false},
		{"internal", 500, "INTERNAL_ERROR", "TRANSIENT", true},
		// A bespoke admin code unknown to the catalog derives from status.
		{"bespoke-4xx", 422, "JUSTIFICATION_REQUIRED", "PERMANENT", false},
		{"bespoke-5xx", 503, "ARTIFACT_REPLICATION_UNAVAILABLE", "TRANSIENT", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeError(rec, c.status, c.code, "boom", nil)
			if rec.Code != c.status {
				t.Errorf("status = %d, want %d", rec.Code, c.status)
			}
			env := decodeErr(t, rec.Body.Bytes())
			if env.Error.Code != c.code {
				t.Errorf("code = %q, want %q", env.Error.Code, c.code)
			}
			if env.Error.Category != c.wantCat {
				t.Errorf("category = %q, want %q", env.Error.Category, c.wantCat)
			}
			if env.Error.Retryable != c.wantRetry {
				t.Errorf("retryable = %v, want %v", env.Error.Retryable, c.wantRetry)
			}
			if env.Error.Message != "boom" {
				t.Errorf("message = %q, want boom", env.Error.Message)
			}
		})
	}
}

// spec: §25.2 line 329 — a retryable 429/5xx admin error advertises a
// backoff via suggestedRetryAfter and the matching Retry-After header;
// a permanent 4xx error advertises neither. F-25.2.6.
func TestWriteErrorRetryAfter_spec_25_2_329(t *testing.T) {
	// Retryable transient default.
	rec := httptest.NewRecorder()
	writeError(rec, 503, "ARTIFACT_REPLICATION_UNAVAILABLE", "down", nil)
	env := decodeErr(t, rec.Body.Bytes())
	if env.Error.SuggestedRetryAfter == "" {
		t.Errorf("suggestedRetryAfter empty on retryable 503, want a value")
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Errorf("Retry-After header empty on retryable 503")
	}

	// Permanent 4xx: no backoff advertised.
	rec = httptest.NewRecorder()
	writeError(rec, 404, "RESOURCE_NOT_FOUND", "missing", nil)
	env = decodeErr(t, rec.Body.Bytes())
	if env.Error.SuggestedRetryAfter != "" {
		t.Errorf("suggestedRetryAfter = %q on permanent 404, want empty", env.Error.SuggestedRetryAfter)
	}
	if got := rec.Header().Get("Retry-After"); got != "" {
		t.Errorf("Retry-After = %q on permanent 404, want empty", got)
	}

	// An explicit backoff overrides the default.
	rec = httptest.NewRecorder()
	writeErrorRetryAfter(rec, 503, "SERVICE_UNAVAILABLE", "down", nil, 30*time.Second)
	env = decodeErr(t, rec.Body.Bytes())
	if env.Error.SuggestedRetryAfter != "30s" {
		t.Errorf("suggestedRetryAfter = %q, want 30s", env.Error.SuggestedRetryAfter)
	}
	if got := rec.Header().Get("Retry-After"); got != "30" {
		t.Errorf("Retry-After = %q, want 30", got)
	}
}

// writeError omits the details field when no details are supplied and
// preserves it otherwise. F-25.2.6.
func TestWriteErrorDetailsRoundTrip(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, 400, "VALIDATION_ERROR", "bad", map[string]any{"field": "name"})
	env := decodeErr(t, rec.Body.Bytes())
	if env.Error.Details["field"] != "name" {
		t.Errorf("details.field = %v, want name", env.Error.Details["field"])
	}
}
