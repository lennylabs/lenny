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
	st.countSignals(stderr)
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

// countSignals counts every catchable signal the process receives, except
// SIGPIPE, which a closed adapter connection raises on a write and which the
// Go runtime already turns into a write error. SIGURG is left alone because
// the Go runtime uses it for goroutine preemption. Each signal is logged as
// "received signal NAME" so a test streaming the runtime container's log
// sees it even when the pod is gone before a report can be read.
func (st *state) countSignals(stderr io.Writer) {
	st.sigs = make(chan os.Signal, 64)
	for s := syscall.Signal(1); s < 65; s++ {
		switch s {
		case syscall.SIGKILL, syscall.SIGSTOP, syscall.SIGPIPE, syscall.SIGURG,
			syscall.SIGSEGV, syscall.SIGBUS, syscall.SIGFPE, syscall.SIGILL:
			continue
		}
		signal.Notify(st.sigs, s)
	}
	go func() {
		for s := range st.sigs {
			st.mu.Lock()
			st.signals[s.String()]++
			st.mu.Unlock()
			fmt.Fprintf(stderr, "supervise-probe: %s %s\n", SignalLogPrefix, s)
		}
	}()
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
