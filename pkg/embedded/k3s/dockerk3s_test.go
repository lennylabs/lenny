// SPDX-License-Identifier: MIT

package k3s

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// dockerk3s_test.go exercises the Docker-backed launcher's concrete
// surface (dockerk3s.go): the docker run argv construction, the
// kubeconfig server-URL rewrite (a pure function), and the Start path end
// to end against an injected fake docker so no real docker is invoked.
// The dockerLauncher type and its helpers compile on every GOOS, so these
// tests run on the Linux CI host as well as on macOS and Windows; the
// launcher is constructed directly rather than through New, which selects
// it only on non-Linux hosts.
//
// spec: §17.4 (on macOS and Windows the embedded k3s runs as a container
// under Docker Desktop's Linux VM with the identical cluster-disabling
// flag set; the in-container kubeconfig server URL is rewritten to the
// published host port), §24.19 (lenny up/down manage the substrate).

// fakeDocker records every docker invocation and returns scripted output
// keyed by the first docker argument (the subcommand). A handler may
// return output and an error; an absent handler returns empty output and
// no error so unmatched calls (such as the stale-container rm) are
// benign.
type fakeDocker struct {
	calls    [][]string
	handlers map[string]func(args []string) ([]byte, error)
}

func newFakeDocker() *fakeDocker {
	return &fakeDocker{handlers: map[string]func(args []string) ([]byte, error){}}
}

func (f *fakeDocker) run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if len(args) > 0 {
		if h, ok := f.handlers[args[0]]; ok {
			return h(args)
		}
	}
	return nil, nil
}

// callsFor returns the recorded invocations whose first argument is sub.
func (f *fakeDocker) callsFor(sub string) [][]string {
	var out [][]string
	for _, c := range f.calls {
		if len(c) > 0 && c[0] == sub {
			out = append(out, c)
		}
	}
	return out
}

// newDockerLauncherForTest builds a Docker-backed launcher wired to a
// fake docker runner, bypassing New (which selects the launcher only on
// non-Linux hosts) so the concrete surface is testable on any host.
func newDockerLauncherForTest(t *testing.T, fd *fakeDocker) *dockerLauncher {
	t.Helper()
	d, ok := newDockerLauncher(Config{Dir: t.TempDir()}.withDefaults()).(*dockerLauncher)
	if !ok {
		t.Fatalf("newDockerLauncher returned %T, want *dockerLauncher", d)
	}
	d.runDocker = fd.run
	return d
}

// spec: §17.4 (the in-container kubeconfig server URL is rewritten so
// host-process controllers reach the API server via the published host
// port). The rewrite is a pure function, exercised here across the URL
// forms k3s emits.
func TestRewriteKubeconfigServer(t *testing.T) {
	cases := []struct {
		name string
		in   string
		port int
		want string
	}{
		{
			name: "loopback in-container url",
			in:   "clusters:\n- cluster:\n    server: https://127.0.0.1:6443\n  name: default\n",
			port: 6443,
			want: "    server: https://127.0.0.1:6443",
		},
		{
			name: "non-default host port",
			in:   "    server: https://127.0.0.1:6443\n",
			port: 16443,
			want: "    server: https://127.0.0.1:16443",
		},
		{
			name: "container hostname url is rewritten to loopback",
			in:   "    server: https://k3s-server:6443\n",
			port: 6443,
			want: "    server: https://127.0.0.1:6443",
		},
		{
			name: "indentation preserved",
			in:   "      server: https://0.0.0.0:6443\n",
			port: 6443,
			want: "      server: https://127.0.0.1:6443",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rewriteKubeconfigServer(tc.in, tc.port)
			if !strings.Contains(got, tc.want) {
				t.Errorf("rewriteKubeconfigServer(...) = %q, want a line %q", got, tc.want)
			}
			// The original in-container address must not survive when it
			// differs from the rewritten loopback host-port URL.
			if strings.Contains(got, "k3s-server") || strings.Contains(got, "0.0.0.0") {
				t.Errorf("rewrite left the in-container address in %q", got)
			}
		})
	}
}

