//go:build contract

// SPDX-License-Identifier: MIT

package adapter_jsonl_test

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

// captureRuntimeScript is a Basic-level runtime that records every inbound
// frame verbatim and answers each one with a canonical response. It stands
// in for a real runtime binary so a contract case can read the envelope the
// gateway's subprocess executor wrote off the wire rather than off the
// producing struct.
const captureRuntimeScript = `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' "$line" >> "CAPTURE_PATH"
  printf '%s\n' '{"schemaVersion":1,"type":"response","output":[{"schemaVersion":1,"type":"text","inline":"ack"}]}'
done
`

// newCaptureRuntime writes the capture runtime into a temp dir and returns
// the binary path and the path it appends inbound frames to.
func newCaptureRuntime(t *testing.T) (binPath, capturePath string) {
	t.Helper()
	dir := t.TempDir()
	binPath = filepath.Join(dir, "capture-runtime")
	capturePath = filepath.Join(dir, "inbound.jsonl")
	script := strings.ReplaceAll(captureRuntimeScript, "CAPTURE_PATH", capturePath)
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write capture runtime: %v", err)
	}
	return binPath, capturePath
}

// spec: 28.5.3 (the outbound message envelope names the session it is
//
//	addressed to), 28.5.3 (the per-session identifier is populated on every
//	session-scoped frame, on every pod), 15.4
//
// diagnosis: the subprocess executor, which is the developer-loop and
//
//	conformance-harness producer of the same message envelope the pod
//	executor emits, wrote a frame carrying no per-session address. The
//	published schema lists the address in this frame's required set
//	(pinned by TestAdapterPopulatedFramesRequireSessionAddress), so an
//	envelope that omits it or carries it empty leaves the runtime it drives
//	with no session to echo, and every frame that runtime emits in response
//	unaddressed. The address rides a struct tag, so no compiler catches a
//	producer that never fills it and the check has to read the wire.
func TestSubprocessEnvelopeCarriesTheAddressedSession_spec_28_5_3(t *testing.T) {
	t.Parallel()
	binPath, capturePath := newCaptureRuntime(t)

	const sessionID = "sess-subprocess-address"
	ex := executor.NewSubprocessExecutor(executor.SubprocessOptions{
		BinPath:     binPath,
		SendTimeout: 10 * time.Second,
	})
	t.Cleanup(func() { _ = ex.Close(context.Background(), sessionID) })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := ex.Send(ctx, sessionID, []executor.Message{{Role: "user", Content: "hello"}}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	raw, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read captured frames: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("no envelope reached the runtime")
	}
	frame := lines[0]

	var probe map[string]any
	if err := json.Unmarshal([]byte(frame), &probe); err != nil {
		t.Fatalf("envelope is not JSON: %v (%s)", err, frame)
	}
	if _, ok := probe["sessionId"]; !ok {
		t.Errorf("envelope carries no sessionId key at all: %s", frame)
	}
	if probe["sessionId"] != sessionID {
		t.Errorf("envelope carries sessionId %v, want %q: %s", probe["sessionId"], sessionID, frame)
	}
}
