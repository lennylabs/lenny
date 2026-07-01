// SPDX-License-Identifier: MIT

package opsserver

import (
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/observability/metrics"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// meRequestsTotal is the §25.4 line 1638 counter of calls to /v1/admin/me
// and its sub-endpoints, labelled by the caller type.
var meRequestsTotal *prometheus.CounterVec

func init() {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_ops_me_requests_total",
		Help: "§25.4: calls to /v1/admin/me and its sub-endpoints, labelled " +
			"by caller type.",
	}, []string{"caller_type"})
	if err != nil {
		panic(err)
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, c)
	meRequestsTotal = c
}

// MeConfig carries the §25.4 GET /v1/admin/me platform context and
// install-state capabilities. The values are resolved once at binary
// startup (installation ID, version, tier, namespace, URLs, OIDC issuer)
// except the live capability snapshot, which reflects the actual wired
// state. A nil *MeConfig on Options leaves the platform/capabilities
// blocks at their zero values (the dev / embedded path).
//
// spec: §25.4 lines 1596-1631.
type MeConfig struct {
	InstallationID           string
	Version                  string
	Tier                     string
	Namespace                string
	OpsServiceURL            string
	GatewayURL               string
	Issuer                   string
	TokenRefreshBeforeExpiry string

	// Capabilities is the §25.4 install-state snapshot resolved at
	// startup. OpsReplicas may be overridden per request by LiveOpsReplicas.
	Capabilities Capabilities

	// LiveOpsReplicas, when non-nil, supplies the current replica count
	// for capabilities.opsReplicas, overriding the static snapshot so an
	// agent reads the live scaling indicator (§25.4 line 1604).
	LiveOpsReplicas func() int
}

// Capabilities is the §25.4 line 1602-1612 capabilities block: the
// actual install state, not compiled feature flags.
type Capabilities struct {
	PrometheusAvailable     bool   `json:"prometheusAvailable"`
	BundledRulesLoaded      bool   `json:"bundledRulesLoaded"`
	OpsReplicas             int    `json:"opsReplicas"`
	MtlsInternal            bool   `json:"mtlsInternal"`
	LockMemoryTier          string `json:"lockMemoryTier"`
	TenantFiltering         bool   `json:"tenantFiltering"`
	McpManagementServer     bool   `json:"mcpManagementServer"`
	OpenApiAvailable        bool   `json:"openApiAvailable"`
	HeadlessServiceFallback bool   `json:"headlessServiceFallback"`
}

// registerMeRoutes wires the §25.4 caller-discovery endpoints. They are
// registered unconditionally (the §25.4 canonical entrypoint must exist
// even on the dev path); the platform/capabilities blocks degrade to
// their zero values when no MeConfig is configured. The /me/operations
// alias routes through the Operations Inventory and returns an empty page
// when no inventory is wired.
func (s *Server) registerMeRoutes() {
	s.mux.HandleFunc("GET /v1/admin/me", s.handleMe)
	s.mux.HandleFunc("GET /v1/admin/me/authorized-tools", s.handleAuthorizedTools)
	s.mux.HandleFunc("GET /v1/admin/me/operations", s.handleMeOperations)
}

// meResponse is the §25.4 GET /v1/admin/me body.
type meResponse struct {
	Identity      meIdentity               `json:"identity"`
	Authorization meAuthorization          `json:"authorization"`
	RateLimits    *meRateLimits            `json:"rateLimits,omitempty"`
	Token         meToken                  `json:"token"`
	Platform      mePlatform               `json:"platform"`
	Capabilities  Capabilities             `json:"capabilities"`
	Links         map[string]string        `json:"links"`
	Degradation   *conventions.Degradation `json:"degradation,omitempty"`
}

type meIdentity struct {
	Sub         string `json:"sub"`
	DisplayName string `json:"displayName,omitempty"`
	CallerType  string `json:"callerType,omitempty"`
	Issuer      string `json:"issuer,omitempty"`
}

type meAuthorization struct {
	Roles                []string `json:"roles"`
	TenantScope          string   `json:"tenantScope"`
	Scope                string   `json:"scope"`
	AuthorizedLockScopes []string `json:"authorizedLockScopes"`
	SubjectToGuards      meGuards `json:"subjectToGuards"`
}

