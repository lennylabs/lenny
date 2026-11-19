// SPDX-License-Identifier: MIT

package main

import (
	"context"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// sessionAdminAdapter builds the session core's forced terminal write over
// a store and the session server's terminal pipeline functions, as the
// core's GWSEAM-ForceTerminate builds it over its own. A nil function runs
// nothing for that step.
type sessionAdminAdapter struct {
	store         sessionstore.Store
	onTerminal    func(ctx context.Context, fromState session.State, sess sessionstore.Session)
	beginTerminal func(sessionID string) (end func())
}

func (a sessionAdminAdapter) terminator() sessionserver.ForceTerminator {
	return sessionserver.ForceTerminator{Store: a.store, BeginTerminal: a.beginTerminal, OnSessionTerminal: a.onTerminal}
}

func (a sessionAdminAdapter) GetByID(ctx context.Context, id string) (sessionstore.Session, error) {
	return a.store.GetByID(ctx, id)
}

func (a sessionAdminAdapter) ForceTerminate(ctx context.Context, tenantID, id string) (sessionstore.Session, session.State, bool, error) {
	return a.terminator().ForceTerminate(ctx, tenantID, id)
}
