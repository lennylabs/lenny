// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"context"
	"net"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// holdRedisClusterPort binds a loopback port from the Redis Cluster
// fixture's range and holds it until the test ends, standing in for another
// process that took a port between the fixture's probe and the container's
// publish (TEST-GAPS T-17.4.39).
func holdRedisClusterPort(t *testing.T) int {
	t.Helper()
	for range 20 {
		p := containers.RedisClusterPorts(t, 1)[0]
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err != nil {
			continue // taken since the probe; choose another
		}
		t.Cleanup(func() { _ = l.Close() })
		return p
	}
	t.Fatal("could not hold a port in the Redis Cluster range")
	return 0
}

// sessionContainersOnPort returns the IDs of this test session's containers,
// running or not, whose Redis Cluster port list names port.
func sessionContainersOnPort(t *testing.T, port int) []string {
	t.Helper()
	out, err := exec.Command("docker", "ps", "-a", "-q", "--no-trunc",
		"--filter", "label=org.testcontainers.sessionId="+testcontainers.SessionID()).Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	var hits []string
	for _, id := range strings.Fields(string(out)) {
		env, err := exec.Command("docker", "inspect", "--format",
			`{{range .Config.Env}}{{println .}}{{end}}`, id).Output()
		if err != nil {
			continue // removed between the list and the inspect
		}
		for _, kv := range strings.Split(string(env), "\n") {
			v, ok := strings.CutPrefix(kv, "REDIS_CLUSTER_PORTS=")
			if ok && slices.Contains(strings.Fields(v), strconv.Itoa(port)) {
				hits = append(hits, id)
			}
		}
	}
	return hits
}

// diagnosis: a failure to start means the fixture did not retry when the
// container runtime refused to publish a port another process held (the
// classifier missed the runtime's error text, or the retry gave up); a
// cluster on the held port means the chooser was not called again; a
// leftover container naming the held port means the failed attempt's
// container was not removed.
//
// spec: TESTING.md §12.4 (Tier 4 — Integration), TESTING.md §17.10 (Flake
// budget); §12.4 Redis HA and Failure Modes (Redis Cluster at Tier 3).
func TestRedisClusterFixtureRestartsOnAnOccupiedPort(t *testing.T) {
	held := holdRedisClusterPort(t)
	var (
		mu    sync.Mutex
		calls int
	)
	choose := func(t testing.TB, n int) []int {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls > 1 {
			return containers.RedisClusterPorts(t, n)
		}
		out := []int{held}
		for _, p := range containers.RedisClusterPorts(t, n) {
			if len(out) < n && p != held {
				out = append(out, p)
			}
		}
		for len(out) < n {
			p := containers.RedisClusterPorts(t, 1)[0]
			if !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
		return out
	}

	rc := containers.StartRedisClusterOpts(t, containers.RedisClusterOptions{Ports: choose})

	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 2 {
		t.Fatalf("port chooser called %d times, want 2 (one lost attempt, one retry)", gotCalls)
	}
	heldAddr := "127.0.0.1:" + strconv.Itoa(held)
	if slices.Contains(rc.Addrs, heldAddr) {
		t.Fatalf("cluster kept the held address %s: %v", heldAddr, rc.Addrs)
	}
	if err := rc.Client.ForEachMaster(context.Background(), func(ctx context.Context, n *redis.Client) error {
		return n.Ping(ctx).Err()
	}); err != nil {
		t.Fatalf("ping masters: %v", err)
	}
	if ids := sessionContainersOnPort(t, held); len(ids) != 0 {
		t.Fatalf("the failed attempt's container was left behind: %v", ids)
	}
}
