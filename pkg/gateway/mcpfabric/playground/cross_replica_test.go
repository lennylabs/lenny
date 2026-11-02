// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/connectoroauth"
	"github.com/lennylabs/lenny/pkg/redisconn/slotguard"
	"github.com/lennylabs/lenny/tests/testinfra/jwtsign"
)

// spec: §27.3.1 — "TestPlaygroundSessionRevocationCrossReplica
// MUST assert that a logout on replica A invalidates a subsequent
// request carrying the same cookie or bearer on replica B, both before
// and after the pub/sub message is delivered (the authoritative Redis
// check covers the pre-delivery case; the LRU negative cache covers the
// post-delivery case)." F-27.3.10.
func TestPlaygroundSessionRevocationCrossReplica_spec_27_3_1_98(t *testing.T) {
	mr := miniredis.RunT(t)
	clientA := slotguard.Install(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	clientB := slotguard.Install(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })

	const tenant = "acme"
	ctx := context.Background()

	// Replica A is the revoking replica.
	replicaA := NewRedisSessionStore(clientA)

	// --- Pre-delivery case: authoritative Redis GET ---
	// replicaBCold runs no subscription, so its negative cache is never
	// warmed and IsBearerRevoked always consults Redis authoritatively.
	replicaBCold := NewRedisSessionStore(clientB)
	const jtiCold = "jti-cold"
	if err := replicaA.PutSession(ctx, tenant, "sess-cold",
		SessionRecord{UserID: "alice", TenantID: tenant, BearerJTIs: []string{jtiCold}}, time.Hour); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	// Before the logout the bearer is honored on replica B.
	if revoked, err := replicaBCold.IsBearerRevoked(ctx, tenant, jtiCold); err != nil || revoked {
		t.Fatalf("pre-logout replicaB IsBearerRevoked = %v, %v; want false, nil", revoked, err)
	}
	// Logout on replica A commits the revocation marker to Redis.
	if err := replicaA.RevokeSession(ctx, tenant, "sess-cold", []string{jtiCold}, time.Hour); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	// Replica B observes the revocation immediately via the authoritative
	// Redis check, before any pub/sub delivery warms a cache.
	if revoked, err := replicaBCold.IsBearerRevoked(ctx, tenant, jtiCold); err != nil || !revoked {
		t.Fatalf("pre-delivery replicaB IsBearerRevoked = %v, %v; want true, nil", revoked, err)
	}

	// --- Post-delivery case: pub/sub-warmed negative cache ---
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	replicaBWarm := NewRedisSessionStore(clientB)
	go replicaBWarm.SubscribeAllRevocations(subCtx)

	const jtiWarm = "jti-warm"
	// Re-publish on each poll until replica B's cache converges; miniredis
	// drops a message published before PSUBSCRIBE registers.
	waitForCondition(t, 3*time.Second, func() bool {
		if err := replicaA.RevokeSession(ctx, tenant, "sess-warm", []string{jtiWarm}, time.Hour); err != nil {
			t.Fatalf("RevokeSession(warm): %v", err)
		}
		replicaBWarm.cacheMu.RLock()
		_, ok := replicaBWarm.cache[revokedKey(tenant, jtiWarm)]
		replicaBWarm.cacheMu.RUnlock()
		return ok
	})
	if revoked, err := replicaBWarm.IsBearerRevoked(ctx, tenant, jtiWarm); err != nil || !revoked {
		t.Fatalf("post-delivery replicaB IsBearerRevoked = %v, %v; want true, nil", revoked, err)
	}
}

