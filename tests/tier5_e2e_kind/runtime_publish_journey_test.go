// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §26 runtime-author publishing journey:
// scaffold, build, push to a real OCI registry, register against the
// gateway, apply the resulting Runtime as a CRD, create a warm pool,
// grant tenant access, and drive a live session against the freshly
// published runtime. Every step shells out to the real lenny-ctl
// binary and the real docker/kind/kubectl tools; none of it is
// mocked. install.sh's own tests/testinfra/kind/install.sh
// resolve_digest / containerd-tag technique is reused verbatim for
// pinning the freshly built image onto the cluster nodes.

package tier5_e2e_kind_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/embedded"
	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// spec: 26 (day-one utility: "once the deployer registers the published
// image digest, applies a Runtime CRD instance, and creates a warm pool
// for it"; tenant access: "Operators grant access per tenant via POST
// /v1/admin/runtimes/{name}/tenant-access ... after install")
// diagnosis: a failure here means the runtime-author journey described
// in §26 — scaffold, build, publish (push + register), apply the
// Runtime CRD, create a pool, grant tenant access, and run a session —
// is broken somewhere along that chain: the CLI dispatch, the admin
// registration validation, the scaffold's generated runtime image, the
// pool-to-pod warm-up path, or the session/message round trip. The
// tier-11 CLI-usage and tier-10 scaffold-registration tests do not
// exercise any of this live; this is the only test that drives a real
// `lenny runtime publish` push against a real registry and a real
// session against the result.
func TestRuntimePublishJourney(t *testing.T) {
	c := kind.InstallLenny(t)
	ctx := context.Background()

	name := fmt.Sprintf("bob-agent-%d", time.Now().UnixNano()%1_000_000)
	poolName := name + "-pool"
	// A per-run tenant id, not a fixed constant or the doc-style "acme"
	// example tenant: the tier-5 suite shares one long-lived Kind
	// cluster across every test in the package and across repeated runs
	// of this same test, and DELETE /v1/admin/tenants/{id} (which
	// sessiondriver.Close issues for every tenant it bootstrapped) only
	// initiates the §12.8 multi-phase deletion lifecycle rather than
	// completing it synchronously — a fixed tenant name reused by the
	// next run can race a still-"deleting" tenant from the previous
	// one and fail closed with 403 TENANT_NOT_ACTIVE. A fresh id per
	// run sidesteps that race, the same reason name above is
	// timestamp-suffixed.
	tenantID := fmt.Sprintf("runtime-publish-journey-tenant-%d", time.Now().UnixNano()%1_000_000)

	lennyCtl := embedded.BuildCtl(t)

	// Step: scaffold. `lenny runtime init --language binary --template
	// minimal` emits a Basic-level runtime with no SDK dependency and no
	// external go.mod requirement, so the image below builds fully
	// offline. spec: §24.18.
	workDir := t.TempDir()
	if out, err := runCLI(t, lennyCtl, workDir, "runtime", "init", name,
		"--language", "binary", "--template", "minimal"); err != nil {
		t.Fatalf("lenny-ctl runtime init: %v\n%s", err, out)
	}
	repo := filepath.Join(workDir, name)

	// The scaffold's default isolationProfile (sandboxed/gVisor) has no
	// matching RuntimeClass on the tier-5 Kind cluster, which only
	// installs the runc RuntimeClass (tests/testinfra/kind/agent-
	// workload.yaml); override it to standard so the pool below can
	// schedule a real pod, mirroring every other tier-5/8/9 pool.
	manifestPath := filepath.Join(repo, "runtime.yaml")
	overrideIsolationProfile(t, manifestPath)

	// Step: build. The scaffold's Dockerfile only needs the Go and
	// distroless base images (already resolvable from the local docker
	// cache on any host that has built the platform images); `go mod
	// download` is a no-op since the binary-language go.mod carries no
	// require directive.
	localImage := name + ":e2e-journey"
	buildImage(t, repo, localImage)

	// Step: push to a real local OCI registry. install.sh's own images
	// deliberately avoid a registry (kind load + pullPolicy: Never), but
	// §26 line 8 requires "CI that publishes OCI images to the canonical
	// Lenny registry", so this test stands up a real one rather than
	// reusing that shortcut.
	registryAddr := startLocalRegistry(t)
	pushTag := fmt.Sprintf("%s/%s:e2e", registryAddr, name)
	if out, err := runDocker(t, "tag", localImage, pushTag); err != nil {
		t.Fatalf("docker tag: %v\n%s", err, out)
	}

	// Step: publish. `lenny runtime publish` (no --skip-push) both
	// pushes pushTag with a real `docker push` and registers it against
	// the gateway, reusing the `admin runtimes register` path. spec:
	// §24.18 line 233.
	// The shared tier-5 Kind cluster accumulates concurrent load across
	// the whole suite; a freshly created pool's first claim can take
	// longer than the driver's 30s default under that load, so this
	// gives the HTTP client more headroom than sessiondriver.New's
	// default.
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 90 * time.Second})
	pubOut, err := runCLI(t, lennyCtl, repo,
		"--api-url", d.BaseURL(), "--dev-tenant", "platform", "--dev-roles", "platform-admin",
		"runtime", "publish", name, "--image", pushTag, "--manifest", "runtime.yaml")
	if err != nil {
		t.Fatalf("lenny-ctl runtime publish: %v\n%s", err, pubOut)
	}
	// No separate cleanup for the registered runtime record: deleting
	// the Runtime CRD applied below runs the RuntimeReconciler's
	// finalizer, which soft-deletes the mirrored registry row
	// (pkg/controller/runtime's doc comment).

	// The registration response's image field is the digest-pinned
	// reference cmdRuntimePublish resolved after the push (§5.1 line
	// 690: registered images must be digest-pinned). Reading it back
	// from the actual response, rather than recomputing it locally,
	// asserts what the CLI genuinely registered.
	registeredImage := registeredImageField(t, pubOut, name)
	if !strings.Contains(registeredImage, "@sha256:") {
		t.Fatalf("lenny-ctl runtime publish registered a non-digest-pinned image: %q", registeredImage)
	}

	// Step: pin the freshly pushed image onto every Kind node's
	// containerd content store under its digest-pinned reference, the
	// same technique tests/testinfra/kind/install.sh's resolve_digest
	// function and per-node `ctr images tag` loop use for the reference
	// runtimes. The e2e cluster runs pullPolicy: Never, so the exact
	// reference string the Runtime CRD below carries must already exist
	// locally on each node.
	pinImageOnNodes(t, c, pushTag, registeredImage)

	// Step: apply the Runtime CRD. §26 line 3 requires it explicitly: a
	// session against a placeholder-pinned reference-runtime record
	// "requires a runnable image digest, an applied Runtime CRD
	// instance, and a warm pool before it starts." The admin
	// registration above only wrote the Postgres-side record; the
	// Sandbox reconciler reads a runtime's image from the CRD object
	// directly (pkg/controller/sandbox), not from that record.
	//
	// The image field carries the tag-stripped `<repo>@sha256:<id>`
	// form to match pinImageOnNodes' local pin exactly (see its doc
	// comment on the tag+digest normalization the kubelet performs).
	runtimeCR := fmt.Sprintf(`apiVersion: lenny.dev/v1alpha1
kind: Runtime
metadata:
  name: %s
spec:
  type: agent
  image: %s
  integrationLevel: basic
  executionMode: session
  isolationProfile: standard
  capabilities:
    interaction: multi_turn
    injection:
      supported: true
      modes: [immediate]
`, name, stripTag(registeredImage))
	if out, err := c.ApplyStdin(t, runtimeCR); err != nil {
		t.Fatalf("kubectl apply Runtime %s: %v\n%s", name, err, out)
	}
	t.Cleanup(func() {
		_, _ = c.DeleteStdin(t, runtimeCR)
	})

	// Step: create a warm pool for the runtime via POST /v1/admin/pools
	// (§15.1 line 792), the same admin surface an operator uses. The
	// PoolScalingController reconciles this Postgres-authoritative pool
	// row into a SandboxTemplate/SandboxWarmPool CRD pair, and the
	// Sandbox reconciler then produces a real agent pod from it.
	poolPath := filepath.Join(workDir, "pool.json")
	poolBody := fmt.Sprintf(`{
  "name": %q,
  "runtimeRef": %q,
  "isolationProfile": "standard",
  "executionMode": "session",
  "resourceClass": "small",
  "warmCount": 1,
  "allowStandardIsolation": true
}`, poolName, name)
	if err := os.WriteFile(poolPath, []byte(poolBody), 0o600); err != nil {
		t.Fatalf("write pool.json: %v", err)
	}
	if out, err := runCLI(t, lennyCtl, repo,
		"--api-url", d.BaseURL(), "--dev-tenant", "platform", "--dev-roles", "platform-admin",
		"admin", "pools", "create", "--from-file", poolPath); err != nil {
		t.Fatalf("lenny-ctl admin pools create: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_, _ = runCLI(t, lennyCtl, repo,
			"--api-url", d.BaseURL(), "--dev-tenant", "platform", "--dev-roles", "platform-admin",
			"admin", "pools", "delete", poolName)
	})

	// The tenant row must exist before granting it runtime access (the
	// grant is a foreign key against tenants), so bootstrap it first.
	if err := d.BootstrapTenant(ctx, tenantID); err != nil {
		t.Fatalf("bootstrap tenant %q: %v", tenantID, err)
	}

	// Step: grant the tenant access to the runtime. spec: §26 line 30 —
	// "Operators grant access per tenant via POST
	// /v1/admin/runtimes/{name}/tenant-access ... after install."
	if out, err := runCLI(t, lennyCtl, repo,
		"--api-url", d.BaseURL(), "--dev-tenant", "platform", "--dev-roles", "platform-admin",
		"admin", "runtimes", "grant-access", "--runtime", name, "--tenant", tenantID); err != nil {
		t.Fatalf("lenny-ctl admin runtimes grant-access: %v\n%s", out, err)
	}

	// Step: wait for the pool to reach a real pod. POST /v1/admin/pools
	// only writes the Postgres-authoritative pool row; the
	// PoolScalingController reconciles it into a SandboxTemplate/
	// SandboxWarmPool CRD pair on its own poll interval, and only once
	// that CRD pair exists can the gateway's pool resolver (which lists
	// SandboxWarmPools, not Postgres rows) find it at all — before then,
	// session creation fails closed with the non-retried
	// "no warm pool matches the runtime" rather than a warming-up
	// response. Waiting for the pod sidesteps that CRD-propagation
	// window; createAndStartTolerant below covers the further §5.2
	// PoolWarmingUp window between the pod existing and it being idle
	// and claimable.
	waitForPoolPod(t, c, poolName, 2*time.Minute)

	// Step: drive a session against the newly published runtime.
	// createAndStartTolerant retries on ErrPoolNotReady for a generous
	// budget beyond CreateAndStart's own short retry window, which is
	// sized for an already-warm reference pool.
	sess := createAndStartTolerant(t, ctx, d, tenantID, name, 3*time.Minute)
	t.Cleanup(func() {
		_ = d.Terminate(context.Background(), tenantID, sess.ID)
	})

	resp, err := d.SendMessage(ctx, tenantID, sess.ID, "hello")
	if err != nil {
		t.Fatalf("send message to session against %q: %v", name, err)
	}
	want := fmt.Sprintf("[%s seq=1] hello", name)
	if !strings.Contains(string(resp.Output), want) {
		t.Fatalf("session against freshly published runtime %q did not echo the expected text: got %s, want substring %q",
			name, resp.Output, want)
	}
}

