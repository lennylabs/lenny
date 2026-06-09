// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos: §12.8 Redis Sentinel automatic failover.
//
// This exercise drives a real master kill against the compose Sentinel
// topology (compose/default.yml: one master, one replica, three
// sentinels with quorum 2) and asserts the cluster transparently
// promotes the replica and serves the pre-failover data from the new
// master. It supersedes the former t.Logf placeholder
// (F-12.4.24).
//
// Host-network limitation. The compose sentinels are configured with
// `resolve-hostnames yes` and monitor `redis 6379`, so a sentinel
// returns the Docker-internal address (`redis:6379`, then
// `redis-replica:6379` after promotion). Only the master service
// publishes a host port, so a host-side client cannot perform data
// operations against the internal-only nodes — least of all the
// promoted replica. The data-plane proof therefore runs in-network via
// `docker exec` against the sentinel/data containers, which is exactly
// the address resolution go-redis's FailoverClient performs
// (`SENTINEL get-master-addr-by-name`). The production constructor
// `redisconn.NewClient` is still exercised against the live Sentinel
// addresses to assert the config path builds a FailoverClient over this
// topology.

package tier8_chaos_test

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/tests/testinfra/compose"
)

// Compose container names from compose/default.yml.
const (
	redisMasterContainer  = "lenny-redis"
	redisReplicaContainer = "lenny-redis-replica"
	sentinelContainer     = "lenny-redis-sentinel-1"
	sentinelPort          = "26379"
)

