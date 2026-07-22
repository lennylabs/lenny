// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// TestProbeGatewayReportsReachableWhenGatewayRefusesTheCall covers the §25.5
// case-1 branch of the degradation matrix. The matrix branches on whether the
// gateway is unreachable, so the gateway half of the source-health signal
// answers reachability alone: a gateway that answers at all is up, whatever the
// status it answers with.
//
// Folding the gateway's authorization outcome into the signal instead makes an
// admin-gate refusal (a credential, role, or RBAC regression) or a single-
// endpoint 5xx read as an outage. The read surface then flips from case 1 to
// case 4 and answers 503 EVENT_STREAM_UNAVAILABLE where §25.5 specifies the
// EVENT_STREAM_DEGRADED 200 classification for Redis-down / gateway-up. A
// gateway that is reachable but cannot serve the buffer query is reported by
// the fan-out data path instead, which distinguishes "no replica served".
//
// spec: §25.5 (degradation matrix — Redis down with the gateway up falls back
// to the gateway event buffer, rather than the dual-outage 503).
func TestProbeGatewayReportsReachableWhenGatewayRefusesTheCall_spec_25_5(t *testing.T) {
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
			if !probeGateway(context.Background(), newProbeGatewayClient(t, srv.URL)) {
				t.Fatalf("gateway answering HTTP %d reported unreachable; the answer proves the "+
					"process is serving, so the §25.5 classification is the case-1 gateway-buffer "+
					"fall-back rather than the dual-outage 503", tc.status)
			}
		})
	}
}

// TestProbeGatewayMeasuresTheUnauthenticatedLivenessPath pins the endpoint the
// probe rides on. The signal is a reachability input to the §25.5 matrix, so it
// is measured on an unauthenticated path whose outcome does not depend on
// whether lenny-ops is currently authorized at the gateway admin API.
//
// spec: §25.5 (source health for the degradation matrix).
func TestProbeGatewayMeasuresTheUnauthenticatedLivenessPath_spec_25_5(t *testing.T) {
	var path atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if !probeGateway(context.Background(), newProbeGatewayClient(t, srv.URL)) {
		t.Fatal("a gateway answering 200 reported unreachable")
	}
	if got := path.Load(); got != gatewayLivenessPath {
		t.Fatalf("probe requested %v, want the unauthenticated liveness path %q; probing an "+
			"authenticated admin endpoint couples the §25.5 degradation state to the gateway "+
			"authorization path", got, gatewayLivenessPath)
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
