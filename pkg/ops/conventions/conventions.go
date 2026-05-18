// SPDX-License-Identifier: MIT

// Package conventions implements the §25.4 conventions shared by every
// operability endpoint on both the gateway and lenny-ops: the canonical
// pagination parameters, the dry-run/confirm pattern for mutating
// endpoints, and the response-level degradation envelope.
package conventions

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Page-size bounds for the §25.4 canonical `limit` parameter.
const (
	DefaultPageLimit = 100
	MaxPageLimit     = 1000
)

// PageParams holds the §25.4 canonical pagination query parameters.
type PageParams struct {
	// Cursor is the opaque continuation token from a prior response.
	// Endpoints and agents MUST treat it as opaque.
	Cursor string
	// Limit is the page size, defaulted to 100 and capped at 1000.
	Limit int
	// Since and Until bound a time-windowed query; a zero value means
	// the bound was not supplied.
	Since time.Time
	Until time.Time
	// SortOrder is "asc" or "desc".
	SortOrder string
}

// ParsePageParams parses the §25.4 pagination query parameters. `limit`
// defaults to 100, is raised to the default when below 1, and is
// capped at 1000; `since` and `until` are RFC 3339 timestamps;
// `sortOrder` is "asc" or "desc" and defaults to defaultSortOrder.
// A malformed `limit`, `since`, `until`, or `sortOrder` is an error.
func ParsePageParams(q url.Values, defaultSortOrder string) (PageParams, error) {
	p := PageParams{
		Cursor:    q.Get("cursor"),
		Limit:     DefaultPageLimit,
		SortOrder: defaultSortOrder,
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return PageParams{}, fmt.Errorf("limit %q is not an integer", v)
		}
		switch {
		case n < 1:
			n = DefaultPageLimit
		case n > MaxPageLimit:
			n = MaxPageLimit
		}
		p.Limit = n
	}
	if v := q.Get("since"); v != "" {
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return PageParams{}, fmt.Errorf("since %q is not an RFC 3339 timestamp", v)
		}
		p.Since = ts
	}
	if v := q.Get("until"); v != "" {
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return PageParams{}, fmt.Errorf("until %q is not an RFC 3339 timestamp", v)
		}
		p.Until = ts
	}
	if v := q.Get("sortOrder"); v != "" {
		if v != "asc" && v != "desc" {
			return PageParams{}, fmt.Errorf("sortOrder %q is not asc or desc", v)
		}
		p.SortOrder = v
	}
	return p, nil
}

// WantsConfirm reports whether a §25.4 mutating-endpoint request body
// carries `"confirm": true`. A body without it — the default — selects
// the dry-run preview path, which mutates no state. A malformed body
// is treated as unconfirmed.
func WantsConfirm(body []byte) bool {
	var v struct {
		Confirm bool `json:"confirm"`
	}
	_ = json.Unmarshal(body, &v)
	return v.Confirm
}

// DegradationLevel is the §25.4 degradation-envelope level.
type DegradationLevel string

const (
	DegradationHealthy  DegradationLevel = "healthy"
	DegradationDegraded DegradationLevel = "degraded"
	DegradationFailed   DegradationLevel = "failed"
)

// Degradation is the §25.4 canonical degradation envelope: the
// response-level signal for an endpoint whose data quality depends on
// an external dependency. Endpoints serving from their primary source
// omit it; an absent envelope is equivalent to healthy.
type Degradation struct {
	Level             DegradationLevel `json:"level"`
	PrimarySource     string           `json:"primarySource,omitempty"`
	ActualSource      string           `json:"actualSource,omitempty"`
	FallbackPath      []string         `json:"fallbackPath,omitempty"`
	Confidence        float64          `json:"confidence,omitempty"`
	UnavailableFields []string         `json:"unavailableFields,omitempty"`
	Warnings          []string         `json:"warnings,omitempty"`
	Since             string           `json:"since,omitempty"`
}

// Pagination is the §25.4 response envelope for a paginated list.
type Pagination struct {
	Cursor      string `json:"cursor,omitempty"`
	HasMore     bool   `json:"hasMore"`
	Limit       int    `json:"limit"`
	GapDetected bool   `json:"gapDetected,omitempty"`
}

// ErrorCategory is the §25.2 error category. It determines the
// retry behavior an agent applies to a failed operability request.
type ErrorCategory string

const (
	// CategoryTransient is a temporary failure that is safe to retry.
	CategoryTransient ErrorCategory = "TRANSIENT"
	// CategoryPermanent will not succeed as-is; the caller must change
	// its input. It is the only non-retryable category.
	CategoryPermanent ErrorCategory = "PERMANENT"
	// CategoryPolicy is a platform-policy rejection; the caller retries
	// after taking the indicated action.
	CategoryPolicy ErrorCategory = "POLICY"
	// CategoryAuth is an authentication or authorization failure.
	CategoryAuth ErrorCategory = "AUTH"
)

// ErrorBody is the inner object of the §25.2 canonical error envelope.
type ErrorBody struct {
	Code                string         `json:"code"`
	Category            ErrorCategory  `json:"category"`
	Message             string         `json:"message"`
	Retryable           bool           `json:"retryable"`
	SuggestedRetryAfter string         `json:"suggestedRetryAfter,omitempty"`
	Details             map[string]any `json:"details,omitempty"`
	DocumentationURL    string         `json:"documentationUrl"`
}

// ErrorResponse is the §25.2 canonical error response envelope every
// operability endpoint returns on failure.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// NewError builds the §25.2 canonical error envelope. retryable is
// derived from the category — every category except PERMANENT is
// retryable — and the documentation URL is derived from the code.
func NewError(code string, category ErrorCategory, message string) ErrorResponse {
	return ErrorResponse{Error: ErrorBody{
		Code:             code,
		Category:         category,
		Message:          message,
		Retryable:        category != CategoryPermanent,
		DocumentationURL: "https://docs.lenny.dev/errors/" + code,
	}}
}

// WriteError writes the §25.2 canonical error envelope as a JSON
// response with the given HTTP status code.
func WriteError(w http.ResponseWriter, status int, code string, category ErrorCategory, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(NewError(code, category, message))
}
