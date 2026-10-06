// SPDX-License-Identifier: MIT

package executor

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// streamTestTimeout bounds every wait in this file so a regression fails
// rather than hangs.
const streamTestTimeout = 5 * time.Second

// scriptedStream is one Attach RPC the scripted adapter is serving. The test
// reads the envelopes the gateway wrote from recv, pushes frames to the
// gateway through push, ends the RPC with a status through end, and observes
// the RPC's context ending through ctxDone.
type scriptedStream struct {
	recv    chan []byte
	push    chan []byte
	end     chan error
	ctxDone chan struct{}
}

// scriptedAdapter is an AdapterServer whose Attach RPCs the test drives frame
// by frame, so a test can hold a turn open, deliver a late reply, flood the
// stream, or end it with a chosen gRPC status.
type scriptedAdapter struct {
	adapterv1.UnimplementedAdapterServer
	opened chan *scriptedStream
}

func (a *scriptedAdapter) Attach(stream grpc.BidiStreamingServer[adapterv1.AttachRequest, adapterv1.AttachResponse]) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	ss := &scriptedStream{
		recv:    make(chan []byte, 256),
		push:    make(chan []byte),
		end:     make(chan error, 1),
		ctxDone: make(chan struct{}),
	}
	ctx := stream.Context()
	go func() {
		<-ctx.Done()
		close(ss.ctxDone)
	}()
	go func() {
		for {
			req, err := stream.Recv()
			if err != nil {
				return
			}
			if env := req.GetEnvelopeJson(); len(env) > 0 {
				ss.recv <- env
			}
		}
	}()
	a.opened <- ss
	for {
		select {
		case f := <-ss.push:
			if err := stream.Send(&adapterv1.AttachResponse{EnvelopeJson: f}); err != nil {
				return err
			}
		case err := <-ss.end:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// dialScripted serves a scripted adapter over bufconn and returns it with a
// connected client.
func dialScripted(t *testing.T) (*scriptedAdapter, *adapterclient.Client) {
	t.Helper()
	a := &scriptedAdapter{opened: make(chan *scriptedStream, 8)}
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, a)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return a, cl
}

// newScriptedExecutor binds sess-1 of tenant acme on sbx-1 to a scripted
// adapter and returns the executor.
func newScriptedExecutor(t *testing.T) (*PodExecutor, *scriptedAdapter, *podsession.Registry) {
	t.Helper()
	a, cl := dialScripted(t)
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess-1", TenantID: "acme", SandboxName: "sbx-1", Adapter: cl})
	return NewPodExecutor(reg, nil), a, reg
}

func (a *scriptedAdapter) nextStream(t *testing.T) *scriptedStream {
	t.Helper()
	select {
	case ss := <-a.opened:
		return ss
	case <-time.After(streamTestTimeout):
		t.Fatal("adapter never received an Attach")
		return nil
	}
}

func (a *scriptedAdapter) assertNoNewStream(t *testing.T) {
	t.Helper()
	select {
	case <-a.opened:
		t.Fatal("the executor opened a second Attach stream, want the held one reused")
	default:
	}
}

// expectFrame returns the next envelope the gateway wrote, decoded.
func (ss *scriptedStream) expectFrame(t *testing.T) map[string]any {
	t.Helper()
	select {
	case env := <-ss.recv:
		var m map[string]any
		if err := json.Unmarshal(env, &m); err != nil {
			t.Fatalf("gateway wrote a non-JSON envelope %q: %v", env, err)
		}
		return m
	case <-time.After(streamTestTimeout):
		t.Fatal("gateway wrote no envelope")
		return nil
	}
}

func (ss *scriptedStream) pushFrame(t *testing.T, frame string) {
	t.Helper()
	select {
	case ss.push <- []byte(frame):
	case <-time.After(streamTestTimeout):
		t.Fatalf("adapter could not push %s: the gateway stopped reading", frame)
	}
}

func reply(text string) string {
	return `{"type":"response","text":"` + text + `"}`
}

// sendResult is a Send's outcome, delivered from a goroutine.
type sendResult struct {
	text string
	err  error
}

func sendAsync(ctx context.Context, e *PodExecutor, content string) <-chan sendResult {
	ch := make(chan sendResult, 1)
	go func() {
		out, err := e.Send(ctx, "sess-1", []Message{{Role: "user", Content: content}})
		var text string
		if len(out.Parts) > 0 {
			text = out.Parts[0].Text
		}
		ch <- sendResult{text: text, err: err}
	}()
	return ch
}

func awaitSend(t *testing.T, ch <-chan sendResult) sendResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(streamTestTimeout):
		t.Fatal("Send did not return")
		return sendResult{}
	}
}

