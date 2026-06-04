// SPDX-License-Identifier: MIT

package llmproxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credfallback"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
)

// maxRequestBytes caps an inbound proxy request body. An LLM request
// carries a prompt and conversation history; 8 MiB is generous and
// bounds memory against a hostile or buggy agent pod.
const maxRequestBytes = 8 << 20

// §4.8 lines 1055-1056, §15.1 lines 1012-1013. A deliberate REJECT by
// a PreLLMRequest interceptor returns LLM_REQUEST_REJECTED (HTTP 403);
// a PostLLMResponse REJECT returns LLM_RESPONSE_REJECTED (HTTP 502).
// Both are distinct from INTERCEPTOR_TIMEOUT (HTTP 503), which a
// fail-closed interceptor error or timeout produces.
const (
	CodeLLMRequestRejected  = "LLM_REQUEST_REJECTED"
	CodeLLMResponseRejected = "LLM_RESPONSE_REJECTED"
)

// PolicyChain is the §4.8 interceptor chain the proxy runs at the
// PreLLMRequest and PostLLMResponse phases. *interceptor.Chain
// satisfies it; a nil PolicyChain disables the LLM interceptor phases.
type PolicyChain interface {
	Run(ctx context.Context, req interceptor.Request) interceptor.Result
	Len(phase interceptor.Phase) int
}

// CredentialResolver resolves a §4.9 credential lease to the real
// upstream credential the proxy injects. §4.9 keeps real upstream keys
// only in the Token Service's in-memory credential cache inside the
// gateway process; the resolver is that cache's read side.
type CredentialResolver interface {
	// UpstreamCredential returns the real upstream API key for the
	// lease's backing credential. ok is false when the cache holds no
	// credential for the lease.
	UpstreamCredential(lease credential.Lease) (apiKey string, ok bool)
}

// DenyList reports whether a credential is revoked, per the §4.9
// source-aware credential deny list.
type DenyList interface {
	// Revoked reports whether the credential identified by key is on
	// the deny list.
	Revoked(key credential.CredentialKey) bool
}

// BudgetGate is the §11.2 / §8.10 mid-session token-budget pre-flight.
// Allow reports whether the session may issue another proxied request;
// it returns false once the session has exhausted its token budget so
// the proxy rejects the request with BUDGET_EXHAUSTED before any
// upstream call (spec: §8.10 line 1108). A nil gate disables the check.
type BudgetGate interface {
	Allow(sessionID string) bool
}

// UsageRecorder receives the authoritative §4.9 token usage the proxy
// extracts from each upstream response. §4.9 makes the proxy-extracted
// counts the record for quota accounting; pod-reported counts are not
// accepted in proxy mode.
type UsageRecorder interface {
	// RecordUsage records the token usage of one request against a
	// lease.
	RecordUsage(lease credential.Lease, usage Usage)
}

// ProxyCache is the §4.9 semantic-cache seam on the proxy path. It backs
// the optional CachePolicy on a CredentialPool: before forwarding a
// non-streaming request the proxy consults the cache, and on a miss it
// records the upstream response for a later hit. A nil Cache, or a lease
// whose pool declares no enabled CachePolicy, leaves the path uncached
// (§4.9 caching is disabled by default and opt-in per pool).
//
// spec: spec/04_system-components.md lines 1542-1556.
type ProxyCache interface {
	// Lookup returns a cached upstream response body for reqBody, in the
	// dialect the agent pod speaks, and true on a hit. It is scoped to
	// the lease's pool CachePolicy and CacheScope; a pool with caching
	// disabled is always a miss. A miss is never an error.
	Lookup(ctx context.Context, lease credential.Lease, reqBody []byte) (respBody []byte, hit bool)
	// Store records respBody for reqBody under the lease's pool
	// CachePolicy. A pool with caching disabled is a no-op.
	Store(ctx context.Context, lease credential.Lease, reqBody, respBody []byte)
}

