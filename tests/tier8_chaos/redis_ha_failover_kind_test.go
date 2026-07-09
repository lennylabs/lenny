// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos: §17.3 Redis Sentinel cross-zone disaster recovery, driven
// against the real Kind-deployed Sentinel topology rather than the
// compose stack.
//
// tests/testinfra/kind/datastores-ha-redis.yaml layers a
// lenny-redis-replica Deployment and a three-pod lenny-redis-sentinel
// StatefulSet (quorum 2) on top of the base single-replica
// datastores.yaml Redis. Before this test, that overlay had no test
// driving it on Kind: TestRedisSentinelFailover
// (redis_sentinel_failover_test.go) proves the same §17.3 behavior only
// against the compose Sentinel topology. TESTING.md's tier-8 cadence
// table lists "Redis Sentinel failover" as a PR-gated Kind-subset
// scenario alongside "Postgres failover", which TestPostgresHAFailover
// already drives against its own Kind HA overlay; this test closes the
// matching gap for Redis using the same delete-the-primary-pod pattern
// tests/testinfra/kind/datastores-ha.md documents for this overlay.
//
// This test scales the base lenny-redis (master) Deployment to zero,
// asserts Sentinel promotes the replica, asserts a canary write that
// replicated before the kill survives on the promoted node, and
// asserts the production pkg/redisconn constructor accepts the live
// Kind Sentinel topology and builds a FailoverClient over it. As with
// the compose test, the resolved master/replica addresses Sentinel
// returns are Kind-internal Service DNS names (resolve-hostnames /
// announce-hostnames yes), so the post-failover data-plane proof runs
// in-cluster via kubectl exec — the same constraint
// TestRedisSentinelFailover documents for docker exec against the
// compose containers.
//
// The chaos injection scales the base Deployment to zero (the same
// scaleDownAndRestore helper TestPostgresHAFailover uses to kill the
// primary) rather than deleting the live pod: the base Deployment
// recreates a deleted pod under the same Service name fast enough
// (sub-second on a Kind node with the image already cached) that
// Sentinel observes only a brief "+reboot"/"-sdown" blip and never
// reaches ODOWN quorum, so no failover is ever triggered. Scaling to
// zero removes the Service's only backend until this test's cleanup
// restores it, giving Sentinel's quorum and leader-election protocol
// the sustained outage it needs to actually promote the replica.

// The overlay this test applies also carries two NetworkPolicies
// (allow-e2e-redis-sentinel-ingress / allow-egress-to-e2e-redis-sentinel
// in datastores-ha-redis.yaml) opening port 26379 between e2e-datastore
// pods: the base e2e NetworkPolicy baseline
// (tests/testinfra/k8s/datastores.yaml) only opens 5432/6379/9000/9090,
// so without these additions the cluster's default-deny-all blocked
// the direct sentinel-to-sentinel SENTINEL IS-MASTER-DOWN-BY-ADDR RPCs
// the ODOWN quorum needs, and every sentinel marked its peers "down"
// instead of the master.

package tier8_chaos_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

const (
	// redisHAOverlayRelPath is the HA overlay this test applies on top
	// of the base e2e datastores.yaml, relative to the repo root.
	redisHAOverlayRelPath = "tests/testinfra/kind/datastores-ha-redis.yaml"

	// redisHAMasterName is the Sentinel-monitored master name the
	// overlay's ConfigMap configures ("sentinel monitor lenny-master
	// lenny-redis.lenny-system.svc 6379 2").
	redisHAMasterName = "lenny-master"

	// Selectors and object names the overlay
	// (tests/testinfra/kind/datastores-ha-redis.yaml) and the base
	// datastores.yaml create.
	redisHAMasterSelector      = "lenny.dev/e2e-datastore=redis"
	redisHAReplicaSelector     = "lenny.dev/e2e-datastore=redis-replica"
	redisHASentinelSelector    = "lenny.dev/e2e-datastore=redis-sentinel"
	redisHAReplicaDeployment   = "lenny-redis-replica"
	redisHASentinelStatefulSet = "lenny-redis-sentinel"

	// redisHAMasterAnnouncedHost / redisHAReplicaAnnouncedHost are the
	// Service DNS names Sentinel resolves the master and replica to,
	// per the overlay's "sentinel monitor ... lenny-redis.lenny-system.svc"
	// directive and the replica's "--replica-announce-ip
	// lenny-redis-replica.lenny-system.svc" flag.
	redisHAMasterAnnouncedHost  = "lenny-redis.lenny-system.svc"
	redisHAReplicaAnnouncedHost = "lenny-redis-replica.lenny-system.svc"

	// redisHASentinelRemotePort is the Sentinel port PortForward dials
	// on each Sentinel pod to build the production client below.
	redisHASentinelRemotePort = 26379
)

