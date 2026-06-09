// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// §27.3.1 tenant-claim rejection codes. These are the redirect query
// parameters the OIDC callback handler translates a §10.2
// tenant-claim rejection into; the playground error page surfaces
// them to the user. The namespace is reserved for this table.
const (
	errTenantClaimMissing       = "tenant_claim_missing"
	errTenantNotFound           = "tenant_not_found"
	errTenantClaimInvalidFormat = "tenant_claim_invalid_format"
)

// Cookie names from §27.3.
const (
	// sessionCookie is the §27.3 opaque playground session cookie. Its
	// Path is exactly /playground/ (trailing slash load-bearing) so it
	// excludes sibling paths such as /playground-admin.
	sessionCookie = "lenny_playground_session"

	// oidcStateCookie is the §27.3.1 short-lived signed cookie that
	// carries the per-login state and PKCE verifier. Its Path is
	// /playground/auth/ so it is scoped to the OIDC handlers only.
	oidcStateCookie = "lenny_playground_oidc_state"

	// sessionCookiePath and statePath are the exact cookie paths.
	sessionCookiePath = "/playground/"
	statePath         = "/playground/auth/"

	// stateCookieTTL is the §27.3.1 lifetime of the OIDC state cookie.
	stateCookieTTL = 10 * time.Minute
)

// stateCookieValue carries the per-login OIDC flow context. It is
// serialized into the signed, HttpOnly state cookie so the callback
// can recover the PKCE verifier without a server-side store. The
// gateway signs the cookie value; an unsigned or tampered cookie is
// rejected at the callback.
type stateCookieValue struct {
	State    string `json:"state"`
	Verifier string `json:"verifier"`
	IssuedAt int64  `json:"issued_at"`
}

// handleLogin serves GET /playground/auth/login: it starts the OIDC
// authorization-code flow (§27.3.1 step 1). It is reachable only in
// oidc mode; the other modes have no login endpoint.
func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if h.cfg.AuthMode != AuthModeOIDC {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
			"the playground login endpoint exists only in authMode=oidc", nil)
		return
	}
	if h.oidc == nil {
		writeError(w, http.StatusServiceUnavailable, "LENNY_PLAYGROUND_OIDC_UNAVAILABLE",
			"the playground OIDC provider is not configured", nil)
		return
	}
	state, err := newOpaqueID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	verifier, err := generateCodeVerifier()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	challenge := codeChallengeS256(verifier)
	cv := stateCookieValue{State: state, Verifier: verifier, IssuedAt: h.now().Unix()}
	signed, err := h.sealState(cv)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookie,
		Value:    signed,
		Path:     statePath,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(stateCookieTTL.Seconds()),
	})
	http.Redirect(w, r, h.oidc.AuthorizationURL(state, challenge, h.callbackURL(r)), http.StatusFound)
}

// handleCallback serves GET /playground/auth/callback: the OIDC
// provider redirects here (§27.3.1 step 1). It verifies state,
// performs the PKCE-protected token exchange, establishes the
// server-side playground session record, and sets the session
// cookie. Every failure clears the state cookie and redirects to the
// playground error page.
func (h *Handler) handleCallback(w http.ResponseWriter, r *http.Request) {
	if h.cfg.AuthMode != AuthModeOIDC {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
			"the playground callback endpoint exists only in authMode=oidc", nil)
		return
	}
	clearCookie(w, oidcStateCookie, statePath)
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		h.redirectAuthError(w, r, "oidc_callback_invalid")
		return
	}
	stateCookie, err := r.Cookie(oidcStateCookie)
	if err != nil {
		h.redirectAuthError(w, r, "oidc_state_missing")
		return
	}
	cv, err := h.openState(stateCookie.Value)
	if err != nil {
		h.redirectAuthError(w, r, "oidc_state_invalid")
		return
	}
	if cv.State != state {
		h.redirectAuthError(w, r, "oidc_state_mismatch")
		return
	}
	if h.now().Unix()-cv.IssuedAt > int64(stateCookieTTL.Seconds()) {
		h.redirectAuthError(w, r, "oidc_state_expired")
		return
	}
	if h.oidc == nil || h.sessions == nil {
		writeError(w, http.StatusServiceUnavailable, "LENNY_PLAYGROUND_OIDC_UNAVAILABLE",
			"the playground OIDC provider or session store is not configured", nil)
		return
	}
	subject, err := h.oidc.Exchange(r.Context(), code, cv.Verifier, h.callbackURL(r))
	if err != nil {
		// A §27.3.1 tenant-claim rejection redirects with the canonical
		// code; any other exchange failure uses the generic code.
		if oe, ok := errIsTenantClaim(err); ok {
			h.redirectAuthError(w, r, oe.Code)
			return
		}
		if oe, ok := asOIDCError(err); ok {
			h.redirectAuthError(w, r, oe.Code)
			return
		}
		h.redirectAuthError(w, r, "oidc_exchange_failed")
		return
	}
	// §27.3.1: the extracted tenant must name a provisioned Tenant CR.
	// TENANT_NOT_FOUND is surfaced as a tenant-claim error redirect.
	if h.tenants != nil {
		ok, regErr := h.tenants.IsRegistered(subject.TenantID)
		if regErr != nil {
			h.redirectAuthError(w, r, "oidc_tenant_lookup_failed")
			return
		}
		if !ok {
			h.redirectAuthError(w, r, errTenantNotFound)
			return
		}
	}
	if err := h.establishSession(r.Context(), w, subject); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	http.Redirect(w, r, sessionCookiePath, http.StatusFound)
}

