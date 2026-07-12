// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// queueProbe captures the §16.1 pod-claim queue metric callbacks so a test can
// synchronize on FIFO depth (both queued waiters present) and observe the
// queue-wait timeout counter deterministically, rather than sleeping.
type queueProbe struct {
	maxDepth atomic.Int32
	timeouts atomic.Int32
}

func (p *queueProbe) onDepth(_ string, depth int) {
	for {
		m := p.maxDepth.Load()
		if int32(depth) <= m || p.maxDepth.CompareAndSwap(m, int32(depth)) {
			return
		}
	}
}

func (p *queueProbe) onTimeout(_ string) { p.timeouts.Add(1) }

// queueExhaustionPool is the §5.2 poolstore record whose sessionPolicy folds
// the onPoolExhausted queue disposition and its wait bound onto the PoolMatch
// the start path's claim queue reads. The CRD pair (SandboxWarmPool +
// SandboxTemplate) does not carry these fields; the poolstore is the source.
func queueExhaustionPool(maxQueueWaitSeconds int) poolstore.Pool {
	return poolstore.Pool{
		Name:       "echo-pool",
		RuntimeRef: "echo",
		SessionPolicy: &runtimestore.SessionPolicy{
			OnPoolExhausted:     runtimestore.PoolExhaustedQueue,
			MaxQueueWaitSeconds: maxQueueWaitSeconds,
		},
	}
}

// queueExhaustionServer wires a session server against a real kube-apiserver
// (envtest) seeded with an echo-pool sized to the given idle Sandboxes, plus a
// poolstore pool carrying the onPoolExhausted queue disposition. The claim
// queue polls at poll so a freed pod is re-acquired within a few milliseconds.
func queueExhaustionServer(t *testing.T, pool poolstore.Pool, poll time.Duration, sandboxes ...string) (
	*sessionserver.Server, client.Client, *memstore.Store, *queueProbe,
) {
	t.Helper()
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}

	objs := []client.Object{
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
	}
	for i, name := range sandboxes {
		objs = append(objs, podBindIdleSandbox(name, "echo-pool", fmt.Sprintf("10.244.2.%d", 5+i)))
	}
	cluster := podBindClient(t, objs...)

	pools := poolstore.NewMemory()
	if err := pools.Create(context.Background(), pool); err != nil {
		t.Fatalf("create poolstore pool: %v", err)
	}

	probe := &queueProbe{}
	var idCounter atomic.Int64
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return fmt.Sprintf("sess-q-%d", idCounter.Add(1)) },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv)),
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          podTestNS,
		Pools:                   pools,
		QueuePollInterval:       poll,
		SetPodClaimQueueDepth:   probe.onDepth,
		IncPodClaimTimeout:      probe.onTimeout,
	})
	return srv, cluster, store, probe
}

// fireQueueCreate issues one create against the handler with a distinct user
// and returns the recorded response. It is safe to call from a goroutine: each
// call builds its own request and recorder.
func fireQueueCreate(h http.Handler, userID string) *httptest.ResponseRecorder {
	body := mustJSON(sessionserver.CreateSessionRequest{RuntimeRef: "echo", UserID: userID})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func waitForQueue(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, desc)
}

// liveClaimCount returns the number of SandboxClaim resources in the agent
// namespace. It is the observable for "a queued request holds no claim": only
// the pod A holds carries a claim while B and C wait.
func liveClaimCount(t *testing.T, c client.Client) int {
	t.Helper()
	var list lennyv1.SandboxClaimList
	if err := c.List(context.Background(), &list, client.InNamespace(podTestNS)); err != nil {
		t.Fatalf("list SandboxClaims: %v", err)
	}
	return len(list.Items)
}

// persistedRowCount returns the number of session rows for tenant acme. A
// queued request holds no session: the row persists only after the create-time
// claim succeeds (§7.1 atomicity), so a waiting request contributes no row.
func persistedRowCount(t *testing.T, s *memstore.Store) int {
	t.Helper()
	rows, err := s.List(context.Background(), "acme", sessionstore.ListFilter{})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	return len(rows)
}