// Metrics receives §16.1 LLM-proxy telemetry. A nil Metrics disables
// emission. *gatewaymetrics.Metrics satisfies it.
//
// spec: §16.1 lines 97, 99, 100.
type Metrics interface {
	// IncLLMProxyConnections / DecLLMProxyConnections move the in-flight
	// proxy-request gauge.
	IncLLMProxyConnections()
	DecLLMProxyConnections()
	// ObserveLLMTranslation records native-translator CPU time for one
	// leg. direction is `request` or `response`.
	ObserveLLMTranslation(pool, provider, proxyDialect, direction string, seconds float64)
	// IncLLMTranslationError counts a translator failure by the §4.9
	// error taxonomy.
	IncLLMTranslationError(pool, provider, errorType string)
}

// Handler is the §4.9 LLM reverse proxy HTTP handler for the Anthropic
// Messages dialect. It resolves the agent pod's bearer lease token,
// runs the §4.9 per-request lease checks, translates the request into
// the upstream provider's wire format, injects the real upstream
// credential, forwards through the circuit breaker, and translates the
// response back. The real upstream key never leaves the gateway.
type Handler struct {
	// Leases resolves a bearer lease token to its §4.9 lease. The
	// interface admits both the in-memory store and the Postgres
	// backend.
	Leases credleasestore.LeaseStore
	// Translators dispatches each request to the translator for the
	// lease's resolved §4.9 provider (spec: §4.9 lines 1525-1526 —
	// Phase 11 extends the proxy to anthropic_direct, aws_bedrock,
	// vertex_ai, azure_openai, and openai_direct). When the registry
	// is non-nil and carries no entry for the lease provider, the
	// request is rejected with UPSTREAM_PROVIDER_UNSUPPORTED. When it
	// is nil the handler falls back to the single Translator below.
	Translators TranslatorRegistry
	// Translator is the default translator used when Translators is
	// nil. It serves the anthropic_direct single-provider deployment.
	Translator Translator
	// Forwarder sends the translated request upstream behind the
	// circuit breaker.
	Forwarder *Forwarder
	// Credentials resolves a lease to its real upstream credential.
	Credentials CredentialResolver
	// DenyList reports credential revocation. A nil DenyList denies
	// nothing.
	DenyList DenyList
	// Usage records the authoritative token usage of each proxied
	// request. A nil Usage discards the counts.
	Usage UsageRecorder
	// BudgetGate is the §11.2 / §8.10 mid-session token-budget
	// pre-flight: a request for a session that has already exhausted its
	// token budget is rejected with BUDGET_EXHAUSTED before any upstream
	// call. A nil gate disables the check.
	BudgetGate BudgetGate
	// Cache is the §4.9 semantic cache consulted on the non-streaming
	// request path. A nil Cache disables caching (the §4.9 default).
	Cache ProxyCache
	// Interceptors is the §4.8 policy chain run at the PreLLMRequest and
	// PostLLMResponse phases (spec: §4.8 lines 1055-1056, 1075). A nil
	// chain, or a phase with no registered interceptors, is a no-op:
	// these phases fire only in proxy mode and only when interceptors
	// are registered.
	Interceptors PolicyChain
	// Now returns the current time, checked against lease expiry. A nil
	// Now selects time.Now.
	Now func() time.Time
	// Metrics receives the §16.1 LLM-proxy telemetry. A nil Metrics
	// disables emission.
	Metrics Metrics
	// Fallback drives the §4.9 credentialPolicy Fallback Flow when an
	// upstream credential fault (RATE_LIMITED / AUTH_EXPIRED /
	// PROVIDER_UNAVAILABLE) is observed for a lease. A nil Fallback
	// leaves the proxy on its pre-fallback behavior: the upstream error
	// is surfaced to the pod with no rotation.
	//
	// spec: §4.9 lines 1383-1411 (Fallback Flow).
	Fallback *credfallback.Controller
	// FallbackRotator mints a replacement lease from the chain's next
	// pool and pushes it to the session's pod via RotateCredentials
	// (Fallback Flow steps 5-7). A nil rotator records the cooldown and
	// budget but performs no replacement push.
	FallbackRotator FallbackRotator
	// FallbackAudit emits the §4.9.2 credential.fallback_exhausted audit
	// event when the chain is exhausted. A nil sink skips emission.
	FallbackAudit FallbackAuditSink
	// FallbackTerminator terminates a session whose fallback chain is
	// exhausted (Fallback Flow step 3). A nil terminator returns the
	// terminal error to the pod without an out-of-band termination.
	FallbackTerminator FallbackTerminator
	// FallbackMetrics receives the §16.1 fallback counters
	// (lenny_credential_rotation_total,
	// lenny_gateway_credential_fallback_exhausted_total). A nil sink
	// disables emission.
	FallbackMetrics FallbackMetrics
}

