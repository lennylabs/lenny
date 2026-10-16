// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// tapRuntime is a RuntimeProcess whose output the adapter taps: emit plays
// a frame the runtime writes, handed straight to the activity tap, and every
// frame the adapter writes is recorded.
type tapRuntime struct {
	mu         sync.Mutex
	tap        func([]byte)
	frames     [][]byte
	interrupts int
	writeErr   error
}

func (r *tapRuntime) TapOutput(fn func(line []byte)) {
	r.mu.Lock()
	r.tap = fn
	r.mu.Unlock()
}

func (r *tapRuntime) emit(line string) {
	r.mu.Lock()
	tap := r.tap
	r.mu.Unlock()
	if tap != nil {
		tap([]byte(line))
	}
}

func (r *tapRuntime) Start(context.Context, string) error { return nil }

func (r *tapRuntime) WriteEnvelope(_ string, envelope []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames = append(r.frames, append([]byte(nil), envelope...))
	return r.writeErr
}

func (r *tapRuntime) Output(ctx context.Context, _ string) (<-chan []byte, error) {
	ch := make(chan []byte)
	go func() { <-ctx.Done(); close(ch) }()
	return ch, nil
}

func (r *tapRuntime) Interrupt(context.Context, string, bool) error {
	r.mu.Lock()
	r.interrupts++
	r.mu.Unlock()
	return nil
}

func (r *tapRuntime) Close(context.Context, string) error { return nil }

func (r *tapRuntime) frameTypes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.frames))
	for _, f := range r.frames {
		out = append(out, jsonlFrameType(f))
	}
	return out
}

func (r *tapRuntime) countType(ty string) int {
	n := 0
	for _, t := range r.frameTypes() {
		if t == ty {
			n++
		}
	}
	return n
}

// manualClock is a settable activity clock.
type manualClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *manualClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *manualClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// sealServer builds a tapped Server over a tapRuntime with a manual clock
// and starts each named session, so each entry's session_start is written.
func sealServer(t *testing.T, sessions ...string) (*Server, *tapRuntime, *manualClock) {
	t.Helper()
	rt := &tapRuntime{}
	s := New("seal-test")
	s.WorkspaceBase = t.TempDir()
	s.Runtime = rt
	clk := &manualClock{t: time.Unix(1_800_000_000, 0)}
	s.clock = clk.now
	s.installActivityTap()
	for _, id := range sessions {
		if _, err := s.StartSession(context.Background(), frameStartReq(id)); err != nil {
			t.Fatalf("StartSession(%s): %v", id, err)
		}
	}
	return s, rt, clk
}

func sealReq(sessionID string, gen, idle int64) *adapterv1.SealSessionRequest {
	return &adapterv1.SealSessionRequest{
		SessionId:              &adapterv1.SessionId{Value: sessionID},
		CoordinationGeneration: gen,
		IdleSeconds:            idle,
	}
}

func mustSeal(t *testing.T, s *Server, sessionID string, idle int64) *adapterv1.SealSessionResponse {
	t.Helper()
	resp, err := s.SealSession(context.Background(), sealReq(sessionID, 0, idle))
	if err != nil {
		t.Fatalf("SealSession(%s): %v", sessionID, err)
	}
	return resp
}

func entryOf(t *testing.T, s *Server, sessionID string) *slotState {
	t.Helper()
	st := s.slotStateForSession(sessionID)
	if st == nil {
		t.Fatalf("no registry entry for %s", sessionID)
	}
	return st
}

func lastActivity(s *Server, st *slotState) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return st.lastActivity
}