// spec: §27.3.1 / §12.4 — the Redis-backed store extends the
// tenant-key-isolation guarantee to the pg:sess:* and pg:revoked:* key
// prefixes: a record or revocation marker written for tenant A must not
// be visible to a request scoped to tenant B reusing the same
// (lexically-equal) session id or jti. F-27.3.10.
func TestRedisSessionStoreTenantKeyIsolation_spec_27_3_1_98(t *testing.T) {
	mr := miniredis.RunT(t)
	client := slotguard.Install(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	t.Cleanup(func() { _ = client.Close() })
	store := NewRedisSessionStore(client)
	ctx := context.Background()

	// pg:sess:* — a record for tenant acme is not readable by tenant globex.
	if err := store.PutSession(ctx, "acme", "sess-shared",
		SessionRecord{UserID: "alice", TenantID: "acme", Origin: PlaygroundOrigin}, time.Hour); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	if _, err := store.GetSession(ctx, "globex", "sess-shared"); err == nil {
		t.Fatal("tenant globex read tenant acme's pg:sess:* record")
	}
	got, err := store.GetSession(ctx, "acme", "sess-shared")
	if err != nil || got.UserID != "alice" {
		t.Fatalf("GetSession(acme) = %+v, %v; want UserID alice", got, err)
	}

	// pg:revoked:* — a marker for tenant acme's jti must not reject a
	// tenant globex request reusing the same jti value.
	if err := store.MarkBearerRevoked(ctx, "acme", "jti-shared", time.Hour); err != nil {
		t.Fatalf("MarkBearerRevoked: %v", err)
	}
	if revoked, err := store.IsBearerRevoked(ctx, "acme", "jti-shared"); err != nil || !revoked {
		t.Fatalf("acme jti-shared revoked = %v, %v; want true, nil", revoked, err)
	}
	if revoked, err := store.IsBearerRevoked(ctx, "globex", "jti-shared"); err != nil || revoked {
		t.Fatalf("globex jti-shared revoked = %v, %v; want false, nil", revoked, err)
	}
}

// TestRedisTenantForSessionIndexRoundTrip exercises the §27.3.1 fan-in
// index on the Redis-backed store: PutSession writes pg:sess-tenant:{id}
// so the tenant is recoverable from the opaque cookie id alone, and
// RevokeSession deletes it. F-27.3.8.
func TestRedisTenantForSessionIndexRoundTrip_spec_27_3_1_81(t *testing.T) {
	mr := miniredis.RunT(t)
	client := slotguard.Install(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	t.Cleanup(func() { _ = client.Close() })
	store := NewRedisSessionStore(client)
	ctx := context.Background()

	rec := SessionRecord{UserID: "alice", TenantID: "acme", Origin: PlaygroundOrigin, BearerJTIs: []string{"jti-1"}}
	if err := store.PutSession(ctx, "acme", "opaque-id", rec, time.Hour); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	// The index key is the platform-scoped fan-in entry, not tenant-prefixed.
	if got, err := client.Get(ctx, "pg:sess-tenant:opaque-id").Result(); err != nil || got != "acme" {
		t.Fatalf("pg:sess-tenant:opaque-id = %q, %v; want acme, nil", got, err)
	}
	tenant, ok, err := store.TenantForSession(ctx, "opaque-id")
	if err != nil || !ok || tenant != "acme" {
		t.Fatalf("TenantForSession = (%q, %v, %v); want (acme, true, nil)", tenant, ok, err)
	}
	if _, ok, _ := store.TenantForSession(ctx, "never-issued"); ok {
		t.Fatal("TenantForSession resolved an id that was never issued")
	}
	if err := store.RevokeSession(ctx, "acme", "opaque-id", rec.BearerJTIs, time.Hour); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if _, ok, _ := store.TenantForSession(ctx, "opaque-id"); ok {
		t.Fatal("TenantForSession resolved a revoked id; index entry survived RevokeSession")
	}
}

// oauthStateBackends runs the §27.3.1 state tests over each StateStore
// backing. A backend's open function returns a store over the shared
// backing: the Redis backend dials a new client on each call, which is what a
// second replica or a restarted process does, and the memory backend returns
// its one instance, which is the single-process posture.
func oauthStateBackends(t *testing.T, run func(t *testing.T, open func() connectoroauth.StateStore)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) {
		mem := connectoroauth.NewMemoryStateStore()
		run(t, func() connectoroauth.StateStore { return mem })
	})
	t.Run("redis", func(t *testing.T) {
		mr := miniredis.RunT(t)
		run(t, func() connectoroauth.StateStore { return redisStateStoreOn(t, mr) })
	})
}

