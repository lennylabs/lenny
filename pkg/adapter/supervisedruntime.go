// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/scrub"
)

// This file holds SupervisedRuntime, the RuntimeProcess of a sidecar pod that
// runs the runtime supervisor. It launches each runtime process through
// CH-SUPERVISE, composes one SocketRuntimeProcess per launched process for
// its CH-MSGSOCK connection, ends and sweeps the process at the points the
// specification names, runs cleanupCommands through the supervisor, and holds
// the pod's not-live state and the end disposition of the current generation.
//
// Lock order: Server.mu before SupervisedRuntime.mu. The runtime never calls
// into the Server while it holds its own lock; the Runtime exits handling
// takes Server.mu and then reads and writes the runtime's state.
//
// spec: §4.7.10 (Runtime supervisor); §5.2 (Supervised scrub sequence);
// §28.5.3 (CH-SUPERVISE).

// LaunchMode is the adapter's --launch-mode: whether and how the pod launches
// its runtime process at warm-up. spec: §4.7.10 (Runtime supervisor, Launch;
// Runtime supervisor, SDK-warm).
type LaunchMode string

const (
	// LaunchSession launches the first process at startup step 7.
	LaunchSession LaunchMode = "session"
	// LaunchService launches the process at warm-up and keeps it for the
	// pod's life.
	LaunchService LaunchMode = "service"
	// LaunchPreconnectSDKWarm launches the SDK process at warm-up.
	LaunchPreconnectSDKWarm LaunchMode = "preconnect-sdk-warm"
	// LaunchPreconnectPodWarm warms pod-warm with no process; the first
	// launch is at startup step 7.
	LaunchPreconnectPodWarm LaunchMode = "preconnect-pod-warm"
)

// WarmUpLaunch reports whether the mode launches a process at warm-up.
func (m LaunchMode) WarmUpLaunch() bool {
	return m == LaunchService || m == LaunchPreconnectSDKWarm
}

// Preconnect reports whether the mode belongs to a preConnect runtime.
func (m LaunchMode) Preconnect() bool {
	return m == LaunchPreconnectSDKWarm || m == LaunchPreconnectPodWarm
}

// Request bounds. The specification bounds every CH-SUPERVISE request without
// stating a value. The end bound covers the supervisor's SIGKILL passes after
// a zero grace, the sweep bound a bounded walk of the runtime container's
// writable volumes, and the cleanup bound exceeds the cleanupTimeoutSeconds
// deadline by the supervisor's backstop and its wait after a kill.
// spec: §28.5.3 (CH-SUPERVISE, Timing).
const (
	superviseLaunchBound  = 30 * time.Second
	superviseEndBound     = 30 * time.Second
	superviseSweepBound   = 5 * time.Minute
	superviseCleanupSlack = 30 * time.Second
)

var (
	// errRuntimeNotLive is the error a start returns while the not-live
	// state is set, or when no process is live and no launch is permitted.
	// spec: §4.7.10 (Runtime supervisor, Launch; Runtime supervisor,
	// Liveness); §5.2 (Runtime not live).
	errRuntimeNotLive = errors.New("adapter: runtime not live")
	// errLaunchRefused is a launched reply that carries an error.
	errLaunchRefused = errors.New("adapter: supervisor refused the launch")
)

// SupervisedRuntimeConfig configures NewSupervisedRuntime.
type SupervisedRuntimeConfig struct {
	// Lifetime is the pod's runtime-process lifetime (--runtime-process).
	Lifetime RuntimeLifetime
	// Mode is the pod's launch mode (--launch-mode).
	Mode LaunchMode
	// NewTransport binds the CH-MSGSOCK listener for the next process. It
	// runs once at construction, so the listener is bound before READY, and
	// again at each rebind.
	NewTransport func() (*SocketRuntimeProcess, error)
	// RuntimeOps is the CH-RUNTIMEOPS channel when bound; the rebind closes
	// and rebinds its listener too.
	RuntimeOps *RuntimeOps
	// Logger receives the runtime's records. Nil logs to slog.Default.
	Logger *slog.Logger
}

