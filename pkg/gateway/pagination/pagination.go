// SPDX-License-Identifier: MIT

// Package pagination implements the §15.1 canonical cursor-paginated
// list envelope for the REST API.
//
// The envelope is `{items, cursor, hasMore, total?}` (spec §15.1 lines
// 1228-1253). Cursors are opaque URL-safe strings encoding a sort key,
// a unique tiebreaker, the sort direction, and an issued-at timestamp
// so the gateway can reject ancient cursors with `cursor_expired`. The
// limit is clamped to [1, 200] (spec line 1236). Sort fields are
// validated against an allow-list per resource; unknown fields fall
// back to the resource default with no rejection so a client supplying
// `?sort=` for a single-sort-field collection still gets results.
//
// spec: §15.1 lines 1228-1253 (cursor envelope, limit clamp, sort
// validation, 24-hour cursor TTL).
package pagination

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Spec-derived limits — §15.1 lines 1236, 1253.
const (
	LimitMin     = 1
	LimitMax     = 200
	LimitDefault = 50
	CursorTTL    = 24 * time.Hour
)

// DirectionAsc / DirectionDesc — §15.1 line 1236 `field:asc|desc`.
const (
	DirectionAsc  = "asc"
	DirectionDesc = "desc"
)

// Cursor is the opaque payload encoded into the URL-safe cursor string.
// Key carries the sort-field value of the last item on the previous
// page; Tiebreak is the unique identifier (id) of that last item — the
// two together guarantee stable iteration across inserts (§15.1 line
// 1253). Field + Direction pin the sort the cursor was minted against;
// a request that paginates with a cursor under a different sort gets
// `VALIDATION_ERROR cursor_sort_mismatch`. IssuedAt anchors the
// 24-hour TTL.
type Cursor struct {
	Key       string `json:"k"`
	Tiebreak  string `json:"t"`
	Field     string `json:"f"`
	Direction string `json:"d"`
	IssuedAt  int64  `json:"iat"`
}

// Encode serialises the cursor to a URL-safe opaque string. Callers
// receive an empty string when the cursor is empty (no next page).
func Encode(c Cursor) string {
	if (c == Cursor{}) {
		return ""
	}
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// Decode parses a cursor string. Returns ErrCursorMalformed when the
// payload is not a valid base64url-JSON Cursor and ErrCursorExpired
// when IssuedAt + CursorTTL is in the past relative to now.
func Decode(s string, now time.Time) (Cursor, error) {
	if s == "" {
		return Cursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, ErrCursorMalformed
	}
	var c Cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return Cursor{}, ErrCursorMalformed
	}
	if c.IssuedAt > 0 && now.Sub(time.Unix(c.IssuedAt, 0)) > CursorTTL {
		return c, ErrCursorExpired
	}
	return c, nil
}

// ErrCursorMalformed signals a non-decodable cursor.
var ErrCursorMalformed = errors.New("pagination: malformed cursor")

// ErrCursorExpired signals the cursor outlived the 24-hour TTL.
var ErrCursorExpired = errors.New("pagination: cursor expired")

// Sort is the parsed `?sort=field:direction` pair. Field is normalised
// to lower-case; Direction is the literal `asc` or `desc`.
type Sort struct {
	Field     string
	Direction string
}

// ParseSort accepts the §15.1 `field:asc|direction` form. Empty input
// returns the supplied default. Unknown fields (not in allowed) return
// ErrInvalidSort. A bare `field` defaults to `asc`. The direction is
// rejected with ErrInvalidSort if neither `asc` nor `desc`.
func ParseSort(raw string, allowed []string, def Sort) (Sort, error) {
	if raw == "" {
		return def, nil
	}
	parts := strings.SplitN(raw, ":", 2)
	field := strings.ToLower(strings.TrimSpace(parts[0]))
	direction := DirectionAsc
	if len(parts) == 2 {
		direction = strings.ToLower(strings.TrimSpace(parts[1]))
	}
	if direction != DirectionAsc && direction != DirectionDesc {
		return Sort{}, ErrInvalidSort
	}
	allowedSet := false
	for _, f := range allowed {
		if strings.EqualFold(field, f) {
			allowedSet = true
			field = f // canonicalise to allowed casing
			break
		}
	}
	if !allowedSet {
		return Sort{}, ErrInvalidSort
	}
	return Sort{Field: field, Direction: direction}, nil
}

