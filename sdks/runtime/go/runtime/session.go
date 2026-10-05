// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

// One runtime process serves every session the pod holds, one after
// another on a recycling pool and side by side on a concurrent pool. The
// adapter opens each session with session_start and ends it with
// session_end on CH-MSGSOCK, and the SDK keeps one sessionState per
// session_start it acts on. A state is keyed by the session's sessionId
// in the routing table while the session is live, and by the startId of
// the session_start that created it from its session_end until its
// context is released. Each state has its own goroutine and its own
// message queue, so one session's handler never holds up another
// session or the frame loop.
//
// spec: §4.7.10 (runtime process lifetime), §28.5.3 (CH-MSGSOCK, Inbound:
// session_start, Inbound: session_end, Outbound: session_started,
// Session errors), §15.7 (Handler).

// sessionState is one session's context on this runtime process.
type sessionState struct {
	id      string
	startID string
	start   inboundSessionStart

	// ctx is the session context every Handler call for the session
	// receives. It derives from the process context and is cancelled when
	// the session's context is released.
	ctx    context.Context
	cancel context.CancelFunc

	queue *sessionQueue
	seq   atomic.Uint64

	// prev is the state an earlier session_end removed for the same
	// sessionId while its context was still held. This state's OnCreate
	// runs only after prev's OnTerminate returns, so a later start never
	// overlaps the release of an earlier one.
	prev *sessionState
	// released is closed once the state's context is released.
	released chan struct{}

	credMu      sync.RWMutex
	credentials *CredentialBundle

	// mu guards the creation and end bookkeeping below.
	mu sync.Mutex
	// createDone is set once the creation finished, after the state's
	// own session_started was written.
	createDone bool
	// createErr is the credential-read or OnCreate failure, if any. It is
	// final once set, which happens before session_started is written.
	createErr error
	// createInvoked records that OnCreate ran, so OnTerminate runs for
	// the same states OnCreate ran for.
	createInvoked bool
	// endRead is set when the frame loop reads the session's session_end.
	endRead bool
	// closeReason is the reason EOF or shutdown closed the session with.
	closeReason *TerminationReason
	// pendingAcks holds the startIds of duplicate session_start frames
	// read while the creation was still running.
	pendingAcks []string

	// ended is the stdout drop mark. The frame loop sets it under the
	// frameWriter's lock when it reads the session's session_end, and
	// every response and tool_call written for the state checks it under
	// that same lock.
	ended bool
}

// failed reports whether the state's context creation failed.
func (st *sessionState) failed() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.createErr != nil
}

// Credentials returns the session's current credential bundle.
func (st *sessionState) Credentials() *CredentialBundle {
	st.credMu.RLock()
	defer st.credMu.RUnlock()
	return st.credentials
}

func (st *sessionState) setCredentials(c *CredentialBundle) {
	st.credMu.Lock()
	st.credentials = c
	st.credMu.Unlock()
}

// terminationReason is the reason OnTerminate receives for the state.
func (st *sessionState) terminationReason() TerminationReason {
	st.mu.Lock()
	defer st.mu.Unlock()
	switch {
	case st.endRead:
		return TerminationReason{Reason: "session_end"}
	case st.closeReason != nil:
		return *st.closeReason
	default:
		return TerminationReason{Reason: "stdin_closed"}
	}
}

// sessionTable is the routing table from sessionId to the live state,
// plus the bookkeeping for states a session_end removed whose context is
// not yet released.
type sessionTable struct {
	mu sync.Mutex
	// live maps a sessionId to the state of the latest session_start
	// that created one for it.
	live map[string]*sessionState
	// tail maps a sessionId to its most recent unreleased state, live or
	// removed, which a new state for the same sessionId waits on.
	tail map[string]*sessionState
	// removed maps a startId to a state a session_end removed from live
	// until the state's context is released.
	removed map[string]*sessionState
}

func newSessionTable() *sessionTable {
	return &sessionTable{
		live:    map[string]*sessionState{},
		tail:    map[string]*sessionState{},
		removed: map[string]*sessionState{},
	}
}

