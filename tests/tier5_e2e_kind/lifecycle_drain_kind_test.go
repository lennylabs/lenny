// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind tests for the §12.8 lifecycle drain across gateway
// replicas. The suspend case binds one session on each of two gateway
// replicas, suspends the tenant through one of them, and asserts that every
// session is terminal, that each session's pod claim is released within the
// coordination Sweeper interval plus maxWorkspaceSealDurationSeconds, and
// that a later create on either replica is refused with TENANT_SUSPENDED.
// The erasure case lowers the controller's claimOrphanTimeout for its run,
// erases a user whose session is bound on the other replica, and asserts
// that the session's claim is no longer bound after claimOrphanTimeout plus
// one orphan-GC pass, the accepted path when the
// erasure deletes the row before the binding replica's Sweeper lists it.
//
// Both cases bootstrap a tenant whose name carries a per-run suffix and
// sweep, at start, every tenant an earlier killed run left under the same
// prefix.
package tier5_e2e_kind_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// lifecycleDrainTenantPrefix prefixes every tenant these tests bootstrap.
const lifecycleDrainTenantPrefix = "lifecycle-drain-"

// lifecycleDrainSchedulingSlack absorbs the kubelet and controller latency
// between the gateway releasing a claim and the SandboxClaim object
// reflecting it.
const lifecycleDrainSchedulingSlack = 30 * time.Second

// adminRequest issues one platform-admin request through d's port-forward
// and returns the status and the raw body.
func adminRequest(t *testing.T, d *sessiondriver.Driver, tenant, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, d.BaseURL()+path, reader)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	req.Header.Set("X-Lenny-User-ID", "alice")
	res, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, raw
}

// sweepStaleLifecycleTenants deletes every tenant under the test prefix
// that an earlier run left behind. It is best-effort: a failure is logged
// and never fails the run.
func sweepStaleLifecycleTenants(t *testing.T, d *sessiondriver.Driver) {
	t.Helper()
	code, raw := adminRequest(t, d, "platform", http.MethodGet, "/v1/admin/tenants", nil)
	if code != http.StatusOK {
		t.Logf("stale-tenant sweep: list tenants answered %d; skipping the sweep", code)
		return
	}
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Logf("stale-tenant sweep: decode tenant list: %v", err)
		return
	}
	for _, tn := range list.Items {
		if !strings.HasPrefix(tn.ID, lifecycleDrainTenantPrefix) {
			continue
		}
		_, _ = adminRequest(t, d, "platform", http.MethodPost, "/v1/admin/tenants/"+tn.ID+"/resume", nil)
		code, _ := adminRequest(t, d, "platform", http.MethodDelete, "/v1/admin/tenants/"+tn.ID, nil)
		t.Logf("stale-tenant sweep: deleted %s left by an earlier run (status %d)", tn.ID, code)
	}
}

// deploymentDurationArg returns the value of a duration flag on a
// lenny-system Deployment, or def when the flag is absent. A bare integer is read as
// seconds.
func deploymentDurationArg(t *testing.T, c *kind.Cluster, deployment, flag string, def time.Duration) time.Duration {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", "lenny-system", "get", "deploy", deployment,
		"-o", "jsonpath={.spec.template.spec.containers[0].args}")
	if err != nil {
		t.Fatalf("read %s args: %v\n%s", deployment, err, out)
	}
	var args []string
	if err := json.Unmarshal([]byte(out), &args); err != nil {
		t.Fatalf("decode %s args %q: %v", deployment, out, err)
	}
	for _, a := range args {
		v, ok := strings.CutPrefix(a, "--"+flag+"=")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatalf("parse %s --%s=%q: %v", deployment, flag, v, err)
		}
		return d
	}
	return def
}

// pollClock is the clock awaitAllBy reads and sleeps on, injected so a
// unit test can drive the shared deadline without a cluster.
type pollClock struct {
	now   func() time.Time
	sleep func(time.Duration)
}

// wallClock is the real clock the Kind cases poll on.
var wallClock = pollClock{now: time.Now, sleep: time.Sleep}

// claimPollInterval spaces the SandboxClaim reads of one polling round.
const claimPollInterval = time.Second

// awaitAllBy polls every probe in one loop until each reports done or the
// clock passes deadline, and returns the last state of every probe still
// pending at the deadline. Every probe shares the one deadline, so a probe
// that finishes late cannot extend the budget of the probes polled after
// it: the §12.8 drain bound is measured from the lifecycle action for
// every pod at once, rather than from the release of the previous pod.
func awaitAllBy(deadline time.Time, clk pollClock, probes map[string]func() (bool, string)) map[string]string {
	pending := make(map[string]string, len(probes))
	for name := range probes {
		pending[name] = ""
	}
	for {
		for name := range pending {
			done, state := probes[name]()
			if done {
				delete(pending, name)
				continue
			}
			pending[name] = state
		}
		if len(pending) == 0 || !clk.now().Before(deadline) {
			return pending
		}
		clk.sleep(claimPollInterval)
	}
}