// establishSession creates the §27.3.1 server-side playground session
// record, pins its TTL to the cookie lifetime, and sets the
// lenny_playground_session cookie.
func (h *Handler) establishSession(ctx context.Context, w http.ResponseWriter, subject OIDCSubject) error {
	sessionID, err := newOpaqueID()
	if err != nil {
		return err
	}
	csrf, err := newOpaqueID()
	if err != nil {
		return err
	}
	rec := SessionRecord{
		UserID:     subject.UserID,
		TenantID:   subject.TenantID,
		CallerType: subject.CallerType,
		Scope:      subject.Scope,
		Origin:     PlaygroundOrigin,
		Labels:     h.cfg.EffectiveLabels(),
		IssuedAt:   h.now(),
		// §27.6 line 201: seed the idle clock at creation so a record that
		// never mints a bearer is still reclaimable once the idle window
		// elapses.
		LastActivityAt: h.now(),
		CSRFToken:      csrf,
	}
	if err := h.sessions.PutSession(ctx, subject.TenantID, sessionID, rec, h.cfg.OIDCSessionTTL); err != nil {
		return err
	}
	// §27.3.1 line 81: the cookie carries only the opaque session id. The
	// tenant is recovered server-side from the fan-in index PutSession
	// wrote, so the cookie value discloses no tenant id. F-27.3.8.
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sessionID,
		Path:     sessionCookiePath,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(h.cfg.OIDCSessionTTL.Seconds()),
	})
	return nil
}

