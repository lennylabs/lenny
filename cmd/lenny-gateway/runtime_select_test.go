// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/executor"
)

// spec: §17.4 line 262 — LENNY_AGENT_RUNTIME=echo selects the built-in
// in-process echo runtime (zero-credential mode), and it wins over a
// configured runtime binary.
func TestResolveExecutorEchoSelectorWins_spec_17_4_262(t *testing.T) {
	exec, desc, err := resolveExecutor("/path/to/agent", "echo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := exec.(*executor.EchoExecutor); !ok {
		t.Fatalf("want *EchoExecutor, got %T", exec)
	}
	if !strings.Contains(desc, "LENNY_AGENT_RUNTIME=echo") {
		t.Errorf("description should name the selector: %q", desc)
	}

	// Case-insensitive: the env var value is matched without regard to
	// case so "Echo"/"ECHO" still selects the built-in runtime.
	if exec, _, err := resolveExecutor("", "ECHO"); err != nil {
		t.Errorf("ECHO: unexpected error: %v", err)
	} else if _, ok := exec.(*executor.EchoExecutor); !ok {
		t.Errorf("ECHO: want *EchoExecutor, got %T", exec)
	}
}

// spec: §17.4 line 323 — LENNY_AGENT_BINARY / --runtime-bin dispatches
// to a child process speaking the §15.4.1 adapter protocol.
func TestResolveExecutorRuntimeBinary_spec_17_4_323(t *testing.T) {
	exec, desc, err := resolveExecutor("/path/to/agent", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := exec.(*executor.SubprocessExecutor); !ok {
		t.Fatalf("want *SubprocessExecutor, got %T", exec)
	}
	if !strings.Contains(desc, "/path/to/agent") {
		t.Errorf("description should name the binary: %q", desc)
	}
}

// spec: §17.4 line 262 — Source Mode's default runtime is the built-in
// echo executor when neither selector is set.
func TestResolveExecutorDefaultEcho_spec_17_4_262(t *testing.T) {
	exec, _, err := resolveExecutor("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := exec.(*executor.EchoExecutor); !ok {
		t.Fatalf("want *EchoExecutor, got %T", exec)
	}
}

// spec: §17.4 line 262 — "echo" is the only built-in dev runtime name;
// a typo or an unsupported value fails closed at startup rather than
// silently falling back.
func TestResolveExecutorUnknownRuntimeFailsClosed_spec_17_4_262(t *testing.T) {
	exec, _, err := resolveExecutor("", "claude-code")
	if err == nil {
		t.Fatal("want error for unknown LENNY_AGENT_RUNTIME, got nil")
	}
	if exec != nil {
		t.Errorf("executor should be nil on error, got %T", exec)
	}
	if !strings.Contains(err.Error(), "claude-code") || !strings.Contains(err.Error(), "echo") {
		t.Errorf("error should name the bad value and the supported one: %v", err)
	}
}
