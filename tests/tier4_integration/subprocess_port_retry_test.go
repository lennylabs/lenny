// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/ports"
	tokensvc "github.com/lennylabs/lenny/tests/testinfra/tokenservice"
)

// occupierHeader marks a response the occupying listener served, so a test
// can tell it from the process under test.
const occupierHeader = "X-Port-Occupier"

// portOccupier is a listener this test process holds and serves HTTP on,
// standing in for another test's process that took a port between the
// harness's choice and the child's bind (TEST-GAPS T-17.4.38).
type portOccupier struct {
	addr string
	hits atomic.Int64
}

// occupyPort binds a loopback port and answers every request on it with 200
// and occupierHeader until the test ends.
func occupyPort(t *testing.T) *portOccupier {
	t.Helper()
	ln := ports.NewListener(t)
	o := &portOccupier{addr: ln.Addr().String()}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		o.hits.Add(1)
		w.Header().Set(occupierHeader, "1")
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return o
}

// reserveOccupiedAt returns a Reserve function that hands out the occupied
// address on the listed calls (1-based) and a fresh address otherwise, and a
// count of its calls.
func reserveOccupiedAt(occupied string, calls ...int) (func(testing.TB) string, *atomic.Int64) {
	var (
		mu sync.Mutex
		n  atomic.Int64
	)
	at := map[int]bool{}
	for _, c := range calls {
		at[c] = true
	}
	return func(t testing.TB) string {
		mu.Lock()
		defer mu.Unlock()
		if at[int(n.Add(1))] {
			return occupied
		}
		return ports.Reserve(t)
	}, &n
}

// assertServedByProcess fails the test when the address belongs to the
// occupier or a request to it reaches the occupier.
func assertServedByProcess(t *testing.T, o *portOccupier, addr, path string) {
	t.Helper()
	if addr == o.addr {
		t.Fatalf("the process kept the occupied address %s", addr)
	}
	resp, err := http.Get("http://" + addr + path)
	if err != nil {
		t.Fatalf("GET %s on the started process: %v", path, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.Header.Get(occupierHeader) != "" {
		t.Fatalf("GET %s on %s reached the occupying listener", path, addr)
	}
}

// spec: TESTING.md §12.4 (Tier 4 — Integration), TESTING.md §17.10 (Flake budget)
// diagnosis: the gateway harness handed back a process on an address another
// process held, either because its readiness probe accepted the other
// process's answer or because it gave up when the gateway exited on the bind
// refusal, rather than starting again on fresh addresses. Parallel tier-4
// tests then fail at random with a readiness timeout or talk to the wrong
// server.
func TestGatewayHarnessRestartsOnAnOccupiedAddress(t *testing.T) {
	t.Parallel()
	gateway.SkipUnlessAvailable(t)
	binary := gateway.Build(t)

	t.Run("REST address", func(t *testing.T) {
		t.Parallel()
		o := occupyPort(t)
		reserve, calls := reserveOccupiedAt(o.addr, 1)
		gw := gateway.StartOpts(t, gateway.Options{Binary: binary, Reserve: reserve})
		assertServedByProcess(t, o, strings.TrimPrefix(gw.BaseURL(), "http://"), "/v1/sessions")
		if calls.Load() < 4 {
			t.Errorf("Reserve called %d times, want at least 4 (two attempts of two addresses)", calls.Load())
		}
	})
	t.Run("forward address", func(t *testing.T) {
		t.Parallel()
		o := occupyPort(t)
		reserve, calls := reserveOccupiedAt(o.addr, 2)
		gw := gateway.StartOpts(t, gateway.Options{Binary: binary, Reserve: reserve})
		if gw.ForwardAddr() == o.addr {
			t.Fatalf("the gateway advertises the occupied forward address %s", o.addr)
		}
		assertServedByProcess(t, o, strings.TrimPrefix(gw.BaseURL(), "http://"), "/v1/sessions")
		if calls.Load() < 4 {
			t.Errorf("Reserve called %d times, want at least 4 (two attempts of two addresses)", calls.Load())
		}
	})
}

// spec: TESTING.md §12.4 (Tier 4 — Integration), TESTING.md §17.10 (Flake budget)
// diagnosis: the Token Service harness handed back a process whose gRPC or
// HTTP address another process held, because a TCP dial the other process
// accepted counted as ready, or it gave up on the bind refusal rather than
// starting again on fresh addresses.
func TestTokenServiceHarnessRestartsOnAnOccupiedAddress(t *testing.T) {
	t.Parallel()
	tokensvc.SkipUnlessAvailable(t)
	binary := tokensvc.Build(t)
	for _, tc := range []struct {
		name string
		call int
	}{{"gRPC address", 1}, {"HTTP address", 2}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := occupyPort(t)
			reserve, _ := reserveOccupiedAt(o.addr, tc.call)
			ts := tokensvc.StartOpts(t, tokensvc.Options{Binary: binary, Reserve: reserve})
			if ts.GRPCAddr() == o.addr || ts.HTTPAddr() == o.addr {
				t.Fatalf("the Token Service kept the occupied address %s (grpc %s, http %s)", o.addr, ts.GRPCAddr(), ts.HTTPAddr())
			}
			if err := ts.Ping(t); err == nil {
				t.Fatalf("Ping of a nonexistent pool succeeded, want the server's refusal")
			}
		})
	}
}

// spec: TESTING.md §12.4 (Tier 4 — Integration), TESTING.md §17.10 (Flake budget)
// diagnosis: the lenny-ops harness handed back a process on an address
// another process held, because /readyz answered by the other process
// counted as ready, or it gave up on the bind refusal rather than starting
// again on a fresh address.
func TestOpsHarnessRestartsOnAnOccupiedAddress(t *testing.T) {
	t.Parallel()
	opsprocess.SkipUnlessAvailable(t)
	pg := sharedDatabase(t)
	rd := containers.StartRedis(t, containers.RedisOptions{})
	o := occupyPort(t)
	reserve, calls := reserveOccupiedAt(o.addr, 1)
	ops := opsprocess.StartOpts(t, opsprocess.Options{
		Args:    []string{"--postgres-dsn=" + pg.DSN, "--redis-url=redis://" + rd.Addr + "/0", "--redis-allow-insecure"},
		Reserve: reserve,
	})
	assertServedByProcess(t, o, strings.TrimPrefix(ops.BaseURL(), "http://"), "/readyz")
	if calls.Load() < 2 {
		t.Errorf("Reserve called %d times, want at least 2", calls.Load())
	}
}