// roundTrip sends one message on ctx and answers it with text.
func roundTrip(t *testing.T, ctx context.Context, e *PodExecutor, ss *scriptedStream, text string) sendResult {
	t.Helper()
	ch := sendAsync(ctx, e, "msg")
	if f := ss.expectFrame(t); f["type"] != "message" {
		t.Fatalf("gateway wrote %v, want a message envelope", f)
	}
	ss.pushFrame(t, reply(text))
	return awaitSend(t, ch)
}

// openRoundTrip sends one message that opens a new stream and answers it
// with text.
func openRoundTrip(t *testing.T, e *PodExecutor, a *scriptedAdapter, text string) sendResult {
	t.Helper()
	ch := sendAsync(context.Background(), e, "msg")
	ss := a.nextStream(t)
	if f := ss.expectFrame(t); f["type"] != "message" {
		t.Fatalf("gateway wrote %v, want a message envelope", f)
	}
	ss.pushFrame(t, reply(text))
	return awaitSend(t, ch)
}

func (e *PodExecutor) cachedConn(sessionID string) *attachConn {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.streams[sessionID]
}

func awaitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(streamTestTimeout):
		t.Fatalf("%s did not happen", what)
	}
}

// spec: 28.5.1 (Gateway-to-pod), 7.2 (Interactive Session Model)
// The second reply is the discriminating assertion: a stream opened on the
// first request's context ended when that request returned, so the second
// message failed.
func TestHeldAttachStreamOutlivesTheDeliveringRequest_spec_28_5_1(t *testing.T) {
	e, a, _ := newScriptedExecutor(t)

	ctx1, cancel1 := context.WithCancel(context.Background())
	first := sendAsync(ctx1, e, "one")
	ss := a.nextStream(t)
	ss.expectFrame(t)
	ss.pushFrame(t, reply("first"))
	if r := awaitSend(t, first); r.err != nil || r.text != "first" {
		t.Fatalf("first Send = %+v, want reply first", r)
	}
	cancel1()

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	if r := roundTrip(t, ctx2, e, ss, "second"); r.err != nil || r.text != "second" {
		t.Fatalf("second Send after the first request's context ended = %+v, want reply second", r)
	}
	a.assertNoNewStream(t)
	select {
	case <-ss.ctxDone:
		t.Fatal("the stream ended when the delivering request's context ended")
	default:
	}
}

// spec: 28.5.1 (Gateway-to-pod), 7.2 (Interactive Session Model)
// An abandoned turn returns promptly, keeps the stream, and its late reply
// is consumed by the reader rather than returned to the next turn, which
// waits for the token and receives its own reply.
func TestAbandonedTurnLateReplyNeverReachesTheNextTurn_spec_28_5_1(t *testing.T) {
	e, a, _ := newScriptedExecutor(t)
	ss := func() *scriptedStream {
		r := sendAsync(context.Background(), e, "open")
		ss := a.nextStream(t)
		ss.expectFrame(t)
		ss.pushFrame(t, reply("opened"))
		awaitSend(t, r)
		return ss
	}()
	held := e.cachedConn("sess-1")

	ctx, cancel := context.WithCancel(context.Background())
	abandoned := sendAsync(ctx, e, "slow")
	ss.expectFrame(t)
	cancel()
	if r := awaitSend(t, abandoned); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("abandoned Send = %+v, want context.Canceled", r)
	}
	if e.cachedConn("sess-1") != held {
		t.Fatal("abandoning a turn evicted the held stream")
	}

	next := sendAsync(context.Background(), e, "next")
	select {
	case f := <-ss.recv:
		t.Fatalf("next turn wrote %s while the abandoned turn held the token", f)
	case <-time.After(50 * time.Millisecond):
	}
	ss.pushFrame(t, reply("late"))
	ss.expectFrame(t)
	ss.pushFrame(t, reply("own"))
	if r := awaitSend(t, next); r.err != nil || r.text != "own" {
		t.Fatalf("next Send = %+v, want its own reply, not the abandoned turn's late reply", r)
	}
	a.assertNoNewStream(t)
}

