// SPDX-License-Identifier: MIT

package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

// errStreamEnded is the cause a Send returns when the session's Attach
// stream ends while the Send is waiting for its turn or its reply. No caller
// branches on it; it is wrapped so the message names the session.
var errStreamEnded = errors.New("attach stream ended")

// approvalErrorReason is the deny reason the reader relays when the §7.2
// approval gate itself fails. The gate fails closed: the runtime's tool call
// receives a denial rather than executing or waiting forever.
const approvalErrorReason = "approval_error"

// turnResult is one completed turn: the output parts and annotations of the
// `response` frame the reader consumed for it.
type turnResult struct {
	parts []MessagePart
	ann   map[string]any
}

// attachConn is a session's held §28.5.1 CH-ATTACH stream. The executor
// holds one per session from the first message delivery until the session's
// binding is released, on a context detached from the request that opened
// it, so the end of a client request never ends the stream.
//
// One reader goroutine consumes every frame the stream carries. Turns are
// serialized by a one-slot token: a Send holds the token from before it
// writes a message until the reader consumes that message's `response`. A
// Send that stops waiting (its request ended) leaves the token held and its
// turn registered, so the abandoned turn's late reply is consumed by the
// reader and never handed to the next turn.
//
// Lock order: PodExecutor.mu before sendMu or turnMu. No code holds a conn
// mutex while it takes PodExecutor.mu.
//
// spec: §28.5.1 (Gateway-to-pod, CH-ATTACH Timing.), §28.5.3, §7.2.
type attachConn struct {
	// ctx is the session-scoped stream context; cancel ends the stream.
	ctx    context.Context
	cancel context.CancelFunc

	// The binding identity, captured at open, so the approval gate and the
	// end-of-stream handling never re-read a registry entry that a later
	// bind may have replaced.
	tenantID    string
	sessionID   string
	sandboxName string
	// bind is the binding the stream was opened for, read under e.mu by
	// streamFor. endConn marks it crashed on a reported stream failure, so a
	// release whose unbind follows the end retires the pod. spec: §28.5.1
	// (Gateway-to-pod).
	bind *podsession.BindResult

	// stream and openErr are written by the open goroutine before ready is
	// closed and only read after it.
	stream  *adapterclient.AttachStream
	openErr error
	ready   chan struct{}

	// sendMu serializes writes. gRPC SendMsg is not safe for concurrent
	// calls, and both a turn's Send and the reader's approval relay write.
	sendMu sync.Mutex

	// closedByGateway is set by EvictStream before it cancels ctx, so the
	// reader treats the resulting end as one the gateway caused.
	closedByGateway atomic.Bool

	// token is the turn token (capacity 1). A Send acquires it by sending
	// into it; the reader returns it by receiving when it completes a turn.
	token chan struct{}

	// turnMu guards turn, the result channel of the registered turn, or nil
	// when no turn is registered.
	turnMu sync.Mutex
	turn   chan turnResult

	// done is closed by the reader when it exits, or by the open goroutine
	// when the open fails.
	done chan struct{}
	// endErr is the error the stream ended with. endConn writes it before
	// it closes done, and it is read only after done closes.
	endErr error
}

// sealedPrefix opens the status message of the adapter's refusal of a
// message to a sealed session. spec: §28.5.1 (CH-ATTACH).
const sealedPrefix = "session_sealed:"

// endedSealed reports whether the stream ended because the adapter refused
// a message to a sealed session: FailedPrecondition with a message
// beginning session_sealed:. It is read only after done closes.
// spec: §4.7.1 (Role and Gateway RPC Contract), §28.5.1 (CH-ATTACH)
func (c *attachConn) endedSealed() bool {
	st, ok := status.FromError(c.endErr)
	return ok && st.Code() == codes.FailedPrecondition && strings.HasPrefix(st.Message(), sealedPrefix)
}

// endCause is the cause a Send waiting on the ended stream returns for a
// turn whose envelope it wrote at seal epoch writeEpoch. It is
// ErrSessionSealed when the adapter refused the message as sealed, or when
// the turn's message was written after the binding was marked sealed and the
// same seal held until the stream ended, because the adapter refuses every
// message that reaches it under a seal and the stream's end, whatever caused
// it, then means the message did not reach the runtime. A message written
// before the seal mark can have reached the runtime, since work the runtime
// began before the seal without producing activity does not prevent the
// seal and the release that follows ends it, so its turn returns
// errStreamEnded, the outcome of a message delivered on a stream that then
// ended. spec: §4.7.1 (Role and Gateway RPC Contract), §6.2
// (maxClientIdleSeconds clock behavior across states)
func (c *attachConn) endCause(writeEpoch int64) error {
	if c.endedSealed() || (c.bind != nil && c.bind.SealHeldSince(writeEpoch)) {
		return ErrSessionSealed
	}
	return errStreamEnded
}