// claimReleasedProbe reports whether the pod's SandboxClaim is gone or no
// longer bound, with the phase it last read.
func claimReleasedProbe(t *testing.T, c *kind.Cluster, pod string) func() (bool, string) {
	return func() (bool, string) {
		out, err := c.KubectlOut(t, "-n", "lenny-agents", "get", "sandboxclaim", "claim-"+pod,
			"-o", "jsonpath={.status.phase}", "--ignore-not-found")
		phase := strings.TrimSpace(out)
		if err != nil {
			return false, fmt.Sprintf("read error: %v", err)
		}
		return phase != "bound" && phase != "Bound", phase
	}
}

// waitClaimsReleasedBy fails unless every pod's SandboxClaim is gone or no
// longer bound by the one absolute deadline.
func waitClaimsReleasedBy(t *testing.T, c *kind.Cluster, deadline time.Time, pods ...string) {
	t.Helper()
	probes := make(map[string]func() (bool, string), len(pods))
	for _, pod := range pods {
		probes[pod] = claimReleasedProbe(t, c, pod)
	}
	if pending := awaitAllBy(deadline, wallClock, probes); len(pending) != 0 {
		t.Fatalf("claims still bound at the deadline %s (pod: last phase): %v", deadline.Format(time.RFC3339), pending)
	}
}

// twoReplicaDrivers pins one driver to each of two Ready gateway replicas
// and bootstraps a fresh tenant through the second.
func twoReplicaDrivers(t *testing.T, c *kind.Cluster) (a, b *sessiondriver.Driver, tenant string) {
	t.Helper()
	ensureGatewayReplicas(t, c, 2)
	pods := readyGatewayPods(t, c)
	if len(pods) < 2 {
		t.Skipf("blocked: need two Ready gateway replicas, have %d (%v)", len(pods), pods)
	}
	a = sessiondriver.NewKeptForTarget(t, "pod/"+pods[0])
	t.Cleanup(a.Close)
	b = sessiondriver.NewKeptForTarget(t, "pod/"+pods[1])
	t.Cleanup(b.Close)
	sweepStaleLifecycleTenants(t, b)
	tenant = fmt.Sprintf("%s%d", lifecycleDrainTenantPrefix, time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := b.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}
	ensureTenantAllowsSessionsWithNoEnvironment(t, b, tenant)
	return a, b, tenant
}

// startOn creates and starts a session through d, skipping when the warm
// pool has no pod to claim.
func startOn(t *testing.T, d *sessiondriver.Driver, tenant string) *sessiondriver.Session {
	t.Helper()
	return startOwnedOn(t, d, tenant, "")
}

// startOwnedOn creates and starts a session owned by user through d, or one
// with no owner when user is empty, skipping when the warm pool has no pod
// to claim.
func startOwnedOn(t *testing.T, d *sessiondriver.Driver, tenant, user string) *sessiondriver.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sess, err := d.CreateAndStartWithOptions(ctx, tenant, sessiondriver.EchoRuntimeSidecar,
		sessiondriver.StartOptions{UserID: user})
	if errors.Is(err, sessiondriver.ErrPoolNotReady) {
		t.Skipf("blocked: warm pool not ready: %v", err)
	}
	if err != nil {
		t.Fatalf("create and start: %v", err)
	}
	if sess.PodAssignment == "" {
		t.Fatalf("session %s carries no pod assignment", sess.ID)
	}
	if user != "" && sess.UserID != user {
		t.Fatalf("session %s is owned by %q, want %q", sess.ID, sess.UserID, user)
	}
	return sess
}

// terminalStateWait caps how long requireTerminalBy waits for a session the
// drain already reported terminal to read terminal through a replica.
const terminalStateWait = 20 * time.Second

// requireTerminalBy asserts the session reads terminal through d within
// terminalStateWait and before deadline, whichever comes first.
func requireTerminalBy(t *testing.T, d *sessiondriver.Driver, tenant, id string, deadline time.Time) {
	t.Helper()
	wait := min(terminalStateWait, max(time.Until(deadline), 0))
	ctx, cancel := context.WithTimeout(context.Background(), wait+10*time.Second)
	defer cancel()
	if _, ok := d.WaitForState(ctx, tenant, id, wait, "failed", "completed", "cancelled", "expired"); !ok {
		got, _ := d.GetSession(ctx, tenant, id)
		t.Fatalf("session %s is not terminal after the drain: %+v", id, got)
	}
}

