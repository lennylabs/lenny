// SPDX-License-Identifier: MIT

package sessionserver

import (
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/auth"
)

// Handler returns the http.Handler that routes the §15.1 session
// endpoints.
//
// Each session endpoint is wrapped in the §10.2 authorization gate for
// its permission-matrix row: the state-mutating endpoints require
// manage_own_sessions ("Create / cancel own sessions") and the read
// endpoints require read_own_sessions ("Read own session history").
// requireSessionPermission honors tenant custom roles and admits a
// caller whose token carries no roles (the minimal gateway's no-OIDC
// dev posture). See rbac_gate.go.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	manage := func(next http.HandlerFunc) http.HandlerFunc {
		return s.requireSessionPermission(auth.PermManageOwnSessions, next)
	}
	// §10.7 — eval submission is gated on the dedicated
	// session:eval:write capability so an external scorer pipeline holds
	// it without the broader manage_own_sessions authority. F-10.7.4.
	evalWrite := func(next http.HandlerFunc) http.HandlerFunc {
		return s.requireSessionPermission(auth.PermSessionEvalWrite, next)
	}
	read := func(next http.HandlerFunc) http.HandlerFunc {
		return s.requireSessionPermission(auth.PermReadOwnSessions, next)
	}
	mux.HandleFunc("POST /v1/sessions", manage(s.handleCreate))
	mux.HandleFunc("GET /v1/runtimes", s.handleListRuntimes)
	// §15.1 — session-facing pool discovery. Mounted bare like
	// GET /v1/runtimes: the handler scopes the list to pools backing a
	// runtime the caller can already discover (§10.6 transparent filter).
	mux.HandleFunc("GET /v1/pools", s.handleListPools)
	mux.HandleFunc("GET /v1/runtimes/{name}/meta/{key}", s.handleRuntimeMeta)
	mux.HandleFunc("GET /internal/runtimes/{name}/meta/{key}", s.handleInternalRuntimeMeta)
	mux.HandleFunc("GET /v1/models", s.handleListModels)
	mux.HandleFunc("POST /v1/environments/{name}/sessions", manage(s.handleEnvironmentSessions))
	mux.HandleFunc("POST /v1/sessions/start", manage(s.handleCreateAndStart))
	mux.HandleFunc("GET /v1/sessions", read(s.handleList))
	mux.HandleFunc("GET /v1/sessions/{id}", read(s.handleGet))
	mux.HandleFunc("DELETE /v1/sessions/{id}", manage(s.handleDelete))
	mux.HandleFunc("POST /v1/sessions/{id}/finalize", manage(s.handleFinalize))
	mux.HandleFunc("POST /v1/sessions/{id}/start", manage(s.handleStart))
	// spec: §7.2 — the interrupt path signals the runtime
	// through the pod's adapter and waits for `interrupt_acknowledged`
	// within deadlineMs, rather than collapsing the transition to a
	// row-only flip.
	mux.HandleFunc("POST /v1/sessions/{id}/interrupt", manage(s.handleInterrupt))
	mux.HandleFunc("POST /v1/sessions/{id}/terminate",
		manage(s.handleTransition(session.EndpointTerminate, transitionTerminate)))
	mux.HandleFunc("POST /v1/sessions/{id}/resume", manage(s.handleResume))
	mux.HandleFunc("POST /v1/sessions/{id}/derive", manage(s.handleDerive))
	mux.HandleFunc("POST /v1/sessions/{id}/replay", manage(s.handleReplay))
	mux.HandleFunc("POST /v1/sessions/{id}/extend-retention", manage(s.handleExtendRetention))
	mux.HandleFunc("POST /v1/sessions/{id}/eval", evalWrite(s.handleEval))
	mux.HandleFunc("POST /v1/sessions/{id}/memory", manage(s.handleMemoryWrite))
	mux.HandleFunc("GET /v1/sessions/{id}/memory", read(s.handleMemoryQuery))
	mux.HandleFunc("DELETE /v1/sessions/{id}/memory/{memoryId}", manage(s.handleMemoryDelete))
	mux.HandleFunc("POST /v1/sessions/{id}/upload", manage(s.handleUpload))
	mux.HandleFunc("POST /v1/sessions/{id}/upload-archive", manage(s.handleUploadArchive))
	mux.HandleFunc("POST /v1/sessions/{id}/upload-to-session", manage(s.handleUploadToSession))
	mux.HandleFunc("POST /v1/sessions/{id}/messages", manage(s.handleMessages))
	// spec: §15.1 — the §15.4 MessageDAG list over the durable
	// session_messages store, the read side of the message endpoint. Shares
	// the transcript backing; projects each row to a message node with its
	// stable id, derived `from`, and delivery state. F-15.1.3.
	mux.HandleFunc("GET /v1/sessions/{id}/messages", read(s.handleMessagesList))
	mux.HandleFunc("GET /v1/sessions/{id}/transcript", read(s.handleTranscript))
	mux.HandleFunc("GET /v1/sessions/{id}/tree", read(s.handleTree))
	mux.HandleFunc("GET /v1/usage", s.handleUsage)
	mux.HandleFunc("GET /v1/metering/events", s.handleMeteringEvents)
	mux.HandleFunc("GET /v1/sessions/{id}/events", read(s.handleEvents))
	// spec: §15.1 / §24.17 — the `lenny session logs`
	// target; session logs over the durable event store, content-
	// negotiated SSE / JSON envelope with the `--since` filter.
	mux.HandleFunc("GET /v1/sessions/{id}/logs", read(s.handleLogs))
	// spec: §15.1 — per-session artifact listing (the §15.2
	// list_artifacts tool's REST equivalent) and reconciled per-session
	// token usage (the §15.2 get_token_usage tool's REST equivalent). The
	// usage route self-gates on view_usage like GET /v1/usage. F-15.2.3.
	mux.HandleFunc("GET /v1/sessions/{id}/artifacts", read(s.handleListArtifacts))
	mux.HandleFunc("GET /v1/sessions/{id}/usage", s.handleSessionUsage)
	// spec: §15.1 — workspace snapshot download (tar.gz)
	// and the §7.5 captured setup-command output, the REST reads the SDK
	// references for artifact recovery and setup debugging. F-15.1.3.
	mux.HandleFunc("GET /v1/sessions/{id}/workspace", read(s.handleWorkspace))
	mux.HandleFunc("GET /v1/sessions/{id}/setup-output", read(s.handleSetupOutput))
	mux.HandleFunc("GET /v1/sessions/{id}/webhook-events", read(s.handleWebhookEvents))
	// spec: §15.1 path-parameter casing — camelCase route templates.
	mux.HandleFunc("POST /v1/sessions/{id}/tool-use/{toolCallId}/approve", manage(s.handleToolUseApprove))
	mux.HandleFunc("POST /v1/sessions/{id}/tool-use/{toolCallId}/deny", manage(s.handleToolUseDeny))
	mux.HandleFunc("POST /v1/sessions/{id}/elicitations/{elicitationId}/respond", manage(s.handleElicitationRespond))
	mux.HandleFunc("POST /v1/sessions/{id}/elicitations/{elicitationId}/dismiss", manage(s.handleElicitationDismiss))
	mux.HandleFunc("GET /v1/blobs/{ref...}", s.handleBlob)
	return mux
}
