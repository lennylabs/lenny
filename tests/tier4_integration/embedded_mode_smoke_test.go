// SPDX-License-Identifier: MIT

//go:build smoke

// Embedded Mode quick-start smoke test (§17.4 line 150, §24.19). The
// documented quick-start path is `lenny up` → `lenny session new` →
// `lenny down`. This test drives that exact sequence through the real
// cmd/lenny binary against a temporary LENNY_HOME, asserting the stack
// comes up, the embedded Kubernetes substrate is healthy, a session
// against the seeded runtime is created (which warms a pod), and the
// stack tears down with --purge removing the state directory. It is the
// end-to-end counterpart to the Source Mode smoke test
// (source_mode_smoke_test.go), which exercises the in-process gateway
// path. F-17.4.18.
//
// This test drives the full single-binary path on every supported host.
// On Linux the embedded k3s is a managed child process; on macOS and
// Windows the same pinned k3s runs as a container under Docker Desktop's
// Linux VM (§17.4 Embedded Mode). The CLI path is substrate-agnostic
// above the provisioning layer, so the same `up → status → session new →
// down` sequence exercises the Docker-backed bring-up end to end:
//
//   - `lenny up` provisions the substrate, imports the platform image
//     bundle, applies the embedded manifests, and runs the production
//     gateway and controllers as in-cluster pods rendered from the chart
//     under the development profile (§17.4 in-cluster control plane), then
//     waits for the gateway Deployment to report Ready. The reported
//     gateway URL is the loopback host-side forwarder port the host reaches
//     the in-cluster gateway on.
//   - `lenny status` reports the `k3s` component healthy only when the
//     real embedded cluster is up (a container probe on the Docker-backed
//     substrate, a host-PID probe on Linux), so a healthy `k3s` row
//     confirms the real-Kubernetes code path ran rather than the
//     controller-simulator fallback the previous non-Linux gate left, and
//     it reports the gateway, controller, and lenny-ops Deployment rows so
//     the test can assert each control-plane pod reached Ready.
//   - `lenny session new` warms a pod in the cluster. The in-cluster
//     gateway dials the agent pod's in-cluster IP directly over the §4.7
//     adapter boundary the way it does in a production cluster, so the
//     host/Docker boundary the host-process gateway could not cross
//     disappears. A non-zero session id confirms placement succeeded.
//
// The Docker-backed bring-up legs are pinned in isolation by the tier-2
// component bring-up (tests/tier2_component/embedded/bringup_test.go: CRD
// install, the in-cluster control-plane apply, and the §4.7 pod-IP dial)
// and the tier-3 contract suite (the §4.7 adapter reachability). This
// tier-4 test is the end-to-end CLI counterpart that composes those legs
// through the real binary.
//
// The test is gated behind embedded.SkipUnlessAvailable (the
// cross-platform substrate prerequisite — Linux, or a non-Linux host with
// Docker on PATH so Docker Desktop supplies the Linux VM — plus the
// LENNY_EMBEDDED_SMOKE opt-in) because the bring-up provisions the
// embedded k3s and imports the platform image bundle. Where a macOS or
// Windows host with Docker Desktop is unavailable in CI, the Docker-backed
// leg is deferred: the test skips rather than fails, stating the
// dependency (the test-coverage tier-5/6 escape hatch). The smoke targets
// the echo runtime, which `lenny up` auto-seeds with a runnable image
// digest, an applied Runtime CRD, and a single-pod warm pool, so it places
// a session on an in-cluster pod with no operator setup (the
// test-smoke-embedded Makefile target defaults LENNY_EMBEDDED_SMOKE_RUNTIME
// to echo). The §26 reference catalog ships placeholder-pinned images, so
// pointing the smoke at one of those runtimes still requires an operator to
// register a pullable image, apply a Runtime CRD, and create a warm pool
// first.
package tier4_integration_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/embedded/stack"
	"github.com/lennylabs/lenny/tests/testinfra/embedded"
)