// SupervisedRuntime is the RuntimeProcess of a supervised sidecar pod.
type SupervisedRuntime struct {
	lifetime     RuntimeLifetime
	mode         LaunchMode
	newTransport func() (*SocketRuntimeProcess, error)
	ops          *RuntimeOps
	logger       *slog.Logger

	// launchMu spans the launch permit check, the launch request, and the
	// composed Start, so concurrent first starts send one launch and the
	// later ones join the process it launched. spec: §4.7.10 (Runtime
	// supervisor, Launch).
	launchMu sync.Mutex
	// sendMu spans a launch from its context check and generation increment
	// to the supervisor's reply. HasLaunched, EndProcess, and SettleLaunch
	// take it, so an end is ordered after the launch it targets and never
	// reaches the supervisor before that launch's request, and a launch the
	// end did not see is refused by its cancelled context. It is narrower
	// than launchMu, which also spans the composed transport's accept wait.
	// Lock order: launchMu, then sendMu, then mu. spec: §4.7.10 (Runtime
	// supervisor, Launch; Runtime supervisor, End); §6.1 (Adapter SIGTERM
	// behavior during sdk_connecting).
	sendMu sync.Mutex

	// mu guards the fields below.
	mu        sync.Mutex
	link      *SupervisorLink
	transport *SocketRuntimeProcess
	tap       func([]byte)
	// gen is the generation of the latest launch, 0 before the first.
	gen uint64
	// live records that the current generation's process was launched and
	// its end has not been handled.
	live bool
	// launchPermitted records that the pod has launched nothing since its
	// creation or its last attested end. spec: §4.7.10 (Runtime supervisor,
	// Launch).
	launchPermitted bool
	// endHandled records that the current generation's end was handled.
	endHandled bool
	// handledDisposition is the disposition that selected the current
	// generation's row when its end was handled.
	handledDisposition EndDisposition
	disposition        EndDisposition
	notLive            bool
	cause              LivenessCause
	// onEnd hands an end event to the Server's Runtime exits handling.
	onEnd func(gen uint64, ev RuntimeEndEvent)
	// onNotLive observes the first cause of the not-live state. It must not
	// block.
	onNotLive func(LivenessCause)
	// onLaunch runs when a launch is sent.
	onLaunch func()
	// prepareWarmUp writes the warm-up manifest and binds the listeners the
	// warm-up launch's process dials, before that launch.
	prepareWarmUp func() error
	// startGens records, per session, the generation its last successful
	// Start launched or joined. The Server's rule-8 record consumes it
	// through confirmStart, so a start is recorded only on the generation it
	// was given to and only while that generation's end is unhandled.
	// spec: §4.7.1 (The registry critical section); §4.7.10 (Runtime
	// supervisor, Crash; Runtime supervisor, Liveness).
	startGens map[string]uint64
	// connectWatch is the timer that holds a launched process to the
	// deadline of the start that launched it after that start was cancelled
	// before its deadline. A successful start and the generation's handled
	// end stop it. spec: §4.7.10 (Runtime supervisor, Liveness).
	connectWatch *time.Timer
}

// NewSupervisedRuntime binds the first CH-MSGSOCK listener and returns the
// runtime. The CH-SUPERVISE link is attached later, by AttachLink, once
// AwaitSupervisor has returned.
func NewSupervisedRuntime(cfg SupervisedRuntimeConfig) (*SupervisedRuntime, error) {
	if cfg.NewTransport == nil {
		return nil, errors.New("supervised runtime: a transport constructor is required")
	}
	if cfg.Lifetime != LifetimeKeep && cfg.Lifetime != LifetimeRestart {
		return nil, fmt.Errorf("supervised runtime: lifetime %q is not keep or restart", cfg.Lifetime)
	}
	r := &SupervisedRuntime{
		lifetime:        cfg.Lifetime,
		mode:            cfg.Mode,
		newTransport:    cfg.NewTransport,
		ops:             cfg.RuntimeOps,
		logger:          loggerOr(cfg.Logger),
		launchPermitted: true,
		disposition:     EndNone,
	}
	t, err := cfg.NewTransport()
	if err != nil {
		return nil, fmt.Errorf("supervised runtime: bind the runtime socket: %w", err)
	}
	r.installTransportLocked(t)
	return r, nil
}

// AttachLink installs the accepted CH-SUPERVISE link. A link failure sets the
// not-live state, and an exited report is handed to the Runtime exits
// handling. spec: §28.5.3 (CH-SUPERVISE, Degradation).
func (r *SupervisedRuntime) AttachLink(l *SupervisorLink) {
	r.mu.Lock()
	r.link = l
	r.mu.Unlock()
	l.setHandlers(
		func(error) { r.MarkNotLive(CauseLinkFailure) },
		func(gen uint64, exit RuntimeExit) { r.endEvent(gen, RuntimeEndEvent{Kind: EndByExited, Exit: exit}) },
	)
}

// Link returns the attached link, nil before AttachLink.
func (r *SupervisedRuntime) Link() *SupervisorLink {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.link
}

// Supervised returns r; the SDK-warm wrapper inherits it, so the Server
// reaches the supervised runtime whichever of the two it holds.
func (r *SupervisedRuntime) Supervised() *SupervisedRuntime { return r }

// Lifetime returns the pod's runtime-process lifetime.
func (r *SupervisedRuntime) Lifetime() RuntimeLifetime { return r.lifetime }

