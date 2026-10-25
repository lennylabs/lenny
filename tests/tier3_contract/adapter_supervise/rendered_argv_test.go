//go:build contract

// SPDX-License-Identifier: MIT

package adapter_supervise_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/tests/testinfra/runtimenonce"
)

// renderedSupervisedPod renders a supervised `restart` pod whose runtime
// command is this test binary, with the Runtime env pairs that make the
// launched process act as the contract runtime.
func renderedSupervisedPod(t *testing.T, msgSock, nonce, envFile string) *corev1.Pod {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pod, err := podspec.Build(podspec.Inputs{
		Name: "contract", Namespace: "lenny-agents",
		RuntimeImage:     "runtime@sha256:" + strings.Repeat("1", 64),
		AdapterImage:     "adapter@sha256:" + strings.Repeat("2", 64),
		SupervisorImage:  "supervisor@sha256:" + strings.Repeat("3", 64),
		IsolationProfile: "standard",
		AdapterUID:       int64(os.Getuid()),
		RuntimeCommand:   []string{self},
		RuntimeEnv: []podspec.EnvPair{
			{Name: "PATH", Value: "/usr/local/bin:/usr/bin:/bin"},
			{Name: runtimeModeEnv, Value: "hold"},
			{Name: runtimeSockEnv, Value: msgSock},
			{Name: runtimeNonce, Value: nonce},
			{Name: runtimeEnvFile, Value: envFile},
		},
		RuntimeProcess: "restart",
		LaunchMode:     podspec.LaunchSession,
	})
	if err != nil {
		t.Fatalf("podspec.Build: %v", err)
	}
	return pod
}

func renderedContainer(t *testing.T, cs []corev1.Container, name string) corev1.Container {
	t.Helper()
	for _, c := range cs {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no container %q", name)
	return corev1.Container{}
}

// flagValue returns the value of a --name=value adapter flag.
func flagValue(t *testing.T, args []string, name string) string {
	t.Helper()
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "--"+name+"="); ok {
			return v
		}
	}
	t.Fatalf("adapter args %v carry no --%s", args, name)
	return ""
}

// stageFromRenderedArgv runs the supervisor binary with the stage init
// container's argv, its mount path mapped to dir, and returns the staged
// binary's path.
func stageFromRenderedArgv(t *testing.T, bin string, stage corev1.Container, dir string) string {
	t.Helper()
	if len(stage.Command) != 0 || len(stage.VolumeMounts) != 1 {
		t.Fatalf("stage container = %+v, want the image entrypoint and one mount", stage)
	}
	mount := stage.VolumeMounts[0].MountPath
	args := make([]string, len(stage.Args))
	for i, a := range stage.Args {
		args[i] = strings.Replace(a, mount, dir, 1)
	}
	if out, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
		t.Fatalf("stage %v: %v: %s", args, err, out)
	}
	staged := filepath.Join(dir, "lenny-supervisor")
	fi, err := os.Stat(staged)
	if err != nil {
		t.Fatalf("staged binary: %v", err)
	}
	if fi.Mode().Perm() != 0o555 {
		t.Errorf("staged binary mode = %o, want 0555", fi.Mode().Perm())
	}
	return staged
}

// spec: 4.7.10 (Runtime supervisor; Runtime supervisor, Environment), 13.1 (Runtime supervisor volume), 28.5.3 (CH-SUPERVISE, Endpoint)
// diagnosis: the argv the pod builder renders for the stage init container or
// the runtime container does not run the real supervisor: the stage mode does
// not copy the binary, the run argv does not parse, the supervisor does not
// reach the adapter on the rendered socket, or the launched process does not
// get the Runtime env and the platform variables in the rendered order.
func TestSupervisorStartsFromTheArgvThePodBuilderRenders_spec_4_7_10(t *testing.T) {
	bin, _ := supervisorBinary(t, prefixedBuild)
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skipf("blocked: unshare not on PATH: %v", err)
	}
	m := runtimenonce.Publish(t, nil)
	msgSock, supSock := socketName("msg"), socketName("sup")
	envFile := filepath.Join(t.TempDir(), "env")
	pod := renderedSupervisedPod(t, msgSock, m.Nonce, envFile)

	staged := stageFromRenderedArgv(t, bin, pod.Spec.InitContainers[0], t.TempDir())

	rt := renderedContainer(t, pod.Spec.Containers, podspec.RuntimeContainerName)
	ad := renderedContainer(t, pod.Spec.Containers, podspec.AdapterContainerName)
	renderedSock := flagValue(t, ad.Args, "supervise-socket")
	lifetime := adapter.RuntimeLifetime(flagValue(t, ad.Args, "runtime-process"))
	if lifetime != adapter.LifetimeRestart || adapter.LaunchMode(flagValue(t, ad.Args, "launch-mode")) != adapter.LaunchSession {
		t.Fatalf("rendered lifetime and launch mode = %s %s", lifetime, flagValue(t, ad.Args, "launch-mode"))
	}
	i := slices.Index(rt.Command, "--adapter-uid")
	if i < 0 || rt.Command[i+1] != strconv.Itoa(os.Getuid()) {
		t.Fatalf("rendered runtime argv %v lacks --adapter-uid %d", rt.Command, os.Getuid())
	}

	// Map the rendered argv onto the host: the volume path becomes the staged
	// binary, and the fixed abstract socket name becomes a per-run name so
	// parallel runs do not collide. Every other token runs as rendered.
	argv := slices.Clone(rt.Command)
	argv[0] = staged
	for j := range argv {
		if argv[j] == renderedSock {
			argv[j] = supSock
		}
	}
	nsArgs := append([]string{"--user", "--map-current-user", "--pid", "--fork", "--mount", "--mount-proc", "--ipc", "--kill-child"}, argv...)
	cmd := exec.Command("unshare", nsArgs...)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start supervisor: %v", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })

	auth := adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid())}
	srt, err := adapter.NewSupervisedRuntime(adapter.SupervisedRuntimeConfig{
		Lifetime: lifetime, Mode: adapter.LaunchSession,
		NewTransport: func() (*adapter.SocketRuntimeProcess, error) {
			return adapter.NewSocketRuntimeProcess(msgSock, auth, m.Provider())
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srt.CloseListener() })
	link, err := adapter.AwaitSupervisor(context.Background(), adapter.AwaitSupervisorConfig{
		Socket: supSock, Auth: auth, Lifetime: lifetime, Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("AwaitSupervisor against the rendered argv: %v", err)
	}
	srt.AttachLink(link)
	if err := srt.Start(ctxFor(t, 10*time.Second), "s1"); err != nil {
		t.Fatalf("launch from the rendered argv: %v", err)
	}
	if srt.RuntimeGeneration() != 1 {
		t.Fatalf("generation = %d, want 1", srt.RuntimeGeneration())
	}

	raw, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read the launched process's environment: %v", err)
	}
	env := strings.Split(string(raw), "\n")
	for _, want := range []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		runtimeModeEnv + "=hold",
		"LENNY_ADAPTER_SOCKET=@lenny-runtime",
	} {
		if !slices.Contains(env, want) {
			t.Errorf("launched environment %v lacks %s", env, want)
		}
	}
	for _, v := range env {
		if strings.HasPrefix(v, "HOME=") || strings.HasPrefix(v, "GOPATH=") {
			t.Errorf("launched environment carries %s from the supervisor's own environment", v)
		}
	}
	if link.Failed() {
		t.Fatal("the link failed")
	}
}
