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

// embeddedEchoLoop is the real §15.4.1 echocore loop wired as an
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
// LENNY_ADAPTER_SOCKET set, and round-trips a §15.4.1 message/response
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
// in-process and round-trips a §15.4.1 message/response over the
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