// Mode returns the pod's launch mode.
func (r *SupervisedRuntime) Mode() LaunchMode { return r.mode }

// SetOnNotLive installs the observer of the first not-live cause. It must not
// block, because a cause can be set inside the registry critical section.
func (r *SupervisedRuntime) SetOnNotLive(fn func(LivenessCause)) {
	r.mu.Lock()
	r.onNotLive = fn
	set, cause := r.notLive, r.cause
	r.mu.Unlock()
	if set && fn != nil {
		fn(cause)
	}
}

// setEndHandler installs the Server's Runtime exits handling.
func (r *SupervisedRuntime) setEndHandler(fn func(gen uint64, ev RuntimeEndEvent)) {
	r.mu.Lock()
	r.onEnd = fn
	r.mu.Unlock()
}

// setLaunchHook installs the observer of each sent launch.
func (r *SupervisedRuntime) setLaunchHook(fn func()) {
	r.mu.Lock()
	r.onLaunch = fn
	r.mu.Unlock()
}

// SetWarmUpPreparer installs the step that writes the manifest the warm-up
// launch's process reads and binds every adapter listener it dials.
func (r *SupervisedRuntime) SetWarmUpPreparer(fn func() error) {
	r.mu.Lock()
	r.prepareWarmUp = fn
	r.mu.Unlock()
}

// installTransportLocked makes t the composed transport and arms its end
// handler for the generation of the next launch. The caller holds mu, or owns
// r exclusively during construction.
func (r *SupervisedRuntime) installTransportLocked(t *SocketRuntimeProcess) {
	r.transport = t
	if r.tap != nil {
		t.TapOutput(r.tap)
	}
}

// armConnectionEnd binds the transport's connection-end handler to gen.
func (r *SupervisedRuntime) armConnectionEnd(t *SocketRuntimeProcess, gen uint64) {
	t.mu.Lock()
	t.onEnded = func() { r.endEvent(gen, RuntimeEndEvent{Kind: EndByConnection}) }
	t.mu.Unlock()
}

// TapOutput records fn as the activity tap of every transport the runtime
// composes. spec: §4.7.1 (Role and Gateway RPC Contract).
func (r *SupervisedRuntime) TapOutput(fn func(line []byte)) {
	r.mu.Lock()
	r.tap = fn
	t := r.transport
	r.mu.Unlock()
	if t != nil {
		t.TapOutput(fn)
	}
}

// RuntimeGeneration returns the generation of the latest launch, 0 before
// the first. spec: §4.7.10 (Runtime supervisor, Generation).
func (r *SupervisedRuntime) RuntimeGeneration() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gen
}

// NotLive reports whether the not-live state is set, and its first cause.
func (r *SupervisedRuntime) NotLive() (bool, LivenessCause) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.notLive, r.cause
}

// MarkNotLive sets the not-live state for cause. Nothing clears it. The first
// cause is recorded and reported to the not-live observer. spec: §4.7.10
// (Runtime supervisor, Liveness).
func (r *SupervisedRuntime) MarkNotLive(cause LivenessCause) {
	if r.markNotLive(cause) {
		r.notifyNotLive(cause)
	}
}

// markNotLive sets the state and reports whether this call set it.
func (r *SupervisedRuntime) markNotLive(cause LivenessCause) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.notLive {
		return false
	}
	r.notLive = true
	r.cause = cause
	return true
}

// LogRuntimeNotLive is the adapter log event that records the cause the
// not-live state was set for. spec: §4.7.10 (Runtime supervisor, Liveness).
const LogRuntimeNotLive = "runtime_not_live"

// notifyNotLive logs the cause and runs the observer.
func (r *SupervisedRuntime) notifyNotLive(cause LivenessCause) {
	r.logger.Warn(LogRuntimeNotLive, "cause", string(cause))
	r.mu.Lock()
	fn := r.onNotLive
	r.mu.Unlock()
	if fn != nil {
		fn(cause)
	}
}

// SetEndDisposition sets the end disposition before an act that ends the
// current generation, under the precedence rule: a value declared after the
// current one leaves the current one in place. spec: §4.7.10 (Runtime
// supervisor, Runtime exits).
func (r *SupervisedRuntime) SetEndDisposition(d EndDisposition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d < r.disposition {
		r.disposition = d
	}
}

// EndDisposition returns the current generation's end disposition.
func (r *SupervisedRuntime) EndDisposition() EndDisposition {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.disposition
}

// takeEnd claims the handling of gen's end and returns the disposition that
// selects its row. It reports false for an event of another generation or of
// a generation whose end was already handled.
func (r *SupervisedRuntime) takeEnd(gen uint64) (EndDisposition, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if gen != r.gen || r.endHandled || gen == 0 {
		return r.disposition, false
	}
	r.endHandled = true
	r.handledDisposition = r.disposition
	r.live = false
	r.stopConnectWatchLocked()
	return r.disposition, true
}