func redisStateStoreOn(t *testing.T, mr *miniredis.Miniredis) *connectoroauth.RedisStateStore {
	t.Helper()
	client := slotguard.Install(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	t.Cleanup(func() { _ = client.Close() })
	return connectoroauth.NewRedisStateStore(client)
}

// oidcStateHandler builds an oidc-mode handler over store and serves its
// /playground routes. Handlers built by separate calls share nothing but the
// arguments, as two replicas or a restarted process do.
func oidcStateHandler(t *testing.T, store connectoroauth.StateStore, oidc *fakeOIDC, now func() time.Time) *httptest.Server {
	t.Helper()
	h := New(Config{Enabled: true, AuthMode: AuthModeOIDC, OIDCSessionTTL: time.Hour, CapabilityTokenTTL: 900 * time.Second}, Options{
		Signer:     jwtsign.HMAC(devSigner()),
		Verifier:   devSigner(),
		Sessions:   NewMemorySessionStore(),
		OIDC:       oidc,
		OAuthState: store,
		Tenants:    fakeTenants{registered: map[string]bool{"acme": true}},
		Now:        now,
	})
	srv := httptest.NewServer(h.PlaygroundRoutes())
	t.Cleanup(srv.Close)
	return srv
}

func aliceOIDC() *fakeOIDC {
	return &fakeOIDC{subject: OIDCSubject{UserID: "alice", TenantID: "acme", Scope: "tools:sessions:read"}}
}

// oidcHop sends one GET without following redirects and returns the response
// with its body drained.
func oidcHop(t *testing.T, target string, cookies ...*http.Cookie) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}

// beginOIDCLogin runs a login and returns the state cookie and the state the
// redirect sent to the provider, which are the same value.
func beginOIDCLogin(t *testing.T, srv *httptest.Server) (*http.Cookie, string) {
	t.Helper()
	resp, body := oidcHop(t, srv.URL+"/playground/auth/login")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login status = %d, want 302; body=%s", resp.StatusCode, body)
	}
	cookie := stateCookieOf(resp)
	if cookie == nil {
		t.Fatal("login set no state cookie")
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	state := loc.Query().Get("state")
	if cookie.Value != state {
		t.Fatalf("state cookie = %q, want the opaque state %q", cookie.Value, state)
	}
	return cookie, state
}

func stateCookieOf(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == oidcStateCookie && c.Value != "" {
			return c
		}
	}
	return nil
}

func oidcCallback(t *testing.T, srv *httptest.Server, state string, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	resp, _ := oidcHop(t, srv.URL+"/playground/auth/callback?code=auth-code&state="+url.QueryEscape(state), cookies...)
	return resp
}

// wantAuthError asserts resp redirects to the error page with code and clears
// the state cookie.
func wantAuthError(t *testing.T, resp *http.Response, code string) {
	t.Helper()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 to the error page", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if loc.Path != "/playground/auth/error" || loc.Query().Get("error") != code {
		t.Fatalf("Location = %q, want the error page with error=%s", loc, code)
	}
	if stateCookieOf(resp) != nil {
		t.Fatal("an error redirect left the state cookie set")
	}
}

// wantSessionEstablished asserts the callback set the session cookie.
func wantSessionEstablished(t *testing.T, resp *http.Response) {
	t.Helper()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != sessionCookiePath {
		t.Fatalf("callback status = %d Location %q, want 302 to %s", resp.StatusCode, resp.Header.Get("Location"), sessionCookiePath)
	}
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			return
		}
	}
	t.Fatal("callback set no session cookie")
}

// faultStateStore wraps a StateStore with injectable Put and Consume faults
// and counts the calls that reach it.
type faultStateStore struct {
	connectoroauth.StateStore
	putErr, consumeErr error
	puts, consumes     atomic.Int64
}

