// SPDX-License-Identifier: MIT

package adapter_test

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
)

// echoLoop is a minimal §28.5.3 loop for the embedded-runtime tests: it
// echoes every newline-delimited inbound frame back on out and returns
// when in reaches EOF.
func echoLoop(_ context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		if _, err := out.Write(append([]byte("echo:"), scanner.Bytes()...)); err != nil {
			return err
		}
		if _, err := out.Write([]byte("\n")); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func TestInProcessRuntimeBridgesTheEmbeddedLoop(t *testing.T) {
	rt := adapter.NewInProcessRuntime(echoLoop)
	if err := rt.Start(context.Background(), "s1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer rt.Close(context.Background(), "s1")

	out, err := rt.Output(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if err := rt.WriteEnvelope("s1", []byte(`{"type":"message"}`)); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	select {
	case got := <-out:
		if string(got) != `echo:{"type":"message"}` {
			t.Errorf("embedded loop produced %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the embedded loop produced no output frame")
	}
}

func TestInProcessRuntimeCloseEndsTheLoop(t *testing.T) {
	rt := adapter.NewInProcessRuntime(echoLoop)
	if err := rt.Start(context.Background(), "s1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	out, err := rt.Output(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	// §15.4: Close is the clean-exit signal; the loop returns and the
	// output channel closes.
	if err := rt.Close(context.Background(), "s1"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case _, ok := <-out:
		if ok {
			// Drain any buffered frame, then confirm the channel closes.
			for range out { //nolint:revive // drain
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the embedded loop's output channel did not close after Close")
	}
}

func TestInProcessRuntimeInterruptEndsTheLoop(t *testing.T) {
	rt := adapter.NewInProcessRuntime(echoLoop)
	if err := rt.Start(context.Background(), "s1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer rt.Close(context.Background(), "s1")
	out, err := rt.Output(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if err := rt.Interrupt(context.Background(), "s1", true); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	select {
	case _, ok := <-out:
		if ok {
			for range out { //nolint:revive // drain
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Interrupt did not end the embedded loop")
	}
}

func TestInProcessRuntimeRejectsWrongSession(t *testing.T) {
	rt := adapter.NewInProcessRuntime(echoLoop)
	if err := rt.Start(context.Background(), "s1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer rt.Close(context.Background(), "s1")

	if err := rt.WriteEnvelope("other", []byte(`{}`)); err == nil {
		t.Error("WriteEnvelope must reject an unbound session")
	}
	if _, err := rt.Output(context.Background(), "other"); err == nil {
		t.Error("Output must reject an unbound session")
	}
	if err := rt.Start(context.Background(), "other"); err == nil {
		t.Error("Start must reject a second concurrent session")
	}
	if !strings.Contains(mustStartErr(rt), "already bound") {
		t.Error("the second-session error should explain the binding")
	}
}

func mustStartErr(rt *adapter.InProcessRuntime) string {
	err := rt.Start(context.Background(), "another")
	if err == nil {
		return ""
	}
	return err.Error()
}

// spec: 28.5.3 (CH-MSGSOCK Outbound: session_started), 28.5.3 (CH-MSGSOCK)
// The embedded transport's Output fans the loop's output out to every live
// subscriber. A start's session_started wait subscribes beside the Attach
// stream, reads the acknowledgement, and cancels; the cancelled
// subscription consumes nothing afterwards, so the first response written
// after the wait reaches the Attach subscription. Before the fan-out, each
// Output scanned the shared pipe itself, and the cancelled wait's reader
// went on consuming frames meant for Attach.
func TestInProcessRuntimeOutputFansOutPastACancelledWait_spec_28_5_3(t *testing.T) {
	rt := adapter.NewInProcessRuntime(echoLoop)
	if err := rt.Start(context.Background(), "s1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer rt.Close(context.Background(), "s1")
	assertFanOutPastCancelledWait(t, rt, "s1")
}

// assertFanOutPastCancelledWait drives one transport through the wait's
// subscribe, read, and cancel, then asserts the Attach subscription
// receives both the acknowledgement and the response written after the
// wait, in order. The transport echoes each frame it is written.
func assertFanOutPastCancelledWait(t *testing.T, rt adapter.RuntimeProcess, sessionID string) {
	t.Helper()
	attach, err := rt.Output(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Attach Output: %v", err)
	}
	waitCtx, cancelWait := context.WithCancel(context.Background())
	wait, err := rt.Output(waitCtx, sessionID)
	if err != nil {
		t.Fatalf("wait Output: %v", err)
	}
	if err := rt.WriteEnvelope(sessionID, []byte(`{"type":"session_started"}`)); err != nil {
		t.Fatalf("write acknowledgement: %v", err)
	}
	if got := recvLine(t, wait); !strings.Contains(got, "session_started") {
		t.Fatalf("wait read %q, want the acknowledgement", got)
	}
	cancelWait()
	if err := rt.WriteEnvelope(sessionID, []byte(`{"type":"response"}`)); err != nil {
		t.Fatalf("write response: %v", err)
	}
	if got := recvLine(t, attach); !strings.Contains(got, "session_started") {
		t.Fatalf("Attach read %q first, want the acknowledgement", got)
	}
	if got := recvLine(t, attach); !strings.Contains(got, `"response"`) {
		t.Fatalf("Attach read %q, want the response written after the wait", got)
	}
}

// recvLine receives one frame or fails the test after two seconds.
func recvLine(t *testing.T, ch <-chan []byte) string {
	t.Helper()
	select {
	case b, ok := <-ch:
		if !ok {
			t.Fatal("the subscription closed before the frame")
		}
		return string(b)
	case <-time.After(2 * time.Second):
		t.Fatal("no frame within 2s")
	}
	return ""
}