// TestRewriteKubeconfigServerLeavesOtherLines confirms the rewrite
// touches only the server line and leaves certificate-authority data and
// other fields intact, so the resulting kubeconfig still authenticates.
func TestRewriteKubeconfigServerLeavesOtherLines(t *testing.T) {
	in := "apiVersion: v1\nclusters:\n- cluster:\n" +
		"    certificate-authority-data: QUJD\n" +
		"    server: https://127.0.0.1:6443\n" +
		"  name: default\n"
	got := rewriteKubeconfigServer(in, 7443)
	if !strings.Contains(got, "certificate-authority-data: QUJD") {
		t.Errorf("rewrite dropped the CA data: %q", got)
	}
	if !strings.Contains(got, "https://127.0.0.1:7443") {
		t.Errorf("rewrite did not apply the host port: %q", got)
	}
}

// spec: §17.4 (the same pinned k3s version runs as a container with the
// identical cluster-disabling flag set, and the substrate-specific
// --bind-address/--rootless flags are dropped for the container path).
func TestDockerRunArgs(t *testing.T) {
	d := newDockerLauncherForTest(t, newFakeDocker())
	d.cfg.APIPort = 16443
	args := d.runArgs()
	joined := strings.Join(args, " ")

	// The pinned image carries the Docker-tag form of the same version the
	// Linux launcher downloads (the `+` build separator is `-` in a Docker
	// tag). The run argv must reference the resolved containerImage exactly.
	if !strings.Contains(joined, containerImage) {
		t.Errorf("docker run argv does not pin the k3s image %q: %v", containerImage, args)
	}
	// The API port is published to the matching host port on loopback.
	if !strings.Contains(joined, "127.0.0.1:16443:16443") {
		t.Errorf("docker run argv does not publish the API port to the host port: %v", args)
	}
	// The container runs detached and privileged.
	if !hasArg(args, "-d") || !hasArg(args, "--privileged") {
		t.Errorf("docker run argv missing -d/--privileged: %v", args)
	}
	// The cluster-disabling flag set matches the Linux launcher.
	for _, want := range []string{"--disable-cloud-controller", "--disable-network-policy"} {
		if !hasArg(args, want) {
			t.Errorf("docker run argv missing %q: %v", want, args)
		}
	}
	if !strings.Contains(joined, "--flannel-backend host-gw") {
		t.Errorf("docker run argv missing --flannel-backend host-gw: %v", args)
	}
	// The host-process-specific flags are dropped for the container path.
	if hasArg(args, "--bind-address") {
		t.Errorf("docker run argv must not carry --bind-address (container path): %v", args)
	}
	if hasArg(args, "--rootless") {
		t.Errorf("docker run argv must not carry --rootless (container path): %v", args)
	}
	// The host.docker.internal alias is mapped to the host gateway IP so an
	// in-cluster agent pod reaches the host gateway across the host/Docker
	// boundary. spec: §4.7.
	if !strings.Contains(joined, "--add-host host.docker.internal:host-gateway") {
		t.Errorf("docker run argv missing --add-host host.docker.internal:host-gateway "+
			"(the §4.7 gateway↔adapter callback alias): %v", args)
	}
}

// TestDockerRunArgsPublishesGatewayNodePortToLoopback asserts the
// Docker-backed launcher publishes the configured in-VM gateway NodePort to
// host loopback (-p 127.0.0.1:<nodePort>:<nodePort>) so the host-side
// forwarder reaches the in-cluster gateway, while the in-VM NodePort's
// 0.0.0.0 bind stays contained inside the Docker VM. A zero GatewayNodePort
// publishes nothing.
//
// diagnosis: a failure means the in-cluster gateway is unreachable from the
// host on the Docker-backed substrate (no node-port publish) or is exposed
// beyond loopback (a publish on a non-loopback host address), violating the
// §17.4 EMBEDDED_MODE_LOCAL_ONLY fail-closed invariant.
//
// spec: §17.4 (the CLI reaches the in-cluster gateway through the
// loopback-only host-side forwarder in front of the node port;
// EMBEDDED_MODE_LOCAL_ONLY).
func TestDockerRunArgsPublishesGatewayNodePortToLoopback(t *testing.T) {
	d := newDockerLauncherForTest(t, newFakeDocker())
	d.cfg.GatewayNodePort = 30080
	joined := strings.Join(d.runArgs(), " ")
	if !strings.Contains(joined, "-p 127.0.0.1:30080:30080") {
		t.Errorf("docker run argv does not publish the gateway NodePort to host loopback: %v", d.runArgs())
	}
	// The publish must bind host loopback only, never 0.0.0.0 on the host.
	if strings.Contains(joined, "0.0.0.0:30080") || strings.Contains(joined, " 30080:30080") {
		t.Errorf("docker run argv publishes the gateway NodePort beyond host loopback: %v", d.runArgs())
	}

	// A zero GatewayNodePort publishes no gateway port (the API-port publish
	// is the only -p for the API port).
	d.cfg.GatewayNodePort = 0
	if strings.Contains(strings.Join(d.runArgs(), " "), ":30080:") {
		t.Errorf("docker run argv published a gateway NodePort with none configured: %v", d.runArgs())
	}
}

