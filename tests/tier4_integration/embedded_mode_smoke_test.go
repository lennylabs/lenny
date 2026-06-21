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
//   - `lenny up` brings up the embedded cluster, installs the CRDs, and
//     starts the production controllers against the launcher's kubeconfig
//     (the published-host-port server URL on the Docker-backed substrate),
//     then waits for a healthy gateway. The reported gateway URL is the
//     published API port the host reaches the in-binary gateway on.
//   - `lenny status` reports the `k3s` component healthy only when the
//     real embedded cluster is up (a container probe on the Docker-backed
//     substrate, a host-PID probe on Linux), so a healthy `k3s` row
//     confirms the real-Kubernetes code path ran rather than the
//     controller-simulator fallback the previous non-Linux gate left.
//   - `lenny session new` warms a pod in the cluster. The pod's adapter
//     reaches the host gateway at host.docker.internal under the Docker
//     VM, and the §4.7 gateway↔adapter gRPC+mTLS callback traverses the
//     host/Docker boundary. A non-zero session id confirms placement
//     succeeded across that boundary.
//
// The Docker-backed bring-up legs are pinned in isolation by the tier-2
// component bring-up (tests/tier2_component/embedded/bringup_test.go: CRD
// install, controllers start, and the §4.7 cross-boundary callback
// address) and the tier-3 contract suite (the §4.7 mTLS callback
// reachability). This tier-4 test is the end-to-end CLI counterpart that
// composes those legs through the real binary.
//
// The test is gated behind embedded.SkipUnlessAvailable (the
// cross-platform substrate prerequisite — Linux, or a non-Linux host with
// Docker on PATH so Docker Desktop supplies the Linux VM — plus the
// LENNY_EMBEDDED_SMOKE opt-in) because the bring-up downloads and runs
// k3s + PostgreSQL. Where a macOS or Windows host with Docker Desktop is
// unavailable in CI, the Docker-backed leg is deferred: the test skips
// rather than fails, stating the dependency (the test-coverage tier-5/6
// escape hatch). Session start pulls the runtime image; the reference
// catalog ships placeholder-pinned images, so an operator running this
// test sets LENNY_EMBEDDED_SMOKE_RUNTIME to a runtime whose image is
// pullable on the host (or pre-loads the chat image).
package tier4_integration_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/embedded"
)

// spec: §17.4 line 150 ("# Ready to use in < 60s"), §24.19, §4.7
// (the gateway↔adapter callback traverses the host/Docker boundary) —
// exercises the embedded quick-start on the cross-platform substrate:
// bring the stack up, assert the embedded Kubernetes substrate is
// healthy (the published API port / real-Kubernetes path, not the
// controller-sim fallback), create a session (which warms a pod across
// the host/Docker boundary), assert a session id, and tear the stack
// down.
//
// diagnosis: a failure means the documented `lenny up` → `session new` →
// `lenny down` quick-start did not complete end to end through the real
// binary on this host. If `lenny up` failed, the embedded stack did not
// become ready (read the supervisor.log path it prints). If the `k3s`
// status row is not healthy, the embedded cluster did not come up and
// the host fell back to the controller-simulator path rather than the
// real-Kubernetes code path — on a Docker-backed host check the
// published API port is free and `docker logs` for the lenny-embedded-k3s
// container. If `session new` failed, pod placement did not succeed: the
// runtime image may be unpullable (set LENNY_EMBEDDED_SMOKE_RUNTIME), or
// the §4.7 gateway↔adapter callback did not traverse the host/Docker
// boundary (the in-cluster adapter could not reach the host gateway at
// host.docker.internal).
func TestEmbeddedModeSmoke_spec_17_4_18(t *testing.T) {
	embedded.SkipUnlessAvailable(t)
	bin := embedded.Build(t)
	// A dedicated subdirectory so `lenny down --purge` can remove the
	// whole state root without racing the t.TempDir cleanup of its parent.
	home := t.TempDir() + "/lenny-home"

	// `lenny up` — first run downloads PostgreSQL + k3s, so allow the
	// §17.4 lifecycle ceiling (lifecycle.go bounds the foreground wait at
	// 6 minutes) rather than the steady-state 60s aspiration. The deadline
	// is operator-tunable through embedded.UpTimeoutEnv so a cold-cache or
	// slow-network host (where the first-run PostgreSQL + k3s + runtime-image
	// downloads exceed the default) can raise it without editing the test.
	// The supervisor is detached and keeps running until `lenny down`.
	upTimeout := embedded.UpTimeout()
	start := time.Now()
	up := embedded.Run(t, bin, home, upTimeout, "up")
	if up.ExitCode != 0 {
		t.Fatalf("lenny up: exit %d\nstdout:\n%s\nstderr:\n%s", up.ExitCode, up.Stdout, up.Stderr)
	}
	t.Logf("lenny up: stack ready in %s", time.Since(start).Round(time.Second))
	// Tear the stack down even if a later step fails, so a failed run
	// does not leak a detached supervisor + Postgres + k3s.
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
	// the published API port path / real-Kubernetes code path ran on this
	// host rather than the controller-simulator fallback. WriteStatus
	// renders the component name and its "ok" health on one tab-separated
	// row, so the row text contains both tokens. spec: §24.19, §17.4.
	if !embeddedComponentHealthy(status.Stdout, "k3s") {
		t.Fatalf("lenny status does not report the embedded k3s component healthy; "+
			"the real embedded cluster did not come up (controller-sim fallback)\nstdout:\n%s", status.Stdout)
	}

	// `lenny session new` against the seeded runtime warms a pod in the
	// embedded cluster and places the session on it. On the Docker-backed
	// substrate the pod runs in-cluster under the Docker VM, its adapter
	// reaches the host gateway at host.docker.internal, and the §4.7
	// gateway↔adapter gRPC+mTLS callback traverses the host/Docker
	// boundary. A non-zero session id confirms placement succeeded across
	// that boundary end to end. Prints the session id to stdout and exits 0
	// on success (cmd/lenny session new). spec: §4.7, §17.4.
	rt := embedded.Runtime()
	r := embedded.Run(t, bin, home, 2*time.Minute,
		"session", "new", "--runtime", rt, "--user", "alice@acme.com")
	if r.ExitCode != 0 {
		t.Fatalf("lenny session new --runtime %s: exit %d\nstdout:\n%s\nstderr:\n%s\n"+
			"(the reference catalog ships placeholder-pinned images; set %s to a runtime whose image is pullable)",
			rt, r.ExitCode, r.Stdout, r.Stderr, embedded.RuntimeEnv)
	}
	sid := strings.TrimSpace(r.Stdout)
	if sid == "" {
		t.Fatalf("lenny session new: empty session id\nstderr:\n%s", r.Stderr)
	}
	t.Logf("lenny session new: created %s", sid)

	// `lenny down --purge` tears the stack down and removes the state dir.
	if r := embedded.Run(t, bin, home, 90*time.Second, "down", "--purge"); r.ExitCode != 0 {
		t.Fatalf("lenny down --purge: exit %d\nstdout:\n%s\nstderr:\n%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("lenny down --purge: state dir %s still present (stat err: %v)", home, err)
	}
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