func claimExists(t *testing.T, c client.Client, podName string) bool {
	t.Helper()
	var claim lennyv1.SandboxClaim
	err := c.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: podclaim.ClaimName(podName)}, &claim)
	return err == nil
}

// spec: §4.6.1 (Pool exhaustion behavior: the per-pool claim FIFO parameterized
// by sessionPolicy.onPoolExhausted; with `queue` the request "remains in the
// same per-pool FIFO ... and re-enters acquisition as pods free. A queued
// request holds no pod, no slot, and no claim, and the §7.1 atomicity contract
// is preserved"), §5.2 (onPoolExhausted, maxQueueWaitSeconds), §7.1 (session_id
// only on success).
// diagnosis: a failure means the onPoolExhausted:queue FIFO does not drain
// against real pod-claim contention on a real kube-apiserver. If a queued
// create never returns 201 after the held pod is released, the queue does not
// re-enter acquisition as pods free (the §4.6.1 draining property is broken).
// If more than one SandboxClaim exists while two requests wait, a queued
// request is holding a claim it should not (the "holds no claim" invariant is
// broken) or the per-pod CREATE guard let two claims race onto one pod. If a
// session row exists for a waiting request, the gateway persisted a row before
// the claim succeeded, breaking the §7.1 "session_id only on success"
// atomicity contract.
func TestQueueDispositionDrainsAsPodsFreeOnRealPool(t *testing.T) {
	srv, cluster, store, probe := queueExhaustionServer(t, queueExhaustionPool(30), 10*time.Millisecond, "sbx-1")
	h := srv.Handler()
	ctx := context.Background()

	// A claims the single warm pod and holds it through the created window.
	if rr := fireQueueCreate(h, "alice@acme.com"); rr.Code != http.StatusCreated {
		t.Fatalf("create A on the single-pod pool: status %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	waitForQueue(t, 10*time.Second, "A's per-pod claim to exist", func() bool { return claimExists(t, cluster, "sbx-1") })

	// B and C: the pool is exhausted, so each enters the FIFO rather than
	// failing immediately (the `reject` default would fail here).
	results := make(chan int, 2)
	for _, user := range []string{"bob@acme.com", "carol@acme.com"} {
		go func() { results <- fireQueueCreate(h, user).Code }()
	}
	// Both requests reach the FIFO (depth 2) while A holds the only pod.
	waitForQueue(t, 10*time.Second, "both waiters queued (depth 2)", func() bool { return probe.maxDepth.Load() >= 2 })

	// While queued, neither B nor C holds a pod, slot, or claim: exactly the one
	// claim A created exists, and only A's session row is persisted.
	if n := liveClaimCount(t, cluster); n != 1 {
		t.Fatalf("SandboxClaim count while two requests are queued = %d, want 1 (a queued request holds no claim)", n)
	}
	if n := persistedRowCount(t, store); n != 1 {
		t.Fatalf("persisted session rows while two requests are queued = %d, want 1 (a queued request holds no session; §7.1)", n)
	}

	// Release the pod. A queued request re-enters acquisition and succeeds; the
	// per-pod CREATE guard admits exactly one waiter onto the freed pod.
	if err := podclaim.DeleteClaim(ctx, cluster, podTestNS, "sbx-1"); err != nil {
		t.Fatalf("release round 1 (delete claim): %v", err)
	}
	if first := <-results; first != http.StatusCreated {
		t.Fatalf("first queued create after release = %d, want 201 (the FIFO did not drain as the pod freed)", first)
	}

	// Exactly one waiter claimed the freed pod; the other is still queued.
	if n := liveClaimCount(t, cluster); n != 1 {
		t.Fatalf("SandboxClaim count after one release = %d, want 1 (one waiter claimed, one still queued)", n)
	}

	// Release again for the remaining waiter, which then drains too.
	if err := podclaim.DeleteClaim(ctx, cluster, podTestNS, "sbx-1"); err != nil {
		t.Fatalf("release round 2 (delete claim): %v", err)
	}
	if second := <-results; second != http.StatusCreated {
		t.Fatalf("second queued create after release = %d, want 201 (the FIFO did not fully drain)", second)
	}
}

// spec: §4.6.1 ("On queue-wait timeout the gateway returns WARM_POOL_EXHAUSTED
// with a Retry-After header"), §5.2 (maxQueueWaitSeconds bound), §7.1 line 23
// (a create-time pod-claim exhaustion is part of the atomic creation unit).
// diagnosis: a failure means a create-time queue-wait timeout on an exhausted
// pool does not surface the retryable exhaustion envelope. If the response is
// not 503, or omits Retry-After, a client cannot back off with a deterministic
// budget. If the queue-wait timeout counter did not increment, the request left
// the FIFO by some path other than the maxQueueWaitSeconds bound. If a session
// row or a second claim exists afterward, the timed-out request leaked state,
// breaking the §7.1 "no row on failure" / "holds no claim while queued"
// invariants. The code is SESSION_CREATION_FAILED because a create-time
// exhaustion is the §7.1 atomicity envelope; the raw §4.6.1 WARM_POOL_EXHAUSTED
// code is the two-step /start claim's, not the eager create-time claim's.
func TestQueueWaitTimeoutReturnsRetryableExhaustionOnRealPool(t *testing.T) {
	srv, cluster, store, probe := queueExhaustionServer(t, queueExhaustionPool(1), 10*time.Millisecond, "sbx-1")
	h := srv.Handler()

	// A holds the single pod for the whole test.
	if rr := fireQueueCreate(h, "alice@acme.com"); rr.Code != http.StatusCreated {
		t.Fatalf("create A on the single-pod pool: status %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	waitForQueue(t, 10*time.Second, "A's per-pod claim to exist", func() bool { return claimExists(t, cluster, "sbx-1") })

	// B queues, never gets a freed pod, and returns when the 1s wait bound elapses.
	start := time.Now()
	rrB := fireQueueCreate(h, "bob@acme.com")
	elapsed := time.Since(start)

	if rrB.Code != http.StatusServiceUnavailable {
		t.Fatalf("queue-wait timeout: status %d, want 503; body=%s", rrB.Code, rrB.Body.String())
	}
	if ra := rrB.Header().Get("Retry-After"); ra == "" {
		t.Error("Retry-After header missing on the queue-wait timeout reply (§4.6.1 requires it)")
	}
	if probe.timeouts.Load() == 0 {
		t.Error("queue-wait timeout counter did not increment; the request did not leave the FIFO via the maxQueueWaitSeconds bound")
	}
	if elapsed < time.Second {
		t.Errorf("queue-wait returned after %s, want >= the 1s maxQueueWaitSeconds bound (it did not actually wait)", elapsed)
	}

	// The timed-out request held nothing: still one claim (A's), still one row (A's).
	if n := liveClaimCount(t, cluster); n != 1 {
		t.Fatalf("SandboxClaim count after the queue-wait timeout = %d, want 1 (the timed-out request holds no claim)", n)
	}
	if n := persistedRowCount(t, store); n != 1 {
		t.Fatalf("persisted session rows after the queue-wait timeout = %d, want 1 (§7.1: no row on a failed create)", n)
	}

	// The create-time queue-wait timeout surfaces the §7.1 SESSION_CREATION_FAILED
	// atomicity envelope (the create-path mapping of the §4.6.1 exhaustion
	// disposition), not the raw §5.2 WARM_POOL_EXHAUSTED code the /start path keeps.
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rrB.Body.Bytes(), &env)
	if env.Error.Code != "SESSION_CREATION_FAILED" {
		t.Errorf("code = %q, want SESSION_CREATION_FAILED (create-time exhaustion is the §7.1 atomicity envelope)", env.Error.Code)
	}
	if env.Error.Details["reason"] != "no_idle_pods" {
		t.Errorf("details.reason = %v, want no_idle_pods", env.Error.Details["reason"])
	}
}