// handledEnd returns the disposition that selected the row of gen's end when
// gen is the current generation and its end was already handled. It reports
// false otherwise, including for a generation a later launch has replaced.
func (r *SupervisedRuntime) handledEnd(gen uint64) (EndDisposition, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if gen == 0 || gen != r.gen || !r.endHandled {
		return EndNone, false
	}
	return r.handledDisposition, true
}

// endEvent hands an end event to the Server's handling, or applies the row
// itself when the runtime is used without a Server.
func (r *SupervisedRuntime) endEvent(gen uint64, ev RuntimeEndEvent) {
	r.mu.Lock()
	fn := r.onEnd
	r.mu.Unlock()
	if fn != nil {
		fn(gen, ev)
		return
	}
	disp, ok := r.takeEnd(gen)
	if !ok {
		return
	}
	if row := runtimeExitRow(disp); row.notLive != "" {
		r.MarkNotLive(row.notLive)
	}
}

// Start makes the runtime live for a session. Under the launch mutex it joins
// a live process, or sends one launch when the launch permit holds, and then
// accepts that process's CH-MSGSOCK connection through the composed
// transport within the start's deadline. While the not-live state is set it
// refuses every start, and a start that finds no live process when no launch
// is permitted fails. A launched process that does not connect within the
// deadline of the start that launched it is the no-connection cause. A start
// cancelled before its deadline returns the cancellation, and the launched
// process stays held to that deadline: the transport's accept loop keeps
// running, a later start joins the process once it connects, and a process
// that has not connected when the deadline passes is the no-connection
// cause, whichever start is waiting then.
// spec: §4.7.9 (step 7); §4.7.10 (Runtime supervisor, Launch; Runtime
// supervisor, Liveness).
func (r *SupervisedRuntime) Start(ctx context.Context, sessionID string) error {
	r.launchMu.Lock()
	defer r.launchMu.Unlock()
	t, gen, launched, err := r.ensureLaunched(ctx)
	if err != nil {
		return err
	}
	waitStart := time.Now()
	if err := startComposed(ctx, t, sessionID); err != nil {
		switch {
		case launched && connectDeadlinePassed(err):
			r.MarkNotLive(CauseNoConnection)
		case launched && errors.Is(err, context.Canceled):
			r.armConnectWatch(t, connectDeadline(ctx, t, waitStart))
		}
		return fmt.Errorf("start supervised runtime: %w", err)
	}
	r.stopConnectWatch()
	r.noteStartGen(sessionID, gen)
	return nil
}

// noteStartGen records gen as the generation sessionID's start was given to.
func (r *SupervisedRuntime) noteStartGen(sessionID string, gen uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.startGens == nil {
		r.startGens = map[string]uint64{}
	}
	r.startGens[sessionID] = gen
}

// confirmStart consumes sessionID's start generation and reports whether the
// session may be recorded as held by the current process. It refuses with
// errRuntimeNotLive while the not-live state is set, when no successful
// Start of the session is outstanding, when the generation the start was
// given to is no longer the current one, and when that generation's end was
// already handled. The Server calls it inside the hold of s.mu that takes
// the rule-8 record, which is the hold the Runtime exits handling takes, so
// each start is either seen by a crash of its generation (record first) or
// sees that generation's end (end first). Lock order: the Server's mu, then
// r.mu. spec: §4.7.1 (The registry critical section); §4.7.10 (Runtime
// supervisor, Crash; Runtime supervisor, Liveness); §5.2 (Pod retirement
// policy, Runtime not live).
func (r *SupervisedRuntime) confirmStart(sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	gen, ok := r.startGens[sessionID]
	delete(r.startGens, sessionID)
	switch {
	case r.notLive:
		return errRuntimeNotLive
	case !ok:
		return fmt.Errorf("%w: session %s has no start outstanding", errRuntimeNotLive, sessionID)
	case gen != r.gen || r.endHandled:
		return fmt.Errorf("%w: generation %d ended before session %s was recorded", errRuntimeNotLive, gen, sessionID)
	}
	return nil
}

// connectDeadline returns the time by which the process a start launched
// must connect: the start's deadline, or, for a start without one, the end
// of the transport's accept bound counted from when the wait began.
// spec: §4.7.10 (Runtime supervisor, Liveness).
func connectDeadline(ctx context.Context, t *SocketRuntimeProcess, waitStart time.Time) time.Time {
	if dl, ok := ctx.Deadline(); ok {
		return dl
	}
	return waitStart.Add(t.acceptBound())
}