// ErrInvalidSort signals an unknown sort field or malformed direction.
var ErrInvalidSort = errors.New("pagination: invalid sort")

// ParseLimit clamps the `?limit=` query parameter into [LimitMin,
// LimitMax]. Empty input returns LimitDefault. Negative or non-integer
// input returns ErrInvalidLimit so a malformed request fails fast
// rather than silently coercing to default.
func ParseLimit(raw string) (int, error) {
	if raw == "" {
		return LimitDefault, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, ErrInvalidLimit
	}
	if n < LimitMin {
		return LimitMin, nil
	}
	if n > LimitMax {
		return LimitMax, nil
	}
	return n, nil
}

// ErrInvalidLimit signals a non-integer `?limit=` value.
var ErrInvalidLimit = errors.New("pagination: invalid limit")

// Params bundles the parsed cursor, limit, and sort for a list handler.
type Params struct {
	Cursor Cursor
	Limit  int
	Sort   Sort
}

// ParseRequest reads `?cursor=`, `?limit=`, `?sort=` off the request,
// validating each against the supplied allow-lists. The returned
// *FieldError carries the §15.1 `details.fields[0].{field, rule}`
// payload so the handler can write a 400 envelope.
func ParseRequest(r *http.Request, allowedSorts []string, defaultSort Sort, now time.Time) (Params, *FieldError) {
	q := r.URL.Query()
	limit, err := ParseLimit(q.Get("limit"))
	if err != nil {
		return Params{}, &FieldError{Field: "limit", Rule: "invalid_limit", Message: err.Error()}
	}
	sort, err := ParseSort(q.Get("sort"), allowedSorts, defaultSort)
	if err != nil {
		return Params{}, &FieldError{Field: "sort", Rule: "invalid_sort_field", Message: err.Error()}
	}
	cursor, err := Decode(q.Get("cursor"), now)
	if err != nil {
		rule := "cursor_invalid"
		if errors.Is(err, ErrCursorExpired) {
			rule = "cursor_expired"
		}
		return Params{}, &FieldError{Field: "cursor", Rule: rule, Message: err.Error()}
	}
	if cursor.Field != "" && (cursor.Field != sort.Field || cursor.Direction != sort.Direction) {
		// A cursor minted under a different sort cannot be honoured —
		// the encoded key/tiebreaker are sort-specific. Reject so the
		// client requests a fresh page instead of getting a silently
		// wrong slice.
		return Params{}, &FieldError{
			Field: "cursor", Rule: "cursor_sort_mismatch",
			Message: "cursor was minted under a different sort",
		}
	}
	return Params{Cursor: cursor, Limit: limit, Sort: sort}, nil
}

// FieldError carries the §15.1 `details.fields[0].{field, rule}`
// pagination validation payload.
type FieldError struct {
	Field   string
	Rule    string
	Message string
}

// Details renders the error as the canonical §15.1
// `details.fields[0]` envelope.
func (e *FieldError) Details() map[string]any {
	return map[string]any{
		"fields": []map[string]any{
			{"field": e.Field, "rule": e.Rule},
		},
	}
}

// Envelope is the §15.1 canonical list envelope. Items is a typed
// slice the caller supplies; Cursor is the opaque next-page cursor
// (empty when there are no more pages); HasMore tracks whether
// additional pages exist beyond this one; Total is populated only when
// cheaply computable per §15.1 line 1252.
type Envelope[T any] struct {
	Items   []T    `json:"items"`
	Cursor  string `json:"cursor,omitempty"`
	HasMore bool   `json:"hasMore"`
	Total   *int64 `json:"total,omitempty"`
}