// spec: §4.7 (the gateway↔adapter gRPC+mTLS callback traverses the
// host/Docker boundary; the in-cluster adapter reaches the host gateway at
// host.docker.internal), §17.4. The Docker-backed launcher runs k3s inside
// the Docker VM, so a pod cannot reach the host at loopback; GatewayHost
// must return the host.docker.internal alias the launcher maps in
// runArgs.
func TestDockerLauncherGatewayHostIsDockerInternal(t *testing.T) {
	d := newDockerLauncherForTest(t, newFakeDocker())
	if got := d.GatewayHost(); got != "host.docker.internal" {
		t.Errorf("dockerLauncher.GatewayHost() = %q, want host.docker.internal", got)
	}
	// The alias GatewayHost returns must be the one runArgs maps, so the
	// address the controller stamps onto pods resolves inside the Docker VM.
	if !strings.Contains(strings.Join(d.runArgs(), " "), d.GatewayHost()+":host-gateway") {
		t.Errorf("runArgs does not map the GatewayHost alias %q to host-gateway", d.GatewayHost())
	}
}

// TestContainerImageTagIsDockerSafe pins the `+`-to-`-` translation
// between the k3s GitHub release tag (Version, which the Linux launcher
// downloads with its `+` build separator) and the Docker image tag (which
// cannot contain `+`). A Docker tag with a `+` is an invalid reference, so
// docker run rejects it; Docker Hub publishes the image under the `-`
// form. The resolved containerImage must carry no `+`.
//
// spec: §17.4 (the same pinned k3s version runs as a container under
// Docker Desktop's Linux VM).
func TestContainerImageTagIsDockerSafe(t *testing.T) {
	if got := containerImageTag("v1.31.4+k3s1"); got != "v1.31.4-k3s1" {
		t.Errorf("containerImageTag(v1.31.4+k3s1) = %q, want v1.31.4-k3s1", got)
	}
	// A version with no build separator is unchanged.
	if got := containerImageTag("v1.31.4"); got != "v1.31.4" {
		t.Errorf("containerImageTag(v1.31.4) = %q, want v1.31.4 (unchanged)", got)
	}
	// The resolved image the launcher runs must be a valid docker
	// reference: a `+` makes `docker run` reject it as an invalid format.
	if strings.Contains(containerImage, "+") {
		t.Errorf("containerImage %q contains a `+`; docker rejects it as an invalid reference", containerImage)
	}
}

// TestDockerServerArgsMatchLinuxDisableSet asserts the container server
// flags are exactly the Linux launcher's cluster-disabling set minus the
// two host-process-specific flags, so the embedded cluster is the same
// k3s distribution on every host.
func TestDockerServerArgsMatchLinuxDisableSet(t *testing.T) {
	d := newDockerLauncherForTest(t, newFakeDocker())
	args := d.serverArgs()
	for _, want := range []string{"--disable", "traefik", "servicelb", "--disable-cloud-controller", "--disable-network-policy", "--flannel-backend", "host-gw"} {
		if !hasArg(args, want) {
			t.Errorf("server argv missing %q: %v", want, args)
		}
	}
	if hasArg(args, "--bind-address") || hasArg(args, "--rootless") {
		t.Errorf("container server argv must drop --bind-address/--rootless: %v", args)
	}
}