// ServeHTTP implements the §4.9 proxy request path for one Anthropic
// Messages request.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED",
			"the LLM proxy accepts POST")
		return
	}

	// spec: §16.1 line 97 — the active-connections gauge reflects
	// in-flight proxy requests on the replica for the request's lifetime.
	if h.Metrics != nil {
		h.Metrics.IncLLMProxyConnections()
		defer h.Metrics.DecLLMProxyConnections()
	}

	// spec: §16.3 line 354 — every LLM-proxy request runs under the
	// credential.proxy_request span so distributed traces show the proxy
	// leg between an agent pod and the upstream provider. The span is
	// opened at the per-request entry; correlation attributes (tenant_id,
	// session_id, …) auto-project from the request context. §16.4 line 376
	// excludes credential-sensitive payload from span attributes, so no
	// lease token or upstream key is recorded here. The status the handler
	// writes is captured so the defer records a categorized span error for
	// any rejected request: a 4xx policy/lease rejection is a POLICY error,
	// a 5xx upstream/provider fault is an UPSTREAM error.
	ctx, span := tracing.NewTracer(nil).Start(r.Context(), tracing.SpanCredentialProxyRequest)
	sw := &statusRecorder{ResponseWriter: w}
	w = sw
	r = r.WithContext(ctx)
	defer func() {
		if sw.status >= http.StatusInternalServerError {
			tracing.RecordError(span, tracing.CategorizeError(
				errors.New(http.StatusText(sw.status)), tracing.CategoryUpstream))
		} else if sw.status >= http.StatusBadRequest {
			tracing.RecordError(span, tracing.CategorizeError(
				errors.New(http.StatusText(sw.status)), tracing.CategoryPolicy))
		}
		span.End()
	}()

	token := leaseToken(r)
	if token == "" {
		h.writeError(w, http.StatusUnauthorized, "LEASE_TOKEN_MISSING",
			"the request carries no lease token")
		return
	}
	lease, ok := h.Leases.GetByToken(token)
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "LEASE_TOKEN_INVALID",
			"the lease token does not resolve to an active lease")
		return
	}

	revoked := false
	if h.DenyList != nil {
		revoked = h.DenyList.Revoked(lease.CredentialKey())
	}
	switch lease.ValidateProxyRequest(credential.ProxyRequestCheck{
		Now:           h.now(),
		PeerSPIFFEURI: peerSPIFFE(r),
		Revoked:       revoked,
	}) {
	case credential.RejectExpired:
		h.writeError(w, http.StatusForbidden, "LEASE_EXPIRED",
			"the credential lease has expired")
		return
	case credential.RejectRevoked:
		h.writeError(w, http.StatusForbidden, "CREDENTIAL_REVOKED",
			"the credential backing this lease has been revoked")
		return
	case credential.RejectSpiffeMismatch:
		h.writeError(w, http.StatusForbidden, "LEASE_SPIFFE_MISMATCH",
			"the request's SPIFFE identity does not match the lease")
		return
	}

	// spec: §8.10 line 1108 / §11.2 line 44 — a session that has already
	// exhausted its token budget is terminated; any further proxied
	// request it issues before the pod drains is rejected up front with
	// BUDGET_EXHAUSTED (POLICY, non-retryable) before any upstream call.
	if h.BudgetGate != nil && lease.SessionID != "" && !h.BudgetGate.Allow(lease.SessionID) {
		h.writeError(w, http.StatusForbidden, "BUDGET_EXHAUSTED",
			"the session's token budget is exhausted")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "REQUEST_BODY_INVALID",
			"the request body could not be read: "+err.Error())
		return
	}

	// spec: §4.8 line 1055 — run the PreLLMRequest chain over the request
	// body before credential headers are injected. A REJECT returns
	// LLM_REQUEST_REJECTED; a MODIFY rewrites the body the proxy then
	// translates and forwards.
	body, ok = h.runLLMPhase(r.Context(), w, lease, interceptor.PhasePreLLMRequest, body)
	if !ok {
		return
	}

	// spec: §4.9 lines 1542-1556 — consult the per-pool semantic cache
	// before any upstream call. A hit replays the cached response in the
	// pod's dialect (re-running the PostLLMResponse chain so policy still
	// applies) and consumes no upstream tokens, so no usage is recorded.
	// Only non-streaming requests are cached; a streaming request always
	// goes upstream. A nil Cache or a pool with caching off is a miss.
	if h.Cache != nil && !requestWantsStream(body) {
		if cached, hit := h.Cache.Lookup(r.Context(), lease, body); hit {
			respBody, ok := h.runLLMPhase(r.Context(), w, lease, interceptor.PhasePostLLMResponse, cached)
			if !ok {
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(respBody)
			return
		}
	}

	tr, ok := h.translatorFor(lease.Provider)
	if !ok {
		h.writeError(w, http.StatusBadGateway, "UPSTREAM_PROVIDER_UNSUPPORTED",
			"no translator is registered for the lease's resolved provider")
		return
	}

	apiKey, ok := h.Credentials.UpstreamCredential(lease)
	if !ok {
		h.writeError(w, http.StatusBadGateway, "UPSTREAM_CREDENTIAL_UNAVAILABLE",
			"the gateway holds no upstream credential for this lease")
		return
	}

	// spec: §16.1 line 99 — measure the request-leg translator CPU time.
	reqStart := h.now()
	upstreamReq, err := tr.TranslateRequest(Request{
		Dialect:          DialectAnthropic,
		Body:             body,
		AnthropicVersion: r.Header.Get("anthropic-version"),
	}, apiKey)
	if err != nil {
		h.writeTranslationError(w, lease, err)
		return
	}
	h.observeTranslation(lease, "request", h.now().Sub(reqStart))

	if requestWantsStream(body) {
		h.serveStream(w, r, lease, upstreamReq)
		return
	}

	upstreamResp, err := h.Forwarder.Forward(r.Context(), upstreamReq)
	if err != nil {
		if errors.Is(err, ErrCircuitOpen) {
			h.writeError(w, http.StatusServiceUnavailable, "PROVIDER_UNAVAILABLE",
				"the upstream provider circuit breaker is open")
			return
		}
		h.writeTranslationError(w, lease, err)
		return
	}

	// spec: §16.1 line 99 — measure the response-leg translator CPU time.
	respStart := h.now()
	resp, err := tr.TranslateResponse(DialectAnthropic, *upstreamResp)
	if err != nil {
		h.writeTranslationError(w, lease, err)
		return
	}
	h.observeTranslation(lease, "response", h.now().Sub(respStart))

	// spec: §4.9 lines 1542-1556 — record the upstream response for a
	// later cache hit. Store the translated (pre-PostLLMResponse) body so
	// a hit replays it through the same interceptor chain as a miss. A
	// nil Cache or a pool with caching off is a no-op.
	if h.Cache != nil {
		h.Cache.Store(r.Context(), lease, body, resp.Body)
	}

	// spec: §4.8 line 1056 — run the PostLLMResponse chain over the
	// translated response before it reaches the pod. A REJECT returns
	// LLM_RESPONSE_REJECTED; a MODIFY rewrites the response body.
	respBody, ok := h.runLLMPhase(r.Context(), w, lease, interceptor.PhasePostLLMResponse, resp.Body)
	if !ok {
		return
	}

	h.recordUsage(lease, resp.Usage)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respBody)
}