func (s *faultStateStore) Put(state string, fc connectoroauth.FlowContext, ttl time.Duration) error {
	s.puts.Add(1)
	if s.putErr != nil {
		return s.putErr
	}
	return s.StateStore.Put(state, fc, ttl)
}

func (s *faultStateStore) Consume(state string, now time.Time) (connectoroauth.FlowContext, error) {
	s.consumes.Add(1)
	if s.consumeErr != nil {
		return connectoroauth.FlowContext{}, s.consumeErr
	}
	return s.StateStore.Consume(state, now)
}

// spec: §27.3.1 (OIDC cookie-to-MCP-bearer exchange)
// `GET /playground/auth/callback`, §10.1.1 (Stateless Replicas and Per-Session
// Coordination)
//
// A login on one handler completes on a peer handler and on a fresh handler
// after a simulated restart, using the verifier the store holds. A failure here
// means the callback depends on state in one process.
func TestOIDCCallbackCompletesAcrossHandlers_spec_27_3_1(t *testing.T) {
	oauthStateBackends(t, func(t *testing.T, open func() connectoroauth.StateStore) {
		for name, target := range map[string]func(store connectoroauth.StateStore) connectoroauth.StateStore{
			"peer sharing the store instance": func(store connectoroauth.StateStore) connectoroauth.StateStore { return store },
			"fresh handler after a restart":   func(connectoroauth.StateStore) connectoroauth.StateStore { return open() },
		} {
			t.Run(name, func(t *testing.T) {
				store := open()
				a := oidcStateHandler(t, store, aliceOIDC(), nil)
				oidcB := aliceOIDC()
				b := oidcStateHandler(t, target(store), oidcB, nil)

				cookie, state := beginOIDCLogin(t, a)
				wantSessionEstablished(t, oidcCallback(t, b, state, cookie))
				if oidcB.lastVerif == "" {
					t.Fatal("the exchange received no PKCE verifier")
				}
			})
		}
	})
}

// spec: §27.3.1 (OIDC cookie-to-MCP-bearer exchange)
// `GET /playground/auth/login`
//
// The verifier the exchange receives is the one behind the login's
// code_challenge, read from the store.
func TestOIDCExchangeUsesTheStoredVerifier_spec_27_3_1(t *testing.T) {
	store := connectoroauth.NewMemoryStateStore()
	oidc := aliceOIDC()
	srv := oidcStateHandler(t, store, oidc, nil)
	resp, _ := oidcHop(t, srv.URL+"/playground/auth/login")
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	wantSessionEstablished(t, oidcCallback(t, srv, loc.Query().Get("state"), stateCookieOf(resp)))
	if got, want := codeChallengeS256(oidc.lastVerif), loc.Query().Get("code_challenge"); got != want {
		t.Fatalf("challenge of the exchanged verifier = %q, want %q", got, want)
	}
}

// spec: §27.3.1 (OIDC cookie-to-MCP-bearer exchange) "so each `state` is single use"
//
// A replayed callback reports oidc_state_invalid, and reaches no exchange.
func TestOIDCCallbackReplayIsInvalid_spec_27_3_1(t *testing.T) {
	oauthStateBackends(t, func(t *testing.T, open func() connectoroauth.StateStore) {
		oidc := aliceOIDC()
		srv := oidcStateHandler(t, open(), oidc, nil)
		cookie, state := beginOIDCLogin(t, srv)
		wantSessionEstablished(t, oidcCallback(t, srv, state, cookie))
		wantAuthError(t, oidcCallback(t, srv, state, cookie), "oidc_state_invalid")
		if oidc.exchanges != 1 {
			t.Fatalf("exchanges = %d, want 1", oidc.exchanges)
		}
	})
}