// spec: §4.7.1 (Role and Gateway RPC Contract), §28.5.3 (CH-MSGSOCK)
// SealSession seals exactly when the last activity is at least the window
// old, answers not sealed with the idle age otherwise, keeps a seal when newer
// activity arrives, seals co-tenants independently, refuses an unopened entry
// and a zero window, and starts a fresh window at each session_start write.
func TestSealSessionDecidesOnTheActivityWindow_spec_4_7_1(t *testing.T) {
	t.Run("seals at the window and not one second before", func(t *testing.T) {
		s, _, clk := sealServer(t, "sess-a")
		clk.advance(59 * time.Second)
		resp := mustSeal(t, s, "sess-a", 60)
		if resp.GetSealed() || resp.GetIdleAgeSeconds() != 59 {
			t.Fatalf("at 59s: sealed=%v age=%d, want not sealed with age 59", resp.GetSealed(), resp.GetIdleAgeSeconds())
		}
		clk.advance(time.Second)
		if resp := mustSeal(t, s, "sess-a", 60); !resp.GetSealed() {
			t.Fatal("at 60s: not sealed, want sealed")
		}
	})
	t.Run("a seal stays when newer activity arrives", func(t *testing.T) {
		s, rt, clk := sealServer(t, "sess-a")
		clk.advance(time.Minute)
		mustSeal(t, s, "sess-a", 60)
		rt.emit(`{"type":"response","sessionId":"sess-a"}`)
		resp := mustSeal(t, s, "sess-a", 60)
		if resp.GetSealed() {
			t.Fatal("an answer after newer activity reported sealed")
		}
		if !entryOf(t, s, "sess-a").sealed {
			t.Fatal("newer activity lifted the seal; only UnsealSession or removal may")
		}
	})
	t.Run("co-tenants seal independently", func(t *testing.T) {
		s, rt, clk := sealServer(t, "sess-a", "sess-b")
		clk.advance(time.Minute)
		rt.emit(`{"type":"response","sessionId":"sess-b"}`)
		if !mustSeal(t, s, "sess-a", 60).GetSealed() {
			t.Fatal("sess-a not sealed")
		}
		if mustSeal(t, s, "sess-b", 60).GetSealed() {
			t.Fatal("sess-b sealed despite its own activity")
		}
		if entryOf(t, s, "sess-b").sealed {
			t.Fatal("sealing sess-a sealed sess-b")
		}
	})
	t.Run("an entry without session_start is refused", func(t *testing.T) {
		s, _, _ := sealServer(t)
		if _, err := s.claimSessionSlot("sess-a", slotResolve{allowCreate: true}, false, false); err != nil {
			t.Fatalf("claim: %v", err)
		}
		_, err := s.SealSession(context.Background(), sealReq("sess-a", 0, 60))
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("SealSession on an unopened entry = %v, want FailedPrecondition", err)
		}
	})
	t.Run("a zero window is refused", func(t *testing.T) {
		s, _, _ := sealServer(t, "sess-a")
		_, err := s.SealSession(context.Background(), sealReq("sess-a", 0, 0))
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("SealSession with idle_seconds 0 = %v, want InvalidArgument", err)
		}
	})
	t.Run("each session_start write starts a fresh window", func(t *testing.T) {
		s, _, clk := sealServer(t, "sess-a")
		clk.advance(2 * time.Minute)
		st := entryOf(t, s, "sess-a")
		s.markSessionStartWritten("sess-a", st, true)
		resp := mustSeal(t, s, "sess-a", 60)
		if resp.GetSealed() || resp.GetIdleAgeSeconds() != 0 {
			t.Fatalf("after a session_start write: sealed=%v age=%d, want a fresh window", resp.GetSealed(), resp.GetIdleAgeSeconds())
		}
	})
	t.Run("seals one window after the last frame whatever the heartbeat interval", func(t *testing.T) {
		s, rt, clk := sealServer(t, "sess-a")
		s.HeartbeatInterval = time.Hour
		clk.advance(30 * time.Second)
		rt.emit(`{"type":"status","sessionId":"sess-a"}`)
		clk.advance(59 * time.Second)
		if mustSeal(t, s, "sess-a", 60).GetSealed() {
			t.Fatal("sealed 59s after the last frame")
		}
		clk.advance(time.Second)
		if !mustSeal(t, s, "sess-a", 60).GetSealed() {
			t.Fatal("not sealed one window after the last frame")
		}
	})
}

