// SPDX-License-Identifier: MIT

// Package playground implements the §27 web playground: a browser UI
// served by the gateway at /playground plus the gateway endpoints
// that back it.
//
// The playground is a client of the public MCP and REST surface for
// session, chat, and discovery traffic (§27.5). The only
// playground-specific endpoints are the auth gatekeepers in §27.3.1:
//
//   - GET  /playground/auth/login    — start the OIDC authorization-code flow
//   - GET  /playground/auth/callback — OIDC provider redirect target
//   - POST /playground/auth/logout   — clear the playground session
//   - GET  /playground/auth/error    — render an OIDC failure to the user
//   - POST /v1/playground/token      — mode-polymorphic session-JWT mint
//
// plus the embedded single-page-app bundle served from /playground/.
//
// Gating. The playground is feature-flag gated (§27.2). When the
// gateway is built without playground.enabled the package is not
// mounted and /playground/* returns 404. The gateway reads the §27.2
// Helm values from its own configuration; see Config below for the
// field-to-value mapping.
package playground

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// AuthMode is the §27.2 playground.authMode enum. It selects how a
// browser request to /playground/* is admitted before the gateway
// mints a session-capability JWT.
type AuthMode string

const (
	// AuthModeOIDC redirects unauthenticated users to the configured
	// OIDC provider and exchanges the resulting browser session for
	// MCP bearer tokens (§27.3.1).
	AuthModeOIDC AuthMode = "oidc"

	// AuthModeAPIKey renders a bearer-token paste form; the pasted
	// token is a standard gateway user_bearer credential (§27.3).
	AuthModeAPIKey AuthMode = "apiKey"

	// AuthModeDev disables auth and synthesizes a dev principal. It is
	// permitted only when global.devMode is true (§27.3).
	AuthModeDev AuthMode = "dev"
)

// IsValid reports whether m is one of the three §27.2 auth modes.
func (m AuthMode) IsValid() bool {
	return m == AuthModeOIDC || m == AuthModeAPIKey || m == AuthModeDev
}

// tenantIDPattern is the canonical §10.2 tenant_id format. The
// playground.devTenantId value is validated against it at startup
// (§27.2 layer 3).
var tenantIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

// Bound constants from §27.2.
const (
	// minBearerTTL and maxBearerTTL bound playground.bearerTtlSeconds.
	minBearerTTL = 60 * time.Second
	maxBearerTTL = 3600 * time.Second

	// minIdleTime bounds the lower end of playground.maxIdleTimeSeconds;
	// the upper bound is the runtime's own maxIdleTimeSeconds, applied
	// per-session in §27.6.
	minIdleTime = 60 * time.Second

	// PlaygroundOrigin is the value of the §27.3 origin claim stamped
	// on every session-capability JWT minted for a /playground/*
	// request. Downstream enforcement (§27.6 idle-timeout override and
	// duration cap, §27.8 metrics) keys on this claim, not authMode.
	PlaygroundOrigin = "playground"

	// DevUserPrincipal is the synthetic subject §27.3 binds dev-mode
	// playground tokens to.
	DevUserPrincipal = "dev-user"
)