// spec: §27.3.1 (OIDC cookie-to-MCP-bearer exchange) "atomically consumes"
//
// Concurrent callbacks for one state establish exactly one session, and every
// other callback reports oidc_state_invalid.
func TestOIDCCallbackConcurrentConsumeHasOneWinner_spec_27_3_1(t *testing.T) {
	oauthStateBackends(t, func(t *testing.T, open func() connectoroauth.StateStore) {
		oidc := aliceOIDC()
		a := oidcStateHandler(t, open(), oidc, nil)
		b := oidcStateHandler(t, open(), oidc, nil)
		cookie, state := beginOIDCLogin(t, a)

		const callers = 16
		codes := make([]string, callers)
		var wg sync.WaitGroup
		for i := range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				srv := a
				if i%2 == 1 {
					srv = b
				}
				req, _ := http.NewRequest(http.MethodGet, srv.URL+"/playground/auth/callback?code=c&state="+url.QueryEscape(state), nil)
				req.AddCookie(cookie)
				resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
					return http.ErrUseLastResponse
				}}).Do(req)
				if err != nil {
					codes[i] = err.Error()
					return
				}
				_ = resp.Body.Close()
				codes[i] = resp.Header.Get("Location")
			}()
		}
		wg.Wait()
		wins := 0
		for _, loc := range codes {
			switch {
			case loc == sessionCookiePath:
				wins++
			case strings.HasSuffix(loc, "error=oidc_state_invalid"):
			default:
				t.Errorf("callback outcome %q, want the session or oidc_state_invalid", loc)
			}
		}
		if wins != 1 || oidc.exchanges != 1 {
			t.Fatalf("wins = %d, exchanges = %d, want 1 and 1", wins, oidc.exchanges)
		}
	})
}

// spec: §27.3.1 (OIDC cookie-to-MCP-bearer exchange) "The cookie binds the
// login to the browser."
//
// A callback whose cookie is absent or differs from state consumes nothing, so
// the login's own browser still completes it.
func TestOIDCCallbackWithoutTheBrowserBindingConsumesNothing_spec_27_3_1(t *testing.T) {
	for name, tc := range map[string]struct {
		cookies []*http.Cookie
		code    string
	}{
		"missing cookie":   {nil, "oidc_state_missing"},
		"differing cookie": {[]*http.Cookie{{Name: oidcStateCookie, Value: "another-login"}}, "oidc_state_mismatch"},
	} {
		t.Run(name, func(t *testing.T) {
			oauthStateBackends(t, func(t *testing.T, open func() connectoroauth.StateStore) {
				store := &faultStateStore{StateStore: open()}
				oidc := aliceOIDC()
				srv := oidcStateHandler(t, store, oidc, nil)
				cookie, state := beginOIDCLogin(t, srv)

				wantAuthError(t, oidcCallback(t, srv, state, tc.cookies...), tc.code)
				if got := store.consumes.Load(); got != 0 {
					t.Fatalf("the refused callback called Consume %d times, want 0", got)
				}
				wantSessionEstablished(t, oidcCallback(t, srv, state, cookie))
			})
		})
	}
}

// spec: §27.3.1 (OIDC cookie-to-MCP-bearer exchange) "or an entry a connector
// flow wrote ends the flow on the playground error page"
//
// One store serves both flows, so the callback refuses a connector entry with
// no provider exchange.
func TestOIDCCallbackRefusesAConnectorEntry_spec_27_3_1(t *testing.T) {
	oauthStateBackends(t, func(t *testing.T, open func() connectoroauth.StateStore) {
		store := open()
		oidc := aliceOIDC()
		srv := oidcStateHandler(t, store, oidc, nil)
		const state = "connector-flow-state"
		if err := store.Put(state, connectoroauth.FlowContext{ConnectorID: "github", CodeVerifier: "v", CreatedAt: time.Now()}, stateCookieTTL); err != nil {
			t.Fatalf("Put: %v", err)
		}
		wantAuthError(t, oidcCallback(t, srv, state, &http.Cookie{Name: oidcStateCookie, Value: state}), "oidc_state_invalid")
		if oidc.exchanges != 0 {
			t.Fatalf("exchanges = %d, want 0", oidc.exchanges)
		}
	})
}

