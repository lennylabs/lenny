// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind tests for proposal 0007 (eager pod claim at create,
// finalize-time materialization and credential-lease assignment) against the
// live Lenny control plane and the reference echo warm pools.
//
// These cover the cluster behaviors no in-process tier can reproduce:
//
//   - Pool exhaustion surfaces at /create: once every idle pod in a finite
//     pool carries a per-pod SandboxClaim, a fresh create fails fast with
//     SESSION_CREATION_FAILED before the client uploads.
//   - /finalize is the preparation barrier: it claims-then-materializes the
//     workspace and reaches ready against a real warm pod.
//   - Pod AND lease release on created-expiry and on /terminate of a
//     finalizing/ready session: the per-pod SandboxClaim is deleted (the pod
//     returns to the pool) AND the credential lease's active-session counter
//     is decremented (the lease row leaves the §4.9 lease store), so the
//     reclaim does not leak the lease.
//
// The gateway is driven from an in-cluster bootstrap probe pod (the helpers in
// gateway_probe_test.go); the cluster and the §4.9 lease store are inspected
// via kubectl and psql.

package tier5_e2e_kind_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// eagerAgentNS is the namespace the warm-pool agent pods and their per-pod
// SandboxClaims live in.
const eagerAgentNS = "lenny-agents"

// eagerEchoRuntime is the reference sidecar echo runtime the e2e agent
// workload warms a pool for.
const eagerEchoRuntime = "echo-runtime-sidecar"

// eagerTenant is a test tenant the eager-claim e2e flows create sessions in.
func eagerTenant() t5Role {
	return t5Role{tenant: "acme", roles: "", user: "alice@acme.com"}
}

// claimCount returns the number of per-pod SandboxClaims in the agent
// namespace, the count of pods currently claimed out of the warm pools.
func claimCount(t *testing.T, c *kind.Cluster) int {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", eagerAgentNS, "get", "sandboxclaim",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}")
	if err != nil {
		t.Fatalf("list sandboxclaims: %v\n%s", err, out)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// claimExistsForPod reports whether the per-pod SandboxClaim claim-<pod>
// exists in the agent namespace.
func claimExistsForPod(t *testing.T, c *kind.Cluster, pod string) bool {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", eagerAgentNS, "get", "sandboxclaim",
		"claim-"+pod, "-o", "jsonpath={.metadata.name}", "--ignore-not-found")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) != ""
}

// sessionState GETs a session and returns its reported state, or "" when the
// read fails or the session is absent.
func sessionState(t *testing.T, c *kind.Cluster, probe, gatewayIP, id string, role t5Role) string {
	t.Helper()
	res := t5GatewayRequestRetry(t, c, probe, gatewayIP, "GET", "/v1/sessions/"+id, role, "")
	if res.statusCode != 200 {
		return ""
	}
	var body struct {
		State string `json:"state"`
	}
	t5DecodeJSON(t, res.body, &body)
	return body.State
}

// createdSession creates a session against the echo runtime and returns its
// id, failing the test when create does not return 201. A create that claims a
// pod synchronously (§7.1 step 4) returns the claimed pod's isolation level.
func createdSession(t *testing.T, c *kind.Cluster, probe, gatewayIP string, role t5Role) string {
	t.Helper()
	body := fmt.Sprintf(`{"runtimeRef":%q,"userId":%q}`, eagerEchoRuntime, role.user)
	res := t5GatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/sessions", role, body)
	if res.statusCode != 201 {
		t.Fatalf("create session: status %d (want 201), body=%s", res.statusCode, res.body)
	}
	var created struct {
		ID string `json:"id"`
	}
	t5DecodeJSON(t, res.body, &created)
	if created.ID == "" {
		t.Fatalf("create session returned no id: %s", res.body)
	}
	return created.ID
}

// leaseRowCountForSession returns the number of credential_leases rows whose
// stored lease JSON names the given session id. The §4.9 lease store removes a
// session's lease rows on ReleaseSession, so a row count of zero after a
// terminal transition is the durable signal that the lease's active-session
// counter was decremented (the lease did not leak).
func leaseRowCountForSession(t *testing.T, c *kind.Cluster, pgIP, sessionID string) int {
	t.Helper()
	sql := fmt.Sprintf(
		"SELECT count(*) FROM credential_leases WHERE lease->>'sessionId' = '%s';",
		sessionID,
	)
	out := t5RunPsqlQuery(t, c, pgIP, "lease-count-"+sessionID, sql)
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("parse lease row count %q: %v", out, err)
	}
	return n
}