// Config carries the §27.2 playground configuration the gateway reads
// from its Helm values / flags and hands to New. The gateway owns the
// flag parsing; this struct is the validated in-process form.
type Config struct {
	// Enabled mirrors playground.enabled. When false the gateway does
	// not mount the package at all; New is not called.
	Enabled bool

	// AuthMode mirrors playground.authMode.
	AuthMode AuthMode

	// DevTenantID mirrors playground.devTenantId. Bound to the dev
	// HMAC JWT tenant_id claim when AuthMode is dev.
	DevTenantID string

	// AllowedRuntimes mirrors playground.allowedRuntimes: a glob list
	// of runtime IDs visible in the runtime picker. An empty list is
	// treated as ["*"] (every runtime visible).
	AllowedRuntimes []string

	// MaxSessionMinutes mirrors playground.maxSessionMinutes: the hard
	// cap on playground-initiated session duration (§27.6).
	MaxSessionMinutes int

	// MaxIdleTimeSeconds mirrors playground.maxIdleTimeSeconds: the
	// hard idle-timeout override for playground sessions (§27.6).
	MaxIdleTimeSeconds int

	// OIDCSessionTTL mirrors playground.oidcSessionTtlSeconds: the
	// lifetime of the server-side session record and the
	// lenny_playground_session cookie.
	OIDCSessionTTL time.Duration

	// BearerTTL mirrors playground.bearerTtlSeconds: the TTL of MCP
	// bearer tokens minted by POST /v1/playground/token.
	BearerTTL time.Duration

	// MultiTenant mirrors auth.multiTenant. When true and AuthMode is
	// dev, DevTenantID must be set explicitly (§27.2 layer 3).
	MultiTenant bool

	// GatewayHost is the public host the playground UI connects to
	// over the MCP WebSocket. It is interpolated into the §27.7
	// connect-src CSP directive.
	GatewayHost string

	// SessionLabels mirrors playground.sessionLabels (§27.2 line 41):
	// the operator-tunable label map stamped on every playground
	// session record and every playground audit event for audit /
	// accounting consumers. The map defaults to {"origin":
	// "playground"} when empty; an operator may add labels but the
	// §27.3 mode-agnostic origin=playground guarantee remains
	// load-bearing, so EffectiveLabels always re-stamps origin.
	SessionLabels map[string]string
}

// withDefaults returns a copy of c with the §27.2 default values
// applied to any zero field.
func (c Config) withDefaults() Config {
	if c.AuthMode == "" {
		c.AuthMode = AuthModeOIDC
	}
	if c.DevTenantID == "" {
		c.DevTenantID = "default"
	}
	if len(c.AllowedRuntimes) == 0 {
		c.AllowedRuntimes = []string{"*"}
	}
	if c.MaxSessionMinutes == 0 {
		c.MaxSessionMinutes = 30
	}
	if c.MaxIdleTimeSeconds == 0 {
		c.MaxIdleTimeSeconds = 300
	}
	if c.OIDCSessionTTL == 0 {
		c.OIDCSessionTTL = 3600 * time.Second
	}
	if c.BearerTTL == 0 {
		c.BearerTTL = 900 * time.Second
	}
	c.SessionLabels = c.EffectiveLabels()
	return c
}

// EffectiveLabels returns the §27.2 line 41 sessionLabels map with the
// §27.3 mode-agnostic origin=playground guarantee enforced. The
// returned map is a copy: callers may mutate it without affecting the
// stored Config. A nil or empty SessionLabels yields {"origin":
// "playground"}; an operator-supplied map is preserved with the
// origin entry re-stamped to the canonical value (operators cannot
// silence the load-bearing label).
func (c Config) EffectiveLabels() map[string]string {
	out := make(map[string]string, len(c.SessionLabels)+1)
	for k, v := range c.SessionLabels {
		out[k] = v
	}
	out["origin"] = PlaygroundOrigin
	return out
}