// handleLogout serves POST /playground/auth/logout (§27.3.1 step 1).
// It deletes the session record server-side and revokes every bearer
// the session minted, fanning the change out to peer replicas, then
// clears the cookie. It does not return 200 until the revocation
// writes have committed (§27.6).
func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if h.cfg.AuthMode != AuthModeOIDC {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
			"the playground logout endpoint exists only in authMode=oidc", nil)
		return
	}
	clearCookie(w, sessionCookie, sessionCookiePath)
	id, ok := parseSessionCookie(r)
	if !ok || h.sessions == nil {
		// No cookie or no store: logout is idempotent, the cleared
		// cookie above is the only effect.
		w.WriteHeader(http.StatusOK)
		return
	}
	// §27.3.1 line 81: recover the tenant from the fan-in index since the
	// cookie carries only the opaque id. A missing entry or store error
	// leaves logout idempotent (the cookie is already cleared). F-27.3.8.
	tenant, found, terr := h.sessions.TenantForSession(r.Context(), id)
	if terr != nil || !found {
		w.WriteHeader(http.StatusOK)
		return
	}
	rec, err := h.sessions.GetSession(r.Context(), tenant, id)
	if err != nil {
		// Record already gone: nothing to revoke.
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := h.revokeSessionRecord(r.Context(), tenant, id, rec, RevokeUserLogout); err != nil {
		writeError(w, http.StatusServiceUnavailable, "REDIS_UNAVAILABLE",
			"the playground revocation write did not commit: "+err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// revokeSessionRecord is the §27.6 single revocation primitive:
// logout, user.invalidated, idle timeout, and admin revocation all
// converge here. It revokes every bearer the record minted, records
// the §27.8 reason and propagation latency, and emits the §27.3.1
// step-6 audit event.
func (h *Handler) revokeSessionRecord(ctx context.Context, tenant, id string, rec SessionRecord, reason RevocationReason) error {
	revokedTTL := h.revokedMarkerTTL(rec)
	if err := h.sessions.RevokeSession(ctx, tenant, id, rec.BearerJTIs, revokedTTL); err != nil {
		return err
	}
	h.metrics.revocation(string(reason))
	// spec: §27.8 line 241 — the propagation histogram measures latency
	// "from when a revocation is written on the originating replica to
	// when peer replicas observe it", so the sample is emitted by the
	// subscribing (peer) replica in SubscribeAllRevocations, not here on
	// the originating replica's local write. Recording the local
	// MULTI/EXEC round-trip under {outcome="redis_authoritative"} (as the
	// prior code did) mislabelled a same-replica write as a cross-replica
	// observation and never captured the quantity the SLO bounds. F-27.6.6.
	h.emitBearerRevokedAudit(ctx, tenant, rec.UserID, id, rec.BearerJTIs)
	return nil
}

// RevokeSession is the §27.6 admin/idle-timeout entry point into the
// revocation primitive. The gateway calls it from the §11.4 user
// invalidation fan-out and the idle-timeout sweep so a playground
// session is revoked through the same path as a user logout. id and
// tenant identify the playground session record; reason attributes
// the §27.8 metric.
func (h *Handler) RevokeSession(ctx context.Context, tenant, id string, reason RevocationReason) error {
	if h.sessions == nil {
		return nil
	}
	rec, err := h.sessions.GetSession(ctx, tenant, id)
	if err != nil {
		// Record already gone: revocation is idempotent.
		return nil
	}
	return h.revokeSessionRecord(ctx, tenant, id, rec, reason)
}

// defaultIdleSweepInterval is the cadence of the §27.6 playground
// idle-timeout sweep when no interval is supplied. It is well below the
// idle reclamation window so an abandoned session is reclaimed promptly
// after it crosses the window.
const defaultIdleSweepInterval = 60 * time.Second

// idleReclaimWindow is the §27.6 line 201 idle window after which an
// abandoned playground session record is reclaimed. A bearer mint is the
// session's activity heartbeat, so the window allows one full bearer
// lifetime (the longest an actively-minting user goes between heartbeats)
// plus the playground.maxIdleTimeSeconds idle grace. This guarantees the
// sweep never reclaims a session a user is actively re-minting against,
// while still bounding the reclamation of a session whose browser closed
// without delivering the best-effort cancel (§27.6 line 202).
func (h *Handler) idleReclaimWindow() time.Duration {
	grace := time.Duration(h.cfg.MaxIdleTimeSeconds) * time.Second
	if grace <= 0 {
		grace = 300 * time.Second
	}
	return h.cfg.BearerTTL + grace
}

// SweepIdleSessions runs one §27.6 idle-timeout pass: it enumerates every
// playground session record idle past the reclamation window and revokes
// each through the shared §27.3.1 revocation primitive with reason
// RevokeIdleTimeout (DEL the record, SET pg:revoked for every minted
// bearer, PUBLISH the fan-out, emit the §27.8 metric). It returns the
// number revoked. The pass is best-effort across records: a per-record
// store error is collected and the remaining records are still attempted,
// so one failure does not strand the sweep. spec: §27.3.1 line 94, §27.6
// line 201/204.
func (h *Handler) SweepIdleSessions(ctx context.Context) (int, error) {
	if h.sessions == nil {
		return 0, nil
	}
	cutoff := h.now().Add(-h.idleReclaimWindow())
	refs, err := h.sessions.IdleSessions(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	var (
		revoked  int
		firstErr error
	)
	for _, ref := range refs {
		if err := h.RevokeSession(ctx, ref.Tenant, ref.ID, RevokeIdleTimeout); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		revoked++
	}
	return revoked, firstErr
}

// RunIdleSweeper drives SweepIdleSessions on interval until ctx is
// cancelled. The gateway runs it in a goroutine when the playground is
// enabled so the §27.6 idle-timeout and admin-revocation reasons are both
// wired end to end. A non-positive interval selects defaultIdleSweepInterval.
// A sweep error is logged and the loop continues; the store stays
// authoritative, so a transient failure only delays reclamation.
func (h *Handler) RunIdleSweeper(ctx context.Context, interval time.Duration) {
	if h.sessions == nil {
		return
	}
	if interval <= 0 {
		interval = defaultIdleSweepInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := h.SweepIdleSessions(ctx); err != nil {
				slog.WarnContext(ctx, "playground: idle-timeout sweep failed", "error", err)
			}
		}
	}
}

// RevokeSessionsForUser is the §11.4 user-invalidation entry point into
// the §27.6 revocation primitive. It revokes every playground session
// the named user holds — DEL the session record, SET a pg:revoked
// marker for each minted bearer, PUBLISH the fan-out — so an OIDC
// principal invalidation (POST /v1/admin/users/{user_id}/invalidate,
// §11.4) disconnects the user's in-flight playground bearers at the next
// frame boundary and blocks new mints (a subsequent
// POST /v1/playground/token finds no record and returns 401). It is
// best-effort across the user's sessions: a per-session store error is
// returned after the remaining sessions are attempted, so the §11.4
// fan-out records a partial propagation rather than aborting. spec:
// §27.3.1 line 148, §27.6 line 204.
func (h *Handler) RevokeSessionsForUser(ctx context.Context, tenant, userID string) (int, error) {
	if h.sessions == nil {
		return 0, nil
	}
	ids, err := h.sessions.SessionsForUser(ctx, tenant, userID)
	if err != nil {
		return 0, err
	}
	var (
		revoked  int
		firstErr error
	)
	for _, id := range ids {
		rec, err := h.sessions.GetSession(ctx, tenant, id)
		if err != nil {
			// Already expired or revoked: revocation is idempotent.
			continue
		}
		if err := h.revokeSessionRecord(ctx, tenant, id, rec, RevokeUserInvalidated); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		revoked++
	}
	return revoked, firstErr
}

// IsBearerRevoked is the §27.3.1 per-request revocation check. The
// gateway auth chain calls it for every playground-origin bearer
// (identified by the origin claim) before the bearer is honored. A
// non-nil error means the backing store is unreachable; the caller
// fails closed (503 REDIS_UNAVAILABLE) per §27.3.1 rather than
// honoring the bearer.
func (h *Handler) IsBearerRevoked(ctx context.Context, tenant, jti string) (bool, error) {
	if h.sessions == nil {
		return false, nil
	}
	return h.sessions.IsBearerRevoked(ctx, tenant, jti)
}

// revokedMarkerTTL returns the §27.3.1 revocation-marker TTL: the
// remaining bearer lifetime plus a 5 s skew budget. When the record
// carries no current expiry the marker is held for the full bearer
// TTL.
func (h *Handler) revokedMarkerTTL(rec SessionRecord) time.Duration {
	if rec.CurrentExp == 0 {
		return h.cfg.BearerTTL + 5*time.Second
	}
	remaining := time.Unix(rec.CurrentExp, 0).Sub(h.now())
	if remaining < 0 {
		remaining = 0
	}
	return remaining + 5*time.Second
}

// handleAuthError serves GET /playground/auth/error: it renders the
// embedded error page so the SPA can surface an OIDC failure code to
// the user. The page itself is part of the SPA bundle.
func (h *Handler) handleAuthError(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(errorPageHTML(r.URL.Query().Get("error")))
}

// redirectAuthError clears the OIDC state cookie and redirects the
// browser to the playground error page with the supplied code.
func (h *Handler) redirectAuthError(w http.ResponseWriter, r *http.Request, code string) {
	clearCookie(w, oidcStateCookie, statePath)
	http.Redirect(w, r, "/playground/auth/error?error="+code, http.StatusFound)
}

// callbackURL returns the absolute OIDC redirect_uri for this
// gateway. It honors the forwarded-proto header so a TLS-terminating
// ingress is reflected.
func (h *Handler) callbackURL(r *http.Request) string {
	scheme := "https"
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if r.TLS == nil {
		scheme = "http"
	}
	return scheme + "://" + r.Host + "/playground/auth/callback"
}

// parseSessionCookie reads the lenny_playground_session cookie and
// returns its opaque session id. Per §27.3.1 line 81 the cookie value
// is the opaque session id alone; the tenant is recovered server-side
// via SessionStore.TenantForSession rather than embedded in the cookie.
// The opaque id is base64url (newOpaqueID) and never contains a dot, so
// a legacy dotted value fails the index lookup and is treated as an
// expired session. F-27.3.8.
func parseSessionCookie(r *http.Request) (id string, ok bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return "", false
	}
	return c.Value, true
}

// clearCookie writes a Max-Age=0 Set-Cookie for name at path.
func clearCookie(w http.ResponseWriter, name, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:   name,
		Value:  "",
		Path:   path,
		MaxAge: -1,
	})
}

// asOIDCError reports whether err is an *OIDCError.
func asOIDCError(err error) (*OIDCError, bool) {
	oe, ok := err.(*OIDCError)
	return oe, ok
}
