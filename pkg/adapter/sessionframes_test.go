// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// frameRuntime is a RuntimeProcess that records, in one ordered log, every
// frame written to it (as "write:<type>") and every Close ("close"), so a
// test can assert where a session frame falls relative to the teardown.
// onWrite, when set, runs after a frame is recorded and before
// WriteEnvelope returns, which lets a test interleave a registry change
// with the open sequence at the point the frame reaches the runtime.
type frameRuntime struct {
	mu       sync.Mutex
	events   []string
	frames   [][]byte
	writeErr error
	onWrite  func(frame []byte)
}

func (r *frameRuntime) Start(context.Context, string) error { return nil }

func (r *frameRuntime) WriteEnvelope(_ string, envelope []byte) error {
	r.mu.Lock()
	if r.writeErr != nil {
		err := r.writeErr
		r.mu.Unlock()
		return err
	}
	r.events = append(r.events, "write:"+jsonlFrameType(envelope))
	r.frames = append(r.frames, append([]byte(nil), envelope...))
	hook := r.onWrite
	r.mu.Unlock()
	if hook != nil {
		hook(envelope)
	}
	return nil
}

func (r *frameRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func (r *frameRuntime) Interrupt(context.Context, string, bool) error { return nil }

func (r *frameRuntime) Close(context.Context, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "close")
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
// records its DemoteSDK in the same log as "demote".
type sdkWarmFrameRuntime struct {
	frameRuntime
}

func (r *sdkWarmFrameRuntime) PreConnect(context.Context) error { return nil }

func (r *sdkWarmFrameRuntime) ConfigureWorkspace(context.Context, string, string) error {
	return nil
}

func (r *sdkWarmFrameRuntime) DemoteSDK(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "demote")
	return nil
}

// frameServer returns a Server wired to a fresh workspace base and rt.
func frameServer(t *testing.T, rt RuntimeProcess) *Server {
	t.Helper()
	s := New("frames-test")
	s.WorkspaceBase = t.TempDir()
	s.Runtime = rt
	return s
}

