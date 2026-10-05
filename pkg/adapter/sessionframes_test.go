// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/ackruntime"
)

// frameRuntime is a RuntimeProcess that records, in one ordered log, every
// Start ("start"), Interrupt ("interrupt") and Close ("close"), and every
// frame written to it as "<type>@running" or "<type>@idle". The suffix is
// whether the adapter's §4.7.1 rule-8 record held the session
// (runtimeHoldsLocked) at the moment the frame reached the runtime, which
// is the Point column of the §28.5.3 Session frame writes table: a start's
// session_start and an open sequence's session_end land before the record,
// and a teardown's session_end lands after it.
//
// onWrite, when set, runs after a frame is recorded and before
// WriteEnvelope returns, which lets a test interleave a registry change
// with the open sequence at the point the frame reaches the runtime. ack,
// when set, receives every frame too and answers it on Output, for a row
// whose start waits for session_started; without it Output is a closed
// channel.
type frameRuntime struct {
	mu       sync.Mutex
	events   []string
	frames   [][]byte
	writeErr error
	onWrite  func(frame []byte)
	holds    func(sessionID string) bool
	ack      *ackruntime.Runtime
}

func (r *frameRuntime) record(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

// recordHolds installs the rule-8 record probe frameServer wires to the
// Server under test.
func (r *frameRuntime) recordHolds(holds func(string) bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.holds = holds
}

func (r *frameRuntime) Start(context.Context, string) error {
	r.record("start")
	return nil
}

func (r *frameRuntime) WriteEnvelope(sessionID string, envelope []byte) error {
	r.mu.Lock()
	if r.writeErr != nil {
		err := r.writeErr
		r.mu.Unlock()
		return err
	}
	holds := r.holds
	r.mu.Unlock()
	point := "idle"
	if holds != nil && holds(sessionID) {
		point = "running"
	}
	r.mu.Lock()
	r.events = append(r.events, jsonlFrameType(envelope)+"@"+point)
	r.frames = append(r.frames, append([]byte(nil), envelope...))
	hook, ack := r.onWrite, r.ack
	r.mu.Unlock()
	if ack != nil {
		_ = ack.WriteEnvelope(sessionID, envelope)
	}
	if hook != nil {
		hook(envelope)
	}
	return nil
}

func (r *frameRuntime) Output(ctx context.Context, sessionID string) (<-chan []byte, error) {
	if r.ack != nil {
		return r.ack.Output(ctx, sessionID)
	}
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func (r *frameRuntime) Interrupt(context.Context, string, bool) error {
	r.record("interrupt")
	return nil
}

func (r *frameRuntime) Close(context.Context, string) error {
	r.record("close")
	return nil
}

func (r *frameRuntime) log() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func (r *frameRuntime) written() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]byte(nil), r.frames...)
}

// sdkWarmFrameRuntime is frameRuntime with the §6.1 SDK-warm surface, which
// records its ConfigureWorkspace as "configure" and its DemoteSDK as
// "demote" in the same log.
type sdkWarmFrameRuntime struct {
	frameRuntime
}

func (r *sdkWarmFrameRuntime) PreConnect(context.Context) error { return nil }

func (r *sdkWarmFrameRuntime) ConfigureWorkspace(context.Context, string, string) error {
	r.record("configure")
	return nil
}

func (r *sdkWarmFrameRuntime) DemoteSDK(context.Context) error {
	r.record("demote")
	return nil
}

// frameServer returns a Server wired to a fresh workspace base and rt, and
// points rt's rule-8 record probe at the Server. WriteEnvelope is never
// called under s.mu, so the probe takes the lock itself.
func frameServer(t *testing.T, rt RuntimeProcess) *Server {
	t.Helper()
	s := New("frames-test")
	s.WorkspaceBase = t.TempDir()
	s.Runtime = rt
	if r, ok := rt.(interface{ recordHolds(func(string) bool) }); ok {
		r.recordHolds(func(sessionID string) bool {
			s.mu.Lock()
			defer s.mu.Unlock()
			return s.runtimeHoldsLocked(sessionID)
		})
	}
	return s
}

// ackFrameServer is frameServer over a runtime whose CH-RUNTIMEOPS
// connection completed its capability handshake, so every start it runs
// waits for session_started, which rt.ack answers by its reply policy. It
// returns the handshaken CH-RUNTIMEOPS peer.
func ackFrameServer(t *testing.T, rt *frameRuntime) (*Server, *fakeRuntime) {
	t.Helper()
	rt.ack = ackruntime.New(t)
	lc, peer := startRuntimeOps(t)
	peer.handshake()
	awaitHandshake(t, lc)
	s := frameServer(t, rt)
	s.Lifecycle = lc
	s.SessionStartAckTimeout = 200 * time.Millisecond
	return s, peer
}

func frameStartReq(sessionID string) *adapterv1.StartSessionRequest {
	return &adapterv1.StartSessionRequest{SessionId: &adapterv1.SessionId{Value: sessionID}}
}

func frameResumeReq(sessionID string) *adapterv1.ResumeRequest {
	return &adapterv1.ResumeRequest{
		SessionId:    &adapterv1.SessionId{Value: sessionID},
		BindAttempt:  "attempt-a",
		CheckpointId: "ckpt-1",
	}
}

func frameConfigureReq(s *Server, sessionID string) *adapterv1.ConfigureWorkspaceRequest {
	return &adapterv1.ConfigureWorkspaceRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Cwd:       s.WorkspaceBase,
	}
}

// decodeFrame decodes one JSONL frame into its members.
func decodeFrame(t *testing.T, frame []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(frame, &m); err != nil {
		t.Fatalf("frame %s is not a JSON object: %v", frame, err)
	}
	return m
}

func equalLog(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// expectLog fails the test when rt's log is not exactly want.
func expectLog(t *testing.T, rt *frameRuntime, want ...string) {
	t.Helper()
	if got := rt.log(); !equalLog(got, want) {
		t.Fatalf("runtime events = %v, want %v", got, want)
	}
}

// expectCode fails the test when err does not carry code.
func expectCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	if status.Code(err) != code {
		t.Fatalf("answer = %v, want %v", err, code)
	}
}

// holdsRecord reports whether the rule-8 record holds sessionID.
func holdsRecord(s *Server, sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtimeHoldsLocked(sessionID)
}

// sdkWarmServer returns a pre-connected SDK-warm Server over rt.
func sdkWarmServer(t *testing.T, rt *sdkWarmFrameRuntime, ack bool) *Server {
	t.Helper()
	var s *Server
	if ack {
		s, _ = ackFrameServer(t, &rt.frameRuntime)
		s.Runtime = rt
	} else {
		s = frameServer(t, rt)
	}
	if err := s.PreConnect(context.Background()); err != nil {
		t.Fatalf("PreConnect: %v", err)
	}
	return s
}

// shortCtx is a context whose deadline a guard wait outlives.
func shortCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}

// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start) — a session with no
// experiment, tracing or LLM context writes the three context members as
// JSON null, and a session the adapter provisioned no credential file for
// omits credentialsPath rather than writing it empty.
func TestSessionStartFrameNullContextAndNoCredentialsPath_spec_28_5_3(t *testing.T) {
	rt := &frameRuntime{}
	s := frameServer(t, rt)
	s.CredentialsDir = t.TempDir()
	if _, err := s.StartSession(context.Background(), frameStartReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	frames := rt.written()
	if len(frames) != 1 {
		t.Fatalf("frames written = %d, want the one session_start", len(frames))
	}
	m := decodeFrame(t, frames[0])
	if m["type"] != "session_start" || m["sessionId"] != "sess-1" {
		t.Fatalf("frame = %s, want session_start for sess-1", frames[0])
	}
	if id, _ := m["startId"].(string); id == "" {
		t.Errorf("startId = %v, want a non-empty string", m["startId"])
	}
	for _, member := range []string{"experimentContext", "tracingContext", "llm"} {
		v, present := m[member]
		if !present || v != nil {
			t.Errorf("%s = %v (present %v), want JSON null", member, v, present)
		}
	}
	if _, present := m["credentialsPath"]; present {
		t.Errorf("credentialsPath present on a session with no provisioned credential file: %s", frames[0])
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start); §4.7.11 (item 4) —
// a session whose credentials were assigned carries its own credential
// file's path and its experiment, tracing and LLM context, and the llm
// object carries no proxy URL, which the credential file alone holds.
func TestSessionStartFrameCarriesSessionContext_spec_28_5_3(t *testing.T) {
	rt := &frameRuntime{}
	s := frameServer(t, rt)
	s.CredentialsDir = t.TempDir()
	s.mu.Lock()
	st, err := s.ensureSlotStateLocked("sess-1", slotResolve{allowCreate: true})
	if err != nil {
		s.mu.Unlock()
		t.Fatalf("ensure slot: %v", err)
	}
	st.assigned = true
	st.creds = map[string]*adapterv1.CredentialLease{
		"anthropic": {LeaseId: "l1", Provider: "anthropic", Payload: []byte(proxyLeasePayload)},
	}
	s.mu.Unlock()
	req := frameStartReq("sess-1")
	req.ExperimentContext = &adapterv1.ExperimentContext{ExperimentId: "exp-1", VariantId: "treatment"}
	req.TracingContext = map[string]string{"otel_trace_id": "0af7651916cd43dd"}
	if _, err := s.StartSession(context.Background(), req); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	frames := rt.written()
	if len(frames) != 1 {
		t.Fatalf("frames written = %d, want 1", len(frames))
	}
	m := decodeFrame(t, frames[0])
	wantPath, err := s.sessionCredentialsPath("sess-1")
	if err != nil {
		t.Fatalf("credentials path: %v", err)
	}
	if m["credentialsPath"] != wantPath || !strings.HasSuffix(wantPath, "/sess-1/credentials.json") {
		t.Errorf("credentialsPath = %v, want %s", m["credentialsPath"], wantPath)
	}
	ec, _ := m["experimentContext"].(map[string]any)
	if ec["experimentId"] != "exp-1" || ec["variantId"] != "treatment" || ec["inherited"] != false {
		t.Errorf("experimentContext = %v, want exp-1/treatment/not inherited", m["experimentContext"])
	}
	tc, _ := m["tracingContext"].(map[string]any)
	if tc["otel_trace_id"] != "0af7651916cd43dd" {
		t.Errorf("tracingContext = %v, want the request's map", m["tracingContext"])
	}
	llm, _ := m["llm"].(map[string]any)
	if llm["deliveryMode"] != "proxy" || llm["dialect"] != "anthropic" || llm["apiKeyEnv"] != "ANTHROPIC_API_KEY" {
		t.Errorf("llm = %v, want the proxy lease's configuration", m["llm"])
	}
	if _, present := llm["baseUrl"]; present {
		t.Errorf("llm carries baseUrl: %s", frames[0])
	}
}

// spec: 28.5.3 (CH-MSGSOCK session frame writes), 4.7.1 (role and gateway RPC contract), 5.2 (slot-identifier reclaim hold)
// One subtest per row of the §28.5.3 Session frame writes table, each
// asserting the row's Frame column (which frame reaches the runtime, if
// any) and its Point column (whether the rule-8 record held the session
// when the frame was written, and where the frame falls relative to the
// runtime's Start, Close, ConfigureWorkspace and DemoteSDK). The variants
// the table implies are subtests of their row: the guard-expired start and
// compensating Shutdown, the DemoteSDK whose guard acquisition expires,
// the acknowledgement failures on a stale startId and on error, the
// hold-timeout pass 1 that lands between the two confirmations, a failed
// session_end write, and a start with no AssignCredentials.
func TestSessionFrameWriteMatrix_spec_28_5_3(t *testing.T) {
	ctx := context.Background()

	t.Run("pod-warm start writes session_start before the record", func(t *testing.T) {
		rt := &frameRuntime{}
		s := frameServer(t, rt)
		s.CredentialsDir = t.TempDir()
		if _, err := s.StartSession(ctx, frameStartReq("sess-1")); err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		expectLog(t, rt, "start", "session_start@idle")
		if !holdsRecord(s, "sess-1") {
			t.Error("the start did not take the rule-8 record")
		}
		// No AssignCredentials ran for the entry, so no credential file was
		// provisioned and the frame omits the member rather than naming a
		// path the runtime cannot read.
		if _, present := decodeFrame(t, rt.written()[0])["credentialsPath"]; present {
			t.Errorf("session_start of an entry with no AssignCredentials carries credentialsPath: %s", rt.written()[0])
		}
	})

	t.Run("SDK-warm start writes session_start after ConfigureWorkspace and before the record", func(t *testing.T) {
		rt := &sdkWarmFrameRuntime{}
		s := sdkWarmServer(t, rt, false)
		if _, err := s.ConfigureWorkspace(ctx, frameConfigureReq(s, "sess-1")); err != nil {
			t.Fatalf("ConfigureWorkspace: %v", err)
		}
		expectLog(t, &rt.frameRuntime, "configure", "session_start@idle")
		if !holdsRecord(s, "sess-1") {
			t.Error("the SDK-warm start did not take the rule-8 record")
		}
	})

	t.Run("SDK-warm repeat writes nothing", func(t *testing.T) {
		rt := &sdkWarmFrameRuntime{}
		s := sdkWarmServer(t, rt, false)
		for i := 0; i < 2; i++ {
			if _, err := s.ConfigureWorkspace(ctx, frameConfigureReq(s, "sess-1")); err != nil {
				t.Fatalf("ConfigureWorkspace #%d: %v", i+1, err)
			}
		}
		expectLog(t, &rt.frameRuntime, "configure", "session_start@idle", "configure")
	})

	t.Run("resume writes session_start under its held guard before the record", func(t *testing.T) {
		rt := &frameRuntime{}
		s := frameServer(t, rt)
		rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if _, err := s.Resume(rctx, frameResumeReq("sess-1")); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		expectLog(t, rt, "start", "session_start@idle")
		if !holdsRecord(s, "sess-1") {
			t.Error("the resumed session did not reach running")
		}
	})

	t.Run("first confirmation refused writes nothing", func(t *testing.T) {
		rt := &frameRuntime{}
		s := frameServer(t, rt)
		claim, err := s.claimSessionSlot("sess-1", slotResolve{allowCreate: true}, false, false)
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		s.mu.Lock()
		s.slots["sess-1"] = &slotState{sessionID: "sess-1", started: true, bindAttempt: claim.attempt}
		s.mu.Unlock()
		confirmed, err := s.openRuntimeSession(ctx, "sess-1", claim, manifestInputs{}, false)
		if err != nil || confirmed {
			t.Fatalf("openRuntimeSession = (%v, %v), want (false, nil)", confirmed, err)
		}
		expectLog(t, rt)
		if holdsRecord(s, "sess-1") {
			t.Error("the refused start took the rule-8 record")
		}
	})

	t.Run("second confirmation refused writes session_end before the start backs out", func(t *testing.T) {
		t.Run("pod-warm", func(t *testing.T) {
			rt := &frameRuntime{}
			s := frameServer(t, rt)
			rt.onWrite = deregisterOnSessionStart(s, "sess-1")
			_, err := s.StartSession(ctx, frameStartReq("sess-1"))
			expectCode(t, err, codes.Aborted)
			expectLog(t, rt, "start", "session_start@idle", "session_end@idle", "close")
			if holdsRecord(s, "sess-1") {
				t.Error("the refused start took the rule-8 record")
			}
		})
		t.Run("SDK-warm session_end precedes DemoteSDK", func(t *testing.T) {
			rt := &sdkWarmFrameRuntime{}
			s := sdkWarmServer(t, rt, false)
			rt.onWrite = deregisterOnSessionStart(s, "sess-1")
			_, err := s.ConfigureWorkspace(ctx, frameConfigureReq(s, "sess-1"))
			expectCode(t, err, codes.Aborted)
			expectLog(t, &rt.frameRuntime, "configure", "session_start@idle", "session_end@idle", "demote")
		})
		t.Run("hold-timeout pass 1 removes the entry after the session_start write", func(t *testing.T) {
			rt := &frameRuntime{}
			s := frameServer(t, rt)
			var members []heldSession
			rt.onWrite = func(frame []byte) {
				if jsonlFrameType(frame) == sessionStartFrameType {
					members = s.deregisterStartedSessions()
				}
			}
			_, err := s.StartSession(ctx, frameStartReq("sess-1"))
			expectCode(t, err, codes.Aborted)
			if len(members) != 1 {
				t.Fatalf("pass 1 removed %d entries, want the starting one", len(members))
			}
			// Pass 2 runs once the open sequence has released the guard,
			// and writes no frame of its own.
			for _, m := range members {
				s.terminateHeldSession(ctx, m)
			}
			expectLog(t, rt, "start", "session_start@idle", "session_end@idle", "close", "close")
		})
	})

	t.Run("acknowledgement failure writes session_end and fails the start", func(t *testing.T) {
		t.Run("answered only with an earlier start's startId", func(t *testing.T) {
			rt := &frameRuntime{}
			s, _ := ackFrameServer(t, rt)
			if _, err := s.StartSession(ctx, frameStartReq("sess-1")); err != nil {
				t.Fatalf("first StartSession: %v", err)
			}
			earlier := decodeFrame(t, rt.written()[0])["startId"].(string)
			if _, err := s.Shutdown(ctx, unconditionalShutdownReq("sess-1")); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
			rt.ack.SetReply(func(in []byte) []byte { return answerWith(in, earlier, "") })
			_, err := s.StartSession(ctx, frameStartReq("sess-1"))
			expectCode(t, err, codes.Internal)
			expectLog(t, rt,
				"start", "session_start@idle", "session_end@running", "close",
				"start", "session_start@idle", "session_end@idle", "close")
			if n := s.slotCount(); n != 0 || holdsRecord(s, "sess-1") {
				t.Errorf("failed start left %d entries (record %v), want none", n, holdsRecord(s, "sess-1"))
			}
		})
		t.Run("session_started carries error", func(t *testing.T) {
			rt := &frameRuntime{}
			s, _ := ackFrameServer(t, rt)
			rt.ack.SetReply(func(in []byte) []byte { return answerWith(in, "", "RUNTIME_ERROR") })
			_, err := s.StartSession(ctx, frameStartReq("sess-1"))
			expectCode(t, err, codes.Internal)
			expectLog(t, rt, "start", "session_start@idle", "session_end@idle", "close")
		})
		t.Run("SDK-warm start answers Internal and the DemoteSDK fallback writes nothing", func(t *testing.T) {
			rt := &sdkWarmFrameRuntime{}
			s := sdkWarmServer(t, rt, true)
			rt.ack.SetReply(ackruntime.Withhold)
			_, err := s.ConfigureWorkspace(ctx, frameConfigureReq(s, "sess-1"))
			expectCode(t, err, codes.Internal)
			if _, err := s.DemoteSDK(ctx, &adapterv1.DemoteSDKRequest{}); err != nil {
				t.Fatalf("DemoteSDK fallback: %v", err)
			}
			expectLog(t, &rt.frameRuntime, "configure", "session_start@idle", "session_end@idle", "demote")
		})
	})

	t.Run("start that fails before the confirmation writes nothing", func(t *testing.T) {
		writeErr := errors.New("connection reset")
		t.Run("pod-warm session_start write fails", func(t *testing.T) {
			rt := &frameRuntime{writeErr: writeErr}
			s := frameServer(t, rt)
			_, err := s.StartSession(ctx, frameStartReq("sess-1"))
			expectCode(t, err, codes.Internal)
			expectLog(t, rt, "start", "close")
			if n := s.slotCount(); n != 0 || holdsRecord(s, "sess-1") {
				t.Errorf("failed start left %d entries (record %v), want none", n, holdsRecord(s, "sess-1"))
			}
		})
		t.Run("resume session_start write fails", func(t *testing.T) {
			rt := &frameRuntime{writeErr: writeErr}
			s := frameServer(t, rt)
			_, err := s.Resume(ctx, frameResumeReq("sess-1"))
			expectCode(t, err, codes.Internal)
			expectLog(t, rt, "start", "close")
			if n := s.slotCount(); n != 0 {
				t.Errorf("registry holds %d entries after the failed resume, want 0", n)
			}
		})
		t.Run("SDK-warm session_start write fails", func(t *testing.T) {
			rt := &sdkWarmFrameRuntime{frameRuntime{writeErr: writeErr}}
			s := sdkWarmServer(t, rt, false)
			_, err := s.ConfigureWorkspace(ctx, frameConfigureReq(s, "sess-1"))
			expectCode(t, err, codes.Internal)
			expectLog(t, &rt.frameRuntime, "configure")
			if n := s.slotCount(); n != 0 || holdsRecord(s, "sess-1") {
				t.Errorf("failed SDK-warm start left %d entries (record %v), want none", n, holdsRecord(s, "sess-1"))
			}
		})
		t.Run("guard acquisition expires", func(t *testing.T) {
			rt := &frameRuntime{}
			s := frameServer(t, rt)
			t.Cleanup(holdGuard(t, s, "sess-1"))
			_, err := s.StartSession(shortCtx(t), frameStartReq("sess-1"))
			expectCode(t, err, codes.Internal)
			expectLog(t, rt, "start", "close")
			if n := s.slotCount(); n != 0 {
				t.Errorf("registry holds %d entries after the failed start, want 0", n)
			}
		})
	})

	t.Run("Shutdown of a running session writes session_end after the record and before Close", func(t *testing.T) {
		rt := &frameRuntime{}
		s := frameServer(t, rt)
		if _, err := s.StartSession(ctx, frameStartReq("sess-1")); err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		if _, err := s.Shutdown(ctx, unconditionalShutdownReq("sess-1")); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		if _, err := s.StartSession(ctx, frameStartReq("sess-1")); err != nil {
			t.Fatalf("second StartSession: %v", err)
		}
		expectLog(t, rt, "start", "session_start@idle", "session_end@running", "close", "start", "session_start@idle")
		frames := rt.written()
		if end := decodeFrame(t, frames[1]); len(end) != 2 || end["sessionId"] != "sess-1" {
			t.Errorf("session_end = %s, want type and sessionId only", frames[1])
		}
		if first, second := decodeFrame(t, frames[0])["startId"], decodeFrame(t, frames[2])["startId"]; first == second {
			t.Errorf("both starts of sess-1 carry startId %v, want distinct values", first)
		}
	})

	t.Run("compensating Shutdown whose guard acquisition expires still writes session_end", func(t *testing.T) {
		rt := &frameRuntime{}
		s := frameServer(t, rt)
		assignForAttempt(t, s, "sess-1", "attempt-a")
		if _, err := s.StartSession(ctx, frameStartReq("sess-1")); err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		t.Cleanup(holdGuard(t, s, "sess-1"))
		resp, err := s.Shutdown(shortCtx(t), &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: "sess-1"}, BindAttempt: "attempt-a",
		})
		if err != nil || resp.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED {
			t.Fatalf("Shutdown = (%v, %v), want RECLAIMED", resp, err)
		}
		expectLog(t, rt, "start", "session_start@idle", "session_end@running", "close")
	})

	t.Run("Shutdown of an entry that never reached running, or of no entry, writes nothing", func(t *testing.T) {
		rt := &frameRuntime{}
		s := frameServer(t, rt)
		if _, err := s.ensureSlotPaths("sess-1", slotResolve{allowCreate: true}); err != nil {
			t.Fatalf("register slot: %v", err)
		}
		for _, id := range []string{"sess-1", "sess-absent"} {
			if _, err := s.Shutdown(ctx, unconditionalShutdownReq(id)); err != nil {
				t.Fatalf("Shutdown(%s): %v", id, err)
			}
		}
		expectLog(t, rt)
	})

	t.Run("a failed session_end write does not fail the teardown", func(t *testing.T) {
		rt := &frameRuntime{}
		s := frameServer(t, rt)
		if _, err := s.StartSession(ctx, frameStartReq("sess-1")); err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		rt.mu.Lock()
		rt.writeErr = errors.New("broken pipe")
		rt.mu.Unlock()
		resp, err := s.Shutdown(ctx, unconditionalShutdownReq("sess-1"))
		if err != nil || !resp.GetExitedCleanly() {
			t.Fatalf("Shutdown = (%v, %v), want a clean teardown", resp, err)
		}
		expectLog(t, rt, "start", "session_start@idle", "close")
	})

	t.Run("DemoteSDK of a running session writes session_end before the demotion", func(t *testing.T) {
		rt := &sdkWarmFrameRuntime{}
		s := sdkWarmServer(t, rt, false)
		if _, err := s.ConfigureWorkspace(ctx, frameConfigureReq(s, "sess-1")); err != nil {
			t.Fatalf("ConfigureWorkspace: %v", err)
		}
		if _, err := s.DemoteSDK(ctx, &adapterv1.DemoteSDKRequest{}); err != nil {
			t.Fatalf("DemoteSDK: %v", err)
		}
		expectLog(t, &rt.frameRuntime, "configure", "session_start@idle", "session_end@running", "demote")
		if n := s.slotCount(); n != 0 || s.SDKWarmReady() {
			t.Errorf("after DemoteSDK: %d entries, SDK-warm ready %v; want none and false", n, s.SDKWarmReady())
		}
	})

	t.Run("DemoteSDK with no running session writes nothing", func(t *testing.T) {
		rt := &sdkWarmFrameRuntime{}
		s := sdkWarmServer(t, rt, false)
		if _, err := s.DemoteSDK(ctx, &adapterv1.DemoteSDKRequest{}); err != nil {
			t.Fatalf("DemoteSDK: %v", err)
		}
		expectLog(t, &rt.frameRuntime, "demote")
	})

	t.Run("DemoteSDK whose guard acquisition expires fails closed", func(t *testing.T) {
		rt := &sdkWarmFrameRuntime{}
		s := sdkWarmServer(t, rt, false)
		if _, err := s.ConfigureWorkspace(ctx, frameConfigureReq(s, "sess-1")); err != nil {
			t.Fatalf("ConfigureWorkspace: %v", err)
		}
		t.Cleanup(holdGuard(t, s, "sess-1"))
		_, err := s.DemoteSDK(shortCtx(t), &adapterv1.DemoteSDKRequest{})
		expectCode(t, err, codes.DeadlineExceeded)
		expectLog(t, &rt.frameRuntime, "configure", "session_start@idle")
		if n := s.slotCount(); n != 1 || !s.SDKWarmReady() || !holdsRecord(s, "sess-1") {
			t.Errorf("failed DemoteSDK changed state: %d entries, SDK-warm ready %v, record %v",
				n, s.SDKWarmReady(), holdsRecord(s, "sess-1"))
		}
	})

	t.Run("coordinator hold timeout writes nothing", func(t *testing.T) {
		rt := &frameRuntime{}
		s := frameServer(t, rt)
		if _, err := s.StartSession(ctx, frameStartReq("sess-1")); err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		s.hold.mu.Lock()
		s.hold.active = true
		s.hold.mu.Unlock()
		s.onHoldTimeout()
		expectLog(t, rt, "start", "session_start@idle", "close")
	})

	t.Run("interrupt writes nothing", func(t *testing.T) {
		rt := &frameRuntime{}
		s := frameServer(t, rt)
		if _, err := s.StartSession(ctx, frameStartReq("sess-1")); err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		for _, mode := range []adapterv1.InterruptRequest_Mode{
			adapterv1.InterruptRequest_MODE_CLEAN, adapterv1.InterruptRequest_MODE_HARD,
		} {
			if _, err := s.Interrupt(ctx, &adapterv1.InterruptRequest{
				SessionId: &adapterv1.SessionId{Value: "sess-1"}, Mode: mode, DeadlineMs: 1000,
			}); err != nil {
				t.Fatalf("Interrupt(%v): %v", mode, err)
			}
		}
		for _, ev := range rt.log()[2:] {
			if strings.HasPrefix(ev, "session_") {
				t.Errorf("an interrupt wrote %s", ev)
			}
		}
	})

	t.Run("type mcp runtime writes no session frame on any path", func(t *testing.T) {
		rt := &frameRuntime{}
		s := frameServer(t, rt)
		s.RuntimeKind = RuntimeKindMCP
		if _, err := s.StartSession(ctx, frameStartReq("sess-1")); err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		if _, err := s.Shutdown(ctx, unconditionalShutdownReq("sess-1")); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		if err := s.writeSessionStart("sess-2", s.nextStartID(), manifestInputs{}); err != nil {
			t.Fatalf("writeSessionStart: %v", err)
		}
		s.writeSessionEnd("sess-2")
		expectLog(t, rt, "start", "close")
	})
}