// spec: §17.3 (disaster recovery) — RPO/RTO table: "Redis (cache,
// leases) | Ephemeral — rebuild from Postgres | < 15s (Sentinel
// failover)"; cross-zone requirements: "Redis: Sentinel nodes spread
// across zones". §12.8 (Tier 8 chaos) lists "Redis Sentinel failover"
// among the Store failures scenarios with cadence "PR (Kind subset)".
// diagnosis: before this test, no test drove the Kind-deployed Redis
// Sentinel HA overlay (datastores-ha-redis.yaml); only the compose
// topology was exercised (TestRedisSentinelFailover) and the fully
// cloud multi-AZ Postgres exercise (TestMultiZoneDR), leaving the
// PR-gated Kind Redis Sentinel scenario TESTING.md's cadence table
// names as unimplemented. A failure here means either Sentinel failed
// to promote the replica after the primary pod was killed (the §17.3
// availability contract is broken on Kind) or the production
// pkg/redisconn constructor cannot build a FailoverClient over the real
// deployed Sentinel topology (the code path §12.4/§12.8 depend on is
// broken against Kind-rendered Sentinel addresses).
func TestRedisHAFailoverKind(t *testing.T) {
	c := kind.InstallLenny(t)
	requireRedisDeployment(t, c)

	overlay := filepath.Join(schematest.RepoRoot(t), redisHAOverlayRelPath)
	c.Apply(t, overlay)
	t.Cleanup(func() { deleteRedisHAOverlay(t, c, overlay) })

	if !pollUntil(120*time.Second, 3*time.Second, func() bool {
		return deploymentReady(t, c, redisHAReplicaDeployment)
	}) {
		t.Fatalf("%s did not become Ready within 120s (state %s)",
			redisHAReplicaDeployment, deploymentReadyState(t, c, redisHAReplicaDeployment))
	}
	// statefulSetReady (not a pod-selector kubectl wait) so this does not
	// race the StatefulSet's default OrderedReady pod management policy,
	// which creates the three sentinel ordinals one at a time: a wait
	// issued before the last ordinal is even created can observe only
	// the ordinals that already exist and return early, leaving the
	// final sentinel pod Pending when the code below port-forwards to
	// every pod the selector matches.
	if !pollUntil(120*time.Second, 3*time.Second, func() bool {
		return statefulSetReady(t, c, redisHASentinelStatefulSet)
	}) {
		t.Fatalf("%s did not reach its desired ready replica count within 120s (state %s)",
			redisHASentinelStatefulSet, statefulSetReadyState(t, c, redisHASentinelStatefulSet))
	}

	sentinelPods := podNames(t, c, redisHASentinelSelector)
	if len(sentinelPods) < 2 {
		t.Fatalf("expected at least 2 Ready %s pods for quorum, found %d", redisHASentinelStatefulSet, len(sentinelPods))
	}
	sentinelPod := sentinelPods[0]

	waitRedisHASentinelDiscoveredReplica(t, c, sentinelPod, redisHAMasterName, 90*time.Second)

	if host, err := redisHASentinelMasterHost(t, c, sentinelPod, redisHAMasterName); err != nil || host != redisHAMasterAnnouncedHost {
		t.Fatalf("pre-failover sentinel master = %q (err %v), want %q", host, err, redisHAMasterAnnouncedHost)
	}

	// Production-client config path: NewClient must accept the live
	// deployed Sentinel topology and return a FailoverClient. Each
	// Sentinel pod is forwarded individually (the headless Service
	// picks one pod arbitrarily on each dial, which is not what a
	// Sentinel-aware client wants: it needs to reach every sentinel).
	var sentinelAddrs []string
	for _, pod := range sentinelPods {
		base, _ := c.PortForward(t, "pod/"+pod, lennySystemNamespace, redisHASentinelRemotePort)
		sentinelAddrs = append(sentinelAddrs, strings.TrimPrefix(base, "http://"))
	}
	client, err := redisconn.NewClient(redisconn.Config{
		SentinelAddrs: sentinelAddrs,
		MasterName:    redisHAMasterName,
		AllowInsecure: true, // the overlay's Redis/Sentinel pods are plaintext, no AUTH
	})
	if err != nil {
		t.Fatalf("redisconn.NewClient over the live Kind sentinels: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	// Seed a canary on the master and confirm it replicates before the kill.
	masterPods := podNames(t, c, redisHAMasterSelector)
	if len(masterPods) == 0 {
		t.Fatalf("no pod matches selector %q", redisHAMasterSelector)
	}
	masterPod := masterPods[0]
	replicaPods := podNames(t, c, redisHAReplicaSelector)
	if len(replicaPods) == 0 {
		t.Fatalf("no pod matches selector %q", redisHAReplicaSelector)
	}
	replicaPod := replicaPods[0]

	const key, val = "chaos:sentinel:canary", "before-failover"
	if out, err := redisHADataExec(t, c, masterPod, "set", key, val); err != nil {
		t.Fatalf("seed canary on master: %v\n%s", err, out)
	}
	if !pollUntil(20*time.Second, 500*time.Millisecond, func() bool {
		out, err := redisHADataExec(t, c, replicaPod, "get", key)
		return err == nil && out == val
	}) {
		t.Fatalf("canary %q did not replicate to %s within 20s", key, replicaPod)
	}

	// Kill the master by scaling the base lenny-redis Deployment to
	// zero (see the file-level comment on why a pod delete does not
	// give Sentinel a real outage to detect). scaleDownAndRestore
	// registers a cleanup that scales back to the original replica
	// count and waits for Ready once the test finishes; that cleanup
	// runs before the overlay-delete cleanup registered above (t.Cleanup
	// is LIFO), so the restored master rejoins while Sentinel and the
	// sentinel-to-sentinel NetworkPolicy are still in place to
	// reconfigure it as a replica of the promoted node.
	scaleDownAndRestore(t, c, redisDeployment)
	if !waitDeploymentScaledDown(t, c, redisDeployment, storeRecoveryBound) {
		t.Fatalf("%s did not scale down to zero replicas after the scale command", redisDeployment)
	}
	killedAt := time.Now()

	// §17.3 names a < 15s Sentinel-failover RTO target; the overlay's
	// down-after-milliseconds (2s) + failover-timeout (5s) aim for
	// that window, but Kind pod-delete-and-reschedule latency is not
	// as tight as a bare docker kill, so the wait bound below carries
	// generous slack (mirroring TestRedisSentinelFailover's own
	// 60s bound for the same promotion) while the elapsed time is
	// logged for visibility into how close the deployment runs to the
	// documented target.
	if !pollUntil(90*time.Second, time.Second, func() bool {
		host, err := redisHASentinelMasterHost(t, c, sentinelPod, redisHAMasterName)
		return err == nil && host == redisHAReplicaAnnouncedHost
	}) {
		t.Fatalf("sentinel did not promote %q to master within 90s", redisHAReplicaAnnouncedHost)
	}
	t.Logf("§17.3: sentinel promoted %s to master %s after the primary pod was killed (target < 15s)",
		redisHAReplicaAnnouncedHost, time.Since(killedAt))

	// The pre-failover canary survives on the promoted master.
	if out, err := redisHADataExec(t, c, replicaPod, "get", key); err != nil || out != val {
		t.Fatalf("canary on promoted master = %q (want %q); err %v", out, val, err)
	}

	// A client that resolves the master through the Sentinel protocol
	// transparently reaches the promoted node and can write to it —
	// the exact resolution go-redis's FailoverClient performs, driven
	// in-cluster (via kubectl exec) so the Kind-internal Service DNS
	// name resolves.
	const post = "after-failover"
	writeViaSentinel := fmt.Sprintf(
		`set -- $(redis-cli -p %s sentinel get-master-addr-by-name %s); redis-cli -h "$1" -p "$2" set chaos:sentinel:post %s`,
		sentinelPort, redisHAMasterName, post,
	)
	if out, err := c.KubectlOut(t, "-n", lennySystemNamespace, "exec", sentinelPod, "-c", "sentinel", "--",
		"sh", "-c", writeViaSentinel); err != nil {
		t.Fatalf("write via sentinel-resolved master: %v\n%s", err, out)
	}
	readViaSentinel := fmt.Sprintf(
		`set -- $(redis-cli -p %s sentinel get-master-addr-by-name %s); redis-cli -h "$1" -p "$2" get chaos:sentinel:post`,
		sentinelPort, redisHAMasterName,
	)
	out, err := c.KubectlOut(t, "-n", lennySystemNamespace, "exec", sentinelPod, "-c", "sentinel", "--",
		"sh", "-c", readViaSentinel)
	if err != nil || strings.TrimSpace(out) != post {
		t.Fatalf("read via sentinel-resolved master = %q (want %q); err %v", out, post, err)
	}
}

// requireRedisDeployment skips the test when the gateway is not wired
// to a Redis URL, mirroring TestPostgresHAFailover's own
// requirePostgresPersistence precondition check.
func requireRedisDeployment(t *testing.T, c *kind.Cluster) {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", lennySystemNamespace, "get", "pods",
		"-l", gatewaySelector, "-o", "jsonpath={.items[0].spec.containers[0].env[*].name}")
	if err != nil || !strings.Contains(out, "LENNY_REDIS_URL") {
		t.Skip("gateway has no LENNY_REDIS_URL; the §17.3 Redis failover exercise needs a Redis-backed install")
	}
}

// waitRedisHASentinelDiscoveredReplica blocks until the sentinel pod
// reports masterName with at least one attached replica, so the
// failover this test drives has somewhere to promote to. Matches
// TestRedisSentinelFailover's compose-side waitSentinelReady.
func waitRedisHASentinelDiscoveredReplica(t *testing.T, c *kind.Cluster, sentinelPod, masterName string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		out, err := c.KubectlOut(t, "-n", lennySystemNamespace, "exec", sentinelPod, "-c", "sentinel", "--",
			"redis-cli", "-p", sentinelPort, "sentinel", "master", masterName)
		if err == nil {
			fields := strings.Fields(out)
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

// redisHASentinelMasterHost returns the host segment the sentinel pod
// currently elects for masterName (the first line of
// get-master-addr-by-name).
func redisHASentinelMasterHost(t *testing.T, c *kind.Cluster, sentinelPod, masterName string) (string, error) {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", lennySystemNamespace, "exec", sentinelPod, "-c", "sentinel", "--",
		"redis-cli", "-p", sentinelPort, "sentinel", "get-master-addr-by-name", masterName)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", fmt.Errorf("empty sentinel reply for %q: %q", masterName, out)
	}
	return fields[0], nil
}

// redisHADataExec runs a redis-cli command against the "redis"
// container of a data-plane pod (master or replica) and returns the
// trimmed output.
func redisHADataExec(t *testing.T, c *kind.Cluster, pod string, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"-n", lennySystemNamespace, "exec", pod, "-c", "redis", "--", "redis-cli"}, args...)
	out, err := c.KubectlOut(t, full...)
	return strings.TrimSpace(out), err
}

// deleteRedisHAOverlay removes every resource the overlay created
// (replica Deployment/Service, Sentinel ConfigMap/StatefulSet/Service),
// leaving the shared cluster back at its single-replica baseline.
func deleteRedisHAOverlay(t *testing.T, c *kind.Cluster, overlayPath string) {
	t.Helper()
	if out, err := c.KubectlOut(t, "delete", "-f", overlayPath,
		"--ignore-not-found", "--wait=true"); err != nil {
		t.Errorf("delete Redis HA overlay %s: %v\n%s", overlayPath, err, out)
	}
}