// armConnectWatch holds the current generation's process, launched by a
// start cancelled before its deadline, to that deadline: when it passes and
// the transport has not installed the process's connection, and the
// generation's end has not been handled, the not-live state is set with the
// no-connection cause. The caller holds launchMu. spec: §4.7.10 (Runtime
// supervisor, Liveness).
func (r *SupervisedRuntime) armConnectWatch(t *SocketRuntimeProcess, deadline time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.endHandled || r.transport != t {
		return
	}
	gen := r.gen
	r.stopConnectWatchLocked()
	r.connectWatch = time.AfterFunc(time.Until(deadline), func() { r.connectDeadlineReached(t, gen) })
}

// connectDeadlineReached sets the no-connection cause when gen's process has
// still not connected through t at its launching start's deadline.
func (r *SupervisedRuntime) connectDeadlineReached(t *SocketRuntimeProcess, gen uint64) {
	if t.hasConnected() {
		return
	}
	r.mu.Lock()
	current := r.gen == gen && !r.endHandled && r.transport == t
	r.mu.Unlock()
	if current {
		r.MarkNotLive(CauseNoConnection)
	}
}

// stopConnectWatch stops the pending connection watch, if any.
func (r *SupervisedRuntime) stopConnectWatch() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopConnectWatchLocked()
}

// stopConnectWatchLocked stops the pending connection watch. The caller
// holds mu.
func (r *SupervisedRuntime) stopConnectWatchLocked() {
	if r.connectWatch != nil {
		r.connectWatch.Stop()
		r.connectWatch = nil
	}
}

// connectDeadlinePassed reports that a connection wait ended because the
// start's deadline passed: the start's context reached its deadline, or the
// transport's accept bound, which startComposed sets to the time left before
// that deadline, elapsed. Only that ending is the no-connection cause; a
// cancellation, an ended connection, and a closed listener are not.
// spec: §4.7.10 (Runtime supervisor, Liveness).
func connectDeadlinePassed(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errRuntimeConnectTimeout)
}

// startComposed waits for the launched process's CH-MSGSOCK connection
// through the composed transport, bounded by ctx alone. The transport's own
// accept timeout is set to the time left before ctx's deadline, so the wait
// ends at the deadline of the start that launched the process, or at the
// --warm-up-timeout bound of the warm-up launch, rather than at the
// transport's default. A ctx without a deadline keeps the transport's
// default bound. The caller holds launchMu, which serializes every Start on
// the transport. spec: §4.7.10 (Runtime supervisor, Launch; Runtime
// supervisor, Liveness).
func startComposed(ctx context.Context, t *SocketRuntimeProcess, sessionID string) error {
	if dl, ok := ctx.Deadline(); ok {
		left := time.Until(dl)
		if left <= 0 {
			left = time.Nanosecond
		}
		t.mu.Lock()
		t.AcceptTimeout = left
		t.mu.Unlock()
	}
	return t.Start(ctx, sessionID)
}

// ensureLaunched returns the transport of the live process, launching one
// when the permit holds. launched reports that this call sent the launch.
// A launch is not sent once ctx is done: on the pre-READY signal path the
// handler cancels the warm-up context before ExitOnSignal decides whether a
// process was launched, so a launch that decision did not see is never sent.
// The caller holds launchMu.
func (r *SupervisedRuntime) ensureLaunched(ctx context.Context) (*SocketRuntimeProcess, uint64, bool, error) {
	r.mu.Lock()
	if r.notLive {
		r.mu.Unlock()
		return nil, 0, false, errRuntimeNotLive
	}
	if r.live && r.transport != nil {
		t, gen := r.transport, r.gen
		r.mu.Unlock()
		return t, gen, false, nil
	}
	r.mu.Unlock()
	r.sendMu.Lock()
	defer r.sendMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, 0, false, fmt.Errorf("launch: %w", err)
	}
	r.mu.Lock()
	if r.notLive {
		r.mu.Unlock()
		return nil, 0, false, errRuntimeNotLive
	}
	if !r.launchPermitted || r.link == nil || r.transport == nil {
		r.mu.Unlock()
		return nil, 0, false, errRuntimeNotLive
	}
	r.gen++
	gen, t, link, onLaunch := r.gen, r.transport, r.link, r.onLaunch
	r.disposition = EndNone
	r.endHandled = false
	r.launchPermitted = false
	r.live = true
	r.mu.Unlock()
	r.armConnectionEnd(t, gen)
	if onLaunch != nil {
		onLaunch()
	}
	if err := r.sendLaunch(ctx, link, gen); err != nil {
		return nil, gen, true, err
	}
	return t, gen, true, nil
}