// spec: §17.4 (Start provisions the container, publishes the API port,
// and extracts and rewrites the in-container kubeconfig). The end-to-end
// Start path is exercised against a fake docker that scripts a ready
// container and an in-container kubeconfig. No persisted container exists,
// so Start provisions fresh with a docker run rather than restarting a
// stopped one (the containerExists probe returns empty).
func TestDockerLauncherStartProvisionsAndRewritesKubeconfig(t *testing.T) {
	withLookPathDocker(t, true)
	fd := newFakeDocker()
	// No persisted container: the State.Status probe returns empty so
	// containerExists is false and Start provisions fresh.
	fd.handlers["inspect"] = func([]string) ([]byte, error) { return []byte("\n"), nil }
	fd.handlers["exec"] = func([]string) ([]byte, error) {
		return []byte("clusters:\n- cluster:\n    server: https://127.0.0.1:6443\n"), nil
	}
	d := newDockerLauncherForTest(t, fd)

	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// A docker run was issued with the pinned image (fresh provision).
	runs := fd.callsFor("run")
	if len(runs) != 1 {
		t.Fatalf("expected exactly one docker run, got %d: %v", len(runs), runs)
	}
	if !hasArg(runs[0], containerImage) {
		t.Errorf("docker run did not use the pinned image: %v", runs[0])
	}
	// No persisted container existed, so Start did not restart one.
	if starts := fd.callsFor("start"); len(starts) != 0 {
		t.Errorf("Start issued a docker start with no persisted container: %v", starts)
	}

	// The kubeconfig was written with its server URL rewritten to the
	// published host port.
	data, err := os.ReadFile(d.KubeconfigPath())
	if err != nil {
		t.Fatalf("read rewritten kubeconfig: %v", err)
	}
	if !strings.Contains(string(data), "https://127.0.0.1:6443") {
		t.Errorf("rewritten kubeconfig missing host-port server URL: %s", data)
	}
	// The kubeconfig is written with owner-only permissions.
	fi, err := os.Stat(d.KubeconfigPath())
	if err != nil {
		t.Fatalf("stat kubeconfig: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("kubeconfig perms = %o, want 0600", fi.Mode().Perm())
	}
}

// spec: §17.4 (an unsupported host fails closed). On a non-Linux host
// without Docker the Docker-backed Start rejects the launch with the
// platform diagnostic and never shells out to docker. On Linux the host
// is supported unconditionally, so this Docker-absent gate is skipped.
func TestDockerLauncherStartFailsClosedWithoutDocker(t *testing.T) {
	if SupportedPlatform() {
		// On a host where the substrate is supported (Linux, or a
		// Docker-present non-Linux host) the gate does not fire.
		withLookPathDocker(t, false)
		if SupportedPlatform() {
			t.Skip("host supports the substrate unconditionally; Docker-absent gate not reachable")
		}
	}
	withLookPathDocker(t, false)
	fd := newFakeDocker()
	d := newDockerLauncherForTest(t, fd)
	err := d.Start(context.Background())
	if err == nil {
		t.Fatal("expected Start to fail closed without Docker")
	}
	if !strings.Contains(err.Error(), "Docker") {
		t.Errorf("error %q does not name the Docker prerequisite", err.Error())
	}
	if len(fd.calls) != 0 {
		t.Errorf("Start shelled out to docker on an unsupported host: %v", fd.calls)
	}
}

// spec: §17.4 (Start bounds the readiness wait by ReadyTimeout). When the
// container stays running but never writes its kubeconfig, waitReady
// surfaces the timeout error rather than blocking indefinitely. A tiny
// ReadyTimeout drives the deadline branch; the retry loop also covers the
// transient cat-error-while-bootstrapping case.
func TestDockerLauncherStartTimesOutWhenNeverReady(t *testing.T) {
	withLookPathDocker(t, true)
	fd := newFakeDocker()
	fd.handlers["inspect"] = func([]string) ([]byte, error) { return []byte("true\n"), nil }
	fd.handlers["exec"] = func([]string) ([]byte, error) {
		// k3s is still bootstrapping: the kubeconfig is absent, so the cat
		// errors on every poll.
		return []byte("cat: can't open"), errors.New("exit status 1")
	}
	d := newDockerLauncherForTest(t, fd)
	d.cfg.ReadyTimeout = time.Millisecond
	err := d.Start(context.Background())
	if err == nil {
		t.Fatal("expected Start to time out when the API server never becomes ready")
	}
	if !strings.Contains(err.Error(), "not ready within") {
		t.Errorf("error %q does not report the readiness timeout", err.Error())
	}
}

// spec: §17.4 (Start fails when the container exits before readiness).
// The fake reports the container not running, so waitReady reports the
// early-exit error rather than blocking to the deadline.
func TestDockerLauncherStartFailsWhenContainerExits(t *testing.T) {
	withLookPathDocker(t, true)
	fd := newFakeDocker()
	fd.handlers["inspect"] = func([]string) ([]byte, error) { return []byte("false\n"), nil }
	fd.handlers["exec"] = func([]string) ([]byte, error) {
		return []byte("crashed"), errors.New("container not running")
	}
	d := newDockerLauncherForTest(t, fd)
	err := d.Start(context.Background())
	if err == nil {
		t.Fatal("expected Start to fail when the container exits before readiness")
	}
	if !strings.Contains(err.Error(), "exited before becoming ready") {
		t.Errorf("error %q does not report the early container exit", err.Error())
	}
}

// spec: §17.4 (Start fails closed and surfaces docker's diagnostic when
// the docker run itself fails, such as an unpullable image or a port
// already bound). The fake scripts the run to error.
func TestDockerLauncherStartFailsWhenDockerRunErrors(t *testing.T) {
	withLookPathDocker(t, true)
	fd := newFakeDocker()
	fd.handlers["run"] = func([]string) ([]byte, error) {
		return []byte("Error response from daemon: port is already allocated"), errors.New("exit status 125")
	}
	d := newDockerLauncherForTest(t, fd)
	err := d.Start(context.Background())
	if err == nil {
		t.Fatal("expected Start to fail when docker run errors")
	}
	if !strings.Contains(err.Error(), "docker run k3s") {
		t.Errorf("error %q does not name the failing docker run", err.Error())
	}
	// docker's own stderr is surfaced so the operator can diagnose.
	if !strings.Contains(err.Error(), "port is already allocated") {
		t.Errorf("error %q does not carry docker's diagnostic", err.Error())
	}
}

// spec: §17.4 (the in-container kubeconfig is extracted after readiness).
// When the post-ready extraction read fails, Start surfaces the
// extraction error and writes no kubeconfig. The fake returns the
// kubeconfig once (satisfying waitReady) then errors on the extraction
// read.
func TestDockerLauncherStartFailsWhenKubeconfigExtractionErrors(t *testing.T) {
	withLookPathDocker(t, true)
	fd := newFakeDocker()
	fd.handlers["inspect"] = func([]string) ([]byte, error) { return []byte("true\n"), nil }
	var execCalls int
	fd.handlers["exec"] = func([]string) ([]byte, error) {
		execCalls++
		if execCalls == 1 {
			// First read (waitReady) sees a kubeconfig: the API server is
			// serving.
			return []byte("    server: https://127.0.0.1:6443\n"), nil
		}
		// Second read (extractKubeconfig) fails.
		return []byte("docker exec error"), errors.New("exit status 1")
	}
	d := newDockerLauncherForTest(t, fd)
	err := d.Start(context.Background())
	if err == nil {
		t.Fatal("expected Start to fail when the kubeconfig extraction read errors")
	}
	if !strings.Contains(err.Error(), "read in-container kubeconfig") {
		t.Errorf("error %q does not name the extraction read", err.Error())
	}
	if _, statErr := os.Stat(d.KubeconfigPath()); statErr == nil {
		t.Error("Start wrote a kubeconfig despite the extraction failure")
	}
}

// TestDockerLauncherStartFailsWhenDirUnwritable covers the state-directory
// creation failure: when Start cannot create the launcher's state
// directory it fails closed before any docker call. The directory's
// parent is made a file so MkdirAll errors.
func TestDockerLauncherStartFailsWhenDirUnwritable(t *testing.T) {
	withLookPathDocker(t, true)
	fd := newFakeDocker()
	d := newDockerLauncherForTest(t, fd)
	// Replace the launcher's state dir with a path whose parent is a
	// regular file, so MkdirAll cannot create it.
	parent := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed parent file: %v", err)
	}
	d.cfg.Dir = filepath.Join(parent, "k3s")
	err := d.Start(context.Background())
	if err == nil {
		t.Fatal("expected Start to fail when the state directory cannot be created")
	}
	if len(fd.callsFor("run")) != 0 {
		t.Errorf("Start issued docker run despite the directory failure: %v", fd.calls)
	}
}