// lookup returns the live state for sessionID, or nil.
func (t *sessionTable) lookup(sessionID string) *sessionState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.live[sessionID]
}

// open installs a new state for start unless the session is already
// held. It returns the new state, or the held state when the frame is a
// duplicate under the session_start rule 3.
func (t *sessionTable) open(parent context.Context, start inboundSessionStart) (st *sessionState, held bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if cur := t.live[start.SessionID]; cur != nil {
		return cur, true
	}
	ctx, cancel := context.WithCancel(parent)
	st = &sessionState{
		id:       start.SessionID,
		startID:  start.StartID,
		start:    start,
		ctx:      ctx,
		cancel:   cancel,
		queue:    newSessionQueue(),
		prev:     t.tail[start.SessionID],
		released: make(chan struct{}),
	}
	t.live[st.id] = st
	t.tail[st.id] = st
	return st, false
}

// remove takes the live state for sessionID out of the routing table and
// tracks it under its startId until its release. It returns nil when the
// session is not held.
func (t *sessionTable) remove(sessionID string) *sessionState {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.live[sessionID]
	if st == nil {
		return nil
	}
	delete(t.live, sessionID)
	t.removed[st.startID] = st
	return st
}

// forget drops a released state from every index that still names it.
func (t *sessionTable) forget(st *sessionState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.live[st.id] == st {
		delete(t.live, st.id)
	}
	if t.tail[st.id] == st {
		delete(t.tail, st.id)
	}
	if t.removed[st.startID] == st {
		delete(t.removed, st.startID)
	}
}

// liveStates returns a snapshot of the live states.
func (t *sessionTable) liveStates() []*sessionState {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]*sessionState, 0, len(t.live))
	for _, st := range t.live {
		out = append(out, st)
	}
	return out
}

// handleSessionStart opens the session a session_start names. A frame
// for a session the runtime already holds creates nothing and is
// answered with session_started again, carrying the held session's
// creation error when there is one.
//
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start rules 2 and 3).
func (p *process) handleSessionStart(line []byte) error {
	var start inboundSessionStart
	if err := json.Unmarshal(line, &start); err != nil {
		return ProtocolError{Msg: fmt.Sprintf("malformed session_start: %v", err)}
	}
	if start.SessionID == "" || start.StartID == "" {
		return ProtocolError{Msg: "session_start carries no sessionId or no startId"}
	}
	st, held := p.sessions.open(p.ctx, start)
	if held {
		p.ackDuplicateStart(st, start.StartID)
		return nil
	}
	p.wg.Add(1)
	go p.serve(st)
	return nil
}

// ackDuplicateStart answers a duplicate session_start for a held session.
// While the held session's creation is still running the answer waits for
// it, so session_started never precedes the context it reports.
func (p *process) ackDuplicateStart(st *sessionState, startID string) {
	st.mu.Lock()
	if !st.createDone {
		st.pendingAcks = append(st.pendingAcks, startID)
		st.mu.Unlock()
		return
	}
	err := st.createErr
	st.mu.Unlock()
	p.writeSessionStarted(st, startID, err)
}

// handleSessionEnd ends the start of the session's live state. The loop
// removes the state from the routing table, marks it ended under the
// stdout writer's lock so no later response or tool_call for it is
// written, cancels its pending tool_call waiters, and drops its queued
// messages. The session's goroutine then cancels the session context once
// the creation has finished, waits for the in-flight handler, and runs
// OnTerminate. A session_end for a session the runtime does not hold is
// ignored.
//
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_end rules 2 and 3).
func (p *process) handleSessionEnd(line []byte) {
	var end inboundSessionEnd
	if err := json.Unmarshal(line, &end); err != nil {
		p.cfg.logf("runtime: malformed session_end frame: %v", err)
		return
	}
	st := p.sessions.remove(end.SessionID)
	if st == nil {
		p.cfg.logf("runtime: ignoring session_end for session %q, which this runtime does not hold", end.SessionID)
		return
	}
	p.w.endOwner(st)
	if n := st.queue.end(); n > 0 {
		p.cfg.logf("runtime: session %s ended with %d undispatched message(s); dropping them", st.id, n)
	}
	st.mu.Lock()
	st.endRead = true
	created := st.createDone
	st.mu.Unlock()
	if created {
		st.cancel()
	}
}

