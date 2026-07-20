// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/ops/gateway"
)

// newProbeGatewayClient returns a gateway admin-API client pointed at srv.
func newProbeGatewayClient(t *testing.T, baseURL string) *gateway.Client {
	t.Helper()
	c, err := gateway.NewClient(gateway.Config{BaseURL: baseURL})
	if err != nil {
		t.Fatalf("build gateway client: %v", err)
	}
	return c
}

// TestProbeGatewayReportsUnreachableWhenTheBufferQueryIsRefused covers the
// §25.5 case-1 branch of the degradation matrix. The gateway half of the
// source-health signal gates a data path, so it is measured on the surface
// that data path consumes: the fall-back serves gateway-originated events by
// fanning GET /v1/admin/events/buffer across the replicas. A gateway process
// that is serving but refuses lenny-ops that query cannot answer the fall-back,
// so classifying it up would route every Redis-outage read to a source that
// returns nothing and label the empty result a healthy gateway-buffer page.
//
// spec: §25.5 (degradation matrix — actualSource names the source the response
// was served from), §25.4 (lenny-ops reaches the admin API as a service
// account holding platform-admin).
func TestProbeGatewayReportsUnreachableWhenTheBufferQueryIsRefused_spec_25_5(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{name: "authorization refused", status: http.StatusForbidden},
		{name: "credential rejected", status: http.StatusUnauthorized},
		{name: "endpoint absent", status: http.StatusNotFound},
		{name: "gateway degraded", status: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			if probeGateway(context.Background(), newProbeGatewayClient(t, srv.URL)) {
				t.Fatalf("a gateway answering HTTP %d on the event-buffer query reported reachable; "+
					"the §25.5 case-1 fall-back would be routed to a source that serves nothing and "+
					"the empty result labelled a healthy gateway-buffer page", tc.status)
			}
		})
	}
}

// TestProbeGatewayReportsHealthyGatewayReachable pins the ordinary case: a
// gateway that serves lenny-ops the §25.3 event-buffer query is reachable, and
// the probe measures that endpoint rather than a liveness path whose outcome
// is independent of whether the fall-back can be served.
//
// spec: §25.5 (source health for the degradation matrix).
func TestProbeGatewayReportsHealthyGatewayReachable_spec_25_5(t *testing.T) {
	var path atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if !probeGateway(context.Background(), newProbeGatewayClient(t, srv.URL)) {
		t.Fatal("a gateway serving the event-buffer query reported unreachable")
	}
	want, _, _ := strings.Cut(gatewayBufferProbePath, "?")
	if got := path.Load(); got != want {
		t.Fatalf("probe requested %v, want the §25.3 event-buffer endpoint %q the fall-back consumes", got, want)
	}
}

// TestProbeGatewayReportsUnreachableOnTransportFailure covers the §25.5
// case-4 branch: only a transport failure (no answer at all) reports the
// gateway down, which with Redis also down escalates the read surface to the
// dual-outage 503.
//
// spec: §25.5 (degradation matrix — both sources unreachable).
func TestProbeGatewayReportsUnreachableOnTransportFailure_spec_25_5(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := srv.URL
	srv.Close() // nothing is listening on addr any more
	if probeGateway(context.Background(), newProbeGatewayClient(t, addr)) {
		t.Fatal("a gateway that could not be dialled reported reachable; the dual-outage case would never be entered")
	}
}

// TestProbeGatewayWithoutClientReportsReachable covers the single-process
// dev deployment with no gateway wired: the read surface must not escalate
// to the dual-outage 503 over an absent optional dependency.
//
// spec: §25.5 (degradation matrix).
func TestProbeGatewayWithoutClientReportsReachable_spec_25_5(t *testing.T) {
	if !probeGateway(context.Background(), nil) {
		t.Fatal("an unwired gateway client reported unreachable")
	}
}

// TestProbeRedisReportsUnreachable covers the Redis half of the source
// health signal: an unwired client and an unreachable server both report
// down, which is what moves the read surface onto the fall-back.
//
// spec: §25.5 (degradation matrix — Redis reachability).
func TestProbeRedisReportsUnreachable_spec_25_5(t *testing.T) {
	if probeRedis(context.Background(), nil) {
		t.Fatal("an unwired Redis client reported reachable")
	}
	// Port 1 is reserved and never serves Redis, so the PING cannot connect.
	cli := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	defer func() { _ = cli.Close() }()
	if probeRedis(context.Background(), cli) {
		t.Fatal("an unreachable Redis reported reachable")
	}
}

// TestSourceHealthProbeStartsOptimisticAndRefreshes covers the cached
// signal's cold start and its refresh loop: it starts with both sources
// reachable so a cold start serves rather than 503-ing, and the first
// refresh resolves the live state. A first refresh that finds Redis up is
// not a recovery edge, so the best-effort recovery flush does not fire for
// an outage that never happened.
//
// spec: §25.5 (degradation matrix, best-effort recovery flush).
func TestSourceHealthProbeStartsOptimisticAndRefreshes_spec_25_5(t *testing.T) {
	p := newSourceHealthProbe()
	if !p.RedisAvailable() || !p.GatewayAvailable() {
		t.Fatal("a fresh probe reported a source down; a cold start must serve rather than report an outage it has not observed")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var flushes, downs atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		// A nil Redis client resolves to unreachable on the first refresh.
		p.run(ctx, 50*time.Millisecond, nil, newProbeGatewayClient(t, srv.URL), redisEdgeCallbacks{
			onRedisDown:      func() { downs.Add(1) },
			onRedisRecovered: func(context.Context) { flushes.Add(1) },
		})
	}()

	deadline := time.Now().Add(5 * time.Second)
	for p.RedisAvailable() {
		if time.Now().After(deadline) {
			t.Fatal("the refresh loop never observed the unreachable Redis source")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !p.GatewayAvailable() {
		t.Fatal("the refresh loop reported a gateway answering 200 as unreachable")
	}
	if n := flushes.Load(); n != 0 {
		t.Fatalf("the recovery flush fired %d time(s) with no down-to-up edge; it must fire only after Redis recovers", n)
	}
	// The up-to-down edge opens the recovery flush's outage window, so the
	// flush that follows a later recovery re-emits the events buffered during
	// the outage rather than the whole local ring.
	if n := downs.Load(); n != 1 {
		t.Fatalf("the outage-window edge fired %d time(s) for one observed Redis outage; want exactly 1", n)
	}
	cancel()
	<-done
}