func frameStartReq(sessionID string) *adapterv1.StartSessionRequest {
	return &adapterv1.StartSessionRequest{SessionId: &adapterv1.SessionId{Value: sessionID}}
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

// spec: §28.5.3 (CH-MSGSOCK, Session frame writes; Inbound: session_start)
// — a Shutdown of a running session writes its session_end before the
// teardown closes the runtime, and the next start of the same session
// writes a session_start whose startId differs from the first one's.
func TestShutdownWritesSessionEndBeforeCloseAndRestartMintsNewStartID_spec_28_5_3(t *testing.T) {
	rt := &frameRuntime{}
	s := frameServer(t, rt)
	ctx := context.Background()
	if _, err := s.StartSession(ctx, frameStartReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if _, err := s.Shutdown(ctx, unconditionalShutdownReq("sess-1")); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got, want := rt.log(), []string{"write:session_start", "write:session_end", "close"}; !equalLog(got, want) {
		t.Fatalf("runtime events = %v, want %v", got, want)
	}
	if _, err := s.StartSession(ctx, frameStartReq("sess-1")); err != nil {
		t.Fatalf("second StartSession: %v", err)
	}
	frames := rt.written()
	if len(frames) != 3 {
		t.Fatalf("frames written = %d, want 3", len(frames))
	}
	end := decodeFrame(t, frames[1])
	if len(end) != 2 || end["sessionId"] != "sess-1" {
		t.Errorf("session_end = %s, want type and sessionId only", frames[1])
	}
	first, second := decodeFrame(t, frames[0])["startId"], decodeFrame(t, frames[2])["startId"]
	if first == second {
		t.Errorf("both starts of sess-1 carry startId %v, want distinct values", first)
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Session frame writes) — a Shutdown that
// removes an entry whose session never reached running writes no frame.
func TestShutdownOfUnstartedEntryWritesNoSessionEnd_spec_28_5_3(t *testing.T) {
	rt := &frameRuntime{}
	s := frameServer(t, rt)
	if _, err := s.ensureSlotPaths("sess-1", slotResolve{allowCreate: true}); err != nil {
		t.Fatalf("register slot: %v", err)
	}
	if _, err := s.Shutdown(context.Background(), unconditionalShutdownReq("sess-1")); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := rt.log(); len(got) != 0 {
		t.Errorf("runtime events = %v, want none for an entry that never started", got)
	}
}

// spec: §4.7.1 (role and gateway RPC contract), rule 8; §28.5.3
// (CH-MSGSOCK, Session frame writes) — an open sequence that finds a
// different entry under the identifier than the one its claim was
// admitted against refuses at the first confirmation and writes no frame.
func TestOpenRuntimeSessionRefusesReplacedEntryWithoutFrames_spec_4_7_1(t *testing.T) {
	rt := &frameRuntime{}
	s := frameServer(t, rt)
	claim, err := s.claimSessionSlot("sess-1", slotResolve{allowCreate: true}, false, false)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	s.mu.Lock()
	s.slots["sess-1"] = &slotState{sessionID: "sess-1", started: true, bindAttempt: claim.attempt}
	s.mu.Unlock()
	confirmed, err := s.openRuntimeSession(context.Background(), "sess-1", claim, manifestInputs{}, false)
	if err != nil || confirmed {
		t.Fatalf("openRuntimeSession = (%v, %v), want (false, nil)", confirmed, err)
	}
	if got := rt.log(); len(got) != 0 {
		t.Errorf("runtime events = %v, want no frame on a refused first confirmation", got)
	}
	s.mu.Lock()
	holds := s.runtimeHoldsLocked("sess-1")
	s.mu.Unlock()
	if holds {
		t.Error("the refused start took the rule-8 record")
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Session frame writes); §4.7.1 (role and
// gateway RPC contract), rule 8; §5.2 (slot-identifier reclaim hold) — when
// the entry leaves the registry after the session_start is written, the
// second confirmation is refused, the open sequence writes session_end
// before the start takes the session back off the runtime, and the start
// answers Aborted with no record taken.
func TestStartSessionWritesSessionEndWhenSecondConfirmationRefused_spec_28_5_3(t *testing.T) {
	rt := &frameRuntime{}
	s := frameServer(t, rt)
	rt.onWrite = func(frame []byte) {
		if jsonlFrameType(frame) != "session_start" {
			return
		}
		// The hold-timeout termination's first pass removes an entry
		// without the slot guard, which is the removal an open sequence can
		// meet between its two confirmations.
		s.mu.Lock()
		s.deregisterSlotLocked("sess-1")
		s.mu.Unlock()
	}
	_, err := s.StartSession(context.Background(), frameStartReq("sess-1"))
	if status.Code(err) != codes.Aborted {
		t.Fatalf("StartSession code = %v, want Aborted", status.Code(err))
	}
	if got, want := rt.log(), []string{"write:session_start", "write:session_end", "close"}; !equalLog(got, want) {
		t.Fatalf("runtime events = %v, want %v", got, want)
	}
	s.mu.Lock()
	holds := s.runtimeHoldsLocked("sess-1")
	s.mu.Unlock()
	if holds {
		t.Error("the refused start took the rule-8 record")
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Session frame writes); §5.2 (slot-identifier
// reclaim hold) — a start whose open sequence cannot take the slot
// serialization before its request's deadline fails with nothing written,
// takes the session back off the runtime, and releases its claim.
func TestStartSessionFailsWhenSlotSerializationNotAcquired_spec_5_2(t *testing.T) {
	rt := &frameRuntime{}
	s := frameServer(t, rt)
	unlock, guarded := s.lockSlotGuard(context.Background(), "sess-1")
	if !guarded {
		t.Fatal("precondition: the test could not take the slot guard")
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := s.StartSession(ctx, frameStartReq("sess-1"))
	if status.Code(err) != codes.Internal {
		t.Fatalf("StartSession code = %v, want Internal", status.Code(err))
	}
	if got, want := rt.log(), []string{"close"}; !equalLog(got, want) {
		t.Errorf("runtime events = %v, want %v (no frame, then the runtime close)", got, want)
	}
	if n := s.slotCount(); n != 0 {
		t.Errorf("registry holds %d entries after the failed start, want 0", n)
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Session frame writes) — a start whose
// session_start write fails treats the frame as undelivered: it answers
// Internal, takes the session back off the runtime, releases its claim and
// takes no record.
func TestStartSessionFailsOnSessionStartWriteError_spec_28_5_3(t *testing.T) {
	rt := &frameRuntime{writeErr: errors.New("connection reset")}
	s := frameServer(t, rt)
	_, err := s.StartSession(context.Background(), frameStartReq("sess-1"))
	if status.Code(err) != codes.Internal {
		t.Fatalf("StartSession code = %v, want Internal", status.Code(err))
	}
	if got, want := rt.log(), []string{"close"}; !equalLog(got, want) {
		t.Errorf("runtime events = %v, want %v", got, want)
	}
	if n := s.slotCount(); n != 0 {
		t.Errorf("registry holds %d entries after the failed start, want 0", n)
	}
	s.mu.Lock()
	holds := s.runtimeHoldsLocked("sess-1")
	s.mu.Unlock()
	if holds {
		t.Error("a start whose session_start was not delivered took the rule-8 record")
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Session frame writes); §5.2 (slot-identifier
// reclaim hold) — Resume holds the slot guard from ahead of its claim, so
// its open sequence runs under that guard rather than acquiring it again,
// and writes the restored session's session_start.
func TestResumeWritesSessionStartUnderItsHeldGuard_spec_28_5_3(t *testing.T) {
	rt := &frameRuntime{}
	s := frameServer(t, rt)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := s.Resume(ctx, &adapterv1.ResumeRequest{
		SessionId:    &adapterv1.SessionId{Value: "sess-1"},
		BindAttempt:  "attempt-a",
		CheckpointId: "ckpt-1",
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got, want := rt.log(), []string{"write:session_start"}; !equalLog(got, want) {
		t.Errorf("runtime events = %v, want %v", got, want)
	}
	s.mu.Lock()
	holds := s.runtimeHoldsLocked("sess-1")
	s.mu.Unlock()
	if !holds {
		t.Error("the resumed session did not reach running")
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Session frame writes) — the SDK-warm start
// writes session_start on its fresh arm, and its idempotent repeat for the
// same session writes nothing.
func TestConfigureWorkspaceWritesSessionStartOnFreshArmOnly_spec_28_5_3(t *testing.T) {
	rt := &sdkWarmFrameRuntime{}
	s := frameServer(t, rt)
	req := &adapterv1.ConfigureWorkspaceRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Cwd:       s.WorkspaceBase,
	}
	for i := 0; i < 2; i++ {
		if _, err := s.ConfigureWorkspace(context.Background(), req); err != nil {
			t.Fatalf("ConfigureWorkspace #%d: %v", i+1, err)
		}
	}
	if got, want := rt.log(), []string{"write:session_start"}; !equalLog(got, want) {
		t.Errorf("runtime events = %v, want %v", got, want)
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Session frame writes) — DemoteSDK while the
// session is running writes its session_end before the pre-connected SDK
// is torn down, and removes the entry.
func TestDemoteSDKWritesSessionEndBeforeTeardown_spec_28_5_3(t *testing.T) {
	rt := &sdkWarmFrameRuntime{}
	s := frameServer(t, rt)
	if err := s.PreConnect(context.Background()); err != nil {
		t.Fatalf("PreConnect: %v", err)
	}
	if _, err := s.ConfigureWorkspace(context.Background(), &adapterv1.ConfigureWorkspaceRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Cwd:       s.WorkspaceBase,
	}); err != nil {
		t.Fatalf("ConfigureWorkspace: %v", err)
	}
	if _, err := s.DemoteSDK(context.Background(), &adapterv1.DemoteSDKRequest{}); err != nil {
		t.Fatalf("DemoteSDK: %v", err)
	}
	if got, want := rt.log(), []string{"write:session_start", "write:session_end", "demote"}; !equalLog(got, want) {
		t.Errorf("runtime events = %v, want %v", got, want)
	}
	if n := s.slotCount(); n != 0 {
		t.Errorf("registry holds %d entries after DemoteSDK, want 0", n)
	}
	if s.SDKWarmReady() {
		t.Error("DemoteSDK left the pod SDK-warm ready")
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Session frame writes, the DemoteSDK deadline
// row); §5.2 (slot-identifier reclaim hold) — a DemoteSDK whose wait for
// the slot serialization outlasts its request's deadline fails closed with
// DeadlineExceeded: it writes no session_end, does not tear the SDK down,
// and leaves the entry and the SDK-warm readiness unchanged.
func TestDemoteSDKFailsClosedWhenSlotSerializationNotAcquired_spec_28_5_3(t *testing.T) {
	rt := &sdkWarmFrameRuntime{}
	s := frameServer(t, rt)
	if err := s.PreConnect(context.Background()); err != nil {
		t.Fatalf("PreConnect: %v", err)
	}
	if _, err := s.ConfigureWorkspace(context.Background(), &adapterv1.ConfigureWorkspaceRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Cwd:       s.WorkspaceBase,
	}); err != nil {
		t.Fatalf("ConfigureWorkspace: %v", err)
	}
	unlock, guarded := s.lockSlotGuard(context.Background(), "sess-1")
	if !guarded {
		t.Fatal("precondition: the test could not take the slot guard")
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := s.DemoteSDK(ctx, &adapterv1.DemoteSDKRequest{})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("DemoteSDK code = %v, want DeadlineExceeded", status.Code(err))
	}
	if got, want := rt.log(), []string{"write:session_start"}; !equalLog(got, want) {
		t.Errorf("runtime events = %v, want %v (no session_end and no demotion)", got, want)
	}
	if n := s.slotCount(); n != 1 {
		t.Errorf("registry holds %d entries, want the entry left standing", n)
	}
	if !s.SDKWarmReady() {
		t.Error("the failed DemoteSDK cleared the SDK-warm readiness")
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Session frame writes) — DemoteSDK while no
// session is running writes no session_end.
func TestDemoteSDKWithNoSessionWritesNoSessionEnd_spec_28_5_3(t *testing.T) {
	rt := &sdkWarmFrameRuntime{}
	s := frameServer(t, rt)
	if err := s.PreConnect(context.Background()); err != nil {
		t.Fatalf("PreConnect: %v", err)
	}
	if _, err := s.DemoteSDK(context.Background(), &adapterv1.DemoteSDKRequest{}); err != nil {
		t.Fatalf("DemoteSDK: %v", err)
	}
	if got, want := rt.log(), []string{"demote"}; !equalLog(got, want) {
		t.Errorf("runtime events = %v, want %v", got, want)
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Session frame writes, the type: mcp row);
// §4.7.10 — a type: mcp runtime exchanges no CH-MSGSOCK frame, so neither
// session frame is written to it.
func TestSessionFramesNotWrittenForMCPRuntime_spec_28_5_3(t *testing.T) {
	rt := &frameRuntime{}
	s := frameServer(t, rt)
	s.RuntimeKind = RuntimeKindMCP
	if err := s.writeSessionStart("sess-1", s.nextStartID(), manifestInputs{}); err != nil {
		t.Fatalf("writeSessionStart: %v", err)
	}
	s.writeSessionEnd("sess-1")
	if got := rt.log(); len(got) != 0 {
		t.Errorf("runtime events = %v, want none for a type: mcp runtime", got)
	}
}

// spec: §28.5.3 (CH-MSGSOCK, Session frame writes) — a teardown proceeds
// when its session_end write fails: the frame is not acknowledged, so the
// failure is logged and the runtime is still closed.
func TestShutdownProceedsWhenSessionEndWriteFails_spec_28_5_3(t *testing.T) {
	rt := &frameRuntime{}
	s := frameServer(t, rt)
	ctx := context.Background()
	if _, err := s.StartSession(ctx, frameStartReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	rt.mu.Lock()
	rt.writeErr = errors.New("broken pipe")
	rt.mu.Unlock()
	resp, err := s.Shutdown(ctx, unconditionalShutdownReq("sess-1"))
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !resp.GetExitedCleanly() {
		t.Error("Shutdown answered exited_cleanly false on a failed session_end write")
	}
	if got, want := rt.log(), []string{"write:session_start", "close"}; !equalLog(got, want) {
		t.Errorf("runtime events = %v, want %v", got, want)
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

// spec: §28.5.3 (CH-MSGSOCK, Session frame writes) — a resume or an
// SDK-warm start whose session_start write fails answers Internal, takes no
// record, and releases its slot, so a retry can land on a fresh pod.
func TestResumeAndSDKWarmStartFailOnSessionStartWriteError_spec_28_5_3(t *testing.T) {
	writeErr := errors.New("connection reset")
	t.Run("resume", func(t *testing.T) {
		rt := &frameRuntime{writeErr: writeErr}
		s := frameServer(t, rt)
		_, err := s.Resume(context.Background(), &adapterv1.ResumeRequest{
			SessionId:    &adapterv1.SessionId{Value: "sess-1"},
			BindAttempt:  "attempt-a",
			CheckpointId: "ckpt-1",
		})
		if status.Code(err) != codes.Internal {
			t.Fatalf("Resume code = %v, want Internal", status.Code(err))
		}
		if got, want := rt.log(), []string{"close"}; !equalLog(got, want) {
			t.Errorf("runtime events = %v, want %v", got, want)
		}
		if n := s.slotCount(); n != 0 {
			t.Errorf("registry holds %d entries after the failed resume, want 0", n)
		}
	})
	t.Run("sdk_warm_start", func(t *testing.T) {
		rt := &sdkWarmFrameRuntime{frameRuntime{writeErr: writeErr}}
		s := frameServer(t, rt)
		_, err := s.ConfigureWorkspace(context.Background(), &adapterv1.ConfigureWorkspaceRequest{
			SessionId: &adapterv1.SessionId{Value: "sess-1"},
			Cwd:       s.WorkspaceBase,
		})
		if status.Code(err) != codes.Internal {
			t.Fatalf("ConfigureWorkspace code = %v, want Internal", status.Code(err))
		}
		if n := s.slotCount(); n != 0 {
			t.Errorf("registry holds %d entries after the failed SDK-warm start, want 0", n)
		}
		s.mu.Lock()
		holds := s.runtimeHoldsLocked("sess-1")
		s.mu.Unlock()
		if holds {
			t.Error("a start whose session_start was not delivered took the rule-8 record")
		}
	})
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
