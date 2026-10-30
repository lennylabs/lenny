// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"os"
	"strings"
	"testing"
)

// TestSuspensionFlags_spec_6_2 pins the suspension flags: each parses with
// its default and its environment variable, the renamed idle flag parses,
// its former name is rejected, and the playground session cap's help names
// active time.
//
// spec: 6.2 (maxClientIdleSeconds clock behavior across states), 27.6
// (Session lifecycle and cleanup)
func TestSuspensionFlags_spec_6_2(t *testing.T) {
	register := func(t *testing.T, args ...string) (*gatewayFlags, error) {
		t.Helper()
		savedCmdLine, savedArgs := flag.CommandLine, os.Args
		t.Cleanup(func() { flag.CommandLine, os.Args = savedCmdLine, savedArgs })
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
		flag.CommandLine.SetOutput(&strings.Builder{})
		os.Args = []string{savedArgs[0]}
		f := &gatewayFlags{}
		f.registerArtifactFlags()
		f.registerOpsFlags()
		return f, flag.CommandLine.Parse(args)
	}
	type intFlag struct {
		env  string
		ptr  func(*gatewayFlags) *int
		def  int
		name string
	}
	flags := []intFlag{
		{"LENNY_MAX_CLIENT_IDLE_SECONDS", func(f *gatewayFlags) *int { return f.maxClientIdleSeconds }, 900, "max-client-idle-seconds"},
		{"LENNY_MAX_SUSPENDED_POD_HOLD_SECONDS", func(f *gatewayFlags) *int { return f.maxSuspendedPodHoldSeconds }, 900, "max-suspended-pod-hold-seconds"},
		{"LENNY_MAX_SUSPENDED_SESSION_SECONDS", func(f *gatewayFlags) *int { return f.maxSuspendedSessionSeconds }, 604800, "max-suspended-session-seconds"},
		{"LENNY_DELEGATION_BUDGET_KEY_TTL_SECONDS", func(f *gatewayFlags) *int { return f.delegationBudgetKeyTTLSeconds }, 691200, "delegation-budget-key-ttl-seconds"},
		{"LENNY_SUSPENDED_BACKLOG_RECHECK_SECONDS", func(f *gatewayFlags) *int { return f.suspendedBacklogRecheckSeconds }, 300, "suspended-backlog-recheck-seconds"},
	}
	for _, fl := range flags {
		t.Run(fl.name, func(t *testing.T) {
			t.Setenv(fl.env, "")
			f, err := register(t)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := *fl.ptr(f); got != fl.def {
				t.Errorf("default = %d, want %d", got, fl.def)
			}
			t.Setenv(fl.env, "1234")
			if f, _ = register(t); *fl.ptr(f) != 1234 {
				t.Errorf("value = %d, want 1234 from %s", *fl.ptr(f), fl.env)
			}
			t.Setenv(fl.env, "")
			if f, err = register(t, "--"+fl.name+"=4321"); err != nil || *fl.ptr(f) != 4321 {
				t.Errorf("--%s=4321 parsed %d, %v", fl.name, *fl.ptr(f), err)
			}
		})
	}
	t.Run("former_idle_flag_rejected", func(t *testing.T) {
		if _, err := register(t, "--max-idle-time-seconds=600"); err == nil {
			t.Fatal("--max-idle-time-seconds parsed; the renamed flag keeps no alias")
		}
	})
	// The hold window has a single deploy-wide source: the watchdog compares
	// against Config.MaxSuspendedPodHoldSeconds alone, so the help must not
	// promise a per-tenant cap that no code path applies.
	t.Run("pod_hold_help_names_interrupt_release_and_single_source", func(t *testing.T) {
		if _, err := register(t); err != nil {
			t.Fatal(err)
		}
		usage := flag.CommandLine.Lookup("max-suspended-pod-hold-seconds").Usage
		for _, want := range []string{"interrupt-suspended", "releases the pod", "stays `suspended`", "only hold source"} {
			if !strings.Contains(usage, want) {
				t.Errorf("usage = %q, want it to contain %q", usage, want)
			}
		}
		if strings.Contains(strings.ToLower(usage), "per-tenant cap apply") ||
			strings.Contains(usage, "more restrictive value wins") {
			t.Errorf("usage = %q claims a per-tenant hold cap the watchdog does not apply", usage)
		}
	})
	t.Run("playground_session_cap_help_names_active_time", func(t *testing.T) {
		if _, err := register(t); err != nil {
			t.Fatal(err)
		}
		usage := flag.CommandLine.Lookup("playground-max-session-minutes").Usage
		if !strings.Contains(usage, "active time") || !strings.Contains(usage, "suspended") {
			t.Errorf("usage = %q, want the active-time cap that pauses while suspended", usage)
		}
	})
}
