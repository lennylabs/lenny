// SPDX-License-Identifier: MIT

package adapter

import (
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// This file holds the outcome of a supervised runtime process's end: the end
// disposition the adapter holds for the current generation, the rows of the
// Runtime exits table keyed on it, the not-live causes, and the one step,
// under the registry critical section, in which the adapter applies a row.
//
// spec: §4.7.10 (Runtime supervisor, Runtime exits; Runtime supervisor,
// Crash; Runtime supervisor, Liveness); §4.7.1 (The registry critical
// section).

// EndDisposition is the adapter's end disposition for the current runtime
// generation. The values are declared in precedence order: other than at a
// launch, which resets the value to EndNone, a set to a value declared after
// the current one leaves the current one in place. spec: §4.7.10 (Runtime
// supervisor, Runtime exits).
type EndDisposition int

const (
	// EndDemote is set before the end a DemoteSDK sends.
	EndDemote EndDisposition = iota
	// EndScrub is set before the first and the second end of a restart pod's
	// whole-pod scrub.
	EndScrub
	// EndShutdown is set before the adapter's CH-MSGSOCK shutdown write.
	EndShutdown
	// EndHoldTimeout is set before the adapter's close of the runtime
	// connection at the coordinator hold timeout.
	EndHoldTimeout
	// EndDeleting is set when the pod's termination reaches the adapter
	// through its preStop hook or its stop signal.
	EndDeleting
	// EndNone is the value each launch sets: no act of the adapter is ending
	// the generation.
	EndNone
)

// String names the disposition as the Runtime exits table does.
func (d EndDisposition) String() string {
	switch d {
	case EndDemote:
		return "endDemote"
	case EndScrub:
		return "endScrub"
	case EndShutdown:
		return "shutdown"
	case EndHoldTimeout:
		return "holdTimeout"
	case EndDeleting:
		return "deleting"
	default:
		return "none"
	}
}

// LivenessCause is a cause of the adapter's not-live state. spec: §4.7.10
// (Runtime supervisor, Liveness).
type LivenessCause string

const (
	// CauseReplyError is a launched, ended, or swept reply that carries an
	// error, a sweep walk that reached its bound included, or a failed
	// listener rebind.
	CauseReplyError LivenessCause = "reply_error"
	// CauseLinkFailure is a CH-SUPERVISE link failure.
	CauseLinkFailure LivenessCause = "link_failure"
	// CauseRuntimeExit is an end whose Runtime exits row names this cause.
	CauseRuntimeExit LivenessCause = "runtime_exit"
	// CauseNoConnection is a launched process that does not connect within
	// the deadline of the start that launched it, or the hold timeout's close
	// of its connection.
	CauseNoConnection LivenessCause = "no_connection"
	// CauseHang is a heartbeat escalation of a session as hung.
	CauseHang LivenessCause = "hang"
	// CauseWarmUpFailure is a warm-up launch that fails or whose process does
	// not connect within the adapter's bounded wait.
	CauseWarmUpFailure LivenessCause = "warm_up_failure"
)

// exitRow is one row of the Runtime exits table.
type exitRow struct {
	// crash records that each session the ended process holds fails with
	// runtime_crash.
	crash bool
	// notLive is the cause the row sets, empty when it sets none.
	notLive LivenessCause
}

// runtimeExitRow returns the row the disposition selects. spec: §4.7.10
// (Runtime supervisor, Runtime exits).
func runtimeExitRow(d EndDisposition) exitRow {
	switch d {
	case EndDemote, EndScrub, EndShutdown:
		return exitRow{}
	case EndHoldTimeout:
		return exitRow{notLive: CauseNoConnection}
	case EndDeleting:
		return exitRow{crash: true}
	default:
		return exitRow{crash: true, notLive: CauseRuntimeExit}
	}
}

// RuntimeEndKind names the event by which the adapter learns that a runtime
// generation ended.
type RuntimeEndKind string

const (
	// EndByExited is the supervisor's exited report.
	EndByExited RuntimeEndKind = "exited"
	// EndByEnded is an ended reply that carries no error.
	EndByEnded RuntimeEndKind = "ended"
	// EndByConnection is the end of the process's CH-MSGSOCK connection that
	// the adapter did not cause by its exit or rebind.
	EndByConnection RuntimeEndKind = "connection"
)

// RuntimeEndEvent is one event that ends a runtime generation.
type RuntimeEndEvent struct {
	Kind RuntimeEndKind
	// Exit is the reported exit, set for EndByExited.
	Exit RuntimeExit
}

// errRuntimeCrash is the status an Attach stream of a session the crashed
// process held ends with. Internal is the code the gateway's stream-failure
// funnel reports as the session's runtime_crash. spec: §4.7.10 (Runtime
// supervisor, Crash); §7.3; §28.5.1 (CH-ATTACH, Degradation).
var errRuntimeCrash = status.Error(codes.Internal, "runtime_crash: the runtime process serving the session ended")

// handleRuntimeEnd applies the Runtime exits row for one end event of a
// supervised runtime's generation gen. The row is chosen and applied under
// s.mu, the lock of the registry critical section, so the handling of an end
// and a Shutdown's deregistration of a session's entry are ordered by the
// lock: a session whose entry stands and whose rule-8 record names gen when
// the lock is taken is a session the process holds. The first event the
// adapter handles for a generation selects the row by the disposition then
// held, and a later event for the same generation changes nothing in the
// row's outcome. A late exited still receives the Exit Codes check, because
// only the exited report carries the status and the connection end or the
// ended reply can be handled first.
// spec: §4.7.10 (Runtime supervisor, Runtime exits; Runtime supervisor,
// Crash); §4.7.1 (The registry critical section); §28.5.3 (Exit Codes).
func (s *Server) handleRuntimeEnd(rt *SupervisedRuntime, gen uint64, ev RuntimeEndEvent) {
	s.mu.Lock()
	disp, ok := rt.takeEnd(gen)
	var crashed int
	var newlyNotLive bool
	row := runtimeExitRow(disp)
	if ok {
		if row.crash {
			crashed = s.crashSessionsLocked(gen)
		}
		if row.notLive != "" {
			newlyNotLive = rt.markNotLive(row.notLive)
		}
	}
	s.mu.Unlock()
	if !ok {
		warnLateShutdownExit(rt, gen, ev)
		return
	}
	if newlyNotLive {
		rt.notifyNotLive(row.notLive)
	}
	logRuntimeEnd(rt.logger, gen, ev, disp, crashed)
}

// warnLateShutdownExit records the Exit Codes alert for an exited report that
// arrives after another event already handled its generation's end on the
// shutdown row. It logs a non-zero status at warning level and changes
// nothing else. spec: §4.7.10 (Runtime supervisor, Runtime exits); §28.5.3
// (Exit Codes).
func warnLateShutdownExit(rt *SupervisedRuntime, gen uint64, ev RuntimeEndEvent) {
	if ev.Kind != EndByExited || ev.Exit.Clean() {
		return
	}
	if disp, handled := rt.handledEnd(gen); handled && disp == EndShutdown {
		rt.logger.Warn("runtime_shutdown_exit_nonzero",
			"gen", gen, "event", string(ev.Kind), "disposition", disp.String(), "exit", ev.Exit.String(), "late", true)
	}
}

// crashSessionsLocked fails every session the ended generation holds: a
// session whose registry entry stands and carries the rule-8 record of gen.
// Each one's Attach stream ends with errRuntimeCrash. The caller holds s.mu.
// spec: §4.7.10 (Runtime supervisor, Crash).
func (s *Server) crashSessionsLocked(gen uint64) int {
	n := 0
	for id := range s.runtimeLive {
		st, ok := s.slots[id]
		if !ok || st.runtimeGen != gen {
			continue
		}
		st.markRuntimeCrashed()
		n++
	}
	return n
}

// logRuntimeEnd records the end. On the shutdown row a non-zero status is
// logged at warning level with the status, which is the Exit Codes alert.
// spec: §28.5.3 (Exit Codes).
func logRuntimeEnd(logger *slog.Logger, gen uint64, ev RuntimeEndEvent, disp EndDisposition, crashed int) {
	attrs := []any{"gen", gen, "event", string(ev.Kind), "disposition", disp.String(), "sessions_failed", crashed}
	if ev.Kind == EndByExited {
		attrs = append(attrs, "exit", ev.Exit.String())
	}
	if disp == EndShutdown && ev.Kind == EndByExited && !ev.Exit.Clean() {
		logger.Warn("runtime_shutdown_exit_nonzero", attrs...)
		return
	}
	logger.Info("runtime_process_ended", attrs...)
}

// markRuntimeCrashed closes the entry's crash channel once. The caller holds
// s.mu.
func (st *slotState) markRuntimeCrashed() {
	if st.crash == nil {
		st.crash = make(chan struct{})
	}
	select {
	case <-st.crash:
	default:
		close(st.crash)
	}
}

// entryRuntimeCrash returns the named session's crash channel, creating it
// so a stream can wait on it. Nil when the registry holds no entry.
func (s *Server) entryRuntimeCrash(sessionID string) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.slots[sessionID]
	if !ok {
		return nil
	}
	if st.crash == nil {
		st.crash = make(chan struct{})
	}
	return st.crash
}
