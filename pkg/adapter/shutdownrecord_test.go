// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// closeRuntime is a probe runtime whose Close can block on a gate and fail
// with a fixed error, so a case can hold a removing Shutdown mid-teardown or
// make it report an unclean exit.
type closeRuntime struct {
	probeRuntime
	mu      sync.Mutex
	err     error
	entered chan struct{}
	gate    chan struct{}
}

func (r *closeRuntime) Close(ctx context.Context, sessionID string) error {
	if r.entered != nil {
		close(r.entered)
		r.entered = nil
	}
	if r.gate != nil {
		<-r.gate
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *closeRuntime) failWith(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

// shutdownAnswer runs an unconditional Shutdown and returns its outcome and
// exit report.
func shutdownAnswer(t *testing.T, s *Server, sessionID string) (adapterv1.SlotReclaimOutcome, bool) {
	t.Helper()
	resp, err := s.Shutdown(context.Background(), unconditionalShutdownReq(sessionID))
	if err != nil {
		t.Fatalf("Shutdown(%s): %v", sessionID, err)
	}
	return resp.GetSlotReclaim(), resp.GetExitedCleanly()
}

const absentOutcome = adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_ABSENT

// waitForGuardWaiters blocks until n goroutines are parked acquiring a
// contended slot guard.
func waitForGuardWaiters(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	buf := make([]byte, 1<<20)
	for time.Now().Before(deadline) {
		k := runtime.Stack(buf, true)
		if strings.Count(string(buf[:k]), "acquireSlotGuardChan(") >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("fewer than %d goroutines parked on the slot guard", n)
}

// spec: §4.7.1 (role and gateway RPC contract), rule 11; §5.2 (Slot-identifier reclaim hold)
//
// Rule 11's absent answer reports an unclean exit while another Shutdown's
// teardown of the session is in flight, and after the last removing
// Shutdown reported an unclean exit, because the session's runtime may then
// still run. Every other absent answer reports a clean exit, including after
// a release outside a Shutdown, which sets neither record. The gateway's
// stale-slot reaper frees a slot set member only on a clean exit, so a clean
// answer in the first two cases would free a slot whose runtime still runs.
func TestAbsentShutdownReportsTheTeardownRecords_spec_4_7_1(t *testing.T) {
	t.Run("no record names the session", func(t *testing.T) {
		s, _ := slotPod(t)
		s.Runtime = &closeRuntime{}
		if out, clean := shutdownAnswer(t, s, "alice"); out != absentOutcome || !clean {
			t.Errorf("answer = %v clean=%v, want ABSENT with a clean exit", out, clean)
		}
	})

	t.Run("another Shutdown's teardown is in flight", func(t *testing.T) {
		s, _ := slotPod(t)
		rt := &closeRuntime{entered: make(chan struct{}), gate: make(chan struct{})}
		entered := rt.entered
		s.Runtime = rt
		bindAndStart(t, s, "alice")
		done := make(chan bool, 1)
		go func() {
			resp, err := s.Shutdown(context.Background(), unconditionalShutdownReq("alice"))
			if err != nil {
				t.Errorf("first Shutdown: %v", err)
			}
			done <- resp.GetExitedCleanly()
		}()
		<-entered
		if out, clean := shutdownAnswer(t, s, "alice"); out != absentOutcome || clean {
			t.Errorf("answer during the teardown = %v clean=%v, want ABSENT with an unclean exit", out, clean)
		}
		close(rt.gate)
		if !<-done {
			t.Error("the first Shutdown's own close succeeded but it reported an unclean exit")
		}
		if out, clean := shutdownAnswer(t, s, "alice"); out != absentOutcome || !clean {
			t.Errorf("answer after a clean teardown = %v clean=%v, want ABSENT with a clean exit", out, clean)
		}
	})

	t.Run("the last removing Shutdown's close failed", func(t *testing.T) {
		s, _ := slotPod(t)
		rt := &closeRuntime{err: errors.New("runtime close failed")}
		s.Runtime = rt
		bindAndStart(t, s, "alice")
		if _, clean := shutdownAnswer(t, s, "alice"); clean {
			t.Fatal("a Shutdown whose Runtime.Close failed reported a clean exit")
		}
		if out, clean := shutdownAnswer(t, s, "alice"); out != absentOutcome || clean {
			t.Errorf("answer = %v clean=%v, want ABSENT with an unclean exit", out, clean)
		}
	})

	t.Run("a completed teardown", func(t *testing.T) {
		s, _ := slotPod(t)
		s.Runtime = &closeRuntime{}
		bindAndStart(t, s, "alice")
		if _, clean := shutdownAnswer(t, s, "alice"); !clean {
			t.Fatal("a completed teardown reported an unclean exit")
		}
		if out, clean := shutdownAnswer(t, s, "alice"); out != absentOutcome || !clean {
			t.Errorf("answer = %v clean=%v, want ABSENT with a clean exit", out, clean)
		}
	})

	t.Run("a running slot whose close succeeded and whose tree removal failed", func(t *testing.T) {
		s, _ := slotPod(t)
		s.Runtime = &closeRuntime{}
		bindAndStart(t, s, "alice")
		s.removeSlotTreeFn = func(*slotState) error { return errors.New("injected removal failure") }
		if _, clean := shutdownAnswer(t, s, "alice"); !clean {
			t.Fatal("a running slot whose close succeeded reported an unclean exit")
		}
		if out, clean := shutdownAnswer(t, s, "alice"); out != absentOutcome || !clean {
			t.Errorf("answer = %v clean=%v, want ABSENT with a clean exit", out, clean)
		}
	})

	t.Run("a release outside a Shutdown whose act failed", func(t *testing.T) {
		s, _ := slotPod(t)
		s.Runtime = &closeRuntime{}
		bindAndStart(t, s, "alice")
		s.removeSlotTreeFn = func(*slotState) error { return errors.New("injected removal failure") }
		s.noteRuntimeClosed("alice")
		s.releaseSessionSlot(context.Background(), "alice")
		if out, clean := shutdownAnswer(t, s, "alice"); out != absentOutcome || !clean {
			t.Errorf("answer = %v clean=%v, want ABSENT with a clean exit", out, clean)
		}
	})
}

// spec: §4.7.1 (role and gateway RPC contract), rule 11; §5.2 (Slot-identifier reclaim hold)
//
// Two unconditional Shutdowns pass the first decision while a holder keeps
// the slot guard. Once freed, the first tears the slot down with a failing
// Runtime.Close, and the second answers from its second decision, which
// finds no entry. That absent answer reports an unclean exit, because the
// record of the first teardown is read in the critical section that made the
// decision.
func TestAbsentFromTheSecondDecisionReportsAnUncleanTeardown_spec_4_7_1(t *testing.T) {
	s, _ := slotPod(t)
	rt := &closeRuntime{}
	s.Runtime = rt
	bindAndStart(t, s, "alice")
	releaseHolder := holdGuard(t, s, "alice")

	type answer struct {
		outcome adapterv1.SlotReclaimOutcome
		clean   bool
	}
	answers := make(chan answer, 2)
	for i := 0; i < 2; i++ {
		go func() {
			resp, err := s.Shutdown(context.Background(), unconditionalShutdownReq("alice"))
			if err != nil {
				t.Errorf("Shutdown: %v", err)
			}
			answers <- answer{resp.GetSlotReclaim(), resp.GetExitedCleanly()}
		}()
	}
	waitForGuardWaiters(t, 2)
	rt.failWith(errors.New("runtime close failed"))
	releaseHolder()

	var absent int
	for i := 0; i < 2; i++ {
		a := <-answers
		if a.clean {
			t.Errorf("answer %v reported a clean exit after a teardown whose close failed", a.outcome)
		}
		if a.outcome == absentOutcome {
			absent++
		}
	}
	if absent != 1 {
		t.Errorf("absent answers = %d, want 1", absent)
	}
}