// runCLI runs the built lenny-ctl (or any CLI) binary with args, working
// directory dir, and returns its combined stdout+stderr.
func runCLI(t *testing.T, bin, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runDocker runs the docker CLI with args and returns its combined
// output.
func runDocker(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	return string(out), err
}

// overrideIsolationProfile rewrites the scaffold's default
// `isolationProfile: sandboxed` to `standard` in place. The tier-5 Kind
// cluster only installs the runc RuntimeClass (no gVisor node pool), so
// a sandboxed pool never schedules a pod there.
func overrideIsolationProfile(t *testing.T, manifestPath string) {
	t.Helper()
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read %s: %v", manifestPath, err)
	}
	rewritten := strings.Replace(string(raw), "isolationProfile: sandboxed", "isolationProfile: standard", 1)
	if rewritten == string(raw) {
		t.Fatalf("scaffold %s does not contain the expected isolationProfile: sandboxed line", manifestPath)
	}
	if err := os.WriteFile(manifestPath, []byte(rewritten), 0o600); err != nil {
		t.Fatalf("write %s: %v", manifestPath, err)
	}
}

// buildImage runs `docker build` against repo, tagging the result as
// tag. A missing docker binary or a build failure fails the test; both
// are genuine external dependencies of this journey (§26 line 8: "CI
// that publishes OCI images to the canonical Lenny registry").
func buildImage(t *testing.T, repo, tag string) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker not on PATH: %v", err)
	}
	cmd := exec.Command("docker", "build", "-t", tag, ".")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker build %s: %v\n%s", tag, err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rmi", tag).Run()
	})
}