// spec: §27.3.1 (OIDC cookie-to-MCP-bearer exchange) "writes them to the shared
// store under `conn:oauth:state:{state}` ... with a 10-minute TTL"
//
// An entry past its TTL reports oidc_state_expired on the memory store, which
// retains it, and oidc_state_invalid on Redis, which evicts it.
func TestOIDCCallbackPastTheTTL_spec_27_3_1(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		clock := time.Unix(1_700_000_000, 0)
		srv := oidcStateHandler(t, connectoroauth.NewMemoryStateStore(), aliceOIDC(), func() time.Time { return clock })
		cookie, state := beginOIDCLogin(t, srv)
		clock = clock.Add(stateCookieTTL + time.Second)
		wantAuthError(t, oidcCallback(t, srv, state, cookie), "oidc_state_expired")
	})
	t.Run("redis", func(t *testing.T) {
		mr := miniredis.RunT(t)
		srv := oidcStateHandler(t, redisStateStoreOn(t, mr), aliceOIDC(), nil)
		cookie, state := beginOIDCLogin(t, srv)
		mr.FastForward(stateCookieTTL + time.Second)
		wantAuthError(t, oidcCallback(t, srv, state, cookie), "oidc_state_invalid")
	})
}

// spec: §27.3.1 (OIDC cookie-to-MCP-bearer exchange), §27.3.1 Failure modes
// summary
//
// A handler with no store answers 503 at login with no panic. A callback on a
// handler missing a dependency answers 503 and leaves the entry in place.
func TestOIDCStateUnwired_spec_27_3_1(t *testing.T) {
	unwired := oidcStateHandler(t, nil, aliceOIDC(), nil)
	resp, body := oidcHop(t, unwired.URL+"/playground/auth/login")
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "LENNY_PLAYGROUND_OIDC_UNAVAILABLE") {
		t.Fatalf("login with no store: status %d body %s", resp.StatusCode, body)
	}
	const state = "pending-state"
	cookie := &http.Cookie{Name: oidcStateCookie, Value: state}
	if resp := oidcCallback(t, unwired, state, cookie); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("callback with no store: status %d, want 503", resp.StatusCode)
	}

	store := &faultStateStore{StateStore: connectoroauth.NewMemoryStateStore()}
	noSessions := New(Config{Enabled: true, AuthMode: AuthModeOIDC}, Options{
		Signer: jwtsign.HMAC(devSigner()), Verifier: devSigner(), OIDC: aliceOIDC(), OAuthState: store,
	})
	srv := httptest.NewServer(noSessions.PlaygroundRoutes())
	t.Cleanup(srv.Close)
	if err := store.Put(state, connectoroauth.FlowContext{CreatedAt: time.Now()}, stateCookieTTL); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if resp := oidcCallback(t, srv, state, cookie); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("callback with no session store: status %d, want 503", resp.StatusCode)
	}
	if got := store.consumes.Load(); got != 0 {
		t.Fatalf("an unconfigured handler called Consume %d times, want 0", got)
	}
}

// spec: §27.3.1 (OIDC cookie-to-MCP-bearer exchange) `GET /playground/auth/login`
//
// A provider failure at login leaves no key in the store and sets no cookie,
// because the route is anonymous.
func TestOIDCLoginProviderFailureWritesNoState_spec_27_3_1(t *testing.T) {
	mr := miniredis.RunT(t)
	store := &faultStateStore{StateStore: redisStateStoreOn(t, mr)}
	srv := oidcStateHandler(t, store, &fakeOIDC{authURLErr: errors.New("discovery unreachable")}, nil)
	resp, body := oidcHop(t, srv.URL+"/playground/auth/login")
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "LENNY_PLAYGROUND_OIDC_UNAVAILABLE") {
		t.Fatalf("status %d body %s, want 503 LENNY_PLAYGROUND_OIDC_UNAVAILABLE", resp.StatusCode, body)
	}
	if store.puts.Load() != 0 || len(mr.Keys()) != 0 {
		t.Fatalf("puts = %d, keys = %v, want none", store.puts.Load(), mr.Keys())
	}
	if len(resp.Cookies()) != 0 {
		t.Fatalf("cookies = %v, want none", resp.Cookies())
	}
}

