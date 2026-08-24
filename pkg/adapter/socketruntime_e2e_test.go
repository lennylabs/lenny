// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/runtimekit/echocore"
)

// embeddedEchoLoop is the real §28.5.3 echocore loop wired as an
// embedded-runtime RuntimeLoop — the same loop cmd/runtimes/echo-embedded
// runs in-process.
func embeddedEchoLoop(ctx context.Context, in io.Reader, out io.Writer) error {
	return echocore.Run(ctx, in, out, io.Discard)
}

// buildRuntime compiles a cmd/runtimes binary into a temp path once for
// the test and returns its path.
func buildRuntime(t *testing.T, pkg string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), filepath.Base(pkg))
	cmd := exec.Command("go", "build", "-o", bin, "./"+pkg)
	cmd.Dir = repoRootForTest()
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build %s: %v", pkg, err)
	}
	return bin
}

// TestSidecarSocketTransportDrivesTheEchoRuntime exercises the §4.7
// sidecar transport end to end: the SocketRuntimeProcess binds the
// abstract socket, spawns the real cmd/runtimes/echo binary with
// LENNY_ADAPTER_SOCKET set, and round-trips a §28.5.3 message/response
// over the socket. echo runs the same echocore loop the sidecar pod
// runs; this proves the socket transport without a Kubernetes cluster.
func TestSidecarSocketTransportDrivesTheEchoRuntime(t *testing.T) {
	echoBin := buildRuntime(t, "cmd/runtimes/echo")

	sp, err := adapter.NewSocketRuntimeProcess(runtimeSocketAddr(t))
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	// SpawnPath makes Start exec the runtime — the developer-loop path
	// that exercises the same transport one process can drive.
	sp.SpawnPath = echoBin
	sp.AcceptTimeout = 10 * time.Second
	defer sp.Close(context.Background(), "s1")

	if err := sp.Start(context.Background(), "s1"); err != nil {
		t.Fatalf("Start (spawn + accept): %v", err)
	}

	out, err := sp.Output(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}

	msg := `{"schemaVersion":1,"type":"message","id":"msg_1",` +
		`"from":{"kind":"client","id":"c"},"input":[{"type":"text","inline":"ping"}]}`
	if err := sp.WriteEnvelope("s1", []byte(msg)); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	select {
	case frame := <-out:
		var resp struct {
			Type   string `json:"type"`
			Output []struct {
				Inline string `json:"inline"`
			} `json:"output"`
		}
		if err := json.Unmarshal(frame, &resp); err != nil {
			t.Fatalf("decode response frame %q: %v", frame, err)
		}
		if resp.Type != "response" {
			t.Errorf("frame type = %q, want response", resp.Type)
		}
		if len(resp.Output) != 1 || !strings.Contains(resp.Output[0].Inline, "ping") {
			t.Errorf("response output = %+v, want the echoed input", resp.Output)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the echo runtime produced no response over the socket transport")
	}
}

// TestEmbeddedRuntimeDrivesTheEchoLoop exercises the §4.7 embedded
// model end to end: an adapter.InProcessRuntime runs the echocore loop
// in-process and round-trips a §28.5.3 message/response over the
// in-memory pipe. This is the same loop the embedded pod container
// runs.
func TestEmbeddedRuntimeDrivesTheEchoLoop(t *testing.T) {
	rt := adapter.NewInProcessRuntime(embeddedEchoLoop)
	if err := rt.Start(context.Background(), "s1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer rt.Close(context.Background(), "s1")

	out, err := rt.Output(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}

	msg := `{"schemaVersion":1,"type":"message","id":"msg_1",` +
		`"from":{"kind":"client","id":"c"},"input":[{"type":"text","inline":"pong"}]}`
	if err := rt.WriteEnvelope("s1", []byte(msg)); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	select {
	case frame := <-out:
		var resp struct {
			Type   string `json:"type"`
			Output []struct {
				Inline string `json:"inline"`
			} `json:"output"`
		}
		if err := json.Unmarshal(frame, &resp); err != nil {
			t.Fatalf("decode response frame %q: %v", frame, err)
		}
		if resp.Type != "response" {
			t.Errorf("frame type = %q, want response", resp.Type)
		}
		if len(resp.Output) != 1 || !strings.Contains(resp.Output[0].Inline, "pong") {
			t.Errorf("response output = %+v, want the echoed input", resp.Output)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the embedded echo loop produced no response")
	}
}

// requireSocketEcho round-trips one §28.5.3 message frame for sessionID
// over the socket transport and fails the test unless the echoed response
// carries the sent text.
func requireSocketEcho(t *testing.T, sp *adapter.SocketRuntimeProcess, sessionID, text string) {
	t.Helper()
	out, err := sp.Output(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Output for %s: %v", sessionID, err)
	}
	msg := `{"schemaVersion":1,"type":"message","id":"msg_` + text + `",` +
		`"from":{"kind":"client","id":"c"},"input":[{"type":"text","inline":"` + text + `"}]}`
	if err := sp.WriteEnvelope(sessionID, []byte(msg)); err != nil {
		t.Fatalf("WriteEnvelope for %s: %v", sessionID, err)
	}
	for {
		select {
		case frame, ok := <-out:
			if !ok {
				t.Fatalf("the runtime's stream closed before %s saw its response", sessionID)
			}
			var resp struct {
				Type   string `json:"type"`
				Output []struct {
					Inline string `json:"inline"`
				} `json:"output"`
			}
			if err := json.Unmarshal(frame, &resp); err != nil {
				t.Fatalf("decode frame %q: %v", frame, err)
			}
			if resp.Type != "response" {
				continue
			}
			if len(resp.Output) != 1 || !strings.Contains(resp.Output[0].Inline, text) {
				t.Fatalf("response output for %s = %+v, want the echoed %q", sessionID, resp.Output, text)
			}
			return
		case <-time.After(15 * time.Second):
			t.Fatalf("no response frame for %s over the socket transport", sessionID)
		}
	}
}

// TestSpawnedRuntimeIsSignalledOnClose pins the developer-loop half of the
// §4.7 socket transport: a runtime the adapter spawned is the adapter's own
// child, so Close signals it rather than leaving it to notice the closed
// connection and run on to the SIGKILL at the grace deadline.
//
// spec: §4.7 (sidecar deployment model), §15.4 (SIGTERM, then SIGKILL at
// the grace deadline).
func TestSpawnedRuntimeIsSignalledOnClose(t *testing.T) {
	echoBin := buildRuntime(t, "cmd/runtimes/echo")

	sp, err := adapter.NewSocketRuntimeProcess(runtimeSocketAddr(t))
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	sp.SpawnPath = echoBin
	sp.AcceptTimeout = 10 * time.Second

	if err := sp.Start(context.Background(), "sess-a"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	requireSocketEcho(t, sp, "sess-a", "spawned")

	// The grace window is the §11.4 10s default; a signalled child exits
	// far inside it, and a child that misses the closed connection burns
	// the whole window before the SIGKILL.
	start := time.Now()
	if err := sp.Close(context.Background(), "sess-a"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Close took %s; the spawned runtime was not signalled and ran to the grace deadline", elapsed)
	}
}
