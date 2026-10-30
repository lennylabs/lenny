// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind tests for the §12.8 lifecycle drain across gateway
// replicas. The suspend case binds one session on each of two gateway
// replicas, suspends the tenant through one of them, and asserts that every
// session is terminal, that each session's pod returns to the pool (its
// claim deleted and its Sandbox out of the occupied phases) within the
// coordination Sweeper interval plus maxWorkspaceSealDurationSeconds, and
// that a later create on either replica is refused with TENANT_SUSPENDED.
// The erasure case erases a user whose session is bound on the other
// replica, with the controller's claimOrphanTimeout at its installed value,
// and asserts that the session's SandboxClaim is no longer bound (deleted,
// or in any phase other than bound) within the CH-BINDRELEASE apply window
// plus maxWorkspaceSealDurationSeconds, which it requires to be below
// claimOrphanTimeout: the erasure drain publishes the session on
// CH-BINDRELEASE and the binding replica releases its binding. The erasure
// case does not require the pod to have returned to the pool: deleting the
// claim and re-warming or retiring the Sandbox may finish after that bound.
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

// suspendReleaseBound is the §12.8 bound within which every pod a suspend
// drained returns to the pool, measured from the suspend's answer: the
// coordination Sweeper interval plus maxWorkspaceSealDurationSeconds.
//
// spec: §12.8 (Lifecycle fence and drain: Drain)
func suspendReleaseBound(sweeperInterval, sealMax time.Duration) time.Duration {
	return sweeperInterval + sealMax
}

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

// podReturnedToPool reports whether a pod's occupancy has ended and its
// warm-pool slot is free again, from its per-pod SandboxClaim (claimFound
// and claimPhase) and its Sandbox phase ("" when the Sandbox is gone).
//
// The release that ends an occupancy deletes the claim (§4.6.3), so a claim
// that still exists in any phase holds the pod out of the pool: bound,
// recycling, and reserved keep it occupied, and released and failed are the
// retirement dispositions the controller has not yet projected. With the
// claim gone, the Sandbox must have left the occupied phases (claimed,
// reserved, and the re-warm sdk_connecting leg): idle is a pod returned for
// reuse, and draining, terminated, or a deleted Sandbox is a pod retired
// under §4.6.1, whose slot the warm pool refills with a fresh pod. A
// failed session on a pool that does not recycle, which is every session
// the drain force-terminates on the echo pool, takes the retire edge.
//
// spec: §12.8 (Lifecycle fence and drain: Drain), §4.6.1 (occupancy
// projection on claim DELETE), §4.6.3 (SandboxClaim binding state)
func podReturnedToPool(claimFound bool, claimPhase, sandboxPhase string) (bool, string) {
	state := fmt.Sprintf("claim %s, sandbox %q", claimState(claimFound, claimPhase), sandboxPhase)
	if claimFound {
		return false, state
	}
	switch sandboxPhase {
	case "", "idle", "draining", "terminated":
		return true, state
	default:
		return false, state
	}
}

// claimState names a claim's observed state for a probe's report.
func claimState(found bool, phase string) string {
	if !found {
		return "deleted"
	}
	return fmt.Sprintf("present in phase %q", phase)
}

// claimNoLongerBound reports whether a pod's per-pod SandboxClaim has left
// the bound phase, from claimFound and claimPhase. It is the erasure case's
// condition: a claim that is deleted, or present in any phase other than
// bound (released and failed included), no longer binds the erased user's
// session to the pod. Unlike podReturnedToPool it does not wait for the
// controller to delete the claim or for the Sandbox to leave the occupied
// phases, because the release bound covers only the binding's release.
//
// spec: §12.8 (Compliance Interfaces: Lifecycle fence and drain), §11.4
// step 2 (User Invalidation), §4.6.3 (SandboxClaim binding state)
func claimNoLongerBound(claimFound bool, claimPhase string) (bool, string) {
	state := "claim " + claimState(claimFound, claimPhase)
	return !claimFound || claimPhase != "bound", state
}

// readClaim reads the pod's per-pod SandboxClaim, reporting whether it
// exists and its status.phase.
func readClaim(t *testing.T, c *kind.Cluster, pod string) (found bool, phase string, err error) {
	claim, err := c.KubectlOut(t, "-n", "lenny-agents", "get", "sandboxclaim", "claim-"+pod,
		"-o", "jsonpath={.metadata.name}{\" \"}{.status.phase}", "--ignore-not-found")
	if err != nil {
		return false, "", fmt.Errorf("read claim claim-%s: %w", pod, err)
	}
	name, phase, _ := strings.Cut(strings.TrimSpace(claim), " ")
	return name != "", strings.TrimSpace(phase), nil
}