// sendLaunch sends launch for gen. A launched that carries an error fails the
// start and is the reply-error cause; a link failure is the link-failure
// cause, which the link's failure handler sets. spec: §5.2 (Supervised scrub
// sequence, Launch of the next generation).
func (r *SupervisedRuntime) sendLaunch(ctx context.Context, link *SupervisorLink, gen uint64) error {
	reply, err := link.request(ctx, superviseRequest{Type: superviseLaunch, Gen: gen}, superviseLaunchBound)
	if err != nil {
		return fmt.Errorf("launch generation %d: %w", gen, err)
	}
	if reply.Error != "" {
		r.MarkNotLive(CauseReplyError)
		return fmt.Errorf("%w: launch generation %d: %s", errLaunchRefused, gen, reply.Error)
	}
	return nil
}

// WarmUp is the warm-up launch: it writes the warm-up manifest and binds the
// listeners the process dials, launches the process under the launch mutex
// and permit, and waits for its CH-MSGSOCK connection within ctx. A failure is the warm-up-failure cause. Nothing
// calls it again after warm-up, so the recycle re-warm edge launches nothing.
// spec: §4.7.10 (Runtime supervisor, Launch; Runtime supervisor, SDK-warm).
func (r *SupervisedRuntime) WarmUp(ctx context.Context) error {
	r.mu.Lock()
	prepare := r.prepareWarmUp
	r.mu.Unlock()
	if prepare != nil {
		if err := prepare(); err != nil {
			r.MarkNotLive(CauseWarmUpFailure)
			return fmt.Errorf("warm-up preparation: %w", err)
		}
	}
	r.launchMu.Lock()
	defer r.launchMu.Unlock()
	t, _, _, err := r.ensureLaunched(ctx)
	if err == nil {
		err = startComposed(ctx, t, "")
	}
	if err != nil {
		r.MarkNotLive(CauseWarmUpFailure)
		return fmt.Errorf("warm-up launch: %w", err)
	}
	return nil
}

// HasLaunched reports whether a launch was sent since the pod's creation or
// the last attested end, which is when an end has a process to end. It waits
// for a launch in flight to receive its reply, so a true answer is followed
// by an end the supervisor receives after that launch.
func (r *SupervisedRuntime) HasLaunched() bool {
	r.sendMu.Lock()
	defer r.sendMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gen > 0 && !r.launchPermitted
}

// EndProcess sends end for the current generation with grace as the SIGTERM
// grace. An ended with no error is an attested end: the end is handled as the
// Runtime exits row the disposition selects, the launch permit is restored,
// and the composed transport's listener is closed, so the transport's ended
// and no-redial rules hold for that process. An ended that carries an error
// is the reply-error cause. The caller sets the disposition first.
// spec: §4.7.10 (Runtime supervisor, End; Runtime supervisor, Runtime exits).
func (r *SupervisedRuntime) EndProcess(ctx context.Context, grace time.Duration) error {
	r.sendMu.Lock()
	defer r.sendMu.Unlock()
	r.mu.Lock()
	gen, link := r.gen, r.link
	r.mu.Unlock()
	if link == nil {
		return fmt.Errorf("%w: end: %w", scrub.ErrNotLive, ErrSuperviseLinkFailed)
	}
	graceMs := uint64(grace / time.Millisecond)
	reply, err := link.request(ctx, superviseRequest{Type: superviseEnd, Gen: gen, GraceMs: &graceMs}, grace+superviseEndBound)
	if err != nil {
		return fmt.Errorf("%w: end generation %d: %w", scrub.ErrNotLive, gen, err)
	}
	if reply.Error != "" {
		r.MarkNotLive(CauseReplyError)
		return fmt.Errorf("%w: end generation %d: %s", scrub.ErrNotLive, gen, reply.Error)
	}
	if gen > 0 {
		r.endEvent(gen, RuntimeEndEvent{Kind: EndByEnded})
	}
	r.mu.Lock()
	r.launchPermitted = true
	r.live = false
	t := r.transport
	r.transport = nil
	r.mu.Unlock()
	if t != nil {
		_ = t.CloseListener()
	}
	return nil
}

// SettleLaunch returns once no launch is in flight. A launch that was in
// flight has then received its reply and run the launch hook, and a later
// launch under a context the caller cancelled first is refused, so the
// caller's decision about what was launched covers every launch the
// supervisor receives. ExitOnSignal calls it before that decision.
// spec: §6.1 (Adapter SIGTERM behavior during sdk_connecting).
func (r *SupervisedRuntime) SettleLaunch() {
	r.sendMu.Lock()
	r.sendMu.Unlock() //nolint:staticcheck // the empty critical section is the wait for an in-flight launch.
}

