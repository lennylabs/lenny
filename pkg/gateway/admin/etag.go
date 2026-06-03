// SPDX-License-Identifier: MIT

package admin

import (
	"net/http"
	"strconv"
	"strings"
)

// ETag-based optimistic concurrency for admin resources.
//
// Every admin resource in Postgres carries an integer version that
// starts at 1 and increments on every successful write; the ETag value
// is the quoted decimal version (`"3"`). These helpers are the reusable
// core of that contract: a single PUT handler enforces the precondition
// with enforceIfMatch and exposes the new version with formatETag, and
// every admin resource picks up the same behaviour by reusing them.
// spec: §15.1 lines 1207-1213 ("ETag-based optimistic concurrency").

// formatETag renders an admin resource version as the RFC 7232 strong
// entity tag the §15.1 contract uses: the quoted decimal version, so
// version 3 becomes `"3"`. spec: §15.1 lines 1207-1208.
func formatETag(version int64) string {
	return `"` + strconv.FormatInt(version, 10) + `"`
}

// parseIfMatch parses an RFC 7232 §2.3 If-Match value carrying a single
// strong validator that is a quoted decimal version. It returns the
// decoded version and ok=true on a well-formed value. A weak validator
// (`W/` prefix), an unquoted token, a non-decimal body, the `*` any-tag,
// or a comma-separated list is rejected (ok=false): the admin surface
// accepts only the single strong quoted-decimal form the gateway emits.
// spec: §15.1 line 1210.
func parseIfMatch(raw string) (int64, bool) {
	v := strings.TrimSpace(raw)
	if len(v) < 3 || strings.HasPrefix(v, "W/") {
		return 0, false
	}
	if v[0] != '"' || v[len(v)-1] != '"' {
		return 0, false
	}
	inner := v[1 : len(v)-1]
	n, err := strconv.ParseInt(inner, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// enforceIfMatch applies the §15.1 lines 1207-1211 optimistic-concurrency
// precondition for an admin PUT. current is the resource's stored
// version. It returns true when the request may proceed; otherwise it has
// already written the error response and the caller must return without
// mutating the resource:
//
//   - missing or empty If-Match → 428 ETAG_REQUIRED
//   - malformed If-Match        → 400 VALIDATION_ERROR (details.fields=["If-Match"])
//   - stale If-Match            → 412 ETAG_MISMATCH (details.currentEtag)
//
// spec: §15.1 lines 1207-1211; error codes §15.1 lines 984-985.
func enforceIfMatch(w http.ResponseWriter, req *http.Request, current int64) bool {
	raw := strings.TrimSpace(req.Header.Get("If-Match"))
	if raw == "" {
		// spec: §15.1 line 985 — ETAG_REQUIRED is 428 Precondition Required.
		writeError(w, http.StatusPreconditionRequired, "ETAG_REQUIRED",
			"If-Match header is required on this PUT",
			map[string]any{"fields": []string{"If-Match"}})
		return false
	}
	want, ok := parseIfMatch(raw)
	if !ok {
		// spec: §15.1 line 1210 — a malformed If-Match is 400 VALIDATION_ERROR
		// with details.fields naming the failing header.
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"If-Match header is not a strong quoted-decimal entity tag",
			map[string]any{"fields": []string{"If-Match"}})
		return false
	}
	if want != current {
		// spec: §15.1 line 984 — ETAG_MISMATCH is 412 and carries the
		// current ETag so clients refresh without a round-trip GET.
		writeError(w, http.StatusPreconditionFailed, "ETAG_MISMATCH",
			"If-Match etag does not match the current resource version",
			map[string]any{"currentEtag": formatETag(current)})
		return false
	}
	return true
}
