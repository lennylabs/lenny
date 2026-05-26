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
	// spec: §11.5 line 277 — an idempotency key that exceeds the 128-rune
	// limit or fails the tenant scoping check is rejected before any
	// dispatch happens. PERMANENT (the same key fails identically until
	// the client picks a different one). The MCP surface uses this
	// dedicated code so callers can distinguish a malformed key from a
	// generic body-validation failure. F-11.5.1 / F-15.1.30.
	"INVALID_IDEMPOTENCY_KEY": {CategoryPermanent, false},
	// spec: §15.1 line 1052 — `POST /v1/sessions/{id}/derive` rejected
	// because the source session is non-terminal and the caller did not
	// pass `allowStale: true`. PERMANENT (the source state controls the
	// failure; the same request fails until the caller sets the flag or
	// the source reaches a terminal state). F-15.1.29.
	"DERIVE_ON_LIVE_SESSION": {CategoryPermanent, false},
	// spec: §15.1 line 1054 — `POST /v1/sessions/{id}/derive` failed
	// because the workspace snapshot object was not found in object
	// storage (TTL expiry or GC). TRANSIENT — re-issuing immediately is
	// unlikely to help but the underlying condition can resolve out of
	// band; HTTP 503.
	"DERIVE_SNAPSHOT_UNAVAILABLE": {CategoryTransient, true},
	// spec: §15.1 line 1055 — derive/replay/delegate rejected by the
	// SEC-001 isolation-monotonicity rule. POLICY (the deployer's
	// isolation lattice forbids the move); HTTP 422. Overridable only
	// by a platform-admin with `allowIsolationDowngrade: true`.
	"ISOLATION_MONOTONICITY_VIOLATED": {CategoryPolicy, false},
	// spec: §15.1 line 1053 — derive rate-limited because too many
	// concurrent derives are running against the same source session.
	// POLICY, retryable=true with exponential backoff; HTTP 429.
	"DERIVE_LOCK_CONTENTION": {CategoryPolicy, true},
	// spec: §15.1 line 1084 — `respond_to_elicitation` /
	// `dismiss_elicitation` rejected because the (session_id, user_id,
	// elicitation_id) triple does not match any pending elicitation.
	// PERMANENT, retryable=false; HTTP 404. 404 is returned in every
	// mismatch case to avoid leaking the existence of other-session
	// elicitations. F-9.2.17 / F-9.2.18.
	"ELICITATION_NOT_FOUND": {CategoryPermanent, false},
	// spec: §15.1 line 1085 — an intermediate hop's elicitation/create
	// re-emission carried a {message, schema} pair that diverges from
	// the gateway-recorded original; the chain walker dropped the
	// forward and the originating pod sees its pending elicitation fail
	// with a tamper signal. PERMANENT, retryable=false; HTTP 409.
	"ELICITATION_CONTENT_TAMPERED": {CategoryPermanent, false},
	// spec: §9.2 line 103 — `lenny/request_elicitation` timed out
	// waiting for a human response within `maxElicitationWait` (default
	// 600 s). The elicitation is dismissed by the gateway and the
	// agent receives this structured error so it can either give up
	// or raise a fresh elicitation. TRANSIENT — a fresh elicitation
	// (a new elicitation_id) may succeed; not retryable as-is because
	// the original elicitation has already been dismissed. F-9.2.18.
	"ELICITATION_TIMEOUT": {CategoryTransient, false},
	// spec: §9.2 line 87 — agent-initiated url-mode elicitation dropped
	// because the URL's effective host does not match any entry in the
	// pool's `urlModeElicitation.domainAllowlist`. POLICY, retryable=
	// false; HTTP 403. The pool admin must allowlist the host or the
	// agent must stop emitting URL-mode elicitations.
	"DOMAIN_NOT_ALLOWLISTED": {CategoryPolicy, false},
	// spec: §15.1 — a `respond_to_elicitation` / `dismiss_elicitation`
	// (REST or MCP) lost the race with a prior resolution. PERMANENT,
	// retryable=false; HTTP 409. The interaction is already resolved,
	// so the second resolver should observe the recorded phase rather
	// than retry. F-9.2.17.
	"INTERACTION_ALREADY_RESOLVED": {CategoryPolicy, false},
}