// MintCursor builds a cursor with the supplied key + tiebreaker + sort
// stamped with the supplied issuedAt. Callers pass the sort key value
// of the last item on the page and the unique tiebreaker (id).
func MintCursor(s Sort, key, tiebreak string, issuedAt time.Time) string {
	return Encode(Cursor{
		Key:       key,
		Tiebreak:  tiebreak,
		Field:     s.Field,
		Direction: s.Direction,
		IssuedAt:  issuedAt.Unix(),
	})
}

// KeyFunc yields the sort key value and the unique tiebreaker (id) for
// one item. Both are returned as strings so the cursor encoding and the
// slice ordering share a single comparison. The key is the value of the
// active sort field; for a `created_at` sort it is the RFC3339Nano
// timestamp, for a `name` sort it is the name. Callers that support
// multiple sort fields close over the parsed Sort and return the key
// for the active field. spec: §15.1 line 1253 (sort key + tiebreaker).
type KeyFunc[T any] func(T) (key, tiebreak string)

// SortSlice orders items in place to match direction using keyOf. The
// sort is stable and total: ties on the sort key fall through to the
// tiebreaker so iteration is deterministic across inserts (§15.1 line
// 1253). The same keyOf is then passed to Page so the encoded cursor
// matches the ordering.
func SortSlice[T any](items []T, direction string, keyOf KeyFunc[T]) {
	sort.SliceStable(items, func(i, j int) bool {
		ki, ti := keyOf(items[i])
		kj, tj := keyOf(items[j])
		c := strings.Compare(ki, kj)
		if c == 0 {
			c = strings.Compare(ti, tj)
		}
		if direction == DirectionDesc {
			return c > 0
		}
		return c < 0
	})
}

// Page slices an already-sorted in-memory collection into the canonical
// §15.1 envelope. items MUST already be ordered to match params.Sort
// (call SortSlice first). The page start is located by comparison
// against params.Cursor rather than by index, so a cursor survives
// inserts and deletes between requests: every item at or before the
// cursor position in sort order is skipped. Up to params.Limit items
// are returned; HasMore and the next Cursor are set when more remain.
// Total is stamped because an in-memory slice has a cheaply-computable
// count (§15.1 line 1252).
//
// spec: §15.1 lines 1228-1253.
func Page[T any](items []T, params Params, now time.Time, keyOf KeyFunc[T]) Envelope[T] {
	start := 0
	if c := params.Cursor; c.Key != "" || c.Tiebreak != "" {
		for start < len(items) {
			k, t := keyOf(items[start])
			if afterCursor(k, t, c, params.Sort.Direction) {
				break
			}
			start++
		}
	}
	end := start + params.Limit
	hasMore := end < len(items)
	if end > len(items) {
		end = len(items)
	}
	page := items[start:end]
	env := Envelope[T]{Items: page, HasMore: hasMore}
	if env.Items == nil {
		env.Items = []T{}
	}
	if hasMore && len(page) > 0 {
		k, t := keyOf(page[len(page)-1])
		env.Cursor = MintCursor(params.Sort, k, t, now)
	}
	total := int64(len(items))
	env.Total = &total
	return env
}

// afterCursor reports whether the (key, tiebreak) tuple sorts strictly
// after the cursor position under the given direction. The comparison
// is the same key-then-tiebreaker tuple ordering SortSlice applies, so
// the first item that is "after" the cursor is the first item of the
// next page.
func afterCursor(key, tiebreak string, c Cursor, direction string) bool {
	cmp := strings.Compare(key, c.Key)
	if cmp == 0 {
		cmp = strings.Compare(tiebreak, c.Tiebreak)
	}
	if direction == DirectionDesc {
		return cmp < 0
	}
	return cmp > 0
}