// spec: §4.7.1 (Role and Gateway RPC Contract), §28.5.3 (CH-MSGSOCK), §6.2
// (maxClientIdleSeconds clock behavior across states)
// Bare busy acks with no sessionId keep no session unsealed, so a session
// seals one window after its last traffic; a tapped response moves the seal.
//
// Discriminates: unattributed busy treated as activity.
func TestUnattributedBusyIsActivityForNoSession_spec_4_7_1(t *testing.T) {
	s, rt, clk := sealServer(t, "sess-a")
	for i := 0; i < 6; i++ {
		clk.advance(10 * time.Second)
		rt.emit(`{"type":"heartbeat_ack","busy":true}`)
	}
	if !mustSeal(t, s, "sess-a", 60).GetSealed() {
		t.Fatal("bare busy acks kept the session unsealed")
	}
	s2, rt2, clk2 := sealServer(t, "sess-b")
	clk2.advance(50 * time.Second)
	rt2.emit(`{"type":"response","sessionId":"sess-b"}`)
	clk2.advance(50 * time.Second)
	if resp := mustSeal(t, s2, "sess-b", 60); resp.GetSealed() || resp.GetIdleAgeSeconds() != 50 {
		t.Fatalf("after a tapped response: sealed=%v age=%d, want not sealed with age 50", resp.GetSealed(), resp.GetIdleAgeSeconds())
	}
}

// spec: §4.7.1 (Role and Gateway RPC Contract), §28.5.3 (CH-MSGSOCK)
// A tapped heartbeat_ack with busy:true naming the session keeps SealSession
// answering not sealed with no Attach open, and the session seals one window
// after the last busy ack.
func TestBusyAckNamingTheSessionKeepsItUnsealed_spec_4_7_1(t *testing.T) {
	s, rt, clk := sealServer(t, "sess-a")
	for i := 0; i < 5; i++ {
		clk.advance(30 * time.Second)
		rt.emit(`{"type":"heartbeat_ack","sessionId":"sess-a","busy":true}`)
		if mustSeal(t, s, "sess-a", 60).GetSealed() {
			t.Fatalf("round %d: sealed while the runtime reported busy", i)
		}
	}
	clk.advance(59 * time.Second)
	if mustSeal(t, s, "sess-a", 60).GetSealed() {
		t.Fatal("sealed 59s after the last busy ack")
	}
	clk.advance(time.Second)
	if !mustSeal(t, s, "sess-a", 60).GetSealed() {
		t.Fatal("not sealed one window after the last busy ack")
	}
	rt.emit(`{"type":"heartbeat_ack","sessionId":"sess-a","busy":false}`)
	rt.emit(`{"type":"heartbeat_ack","sessionId":"sess-x","busy":true}`)
}