type meGuards struct {
	ConfirmRequiredFor             []string `json:"confirmRequiredFor"`
	AcknowledgeDataLossRequiredFor []string `json:"acknowledgeDataLossRequiredFor"`
}

type meRateLimits struct {
	RequestsPerSecond      float64 `json:"requestsPerSecond"`
	Burst                  int     `json:"burst"`
	CurrentTokensAvailable int     `json:"currentTokensAvailable"`
}

type meToken struct {
	RefreshBeforeExpiry string `json:"refreshBeforeExpiry,omitempty"`
	RefreshEndpoint     string `json:"refreshEndpoint,omitempty"`
}

type mePlatform struct {
	InstallationID string `json:"installationId,omitempty"`
	Version        string `json:"version"`
	Tier           string `json:"tier,omitempty"`
	Namespace      string `json:"namespace,omitempty"`
	OpsServiceURL  string `json:"opsServiceURL,omitempty"`
	GatewayURL     string `json:"gatewayURL,omitempty"`
}

// handleMe serves GET /v1/admin/me: the §25.4 single-call discovery
// endpoint. Any authenticated caller may read it; it returns only the
// calling identity's context. On the first call per token it emits the
// identity.discovered audit event.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	p, hasPrincipal := callerPrincipal(r)
	meRequestsTotal.WithLabelValues(callerTypeLabel(p, hasPrincipal)).Inc()

	resp := meResponse{
		Identity:      s.meIdentity(p, r),
		Authorization: s.meAuthorization(p, hasPrincipal),
		Token:         s.meToken(),
		Platform:      s.mePlatform(),
		Capabilities:  s.meCapabilities(),
		Links:         s.meLinks(p),
	}
	if rl := s.meRateLimits(p, hasPrincipal); rl != nil {
		resp.RateLimits = rl
	}

	// §25.4 line 1641: identity.discovered on first call per token,
	// deduplicated by sub (a precise (sub, token_iat) key requires the iat
	// claim the principal does not surface in v1).
	if hasPrincipal && p.Subject != "" {
		if _, seen := s.identitySeen.LoadOrStore(p.Subject, struct{}{}); !seen {
			s.recordOpsAudit(r, "identity.discovered", map[string]any{
				"roles":  rolesToStrings(p.Roles),
				"scopes": p.Scopes.Raw,
			})
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) meIdentity(p authmw.Principal, r *http.Request) meIdentity {
	display := callerAgentName(r)
	if display == "" {
		display = p.Subject
	}
	callerType := p.CallerType
	if callerType == "" {
		callerType = "agent"
	}
	return meIdentity{
		Sub:         p.Subject,
		DisplayName: display,
		CallerType:  callerType,
		Issuer:      s.meIssuer(),
	}
}

func (s *Server) meAuthorization(p authmw.Principal, hasPrincipal bool) meAuthorization {
	roles := rolesToStrings(p.Roles)
	tenantScope := "*"
	if hasPrincipal && !p.HasRole(auth.RolePlatformAdmin) {
		tenantScope = p.TenantID
	}
	scope := p.Scopes.Raw
	if scope == "" {
		// §25.4 line 1592: an absent scope claim echoes as tools:* (no
		// restriction beyond role).
		scope = "tools:*"
	}
	return meAuthorization{
		Roles:                roles,
		TenantScope:          tenantScope,
		Scope:                scope,
		AuthorizedLockScopes: authorizedLockScopes(p, hasPrincipal),
		SubjectToGuards:      subjectToGuards(),
	}
}

// authorizedLockScopes returns the §25.4 line 1583-1589 lock scopes the
// caller's role authorizes. Platform-admin gets the full platform set;
// tenant-admin gets tenant-scoped expansions rather than wildcards
// (§25.4 line 1647). The dev path (no principal) returns the
// platform-admin set.
func authorizedLockScopes(p authmw.Principal, hasPrincipal bool) []string {
	if hasPrincipal && !p.HasRole(auth.RolePlatformAdmin) {
		return []string{
			"session:*",
			"tenant:" + p.TenantID + ":*",
		}
	}
	return []string{
		"pool:*", "credential-pool:*", "session:*",
		"tenant:*:*", "platform:*", "upgrade:platform",
		"restore:platform", "config:global",
	}
}

// subjectToGuards returns the §25.4 line 1593-1595 conditional-requirement
// rules surfaced from the tool x-lenny-guards extensions so an agent
// learns upfront which operations require confirm / acknowledgeDataLoss.
func subjectToGuards() meGuards {
	return meGuards{
		ConfirmRequiredFor: []string{
			"pool-scale-over-1.5x", "full-backup", "restore", "upgrade-start",
		},
		AcknowledgeDataLossRequiredFor: []string{
			"restore-with-recent-writes",
		},
	}
}

func (s *Server) meRateLimits(p authmw.Principal, hasPrincipal bool) *meRateLimits {
	if s.rateLimiter == nil || !hasPrincipal || p.Subject == "" {
		return nil
	}
	tokens, rps, burst := s.rateLimiter.Snapshot(p.Subject)
	return &meRateLimits{
		RequestsPerSecond:      rps,
		Burst:                  burst,
		CurrentTokensAvailable: int(math.Floor(tokens)),
	}
}

func (s *Server) meToken() meToken {
	if s.me == nil {
		return meToken{}
	}
	return meToken{
		RefreshBeforeExpiry: s.me.TokenRefreshBeforeExpiry,
		RefreshEndpoint:     s.me.Issuer,
	}
}

func (s *Server) mePlatform() mePlatform {
	if s.me == nil {
		return mePlatform{}
	}
	return mePlatform{
		InstallationID: s.me.InstallationID,
		Version:        s.me.Version,
		Tier:           s.me.Tier,
		Namespace:      s.me.Namespace,
		OpsServiceURL:  s.me.OpsServiceURL,
		GatewayURL:     s.me.GatewayURL,
	}
}

func (s *Server) meCapabilities() Capabilities {
	if s.me == nil {
		// Even without a MeConfig the in-process facts are known.
		return Capabilities{McpManagementServer: s.mcp != nil}
	}
	caps := s.me.Capabilities
	caps.McpManagementServer = s.mcp != nil
	if s.me.LiveOpsReplicas != nil {
		caps.OpsReplicas = s.me.LiveOpsReplicas()
	}
	return caps
}

func (s *Server) meIssuer() string {
	if s.me == nil {
		return ""
	}
	return s.me.Issuer
}

// meLinks returns the §25.4 line 1616-1623 discovery hop-off block.
//
// spec: §25.4 (`/me` example, links.openApi/platformHealth/myRecentAudit)
// — `authorizedTools` and `myOperations` are `lenny-ops`'s own routes and
// stay relative (they resolve against lenny-ops's own origin). The other
// three target gateway-hosted resources: the §25.9 audit-events query and
// the §15.1 platform-health summary and OpenAPI document all live on the
// gateway, so a relative link 404s against lenny-ops's origin. They are
// joined to the gateway base URL the agent already receives in
// `platform.gatewayURL`, so the discovery hop resolves. When `GatewayURL`
// is unset (the dev / embedded path with no separate gateway), the join
// falls back to the relative path rather than emitting a bare host. F-COV-1.
func (s *Server) meLinks(p authmw.Principal) map[string]string {
	links := map[string]string{
		"authorizedTools": "/v1/admin/me/authorized-tools",
		"myOperations":    "/v1/admin/me/operations",
		"platformHealth":  s.gatewayLink("/v1/admin/health/summary"),
		"openApi":         s.gatewayLink("/v1/openapi.json"),
	}
	if p.Subject != "" {
		links["myRecentAudit"] = s.gatewayLink("/v1/admin/audit-events?actorId=" + p.Subject + "&limit=50")
	}
	return links
}

// gatewayLink joins a gateway-resident relative path to the configured
// gateway base URL so a `/me` discovery hop resolves against the gateway
// rather than 404ing against lenny-ops's own origin. It falls back to the
// relative path when no gateway URL is configured (the dev / embedded
// path), so the link is never a bare host with no path.
//
// spec: §25.4 (`/me` links resolve to the gateway-hosted resource).
func (s *Server) gatewayLink(path string) string {
	if s.me == nil || s.me.GatewayURL == "" {
		return path
	}
	return strings.TrimRight(s.me.GatewayURL, "/") + path
}

// authorizedToolEntry is one §25.4 /v1/admin/me/authorized-tools row.
type authorizedToolEntry struct {
	Tool        string `json:"tool"`
	Scope       string `json:"scope,omitempty"`
	Category    string `json:"category,omitempty"`
	Description string `json:"description,omitempty"`
	// RequiresConfirm annotates a tool whose §25.12 dry-run/confirm guard
	// applies, so an agent knows upfront a confirm:true is needed.
	RequiresConfirm bool `json:"requiresConfirm,omitempty"`
}

// handleAuthorizedTools serves GET /v1/admin/me/authorized-tools: the MCP
// management tool inventory pre-filtered to the tools the caller's roles
// and scope claim authorize. When the MCP management server is unwired the
// endpoint returns 503 AUTHORIZED_TOOLS_UNAVAILABLE with the OpenAPI
// fallback hint (§25.4 line 1668).
func (s *Server) handleAuthorizedTools(w http.ResponseWriter, r *http.Request) {
	p, hasPrincipal := callerPrincipal(r)
	meRequestsTotal.WithLabelValues(callerTypeLabel(p, hasPrincipal)).Inc()
	if s.mcp == nil {
		// spec: §25.4 line 1668 — the fallback hint points at the
		// gateway-hosted OpenAPI document; join the gateway base URL so an
		// agent that reads the hint reaches the gateway rather than a route
		// lenny-ops does not serve. F-COV-1.
		conventions.WriteError(w, http.StatusServiceUnavailable, "AUTHORIZED_TOOLS_UNAVAILABLE",
			conventions.CategoryTransient,
			"MCP Management Server unreachable; derive the tool surface from the OpenAPI document at "+
				s.gatewayLink("/v1/openapi.json")+" plus the authorization block from /v1/admin/me")
		return
	}
	tools := make([]authorizedToolEntry, 0)
	for _, t := range s.mcp.Registry().All() {
		if !callerHasToolRole(p, hasPrincipal, t.RequiredRole) {
			continue
		}
		if hasPrincipal && t.Scope != "" && !p.HasScope(t.Scope) {
			continue
		}
		tools = append(tools, authorizedToolEntry{
			Tool:            t.Name,
			Scope:           t.Scope,
			Category:        t.Category,
			Description:     t.Description,
			RequiresConfirm: t.DryRunSupport == mcpConfirmBool,
		})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Tool < tools[j].Tool })
	writeJSON(w, http.StatusOK, map[string]any{"tools": tools})
}

