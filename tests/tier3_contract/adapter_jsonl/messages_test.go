//go:build contract

// SPDX-License-Identifier: MIT

// Package adapter_jsonl_test is the Tier 3 contract suite for the
// adapter ↔ agent binary protocol. Phase 1 shipped the schema; Phase 2
// converts these tests from failing stubs to real round-trip checks
// against the Basic-level echo runtime (cmd/runtimes/echo).
//
// Standard-level checks (tool_call/tool_result correlation, adapter-
// local tool path confinement) remain failing-with-diagnosis stubs
// until a Standard-level runtime ships. Same for the gateway-side
// response shorthand normalisation.
package adapter_jsonl_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// echoBinary is the path to the compiled cmd/runtimes/echo binary. It is
// built once per test-process invocation by TestMain.
var echoBinary string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "echo-contract-*")
	if err != nil {
		panic("echo contract TestMain: mkdtemp: " + err.Error())
	}
	defer os.RemoveAll(tmp)

	echoBinary = filepath.Join(tmp, "echo")
	root := repoRoot()
	cmd := exec.Command("go", "build", "-o", echoBinary, "./cmd/runtimes/echo")
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic("echo contract TestMain: build: " + err.Error())
	}
	os.Exit(m.Run())
}

func repoRoot() string {
	wd, _ := os.Getwd()
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	return wd
}

// driveEcho launches a fresh echo process, writes the given lines on
// stdin, then closes stdin. It returns the first n stdout lines (or
// fewer if the process exits sooner) and the exit code. ctx caps the
// total wall-clock budget.
func driveEcho(t *testing.T, ctx context.Context, inputs []string, n int) (lines []string, exitCode int) {
	t.Helper()
	cmd := exec.CommandContext(ctx, echoBinary)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start echo: %v", err)
	}

	var writeWG sync.WaitGroup
	writeWG.Add(1)
	go func() {
		defer writeWG.Done()
		defer stdin.Close()
		for _, in := range inputs {
			if !strings.HasSuffix(in, "\n") {
				in += "\n"
			}
			if _, err := io.WriteString(stdin, in); err != nil {
				return
			}
		}
	}()

	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 64*1024), 50*1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if n > 0 && len(lines) >= n {
			break
		}
	}
	writeWG.Wait()

	waitErr := cmd.Wait()
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return lines, exitCode
}

// spec: 15.4 (Basic-level: stdin/stdout JSONL contract)
// diagnosis: cmd/runtimes/echo did not produce the expected envelope for
//
//	each canonical inbound message kind. Verify the adapter
//	dispatches on `type` correctly and that response/heartbeat_ack
//	shapes match schemas/lenny-adapter-jsonl.schema.json.
func TestAdapterAcceptsCanonicalMessages(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	inputs := []string{
		`{"type":"message","id":"msg_01J9X0ZW1ZF7K8Q1V2T3M4N5P1","from":{"kind":"client","id":"client_alice"},"input":[{"type":"text","inline":"ping"}]}`,
		`{"type":"heartbeat","ts":1}`,
	}
	lines, exit := driveEcho(t, ctx, inputs, 2)
	if exit != 0 {
		t.Fatalf("exit code %d, want 0", exit)
	}
	if len(lines) < 2 {
		t.Fatalf("got %d stdout lines, want >=2: %q", len(lines), lines)
	}

	// Each line must validate against the JSONL schema.
	c := schematest.NewCompiler(t)
	schematest.MustAddLocalSchema(t, c, "https://schemas.lenny.dev/outputpart/v1.json", "schemas/outputpart.schema.json")
	jsonl := schematest.MustCompile(t, c, "schemas/lenny-adapter-jsonl.schema.json")
	for i, line := range lines[:2] {
		var v any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatalf("line %d not JSON: %v (%s)", i, err, line)
		}
		if err := jsonl.Validate(v); err != nil {
			t.Errorf("line %d failed schema: %v\n  payload: %s", i, err, line)
		}
	}
}

// spec: 15.4
// diagnosis: heartbeat → heartbeat_ack must complete within 10 seconds.
//
//	If the adapter delays beyond that, the gateway sends SIGTERM
//	per the spec.
func TestAdapterHeartbeatAckWithin10s(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	lines, exit := driveEcho(t, ctx, []string{`{"type":"heartbeat","ts":1}`}, 1)
	elapsed := time.Since(start)

	if exit != 0 {
		t.Fatalf("exit code %d", exit)
	}
	if len(lines) == 0 {
		t.Fatalf("no heartbeat_ack received")
	}
	if elapsed > 10*time.Second {
		t.Errorf("heartbeat_ack arrived after %s, must be within 10s", elapsed)
	}
	var ack struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &ack); err != nil {
		t.Fatalf("ack not JSON: %v", err)
	}
	if ack.Type != "heartbeat_ack" {
		t.Errorf("ack.type = %q, want heartbeat_ack", ack.Type)
	}
}

