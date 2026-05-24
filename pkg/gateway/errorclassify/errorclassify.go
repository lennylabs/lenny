// SPDX-License-Identifier: MIT

// Package errorclassify is the shared §15.2.1 error classifier the
// gateway's REST and MCP transports both consult to populate the
// category and retryable fields on every error envelope. The spec
// requires identical category and retryable values across surfaces
// for the same error code; routing every emission through one
// function is how that parity holds.
package errorclassify

// Category is the §15.2.1 / §16.3 error category. Every lenny error
// code maps to exactly one Category.
type Category string

const (
	// CategoryTransient covers errors a retry is expected to clear:
	// transport hiccups, leader-election handoffs, store contention.
	CategoryTransient Category = "TRANSIENT"

	// CategoryPermanent covers errors the same request will fail
	// against indefinitely: malformed bodies, schema violations,
	// resource-not-found, and protocol-version retirements.
	CategoryPermanent Category = "PERMANENT"

	// CategoryPolicy covers errors a policy decision produced: an
	// admission webhook rejection, a circuit breaker that is open,
	// or a delegation cycle.
	CategoryPolicy Category = "POLICY"

	// CategoryUpstream covers errors that originate from a backing
	// service the gateway depends on: an LLM provider that returned
	// a 5xx, a connector OAuth token endpoint that timed out.
	CategoryUpstream Category = "UPSTREAM"
)

// Classify returns the §15.2.1 category and retryable pair for code.
// Unknown codes fall back to (CategoryTransient, true): an unknown
// failure is provisionally retryable so a downstream client does not
// give up on a transient condition the classifier has not yet learned.
// New codes should be added to the table explicitly so the fallback
// stays informational rather than load-bearing.
func Classify(code string) (Category, bool) {
	if c, ok := table[code]; ok {
		return c.cat, c.retryable
	}
	return CategoryTransient, true
}

type entry struct {
	cat       Category
	retryable bool
}

// table is the §15.2.1 code → (category, retryable) map. The keys
// are the canonical lenny error codes the gateway emits.
var table = map[string]entry{
	"INVALID_REQUEST":             {CategoryPermanent, false},
	"VALIDATION_ERROR":            {CategoryPermanent, false},
	"WORKSPACE_PLAN_INVALID":      {CategoryPermanent, false},
	"RESOURCE_NOT_FOUND":          {CategoryPermanent, false},
	"RESOURCE_HAS_DEPENDENTS":     {CategoryPolicy, false},
	"IDEMPOTENCY_KEY_REUSED":      {CategoryPermanent, false},
	"PRECONDITION_FAILED":         {CategoryPermanent, false},
	"UNAUTHORIZED":                {CategoryPermanent, false},
	"FORBIDDEN":                   {CategoryPermanent, false},
	"CONFLICT":                    {CategoryPermanent, false},
	"PAYLOAD_TOO_LARGE":           {CategoryPermanent, false},
	"UNSUPPORTED_MEDIA_TYPE":      {CategoryPermanent, false},
	"INTERNAL_ERROR":              {CategoryTransient, true},
	"SERVICE_UNAVAILABLE":         {CategoryTransient, true},
	"TIMEOUT":                     {CategoryTransient, true},
	"CIRCUIT_BREAKER_OPEN":        {CategoryPolicy, false},
	"DELEGATION_CYCLE_DETECTED":   {CategoryPermanent, false},
	"DELEGATION_BUDGET_EXHAUSTED": {CategoryPolicy, false},
	"QUOTA_EXCEEDED":              {CategoryPolicy, false},
	"RATE_LIMITED":                {CategoryPolicy, true},
	"INTERCEPTOR_REJECTED":        {CategoryPolicy, false},
	// spec: §15.1 line 1008 — an interceptor timeout in a fail-closed
	// chain is TRANSIENT and retryable, distinct from the POLICY
	// INTERCEPTOR_REJECTED a deliberate REJECT produces.
	"INTERCEPTOR_TIMEOUT":            {CategoryTransient, true},
	"INTERCEPTOR_COOLDOWN_IMMUTABLE": {CategoryPolicy, false},
	// spec: §15.1 lines 1012-1013 — a deliberate REJECT by a PreLLMRequest
	// or PostLLMResponse interceptor in the §4.9 LLM proxy. PERMANENT (the
	// same request fails again), distinct from the TRANSIENT
	// INTERCEPTOR_TIMEOUT a fail-closed interceptor error produces.
	"LLM_REQUEST_REJECTED":         {CategoryPermanent, false},
	"LLM_RESPONSE_REJECTED":        {CategoryPermanent, false},
	"MCP_PROTOCOL_VERSION_RETIRED": {CategoryPermanent, false},
	"MCP_VERSION_UNSUPPORTED":      {CategoryPermanent, false},
	"UPSTREAM_ERROR":               {CategoryUpstream, true},
	"UPSTREAM_TIMEOUT":             {CategoryUpstream, true},
	// §4.1 dedicated /mcp/runtimes/{name} surface error codes.
	"INVALID_RUNTIME_TYPE": {CategoryPermanent, false},
	"RUNTIME_UNAVAILABLE":  {CategoryTransient, true},
	"METHOD_NOT_ALLOWED":   {CategoryPermanent, false},
	// §4.3 line 214: a session that requires LLM or OAuth credentials
	// fails with a retryable error when the Token Service is
	// unavailable, so clients can back off and retry. The session-start
	// handler emits this code with HTTP 503 + Retry-After.
	"TOKEN_SERVICE_UNAVAILABLE": {CategoryUpstream, true},
}
