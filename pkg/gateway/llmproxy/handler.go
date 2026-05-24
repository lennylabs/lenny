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
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
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

// UsageRecorder receives the authoritative §4.9 token usage the proxy
// extracts from each upstream response. §4.9 makes the proxy-extracted
// counts the record for quota accounting; pod-reported counts are not
// accepted in proxy mode.
type UsageRecorder interface {
	// RecordUsage records the token usage of one request against a
	// lease.
	RecordUsage(lease credential.Lease, usage Usage)
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
	// Translator converts the proxy-dialect request and response to and
	// from the upstream provider's wire format.
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
	// Interceptors is the §4.8 policy chain run at the PreLLMRequest and
	// PostLLMResponse phases (spec: §4.8 lines 1055-1056, 1075). A nil
	// chain, or a phase with no registered interceptors, is a no-op:
	// these phases fire only in proxy mode and only when interceptors
	// are registered.
	Interceptors PolicyChain
	// Now returns the current time, checked against lease expiry. A nil
	// Now selects time.Now.
	Now func() time.Time
}

// ServeHTTP implements the §4.9 proxy request path for one Anthropic
// Messages request.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED",
			"the LLM proxy accepts POST")
		return
	}

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

	apiKey, ok := h.Credentials.UpstreamCredential(lease)
	if !ok {
		h.writeError(w, http.StatusBadGateway, "UPSTREAM_CREDENTIAL_UNAVAILABLE",
			"the gateway holds no upstream credential for this lease")
		return
	}

	upstreamReq, err := h.Translator.TranslateRequest(Request{
		Dialect:          DialectAnthropic,
		Body:             body,
		AnthropicVersion: r.Header.Get("anthropic-version"),
	}, apiKey)
	if err != nil {
		h.writeTranslationError(w, err)
		return
	}

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
		h.writeTranslationError(w, err)
		return
	}

	resp, err := h.Translator.TranslateResponse(DialectAnthropic, *upstreamResp)
	if err != nil {
		h.writeTranslationError(w, err)
		return
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
		h.writeTranslationError(w, err)
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

// writeTranslationError maps a §4.9 translator error to its pod-facing
// HTTP status and error code.
func (h *Handler) writeTranslationError(w http.ResponseWriter, err error) {
	var te *TranslationError
	if !errors.As(err, &te) {
		h.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
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