// Validate enforces the §27.2 layer-3 startup gate: the gateway
// refuses to start (the caller treats a non-nil error as fatal) when
// the configuration is malformed. The returned error string carries
// the canonical §27.2 fatal code as its prefix so the operator sees
// LENNY_PLAYGROUND_DEV_TENANT_INVALID / _REQUIRED in the crash log.
//
// Validate covers format and cross-field violations only.
// Tenant-existence is Ready-gated per-request at /playground/* and is
// not a startup gate (§27.2 layer 4), so a well-formed devTenantId
// that is not yet seeded passes Validate.
func (c Config) Validate() error {
	if !c.AuthMode.IsValid() {
		return fmt.Errorf("LENNY_PLAYGROUND_CONFIG_INVALID: playground.authMode %q is not one of oidc, apiKey, dev", c.AuthMode)
	}
	if c.BearerTTL < minBearerTTL || c.BearerTTL > maxBearerTTL {
		return fmt.Errorf("LENNY_PLAYGROUND_CONFIG_INVALID: playground.bearerTtlSeconds %s is outside the bound 60s..3600s", c.BearerTTL)
	}
	if time.Duration(c.MaxIdleTimeSeconds)*time.Second < minIdleTime {
		return fmt.Errorf("LENNY_PLAYGROUND_CONFIG_INVALID: playground.maxIdleTimeSeconds %d is below the lower bound of 60", c.MaxIdleTimeSeconds)
	}
	if c.AuthMode == AuthModeDev {
		if strings.TrimSpace(c.DevTenantID) == "" {
			return fmt.Errorf("LENNY_PLAYGROUND_DEV_TENANT_REQUIRED: playground.authMode=dev requires a non-empty playground.devTenantId")
		}
		if !tenantIDPattern.MatchString(c.DevTenantID) {
			return fmt.Errorf("LENNY_PLAYGROUND_DEV_TENANT_INVALID: playground.devTenantId %q fails the tenant_id format ^[a-zA-Z0-9_-]{1,128}$", c.DevTenantID)
		}
		if c.MultiTenant && c.DevTenantID == "default" {
			return fmt.Errorf("LENNY_PLAYGROUND_DEV_TENANT_REQUIRED: playground.authMode=dev with auth.multiTenant=true requires an explicit (non-default) playground.devTenantId")
		}
	}
	return nil
}

// effectiveIdleSeconds returns the §27.6 effective idle cap for a
// playground session running against a runtime whose own
// maxIdleTimeSeconds is runtimeIdleSeconds. The override never
// relaxes a stricter runtime limit; it only tightens a looser one.
// A zero runtimeIdleSeconds (runtime did not declare a limit) yields
// the configured playground cap unchanged.
func (c Config) effectiveIdleSeconds(runtimeIdleSeconds int) int {
	pg := c.MaxIdleTimeSeconds
	if runtimeIdleSeconds > 0 && runtimeIdleSeconds < pg {
		return runtimeIdleSeconds
	}
	return pg
}

// effectiveSessionMinutes returns the §27.6 hard duration cap for a
// playground session running against a runtime whose own
// maxSessionMinutes is runtimeMinutes. The cap is the min of the two;
// a zero runtimeMinutes yields the configured playground cap.
func (c Config) effectiveSessionMinutes(runtimeMinutes int) int {
	pg := c.MaxSessionMinutes
	if runtimeMinutes > 0 && runtimeMinutes < pg {
		return runtimeMinutes
	}
	return pg
}

// TenantRegistry resolves whether a tenant row is present. The
// playground consults it on the §27.2 layer-4 Ready-gate for
// authMode=dev so /playground/* returns 503 until the lenny-bootstrap
// Job seeds the configured tenant. *tenantstore.Memory and the
// Postgres-backed tenant store both satisfy it.
type TenantRegistry interface {
	// IsRegistered reports whether tenantID names a provisioned
	// tenant. The bool is false (no error) when the tenant is absent.
	IsRegistered(tenantID string) (bool, error)
}

// Handler is the §27 playground HTTP surface. It is mounted by the
// gateway under two route families:
//
//   - /playground/...        — the SPA bundle and the auth gatekeepers
//   - /v1/playground/token   — the mode-polymorphic mint endpoint
//
// Routes returns the http.Handler for each family.
type Handler struct {
	cfg      Config
	signer   jwt.Signer
	verifier jwt.Verifier
	tenants  TenantRegistry
	sessions SessionStore
	oidc     OIDCExchanger
	metrics  *Metrics
	audit    AuditEmitter
	now      func() time.Time
}

