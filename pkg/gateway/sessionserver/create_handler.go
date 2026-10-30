// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// CreateSessionRequest is the §15.1 POST /v1/sessions body. Each
// optional field is validated when present; only `runtimeRef` is
// required by the minimal gateway. Future phases add `timeouts`,
// `credentialPolicy`, `delegationPolicy`, etc.
type CreateSessionRequest struct {
	RuntimeRef    string          `json:"runtimeRef"`
	UserID        string          `json:"userId,omitempty"`
	WorkspacePlan json.RawMessage `json:"workspacePlan,omitempty"`

	// Environment is the optional §10.6 environment the session is
	// created in. Recorded on the session row; an empty value leaves
	// the session unscoped to any environment.
	Environment string `json:"environment,omitempty"`

	// IsolationProfile is an optional override that pins the session
	// to a specific §5.3 profile. Production resolves this from the
	// `targetPool`'s pool definition; the minimal gateway accepts it
	// from the body so SEC-001 monotonicity tests have a knob to drive.
	IsolationProfile isolation.Profile `json:"isolationProfile,omitempty"`

	// Metadata is the §7.1 client-supplied
	// CreateSession(..., metadata) payload — a flat string→string map
	// of caller annotations preserved verbatim for the session
	// lifetime. Non-string values rejected at decode with
	// 400 VALIDATION_ERROR so the on-row shape stays bounded. The §15.1
	// GET envelope echoes this back so a client that lost the create
	// response can retrieve its own annotations. F-7.3.20.
	// spec: §7.1 — "CreateSession(runtime, pool, retryPolicy,
	// metadata)".
	Metadata map[string]string `json:"metadata,omitempty"`

	// Labels is the §14 client-supplied session label set — a
	// flat string→string map of caller tags the `GET /v1/sessions` list
	// endpoint filters on (§15.1). Keys must be non-empty. The
	// §15.1 GET envelope echoes them back. F-15.1.15.
	// spec: §14; §15.1.
	Labels map[string]string `json:"labels,omitempty"`

	// RetryPolicy is the §7.3 client-supplied retry policy. The gateway
	// clamps each field against the deployer caps (RetryPolicyCaps) at
	// admission so a client cannot grow its budget past the platform
	// bounds; an unset/zero value falls through to the corresponding
	// cap as the effective value. Negative values reject as
	// 400 VALIDATION_ERROR. The §15.1 GET envelope echoes the clamped
	// policy back. F-7.3.1.
	// spec: §7.3.
	RetryPolicy *session.RetryPolicy `json:"retryPolicy,omitempty"`

	// Env is the §14 client-supplied environment-variable map injected
	// into the agent session. Every key is validated against the deployer
	// blocklist at admission; a blocked key rejects with
	// 400 ENV_VAR_BLOCKLISTED. The §15.1 GET envelope echoes it back.
	// spec: §14. F-14.1.12.
	Env map[string]string `json:"env,omitempty"`

	// Pool is the §14 / §14.1 client-requested target pool. The
	// minimal gateway records it for echo and admission pool-scope; the
	// resolved pool the gateway schedules against is reported separately.
	// spec: §14 example; §14.1. F-14.1.14.
	Pool string `json:"pool,omitempty"`

	// Timeouts is the §14 per-session timeout override block. The gateway
	// rejects a maxSessionAge that exceeds the runtime's
	// limits.maxSessionAge. spec: §14. F-14.1.14.
	Timeouts *sessionstore.SessionTimeouts `json:"timeouts,omitempty"`

	// CredentialPolicy is the §14 per-session credentialPolicy override.
	// A per-session override can only restrict, never expand, the tenant
	// policy. spec: §14 credentialPolicy; §4.9. F-14.1.14.
	CredentialPolicy *sessionstore.CredentialPolicyOverride `json:"credentialPolicy,omitempty"`

	// DelegationLease is the §14 client-requested delegation lease bounds
	// {maxDepth, maxChildrenTotal, delegationPolicyRef}. spec: §14. F-14.1.14.
	DelegationLease *sessionstore.DelegationLeaseRequest `json:"delegationLease,omitempty"`

	// RuntimeOptions is the §14 per-runtime discriminated-union options
	// blob (≤64 KB). Validated against the target runtime's
	// runtimeOptionsSchema when registered; when no schema is registered
	// a RuntimeOptionsUnschematized warning is emitted. spec: §14. F-14.1.14 / F-14.1.15.
	RuntimeOptions json.RawMessage `json:"runtimeOptions,omitempty"`

	// CallbackURL is the §14 optional session-terminal webhook. The
	// gateway validates it against the §14 SSRF mitigations at admission
	// (HTTPS-only, IP-literal/private-range rejection, DNS pinning, and
	// the optional deployer domain allowlist) and rejects a failing URL
	// with 400 INVALID_CALLBACK_URL. spec: §14. F-14.1.11.
	CallbackURL string `json:"callbackUrl,omitempty"`

	// CallbackSecret is the §14 HMAC signing secret for callback
	// deliveries. It is write-only: the gateway KMS-envelope-encrypts it
	// at admission and never returns the plaintext on any API. spec: §14. F-14.1.11.
	CallbackSecret string `json:"callbackSecret,omitempty"`
}

// handleCreate implements POST /v1/sessions. Returns 201 with the
// CreateSessionResponse envelope on success.
func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeCreateRequest(w, r)
	if !ok {
		return
	}
	s.createSession(w, r, req)
}

// handleEnvironmentSessions implements POST /v1/environments/{name}/sessions
// — the §10.6 explicit-environment session-creation path. It runs the
// regular create flow with the session environment taken from the URL
// path, overriding any environment supplied in the request body.
func (s *Server) handleEnvironmentSessions(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeCreateRequest(w, r)
	if !ok {
		return
	}
	req.Environment = r.PathValue("name")
	s.createSession(w, r, req)
}

// decodeCreateRequest reads a CreateSessionRequest from the request
// body, writing the §15.1 INVALID_REQUEST envelope and returning
// ok=false on a malformed body.
func (s *Server) decodeCreateRequest(w http.ResponseWriter, r *http.Request) (CreateSessionRequest, bool) {
	var req CreateSessionRequest
	body := jsonReader(w, r)
	defer body.Close()
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return CreateSessionRequest{}, false
	}
	return req, true
}
