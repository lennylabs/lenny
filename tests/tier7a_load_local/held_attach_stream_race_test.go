// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the session-scoped Attach
// stream the pod executor holds for each pod-backed session.
//
// The executor holds one stream per session from the first message until
// the binding is released, with one reader goroutine per stream and a
// one-slot turn token that serializes turns. Several things act on that
// stream at once in production: concurrent Send calls for the same session,
// a client request that ends mid-turn and abandons its turn, the
// coordination sweep's EvictStream, Release, the adapter ending the stream
// on its own, and the reader relaying a tool-use approval verdict while a
// turn's message is being written. These cases drive those actors together
// under the race detector and assert the invariants a race would break: a
// Send only ever receives the reply to its own message, every approval
// receives exactly one verdict, an open blocked on an unreachable adapter
// delays no other session, and no reader or open goroutine outlives the
// stream it served.
//
// spec: §28.5.1 (Gateway-to-pod, CH-ATTACH), §7.2 (Interactive Session
// Model).
package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// heldWait bounds every wait in this file so a deadlock fails the test
// rather than hanging the tier.
const heldWait = 10 * time.Second

// heldEchoAdapter is an AdapterServer whose Attach RPCs answer every
// message envelope with a `response` echoing the message's text, after a
// short varying delay so turns overlap request deadlines. It can end each
// stream with a gRPC status after a fixed number of messages, push
// approval-required tool calls on its own schedule, and it records every
// verdict the gateway relays.
type heldEchoAdapter struct {
	adapterv1.UnimplementedAdapterServer

	// endAfter, when positive, ends each stream with codes.Internal after
	// that many messages, so the executor's reader sees an end it did not
	// cause.
	endAfter int64

	// approvals, when positive, makes each stream push that many
	// approval-required tool calls, interleaved with the turns.
	approvals int

	mu       sync.Mutex
	verdicts map[string]int
}

func (a *heldEchoAdapter) recordVerdict(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.verdicts == nil {
		a.verdicts = map[string]int{}
	}
	a.verdicts[id]++
}

func (a *heldEchoAdapter) verdictCounts() map[string]int {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]int, len(a.verdicts))
	for k, v := range a.verdicts {
		out[k] = v
	}
	return out
}

// heldInbound is the subset of an inbound envelope the fake adapter reads.
type heldInbound struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Input []struct {
		Inline string `json:"inline"`
	} `json:"input"`
}