// sealEpoch returns the binding's seal epoch, or zero, which is never a
// sealed epoch, for a conn with no binding.
func (c *attachConn) sealEpoch() int64 {
	if c.bind == nil {
		return 0
	}
	return c.bind.SealEpoch()
}

// newAttachConn builds an unopened conn for bind. Its context is derived
// from ctx with ctx's cancellation removed, so the stream outlives the
// request that opened it while keeping the request's values.
func newAttachConn(ctx context.Context, bind *podsession.BindResult) *attachConn {
	sctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	return &attachConn{
		ctx:         sctx,
		cancel:      cancel,
		tenantID:    bind.TenantID,
		sessionID:   bind.SessionID,
		sandboxName: bind.SandboxName,
		bind:        bind,
		ready:       make(chan struct{}),
		token:       make(chan struct{}, 1),
		done:        make(chan struct{}),
	}
}

// waitReady blocks until the conn's open completes or ctx ends, and returns
// the open error when the open failed.
func (c *attachConn) waitReady(ctx context.Context) error {
	select {
	case <-c.ready:
		return c.openErr
	case <-ctx.Done():
		return fmt.Errorf("podexec: wait for attach stream of session %s: %w", c.sessionID, ctx.Err())
	}
}

// send writes one envelope under sendMu. Every write to the stream goes
// through it.
func (c *attachConn) send(b []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.stream.Send(b)
}

// acquireTurn takes the turn token. It returns an error when ctx ends or the
// stream ends first.
func (c *attachConn) acquireTurn(ctx context.Context) error {
	select {
	case c.token <- struct{}{}:
	case <-ctx.Done():
		return fmt.Errorf("podexec: wait for turn on session %s: %w", c.sessionID, ctx.Err())
	case <-c.done:
		return fmt.Errorf("podexec: session %s: %w", c.sessionID, errStreamEnded)
	}
	// The token and done can both be ready; a closed stream wins so the
	// Send does not write to it.
	select {
	case <-c.done:
		<-c.token
		return fmt.Errorf("podexec: session %s: %w", c.sessionID, errStreamEnded)
	default:
		return nil
	}
}

// registerTurn records the result channel of the turn the caller holds the
// token for. The channel has capacity 1 so the reader's completion never
// blocks on a Send that stopped waiting.
func (c *attachConn) registerTurn() chan turnResult {
	ch := make(chan turnResult, 1)
	c.turnMu.Lock()
	c.turn = ch
	c.turnMu.Unlock()
	return ch
}

// abortTurn clears the turn registered as ch, whose message was never
// written, and returns the token, because no reply will arrive to complete
// it. The reader can complete the turn first, between the registration and
// the failed write, when the runtime emits a `response` no message caused;
// completing a turn already cleared the registration and returned the token,
// so abortTurn then leaves both alone. Taking the token unconditionally would
// either block forever on an empty token or take the token of a later Send
// that acquired it meanwhile, which breaks one turn at a time and leaves the
// reader blocked on its next completion. spec: §28.5.1 (CH-ATTACH Timing.),
// §28.5.3.
func (c *attachConn) abortTurn(ch chan turnResult) {
	c.turnMu.Lock()
	defer c.turnMu.Unlock()
	if c.turn != ch {
		return
	}
	c.turn = nil
	<-c.token
}

// completeTurn delivers res to the registered turn, clears the registration,
// and returns the token. A `response` with no turn registered is discarded.
func (c *attachConn) completeTurn(res turnResult) {
	c.turnMu.Lock()
	ch := c.turn
	c.turn = nil
	c.turnMu.Unlock()
	if ch == nil {
		return
	}
	ch <- res
	<-c.token
}

// runTurn writes one message envelope and waits for its reply. On ctx's end
// it returns the context error and leaves the turn registered and the token
// held, so only the reader completes the turn. spec: §28.5.1 (CH-ATTACH
// Timing.), §28.5.3.
func (c *attachConn) runTurn(ctx context.Context, envelope []byte) (turnResult, error) {
	if err := c.acquireTurn(ctx); err != nil {
		return turnResult{}, err
	}
	result := c.registerTurn()
	// The epoch is read before the write, so a seal marked while the write
	// is in flight does not make a message the runtime may have received
	// read as refused.
	writeEpoch := c.sealEpoch()
	if err := c.send(envelope); err != nil {
		c.abortTurn(result)
		return turnResult{}, c.awaitEndAfterWriteError(ctx, writeEpoch, err)
	}
	return c.awaitReply(ctx, writeEpoch, result)
}

