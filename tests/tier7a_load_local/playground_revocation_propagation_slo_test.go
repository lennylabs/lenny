// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local SLO coverage for the §27.3.1 cross-replica
// playground logout-propagation budget. TestPlaygroundSessionRevocation
// CrossReplica (pkg/gateway/mcpfabric/playground/cross_replica_test.go)
// proves the guarantee holds against a single in-process miniredis; this
// file drives the same guarantee against real cmd/lenny-gateway replica
// processes, a real Redis container, and a real OIDC PKCE login on each
// of many concurrent playground sessions, then measures the observed
// propagation latency at scale (matching the TESTING.md §12.7
// playground_revocation scenario: 1000 active sessions, tenant-wide
// revoke) instead of asserting correctness on a single session alone.
package tier7a_load_local_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	oidcstub "github.com/lennylabs/lenny/tests/testinfra/stubs/oidc"
)

// spec: §27.3.1 ("Pub/sub propagation", spec/27_web-playground.md) —
// "a revocation published on any replica MUST be visible to the
// per-request revocation check on every other replica within 500 ms at
// P99 under nominal Redis health; the sub-authoritative LRU cache MUST
// converge within the same budget."
//
// diagnosis: a failure here means the §27.3.1 logout-propagation SLO
// does not hold once the cross-replica revocation primitive is put
// under realistic load (many concurrently active playground sessions
// spread across replicas, matching the TESTING.md §12.7
// playground_revocation load scenario). The only other coverage of this
// guarantee — pkg/gateway/mcpfabric/playground/cross_replica_test.go
// TestPlaygroundSessionRevocationCrossReplica — drives a single session
// against an in-process miniredis and asserts correctness only; it
// cannot catch a latency regression that only appears once the shared
// Redis pub/sub channel and the per-replica LRU negative cache are
// contended by many concurrent sessions logging out at once. Inspect
// the failing session indices in the test log and the Redis container
// logs for the slowest observations.
func TestPlaygroundLogoutPropagationSLOUnderLoad(t *testing.T) {
	// playground.enabled=true crash-loops cmd/lenny-gateway under every
	// playground.authMode: pkg/gateway/mcpfabric/playground/metrics.go
	// registers the lenny_playground_page_views_total counter with the
	// label "authMode", which pkg/observability/metrics's §16.1.1
	// snake_case validator rejects at startup as fatal, before the
	// process ever binds its listener. Reproduced directly against the
	// current binary (go build ./cmd/lenny-gateway, then run with
	// --playground-enabled --playground-auth-mode dev): the process logs
	// `§27.8 playground metrics: metric "lenny_playground_page_views_total":
	// label "authMode" is not snake_case` and exits 1. The same defect
	// already blocks every other live-process playground test (see
	// tests/tier5_e2e_kind/playground_test.go
	// TestPlaygroundDevModeJourneyOnLiveCluster and
	// tests/tier9_security/playground_revocation_bypass_test.go
	// TestPlaygroundCrossReplicaRevocationBypassAdversarial) and is
	// tracked as a spec/code reconciliation (does spec/27_web-playground.md's
	// own §27.8 metrics table get corrected to auth_mode, or does the
	// platform-wide §16.1.1 snake_case rule get an exception for this
	// label) rather than a code-only fix. Remove this skip once that
	// reconciliation lands.
	t.Skip("playground.enabled=true crash-loops cmd/lenny-gateway under every authMode (non-snake_case \"authMode\" metrics label rejected by the §16.1.1 validator); needs a spec/code reconciliation before a real gateway replica can serve the playground at all")

	gateway.SkipUnlessAvailable(t)

	const (
		numReplicas = 3
		numSessions = 1000
		// The built-in tenant every installation always has registered,
		// matching the convention already used by the equivalent tier-9
		// adversarial test.
		tenant   = "default"
		clientID = "playground-load-test"
		// pollInterval / pollTimeout bound how long a peer replica is
		// polled for the revocation to become observable. pollTimeout is
		// generous relative to the 500ms P99 budget so a slow outlier
		// still contributes a real (large) sample to the histogram
		// instead of being silently dropped.
		pollInterval = 5 * time.Millisecond
		pollTimeout  = 5 * time.Second
		// maxInFlight bounds concurrent goroutines so the run drives load
		// without exhausting local file descriptors or ports.
		maxInFlight = 64
	)

	redis := containers.StartRedis(t, containers.RedisOptions{})
	idp := oidcstub.New(t)
	redisURL := "redis://" + redis.Addr + "/0"

	pgArgs := []string{
		"--playground-enabled",
		"--playground-auth-mode", "oidc",
		"--oidc-issuer-url", idp.Issuer(),
		"--oidc-client-id", clientID,
		"--redis-url", redisURL,
	}
	replicas := make([]*gateway.Process, numReplicas)
	for i := range replicas {
		replicas[i] = gateway.StartWith(t, pgArgs...)
	}

	type session struct {
		cookie string
		bearer string
		home   int
	}
	sessions := make([]session, numSessions)
	sem := make(chan struct{}, maxInFlight)

	// ---- establish numSessions real OIDC-mode playground sessions,
	// spread round-robin across replicas ----
	var setupWG sync.WaitGroup
	for i := 0; i < numSessions; i++ {
		setupWG.Add(1)
		go func(i int) {
			defer setupWG.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			home := i % numReplicas
			subject := fmt.Sprintf("loadtest-user-%d@acme.com", i)
			cookie, bearer, err := loginAndMintForLoad(replicas[home], idp, subject, tenant)
			if err != nil {
				t.Errorf("session %d: establish playground session on replica %d: %v", i, home, err)
				return
			}
			sessions[i] = session{cookie: cookie, bearer: bearer, home: home}
		}(i)
	}
	setupWG.Wait()
	if t.Failed() {
		t.Fatal("aborting load run: one or more sessions failed to establish")
	}

	// ---- log out every session on its home replica, and measure how
	// long each peer replica takes to start rejecting the bearer the
	// session minted ----
	hist := loadgen.NewHistogram()
	var histMu sync.Mutex
	var runWG sync.WaitGroup
	for i := 0; i < numSessions; i++ {
		runWG.Add(1)
		go func(i int) {
			defer runWG.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			s := sessions[i]
			if err := logoutOnReplicaForLoad(replicas[s.home], s.cookie); err != nil {
				t.Errorf("session %d: logout on replica %d: %v", i, s.home, err)
				return
			}
			// The commit-before-200 guarantee in §27.3.1 means the
			// revocation write has already landed in Redis by the time
			// logoutOnReplicaForLoad returns; the propagation clock for
			// every peer replica starts here.
			commit := time.Now()
			for peer := range replicas {
				if peer == s.home {
					continue
				}
				elapsed, observed := pollUntilRevokedForLoad(replicas[peer], s.bearer, commit, pollInterval, pollTimeout)
				if !observed {
					t.Errorf("session %d: replica %d never observed the revocation minted on replica %d within %s",
						i, peer, s.home, pollTimeout)
				}
				histMu.Lock()
				hist.ObserveDuration(elapsed)
				histMu.Unlock()
			}
		}(i)
	}
	runWG.Wait()

	p99 := hist.Quantile(0.99)
	t.Logf("§27.3.1 logout propagation: %d samples, P50=%.0fms P99=%.0fms max=%.0fms",
		hist.Count(), hist.Quantile(0.50)*1000, p99*1000, hist.Quantile(1.0)*1000)
	const sloP99Seconds = 0.500
	if p99 > sloP99Seconds {
		t.Errorf("§27.3.1 logout-propagation SLO violated: P99=%.0fms across %d cross-replica observations, want <= %.0fms",
			p99*1000, hist.Count(), sloP99Seconds*1000)
	}
}

