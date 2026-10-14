// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/linefanout"
)

// tapRecorder collects the lines an activity tap receives.
type tapRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (r *tapRecorder) tap(line []byte) {
	r.mu.Lock()
	r.lines = append(r.lines, string(line))
	r.mu.Unlock()
}

// waitLine waits until the recorder holds a line containing want.
func (r *tapRecorder) waitLine(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, l := range r.lines {
			if strings.Contains(l, want) {
				r.mu.Unlock()
				return
			}
		}
		r.mu.Unlock()
		time.Sleep(2 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Fatalf("the tap never received a line containing %q; got %q", want, r.lines)
}

// spec: §4.7.1 (Role and Gateway RPC Contract), §28.5.3 (CH-MSGSOCK)
// The line hub hands every line to its tap with no subscriber registered.
func TestLineHubTapReadsWithoutSubscribers_spec_4_7_1(t *testing.T) {
	rec := &tapRecorder{}
	hub := linefanout.New()
	hub.Tap(rec.tap)
	pr, pw := io.Pipe()
	hub.Serve(pr, 1<<20, nil)
	if _, err := pw.Write([]byte("{\"type\":\"response\"}\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	rec.waitLine(t, `"response"`)
	_ = pw.Close()
}

// spec: §4.7.1 (Role and Gateway RPC Contract), §28.5.3 (CH-MSGSOCK)
// The socket transport delivers every runtime frame to the activity tap with
// no Output subscriber.
func TestSocketRuntimeTapReadsWithoutOutput_spec_4_7_1(t *testing.T) {
	socket := runtimeSocketAddr(t)
	sp, err := newSocketRuntime(t, socket, adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid())})
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	rec := &tapRecorder{}
	sp.TapOutput(rec.tap)
	connCh := make(chan interface {
		Write([]byte) (int, error)
		Close() error
	}, 1)
	go func() { connCh <- dialRuntimeSocket(t, sp.SocketPath()) }()
	if err := sp.Start(context.Background(), "s1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	conn := <-connCh
	defer conn.Close()
	if _, err := conn.Write([]byte(`{"type":"heartbeat_ack","sessionId":"s1","busy":true}` + "\n")); err != nil {
		t.Fatalf("runtime write: %v", err)
	}
	rec.waitLine(t, `"heartbeat_ack"`)
}

// spec: §4.7.1 (Role and Gateway RPC Contract), §28.5.3 (CH-MSGSOCK)
// The in-process transport delivers every frame to the tap with no Output
// subscriber, across a restart that creates a new hub.
func TestInProcessRuntimeTapReadsAcrossRestarts_spec_4_7_1(t *testing.T) {
	loop := func(_ context.Context, in io.Reader, out io.Writer) error {
		_, _ = out.Write([]byte("{\"type\":\"status\",\"state\":\"ready\"}\n"))
		_, err := io.Copy(io.Discard, in)
		return err
	}
	rt := adapter.NewInProcessRuntime(loop)
	rec := &tapRecorder{}
	rt.TapOutput(rec.tap)
	for i, id := range []string{"s1", "s2"} {
		if err := rt.Start(context.Background(), id); err != nil {
			t.Fatalf("Start(%s): %v", id, err)
		}
		rec.mu.Lock()
		before := len(rec.lines)
		rec.mu.Unlock()
		deadline := time.Now().Add(5 * time.Second)
		for {
			rec.mu.Lock()
			n := len(rec.lines)
			rec.mu.Unlock()
			if n > before || n > i {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("start %d: the tap received no frame", i)
			}
			time.Sleep(2 * time.Millisecond)
		}
		if err := rt.Close(context.Background(), id); err != nil {
			t.Fatalf("Close(%s): %v", id, err)
		}
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.lines) < 2 {
		t.Fatalf("tap lines = %q, want one per start", rec.lines)
	}
}

// spec: §4.7.1 (Role and Gateway RPC Contract)
// The MCP transport hands each response frame it delivers to the tap,
// whether or not Output was called.
func TestMCPRuntimeTapReadsEachResponse_spec_4_7_1(t *testing.T) {
	rt := &adapter.MCPRuntime{Command: mcpReferenceBinary}
	rec := &tapRecorder{}
	rt.TapOutput(rec.tap)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := rt.Start(ctx, "sess-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background(), "sess-1") })
	env := messageEnvelope("msg-1", "echo", map[string]any{"text": "hi"})
	if err := rt.WriteEnvelope("sess-1", env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	rec.waitLine(t, `"msg-1"`)
}
