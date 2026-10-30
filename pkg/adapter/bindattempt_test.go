// SPDX-License-Identifier: MIT

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/observability/tracing"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// The bind attempt tokens the cases below stamp and compare. They are
// opaque to the adapter, so any two distinct non-empty strings serve.
const (
	tokenA = "attempt-token-a"
	tokenB = "attempt-token-b"
)

// bindRuntime is an SDK-warm runtime whose Close runs an injectable hook,
// so a case can park a §10.1.4 hold termination inside its runtime close,
// fail that close, or panic out of it. It implements SDKWarmRuntime so
// ConfigureWorkspace is reachable on the same server as the other
// admission RPCs.
type bindRuntime struct {
	mu      sync.Mutex
	onClose func(sessionID string) error
	// onConfigure, when set, answers ConfigureWorkspace, so a case can park
	// the SDK-warm start inside the runtime call and fail it.
	onConfigure func(sessionID string) error
}

func (r *bindRuntime) Start(context.Context, string) error { return nil }
func (r *bindRuntime) WriteEnvelope(string, []byte) error  { return nil }
func (r *bindRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}
func (r *bindRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (r *bindRuntime) PreConnect(context.Context) error              { return nil }
func (r *bindRuntime) ConfigureWorkspace(_ context.Context, sessionID, _ string) error {
	r.mu.Lock()
	hook := r.onConfigure
	r.mu.Unlock()
	if hook != nil {
		return hook(sessionID)
	}
	return nil
}
func (r *bindRuntime) DemoteSDK(context.Context) error { return nil }

func (r *bindRuntime) Close(_ context.Context, sessionID string) error {
	r.mu.Lock()
	hook := r.onClose
	r.mu.Unlock()
	if hook != nil {
		return hook(sessionID)
	}
	return nil
}

func (r *bindRuntime) setOnClose(f func(string) error) {
	r.mu.Lock()
	r.onClose = f
	r.mu.Unlock()
}

// bindServer builds an adapter with every per-slot root under one temp
// base and an SDK-warm bindRuntime.
func bindServer(t *testing.T) (*Server, *bindRuntime) {
	t.Helper()
	base := t.TempDir()
	s := New("bind-attempt-test")
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	rt := &bindRuntime{}
	s.Runtime = rt
	return s, rt
}

// resolveLocked runs the resolve under s.mu, as its production callers do.
func resolveLocked(s *Server, slotID string, r slotResolve) (*slotState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureSlotStateLocked(slotID, r)
}

// seedEntry creates slotID's entry stamped with token and, when started,
// marks it started, the state a completed start leaves.
func seedEntry(t *testing.T, s *Server, slotID, token string, started bool) *slotState {
	t.Helper()
	st, err := resolveLocked(s, slotID, slotResolve{bindAttempt: token, allowCreate: true})
	if err != nil {
		t.Fatalf("seed %s: %v", slotID, err)
	}
	if started {
		s.mu.Lock()
		st.sessionID = slotID
		st.started = true
		s.mu.Unlock()
	}
	return st
}

// hasEntry reports whether the registry holds an entry for slotID.
func hasEntry(s *Server, slotID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.slots[slotID]
	return ok
}

// slotDirExists reports whether any per-slot directory exists for slotID.
func slotDirExists(s *Server, slotID string) bool {
	for _, root := range []string{
		filepath.Join(s.WorkspaceBase, "slots", slotID),
		filepath.Join(s.SessionsRoot, slotID),
		filepath.Join(s.ArtifactsRoot, slotID),
	} {
		if _, err := os.Stat(root); err == nil {
			return true
		}
	}
	return false
}

// adapterErrorCode returns the adapterv1.Error code a status carries in its
// detail, or zero when it carries none.
func adapterErrorCode(err error) adapterv1.Error_ErrorCode {
	st, ok := status.FromError(err)
	if !ok {
		return 0
	}
	for _, d := range st.Details() {
		if e, ok := d.(*adapterv1.Error); ok {
			return e.GetCode()
		}
	}
	return 0
}

// captureLogs redirects slog and the standard logger into one buffer for
// the rest of the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prevSlog := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, nil)))
	prevOut := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() {
		slog.SetDefault(prevSlog)
		log.SetOutput(prevOut)
	})
	return buf
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow)
//
// The rule 2-through-7 predicate in ensureSlotStateLocked, one subtest per
// rule and per ordering the rules fix.
func TestResolveAppliesTheAdmissionCascade_spec_4_7_1(t *testing.T) {
	t.Run("mid-session-create rule creates nothing", func(t *testing.T) {
		s, _ := bindServer(t)
		_, err := resolveLocked(s, "alice", slotResolve{allowStarted: true})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("code = %v, want FailedPrecondition", status.Code(err))
		}
		if hasEntry(s, "alice") {
			t.Error("a mid-session resolve created a registry entry")
		}
		if slotDirExists(s, "alice") {
			t.Error("a mid-session resolve created the slot's tree on disk")
		}
	})
	t.Run("create-and-stamp rule stamps the caller's token", func(t *testing.T) {
		s, _ := bindServer(t)
		st := seedEntry(t, s, "alice", tokenA, false)
		if st.bindAttempt != tokenA {
			t.Errorf("stamp = %q, want the creating request's token", st.bindAttempt)
		}
		st = seedEntry(t, s, "bob", "", false)
		if st.bindAttempt != "" {
			t.Errorf("an untokened create stamped %q, want empty", st.bindAttempt)
		}
	})
	t.Run("create arm refuses a malformed identifier before inserting", func(t *testing.T) {
		for _, id := range []string{".", "..", "a/b", `a\b`, "a\x00b", "./a", "a/"} {
			s, _ := bindServer(t)
			if _, err := resolveLocked(s, id, slotResolve{bindAttempt: tokenA, allowCreate: true}); err == nil {
				t.Errorf("resolve %q admitted a malformed identifier", id)
			}
			s.mu.Lock()
			n := len(s.slots)
			s.mu.Unlock()
			if n != 0 {
				t.Errorf("resolve %q left %d registry entries, want 0", id, n)
			}
			entries, _ := os.ReadDir(filepath.Join(s.WorkspaceBase, "slots"))
			if len(entries) != 0 {
				t.Errorf("resolve %q created %d directories under slots, want 0", id, len(entries))
			}
		}
	})
	t.Run("a tree-creation failure inserts nothing", func(t *testing.T) {
		s, _ := bindServer(t)
		slots := filepath.Join(s.WorkspaceBase, "slots")
		if err := os.MkdirAll(slots, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(slots, "alice"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := resolveLocked(s, "alice", slotResolve{bindAttempt: tokenA, allowCreate: true}); err == nil {
			t.Fatal("resolve succeeded over a file planted where the slot directory belongs")
		}
		if hasEntry(s, "alice") {
			t.Error("a failed tree creation inserted a registry entry")
		}
	})
	t.Run("attempt identity rule refuses a differing token", func(t *testing.T) {
		s, _ := bindServer(t)
		st := seedEntry(t, s, "alice", tokenA, false)
		_, err := resolveLocked(s, "alice", slotResolve{bindAttempt: tokenB, allowCreate: true})
		if status.Code(err) != codes.Aborted {
			t.Fatalf("code = %v, want Aborted", status.Code(err))
		}
		if got := adapterErrorCode(err); got != adapterv1.Error_ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED {
			t.Errorf("detail code = %v, want SLOT_BIND_ATTEMPT_SUPERSEDED", got)
		}
		if st.bindAttempt != tokenA {
			t.Errorf("stamp after the refusal = %q, want it unchanged", st.bindAttempt)
		}
	})
	t.Run("started-session rule refuses unless the caller allows a started entry", func(t *testing.T) {
		s, _ := bindServer(t)
		seedEntry(t, s, "alice", tokenA, true)
		_, err := resolveLocked(s, "alice", slotResolve{bindAttempt: tokenA, allowCreate: true})
		if status.Code(err) != codes.FailedPrecondition ||
			adapterErrorCode(err) != adapterv1.Error_ERROR_CODE_SLOT_BIND_ALREADY_STARTED {
			t.Fatalf("err = %v, want FailedPrecondition with SLOT_BIND_ALREADY_STARTED", err)
		}
		if _, err := resolveLocked(s, "alice", slotResolve{allowStarted: true}); err != nil {
			t.Errorf("a resolve allowing a started entry was refused: %v", err)
		}
	})
	t.Run("admit rule both arms and the stamp-once rule", func(t *testing.T) {
		s, _ := bindServer(t)
		untokened := seedEntry(t, s, "alice", "", false)
		if _, err := resolveLocked(s, "alice", slotResolve{bindAttempt: tokenB, allowCreate: true}); err != nil {
			t.Fatalf("a tokened resolve of an untokened entry was refused: %v", err)
		}
		if untokened.bindAttempt != "" {
			t.Errorf("an admitted resolve stamped %q onto an existing entry; only the create writes", untokened.bindAttempt)
		}
		stamped := seedEntry(t, s, "bob", tokenA, false)
		if _, err := resolveLocked(s, "bob", slotResolve{allowCreate: true}); err != nil {
			t.Fatalf("an untokened resolve of a stamped entry was refused: %v", err)
		}
		if stamped.bindAttempt != tokenA {
			t.Errorf("stamp after an untokened resolve = %q, want it unchanged", stamped.bindAttempt)
		}
	})
	t.Run("the identity rule precedes the started-session rule", func(t *testing.T) {
		s, _ := bindServer(t)
		seedEntry(t, s, "alice", tokenA, true)
		_, err := resolveLocked(s, "alice", slotResolve{bindAttempt: tokenB, allowCreate: true})
		if got := adapterErrorCode(err); got != adapterv1.Error_ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED {
			t.Errorf("detail code = %v, want SLOT_BIND_ATTEMPT_SUPERSEDED ahead of the started-session refusal", got)
		}
	})
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow)
//
// Every production caller of the resolve applies the identity gate: a
// comparison placed in the workspace handlers alone would leave the claim
// path ungated, so the claim row comes first.
func TestEveryProductionResolveCallerIsGated_spec_4_7_1(t *testing.T) {
	cases := []struct {
		name string
		call func(s *Server) error
	}{
		{"claimSessionSlotUnderLock", func(s *Server) error {
			_, _, err := s.claimSessionSlotUnderLock("alice",
				slotResolve{bindAttempt: tokenB, allowCreate: true}, false, false)
			return err
		}},
		{"ensureSlotPaths", func(s *Server) error {
			_, err := s.ensureSlotPaths("alice", slotResolve{bindAttempt: tokenB, allowCreate: true})
			return err
		}},
		{"assignCredentialsSlot", func(s *Server) error {
			_, err := s.assignCredentialsSlot("alice", "alice", nil,
				slotResolve{bindAttempt: tokenB, allowCreate: true})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := bindServer(t)
			seedEntry(t, s, "alice", tokenA, false)
			err := tc.call(s)
			if status.Code(err) != codes.Aborted ||
				adapterErrorCode(err) != adapterv1.Error_ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED {
				t.Errorf("err = %v, want the superseded refusal", err)
			}
		})
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow)
//
// The claim's own started-entry arm answers rule 6's typed refusal rather
// than an untyped Unavailable, and its idempotent-repeat arm is unchanged.
func TestClaimRefusesAStartedEntryWithTheTypedCode_spec_4_7_1(t *testing.T) {
	s, _ := bindServer(t)
	seedEntry(t, s, "alice", "", true)
	_, _, err := s.claimSessionSlotUnderLock("alice",
		slotResolve{allowCreate: true, allowStarted: true}, false, false)
	if status.Code(err) != codes.FailedPrecondition ||
		adapterErrorCode(err) != adapterv1.Error_ERROR_CODE_SLOT_BIND_ALREADY_STARTED {
		t.Errorf("err = %v, want SLOT_BIND_ALREADY_STARTED on FailedPrecondition", err)
	}
	claim, stale, err := s.claimSessionSlotUnderLock("alice",
		slotResolve{allowCreate: true, allowStarted: true}, false, true)
	if err != nil || claim.fresh || claim.startMCP || stale != nil {
		t.Errorf("idempotent repeat = (%+v, %v, %v), want a satisfied claim", claim, stale, err)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow); §7.4 (upload safety)
//
// FinalizeWorkspace reads mid_session before it resolves, so a mid-session
// finalize for a session the pod holds no entry for creates nothing.
func TestFinalizeWorkspaceReadsMidSessionBeforeItResolves_spec_4_7_1(t *testing.T) {
	s, _ := bindServer(t)
	_, err := s.FinalizeWorkspace(context.Background(), &adapterv1.FinalizeWorkspaceRequest{
		SessionId:     &adapterv1.SessionId{Value: "alice"},
		MidSession:    true,
		WorkspacePlan: &adapterv1.WorkspacePlan{SchemaVersion: 1},
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", status.Code(err))
	}
	if hasEntry(s, "alice") || slotDirExists(s, "alice") {
		t.Error("a mid-session finalize with no entry created an entry or a tree")
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §7.4 (upload safety)
//
// PrepareWorkspace applies rule 3 on its first frame: a mid-session upload
// for a session the pod holds no entry for is refused FAILED_PRECONDITION
// and creates no entry and no tree. The resolve site wraps every failure
// other than a typed refusal as INVALID_ARGUMENT, so this pins the rule-3
// refusal as one that passes through the wrap.
func TestPrepareWorkspaceMidSessionWithoutEntryRefusesFailedPrecondition_spec_4_7_1(t *testing.T) {
	s, _ := bindServer(t)
	err := s.PrepareWorkspace(&prepareWorkspaceStreamStub{
		ctx:    context.Background(),
		frames: []*adapterv1.PrepareWorkspaceRequest{prepareFrame("alice", "", true, "ab")},
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", status.Code(err))
	}
	if hasEntry(s, "alice") || slotDirExists(s, "alice") {
		t.Error("a mid-session upload with no entry created an entry or a tree")
	}
}

// spec: §4.7.1 (role and gateway RPC contract), rule 3; §16.3 (distributed tracing)
//
// The rule-3 refusal passes through the resolve-site wrap unchanged: it
// keeps FAILED_PRECONDITION rather than becoming INVALID_ARGUMENT, and its
// span category is PERMANENT. A non-refusal resolve failure is wrapped as
// INVALID_ARGUMENT under the site prefix.
func TestSlotResolveErrorPassesTheMidSessionRefusalThrough_spec_4_7_1(t *testing.T) {
	refusal := errSlotMidSessionNoEntry("alice")
	got := slotResolveError(refusal, "resolve staging for session alice")
	if status.Code(got) != codes.FailedPrecondition {
		t.Errorf("wrapped rule-3 refusal code = %v, want FailedPrecondition", status.Code(got))
	}
	if cat := slotResolveCategory(got); cat != tracing.CategoryPermanent {
		t.Errorf("rule-3 refusal span category = %v, want %v", cat, tracing.CategoryPermanent)
	}
	plain := slotResolveError(errors.New("invalid slot id"), "resolve staging for session alice")
	if status.Code(plain) != codes.InvalidArgument {
		t.Errorf("non-refusal resolve failure code = %v, want InvalidArgument", status.Code(plain))
	}
}

// prepareFrame is one PrepareWorkspace frame carrying the given fields.
func prepareFrame(sessionID, token string, midSession bool, chunk string) *adapterv1.PrepareWorkspaceRequest {
	return &adapterv1.PrepareWorkspaceRequest{
		SessionId:   &adapterv1.SessionId{Value: sessionID},
		BindAttempt: token,
		MidSession:  midSession,
		UploadRef:   "lenny-blob://t/a",
		Chunk:       []byte(chunk),
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow); §7.4 (upload safety)
//
// Rule 9: the frame that resolves the slot identifier decides the call's
// admission, and no later frame's bind_attempt or mid_session is read.
func TestPrepareWorkspaceLatchesTheFirstFramesAssertion_spec_4_7_1(t *testing.T) {
	run := func(t *testing.T, s *Server, frames ...*adapterv1.PrepareWorkspaceRequest) *adapterv1.PrepareWorkspaceResponse {
		t.Helper()
		stream := &prepareWorkspaceStreamStub{ctx: context.Background(), frames: frames}
		if err := s.PrepareWorkspace(stream); err != nil {
			t.Fatalf("PrepareWorkspace: %v", err)
		}
		return stream.resp
	}
	t.Run("a later frame's differing token is not read", func(t *testing.T) {
		s, _ := bindServer(t)
		resp := run(t, s, prepareFrame("alice", tokenA, false, "ab"), prepareFrame("alice", tokenB, false, "cd"))
		if resp.GetStagedBytes() != 4 {
			t.Errorf("staged bytes = %d, want 4", resp.GetStagedBytes())
		}
		if st := slotStateForTest(s, "alice"); st == nil || st.bindAttempt != tokenA {
			t.Errorf("entry stamp = %+v, want the first frame's token", st)
		}
	})
	t.Run("a later frame's mid_session is not read", func(t *testing.T) {
		s, _ := bindServer(t)
		resp := run(t, s, prepareFrame("alice", tokenA, false, "ab"), prepareFrame("alice", "", true, "cd"))
		if resp.GetStagedBytes() != 4 {
			t.Errorf("staged bytes = %d, want 4", resp.GetStagedBytes())
		}
		s.mu.Lock()
		n := len(s.slots)
		s.mu.Unlock()
		if n != 1 {
			t.Errorf("registry entries = %d, want 1", n)
		}
	})
	t.Run("a multi-chunk mid-session upload stages in full", func(t *testing.T) {
		s, _ := bindServer(t)
		seedEntry(t, s, "alice", tokenA, true)
		later := &adapterv1.PrepareWorkspaceRequest{
			SessionId: &adapterv1.SessionId{Value: "alice"},
			UploadRef: "lenny-blob://t/a",
			Chunk:     []byte("cd"),
		}
		resp := run(t, s, prepareFrame("alice", "", true, "ab"), later)
		if resp.GetStagedBytes() != 4 {
			t.Errorf("staged bytes = %d, want 4", resp.GetStagedBytes())
		}
	})
}

// slotStateForTest returns slotID's registry entry, nil when absent.
func slotStateForTest(s *Server, slotID string) *slotState {
	return s.slotStateForSession(slotID)
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow)
//
// Rule 1 in both directions at every handler whose message carries the
// fields: the refusal comes before any resolve, so no entry and no tree
// exist afterwards.
func TestPairingRuleRefusesBothDirectionsAtEveryHandler_spec_4_7_1(t *testing.T) {
	ctx := context.Background()
	plan := &adapterv1.WorkspacePlan{SchemaVersion: 1}
	cases := []struct {
		name string
		call func(s *Server) error
	}{
		{"PrepareWorkspace untokened", func(s *Server) error {
			return s.PrepareWorkspace(&prepareWorkspaceStreamStub{
				ctx:    ctx,
				frames: []*adapterv1.PrepareWorkspaceRequest{prepareFrame("alice", "", false, "x")},
			})
		}},
		{"PrepareWorkspace mid-session tokened", func(s *Server) error {
			return s.PrepareWorkspace(&prepareWorkspaceStreamStub{
				ctx:    ctx,
				frames: []*adapterv1.PrepareWorkspaceRequest{prepareFrame("alice", tokenA, true, "x")},
			})
		}},
		{"FinalizeWorkspace untokened", func(s *Server) error {
			_, err := s.FinalizeWorkspace(ctx, &adapterv1.FinalizeWorkspaceRequest{
				SessionId: &adapterv1.SessionId{Value: "alice"}, WorkspacePlan: plan,
			})
			return err
		}},
		{"FinalizeWorkspace mid-session tokened", func(s *Server) error {
			_, err := s.FinalizeWorkspace(ctx, &adapterv1.FinalizeWorkspaceRequest{
				SessionId: &adapterv1.SessionId{Value: "alice"}, WorkspacePlan: plan,
				MidSession: true, BindAttempt: tokenA,
			})
			return err
		}},
		{"RunSetup untokened", func(s *Server) error {
			_, err := s.RunSetup(ctx, &adapterv1.RunSetupRequest{SessionId: &adapterv1.SessionId{Value: "alice"}})
			return err
		}},
		{"AssignCredentials untokened", func(s *Server) error {
			_, err := s.AssignCredentials(ctx, &adapterv1.AssignCredentialsRequest{SessionId: &adapterv1.SessionId{Value: "alice"}})
			return err
		}},
		{"Resume untokened", func(s *Server) error {
			_, err := s.Resume(ctx, &adapterv1.ResumeRequest{
				SessionId: &adapterv1.SessionId{Value: "alice"}, CheckpointId: "ckpt-1",
			})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := bindServer(t)
			if err := tc.call(s); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("code = %v (%v), want InvalidArgument", status.Code(err), err)
			}
			if hasEntry(s, "alice") || slotDirExists(s, "alice") {
				t.Error("a request the pairing rule refuses created an entry or a tree")
			}
		})
	}
}

// admissionCall drives one of the requests §4.7.1's admission rules
// govern for sessionID, carrying the attempt's token where the request
// carries one.
type admissionCall struct {
	name string
	call func(s *Server, sessionID string) error
}

func admissionCalls() []admissionCall {
	ctx := context.Background()
	return []admissionCall{
		{"PrepareWorkspace", func(s *Server, id string) error {
			return s.PrepareWorkspace(&prepareWorkspaceStreamStub{
				ctx:    ctx,
				frames: []*adapterv1.PrepareWorkspaceRequest{prepareFrame(id, tokenA, false, "x")},
			})
		}},
		{"FinalizeWorkspace", func(s *Server, id string) error {
			_, err := s.FinalizeWorkspace(ctx, &adapterv1.FinalizeWorkspaceRequest{
				SessionId: &adapterv1.SessionId{Value: id}, BindAttempt: tokenA,
				WorkspacePlan: &adapterv1.WorkspacePlan{SchemaVersion: 1},
			})
			return err
		}},
		{"RunSetup", func(s *Server, id string) error {
			_, err := s.RunSetup(ctx, &adapterv1.RunSetupRequest{
				SessionId: &adapterv1.SessionId{Value: id}, BindAttempt: tokenA,
			})
			return err
		}},
		{"AssignCredentials", func(s *Server, id string) error {
			_, err := s.AssignCredentials(ctx, &adapterv1.AssignCredentialsRequest{
				SessionId: &adapterv1.SessionId{Value: id}, BindAttempt: tokenA,
			})
			return err
		}},
		{"StartSession", func(s *Server, id string) error {
			_, err := s.StartSession(ctx, &adapterv1.StartSessionRequest{SessionId: &adapterv1.SessionId{Value: id}})
			return err
		}},
		{"Resume", func(s *Server, id string) error {
			_, err := s.Resume(ctx, &adapterv1.ResumeRequest{
				SessionId: &adapterv1.SessionId{Value: id}, CheckpointId: "ckpt-1", BindAttempt: tokenA,
			})
			return err
		}},
		{"ConfigureWorkspace", func(s *Server, id string) error {
			_, err := s.ConfigureWorkspace(ctx, &adapterv1.ConfigureWorkspaceRequest{
				SessionId: &adapterv1.SessionId{Value: id}, Cwd: "/workspace",
			})
			return err
		}},
	}
}

// startHeldTermination runs §10.1.4 pass 1 over the server's started
// sessions and hands each member to pass 2 on its own goroutine, returning
// a channel closed once every member's termination has returned.
func startHeldTermination(s *Server) <-chan struct{} {
	members := s.deregisterStartedSessions()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, m := range members {
			s.terminateHeldSession(context.Background(), m)
		}
	}()
	return done
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow); §10.1.4 (coordinator-loss detection and hold state)
//
// The reclaim hold refuses every admission RPC while the cleanup is parked
// inside its runtime close, with the transient sentinel and without
// blocking the caller, and admits each once the cleanup completes.
func TestReclaimHoldRefusesAdmissionUntilTheTeardownCompletes_spec_5_2(t *testing.T) {
	for _, ac := range admissionCalls() {
		t.Run(ac.name, func(t *testing.T) {
			s, rt := bindServer(t)
			if err := s.claimSessionForTest("alice"); err != nil {
				t.Fatalf("claim alice: %v", err)
			}
			parked := make(chan struct{})
			unpark := make(chan struct{})
			rt.setOnClose(func(string) error {
				close(parked)
				<-unpark
				return nil
			})
			done := startHeldTermination(s)
			<-parked
			// The parked termination holds the slot's guard, so a guarded
			// entry point that tested the hold only after waiting on the guard
			// would block here rather than return the refusal.
			if release, ok := s.lockSlotGuard(cancelledContext(), "alice"); ok {
				release()
				t.Fatal("the parked hold termination does not hold the slot's guard")
			}

			refused := make(chan error, 1)
			go func() { refused <- ac.call(s, "alice") }()
			select {
			case err := <-refused:
				if !isSlotReclaimInProgress(err) || status.Code(err) != codes.Aborted {
					t.Errorf("%s during the parked cleanup = %v, want the reclaim-hold refusal on Aborted", ac.name, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s blocked on the parked cleanup instead of being refused", ac.name)
			}

			close(unpark)
			<-done
			if err := ac.call(s, "alice"); isSlotReclaimInProgress(err) {
				t.Errorf("%s after the cleanup completed is still refused by the hold", ac.name)
			} else if err != nil {
				t.Errorf("%s after the cleanup completed = %v, want admitted", ac.name, err)
			}
		})
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow)
//
// Every site that deregisters an entry and then destroys its tree holds the
// identifier during the destruction and releases it once the cleanup
// completes. A site added without routing through reclaimSlotLocked fails
// the in-removal assertion.
func TestEveryDeregisterThenDestroySiteTakesTheHold_spec_5_2(t *testing.T) {
	cases := []struct {
		name    string
		destroy func(s *Server)
	}{
		{"releaseSessionSlot", func(s *Server) { s.releaseSessionSlot(t.Context(), "alice") }},
		{"releaseClaimedSlot", func(s *Server) {
			s.releaseClaimedSlot(t.Context(), "alice", slotClaim{entry: slotStateForTest(s, "alice")})
		}},
		{"hold termination", func(s *Server) { <-startHeldTermination(s) }},
		{"Shutdown removing arm", func(s *Server) { unconditionalShutdown(t, s, t.Context(), "alice") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := bindServer(t)
			if err := s.claimSessionForTest("alice"); err != nil {
				t.Fatalf("claim alice: %v", err)
			}
			var heldDuringRemoval bool
			s.removeSlotTreeFn = func(st *slotState) error {
				_, err := resolveLocked(s, "alice", slotResolve{bindAttempt: tokenA, allowCreate: true})
				heldDuringRemoval = isSlotReclaimInProgress(err)
				return removeSlotTree(st)
			}
			tc.destroy(s)
			if !heldDuringRemoval {
				t.Error("the identifier was not held while its tree was being removed")
			}
			if _, err := resolveLocked(s, "alice", slotResolve{bindAttempt: tokenA, allowCreate: true}); err != nil {
				t.Errorf("a bind after the completed cleanup = %v, want admitted", err)
			}
		})
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow)
//
// A tree removal that fails is a cleanup that did not complete, so the
// identifier stays held and the failure is logged.
func TestACleanupWhoseTreeRemovalFailsKeepsTheHold_spec_5_2(t *testing.T) {
	cases := []struct {
		name    string
		destroy func(s *Server)
	}{
		{"releaseSessionSlot", func(s *Server) { s.releaseSessionSlot(t.Context(), "alice") }},
		{"releaseClaimedSlot", func(s *Server) {
			s.releaseClaimedSlot(t.Context(), "alice", slotClaim{entry: slotStateForTest(s, "alice")})
		}},
		{"hold termination", func(s *Server) { <-startHeldTermination(s) }},
		{"Shutdown removing arm", func(s *Server) { unconditionalShutdown(t, s, t.Context(), "alice") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)
			s, _ := bindServer(t)
			if err := s.claimSessionForTest("alice"); err != nil {
				t.Fatalf("claim alice: %v", err)
			}
			s.removeSlotTreeFn = func(*slotState) error { return errors.New("injected removal failure") }
			tc.destroy(s)
			_, err := resolveLocked(s, "alice", slotResolve{bindAttempt: tokenA, allowCreate: true})
			if !isSlotReclaimInProgress(err) {
				t.Errorf("a bind after a failed tree removal = %v, want the reclaim-hold refusal", err)
			}
			if out := logs.String(); !strings.Contains(out, `"msg":"slot_tree_removal_failed"`) ||
				!strings.Contains(out, `"slot_id":"alice"`) {
				t.Errorf("no slot_tree_removal_failed record naming alice; logs: %s", out)
			}
		})
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow); §10.1.4 (coordinator-loss detection and hold state)
//
// A runtime close that fails at the hold termination keeps the hold, and
// the close stays best-effort in control flow: the final usage report, the
// tree removal and AdapterTerminating still happen.
func TestACleanupWhoseRuntimeCloseFailsKeepsTheHold_spec_5_2(t *testing.T) {
	setCoordinatorHold(false)
	logs := captureLogs(t)
	rt := &bindRuntime{}
	rt.setOnClose(func(string) error { return errors.New("injected close failure") })
	s, clk := holdTerminationServer(t, rt, "alice")
	meter := NewSessionUsageMeter(time.Now)
	meter.Add("alice", 5, 1)
	s.Usage = meter
	stream, cancel := attachControlStream(t, s)
	defer cancel()

	fireHoldTimeout(t, s, clk)

	var sawUsage, sawTerminating bool
	for _, ev := range drainControlEvents(t, stream, 2) {
		switch ev.Type {
		case eventFinalUsageReport:
			sawUsage = ev.SessionID == "alice"
		case eventAdapterTerminating:
			sawTerminating = ev.SessionID == "alice"
		}
	}
	if !sawUsage || !sawTerminating {
		t.Errorf("final usage = %v, AdapterTerminating = %v; want both for alice", sawUsage, sawTerminating)
	}
	if slotDirExists(s, "alice") {
		t.Error("the slot tree survived a termination whose runtime close failed")
	}
	_, err := resolveLocked(s, "alice", slotResolve{bindAttempt: tokenA, allowCreate: true})
	if !isSlotReclaimInProgress(err) {
		t.Errorf("a bind after a failed runtime close = %v, want the reclaim-hold refusal", err)
	}
	if out := logs.String(); !strings.Contains(out, `"msg":"runtime_close_failed"`) {
		t.Errorf("no runtime_close_failed record; logs: %s", out)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow); §10.1.4 (coordinator-loss detection and hold state)
//
// A panic out of the destructive section is a cleanup that did not
// complete, so the deferred release keeps the identifier held.
func TestAPanicOutOfTheDestructiveSectionKeepsTheHold_spec_5_2(t *testing.T) {
	s, rt := bindServer(t)
	if err := s.claimSessionForTest("alice"); err != nil {
		t.Fatalf("claim alice: %v", err)
	}
	rt.setOnClose(func(string) error { panic("injected close panic") })
	members := s.deregisterStartedSessions()
	if len(members) != 1 {
		t.Fatalf("pass 1 collected %d members, want 1", len(members))
	}
	func() {
		defer func() { _ = recover() }()
		s.terminateHeldSession(context.Background(), members[0])
	}()
	_, err := resolveLocked(s, "alice", slotResolve{bindAttempt: tokenA, allowCreate: true})
	if !isSlotReclaimInProgress(err) {
		t.Errorf("a bind after a panicking cleanup = %v, want the reclaim-hold refusal", err)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow); §16.3 (distributed tracing)
//
// Each resolve site passes the refusals through with their own codes and
// wraps every other resolve failure as InvalidArgument, and the sites that
// stamp a span category stamp the refusal's own category.
func TestSlotRefusalsKeepTheirClassificationThroughEveryResolveSite_spec_4_7_1(t *testing.T) {
	ctx := context.Background()
	type arm struct {
		name     string
		slotID   string
		setup    func(t *testing.T, s *Server)
		code     codes.Code
		category tracing.ErrorCategory
	}
	arms := []arm{
		{"reclaim hold", "alice", func(_ *testing.T, s *Server) {
			s.mu.Lock()
			s.openReclaimHoldLocked("alice")
			s.mu.Unlock()
		}, codes.Aborted, tracing.CategoryTransient},
		{
			"superseded", "alice", func(t *testing.T, s *Server) { seedEntry(t, s, "alice", tokenB, false) },
			codes.Aborted, tracing.CategoryTransient,
		},
		{
			"already started", "alice", func(t *testing.T, s *Server) { seedEntry(t, s, "alice", tokenA, true) },
			codes.FailedPrecondition, tracing.CategoryPermanent,
		},
		{"non-sentinel", "a/b", func(*testing.T, *Server) {}, codes.InvalidArgument, tracing.CategoryPermanent},
	}
	sites := []struct {
		name string
		span string
		call func(s *Server, id string) error
	}{
		{"resolvePrepareStagingDir", string(tracing.SpanSessionUpload), func(s *Server, id string) error {
			return s.PrepareWorkspace(&prepareWorkspaceStreamStub{
				ctx:    ctx,
				frames: []*adapterv1.PrepareWorkspaceRequest{prepareFrame(id, tokenA, false, "x")},
			})
		}},
		{"FinalizeWorkspace", string(tracing.SpanSessionFinalizeWorkspace), func(s *Server, id string) error {
			_, err := s.FinalizeWorkspace(ctx, &adapterv1.FinalizeWorkspaceRequest{
				SessionId: &adapterv1.SessionId{Value: id}, BindAttempt: tokenA,
				WorkspacePlan: &adapterv1.WorkspacePlan{SchemaVersion: 1},
			})
			return err
		}},
		{"RunSetup", string(tracing.SpanSessionRunSetup), func(s *Server, id string) error {
			_, err := s.RunSetup(ctx, &adapterv1.RunSetupRequest{
				SessionId: &adapterv1.SessionId{Value: id}, BindAttempt: tokenA,
			})
			return err
		}},
		{"claimSessionSlotUnderLock", "", func(s *Server, id string) error {
			_, _, err := s.claimSessionSlotUnderLock(id, slotResolve{bindAttempt: tokenA, allowCreate: true}, false, false)
			return err
		}},
		{"assignCredentialsSlot", "", func(s *Server, id string) error {
			_, err := s.assignCredentialsSlot(id, id, nil, slotResolve{bindAttempt: tokenA, allowCreate: true})
			return err
		}},
	}
	for _, site := range sites {
		for _, a := range arms {
			t.Run(site.name+"/"+a.name, func(t *testing.T) {
				rec := installInternalSpanRecorder(t)
				s, _ := bindServer(t)
				a.setup(t, s)
				err := site.call(s, a.slotID)
				if got := status.Code(err); got != a.code {
					t.Fatalf("code = %v (%v), want %v", got, err, a.code)
				}
				if site.span == "" {
					return
				}
				span := endedSpanNamed(rec.Ended(), site.span)
				if span == nil {
					t.Fatalf("span %s not recorded", site.span)
				}
				var got string
				for _, kv := range span.Attributes() {
					if string(kv.Key) == tracing.AttrErrorCategory {
						got = kv.Value.AsString()
					}
				}
				if got != string(a.category) {
					t.Errorf("span error.category = %q, want %q", got, a.category)
				}
			})
		}
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow)
//
// The token is a capability over a live session's teardown, so no refusal
// message, no detail and no log line the resolve path emits carries it.
func TestTheBindAttemptTokenIsNeverInAMessage_spec_4_7_1(t *testing.T) {
	logs := captureLogs(t)
	s, _ := bindServer(t)
	seedEntry(t, s, "alice", tokenA, false)
	seedEntry(t, s, "bob", tokenA, true)
	var errs []error
	_, err := s.FinalizeWorkspace(context.Background(), &adapterv1.FinalizeWorkspaceRequest{
		SessionId: &adapterv1.SessionId{Value: "alice"}, BindAttempt: tokenB,
		WorkspacePlan: &adapterv1.WorkspacePlan{SchemaVersion: 1},
	})
	errs = append(errs, err)
	_, err = s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "bob"}, BindAttempt: tokenA,
	})
	errs = append(errs, err)
	_, err = s.RunSetup(context.Background(), &adapterv1.RunSetupRequest{
		SessionId: &adapterv1.SessionId{Value: "alice"}, BindAttempt: tokenB,
	})
	errs = append(errs, err)
	for _, e := range errs {
		if e == nil {
			t.Fatal("a refusal case was admitted")
		}
		st, _ := status.FromError(e)
		texts := []string{e.Error(), st.Message()}
		for _, d := range st.Details() {
			if ae, ok := d.(*adapterv1.Error); ok {
				texts = append(texts, ae.GetMessage())
			}
		}
		for _, text := range texts {
			if strings.Contains(text, tokenA) || strings.Contains(text, tokenB) {
				t.Errorf("a refusal carries the token: %q", text)
			}
		}
	}
	if out := logs.String(); strings.Contains(out, tokenA) || strings.Contains(out, tokenB) {
		t.Errorf("a log line carries the token: %s", out)
	}
}

// unconditionalShutdown sends the unconditional form of Shutdown for
// slotID on ctx and fails the case on an RPC error.
func unconditionalShutdown(t *testing.T, s *Server, ctx context.Context, slotID string) *adapterv1.ShutdownResponse {
	t.Helper()
	resp, err := s.Shutdown(ctx, &adapterv1.ShutdownRequest{
		SessionId:             &adapterv1.SessionId{Value: slotID},
		UnconditionalTeardown: true,
	})
	if err != nil {
		t.Errorf("Shutdown %s: %v", slotID, err)
	}
	return resp
}

// cancelledContext returns a context that is already cancelled.
func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// holdGuard takes slotID's per-slot guard on the test's behalf, standing in
// for a section still inside its path work, and returns its release.
func holdGuard(t *testing.T, s *Server, slotID string) func() {
	t.Helper()
	release, ok := s.lockSlotGuard(context.Background(), slotID)
	if !ok {
		t.Fatalf("could not take the guard for %s", slotID)
	}
	return release
}

// slogRecords decodes the JSON slog records captured in buf.
func slogRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var recs []map[string]any
	for _, line := range strings.Split(buf.String(), "\n") {
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err == nil {
			recs = append(recs, rec)
		}
	}
	return recs
}

// guardNotAcquiredCallers returns the caller field of every
// slot_guard_not_acquired record naming slotID.
func guardNotAcquiredCallers(t *testing.T, buf *bytes.Buffer, slotID string) []string {
	t.Helper()
	var callers []string
	for _, rec := range slogRecords(t, buf) {
		if rec["msg"] == "slot_guard_not_acquired" && rec["slot_id"] == slotID {
			caller, _ := rec["caller"].(string)
			callers = append(callers, caller)
		}
	}
	return callers
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow)
//
// The acquisition step tries the guard before it consults the context, so a
// free guard is held on an already-cancelled context every time rather than
// on the half of the iterations a bare two-case select would pick the send.
// The guard-acquiring release on that context runs guarded, logs no expiry,
// and ends the reclaim hold.
func TestAnUncontendedAcquisitionOnACancelledContextHoldsTheGuard_spec_5_2(t *testing.T) {
	s, _ := bindServer(t)
	ctx := cancelledContext()
	for i := range 64 {
		release, ok := s.lockSlotGuard(ctx, "alice")
		if !ok {
			t.Fatalf("iteration %d: lockSlotGuard on a free guard and a cancelled context = false", i)
		}
		release()
		release, err := s.acquireSlotGuardForResolve(ctx, "alice")
		if err != nil {
			t.Fatalf("iteration %d: acquireSlotGuardForResolve on a free guard and a cancelled context = %v", i, err)
		}
		release()
	}

	logs := captureLogs(t)
	if err := s.claimSessionForTest("alice"); err != nil {
		t.Fatalf("claim alice: %v", err)
	}
	s.releaseSessionSlot(ctx, "alice")
	if callers := guardNotAcquiredCallers(t, logs, "alice"); len(callers) != 0 {
		t.Errorf("an uncontended release logged slot_guard_not_acquired for %v", callers)
	}
	if _, err := resolveLocked(s, "alice", slotResolve{bindAttempt: tokenA, allowCreate: true}); err != nil {
		t.Errorf("a bind after the guarded release = %v, want admitted because the hold was released", err)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow); §10.1.4 (coordinator-loss detection and hold state)
//
// A destructive section whose guard acquisition expires does not wait for
// the holder and does not abandon the removal: it removes the entry and the
// tree unguarded, logs slot_guard_not_acquired naming the slot and the
// removing site, and keeps the reclaim hold after the holder lets go,
// because an unguarded removal can run beside a request still writing
// under the identifier.
func TestADestructiveSectionWhoseGuardAcquisitionExpiresRemovesUnguardedAndKeepsTheHold_spec_5_2(t *testing.T) {
	cases := []struct {
		name    string
		caller  string
		destroy func(s *Server, ctx context.Context)
	}{
		{"releaseSessionSlot", "releaseSessionSlot", func(s *Server, ctx context.Context) {
			s.releaseSessionSlot(ctx, "alice")
		}},
		{"releaseClaimedSlot", "releaseClaimedSlot", func(s *Server, ctx context.Context) {
			s.releaseClaimedSlot(ctx, "alice", slotClaim{entry: slotStateForTest(s, "alice")})
		}},
		{"terminateHeldSession", "terminateHeldSession", func(s *Server, ctx context.Context) {
			for _, m := range s.deregisterStartedSessions() {
				s.terminateHeldSession(ctx, m)
			}
		}},
		{"Shutdown removing arm", "Shutdown", func(s *Server, ctx context.Context) {
			unconditionalShutdown(t, s, ctx, "alice")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)
			s, _ := bindServer(t)
			if err := s.claimSessionForTest("alice"); err != nil {
				t.Fatalf("claim alice: %v", err)
			}
			releaseHolder := holdGuard(t, s, "alice")

			done := make(chan struct{})
			go func() {
				defer close(done)
				tc.destroy(s, cancelledContext())
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				releaseHolder()
				t.Fatal("the destructive section waited for the guard holder past its context")
			}
			if hasEntry(s, "alice") || slotDirExists(s, "alice") {
				t.Error("an expired acquisition abandoned the removal; the entry or its tree survived")
			}
			callers := guardNotAcquiredCallers(t, logs, "alice")
			if len(callers) != 1 || callers[0] != tc.caller {
				t.Errorf("slot_guard_not_acquired callers = %v, want [%s]", callers, tc.caller)
			}
			releaseHolder()
			_, err := resolveLocked(s, "alice", slotResolve{bindAttempt: tokenA, allowCreate: true})
			if !isSlotReclaimInProgress(err) {
				t.Errorf("a bind after an unguarded removal = %v, want the reclaim-hold refusal", err)
			}
		})
	}
}

// guardedAdmissionCalls are the admission RPCs whose path work runs outside
// s.mu and which therefore take the per-slot guard ahead of their resolve.
func guardedAdmissionCalls(ctx context.Context) []admissionCall {
	return []admissionCall{
		{"PrepareWorkspace", func(s *Server, id string) error {
			return s.PrepareWorkspace(&prepareWorkspaceStreamStub{
				ctx:    ctx,
				frames: []*adapterv1.PrepareWorkspaceRequest{prepareFrame(id, tokenA, false, "x")},
			})
		}},
		{"FinalizeWorkspace", func(s *Server, id string) error {
			_, err := s.FinalizeWorkspace(ctx, &adapterv1.FinalizeWorkspaceRequest{
				SessionId: &adapterv1.SessionId{Value: id}, BindAttempt: tokenA,
				WorkspacePlan: &adapterv1.WorkspacePlan{SchemaVersion: 1},
			})
			return err
		}},
		{"RunSetup", func(s *Server, id string) error {
			_, err := s.RunSetup(ctx, &adapterv1.RunSetupRequest{
				SessionId: &adapterv1.SessionId{Value: id}, BindAttempt: tokenA,
			})
			return err
		}},
		{"Resume", func(s *Server, id string) error {
			_, err := s.Resume(ctx, &adapterv1.ResumeRequest{
				SessionId: &adapterv1.SessionId{Value: id}, CheckpointId: "ckpt-1", BindAttempt: tokenA,
			})
			return err
		}},
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow)
//
// An admission RPC whose guard acquisition outlives its context is refused
// with the context's own error before it resolves anything, so a
// materialization never proceeds unguarded and re-creates a tree a reclaim
// has just removed.
func TestAnAdmissionRPCWhoseGuardAcquisitionExpiresIsRefused_spec_5_2(t *testing.T) {
	for _, ac := range guardedAdmissionCalls(cancelledContext()) {
		t.Run(ac.name, func(t *testing.T) {
			s, _ := bindServer(t)
			releaseHolder := holdGuard(t, s, "alice")
			defer releaseHolder()
			if _, err := s.acquireSlotGuardForResolve(cancelledContext(), "alice"); !errors.Is(err, context.Canceled) {
				t.Fatalf("acquireSlotGuardForResolve on a held guard = %v, want context.Canceled", err)
			}
			err := ac.call(s, "alice")
			if !errors.Is(err, context.Canceled) {
				t.Errorf("%s with an expired guard acquisition = %v, want context.Canceled", ac.name, err)
			}
			if hasEntry(s, "alice") || slotDirExists(s, "alice") {
				t.Errorf("%s created an entry or a tree without holding the guard", ac.name)
			}
		})
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow)
//
// The per-slot guard brackets FinalizeWorkspace's resolve and its
// materialization: while another section holds the slot's guard the
// finalize neither resolves nor writes, and it runs once the guard is
// free. Two finalizes by the same attempt therefore never interleave their
// materialization, and each observes the whole tree the other built.
func TestThePerSlotGuardSerializesTheDestructiveSection_spec_5_2(t *testing.T) {
	t.Run("a held guard parks the finalize ahead of its resolve", func(t *testing.T) {
		s, _ := bindServer(t)
		releaseHolder := holdGuard(t, s, "alice")
		done := make(chan error, 1)
		go func() {
			_, err := s.FinalizeWorkspace(context.Background(),
				finalizeReq("alice", wsSource("inlineFile", "a.txt", "a", "644")))
			done <- err
		}()
		select {
		case err := <-done:
			releaseHolder()
			t.Fatalf("FinalizeWorkspace returned %v while another section held the slot's guard", err)
		case <-time.After(200 * time.Millisecond):
		}
		if hasEntry(s, "alice") || slotDirExists(s, "alice") {
			t.Error("the finalize resolved or wrote while another section held the slot's guard")
		}
		releaseHolder()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("FinalizeWorkspace after the guard was released = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("FinalizeWorkspace did not proceed once the guard was released")
		}
		if _, err := os.Stat(filepath.Join(s.WorkspaceBase, "slots", "alice", "current", "a.txt")); err != nil {
			t.Errorf("the materialized file is missing: %v", err)
		}
	})

	t.Run("concurrent finalizes by one attempt each build the whole tree", func(t *testing.T) {
		s, _ := bindServer(t)
		sources := []*adapterv1.WorkspaceSource{
			wsSource("mkdir", "docs", "", "755"),
			wsSource("inlineFile", "docs/a.txt", "a", "644"),
			wsSource("inlineFile", "docs/b.txt", "b", "644"),
		}
		const n = 8
		var wg sync.WaitGroup
		errs := make(chan error, n)
		for range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := s.FinalizeWorkspace(context.Background(), finalizeReq("alice", sources...))
				errs <- err
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Errorf("a concurrent finalize failed: %v", err)
			}
		}
		for _, name := range []string{"a.txt", "b.txt"} {
			if _, err := os.Stat(filepath.Join(s.WorkspaceBase, "slots", "alice", "current", "docs", name)); err != nil {
				t.Errorf("docs/%s missing after concurrent finalizes: %v", name, err)
			}
		}
	})
}
