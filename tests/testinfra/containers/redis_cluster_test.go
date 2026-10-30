// SPDX-License-Identifier: MIT

package containers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"testing"
)

// spec: TESTING.md §12.4 (Tier 4 — Integration), TESTING.md §17.10 (Flake budget)
//
// TestIsPortBindingErrorMatchesOnlyAHostPortConflict pins the classifier the
// Redis Cluster fixture uses to decide whether a failed container start is
// retried on fresh ports (TEST-GAPS T-17.4.39). A port conflict reported by
// Docker's allocator or by its proxy's bind is retried, wrapped or not and in
// any case; every other start failure is not, because fresh ports cannot cure
// it and a retry would only hide it.
func TestIsPortBindingErrorMatchesOnlyAHostPortConflict(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{
			"docker allocator",
			errors.New("start container: Error response from daemon: driver failed programming external connectivity on endpoint x: Bind for 127.0.0.1:23456 failed: port is already allocated"),
			true,
		},
		{
			"docker proxy bind",
			errors.New("Error response from daemon: driver failed programming external connectivity on endpoint x: Error starting userland proxy: listen tcp4 127.0.0.1:23456: bind: address already in use"),
			true,
		},
		{
			"wrapped twice",
			fmt.Errorf("generic container: %w", fmt.Errorf("start: %w", errors.New("bind: address already in use"))),
			true,
		},
		{"upper case", errors.New("BIND: ADDRESS ALREADY IN USE"), true},
		{"image pull", errors.New("Error response from daemon: pull access denied for redis, repository does not exist"), false},
		{"wait timeout", context.DeadlineExceeded, false},
		{"log wait", errors.New("container exited with code 1: lenny redis cluster ready not found"), false},
		{"empty", errors.New(""), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isPortBindingError(tc.err); got != tc.want {
				t.Fatalf("isPortBindingError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// spec: TESTING.md §12.4 (Tier 4 — Integration), TESTING.md §17.10 (Flake budget)
//
// TestRedisClusterPortsLeavesRoomForTheBusPort pins the range the default
// port chooser draws from: every port is distinct, free at the time of the
// call, and low enough that the Redis Cluster bus port (data port plus 10000)
// stays a valid port number.
func TestRedisClusterPortsLeavesRoomForTheBusPort(t *testing.T) {
	t.Parallel()
	for range 20 {
		got := RedisClusterPorts(t, redisClusterMasters)
		if len(got) != redisClusterMasters {
			t.Fatalf("RedisClusterPorts returned %d ports, want %d", len(got), redisClusterMasters)
		}
		seen := map[int]bool{}
		for _, p := range got {
			if p < 20000 || p >= 45000 {
				t.Fatalf("port %d outside [20000, 45000)", p)
			}
			if p+10000 > 65535 {
				t.Fatalf("port %d leaves no valid bus port", p)
			}
			if seen[p] {
				t.Fatalf("port %d returned twice in %v", p, got)
			}
			seen[p] = true
			// The probe listener is closed on return, so the port binds.
			l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
			if err != nil {
				// Another process may have taken it since; that is the race
				// the fixture retries, so it is logged rather than failed.
				t.Logf("port %d taken since the probe: %v", p, err)
				continue
			}
			_ = l.Close()
		}
	}
}