// podReturnedProbe reports whether the pod has returned to the pool, as
// podReturnedToPool defines it, with the claim and Sandbox state it last
// read. The suspend case polls it.
func podReturnedProbe(t *testing.T, c *kind.Cluster, pod string) func() (bool, string) {
	return func() (bool, string) {
		found, phase, err := readClaim(t, c, pod)
		if err != nil {
			return false, err.Error()
		}
		sandbox, err := c.KubectlOut(t, "-n", "lenny-agents", "get", "sandbox", pod,
			"-o", "jsonpath={.status.phase}", "--ignore-not-found")
		if err != nil {
			return false, fmt.Sprintf("read sandbox: %v", err)
		}
		return podReturnedToPool(found, phase, strings.TrimSpace(sandbox))
	}
}

// claimUnboundProbe reports whether the pod's claim is no longer bound, as
// claimNoLongerBound defines it, with the claim state it last read. The
// erasure case polls it.
func claimUnboundProbe(t *testing.T, c *kind.Cluster, pod string) func() (bool, string) {
	return func() (bool, string) {
		found, phase, err := readClaim(t, c, pod)
		if err != nil {
			return false, err.Error()
		}
		return claimNoLongerBound(found, phase)
	}
}

// waitPodsBy fails unless the probe newProbe builds for every pod reports
// done by the one absolute deadline. want names the condition in the
// failure message.
func waitPodsBy(t *testing.T, c *kind.Cluster, deadline time.Time, want string,
	newProbe func(*testing.T, *kind.Cluster, string) func() (bool, string), pods ...string,
) {
	t.Helper()
	probes := make(map[string]func() (bool, string), len(pods))
	for _, pod := range pods {
		probes[pod] = newProbe(t, c, pod)
	}
	if pending := awaitAllBy(deadline, wallClock, probes); len(pending) != 0 {
		t.Fatalf("%s at the deadline %s fails for (pod: last observed state): %v", want, deadline.Format(time.RFC3339), pending)
	}
}

// waitPodsReturnedBy fails unless every pod has returned to the pool, as
// podReturnedToPool defines it, by the one absolute deadline.
func waitPodsReturnedBy(t *testing.T, c *kind.Cluster, deadline time.Time, pods ...string) {
	t.Helper()
	waitPodsBy(t, c, deadline, "pods returned to the pool", podReturnedProbe, pods...)
}

// waitClaimsUnboundBy fails unless every pod's claim is no longer bound, as
// claimNoLongerBound defines it, by the one absolute deadline.
func waitClaimsUnboundBy(t *testing.T, c *kind.Cluster, deadline time.Time, pods ...string) {
	t.Helper()
	waitPodsBy(t, c, deadline, "claims no longer bound", claimUnboundProbe, pods...)
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
	bound := suspendReleaseBound(
		deploymentDurationArg(t, c, "lenny-gateway", "coordination-interval", 15*time.Second),
		deploymentDurationArg(t, c, "lenny-gateway", "workspace-seal-max-duration-seconds", 300*time.Second),
	)
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
	// it served and in whatever order the claims are read. The poller
	// reads every pod once more after the deadline passes, so a release
	// completed by the deadline is observed without any slack on the bound.
	requireTerminalBy(t, b, tenant, sessA.ID, deadline)
	requireTerminalBy(t, a, tenant, sessB.ID, deadline)
	waitPodsReturnedBy(t, c, deadline, sessA.PodAssignment, sessB.PodAssignment)

	for name, d := range map[string]*sessiondriver.Driver{"replica A": a, "replica B": b} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := d.CreateSession(ctx, tenant, sessiondriver.EchoRuntimeSidecar)
		cancel()
		if err == nil || !strings.Contains(err.Error(), "TENANT_SUSPENDED") {
			t.Errorf("create on %s after the suspend = %v, want a 403 TENANT_SUSPENDED refusal", name, err)
		}
	}
}

// bindReleaseApplyWindow is the window within which a peer replica applies
// a release request published on CH-BINDRELEASE. §11.4 and the
// CH-BINDRELEASE card state that peers apply it "within seconds"; the test
// reads that as 30 seconds.
const bindReleaseApplyWindow = 30 * time.Second

// erasureJobSlack bounds how long the erasure job may run past the release
// bound: once the drained session's post-terminal window ends with the
// release, the job still deletes the user's stores before it completes.
const erasureJobSlack = 2 * time.Minute

// controllerClaimOrphanTimeoutFlag is the warm-pool controller flag that
// carries the §4.6.1 claimOrphanTimeout.
const controllerClaimOrphanTimeoutFlag = "claim-orphan-timeout"

// erasureReleaseBound is the bound within which the claim of a pod bound,
// on another replica, to an erased user's session is no longer bound,
// measured from the erase request's answer: the CH-BINDRELEASE apply window
// plus maxWorkspaceSealDurationSeconds, because the release the binding
// replica runs on the published request may seal the workspace first.
//
// spec: §12.8 (Compliance Interfaces: Lifecycle fence and drain), §11.4 step 2 (User Invalidation), §28.5.7 (Gateway-to-store)
func erasureReleaseBound(applyWindow, sealMax time.Duration) time.Duration {
	return applyWindow + sealMax
}

