// SPDX-License-Identifier: MIT

package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/gateway"
)

// TestClient_GetSendsBearer verifies the §25.4 GatewayClient stamps
// the service-account token on every request and decodes the JSON
// body the gateway returns.
func TestClient_GetSendsBearer(t *testing.T) {
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"pool":"default-gvisor","desired":12}`))
	}))
	defer srv.Close()

	c, err := gateway.NewClient(gateway.Config{
		BaseURL: srv.URL,
		Token:   gateway.StaticToken("sa-token"),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	var got map[string]any
	if err := c.Get(context.Background(), "/v1/admin/pools/default-gvisor", &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if captured != "Bearer sa-token" {
		t.Errorf("Authorization = %q, want Bearer sa-token", captured)
	}
	if got["pool"] != "default-gvisor" {
		t.Errorf("Get body = %v, want pool field decoded", got)
	}
}

// TestClient_GetNon2xxReturnsHTTPError checks the §25.4 error envelope
// surface: non-2xx responses bubble out as *gateway.HTTPError carrying
// the status and body so callers can branch on POLICY/AUTH codes.
func TestClient_GetNon2xxReturnsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"SCOPE_FORBIDDEN"}}`))
	}))
	defer srv.Close()

	c, _ := gateway.NewClient(gateway.Config{BaseURL: srv.URL, Token: gateway.StaticToken("t")})
	err := c.Get(context.Background(), "/v1/admin/locks", nil)
	var httpErr *gateway.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("Get err = %v, want *HTTPError", err)
	}
	if httpErr.Status != http.StatusForbidden {
		t.Errorf("Status = %d, want 403", httpErr.Status)
	}
	if !strings.Contains(string(httpErr.Body), "SCOPE_FORBIDDEN") {
		t.Errorf("Body = %q, want SCOPE_FORBIDDEN payload", httpErr.Body)
	}
}

// TestClient_PostJSONRoundtrip exercises the §25.4 POST path: the
// request body is marshalled JSON, the gateway response is decoded
// back into the caller's struct.
func TestClient_PostJSONRoundtrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"echo": body})
	}))
	defer srv.Close()

	c, _ := gateway.NewClient(gateway.Config{BaseURL: srv.URL})
	var out map[string]any
	if err := c.PostJSON(context.Background(), "/v1/admin/pools/p/scale",
		map[string]any{"desired": 5}, &out); err != nil {
		t.Fatalf("PostJSON: %v", err)
	}
	echo, _ := out["echo"].(map[string]any)
	if v, _ := echo["desired"].(float64); v != 5 {
		t.Errorf("echo.desired = %v, want 5 round-trip", v)
	}
}

// TestClient_TokenSourceError aborts the request when the token
// source fails, so a refresh outage never leaks an empty
// Authorization header.
func TestClient_TokenSourceError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("server should not be hit when token source fails")
	}))
	defer srv.Close()

	c, _ := gateway.NewClient(gateway.Config{
		BaseURL: srv.URL,
		Token:   failingTokenSource{err: errors.New("refresh failed")},
	})
	if err := c.Get(context.Background(), "/v1/admin/pools/p", nil); err == nil ||
		!strings.Contains(err.Error(), "refresh failed") {
		t.Errorf("Get err = %v, want refresh-failed surfacing", err)
	}
}

// TestClient_NewClientRejectsEmptyBaseURL guards the §25.4 explicit-
// configuration rule: a misconfigured chart must fail at construction
// rather than silently hit a default.
func TestClient_NewClientRejectsEmptyBaseURL(t *testing.T) {
	if _, err := gateway.NewClient(gateway.Config{}); err == nil {
		t.Errorf("NewClient with empty BaseURL succeeded; want error")
	}
}

// TestFanOutGet_Discovery exercises the §25.4 per-replica fan-out:
// every endpoint discovery returns is queried concurrently and the
// per-replica results are returned in the order discovery emitted.
func TestFanOutGet_Discovery(t *testing.T) {
	var hits atomic.Int64
	makeReplica := func(value int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			fmt.Fprintf(w, `{"replica":%d}`, value)
		}))
	}
	r1 := makeReplica(1)
	r2 := makeReplica(2)
	r3 := makeReplica(3)
	defer r1.Close()
	defer r2.Close()
	defer r3.Close()

	c, _ := gateway.NewClient(gateway.Config{
		BaseURL:   r1.URL,
		Discovery: gateway.StaticDiscovery{r1.URL, r2.URL, r3.URL},
	})
	results, err := c.FanOutGet(context.Background(), "/v1/admin/events/buffer")
	if err != nil {
		t.Fatalf("FanOutGet: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("len(results) = %d, want 3", len(results))
	}
	if hits.Load() != 3 {
		t.Errorf("fan-out hits = %d, want 3 concurrent replicas dialled", hits.Load())
	}
	for i, r := range results {
		if r.Err != nil {
			t.Errorf("replica %d error: %v", i, r.Err)
		}
	}
}

// TestFanOutGet_NoDiscovery is the §25.4 ErrFanOutUnavailable path:
// without a ReplicaDiscovery, fan-out short-circuits so callers can
// fall back to the ClusterIP path.
func TestFanOutGet_NoDiscovery(t *testing.T) {
	c, _ := gateway.NewClient(gateway.Config{BaseURL: "https://example"})
	if _, err := c.FanOutGet(context.Background(), "/v1/admin/health"); !errors.Is(err, gateway.ErrFanOutUnavailable) {
		t.Errorf("FanOutGet err = %v, want ErrFanOutUnavailable", err)
	}
}

