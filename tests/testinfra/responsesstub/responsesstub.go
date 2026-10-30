// SPDX-License-Identifier: MIT

// Package responsesstub holds explicit test stand-ins for the holder
// forwarder and the session terminal the Open Responses handler requires.
//
// The gateway always wires the session server's forwarder and the session
// server itself as the terminal, and the handler constructor refuses a nil
// for either. An in-process test that builds the handler without a session
// server passes these stubs instead, so the single-replica assumption each
// one encodes is visible at the call site.
//
// spec: §15 (Open Responses conversation sessions); §29.3 (Interactive
// message send) Forwarding.
package responsesstub

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// LocalForwarder reports every turn local and no request forwarded, which is
// what the session server's forwarder answers on a replica that coordinates
// every session.
type LocalForwarder struct{}

// ForwardResponsesTurn reports the turn local, so the handler serves it.
func (LocalForwarder) ForwardResponsesTurn(http.ResponseWriter, *http.Request, sessionstore.Session, []byte,
	func(http.ResponseWriter, string),
) bool {
	return true
}

// Forwarded reports that no request arrived forwarded.
func (LocalForwarder) Forwarded(context.Context) bool { return false }

// ReleasingTerminal opens no terminal claim, and its post-terminal hook
// releases the session's executor binding with the failed disposition and
// records the pod release on the row, which is the release the session
// server's hook performs for a failed session. Store may be nil, in which
// case no release marker is written.
type ReleasingTerminal struct {
	Exec  executor.Executor
	Store sessionstore.Store
}

// BeginTerminal opens no claim and returns a no-op end.
func (ReleasingTerminal) BeginTerminal(string) func() { return func() {} }

// AfterTerminalWrite releases the session's executor binding with the failed
// disposition and records the pod release.
func (t ReleasingTerminal) AfterTerminalWrite(ctx context.Context, _ session.State, sess sessionstore.Session,
	_ func(context.Context, sessionstore.Session),
) sessionstore.Session {
	if t.Exec != nil {
		if err := executor.ReleaseSession(ctx, t.Exec, sess.ID, executor.DispositionFailed); err != nil &&
			!errors.Is(err, executor.ErrUnsupported) {
			return sess
		}
	}
	if t.Store != nil {
		_ = sessionstore.RecordPodRelease(ctx, t.Store, sess.TenantID, sess.ID, time.Now().UTC())
	}
	return sess
}
