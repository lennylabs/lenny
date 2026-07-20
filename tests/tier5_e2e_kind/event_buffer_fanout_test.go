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
// This test proves that surface on a real cluster. It discovers the headless
// Service resolves to every gateway replica IP (the discovery input the fan-out
// consumes), emits one distinct operational event into each replica's own
// buffer, scales the e2e Redis Deployment to zero, and polls the deployed
// lenny-ops read surface until it reports the gateway-buffer degradation. It
// then asserts the merged page itself: both replicas' events are present, each
// exactly once, across repeated polls of the same overlapping fan-out windows.
// The label alone proves nothing about the data, since actualSource is computed
// from the source-health signal and is attached whether or not a single gateway
// event was retrieved. Finally it restores Redis and asserts the surface
// returns to the healthy Redis source.
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
	// efGatewayHTTPPort is the gateway's internal HTTP listener, which the
	// per-replica admin call this test emits its buffer events through binds.
	efGatewayHTTPPort = 8080
)

// efPollBody is the §25.5 poll response this test reads: the served items and
// the EVENT_STREAM_DEGRADED envelope, narrowed to the actualSource field.
type efPollBody struct {
	Items []struct {
		Event struct {
			ID   string          `json:"id"`
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		} `json:"event"`
	} `json:"items"`
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
	body := efDecodePoll(t, raw)
	if body.Degradation == nil {
		return resp.StatusCode, ""
	}
	return resp.StatusCode, body.Degradation.ActualSource
}

// efDecodePoll parses a §25.5 poll response body.
func efDecodePoll(t *testing.T, raw []byte) efPollBody {
	t.Helper()
	var body efPollBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode %s response: %v; body=%s", efEventsPath, err, raw)
	}
	return body
}

// efPollPage issues one authenticated GET /v1/admin/events with a large limit
// and returns the parsed page, so a caller can assert what the fan-out merged
// rather than only the label the envelope carries.
func efPollPage(t *testing.T, baseURL string) efPollBody {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+efEventsPath+"?limit=1000", nil)
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
		t.Fatalf("GET %s during the outage returned %d, want 200 with the gateway-buffer page: %s",
			efEventsPath, resp.StatusCode, raw)
	}
	return efDecodePoll(t, raw)
}

// efOpenBreakerOn opens a circuit breaker on one gateway replica, which emits a
// §25.3 circuit_breaker_opened operational event into that replica's own
// in-memory buffer. It is how this test puts a known, replica-local event into
// each buffer the fan-out has to merge. It reports false when the deployment
// does not serve the endpoint, so the caller can treat that as an unmet
// precondition rather than a contract failure.
func efOpenBreakerOn(t *testing.T, c *kind.Cluster, pod, name string) bool {
	t.Helper()
	baseURL, stop := c.PortForward(t, "pod/"+pod, t5SystemNS, efGatewayHTTPPort)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	body := strings.NewReader(`{"reason":"tier-5 cross-replica fan-out probe","limitTier":"soft"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/v1/admin/circuit-breakers/"+name+"/open", body)
	if err != nil {
		t.Fatalf("build breaker-open request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "platform")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	req.Header.Set("X-Lenny-User-ID", "alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("POST breaker open on %s: %v", pod, err)
		return false
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Logf("POST breaker open on %s returned %d: %s", pod, resp.StatusCode, raw)
		return false
	}
	return true
}

// efCountBreakerEvents counts, per breaker name, how many items on the page are
// circuit_breaker_opened events for that name. Each name is opened on exactly
// one replica, so a count other than one means the merge dropped a replica's
// event or delivered one twice.
func efCountBreakerEvents(page efPollBody, names []string) map[string]int {
	counts := map[string]int{}
	for _, name := range names {
		counts[name] = 0
	}
	for _, it := range page.Items {
		if !strings.HasSuffix(it.Event.Type, "circuit_breaker_opened") {
			continue
		}
		for _, name := range names {
			if strings.Contains(string(it.Event.Data), `"name":"`+name+`"`) {
				counts[name]++
			}
		}
	}
	return counts
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

	// Put one distinct operational event into each replica's own buffer: a
	// circuit-breaker open served by replica A and another served by replica B.
	// Each event exists in exactly one replica's ring, so a merged page that
	// carries both proves the fan-out reached both replicas over the headless
	// Service, and a page that carries either twice proves the eventKey dedup
	// did not collapse the overlapping fan-out windows.
	breakerA := "t5-fanout-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "-a"
	breakerB := "t5-fanout-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "-b"
	if !efOpenBreakerOn(t, c, pods[0], breakerA) || !efOpenBreakerOn(t, c, pods[1], breakerB) {
		t.Skip("precondition not met: the gateway replicas did not serve the circuit-breaker open " +
			"used to seed a known event into each per-replica buffer")
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

	// The merged page, rather than the label: both replicas' events must be
	// present and each must appear exactly once. The two polls read the same
	// overlapping fan-out windows a second apart, so an event that survives one
	// poll and duplicates on the next fails here too.
	names := []string{breakerA, breakerB}
	for attempt := 0; attempt < 2; attempt++ {
		page := efPollPage(t, baseURL)
		counts := efCountBreakerEvents(page, names)
		if counts[breakerA] != 1 || counts[breakerB] != 1 {
			t.Fatalf("the gateway-buffer page served %d event(s) from replica %s and %d from replica %s, "+
				"want exactly one each: 0 means the fan-out never merged that replica's buffer (the "+
				"degradation label is attached to a page the cross-process fetch did not fill), and more "+
				"than one means the eventKey dedup did not collapse the overlapping windows (page: %d items)",
				counts[breakerA], pods[0], counts[breakerB], pods[1], len(page.Items))
		}
		time.Sleep(time.Second)
	}
	t.Logf("gateway-buffer page carries both replicas' events exactly once")

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
