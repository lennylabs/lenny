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
)

// setStub configures the test binary as a stub runtime for the rest of the
// test. env holds the stub's settings beyond stub mode itself.
func setStub(t *testing.T, env map[string]string) string {
	t.Helper()
	t.Setenv(complianceStubEnv, "1")
	for _, k := range []string{stubAckEnv, stubAckDelayEnv, stubNoRereadEnv, stubDeadlineEnv, stubExitOnEndEnv, stubExitAfterResponsesEnv} {
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
		switch f.Type {
		case "session_start":
			s.acknowledge(f.SessionID, f.StartID)
		case "session_end":
			if os.Getenv(stubExitOnEndEnv) != "" {
				return 0
			}
		case "message":
			s.write(map[string]any{"type": "response", "sessionId": f.SessionID, "output": []map[string]any{{"type": "text", "inline": "pong"}}})
			s.respondedOnce.Do(func() { close(s.responded) })
			s.responses++
			if limit, err := strconv.Atoi(os.Getenv(stubExitAfterResponsesEnv)); err == nil && s.responses == limit {
				return 0
			}
		case "heartbeat":
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
			s.write(ack)
			s.acked.Store(true)
		}()
		return
	}
	s.write(ack)
	s.acked.Store(true)
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