// routeMessage hands a message frame to its session's queue. A message
// for a session the runtime does not hold is answered at once with a
// RUNTIME_ERROR response; a message for a session whose creation failed
// is answered the same way by the session's goroutine, in order.
//
// spec: §28.5.3 (CH-MSGSOCK, Session errors).
func (p *process) routeMessage(env *MessageEnvelope) {
	st := p.sessions.lookup(env.SessionID)
	if st != nil && st.queue.push(env) {
		return
	}
	p.cfg.logf("runtime: message %q for session %q, which this runtime does not hold", env.ID, env.SessionID)
	if err := p.w.write(sessionErrorResponse(env.SessionID, "no session "+env.SessionID+" is open on this runtime")); err != nil {
		p.cfg.logf("runtime: write error response: %v", err)
	}
}

// serve is a session's goroutine: it creates the session's context,
// dispatches the session's messages in order, and releases the context.
func (p *process) serve(st *sessionState) {
	defer p.wg.Done()
	if st.prev != nil {
		<-st.prev.released
	}
	p.create(st)
	for {
		env, ok := st.queue.next()
		if !ok {
			break
		}
		p.dispatch(st, env)
	}
	p.release(st)
}

// create loads the session's credential bundle, invokes OnCreate, and
// writes the session's session_started, with error when either failed.
// It then answers any duplicate session_start read meanwhile. A failure
// is the session's own: the process keeps serving its other sessions.
//
// spec: §28.5.3 (CH-MSGSOCK, Outbound: session_started rules 1 and 2,
// Session errors), §15.7 (Handler).
func (p *process) create(st *sessionState) {
	err := p.createContext(st)
	if err != nil {
		p.cfg.logf("runtime: session %s: %v", st.id, err)
	}
	st.mu.Lock()
	st.createErr = err
	st.mu.Unlock()

	p.writeSessionStarted(st, st.startID, err)

	st.mu.Lock()
	st.createDone = true
	acks := st.pendingAcks
	st.pendingAcks = nil
	ended := st.endRead
	st.mu.Unlock()
	for _, id := range acks {
		p.writeSessionStarted(st, id, err)
	}
	if ended {
		st.cancel()
	}
}

// createContext reads the credential file the session_start names and
// invokes OnCreate with the session's CreateRequest.
func (p *process) createContext(st *sessionState) error {
	creds, err := loadCredentialBundle(st.start.CredentialsPath)
	if err != nil {
		return err
	}
	st.setCredentials(creds)
	req := CreateRequest{
		SessionID:         st.id,
		TaskID:            st.id,
		Credentials:       creds,
		ExperimentContext: st.start.ExperimentContext,
		TracingContext:    st.start.TracingContext,
		LLM:               st.start.LLM,
		ManifestSnapshot:  p.manifest,
	}
	if p.manifest != nil {
		req.RuntimeOptions = p.manifest.RuntimeOptions
	}
	st.mu.Lock()
	st.createInvoked = true
	st.mu.Unlock()
	if err := p.handler.OnCreate(p.withSessionContext(st), req); err != nil {
		return fmt.Errorf("OnCreate: %w", err)
	}
	return nil
}

// dispatch handles one queued message for the session.
func (p *process) dispatch(st *sessionState, env *MessageEnvelope) {
	if st.failed() {
		p.writeFor(st, sessionErrorResponse(st.id, "the context of session "+st.id+" could not be created"))
		return
	}
	p.handleMessage(st, env)
}

