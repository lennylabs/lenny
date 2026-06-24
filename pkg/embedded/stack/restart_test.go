// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// spec: §24.19 line 264 — the pod-backed components (gateway, controller,
// ops Deployments) are individually restartable; the removed host-process
// components are not.
func TestRestartableComponents_spec_24_19_264(t *testing.T) {
	for _, name := range []string{"gateway", "controller", "ops"} {
		if !Restartable(name) {
			t.Errorf("Restartable(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"redis", "postgres", "oidc", "k3s", "supervisor", ""} {
		if Restartable(name) {
			t.Errorf("Restartable(%q) = true, want false", name)
		}
	}
}

func TestRunRestartRequiresComponent(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	err := RunRestart(context.Background(), RestartOptions{Component: ""})
	if err == nil || !strings.Contains(err.Error(), "component") {
		t.Errorf("empty component error = %v, want a required-argument error", err)
	}
}

func TestRunRestartRejectsUnknownComponent(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	err := RunRestart(context.Background(), RestartOptions{Component: "redis"})
	if err == nil || !strings.Contains(err.Error(), "cannot be restarted individually") {
		t.Errorf("unknown-component error = %v, want a rejection", err)
	}
}

// spec: §24.19 line 264 — restart against a stack that is not running
// reports ErrNoRunningStack so the CLI can present a precise message.
func TestRunRestartNoStack_spec_24_19_264(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	err := RunRestart(context.Background(), RestartOptions{Component: "gateway"})
	if !errors.Is(err, ErrNoRunningStack) {
		t.Errorf("error = %v, want ErrNoRunningStack", err)
	}
}

// A recorded stack reaches the in-cluster rollout-restart path, which lands
// in a later build step (proposal 0017 C5): RunRestart returns the
// not-yet-wired sentinel rather than touching a removed host-process
// supervisor.
//
// spec: §24.19 line 264.
func TestRunRestartRecordedStackReachesRolloutPath_spec_24_19_264(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LENNY_HOME", home)
	paths := NewPaths(home)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if err := writeState(paths.StateFile(), State{K3sEnabled: true, GatewayForwarderAddr: "127.0.0.1:8443"}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	err := RunRestart(context.Background(), RestartOptions{Component: "gateway"})
	if !errors.Is(err, errRestartNotWired) {
		t.Errorf("error = %v, want the not-yet-wired rollout-restart sentinel", err)
	}
}
