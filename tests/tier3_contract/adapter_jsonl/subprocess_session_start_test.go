//go:build contract

// SPDX-License-Identifier: MIT

package adapter_jsonl_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// captureLines returns the frames the capture runtime recorded, one per
// line.
func captureLines(t *testing.T, capturePath string) []string {
	t.Helper()
	raw, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read captured frames: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_start), 28.5.3 (CH-MSGSOCK,
//
//	Session frame writes), 17.4 (Local Development Mode)
//
// diagnosis: the developer-loop subprocess executor opened a child it
//
//	spawned on Send with a session_start that does not validate against the
//	published JSON Lines schema, wrote it after the session's first message,
//	wrote it more than once, or wrote one to a child the adapter spawned
//	through Start, which receives the adapter's own session_start. A runtime
//	built against the schema then rejects the frame that opens its session,
//	answers the message with a session error, or reads two opens for one
//	session. The frame is built from a Go struct whose JSON tags no compiler
//	checks against the schema, so the check has to read the wire.
func TestSubprocessSessionStartValidatesAgainstSchema_spec_28_5_3(t *testing.T) {
	t.Parallel()
	c := schematest.NewCompiler(t)
	schematest.MustAddLocalSchema(t, c, "https://schemas.lenny.dev/messagepart/v1.json", "schemas/messagepart.schema.json")
	schema := schematest.MustCompile(t, c, "schemas/lenny-adapter-jsonl.schema.json")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	sendBin, sendCapture := newCaptureRuntime(t)
	sendEx := executor.NewSubprocessExecutor(executor.SubprocessOptions{BinPath: sendBin, SendTimeout: 10 * time.Second})
	const sendSession = "sess-devloop-open"
	for _, content := range []string{"one", "two"} {
		if _, err := sendEx.Send(ctx, sendSession, []executor.Message{{Content: content}}); err != nil {
			t.Fatalf("Send(%s): %v", content, err)
		}
	}
	_ = sendEx.Close(ctx, sendSession)

	lines := captureLines(t, sendCapture)
	if len(lines) != 3 {
		t.Fatalf("Send-spawned child read %d frames, want session_start and two messages: %q", len(lines), lines)
	}
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader([]byte(lines[0])))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("first frame is not JSON: %v (%s)", err, lines[0])
	}
	if doc["type"] != "session_start" || doc["sessionId"] != sendSession || doc["startId"] != "1" || len(doc) != 3 {
		t.Errorf("first frame = %s, want {type:session_start, sessionId:%s, startId:\"1\"} and no other member", lines[0], sendSession)
	}
	if err := schema.Validate(doc); err != nil {
		t.Errorf("developer-loop session_start does not validate against the published schema: %v\n%s", err, lines[0])
	}
	for i, line := range lines[1:] {
		if !strings.Contains(line, `"type":"message"`) {
			t.Errorf("frame %d after session_start is not a message: %s", i+1, line)
		}
	}

	startBin, startCapture := newCaptureRuntime(t)
	startEx := executor.NewSubprocessExecutor(executor.SubprocessOptions{BinPath: startBin, SendTimeout: 10 * time.Second})
	const startSession = "sess-adapter-open"
	if err := startEx.Start(ctx, startSession); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := startEx.Send(ctx, startSession, []executor.Message{{Content: "hi"}}); err != nil {
		t.Fatalf("Send after Start: %v", err)
	}
	_ = startEx.Close(ctx, startSession)
	for _, line := range captureLines(t, startCapture) {
		if strings.Contains(line, `"type":"session_start"`) {
			t.Errorf("Start-spawned child read a session_start from the executor: %s", line)
		}
	}
}