// startLocalRegistry starts a local `registry:2` container publishing
// to an explicit host port on all interfaces (0.0.0.0), and returns its
// "127.0.0.1:<port>" address. It skips the test when the registry:2
// image cannot be pulled (no network egress) — a genuine external
// dependency the same way the tier-11 tests treat docker/homebrew.
//
// The host port must be explicit and bound to 0.0.0.0 rather than the
// ephemeral-port `-p 127.0.0.1::5000` form: on a Docker Desktop host the
// daemon that services `docker push` runs inside the Desktop VM and
// cannot reach a loopback-only publish set up by the macOS-side port
// forwarder, but it can reach an explicit 0.0.0.0-bound port from its
// own root network namespace.
func startLocalRegistry(t *testing.T) string {
	t.Helper()
	if out, err := runDocker(t, "pull", "registry:2"); err != nil {
		t.Skipf("registry:2 image unavailable (no network egress?): %v\n%s", err, out)
	}
	cname := fmt.Sprintf("lenny-e2e-journey-registry-%d", time.Now().UnixNano())
	port := freeLocalPort(t)
	run := exec.Command("docker", "run", "-d", "--rm", "--name", cname,
		"-p", fmt.Sprintf("%d:5000", port), "registry:2")
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("docker run registry:2: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", cname).Run()
	})

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(30 * time.Second)
	for {
		res, err := http.Get("http://" + addr + "/v2/")
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return addr
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("local registry at %s did not become ready", addr)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// freeLocalPort asks the OS for an unused TCP port by binding to :0 and
// immediately closing the listener. A small race remains between close
// and the caller's own bind, the same tradeoff
// tests/testinfra/kind/portforward.go's freeLocalPort accepts.
func freeLocalPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freeLocalPort: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// registeredImageField parses the `lenny-ctl runtime publish` combined
// stdout+stderr for the JSON registration response and returns its
// image field. Publish writes its progress lines ("pushing ...",
// "runtime ... registered") to stderr and the registration response
// JSON to stdout; CombinedOutput interleaves them, so this scans for
// the first line that parses as JSON with the expected name.
func registeredImageField(t *testing.T, combined, wantName string) string {
	t.Helper()
	// The JSON response is pretty-printed (multi-line) and interleaved
	// with stderr progress lines; find the first "{" and decode the
	// remainder as one JSON document rather than scanning line by line.
	idx := strings.Index(combined, "{")
	if idx < 0 {
		t.Fatalf("lenny-ctl runtime publish output has no JSON response:\n%s", combined)
	}
	dec := json.NewDecoder(strings.NewReader(combined[idx:]))
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decode lenny-ctl runtime publish response: %v\n%s", err, combined)
	}
	if out["name"] != wantName {
		t.Fatalf("lenny-ctl runtime publish registered name %v, want %q", out["name"], wantName)
	}
	image, _ := out["image"].(string)
	if image == "" {
		t.Fatalf("lenny-ctl runtime publish response has no image field:\n%s", combined)
	}
	return image
}

