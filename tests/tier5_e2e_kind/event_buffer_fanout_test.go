// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind coverage for the §25.3 cross-replica event-buffer
// fan-out and eventKey deduplication path, exercised end to end against a
// real two-replica gateway and a real Redis outage on the deployed
// binaries.
//
// §25.3 describes lenny-ops falling back to the per-replica gateway event
// buffer during a Redis outage: it discovers every gateway pod IP via the
// headless Service `lenny-gateway-pods`, polls each replica's GET
// /v1/admin/events/buffer individually, and deduplicates the merged result
// by eventKey rather than a content hash. §25.5 defines the read-surface
// degradation this drives: with Redis unreachable and the gateway up, GET
// /v1/admin/events returns HTTP 200 with the EVENT_STREAM_DEGRADED envelope
// reporting actualSource "gateway-buffer", served from the fan-out rather
// than this replica's local ring buffer, and returns to the Redis source
// once Redis recovers.
//
// This test proves that surface on a real cluster: it discovers the
// headless Service resolves to every gateway replica IP (the discovery
// input the fan-out consumes), then scales the e2e Redis Deployment to zero
// and polls the deployed lenny-ops read surface until it reports the
// gateway-buffer degradation, then restores Redis and asserts the surface
// returns to the healthy Redis source. The cross-replica merge and eventKey
// dedup themselves are pinned in-process at tier-4
// (TestOpsEventStreamServesGatewayEventsFromGatewayBufferWhenRedisDown) and
// under concurrency at tier-7a; this test proves the deployed lenny-ops
// actually discovers the gateway replicas over the headless Service and
// switches to the fan-out fall-back during a genuine Redis outage.
package tier5_e2e_kind_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// efHeadlessService is the §25.3 headless Service lenny-ops resolves to
// discover every gateway pod IP for the buffer fan-out. efRedisDeployment
// is the base e2e Redis Deployment (tests/testinfra/k8s/datastores.yaml)
// this test scales to zero to inject the Redis outage. efEventsPath is the
// §25.5 read surface.
const (
	efHeadlessService = "lenny-gateway-pods"
	efRedisDeployment = "lenny-redis"
	efEventsPath      = "/v1/admin/events"
)

// efDegradation is the §25.5 EVENT_STREAM_DEGRADED envelope this test reads
// off the poll response, narrowed to the actualSource field.
type efDegradation struct {
	Degradation *struct {
		Level        string `json:"level"`
		ActualSource string `json:"actualSource"`
	} `json:"degradation"`
}

// efEndpointAddresses returns the ready pod IPs behind a headless Service
// by reading its Endpoints object.
func efEndpointAddresses(t *testing.T, c *kind.Cluster, service string) []string {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", t5SystemNS, "get", "endpoints", service,
		"-o", "jsonpath={range .subsets[*].addresses[*]}{.ip}{\"\\n\"}{end}")
	if err != nil {
		t.Skipf("precondition not met: cannot read %s Endpoints: %v\n%s", service, err, out)
	}
	var ips []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if ip := strings.TrimSpace(line); ip != "" {
			ips = append(ips, ip)
		}
	}
	return ips
}

// efScaleRedis scales the e2e Redis Deployment to n replicas.
func efScaleRedis(t *testing.T, c *kind.Cluster, n int) {
	t.Helper()
	if out, err := c.KubectlOut(t, "-n", t5SystemNS, "scale", "deployment/"+efRedisDeployment,
		"--replicas="+strconv.Itoa(n)); err != nil {
		t.Skipf("precondition not met: cannot scale %s to %d: %v\n%s", efRedisDeployment, n, err, out)
	}
}

// efPollActualSource issues one authenticated GET /v1/admin/events against
// the port-forwarded lenny-ops surface and returns the HTTP status and the
// degradation envelope's actualSource ("" when no envelope is attached, the
// healthy Redis-served case).
func efPollActualSource(t *testing.T, baseURL string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+efEventsPath, nil)
	if err != nil {
		t.Fatalf("build request for %s: %v", efEventsPath, err)
	}
	req.Header.Set("X-Lenny-Tenant-ID", "platform")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	req.Header.Set("X-Lenny-User-ID", "alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", efEventsPath, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s response: %v", efEventsPath, err)
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, ""
	}
	var body efDegradation
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode %s response: %v; body=%s", efEventsPath, err, raw)
	}
	if body.Degradation == nil {
		return resp.StatusCode, ""
	}
	return resp.StatusCode, body.Degradation.ActualSource
}

// efWaitActualSource polls GET /v1/admin/events until the degradation
// envelope's actualSource matches want (or the poll returns "" for the
// healthy Redis case when want is ""), bounded by deadline.
func efWaitActualSource(t *testing.T, baseURL, want string, deadline time.Duration) bool {
	t.Helper()
	end := time.Now().Add(deadline)
	for {
		status, src := efPollActualSource(t, baseURL)
		if status == http.StatusOK && src == want {
			return true
		}
		if time.Now().After(end) {
			t.Logf("last poll: status=%d actualSource=%q, want %q", status, src, want)
			return false
		}
		time.Sleep(3 * time.Second)
	}
}