// dockerExec runs `docker exec <container> <args...>` and returns the
// trimmed combined output. A non-zero exit is returned as the error so
// callers can fail the test with the container's own diagnostics.
func dockerExec(t *testing.T, container string, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"exec", container}, args...)
	out, err := exec.Command("docker", full...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// sentinelMasterHost returns the host segment the sentinel currently
// elects for masterName (the first line of get-master-addr-by-name).
func sentinelMasterHost(t *testing.T, masterName string) string {
	t.Helper()
	out, err := dockerExec(t, sentinelContainer,
		"redis-cli", "-p", sentinelPort, "sentinel", "get-master-addr-by-name", masterName)
	if err != nil {
		t.Fatalf("sentinel get-master-addr-by-name: %v\n%s", err, out)
	}
	// Output is two lines: host then port. Take the host.
	lines := strings.Fields(out)
	if len(lines) == 0 {
		t.Fatalf("sentinel returned no master address for %q: %q", masterName, out)
	}
	return lines[0]
}

// waitSentinelReady blocks until the sentinel reports masterName with at
// least one replica attached, so a failover has somewhere to promote to.
func waitSentinelReady(t *testing.T, masterName string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		out, err := dockerExec(t, sentinelContainer,
			"redis-cli", "-p", sentinelPort, "sentinel", "master", masterName)
		if err == nil {
			fields := strings.Fields(out)
			// The flat reply alternates name/value; find num-slaves.
			for i := 0; i+1 < len(fields); i++ {
				if fields[i] == "num-slaves" && fields[i+1] != "0" {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("sentinel did not discover a replica for %q within %s; last reply: %q (err %v)",
				masterName, timeout, out, err)
		}
		time.Sleep(time.Second)
	}
}

// waitKeyOnContainer blocks until a GET of key on container returns want,
// confirming replication reached the node before the failover.
func waitKeyOnContainer(t *testing.T, container, key, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		out, err := dockerExec(t, container, "redis-cli", "get", key)
		if err == nil && out == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("key %q on %s = %q (want %q) within %s; err %v", key, container, out, want, timeout, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// waitMasterPromotedTo blocks until the sentinel elects wantHost as the
// master, asserting the automatic promotion happened.
func waitMasterPromotedTo(t *testing.T, masterName, wantHost string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		host := sentinelMasterHost(t, masterName)
		if host == wantHost {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("sentinel did not promote %q to master within %s; still %q", wantHost, timeout, host)
		}
		time.Sleep(time.Second)
	}
}

// spec: 12.8 / 12.4 line 197
// diagnosis: §12.8 promises a 3-sentinel + 1-primary + 1-replica Redis
// topology that automatically promotes the replica when the primary
// fails, and a Sentinel-aware client (pkg/redisconn) that transparently
// follows the new master. The former scaffold only logged. This kills
// the master and asserts (a) the production redisconn constructor builds
// a FailoverClient over the live sentinels, (b) the sentinel quorum
// promotes the replica to master, (c) the pre-failover canary survives
// on the promoted master, and (d) a client resolving the master through
// the Sentinel protocol reaches the promoted node and can write to it.
func TestRedisSentinelFailover(t *testing.T) {
	compose.SkipUnlessAvailable(t)
	stack := compose.Up(t, compose.ProfileDefault)
	master := stack.RedisSentinelMasterName()

	waitSentinelReady(t, master, 90*time.Second)

	if host := sentinelMasterHost(t, master); host != "redis" {
		t.Fatalf("pre-failover sentinel master = %q, want redis", host)
	}

	// Production-client config path: NewClient must accept the live
	// Sentinel topology and return a FailoverClient. Host-network ops
	// are not possible against the internal-only nodes, so the data
	// proof runs in-network below; this asserts the constructor itself.
	client, err := redisconn.NewClient(redisconn.Config{
		SentinelAddrs: stack.RedisSentinelAddrs(),
		MasterName:    master,
		AllowInsecure: true, // compose Redis is plaintext, no AUTH
	})
	if err != nil {
		t.Fatalf("redisconn.NewClient over live sentinels: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	// Seed a canary on the master and confirm it replicates before the kill.
	const key, val = "chaos:sentinel:canary", "before-failover"
	if out, err := dockerExec(t, redisMasterContainer, "redis-cli", "set", key, val); err != nil {
		t.Fatalf("seed canary on master: %v\n%s", err, out)
	}
	waitKeyOnContainer(t, redisReplicaContainer, key, val, 20*time.Second)

	// Kill the master. Register restore first so a mid-test failure still
	// returns the original node (it rejoins as a replica of the new master).
	restored := false
	t.Cleanup(func() {
		if !restored {
			if out, err := exec.Command("docker", "start", redisMasterContainer).CombinedOutput(); err != nil {
				t.Logf("restore %s: %v\n%s", redisMasterContainer, err, out)
			}
		}
	})
	if out, err := exec.Command("docker", "kill", redisMasterContainer).CombinedOutput(); err != nil {
		t.Fatalf("kill master %s: %v\n%s", redisMasterContainer, err, out)
	}

	// The sentinel quorum must promote the replica within the failover
	// window (down-after 2s + failover-timeout 5s; allow generous slack).
	waitMasterPromotedTo(t, master, "redis-replica", 60*time.Second)

	// The pre-failover canary survives on the promoted master.
	if out, err := dockerExec(t, redisReplicaContainer, "redis-cli", "get", key); err != nil || out != val {
		t.Fatalf("canary on promoted master = %q (want %q); err %v", out, val, err)
	}

	// A client that resolves the master through the Sentinel protocol
	// transparently reaches the promoted node and can write to it — the
	// exact resolution go-redis's FailoverClient performs, driven
	// in-network so the Docker-internal hostname resolves.
	const post = "after-failover"
	writeViaSentinel := "set -- $(redis-cli -p " + sentinelPort +
		" sentinel get-master-addr-by-name " + master + "); redis-cli -h \"$1\" -p \"$2\" set chaos:sentinel:post " + post
	if out, err := dockerExec(t, sentinelContainer, "sh", "-c", writeViaSentinel); err != nil {
		t.Fatalf("write via sentinel-resolved master: %v\n%s", err, out)
	}
	readViaSentinel := "set -- $(redis-cli -p " + sentinelPort +
		" sentinel get-master-addr-by-name " + master + "); redis-cli -h \"$1\" -p \"$2\" get chaos:sentinel:post"
	if out, err := dockerExec(t, sentinelContainer, "sh", "-c", readViaSentinel); err != nil || out != post {
		t.Fatalf("read via sentinel-resolved master = %q (want %q); err %v", out, post, err)
	}

	// Restore the original node so it rejoins the topology as a replica.
	if out, err := exec.Command("docker", "start", redisMasterContainer).CombinedOutput(); err != nil {
		t.Logf("restart original master: %v\n%s", err, out)
	} else {
		restored = true
	}
}