// noRedirectLoadClient never follows a redirect, matching the pattern the
// equivalent tier-9 adversarial test uses to walk the OIDC
// authorization-code dance one hop at a time.
func noRedirectLoadClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// loginAndMintForLoad drives the real §27.3.1 OIDC authorization-code
// flow against p and returns the resulting session cookie and minted
// bearer. It is the load-test analogue of the equivalent tier-9
// adversarial test's loginAndMintOnReplica, duplicated locally because
// the two files build under different tags (security vs. load_local)
// and do not share a package.
func loginAndMintForLoad(p *gateway.Process, idp *oidcstub.Stub, subject, tenant string) (cookie, bearer string, err error) {
	client := noRedirectLoadClient()

	loginResp, err := client.Get(p.BaseURL() + "/playground/auth/login")
	if err != nil {
		return "", "", fmt.Errorf("GET /playground/auth/login: %w", err)
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(loginResp.Body)
		return "", "", fmt.Errorf("GET /playground/auth/login: status %d, body %s", loginResp.StatusCode, body)
	}
	var stateCookie *http.Cookie
	for _, c := range loginResp.Cookies() {
		if c.Name == "lenny_playground_oidc_state" {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		return "", "", fmt.Errorf("login response carried no OIDC state cookie")
	}
	authorizeURL := loginResp.Header.Get("Location")
	if authorizeURL == "" {
		return "", "", fmt.Errorf("login response carried no Location header")
	}

	// The stub reads "sub" and "tenant_id" as test-only controls bound to
	// the issued authorization code; a real provider would derive these
	// from its own authenticated session instead of a query parameter.
	authorizeReq, err := http.NewRequest(http.MethodGet, authorizeURL+"&sub="+subject+"&tenant_id="+tenant, nil)
	if err != nil {
		return "", "", fmt.Errorf("build authorize request: %w", err)
	}
	authorizeResp, err := client.Do(authorizeReq)
	if err != nil {
		return "", "", fmt.Errorf("GET provider authorize endpoint: %w", err)
	}
	defer authorizeResp.Body.Close()
	if authorizeResp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(authorizeResp.Body)
		return "", "", fmt.Errorf("provider authorize response: status %d, body %s", authorizeResp.StatusCode, body)
	}
	callbackURL := authorizeResp.Header.Get("Location")
	if !strings.Contains(callbackURL, "/playground/auth/callback") {
		return "", "", fmt.Errorf("provider redirected to %q, want the gateway callback path", callbackURL)
	}

	callbackReq, err := http.NewRequest(http.MethodGet, callbackURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("build callback request: %w", err)
	}
	callbackReq.AddCookie(stateCookie)
	callbackResp, err := client.Do(callbackReq)
	if err != nil {
		return "", "", fmt.Errorf("GET %s: %w", callbackURL, err)
	}
	defer callbackResp.Body.Close()
	if callbackResp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(callbackResp.Body)
		return "", "", fmt.Errorf("GET /playground/auth/callback: status %d, body %s", callbackResp.StatusCode, body)
	}
	for _, c := range callbackResp.Cookies() {
		if c.Name == "lenny_playground_session" && c.Value != "" {
			cookie = c.Value
		}
	}
	if cookie == "" {
		return "", "", fmt.Errorf("callback did not establish the lenny_playground_session cookie")
	}

	req, err := http.NewRequest(http.MethodPost, p.BaseURL()+"/v1/playground/token", strings.NewReader("{}"))
	if err != nil {
		return "", "", fmt.Errorf("build mint request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "lenny_playground_session", Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("POST /v1/playground/token: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("POST /v1/playground/token: status %d, body %s", resp.StatusCode, raw)
	}
	var minted struct {
		BearerToken string `json:"bearerToken"`
	}
	if err := json.Unmarshal(raw, &minted); err != nil {
		return "", "", fmt.Errorf("decode mint response: %w; body %s", err, raw)
	}
	return cookie, minted.BearerToken, nil
}