// TestDockerLauncherStopBeforeStartReportsNotRunning confirms the
// Launcher-interface invariants before Start hold for the Docker-backed
// launcher: Running is false (docker reports no such container) and PID is
// zero.
func TestDockerLauncherStopBeforeStartReportsNotRunning(t *testing.T) {
	fd := newFakeDocker()
	fd.handlers["inspect"] = func([]string) ([]byte, error) { return nil, errors.New("no such container") }
	d := newDockerLauncherForTest(t, fd)
	if d.Running() {
		t.Error("Running() = true before Start")
	}
	if d.PID() != 0 {
		t.Errorf("PID() = %d before Start, want 0", d.PID())
	}
}

// TestDockerLauncherStopPersistsContainer confirms Stop issues a docker stop
// (not a docker rm) so the container and its containerd image store persist
// for a warm lenny up, and is idempotent before Start.
//
// spec: §17.4 (lenny down persists the substrate and the imported-image
// store).
func TestDockerLauncherStopPersistsContainer(t *testing.T) {
	fd := newFakeDocker()
	d := newDockerLauncherForTest(t, fd)
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop before Start errored: %v", err)
	}
	stops := fd.callsFor("stop")
	if len(stops) == 0 {
		t.Fatal("Stop did not issue a docker stop")
	}
	if !hasArg(stops[0], d.name) {
		t.Errorf("docker stop did not target the container name %q: %v", d.name, stops[0])
	}
	// Stop must not force-remove the container; the image store must survive.
	if rms := fd.callsFor("rm"); len(rms) != 0 {
		t.Errorf("Stop force-removed the container instead of persisting it: %v", rms)
	}
}