// Options configures New.
type Options struct {
	// Signer mints the playground session-capability JWTs. In
	// production this is the KMS-backed gateway signer; the dev HMAC
	// signer satisfies the same interface for the no-cloud path.
	Signer jwt.Signer

	// Verifier validates pasted bearer tokens in apiKey mode. When nil
	// and Signer also verifies (the HMAC and KMS signers both do), New
	// uses the signer itself.
	Verifier jwt.Verifier

	// Tenants resolves tenant existence for the §27.2 layer-4
	// Ready-gate. Required in dev mode; ignored in oidc and apiKey
	// mode.
	Tenants TenantRegistry

	// Sessions backs the §27.3.1 playground session record and the
	// revocation marker. A nil store is permitted only in dev and
	// apiKey mode (which carry no server-side session); oidc mode
	// requires it.
	Sessions SessionStore

	// OIDC performs the authorization-code exchange with the
	// configured provider. Required in oidc mode; ignored otherwise.
	OIDC OIDCExchanger

	// Metrics is the §27.8 metric set. A nil Metrics disables metric
	// emission (the handler still serves).
	Metrics *Metrics

	// Now overrides the clock for tests. Production leaves it nil.
	Now func() time.Time
}

// New returns a playground Handler for cfg. cfg is normalized with
// the §27.2 defaults and must already have passed Validate (the
// gateway calls Validate before New and treats a failure as fatal).
func New(cfg Config, opts Options) *Handler {
	cfg = cfg.withDefaults()
	verifier := opts.Verifier
	if verifier == nil {
		if v, ok := opts.Signer.(jwt.Verifier); ok {
			verifier = v
		}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Handler{
		cfg:      cfg,
		signer:   opts.Signer,
		verifier: verifier,
		tenants:  opts.Tenants,
		sessions: opts.Sessions,
		oidc:     opts.OIDC,
		metrics:  opts.Metrics,
		now:      now,
	}
}

// Config returns the normalized configuration the handler runs with.
func (h *Handler) Config() Config { return h.cfg }

// PlaygroundRoutes returns the http.Handler for the /playground/*
// route family: the SPA bundle and the §27.3.1 auth gatekeepers. The
// gateway mounts it at the /playground/ path prefix. Every response
// carries the §27.7 security headers; the CSP is applied here and
// nowhere else in the gateway.
func (h *Handler) PlaygroundRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /playground/auth/login", h.handleLogin)
	mux.HandleFunc("GET /playground/auth/callback", h.handleCallback)
	mux.HandleFunc("POST /playground/auth/logout", h.handleLogout)
	mux.HandleFunc("GET /playground/auth/error", h.handleAuthError)
	mux.HandleFunc("GET /playground/config.json", h.handleConfigJSON)
	// The SPA bundle is the catch-all: every other /playground/ path
	// resolves an embedded asset or falls back to index.html.
	mux.HandleFunc("GET /playground/", h.handleAsset)
	mux.HandleFunc("GET /playground", h.handleAsset)
	return h.securityHeaders(h.readyGate(mux))
}

// TokenRoutes returns the http.Handler for POST /v1/playground/token,
// the §27.3.1 mode-polymorphic bearer-mint endpoint. The gateway
// mounts it at the /v1/playground/token path. It is also subject to
// the §27.2 layer-4 Ready-gate so a dev deployment with an unseeded
// tenant returns 503 here too.
func (h *Handler) TokenRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/playground/token", h.handleToken)
	return h.securityHeaders(h.readyGate(mux))
}

// readyGate is the §27.2 layer-4 per-request Ready-gate. In dev mode
// it consults the tenant registry on every /playground/* request and
// returns 503 LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED with
// Retry-After: 5 until the configured devTenantId is present. It is a
// pass-through in oidc and apiKey mode.
func (h *Handler) readyGate(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.cfg.AuthMode != AuthModeDev {
			inner.ServeHTTP(w, r)
			return
		}
		if h.tenants == nil {
			inner.ServeHTTP(w, r)
			return
		}
		ok, err := h.tenants.IsRegistered(h.cfg.DevTenantID)
		if err == nil && ok {
			inner.ServeHTTP(w, r)
			return
		}
		// A registry error is treated the same as "not seeded": the
		// gate fails closed for the playground routes while leaving the
		// rest of the gateway unaffected (the gateway mounts the
		// non-playground routes outside this handler).
		h.metrics.devTenantNotSeeded()
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, "LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED",
			"the playground dev tenant has not been seeded yet; the lenny-bootstrap Job is still running",
			map[string]any{"devTenantId": h.cfg.DevTenantID})
	})
}
