// SPDX-License-Identifier: MIT

package main

import (
	"context"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/api/admin"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// sessionAdminAdapter builds the API front's session terminator over a
// store and the session server's terminal pipeline functions, as the
// composition root wires admin.SessionTerminator with the session core's
// GWSEAM-BeginTerminal and GWSEAM-OnSessionTerminal. A nil function runs
// nothing for that operation.
type sessionAdminAdapter struct {
	store         sessionstore.Store
	onTerminal    func(ctx context.Context, fromState session.State, sess sessionstore.Session)
	beginTerminal func(sessionID string) (end func())
}

func (a sessionAdminAdapter) terminator() admin.SessionTerminator {
	return admin.SessionTerminator{Store: a.store, Terminals: funcTerminals{onTerminal: a.onTerminal, beginTerminal: a.beginTerminal}}
}

func (a sessionAdminAdapter) GetByID(ctx context.Context, id string) (sessionstore.Session, error) {
	return a.terminator().GetByID(ctx, id)
}

func (a sessionAdminAdapter) ForceTerminate(ctx context.Context, tenantID, id string) (sessionstore.Session, session.State, bool, error) {
	return a.terminator().ForceTerminate(ctx, tenantID, id)
}

// funcTerminals serves seam.Terminals over the session server's terminal
// functions, handing the row the session record carries to onTerminal.
type funcTerminals struct {
	onTerminal    func(ctx context.Context, fromState session.State, sess sessionstore.Session)
	beginTerminal func(sessionID string) (end func())
}

func (f funcTerminals) BeginTerminal(sessionID string) func() {
	if f.beginTerminal == nil {
		return func() {}
	}
	return f.beginTerminal(sessionID)
}

func (f funcTerminals) OnSessionTerminal(ctx context.Context, from state.State, s seam.Session) {
	if f.onTerminal == nil {
		return
	}
	row, _ := s.Stored.(sessionstore.Session)
	f.onTerminal(ctx, session.State(from), row)
}

func (f funcTerminals) AfterTerminalWrite(_ context.Context, _ state.State, s seam.Session, _ func(context.Context, seam.Session)) seam.Session {
	return s
}
