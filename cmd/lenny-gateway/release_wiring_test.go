// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// assertWired fails the test for every seam whose wiring text is missing
// from its file, comparing with whitespace collapsed so formatting changes
// do not break the pin.
func assertWired(t *testing.T, seams []struct{ file, want, seam string }) {
	t.Helper()
	space := regexp.MustCompile(`\s+`)
	for _, tc := range seams {
		b, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatalf("read %s: %v", tc.file, err)
		}
		if !strings.Contains(space.ReplaceAllString(string(b), " "), tc.want) {
			t.Errorf("%s: %s is not wired (%q missing)", tc.file, tc.seam, tc.want)
		}
	}
}

// TestSlotSetRecoveryProbeRunsOnTheWatchdogContext_spec_12_4 pins the
// gateway's wiring of the slot-set recovery: the slot counter is built with
// the coordination-client probe and the operator-tunable probe interval, it
// is published on the wiring, and the reconciler workers start its recovery
// loop on the watchdog context. Counter.RunRecovery is tested on its own; a
// gateway that never starts it leaves a replica with no slot traffic unable
// to rebuild the slot sets when a Redis outage ends, which only this pin
// catches.
//
// spec: §12.4 (Redis HA and Failure Modes), §5.2 (Slot assignment atomicity)
func TestSlotSetRecoveryProbeRunsOnTheWatchdogContext_spec_12_4(t *testing.T) {
	assertWired(t, []struct{ file, want, seam string }{
		{"pod_lifecycle.go", "slotcounter.WithRecoveryProbeInterval(*slotCounterRecoveryProbeInterval),", "the recovery probe interval"},
		{"pod_lifecycle.go", "slotcounter.WithRecoveryProbe(func(context.Context) error { return redisconn.PingWithTimeout(slotRedis, slotCounterProbeTimeout) })", "the recovery probe"},
		{"pod_lifecycle.go", "w.slotCounter = slotCounter", "the slot counter's publication on the wiring"},
		{"reconciler_workers.go", "if w.slotCounter != nil { go w.slotCounter.RunRecovery(w.watchdogCtx) }", "the recovery loop on the watchdog context"},
	})
}

// TestFullRevokeFanOutReleasesThroughTheSessionServer_spec_11_4 pins the
// admin router's full_revoke fan-out wiring: each binding is released
// through the session server's terminal binding release, and the lease
// revoker returns each lease to its pool through the credential assigner
// and propagates it over the renewal propagator. The fan-out and the
// revoker are unit-tested with injected fakes, so a production wiring that
// drops the releaser or passes no assigner is caught only here.
//
// spec: §11.4 (User Invalidation), §4.9 (Credential Leasing)
func TestFullRevokeFanOutReleasesThroughTheSessionServer_spec_11_4(t *testing.T) {
	assertWired(t, []struct{ file, want, seam string }{
		{"adminrouter.go", "fanOut := &podTerminateFanOut{registry: w.podRegistry, releaser: sessionSrv}", "the binding release through the session server"},
		{"adminrouter.go", "userPods = fanOut", "the pod terminator handed to the admin router"},
		{"adminrouter.go", "userLeases = leaserevoke.New(ls, w.credAssign, credRenewalProp, nil)", "the lease revoker's credential assigner"},
	})
}
