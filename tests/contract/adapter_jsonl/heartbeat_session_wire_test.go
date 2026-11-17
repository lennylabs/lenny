//go:build contract

// SPDX-License-Identifier: MIT

package adapter_jsonl_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/runtimenonce"
)

// spec: 28.5.3 (CH-MSGSOCK)
// diagnosis: the heartbeat the adapter itself writes to a socket runtime
// for a started session did not name that session in sessionId, or did not
// validate against the published JSONL schema. A multi-session runtime then
// cannot tell which session the probe is for and cannot attribute its
// busy report, so the adapter's per-session heartbeat monitor never sees a
// busy ack for the session it probes.
func TestAdapterHeartbeatOnSocketRuntimeNamesTheSession_spec_28_5_3(t *testing.T) {
	addr := runtimeSocketAddress(t)
	manifest := runtimenonce.Publish(t, nil)
	sp, err := adapter.NewSocketRuntimeProcess(addr, adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid())}, adapter.PublishedManifestNonce(manifest.Dir))
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	sp.AcceptTimeout = 10 * time.Second
	tap, _ := dialEchoRuntime(t, addr, manifest.Nonce)

	s := adapter.New("heartbeat-session")
	s.WorkspaceBase = t.TempDir()
	s.Runtime = sp
	s.HeartbeatInterval = 30 * time.Millisecond
	s.HeartbeatAckTimeout = 10 * time.Second
	// NewGRPCServer installs the activity tap, without which the adapter
	// runs no heartbeat monitor.
	gs := adapter.NewGRPCServer(s)
	t.Cleanup(gs.Stop)
	const id = "sess-hb"
	ctx := context.Background()
	if _, err := s.StartSession(ctx, &adapterv1.StartSessionRequest{SessionId: &adapterv1.SessionId{Value: id}}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: id}, UnconditionalTeardown: true,
		})
	})

	schema := compileJSONL(t)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if line, ok := firstHeartbeat(tap); ok {
			var f map[string]any
			if err := json.Unmarshal(line, &f); err != nil {
				t.Fatalf("decode %s: %v", line, err)
			}
			if f["sessionId"] != id {
				t.Fatalf("adapter heartbeat = %s, want sessionId %q", line, id)
			}
			if err := validateFrame(t, schema, string(line)); err != nil {
				t.Fatalf("adapter heartbeat %s fails the JSONL schema: %v", line, err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the runtime read no heartbeat within 10s; it read %v", tap.frames(t))
}

// firstHeartbeat returns the first complete heartbeat line the runtime read.
func firstHeartbeat(w *wireTap) ([]byte, bool) {
	w.mu.Lock()
	raw := append([]byte(nil), w.buf.Bytes()...)
	w.mu.Unlock()
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		var f struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(sc.Bytes(), &f) == nil && f.Type == "heartbeat" {
			return append([]byte(nil), sc.Bytes()...), true
		}
	}
	return nil, false
}
