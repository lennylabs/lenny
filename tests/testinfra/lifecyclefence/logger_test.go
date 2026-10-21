// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// captureTB is a testing.TB whose Log calls are recorded, so a test can read
// what the scenario world wrote through t.Log.
type captureTB struct {
	*testing.T
	mu    sync.Mutex
	lines []string
}

func (c *captureTB) Log(args ...any) {
	c.mu.Lock()
	c.lines = append(c.lines, fmt.Sprint(args...))
	c.mu.Unlock()
}

func (c *captureTB) Helper() {}

// logged reports whether any recorded line contains every one of subs.
func (c *captureTB) logged(subs ...string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, l := range c.lines {
		match := true
		for _, s := range subs {
			match = match && strings.Contains(l, s)
		}
		if match {
			return true
		}
	}
	return false
}

// TestErasureRunnerLogsDrainTimeThroughTestLog_spec_12_8 runs an erasure to
// completion in a world built over a capturing TB and requires the runner's
// drain record, with its elapsed_seconds, to reach the test log. A world
// whose runners log to a discarded handler leaves the scenario output
// without the drain time.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 12.8 (Processing restriction during erasure (GDPR Article 18))
func TestErasureRunnerLogsDrainTimeThroughTestLog_spec_12_8(t *testing.T) {
	tb := &captureTB{T: t}
	w := NewWorld(tb, MemoryStores(), 1)
	e := w.NewErasure()
	ctx := context.Background()
	job, err := e.Start(ctx, "alice")
	if err != nil {
		t.Fatalf("start erasure: %v", err)
	}
	if _, err := e.RunToCompletion(ctx, job.ID, 6); err != nil {
		t.Fatal(err)
	}
	if !tb.logged("erasure job session drain finished", "elapsed_seconds=") {
		t.Fatalf("the erasure drain's elapsed time never reached the test log: %v", tb.lines)
	}
}

// TestDeletionReconcilerLogsPhase2PauseThroughTestLog_spec_12_8 fails one
// Phase 2 drain listing and requires the reconciler's pause warning to
// reach the test log.
//
// spec: 12.8 (Tenant deletion lifecycle), 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestDeletionReconcilerLogsPhase2PauseThroughTestLog_spec_12_8(t *testing.T) {
	tb := &captureTB{T: t}
	w := NewWorld(tb, MemoryStores(), 1)
	d := w.NewDeletion()
	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Tick(ctx); err != nil {
		t.Fatalf("Phase 1 tick: %v", err)
	}
	w.Faults.FailDrainListings(1)
	if _, err := d.Tick(ctx); err != nil {
		t.Fatalf("faulted Phase 2 tick: %v", err)
	}
	if !tb.logged("tenant deletion paused at Phase 2") {
		t.Fatalf("the Phase 2 pause warning never reached the test log: %v", tb.lines)
	}
}

// TestDeletionPhase2LogsDrainTimeThroughTestLog_spec_12_8 runs one
// successful and one faulted Phase 2 drain and requires each drain's
// record, with its tenant, elapsed_seconds, and terminated count, to reach
// the test log, and the faulted one to carry its error. A Phase 2 drain
// that records no timing leaves the tier-8 deletion runs without the drain
// time that bounds the tenant-deletion tick delay.
//
// spec: 12.8 (Tenant deletion lifecycle), 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestDeletionPhase2LogsDrainTimeThroughTestLog_spec_12_8(t *testing.T) {
	tb := &captureTB{T: t}
	w := NewWorld(tb, MemoryStores(), 1)
	d := w.NewDeletion()
	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Tick(ctx); err != nil {
		t.Fatalf("Phase 1 tick: %v", err)
	}
	w.Faults.FailDrainListings(1)
	if _, err := d.Tick(ctx); err != nil {
		t.Fatalf("faulted Phase 2 tick: %v", err)
	}
	if !tb.logged("tenant deletion phase 2 drain finished", "tenant_id="+w.Tenant, "elapsed_seconds=", "terminated=0", `error="`+faultedDrainError(t, w)) {
		t.Fatalf("the faulted Phase 2 drain's record never reached the test log with its error: %v", tb.lines)
	}
	if _, err := d.ThroughPhase2(ctx, 4); err != nil {
		t.Fatalf("Phase 2 after the fault: %v", err)
	}
	if !tb.logged("tenant deletion phase 2 drain finished", "tenant_id="+w.Tenant, "elapsed_seconds=", `error=""`) {
		t.Fatalf("the successful Phase 2 drain's elapsed time never reached the test log: %v", tb.lines)
	}
}

// faultedDrainError returns the leading text of the error a faulted drain
// listing returns, as the drain record quotes it.
func faultedDrainError(t *testing.T, w *World) string {
	t.Helper()
	w.Faults.FailDrainListings(1)
	_, err := w.Drain(context.Background(), "")
	if err == nil {
		t.Fatal("a faulted drain listing returned no error")
	}
	return err.Error()
}

// TestTestLogWriterDropsRecordsAfterTheTestEnds_spec_12_8 requires a record
// written after the writer closes to be dropped rather than passed to
// t.Log, which panics once its test has completed.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestTestLogWriterDropsRecordsAfterTheTestEnds_spec_12_8(t *testing.T) {
	tb := &captureTB{T: t}
	w := &testLogWriter{t: tb}
	if _, err := w.Write([]byte("before\n")); err != nil {
		t.Fatal(err)
	}
	w.close()
	if n, err := w.Write([]byte("after\n")); err != nil || n != len("after\n") {
		t.Fatalf("write after close = %d, %v", n, err)
	}
	if len(tb.lines) != 1 || tb.lines[0] != "before" {
		t.Fatalf("logged lines = %q, want only %q", tb.lines, "before")
	}
}