// awaitEndAfterWriteError returns once the conn's end handling has finished
// or ctx has ended. A write error on an opened conn means the stream has
// ended, and the reader then always runs endConn and closes done. Waiting
// for done keeps a Send from returning a delivery failure while the
// stream-failure handler is still reporting the end, so a caller that
// re-reads the session after the error sees the state the report recorded,
// and a retried delivery cannot open a new stream over a binding the report
// is about to release. spec: §28.5.1 (CH-ATTACH Timing.).
func (c *attachConn) awaitEndAfterWriteError(ctx context.Context, writeEpoch int64, sendErr error) error {
	select {
	case <-c.done:
		return fmt.Errorf("podexec: send to pod for session %s: %w: %w", c.sessionID, c.endCause(writeEpoch), sendErr)
	case <-ctx.Done():
		return fmt.Errorf("podexec: send to pod for session %s: %w: %w", c.sessionID, ctx.Err(), sendErr)
	}
}

// awaitReply waits for the reader to complete the registered turn. On ctx's
// end it leaves the turn registered and the token held, so only the reader
// completes the turn.
//
// The reader completes a turn before its next Recv can observe the stream's
// end, so the result and the end of ctx or of the stream can be ready at
// once, and select picks among ready cases at random. A reply the runtime
// produced wins: the caller must not see a delivery failure for a message
// the runtime answered. spec: §28.5.1 (CH-ATTACH Timing.), §28.5.3.
func (c *attachConn) awaitReply(ctx context.Context, writeEpoch int64, result <-chan turnResult) (turnResult, error) {
	var cause error
	select {
	case res := <-result:
		return res, nil
	case <-ctx.Done():
		cause = ctx.Err()
	case <-c.done:
		cause = c.endCause(writeEpoch)
	}
	select {
	case res := <-result:
		return res, nil
	default:
		return turnResult{}, fmt.Errorf("podexec: await reply on session %s: %w", c.sessionID, cause)
	}
}

// openConn opens c's stream on its session-scoped context and starts the
// reader. It runs on its own goroutine so a slow open never holds e.mu. A
// failed open removes the cache entry when it is still c, so the next Send
// opens a new stream.
func (e *PodExecutor) openConn(c *attachConn, adapter *adapterclient.Client) {
	var stream *adapterclient.AttachStream
	err := errors.New("binding carries no adapter connection")
	if adapter != nil {
		stream, err = adapter.Attach(c.ctx, c.sessionID)
	}
	if err != nil {
		c.openErr = fmt.Errorf("podexec: open attach stream for session %s: %w", c.sessionID, err)
		e.forgetConn(c)
		c.cancel()
		close(c.ready)
		close(c.done)
		return
	}
	c.stream = stream
	close(c.ready)
	go e.readConn(c)
}

// forgetConn deletes the cache entry for c's session when the cached pointer
// is still c, so a stale conn never removes the conn that replaced it.
func (e *PodExecutor) forgetConn(c *attachConn) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.streams[c.sessionID] == c {
		delete(e.streams, c.sessionID)
	}
}

// readConn is the conn's single reader. It blocks only in Recv, in the
// approval gate, and in a relay write; it never waits on a consumer, so the
// adapter's per-subscriber intake never fills. spec: §28.5.1 (CH-ATTACH),
// §28.5.3.
func (e *PodExecutor) readConn(c *attachConn) {
	for {
		frame, err := c.stream.Recv()
		if err != nil {
			e.endConn(c, err)
			return
		}
		e.handleFrame(c, frame)
	}
}

// handleFrame dispatches one frame: an approval-required tool_call goes to
// the gate whether or not a turn is registered, a `response` completes the
// registered turn, and every other frame is discarded. spec: §7.2, §28.5.3.
func (e *PodExecutor) handleFrame(c *attachConn, frame []byte) {
	if e.approvals != nil && e.maybeGateToolCall(c, frame) {
		return
	}
	var env responseEnvelope
	if err := json.Unmarshal(frame, &env); err != nil || env.Type != "response" {
		return
	}
	parts, ann := ingestResponse(env)
	c.completeTurn(turnResult{parts: parts, ann: ann})
}

