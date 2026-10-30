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

// spec: 4.7.11 (Separate UIDs and connection authentication)
func TestPeerCheckedListenerReportsEachRefusal_spec_4_7_11(t *testing.T) {
	inner := bufconn.Listen(1 << 20)
	refusal := &PeerUIDMismatchError{Peer: 4242, Expected: 1001}
	calls := 0
	var reported []error
	lis := &peerCheckedListener{
		Listener: inner,
		check: func(net.Conn) error {
			calls++
			if calls == 1 {
				return refusal
			}
			return nil
		},
		onReject: func(err error) { reported = append(reported, err) },
	}

	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := lis.Accept(); err == nil {
			accepted <- c
		}
	}()
	refused, err := inner.Dial()
	if err != nil {
		t.Fatalf("dial refused peer: %v", err)
	}
	defer refused.Close()
	admitted, err := inner.Dial()
	if err != nil {
		t.Fatalf("dial admitted peer: %v", err)
	}
	defer admitted.Close()

	select {
	case c := <-accepted:
		_ = c.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("Accept did not return the admitted connection after the refusal")
	}
	if len(reported) != 1 || reported[0] != refusal {
		t.Fatalf("onReject saw %v, want exactly the one refusal", reported)
	}
	var mismatch *PeerUIDMismatchError
	if !errors.As(reported[0], &mismatch) || mismatch.Peer != 4242 || mismatch.Expected != 1001 {
		t.Fatalf("refusal %v does not carry the peer and expected UIDs", reported[0])
	}
	if got := mismatch.Error(); got != "adapter: peer uid 4242 does not match the runtime uid 1001" {
		t.Errorf("Error() = %q", got)
	}
}