// release cancels the session context and runs OnTerminate. It runs on
// the session's goroutine after the in-flight handler returned, so
// OnTerminate never overlaps one of the session's own handler calls.
// OnTerminate runs for every state whose OnCreate ran, and receives a
// context that keeps the session's values but is not cancelled, so the
// teardown can still do I/O.
func (p *process) release(st *sessionState) {
	st.cancel()
	st.mu.Lock()
	invoked := st.createInvoked
	st.mu.Unlock()
	if invoked {
		ctx := context.WithoutCancel(p.withSessionContext(st))
		if err := p.handler.OnTerminate(ctx, st.id, st.terminationReason()); err != nil {
			p.cfg.logf("runtime: session %s: OnTerminate error: %v", st.id, err)
		}
	}
	close(st.released)
	p.sessions.forget(st)
}

// closeSessions closes every live session's queue with reason, on EOF or
// shutdown. Each session dispatches the messages it already queued and
// then runs OnTerminate with reason.
func (p *process) closeSessions(reason TerminationReason) {
	for _, st := range p.sessions.liveStates() {
		st.mu.Lock()
		if st.closeReason == nil {
			r := reason
			st.closeReason = &r
		}
		st.mu.Unlock()
		st.queue.close()
	}
}

// heldSession returns the live state a session-scoped CH-RUNTIMEOPS event
// names, or nil when the runtime does not hold the session or failed to
// create its context. The caller drops the event without a reply.
//
// spec: §28.5.3 (CH-RUNTIMEOPS, Messages).
func (p *process) heldSession(sessionID string) *sessionState {
	st := p.sessions.lookup(sessionID)
	if st == nil || st.failed() {
		return nil
	}
	return st
}

// writeSessionStarted writes the session_started frame answering the
// session_start whose startId is startID. It is written even after the
// session's session_end, because the frame answers a session_start read
// before it.
func (p *process) writeSessionStarted(st *sessionState, startID string, createErr error) {
	frame := outboundSessionStarted{Type: "session_started", SessionID: st.id, StartID: startID}
	if createErr != nil {
		frame.Error = &ResponseError{Code: "RUNTIME_ERROR", Message: createErr.Error()}
	}
	if err := p.w.write(frame); err != nil {
		p.cfg.logf("runtime: write session_started for session %s: %v", st.id, err)
	}
}

// writeFor writes a frame addressed to st, dropping it when the session
// already ended.
func (p *process) writeFor(st *sessionState, frame any) {
	if err := p.w.writeFor(st, frame); err != nil {
		p.cfg.logf("runtime: session %s: %v", st.id, err)
	}
}

// sessionErrorResponse is the RUNTIME_ERROR response the SDK writes for a
// message it cannot dispatch to a session.
func sessionErrorResponse(sessionID, msg string) outboundResponse {
	return outboundResponse{
		Type:      "response",
		Output:    []MessagePart{},
		Error:     &ResponseError{Code: "RUNTIME_ERROR", Message: msg},
		SessionID: sessionID,
	}
}

// loadCredentialBundle reads and parses the credential file at path. An
// empty path loads no bundle: the adapter names the file only when it
// provisioned one. A named file that cannot be read or parsed is an
// error, which fails the session's creation.
//
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start credentialsPath),
// §4.7.11 (item 4).
func loadCredentialBundle(path string) (*CredentialBundle, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read credential file %s: %w", path, err)
	}
	var c CredentialBundle
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("malformed credential file %s: %w", path, err)
	}
	return &c, nil
}

// reloadCredentials re-reads the credential file a credentials_rotated
// event names for the session and returns the bundle the session holds
// afterwards. The event names the file the adapter just rewrote, so a
// failed read is reported, and the session keeps the bundle it holds. An
// event carrying no path breaks the frame's contract; the session keeps
// its bundle rather than reading a file the event did not name.
//
// spec: §28.5.3 (CH-RUNTIMEOPS, credentials_rotated), §4.7.11 (item 4).
func (p *process) reloadCredentials(st *sessionState, path string) *CredentialBundle {
	if path == "" {
		p.cfg.logf("runtime: credential rotation for session %s: event carries no credentialsPath; keeping the bundle already held", st.id)
		return st.Credentials()
	}
	c, err := loadCredentialBundle(path)
	if err != nil {
		p.cfg.logf("runtime: credential rotation for session %s: %v", st.id, err)
		return st.Credentials()
	}
	st.setCredentials(c)
	return c
}
