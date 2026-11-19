// SPDX-License-Identifier: MIT

package uploadhandler_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/uploadhandler"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
	"github.com/lennylabs/lenny/tests/testinfra/uploadwire"
)

// These helpers build the session server and the upload handler together,
// as the composition root does, and carry no specification annotation.

// testOwner is the subject the tests in this package authenticate as when a
// test sets no principal of its own, and therefore the owner of every
// session such a test seeds.
const testOwner = ownerprincipal.Alice

// asOwner wraps h so that a request reaching it with no principal carries a
// principal whose Subject is testOwner, as the session routes' ownership
// check requires. A request that already carries a principal keeps it.
func asOwner(h http.Handler) http.Handler {
	return ownerprincipal.Wrap(h, testOwner)
}

// serveUploads builds a session server over store with core, attaches an
// upload handler built from up, and returns the session server's handler,
// which serves the session routes and the upload routes together.
func serveUploads(t *testing.T, store sessionstore.Store, core sessionserver.Options, up uploadhandler.Options) (*sessionserver.Server, http.Handler) {
	t.Helper()
	srv := sessionserver.New(store, core)
	uploadwire.Attach(t, srv, up)
	return srv, srv.Handler()
}

// lifecycleSink records the session lifecycle audit events the session core
// emits, including the session.upload events the upload handler records
// through it.
type lifecycleSink struct {
	mu     sync.Mutex
	events []sessionserver.SessionLifecycleEvent
}

func (s *lifecycleSink) EmitSessionLifecycle(_ context.Context, ev sessionserver.SessionLifecycleEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *lifecycleSink) eventsOf(eventType string) []sessionserver.SessionLifecycleEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]sessionserver.SessionLifecycleEvent, 0)
	for _, ev := range s.events {
		if ev.EventType == eventType {
			out = append(out, ev)
		}
	}
	return out
}