// pinImageOnNodes loads pushedTag (the reference `docker push` just
// pushed) onto every node of the cluster's containerd content store,
// then tags the same content under its digest-pinned reference on each
// node. This mirrors tests/testinfra/kind/install.sh's resolve_digest
// function and its per-node `ctr images tag --force` loop: `kind load
// docker-image` imports content under the tag name only, but the
// Runtime CRD needs the digest-pinned reference to resolve locally
// under the e2e cluster's pullPolicy: Never overlay.
//
// The local tag target is the tag-stripped `<repo>@sha256:<id>` form,
// not `<repo>:<tag>@sha256:<id>`: a reference carrying both a tag and a
// digest is normalized down to the bare digest form before the kubelet
// queries containerd for it (observed directly — a pod whose image is
// registered under the tag+digest form still reports ImagePullBackOff
// against the bare-digest reference), the same normalization
// install.sh's own resolve_digest / per-node tag loop targets by
// stripping the tag from its destination reference.
func pinImageOnNodes(t *testing.T, c *kind.Cluster, pushedTag, digestRef string) {
	t.Helper()
	load := exec.Command("kind", "load", "docker-image", "--name", c.Name, pushedTag)
	if out, err := load.CombinedOutput(); err != nil {
		t.Fatalf("kind load docker-image %s: %v\n%s", pushedTag, err, out)
	}
	bareDigestRef := stripTag(digestRef)
	nodesOut, err := exec.Command("kind", "get", "nodes", "--name", c.Name).Output()
	if err != nil {
		t.Fatalf("kind get nodes: %v", err)
	}
	nodes := strings.Fields(string(nodesOut))
	if len(nodes) == 0 {
		t.Fatalf("kind get nodes --name %s returned no nodes", c.Name)
	}
	for _, node := range nodes {
		tagCmd := exec.Command("docker", "exec", node,
			"ctr", "-n", "k8s.io", "images", "tag", "--force", pushedTag, bareDigestRef)
		if out, err := tagCmd.CombinedOutput(); err != nil {
			t.Fatalf("ctr images tag %s -> %s on %s: %v\n%s", pushedTag, bareDigestRef, node, err, out)
		}
	}
}

