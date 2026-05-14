// SPDX-License-Identifier: MIT

// Package goleak implements the §17.4 goroutine-leak check.
//
// Every test that spawns goroutines wraps its body with VerifyNone
// at the top: the deferred check compares the goroutine count at
// test exit to the count at test entry and fails the test if any new
// goroutine survived.
//
// We do not depend on the upstream go.uber.org/goleak package: the
// project policy is to keep test-time dependencies thin. The check
// here is a tighter, project-local version that reads
// runtime.Stack and ignores known noisy frames (the test runner's
// own goroutines, HTTP/2's idle background server, etc.).
//
// Usage:
//
//	func TestThing(t *testing.T) {
//	    defer goleak.VerifyNone(t)
//	    // ... body that spawns goroutines and is expected to clean up
//	}
package goleak

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"time"
)

// VerifyNone records the current goroutine count and registers a
// deferred check that fails the test if the count grew. It allows
// a short grace period for spawned goroutines to wind down before
// the failure trips.
func VerifyNone(t testing.TB) {
	t.Helper()
	baseline := snapshot()
	check := func() {
		// Tight loop with a short deadline. Most leaks are
		// background goroutines that never exit; a 250 ms window
		// gives clean shutdown enough time.
		deadline := time.Now().Add(250 * time.Millisecond)
		for time.Now().Before(deadline) {
			current := snapshot()
			if leaks := diff(baseline, current); len(leaks) == 0 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		// Final reading.
		current := snapshot()
		leaks := diff(baseline, current)
		if len(leaks) == 0 {
			return
		}
		t.Errorf("goleak: %d goroutine(s) leaked:\n%s", len(leaks), strings.Join(leaks, "\n"))
	}
	t.Cleanup(check)
}

// snapshot returns one stack-trace string per live goroutine, with
// known-noise frames filtered out.
func snapshot() []string {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	raw := string(buf[:n])
	// Split into per-goroutine blocks delimited by an empty line.
	blocks := strings.Split(raw, "\n\n")
	out := make([]string, 0, len(blocks))
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		if isIgnored(block) {
			continue
		}
		out = append(out, block)
	}
	return out
}

// diff returns goroutines present in current that are absent from
// baseline (by full-stack equality).
func diff(baseline, current []string) []string {
	in := make(map[string]bool, len(baseline))
	for _, s := range baseline {
		in[s] = true
	}
	out := []string{}
	for _, s := range current {
		if !in[s] {
			out = append(out, s)
		}
	}
	return out
}

// isIgnored filters known-quiet noise frames so a leak report is not
// drowned by the test runner's own goroutines.
func isIgnored(stack string) bool {
	for _, marker := range ignoredMarkers {
		if strings.Contains(stack, marker) {
			return true
		}
	}
	return false
}

var ignoredMarkers = []string{
	"testing.(*M).Run",
	"testing.tRunner",
	"runtime.goexit",
	"runtime/trace.Start",
	"created by net/http.(*Server).Serve",
	"net/http.(*persistConn).readLoop",
	"net/http.(*persistConn).writeLoop",
	"created by go.uber.org/goleak",
}

// stackPrefix is the conventional prefix on a runtime.Stack block.
// We don't actually consume it — kept exported for callers that want
// to render the snapshot themselves.
var stackPrefix = []byte("goroutine ")

func init() {
	// Defensive: keep stackPrefix reachable in case the snapshot
	// helpers grow to use it.
	_ = bytes.Equal(stackPrefix, stackPrefix)
}
