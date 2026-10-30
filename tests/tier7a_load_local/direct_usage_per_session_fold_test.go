// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the direct-mode token fold on
// a pod whose one shared runtime process serves two sessions.
//
// Each llm_request_completed frame on CH-RUNTIMEOPS names the session it
// concerns, and the adapter folds the frame's counts into that session's
// cumulative total. The case interleaves frames for two co-tenant sessions
// on the pod's single CH-RUNTIMEOPS connection while ReportUsage pulls both
// sessions' deltas concurrently, and asserts that each session's pulled
// deltas sum to exactly the counts its own frames carried.
//
// spec: §28.5.3 (CH-RUNTIMEOPS, Messages), §11.2 (direct-mode usage).
package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/runtimenonce"
)

// usageFrame is the runtime side of the CH-RUNTIMEOPS frames the case
// writes: the handshake reply and llm_request_completed.
type usageFrame struct {
	Type         string   `json:"type"`
	SessionID    string   `json:"sessionId,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	RequestID    string   `json:"requestId,omitempty"`
	Provider     string   `json:"provider,omitempty"`
	Status       string   `json:"status,omitempty"`
	InputTokens  int64    `json:"inputTokens,omitempty"`
	OutputTokens int64    `json:"outputTokens,omitempty"`
}

// startUsageRuntimeOps runs a CH-RUNTIMEOPS on a short socket path, wires
// the production direct-mode usage path onto s, and dials it as the
// runtime, completing the handshake. It returns the runtime's encoder.
func startUsageRuntimeOps(t *testing.T, s *adapter.Server) *json.Encoder {
	t.Helper()
	dir, err := os.MkdirTemp("", "lenny-fold-*")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "lc.sock")
	// The listener compares the runtime's nonce line with the manifest the
	// Server's starts published in ManifestDir. spec: 4.7.11 (Runtime
	// connection handshake).
	lc, err := adapter.NewRuntimeOps(sock, adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid())}, adapter.PublishedManifestNonce(s.ManifestDir))
	if err != nil {
		t.Fatalf("NewRuntimeOps: %v", err)
	}
	s.Lifecycle = lc
	adapter.WireDirectModeUsage(s, lc)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- lc.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = lc.Close()
		<-done
	})

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial CH-RUNTIMEOPS: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	nonce, err := runtimenonce.ReadNonce(filepath.Join(s.ManifestDir, adapter.ManifestFilename))
	if err != nil {
		t.Fatalf("read the published nonce: %v", err)
	}
	if err := runtimenonce.Write(conn, nonce); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(conn)
	var caps usageFrame
	if err := dec.Decode(&caps); err != nil || caps.Type != "lifecycle_capabilities" {
		t.Fatalf("handshake read = (%+v, %v), want lifecycle_capabilities", caps, err)
	}
	enc := json.NewEncoder(conn)
	if err := enc.Encode(usageFrame{Type: "lifecycle_support", Capabilities: caps.Capabilities}); err != nil {
		t.Fatalf("write lifecycle_support: %v", err)
	}
	if !lc.WaitHandshake(context.Background(), 5*time.Second) {
		t.Fatal("CH-RUNTIMEOPS handshake did not complete")
	}
	return enc
}

// pullTotals issues delta ReportUsage reads for sessionID until stop
// closes, then one final read, and returns the summed input and output
// tokens.
func pullTotals(t *testing.T, s *adapter.Server, sessionID string, stop <-chan struct{}) (int64, int64) {
	var in, out int64
	read := func() {
		resp, err := s.ReportUsage(context.Background(), &adapterv1.ReportUsageRequest{
			SessionId: &adapterv1.SessionId{Value: sessionID},
		})
		if err != nil {
			t.Errorf("ReportUsage(%s): %v", sessionID, err)
			return
		}
		in += resp.GetInputTokens()
		out += resp.GetOutputTokens()
	}
	for {
		select {
		case <-stop:
			read()
			return in, out
		default:
			read()
		}
	}
}

// spec: 28.5.3 (CH-RUNTIMEOPS, Messages), 11.2 (direct-mode usage)
// diagnosis: on a pod serving two sessions, a direct-mode token count was
// folded into a session other than the one its llm_request_completed frame
// named, or was lost or double-counted while ReportUsage pulled
// concurrently. Either charges one session's §11.2 budget with another's
// tokens or lets a session's spend escape its budget. Confirm the token
// sink keys the meter by the frame's sessionId and the meter's fold and
// read share its lock.
func TestDirectUsageFoldsEachFrameIntoItsNamedSessionUnderConcurrentPulls_spec_28_5_3(t *testing.T) {
	base := t.TempDir()
	s := adapter.New("test")
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	s.ManifestDir = t.TempDir()
	s.Runtime = newGatedRuntime()
	startDrainSession(t, s, "alice")
	startDrainSession(t, s, "bob")
	enc := startUsageRuntimeOps(t, s)

	const frames = 400
	var wantAlice, wantBob [2]int64
	stop := make(chan struct{})
	type totals struct{ in, out int64 }
	var aliceGot, bobGot totals
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); aliceGot.in, aliceGot.out = pullTotals(t, s, "alice", stop) }()
	go func() { defer wg.Done(); bobGot.in, bobGot.out = pullTotals(t, s, "bob", stop) }()

	for i := 0; i < frames; i++ {
		session, in, out := "alice", int64(3), int64(1)
		if i%2 == 1 {
			session, in, out = "bob", 5, 2
		}
		if err := enc.Encode(usageFrame{
			Type: "llm_request_completed", SessionID: session, RequestID: "r",
			Provider: "anthropic", Status: "ok", InputTokens: in, OutputTokens: out,
		}); err != nil {
			t.Fatalf("write frame %d: %v", i, err)
		}
		if session == "alice" {
			wantAlice[0] += in
			wantAlice[1] += out
		} else {
			wantBob[0] += in
			wantBob[1] += out
		}
	}

	close(stop)
	wg.Wait()

	// The pullers stopped after one final read each, and the read loop may
	// fold frames after that read, so drain each session until its total
	// reaches what its frames carried. A count folded into the wrong
	// session leaves one total short, which never reaches the bound, and
	// the other over it.
	for _, p := range []struct {
		name string
		got  *totals
	}{{"alice", &aliceGot}, {"bob", &bobGot}} {
		end := time.Now().Add(10 * time.Second)
		want := wantAlice
		if p.name == "bob" {
			want = wantBob
		}
		for p.got.in < want[0] && time.Now().Before(end) {
			resp, err := s.ReportUsage(context.Background(), &adapterv1.ReportUsageRequest{
				SessionId: &adapterv1.SessionId{Value: p.name},
			})
			if err != nil {
				t.Fatalf("ReportUsage(%s): %v", p.name, err)
			}
			p.got.in += resp.GetInputTokens()
			p.got.out += resp.GetOutputTokens()
			time.Sleep(5 * time.Millisecond)
		}
		if p.got.in != want[0] || p.got.out != want[1] {
			t.Errorf("%s pulled (%d,%d), want (%d,%d) from the frames that named it",
				p.name, p.got.in, p.got.out, want[0], want[1])
		}
	}
}
