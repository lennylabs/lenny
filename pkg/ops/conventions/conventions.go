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
	"sort"
	"strconv"
	"time"

	"github.com/lennylabs/lenny/pkg/observability/tracing"
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

// ThresholdSource is the §25.4 envelope field for endpoints whose
// recommendations or health-derivation logic depends on alert
// thresholds. It distinguishes the operator-configured rule set
// (Prometheus-backed) from the gateway's compiled-in default thresholds
// the in-process tracker uses during the §25.13 Prometheus fallback.
//
// spec: §25.4 line 215, §25.13 line 4848.
type ThresholdSource string

const (
	// ThresholdSourceOperatorCustomized signals the response was derived
	// from the operator-configured rules loaded into Prometheus.
	ThresholdSourceOperatorCustomized ThresholdSource = "operator-customized"
	// ThresholdSourceCompiledInDefaults signals the response was derived
	// from the gateway's compiled-in default thresholds — the in-process
	// alert tracker fallback used when Prometheus is unreachable.
	ThresholdSourceCompiledInDefaults ThresholdSource = "compiled-in-defaults"
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
	// ThresholdSource is set on health and capacity-recommendations
	// responses to signal whether the active rule set is the
	// operator-customized one or the gateway's compiled-in defaults.
	// Empty on responses unaffected by alert-threshold provenance.
	// spec: §25.4 line 215, §25.13 line 4848.
	ThresholdSource ThresholdSource `json:"thresholdSource,omitempty"`
	Warnings        []string        `json:"warnings,omitempty"`
	Since           string          `json:"since,omitempty"`
}

// Pagination is the §25.4 response envelope for a paginated list.
type Pagination struct {
	Cursor  string `json:"cursor,omitempty"`
	HasMore bool   `json:"hasMore"`
	Limit   int    `json:"limit"`
	// CursorKind names the continuation-token family this endpoint's page
	// was produced by (§25.4 Pagination: "redis-stream-id", "buffer-seq",
	// "timestamp", "pk", "none", etc.). Agents MUST treat the cursor as
	// opaque; the kind lets an agent detect a source transition. Omitted
	// when the endpoint does not classify its cursor.
	CursorKind  string `json:"cursorKind,omitempty"`
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

// SpanCategory maps a §25.2 ops error category onto the §16.3 span error
// taxonomy (TRANSIENT, PERMANENT, POLICY, UPSTREAM). The two taxonomies
// share three names; §25.2 splits out AUTH, which §16.3 folds into UPSTREAM
// ("external dependency failure (MCP tool error, auth failure)"). Recording
// the §16.3 value on the span's error.category attribute keeps spans within
// the four categories §16.3 enumerates, so a span never carries a value the
// trace taxonomy does not list (AUTH) nor omits one it requires (UPSTREAM).
// spec: §16.3 line 372 (UPSTREAM includes auth failure); §25.2 ErrorCategory.
func (c ErrorCategory) SpanCategory() tracing.ErrorCategory {
	switch c {
	case CategoryTransient:
		return tracing.CategoryTransient
	case CategoryPermanent:
		return tracing.CategoryPermanent
	case CategoryPolicy:
		return tracing.CategoryPolicy
	case CategoryAuth:
		return tracing.CategoryUpstream
	default:
		// An unrecognized category is treated as an upstream/dependency
		// failure rather than silently emitting an off-taxonomy value.
		return tracing.CategoryUpstream
	}
}

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

// WriteErrorWithDetails writes the §25.2 canonical error envelope with a
// structured details object (for example CONFIG_VALIDATION_FAILED's
// details.errors list or UPGRADE_ROLLBACK_UNAVAILABLE's
// details.requiresRestore flag). A nil details map omits the field.
func WriteErrorWithDetails(w http.ResponseWriter, status int, code string, category ErrorCategory, message string, details map[string]any) {
	resp := NewError(code, category, message)
	resp.Error.Details = details
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

// EtaMethod is the §25.2 method by which a progress ETA was produced.
type EtaMethod string

const (
	EtaHistoricalP50       EtaMethod = "historical_p50"
	EtaLinearExtrapolation EtaMethod = "linear_extrapolation"
	EtaFixedPhaseDurations EtaMethod = "fixed_phase_durations"
	EtaRateBased           EtaMethod = "rate_based"
	EtaNone                EtaMethod = "none"
)

// RateMetric is the §25.2 progress throughput metric: an agent that
// distrusts the server ETA can recompute its own from this value.
type RateMetric struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

// Progress is the §25.2 canonical progress envelope, included in the
// status responses of long-running operations (upgrades, restores,
// backups, drift reconciliations). The nullable fields are pointers so
// "no meaningful value" serializes as JSON null, distinct from zero.
type Progress struct {
	Percent           *float64    `json:"percent"`
	CompletedSteps    *int        `json:"completedSteps"`
	TotalSteps        *int        `json:"totalSteps"`
	CurrentStep       string      `json:"currentStep,omitempty"`
	CurrentStepDetail string      `json:"currentStepDetail,omitempty"`
	EtaSeconds        *int        `json:"etaSeconds"`
	EtaConfidence     float64     `json:"etaConfidence,omitempty"`
	EtaMethod         EtaMethod   `json:"etaMethod,omitempty"`
	RateMetric        *RateMetric `json:"rateMetric,omitempty"`
	StartedAt         string      `json:"startedAt,omitempty"`
	LastProgressAt    string      `json:"lastProgressAt,omitempty"`
	StalledForSeconds *int        `json:"stalledForSeconds"`
}

// SuggestedAction is the §25.3 machine-executable remediation hint
// returned for a degraded or unhealthy component. Confidence and Risk
// are populated only on ranked alternatives (the suggestedActions
// array form); a singular canonical action omits them.
type SuggestedAction struct {
	Action     string          `json:"action"`
	Endpoint   string          `json:"endpoint"`
	Body       json.RawMessage `json:"body,omitempty"`
	Reasoning  string          `json:"reasoning"`
	Runbook    string          `json:"runbook,omitempty"`
	Confidence float64         `json:"confidence,omitempty"`
	Risk       string          `json:"risk,omitempty"`
}

// rankedActionIssues are the §25.3 issues that present multiple ranked
// remediation alternatives rather than a single canonical action.
var rankedActionIssues = map[string]bool{
	"WARM_POOL_EXHAUSTED":       true,
	"WARM_POOL_LOW":             true,
	"CREDENTIAL_POOL_EXHAUSTED": true,
	"CIRCUIT_BREAKER_OPEN":      true,
}

// UsesRankedActions reports whether a §25.3 issue is presented with the
// ordered suggestedActions array (multiple reasonable responses) as
// opposed to a single canonical suggestedAction.
func UsesRankedActions(issue string) bool {
	return rankedActionIssues[issue]
}

// SortByConfidence orders ranked remediation alternatives by descending
// confidence in place, as §25.3 requires of the suggestedActions
// array. The sort is stable, so equal-confidence entries keep their
// original order.
func SortByConfidence(actions []SuggestedAction) {
	sort.SliceStable(actions, func(i, j int) bool {
		return actions[i].Confidence > actions[j].Confidence
	})
}
