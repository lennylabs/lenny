// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// fakeRuntime is the test double for RuntimeProcess. The §15.4.1
// heartbeat monitor writes frames and may interrupt from a background
// goroutine while a test reads the recorded slices, so the recording
// fields are guarded by mu; use the *Snapshot accessors from a test that
// runs alongside an active Attach loop.
type fakeRuntime struct {
	mu           sync.Mutex
	started      []string
	startErr     error
	envelopes    [][]byte
	writeErr     error
	closed       []string
	closeCtxDL   time.Duration // remaining ctx deadline at Close, if any
	closeHadDL   bool
	interrupts   []bool // the hard flag of each Interrupt call
	interruptErr error
	output       chan []byte // when set, Output fans this stream out to subscribers
	outputErr    error
	echoInput    bool // when set, WriteEnvelope echoes the envelope to output

	// subs holds the per-Output subscriber channels the single f.output
	// stream fans out to, mirroring the production SocketRuntimeProcess:
	// one runtime process per pod serves every slot over one connection,
	// so each concurrent Attach stream sees the runtime's full output and
	// demultiplexes by slotId. fanOnce starts the single fan-out reader on
	// the first Output call. subCond signals every change to len(subs) so a
	// concurrent-slot test can wait for both Attach handlers to subscribe
	// before writing to f.output, since a frame written before a slot
	// subscribes is not delivered to that slot's later subscription.
	subs    []chan []byte
	subCond *sync.Cond
	fanOnce sync.Once
}

func (f *fakeRuntime) Start(_ context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
	f.started = append(f.started, sessionID)
	return nil
}

func (f *fakeRuntime) WriteEnvelope(_ string, envelope []byte) error {
	f.mu.Lock()
	if f.writeErr != nil {
		err := f.writeErr
		f.mu.Unlock()
		return err
	}
	f.envelopes = append(f.envelopes, envelope)
	echo := f.echoInput && f.output != nil
	out := f.output
	f.mu.Unlock()
	// Echo outside the lock so a blocking channel send never stalls a
	// concurrent reader of the recorded slices.
	if echo {
		out <- append([]byte(nil), envelope...)
	}
	return nil
}

func (f *fakeRuntime) Output(_ context.Context, _ string) (<-chan []byte, error) {
	if f.outputErr != nil {
		return nil, f.outputErr
	}
	if f.output == nil {
		ch := make(chan []byte)
		close(ch)
		return ch, nil
	}
	// Subscribe to the fanned-out stream. The first Output call starts the
	// single reader that broadcasts f.output to every subscriber, so two
	// concurrent per-slot Attach streams each receive the full output and
	// demultiplex by slotId (matching SocketRuntimeProcess).
	sub := make(chan []byte, 8)
	f.mu.Lock()
	f.subs = append(f.subs, sub)
	if f.subCond == nil {
		f.subCond = sync.NewCond(&f.mu)
	}
	f.subCond.Broadcast()
	f.mu.Unlock()
	f.fanOnce.Do(func() {
		go func() {
			for line := range f.output {
				f.mu.Lock()
				subs := append([]chan []byte(nil), f.subs...)
				f.mu.Unlock()
				for _, s := range subs {
					s <- line
				}
			}
			f.mu.Lock()
			subs := append([]chan []byte(nil), f.subs...)
			f.mu.Unlock()
			for _, s := range subs {
				close(s)
			}
		}()
	})
	return sub, nil
}

func (f *fakeRuntime) Interrupt(_ context.Context, _ string, hard bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.interruptErr != nil {
		return f.interruptErr
	}
	f.interrupts = append(f.interrupts, hard)
	return nil
}

func (f *fakeRuntime) Close(ctx context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = append(f.closed, sessionID)
	if dl, ok := ctx.Deadline(); ok {
		f.closeHadDL = true
		f.closeCtxDL = time.Until(dl)
	}
	return nil
}

// interruptsSnapshot returns a copy of the recorded Interrupt hard flags
// under the lock, for tests that read alongside a live Attach loop.
func (f *fakeRuntime) interruptsSnapshot() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.interrupts...)
}