// Sweep sends sweep for the current generation. A swept that carries an
// error, the bound error included, is the reply-error cause. spec: §4.7.10
// (Runtime supervisor, Sweep); §5.2 (Supervised scrub sequence).
func (r *SupervisedRuntime) Sweep(ctx context.Context) error {
	r.mu.Lock()
	gen, link := r.gen, r.link
	r.mu.Unlock()
	if link == nil {
		return fmt.Errorf("%w: sweep: %w", scrub.ErrNotLive, ErrSuperviseLinkFailed)
	}
	reply, err := link.request(ctx, superviseRequest{Type: superviseSweep, Gen: gen}, superviseSweepBound)
	if err != nil {
		return fmt.Errorf("%w: sweep generation %d: %w", scrub.ErrNotLive, gen, err)
	}
	if reply.Error != "" {
		r.MarkNotLive(CauseReplyError)
		return fmt.Errorf("%w: sweep generation %d: %s", scrub.ErrNotLive, gen, reply.Error)
	}
	return nil
}

// EndAndSweep is one boundary end of a restart pod's whole-pod scrub: it
// sets the endScrub disposition, ends the process with no SIGTERM grace, and
// sweeps. The sweep runs after an end that returned an error, and is skipped
// by the link once the link has failed. spec: §5.2 (Supervised scrub
// sequence, First end; First sweep; Second end; Second sweep).
func (r *SupervisedRuntime) EndAndSweep(ctx context.Context) error {
	r.SetEndDisposition(EndScrub)
	endErr := r.EndProcess(ctx, 0)
	return errors.Join(endErr, r.Sweep(ctx))
}

// RunCleanup runs the pool's cleanupCommands through the supervisor's cleanup
// worker. Each command is split on whitespace as the argv mode of setup
// commands does. A cleaned that carries an error fails the scrub; a link
// failure is not live. The request's bound exceeds the cleanupTimeoutSeconds
// deadline. spec: §4.7.10 (Runtime supervisor, Cleanup); §5.2 (Supervised
// scrub sequence, cleanupCommands); §28.5.3 (CH-SUPERVISE, Timing).
func (r *SupervisedRuntime) RunCleanup(ctx context.Context, req scrub.CleanupRequest) error {
	cmds := make([][]string, 0, len(req.Commands))
	for _, c := range req.Commands {
		argv := strings.Fields(c)
		if len(argv) == 0 {
			return errors.New("cleanup command is empty after whitespace split")
		}
		cmds = append(cmds, argv)
	}
	r.mu.Lock()
	gen, link := r.gen, r.link
	r.mu.Unlock()
	if link == nil {
		return fmt.Errorf("%w: cleanup: %w", scrub.ErrNotLive, ErrSuperviseLinkFailed)
	}
	timeoutMs := uint64(req.Timeout / time.Millisecond)
	prov, lease := req.PrevProvider, req.PrevLeaseID
	reply, err := link.request(ctx, superviseRequest{
		Type: superviseCleanup, Gen: gen, Commands: cmds,
		TimeoutMs: &timeoutMs, PrevProvider: &prov, PrevLeaseID: &lease,
	}, req.Timeout+superviseCleanupSlack)
	if err != nil {
		return fmt.Errorf("%w: cleanup: %w", scrub.ErrNotLive, err)
	}
	if reply.Error != "" {
		return fmt.Errorf("cleanup commands: %s", reply.Error)
	}
	return nil
}

// RebindListeners closes the composed transport's listener and binds a new
// one for the next process, and closes and rebinds the CH-RUNTIMEOPS listener
// when bound, which discards every connection the ended process opened or
// left pending. A rebind error is the reply-error cause. spec: §4.7.10
// (Runtime supervisor, End); §5.2 (Supervised scrub sequence, Listener
// rebind).
func (r *SupervisedRuntime) RebindListeners() error {
	r.launchMu.Lock()
	defer r.launchMu.Unlock()
	r.mu.Lock()
	old := r.transport
	r.transport = nil
	r.mu.Unlock()
	if old != nil {
		_ = old.CloseListener()
	}
	err := r.bindTransport()
	if err == nil && r.ops != nil {
		err = r.ops.Rebind()
	}
	if err != nil {
		r.MarkNotLive(CauseReplyError)
		return fmt.Errorf("%w: rebind listeners: %w", scrub.ErrNotLive, err)
	}
	return nil
}

// bindTransport binds a new composed transport.
func (r *SupervisedRuntime) bindTransport() error {
	t, err := r.newTransport()
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.installTransportLocked(t)
	r.mu.Unlock()
	return nil
}

// current returns the composed transport.
func (r *SupervisedRuntime) current() (*SocketRuntimeProcess, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.transport == nil {
		return nil, errRuntimeConnectionEnded
	}
	return r.transport, nil
}

// WriteEnvelope writes to the current process's connection.
func (r *SupervisedRuntime) WriteEnvelope(sessionID string, envelope []byte) error {
	t, err := r.current()
	if err != nil {
		return err
	}
	return t.WriteEnvelope(sessionID, envelope)
}