// stripTag removes a `:<tag>` component that precedes an `@sha256:...`
// digest suffix in ref, returning the bare `<repo>@sha256:<hex>` form.
// ref without a digest suffix is returned unchanged.
func stripTag(ref string) string {
	at := strings.Index(ref, "@")
	if at < 0 {
		return ref
	}
	repo, digest := ref[:at], ref[at:]
	if colon := strings.LastIndex(repo, ":"); colon > strings.LastIndex(repo, "/") {
		repo = repo[:colon]
	}
	return repo + digest
}

// waitForPoolPod polls the lenny-agents namespace for a Running pod
// carrying the lenny.dev/pool=poolName label, up to timeout. It does
// not require the pod's containers to be Ready (that is the further
// §5.2 PoolWarmingUp window createAndStartTolerant covers); it only
// confirms the PoolScalingController has reconciled the pool into a
// live SandboxTemplate/SandboxWarmPool CRD pair and the Sandbox
// reconciler has scheduled a pod from it.
func waitForPoolPod(t *testing.T, c *kind.Cluster, poolName string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		out, err := c.KubectlOut(t,
			"-n", "lenny-agents", "get", "pods",
			"-l", "lenny.dev/pool="+poolName,
			"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\t\"}{.status.phase}{\"\\n\"}{end}")
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				fields := strings.Split(line, "\t")
				if len(fields) == 2 && fields[1] == "Running" {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no Running pod for pool %q within %s (last kubectl output: %s, err: %v)",
				poolName, timeout, out, err)
		}
		time.Sleep(3 * time.Second)
	}
}

// createAndStartTolerant retries sessiondriver.CreateAndStart on
// ErrPoolNotReady for up to timeout. A freshly created pool needs real
// wall-clock time to schedule a pod and complete the adapter's
// readiness handshake — well beyond CreateAndStart's own ~10s retry
// budget, which is sized for an already-warm reference pool.
func createAndStartTolerant(t *testing.T, ctx context.Context, d *sessiondriver.Driver, tenantID, runtimeRef string, timeout time.Duration) *sessiondriver.Session {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		sess, err := d.CreateAndStart(ctx, tenantID, runtimeRef)
		if err == nil {
			return sess
		}
		if !errors.Is(err, sessiondriver.ErrPoolNotReady) {
			t.Fatalf("create-and-start session against %q: %v", runtimeRef, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("pool for runtime %q did not warm up within %s: %v", runtimeRef, timeout, err)
		}
		time.Sleep(5 * time.Second)
	}
}
