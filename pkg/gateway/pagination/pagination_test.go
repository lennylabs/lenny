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

// item is a tiny record exercising the SortSlice + Page helpers.
type item struct {
	id  string
	cat string // created_at, RFC3339Nano-shaped for lexical ordering
}

func keyByCreatedAt(it item) (string, string) { return it.cat, it.id }

func ids[T any](items []T, idOf func(T) string) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, idOf(it))
	}
	return out
}

func TestSortSliceOrdersByKeyThenTiebreak_spec_15_1_1253(t *testing.T) {
	items := []item{
		{id: "b", cat: "2026-01-01"},
		{id: "a", cat: "2026-01-01"}, // tie on cat → tiebreak a < b
		{id: "c", cat: "2026-01-03"},
	}
	pagination.SortSlice(items, pagination.DirectionAsc, keyByCreatedAt)
	got := ids(items, func(it item) string { return it.id })
	want := []string{"a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("asc order: got %v want %v", got, want)
		}
	}
	pagination.SortSlice(items, pagination.DirectionDesc, keyByCreatedAt)
	got = ids(items, func(it item) string { return it.id })
	want = []string{"c", "b", "a"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("desc order: got %v want %v", got, want)
		}
	}
}

func TestPageWalksEveryItemExactlyOnce_spec_15_1_1228(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	sortAsc := pagination.Sort{Field: "created_at", Direction: pagination.DirectionAsc}
	items := make([]item, 0, 7)
	for i := 0; i < 7; i++ {
		items = append(items, item{id: string(rune('a' + i)), cat: "2026-01-0" + string(rune('1'+i))})
	}
	pagination.SortSlice(items, sortAsc.Direction, keyByCreatedAt)

	var seen []string
	cursor := pagination.Cursor{}
	pages := 0
	for {
		params := pagination.Params{Cursor: cursor, Limit: 3, Sort: sortAsc}
		env := pagination.Page(items, params, now, keyByCreatedAt)
		pages++
		if env.Total == nil || *env.Total != 7 {
			t.Fatalf("total: got %v want 7", env.Total)
		}
		seen = append(seen, ids(env.Items, func(it item) string { return it.id })...)
		if !env.HasMore {
			if env.Cursor != "" {
				t.Errorf("last page must carry no cursor, got %q", env.Cursor)
			}
			break
		}
		c, err := pagination.Decode(env.Cursor, now)
		if err != nil {
			t.Fatalf("decode page cursor: %v", err)
		}
		cursor = c
		if pages > 10 {
			t.Fatalf("pagination did not terminate")
		}
	}
	if len(seen) != 7 {
		t.Fatalf("walked %d items want 7: %v", len(seen), seen)
	}
	for i := 0; i < 7; i++ {
		if seen[i] != string(rune('a'+i)) {
			t.Fatalf("walk order wrong at %d: %v", i, seen)
		}
	}
}

func TestPageEmptyCollectionYieldsEmptyItemsNotNull_spec_15_1_1251(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	params := pagination.Params{Limit: 50, Sort: pagination.Sort{Field: "created_at", Direction: "desc"}}
	env := pagination.Page([]item{}, params, now, keyByCreatedAt)
	if env.Items == nil {
		t.Fatalf("Items must be a non-nil empty slice")
	}
	if env.HasMore {
		t.Errorf("empty page must not report hasMore")
	}
	if env.Cursor != "" {
		t.Errorf("empty page must not mint a cursor")
	}
	if env.Total == nil || *env.Total != 0 {
		t.Errorf("total: got %v want 0", env.Total)
	}
}

func TestPageCursorSurvivesDeletionOfCursorItem_spec_15_1_1253(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	sortAsc := pagination.Sort{Field: "created_at", Direction: pagination.DirectionAsc}
	// First page over a, b, c yields a, b; cursor pins b.
	items := []item{{id: "a", cat: "1"}, {id: "b", cat: "2"}, {id: "c", cat: "3"}}
	env := pagination.Page(items, pagination.Params{Limit: 2, Sort: sortAsc}, now, keyByCreatedAt)
	if env.Cursor == "" {
		t.Fatalf("expected a next cursor")
	}
	c, _ := pagination.Decode(env.Cursor, now)
	// b is deleted before the next request; comparison-based skip must
	// still resume at c rather than re-emitting b's neighbour.
	remaining := []item{{id: "a", cat: "1"}, {id: "c", cat: "3"}}
	next := pagination.Page(remaining, pagination.Params{Cursor: c, Limit: 2, Sort: sortAsc}, now, keyByCreatedAt)
	got := ids(next.Items, func(it item) string { return it.id })
	if len(got) != 1 || got[0] != "c" {
		t.Fatalf("resume after deleted cursor item: got %v want [c]", got)
	}
}
