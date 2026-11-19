// SPDX-License-Identifier: MIT

package sessionserver

import (
	"net/http"
	"strings"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// RegisterRoutes registers the §15.1 session endpoints, the runtime and
// pool discovery, and the model list on the API front, each with the access
// check its §10.2 permission-matrix row requires, which the API front's
// request pipeline applies before the handler runs:
//
//   - session creation requires manage_own_sessions ("Create / cancel own
//     sessions"), and the session list requires read_own_sessions ("Read
//     own session history");
//   - each read of a named session takes the read check and each action on
//     a named session the manage check, which add the §10.2 Session
//     ownership rule to the permission gate;
//   - the eval submission requires the §10.7 session:eval:write capability,
//     so an external scorer pipeline holds it without the broader
//     manage_own_sessions authority (F-10.7.4), and its handler applies the
//     §10.7 caller rule;
//   - the runtime, pool, and model discovery and the usage reads take a
//     resolved tenant, because their handlers filter or gate by the caller
//     themselves;
//   - the published runtime metadata entry and the internal metadata fetch
//     leave authentication to their handlers.
//
// Each route under /v1/sessions is also registered on the forward listener,
// on which another replica forwards a session request to the holder.
//
// spec: GWSEAM-RegisterRoutes; §10.2 (Session ownership); §7.2; §15.1.
func (s *Server) RegisterRoutes(routes seam.Routes) error {
	type route struct {
		method, pattern string
		access          seam.RouteAccess
		handler         http.HandlerFunc
	}
	create, list := seam.AccessSessionCreate, seam.AccessSessionList
	read, manage := seam.AccessSessionRead, seam.AccessSessionManage
	tenant, open := seam.AccessTenant, seam.AccessHandler
	get, post, del := http.MethodGet, http.MethodPost, http.MethodDelete
	client := []route{
		{post, "/v1/sessions", create, s.handleCreate},
		{get, "/v1/runtimes", tenant, s.handleListRuntimes},
		// §15.1 session-facing pool discovery: the handler scopes the list
		// to pools backing a runtime the caller can already discover
		// (§10.6 transparent filter).
		{get, "/v1/pools", tenant, s.handleListPools},
		// §5.1: a public publishedMetadata entry is served with no
		// principal, and the handler answers any other entry by its
		// visibility.
		{get, "/v1/runtimes/{name}/meta/{key}", open, s.handleRuntimeMeta},
		{get, "/v1/models", tenant, s.handleListModels},
		{post, "/v1/environments/{name}/sessions", create, s.handleEnvironmentSessions},
		{post, "/v1/sessions/start", create, s.handleCreateAndStart},
		{get, "/v1/sessions", list, s.handleList},
		{get, "/v1/sessions/{id}", read, s.handleGet},
		{del, "/v1/sessions/{id}", manage, s.handleDelete},
		{post, "/v1/sessions/{id}/finalize", manage, s.handleFinalize},
		{post, "/v1/sessions/{id}/start", manage, s.handleStart},
		// §7.2: the interrupt path signals the runtime
		// through the pod's adapter and waits for `interrupt_acknowledged`
		// within deadlineMs, rather than collapsing the transition to a
		// row-only flip.
		{post, "/v1/sessions/{id}/interrupt", manage, s.handleInterrupt},
		{post, "/v1/sessions/{id}/terminate", manage, s.handleTransition(session.EndpointTerminate, transitionTerminate)},
		{post, "/v1/sessions/{id}/resume", manage, s.handleResume},
		{post, "/v1/sessions/{id}/derive", manage, s.handleDerive},
		{post, "/v1/sessions/{id}/replay", manage, s.handleReplay},
		{post, "/v1/sessions/{id}/extend-retention", manage, s.handleExtendRetention},
		{post, "/v1/sessions/{id}/eval", seam.AccessSessionEval, s.handleEval},
		{post, "/v1/sessions/{id}/messages", manage, s.handleMessages},
		// §15.1: the §15.4 MessageDAG list over the durable
		// session_messages store, the read side of the message endpoint. Shares
		// the transcript backing; projects each row to a message node with its
		// stable id, derived `from`, and delivery state. F-15.1.3.
		{get, "/v1/sessions/{id}/messages", read, s.handleMessagesList},
		{get, "/v1/sessions/{id}/transcript", read, s.handleTranscript},
		{get, "/v1/usage", tenant, s.handleUsage},
		{get, "/v1/metering/events", tenant, s.handleMeteringEvents},
		// §15.1: per-session artifact listing (the §15.2
		// list_artifacts tool's REST equivalent) and reconciled per-session
		// token usage (the §15.2 get_token_usage tool's REST equivalent). The
		// usage route self-gates on view_usage like GET /v1/usage. F-15.2.3.
		{get, "/v1/sessions/{id}/artifacts", read, s.handleListArtifacts},
		{get, "/v1/sessions/{id}/usage", tenant, s.handleSessionUsage},
		// §15.1: workspace snapshot download (tar.gz)
		// and the §7.5 captured setup-command output, the REST reads the SDK
		// references for artifact recovery and setup debugging. F-15.1.3.
		{get, "/v1/sessions/{id}/workspace", read, s.handleWorkspace},
		{get, "/v1/sessions/{id}/setup-output", read, s.handleSetupOutput},
		{get, "/v1/sessions/{id}/webhook-events", read, s.handleWebhookEvents},
	}
	for _, r := range client {
		listeners := []seam.Listener{seam.ListenerClient}
		if r.pattern == "/v1/sessions" || strings.HasPrefix(r.pattern, "/v1/sessions/") {
			listeners = append(listeners, seam.ListenerForward)
		}
		for _, l := range listeners {
			if err := routes.HandleRoute(seam.Route{
				Listener: l, Method: r.method, Pattern: r.pattern, Access: r.access, Handler: r.handler,
			}); err != nil {
				return err
			}
		}
	}
	// §5.1 internal metadata fetch: the handler checks its own principal
	// and answers a missing one with the same 404 as a missing entry.
	return routes.HandleRoute(seam.Route{
		Listener: seam.ListenerInternal, Method: get, Pattern: "/internal/runtimes/{name}/meta/{key}",
		Access: open, Handler: http.HandlerFunc(s.handleInternalRuntimeMeta),
	})
}

// SetServiceHandler sets the handler through which ServiceCall,
// CreateSessionService, and CreateAndStartService run an overlapping REST
// operation in-process: the API front's request pipeline of the client
// listener, so an in-process call passes the same tenant, permission, and
// session checks as a client request. Call it before the server serves.
func (s *Server) SetServiceHandler(h http.Handler) {
	s.serviceHandler = h
}

// sessionStateOf returns the client API state of a seam session record's
// state. The two types spell the same states.
func sessionStateOf(st state.State) session.State {
	return session.State(st)
}