// serveStream runs the §4.9 streaming proxy path: it forwards the
// request for a streaming response and relays the upstream Server-Sent
// Events stream to the agent pod. Once the 200 status and SSE headers
// are written the response is committed, so a mid-stream relay failure
// can only end the stream — it cannot change the status.
func (h *Handler) serveStream(w http.ResponseWriter, r *http.Request, lease credential.Lease, upstreamReq *UpstreamRequest) {
	resp, err := h.Forwarder.ForwardStream(r.Context(), upstreamReq)
	if err != nil {
		if errors.Is(err, ErrCircuitOpen) {
			h.writeError(w, http.StatusServiceUnavailable, "PROVIDER_UNAVAILABLE",
				"the upstream provider circuit breaker is open")
			return
		}
		h.writeTranslationError(w, lease, err)
		return
	}
	defer resp.Body.Close()

	// spec: §4.8 line 1056, 1075 — for a streaming response the
	// PostLLMResponse chain fires once on the initial response metadata
	// before any chunk is relayed; individual stream chunks are not
	// intercepted. A REJECT before the headers are committed converts the
	// stream into an LLM_RESPONSE_REJECTED error. A MODIFY does not apply
	// to the streamed chunks (they pass through unmodified per spec).
	if _, ok := h.runLLMPhase(r.Context(), w, lease, interceptor.PhasePostLLMResponse, streamMetadata(resp)); !ok {
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	flush := func() {}
	if f, ok := w.(http.Flusher); ok {
		flush = f.Flush
	}
	usage, _ := RelayStream(w, resp.Body, flush)
	h.recordUsage(lease, usage)
}

// runLLMPhase runs the §4.8 interceptor chain for an LLM proxy phase
// over content. It returns the chain's (possibly MODIFY-rewritten)
// content and ok == true when the chain admits the request. On a chain
// REJECT it writes the phase's pod-facing error envelope and returns
// ok == false so the caller stops. A nil chain or an empty phase chain
// is a no-op that returns content unchanged.
func (h *Handler) runLLMPhase(ctx context.Context, w http.ResponseWriter, lease credential.Lease, phase interceptor.Phase, content []byte) ([]byte, bool) {
	if h.Interceptors == nil || h.Interceptors.Len(phase) == 0 {
		return content, true
	}
	res := h.Interceptors.Run(ctx, interceptor.Request{
		Phase:     phase,
		SessionID: lease.SessionID,
		TenantID:  lease.TenantID,
		Content:   content,
	})
	switch res.Action {
	case interceptor.ActionReject:
		h.writeLLMRejection(w, phase, res)
		return nil, false
	case interceptor.ActionModify:
		return res.ModifiedContent, true
	default:
		return content, true
	}
}

// writeLLMRejection maps a REJECT from an LLM proxy phase to its
// pod-facing HTTP status and error code (spec: §4.8 line 1056, §15.1
// lines 1012-1013). A fail-closed interceptor timeout or error carries
// CodeInterceptorTimeout and returns 503 INTERCEPTOR_TIMEOUT; a
// deliberate PreLLMRequest REJECT returns 403 LLM_REQUEST_REJECTED and
// a PostLLMResponse REJECT returns 502 LLM_RESPONSE_REJECTED.
func (h *Handler) writeLLMRejection(w http.ResponseWriter, phase interceptor.Phase, res interceptor.Result) {
	if res.Code == interceptor.CodeInterceptorTimeout {
		h.writeError(w, http.StatusServiceUnavailable, interceptor.CodeInterceptorTimeout, res.Reason)
		return
	}
	if phase == interceptor.PhasePreLLMRequest {
		h.writeError(w, http.StatusForbidden, CodeLLMRequestRejected, res.Reason)
		return
	}
	h.writeError(w, http.StatusBadGateway, CodeLLMResponseRejected, res.Reason)
}

// streamMetadata serializes the initial upstream streaming-response
// metadata the PostLLMResponse chain inspects: the upstream HTTP status
// and response headers. The full SSE stream is never buffered (spec:
// §4.8 line 1075).
func streamMetadata(resp *http.Response) []byte {
	b, _ := json.Marshal(struct {
		Status  int         `json:"status"`
		Headers http.Header `json:"headers"`
	}{Status: resp.StatusCode, Headers: resp.Header})
	return b
}

// requestWantsStream reports whether an Anthropic Messages request body
// asks for a streaming response.
func requestWantsStream(body []byte) bool {
	var req struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &req)
	return req.Stream
}

