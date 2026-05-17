// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/opsevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// hasSessionFailed reports whether the buffer holds a session_failed
// operational event.
func hasSessionFailed(buf *opsevents.EventBuffer) bool {
	for _, e := range buf.Query(0, opsevents.EventFilter{}, 100).Events {
		if e.Event.Type == "dev.lenny.session_failed" {
			return true
		}
	}
	return false
}

func TestRecordSessionCompletedEmitsSessionFailed(t *testing.T) {
	// §25.3: a session reaching the failed state emits session_failed.
	emitter := opsevents.NewEmitter(opsevents.NewEventBuffer(0), "test")
	srv := New(memstore.New(), Options{OpsEmitter: emitter})

	srv.recordSessionCompleted(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", RuntimeRef: "echo", State: session.StateFailed,
	})
	if !hasSessionFailed(emitter.Buffer()) {
		t.Error("a failed session must emit a session_failed operational event")
	}
}

func TestRecordSessionCompletedNoEmitForNonFailed(t *testing.T) {
	emitter := opsevents.NewEmitter(opsevents.NewEventBuffer(0), "test")
	srv := New(memstore.New(), Options{OpsEmitter: emitter})

	srv.recordSessionCompleted(context.Background(), sessionstore.Session{
		ID: "s2", TenantID: "acme", State: session.StateCompleted,
	})
	if hasSessionFailed(emitter.Buffer()) {
		t.Error("a completed session must not emit session_failed")
	}
}

func TestRecordSessionCompletedNoEmitterIsSafe(t *testing.T) {
	// The emit is best-effort: a nil OpsEmitter must not panic.
	srv := New(memstore.New(), Options{})
	srv.recordSessionCompleted(context.Background(), sessionstore.Session{
		ID: "s3", TenantID: "acme", State: session.StateFailed,
	})
}
