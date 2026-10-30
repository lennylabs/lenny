// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/scrub"
)

// TestNewScrubOpsWiresRealScrub_spec_5_2 pins the production wiring of the §5.2
// whole-pod scrub. Before the fix, cmd/lenny-adapter never assigned
// adapterSrv.ScrubOps, so a session-mode recycle ran scrub.Run with nil Ops,
// reported PodScrubFailed, and the default warn policy reused the pod for the
// next session without any scrub having run (a between-session isolation
// regression). newScrubOps now backs the driver with the real DefaultOps. This
// asserts the production build supplies a non-nil scrub.Ops, so a recycle runs
// the real scrub rather than falling into the fail-open report path.
//
// spec: 5.2 (whole-pod scrub), 4.7 (reportpodscrub)
func TestNewScrubOpsWiresRealScrub_spec_5_2(t *testing.T) {
	var ops scrub.Ops = newScrubOps()
	if ops == nil {
		t.Fatal("newScrubOps returned nil; a production recycle would report PodScrubFailed and be reused without a scrub")
	}
	if _, isDefault := ops.(scrub.DefaultOps); !isDefault {
		t.Fatalf("newScrubOps returned %T, want scrub.DefaultOps (the real host operations)", ops)
	}
}

// TestResolveRuntimeUID_spec_4_7 covers the §4.7/§13 SO_PEERCRED peer-UID
// resolution (§4.7.10): the flag wins,
// a zero flag falls back to LENNY_RUNTIME_UID, and an unparseable or
// missing value leaves the check disabled (UID 0).
func TestResolveRuntimeUID_spec_4_7(t *testing.T) {
	tests := []struct {
		name    string
		flagUID uint
		env     string
		setEnv  bool
		want    uint32
	}{
		{name: "flag takes precedence over env", flagUID: 1001, env: "2002", setEnv: true, want: 1001},
		{name: "zero flag falls back to env", flagUID: 0, env: "1001", setEnv: true, want: 1001},
		{name: "zero flag and no env disables check", flagUID: 0, setEnv: false, want: 0},
		{name: "unparseable env disables check", flagUID: 0, env: "not-a-number", setEnv: true, want: 0},
		{name: "empty env disables check", flagUID: 0, env: "", setEnv: true, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setEnv {
				t.Setenv("LENNY_RUNTIME_UID", tc.env)
			} else {
				// Setenv with empty then Unsetenv-like: ensure no inherited value.
				t.Setenv("LENNY_RUNTIME_UID", "")
			}
			if got := resolveRuntimeUID(tc.flagUID); got != tc.want {
				t.Fatalf("resolveRuntimeUID(%d) = %d, want %d", tc.flagUID, got, tc.want)
			}
		})
	}
}

// TestAgentSocketPeerAuth_spec_4_7_11 covers the SO_PEERCRED posture of the
// CH-MSGSOCK and intra-pod MCP listeners: outside nonce-only mode the
// listeners admit only the agent UID and a missing UID is refused when an
// agent socket is bound, and in nonce-only mode the peer check is off.
// spec: 4.7.11 (Separate UIDs and connection authentication), 28.5.3
// (CH-MSGSOCK)
func TestAgentSocketPeerAuth_spec_4_7_11(t *testing.T) {
	tests := []struct {
		name      string
		uid       uint32
		require   bool
		binds     bool
		want      adapter.SocketPeerAuth
		wantError bool
	}{
		{name: "agent UID enforced", uid: 1001, require: true, binds: true, want: adapter.SocketPeerAuth{ExpectedUID: 1001}},
		{name: "missing agent UID refused", uid: 0, require: true, binds: true, wantError: true},
		{name: "no agent socket needs no UID", uid: 0, require: true, binds: false, want: adapter.SocketPeerAuth{}},
		{name: "nonce-only mode without UID", uid: 0, require: false, binds: true, want: adapter.SocketPeerAuth{NonceOnly: true}},
		{name: "nonce-only mode keeps UID", uid: 1001, require: false, binds: true, want: adapter.SocketPeerAuth{ExpectedUID: 1001, NonceOnly: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := agentSocketPeerAuth(tc.uid, tc.require, tc.binds)
			if tc.wantError {
				if err == nil {
					t.Fatalf("agentSocketPeerAuth(%d, %v, %v) = %+v, want an error", tc.uid, tc.require, tc.binds, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("agentSocketPeerAuth(%d, %v, %v) = (%+v, %v), want (%+v, nil)",
					tc.uid, tc.require, tc.binds, got, err, tc.want)
			}
		})
	}
}

// TestEnvIntOr_spec_11_3 covers the helper that backs the §11.3 keepalive
// flag defaults: a present-and-valid env wins, anything else falls back
// to the default. F-11.3.12.
func TestEnvIntOr_spec_11_3(t *testing.T) {
	tests := []struct {
		name   string
		setEnv bool
		val    string
		def    int
		want   int
	}{
		{name: "valid env wins", setEnv: true, val: "12345", def: 10_000, want: 12_345},
		{name: "empty env returns default", setEnv: true, val: "", def: 10_000, want: 10_000},
		{name: "unparseable env returns default", setEnv: true, val: "not-a-number", def: 5_000, want: 5_000},
		{name: "unset env returns default", setEnv: false, def: 5_000, want: 5_000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setEnv {
				t.Setenv("LENNY_KEEPALIVE_TEST", tc.val)
			}
			got := envIntOr("LENNY_KEEPALIVE_TEST", tc.def)
			if got != tc.want {
				t.Errorf("envIntOr() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestCloseRuntimeListenerReleasesAndLogsOnlyOnFailure_spec_4_7_10 pins the
// process-exit release of the pod-scoped runtime socket listener. The closer
// runs exactly once on every exit; a successful close logs nothing, and a
// failed close is logged with its error rather than dropped.
//
// spec: 4.7.10 (Deployment Model), 28.5.3 (Intra-pod)
func TestCloseRuntimeListenerReleasesAndLogsOnlyOnFailure_spec_4_7_10(t *testing.T) {
	closeErr := errors.New("listener already closed")
	tests := []struct {
		name    string
		err     error
		wantLog bool
	}{
		{name: "clean close logs nothing", err: nil, wantLog: false},
		{name: "failed close is logged", err: closeErr, wantLog: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var logged []string
			closeRuntimeListener(
				func() error { calls++; return tc.err },
				func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
			)
			if calls != 1 {
				t.Fatalf("closer called %d times, want 1", calls)
			}
			if !tc.wantLog {
				if len(logged) != 0 {
					t.Fatalf("clean close logged %q, want nothing", logged)
				}
				return
			}
			if len(logged) != 1 || !strings.Contains(logged[0], closeErr.Error()) {
				t.Fatalf("failed close logged %q, want one line carrying %q", logged, closeErr)
			}
		})
	}
}