// TestDockerLauncherRemoveRemovesContainer confirms Remove issues a forced
// docker rm so --purge discards the container and its containerd image store,
// and is idempotent before Start.
//
// spec: §17.4 (--purge removes the persisted substrate and the
// imported-image store).
func TestDockerLauncherRemoveRemovesContainer(t *testing.T) {
	fd := newFakeDocker()
	d := newDockerLauncherForTest(t, fd)
	if err := d.Remove(); err != nil {
		t.Fatalf("Remove before Start errored: %v", err)
	}
	rms := fd.callsFor("rm")
	if len(rms) == 0 {
		t.Fatal("Remove did not issue a docker rm")
	}
	if !hasArg(rms[0], d.name) || !hasArg(rms[0], "-f") {
		t.Errorf("docker rm was not a forced removal of the container name %q: %v", d.name, rms[0])
	}
}

// TestDockerLauncherStartRestartsPersistedContainer confirms a warm Start
// reuses a persisted (stopped) container with a docker start rather than
// re-running a fresh one, so the containerd image store is not discarded.
//
// spec: §17.4 (a warm lenny up restarts the persisted substrate without
// re-pulling k3s or re-importing the platform images).
func TestDockerLauncherStartRestartsPersistedContainer(t *testing.T) {
	withLookPathDocker(t, true)
	fd := newFakeDocker()
	// A persisted container exists: the State.Status probe returns a status.
	fd.handlers["inspect"] = func([]string) ([]byte, error) { return []byte("exited\n"), nil }
	fd.handlers["exec"] = func([]string) ([]byte, error) {
		return []byte("clusters:\n- cluster:\n    server: https://127.0.0.1:6443\n"), nil
	}
	d := newDockerLauncherForTest(t, fd)

	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	starts := fd.callsFor("start")
	if len(starts) != 1 || !hasArg(starts[0], d.name) {
		t.Fatalf("warm Start did not docker start the persisted container: %v", starts)
	}
	// A warm Start must not provision a fresh container.
	if runs := fd.callsFor("run"); len(runs) != 0 {
		t.Errorf("warm Start re-ran a fresh container instead of restarting the persisted one: %v", runs)
	}
}

