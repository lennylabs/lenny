// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §4.3 canonical token endpoint served
// through the gateway reverse proxy. Boots cmd/lenny-token-service and
// cmd/lenny-gateway as real subprocesses, wires the gateway's
// --token-service-http-url at the Token Service's HTTP listener, and
// drives an RFC 8693 token exchange at the gateway's public
// POST /v1/oauth/token. The assertions gate that:
//
//   - The gateway forwards /v1/oauth/* to the deployed Token Service
//     binary, and the Token Service's own RFC 8693 error envelope
//     travels back through the proxy unchanged. The Token Service is
//     the component that authenticates the caller (the §4.3 trust
//     boundary), so a request that reaches it with no caller credential
//     is rejected with the Token Service's 401 invalid_client, not a
//     gateway-minted response.
//   - With --token-service-http-url unset the gateway does not serve
//     /v1/oauth/ in-process at all: the endpoint 404s, confirming the
//     gateway no longer mints tokens itself and the canonical surface
//     exists only when it proxies to the actual minter.
//
// The full success path (a 200 with a verifiable access_token) cannot
// run in this harness: the deployed Token Service signs with a random
// KMS-sealed key generated at process start, so a test cannot forge a
// caller/subject token the deployed binary would accept, and there is
// no test-only signing-key injection on a security component. These
// assertions instead pin the observable proxy contract end to end: an
// exchange request reaches the deployed minter and the minter's
// response returns to the caller.

package tier4_integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	tokensvc "github.com/lennylabs/lenny/tests/testinfra/tokenservice"
)

// waitHTTPListener polls addr until a TCP connection succeeds or the
// deadline elapses. The token-service harness readiness gate waits on
// the gRPC port; the HTTP token-exchange listener comes up in the same
// serve batch, so a short poll here removes the boot race before the
// proxy forwards the first request.
func waitHTTPListener(t *testing.T, addr string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("token-service HTTP %s did not accept connections within %s", addr, d)
}

// spec: 4.3 / 15.1
// diagnosis: the gateway reverse-proxies POST /v1/oauth/token to the
// deployed lenny-token-service and returns the Token Service's RFC 8693
// response. A failure here means the §4.3 canonical-endpoint proxy is
// not wired: either the gateway did not forward /v1/oauth/* to the
// Token Service HTTP listener, or it answered the request in-process
// instead of delegating to the actual minter. The token exchange
// reaching the Token Service with no caller credential must surface the
// Token Service's own 401 invalid_client, proving the request crossed
// the proxy to the binary that authenticates callers and mints tokens.
func TestOAuthTokenExchangeProxiedToDeployedTokenService(t *testing.T) {
	t.Parallel()
	tokensvc.SkipUnlessAvailable(t)
	ts := tokensvc.Start(t)
	waitHTTPListener(t, ts.HTTPAddr(), 5*time.Second)

	// §4.3 line 194: the gateway reverse-proxies /v1/oauth/* to the
	// Token Service when --token-service-http-url is set, so the
	// canonical POST /v1/oauth/token is served by the actual minter.
	gw := gateway.StartWith(
		t,
		"--token-service-http-url", "http://"+ts.HTTPAddr(),
		"--no-environment-policy", "allow-all",
	)

	// A genuine RFC 8693 token-exchange form body carrying no
	// Authorization: Bearer caller token. No Authorization header is
	// sent so the gateway auth middleware does not attempt to verify a
	// (gateway-signed) bearer and short-circuit the request before the
	// proxy; the dev tenant/user headers resolve a principal so the
	// request passes the gateway middleware and reaches the proxy. The
	// Token Service is then the component that finds no caller
	// credential and rejects with 401 invalid_client.
	form := url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {"opaque-subject"},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:jwt"},
		"audience":           {"lenny-gateway"},
	}
	req, _ := http.NewRequest(http.MethodPost, gw.BaseURL()+"/v1/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Lenny-Tenant-ID", "tier4-oauth")
	req.Header.Set("X-Lenny-User-ID", "alice")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/oauth/token through gateway proxy: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (Token Service invalid_client through the proxy); body = %s",
			resp.StatusCode, http.StatusUnauthorized, raw)
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode RFC 8693 error envelope: %v; body = %s", err, raw)
	}
	// The Token Service's RFC 8693 envelope carries `error` and
	// `error_description`. A gateway-produced 401 does not use this
	// form, so the invalid_client code proves the deployed Token
	// Service produced the response after the proxy forwarded it.
	if env["error"] != "invalid_client" {
		t.Errorf("error = %v, want invalid_client (Token Service caller-auth rejection); body = %s", env["error"], raw)
	}
	if _, ok := env["error_description"]; !ok {
		t.Errorf("missing error_description in RFC 8693 envelope; body = %s", raw)
	}
}

// spec: 4.3
// diagnosis: with --token-service-http-url unset the gateway does not
// mount the /v1/oauth/ surface, so POST /v1/oauth/token 404s. A failure
// (any non-404) means the gateway still answers the canonical token
// endpoint in-process rather than delegating to the deployed minter,
// which is the pre-cutover behavior §4.3 removed: the Token Service is
// the only component that holds the signing key and mints bearer tokens.
func TestOAuthTokenSurfaceUnmountedWithoutProxy(t *testing.T) {
	t.Parallel()
	gateway.SkipUnlessAvailable(t)
	// No --token-service-http-url: the gateway leaves /v1/oauth/
	// unmounted.
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all")

	req, _ := http.NewRequest(http.MethodPost, gw.BaseURL()+"/v1/oauth/token", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Lenny-Tenant-ID", "tier4-oauth")
	req.Header.Set("X-Lenny-User-ID", "alice")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/oauth/token with proxy disabled: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (gateway serves no in-process /v1/oauth surface); body = %s",
			resp.StatusCode, http.StatusNotFound, raw)
	}
}