// waitForSubscribers blocks until at least n Output subscribers have
// registered. A concurrent-slot test calls this before writing to f.output
// so every per-slot Attach handler has subscribed to the single fan-out
// first; the production SocketRuntimeProcess delivers each frame only to the
// subscribers present when the frame arrives, so a frame written before a
// slot subscribes would be lost to that slot. The timeout fails the test
// rather than hanging when a handler never subscribes.
func (f *fakeRuntime) waitForSubscribers(t *testing.T, n int) {
	t.Helper()
	f.mu.Lock()
	if f.subCond == nil {
		f.subCond = sync.NewCond(&f.mu)
	}
	// A watchdog goroutine wakes the wait so a missing subscriber surfaces
	// as a test failure instead of a deadlock.
	timedOut := false
	timer := time.AfterFunc(5*time.Second, func() {
		f.mu.Lock()
		timedOut = true
		f.subCond.Broadcast()
		f.mu.Unlock()
	})
	defer timer.Stop()
	for len(f.subs) < n && !timedOut {
		f.subCond.Wait()
	}
	got := len(f.subs)
	f.mu.Unlock()
	if got < n {
		t.Fatalf("only %d of %d Output subscribers registered before timeout", got, n)
	}
}

// envelopesSnapshot returns a copy of the frames written to the runtime
// under the lock.
func (f *fakeRuntime) envelopesSnapshot() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.envelopes))
	copy(out, f.envelopes)
	return out
}

// shortSocketName returns a Unix socket path under a short temp directory.
// t.TempDir() embeds the (often long) test name, so a socket derived from it
// can overflow the platform sun_path limit (~104 bytes on darwin); binding
// under os.MkdirTemp's short root keeps the path within that limit.
func shortSocketName(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "lenny-sock-*")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}

// sessionServer builds an adapter Server wired to a fresh workspace
// directory and a fake runtime.
func sessionServer(t *testing.T) (*adapter.Server, *fakeRuntime, string) {
	t.Helper()
	root := t.TempDir()
	rt := &fakeRuntime{}
	s := adapter.New("test")
	s.WorkspaceRoot = root
	s.Runtime = rt
	return s, rt, root
}

func startReq(sessionID string) *adapterv1.StartSessionRequest {
	return &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo",
	}
}

func TestStartSessionStartsRuntime(t *testing.T) {
	// Workspace materialization and setup are handled by FinalizeWorkspace
	// and RunSetup; StartSession claims the pod and starts the runtime.
	s, rt, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if len(rt.started) != 1 || rt.started[0] != "sess-1" {
		t.Errorf("runtime started = %v, want [sess-1]", rt.started)
	}
}

func TestStartSessionRejectsSecondSession(t *testing.T) {
	s, _, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("first StartSession: %v", err)
	}
	_, err := s.StartSession(context.Background(), startReq("sess-2"))
	if status.Code(err) != codes.Unavailable {
		t.Errorf("second StartSession code = %v, want Unavailable", status.Code(err))
	}
}