// spec: 28.5.1 (Gateway-to-pod)
// EvictStream cancels the stream (the adapter observes the end) and removes
// the entry, and no stream-end observation follows a gateway-caused end.
func TestEvictStreamCancelsTheStreamAndIsSilent_spec_28_5_1(t *testing.T) {
	e, a, _ := newScriptedExecutor(t)
	var observed []error
	var mu sync.Mutex
	e.onStreamEnd = func(_, _, _ string, err error) {
		mu.Lock()
		observed = append(observed, err)
		mu.Unlock()
	}
	r := sendAsync(context.Background(), e, "open")
	ss := a.nextStream(t)
	ss.expectFrame(t)
	ss.pushFrame(t, reply("ok"))
	awaitSend(t, r)
	conn := e.cachedConn("sess-1")

	e.EvictStream("sess-1")
	awaitClosed(t, ss.ctxDone, "adapter observing the evicted stream's end")
	awaitClosed(t, conn.done, "reader exit after eviction")
	if e.cachedConn("sess-1") != nil {
		t.Error("EvictStream left the stream cached")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(observed) != 0 {
		t.Errorf("a gateway-caused end reached the stream-end observer: %v", observed)
	}
}

// spec: 28.5.1 (Gateway-to-pod)
// Release ends the held stream through EvictStream, so its end is not
// observed as a stream failure.
func TestReleaseEndOfStreamIsSilent_spec_28_5_1(t *testing.T) {
	e, a, reg := newScriptedExecutor(t)
	called := make(chan struct{}, 1)
	e.onStreamEnd = func(_, _, _ string, _ error) { called <- struct{}{} }
	r := sendAsync(context.Background(), e, "open")
	ss := a.nextStream(t)
	ss.expectFrame(t)
	ss.pushFrame(t, reply("ok"))
	awaitSend(t, r)
	conn := e.cachedConn("sess-1")

	// The binding is removed first so Release stops after the eviction and
	// needs no binder.
	reg.Remove("sess-1")
	if err := e.Release(context.Background(), "sess-1", ""); err != nil {
		t.Fatalf("Release: %v", err)
	}
	awaitClosed(t, conn.done, "reader exit after Release")
	select {
	case <-called:
		t.Error("a Release end reached the stream-end observer")
	default:
	}
}

// spec: 28.5.1 (Gateway-to-pod)
// A stream that ends on its own leaves the cache, the end is observed with
// the binding's identity and the status, and the next Send re-Attaches.
func TestStreamEndEvictsAndNextSendReattaches_spec_28_5_1(t *testing.T) {
	e, a, _ := newScriptedExecutor(t)
	type end struct {
		tenant, session, sandbox string
		code                     codes.Code
	}
	ends := make(chan end, 1)
	e.onStreamEnd = func(tenant, session, sandbox string, err error) {
		ends <- end{tenant, session, sandbox, status.Code(err)}
	}
	r := sendAsync(context.Background(), e, "open")
	ss := a.nextStream(t)
	ss.expectFrame(t)
	ss.pushFrame(t, reply("ok"))
	awaitSend(t, r)
	conn := e.cachedConn("sess-1")

	ss.end <- status.Error(codes.DeadlineExceeded, "runtime missed heartbeat ack deadline")
	awaitClosed(t, conn.done, "reader exit on stream end")
	if e.cachedConn("sess-1") != nil {
		t.Fatal("an ended stream stayed cached")
	}
	select {
	case got := <-ends:
		want := end{"acme", "sess-1", "sbx-1", codes.DeadlineExceeded}
		if got != want {
			t.Errorf("observed end = %+v, want %+v", got, want)
		}
	default:
		t.Fatal("the stream's end was not observed before done closed")
	}

	if r := openRoundTrip(t, e, a, "again"); r.err != nil || r.text != "again" {
		t.Fatalf("Send after the stream ended = %+v, want a reply on a new stream", r)
	}
}

// spec: 28.5.1 (Gateway-to-pod)
// A Send waiting on its reply returns a wrapped stream-ended error when the
// stream ends.
func TestSendWaitingOnAReplyReturnsWhenTheStreamEnds_spec_28_5_1(t *testing.T) {
	e, a, _ := newScriptedExecutor(t)
	pending := sendAsync(context.Background(), e, "hang")
	ss := a.nextStream(t)
	ss.expectFrame(t)
	ss.end <- status.Error(codes.Internal, "deliver message to runtime")
	r := awaitSend(t, pending)
	if !errors.Is(r.err, errStreamEnded) {
		t.Fatalf("Send = %+v, want a wrapped stream-ended error", r)
	}
	if !strings.Contains(r.err.Error(), "sess-1") {
		t.Errorf("stream-ended error %q does not name the session", r.err)
	}
}

// spec: 28.5.1 (Gateway-to-pod), 28.5.3 (Intra-pod)
// The reader drains unsolicited frames while no turn is registered, so a
// flood larger than the transport window never stalls the stream and the
// next Send still receives its own reply.
func TestReaderDrainsUnsolicitedFramesBetweenTurns_spec_28_5_1(t *testing.T) {
	e, a, _ := newScriptedExecutor(t)
	r := sendAsync(context.Background(), e, "open")
	ss := a.nextStream(t)
	ss.expectFrame(t)
	ss.pushFrame(t, reply("ok"))
	awaitSend(t, r)

	pad := strings.Repeat("x", 2048)
	for i := 0; i < 200; i++ {
		ss.pushFrame(t, `{"type":"status","detail":"`+pad+`"}`)
	}
	if r := roundTrip(t, context.Background(), e, ss, "mine"); r.err != nil || r.text != "mine" {
		t.Fatalf("Send after the flood = %+v, want its own reply", r)
	}
}

// spec: 28.5.1 (Gateway-to-pod)
// After an eviction and a re-Attach, the old stream's end leaves the new
// stream cached, whether the old end is gateway-caused or its own.
func TestStaleReaderLeavesTheReplacementCached_spec_28_5_1(t *testing.T) {
	e, a, _ := newScriptedExecutor(t)
	r := sendAsync(context.Background(), e, "open")
	old := a.nextStream(t)
	old.expectFrame(t)
	old.pushFrame(t, reply("ok"))
	awaitSend(t, r)
	oldConn := e.cachedConn("sess-1")

	e.EvictStream("sess-1")
	if r := openRoundTrip(t, e, a, "new"); r.err != nil {
		t.Fatalf("Send after eviction: %v", r.err)
	}
	fresh := e.cachedConn("sess-1")
	awaitClosed(t, oldConn.done, "old reader exit")
	if e.cachedConn("sess-1") != fresh || fresh == nil {
		t.Fatal("the evicted stream's end removed its replacement")
	}

	// A conn that ends on its own while another conn is cached leaves the
	// cached one in place.
	stale := &attachConn{sessionID: "sess-1", cancel: func() {}, done: make(chan struct{})}
	e.endConn(stale, status.Error(codes.Internal, "late end"))
	if e.cachedConn("sess-1") != fresh {
		t.Fatal("a stale conn's end removed the cached conn")
	}
}

// gateFunc adapts a function to ApprovalGate.
type gateFunc func(ctx context.Context, tenantID, sessionID string, call PendingToolCall) (ApprovalDecision, error)

func (f gateFunc) AwaitApproval(ctx context.Context, tenantID, sessionID string, call PendingToolCall) (ApprovalDecision, error) {
	return f(ctx, tenantID, sessionID, call)
}

const approvalCall = `{"type":"tool_call","id":"tc-1","name":"lenny/deploy","arguments":{},"approvalRequired":true}`

// spec: 7.2 (Interactive Session Model), 28.5.1 (Gateway-to-pod)
// An approval-required tool_call that arrives while no turn is registered is
// still gated with the binding's tenant and answered, so the runtime never
// blocks on a call the gateway discarded.
func TestBetweenTurnApprovalIsGatedAndAnswered_spec_7_2(t *testing.T) {
	e, a, _ := newScriptedExecutor(t)
	gated := make(chan string, 1)
	e.SetApprovalGate(gateFunc(func(_ context.Context, tenantID, sessionID string, call PendingToolCall) (ApprovalDecision, error) {
		gated <- tenantID + "/" + sessionID + "/" + call.ID
		return ApprovalDecision{Approved: true}, nil
	}))
	r := sendAsync(context.Background(), e, "open")
	ss := a.nextStream(t)
	ss.expectFrame(t)
	ss.pushFrame(t, reply("ok"))
	awaitSend(t, r)

	ss.pushFrame(t, approvalCall)
	f := ss.expectFrame(t)
	if f["type"] != "tool_call" || f["id"] != "tc-1" || f["approvalRequired"] == true {
		t.Fatalf("relayed verdict = %v, want the approved tool_call with the flag cleared", f)
	}
	if got := <-gated; got != "acme/sess-1/tc-1" {
		t.Errorf("gate called with %q, want acme/sess-1/tc-1", got)
	}
}

// spec: 7.2 (Interactive Session Model), 28.5.1 (Gateway-to-pod)
// A gate error fails closed: mid-turn and between turns the reader relays a
// deny tool_result with isError and the reason approval_error, and the gated
// turn still receives its response.
func TestApprovalGateErrorRelaysADeny_spec_7_2(t *testing.T) {
	e, a, _ := newScriptedExecutor(t)
	e.SetApprovalGate(gateFunc(func(context.Context, string, string, PendingToolCall) (ApprovalDecision, error) {
		return ApprovalDecision{}, errors.New("interaction store unavailable")
	}))
	assertDeny := func(f map[string]any) {
		t.Helper()
		content, _ := f["content"].([]any)
		var reason string
		if len(content) == 1 {
			part, _ := content[0].(map[string]any)
			reason, _ = part["inline"].(string)
		}
		if f["type"] != "tool_result" || f["id"] != "tc-1" || f["isError"] != true || reason != approvalErrorReason {
			t.Fatalf("relayed verdict = %v, want a deny tool_result with isError and reason %s", f, approvalErrorReason)
		}
	}

	// Mid-turn.
	pending := sendAsync(context.Background(), e, "deploy")
	ss := a.nextStream(t)
	ss.expectFrame(t)
	ss.pushFrame(t, approvalCall)
	assertDeny(ss.expectFrame(t))
	ss.pushFrame(t, reply("denied"))
	if r := awaitSend(t, pending); r.err != nil || r.text != "denied" {
		t.Fatalf("gated Send = %+v, want the turn's response", r)
	}

	// Between turns.
	ss.pushFrame(t, approvalCall)
	assertDeny(ss.expectFrame(t))
}

// spec: 7.2 (Interactive Session Model), 28.5.1 (Gateway-to-pod)
// When EvictStream runs while the reader waits in the gate, the reader
// relays nothing after the gate returns, and the waiting Send returns the
// wrapped stream-ended error. With a live conn the same gate error relays a
// deny (TestApprovalGateErrorRelaysADeny_spec_7_2).
func TestEvictedConnRelaysNoVerdict_spec_7_2(t *testing.T) {
	e, a, _ := newScriptedExecutor(t)
	inGate := make(chan struct{})
	gateReturned := make(chan struct{})
	e.SetApprovalGate(gateFunc(func(ctx context.Context, _, _ string, _ PendingToolCall) (ApprovalDecision, error) {
		close(inGate)
		<-ctx.Done()
		defer close(gateReturned)
		return ApprovalDecision{}, ctx.Err()
	}))
	pending := sendAsync(context.Background(), e, "deploy")
	ss := a.nextStream(t)
	ss.expectFrame(t)
	ss.pushFrame(t, approvalCall)
	awaitClosed(t, inGate, "reader entering the gate")
	conn := e.cachedConn("sess-1")

	e.EvictStream("sess-1")
	awaitClosed(t, gateReturned, "gate returning after eviction")
	r := awaitSend(t, pending)
	if !errors.Is(r.err, errStreamEnded) {
		t.Fatalf("waiting Send = %+v, want the wrapped stream-ended error", r)
	}
	awaitClosed(t, conn.done, "reader exit")
	select {
	case f := <-ss.recv:
		t.Fatalf("an evicted conn relayed %s", f)
	default:
	}
}

// spec: 28.5.1 (Gateway-to-pod)
// A failed open is returned to every waiting Send, and the next Send opens a
// new stream.
func TestFailedOpenReachesEveryWaiterAndIsRetried_spec_28_5_1(t *testing.T) {
	release := make(chan struct{})
	dialed := make(chan struct{}, 8)
	cl, err := adapterclient.Dial("passthrough:///unreachable",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			dialed <- struct{}{}
			<-release
			return nil, errors.New("adapter unreachable")
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess-1", TenantID: "acme", SandboxName: "sbx-1", Adapter: cl})
	e := NewPodExecutor(reg, nil)

	waiters := []<-chan sendResult{
		sendAsync(context.Background(), e, "a"),
		sendAsync(context.Background(), e, "b"),
		sendAsync(context.Background(), e, "c"),
	}
	select {
	case <-dialed:
	case <-time.After(streamTestTimeout):
		t.Fatal("the open never dialed the adapter")
	}
	close(release)
	for i, w := range waiters {
		if r := awaitSend(t, w); r.err == nil {
			t.Errorf("waiter %d succeeded over a failed open", i)
		}
	}
	if e.cachedConn("sess-1") != nil {
		t.Fatal("a failed open stayed cached")
	}

	a, good := dialScripted(t)
	reg.Put(&podsession.BindResult{SessionID: "sess-1", TenantID: "acme", SandboxName: "sbx-1", Adapter: good})
	if r := openRoundTrip(t, e, a, "opened"); r.err != nil || r.text != "opened" {
		t.Fatalf("Send after a failed open = %+v, want a reply on a new stream", r)
	}
}

// spec: 28.5.1 (Gateway-to-pod)
// A binding with no adapter connection fails the open rather than panicking.
func TestOpenWithoutAdapterFails_spec_28_5_1(t *testing.T) {
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess-1"})
	e := NewPodExecutor(reg, nil)
	if _, err := e.Send(context.Background(), "sess-1", []Message{{Content: "x"}}); err == nil {
		t.Fatal("Send over a binding with no adapter succeeded")
	}
}

// spec: 28.5.1 (Gateway-to-pod)
// A Send whose context ends while it waits for the turn token returns the
// context error without writing.
func TestSendWaitingForTheTokenHonorsItsContext_spec_28_5_1(t *testing.T) {
	e, a, _ := newScriptedExecutor(t)
	first := sendAsync(context.Background(), e, "first")
	ss := a.nextStream(t)
	ss.expectFrame(t)

	ctx, cancel := context.WithCancel(context.Background())
	second := sendAsync(ctx, e, "second")
	cancel()
	if r := awaitSend(t, second); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("Send waiting for the token = %+v, want context.Canceled", r)
	}
	ss.pushFrame(t, reply("one"))
	if r := awaitSend(t, first); r.text != "one" {
		t.Fatalf("first Send = %+v, want reply one", r)
	}
	select {
	case f := <-ss.recv:
		t.Fatalf("a cancelled Send wrote %s", f)
	default:
	}
}

// spec: 28.5.1 (Gateway-to-pod)
// A turn whose write fails clears its registration and returns the token,
// because no reply will arrive to complete it, so the next turn is not
// blocked behind a message the runtime never received.
func TestFailedWriteReleasesTheTurn_spec_28_5_1(t *testing.T) {
	_, cl := dialScripted(t)
	streamCtx, cancelStream := context.WithCancel(context.Background())
	stream, err := cl.Attach(streamCtx, "sess-1")
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	cancelStream()
	// Recv returns once the client stream has finished, after which every
	// write fails.
	if _, err := stream.Recv(); err == nil {
		t.Fatal("Recv on a cancelled stream succeeded")
	}
	c := newAttachConn(context.Background(), &podsession.BindResult{SessionID: "sess-1"})
	defer c.cancel()
	c.stream = stream
	close(c.ready)

	ctx, cancel := context.WithTimeout(context.Background(), streamTestTimeout)
	defer cancel()
	if _, err := c.runTurn(ctx, []byte(`{"type":"message"}`)); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runTurn on a finished stream = %v, want the write error", err)
	}
	if len(c.token) != 0 {
		t.Error("a failed write kept the turn token")
	}
	c.turnMu.Lock()
	defer c.turnMu.Unlock()
	if c.turn != nil {
		t.Error("a failed write left its turn registered")
	}
}

