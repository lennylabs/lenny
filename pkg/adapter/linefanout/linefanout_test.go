// SPDX-License-Identifier: MIT

package linefanout

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// recv receives one line or fails after two seconds; ok is false when the
// channel closed.
func recv(t *testing.T, ch <-chan []byte) (string, bool) {
	t.Helper()
	select {
	case b, ok := <-ch:
		return string(b), ok
	case <-time.After(2 * time.Second):
		t.Fatal("no line within 2s")
	}
	return "", false
}

// spec: 28.5.3 (CH-MSGSOCK), 28.5.3 (CH-MSGSOCK Outbound: session_started)
// Every live subscriber receives every line; a subscriber whose context
// ends is removed and receives nothing further while its siblings keep
// receiving; and a slow subscriber does not hold back a sibling.
func TestHubBroadcastsToLiveSubscribersAndDropsCancelledOnes_spec_28_5_3(t *testing.T) {
	pr, pw := io.Pipe()
	h := New()
	attach, err := h.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	waitCtx, cancel := context.WithCancel(context.Background())
	wait, err := h.Subscribe(waitCtx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	slow, err := h.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	h.Serve(pr, 1024, nil)
	h.Serve(pr, 1024, nil) // a second Serve starts no second reader

	if _, err := pw.Write([]byte("ack\n")); err != nil {
		t.Fatal(err)
	}
	if got, _ := recv(t, wait); got != "ack" {
		t.Fatalf("wait got %q, want ack", got)
	}
	cancel()
	// The cancelled subscription closes without delivering anything more.
	for line := range wait {
		t.Fatalf("cancelled subscription received %q", line)
	}
	if _, err := pw.Write([]byte("response\n")); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ack", "response"} {
		if got, _ := recv(t, attach); got != want {
			t.Fatalf("attach got %q, want %q", got, want)
		}
	}
	// slow has not been read at all; its queue still holds both lines.
	for _, want := range []string{"ack", "response"} {
		if got, _ := recv(t, slow); got != want {
			t.Fatalf("slow got %q, want %q", got, want)
		}
	}
	_ = pw.Close()
}

// spec: 28.5.3 (CH-MSGSOCK)
// When the source ends, each subscriber receives the lines already queued
// for it before its channel closes, the end callback runs first, and a later
// Subscribe fails with ErrEnded.
func TestHubEndDrainsQueuedLinesThenCloses_spec_28_5_3(t *testing.T) {
	pr, pw := io.Pipe()
	h := New()
	sub, err := h.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	ended := make(chan struct{})
	h.Serve(pr, 1024, func() { close(ended) })
	go func() {
		_, _ = pw.Write([]byte("one\ntwo\n"))
		_ = pw.Close()
	}()
	<-ended
	for _, want := range []string{"one", "two"} {
		if got, ok := recv(t, sub); !ok || got != want {
			t.Fatalf("got (%q, %v), want %q", got, ok, want)
		}
	}
	if _, ok := recv(t, sub); ok {
		t.Fatal("subscription stayed open after the source ended")
	}
	if _, err := h.Subscribe(context.Background()); !errors.Is(err, ErrEnded) {
		t.Fatalf("Subscribe after end = %v, want ErrEnded", err)
	}
}