// TestDockerLauncherRunning maps docker inspect's running state to the
// Launcher Running() result, including the not-running and docker-error
// cases.
func TestDockerLauncherRunning(t *testing.T) {
	fd := newFakeDocker()
	d := newDockerLauncherForTest(t, fd)

	fd.handlers["inspect"] = func([]string) ([]byte, error) { return []byte("true\n"), nil }
	if !d.Running() {
		t.Error("Running() = false when docker reports the container running")
	}
	fd.handlers["inspect"] = func([]string) ([]byte, error) { return []byte("false\n"), nil }
	if d.Running() {
		t.Error("Running() = true when docker reports the container stopped")
	}
	fd.handlers["inspect"] = func([]string) ([]byte, error) { return nil, errors.New("no such container") }
	if d.Running() {
		t.Error("Running() = true when docker errors (no such container)")
	}
}

// TestDockerLauncherPIDIsZero confirms the Docker-backed launcher reports
// no host PID: its k3s runs inside the Docker VM, not as a host process.
func TestDockerLauncherPIDIsZero(t *testing.T) {
	d := newDockerLauncherForTest(t, newFakeDocker())
	if d.PID() != 0 {
		t.Errorf("PID() = %d, want 0 for the Docker-backed launcher", d.PID())
	}
}

// TestContainerNameSanitizes confirms the container name is derived
// deterministically from the state directory and is a valid docker name.
func TestContainerNameSanitizes(t *testing.T) {
	cases := []struct {
		dir  string
		want string
	}{
		{filepath.Join("/home", "alice", ".lenny", "k3s"), "lenny-embedded-k3s-k3s"},
		{filepath.Join("/tmp", "weird name!"), "lenny-embedded-k3s-weird-name"},
		{"/", "lenny-embedded-k3s-k3s"},
	}
	for _, tc := range cases {
		got := containerName(tc.dir)
		if got != tc.want {
			t.Errorf("containerName(%q) = %q, want %q", tc.dir, got, tc.want)
		}
		if !dockerNamePattern.MatchString(got) {
			t.Errorf("containerName(%q) = %q is not a valid docker name", tc.dir, got)
		}
	}
}

// dockerNamePattern is docker's container-name grammar, used only by the
// test to assert the derived name is valid.
var dockerNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// TestDockerLauncherContainerName confirms the Docker-backed launcher
// exposes its container handle so the stack can record it for a later
// lenny status probe in place of a host PID.
//
// spec: §24.19 (a container-backed launcher records a container handle
// where there is no host PID).
func TestDockerLauncherContainerName(t *testing.T) {
	d := newDockerLauncherForTest(t, newFakeDocker())
	if d.ContainerName() != d.name {
		t.Errorf("ContainerName() = %q, want the launcher container name %q", d.ContainerName(), d.name)
	}
	if d.ContainerName() == "" {
		t.Error("ContainerName() = empty, want a derived docker container name")
	}
}

// withRunDocker swaps the package-level docker runner for the duration of
// a test so the container probe (ContainerRunning) can be exercised
// without invoking a real docker, then restores it.
func withRunDocker(t *testing.T, fn func(ctx context.Context, args ...string) ([]byte, error)) {
	t.Helper()
	prev := runDocker
	t.Cleanup(func() { runDocker = prev })
	runDocker = fn
}

// TestContainerRunning maps docker inspect's running state to the
// ContainerRunning result lenny status uses to probe the Docker-backed
// substrate by name, including the not-running, docker-error, and empty-
// name fail-closed cases.
//
// spec: §24.19 (the k3s health probe is a container probe on the
// Docker-backed substrate, where there is no host PID).
func TestContainerRunning(t *testing.T) {
	// An empty handle reports not-running and never shells out.
	withRunDocker(t, func(context.Context, ...string) ([]byte, error) {
		t.Fatal("ContainerRunning('') must not shell out to docker")
		return nil, nil
	})
	if ContainerRunning("") {
		t.Error("ContainerRunning(\"\") = true, want false (fail closed)")
	}

	// docker reports the container running.
	withRunDocker(t, func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) == 0 || args[0] != "inspect" {
			t.Errorf("ContainerRunning issued %v, want a docker inspect", args)
		}
		if !hasArg(args, "lenny-embedded-k3s-demo") {
			t.Errorf("ContainerRunning did not target the container name: %v", args)
		}
		return []byte("true\n"), nil
	})
	if !ContainerRunning("lenny-embedded-k3s-demo") {
		t.Error("ContainerRunning = false when docker reports the container running")
	}

	// docker reports the container stopped.
	withRunDocker(t, func(context.Context, ...string) ([]byte, error) { return []byte("false\n"), nil })
	if ContainerRunning("lenny-embedded-k3s-demo") {
		t.Error("ContainerRunning = true when docker reports the container stopped")
	}

	// docker errors (absent container / docker unavailable): fail closed.
	withRunDocker(t, func(context.Context, ...string) ([]byte, error) {
		return nil, errors.New("no such container")
	})
	if ContainerRunning("lenny-embedded-k3s-demo") {
		t.Error("ContainerRunning = true when docker errors, want false (fail closed)")
	}
}

