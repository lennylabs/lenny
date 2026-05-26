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
	// spec: §11.5 line 277 ("returns the cached response … without
	// re-executing the operation"). The middleware atomically claims the
	// (tenant_id, idempotency_key) row before executing the inner handler;
	// a concurrent retry that arrives while the original is still in flight
	// observes the pending claim and is rejected with this code so the two
	// retries do not double-execute the side effect. POLICY: the gate is a
	// deliberate concurrency rule. retryable=true: once the original
	// finishes, the next retry replays the cached response. F-11.5.2.
	"IDEMPOTENCY_KEY_IN_FLIGHT": {CategoryPolicy, true},
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
	"LLM_REQUEST_REJECTED":  {CategoryPermanent, false},
	"LLM_RESPONSE_REJECTED": {CategoryPermanent, false},
	// spec: §15.1 line 1067, §8.3 line 157 — a delegation whose
	// TaskSpec.input exceeds the effective contentPolicy.maxInputSize is
	// rejected by the §4.8 DelegationPolicyEvaluator at PreDelegation.
	// PERMANENT and not retryable: the caller must reduce the input size.
	"INPUT_TOO_LARGE":              {CategoryPermanent, false},
	"MCP_PROTOCOL_VERSION_RETIRED": {CategoryPermanent, false},
	"MCP_VERSION_UNSUPPORTED":      {CategoryPermanent, false},
	"UPSTREAM_ERROR":               {CategoryUpstream, true},
	"UPSTREAM_TIMEOUT":             {CategoryUpstream, true},
	// §4.1 dedicated /mcp/runtimes/{name} surface error codes.
	"INVALID_RUNTIME_TYPE": {CategoryPermanent, false},
	"RUNTIME_UNAVAILABLE":  {CategoryTransient, true},
	"METHOD_NOT_ALLOWED":   {CategoryPermanent, false},
	// spec: §15.2.1 line 1017 — no idle pods (or no free concurrent
	// slots) are available after exhausting the claim path; the client
	// should retry with exponential backoff.
	"WARM_POOL_EXHAUSTED": {CategoryTransient, true},
	// §4.3 line 214: a session that requires LLM or OAuth credentials
	// fails with a retryable error when the Token Service is
	// unavailable, so clients can back off and retry. The session-start
	// handler emits this code with HTTP 503 + Retry-After.
	"TOKEN_SERVICE_UNAVAILABLE": {CategoryUpstream, true},
	// §4.9 line 1212 admin-time RBAC live-probe. A DENIED/NOT_FOUND
	// verdict is PERMANENT: the secretRef cannot be read until the
	// operator patches the Token Service RBAC, so retrying the same
	// write fails identically.
	"CREDENTIAL_SECRET_RBAC_MISSING": {CategoryPermanent, false},
	// §4.9 line 1212: the probe could not be evaluated (Token Service
	// unreachable, mTLS handshake failure, upstream Kubernetes API
	// timeout). TRANSIENT and retryable; the write was rejected rather
	// than fail open.
	"CREDENTIAL_PROBE_UNAVAILABLE": {CategoryTransient, true},
	// §4.9 lines 1218, §15.1 line 990 — the §4.9 pre-claim check found
	// no provider in the intersection with an assignable credential, or
	// the credential pool exhausted at assignment. POLICY, HTTP 503; the
	// client may retry once the pool frees up.
	"CREDENTIAL_POOL_EXHAUSTED": {CategoryPolicy, true},
	// §4.9 lines 1364, §15.1 line 993 — a user-only credentialPolicy had
	// no pre-registered credential for the user and provider. PERMANENT,
	// HTTP 404; the same request fails until the user registers a
	// credential or the operator configures pool fallback.
	"USER_CREDENTIAL_NOT_FOUND": {CategoryPermanent, false},
}