// spec: §4.7.1 (Role and Gateway RPC Contract), §28.5.3 (CH-MSGSOCK,
// CH-RUNTIMEOPS), §6.2 (maxClientIdleSeconds clock behavior across states)
// Every adapter row of the activity inventory stamps the entry its setting
// site resolves for each counted event and stamps nothing for each event it
// does not count, with co-tenant isolation on a two-slot server.
func TestActivityInventoryStampsOnlyTheResolvedEntry_spec_4_7_1(t *testing.T) {
	type step struct {
		name   string
		do     func(s *Server, rt *tapRuntime)
		stamps map[string]bool
	}
	steps := []step{
		{"response naming sess-a", func(_ *Server, rt *tapRuntime) { rt.emit(`{"type":"response","sessionId":"sess-a"}`) }, map[string]bool{"sess-a": true}},
		{"tool_call naming sess-b", func(_ *Server, rt *tapRuntime) { rt.emit(`{"type":"tool_call","sessionId":"sess-b"}`) }, map[string]bool{"sess-b": true}},
		{"unaddressed response on a two-slot pod", func(_ *Server, rt *tapRuntime) { rt.emit(`{"type":"response"}`) }, nil},
		{"response naming an unknown session", func(_ *Server, rt *tapRuntime) { rt.emit(`{"type":"response","sessionId":"sess-x"}`) }, nil},
		{"session_started", func(_ *Server, rt *tapRuntime) {
			rt.emit(`{"type":"session_started","sessionId":"sess-a","startId":"1"}`)
		}, nil},
		{"heartbeat_ack busy naming sess-a", func(_ *Server, rt *tapRuntime) {
			rt.emit(`{"type":"heartbeat_ack","sessionId":"sess-a","busy":true}`)
		}, map[string]bool{"sess-a": true}},
		{"heartbeat_ack not busy", func(_ *Server, rt *tapRuntime) {
			rt.emit(`{"type":"heartbeat_ack","sessionId":"sess-a"}`)
		}, nil},
		{"heartbeat_ack busy naming no session", func(_ *Server, rt *tapRuntime) {
			rt.emit(`{"type":"heartbeat_ack","busy":true}`)
		}, nil},
		{"llm_request_completed naming sess-b", func(s *Server, _ *tapRuntime) { s.observeRuntimeOpsSession("sess-b") }, map[string]bool{"sess-b": true}},
		{"llm_request_completed naming no session", func(s *Server, _ *tapRuntime) { s.observeRuntimeOpsSession("") }, nil},
		{"message write to sess-a", func(s *Server, rt *tapRuntime) {
			if err := s.writeSessionEnvelope(rt, "sess-a", []byte(`{"type":"message","id":"m"}`)); err != nil {
				panic(err)
			}
		}, map[string]bool{"sess-a": true}},
		{"tool_result write to sess-b", func(s *Server, rt *tapRuntime) {
			if err := s.writeSessionEnvelope(rt, "sess-b", []byte(`{"type":"tool_result","id":"t"}`)); err != nil {
				panic(err)
			}
		}, map[string]bool{"sess-b": true}},
		{"unknown frame type", func(_ *Server, rt *tapRuntime) { rt.emit(`{"type":"shutdown_ack","sessionId":"sess-a"}`) }, nil},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			s, rt, clk := sealServer(t, "sess-a", "sess-b")
			a, b := entryOf(t, s, "sess-a"), entryOf(t, s, "sess-b")
			before := map[string]time.Time{"sess-a": lastActivity(s, a), "sess-b": lastActivity(s, b)}
			clk.advance(time.Minute)
			st.do(s, rt)
			for id, e := range map[string]*slotState{"sess-a": a, "sess-b": b} {
				moved := lastActivity(s, e).After(before[id])
				if moved != st.stamps[id] {
					t.Errorf("%s stamped=%v, want %v", id, moved, st.stamps[id])
				}
			}
		})
	}
	t.Run("unaddressed frame resolves to the sole entry of a one-slot pod", func(t *testing.T) {
		s, rt, clk := sealServer(t, "sess-a")
		a := entryOf(t, s, "sess-a")
		before := lastActivity(s, a)
		clk.advance(time.Minute)
		rt.emit(`{"type":"status"}`)
		if !lastActivity(s, a).After(before) {
			t.Fatal("an unaddressed status on a one-slot pod did not stamp the sole entry")
		}
	})
	t.Run("session_start write is the first stamp", func(t *testing.T) {
		s, _, _ := sealServer(t, "sess-a")
		if lastActivity(s, entryOf(t, s, "sess-a")).IsZero() {
			t.Fatal("the session_start write left the entry unstamped")
		}
	})
}