// spec: §27.3.1 Failure modes summary "Redis unavailable at
// `GET /playground/auth/login`"
//
// A store fault at login answers JSON 503 REDIS_UNAVAILABLE and sets no cookie.
func TestOIDCLoginStoreFaultAnswers503_spec_27_3_1(t *testing.T) {
	for name, store := range map[string]func(t *testing.T) connectoroauth.StateStore{
		"injected Put fault": func(*testing.T) connectoroauth.StateStore {
			return &faultStateStore{StateStore: connectoroauth.NewMemoryStateStore(), putErr: errors.New("put failed")}
		},
		"redis error reply": func(t *testing.T) connectoroauth.StateStore {
			mr := miniredis.RunT(t)
			mr.SetError("LOADING")
			return redisStateStoreOn(t, mr)
		},
	} {
		t.Run(name, func(t *testing.T) {
			srv := oidcStateHandler(t, store(t), aliceOIDC(), nil)
			resp, body := oidcHop(t, srv.URL+"/playground/auth/login")
			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", resp.StatusCode)
			}
			var env errorEnvelope
			if err := json.Unmarshal([]byte(body), &env); err != nil || env.Error.Code != "REDIS_UNAVAILABLE" {
				t.Fatalf("body = %s (err %v), want the REDIS_UNAVAILABLE envelope", body, err)
			}
			if len(resp.Cookies()) != 0 {
				t.Fatalf("cookies = %v, want none", resp.Cookies())
			}
		})
	}
}

// spec: §27.3.1 (OIDC cookie-to-MCP-bearer exchange) "A `state` store fault does
// the same with `error=oidc_state_unavailable`, the HTTP status on the final
// response to the browser is 503"
//
// A store fault at the callback redirects to oidc_state_unavailable with no
// exchange, and following the redirect renders 503.
func TestOIDCCallbackStoreFaultRendersUnavailable_spec_27_3_1(t *testing.T) {
	for name, tc := range map[string]func(t *testing.T) (connectoroauth.StateStore, func()){
		"injected Consume fault": func(*testing.T) (connectoroauth.StateStore, func()) {
			store := &faultStateStore{StateStore: connectoroauth.NewMemoryStateStore()}
			return store, func() { store.consumeErr = errors.New("consume failed") }
		},
		"redis error reply": func(t *testing.T) (connectoroauth.StateStore, func()) {
			mr := miniredis.RunT(t)
			return redisStateStoreOn(t, mr), func() { mr.SetError("LOADING") }
		},
	} {
		t.Run(name, func(t *testing.T) {
			store, cut := tc(t)
			oidc := aliceOIDC()
			srv := oidcStateHandler(t, store, oidc, nil)
			cookie, state := beginOIDCLogin(t, srv)
			cut()

			resp := oidcCallback(t, srv, state, cookie)
			wantAuthError(t, resp, errOIDCStateUnavailable)
			if oidc.exchanges != 0 {
				t.Fatalf("exchanges = %d, want 0", oidc.exchanges)
			}
			page, body := oidcHop(t, srv.URL+resp.Header.Get("Location"))
			if page.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, errOIDCStateUnavailable) {
				t.Fatalf("error page status %d, want 503 naming the code", page.StatusCode)
			}
		})
	}
}

// spec: §27.3.1 Tenant-claim rejection codes (OIDC callback)
//
// Only the store-fault code changes the error page status. Each other code
// keeps 200.
func TestOIDCErrorPageStatusByCode_spec_27_3_1(t *testing.T) {
	srv := oidcStateHandler(t, connectoroauth.NewMemoryStateStore(), aliceOIDC(), nil)
	for _, code := range []string{"", "oidc_state_invalid", "oidc_state_expired", "oidc_state_missing", "oidc_state_mismatch", errTenantNotFound} {
		resp, _ := oidcHop(t, srv.URL+"/playground/auth/error?error="+code)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("error=%q status = %d, want 200", code, resp.StatusCode)
		}
	}
}
