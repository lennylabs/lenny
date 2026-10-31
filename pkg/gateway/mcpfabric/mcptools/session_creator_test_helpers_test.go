// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"net/http"
	"testing"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// toolOwner is the subject the create_session tests call as, and therefore
// the owner of every session they create.
//
// spec: §10.2 (Session ownership) rule 1.
const toolOwner = "alice@acme.com"

// newSessionCreator returns a session server over store that the
// create_session tool uses as its SessionCreator. The tool creates sessions
// only through the shared §15.1 service, which binds the owner from the
// caller's principal, so every test that calls create_session wires one.
//
// spec: §15.2.1 rule 1; §10.2 (Session ownership) rule 1.
func newSessionCreator(store sessionstore.Store, id string) *sessionserver.Server {
	return sessionserver.New(store, sessionserver.Options{IDFunc: func() string { return id }})
}

// callAsOwner invokes an MCP tool over h with a principal whose Subject is
// toolOwner and whose tenant is empty, so the tool resolves the tenant from
// its Register-time fallback as a principal-less call does.
func callAsOwner(t *testing.T, h http.Handler, tool, args string) map[string]any {
	t.Helper()
	return callWithPrincipal(t, h, authmw.Principal{Subject: toolOwner}, tool, args)
}