// Output subscribes to the current process's output.
func (r *SupervisedRuntime) Output(ctx context.Context, sessionID string) (<-chan []byte, error) {
	t, err := r.current()
	if err != nil {
		return nil, err
	}
	return t.Output(ctx, sessionID)
}

// Interrupt returns nil: no interrupt ends the runtime process. spec:
// §4.7.10 (Runtime process lifetime).
func (r *SupervisedRuntime) Interrupt(context.Context, string, bool) error { return nil }

// Close drops the session's outstanding start generation and returns nil: a
// session's teardown ends no runtime process. spec: §4.7.10 (Runtime process
// lifetime).
func (r *SupervisedRuntime) Close(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.startGens, sessionID)
	return nil
}

// ServesNextSession reports whether the runtime can serve the pod's next
// session. It is false while the not-live state is set. On a keep pod it is
// otherwise true when a launch is permitted or the composed transport's
// connection serves, and on a restart pod, whose next session launches a new
// process after the scrub, it is otherwise true.
// spec: §4.7.10 (Runtime supervisor, Liveness); §5.2 (Runtime not live).
func (r *SupervisedRuntime) ServesNextSession() bool {
	r.mu.Lock()
	notLive, permitted, t := r.notLive, r.launchPermitted, r.transport
	r.mu.Unlock()
	if notLive {
		return false
	}
	if r.lifetime == LifetimeRestart || permitted {
		return true
	}
	return t != nil && t.ServesNextSession()
}

// CloseForHoldTimeout sets the holdTimeout disposition and closes the current
// process's connection through the path that runs the end handler, so the
// end takes the holdTimeout row. spec: §4.7.10 (Runtime supervisor, Runtime
// exits); §10.1 (Hold state timeout).
func (r *SupervisedRuntime) CloseForHoldTimeout() {
	r.SetEndDisposition(EndHoldTimeout)
	r.mu.Lock()
	t := r.transport
	r.mu.Unlock()
	if t != nil {
		t.CloseConnection()
	}
}

// CloseListener is the adapter's exit teardown: it closes the composed
// transport through its pod-scope teardown, which runs no end handler, and
// closes the CH-SUPERVISE link, on which the supervisor exits.
// spec: §4.7.10 (Runtime supervisor, Runtime exits).
func (r *SupervisedRuntime) CloseListener() error {
	r.mu.Lock()
	t, link := r.transport, r.link
	r.mu.Unlock()
	var err error
	if t != nil {
		err = t.CloseListener()
	}
	if link != nil {
		_ = link.Close()
	}
	return err
}

// gracefulEnd returns the SIGTERM grace for an end that must finish within
// ctx: half the time left, and none when ctx has no deadline.
func gracefulEnd(ctx context.Context) time.Duration {
	dl, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	g := time.Until(dl) / 2
	if g < 0 {
		return 0
	}
	return g
}

// SupervisedSDKWarmRuntime adds the SDKWarmRuntime methods to a
// SupervisedRuntime. main uses it only on the preconnect launch modes.
// spec: §4.7.10 (Runtime supervisor, SDK-warm).
type SupervisedSDKWarmRuntime struct {
	*SupervisedRuntime
}

// PreConnect is the warm-up launch of a preconnect-sdk-warm pod.
func (w SupervisedSDKWarmRuntime) PreConnect(ctx context.Context) error {
	return w.WarmUp(ctx)
}

// ConfigureWorkspace gives the session to the live process, and with no live
// process launches one as a step-7 start, after the Server wrote the final
// manifest.
func (w SupervisedSDKWarmRuntime) ConfigureWorkspace(ctx context.Context, sessionID, _ string) error {
	return w.Start(ctx, sessionID)
}

// DemoteSDK ends the live process with a SIGTERM grace that ends within the
// request's deadline and rebinds the listeners for the next launch. It sets
// the endDemote disposition before the end; EndProcess sets the not-live
// state when the end fails, and an end error is returned. With no launched
// process there is nothing to end.
func (w SupervisedSDKWarmRuntime) DemoteSDK(ctx context.Context) error {
	if !w.HasLaunched() {
		return nil
	}
	w.SetEndDisposition(EndDemote)
	if err := w.EndProcess(ctx, gracefulEnd(ctx)); err != nil {
		return err
	}
	return w.RebindListeners()
}

var (
	_ RuntimeProcess      = (*SupervisedRuntime)(nil)
	_ SDKWarmRuntime      = SupervisedSDKWarmRuntime{}
	_ nextSessionRuntime  = (*SupervisedRuntime)(nil)
	_ scrub.CleanupRunner = (*SupervisedRuntime)(nil)
)
