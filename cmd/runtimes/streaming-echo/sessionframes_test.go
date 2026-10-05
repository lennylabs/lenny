// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// decodeLines decodes each JSON Lines frame in raw.
func decodeLines(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var frames []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if line == "" {
			continue
		}
		var f map[string]any
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		frames = append(frames, f)
	}
	return frames
}

// spec: 28.5.3 (CH-MSGSOCK Outbound: session_started, Inbound: session_end),
// 4.7.10 (Runtime process lifetime)
//
// streaming-echo opens CH-RUNTIMEOPS, so it answers every session_start
// with a session_started echoing its sessionId and startId. It keeps no
// per-session context, so session_end is a no-op and the process keeps
// serving the next session and the next heartbeat.
func TestSessionStartIsAnsweredWithSessionStarted_spec_28_5_3(t *testing.T) {
	t.Setenv("LENNY_ADAPTER_MANIFEST", filepath.Join(t.TempDir(), "absent.json"))
	in := `{"type":"session_start","sessionId":"sess_a","startId":"st_1"}` + "\n" +
		`{"type":"session_end","sessionId":"sess_a"}` + "\n" +
		`{"type":"session_start","sessionId":"sess_b","startId":"st_2"}` + "\n" +
		`{"type":"heartbeat","ts":1}` + "\n"
	var out bytes.Buffer
	if err := run(strings.NewReader(in), &out, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	frames := decodeLines(t, out.String())
	if len(frames) != 3 {
		t.Fatalf("frames = %v, want two session_started and one heartbeat_ack", frames)
	}
	for i, want := range [][2]string{{"sess_a", "st_1"}, {"sess_b", "st_2"}} {
		f := frames[i]
		if f["type"] != "session_started" || f["sessionId"] != want[0] || f["startId"] != want[1] {
			t.Fatalf("frame %d = %v, want session_started for %s/%s", i, f, want[0], want[1])
		}
	}
	if frames[2]["type"] != "heartbeat_ack" {
		t.Fatalf("last frame = %v, want heartbeat_ack after session_end", frames[2])
	}
}

// spec: 15.4.6 (credential rotation handling), 28.5.3 (CH-RUNTIMEOPS
// credentials_rotated)
//
// streaming-echo re-reads the file credentials_rotated names before it
// acknowledges, and leaves a rotation whose file it cannot read
// unacknowledged rather than claiming a rebind it did not make.
func TestCredentialsRotatedRereadsTheNamedFile_spec_15_4_6(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte(`{"providers":[]}`), 0o600); err != nil {
		t.Fatalf("write credential file: %v", err)
	}
	frame := func(p string) []byte {
		b, _ := json.Marshal(map[string]any{"type": "credentials_rotated", "sessionId": "sess_a", "provider": "anthropic", "leaseId": "lease_1", "credentialsPath": p})
		return b
	}

	var out bytes.Buffer
	handleCredentialsRotated(frame(path), newWriter(&out), io.Discard)
	frames := decodeLines(t, out.String())
	if len(frames) != 1 || frames[0]["type"] != "credentials_acknowledged" || frames[0]["leaseId"] != "lease_1" {
		t.Fatalf("reply to a readable rotation = %v, want credentials_acknowledged for lease_1", frames)
	}

	out.Reset()
	var stderr bytes.Buffer
	absent := filepath.Join(t.TempDir(), "absent.json")
	handleCredentialsRotated(frame(absent), newWriter(&out), &stderr)
	if out.Len() != 0 {
		t.Fatalf("reply to a rotation naming an unreadable file = %q, want none", out.String())
	}
	if !strings.Contains(stderr.String(), absent) {
		t.Fatalf("diagnostic = %q, want one naming %s", stderr.String(), absent)
	}
}

// spec: 4.7.10 (Runtime process lifetime), 28.5.3 (CH-RUNTIMEOPS Messages)
//
// A frame typed terminate on CH-RUNTIMEOPS is an unknown frame: it ends
// neither the channel nor the process, so a checkpoint_request after it is
// still answered.
func TestRuntimeOpsTerminateFrameIsIgnored_spec_4_7_10(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "ops.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runRuntimeOps(ctx, sock, io.Discard)
	}()
	conn, err := l.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = conn.Close()
		<-done
	})
	r := bufio.NewReader(conn)
	send := func(v any) {
		b, _ := json.Marshal(v)
		if _, err := conn.Write(append(b, '\n')); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	recv := func() map[string]any {
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		line, err := r.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		return m
	}
	send(map[string]any{"type": "lifecycle_capabilities", "protocolVersion": "1.0", "capabilities": []string{"checkpoint"}})
	if f := recv(); f["type"] != "lifecycle_support" {
		t.Fatalf("handshake reply = %v, want lifecycle_support", f)
	}
	send(map[string]any{"type": "terminate", "reason": "done", "deadlineMs": 1000})
	send(map[string]any{"type": "checkpoint_request", "sessionId": "sess_a", "checkpointId": "ckpt_1", "deadlineMs": 1000})
	if f := recv(); f["type"] != "checkpoint_ready" || f["checkpointId"] != "ckpt_1" {
		t.Fatalf("reply after terminate = %v, want checkpoint_ready for ckpt_1", f)
	}
}