// spec: 28.5.1 (Gateway-to-pod)
// A turn on a conn whose stream has ended returns the stream-ended error
// without taking the token, whether the token is free or held.
func TestTurnOnAnEndedStreamFails_spec_28_5_1(t *testing.T) {
	for _, held := range []bool{false, true} {
		c := newAttachConn(context.Background(), &podsession.BindResult{SessionID: "sess-1"})
		if held {
			c.token <- struct{}{}
		}
		close(c.done)
		_, err := c.runTurn(context.Background(), []byte(`{}`))
		if !errors.Is(err, errStreamEnded) {
			t.Errorf("token held %v: runTurn = %v, want the stream-ended error", held, err)
		}
		want := 0
		if held {
			want = 1
		}
		if len(c.token) != want {
			t.Errorf("token held %v: token count %d, want %d", held, len(c.token), want)
		}
		c.cancel()
	}
}

// spec: 28.5.1 (Gateway-to-pod)
// A Send waiting for an open that has not completed returns when its own
// context ends.
func TestWaitForOpenHonorsTheCallerContext_spec_28_5_1(t *testing.T) {
	c := newAttachConn(context.Background(), &podsession.BindResult{SessionID: "sess-1"})
	defer c.cancel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.waitReady(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitReady = %v, want context.Canceled", err)
	}
}