// deregisterOnSessionStart returns an onWrite hook that removes sessionID's
// entry without the slot guard when its session_start reaches the runtime,
// as the hold-timeout termination's pass 1 and a Shutdown whose guard
// acquisition expired do. That is the removal an open sequence can meet
// between its two confirmations.
func deregisterOnSessionStart(s *Server, sessionID string) func([]byte) {
	return func(frame []byte) {
		if jsonlFrameType(frame) != sessionStartFrameType {
			return
		}
		s.mu.Lock()
		s.deregisterSlotLocked(sessionID)
		s.mu.Unlock()
	}
}

// assignForAttempt registers sessionID's entry under attempt through
// AssignCredentials, so a compensating Shutdown carrying the attempt's
// token matches it.
func assignForAttempt(t *testing.T, s *Server, sessionID, attempt string) {
	t.Helper()
	if s.CredentialsDir == "" {
		s.CredentialsDir = t.TempDir()
	}
	if _, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
		BindAttempt: attempt,
		SessionId:   &adapterv1.SessionId{Value: sessionID},
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic": {LeaseId: "l-" + attempt, Provider: "anthropic", Payload: []byte("{}")},
		},
	}); err != nil {
		t.Fatalf("AssignCredentials: %v", err)
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start) — a session
// identifier that is not a safe path segment fails the frame build rather
// than naming a path outside the slot tree.
func TestSessionStartFrameRejectsUnsafeSessionIdentifier_spec_28_5_3(t *testing.T) {
	s := frameServer(t, &frameRuntime{})
	s.CredentialsDir = t.TempDir()
	s.mu.Lock()
	s.slots = map[string]*slotState{"../escape": {sessionID: "../escape", assigned: true}}
	s.mu.Unlock()
	if _, err := s.buildSessionStartFrame("../escape", "1", manifestInputs{}); err == nil {
		t.Error("buildSessionStartFrame accepted an unsafe session identifier")
	}
}