// spec: §28.5.1 (CH-ATTACH), §4.7.1 (Role and Gateway RPC Contract)
// A sealed session refuses every message write with FailedPrecondition
// session_sealed: and no stamp, still writes tool_result, heartbeat, and
// session_end, leaves a co-tenant unaffected, and is unsealed by
// UnsealSession or by the entry's removal and recreation.
func TestSealedSessionRefusesOnlyMessages_spec_28_5_1(t *testing.T) {
	ctx := context.Background()
	s, rt, clk := sealServer(t, "sess-a", "sess-b")
	clk.advance(time.Minute)
	rt.emit(`{"type":"response","sessionId":"sess-b"}`)
	if !mustSeal(t, s, "sess-a", 60).GetSealed() {
		t.Fatal("sess-a not sealed")
	}
	a := entryOf(t, s, "sess-a")
	sealedAt := lastActivity(s, a)
	clk.advance(time.Minute)

	_, err := s.SendMessage(ctx, &adapterv1.SendMessageRequest{
		SessionId:    &adapterv1.SessionId{Value: "sess-a"},
		EnvelopeJson: []byte(`{"type":"message","id":"m1"}`),
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), sessionSealedPrefix) {
		t.Fatalf("SendMessage to a sealed session = %v, want FailedPrecondition with prefix %q", err, sessionSealedPrefix)
	}
	if rt.countType("message") != 0 {
		t.Fatal("a sealed session's message reached the runtime")
	}
	if !lastActivity(s, a).Equal(sealedAt) {
		t.Fatal("a refused message stamped the entry")
	}
	if err := s.writeSessionEnvelope(rt, "sess-a", []byte(`{"type":"tool_result","id":"t1"}`)); err != nil {
		t.Fatalf("tool_result to a sealed session: %v", err)
	}
	if err := s.writeSessionEnvelope(rt, "sess-b", []byte(`{"type":"message","id":"m2"}`)); err != nil {
		t.Fatalf("message to the unsealed co-tenant: %v", err)
	}
	s.writeSessionEnd("sess-a", a)
	if rt.countType("session_end") != 1 {
		t.Fatal("session_end was not written for a sealed session")
	}
	if _, err := s.UnsealSession(ctx, &adapterv1.UnsealSessionRequest{SessionId: &adapterv1.SessionId{Value: "sess-a"}}); err != nil {
		t.Fatalf("UnsealSession: %v", err)
	}
	if err := s.writeSessionEnvelope(rt, "sess-a", []byte(`{"type":"message","id":"m3"}`)); err != nil {
		t.Fatalf("message after UnsealSession: %v", err)
	}

	t.Run("removal and recreation ends the seal", func(t *testing.T) {
		s, rt, clk := sealServer(t, "sess-c")
		clk.advance(time.Minute)
		mustSeal(t, s, "sess-c", 60)
		if _, err := s.Shutdown(ctx, unconditionalShutdownReq("sess-c")); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		if _, err := s.StartSession(ctx, frameStartReq("sess-c")); err != nil {
			t.Fatalf("StartSession again: %v", err)
		}
		if err := s.writeSessionEnvelope(rt, "sess-c", []byte(`{"type":"message","id":"m"}`)); err != nil {
			t.Fatalf("message to a recreated entry: %v", err)
		}
	})
	t.Run("a sealed refusal on Attach's first frame ends the stream", func(t *testing.T) {
		s, _, clk := sealServer(t, "sess-d")
		clk.advance(time.Minute)
		mustSeal(t, s, "sess-d", 60)
		stream := &firstFrameStream{ctx: ctx, first: &adapterv1.AttachRequest{
			SessionId:    &adapterv1.SessionId{Value: "sess-d"},
			EnvelopeJson: []byte(`{"type":"message","id":"m"}`),
		}}
		err := s.Attach(stream)
		if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), sessionSealedPrefix) {
			t.Fatalf("Attach with a message to a sealed session = %v, want session_sealed", err)
		}
	})
}