func (a *heldEchoAdapter) Attach(stream grpc.BidiStreamingServer[adapterv1.AttachRequest, adapterv1.AttachResponse]) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	out := make(chan []byte, 64)
	ended := make(chan error, 1)
	go a.readInbound(ctx, stream, out, ended)
	if a.approvals > 0 {
		go pushApprovals(ctx, out, a.approvals)
	}
	// One goroutine owns stream.Send: gRPC SendMsg is not safe for
	// concurrent calls.
	for {
		select {
		case f := <-out:
			if err := stream.Send(&adapterv1.AttachResponse{EnvelopeJson: f}); err != nil {
				return err
			}
		case err := <-ended:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// readInbound answers each message with its echo and records each verdict.
// It reports the stream's scripted end on ended.
func (a *heldEchoAdapter) readInbound(ctx context.Context, stream grpc.BidiStreamingServer[adapterv1.AttachRequest, adapterv1.AttachResponse], out chan<- []byte, ended chan<- error) {
	var messages int64
	for {
		req, err := stream.Recv()
		if err != nil {
			return
		}
		var in heldInbound
		if json.Unmarshal(req.GetEnvelopeJson(), &in) != nil {
			continue
		}
		switch in.Type {
		case "tool_call", "tool_result":
			a.recordVerdict(in.ID)
			continue
		case "message":
		default:
			continue
		}
		text := ""
		if len(in.Input) > 0 {
			text = in.Input[0].Inline
		}
		messages++
		delay := time.Duration(messages%4) * time.Millisecond
		go func() {
			time.Sleep(delay)
			b, _ := json.Marshal(map[string]any{"type": "response", "text": "echo:" + text})
			select {
			case out <- b:
			case <-ctx.Done():
			}
		}()
		if a.endAfter > 0 && messages >= a.endAfter {
			time.Sleep(delay + time.Millisecond)
			ended <- status.Error(codes.Internal, "scripted end of stream")
			return
		}
	}
}

// pushApprovals pushes n approval-required tool calls with distinct ids.
func pushApprovals(ctx context.Context, out chan<- []byte, n int) {
	for i := 0; i < n; i++ {
		f := fmt.Sprintf(`{"type":"tool_call","id":"tc-%d","name":"lenny/deploy","arguments":{},"approvalRequired":true}`, i)
		select {
		case out <- []byte(f):
		case <-ctx.Done():
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// heldServe serves srv over bufconn and returns a connected client.
func heldServe(t *testing.T, srv adapterv1.AdapterServer) *adapterclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return heldDial(t, func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) })
}

func heldDial(t *testing.T, dialer func(context.Context, string) (net.Conn, error)) *adapterclient.Client {
	t.Helper()
	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// heldBind publishes a binding for each session on cl.
func heldBind(reg *podsession.Registry, cl *adapterclient.Client, sessions ...string) {
	for _, id := range sessions {
		reg.Put(&podsession.BindResult{SessionID: id, TenantID: "acme", SandboxName: "sbx-" + id, Adapter: cl})
	}
}

// heldSend sends content on sessionID and fails the test when a successful
// reply is not the echo of content. It returns the Send's error.
func heldSend(t *testing.T, ctx context.Context, e *executor.PodExecutor, sessionID, content string) error {
	out, err := e.Send(ctx, sessionID, []executor.Message{{Role: "user", Content: content}})
	if err != nil {
		return err
	}
	if len(out.Parts) != 1 || out.Parts[0].Text != "echo:"+content {
		t.Errorf("Send %q on %s received %+v, want only its own reply", content, sessionID, out.Parts)
	}
	return nil
}

// heldRelease ends every session's stream through Release. The binding is
// removed first, so Release stops after the stream eviction and needs no
// pod binder.
func heldRelease(t *testing.T, e *executor.PodExecutor, reg *podsession.Registry, sessions ...string) {
	t.Helper()
	for _, id := range sessions {
		reg.Remove(id)
		if err := e.Release(context.Background(), id, ""); err != nil {
			t.Errorf("Release %s: %v", id, err)
		}
	}
}

// heldExecutorGoroutines returns the stacks of live goroutines running the
// executor's stream reader or stream open.
func heldExecutorGoroutines() []string {
	buf := make([]byte, 1<<22)
	buf = buf[:runtime.Stack(buf, true)]
	var leaked []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "executor.(*PodExecutor).readConn") ||
			strings.Contains(g, "executor.(*PodExecutor).openConn") {
			leaked = append(leaked, g)
		}
	}
	return leaked
}

// heldAssertNoStreamGoroutines waits for every reader and open goroutine to
// exit, and fails with their stacks when one remains.
func heldAssertNoStreamGoroutines(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(heldWait)
	for {
		leaked := heldExecutorGoroutines()
		if len(leaked) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d executor stream goroutine(s) outlived Release:\n%s", len(leaked), strings.Join(leaked, "\n\n"))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// spec: 28.5.1 (Gateway-to-pod), 7.2 (Interactive Session Model)
// diagnosis: concurrent Send, EvictStream, Release, and adapter-side stream
// ends on the same sessions raced on the executor's stream cache, the turn
// token, or the conn's lifecycle. A race-detector report means unguarded
// shared state; a Send that received another turn's reply means an
// abandoned turn's late reply or a stale reader reached a later turn; a
// leftover reader or open goroutine after Release means a stream's end did
// not stop the goroutine serving it.
func TestHeldStreamSendEvictReleaseAndStreamEndRace_spec_28_5_1(t *testing.T) {
	adapterSrv := &heldEchoAdapter{endAfter: 7}
	cl := heldServe(t, adapterSrv)
	reg := podsession.NewRegistry()
	sessions := []string{"sess-0", "sess-1", "sess-2", "sess-3"}
	heldBind(reg, cl, sessions...)
	e := executor.NewPodExecutor(reg, nil)

	var wg sync.WaitGroup
	var delivered atomic.Int64
	stop := make(chan struct{})
	for _, id := range sessions {
		for s := 0; s < 3; s++ {
			wg.Add(1)
			go func(id string, s int) {
				defer wg.Done()
				for i := 0; i < 40; i++ {
					// Every fourth turn runs on a deadline shorter than
					// the adapter's reply delay, so it is abandoned
					// mid-turn and leaves a late reply on the stream.
					timeout := heldWait
					if i%4 == 0 {
						timeout = time.Millisecond
					}
					ctx, cancel := context.WithTimeout(context.Background(), timeout)
					if heldSend(t, ctx, e, id, fmt.Sprintf("%s/%d/%d", id, s, i)) == nil {
						delivered.Add(1)
					}
					cancel()
				}
			}(id, s)
		}
	}
	evictDone := make(chan struct{})
	go func() {
		defer close(evictDone)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			e.EvictStream(sessions[i%len(sessions)])
			time.Sleep(3 * time.Millisecond)
		}
	}()

	waitDone := make(chan struct{})
	go func() { wg.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(6 * heldWait):
		t.Fatal("concurrent Sends did not finish: a turn or an open deadlocked")
	}
	close(stop)
	<-evictDone

	t.Logf("%d of %d Sends delivered across evictions, abandons, and stream ends", delivered.Load(), len(sessions)*3*40)
	if delivered.Load() == 0 {
		t.Fatal("no Send was delivered under load; the stream never carried a turn")
	}
	heldRelease(t, e, reg, sessions...)
	heldAssertNoStreamGoroutines(t)
}

// spec: 7.2 (Interactive Session Model), 28.5.1 (Gateway-to-pod)
// diagnosis: the reader's approval-verdict relay and a turn's message write
// raced on the stream. A race-detector report means the two writers are not
// serialized; a missing or duplicated verdict means an approval-required
// tool call was dropped or answered twice while a turn was in flight; a Send
// that received another turn's reply means the relay disturbed the turn
// token.
func TestApprovalRelayRacesATurnSend_spec_7_2(t *testing.T) {
	const approvals = 150
	adapterSrv := &heldEchoAdapter{approvals: approvals}
	cl := heldServe(t, adapterSrv)
	reg := podsession.NewRegistry()
	heldBind(reg, cl, "sess-approve")
	e := executor.NewPodExecutor(reg, nil)
	var gated atomic.Int64
	e.SetApprovalGate(heldGate(func(_ context.Context, _, _ string, call executor.PendingToolCall) (executor.ApprovalDecision, error) {
		n := gated.Add(1)
		// Alternate the verdict forms so both relay frames race the turn
		// writes: an approval re-sends the tool_call, a deny writes a
		// tool_result.
		return executor.ApprovalDecision{Approved: n%2 == 0, Reason: "denied by test"}, nil
	}))

	var wg sync.WaitGroup
	for s := 0; s < 2; s++ {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			for i := 0; i < 60; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), heldWait)
				if err := heldSend(t, ctx, e, "sess-approve", fmt.Sprintf("turn/%d/%d", s, i)); err != nil {
					t.Errorf("Send %d/%d: %v", s, i, err)
				}
				cancel()
			}
		}(s)
	}
	wg.Wait()

	deadline := time.Now().Add(heldWait)
	for {
		counts := adapterSrv.verdictCounts()
		if len(counts) == approvals {
			for id, n := range counts {
				if n != 1 {
					t.Errorf("tool call %s received %d verdicts, want exactly one", id, n)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("adapter received verdicts for %d of %d approval-required tool calls", len(counts), approvals)
		}
		time.Sleep(10 * time.Millisecond)
	}
	heldRelease(t, e, reg, "sess-approve")
	heldAssertNoStreamGoroutines(t)
}

// heldGate adapts a function to executor.ApprovalGate.
type heldGate func(ctx context.Context, tenantID, sessionID string, call executor.PendingToolCall) (executor.ApprovalDecision, error)

func (g heldGate) AwaitApproval(ctx context.Context, tenantID, sessionID string, call executor.PendingToolCall) (executor.ApprovalDecision, error) {
	return g(ctx, tenantID, sessionID, call)
}

// spec: 28.5.1 (Gateway-to-pod), 7.2 (Interactive Session Model)
// diagnosis: an Attach open blocked against an unreachable adapter held the
// executor's lock or otherwise serialized unrelated sessions, so a Send on
// another session's held stream or a Release of another session waited on
// it; or EvictStream did not end the blocked open, so the session's waiting
// Send hung until the dial timed out.
func TestBlockedOpenDelaysNoOtherSessionAndEndsOnEvict_spec_28_5_1(t *testing.T) {
	live := heldServe(t, &heldEchoAdapter{})
	dialed := make(chan struct{}, 8)
	unblock := make(chan struct{})
	t.Cleanup(func() { close(unblock) })
	unreachable := heldDial(t, func(ctx context.Context, _ string) (net.Conn, error) {
		dialed <- struct{}{}
		select {
		case <-unblock:
		case <-ctx.Done():
		}
		return nil, errors.New("adapter unreachable")
	})

	reg := podsession.NewRegistry()
	heldBind(reg, live, "sess-live", "sess-other")
	heldBind(reg, unreachable, "sess-blocked")
	e := executor.NewPodExecutor(reg, nil)
	for _, id := range []string{"sess-live", "sess-other"} {
		ctx, cancel := context.WithTimeout(context.Background(), heldWait)
		if err := heldSend(t, ctx, e, id, "open"); err != nil {
			t.Fatalf("open %s: %v", id, err)
		}
		cancel()
	}

	blocked := make(chan error, 1)
	go func() {
		_, err := e.Send(context.Background(), "sess-blocked", []executor.Message{{Role: "user", Content: "stuck"}})
		blocked <- err
	}()
	select {
	case <-dialed:
	case <-time.After(heldWait):
		t.Fatal("the blocked session's open never dialed its adapter")
	}

	const prompt = time.Second
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), heldWait)
	defer cancel()
	if err := heldSend(t, ctx, e, "sess-live", "while-blocked"); err != nil {
		t.Fatalf("Send on another session's held stream during a blocked open: %v", err)
	}
	if d := time.Since(start); d > prompt {
		t.Errorf("Send on another session took %v during a blocked open, want under %v", d, prompt)
	}
	start = time.Now()
	heldRelease(t, e, reg, "sess-other")
	if d := time.Since(start); d > prompt {
		t.Errorf("Release of another session took %v during a blocked open, want under %v", d, prompt)
	}
	select {
	case err := <-blocked:
		t.Fatalf("the blocked session's Send returned %v before the eviction", err)
	default:
	}

	e.EvictStream("sess-blocked")
	select {
	case err := <-blocked:
		if err == nil {
			t.Fatal("the blocked session's Send succeeded after its open was evicted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("EvictStream did not end the blocked open: its waiting Send is still blocked")
	}
	// The live session's reader is running, so the leak check below is
	// not vacuous: it sees reader goroutines while a stream is held.
	if len(heldExecutorGoroutines()) == 0 {
		t.Fatal("no executor reader goroutine found while a stream is held; the leak check cannot see readers")
	}
	heldRelease(t, e, reg, "sess-live", "sess-blocked")
	heldAssertNoStreamGoroutines(t)
}