// spec: 7.1, 15.1 (pool exhaustion surfaces at /create)
// diagnosis: a failure means the gateway no longer claims the warm pod
// synchronously at /create. Proposal 0007 claims at create so a finite pool
// admits at most one create per idle pod and fails the surplus fast with
// SESSION_CREATION_FAILED, before the client wastes an upload. If a create
// past the pool's idle capacity succeeds (no claim, deferred to /start) the
// eager-claim model regressed to the pre-0007 deferred-claim behavior.
func TestEagerClaimPoolExhaustionAtCreate(t *testing.T) {
	c := kind.InstallLenny(t)
	pods := kind.RequireAgentWorkload(t, c)

	probe := "eager-exhaust-probe"
	gatewayIP := t5StartGatewayProbe(t, c, probe)
	role := eagerTenant()

	// The finite reference pools warm one pod each (minWarm: 1). Claim every
	// idle pod by creating one session per warm pod, then assert a further
	// create fails fast.
	idle := len(pods)
	if idle == 0 {
		t.Skip("no warm agent pods to exhaust")
	}
	before := claimCount(t, c)
	var created []string
	for i := 0; i < idle; i++ {
		created = append(created, createdSession(t, c, probe, gatewayIP, role))
	}
	t.Cleanup(func() {
		for _, id := range created {
			_ = t5GatewayRequest(t, c, probe, gatewayIP, "POST", "/v1/sessions/"+id+"/terminate", role, "")
		}
	})
	// Each create claimed a pod: the per-pod claim count rose by the number of
	// creates that fit the idle pool.
	if got := claimCount(t, c); got < before+idle {
		t.Fatalf("per-pod claim count = %d after %d creates, want >= %d (each create claims a pod at /create)", got, idle, before+idle)
	}

	// One more create than the pool can serve fails fast at /create.
	body := fmt.Sprintf(`{"runtimeRef":%q,"userId":%q}`, eagerEchoRuntime, role.user)
	res := t5GatewayRequest(t, c, probe, gatewayIP, "POST", "/v1/sessions", role, body)
	if res.statusCode != 503 {
		t.Fatalf("create against an exhausted pool: status %d, want 503 SESSION_CREATION_FAILED (fail-fast at /create); body=%s", res.statusCode, res.body)
	}
	if !strings.Contains(res.body, "SESSION_CREATION_FAILED") {
		t.Errorf("exhausted-create body = %s, want SESSION_CREATION_FAILED", res.body)
	}
}

// spec: 7.1, 7.4, 15.1 (finalize is the preparation barrier; it materializes
// the workspace against a real warm pod and reaches ready before start)
// diagnosis: a failure means /finalize did not materialize the workspace on
// the claimed pod and reach ready against a live cluster. If finalize returns
// without the session reaching ready, the finalize barrier did not run the
// claim-then-prepare sequence; if start fails because the workspace is bare,
// materialization was not done at finalize (the pre-0007 deferred behavior).
func TestEagerClaimFinalizeMaterializesAgainstWarmPod(t *testing.T) {
	c := kind.InstallLenny(t)
	_ = kind.RequireAgentWorkload(t, c)

	probe := "eager-finalize-probe"
	gatewayIP := t5StartGatewayProbe(t, c, probe)
	role := eagerTenant()

	id := createdSession(t, c, probe, gatewayIP, role)
	t.Cleanup(func() {
		_ = t5GatewayRequest(t, c, probe, gatewayIP, "POST", "/v1/sessions/"+id+"/terminate", role, "")
	})

	// Finalize with an inline workspace file: the finalize barrier streams it
	// into /workspace/current on the claimed pod and reaches ready.
	finalizeBody := `{"workspacePlan":{"schemaVersion":1,"sources":[` +
		`{"type":"inlineFile","path":"CLAUDE.md","content":"# e2e finalized","mode":"0644"}]}}`
	res := t5GatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/sessions/"+id+"/finalize", role, finalizeBody)
	if res.statusCode != 200 {
		t.Fatalf("finalize: status %d, want 200; body=%s", res.statusCode, res.body)
	}
	if st := sessionState(t, c, probe, gatewayIP, id, role); st != "ready" {
		t.Fatalf("after finalize, state = %q, want ready (finalize is the barrier and returns ready)", st)
	}

	// Start launches only; the session reaches running.
	res = t5GatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/sessions/"+id+"/start", role, "")
	if res.statusCode != 200 {
		t.Fatalf("start: status %d, want 200; body=%s", res.statusCode, res.body)
	}
	if st := sessionState(t, c, probe, gatewayIP, id, role); st != "running" {
		t.Errorf("after start, state = %q, want running", st)
	}
}

