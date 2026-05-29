// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/connectoroauth"
	"github.com/lennylabs/lenny/pkg/gateway/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/observability/audit"
)

// ClientSecretResolver resolves a connector's confidential-client
// secret from its §9.3 auth.clientSecretRef (a `namespace/name`
// reference). §9.3 keeps the raw secret out of the connector registry,
// so the OAuth token exchange resolves it through this seam at flow
// time. A production deployment backs it with a Kubernetes Secret
// reader; the in-process MemoryClientSecretResolver serves tests and
// the minimal gateway.
//
// A connector with no clientSecretRef is a §9.3 public client: the
// OAuth handlers never call Resolve for it and rely on PKCE alone.
type ClientSecretResolver interface {
	// Resolve returns the client secret for the given
	// `namespace/name` reference. It returns ErrClientSecretNotFound
	// when the reference names no secret.
	Resolve(ctx context.Context, ref string) (string, error)
}

// ErrClientSecretNotFound is returned by a ClientSecretResolver when a
// clientSecretRef names no secret.
var ErrClientSecretNotFound = errors.New("admin: connector client secret not found")

// MemoryClientSecretResolver is an in-process ClientSecretResolver
// backed by a static map of `namespace/name` reference to secret. It
// serves tests and the minimal gateway; production wires a Kubernetes
// Secret reader.
type MemoryClientSecretResolver struct {
	secrets map[string]string
}

// NewMemoryClientSecretResolver returns a resolver over a copy of
// secrets, keyed by `namespace/name` reference.
func NewMemoryClientSecretResolver(secrets map[string]string) *MemoryClientSecretResolver {
	cp := make(map[string]string, len(secrets))
	for k, v := range secrets {
		cp[k] = v
	}
	return &MemoryClientSecretResolver{secrets: cp}
}

// Resolve implements ClientSecretResolver.
func (m *MemoryClientSecretResolver) Resolve(_ context.Context, ref string) (string, error) {
	s, ok := m.secrets[ref]
	if !ok {
		return "", ErrClientSecretNotFound
	}
	return s, nil
}

// ConnectorOAuth bundles the dependencies of the §9.3 connector OAuth
// 2.1 authorization-code flow handlers: the signed-state minter, the
// per-flow state store, the connector-credential store the resulting
// tokens land in, and the HTTP client used for the provider token
// exchange.
type ConnectorOAuth struct {
	// StateSigner mints and verifies the §9.3 anti-CSRF `state`.
	StateSigner *connectoroauth.StateSigner

	// StateStore holds the per-flow PKCE context keyed by `state`, with
	// the §9.3 10-minute TTL and single-use consumption.
	StateStore connectoroauth.StateStore

	// Credentials is the §9.3 connector-credential registry the
	// exchanged tokens are stored in.
	Credentials connectorcredstore.Store

	// ClientSecrets resolves a confidential connector's client secret
	// at token-exchange time. Optional: a deployment with public
	// connectors only may leave it nil.
	ClientSecrets ClientSecretResolver

	// HTTPClient is the client used for the provider token exchange.
	// Nil defaults to a client with a 15-second timeout.
	HTTPClient connectoroauth.HTTPDoer

	// CallbackURL is the absolute URL the provider redirects back to —
	// the gateway's connector OAuth callback endpoint. It is sent as
	// `redirect_uri` in the authorization request and replayed in the
	// token exchange.
	CallbackURL string

	// StateTTL overrides the §9.3 default 10-minute state TTL. Zero
	// uses connectoroauth.DefaultStateTTL.
	StateTTL time.Duration
}

// WithConnectorOAuth wires the §9.3 connector OAuth 2.1 flow handlers
// onto the Router. The connector registry must also be wired (via
// WithConnectors) for the OAuth routes to register.
func (r *Router) WithConnectorOAuth(c *ConnectorOAuth) *Router {
	r.connectorOAuth = c
	return r
}

// AuthorizeConnectorResponse is the §9.3 authorization-initiation
// response: the provider authorization URL the client opens, plus the
// connector and the flow's state value for correlation.
type AuthorizeConnectorResponse struct {
	// AuthorizationURL is the provider authorization-code URL carrying
	// the PKCE code_challenge and the signed state.
	AuthorizationURL string `json:"authorizationUrl"`

	// ConnectorID is the connector the flow authorizes.
	ConnectorID string `json:"connectorId"`

	// State is the signed anti-CSRF value bound to this flow.
	State string `json:"state"`
}