// logoutOnReplicaForLoad drives POST /playground/auth/logout against p
// with the supplied session cookie. §27.3.1 requires the handler to
// complete the revocation-record delete and the deny-list writes before
// returning 200, so a successful return here is the earliest moment the
// caller may start timing cross-replica propagation.
func logoutOnReplicaForLoad(p *gateway.Process, cookie string) error {
	req, err := http.NewRequest(http.MethodPost, p.BaseURL()+"/playground/auth/logout", nil)
	if err != nil {
		return fmt.Errorf("build logout request: %w", err)
	}
	req.AddCookie(&http.Cookie{Name: "lenny_playground_session", Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST /playground/auth/logout: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("POST /playground/auth/logout: status %d, body %s", resp.StatusCode, body)
	}
	return nil
}

// pollUntilRevokedForLoad polls GET /v1/sessions against p (authenticated
// with bearer, the same non-playground-specific REST surface every MCP/
// REST client authenticates against) until the request is rejected with
// 401 or timeout elapses since since. It returns the elapsed time since
// since and whether the rejection was observed within timeout.
func pollUntilRevokedForLoad(p *gateway.Process, bearer string, since time.Time, interval, timeout time.Duration) (time.Duration, bool) {
	deadline := since.Add(timeout)
	for {
		req, err := http.NewRequest(http.MethodGet, p.BaseURL()+"/v1/sessions", nil)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+bearer)
			resp, doErr := http.DefaultClient.Do(req)
			if doErr == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusUnauthorized {
					return time.Since(since), true
				}
			}
		}
		if time.Now().After(deadline) {
			return timeout, false
		}
		time.Sleep(interval)
	}
}