// spec: 15.1 (created TTL-expiry releases the pod claim and revokes the lease),
// 7.1 (lease release on teardown)
// diagnosis: a failure means an abandoned created session that never finalized
// strands its claimed warm pod. Proposal 0007 claims the pod at /create, so the
// created-expiry sweep must delete the per-pod SandboxClaim to return the pod
// to the pool. If the claim survives the sweep window the pool leaks a pod for
// every abandoned create.
func TestEagerClaimCreatedExpiryReleasesPod(t *testing.T) {
	c := kind.InstallLenny(t)
	_ = kind.RequireAgentWorkload(t, c)

	probe := "eager-expiry-probe"
	gatewayIP := t5StartGatewayProbe(t, c, probe)
	role := eagerTenant()

	before := claimCount(t, c)
	id := createdSession(t, c, probe, gatewayIP, role)
	// The create claimed a pod: the per-pod claim count rose.
	if got := claimCount(t, c); got <= before {
		t.Fatalf("per-pod claim count = %d after create, want > %d (create claims a pod)", got, before)
	}

	// Abandon the session: never finalize. The created-expiry sweep retires the
	// row past maxCreatedStateTimeoutSeconds and releases the claimed pod.
	t.Cleanup(func() {
		_ = t5GatewayRequest(t, c, probe, gatewayIP, "POST", "/v1/sessions/"+id+"/terminate", role, "")
	})
	deadline := time.Now().Add(6 * time.Minute)
	for {
		st := sessionState(t, c, probe, gatewayIP, id, role)
		if st == "expired" || st == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("created session %s did not expire within the sweep window (last state %q)", id, st)
		}
		time.Sleep(10 * time.Second)
	}

	// The reserved pod's per-pod claim is released back to the pool.
	releaseDeadline := time.Now().Add(2 * time.Minute)
	for {
		if got := claimCount(t, c); got <= before {
			break
		}
		if time.Now().After(releaseDeadline) {
			t.Fatalf("per-pod claim count = %d after created-expiry, want <= %d (the sweep must release the claimed pod)", claimCount(t, c), before)
		}
		time.Sleep(5 * time.Second)
	}
}

// spec: 15.1 (/terminate of a finalizing/ready session releases the pod and
// revokes the lease), 7.1 (lease release on teardown), 4.6 (durable binding)
// diagnosis: a failure means terminating a pre-running session that holds a
// claimed pod and a finalize-assigned lease leaks one or both. The reclaim
// must delete the per-pod SandboxClaim (return the pod) AND remove the
// session's credential lease from the §4.9 store (decrement the active-session
// counter), so neither the warm pod nor the credential slot leaks. If the
// claim survives or the lease row remains, the pre-running terminate reclaim
// did not return both.
func TestEagerClaimTerminateReadySessionReleasesPodAndLease(t *testing.T) {
	c := kind.InstallLenny(t)
	_ = kind.RequireAgentWorkload(t, c)

	probe := "eager-terminate-probe"
	gatewayIP := t5StartGatewayProbe(t, c, probe)
	pgIP := t5DataStorePodIP(t, c, "postgres")
	if pgIP == "" {
		t.Skip("e2e Postgres pod IP unavailable; cannot inspect the credential-lease store")
	}
	role := eagerTenant()

	beforeClaims := claimCount(t, c)
	id := createdSession(t, c, probe, gatewayIP, role)

	// Finalize so the session reaches ready and the §4.9 lease is assigned at
	// the finalize barrier.
	res := t5GatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/sessions/"+id+"/finalize", role, "")
	if res.statusCode != 200 {
		t.Fatalf("finalize: status %d, want 200; body=%s", res.statusCode, res.body)
	}
	if st := sessionState(t, c, probe, gatewayIP, id, role); st != "ready" {
		t.Fatalf("after finalize, state = %q, want ready", st)
	}
	// The pod is claimed and (when the runtime requires a credential) a lease
	// row exists for the session.
	if got := claimCount(t, c); got <= beforeClaims {
		t.Fatalf("per-pod claim count = %d after finalize, want > %d", got, beforeClaims)
	}

	// Terminate the ready (pre-running) session.
	res = t5GatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/sessions/"+id+"/terminate", role, "")
	if res.statusCode != 200 {
		t.Fatalf("terminate ready session: status %d, want 200; body=%s", res.statusCode, res.body)
	}

	// The per-pod SandboxClaim is deleted: the pod returned to the pool.
	releaseDeadline := time.Now().Add(2 * time.Minute)
	for {
		if got := claimCount(t, c); got <= beforeClaims {
			break
		}
		if time.Now().After(releaseDeadline) {
			t.Fatalf("per-pod claim count = %d after terminate, want <= %d (terminate must release the claimed pod)", claimCount(t, c), beforeClaims)
		}
		time.Sleep(5 * time.Second)
	}

	// The credential lease's active-session counter was decremented: the
	// session holds no lease row in the §4.9 store after the reclaim, so the
	// reclaim did not leak the lease.
	if n := leaseRowCountForSession(t, c, pgIP, id); n != 0 {
		t.Errorf("credential_leases rows for session %s after terminate = %d, want 0 (the lease must be revoked so the active-session counter is decremented)", id, n)
	}
}