// spec: §17.4 (the in-cluster control plane on the cross-platform
// substrate), §4.7 (the in-cluster gateway dials the agent pod's in-cluster
// IP over the adapter boundary), §5.2 (the PoolWarmingUp initial-fill
// window) — exercises the embedded quick-start: bring the stack up, assert
// the embedded Kubernetes substrate is healthy (the real-Kubernetes path
// rather than the controller-sim fallback), assert every rendered
// control-plane pod (gateway, controller, lenny-ops) reached Ready, create
// a session against the auto-seeded echo runtime (retrying across the §5.2
// PoolWarmingUp initial-fill window while the single-pod pool warms),
// register and place a custom sidecar-model runtime to prove placement is
// runtime-agnostic, exercise the warm persisted-substrate bring-up, and
// tear the stack down.
//
// diagnosis: a failure means the documented `lenny up` → `session new` →
// `lenny down` quick-start did not complete end to end through the real
// binary on this host. If `lenny up` failed, the embedded stack did not
// become ready (read the WARNING lines it prints). If the `k3s` status row
// is not healthy, the embedded cluster did not come up and the host fell
// back to the controller-simulator path; on a Docker-backed host check the
// published API port is free and `docker logs` for the lenny-embedded-k3s
// container. If a control-plane row (gateway, controller, ops) stays down,
// the in-cluster Deployment did not roll out a Ready pod (an
// ImagePullBackOff or crash loop). If `session new` failed after the §5.2
// warmup window elapsed, pod placement did not succeed: the pool never
// became idle (check the WarmPoolController and the pod's image-pull
// status), or the in-cluster gateway did not place the session on a pod
// over the §4.7 adapter boundary.
func TestEmbeddedModeSmoke_spec_17_4_18(t *testing.T) {
	embedded.SkipUnlessAvailable(t)
	bin := embedded.Build(t)
	// A dedicated subdirectory so `lenny down --purge` can remove the
	// whole state root without racing the t.TempDir cleanup of its parent.
	home := t.TempDir() + "/lenny-home"

	// `lenny up` — the first run provisions the embedded k3s and imports the
	// platform image bundle (the gateway, controller, lenny-ops, and
	// lenny-adapter images plus the echo image; §17.4 in-cluster control
	// plane), so allow the §17.4 lifecycle ceiling (lifecycle.go bounds the
	// foreground wait at 6 minutes) rather than the steady-state 60s
	// aspiration. The deadline is operator-tunable through
	// embedded.UpTimeoutEnv so a cold-cache or slow-network host (where the
	// first-run k3s + image-bundle import exceeds the default) can raise it
	// without editing the test. The control plane runs as in-cluster pods,
	// not host processes, and persists across down→up unless --purge is
	// passed.
	upTimeout := embedded.UpTimeout()
	start := time.Now()
	up := embedded.Run(t, bin, home, upTimeout, "up")
	if up.ExitCode != 0 {
		t.Fatalf("lenny up: exit %d\nstdout:\n%s\nstderr:\n%s", up.ExitCode, up.Stdout, up.Stderr)
	}
	t.Logf("lenny up: stack ready in %s", time.Since(start).Round(time.Second))
	// Tear the stack down even if a later step fails, so a failed run does
	// not leak the embedded k3s and the imported image store.
	t.Cleanup(func() {
		_ = embedded.Run(t, bin, home, 90*time.Second, "down", "--purge")
	})

	// `lenny up` reports the gateway URL on its published host port — the
	// port the host reaches the in-binary gateway on, and (on the
	// Docker-backed substrate) the host side of the published-port wiring.
	// A bring-up that reached a healthy gateway prints the URL; its absence
	// means the gateway never came up.
	if !strings.Contains(up.Stdout, "gateway") || !strings.Contains(up.Stdout, "localhost") {
		t.Errorf("lenny up did not report the published gateway URL\nstdout:\n%s", up.Stdout)
	}
	// `lenny up` must not report the cluster-unavailable note: on a
	// supported host (Linux, or a Docker-equipped non-Linux host) the
	// embedded cluster comes up, so the host does not fall back to the
	// no-placement path the previous non-Linux gate left.
	if strings.Contains(up.Stdout, "session placement is unavailable") {
		t.Fatalf("lenny up reported the embedded cluster unavailable; the real-Kubernetes path did not run\nstdout:\n%s", up.Stdout)
	}

	// `lenny status` reports the running stack.
	status := embedded.Run(t, bin, home, 30*time.Second, "status")
	if status.ExitCode != 0 {
		t.Fatalf("lenny status: exit %d\nstdout:\n%s\nstderr:\n%s", status.ExitCode, status.Stdout, status.Stderr)
	}
	// The `k3s` component is healthy only when the real embedded cluster is
	// up: a container probe on the Docker-backed substrate, a host-PID probe
	// on Linux (status.go k3sComponentStatus). A healthy `k3s` row confirms
	// the real-Kubernetes code path ran on this host rather than the
	// controller-simulator fallback. WriteStatus renders the component name
	// and its "ok" health on one tab-separated row, so the row text contains
	// both tokens. spec: §24.19, §17.4.
	if !embeddedComponentHealthy(status.Stdout, "k3s") {
		t.Fatalf("lenny status does not report the embedded k3s component healthy; "+
			"the real embedded cluster did not come up (controller-sim fallback)\nstdout:\n%s", status.Stdout)
	}

	// Every rendered control-plane pod must reach Ready. `lenny status`
	// reports the gateway, controller, and lenny-ops Deployment readiness as
	// `ok` rows read from the embedded kubeconfig (status.go
	// collectClusterComponents), so a healthy row for each is the public
	// proof that the Deployment rolled out to a Ready replica. A pod stuck in
	// ImagePullBackOff never becomes Ready, so its Deployment never reports a
	// ready replica and its row stays `down`: the import bundle carries the
	// lenny-ops image (Makefile `make images` adds lenny-ops) and the
	// token-service Deployment is gated off in the dev profile
	// (tokenService.enabled=false), so the control plane is exactly the
	// gateway, controller, and lenny-ops pods and all three must be Ready. A
	// `down` row here means a control-plane pod did not pull its image or did
	// not become Ready. The control plane reaches readiness only after the
	// image-bundle import lands, so the up wait already gated on it; this
	// asserts the post-up steady state. spec: §17.4, §4.7.
	for _, comp := range []string{"gateway", "controller", "ops"} {
		waitControlPlanePodReady(t, bin, home, comp)
	}

	// `lenny session new` against the seeded runtime warms a pod in the
	// embedded cluster and places the session on it. The in-cluster gateway
	// dials the agent pod's in-cluster IP directly over the §4.7 adapter
	// boundary the way it does in a production cluster, so the host/Docker
	// boundary the host-process gateway could not cross is gone. A non-zero
	// session id confirms placement succeeded end to end. Prints the session
	// id to stdout and exits 0 on success (cmd/lenny session new). spec: §4.7,
	// §17.4.
	//
	// The single-pod warm pool the bring-up seeds (warmCount: 1) has
	// minWarm > 0, so during the §5.2 initial-fill window the pool carries
	// the PoolWarmingUp condition and the gateway returns 503
	// RUNTIME_UNAVAILABLE with a Retry-After header until the WarmPoolController
	// fills its one pod. The smoke must not assert an immediately-idle pod:
	// it retries `session new` across that warming window until the pod
	// becomes idle and placement succeeds. spec: §5.2 (PoolWarmingUp /
	// RUNTIME_UNAVAILABLE initial-fill window).
	rt := embedded.Runtime()
	sid := newSessionToleratingWarmup(t, bin, home, rt)
	t.Logf("lenny session new: created %s", sid)

	// Custom-sidecar-runtime leg (S5/C2): the in-cluster control plane places
	// any runtime over the §4.7 boundary, not echo alone. Register a custom
	// sidecar-model runtime through the §17.4 walkthrough (lenny-ctl runtime
	// publish writes the registry record; lenny runtime apply materializes its
	// Runtime, SandboxTemplate, and SandboxWarmPool CRD set), start a session,
	// and assert the controller stamped the lenny-adapter sidecar container on
	// the placed pod, proving placement is runtime-agnostic. spec: §17.4, §4.7.
	customSidecarRuntimeLeg(t, bin, home)

	// Warm-up persisted-substrate leg (S8). A non-`--purge` `lenny down`
	// stops the substrate and the forwarder but persists the substrate handle
	// and the imported image store, so the next `lenny up` reuses the
	// persisted control plane through the version-aware reconcile rather than
	// re-provisioning from scratch (lifecycle.go down-without-purge,
	// stack.go needsReapply). This leg drives down (no --purge) → up and
	// re-asserts the gateway and k3s rows healthy, proving the warm bring-up
	// restarts the persisted control plane. The CLI version is unchanged
	// between the two `up`s, so the reconcile takes the tag-match warm-reuse
	// path rather than a re-import. spec: §17.4 (the substrate persists across
	// a non-`--purge` down/up; an unchanged CLI version reuses it).
	warmUpReusesPersistedSubstrate(t, bin, home)

	// `lenny down --purge` tears the stack down and removes the state dir.
	if r := embedded.Run(t, bin, home, 90*time.Second, "down", "--purge"); r.ExitCode != 0 {
		t.Fatalf("lenny down --purge: exit %d\nstdout:\n%s\nstderr:\n%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("lenny down --purge: state dir %s still present (stat err: %v)", home, err)
	}
}

// warmupWindow bounds how long the smoke retries `session new` across the
// §5.2 PoolWarmingUp initial-fill window before giving up. The
// WarmPoolBootstrapping alert fires at warmupDeadlineSeconds (300s by
// default; spec/05 §5.2), so a single echo pod that has not become idle by
// then is a genuine bring-up failure rather than an expected warm window.
const warmupWindow = 5 * time.Minute

// warmupRetryInterval is the floor between `session new` retries while the
// pool warms. The gateway's Retry-After is max(30, estimatedWarmupSeconds)
// (spec/05 §5.2). The CLI does not expose the header, so the smoke honors
// that floor by polling at this interval until the pod becomes idle.
const warmupRetryInterval = 5 * time.Second

// newSessionToleratingWarmup runs `lenny session new --runtime rt` and
// returns the created session id, retrying across the §5.2 PoolWarmingUp
// initial-fill window. The bring-up seeds a single-pod warm pool
// (warmCount: 1, minWarm > 0), so the first `session new` may land while
// the WarmPoolController is still filling the pool, during which the
// gateway returns 503 RUNTIME_UNAVAILABLE.
//
// The smoke runs `session new` without a workspace, so the CLI takes the
// MCP create_session path (pkg/embedded/localcli/session.go), and the SDK
// builds the CLI's error from the tool result's text content block alone
// (sdks/client/go/lenny/mcp.go CreateSession via MCPToolResult.Text). The
// gateway puts the RUNTIME_UNAVAILABLE code in a separate `lenny/error`
// block and only the human warming message in the text block
// (pkg/gateway/mcp/mcp.go), so the RUNTIME_UNAVAILABLE code string never
// reaches stderr. The smoke therefore cannot key its retry on that code.
// Per the §3 C5 second sanctioned approach, it retries any non-zero exit
// until the warmup deadline elapses rather than pinning to a message or
// code substring (the warming message is transient runtime output and
// pinning to it is the same fragility class). A non-zero exit that persists
// past the deadline (an unpullable image, a failed §4.7 callback, or a pod
// that never became idle) is the hard failure. spec: §5.2 (PoolWarmingUp /
// RUNTIME_UNAVAILABLE initial-fill window), §4.7, §17.4.
func newSessionToleratingWarmup(t *testing.T, bin, home, rt string) string {
	t.Helper()
	deadline := time.Now().Add(warmupWindow)
	var last embedded.Result
	for {
		last = embedded.Run(t, bin, home, 2*time.Minute,
			"session", "new", "--runtime", rt, "--user", "alice@acme.com")
		if last.ExitCode == 0 {
			sid := strings.TrimSpace(last.Stdout)
			if sid == "" {
				t.Fatalf("lenny session new: empty session id\nstderr:\n%s", last.Stderr)
			}
			return sid
		}
		// The single-pod pool may still be in its §5.2 PoolWarmingUp
		// window, in which case the gateway returns 503 RUNTIME_UNAVAILABLE
		// and the pod is not idle yet. The CLI surfaces only the human
		// warming message on stderr (the RUNTIME_UNAVAILABLE code rides in
		// the discarded `lenny/error` block), so the smoke cannot
		// distinguish a transient warming 503 from another failure by the
		// error text. It retries every non-zero exit and lets the warmup
		// deadline bound how long it tolerates one before declaring a
		// genuine bring-up failure.
		if time.Now().After(deadline) {
			break
		}
		t.Logf("lenny session new: not yet placed (exit %d), retrying in %s\nstderr:\n%s",
			last.ExitCode, warmupRetryInterval, last.Stderr)
		time.Sleep(warmupRetryInterval)
	}
	t.Fatalf("lenny session new --runtime %s: still failing after %s; the single echo pod "+
		"never became idle (exit %d)\nstdout:\n%s\nstderr:\n%s\n"+
		"(echo is the auto-seeded runnable runtime; set %s to another runtime only if its image is pullable on this host)",
		rt, warmupWindow, last.ExitCode, last.Stdout, last.Stderr, embedded.RuntimeEnv)
	return "" // unreachable: t.Fatalf stops the test
}

// embeddedComponentHealthy reports whether the `lenny status` table marks
// the named component healthy. WriteStatus (pkg/embedded/stack/status.go)
// renders one tab-aligned row per component as `COMPONENT  HEALTH  ...`,
// with HEALTH the literal "ok" when the component is healthy and "down"
// otherwise. The helper finds the row whose first whitespace-delimited
// field equals name and reports whether its second field is "ok". It
// matches on the row's leading fields rather than a substring so a
// "k3s" detail in another component's DETAIL column cannot produce a
// false match.
func embeddedComponentHealthy(statusOutput, name string) bool {
	for _, line := range strings.Split(statusOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == name {
			return fields[1] == "ok"
		}
	}
	return false
}

// controlPlaneReadyWindow bounds how long waitControlPlanePodReady polls for a
// control-plane Deployment row to report healthy. `lenny up` already waited for
// the gateway Deployment Ready before returning, and the controller and ops
// Deployments settle within a short window after; a row still down past this
// window is a genuine bring-up failure (a pod in ImagePullBackOff or a crash
// loop) rather than an expected rollout delay.
const controlPlaneReadyWindow = 2 * time.Minute

// controlPlaneReadyInterval is the poll interval while waiting for a
// control-plane Deployment to report ready.
const controlPlaneReadyInterval = 3 * time.Second

// waitControlPlanePodReady polls `lenny status` until the named control-plane
// component (gateway, controller, or ops) reports its Deployment healthy, or
// fails the test once controlPlaneReadyWindow elapses. A healthy row means the
// Deployment rolled out a Ready replica; a pod stuck in ImagePullBackOff never
// becomes Ready, so its row stays down and this fails. The import bundle carries
// the lenny-ops image and the token-service is gated off in the dev profile, so
// the control plane is exactly the gateway, controller, and lenny-ops pods and
// each must reach Ready with no ImagePullBackOff. spec: §17.4, §4.7.
func waitControlPlanePodReady(t *testing.T, bin, home, component string) {
	t.Helper()
	deadline := time.Now().Add(controlPlaneReadyWindow)
	for {
		status := embedded.Run(t, bin, home, 30*time.Second, "status")
		if status.ExitCode == 0 && embeddedComponentHealthy(status.Stdout, component) {
			t.Logf("control-plane component %q is Ready", component)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("control-plane component %q did not reach Ready within %s; the pod did not roll out "+
				"(an ImagePullBackOff or crash loop)\nstatus stdout:\n%s\nstderr:\n%s",
				component, controlPlaneReadyWindow, status.Stdout, status.Stderr)
		}
		time.Sleep(controlPlaneReadyInterval)
	}
}

// warmUpReusesPersistedSubstrate exercises the §17.4 S8 down-without-purge then
// up version-aware reconcile: a non-`--purge` `lenny down` stops the substrate
// and the forwarder while persisting the substrate handle and the imported
// image store, so the next `lenny up` reuses the persisted control plane rather
// than re-provisioning. It drives down (no --purge) → up and re-asserts the
// gateway and k3s rows healthy, proving the warm bring-up restarts the
// persisted control plane. spec: §17.4 (the substrate persists across a
// non-`--purge` down/up; the version-aware reconcile reuses it when the CLI
// version is unchanged).
func warmUpReusesPersistedSubstrate(t *testing.T, bin, home string) {
	t.Helper()
	if r := embedded.Run(t, bin, home, 90*time.Second, "down"); r.ExitCode != 0 {
		t.Fatalf("lenny down (no --purge): exit %d\nstdout:\n%s\nstderr:\n%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	// The state directory must survive a non-`--purge` down: the persisted
	// substrate and imported-image store are what the warm up reuses.
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("lenny down (no --purge) removed the state dir %s (stat err: %v); the substrate must persist", home, err)
	}

	start := time.Now()
	up := embedded.Run(t, bin, home, embedded.UpTimeout(), "up")
	if up.ExitCode != 0 {
		t.Fatalf("warm lenny up: exit %d\nstdout:\n%s\nstderr:\n%s", up.ExitCode, up.Stdout, up.Stderr)
	}
	t.Logf("warm lenny up: stack ready in %s", time.Since(start).Round(time.Second))

	status := embedded.Run(t, bin, home, 30*time.Second, "status")
	if status.ExitCode != 0 {
		t.Fatalf("warm lenny status: exit %d\nstdout:\n%s\nstderr:\n%s", status.ExitCode, status.Stdout, status.Stderr)
	}
	for _, comp := range []string{"k3s", "gateway"} {
		if !embeddedComponentHealthy(status.Stdout, comp) {
			t.Fatalf("after a warm up, lenny status does not report %q healthy; the persisted control plane "+
				"did not restart\nstdout:\n%s", comp, status.Stdout)
		}
	}
}

// customRuntimeName is the §17.4 walkthrough runtime name the custom-sidecar
// leg registers, places, and inspects.
const customRuntimeName = "my-agent"

// customRuntimeImage is the image the custom sidecar-model runtime's runtime
// container references. The leg reuses the echo-embedded image `lenny up`
// already imported into the substrate's containerd store, so `lenny runtime
// apply` resolves its tag to a §5.3 digest from the local store with no extra
// import. The runtime container image is immaterial to the assertion (the leg
// asserts the stamped lenny-adapter sidecar, which the controller adds from
// controller.adapterImage for any sidecar-model runtime); reusing an
// already-imported image keeps the leg self-contained.
const customRuntimeImage = "ghcr.io/lennylabs/runtime-echo-embedded:dev"

// customSidecarRuntimeLeg registers a custom sidecar-model runtime through the
// §17.4 walkthrough and asserts the in-cluster control plane places it with the
// stamped lenny-adapter sidecar, proving placement is runtime-agnostic rather
// than echo-only. It follows the walkthrough verbatim: `lenny-ctl runtime
// publish --skip-push` writes the registry record through the gateway admin API
// (authenticated with the dev bearer the gateway trusts), then `lenny runtime
// apply` materializes the runtime's Runtime, SandboxTemplate, and
// SandboxWarmPool CRD set (the no-Postgres pool-materialization path), then
// `lenny session new` places a session, and finally the placed agent pod is
// inspected through the embedded kubeconfig for the stamped "adapter" container.
//
// spec: §17.4 (the custom-runtime walkthrough; a sidecar-model runtime runs
// with the stamped lenny-adapter container), §4.7 (the adapter sidecar), §5.2
// (the applied SandboxWarmPool warms a pod), §5.3 (the applied Runtime is
// digest-pinned).
func customSidecarRuntimeLeg(t *testing.T, bin, home string) {
	t.Helper()
	ctl := embedded.BuildCtl(t)

	// The dev bearer the in-cluster gateway trusts; it carries platform-admin,
	// so the admin runtimes register endpoint admits the publish step.
	tok := embedded.Run(t, bin, home, 30*time.Second, "token", "print")
	if tok.ExitCode != 0 {
		t.Fatalf("lenny token print: exit %d\nstderr:\n%s", tok.ExitCode, tok.Stderr)
	}
	bearer := strings.TrimSpace(tok.Stdout)
	if bearer == "" {
		t.Fatalf("lenny token print: empty token\nstderr:\n%s", tok.Stderr)
	}

	gatewayURL, err := stack.RunningGateway(home)
	if err != nil {
		t.Fatalf("RunningGateway: %v", err)
	}

	// Step: register the runtime record through the walkthrough's
	// `lenny-ctl runtime publish --skip-push` (the image is already in the
	// substrate store, so no docker push). The §24.16 global flags
	// (--api-url, --token, --insecure-skip-verify) precede the subcommand. The
	// TLS forwarder presents the per-`lenny up` self-signed leaf, which the
	// smoke trusts with --insecure-skip-verify because the leaf is loopback-only.
	pub := embedded.RunBin(t, ctl, home, 60*time.Second,
		"--api-url", gatewayURL,
		"--token", bearer,
		"--insecure-skip-verify",
		"runtime", "publish", customRuntimeName,
		"--image", customRuntimeImage,
		"--skip-push")
	if pub.ExitCode != 0 {
		t.Fatalf("lenny-ctl runtime publish: exit %d\nstdout:\n%s\nstderr:\n%s", pub.ExitCode, pub.Stdout, pub.Stderr)
	}

	// Step: materialize the runtime's CRD set with the §17.4 `lenny runtime
	// apply` verb (S16). The file declares the sidecar deployment model, so the
	// controller stamps the adapter sidecar onto the placed pod.
	crdFile := filepath.Join(t.TempDir(), "runtime-crds.yaml")
	body := "apiVersion: lenny.dev/v1alpha1\n" +
		"kind: Runtime\n" +
		"metadata:\n" +
		"  name: " + customRuntimeName + "\n" +
		"spec:\n" +
		"  image: " + customRuntimeImage + "\n" +
		"  integrationLevel: basic\n" +
		"  deploymentModel: sidecar\n"
	if err := os.WriteFile(crdFile, []byte(body), 0o600); err != nil {
		t.Fatalf("write runtime-crds.yaml: %v", err)
	}
	apply := embedded.Run(t, bin, home, 60*time.Second, "runtime", "apply", "--file", crdFile)
	if apply.ExitCode != 0 {
		t.Fatalf("lenny runtime apply: exit %d\nstdout:\n%s\nstderr:\n%s", apply.ExitCode, apply.Stdout, apply.Stderr)
	}

	// Step: place a session against the custom runtime, tolerating the §5.2
	// PoolWarmingUp window while the WarmPoolController warms its pod.
	sid := newSessionToleratingWarmup(t, bin, home, customRuntimeName)
	t.Logf("lenny session new --runtime %s: created %s", customRuntimeName, sid)

	// Assert the placed agent pod carries the stamped lenny-adapter sidecar
	// container the controller adds for a sidecar-model runtime. A pod with no
	// "adapter" container would mean the controller did not stamp the sidecar,
	// so a sidecar-model runtime could not run; the echo seed is exempt (it is
	// the embedded model with no separate adapter container), so this leg is
	// what proves the runtime-agnostic sidecar path.
	kubeconfig, err := stack.RunningKubeconfig(home)
	if err != nil {
		t.Fatalf("RunningKubeconfig: %v", err)
	}
	assertAdapterStamped(t, kubeconfig, customRuntimeName)
}

// assertAdapterStamped polls the placed agent pods for the custom runtime until
// one carries the stamped "adapter" sidecar container, or fails once the window
// elapses. The pod may take a moment to appear after `session new` returns, so
// the assertion polls rather than reading once. spec: §17.4 (the controller
// stamps the lenny-adapter sidecar for a sidecar-model runtime), §4.7.
func assertAdapterStamped(t *testing.T, kubeconfig, runtimeName string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	var lastNames []string
	for {
		names, err := stack.RunningRuntimePodContainerNames(context.Background(), kubeconfig, runtimeName)
		if err != nil {
			t.Fatalf("RunningRuntimePodContainerNames(%s): %v", runtimeName, err)
		}
		lastNames = names
		for _, n := range names {
			if n == "adapter" {
				t.Logf("runtime %q agent pod carries the stamped lenny-adapter sidecar container", runtimeName)
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime %q agent pod does not carry the stamped 'adapter' sidecar container (container names: %v); "+
				"the controller did not stamp the lenny-adapter sidecar for the sidecar-model runtime", runtimeName, lastNames)
		}
		time.Sleep(controlPlaneReadyInterval)
	}
}
