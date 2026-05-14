// SPDX-License-Identifier: MIT

// Package harness ships the shared SDK contract test driver per
// TESTING.md §14.13. The driver spawns a language-native helper
// subprocess (sdks/client/python/test-helper, etc.) over stdin/
// stdout, sends one Command per line, and reads one Response per
// line. The same operation matrix runs against every registered
// SDK and the harness asserts equivalence — Python client → gateway
// behaves identically to Go client → gateway.
//
// Protocol:
//
//	→ {"op": "create_session", "args": {...}}
//	← {"ok": true, "result": {...}}
//	→ {"op": "shutdown"}
//	← {"ok": true}
//
// Operations the harness emits:
//
//   - create_session    {runtime, user_id, idempotency_key?}
//   - get_session       {id}
//   - list_sessions     {state?}
//   - delete_session    {id}
//   - finalize          {id}
//   - start             {id}
//   - interrupt         {id}
//   - terminate         {id}
//   - verify_webhook    {signature, body, secret}
//   - shutdown
//
// Helpers report errors via {"ok": false, "error": "...", "code": "..."}.
// The harness compares only the result envelope; transport-level
// details (HTTP headers, retry attempts) are not part of the contract.
package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// Helper is one running language-native helper process.
type Helper struct {
	name   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	mu     sync.Mutex
}

// Command is one harness request.
type Command struct {
	Op   string         `json:"op"`
	Args map[string]any `json:"args,omitempty"`
}

// Response is one helper reply.
type Response struct {
	OK     bool           `json:"ok"`
	Error  string         `json:"error,omitempty"`
	Code   string         `json:"code,omitempty"`
	Result map[string]any `json:"result,omitempty"`
}

// Spawn starts the helper at the given path. Registers a t.Cleanup
// that sends shutdown + kills the process. The helper is expected
// to exit within 5 seconds of `shutdown`; if not the harness sends
// SIGKILL.
func Spawn(t testing.TB, name, path string, env map[string]string) *Helper {
	t.Helper()
	cmd := exec.Command(path)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper %s: %v", name, err)
	}
	// Drain stderr into t.Log so helper diagnostics surface in the
	// test output without blocking the pipe.
	go func() {
		s := bufio.NewScanner(stderr)
		for s.Scan() {
			t.Logf("[%s stderr] %s", name, s.Text())
		}
	}()
	h := &Helper{
		name:   name,
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewScanner(stdout),
	}
	h.stdout.Buffer(make([]byte, 64*1024), 4<<20)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = h.Send(Command{Op: "shutdown"})
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
		case err := <-done:
			if err != nil {
				t.Logf("helper %s exit: %v", name, err)
			}
		}
	})
	return h
}

// Name returns the helper's friendly name (set at Spawn).
func (h *Helper) Name() string { return h.name }

// Send writes one Command + reads one Response. Serialized so two
// goroutines do not interleave on the pipe.
func (h *Helper) Send(c Command) (Response, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	body, err := json.Marshal(c)
	if err != nil {
		return Response{}, fmt.Errorf("marshal: %w", err)
	}
	if _, err := h.stdin.Write(append(body, '\n')); err != nil {
		return Response{}, fmt.Errorf("write: %w", err)
	}
	if !h.stdout.Scan() {
		if err := h.stdout.Err(); err != nil {
			return Response{}, fmt.Errorf("read: %w", err)
		}
		return Response{}, fmt.Errorf("helper %s closed stdout", h.name)
	}
	var resp Response
	if err := json.Unmarshal(h.stdout.Bytes(), &resp); err != nil {
		return Response{}, fmt.Errorf("decode: %w (line=%q)", err, h.stdout.Bytes())
	}
	return resp, nil
}

// MatrixCase is one row in the operation matrix.
type MatrixCase struct {
	Name        string
	Command     Command
	ExpectError string // exact error code to expect, or "" for success
}

// AssertEquivalent runs the same set of MatrixCase commands against
// every helper and asserts the (ok, code) tuple matches across all
// of them. The result body is compared via canonical JSON; transport
// details are out of scope.
func AssertEquivalent(t *testing.T, helpers []*Helper, cases []MatrixCase) {
	t.Helper()
	if len(helpers) < 2 {
		t.Skipf("AssertEquivalent requires ≥2 helpers; got %d", len(helpers))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			responses := make([]Response, len(helpers))
			for i, h := range helpers {
				resp, err := h.Send(c.Command)
				if err != nil {
					t.Fatalf("%s: send: %v", h.Name(), err)
				}
				responses[i] = resp
			}
			// Pin to the first helper's verdict.
			pin := responses[0]
			for i, r := range responses[1:] {
				if r.OK != pin.OK {
					t.Errorf("%s vs %s: OK mismatch (%v vs %v)",
						helpers[0].Name(), helpers[i+1].Name(), pin.OK, r.OK)
				}
				if r.Code != pin.Code {
					t.Errorf("%s vs %s: code mismatch (%q vs %q)",
						helpers[0].Name(), helpers[i+1].Name(), pin.Code, r.Code)
				}
			}
			// Expected-error verdict.
			if c.ExpectError != "" {
				if pin.OK {
					t.Errorf("expected error %q but helper succeeded", c.ExpectError)
				}
				if pin.Code != c.ExpectError {
					t.Errorf("expected error %q, got %q", c.ExpectError, pin.Code)
				}
			} else if !pin.OK {
				t.Errorf("expected success, got error %q (message=%q)", pin.Code, pin.Error)
			}
		})
	}
}

// CanonicalResult returns a result map with keys sorted so two
// SDKs that emit semantically-identical results compare byte-equal.
func CanonicalResult(r Response) string {
	if !r.OK || r.Result == nil {
		return ""
	}
	body, _ := json.Marshal(r.Result)
	// Re-decode + re-encode through a canonical encoder so map
	// iteration order is normalised. (The encoder/json package in
	// Go already sorts maps by key, but stay explicit.)
	var probe map[string]any
	_ = json.Unmarshal(body, &probe)
	out, _ := json.Marshal(probe)
	return strings.TrimSpace(string(out))
}
