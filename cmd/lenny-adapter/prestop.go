// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"flag"
	"syscall"
	"time"
)

// preStopProcessID is the adapter process the preStop drain signals.
// shareProcessNamespace is left unset on agent pods (§13.1), so within
// the adapter container the adapter is PID 1 and the preStop exec runs
// in the same PID namespace.
const preStopProcessID = 1

// preStopConfig holds the parsed `lenny-adapter prestop` options.
type preStopConfig struct {
	// timeout bounds the drain wait. The podspec sets it to the pod's
	// terminationGracePeriodSeconds minus a reaping margin so the
	// kubelet never SIGKILLs the adapter mid-drain.
	timeout time.Duration
	// pollInterval is how often the drain re-checks that the adapter
	// process is still alive.
	pollInterval time.Duration
}

// preStopDeps are the OS interactions the drain performs. They are
// injected so runPreStop is unit-testable without a real process.
type preStopDeps struct {
	signal func() error        // signal the adapter to terminate (SIGTERM)
	alive  func() bool         // report whether the adapter is still running
	sleep  func(time.Duration) // block for one poll interval
	logf   func(string, ...any)
}

// parsePreStopArgs parses the prestop subcommand's flags. args excludes
// the "prestop" verb itself.
func parsePreStopArgs(args []string) (preStopConfig, error) {
	fs := flag.NewFlagSet("prestop", flag.ContinueOnError)
	timeout := fs.Duration("timeout", 110*time.Second,
		"maximum time to wait for the adapter to drain before returning")
	poll := fs.Duration("poll-interval", 200*time.Millisecond,
		"interval between adapter liveness checks")
	if err := fs.Parse(args); err != nil {
		return preStopConfig{}, err
	}
	if *timeout <= 0 {
		return preStopConfig{}, errors.New("prestop: --timeout must be positive")
	}
	if *poll <= 0 {
		return preStopConfig{}, errors.New("prestop: --poll-interval must be positive")
	}
	return preStopConfig{timeout: *timeout, pollInterval: *poll}, nil
}

// runPreStop executes the §4.6.1 preStop drain: it signals the running
// adapter to terminate, then blocks until the adapter exits or the
// timeout elapses. Front-loading the drain into the preStop window lets
// an in-flight gateway Checkpoint RPC complete within the grace period
// instead of being cut short by the kubelet termination clock.
//
// It always returns 0. The kubelet SIGKILL at the grace deadline is the
// backstop; a non-zero preStop exit only emits noisy FailedPreStopHook
// events without changing the termination outcome. spec: §4.6.1
// "Disruption protection for agent pods".
func runPreStop(cfg preStopConfig, deps preStopDeps) int {
	if !deps.alive() {
		// The adapter already exited (e.g. it processed an earlier
		// SIGTERM). Nothing to drain.
		return 0
	}
	if err := deps.signal(); err != nil {
		// ESRCH means the adapter exited between the alive check and the
		// signal; any other error is logged but still non-fatal.
		if !errors.Is(err, syscall.ESRCH) {
			deps.logf("lenny-adapter: prestop: signal adapter: %v", err)
		}
		return 0
	}
	deadline := cfg.timeout
	var waited time.Duration
	for waited < deadline {
		if !deps.alive() {
			return 0
		}
		deps.sleep(cfg.pollInterval)
		waited += cfg.pollInterval
	}
	deps.logf("lenny-adapter: prestop: adapter still draining after %s; "+
		"deferring to kubelet SIGKILL", cfg.timeout)
	return 0
}

// preStopOSDeps returns the production preStopDeps that signal and poll
// the adapter via the process API.
func preStopOSDeps(logf func(string, ...any)) preStopDeps {
	return preStopDeps{
		signal: func() error { return syscall.Kill(preStopProcessID, syscall.SIGTERM) },
		alive:  func() bool { return syscall.Kill(preStopProcessID, 0) == nil },
		sleep:  time.Sleep,
		logf:   logf,
	}
}