// TestRemoveContainer covers the container removal lenny down uses to keep a
// crashed-supervisor teardown (and lenny down --purge) from leaking the
// Docker-backed k3s container: the container runs inside the Docker VM with
// no host PID, so removing it by its recorded handle is the only teardown
// reach. An empty handle is a no-op (the Linux child-process substrate
// records no container), a non-empty handle issues `docker rm -f <name>`,
// and a docker error (an already-removed container) is swallowed.
//
// spec: §24.19 (lenny up/down manage the substrate; a crashed supervisor
// must not leak the Docker-backed k3s container).
func TestRemoveContainer(t *testing.T) {
	// An empty handle is a no-op and never shells out: the Linux substrate
	// records no container name.
	withRunDocker(t, func(context.Context, ...string) ([]byte, error) {
		t.Fatal("RemoveContainer(\"\") must not shell out to docker")
		return nil, nil
	})
	RemoveContainer("")

	// A non-empty handle issues a forced docker rm targeting the container.
	var got [][]string
	withRunDocker(t, func(_ context.Context, args ...string) ([]byte, error) {
		got = append(got, args)
		return nil, nil
	})
	RemoveContainer("lenny-embedded-k3s-demo")
	if len(got) != 1 {
		t.Fatalf("RemoveContainer issued %d docker calls, want 1: %v", len(got), got)
	}
	if got[0][0] != "rm" || !hasArg(got[0], "-f") || !hasArg(got[0], "lenny-embedded-k3s-demo") {
		t.Errorf("RemoveContainer issued %v, want a forced docker rm of the container", got[0])
	}

	// A docker error (the container is already gone, or docker is
	// unavailable) is swallowed: the teardown must not fail on a benign rm.
	withRunDocker(t, func(context.Context, ...string) ([]byte, error) {
		return []byte("Error: No such container"), errors.New("exit status 1")
	})
	RemoveContainer("lenny-embedded-k3s-demo") // must not panic or block
}

// TestStopContainer covers the persist-stop lenny down (without --purge) uses
// to halt the Docker-backed k3s container while keeping it and its containerd
// image store, so a warm lenny up restarts it. An empty handle is a no-op
// (the Linux substrate records no container), a non-empty handle issues
// `docker stop <name>` (never a forced rm), and a docker error is swallowed.
//
// spec: §17.4 (lenny down persists the substrate and the imported-image
// store; --purge removes them).
func TestStopContainer(t *testing.T) {
	// An empty handle is a no-op and never shells out.
	withRunDocker(t, func(context.Context, ...string) ([]byte, error) {
		t.Fatal("StopContainer(\"\") must not shell out to docker")
		return nil, nil
	})
	StopContainer("")

	// A non-empty handle issues a plain docker stop (not a forced rm), so the
	// container and its image store persist.
	var got [][]string
	withRunDocker(t, func(_ context.Context, args ...string) ([]byte, error) {
		got = append(got, args)
		return nil, nil
	})
	StopContainer("lenny-embedded-k3s-demo")
	if len(got) != 1 {
		t.Fatalf("StopContainer issued %d docker calls, want 1: %v", len(got), got)
	}
	if got[0][0] != "stop" || !hasArg(got[0], "lenny-embedded-k3s-demo") {
		t.Errorf("StopContainer issued %v, want a docker stop of the container", got[0])
	}
	if hasArg(got[0], "rm") || hasArg(got[0], "-f") {
		t.Errorf("StopContainer force-removed the container instead of persisting it: %v", got[0])
	}

	// A docker error (the container is already stopped, or docker is
	// unavailable) is swallowed.
	withRunDocker(t, func(context.Context, ...string) ([]byte, error) {
		return []byte("Error: No such container"), errors.New("exit status 1")
	})
	StopContainer("lenny-embedded-k3s-demo") // must not panic or block
}

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