// firstFrameStream is an Attach server stream that delivers one request and
// then blocks until its context ends.
type firstFrameStream struct {
	grpc.ServerStream
	ctx   context.Context
	first *adapterv1.AttachRequest
	sent  bool
}

func (f *firstFrameStream) Context() context.Context { return f.ctx }
func (f *firstFrameStream) Send(*adapterv1.AttachResponse) error {
	return nil
}

func (f *firstFrameStream) Recv() (*adapterv1.AttachRequest, error) {
	if !f.sent {
		f.sent = true
		return f.first, nil
	}
	<-f.ctx.Done()
	return nil, f.ctx.Err()
}

// spec: §28.5.1 (CH-ATTACH), §4.7.1 (Role and Gateway RPC Contract), §10.1
// (Horizontal Scaling)
// The seal RPCs refuse a missing session ID, an unbound session, an untapped
// runtime, hold state, and a generation other than the entry's last fenced
// one; a seal survives a later CoordinatorFence until a seal or unseal at the
// new generation.
func TestSealRPCsRefuseAndSurviveAFence_spec_4_7_1(t *testing.T) {
	ctx := context.Background()
	t.Run("missing session ID", func(t *testing.T) {
		s, _, _ := sealServer(t)
		if _, err := s.SealSession(ctx, sealReq("", 0, 60)); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("SealSession = %v, want InvalidArgument", err)
		}
		if _, err := s.UnsealSession(ctx, &adapterv1.UnsealSessionRequest{}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("UnsealSession = %v, want InvalidArgument", err)
		}
	})
	t.Run("unbound session", func(t *testing.T) {
		s, _, _ := sealServer(t)
		if _, err := s.SealSession(ctx, sealReq("sess-x", 0, 60)); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("SealSession = %v, want FailedPrecondition", err)
		}
		if _, err := s.UnsealSession(ctx, &adapterv1.UnsealSessionRequest{SessionId: &adapterv1.SessionId{Value: "sess-x"}}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("UnsealSession = %v, want FailedPrecondition", err)
		}
	})
	t.Run("untapped runtime", func(t *testing.T) {
		s, _, clk := sealServer(t, "sess-a")
		s.activityTapped = false
		clk.advance(time.Hour)
		_, err := s.SealSession(ctx, sealReq("sess-a", 0, 60))
		if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), activityUntappedPrefix) {
			t.Fatalf("SealSession on an untapped runtime = %v, want activity_untapped", err)
		}
		if entryOf(t, s, "sess-a").sealed {
			t.Fatal("an untapped refusal sealed the session")
		}
	})
	t.Run("hold state", func(t *testing.T) {
		s, _, _ := sealServer(t, "sess-a")
		s.enterHoldState()
		for _, m := range []string{"/lenny.adapter.v1.Adapter/SealSession", "/lenny.adapter.v1.Adapter/UnsealSession"} {
			_, err := s.holdStateUnaryInterceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: m},
				func(context.Context, any) (any, error) { return nil, nil })
			if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "coordinator_hold") {
				t.Fatalf("%s in hold state = %v, want UNAVAILABLE coordinator_hold", m, err)
			}
		}
	})
	t.Run("stale generation, and a seal that survives a fence", func(t *testing.T) {
		s, rt, clk := sealServer(t, "sess-a")
		fence := func(gen int64) {
			if _, err := s.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
				SessionId: &adapterv1.SessionId{Value: "sess-a"}, CoordinationGeneration: gen,
			}); err != nil {
				t.Fatalf("CoordinatorFence(%d): %v", gen, err)
			}
		}
		fence(3)
		clk.advance(time.Minute)
		if _, err := s.SealSession(ctx, sealReq("sess-a", 2, 60)); status.Code(err) != codes.FailedPrecondition ||
			!strings.Contains(err.Error(), "coordinator_handoff_stale") {
			t.Fatalf("SealSession at a stale generation = %v, want coordinator_handoff_stale", err)
		}
		if resp, err := s.SealSession(ctx, sealReq("sess-a", 3, 60)); err != nil || !resp.GetSealed() {
			t.Fatalf("SealSession at the fenced generation = %v, %v", resp, err)
		}
		fence(4)
		if err := s.writeSessionEnvelope(rt, "sess-a", []byte(`{"type":"message","id":"m"}`)); !errors.Is(err, errSessionSealed) {
			t.Fatalf("message after a later fence = %v, want the seal to stand", err)
		}
		if _, err := s.UnsealSession(ctx, &adapterv1.UnsealSessionRequest{
			SessionId: &adapterv1.SessionId{Value: "sess-a"}, CoordinationGeneration: 3,
		}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("UnsealSession at the superseded generation = %v, want FailedPrecondition", err)
		}
		if _, err := s.UnsealSession(ctx, &adapterv1.UnsealSessionRequest{
			SessionId: &adapterv1.SessionId{Value: "sess-a"}, CoordinationGeneration: 4,
		}); err != nil {
			t.Fatalf("UnsealSession at the new generation: %v", err)
		}
		if err := s.writeSessionEnvelope(rt, "sess-a", []byte(`{"type":"message","id":"m"}`)); err != nil {
			t.Fatalf("message after the unseal: %v", err)
		}
	})
}

