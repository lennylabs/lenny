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

// echoLoop is a minimal §15.4.1 loop for the embedded-runtime tests: it
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
