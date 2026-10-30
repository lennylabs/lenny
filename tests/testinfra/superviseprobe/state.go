// SPDX-License-Identifier: MIT

package superviseprobe

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// state is the serving process's identity and the records cleanup commands
// reported to it.
type state struct {
	token    string
	manifest ManifestView
	env      []string
	delay    time.Duration

	sigs chan os.Signal
	srv  *recordServer
	// uncounted names the catchable signals the probe does not register.
	uncounted []string

	// mu guards links, cleanups, and signals.
	mu       sync.Mutex
	links    []string
	cleanups []CleanupRecord
	signals  map[string]int
}

// newState captures the identity before the dial, claims the dial delay
// against launchMarker, and starts the record server and the signal
// counter, which logs each signal to stderr, the runtime container's log.
func newState(stderr io.Writer, launchMarker string) (*state, error) {
	token, err := selfToken()
	if err != nil {
		return nil, err
	}
	delay, err := claimDialDelay(os.Getenv(DialDelayEnvVar), launchMarker)
	if err != nil {
		return nil, err
	}
	st := &state{
		token:    token,
		manifest: ReadManifestView(manifestPath()),
		env:      environment(),
		delay:    delay,
		signals:  map[string]int{},
	}
	st.countSignals(stderr, os.Exit)
	st.srv = startRecordServer(RecordSocket, st)
	return st, nil
}

// claimDialDelay is the delay this process sleeps before its CH-MSGSOCK
// dial. The configured delay is sized against the warm-up launch's
// deadline, the rendered --warm-up-timeout, and the warm-up launch is the
// pod's first launch. The first process to create marker, a file on the
// pod-scoped /tmp volume, takes the delay; a later launch in the same pod,
// such as the startup step 7 launch that serves a session after DemoteSDK
// ended the warm-up process, finds marker present and dials at once,
// because its deadline is the launching start's rather than the warm-up
// timeout. No marker is created when no delay is configured. spec: §4.7.10
// (Runtime supervisor, Launch; SDK-warm).
func claimDialDelay(raw, marker string) (time.Duration, error) {
	delay, err := ParseDialDelay(raw)
	if err != nil || delay == 0 {
		return 0, err
	}
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("claim launch marker %s: %w", marker, err)
	}
	if err := f.Close(); err != nil {
		return 0, fmt.Errorf("close launch marker %s: %w", marker, err)
	}
	return delay, nil
}

// countSignals counts every catchable signal the process receives and logs
// each one as "received signal NAME", so a test streaming the runtime
// container's log sees it even when the pod is gone before a report can be
// read. The fault signals and SIGPIPE are registered too: Go delivers only a
// kill()-sent fault signal to the channel and still crashes on a real fault,
// and a write to a closed connection still returns EPIPE, while an
// unregistered fault signal or SIGPIPE would be dropped and a forwarded one
// would go unseen. SIGURG is counted only when the process's GODEBUG turns
// off asynchronous preemption, the Go runtime's own use of SIGURG; otherwise
// the record lists it as uncounted. SIGTERM is counted, logged, and then
// honored by exiting 0, as a Standard-level runtime ends on SIGTERM, so a
// supervisor `end` finishes on the SIGTERM rather than at its SIGKILL.
// spec: §4.7.10 (Runtime supervisor, Signals; End); §15.4 (Standard-level
// graceful shutdown).
func (st *state) countSignals(stderr io.Writer, exit func(code int)) {
	counted, uncounted := countedSignals(os.Getenv("GODEBUG"))
	st.uncounted = uncounted
	st.sigs = make(chan os.Signal, 64)
	signal.Notify(st.sigs, counted...)
	go st.recordSignals(stderr, exit)
}

// countedSignals splits the catchable signals into those the probe
// registers and the names of those it leaves to the Go runtime. Only SIGURG
// can be left out, when godebug does not set asyncpreemptoff=1.
func countedSignals(godebug string) (counted []os.Signal, uncounted []string) {
	preemptOff := false
	for _, kv := range strings.Split(godebug, ",") {
		if strings.TrimSpace(kv) == "asyncpreemptoff=1" {
			preemptOff = true
		}
	}
	for s := syscall.Signal(1); s < 65; s++ {
		switch {
		case s == syscall.SIGKILL || s == syscall.SIGSTOP:
			continue
		case s == syscall.SIGURG && !preemptOff:
			uncounted = append(uncounted, s.String())
			continue
		}
		counted = append(counted, s)
	}
	return counted, uncounted
}

// recordSignals counts and logs each signal st.sigs delivers, and exits 0
// through exit after logging SIGTERM.
func (st *state) recordSignals(stderr io.Writer, exit func(code int)) {
	for s := range st.sigs {
		st.mu.Lock()
		st.signals[s.String()]++
		st.mu.Unlock()
		fmt.Fprintf(stderr, "supervise-probe: %s %s\n", SignalLogPrefix, s)
		if s == syscall.SIGTERM {
			exit(exitOK)
			return
		}
	}
}

// close stops the signal counter and the record server.
func (st *state) close() {
	signal.Stop(st.sigs)
	close(st.sigs)
	st.srv.close()
}

// record is the CmdReport answer.
func (st *state) record() Record {
	st.mu.Lock()
	defer st.mu.Unlock()
	signals := make(map[string]int, len(st.signals))
	for k, v := range st.signals {
		signals[k] = v
	}
	r := Record{
		Token:           st.token,
		PID:             os.Getpid(),
		ManifestAtStart: st.manifest,
		ManifestNow:     ReadManifestView(manifestPath()),
		Env:             st.env,
		Cleanups:        append([]CleanupRecord{}, st.cleanups...),
		Signals:         signals,
		Uncounted:       append([]string(nil), st.uncounted...),
	}
	if st.delay > 0 {
		r.DialDelay = st.delay.String()
	}
	if st.srv.err != nil {
		r.RecordServerError = st.srv.err.Error()
	}
	return r
}

// addCleanup stores one record a cleanup command reported.
func (st *state) addCleanup(rec CleanupRecord) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.cleanups = append(st.cleanups, rec)
}

// plantedLinks are the links CmdSymlink planted.
func (st *state) plantedLinks() []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]string(nil), st.links...)
}

// plantLink plants a symbolic link to AdapterRunDir under the workspace
// base: at its top when the agent can write there, otherwise in the
// session's slot. It also records the probe's own access attempts at the
// target, which a cleanup command's attempts through the link must not
// exceed. spec: §5.2 (Supervised scrub sequence, cleanupCommands).
func (st *state) plantLink(sessionID string) ActionResult {
	name := fmt.Sprintf("probe-link-%d", time.Now().UnixNano())
	candidates := []string{
		filepath.Join(WorkspaceBase, name),
		filepath.Join(WorkspaceBase, "slots", sessionID, "current", name),
	}
	var lastErr error
	for _, p := range candidates {
		if err := os.Symlink(AdapterRunDir, p); err != nil {
			lastErr = err
			continue
		}
		st.mu.Lock()
		st.links = append(st.links, p)
		st.mu.Unlock()
		return ActionResult{OK: true, Residue: Residue{Link: p}, Direct: accessAttempts(AdapterRunDir)}
	}
	return ActionResult{Error: fmt.Sprintf("plant link: %v", lastErr)}
}
