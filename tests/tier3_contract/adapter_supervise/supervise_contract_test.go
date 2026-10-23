//go:build contract

// SPDX-License-Identifier: MIT

// Tier-3 contract coverage of CH-SUPERVISE: the real runtime supervisor
// binary, run as the init of a scratch user and PID namespace, against the
// real adapter client (AwaitSupervisor, SupervisorLink, SupervisedRuntime)
// over the abstract socket. The specification leaves the members of cleanup
// and exited, the property names of ready, and the error codes to the two
// platform binaries; this test pins them.
//
// The supervisor is the build whose platform volume paths sit under a
// scratch directory, so its sweep never reaches the host's /tmp; its wire,
// signal, and process code is the shipped code.
package adapter_supervise_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/scrub"
	"github.com/lennylabs/lenny/tests/testinfra/runtimenonce"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/supervisorbuild"
)

// The launched runtime is this test binary, re-executed with these
// variables: it dials the CH-MSGSOCK socket, writes the nonce line, and
// holds the connection, or exits after connecting.
const (
	runtimeModeEnv = "LENNY_SUPERVISE_CONTRACT_RUNTIME"
	runtimeSockEnv = "LENNY_SUPERVISE_CONTRACT_SOCKET"
	runtimeNonce   = "LENNY_SUPERVISE_CONTRACT_NONCE"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(runtimeModeEnv); mode != "" {
		os.Exit(runAsRuntime(mode))
	}
	os.Exit(m.Run())
}

// runAsRuntime is the launched runtime process.
func runAsRuntime(mode string) int {
	c, err := net.Dial("unix", os.Getenv(runtimeSockEnv))
	if err != nil {
		fmt.Fprintf(os.Stderr, "runtime dial: %v\n", err)
		return 3
	}
	if err := runtimenonce.Write(c, os.Getenv(runtimeNonce)); err != nil {
		return 4
	}
	if mode == "exit" {
		time.Sleep(100 * time.Millisecond)
		return 7
	}
	_, _ = bufio.NewReader(c).ReadString(0)
	return 0
}

var (
	buildOnce sync.Once
	buildBin  string
	buildErr  error
	prefix    string
)

// supervisorBinary builds the supervisor once per test binary.
func supervisorBinary(t *testing.T) (bin, root string) {
	t.Helper()
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skipf("unshare not on PATH: %v", err)
	}
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "supervise-contract-")
		if err != nil {
			buildErr = err
			return
		}
		prefix = filepath.Join(dir, "root")
		for _, d := range []string{"workspace", "sessions", "artifacts", "tmp", "dev/shm"} {
			if err := os.MkdirAll(filepath.Join(prefix, d), 0o755); err != nil {
				buildErr = err
				return
			}
		}
		out := filepath.Join(dir, "bin")
		target := filepath.Join(out, "lenny-supervisor-prefixed")
		_, buildErr = supervisorbuild.Build(context.Background(), supervisorbuild.Options{
			RepoRoot: schematest.RepoRoot(t), Out: out, TestPrefix: prefix, Targets: []string{target},
		})
		buildBin = target
	})
	if errors.Is(buildErr, supervisorbuild.ErrToolchainMissing) {
		t.Skipf("supervisor toolchain missing: %v", buildErr)
	}
	if buildErr != nil {
		t.Fatalf("build supervisor: %v", buildErr)
	}
	return buildBin, prefix
}

func socketName(kind string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "@lenny-contract-" + kind + "-" + hex.EncodeToString(b[:])
}

// supervisorProc is one running supervisor.
type supervisorProc struct {
	cmd  *exec.Cmd
	done chan error
}

// startSupervisor runs the supervisor as the init of a scratch user, PID,
// mount, and IPC namespace.
func startSupervisor(t *testing.T, socket string, flags []string, env []string) *supervisorProc {
	t.Helper()
	bin, _ := supervisorBinary(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--user", "--map-current-user", "--pid", "--fork", "--mount", "--mount-proc", "--ipc", "--kill-child",
		bin, "run", "--socket", socket,
	}
	args = append(args, flags...)
	for _, e := range env {
		args = append(args, "--env", e)
	}
	args = append(args, "--", self)
	cmd := exec.Command("unshare", args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start supervisor: %v", err)
	}
	p := &supervisorProc{cmd: cmd, done: make(chan error, 1)}
	go func() { p.done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-p.done
	})
	return p
}