const proxyLeasePayload = `{"deliveryMode":"proxy","materializedConfig":` +
	`{"proxyUrl":"https://proxy.lenny-system/v1","proxyDialect":"anthropic","leaseToken":"lease-tok"}}`

const directLeasePayload = `{"deliveryMode":"direct","materializedConfig":{"apiKey":"sk-x"}}`

// setSessionLeasesForTest puts the named session's own §6.1 lease set in
// place, which is where the session_start llm object is derived from.
// assigned records that AssignCredentials ran for the session, which is
// what makes the frame carry credentialsPath. spec: §6.1.
func setSessionLeasesForTest(t *testing.T, srv *Server, sessionID string, assigned bool, leases map[string]*adapterv1.CredentialLease) {
	t.Helper()
	srv.mu.Lock()
	defer srv.mu.Unlock()
	st, err := srv.ensureSlotStateLocked(sessionID, slotResolve{allowCreate: true})
	if err != nil {
		t.Fatalf("ensure slot state for %s: %v", sessionID, err)
	}
	st.sessionID = sessionID
	st.assigned = assigned
	st.creds = leases
}

// buildFrameMembers builds one start's session_start for sessionID and
// returns it decoded into its JSON members, so a case asserts the wire
// form a runtime reads rather than the Go struct.
func buildFrameMembers(t *testing.T, s *Server, sessionID string, in manifestInputs) map[string]any {
	t.Helper()
	f, err := s.buildSessionStartFrame(sessionID, "1", in)
	if err != nil {
		t.Fatalf("buildSessionStartFrame(%s): %v", sessionID, err)
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("encode session_start: %v", err)
	}
	return decodeFrame(t, b)
}

// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start); §4.7.11 (item 4) —
// a proxy-mode lease yields an llm object with the dialect and canonical
// API-key env var the runtime configures its SDK with, and no proxy URL,
// which stays in the session's credential file alone.
func TestSessionStartLLMFromProxyLease_spec_28_5_3(t *testing.T) {
	llm := manifestLLMFromPayload([]byte(proxyLeasePayload))
	if llm == nil {
		t.Fatal("manifestLLMFromPayload(proxy) = nil, want an llm object")
	}
	if llm.DeliveryMode != "proxy" || llm.Dialect != "anthropic" || llm.APIKeyEnv != "ANTHROPIC_API_KEY" {
		t.Errorf("llm = %+v, want proxy / anthropic / ANTHROPIC_API_KEY", llm)
	}
	raw, err := json.Marshal(llm)
	if err != nil {
		t.Fatalf("encode llm: %v", err)
	}
	if fieldPresent(t, raw, "baseUrl") {
		t.Errorf("proxy-mode llm carries baseUrl: %s; the proxy URL belongs to the credential file alone", raw)
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start) — a direct-mode
// lease omits the dialect and the API-key variable, because the runtime
// uses the upstream provider's native SDK.
func TestSessionStartLLMFromDirectLease_spec_28_5_3(t *testing.T) {
	llm := manifestLLMFromPayload([]byte(directLeasePayload))
	if llm == nil {
		t.Fatal("manifestLLMFromPayload(direct) = nil, want an llm object")
	}
	if llm.DeliveryMode != "direct" || llm.Dialect != "" || llm.APIKeyEnv != "" {
		t.Errorf("direct-mode llm = %+v, want deliveryMode direct and no dialect or apiKeyEnv", llm)
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start) — a lease payload
// with no delivery mode yields no llm object.
func TestSessionStartLLMFromEmptyPayload_spec_28_5_3(t *testing.T) {
	if llm := manifestLLMFromPayload(nil); llm != nil {
		t.Errorf("manifestLLMFromPayload(nil) = %+v, want nil", llm)
	}
	if llm := manifestLLMFromPayload([]byte(`{}`)); llm != nil {
		t.Errorf("manifestLLMFromPayload(no deliveryMode) = %+v, want nil", llm)
	}
}

// spec: §4.9 (anthropic and openai dialects); §26.5 (google); §26.6
// (cursor) — each proxy dialect maps to the canonical API-key variable its
// SDK reads, and an unrecognized dialect maps to none.
func TestAPIKeyEnvForDialect_spec_4_9(t *testing.T) {
	cases := map[string]string{
		"anthropic": "ANTHROPIC_API_KEY",
		"openai":    "OPENAI_API_KEY",
		"google":    "GOOGLE_API_KEY",
		"cursor":    "CURSOR_API_KEY",
		"mystery":   "",
	}
	for dialect, want := range cases {
		if got := apiKeyEnvForDialect(dialect); got != want {
			t.Errorf("apiKeyEnvForDialect(%q) = %q, want %q", dialect, got, want)
		}
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start) — the frame's llm
// member is derived from the session's own assigned lease, is JSON null
// while no lease is assigned, and in proxy mode carries no baseUrl.
func TestSessionStartFrameLLMFollowsSessionLease_spec_28_5_3(t *testing.T) {
	s := frameServer(t, &frameRuntime{})
	m := buildFrameMembers(t, s, "sess-1", manifestInputs{})
	if v, present := m["llm"]; !present || v != nil {
		t.Errorf("llm = %v (present %v), want JSON null with no lease assigned", v, present)
	}

	setSessionLeasesForTest(t, s, "sess-1", true, map[string]*adapterv1.CredentialLease{
		"anthropic": {LeaseId: "l1", Provider: "anthropic", Payload: []byte(proxyLeasePayload)},
	})
	llm, _ := buildFrameMembers(t, s, "sess-1", manifestInputs{})["llm"].(map[string]any)
	if llm["deliveryMode"] != "proxy" || llm["dialect"] != "anthropic" {
		t.Errorf("llm = %v, want the proxy lease's configuration", llm)
	}
	if _, present := llm["baseUrl"]; present {
		t.Errorf("proxy-mode llm carries baseUrl: %v", llm)
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start); §6.1 — when more
// than one provider lease is assigned, the llm member is derived from a
// deterministic, provider-sorted lease.
func TestSessionStartFrameLLMMultiProviderDeterministic_spec_28_5_3(t *testing.T) {
	s := frameServer(t, &frameRuntime{})
	setSessionLeasesForTest(t, s, "sess-1", true, map[string]*adapterv1.CredentialLease{
		"openai":    {LeaseId: "l2", Provider: "openai", Payload: []byte(directLeasePayload)},
		"anthropic": {LeaseId: "l1", Provider: "anthropic", Payload: []byte(proxyLeasePayload)},
	})
	for i := 0; i < 5; i++ {
		llm, _ := buildFrameMembers(t, s, "sess-1", manifestInputs{})["llm"].(map[string]any)
		// "anthropic" sorts before "openai", so its proxy lease drives llm.
		if llm["dialect"] != "anthropic" {
			t.Fatalf("build #%d: llm = %v, want the provider-sorted (anthropic) lease", i, llm)
		}
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start); §6.1 (per-session
// credential file) — each session's frame names that session's own
// credential file, which is the file the credential handlers write. Two
// sessions on one pod resolve to two paths.
func TestSessionStartFrameCredentialsPathIsPerSession_spec_28_5_3(t *testing.T) {
	s := frameServer(t, &frameRuntime{})
	credRoot := t.TempDir()
	s.CredentialsDir = credRoot
	lease := map[string]*adapterv1.CredentialLease{
		"anthropic": {LeaseId: "l1", Provider: "anthropic", Payload: []byte(proxyLeasePayload)},
	}
	setSessionLeasesForTest(t, s, "sess-alice", true, lease)
	setSessionLeasesForTest(t, s, "sess-bob", true, lease)

	alice, _ := buildFrameMembers(t, s, "sess-alice", manifestInputs{})["credentialsPath"].(string)
	want := filepath.Join(credRoot, "slots", "sess-alice", "credentials.json")
	if alice != want {
		t.Errorf("credentialsPath = %q, want %q", alice, want)
	}
	bob, _ := buildFrameMembers(t, s, "sess-bob", manifestInputs{})["credentialsPath"].(string)
	if bob == "" || bob == alice {
		t.Errorf("sess-bob credentialsPath = %q, want a path distinct from sess-alice's %q", bob, alice)
	}
	handlerPath, _, err := s.sessionCredentialFile("sess-alice")
	if err != nil {
		t.Fatalf("sessionCredentialFile: %v", err)
	}
	if handlerPath != alice {
		t.Errorf("credential handlers write %q but the frame names %q", handlerPath, alice)
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start) — the frame carries
// the start's tracing and experiment context as given, and writes the
// three nullable members as JSON null when the start has none, an empty
// tracing map included.
func TestSessionStartFrameTracingAndExperimentContext_spec_28_5_3(t *testing.T) {
	s := frameServer(t, &frameRuntime{})
	m := buildFrameMembers(t, s, "sess-y", manifestInputs{
		experimentContext: &adapterv1.ExperimentContext{ExperimentId: "exp_1", VariantId: "treatment", Inherited: true},
		tracingContext:    map[string]string{"langsmith_run_id": "run_abc"},
	})
	ec, _ := m["experimentContext"].(map[string]any)
	if ec["experimentId"] != "exp_1" || ec["variantId"] != "treatment" || ec["inherited"] != true {
		t.Errorf("experimentContext = %v, want exp_1/treatment inherited", m["experimentContext"])
	}
	tc, _ := m["tracingContext"].(map[string]any)
	if tc["langsmith_run_id"] != "run_abc" {
		t.Errorf("tracingContext = %v, want the langsmith run id", m["tracingContext"])
	}

	empty := buildFrameMembers(t, s, "sess-z", manifestInputs{tracingContext: map[string]string{}})
	for _, member := range []string{"experimentContext", "tracingContext", "llm"} {
		if v, present := empty[member]; !present || v != nil {
			t.Errorf("%s = %v (present %v), want JSON null for a start with no context", member, v, present)
		}
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start) — an unenrolled
// session has no experimentContext, which the frame writes as JSON null.
func TestSessionStartExperimentContextNil_spec_28_5_3(t *testing.T) {
	if got := manifestExperimentContext(nil); got != nil {
		t.Errorf("manifestExperimentContext(nil) = %v, want nil", got)
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start) — the frame's
// experimentContext carries the StartSession proto's enrollment member for
// member.
func TestSessionStartExperimentContextMapsProtoFields_spec_28_5_3(t *testing.T) {
	got := manifestExperimentContext(&adapterv1.ExperimentContext{
		ExperimentId: "exp_9", VariantId: "control", Inherited: false,
	})
	if got == nil {
		t.Fatal("manifestExperimentContext returned nil for a populated proto")
	}
	if got.ExperimentID != "exp_9" || got.VariantID != "control" || got.Inherited {
		t.Errorf("session_start experimentContext = %+v", got)
	}
}

// subscribeRecorder is frameRuntime that also logs each Output
// subscription as "subscribe" and keeps its context, so a case can assert
// where the open sequence subscribes relative to its session_start write
// and that it cancels the subscription when it returns.
type subscribeRecorder struct {
	*frameRuntime
	ctxs []context.Context
}

func (r *subscribeRecorder) Output(ctx context.Context, sessionID string) (<-chan []byte, error) {
	r.record("subscribe")
	r.mu.Lock()
	r.ctxs = append(r.ctxs, ctx)
	r.mu.Unlock()
	return r.frameRuntime.Output(ctx, sessionID)
}

// subscriptions returns the contexts of every Output call so far.
func (r *subscribeRecorder) subscriptions() []context.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]context.Context(nil), r.ctxs...)
}

// startWithheld starts sessionID on s in the background with its
// session_started withheld, and returns once the session_start reached the
// runtime, with the channel the start's answer arrives on.
func startWithheld(t *testing.T, s *Server, rt *frameRuntime, sessionID string) <-chan error {
	t.Helper()
	rt.ack.SetReply(ackruntime.Withhold)
	done := make(chan error, 1)
	go func() { done <- startSession(context.Background(), s, sessionID) }()
	waitForStartID(t, rt.ack)
	return done
}

// signalDeadlineAsync issues SignalDeadline for sessionID with a long
// remainingMs in the background, so the call waits on the session's gate
// until a transition lets it return.
func signalDeadlineAsync(s *Server, sessionID string) <-chan *adapterv1.SignalDeadlineResponse {
	out := make(chan *adapterv1.SignalDeadlineResponse, 1)
	go func() {
		resp, _ := s.SignalDeadline(context.Background(), &adapterv1.SignalDeadlineRequest{
			SessionId: &adapterv1.SessionId{Value: sessionID}, RemainingMs: 60_000,
		})
		out <- resp
	}()
	return out
}

// expectUndeliveredWithin fails the test unless the SignalDeadline answer
// arrives, undelivered, within d.
func expectUndeliveredWithin(t *testing.T, answers <-chan *adapterv1.SignalDeadlineResponse, d time.Duration) {
	t.Helper()
	select {
	case resp := <-answers:
		if resp == nil || resp.GetDelivered() {
			t.Fatalf("SignalDeadline = %+v, want undelivered", resp)
		}
	case <-time.After(d):
		t.Fatalf("SignalDeadline still waiting %s after the removal released the gate", d)
	}
}

// waitPending polls until pending reports true.
func waitPending(t *testing.T, what string, pending func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !pending() {
		if time.Now().After(deadline) {
			t.Fatalf("%s never queued on the op lock", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// boundUnstarted registers sessionID as a bound entry whose start has not
// run, so its gate stays not started for as long as the case needs.
func boundUnstarted(t *testing.T, s *Server, sessionID string) *slotState {
	t.Helper()
	s.WorkspaceBase = t.TempDir()
	assignForAttempt(t, s, sessionID, "attempt-a")
	st := s.slotStateForSession(sessionID)
	if st == nil || st.ack.current() != ackNotStarted {
		t.Fatalf("precondition: %s is not a registered entry with a not-started gate", sessionID)
	}
	return st
}

// spec: 28.5.3 (CH-MSGSOCK Outbound: session_started), 28.5.3 (CH-RUNTIMEOPS Messages), 28.5.3 (CH-MSGSOCK session frame writes)
// The rules the acknowledgement gate and the session_started wait add,
// beyond the gate transitions, the wait's start-failure arms and the
// zero-value default that TestAckGateTransitions_spec_28_5_3,
// TestOpenSequenceWaitsForSessionStarted_spec_28_5_3 and
// TestSessionStartedWaitBounds_spec_28_5_3 pin: the subscription rule of
// the wait; each session-scoped sender's own bound on its gate wait; a
// removal while an acknowledgement is pending, by a Shutdown whose guard
// acquisition expired and by the hold-timeout termination's pass 1, waking
// a waiting sender without a frame; and a Checkpoint or clean Interrupt
// queued on the pod-level op lock behind a co-tenant's upload while its
// entry is released and a successor registers under the same identifier,
// which writes no frame for the successor.
func TestSessionStartedGatesRuntimeOpsAndBoundsTheStart_spec_28_5_3(t *testing.T) {
	ctx := context.Background()

	t.Run("a waiting start subscribes before session_start and cancels the subscription on every return", func(t *testing.T) {
		for _, withhold := range []bool{false, true} {
			base := &frameRuntime{}
			s, _ := ackFrameServer(t, base)
			rt := &subscribeRecorder{frameRuntime: base}
			s.Runtime = rt
			if withhold {
				base.ack.SetReply(ackruntime.Withhold)
			}
			err := startSession(ctx, s, "sess-a")
			if (err != nil) != withhold {
				t.Fatalf("withhold=%v: StartSession = %v", withhold, err)
			}
			if got := rt.log(); len(got) < 3 || got[1] != "subscribe" || got[2] != "session_start@idle" {
				t.Fatalf("withhold=%v: runtime events = %v, want the subscription ahead of session_start", withhold, got)
			}
			subs := rt.subscriptions()
			if len(subs) != 1 || subs[0].Err() == nil {
				t.Fatalf("withhold=%v: %d subscriptions, want one whose context the sequence cancelled", withhold, len(subs))
			}
		}
	})

	t.Run("a start that does not wait opens no subscription", func(t *testing.T) {
		rt := &subscribeRecorder{frameRuntime: &frameRuntime{}}
		s := frameServer(t, rt)
		if err := startSession(ctx, s, "sess-a"); err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		if len(rt.subscriptions()) != 0 {
			t.Errorf("a start with no CH-RUNTIMEOPS handshake subscribed to the runtime output")
		}
		if st, _ := gateOf(s, "sess-a"); st != ackNotAwaiting {
			t.Errorf("gate = %s, want not_awaiting", st)
		}
	})

	t.Run("each sender's gate wait ends at its own bound", func(t *testing.T) {
		rt := &frameRuntime{}
		s, peer := ackFrameServer(t, rt)
		s.SessionStartAckTimeout = time.Minute
		s.CredentialsAckTimeout = 100 * time.Millisecond
		st := boundUnstarted(t, s, "sess-a")
		sid := &adapterv1.SessionId{Value: "sess-a"}
		within := func(name string, f func()) {
			t.Helper()
			began := time.Now()
			f()
			if d := time.Since(began); d > 5*time.Second {
				t.Errorf("%s waited %s on a gate no start settles, want its own short bound", name, d)
			}
		}
		within("checkpoint", func() {
			err := s.awaitCheckpointGate(ctx, st, &adapterv1.CheckpointStart{DeadlineMs: 100})
			expectCode(t, err, codes.Internal)
		})
		within("interrupt", func() {
			resp, err := s.Interrupt(ctx, &adapterv1.InterruptRequest{
				SessionId: sid, Mode: adapterv1.InterruptRequest_MODE_CLEAN, DeadlineMs: 100,
			})
			if err != nil || resp.GetStatus() != adapterv1.InterruptResponse_STATUS_INTERRUPT_TIMEOUT {
				t.Errorf("Interrupt = (%+v, %v), want INTERRUPT_TIMEOUT", resp, err)
			}
		})
		within("deadline signal", func() {
			resp, err := s.SignalDeadline(ctx, &adapterv1.SignalDeadlineRequest{SessionId: sid, RemainingMs: 100})
			if err != nil || resp.GetDelivered() {
				t.Errorf("SignalDeadline = (%+v, %v), want undelivered", resp, err)
			}
		})
		within("rotation", func() {
			_, err := s.RotateCredentials(ctx, &adapterv1.RotateCredentialsRequest{
				SessionId: sid,
				Leases: map[string]*adapterv1.CredentialLease{
					"anthropic": {LeaseId: "l-2", Provider: "anthropic", Payload: []byte("{}")},
				},
			})
			expectCode(t, err, codes.DeadlineExceeded)
		})
		s.SessionStartAckTimeout = 100 * time.Millisecond
		within("files_updated", func() { s.signalFilesUpdated(ctx, st, "sess-a") })
		expectNoFrame(t, peer, 200*time.Millisecond)
	})

	removals := map[string]func(t *testing.T, s *Server) (wait func()){
		"Shutdown whose guard acquisition expired": func(t *testing.T, s *Server) func() {
			if _, err := s.Shutdown(shortCtx(t), unconditionalShutdownReq("sess-a")); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
			return func() {}
		},
		"hold-timeout pass 1": func(t *testing.T, s *Server) func() {
			s.hold.mu.Lock()
			s.hold.active = true
			s.hold.mu.Unlock()
			done := make(chan struct{})
			go func() { s.onHoldTimeout(); close(done) }()
			return func() { <-done }
		},
	}
	for name, remove := range removals {
		t.Run(name+" while an acknowledgement is pending wakes a waiting sender", func(t *testing.T) {
			rt := &frameRuntime{}
			s, peer := ackFrameServer(t, rt)
			s.SessionStartAckTimeout = 2 * time.Second
			started := startWithheld(t, s, rt, "sess-a")
			answers := signalDeadlineAsync(s, "sess-a")
			select {
			case resp := <-answers:
				t.Fatalf("SignalDeadline returned %+v before the gate moved", resp)
			case <-time.After(50 * time.Millisecond):
			}
			finish := remove(t, s)
			expectUndeliveredWithin(t, answers, time.Second)
			if err := <-started; err == nil {
				t.Error("the start whose entry was removed while it waited succeeded")
			}
			finish()
			expectNoFrame(t, peer, 200*time.Millisecond)
		})
	}

	t.Run("a sender queued on the op lock while its entry is replaced writes no frame", func(t *testing.T) {
		for _, op := range []string{"interrupt", "checkpoint"} {
			t.Run(op, func(t *testing.T) {
				rt := &frameRuntime{}
				s, peer := ackFrameServer(t, rt)
				s.CheckpointTransport = nopCheckpointTransport{}
				if err := startSession(ctx, s, "sess-a"); err != nil {
					t.Fatalf("StartSession: %v", err)
				}
				// The co-tenant's upload holds the pod-level op lock.
				release, err := s.ops.Begin(ctx, opCheckpoint, "sess-b")
				if err != nil {
					t.Fatalf("co-tenant op lock: %v", err)
				}
				answer := queueOnOpLock(t, s, op)
				if _, err := s.Shutdown(ctx, unconditionalShutdownReq("sess-a")); err != nil {
					t.Fatalf("Shutdown: %v", err)
				}
				if err := startSession(ctx, s, "sess-a"); err != nil {
					t.Fatalf("successor StartSession: %v", err)
				}
				if st, _ := gateOf(s, "sess-a"); st != ackRead {
					t.Fatalf("successor gate = %s, want read", st)
				}
				release()
				select {
				case err := <-answer:
					if err == nil {
						t.Errorf("%s for the released entry succeeded", op)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("%s did not return once the co-tenant released the op lock", op)
				}
				expectNoFrame(t, peer, 200*time.Millisecond)
			})
		}
	})
}

// queueOnOpLock starts a clean Interrupt or a Checkpoint for sess-a in the
// background, returns once it is pending on the op lock, and delivers its
// outcome as an error: a refused interrupt is reported as an error so the
// caller treats every outcome but a delivered frame alike.
func queueOnOpLock(t *testing.T, s *Server, op string) <-chan error {
	t.Helper()
	answer := make(chan error, 1)
	switch op {
	case "interrupt":
		go func() {
			resp, err := s.Interrupt(context.Background(), &adapterv1.InterruptRequest{
				SessionId: &adapterv1.SessionId{Value: "sess-a"}, Mode: adapterv1.InterruptRequest_MODE_CLEAN, DeadlineMs: 2000,
			})
			if err == nil && resp.GetStatus() != adapterv1.InterruptResponse_STATUS_ACKNOWLEDGED {
				err = fmt.Errorf("interrupt answered %s", resp.GetStatus())
			}
			answer <- err
		}()
		waitPending(t, op, func() bool {
			s.ops.mu.Lock()
			defer s.ops.mu.Unlock()
			return s.ops.interruptPending
		})
	default:
		stream := &abortingCheckpointStream{
			ctx: context.Background(), onChunkReady: func() {}, abort: make(chan struct{}),
			start: &adapterv1.CheckpointStart{
				CheckpointId: "ckpt-1", SessionId: &adapterv1.SessionId{Value: "sess-a"},
				Trigger:        adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_PERIODIC,
				ChunkSizeBytes: 1 << 20, DeadlineMs: 2000,
			},
		}
		go func() { answer <- s.Checkpoint(stream) }()
		waitPending(t, op, func() bool {
			s.ops.mu.Lock()
			defer s.ops.mu.Unlock()
			_, pending := s.ops.checkpoints["sess-a"]
			return pending
		})
	}
	return answer
}
