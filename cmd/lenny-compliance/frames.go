// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/lennylabs/lenny/pkg/runtimekit"
)

// sessionStartedMargin is the fixed scheduling margin the battery adds to
// the adapter's default acknowledgement wait when it reads session_started.
// It absorbs the harness's own process and pipe scheduling, so a runtime
// that answers within what a default adapter accepts is not failed by the
// harness's overhead. It has no flag: the tunable value is the adapter's
// own acknowledgement-timeout flag.
const sessionStartedMargin = time.Second

// sessionStartedWait is the bound the battery places on its read of a
// session_started frame: the adapter's default acknowledgement wait plus
// sessionStartedMargin. The default is imported from pkg/runtimekit, which
// imports only the standard library, so this standalone binary bounds its
// read by the adapter's default without importing the adapter.
//
// spec: §15.4.6 (Conformance Test Suite, Test categories by integration
// level), §28.5.3 (CH-MSGSOCK, Outbound: session_started rule 3).
func sessionStartedWait() time.Duration {
	return runtimekit.DefaultSessionStartAckTimeout + sessionStartedMargin
}

// fullCheckBudget is the process budget a Full check has for its
// connection, its CH-RUNTIMEOPS handshake, and its own exchange. A Full
// check's process context is the session_started wait plus this budget,
// so a runtime that takes the whole wait still has the full budget for the
// rest of the check and is not killed while the read is pending.
const fullCheckBudget = 10 * time.Second

// errSessionStartedWait marks a session_started read whose wait elapsed
// before a matching frame arrived. A caller tells it apart from every
// other read failure, such as the end of stdout, with errors.Is.
var errSessionStartedWait = errors.New("session_started did not arrive within the acknowledgement wait")

// errStdoutClosed marks a read that found the runtime's stdout closed.
var errStdoutClosed = errors.New("runtime closed stdout")

// errFrameWait marks a read whose wait elapsed before any frame arrived.
var errFrameWait = errors.New("no frame within the wait")

// frameReader pumps a runtime's stdout lines onto a channel from one
// goroutine, so a check reads frames under a wait without leaving a read
// pending on the scanner when the wait elapses. Every read of the
// process's stdout goes through the one reader, so no two reads share the
// scanner.
type frameReader struct {
	lines chan string
	stop  chan struct{}
}

// newFrameReader starts the pump over s. The pump ends at the end of
// stdout or when close is called; the caller calls close once it is done
// with the process.
func newFrameReader(s *bufio.Scanner) *frameReader {
	r := &frameReader{lines: make(chan string), stop: make(chan struct{})}
	go func() {
		defer close(r.lines)
		for s.Scan() {
			select {
			case r.lines <- s.Text():
			case <-r.stop:
				return
			}
		}
	}()
	return r
}

// close stops the pump. The pump exits at its next line or at the end of
// stdout, whichever comes first, so closing the process's stdout (reap)
// ends it.
func (r *frameReader) close() { close(r.stop) }

// next returns the next stdout line, errStdoutClosed when stdout ends
// first, or errFrameWait when wait elapses first.
func (r *frameReader) next(wait time.Duration) (string, error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case line, ok := <-r.lines:
		if !ok {
			return "", errStdoutClosed
		}
		return line, nil
	case <-timer.C:
		return "", errFrameWait
	}
}

// sessionFrame is the subset of a runtime's stdout frame the battery's
// session reads decode.
type sessionFrame struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	StartID   string `json:"startId"`
	Error     *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// decodeSessionFrame decodes line, reporting false when it is not a JSON
// object.
func decodeSessionFrame(line string) (sessionFrame, bool) {
	var f sessionFrame
	return f, json.Unmarshal([]byte(line), &f) == nil
}

// awaitSessionStarted reads stdout until the session_started that carries
// sessionID and startID. It skips every other frame, including a
// session_started for another session or another start. It fails when the
// matching frame carries error, when stdout ends first, and, with an error
// wrapping errSessionStartedWait, when wait elapses first. A check whose
// read fails returns at once.
//
// spec: §15.4.6 (Conformance Test Suite, Test categories by integration
// level), §28.5.3 (CH-MSGSOCK, Outbound: session_started).
func awaitSessionStarted(r *frameReader, sessionID, startID string, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		line, err := r.next(time.Until(deadline))
		switch {
		case errors.Is(err, errFrameWait):
			return fmt.Errorf("%w: no session_started for session %s start %s within %s", errSessionStartedWait, sessionID, startID, wait)
		case err != nil:
			return fmt.Errorf("read session_started for session %s: %w", sessionID, err)
		}
		f, ok := decodeSessionFrame(line)
		if !ok || f.Type != "session_started" || f.SessionID != sessionID || f.StartID != startID {
			continue
		}
		if f.Error != nil {
			return fmt.Errorf("session_started for session %s start %s carries error %s: %s", sessionID, startID, f.Error.Code, f.Error.Message)
		}
		return nil
	}
}

// writeLines writes each line to w, newline-terminated.
func writeLines(w io.Writer, lines ...string) error {
	for _, line := range lines {
		if _, err := io.WriteString(w, line+"\n"); err != nil {
			return fmt.Errorf("write runtime input: %w", err)
		}
	}
	return nil
}