// endConn runs once when the conn's stream ends. It records the end error,
// which a Send waiting on the conn reads to tell the adapter's sealed
// refusal (§28.5.1, a FailedPrecondition that is not a stream failure) from
// any other end. A gateway-caused end
// (EvictStream or Release) only closes done. Any other end removes the conn
// from the cache when it is still the cached conn, hands an end that
// isReportedStreamFailure classifies as a failure to the stream-failure
// handler, and then cancels and closes done, so a Send waiting on the conn
// returns only after the handler has run.
//
// closedByGateway is read under e.mu because EvictStream sets it under e.mu:
// an end that races an eviction is attributed to the eviction.
//
// A reported stream failure marks the conn's binding crashed in the same hold
// of e.mu, whether or not a handler is wired. Every release that can pass a
// disposition other than failed unbinds under e.mu, so exactly one of the two
// runs first: when endConn does, the release reads the mark and retires the
// pod; when the unbind does, it evicts the conn and sets closedByGateway, so
// endConn sets no mark, and the release, having ended the stream itself,
// keeps its disposition. A gateway-caused end and an unreported status code
// set nothing.
//
// spec: §28.5.1 (CH-ATTACH Timing. and Degradation.); §5.2 (Pool
// Configuration and Execution Modes).
func (e *PodExecutor) endConn(c *attachConn, err error) {
	c.endErr = err
	e.mu.Lock()
	if c.closedByGateway.Load() {
		e.mu.Unlock()
		close(c.done)
		return
	}
	if e.streams[c.sessionID] == c {
		delete(e.streams, c.sessionID)
	}
	if c.bind != nil && isReportedStreamFailure(err) {
		c.bind.MarkCrashed()
	}
	e.mu.Unlock()

	// The handler runs after the conn has left the cache, so a delivery that
	// arrives during it opens a new stream rather than reusing this one, and
	// before done closes, so a Send waiting on this conn returns only after
	// the failure has been reported.
	if e.onStreamFailure != nil && isReportedStreamFailure(err) {
		e.onStreamFailure(c.tenantID, c.sessionID, c.sandboxName, err)
	}

	c.cancel()
	close(c.done)
}

// isReportedStreamFailure classifies a stream end that the gateway did not
// cause by its gRPC status code. DeadlineExceeded is the adapter's heartbeat
// escalation for a runtime that stopped answering, and Internal is a runtime
// or adapter fault; both are reported. A clean end (io.EOF) is normal
// completion, FailedPrecondition can be a retryable ordering race before
// session_start, InvalidArgument is a malformed frame, and Unavailable is a
// transport loss the coordination Sweeper owns, so none of these, nor any
// other code, is reported. The status code is the discriminator; the
// connection's liveness is not, because a dropped channel can read idle.
// spec: §28.5.1 (CH-ATTACH Degradation.).
func isReportedStreamFailure(err error) bool {
	switch status.Code(err) {
	case codes.DeadlineExceeded, codes.Internal:
		return true
	default:
		return false
	}
}

// maybeGateToolCall inspects one frame. When it is a tool_call requiring
// approval, it drives the §7.2 approval handshake on the conn's context with
// the conn's tenant, relays the verdict through conn.send, and returns true.
// For any other frame it returns false.
//
// A gate error fails closed: the reader relays a deny tool_result with
// isError and the reason approval_error, so the runtime's call neither runs
// nor waits forever. When the conn's context has ended, because EvictStream
// cancelled it, the reader relays nothing: an evicted conn's replica may no
// longer coordinate the session. A relay write error is dropped; the
// reader's next Recv returns the stream's end.
// spec: §7.2; §28.5.1 (CH-ATTACH); §28.5.3.
func (e *PodExecutor) maybeGateToolCall(c *attachConn, frame []byte) bool {
	var call toolCallFrame
	if err := json.Unmarshal(frame, &call); err != nil {
		return false
	}
	if call.Type != "tool_call" || !call.ApprovalRequired {
		return false
	}
	decision, err := e.approvals.AwaitApproval(c.ctx, c.tenantID, c.sessionID, PendingToolCall{
		ID:        call.ID,
		Name:      call.Name,
		Arguments: call.Arguments,
	})
	if c.ctx.Err() != nil {
		return true
	}
	if err != nil {
		slog.Warn("podexec: tool-use approval gate failed; denying the call",
			"session_id", c.sessionID, "tool_call_id", call.ID, "error", err)
		decision = ApprovalDecision{Approved: false, Reason: approvalErrorReason}
	}
	verdict, err := verdictFrame(call, decision)
	if err != nil {
		slog.Warn("podexec: encode tool-use verdict", "session_id", c.sessionID, "tool_call_id", call.ID, "error", err)
		return true
	}
	if err := c.send(verdict); err != nil {
		slog.Debug("podexec: relay tool-use verdict", "session_id", c.sessionID, "tool_call_id", call.ID, "error", err)
	}
	return true
}

// verdictFrame encodes the frame that relays decision to the runtime. §7.2:
// an approval re-sends the tool_call with the approval flag cleared so the
// runtime executes it without re-entering the gate; a denial returns a
// tool_result carrying isError and the deny reason.
func verdictFrame(call toolCallFrame, decision ApprovalDecision) ([]byte, error) {
	if decision.Approved {
		return json.Marshal(toolCallFrame{
			Type:      "tool_call",
			ID:        call.ID,
			Name:      call.Name,
			Arguments: call.Arguments,
			SessionID: call.SessionID,
		})
	}
	return json.Marshal(toolResultFrame{
		Type:    "tool_result",
		ID:      call.ID,
		Content: []wireMessagePart{{Type: "text", Inline: decision.Reason}},
		IsError: true,
	})
}
