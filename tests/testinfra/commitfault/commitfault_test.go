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

// slowCloseServer accepts one connection, reads it to EOF, holds the
// connection open for delay, writes a reply, and then closes it. The
// returned channel receives the instant the server closed.
func slowCloseServer(t *testing.T, delay time.Duration) (string, <-chan time.Time) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	closed := make(chan time.Time, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			closed <- time.Time{}
			return
		}
		_, _ = io.ReadAll(c)
		time.Sleep(delay)
		_, _ = c.Write([]byte("reply"))
		closed <- time.Now()
		_ = c.Close()
	}()
	return ln.Addr().String(), closed
}

// The lost-acknowledgement cases read the primary as soon as the code under
// test returns, so a CommitThenCut fault returns only once the server has
// processed the COMMIT and closed its side. A fault that returned at the
// half-close let that read race the server's processing of the COMMIT.
// spec: 12.3 (Postgres HA Requirements)
func TestCommitThenCutReturnsAfterTheServerCloses_spec_12_3(t *testing.T) {
	const delay = 200 * time.Millisecond
	addr, closed := slowCloseServer(t, delay)
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
	returned := time.Now()
	select {
	case at := <-closed:
		if returned.Before(at) {
			t.Errorf("Write returned at %v, before the server closed at %v", returned, at)
		}
	default:
		t.Fatal("Write returned before the server closed its side")
	}
	if _, err := c.Read(make([]byte, 8)); !errors.Is(err, ErrCut) {
		t.Errorf("Read after the cut = %v, want ErrCut", err)
	}
}

// pgconn starts a background reader when a write blocks for more than 15ms,
// and that reader can still be blocked in Read when the COMMIT goes out. It
// must not receive the COMMIT reply, or the client sees the commit succeed.
// spec: 12.3 (Postgres HA Requirements)
func TestCommitThenCutHidesTheReplyFromABlockedReader_spec_12_3(t *testing.T) {
	addr, closed := slowCloseServer(t, 50*time.Millisecond)
	inj := &Injector{}
	inj.Arm(CommitThenCut)
	c, err := inj.dial(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	type result struct {
		n   int
		err error
	}
	read := make(chan result, 1)
	go func() {
		n, err := c.Read(make([]byte, 64))
		read <- result{n, err}
	}()
	// Give the reader time to block in the underlying Read before the
	// COMMIT is written; the server sends nothing until it reads EOF.
	time.Sleep(20 * time.Millisecond)
	if _, err := c.Write(commitMsg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	<-closed
	select {
	case r := <-read:
		if !errors.Is(r.err, ErrCut) || r.n != 0 {
			t.Errorf("blocked Read = (%d, %v), want (0, ErrCut)", r.n, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked Read did not return after the server closed")
	}
}
