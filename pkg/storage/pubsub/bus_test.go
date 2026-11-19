// SPDX-License-Identifier: MIT

package pubsub

import (
	"context"
	"testing"
	"time"
)

// TestNilBusFromNilClient confirms pubsub.New returns a nil *Bus for a
// nil client, which is the single-replica mode the gateway uses when
// --redis-url is unset.
func TestNilBusFromNilClient(t *testing.T) {
	if b := New(nil); b != nil {
		t.Errorf("New(nil) = %v, want a nil *Bus", b)
	}
}

// TestNilBusPublishIsNoop confirms Publish on a nil Bus does nothing
// and reports no error: a replica without Redis publishes nothing and
// has no error path to handle.
func TestNilBusPublishIsNoop(t *testing.T) {
	var b *Bus
	if err := b.Publish(context.Background(), "any", []byte("payload")); err != nil {
		t.Errorf("nil Bus Publish = %v, want nil", err)
	}
}

// TestNilBusSubscribeBlocksUntilCancel confirms Subscribe on a nil Bus
// blocks until the context is cancelled and delivers nothing, so the
// gateway can start the subscribe goroutine unconditionally.
func TestNilBusSubscribeBlocksUntilCancel(t *testing.T) {
	var b *Bus
	ctx, cancel := context.WithCancel(context.Background())

	delivered := false
	done := make(chan struct{})
	go func() {
		b.Subscribe(ctx, "any", func([]byte) { delivered = true })
		close(done)
	}()

	// The subscribe loop must still be running before the context is
	// cancelled; a nil Bus that returned immediately would fail this.
	select {
	case <-done:
		t.Fatal("nil Bus Subscribe returned before the context was cancelled")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("nil Bus Subscribe did not return after the context was cancelled")
	}
	if delivered {
		t.Error("nil Bus Subscribe delivered a message, want none")
	}
}