// handleAuthorizeConnector implements the §9.3 authorization-initiation
// endpoint: POST /v1/admin/connectors/{id}/oauth/authorize.
//
// It builds the provider authorization URL for the path connector with
// a PKCE S256 code_challenge and a signed state, stores the
// code_verifier and per-flow context in the StateStore keyed by the
// state value with the §9.3 10-minute TTL, and returns the URL. Any
// authenticated caller may initiate a flow; the resulting credential
// is scoped to that caller's (tenant, connector, user) triple.
func (r *Router) handleAuthorizeConnector(w http.ResponseWriter, req *http.Request) {
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		// spec: §15.1 line 986 — UNAUTHORIZED is the canonical 401 code.
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"connector OAuth authorization requires an authenticated caller",
			map[string]any{"reason": "auth_required"})
		return
	}
	id := req.PathValue("id")
	// spec: §4.2 line 173 — connectors are tenant-scoped; OAuth
	// authorize resolves the connector under the caller's tenant.
	conn, err := r.connectors.Get(req.Context(), principal.TenantID, id)
	if err != nil {
		if errors.Is(err, connectorstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "connector not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if !conn.IsActive() {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "connector not found", nil)
		return
	}
	if conn.Auth == nil || conn.Auth.Type != "oauth2" {
		writeError(w, http.StatusBadRequest, "CONNECTOR_NOT_OAUTH",
			"connector does not declare an oauth2 auth block", nil)
		return
	}
	if conn.Auth.AuthorizationEndpoint == "" || conn.Auth.TokenEndpoint == "" {
		writeError(w, http.StatusBadRequest, "CONNECTOR_OAUTH_INCOMPLETE",
			"connector oauth2 auth block is missing the authorization or token endpoint", nil)
		return
	}

	// §9.3: generate the PKCE verifier/challenge. PKCE is mandatory for
	// public clients and applied for confidential clients too.
	pkce, err := connectoroauth.GeneratePKCE()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"could not generate the PKCE challenge", nil)
		return
	}
	state, err := r.connectorOAuth.StateSigner.Mint()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"could not mint the OAuth state", nil)
		return
	}

	authURL, err := connectoroauth.BuildAuthorizationURL(connectoroauth.AuthorizationRequest{
		AuthorizationEndpoint: conn.Auth.AuthorizationEndpoint,
		ClientID:              conn.Auth.ClientID,
		RedirectURI:           r.connectorOAuth.CallbackURL,
		Scopes:                conn.Auth.Scopes,
		State:                 state,
		CodeChallenge:         pkce.Challenge,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "CONNECTOR_OAUTH_INCOMPLETE", err.Error(), nil)
		return
	}

	ttl := r.connectorOAuth.StateTTL
	if ttl <= 0 {
		ttl = connectoroauth.DefaultStateTTL
	}
	// §4.3 line 202 environment scoping. The caller names the active
	// environment via the `environment` query parameter; an empty value
	// stores the credential under the no-environment scope (the default
	// access path). The credential row's environment column is bound to
	// the flow context here and re-read on the callback so the
	// (tenant, connector, user, environment) four-tuple is consistent
	// across the authorize/callback boundary.
	environment := strings.TrimSpace(req.URL.Query().Get("environment"))
	flow := connectoroauth.FlowContext{
		ConnectorID:  conn.ID,
		TenantID:     principal.TenantID,
		UserID:       principal.Subject,
		Environment:  environment,
		SessionID:    principal.SessionID,
		CodeVerifier: pkce.Verifier,
		RedirectURI:  r.connectorOAuth.CallbackURL,
		Scopes:       conn.Auth.Scopes,
		CreatedAt:    r.clock(),
		// spec: §9.3 line 140 — audit logging is the prescribed
		// forensic surface. Record the authorize-time client IP / UA
		// so the callback-time pair lets an operator investigate a
		// state replay or session hijack. F-9.3.11.
		InitiatorIP: connectorRequestPeerIP(req),
		InitiatorUA: req.UserAgent(),
	}
	if err := r.connectorOAuth.StateStore.Put(state, flow, ttl); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"could not persist the OAuth flow state", nil)
		return
	}
	// spec: §16.7 / §9.3 line 144-164 — emit through the typed catalog
	// so audit-sink validators recognize the event type. Capture the
	// initiator IP and user agent (F-9.3.11) so the callback's
	// `completed_ip` / `completed_user_agent` can be cross-checked for
	// state replay or hijack between authorize and callback.
	r.emit(req.Context(), principal, audit.EventConnectorOAuthAuthorizationInitiated.String(), conn.ID, map[string]any{
		"connectorId":          conn.ID,
		"initiated_ip":         flow.InitiatorIP,
		"initiated_user_agent": flow.InitiatorUA,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(AuthorizeConnectorResponse{
		AuthorizationURL: authURL,
		ConnectorID:      conn.ID,
		State:            state,
	})
}

// handleConnectorOAuthCallback implements the §9.3 OAuth callback
// endpoint: GET /v1/admin/connectors/oauth/callback.
//
// The provider redirects the user's browser here with `code` and
// `state`. The handler verifies the state signature, consumes the
// stored flow context (rejecting an unknown, expired, or already-used
// state), exchanges the code and the PKCE code_verifier at the
// provider's token endpoint, and stores the resulting access and
// refresh tokens in the connector-credential store for the flow's
// (tenant, connector, user) triple.
//
// This endpoint carries no Bearer token — it is a browser redirect.
// The signed `state` is the §9.3 anti-CSRF control: a callback whose
// state was not minted by this gateway is rejected before any code
// exchange, so a forged callback cannot drive a token exchange.
func (r *Router) handleConnectorOAuthCallback(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	state := q.Get("state")
	code := q.Get("code")

	// A provider may redirect with an `error` instead of a `code` when
	// the user denied consent. Surface it without touching the store.
	if oauthErr := q.Get("error"); oauthErr != "" {
		writeError(w, http.StatusBadRequest, "CONNECTOR_OAUTH_DENIED",
			"the OAuth provider returned an error: "+oauthErr, nil)
		return
	}
	if state == "" || code == "" {
		writeError(w, http.StatusBadRequest, "CONNECTOR_OAUTH_INVALID_CALLBACK",
			"the OAuth callback is missing the code or state parameter", nil)
		return
	}

	// §9.3 anti-CSRF: the state signature must verify before anything
	// touches the store or the provider.
	if err := r.connectorOAuth.StateSigner.Verify(state); err != nil {
		writeError(w, http.StatusBadRequest, "CONNECTOR_OAUTH_STATE_INVALID",
			"the OAuth callback state failed signature verification", nil)
		return
	}

	// Consume the flow context. A single-use consume rejects an
	// unknown, expired, or replayed state.
	flow, err := r.connectorOAuth.StateStore.Consume(state, r.clock())
	if err != nil {
		switch {
		case errors.Is(err, connectoroauth.ErrStateExpired):
			writeError(w, http.StatusBadRequest, "CONNECTOR_OAUTH_STATE_EXPIRED",
				"the OAuth flow state has expired", nil)
		case errors.Is(err, connectoroauth.ErrStateConsumed):
			writeError(w, http.StatusBadRequest, "CONNECTOR_OAUTH_STATE_REPLAYED",
				"the OAuth flow state was already used", nil)
		default:
			writeError(w, http.StatusBadRequest, "CONNECTOR_OAUTH_STATE_UNKNOWN",
				"the OAuth flow state is unknown", nil)
		}
		return
	}

	// spec: §4.2 line 173 — the connector belongs to the tenant that
	// initiated the OAuth flow; resolve under that tenant context.
	conn, err := r.connectors.Get(req.Context(), flow.TenantID, flow.ConnectorID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "CONNECTOR_OAUTH_INVALID_CALLBACK",
			"the connector for this OAuth flow no longer exists", nil)
		return
	}
	if conn.Auth == nil || conn.Auth.TokenEndpoint == "" {
		writeError(w, http.StatusBadRequest, "CONNECTOR_OAUTH_INCOMPLETE",
			"the connector oauth2 auth block is missing the token endpoint", nil)
		return
	}

	// Resolve the confidential-client secret. A connector with no
	// clientSecretRef is a §9.3 public client: PKCE alone authenticates
	// the token exchange.
	clientSecret := ""
	if conn.Auth.ClientSecretRef != "" {
		if r.connectorOAuth.ClientSecrets == nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"connector is a confidential client but no client-secret resolver is wired", nil)
			return
		}
		clientSecret, err = r.connectorOAuth.ClientSecrets.Resolve(req.Context(), conn.Auth.ClientSecretRef)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"could not resolve the connector client secret", nil)
			return
		}
	}

	tok, err := connectoroauth.ExchangeCode(req.Context(), r.connectorHTTPClient(), connectoroauth.TokenExchangeRequest{
		TokenEndpoint: conn.Auth.TokenEndpoint,
		ClientID:      conn.Auth.ClientID,
		ClientSecret:  clientSecret,
		Code:          code,
		CodeVerifier:  flow.CodeVerifier,
		RedirectURI:   flow.RedirectURI,
	})
	if err != nil {
		// A token-exchange failure is an upstream provider failure, not
		// a caller error. The provider's error body stays in the gateway
		// log; the client sees a generic 502.
		if errors.Is(err, connectoroauth.ErrNoAccessToken) {
			writeError(w, http.StatusBadGateway, "CONNECTOR_OAUTH_EXCHANGE_FAILED",
				"the OAuth provider returned no access token", nil)
			return
		}
		writeError(w, http.StatusBadGateway, "CONNECTOR_OAUTH_EXCHANGE_FAILED",
			"the OAuth token exchange with the provider failed", nil)
		return
	}

	now := r.clock()
	scopes := conn.Auth.Scopes
	if tok.Scope != "" {
		scopes = splitScope(tok.Scope)
	}
	cred := connectorcredstore.ConnectorCredential{
		TenantID:     flow.TenantID,
		ConnectorID:  flow.ConnectorID,
		UserID:       flow.UserID,
		Environment:  flow.Environment,
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		TokenType:    tok.TokenType,
		Scopes:       scopes,
		ExpiresAt:    tok.ExpiresAt(now),
		CreatedAt:    now,
	}
	if err := r.connectorOAuth.Credentials.Put(req.Context(), cred); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"could not store the connector credential", nil)
		return
	}

	// Audit the completed flow against the flow's principal. The token
	// material is never in the audit detail per §9.3. The
	// initiated/completed IP and user agent pair lets an operator
	// investigate a state replay or session hijack between authorize
	// and callback — the callback carries no Bearer token and the
	// `state` parameter is the only server-side binding. F-9.3.11.
	if r.audit != nil {
		r.audit.EmitAdminEvent(req.Context(), AuditEvent{
			Type:           audit.EventConnectorOAuthCredentialStored.String(),
			ActorSubject:   flow.UserID,
			ActorTenantID:  flow.TenantID,
			TargetResource: flow.ConnectorID,
			Detail: map[string]any{
				"connectorId":          flow.ConnectorID,
				"hasRefreshToken":      tok.RefreshToken != "",
				"initiated_ip":         flow.InitiatorIP,
				"initiated_user_agent": flow.InitiatorUA,
				"completed_ip":         connectorRequestPeerIP(req),
				"completed_user_agent": req.UserAgent(),
			},
			At: now,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":      "connector_authorized",
		"connectorId": flow.ConnectorID,
	})
}

// connectorHTTPClient returns the HTTP client for the token exchange,
// defaulting to a 15-second-timeout client when none is wired.
func (r *Router) connectorHTTPClient() connectoroauth.HTTPDoer {
	if r.connectorOAuth.HTTPClient != nil {
		return r.connectorOAuth.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// connectorRequestPeerIP extracts the connector OAuth caller's IP for
// the audit trail. It honours `X-Forwarded-For` (first-hop client) when
// set by a trusted proxy and falls back to `RemoteAddr`. The string is
// recorded verbatim in the audit detail — `audit.EmitAdminEvent` does
// not attribute trust to the value beyond "what the gateway observed at
// authorize / callback time" so an operator investigating a state
// replay can compare the pair.
//
// spec: §9.3 line 140 — audit logging is the prescribed forensic
// surface. F-9.3.11.
func connectorRequestPeerIP(req *http.Request) string {
	if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}

// splitScope splits an RFC 6749 space-delimited scope string into a
// slice, dropping empty fields.
func splitScope(s string) []string {
	return strings.Fields(s)
}