// TestTenantSuspendDrainsSessionsOnTwoReplicas_spec_12_8 suspends a tenant
// whose sessions are bound on two gateway replicas.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 15.1 (REST API), 10.1 (Horizontal Scaling)
// diagnosis: a failure means the suspend drain did not reach a session
// bound on another replica (a session still live), that replica's Sweeper
// did not release the claim of a session the drain made terminal within
// its interval plus the seal bound (a pod held out of the pool), or the
// creation fence admitted a create into the suspended tenant on one of the
// replicas.
func TestTenantSuspendDrainsSessionsOnTwoReplicas_spec_12_8(t *testing.T) {
	c := kind.InstallLenny(t)
	a, b, tenant := twoReplicaDrivers(t, c)
	sessA, sessB := startOn(t, a, tenant), startOn(t, b, tenant)

	// Read the bound before the suspend so the deployment lookups do not
	// eat into it, and start the clock when the suspend answers.
	bound := deploymentDurationArg(t, c, "lenny-gateway", "coordination-interval", 15*time.Second) +
		deploymentDurationArg(t, c, "lenny-gateway", "workspace-seal-max-duration-seconds", 300*time.Second) +
		lifecycleDrainSchedulingSlack
	code, raw := adminRequest(t, b, "platform", http.MethodPost, "/v1/admin/tenants/"+tenant+"/suspend", map[string]any{"reason": "tier-5 drain"})
	deadline := time.Now().Add(bound)
	if code != http.StatusOK {
		t.Fatalf("suspend: %d %s", code, raw)
	}
	var out struct {
		Suspended      bool     `json:"suspended"`
		FailedSessions []string `json:"failedSessions"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || !out.Suspended || len(out.FailedSessions) != 0 {
		t.Fatalf("suspend body = %s (%v), want suspended with no failed sessions", raw, err)
	}
	defer adminRequest(t, b, "platform", http.MethodPost, "/v1/admin/tenants/"+tenant+"/resume", nil)

	// Every check below runs against the one deadline, so a pod released
	// after suspend time plus the bound fails the case whichever session
	// it served and in whatever order the claims are read.
	requireTerminalBy(t, b, tenant, sessA.ID, deadline)
	requireTerminalBy(t, a, tenant, sessB.ID, deadline)
	waitClaimsReleasedBy(t, c, deadline, sessA.PodAssignment, sessB.PodAssignment)

	for name, d := range map[string]*sessiondriver.Driver{"replica A": a, "replica B": b} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := d.CreateSession(ctx, tenant, sessiondriver.EchoRuntimeSidecar)
		cancel()
		if err == nil || !strings.Contains(err.Error(), "TENANT_SUSPENDED") {
			t.Errorf("create on %s after the suspend = %v, want a 403 TENANT_SUSPENDED refusal", name, err)
		}
	}
}

// loweredClaimOrphanTimeout is the claimOrphanTimeout the erasure case sets
// on the controller for its run. The chart refuses any value at or below the
// gateway's 600s finalizing deadline, because §5.2 requires the timeout to
// exceed every post-terminal slot activity window, so the test lowers the
// controller's flag directly instead of through the chart. Only the
// controller's orphan GC reads it, and the GC reclaims only a claim with no
// active session, so a live session elsewhere on the cluster is unaffected.
const loweredClaimOrphanTimeout = 60 * time.Second

// orphanGCPass is one orphan-GC sweep. The warm-pool controller's orphan GC
// runs once a minute and has no flag (pkg/controller/warmpool
// defaultGCInterval).
const orphanGCPass = 60 * time.Second

// controllerLeaderLease is the controller manager's leader-election Lease
// (cmd/lenny-controller LeaderElectionID), whose holder runs the orphan GC.
const controllerLeaderLease = "lenny-warm-pool-controller"

// controllerLeaderWait bounds the wait for a controller pod of the current
// rollout to hold the leader Lease after the flag override.
const controllerLeaderWait = 3 * time.Minute

// waitControllerLeader waits until the controller's leader Lease is held by
// a running controller pod that is not terminating, so the leader-election
// handover after the flag override's rollout has finished before the test
// starts the clock on the orphan-GC bound.
func waitControllerLeader(t *testing.T, c *kind.Cluster) {
	t.Helper()
	deadline := time.Now().Add(controllerLeaderWait)
	for {
		holder, _ := c.KubectlOut(t, "-n", "lenny-system", "get", "lease", controllerLeaderLease,
			"-o", "jsonpath={.spec.holderIdentity}")
		pods, _ := c.KubectlOut(t, "-n", "lenny-system", "get", "pods", "-o",
			`jsonpath={range .items[?(@.status.phase=="Running")]}{.metadata.name} {.metadata.deletionTimestamp}{"\n"}{end}`)
		if leaderIsLivePod(strings.TrimSpace(holder), pods) {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("no running lenny-controller pod holds lease %s after %s (holder %q)", controllerLeaderLease, controllerLeaderWait, holder)
		}
		time.Sleep(2 * time.Second)
	}
}

// leaderIsLivePod reports whether holder, a controller-runtime holder
// identity of the form <pod>_<uuid>, names a running controller pod with no
// deletion timestamp in pods, one "<name> <deletionTimestamp>" per line.
func leaderIsLivePod(holder, pods string) bool {
	pod, _, ok := strings.Cut(holder, "_")
	if !ok || !strings.HasPrefix(pod, "lenny-controller-") {
		return false
	}
	for _, line := range strings.Split(pods, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 1 && fields[0] == pod {
			return true
		}
	}
	return false
}

// lowerClaimOrphanTimeout sets the controller's --claim-orphan-timeout to
// loweredClaimOrphanTimeout for the test and restores it afterwards. It also
// restores, at start, an override an earlier killed run left. It skips only
// when the Deployment cannot be patched.
func lowerClaimOrphanTimeout(t *testing.T, c *kind.Cluster) {
	t.Helper()
	if err := c.OverrideDeploymentFlag(t, "lenny-system", "lenny-controller",
		"claim-orphan-timeout", loweredClaimOrphanTimeout.String()); err != nil {
		t.Skipf("blocked: cannot lower the controller's claimOrphanTimeout: %v", err)
	}
	if got := deploymentDurationArg(t, c, "lenny-controller", "claim-orphan-timeout", 0); got != loweredClaimOrphanTimeout {
		t.Fatalf("controller --claim-orphan-timeout = %s after the override, want %s", got, loweredClaimOrphanTimeout)
	}
}

// TestUserErasureReleasesClaimOnOtherReplica_spec_12_8 erases a user whose
// session is bound on the other gateway replica, with the controller's
// claimOrphanTimeout lowered for the run. The erasure deletes the session
// row, so the binding replica may never list it terminal; the orphan GC then
// releases the claim after claimOrphanTimeout.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 12.8 (User-level erasure invocation), 4.6.1 (Orphan claim detection)
// diagnosis: a failure means a pod bound to an erased user's session stays
// bound past claimOrphanTimeout plus one orphan-GC pass, so an erasure
// strands a pod out of the warm pool, or the erasure job never completed.
func TestUserErasureReleasesClaimOnOtherReplica_spec_12_8(t *testing.T) {
	c := kind.InstallLenny(t)
	lowerClaimOrphanTimeout(t, c)
	waitControllerLeader(t, c)
	a, b, tenant := twoReplicaDrivers(t, c)
	// The erasure target must be a registered user of the tenant, and the
	// session must be owned by it: the §12.8 drain and erasers act on the
	// rows whose owner is the erased user, which is the body's userId.
	const user = "alice"
	if code, raw := adminRequest(t, b, "platform", http.MethodPost, "/v1/admin/bootstrap", map[string]any{
		"tenants": []map[string]any{{"id": tenant}},
		"users":   []map[string]any{{"subject": user, "tenantId": tenant, "email": user + "@acme.com", "roles": []string{"user"}}},
	}); code != http.StatusOK && code != http.StatusMultiStatus {
		t.Fatalf("register %s: %d %s", user, code, raw)
	}
	sess := startOwnedOn(t, a, tenant, user)

	code, raw := adminRequest(t, b, "platform", http.MethodPost, "/v1/admin/users/"+user+"/erase", map[string]any{"tenantId": tenant})
	if code != http.StatusAccepted && code != http.StatusOK {
		t.Fatalf("erase %s: %d %s", user, code, raw)
	}
	var started struct {
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(raw, &started); err != nil || started.JobID == "" {
		t.Fatalf("erase body %s carries no jobId (%v)", raw, err)
	}
	jobDeadline := time.Now().Add(3 * time.Minute)
	for {
		code, raw := adminRequest(t, b, "platform", http.MethodGet, "/v1/admin/erasure-jobs/"+started.JobID, nil)
		var job struct {
			Phase string `json:"phase"`
		}
		_ = json.Unmarshal(raw, &job)
		if code == http.StatusOK && job.Phase == "completed" {
			break
		}
		if job.Phase == "failed" || !time.Now().Before(jobDeadline) {
			t.Fatalf("erasure job %s did not complete: %d %s", started.JobID, code, raw)
		}
		time.Sleep(2 * time.Second)
	}
	waitClaimsReleasedBy(t, c,
		time.Now().Add(loweredClaimOrphanTimeout+orphanGCPass+lifecycleDrainSchedulingSlack), sess.PodAssignment)
}