// spec: 15.4 (forward compatibility)
// diagnosis: An unknown `type` value must NOT crash the adapter; it must
//
//	be silently dropped. The adapter must continue processing
//	subsequent messages.
func TestAdapterIgnoresUnknownMessageTypes(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	inputs := []string{
		`{"type":"some_future_kind","payload":{"x":1}}`,
		`{"type":"heartbeat","ts":99}`,
	}
	lines, exit := driveEcho(t, ctx, inputs, 1)
	if exit != 0 {
		t.Fatalf("exit %d (unknown type must be ignored, not fatal)", exit)
	}
	if len(lines) == 0 {
		t.Fatalf("heartbeat after unknown type produced no ack — the adapter likely treated the unknown type as fatal")
	}
}

// spec: 15.4
// diagnosis: After receiving `shutdown`, the adapter must exit cleanly
//
//	within deadline_ms. Echo is single-goroutine so its exit
//	is fast; this test guards against a regression that hangs
//	the adapter past the deadline.
func TestAdapterShutdownExitsWithinDeadline(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	_, exit := driveEcho(t, ctx, []string{
		`{"type":"shutdown","reason":"test","deadline_ms":250}`,
	}, 0)
	elapsed := time.Since(start)

	if exit != 0 {
		t.Fatalf("exit %d, want 0", exit)
	}
	if elapsed > 2*time.Second {
		t.Errorf("shutdown took %s, must be well under the 250ms deadline (with OS scheduling slack)", elapsed)
	}
}

// spec: 15.4
// diagnosis: Sequential message envelopes must each receive a response
//
//	in order. The adapter must not buffer or coalesce.
func TestAdapterSequentialMessagesHandled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	inputs := []string{
		`{"type":"message","id":"msg_01J9X0ZW1ZF7K8Q1V2T3M4N5A1","from":{"kind":"client","id":"client_alice"},"input":[{"type":"text","inline":"one"}]}`,
		`{"type":"message","id":"msg_01J9X0ZW1ZF7K8Q1V2T3M4N5A2","from":{"kind":"client","id":"client_alice"},"input":[{"type":"text","inline":"two"}]}`,
		`{"type":"message","id":"msg_01J9X0ZW1ZF7K8Q1V2T3M4N5A3","from":{"kind":"client","id":"client_alice"},"input":[{"type":"text","inline":"three"}]}`,
	}
	lines, exit := driveEcho(t, ctx, inputs, 3)
	if exit != 0 {
		t.Fatalf("exit %d", exit)
	}
	if len(lines) < 3 {
		t.Fatalf("got %d response(s), want 3", len(lines))
	}
	for i, line := range lines[:3] {
		var resp struct {
			Type   string `json:"type"`
			Output []struct {
				Type   string `json:"type"`
				Inline string `json:"inline"`
			} `json:"output"`
		}
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("response %d not JSON: %v", i, err)
		}
		if resp.Type != "response" {
			t.Errorf("response[%d].type = %q, want response", i, resp.Type)
		}
		if len(resp.Output) == 0 || resp.Output[0].Type != "text" {
			t.Errorf("response[%d] missing text part", i)
		}
	}
}

// spec: 15.4 (Standard-level deliverable; Phase 2 stub)
// diagnosis: tool_call/tool_result correlation lives at the Standard
//
//	integration level. A Basic-level adapter never emits
//	tool_call, so this test cannot run against echo. It will
//	exercise streaming-echo (Phase 2.8) and delegation-echo
//	(Phase 9) when those runtimes ship.
func TestAdapterToolResultCorrelation(t *testing.T) {
	t.Skipf("not yet applicable: tool_call/tool_result correlation requires a Standard-level adapter; ships in Phase 2.8+")
}

// spec: 15.4 (gateway-side normalisation; tested at the gateway in a
//
//	later phase)
//
// diagnosis: The shorthand {"type":"response","text":"..."} is valid on
//
//	the wire. The gateway normalises it to the canonical
//	{"type":"response","output":[{"type":"text","inline":"..."}]}
//	form. This is a gateway-side test that lands when the
//	gateway implementation does.
func TestAdapterResponseShorthandNormalised(t *testing.T) {
	t.Skipf("not yet applicable: shorthand response normalisation is a gateway-side responsibility; tested in the gateway phase")
}

// spec: 15.4 (Standard-level deliverable)
// diagnosis: Adapter-local tools (read_file, write_file, list_dir,
//
//	delete_file) require the Standard-level MCP socket. Not
//	implemented in Basic-level echo.
func TestAdapterLocalToolsRejectPathTraversal(t *testing.T) {
	t.Skipf("not yet applicable: adapter-local tools require Standard-level MCP socket; ships in Phase 5+")
}
