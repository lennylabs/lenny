// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local ordering coverage for the session_start the
// developer-loop subprocess executor writes to a child it spawns on Send.
//
// Concurrent Sends on one fresh session race to spawn its child. Whichever
// Send spawns it, the child must read the session's session_start exactly
// once and ahead of every message, including the message of a Send that
// found the child already spawned, because a runtime that keeps
// per-session context answers a message for a session it has not opened
// with a session error.
//
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start), §17.4 (Local
// Development Mode).
package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
)

// sessionStartRecorderScript records every inbound frame and answers each
// message frame with one response.
const sessionStartRecorderScript = `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' "$line" >> "RECORD_PATH"
  case "$line" in
    *'"type":"message"'*)
      printf '%s\n' '{"type":"response","output":[{"type":"text","inline":"ack"}]}' ;;
  esac
done
`

// spec: 28.5.3 (CH-MSGSOCK session_start), 17.4 (Local Development Mode)
// diagnosis: under concurrent first Sends on a fresh session, the
//
//	developer-loop child read a message before the session's
//	session_start, read no session_start, or read more than one. The
//	executor publishes the child to concurrent Sends before the frame is on
//	the child's stdin, or writes the frame from a path that more than one
//	Send takes. Many fresh sessions share one executor, so a frame written
//	to another session's child, or ordering kept per executor rather than
//	per session, also fails here.
func TestConcurrentFirstSendsWriteSessionStartFirst_spec_28_5_3(t *testing.T) {
	const (
		sessions = 12
		senders  = 8
	)
	dir := t.TempDir()
	bin := filepath.Join(dir, "recorder")
	record := filepath.Join(dir, "record.jsonl")
	script := strings.ReplaceAll(sessionStartRecorderScript, "RECORD_PATH", record)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write recorder: %v", err)
	}
	ex := executor.NewSubprocessExecutor(executor.SubprocessOptions{BinPath: bin, SendTimeout: 20 * time.Second})
	ids := make([]string, sessions)
	var wg sync.WaitGroup
	for i := range ids {
		ids[i] = fmt.Sprintf("sess-concurrent-%d", i)
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			runConcurrentSends(t, ex, id, senders)
		}(ids[i])
	}
	wg.Wait()
	for _, id := range ids {
		if err := ex.Close(context.Background(), id); err != nil {
			t.Fatalf("Close(%s): %v", id, err)
		}
	}
	bySession := recordedFramesBySession(t, record)
	for _, id := range ids {
		assertSessionStartLeads(t, id, bySession[id], senders)
	}
}

// runConcurrentSends issues n Sends on sessionID at once.
func runConcurrentSends(t *testing.T, ex *executor.SubprocessExecutor, sessionID string, n int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := ex.Send(ctx, sessionID, []executor.Message{{Content: fmt.Sprintf("m%d", i)}})
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Send on %s: %v", sessionID, err)
		}
	}
}

// recordedFramesBySession groups the frames every child recorded, in the
// order each child read them, by the sessionId each frame carries. Each
// child appends one short line per write, which O_APPEND keeps whole.
func recordedFramesBySession(t *testing.T, record string) map[string][]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	out := map[string][]map[string]any{}
	for i, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var f map[string]any
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			t.Fatalf("recorded line %d is not JSON: %v (%q)", i, err, line)
		}
		id, _ := f["sessionId"].(string)
		out[id] = append(out[id], f)
	}
	return out
}

// assertSessionStartLeads checks that a session's child read exactly one
// session_start, first, followed by n messages.
func assertSessionStartLeads(t *testing.T, sessionID string, frames []map[string]any, n int) {
	t.Helper()
	if len(frames) != n+1 {
		t.Fatalf("%s: child read %d frames, want %d", sessionID, len(frames), n+1)
	}
	for i, f := range frames {
		wantType := "message"
		if i == 0 {
			wantType = "session_start"
		}
		if f["type"] != wantType {
			t.Errorf("%s: frame %d type = %v, want %s", sessionID, i, f["type"], wantType)
		}
	}
}
