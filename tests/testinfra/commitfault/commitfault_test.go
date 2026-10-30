// SPDX-License-Identifier: MIT

package commitfault

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// echoServer accepts one connection and returns everything it receives
// until the client half-closes or closes it.
func echoServer(t *testing.T) (string, <-chan []byte) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- nil
			return
		}
		defer c.Close()
		b, _ := io.ReadAll(c)
		got <- b
	}()
	return ln.Addr().String(), got
}

func recv(t *testing.T, got <-chan []byte) []byte {
	t.Helper()
	select {
	case b := <-got:
		return b
	case <-time.After(5 * time.Second):
		t.Fatal("server did not finish reading")
		return nil
	}
}

// spec: 12.3 (Postgres HA Requirements)
func TestCommitThenCutDeliversTheCommitAndFailsTheReply_spec_12_3(t *testing.T) {
	addr, got := echoServer(t)
	inj := &Injector{}
	inj.Arm(CommitThenCut)
	c, err := inj.dial(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if _, err := c.Write(commitMsg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if b := recv(t, got); string(b) != string(commitMsg) {
		t.Errorf("server received %q, want the COMMIT message", b)
	}
	if _, err := c.Read(make([]byte, 8)); !errors.Is(err, ErrCut) {
		t.Errorf("Read after the cut = %v, want ErrCut", err)
	}
	if !inj.Fired() || !inj.Saw("commit") {
		t.Errorf("Fired=%v Saw(commit)=%v, want both true", inj.Fired(), inj.Saw("commit"))
	}
}

// spec: 12.3 (Postgres HA Requirements)
func TestCutBeforeCommitWithholdsTheCommit_spec_12_3(t *testing.T) {
	addr, got := echoServer(t)
	inj := &Injector{}
	inj.Arm(CutBeforeCommit)
	c, err := inj.dial(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := c.Write([]byte("before")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := c.Write(commitMsg); !errors.Is(err, ErrCut) {
		t.Fatalf("Write(COMMIT) = %v, want ErrCut", err)
	}
	if b := recv(t, got); string(b) != "before" {
		t.Errorf("server received %q, want only the bytes before the COMMIT", b)
	}
}

// spec: 12.3 (Postgres HA Requirements)
func TestRedialHookRunsOnlyAfterAFault_spec_12_3(t *testing.T) {
	addr, _ := echoServer(t)
	inj := &Injector{}
	refused := errors.New("refused")
	inj.OnRedial(func(context.Context) error { return refused })
	c, err := inj.dial(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatalf("dial before any fault: %v", err)
	}
	inj.Arm(CutBeforeCommit)
	_, _ = c.Write(commitMsg)
	if _, err := inj.dial(context.Background(), "tcp", addr); !errors.Is(err, refused) {
		t.Errorf("dial after the fault = %v, want the hook's error", err)
	}
	inj.Reset()
	if inj.Saw("commit") {
		t.Error("Reset kept a recorded write")
	}
	inj.Arm(Pass)
	if _, err := inj.dial(context.Background(), "tcp", addr); err != nil && errors.Is(err, refused) {
		t.Error("Arm did not clear the fired state")
	}
}
