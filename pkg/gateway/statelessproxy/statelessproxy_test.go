// SPDX-License-Identifier: MIT

package statelessproxy

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/tenantaffinity"
)

// podBackend stands up an httptest server bound to a loopback IP:port the
// router will hand back as a "pod IP", so the proxy's Service-bypass dial
// reaches a real backend.
func podBackend(t *testing.T, handler http.HandlerFunc) (ip string, port int, stop func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(lis) }()
	host, p, _ := net.SplitHostPort(lis.Addr().String())
	pn, _ := strconv.Atoi(p)
	return host, pn, func() { _ = srv.Close() }
}

func tenantHeader(r *http.Request) (string, error) { return r.Header.Get("X-Tenant"), nil }

type fakeLabeler struct {
	mu    sync.Mutex
	calls []string // podIP|tenant
	err   error
}

func (f *fakeLabeler) LabelTenant(_ context.Context, podIP, tenantID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, podIP+"|"+tenantID)
	return f.err
}

// spec: §5.2 line 500 — a stateless request reverse-proxies to the
// tenant-pinned pod IP the router returns, bypassing the Service LB, and
// the pin label is stamped on the newly-pinned pod.
func TestProxyRoutesToPinnedPodAndLabelsOnFirstPin(t *testing.T) {
	ip, port, stop := podBackend(t, func(w http.ResponseWriter, r *http.Request) {
		// The runtime sees the tenant attribution header and the verbatim body.
		if got := r.Header.Get("X-Lenny-Tenant-Id"); got != "acme" {
			t.Errorf("backend tenant header = %q, want acme", got)
		}
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write([]byte("echo:" + string(body)))
	})
	defer stop()

	r := tenantaffinity.New("acme-stateless", nil)
	r.UpdateEndpoints([]tenantaffinity.Endpoint{{PodIP: ip, Ready: true}})
	labeler := &fakeLabeler{}
	p := &Proxy{Router: r, Tenant: tenantHeader, Labeler: labeler, PodPort: port}

	req := httptest.NewRequest(http.MethodPost, "/run", strings.NewReader("hi"))
	req.Header.Set("X-Tenant", "acme")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "echo:hi" {
		t.Fatalf("body = %q, want echo:hi", rec.Body.String())
	}
	labeler.mu.Lock()
	defer labeler.mu.Unlock()
	if len(labeler.calls) != 1 || labeler.calls[0] != ip+"|acme" {
		t.Fatalf("label calls = %v, want one %s|acme", labeler.calls, ip)
	}
	// The router released the in-flight count after the request finished.
	if got := r.ActiveCount(); got != 0 {
		t.Fatalf("ActiveCount after request = %d, want 0 (released)", got)
	}
}

// spec: §5.2 line 502 — a second request for the same tenant reuses the
// already-pinned pod and does NOT re-stamp the label.
func TestProxyReusesPinWithoutRelabel(t *testing.T) {
	ip, port, stop := podBackend(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	defer stop()
	r := tenantaffinity.New("acme-stateless", nil)
	r.UpdateEndpoints([]tenantaffinity.Endpoint{{PodIP: ip, Ready: true}})
	labeler := &fakeLabeler{}
	p := &Proxy{Router: r, Tenant: tenantHeader, Labeler: labeler, PodPort: port}

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Tenant", "acme")
		p.ServeHTTP(httptest.NewRecorder(), req)
	}
	labeler.mu.Lock()
	defer labeler.mu.Unlock()
	if len(labeler.calls) != 1 {
		t.Fatalf("label calls = %d, want exactly 1 (first pin only)", len(labeler.calls))
	}
}

// spec: §5.2 line 519 — exhaustion (no available pod) returns
// WARM_POOL_EXHAUSTED with details.reason=no_idle_pods and Retry-After.
func TestProxyEmptyPoolWarmPoolExhausted(t *testing.T) {
	r := tenantaffinity.New("acme-stateless", nil)
	// No endpoints.
	p := &Proxy{Router: r, Tenant: tenantHeader}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant", "acme")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After header")
	}
	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "WARM_POOL_EXHAUSTED" {
		t.Errorf("code = %q, want WARM_POOL_EXHAUSTED", env.Error.Code)
	}
	if env.Error.Details["reason"] != "no_idle_pods" {
		t.Errorf("reason = %v, want no_idle_pods", env.Error.Details["reason"])
	}
	if !env.Error.Retryable {
		t.Error("WARM_POOL_EXHAUSTED must classify retryable=true")
	}
	if got := r.ActiveCount(); got != 0 {
		t.Errorf("ActiveCount after exhausted route = %d, want 0 (no Release leak)", got)
	}
}

// spec: §5.2 line 500 — a missing authenticated tenant is rejected
// before any routing.
func TestProxyRejectsMissingTenant(t *testing.T) {
	r := tenantaffinity.New("acme-stateless", nil)
	p := &Proxy{Router: r, Tenant: tenantHeader}
	req := httptest.NewRequest(http.MethodGet, "/", nil) // no X-Tenant
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// A dial failure to the pinned pod yields BAD_GATEWAY and still releases
// the in-flight count.
func TestProxyDialFailureBadGateway(t *testing.T) {
	r := tenantaffinity.New("acme-stateless", nil)
	// Pin a pod IP:port with nothing listening.
	r.UpdateEndpoints([]tenantaffinity.Endpoint{{PodIP: "127.0.0.1", Ready: true}})
	p := &Proxy{Router: r, Tenant: tenantHeader, PodPort: 1} // port 1: refused
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant", "acme")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if got := r.ActiveCount(); got != 0 {
		t.Errorf("ActiveCount after dial failure = %d, want 0 (released)", got)
	}
}
