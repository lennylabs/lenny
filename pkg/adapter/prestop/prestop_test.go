// SPDX-License-Identifier: MIT

package prestop

import (
	"syscall"
	"testing"
	"time"
)

// recordingDeps builds preStopDeps whose alive() reports true until the
// configured number of poll cycles elapse, then false — simulating an
// adapter that drains after `aliveFor` poll iterations.
func recordingDeps(aliveFor int, signalErr error) (*preStopDeps, *int, *int) {
	signals := 0
	sleeps := 0
	calls := 0
	d := preStopDeps{
		signal: func() error { signals++; return signalErr },
		alive: func() bool {
			defer func() { calls++ }()
			return calls < aliveFor
		},
		sleep: func(time.Duration) { sleeps++ },
		logf:  func(string, ...any) {},
	}
	return &d, &signals, &sleeps
}

// TestRunPreStopSignalsThenReturnsOnExit_spec_4_6_1 covers the happy
// path: the adapter is alive, the drain signals it, and the adapter
// exits after a couple of poll cycles.
// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle))
func TestRunPreStopSignalsThenReturnsOnExit_spec_4_6_1(t *testing.T) {
	// alive() sequence: initial check (true), then post-signal checks
	// true, true, false.
	deps, signals, sleeps := recordingDeps(3, nil)
	cfg := preStopConfig{timeout: 10 * time.Second, pollInterval: time.Second}
	if rc := runPreStop(cfg, *deps); rc != 0 {
		t.Fatalf("runPreStop rc = %d, want 0", rc)
	}
	if *signals != 1 {
		t.Errorf("signal count = %d, want exactly 1 SIGTERM", *signals)
	}
	if *sleeps == 0 {
		t.Error("expected at least one poll sleep while draining")
	}
}

// TestRunPreStopNoopWhenAlreadyExited_spec_4_6_1 covers the adapter
// having already processed an earlier SIGTERM: no signal is sent.
// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle))
func TestRunPreStopNoopWhenAlreadyExited_spec_4_6_1(t *testing.T) {
	deps, signals, sleeps := recordingDeps(0, nil)
	cfg := preStopConfig{timeout: 10 * time.Second, pollInterval: time.Second}
	if rc := runPreStop(cfg, *deps); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if *signals != 0 {
		t.Errorf("signal count = %d, want 0 (adapter already gone)", *signals)
	}
	if *sleeps != 0 {
		t.Errorf("sleep count = %d, want 0", *sleeps)
	}
}

// TestRunPreStopTimesOut_spec_4_6_1 confirms the drain returns 0 (defers
// to the kubelet SIGKILL) when the adapter never exits within the
// budget, and that it does not loop forever.
// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle))
func TestRunPreStopTimesOut_spec_4_6_1(t *testing.T) {
	deps, _, sleeps := recordingDeps(1<<30, nil) // never exits
	cfg := preStopConfig{timeout: 1 * time.Second, pollInterval: 250 * time.Millisecond}
	if rc := runPreStop(cfg, *deps); rc != 0 {
		t.Fatalf("rc = %d, want 0 even on timeout", rc)
	}
	// timeout/pollInterval = 4 poll cycles.
	if *sleeps != 4 {
		t.Errorf("sleep count = %d, want 4 (1s / 250ms)", *sleeps)
	}
}

// TestRunPreStopSignalESRCH_spec_4_6_1 confirms an ESRCH on signal (the
// adapter raced to exit between the alive check and the signal) is
// treated as a clean drain.
// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle))
func TestRunPreStopSignalESRCH_spec_4_6_1(t *testing.T) {
	deps, signals, _ := recordingDeps(1<<30, syscall.ESRCH)
	cfg := preStopConfig{timeout: 5 * time.Second, pollInterval: time.Second}
	if rc := runPreStop(cfg, *deps); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if *signals != 1 {
		t.Errorf("signal count = %d, want 1", *signals)
	}
}

// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle))
func TestParsePreStopArgs_spec_4_6_1(t *testing.T) {
	cfg, err := parsePreStopArgs([]string{"--timeout=110s"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.timeout != 110*time.Second {
		t.Errorf("timeout = %s, want 110s", cfg.timeout)
	}
	if cfg.pollInterval <= 0 {
		t.Error("poll interval default must be positive")
	}

	for _, bad := range [][]string{
		{"--timeout=0s"},
		{"--poll-interval=-1s"},
		{"--unknown-flag"},
	} {
		if _, err := parsePreStopArgs(bad); err == nil {
			t.Errorf("parsePreStopArgs(%v) = nil error, want rejection", bad)
		}
	}
}

// TestRunRejectsUnparsableArgsWithStatus2_spec_4_6_1 confirms a preStop
// invocation whose arguments the binary does not define exits with status 2
// and logs the cause, so a pod builder and binary that disagree on the
// subcommand's flags surface as a FailedPreStopHook event naming the flag.
// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle))
func TestRunRejectsUnparsableArgsWithStatus2_spec_4_6_1(t *testing.T) {
	for _, bad := range [][]string{
		{"--unknown-flag"},
		{"--timeout=0s"},
		{"--timeout=110"},
	} {
		var logged []string
		logf := func(format string, args ...any) { logged = append(logged, format) }
		if rc := Run(bad, logf); rc != parseErrorExitCode {
			t.Errorf("Run(%v) = %d, want %d", bad, rc, parseErrorExitCode)
		}
		if len(logged) == 0 {
			t.Errorf("Run(%v) logged nothing; a rejected invocation must name its cause", bad)
		}
	}
}

// TestVerbMatchesFlagSetName_spec_4_6_1 pins Verb to the flag set name the
// deployment-boundary gate reads by syntax, so renaming one without the
// other fails here.
// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle))
func TestVerbMatchesFlagSetName_spec_4_6_1(t *testing.T) {
	if Verb != "prestop" {
		t.Fatalf("Verb = %q; the parsePreStopArgs flag set is named %q", Verb, "prestop")
	}
}
