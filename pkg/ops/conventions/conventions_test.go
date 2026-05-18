// SPDX-License-Identifier: MIT

package conventions_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

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
