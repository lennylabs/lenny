// SPDX-License-Identifier: MIT

package main

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
)

// parseSupervise parses args through the adapter's supervised flag set,
// starting from their compiled defaults, and validates them beside a bound
// runtime socket.
func parseSupervise(t *testing.T, args ...string) (superviseInputs, error) {
	t.Helper()
	parsed := newSuperviseFlagSet(t, args...)
	return parseSuperviseInputs(parsed.flags, parsed.set, "@lenny-runtime")
}

var supervisedBase = []string{"--supervise-socket", "@lenny-supervise", "--runtime-process", "keep", "--launch-mode", "session"}

func withFlags(extra ...string) []string {
	return append(append([]string{}, supervisedBase...), extra...)
}

// spec: 4.7.10 (Runtime supervisor), 28.5.3 (CH-SUPERVISE, Endpoint; CH-SUPERVISE, Timing)
func TestAdapterInputsTable_spec_4_7_10(t *testing.T) {
	t.Run("--supervise-socket absent: unsupervised", func(t *testing.T) {
		in, err := parseSupervise(t)
		if err != nil || in.Supervised() {
			t.Fatalf("= %+v, %v; want unsupervised", in, err)
		}
	})
	t.Run("--supervise-socket outside the domain", func(t *testing.T) {
		for _, v := range []string{"lenny-supervise", "@", "/run/x.sock"} {
			args := withFlags()
			args[1] = v
			if _, err := parseSupervise(t, args...); err == nil {
				t.Errorf("--supervise-socket=%q accepted", v)
			}
		}
	})
	for _, name := range superviseFlagNames {
		t.Run("--"+name+" without --supervise-socket", func(t *testing.T) {
			v := "keep"
			switch name {
			case "launch-mode":
				v = "session"
			case "supervise-ping-interval", "supervise-ping-timeout", "supervise-wait-timeout", "warm-up-timeout":
				v = "1s"
			}
			if _, err := parseSupervise(t, "--"+name, v); err == nil {
				t.Fatalf("--%s accepted without --supervise-socket", name)
			}
		})
	}
	t.Run("--runtime-process domain", func(t *testing.T) {
		for _, v := range []adapter.RuntimeLifetime{adapter.LifetimeKeep, adapter.LifetimeRestart} {
			in, err := parseSupervise(t, "--supervise-socket", "@s", "--runtime-process", string(v), "--launch-mode", "session")
			if err != nil || in.Lifetime != v {
				t.Errorf("%s = %+v, %v", v, in, err)
			}
		}
		if _, err := parseSupervise(t, "--supervise-socket", "@s", "--launch-mode", "session"); err == nil {
			t.Error("absent --runtime-process accepted")
		}
		if _, err := parseSupervise(t, "--supervise-socket", "@s", "--runtime-process", "always", "--launch-mode", "session"); err == nil {
			t.Error("--runtime-process=always accepted")
		}
	})
	t.Run("--launch-mode domain", func(t *testing.T) {
		for _, v := range []adapter.LaunchMode{adapter.LaunchSession, adapter.LaunchService, adapter.LaunchPreconnectSDKWarm, adapter.LaunchPreconnectPodWarm} {
			in, err := parseSupervise(t, "--supervise-socket", "@s", "--runtime-process", "keep", "--launch-mode", string(v))
			if err != nil || in.Mode != v {
				t.Errorf("%s = %+v, %v", v, in, err)
			}
			_, err = parseSupervise(t, "--supervise-socket", "@s", "--runtime-process", "restart", "--launch-mode", string(v))
			if (err == nil) != (v == adapter.LaunchSession) {
				t.Errorf("restart beside %s: err = %v", v, err)
			}
		}
		if _, err := parseSupervise(t, "--supervise-socket", "@s", "--runtime-process", "keep"); err == nil {
			t.Error("absent --launch-mode accepted")
		}
		if _, err := parseSupervise(t, "--supervise-socket", "@s", "--runtime-process", "keep", "--launch-mode", "eager"); err == nil {
			t.Error("--launch-mode=eager accepted")
		}
	})
	durations := map[string]func(superviseInputs) time.Duration{
		"supervise-ping-interval": func(in superviseInputs) time.Duration { return in.PingInterval },
		"supervise-ping-timeout":  func(in superviseInputs) time.Duration { return in.PingTimeout },
		"supervise-wait-timeout":  func(in superviseInputs) time.Duration { return in.WaitTimeout },
		"warm-up-timeout":         func(in superviseInputs) time.Duration { return in.WarmUpTimeout },
	}
	defaults := map[string]time.Duration{
		"supervise-ping-interval": defaultSupervisePingInterval,
		"supervise-ping-timeout":  defaultSupervisePingTimeout,
		"supervise-wait-timeout":  defaultSuperviseWaitTimeout,
		"warm-up-timeout":         defaultWarmUpTimeout,
	}
	for name, get := range durations {
		t.Run("--"+name, func(t *testing.T) {
			in, err := parseSupervise(t, withFlags()...)
			if err != nil || get(in) != defaults[name] {
				t.Fatalf("absent = %v, %v; want the compiled default %s", get(in), err, defaults[name])
			}
			in, err = parseSupervise(t, withFlags("--"+name, "750ms")...)
			if err != nil || get(in) != 750*time.Millisecond {
				t.Fatalf("750ms = %v, %v", get(in), err)
			}
			for _, bad := range []string{"0s", "-1s"} {
				if _, err := parseSupervise(t, withFlags("--"+name, bad)...); err == nil {
					t.Errorf("--%s=%s accepted", name, bad)
				}
			}
		})
	}
}

// spec: 28.5.3 (CH-SUPERVISE, Endpoint), 13.1 (Container identity), 4.7.11
// (Separate UIDs and connection authentication)
func TestZeroRuntimeUIDRefusedWhenOnlyTheSuperviseSocketIsBound_spec_28_5_3(t *testing.T) {
	in, err := parseSupervise(t, withFlags()...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agentSocketPeerAuth(0, true, in.Supervised()); err == nil {
		t.Fatal("--runtime-uid=0 with --supervise-socket under --require-so-peercred=true was accepted")
	}
	if _, err := agentSocketPeerAuth(0, false, in.Supervised()); err != nil {
		t.Fatalf("nonce-only mode refused a zero UID: %v", err)
	}
}