// toolCallServer is sealServer whose shared runtime process has served only
// the named session, so the intra-pod MCP surface resolves calls to it.
func toolCallServer(t *testing.T, sessionID string) (*Server, *tapRuntime, *manualClock) {
	t.Helper()
	s, rt, clk := sealServer(t, sessionID)
	if got := s.soleSession(); got != sessionID {
		t.Fatalf("sole session = %q, want %q", got, sessionID)
	}
	return s, rt, clk
}

// spec: §4.7.1 (Role and Gateway RPC Contract), §28.5.3 (CH-MCP-PLATFORM,
// CH-MCP-CONNECTOR), §9.2 (Elicitation Chain)
// A platform or connector call in flight keeps its session unsealed with idle
// age 0 for as long as it blocks, the window restarts at its return,
// concurrent calls keep the count above zero until all return, a call on a
// two-slot server is refused with no entry changed, a call that outlives its
// entry changes no recreated entry, and List is not activity.
func TestToolCallInFlightKeepsTheSessionUnsealed_spec_4_7_1(t *testing.T) {
	ctx := context.Background()
	t.Run("in flight, then the window restarts at the return", func(t *testing.T) {
		s, _, clk := toolCallServer(t, "sess-a")
		release := make(chan struct{})
		fwd := &blockingForwarder{release: release, entered: make(chan struct{}, 2)}
		plat := &platformToolProvider{forwarder: fwd, server: s}
		conn := &connectorToolProvider{forwarder: fwd, server: s, connectorID: "c1"}
		done := make(chan struct{}, 2)
		go func() { _, _ = plat.Call(ctx, "lenny/request_elicitation", nil); done <- struct{}{} }()
		go func() { _, _ = conn.Call(ctx, "search", nil); done <- struct{}{} }()
		<-fwd.entered
		<-fwd.entered
		clk.advance(time.Hour)
		resp := mustSeal(t, s, "sess-a", 60)
		if resp.GetSealed() || resp.GetIdleAgeSeconds() != 0 {
			t.Fatalf("with calls in flight: sealed=%v age=%d, want not sealed with age 0", resp.GetSealed(), resp.GetIdleAgeSeconds())
		}
		release <- struct{}{}
		<-done
		if mustSeal(t, s, "sess-a", 60).GetSealed() {
			t.Fatal("sealed while the second call is still in flight")
		}
		release <- struct{}{}
		<-done
		if st := entryOf(t, s, "sess-a"); st.toolCalls != 0 {
			t.Fatalf("toolCalls = %d after every call returned, want 0", st.toolCalls)
		}
		clk.advance(59 * time.Second)
		if mustSeal(t, s, "sess-a", 60).GetSealed() {
			t.Fatal("sealed 59s after the last call returned")
		}
		clk.advance(time.Second)
		if !mustSeal(t, s, "sess-a", 60).GetSealed() {
			t.Fatal("not sealed one window after the last call returned")
		}
	})
	t.Run("List is not activity", func(t *testing.T) {
		s, _, clk := toolCallServer(t, "sess-a")
		fwd := &blockingForwarder{}
		plat := &platformToolProvider{forwarder: fwd, server: s}
		clk.advance(time.Minute)
		if _, err := plat.List(ctx); err != nil {
			t.Fatalf("List: %v", err)
		}
		if !mustSeal(t, s, "sess-a", 60).GetSealed() {
			t.Fatal("List kept the session unsealed")
		}
	})
	t.Run("a two-slot server refuses the call with no entry changed", func(t *testing.T) {
		s, _, clk := sealServer(t, "sess-a", "sess-b")
		plat := &platformToolProvider{forwarder: &blockingForwarder{}, server: s}
		a, b := entryOf(t, s, "sess-a"), entryOf(t, s, "sess-b")
		ta, tb := lastActivity(s, a), lastActivity(s, b)
		clk.advance(time.Minute)
		if _, err := plat.Call(ctx, "x", nil); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("Call on a two-slot server = %v, want FailedPrecondition", err)
		}
		if a.toolCalls != 0 || b.toolCalls != 0 || !lastActivity(s, a).Equal(ta) || !lastActivity(s, b).Equal(tb) {
			t.Fatal("a refused call changed an entry")
		}
	})
	t.Run("a call that outlives its entry changes no recreated entry", func(t *testing.T) {
		s, _, _ := toolCallServer(t, "sess-a")
		_, end, err := s.beginToolCall()
		if err != nil {
			t.Fatalf("beginToolCall: %v", err)
		}
		old := entryOf(t, s, "sess-a")
		s.mu.Lock()
		s.deregisterSlotLocked("sess-a")
		s.mu.Unlock()
		if _, err := s.claimSessionSlot("sess-a", slotResolve{allowCreate: true}, false, false); err != nil {
			t.Fatalf("recreate: %v", err)
		}
		fresh := entryOf(t, s, "sess-a")
		end()
		end()
		if fresh.toolCalls != 0 {
			t.Fatalf("recreated entry toolCalls = %d, want 0", fresh.toolCalls)
		}
		if old.toolCalls != 0 {
			t.Fatalf("old entry toolCalls = %d, want 0 after one end", old.toolCalls)
		}
	})
}

// blockingForwarder is a platform and connector forwarder whose calls block
// until released.
type blockingForwarder struct {
	release chan struct{}
	entered chan struct{}
}

func (f *blockingForwarder) wait() {
	if f.entered != nil {
		f.entered <- struct{}{}
	}
	if f.release != nil {
		<-f.release
	}
}

func (f *blockingForwarder) ListPlatformTools(context.Context, string) ([]mcp.Tool, error) {
	return nil, nil
}

func (f *blockingForwarder) CallPlatformTool(context.Context, string, string, json.RawMessage) (json.RawMessage, error) {
	f.wait()
	return json.RawMessage(`{}`), nil
}

func (f *blockingForwarder) ListSessionConnectors(context.Context, string) ([]mcp.ConnectorRef, error) {
	return nil, nil
}

func (f *blockingForwarder) ListConnectorTools(context.Context, string, string) ([]mcp.Tool, error) {
	return nil, nil
}

func (f *blockingForwarder) CallConnectorTool(context.Context, string, string, string, json.RawMessage) (json.RawMessage, error) {
	f.wait()
	return json.RawMessage(`{}`), nil
}