// TestFanOutGet_PartialFailure verifies the §25.4 partial-
// aggregation guarantee: a single unresponsive replica does not
// abort the fan-out — its error is reported in its slot.
func TestFanOutGet_PartialFailure(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{}`)
	}))
	defer ok.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(150 * time.Millisecond)
		fmt.Fprint(w, `{}`)
	}))
	defer slow.Close()

	c, _ := gateway.NewClient(gateway.Config{
		BaseURL:       ok.URL,
		Discovery:     gateway.StaticDiscovery{ok.URL, slow.URL},
		FanOutTimeout: 25 * time.Millisecond,
	})
	results, err := c.FanOutGet(context.Background(), "/v1/admin/health")
	if err != nil {
		t.Fatalf("FanOutGet: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].Err != nil {
		t.Errorf("fast replica errored: %v", results[0].Err)
	}
	if results[1].Err == nil {
		t.Errorf("slow replica should have timed out under FanOutTimeout")
	}
}

// TestProxyAdminCall_ForbiddenReturnsVerbatim verifies the §25.12
// identity-forwarding proxy re-emits a gateway RBAC denial verbatim:
// a 403 comes back as (403, body, nil) with no *HTTPError wrapping so
// the denial reaches the agent unchanged.
func TestProxyAdminCall_ForbiddenReturnsVerbatim(t *testing.T) {
	const denial = `{"error":{"code":"RBAC_FORBIDDEN","message":"platform-admin required"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(denial))
	}))
	defer srv.Close()

	c, _ := gateway.NewClient(gateway.Config{BaseURL: srv.URL, Token: gateway.StaticToken("sa-token")})
	status, body, err := c.ProxyAdminCall(context.Background(), http.MethodPost, "/v1/admin/tenants", nil, nil)
	if err != nil {
		t.Fatalf("ProxyAdminCall err = %v, want nil for a completed 403", err)
	}
	var httpErr *gateway.HTTPError
	if errors.As(err, &httpErr) {
		t.Fatalf("ProxyAdminCall wrapped into *HTTPError; want raw passthrough")
	}
	if status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", status)
	}
	if string(body) != denial {
		t.Errorf("body = %q, want verbatim %q", body, denial)
	}
}

// TestProxyAdminCall_SuccessReturnsBody verifies the §25.12 proxy
// returns the gateway 200 body verbatim.
func TestProxyAdminCall_SuccessReturnsBody(t *testing.T) {
	const payload = `{"pool":"default-gvisor","desired":12}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	c, _ := gateway.NewClient(gateway.Config{BaseURL: srv.URL})
	status, body, err := c.ProxyAdminCall(context.Background(), http.MethodGet, "/v1/admin/pools/default-gvisor", nil, nil)
	if err != nil {
		t.Fatalf("ProxyAdminCall: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if string(body) != payload {
		t.Errorf("body = %q, want %q", body, payload)
	}
}

// TestProxyAdminCall_ForwardsHeadersVerbatimAndSetsNoSABearer verifies
// the §25.12 identity-forwarding contract: the caller-assembled headers
// (the forwarded Authorization bearer and correlation headers) reach the
// gateway verbatim, and the client stamps no service-account bearer of
// its own even when a Token source is configured, so the gateway
// re-authorizes as the real caller (§25.12 Security Model layer 3).
func TestProxyAdminCall_ForwardsHeadersVerbatimAndSetsNoSABearer(t *testing.T) {
	var gotAuth, gotOp, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotOp = r.Header.Get("X-Lenny-Operation-ID")
		gotCT = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	// A Token source is configured to prove ProxyAdminCall ignores it
	// and never overrides the forwarded caller identity with the SA bearer.
	c, _ := gateway.NewClient(gateway.Config{BaseURL: srv.URL, Token: gateway.StaticToken("sa-token")})
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer caller-jwt")
	hdr.Set("X-Lenny-Operation-ID", "op-42")
	hdr.Set("Content-Type", "application/json")
	body := []byte(`{"name":"acme"}`)
	if _, _, err := c.ProxyAdminCall(context.Background(), http.MethodPost, "/v1/admin/tenants", body, hdr); err != nil {
		t.Fatalf("ProxyAdminCall: %v", err)
	}
	if gotAuth != "Bearer caller-jwt" {
		t.Errorf("Authorization = %q, want forwarded caller bearer, not the SA token", gotAuth)
	}
	if gotOp != "op-42" {
		t.Errorf("X-Lenny-Operation-ID = %q, want forwarded verbatim", gotOp)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q, want forwarded verbatim", gotCT)
	}
}

// TestProxyAdminCall_TransportFailureReturnsErr verifies the §25.12
// proxy returns a non-nil err only when the request never completes:
// a dial failure surfaces as an error so the invoker maps it to
// ENDPOINT_UNAVAILABLE.
func TestProxyAdminCall_TransportFailureReturnsErr(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // close so the dial fails

	c, _ := gateway.NewClient(gateway.Config{BaseURL: url, PerRequestTimeout: 500 * time.Millisecond})
	status, body, err := c.ProxyAdminCall(context.Background(), http.MethodGet, "/v1/admin/pools", nil, nil)
	if err == nil {
		t.Fatalf("ProxyAdminCall err = nil, want transport failure")
	}
	if status != 0 || body != nil {
		t.Errorf("status/body = %d/%q, want 0/nil on transport failure", status, body)
	}
}

// failingTokenSource is a TokenSource that always errors. Used to
// confirm the client surfaces refresh failures.
type failingTokenSource struct{ err error }

func (f failingTokenSource) Token(context.Context) (string, error) { return "", f.err }