// mcpConfirmBool mirrors mcp.DryRunConfirmBool without importing the mcp
// package for a single constant in the hot path.
const mcpConfirmBool = "confirm-bool"

// handleMeOperations serves GET /v1/admin/me/operations: the §25.4 alias
// for GET /v1/admin/operations?actor=me&status=in_progress,paused,held,-
// awaiting_flush. The caller's own in-flight operations, scoped through
// the shared inventory authorization.
func (s *Server) handleMeOperations(w http.ResponseWriter, r *http.Request) {
	meRequestsTotal.WithLabelValues(callerTypeLabel(callerPrincipal(r))).Inc()
	if s.inventory == nil {
		writeJSON(w, http.StatusOK, emptyOperationsPage())
		return
	}
	s.listOperations(w, r, listOptions{
		actor: "me",
		limit: conventions.DefaultPageLimit,
	})
}

// callerTypeLabel resolves the §25.4 caller_type metric label. An
// unauthenticated dev request reports "unknown"; an authenticated caller
// reports its caller_type claim (defaulting to "agent").
func callerTypeLabel(p authmw.Principal, hasPrincipal bool) string {
	if !hasPrincipal {
		return "unknown"
	}
	if p.CallerType == "" {
		return "agent"
	}
	return p.CallerType
}

// callerHasToolRole reports whether the caller's roles satisfy a tool's
// §25.12 x-lenny-required-role. The dev path (no principal) admits every
// tool.
func callerHasToolRole(p authmw.Principal, hasPrincipal bool, required string) bool {
	if !hasPrincipal {
		return true
	}
	switch required {
	case "", string(auth.RoleTenantAdmin):
		return p.HasRole(auth.RolePlatformAdmin) || p.HasRole(auth.RoleTenantAdmin)
	default:
		return p.HasRole(auth.Role(required))
	}
}

func rolesToStrings(roles []auth.Role) []string {
	out := make([]string, 0, len(roles))
	for _, r := range roles {
		out = append(out, string(r))
	}
	return out
}
