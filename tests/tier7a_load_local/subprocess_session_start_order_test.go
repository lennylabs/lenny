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

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_start), 17.4 (Local
// Development Mode)
//
// diagnosis: under concurrent Sends on one fresh session, the developer-loop
//
//	child read a message before the session's session_start, read no
//	session_start, or read more than one. The executor publishes the child
//	to concurrent Sends before the frame is on the child's stdin, or writes
//	the frame from a path that more than one Send takes.
func TestSubprocessSessionStartPrecedesConcurrentSends_spec_28_5_3(t *testing.T) {
	const (
		rounds  = 10
		senders = 16
	)
	dir := t.TempDir()
	for round := 0; round < rounds; round++ {
		bin := filepath.Join(dir, fmt.Sprintf("recorder-%d", round))
		record := filepath.Join(dir, fmt.Sprintf("record-%d.jsonl", round))
		script := strings.ReplaceAll(sessionStartRecorderScript, "RECORD_PATH", record)
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			t.Fatalf("write recorder: %v", err)
		}
		sessionID := fmt.Sprintf("sess-concurrent-%d", round)
		ex := executor.NewSubprocessExecutor(executor.SubprocessOptions{BinPath: bin, SendTimeout: 20 * time.Second})
		runConcurrentSends(t, ex, sessionID, senders)
		if err := ex.Close(context.Background(), sessionID); err != nil {
			t.Fatalf("round %d: Close: %v", round, err)
		}
		assertSessionStartLeads(t, round, record, sessionID, senders)
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
			t.Fatalf("Send on %s: %v", sessionID, err)
		}
	}
}

// assertSessionStartLeads checks that the child read exactly one
// session_start, first, followed by n messages.
func assertSessionStartLeads(t *testing.T, round int, record, sessionID string, n int) {
	t.Helper()
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("round %d: read record: %v", round, err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != n+1 {
		t.Fatalf("round %d: child read %d frames, want %d", round, len(lines), n+1)
	}
	for i, line := range lines {
		var f map[string]any
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			t.Fatalf("round %d: frame %d is not JSON: %v", round, i, err)
		}
		wantType := "message"
		if i == 0 {
			wantType = "session_start"
		}
		if f["type"] != wantType {
			t.Errorf("round %d: frame %d type = %v, want %s", round, i, f["type"], wantType)
		}
		if f["sessionId"] != sessionID {
			t.Errorf("round %d: frame %d sessionId = %v, want %s", round, i, f["sessionId"], sessionID)
		}
	}
}