// recordUsage forwards the authoritative token usage to the configured
// recorder. It is a no-op when no recorder is set.
func (h *Handler) recordUsage(lease credential.Lease, usage Usage) {
	if h.Usage != nil {
		h.Usage.RecordUsage(lease, usage)
	}
}

// now returns the handler clock.
func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// translatorFor resolves the §4.9 translator for a lease's provider.
// When the Translators registry is set it dispatches on the provider
// and reports false for an unregistered provider. When the registry is
// nil it falls back to the single default Translator.
func (h *Handler) translatorFor(provider credential.Provider) (Translator, bool) {
	if h.Translators != nil {
		return h.Translators.For(string(provider))
	}
	if h.Translator != nil {
		return h.Translator, true
	}
	return nil, false
}

// leaseToken extracts the bearer lease token an Anthropic-dialect agent
// pod presents. The Anthropic SDK sends the lease token as its API key
// in the x-api-key header; an Authorization: Bearer header is also
// accepted.
func leaseToken(r *http.Request) string {
	if k := r.Header.Get("x-api-key"); k != "" {
		return k
	}
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
		return strings.TrimPrefix(a, "Bearer ")
	}
	return ""
}

// peerSPIFFE returns the SPIFFE URI from the request's mTLS peer
// certificate, or "" when the connection carries no SPIFFE identity.
// The SPIFFE identity is a spiffe:// URI SAN on the leaf certificate.
func peerSPIFFE(r *http.Request) string {
	if r.TLS == nil {
		return ""
	}
	for _, cert := range r.TLS.PeerCertificates {
		for _, u := range cert.URIs {
			if u != nil && u.Scheme == "spiffe" {
				return u.String()
			}
		}
	}
	return ""
}

