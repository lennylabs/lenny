// SPDX-License-Identifier: MIT

package stack

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteReadState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stack.json")
	want := State{
		StartedAt:            time.Now().UTC().Truncate(time.Second),
		K3sContainer:         "lenny-embedded-k3s-k3s",
		GatewayForwarderAddr: "127.0.0.1:8443",
		DeployedImageTag:     "v0.0.0-dev",
		KubeconfigPath:       "/state/k3s/kubeconfig.yaml",
		K3sEnabled:           true,
	}
	if err := writeState(path, want); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	got, ok, err := readState(path)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if !ok {
		t.Fatal("readState reported no state file")
	}
	if got != want {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestReadStateMissingFile(t *testing.T) {
	_, ok, err := readState(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("readState of a missing file should not error: %v", err)
	}
	if ok {
		t.Error("readState reported ok for a missing file")
	}
}

func TestReadStateCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stack.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}
	if _, _, err := readState(path); err == nil {
		t.Error("expected readState to error on a corrupt file")
	}
}

func TestRemoveState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stack.json")
	if err := writeState(path, State{K3sEnabled: true}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	if err := removeState(path); err != nil {
		t.Fatalf("removeState: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("state file still present after removeState")
	}
	// removeState is idempotent: removing an absent file is not an
	// error.
	if err := removeState(path); err != nil {
		t.Errorf("second removeState errored: %v", err)
	}
}

// spec: §24.19.1 (the image bridge selects its containerd-reach path from
// the running substrate), §17.4 (the substrate is provisioned per host
// operating system) — RunningSubstrate reports a Docker-backed substrate
// when the state records a k3s container handle, a host substrate when it
// does not, and ErrNoRunningStack when no stack is recorded.
func TestRunningSubstrate(t *testing.T) {
	t.Run("no running stack", func(t *testing.T) {
		root := t.TempDir()
		if _, err := RunningSubstrate(root); !errors.Is(err, ErrNoRunningStack) {
			t.Fatalf("RunningSubstrate without a stack = %v, want ErrNoRunningStack", err)
		}
	})

	t.Run("docker-backed substrate", func(t *testing.T) {
		root := t.TempDir()
		writeSubstrateState(t, root, "lenny-embedded-k3s-x")
		sub, err := RunningSubstrate(root)
		if err != nil {
			t.Fatalf("RunningSubstrate: %v", err)
		}
		if !sub.DockerBacked() {
			t.Errorf("DockerBacked() = false, want true for a recorded container handle")
		}
		if sub.Container != "lenny-embedded-k3s-x" {
			t.Errorf("Container = %q, want lenny-embedded-k3s-x", sub.Container)
		}
	})

	t.Run("host substrate", func(t *testing.T) {
		root := t.TempDir()
		writeSubstrateState(t, root, "")
		sub, err := RunningSubstrate(root)
		if err != nil {
			t.Fatalf("RunningSubstrate: %v", err)
		}
		if sub.DockerBacked() {
			t.Errorf("DockerBacked() = true, want false for a host child-process substrate")
		}
	})
}

// writeSubstrateState records a stack state file under root with the given
// k3s container handle so RunningSubstrate has a state to read.
func writeSubstrateState(t *testing.T, root, container string) {
	t.Helper()
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if err := writeState(paths.StateFile(), State{K3sEnabled: true, K3sContainer: container}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
}

// writeRunningState records a running stack state file under root with the
// given forwarder address and kubeconfig path so RunningGateway and
// RunningKubeconfig have a state to read.
func writeRunningState(t *testing.T, root, forwarderAddr, kubeconfigPath string) {
	t.Helper()
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st := State{
		K3sEnabled:           true,
		GatewayForwarderAddr: forwarderAddr,
		KubeconfigPath:       kubeconfigPath,
	}
	if err := writeState(paths.StateFile(), st); err != nil {
		t.Fatalf("writeState: %v", err)
	}
}

// spec: §17.4 (the CLI reaches the in-cluster gateway through the loopback-only
// host-side forwarder) — RunningGateway returns the recorded forwarder HTTPS
// URL when a stack is running, ErrNoRunningStack when none is recorded, and a
// precise error when the running state carries no forwarder address.
func TestRunningGateway(t *testing.T) {
	t.Run("no running stack", func(t *testing.T) {
		root := t.TempDir()
		if _, err := RunningGateway(root); !errors.Is(err, ErrNoRunningStack) {
			t.Fatalf("RunningGateway without a stack = %v, want ErrNoRunningStack", err)
		}
	})

	t.Run("stopped stack reports no running stack", func(t *testing.T) {
		root := t.TempDir()
		paths := NewPaths(root)
		if err := paths.EnsureDirs(); err != nil {
			t.Fatalf("EnsureDirs: %v", err)
		}
		// A stopped (non-`--purge` lenny down) state must read as not running.
		st := State{K3sEnabled: true, GatewayForwarderAddr: "127.0.0.1:8443", Stopped: true}
		if err := writeState(paths.StateFile(), st); err != nil {
			t.Fatalf("writeState: %v", err)
		}
		if _, err := RunningGateway(root); !errors.Is(err, ErrNoRunningStack) {
			t.Fatalf("RunningGateway on a stopped stack = %v, want ErrNoRunningStack", err)
		}
	})

	t.Run("running stack returns the forwarder URL", func(t *testing.T) {
		root := t.TempDir()
		writeRunningState(t, root, "127.0.0.1:8443", "/state/k3s/kubeconfig.yaml")
		got, err := RunningGateway(root)
		if err != nil {
			t.Fatalf("RunningGateway: %v", err)
		}
		if want := "https://127.0.0.1:8443"; got != want {
			t.Errorf("RunningGateway = %q, want %q", got, want)
		}
	})

	t.Run("running stack with no forwarder address errors", func(t *testing.T) {
		root := t.TempDir()
		writeRunningState(t, root, "", "/state/k3s/kubeconfig.yaml")
		if _, err := RunningGateway(root); err == nil {
			t.Fatal("RunningGateway with no forwarder address = nil, want a precise error")
		}
	})
}

// spec: §17.4 (the in-cluster control plane is reached through the embedded
// kubeconfig) — RunningKubeconfig returns the recorded admin kubeconfig path
// when a stack is running, ErrNoRunningStack when none is recorded, and a
// precise error when the running state carries no kubeconfig path.
func TestRunningKubeconfig(t *testing.T) {
	t.Run("no running stack", func(t *testing.T) {
		root := t.TempDir()
		if _, err := RunningKubeconfig(root); !errors.Is(err, ErrNoRunningStack) {
			t.Fatalf("RunningKubeconfig without a stack = %v, want ErrNoRunningStack", err)
		}
	})

	t.Run("running stack returns the kubeconfig path", func(t *testing.T) {
		root := t.TempDir()
		const kc = "/state/k3s/kubeconfig.yaml"
		writeRunningState(t, root, "127.0.0.1:8443", kc)
		got, err := RunningKubeconfig(root)
		if err != nil {
			t.Fatalf("RunningKubeconfig: %v", err)
		}
		if got != kc {
			t.Errorf("RunningKubeconfig = %q, want %q", got, kc)
		}
	})

	t.Run("running stack with no kubeconfig path errors", func(t *testing.T) {
		root := t.TempDir()
		writeRunningState(t, root, "127.0.0.1:8443", "")
		if _, err := RunningKubeconfig(root); err == nil {
			t.Fatal("RunningKubeconfig with no kubeconfig path = nil, want a precise error")
		}
	})
}
