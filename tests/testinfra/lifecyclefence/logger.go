// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// testLogWriter writes each slog record through t.Log, so a scenario's
// verbose output carries what the erasure runner and the tenant-deletion
// reconciler log: the drain's elapsed_seconds and any Phase 2 pause
// warning. The tier-8 and tier-7a runs rely on that output to show how long
// each drain took.
//
// A record written after the test has finished is dropped rather than
// passed to t.Log, which panics once its test has completed. A runner
// goroutine that outlives its scenario by a tick would otherwise crash the
// whole test binary.
type testLogWriter struct {
	t    testing.TB
	mu   sync.Mutex
	done bool
}

// Write logs p through t.Log unless the test has finished.
func (w *testLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.done {
		w.t.Helper()
		w.t.Log(strings.TrimRight(string(p), "\n"))
	}
	return len(p), nil
}

// close stops the writer from forwarding to t.Log.
func (w *testLogWriter) close() {
	w.mu.Lock()
	w.done = true
	w.mu.Unlock()
}

// newTestLogger returns a text logger that writes through t.Log for the
// lifetime of t.
// spec: §12.8 (Compliance Interfaces: Lifecycle fence and drain)
func newTestLogger(t testing.TB) *slog.Logger {
	w := &testLogWriter{t: t}
	t.Cleanup(w.close)
	return slog.New(slog.NewTextHandler(w, nil))
}
