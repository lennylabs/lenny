// SPDX-License-Identifier: MIT

package statelessrouting

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/tenantaffinity"
)

func backend(t *testing.T, h http.HandlerFunc) (ip string, port int, stop func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(lis) }()
	host, p, _ := net.SplitHostPort(lis.Addr().String())
	pn, _ := strconv.Atoi(p)
	return host, pn, func() { _ = srv.Close() }
}

type fixedLister struct {
	eps []tenantaffinity.Endpoint
}

func (f fixedLister) ListEndpoints(context.Context) ([]tenantaffinity.Endpoint, error) {
	return f.eps, nil
}

func tenantHdr(r *http.Request) (string, error) { return r.Header.Get("X-Tenant"), nil }

// spec: §5.2 line 500 — a /v1/stateless/{pool}/... request routes to a
// pinned pod for a concurrent-stateless pool, rebasing the path so the
// runtime sees its own path.
func TestManagerRoutesStatelessPool(t *testing.T) {
	var gotPath string
	ip, port, stop := backend(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte("ok"))
	})
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := New(ctx, Options{
		Resolver: func(context.Context, string) (PoolInfo, bool, error) {
			return PoolInfo{Stateless: true, MaxConcurrent: 8}, true, nil
		},
		NewLister: func(string) tenantaffinity.EndpointLister {
			return fixedLister{eps: []tenantaffinity.Endpoint{{PodIP: ip, Ready: true}}}
		},
		Tenant:  tenantHdr,
		PodPort: port,
	})

	// Wait for the lazily-started poller to populate the router.
	req := httptest.NewRequest(http.MethodGet, "/v1/stateless/acme-pool/run/task", nil)
	req.Header.Set("X-Tenant", "acme")
	waitOK(t, m, req)

	if gotPath != "/run/task" {
		t.Fatalf("backend path = %q, want /run/task (prefix stripped)", gotPath)
	}
}

// A pool that is not concurrent-stateless is rejected: the stateless
// ingress must never proxy to a session/task pool.
func TestManagerRejectsNonStatelessPool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := New(ctx, Options{
		Resolver: func(context.Context, string) (PoolInfo, bool, error) {
			return PoolInfo{Stateless: false}, true, nil // exists but session-mode
		},
		NewLister: func(string) tenantaffinity.EndpointLister { return fixedLister{} },
		Tenant:    tenantHdr,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/stateless/session-pool/x", nil)
	req.Header.Set("X-Tenant", "acme")
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a non-stateless pool", rec.Code)
	}
}

// A non-existent pool is rejected with 404.
func TestManagerRejectsUnknownPool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := New(ctx, Options{
		Resolver:  func(context.Context, string) (PoolInfo, bool, error) { return PoolInfo{}, false, nil },
		NewLister: func(string) tenantaffinity.EndpointLister { return fixedLister{} },
		Tenant:    tenantHdr,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/stateless/ghost/x", nil)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// A malformed ingress path (no pool segment) is rejected.
func TestManagerRejectsMissingPoolSegment(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := New(ctx, Options{
		Resolver:  func(context.Context, string) (PoolInfo, bool, error) { return PoolInfo{Stateless: true}, true, nil },
		NewLister: func(string) tenantaffinity.EndpointLister { return fixedLister{} },
		Tenant:    tenantHdr,
	})
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/stateless/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// The route (and its poller) is built exactly once across concurrent
// first requests.
func TestManagerBuildsRouteOncePerPool(t *testing.T) {
	ip, port, stop := backend(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	defer stop()
	var listerBuilds int32
	var mu sync.Mutex
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := New(ctx, Options{
		Resolver: func(context.Context, string) (PoolInfo, bool, error) {
			return PoolInfo{Stateless: true}, true, nil
		},
		NewLister: func(string) tenantaffinity.EndpointLister {
			mu.Lock()
			listerBuilds++
			mu.Unlock()
			return fixedLister{eps: []tenantaffinity.Endpoint{{PodIP: ip, Ready: true}}}
		},
		Tenant:  tenantHdr,
		PodPort: port,
	})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/v1/stateless/p/x", nil)
			req.Header.Set("X-Tenant", "acme")
			m.ServeHTTP(httptest.NewRecorder(), req)
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if listerBuilds != 1 {
		t.Fatalf("lister builds = %d, want exactly 1 (one route/poller per pool)", listerBuilds)
	}
}

func waitOK(t *testing.T, m *Manager, req *http.Request) {
	t.Helper()
	for i := 0; i < 200; i++ {
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, req.Clone(req.Context()))
		if rec.Code == http.StatusOK {
			return
		}
		// The lazily-started poller may not have populated the router yet.
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("request never succeeded")
}
