// SPDX-License-Identifier: MIT

// A configurable stub runtime for the battery's reject and pass cases.
// TestMain runs this test binary as the stub when complianceStubEnv is
// set, so a test points a check at os.Args[0] and sets the environment
// to choose the stub's one defect. Every stub is a valid runtime apart
// from the defect it is configured with: it answers every message,
// heartbeat, and session-scoped CH-RUNTIMEOPS frame, and keeps stdout open
// until stdin closes.
package main

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	// complianceStubEnv selects stub mode for the test binary.
	complianceStubEnv = "LENNY_COMPLIANCE_STUB"
	// stubAckEnv selects the stub's session_started behavior: immediate
	// (the default), never, error, wrongstart, or delayed.
	stubAckEnv = "LENNY_COMPLIANCE_STUB_ACK"
	// stubAckDelayEnv is the delay of the delayed acknowledgement, as a
	// Go duration. A delayed stub exits when it reads a session-scoped
	// CH-RUNTIMEOPS frame before it wrote the acknowledgement.
	stubAckDelayEnv = "LENNY_COMPLIANCE_STUB_ACK_DELAY"
	// stubNoRereadEnv, when set, makes the stub acknowledge a rotation
	// without opening the credential file it names.
	stubNoRereadEnv = "LENNY_COMPLIANCE_STUB_NO_REREAD"
	// stubDeadlineEnv selects the stub's deadline_approaching behavior:
	// ok (the default), exit (exit after the message's response),
	// exitafterack (exit right after the first heartbeat_ack written
	// after a response), or second (write another response for the
	// session).
	stubDeadlineEnv = "LENNY_COMPLIANCE_STUB_DEADLINE"
	// stubExitOnEndEnv, when set, makes the stub exit on its first
	// session_end.
	stubExitOnEndEnv = "LENNY_COMPLIANCE_STUB_EXIT_ON_END"
	// stubExitAfterResponsesEnv, when set to a count N, makes the stub
	// exit right after it writes its Nth response.
	stubExitAfterResponsesEnv = "LENNY_COMPLIANCE_STUB_EXIT_AFTER_RESPONSES"
	// stubStatusEnv, when set, makes the stub write an optional outbound
	// status frame before each response and before each heartbeat_ack, as
	// a runtime that reports progress while it serves a message does.
	stubStatusEnv = "LENNY_COMPLIANCE_STUB_STATUS"
	// stubResponseErrorEnv, when set, makes every response the stub
	// writes carry an error, as a runtime whose model call fails does.
	stubResponseErrorEnv = "LENNY_COMPLIANCE_STUB_RESPONSE_ERROR"
)

// setStub configures the test binary as a stub runtime for the rest of the
// test. env holds the stub's settings beyond stub mode itself.
func setStub(t *testing.T, env map[string]string) string {
	t.Helper()
	t.Setenv(complianceStubEnv, "1")
	for _, k := range []string{stubAckEnv, stubAckDelayEnv, stubNoRereadEnv, stubDeadlineEnv, stubExitOnEndEnv, stubExitAfterResponsesEnv, stubStatusEnv, stubResponseErrorEnv} {
		t.Setenv(k, env[k])
	}
	return os.Args[0]
}

// complianceStub is the stub runtime's state.
type complianceStub struct {
	outMu sync.Mutex
	out   *json.Encoder
	// acked is set once the stub wrote a session_started.
	acked atomic.Bool
	// responded is closed once the stub answered a message.
	responded     chan struct{}
	respondedOnce sync.Once
	// responses counts the responses the stub wrote.
	responses int
	// lastSession is the sessionId of the last session-scoped frame the
	// stub read, which its status frames before a heartbeat_ack carry.
	lastSession string
}

// writeStatus writes a status frame for sessionID when stubStatusEnv is
// set, and does nothing otherwise.
func (s *complianceStub) writeStatus(sessionID string) {
	if os.Getenv(stubStatusEnv) == "" || sessionID == "" {
		return
	}
	s.write(map[string]any{"type": "status", "state": "thinking", "message": "working", "sessionId": sessionID})
}

