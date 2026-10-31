// SPDX-License-Identifier: MIT

// Package mcpcreate adapts a session server into the create_session tool's
// SessionCreator for suites that drive a session over MCP right after
// creating it. create_session creates a session through the shared §15.1
// service, which binds the owner from the caller's principal and leaves the
// session in `created`; a message sent to a pre-running session is held
// rather than delivered. A suite that sends a message and reads the
// runtime's reply in the same breath wires StartingCreator, so the create
// runs the real create-and-start service and the session is `running`.
//
// spec: §15.2.1 rule 1; §10.2 (Session ownership) rule 1; §7.2 (Message
// delivery routing).
package mcpcreate

import (
	"context"

	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// StartingCreator runs create_session through the session server's
// create-and-start service. The owner, the admission gates, and the tenant
// resolution are the server's own.
type StartingCreator struct {
	Srv *sessionserver.Server
}

// CreateSessionService creates and starts the session.
func (c StartingCreator) CreateSessionService(ctx context.Context, tenantID string, req sessionserver.CreateSessionRequest) (sessionserver.CreateSessionResponse, *sessionserver.ServiceError) {
	return c.Srv.CreateAndStartService(ctx, tenantID, req)
}
