// SPDX-License-Identifier: MIT

package executor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// failingWriter is a stdin whose every write fails.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_start), 17.4 (Local
// Development Mode)
//
// The developer-loop session_start is one newline-terminated JSON line
// with the frame's required members only.
func TestWriteDevLoopSessionStartFrame_spec_28_5_3(t *testing.T) {
	var buf bytes.Buffer
	if err := writeDevLoopSessionStart(&buf, "sess-1"); err != nil {
		t.Fatalf("writeDevLoopSessionStart: %v", err)
	}
	want := `{"type":"session_start","sessionId":"sess-1","startId":"1"}` + "\n"
	if buf.String() != want {
		t.Errorf("wrote %q, want %q", buf.String(), want)
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_start)
//
// A failed session_start write is returned with the session named, so
// session reaps the child instead of publishing a session the runtime was
// never opened on.
func TestWriteDevLoopSessionStartWriteFailure_spec_28_5_3(t *testing.T) {
	err := writeDevLoopSessionStart(failingWriter{}, "sess-2")
	if err == nil {
		t.Fatal("a failed write returned no error")
	}
	if !strings.Contains(err.Error(), "sess-2") || !strings.Contains(err.Error(), "broken pipe") {
		t.Errorf("error %q does not name the session and wrap the cause", err)
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_start rule 1), 17.4 (Local
// Development Mode)
//
// A child whose session_start write fails is reaped and never published, so
// no message reaches a session the runtime was not opened on, and the next
// Send spawns a fresh child that is opened.
func TestSendSpawnWithFailedSessionStartIsNotPublished_spec_28_5_3(t *testing.T) {
	e := NewSubprocessExecutor(SubprocessOptions{BinPath: "/bin/cat"})
	e.writeSessionStart = func(io.Writer, string) error { return errors.New("broken pipe") }
	if _, err := e.session("sess-fail", spawnedBySend); err == nil {
		t.Fatal("session returned no error for a failed session_start write")
	}
	e.mu.Lock()
	_, published := e.procs["sess-fail"]
	e.mu.Unlock()
	if published {
		t.Fatal("a child whose session_start write failed was published")
	}

	e.writeSessionStart = writeDevLoopSessionStart
	sess, err := e.session("sess-fail", spawnedBySend)
	if err != nil {
		t.Fatalf("respawn after a failed open: %v", err)
	}
	if !sess.stdout.Scan() {
		t.Fatalf("respawned child echoed nothing: %v", sess.stdout.Err())
	}
	if got := sess.stdout.Text(); !strings.Contains(got, `"type":"session_start"`) {
		t.Errorf("respawned child read %q first, want its session_start", got)
	}
	if err := e.Close(context.Background(), "sess-fail"); err != nil {
		t.Errorf("Close: %v", err)
	}
}