// exit closes stdout under the writer lock and then exits. Closing stdout
// first makes the harness see the end of stdout at once, rather than after
// the race detector's exit delay, and the lock keeps a concurrent write
// from landing after the close.
func (s *complianceStub) exit() {
	s.outMu.Lock()
	_ = os.Stdout.Close()
	os.Exit(0)
}

// write encodes one stdout frame under the writer lock.
func (s *complianceStub) write(v any) {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	_ = s.out.Encode(v)
}

// writeAck marks the session acknowledged and then encodes the
// session_started frame, both under the writer lock. The flag is set before
// any byte of the frame reaches stdout, so a harness that reads the
// acknowledgement and then writes a session-scoped CH-RUNTIMEOPS frame can
// never find the runtimeOps guard still reading acked as false.
func (s *complianceStub) writeAck(ack map[string]any) {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	s.acked.Store(true)
	_ = s.out.Encode(ack)
}

// stubResponse builds the stub's response to a message for sessionID. It
// carries an error when stubResponseErrorEnv is set.
func stubResponse(sessionID string) map[string]any {
	if os.Getenv(stubResponseErrorEnv) != "" {
		return map[string]any{"type": "response", "sessionId": sessionID, "output": []map[string]any{}, "error": map[string]any{"code": "RATE_LIMITED", "message": "stub model call failed"}}
	}
	return map[string]any{"type": "response", "sessionId": sessionID, "output": []map[string]any{{"type": "text", "inline": "pong"}}}
}

// runComplianceStub runs the stub over stdin and stdout, dialing
// CH-RUNTIMEOPS when the manifest names a socket. It returns the exit code.
func runComplianceStub(string) int {
	s := &complianceStub{out: json.NewEncoder(os.Stdout), responded: make(chan struct{})}
	if path := os.Getenv("LENNY_ADAPTER_MANIFEST"); path != "" {
		go s.runtimeOps(path)
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var f struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
			StartID   string `json:"startId"`
		}
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			continue
		}
		if f.SessionID != "" {
			s.lastSession = f.SessionID
		}
		switch f.Type {
		case "session_start":
			s.acknowledge(f.SessionID, f.StartID)
		case "session_end":
			if os.Getenv(stubExitOnEndEnv) != "" {
				return 0
			}
		case "message":
			s.writeStatus(f.SessionID)
			s.write(stubResponse(f.SessionID))
			s.respondedOnce.Do(func() { close(s.responded) })
			s.responses++
			if limit, err := strconv.Atoi(os.Getenv(stubExitAfterResponsesEnv)); err == nil && s.responses == limit {
				return 0
			}
		case "heartbeat":
			s.writeStatus(s.lastSession)
			s.write(map[string]any{"type": "heartbeat_ack"})
			if os.Getenv(stubDeadlineEnv) == "exitafterack" && s.responses > 0 {
				s.exit()
			}
		case "shutdown":
			return 0
		}
	}
	return 0
}

// acknowledge answers a session_start as stubAckEnv selects.
func (s *complianceStub) acknowledge(sessionID, startID string) {
	ack := map[string]any{"type": "session_started", "sessionId": sessionID, "startId": startID}
	switch os.Getenv(stubAckEnv) {
	case "never":
		return
	case "error":
		ack["error"] = map[string]any{"code": "RUNTIME_ERROR", "message": "stub context failure"}
	case "wrongstart":
		ack["startId"] = startID + "-other"
	case "delayed":
		delay, _ := time.ParseDuration(os.Getenv(stubAckDelayEnv))
		go func() {
			time.Sleep(delay)
			s.writeAck(ack)
		}()
		return
	}
	s.writeAck(ack)
}

