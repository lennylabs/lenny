// SPDX-License-Identifier: MIT

package pagination_test

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/pagination"
)

// spec: §15.1 lines 1228-1253 — cursor envelope, limit clamp [1, 200],
// 24-hour cursor TTL, opaque cursors.

func TestParseLimitClampsToSpecRange_spec_15_1_1236(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"", pagination.LimitDefault, false},
		{"0", pagination.LimitMin, false},
		{"-1", pagination.LimitMin, false},
		{"1", 1, false},
		{"50", 50, false},
		{"200", 200, false},
		{"201", pagination.LimitMax, false},
		{"5000", pagination.LimitMax, false},
		{"oops", 0, true},
	}
	for _, tc := range cases {
		got, err := pagination.ParseLimit(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("limit=%q: want error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("limit=%q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("limit=%q: got %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseSortValidatesField_spec_15_1_1236(t *testing.T) {
	def := pagination.Sort{Field: "created_at", Direction: pagination.DirectionDesc}
	allowed := []string{"created_at", "updated_at", "name"}

	cases := []struct {
		raw     string
		want    pagination.Sort
		wantErr bool
	}{
		{"", def, false},
		{"created_at:asc", pagination.Sort{Field: "created_at", Direction: "asc"}, false},
		{"updated_at:desc", pagination.Sort{Field: "updated_at", Direction: "desc"}, false},
		{"name", pagination.Sort{Field: "name", Direction: "asc"}, false},
		{"unknown_field:desc", pagination.Sort{}, true},
		{"created_at:weird", pagination.Sort{}, true},
	}
	for _, tc := range cases {
		got, err := pagination.ParseSort(tc.raw, allowed, def)
		if tc.wantErr {
			if err == nil {
				t.Errorf("sort=%q: want error, got %+v", tc.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("sort=%q: %v", tc.raw, err)
		}
		if got != tc.want {
			t.Errorf("sort=%q: got %+v, want %+v", tc.raw, got, tc.want)
		}
	}
}

func TestCursorRoundTripsThroughBase64URL_spec_15_1_1253(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	c := pagination.Cursor{
		Key: "42", Tiebreak: "sess_1234", Field: "seq", Direction: "asc",
		IssuedAt: now.Unix(),
	}
	enc := pagination.Encode(c)
	if enc == "" {
		t.Fatalf("encode returned empty string")
	}
	got, err := pagination.Decode(enc, now)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != c {
		t.Errorf("round-trip mismatch: got %+v want %+v", got, c)
	}
}

func TestDecodeRejectsExpiredCursor_spec_15_1_1253(t *testing.T) {
	issued := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	enc := pagination.Encode(pagination.Cursor{
		Key: "x", Tiebreak: "y", Field: "seq", Direction: "asc",
		IssuedAt: issued.Unix(),
	})
	now := issued.Add(24*time.Hour + time.Second)
	if _, err := pagination.Decode(enc, now); !errors.Is(err, pagination.ErrCursorExpired) {
		t.Errorf("expected ErrCursorExpired, got %v", err)
	}
	// Within TTL the same cursor is accepted.
	if _, err := pagination.Decode(enc, issued.Add(23*time.Hour)); err != nil {
		t.Errorf("within-TTL decode failed: %v", err)
	}
}

func TestDecodeRejectsMalformedCursor(t *testing.T) {
	now := time.Now()
	if _, err := pagination.Decode("not-base64-!@#", now); !errors.Is(err, pagination.ErrCursorMalformed) {
		t.Errorf("malformed base64: got %v want ErrCursorMalformed", err)
	}
	// Valid base64 but not JSON.
	if _, err := pagination.Decode("YWJj", now); !errors.Is(err, pagination.ErrCursorMalformed) {
		t.Errorf("non-JSON base64: got %v want ErrCursorMalformed", err)
	}
}

func TestParseRequestSurfacesFieldErrorPayload_spec_15_1_1253(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	allowed := []string{"created_at"}
	def := pagination.Sort{Field: "created_at", Direction: "desc"}

	r := httptest.NewRequest("GET", "/v1/sessions?limit=oops", nil)
	_, ferr := pagination.ParseRequest(r, allowed, def, now)
	if ferr == nil || ferr.Field != "limit" || ferr.Rule != "invalid_limit" {
		t.Errorf("limit error: got %+v, want field=limit rule=invalid_limit", ferr)
	}

	r = httptest.NewRequest("GET", "/v1/sessions?sort=unknown:asc", nil)
	_, ferr = pagination.ParseRequest(r, allowed, def, now)
	if ferr == nil || ferr.Field != "sort" || ferr.Rule != "invalid_sort_field" {
		t.Errorf("sort error: got %+v", ferr)
	}

	// Expired cursor surfaces the spec's `cursor_expired` rule verbatim.
	enc := pagination.Encode(pagination.Cursor{
		Key: "1", Tiebreak: "x", Field: "created_at", Direction: "desc",
		IssuedAt: now.Add(-25 * time.Hour).Unix(),
	})
	r = httptest.NewRequest("GET", "/v1/sessions?cursor="+enc, nil)
	_, ferr = pagination.ParseRequest(r, allowed, def, now)
	if ferr == nil || ferr.Field != "cursor" || ferr.Rule != "cursor_expired" {
		t.Errorf("cursor expired: got %+v", ferr)
	}
	if d := ferr.Details(); d == nil {
		t.Errorf("Details() returned nil")
	}
}

func TestParseRequestRejectsCursorMintedUnderDifferentSort(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	enc := pagination.MintCursor(pagination.Sort{Field: "created_at", Direction: "desc"},
		"v", "id", now)

	r := httptest.NewRequest("GET", "/v1/sessions?cursor="+enc+"&sort=name:asc", nil)
	_, ferr := pagination.ParseRequest(r,
		[]string{"created_at", "name"},
		pagination.Sort{Field: "created_at", Direction: "desc"}, now)
	if ferr == nil || ferr.Rule != "cursor_sort_mismatch" {
		t.Errorf("expected cursor_sort_mismatch, got %+v", ferr)
	}
}

func TestMintCursorPreservesSortPin(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	enc := pagination.MintCursor(pagination.Sort{Field: "seq", Direction: "asc"},
		"7", "abc", now)
	c, err := pagination.Decode(enc, now)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if c.Field != "seq" || c.Direction != "asc" || c.Key != "7" || c.Tiebreak != "abc" {
		t.Errorf("cursor: %+v", c)
	}
}
