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
	"encoding/json"

	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// StartingCreator runs create_session through the session server's
// create-and-start service. The owner, the admission gates, and the tenant
// resolution are the server's own.
type StartingCreator struct {
	seam.ChildSessions
	Srv *sessionserver.Server
}

// CreateSession decodes the create body and creates and starts the session.
// A body that does not decode and a refusal of the service come back as
// refusals, as the session core's create does.
func (c StartingCreator) CreateSession(ctx context.Context, tenantID string, body json.RawMessage) (seam.CreatedSession, error) {
	var req sessionserver.CreateSessionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return seam.CreatedSession{}, &seam.Refusal{Code: "VALIDATION_ERROR", Message: "invalid arguments: " + err.Error()}
	}
	resp, svcErr := c.Srv.CreateAndStartService(ctx, tenantID, req)
	if svcErr != nil {
		return seam.CreatedSession{}, &seam.Refusal{Code: svcErr.Code, Message: svcErr.Message, Details: svcErr.Details}
	}
	return seam.CreatedSession{ID: resp.ID, State: state.State(resp.State), UploadToken: resp.UploadToken}, nil
}