// exitCode waits for the supervisor to exit within d and returns its status.
func (p *supervisorProc) exitCode(t *testing.T, d time.Duration) int {
	t.Helper()
	select {
	case err := <-p.done:
		p.done <- err
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		if err != nil {
			t.Fatalf("supervisor wait: %v", err)
		}
		return 0
	case <-time.After(d):
		t.Fatalf("supervisor still running after %s", d)
		return -1
	}
}

// contractPod is a supervised runtime attached to a real supervisor.
type contractPod struct {
	rt   *adapter.SupervisedRuntime
	link *adapter.SupervisorLink
	sup  *supervisorProc
}

func newContractPod(t *testing.T, lifetime adapter.RuntimeLifetime, runtimeMode string) *contractPod {
	t.Helper()
	m := runtimenonce.Publish(t, nil)
	msgSock, supSock := socketName("msg"), socketName("sup")
	auth := adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid())}
	rt, err := adapter.NewSupervisedRuntime(adapter.SupervisedRuntimeConfig{
		Lifetime: lifetime, Mode: adapter.LaunchSession,
		NewTransport: func() (*adapter.SocketRuntimeProcess, error) {
			return adapter.NewSocketRuntimeProcess(msgSock, auth, m.Provider())
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.CloseListener() })
	sup := startSupervisor(t, supSock, []string{"--dial-timeout", "10s", "--adapter-uid", fmt.Sprint(os.Getuid())},
		[]string{runtimeModeEnv + "=" + runtimeMode, runtimeSockEnv + "=" + msgSock, runtimeNonce + "=" + m.Nonce})
	link, err := adapter.AwaitSupervisor(context.Background(), adapter.AwaitSupervisorConfig{
		Socket: supSock, Auth: auth, Lifetime: lifetime, Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("AwaitSupervisor: %v", err)
	}
	rt.AttachLink(link)
	return &contractPod{rt: rt, link: link, sup: sup}
}

func ctxFor(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

// spec: 28.5.3 (CH-SUPERVISE, Messages; CH-SUPERVISE, Preconditions), 4.7.11
// (Runtime supervisor, Property self-test)
// diagnosis: the real supervisor and the adapter client disagree on the
// ready message, its property names, a reply's members, or an error code,
// so the pod never becomes READY or misreads every reply as a link failure.
func TestSupervisorAndAdapterClientAgreeOnEveryMessage_spec_28_5_3(t *testing.T) {
	pod := newContractPod(t, adapter.LifetimeRestart, "hold")
	names := make([]string, 0)
	for k := range pod.link.Properties() {
		names = append(names, k)
	}
	sort.Strings(names)
	want := append([]string{}, adapter.SupervisorProperties...)
	sort.Strings(want)
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("ready properties = %v, want %v", names, want)
	}
	if err := pod.rt.Start(ctxFor(t, 10*time.Second), "s1"); err != nil {
		t.Fatalf("launch and accept: %v", err)
	}
	if pod.rt.RuntimeGeneration() != 1 {
		t.Fatalf("generation = %d", pod.rt.RuntimeGeneration())
	}
	if err := pod.rt.RunCleanup(ctxFor(t, 10*time.Second), scrub.CleanupRequest{Commands: []string{"/bin/true"}, Timeout: 5 * time.Second}); err != nil {
		t.Fatalf("cleanup /bin/true: %v", err)
	}
	err := pod.rt.RunCleanup(ctxFor(t, 10*time.Second), scrub.CleanupRequest{Commands: []string{"/bin/false"}, Timeout: 5 * time.Second})
	if err == nil || errors.Is(err, scrub.ErrNotLive) || !strings.Contains(err.Error(), adapter.SuperviseErrCleanupFailed) {
		t.Fatalf("cleanup /bin/false = %v, want %s", err, adapter.SuperviseErrCleanupFailed)
	}
	err = pod.rt.RunCleanup(ctxFor(t, 10*time.Second), scrub.CleanupRequest{Commands: []string{"/bin/sleep 5"}, Timeout: 200 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), adapter.SuperviseErrCleanupTimeout) {
		t.Fatalf("cleanup past its deadline = %v, want %s", err, adapter.SuperviseErrCleanupTimeout)
	}
	if err := pod.rt.EndAndSweep(ctxFor(t, 30*time.Second)); err != nil {
		t.Fatalf("end and sweep: %v", err)
	}
	if err := pod.rt.RebindListeners(); err != nil {
		t.Fatal(err)
	}
	if err := pod.rt.Start(ctxFor(t, 10*time.Second), "s2"); err != nil {
		t.Fatalf("second launch: %v", err)
	}
	pingCtx, stop := context.WithCancel(context.Background())
	go pod.link.RunPing(pingCtx, pod.rt.RuntimeGeneration, 10*time.Millisecond, 2*time.Second)
	time.Sleep(100 * time.Millisecond)
	stop()
	if pod.link.Failed() {
		t.Fatal("the link failed: a reply did not decode strictly")
	}
	if nl, cause := pod.rt.NotLive(); nl {
		t.Fatalf("not live: %s", cause)
	}
}

// spec: 4.7.10 (Runtime supervisor, Children; Runtime supervisor, Runtime
// exits), 28.5.3 (CH-SUPERVISE, Messages)
// diagnosis: the supervisor's exited report did not decode or did not reach
// the adapter's Runtime exits handling, so a crashed runtime leaves the pod
// serving sessions.
func TestSupervisorReportsTheRuntimeExit_spec_4_7_10(t *testing.T) {
	pod := newContractPod(t, adapter.LifetimeKeep, "exit")
	_ = pod.rt.Start(ctxFor(t, 10*time.Second), "s1")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if nl, _ := pod.rt.NotLive(); nl {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if nl, cause := pod.rt.NotLive(); !nl || cause != adapter.CauseRuntimeExit {
		t.Fatalf("not-live = %v (%s), want runtime_exit", nl, cause)
	}
	if pod.link.Failed() {
		t.Fatal("exited failed the link")
	}
}

// spec: 28.5.3 (CH-SUPERVISE, Timing; CH-SUPERVISE, Endpoint; CH-SUPERVISE,
// Degradation), 4.7.10 (Runtime supervisor, Signals)
// diagnosis: the supervisor's dial window, its peer check, or its exit on a
// closed connection does not hold, so a pod can run a runtime container with
// no adapter or with a foreign peer.
func TestSupervisorDialAndConnectionLifetime_spec_28_5_3(t *testing.T) {
	auth := adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid())}
	t.Run("a supervisor started before the adapter listens connects once it does", func(t *testing.T) {
		sock := socketName("late")
		startSupervisor(t, sock, []string{"--dial-timeout", "10s"}, nil)
		time.Sleep(500 * time.Millisecond)
		link, err := adapter.AwaitSupervisor(context.Background(), adapter.AwaitSupervisorConfig{Socket: sock, Auth: auth, Lifetime: adapter.LifetimeKeep, Timeout: 10 * time.Second})
		if err != nil {
			t.Fatalf("AwaitSupervisor: %v", err)
		}
		_ = link.Close()
	})
	t.Run("a short dial window with no adapter exits non-zero", func(t *testing.T) {
		p := startSupervisor(t, socketName("nobody"), []string{"--dial-timeout", "300ms"}, nil)
		if code := p.exitCode(t, 10*time.Second); code == 0 {
			t.Fatal("supervisor exited 0 after its dial window ended")
		}
	})
	t.Run("closing the adapter's end makes the supervisor exit", func(t *testing.T) {
		sock := socketName("close")
		p := startSupervisor(t, sock, []string{"--dial-timeout", "10s"}, nil)
		link, err := adapter.AwaitSupervisor(context.Background(), adapter.AwaitSupervisorConfig{Socket: sock, Auth: auth, Lifetime: adapter.LifetimeKeep, Timeout: 10 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		_ = link.Close()
		p.exitCode(t, 10*time.Second)
	})
	t.Run("no --adapter-uid connects", func(t *testing.T) {
		sock := socketName("nouid")
		startSupervisor(t, sock, []string{"--dial-timeout", "10s"}, nil)
		link, err := adapter.AwaitSupervisor(context.Background(), adapter.AwaitSupervisorConfig{Socket: sock, Auth: auth, Lifetime: adapter.LifetimeKeep, Timeout: 10 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		_ = link.Close()
	})
	t.Run("--adapter-uid naming another UID exits without ready", func(t *testing.T) {
		sock := socketName("baduid")
		p := startSupervisor(t, sock, []string{"--dial-timeout", "10s", "--adapter-uid", fmt.Sprint(os.Getuid() + 4242)}, nil)
		_, err := adapter.AwaitSupervisor(context.Background(), adapter.AwaitSupervisorConfig{Socket: sock, Auth: auth, Lifetime: adapter.LifetimeKeep, Timeout: 3 * time.Second})
		if err == nil {
			t.Fatal("AwaitSupervisor succeeded against a supervisor whose peer check failed")
		}
		if code := p.exitCode(t, 10*time.Second); code == 0 {
			t.Fatal("supervisor exited 0 after a peer mismatch")
		}
	})
}