func TestStartSessionRejectsEmptySessionID(t *testing.T) {
	s, _, _ := sessionServer(t)
	_, err := s.StartSession(context.Background(), startReq(""))
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestStartSessionRequiresConfiguration(t *testing.T) {
	s := adapter.New("test") // no WorkspaceRoot, no Runtime
	_, err := s.StartSession(context.Background(), startReq("sess-1"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition", status.Code(err))
	}
}

func TestSendMessageForwardsEnvelopeToRuntime(t *testing.T) {
	s, rt, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	envelope := []byte(`{"type":"message","input":[{"type":"text","inline":"hi"}]}`)
	_, err := s.SendMessage(context.Background(), &adapterv1.SendMessageRequest{
		SessionId:    &adapterv1.SessionId{Value: "sess-1"},
		EnvelopeJson: envelope,
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if len(rt.envelopes) != 1 || string(rt.envelopes[0]) != string(envelope) {
		t.Errorf("runtime received envelopes %v, want one matching the request", rt.envelopes)
	}
}

func TestSendMessageRejectsUnknownSession(t *testing.T) {
	s, _, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	_, err := s.SendMessage(context.Background(), &adapterv1.SendMessageRequest{
		SessionId:    &adapterv1.SessionId{Value: "sess-other"},
		EnvelopeJson: []byte(`{}`),
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("code = %v, want NotFound for a session not assigned to the pod", status.Code(err))
	}
}

func TestSendMessageRejectsWhenNoSessionAssigned(t *testing.T) {
	s, _, _ := sessionServer(t)
	_, err := s.SendMessage(context.Background(), &adapterv1.SendMessageRequest{
		SessionId:    &adapterv1.SessionId{Value: "sess-1"},
		EnvelopeJson: []byte(`{}`),
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition when the pod is idle", status.Code(err))
	}
}

func TestSendMessageRejectsEmptyEnvelope(t *testing.T) {
	s, _, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	_, err := s.SendMessage(context.Background(), &adapterv1.SendMessageRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument for an empty envelope", status.Code(err))
	}
}

func TestShutdownClosesRuntimeAndReleasesPod(t *testing.T) {
	s, rt, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	resp, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
	})
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !resp.ExitedCleanly {
		t.Error("Shutdown reported an unclean exit for a healthy runtime")
	}
	if len(rt.closed) != 1 || rt.closed[0] != "sess-1" {
		t.Errorf("runtime closed = %v, want [sess-1]", rt.closed)
	}
	// The pod must be idle again so a replacement session can be assigned.
	if _, retryErr := s.StartSession(context.Background(), startReq("sess-2")); retryErr != nil {
		t.Errorf("pod was not released after Shutdown: %v", retryErr)
	}
}

// TestShutdownPlumbsDeadlineMsIntoRuntimeClose asserts §11.4 step 3:
// the §4.7 ShutdownRequest.deadline_ms field flows into the runtime
// adapter's Close as a context deadline, so the §11.4 10s graceful
// window is honored by the adapter instead of an internal default.
// spec: §11.4 line 258.
func TestShutdownPlumbsDeadlineMsIntoRuntimeClose_spec_11_4_258(t *testing.T) {
	s, rt, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId:  &adapterv1.SessionId{Value: "sess-1"},
		DeadlineMs: 10_000,
	}); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !rt.closeHadDL {
		t.Fatal("Runtime.Close got no context deadline; §11.4 step-3 grace window is dropped")
	}
	// The remaining must be ≤10s (the request grace) and >0; CI jitter
	// can shave milliseconds off the budget.
	if rt.closeCtxDL <= 0 || rt.closeCtxDL > 10*time.Second {
		t.Errorf("Close ctx deadline remaining = %v, want (0, 10s]", rt.closeCtxDL)
	}
}

// TestShutdownWithoutDeadlineMsInheritsContext asserts that a request
// with a zero (or absent) deadline_ms falls through to the inbound RPC
// context, preserving the legacy behavior for callers that do not pin
// the §11.4 graceful window. spec: §11.4 line 258.
func TestShutdownWithoutDeadlineMsInheritsContext_spec_11_4_258(t *testing.T) {
	s, rt, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
	}); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if rt.closeHadDL {
		t.Errorf("Runtime.Close got a derived deadline when none was requested (remaining=%v)", rt.closeCtxDL)
	}
}

func TestShutdownRejectsUnknownSession(t *testing.T) {
	s, _, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	_, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-other"},
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("code = %v, want NotFound", status.Code(err))
	}
}

func TestStartSessionReleasesPodOnRuntimeFailure(t *testing.T) {
	root := t.TempDir()
	s := adapter.New("test")
	s.WorkspaceRoot = root
	s.Runtime = &fakeRuntime{startErr: errors.New("runtime crashed")}

	_, err := s.StartSession(context.Background(), startReq("sess-1"))
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal", status.Code(err))
	}
	// A healthy runtime must be able to take a fresh session afterward.
	s.Runtime = &fakeRuntime{}
	if _, retryErr := s.StartSession(context.Background(), startReq("sess-2")); retryErr != nil {
		t.Errorf("pod was not released after a runtime-start failure: %v", retryErr)
	}
}
