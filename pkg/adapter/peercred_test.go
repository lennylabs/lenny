// SPDX-License-Identifier: MIT

package adapter

import (
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc/test/bufconn"
)

func TestPeerCheckedListenerAccepts(t *testing.T) {
	inner := bufconn.Listen(1 << 20)
	lis := &peerCheckedListener{Listener: inner, check: func(net.Conn) error { return nil }}

	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := lis.Accept(); err == nil {
			accepted <- c
		}
	}()
	conn, err := inner.Dial()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	select {
	case c := <-accepted:
		_ = c.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("Accept did not return a connection the check accepted")
	}
}

func TestPeerCheckedListenerSkipsRejected(t *testing.T) {
	inner := bufconn.Listen(1 << 20)
	calls := 0
	lis := &peerCheckedListener{Listener: inner, check: func(net.Conn) error {
		calls++
		if calls == 1 {
			return errors.New("rejected")
		}
		return nil
	}}

	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := lis.Accept(); err == nil {
			accepted <- c
		}
	}()
	c1, err := inner.Dial()
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	defer c1.Close()
	c2, err := inner.Dial()
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	defer c2.Close()

	select {
	case c := <-accepted:
		_ = c.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("Accept did not return the connection after skipping a rejected one")
	}
	if calls != 2 {
		t.Errorf("check ran %d times, want 2 (one rejected, one accepted)", calls)
	}
}