// requireBoundBelowOrphanTimeout fails unless bound is strictly below the
// controller's claimOrphanTimeout, so the orphan GC cannot be what moves the
// claim out of bound within it. It first restores a claimOrphanTimeout
// override a killed run of an earlier revision of this case left.
func requireBoundBelowOrphanTimeout(t *testing.T, c *kind.Cluster, bound time.Duration) {
	t.Helper()
	if err := c.RestoreDeploymentFlag(t, "lenny-system", "lenny-controller", controllerClaimOrphanTimeoutFlag); err != nil {
		t.Fatalf("restore an earlier --%s override: %v", controllerClaimOrphanTimeoutFlag, err)
	}
	orphan := deploymentDurationArg(t, c, "lenny-controller", controllerClaimOrphanTimeoutFlag, 15*time.Minute)
	if bound >= orphan {
		t.Fatalf("the release bound %s is not below the controller's claimOrphanTimeout %s; the orphan GC "+
			"could satisfy it, so the case cannot show the drain's release", bound, orphan)
	}
}

// waitErasureJob polls the erasure job until it completes, failing when it
// fails or is still running at deadline.
func waitErasureJob(t *testing.T, d *sessiondriver.Driver, jobID string, deadline time.Time) {
	t.Helper()
	for {
		code, raw := adminRequest(t, d, "platform", http.MethodGet, "/v1/admin/erasure-jobs/"+jobID, nil)
		var job struct {
			Phase string `json:"phase"`
		}
		_ = json.Unmarshal(raw, &job)
		if code == http.StatusOK && job.Phase == "completed" {
			return
		}
		if job.Phase == "failed" || !time.Now().Before(deadline) {
			t.Fatalf("erasure job %s did not complete: %d %s", jobID, code, raw)
		}
		time.Sleep(2 * time.Second)
	}
}

// TestUserErasureReleasesTheRemoteBindingOnBindRelease_spec_12_8 erases a
// user whose session is bound on the other gateway replica, with the
// controller's claimOrphanTimeout at its installed value. The erasure's
// drain publishes the session it moved to a terminal state on
// CH-BINDRELEASE, and the binding replica releases the binding as §11.4
// step 2 states. The case asserts that the erasure job completes and that
// the session's claim is no longer bound within the CH-BINDRELEASE apply
// window plus maxWorkspaceSealDurationSeconds, a bound it requires to be
// below claimOrphanTimeout so the orphan GC cannot satisfy it. The binding
// replica's coordination Sweeper also releases a terminal session's binding
// on its next listing, so the case shows that the remote binding is
// released after erasure; the in-process drain tests show that the publish
// is what reaches a peer.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 12.8 (User-level erasure invocation), 28.5.7 (Gateway-to-store), 11.4 (User Invalidation)
// diagnosis: a failure means the claim of a pod bound on another replica to
// an erased user's session is still bound past the release bound, so the
// erasure drain's release did not reach the binding replica (no publish on
// CH-BINDRELEASE, no subscriber, or a subscriber that refused the terminal
// row) and no Sweeper listing released it either, or the erasure job never completed because the drained session's
// post-terminal window never ended.
func TestUserErasureReleasesTheRemoteBindingOnBindRelease_spec_12_8(t *testing.T) {
	c := kind.InstallLenny(t)
	bound := erasureReleaseBound(bindReleaseApplyWindow,
		deploymentDurationArg(t, c, "lenny-gateway", "workspace-seal-max-duration-seconds", 300*time.Second))
	requireBoundBelowOrphanTimeout(t, c, bound)
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
	// The session is bound on replica A and the erasure runs on replica B,
	// so only the published release reaches the binding.
	sess := startOwnedOn(t, a, tenant, user)

	code, raw := adminRequest(t, b, "platform", http.MethodPost, "/v1/admin/users/"+user+"/erase", map[string]any{"tenantId": tenant})
	// The deadline is taken when the erase answers: the drain and its
	// publish run inside the job after it.
	deadline := time.Now().Add(bound)
	if code != http.StatusAccepted && code != http.StatusOK {
		t.Fatalf("erase %s: %d %s", user, code, raw)
	}
	var started struct {
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(raw, &started); err != nil || started.JobID == "" {
		t.Fatalf("erase body %s carries no jobId (%v)", raw, err)
	}
	// The claim is read against the deadline before the job is awaited, so
	// a release that lands after the deadline fails the case even when the
	// job completes later.
	waitClaimsUnboundBy(t, c, deadline, sess.PodAssignment)
	waitErasureJob(t, b, started.JobID, deadline.Add(erasureJobSlack))
}
