// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// heartbeatServer is sealServer with heartbeats enabled before the sessions
// start, so each session_start write starts the session's monitor.
func heartbeatServer(t *testing.T, interval, ackTimeout time.Duration, sessions ...string) (*Server, *tapRuntime) {
	t.Helper()
	s, rt, _ := sealServer(t)
	s.clock = nil
	s.HeartbeatInterval = interval
	s.HeartbeatAckTimeout = ackTimeout
	for _, id := range sessions {
		if _, err := s.StartSession(context.Background(), frameStartReq(id)); err != nil {
			t.Fatalf("StartSession(%s): %v", id, err)
		}
	}
	return s, rt
}

// heartbeatsFor counts the heartbeat frames written for sessionID.
func (r *tapRuntime) heartbeatsFor(sessionID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, f := range r.frames {
		if jsonlFrameType(f) == "heartbeat" && frameSessionID(f) == sessionID {
			n++
		}
	}
	return n
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// spec: §28.5.3 (CH-MSGSOCK), §4.7.1 (Role and Gateway RPC Contract)
// Heartbeats for a session run from its session_start write with no Attach
// open and stop at session_end or deregistration with the monitor exiting; no
// heartbeat follows session_end even when the entry stays; and no monitor
// runs on an MCP runtime, an untapped runtime, or with heartbeats disabled.
//
// Discriminates: a heartbeat after session_end.
func TestHeartbeatsRunPerEntryFromSessionStart_spec_28_5_3(t *testing.T) {
	ctx := context.Background()
	t.Run("from session_start with no Attach, until Shutdown", func(t *testing.T) {
		s, rt := heartbeatServer(t, 2*time.Millisecond, time.Minute, "sess-a")
		waitFor(t, "a heartbeat for sess-a", func() bool { return rt.heartbeatsFor("sess-a") >= 2 })
		hb := entryOf(t, s, "sess-a").hb
		if _, err := s.Shutdown(ctx, unconditionalShutdownReq("sess-a")); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		<-hb.done
		assertNoHeartbeatAfterSessionEnd(t, rt, "sess-a")
	})
	t.Run("session_end while the entry stays", func(t *testing.T) {
		s, rt := heartbeatServer(t, 2*time.Millisecond, time.Minute, "sess-a")
		waitFor(t, "a heartbeat", func() bool { return rt.heartbeatsFor("sess-a") >= 1 })
		entry := entryOf(t, s, "sess-a")
		s.writeSessionEnd("sess-a", entry)
		before := rt.heartbeatsFor("sess-a")
		time.Sleep(20 * time.Millisecond)
		if got := rt.heartbeatsFor("sess-a"); got != before {
			t.Fatalf("heartbeats grew from %d to %d after session_end with the entry still registered", before, got)
		}
		assertNoHeartbeatAfterSessionEnd(t, rt, "sess-a")
		if s.slotStateForSession("sess-a") == nil {
			t.Fatal("the entry was removed; this case needs it to stay")
		}
	})
	t.Run("deregistration cancels the monitor", func(t *testing.T) {
		s, _ := heartbeatServer(t, 2*time.Millisecond, time.Minute, "sess-a")
		hb := entryOf(t, s, "sess-a").hb
		s.mu.Lock()
		s.deregisterSlotLocked("sess-a")
		s.mu.Unlock()
		select {
		case <-hb.done:
		case <-time.After(3 * time.Second):
			t.Fatal("the monitor outlived its entry")
		}
	})
	for _, tc := range []struct {
		name  string
		setup func(s *Server)
	}{
		{"mcp runtime", func(s *Server) { s.RuntimeKind = RuntimeKindMCP }},
		{"untapped runtime", func(s *Server) { s.activityTapped = false }},
		{"heartbeats disabled", func(s *Server) { s.HeartbeatInterval = 0 }},
	} {
		t.Run("no monitor: "+tc.name, func(t *testing.T) {
			s, rt, _ := sealServer(t)
			s.HeartbeatInterval = 2 * time.Millisecond
			tc.setup(s)
			if _, err := s.StartSession(ctx, frameStartReq("sess-a")); err != nil {
				t.Fatalf("StartSession: %v", err)
			}
			if entryOf(t, s, "sess-a").hb != nil {
				t.Fatal("a monitor was started")
			}
			time.Sleep(10 * time.Millisecond)
			if rt.heartbeatsFor("sess-a") != 0 {
				t.Fatal("a heartbeat was written")
			}
		})
	}
}

// assertNoHeartbeatAfterSessionEnd fails when a heartbeat for sessionID
// follows the session's session_end in the runtime's frame order.
func assertNoHeartbeatAfterSessionEnd(t *testing.T, rt *tapRuntime, sessionID string) {
	t.Helper()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	ended := false
	for _, f := range rt.frames {
		if frameSessionID(f) != sessionID {
			continue
		}
		switch jsonlFrameType(f) {
		case "session_end":
			ended = true
		case "heartbeat":
			if ended {
				t.Fatal("a heartbeat followed the session's session_end")
			}
		}
	}
	if !ended {
		t.Fatal("no session_end was written")
	}
}

// spec: §28.5.3 (CH-MSGSOCK), §4.7.1 (Role and Gateway RPC Contract)
// A missed ack deadline interrupts the runtime once and ends the open Attach
// and every later Attach for the entry with DeadlineExceeded; a re-registered
// session heartbeats afresh; and acks reach the monitor through the tap.
func TestHeartbeatHangEndsEveryAttachForTheEntry_spec_28_5_3(t *testing.T) {
	ctx := context.Background()
	t.Run("acks through the tap keep the session alive", func(t *testing.T) {
		s, rt := heartbeatServer(t, 2*time.Millisecond, 30*time.Millisecond, "sess-a")
		stop := make(chan struct{})
		go func() {
			for {
				select {
				case <-stop:
					return
				case <-time.After(time.Millisecond):
					rt.emit(`{"type":"heartbeat_ack","sessionId":"sess-a"}`)
				}
			}
		}()
		time.Sleep(100 * time.Millisecond)
		close(stop)
		select {
		case <-entryOf(t, s, "sess-a").hb.hung:
			t.Fatal("a runtime acking through the tap was declared hung")
		default:
		}
	})
	t.Run("a hang ends the open and every later Attach", func(t *testing.T) {
		s, rt := heartbeatServer(t, 2*time.Millisecond, 20*time.Millisecond, "sess-a")
		attachCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		open := make(chan error, 1)
		go func() {
			open <- s.Attach(&firstFrameStream{ctx: attachCtx, first: &adapterv1.AttachRequest{
				SessionId: &adapterv1.SessionId{Value: "sess-a"},
			}})
		}()
		select {
		case err := <-open:
			if status.Code(err) != codes.DeadlineExceeded {
				t.Fatalf("open Attach ended with %v, want DeadlineExceeded", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("the open Attach outlived the hang")
		}
		err := s.Attach(&firstFrameStream{ctx: attachCtx, first: &adapterv1.AttachRequest{
			SessionId: &adapterv1.SessionId{Value: "sess-a"},
		}})
		if status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("later Attach = %v, want DeadlineExceeded", err)
		}
		interrupts := func() int {
			rt.mu.Lock()
			defer rt.mu.Unlock()
			return rt.interrupts
		}
		// The monitor closes hung before it escalates, so the escalation
		// can land after the stream ended.
		<-entryOf(t, s, "sess-a").hb.done
		if got := interrupts(); got != 1 {
			t.Fatalf("interrupts = %d, want exactly 1", got)
		}
	})
	t.Run("a re-registered session heartbeats afresh", func(t *testing.T) {
		s, rt := heartbeatServer(t, 2*time.Millisecond, 10*time.Millisecond, "sess-a")
		hung := entryOf(t, s, "sess-a").hb.hung
		<-hung
		if _, err := s.Shutdown(ctx, unconditionalShutdownReq("sess-a")); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		s.HeartbeatAckTimeout = time.Minute
		if _, err := s.StartSession(ctx, frameStartReq("sess-a")); err != nil {
			t.Fatalf("StartSession again: %v", err)
		}
		fresh := entryOf(t, s, "sess-a").hb
		if fresh == nil {
			t.Fatal("the recreated entry has no monitor")
		}
		select {
		case <-fresh.hung:
			t.Fatal("the recreated entry inherited the hang")
		default:
		}
		before := rt.heartbeatsFor("sess-a")
		waitFor(t, "a fresh heartbeat", func() bool { return rt.heartbeatsFor("sess-a") > before })
		var probe map[string]any
		rt.mu.Lock()
		last := rt.frames[len(rt.frames)-1]
		rt.mu.Unlock()
		_ = json.Unmarshal(last, &probe)
	})
}

// spec: §28.5.3 (CH-MSGSOCK), §4.7.1 (Role and Gateway RPC Contract)
// Every entry whose session_start is written and whose session_end is not
// holds exactly one running monitor, including an entry that survives its
// session_end and has its session_start written again. A monitor stopped by
// session_end is replaced at the next session_start write; a hung monitor is
// kept, so the entry's hang stays visible until the entry is removed.
//
// Discriminates: a start site that only starts a monitor when the entry has
// none, which leaves a re-started entry with a stopped monitor and no probe.
func TestHeartbeatRestartsOnSessionStartRewrittenOnSameEntry_spec_28_5_3(t *testing.T) {
	t.Run("stopped monitor is replaced", func(t *testing.T) {
		s, rt := heartbeatServer(t, 2*time.Millisecond, time.Minute, "sess-a")
		entry := entryOf(t, s, "sess-a")
		waitFor(t, "a heartbeat", func() bool { return rt.heartbeatsFor("sess-a") >= 1 })
		s.writeSessionEnd("sess-a", entry)
		old := entry.hb
		<-old.done
		s.markSessionStartWritten("sess-a", entry, true)
		s.mu.Lock()
		fresh := entry.hb
		s.mu.Unlock()
		if fresh == old {
			t.Fatal("the stopped monitor was kept; the re-started session has no running monitor")
		}
		select {
		case <-fresh.done:
			t.Fatal("the replacement monitor is not running")
		default:
		}
		before := rt.heartbeatsFor("sess-a")
		waitFor(t, "a heartbeat from the replacement monitor", func() bool {
			return rt.heartbeatsFor("sess-a") > before
		})
		s.writeSessionEnd("sess-a", entry)
	})
	t.Run("hung monitor is kept", func(t *testing.T) {
		s, _ := heartbeatServer(t, 2*time.Millisecond, 10*time.Millisecond, "sess-a")
		entry := entryOf(t, s, "sess-a")
		hungMon := entry.hb
		<-hungMon.hung
		<-hungMon.done
		s.markSessionStartWritten("sess-a", entry, true)
		s.mu.Lock()
		got := entry.hb
		s.mu.Unlock()
		if got != hungMon {
			t.Fatal("a hung monitor was replaced, hiding the entry's hang")
		}
	})
}
