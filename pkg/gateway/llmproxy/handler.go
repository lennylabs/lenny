// SPDX-License-Identifier: MIT

package llmproxy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
)

// maxRequestBytes caps an inbound proxy request body. An LLM request
// carries a prompt and conversation history; 8 MiB is generous and
// bounds memory against a hostile or buggy agent pod.
const maxRequestBytes = 8 << 20

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
	Revoked(key credential.DenyListKey) bool
}

// Handler is the §4.9 LLM reverse proxy HTTP handler for the Anthropic
// Messages dialect. It resolves the agent pod's bearer lease token,
// runs the §4.9 per-request lease checks, translates the request into
// the upstream provider's wire format, injects the real upstream
// credential, forwards through the circuit breaker, and translates the
// response back. The real upstream key never leaves the gateway.
type Handler struct {
	// Leases resolves a bearer lease token to its §4.9 lease.
	Leases *credleasestore.Store
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
		revoked = h.DenyList.Revoked(lease.DenyListKey())
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(resp.Body)
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
