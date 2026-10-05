// SPDX-License-Identifier: MIT

package executor_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
)

// recordingRuntimeScript is a Basic-level runtime that appends every
// inbound frame verbatim to a record file and answers each message frame,
// and only a message frame, with one response. Answering only messages
// keeps the executor's response reads aligned with its message writes when
// the child also receives a session_start.
const recordingRuntimeScript = `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' "$line" >> "RECORD_PATH"
  case "$line" in
    *'"type":"message"'*)
      printf '%s\n' '{"type":"response","output":[{"type":"text","inline":"ack"}]}' ;;
  esac
done
`

// newRecordingRuntime writes the recording runtime into a temp dir and
// returns its path and the path of the file it records inbound frames to.
func newRecordingRuntime(t *testing.T) (binPath, recordPath string) {
	t.Helper()
	dir := t.TempDir()
	binPath = filepath.Join(dir, "recording-runtime")
	recordPath = filepath.Join(dir, "inbound.jsonl")
	script := strings.ReplaceAll(recordingRuntimeScript, "RECORD_PATH", recordPath)
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write recording runtime: %v", err)
	}
	return binPath, recordPath
}

// recordedFrames decodes every frame the recording runtime recorded.
func recordedFrames(t *testing.T, recordPath string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read recorded frames: %v", err)
	}
	var frames []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var doc map[string]any
		if err := json.Unmarshal([]byte(line), &doc); err != nil {
			t.Fatalf("recorded frame is not JSON: %v (%s)", err, line)
		}
		frames = append(frames, doc)
	}
	return frames
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_start), 17.4 (Local
// Development Mode)
//
// A child the developer loop spawns on Send reads exactly one
// session_start, carrying only type, sessionId, and the constant startId,
// before the first message, and later Sends on the same session write no
// further session_start.
func TestSubprocessSendSpawnOpensSessionWithSessionStart_spec_28_5_3(t *testing.T) {
	bin, record := newRecordingRuntime(t)
	ex := executor.NewSubprocessExecutor(executor.SubprocessOptions{BinPath: bin, SendTimeout: 10 * time.Second})
	const sessionID = "sess-devloop"
	for _, content := range []string{"first", "second"} {
		if _, err := ex.Send(context.Background(), sessionID, []executor.Message{{Content: content}}); err != nil {
			t.Fatalf("Send(%s): %v", content, err)
		}
	}
	if err := ex.Close(context.Background(), sessionID); err != nil {
		t.Fatalf("Close: %v", err)
	}

	frames := recordedFrames(t, record)
	wantTypes := []string{"session_start", "message", "message"}
	if len(frames) != len(wantTypes) {
		t.Fatalf("runtime read %d frames, want %d: %v", len(frames), len(wantTypes), frames)
	}
	for i, f := range frames {
		if f["type"] != wantTypes[i] {
			t.Errorf("frame %d type = %v, want %s", i, f["type"], wantTypes[i])
		}
	}
	want := map[string]any{"type": "session_start", "sessionId": sessionID, "startId": "1"}
	if len(frames[0]) != len(want) {
		t.Errorf("session_start carries members %v, want exactly %v", frames[0], want)
	}
	for k, v := range want {
		if frames[0][k] != v {
			t.Errorf("session_start %s = %v, want %v", k, frames[0][k], v)
		}
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Session frame writes), 17.4 (Local
// Development Mode)
//
// A child Start spawns belongs to the adapter, which writes the session's
// session_start itself through WriteEnvelope, so the executor writes none
// to it, neither at Start nor on a later Send.
func TestSubprocessStartSpawnWritesNoSessionStart_spec_28_5_3(t *testing.T) {
	bin, record := newRecordingRuntime(t)
	ex := executor.NewSubprocessExecutor(executor.SubprocessOptions{BinPath: bin, SendTimeout: 10 * time.Second})
	const sessionID = "sess-adapter"
	if err := ex.Start(context.Background(), sessionID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := ex.Send(context.Background(), sessionID, []executor.Message{{Content: "hi"}}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := ex.Close(context.Background(), sessionID); err != nil {
		t.Fatalf("Close: %v", err)
	}

	frames := recordedFrames(t, record)
	if len(frames) != 1 || frames[0]["type"] != "message" {
		t.Errorf("runtime read %v, want a single message frame and no session_start", frames)
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_start), 17.4 (Local
// Development Mode)
//
// A runtime that answers session_start with session_started does not
// desynchronize Send: the acknowledgement is skipped and the message's own
// response is returned.
func TestSubprocessSendSkipsSessionStarted_spec_28_5_3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acking-runtime")
	script := `#!/bin/sh
read start
printf '%s\n' '{"type":"session_started","sessionId":"sess-ack","startId":"1"}'
read msg
printf '%s\n' '{"type":"response","output":[{"type":"text","inline":"answered"}]}'
read eof
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write runtime: %v", err)
	}
	ex := executor.NewSubprocessExecutor(executor.SubprocessOptions{BinPath: path, SendTimeout: 10 * time.Second})
	defer ex.Close(context.Background(), "sess-ack")
	out, err := ex.Send(context.Background(), "sess-ack", []executor.Message{{Content: "hi"}})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(out.Parts) != 1 || out.Parts[0].Text != "answered" {
		t.Errorf("Send returned %+v, want the message's response", out)
	}
}