// runtimeOps dials the manifest's CH-RUNTIMEOPS socket, answers the
// capability handshake with every capability, and answers each
// session-scoped frame.
func (s *complianceStub) runtimeOps(manifestPath string) {
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		return
	}
	var m struct {
		RuntimeOps struct {
			Socket string `json:"socket"`
		} `json:"runtimeOps"`
	}
	if json.Unmarshal(body, &m) != nil || m.RuntimeOps.Socket == "" {
		return
	}
	conn, err := net.Dial("unix", m.RuntimeOps.Socket)
	if err != nil {
		return
	}
	defer conn.Close()
	enc := json.NewEncoder(conn)
	r := bufio.NewReader(conn)
	if _, err := r.ReadBytes('\n'); err != nil {
		return
	}
	_ = enc.Encode(map[string]any{"type": "lifecycle_support", "capabilities": []string{"checkpoint", "interrupt", "credential_rotation", "deadline_signal"}})
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		var f map[string]any
		if json.Unmarshal(line, &f) != nil {
			continue
		}
		if os.Getenv(stubAckEnv) == "delayed" && !s.acked.Load() {
			// The frame arrived before the acknowledgement: a check that
			// writes before its read succeeds sees the runtime exit.
			os.Exit(3)
		}
		s.answerOp(f, enc)
	}
}

// answerOp answers one session-scoped CH-RUNTIMEOPS frame.
func (s *complianceStub) answerOp(f map[string]any, enc *json.Encoder) {
	switch f["type"] {
	case "checkpoint_request":
		_ = enc.Encode(map[string]any{"type": "checkpoint_ready", "checkpointId": f["checkpointId"]})
	case "interrupt_request":
		_ = enc.Encode(map[string]any{"type": "interrupt_acknowledged", "interruptId": f["interruptId"]})
	case "credentials_rotated":
		if os.Getenv(stubNoRereadEnv) == "" {
			path, _ := f["credentialsPath"].(string)
			if _, err := os.ReadFile(path); err != nil {
				return
			}
		}
		_ = enc.Encode(map[string]any{"type": "credentials_acknowledged", "leaseId": f["leaseId"], "provider": f["provider"]})
	case "deadline_approaching":
		s.onDeadline(f)
	}
}

// onDeadline plays the deadline behavior stubDeadlineEnv selects, after
// the message's response is written.
func (s *complianceStub) onDeadline(f map[string]any) {
	mode := os.Getenv(stubDeadlineEnv)
	if mode == "" || mode == "ok" {
		return
	}
	select {
	case <-s.responded:
	case <-time.After(2 * time.Second):
	}
	switch mode {
	case "exit":
		s.exit()
	case "second":
		s.write(map[string]any{"type": "response", "sessionId": f["sessionId"], "output": []map[string]any{}, "error": map[string]any{"code": "DEADLINE_EXCEEDED", "message": "deadline"}})
	}
}

// ackObservingWriter records, for each write, whether the stub's acked flag
// was already set when the bytes reached stdout.
type ackObservingWriter struct {
	stub *complianceStub
	mu   sync.Mutex
	seen []bool
	done chan struct{}
}

func (w *ackObservingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.seen = append(w.seen, w.stub.acked.Load())
	w.mu.Unlock()
	close(w.done)
	return len(p), nil
}

// TestStubMarksAcknowledgedBeforeSessionStartedReachesStdout pins the stub's
// ordering: in both the immediate and the delayed arm, acked is true by the
// time any byte of session_started is written. A harness check that reads
// the acknowledgement and then writes a session-scoped CH-RUNTIMEOPS frame
// must never trip the delayed stub's early-frame exit.
// spec: 15.4.6 (Conformance Test Suite), 28.5.3 (CH-MSGSOCK Outbound:
// session_started)
func TestStubMarksAcknowledgedBeforeSessionStartedReachesStdout(t *testing.T) {
	for _, mode := range []string{"", "delayed"} {
		t.Run("mode="+mode, func(t *testing.T) {
			t.Setenv(stubAckEnv, mode)
			t.Setenv(stubAckDelayEnv, "1ms")
			s := &complianceStub{responded: make(chan struct{})}
			w := &ackObservingWriter{stub: s, done: make(chan struct{})}
			s.out = json.NewEncoder(w)
			s.acknowledge("sess-1", "start-1")
			select {
			case <-w.done:
			case <-time.After(5 * time.Second):
				t.Fatal("the stub never wrote session_started")
			}
			w.mu.Lock()
			defer w.mu.Unlock()
			if len(w.seen) != 1 || !w.seen[0] {
				t.Fatalf("acked at write time = %v; want [true]: the flag must be set before session_started reaches stdout", w.seen)
			}
		})
	}
}