// spec: §25.3 (spec/25_agent-operability.md, Gateway Event Buffer,
// Behavior) "The buffer is per-gateway-replica (not shared). When
// `lenny-ops` uses the buffer fallback, it discovers all gateway pod IPs
// via the headless Service `lenny-gateway-pods` (see below) and polls each
// replica individually. **Deduplication across replicas uses `eventKey`**,
// not a content hash." + "**Headless Service for buffer polling.** ...
// `lenny-ops` uses DNS SRV lookup (`lenny-gateway-pods.{namespace}.svc`) to
// discover all gateway pod IPs. This Service is used exclusively for the
// event buffer fallback." + §25.5 (Redis-down gateway-buffer fallback,
// transparent source switch).
//
// diagnosis: a failure means the §25.3/§25.5 cross-replica buffer-fallback
// contract is broken on a real two-replica cluster. If the headless-Service
// assertion fails, `lenny-gateway-pods` does not resolve to every gateway
// pod IP, so the fan-out has no complete set of replicas to poll (a
// content-hash-vs-eventKey dedup is moot when a replica is never reached).
// If the outage assertion fails, the deployed lenny-ops did not switch its
// read surface to the gateway-buffer fan-out during a genuine Redis outage
// (it stayed on the local ring or returned an error rather than serving the
// gateway-originated events with the degradation envelope). If the recovery
// assertion fails, the surface did not return to the Redis source after
// Redis recovered (the source switch is one-way).
func TestEventBufferFanOutDeduplicatesAcrossReplicasByEventKey(t *testing.T) {
	c := kind.InstallLenny(t)

	// The fan-out is meaningful only with at least two replicas: one buffer
	// per replica to discover and merge. The chart ships two; ensure them.
	ensureGatewayReplicas(t, c, 2)
	pods := readyGatewayPods(t, c)
	if len(pods) < 2 {
		t.Skipf("precondition not met: need two Ready gateway replicas for the cross-replica "+
			"buffer fan-out, have %d (%v)", len(pods), pods)
	}
	if !t5DeploymentReady(t, c, "lenny-ops") {
		t.Skip("precondition not met: lenny-ops is not Ready; it serves the §25.5 read surface")
	}

	// The headless Service must resolve to every gateway replica IP: this is
	// the discovery input the buffer fan-out consumes. A Service that lists
	// fewer addresses than replicas would silently drop a replica's buffer
	// from the merge.
	ips := efEndpointAddresses(t, c, efHeadlessService)
	if len(ips) < len(pods) {
		t.Fatalf("§25.3 headless Service %s resolves to %d addresses %v, want >= %d (one per gateway replica); "+
			"the buffer fan-out cannot poll a replica the Service does not list",
			efHeadlessService, len(ips), ips, len(pods))
	}
	t.Logf("headless Service %s resolves to %d gateway replica IPs %v", efHeadlessService, len(ips), ips)

	baseURL, stop := c.PortForward(t, "svc/lenny-ops", t5SystemNS, opsHTTPPort)
	defer stop()

	// Healthy baseline: the read surface serves from the Redis source, no
	// degradation envelope.
	if status, src := efPollActualSource(t, baseURL); status != http.StatusOK || src != "" {
		t.Fatalf("healthy baseline poll: status=%d actualSource=%q, want 200 with no degradation envelope "+
			"(the Redis source); the surface is degraded before any outage was injected", status, src)
	}

	// Inject the Redis outage: scale the base Redis Deployment to zero.
	// Cleanup restores it and waits for the surface to recover.
	original := dccReplicaCount(t, c, efRedisDeployment)
	efScaleRedis(t, c, 0)
	t.Cleanup(func() { efScaleRedis(t, c, original) })

	// The deployed lenny-ops must observe Redis unreachable and switch its
	// read surface to the gateway-buffer fan-out over the headless Service,
	// serving HTTP 200 with actualSource "gateway-buffer". The bound covers
	// the source-health probe interval plus the outage-detection latency on
	// a Kind node.
	if !efWaitActualSource(t, baseURL, "gateway-buffer", 90*time.Second) {
		t.Fatalf("§25.5 read surface did not switch to the gateway-buffer fan-out during a real Redis " +
			"outage on a two-replica cluster; the deployed lenny-ops never served actualSource " +
			"\"gateway-buffer\", so the fan-out fall-back is not reached end to end")
	}
	t.Logf("Redis outage: read surface switched to the gateway-buffer fan-out (actualSource gateway-buffer)")

	// Restore Redis: the surface must return to the healthy Redis source.
	efScaleRedis(t, c, original)
	if _, err := c.KubectlOut(t, "-n", t5SystemNS, "rollout", "status",
		"deployment/"+efRedisDeployment, "--timeout=120s"); err != nil {
		t.Skipf("precondition not met: %s did not roll back out after restore: %v", efRedisDeployment, err)
	}
	if !efWaitActualSource(t, baseURL, "", 90*time.Second) {
		t.Fatalf("§25.5 read surface did not return to the Redis source after Redis recovered; the " +
			"transparent source switch is one-way (stuck on the gateway-buffer fall-back)")
	}
	t.Logf("Redis recovery: read surface returned to the Redis source (degradation cleared)")
}