// statusRecorder wraps an http.ResponseWriter to capture the status code
// the handler writes so the credential.proxy_request span can record a
// categorized error for a rejected request without instrumenting every
// error return. It forwards Flush so the streaming path still detects an
// http.Flusher (spec: §4.9 streaming proxy path). A status of 0 (never
// written, which net/http treats as 200) is a success.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// proxyError is the JSON error envelope the proxy returns to the agent
// pod on a request the gateway rejects before or during the upstream
// call.
type proxyError struct {
	Error proxyErrorBody `json:"error"`
}

type proxyErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeError writes a proxy error envelope with the given HTTP status.
func (h *Handler) writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(proxyError{Error: proxyErrorBody{Code: code, Message: message}})
}

// observeTranslation records the §16.1 translator-leg CPU time for a
// completed translation. direction is `request` or `response`. It is a
// no-op when no Metrics sink is set.
//
// spec: §16.1 line 99.
func (h *Handler) observeTranslation(lease credential.Lease, direction string, d time.Duration) {
	if h.Metrics != nil {
		h.Metrics.ObserveLLMTranslation(lease.PoolID, string(lease.Provider), string(DialectAnthropic), direction, d.Seconds())
	}
}

// writeTranslationError maps a §4.9 translator error to its pod-facing
// HTTP status and error code, and counts the failure under the §16.1
// lenny_gateway_llm_translation_errors_total taxonomy (spec: §16.1 line
// 100). A non-TranslationError is an internal fault and is not counted
// against the translator taxonomy.
func (h *Handler) writeTranslationError(w http.ResponseWriter, lease credential.Lease, err error) {
	var te *TranslationError
	if !errors.As(err, &te) {
		h.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	if h.Metrics != nil {
		h.Metrics.IncLLMTranslationError(lease.PoolID, string(lease.Provider), string(te.Type))
	}
	// spec: §4.9 lines 1383-1411 — an upstream credential fault drives
	// the Fallback Flow before the pod-facing error is written. When the
	// chain is exhausted the terminal CREDENTIAL_FALLBACK_EXHAUSTED error
	// is written here and no further mapping runs.
	if trig, ok := faultTrigger(te.Type); ok {
		if h.driveFallback(w, lease, trig, string(te.Type)) {
			return
		}
	}
	switch te.Type {
	case ErrUnsupportedField, ErrSchemaMismatch, ErrUpstream4xx:
		h.writeError(w, http.StatusBadRequest, "PROVIDER_REQUEST_INVALID", te.Message)
	case ErrAuthFailed:
		h.writeError(w, http.StatusBadGateway, "PROVIDER_AUTH_FAILED", te.Message)
	case ErrTimeout:
		h.writeError(w, http.StatusGatewayTimeout, "PROVIDER_TIMEOUT", te.Message)
	default: // ErrUpstream5xx, ErrStreamingInterrupted
		h.writeError(w, http.StatusServiceUnavailable, "PROVIDER_UNAVAILABLE", te.Message)
	}
}
