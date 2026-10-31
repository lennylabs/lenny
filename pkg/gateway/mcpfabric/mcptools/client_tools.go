// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// SessionService is the §15.2.1 rule-1 shared service layer the
// overlapping client-facing MCP tools dispatch through.
// *sessionserver.Server implements it: a tool builds the REST request
// target and forwards it in-process, so the MCP surface runs the exact
// REST route, validation, and response shaping rather than a parallel
// implementation that could drift. The 2xx body is returned verbatim and
// a non-2xx surfaces the §15.1 error envelope (code/category/retryable)
// so the §15.2.1 rule-3/5(d) parity holds on both surfaces.
//
// SessionAccessible is the §10.2 ownership predicate the tools that read
// the session store directly call on the row a tool call names: it
// reports whether the principal on ctx may read (act == false) or act on
// (act == true) that row.
//
// spec: §15.2.1 rule 1. F-15.2.3. §10.2 (Session ownership) rules 4 and 5.
type SessionService interface {
	ServiceCall(ctx context.Context, tenantID, method, target string, body []byte, contentType string, headers map[string]string) (sessionserver.ServiceResult, *sessionserver.ServiceError)
	SessionAccessible(ctx context.Context, row sessionstore.Session, act bool) bool
}

// registerClientFacingTools installs the §15.2
// client-facing tools that map to an overlapping REST operation. They
// register only when a SessionService is wired; the minimal in-process
// gateway and the unit suite leave it nil. spec: §15.2. F-15.2.3.
func registerClientFacingTools(srv *mcp.Server, deps Deps, tenant string) {
	if deps.SessionService == nil {
		return
	}
	svc := deps.SessionService

	// jsonBody is the application/json content type the POST tools forward.
	const jsonBody = "application/json"

	// dispatch runs the shared service call and projects the result onto
	// the MCP tool envelope: a 2xx body is forwarded verbatim as a text
	// block (byte-identical to REST), a non-2xx becomes a *mcp.ToolError
	// carrying the canonical lenny code so the shared errorclassify table
	// assigns the same (category, retryable) pair on both surfaces.
	dispatch := func(ctx context.Context, method, target string, body []byte, contentType string, headers map[string]string) (mcp.ToolResult, error) {
		res, svcErr := svc.ServiceCall(ctx, callerTenantID(ctx, tenant), method, target, body, contentType, headers)
		if svcErr != nil {
			return mcp.ToolResult{}, mcp.NewToolError(svcErr.Code, svcErr.Message, svcErr.Details)
		}
		return textResult(string(res.Body)), nil
	}

	// postByID registers a POST tool that targets one session: it requires
	// a non-empty `sessionId`, strips it from the forwarded body, and POSTs
	// the remainder to the per-session path. inputSchema is the advertised
	// tool input schema, generated from the REST operation with its `id`
	// path parameter renamed to `sessionId` (§15.2.1 item 4).
	postByID := func(name, description, subpath string, inputSchema json.RawMessage) {
		srv.RegisterTool(mcp.Tool{
			Name:        name,
			Description: description,
			InputSchema: inputSchema,
		}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
			id, body, err := extractSessionID(args)
			if err != nil {
				return mcp.ToolResult{}, err
			}
			return dispatch(ctx, "POST", "/v1/sessions/"+url.PathEscape(id)+subpath, body, jsonBody, nil)
		})
	}

	// getByID registers a GET tool that targets one session subresource.
	// inputSchema is generated from the REST operation, as for postByID.
	getByID := func(name, description, subpath string, inputSchema json.RawMessage) {
		srv.RegisterTool(mcp.Tool{
			Name:        name,
			Description: description,
			InputSchema: inputSchema,
		}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
			id, err := requireSessionID(args)
			if err != nil {
				return mcp.ToolResult{}, err
			}
			return dispatch(ctx, "GET", "/v1/sessions/"+url.PathEscape(id)+subpath, nil, "", nil)
		})
	}

	// spec: §15.2 — create, upload inline files, and start in one
	// call. The full POST /v1/sessions/start body is forwarded verbatim.
	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/create_and_start_session",
		Description: "Create a session, upload inline files, and start the runtime in one call.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"runtimeRef":{"type":"string"},"environment":{"type":"string"}},"additionalProperties":true}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		body := normalizeBody(args)
		return dispatch(ctx, "POST", "/v1/sessions/start", body, jsonBody, nil)
	})

	// spec: §15.2 — per-session lifecycle transitions.
	postByID("lenny/start_session", "Start the agent runtime for a created session.", "/start", GeneratedStartSessionInputSchema)
	// spec: §15.2, §14.1 (Plan binding) — the optional workspacePlan
	// argument is forwarded as the REST finalize body. §7.4 Upload Safety
	// (Upload authorization): every finalize request carries the §7.1
	// uploadToken, so the required uploadToken argument is stripped from
	// the body and forwarded in the X-Lenny-Upload-Token header the REST
	// handler verifies, as lenny/upload_files does.
	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/finalize_workspace",
		Description: "Seal the session workspace using its upload token, binding an optional workspace plan, and run setup.",
		InputSchema: GeneratedFinalizeWorkspaceInputSchema,
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		id, token, body, err := extractFinalizeArgs(args)
		if err != nil {
			return mcp.ToolResult{}, err
		}
		return dispatch(ctx, "POST", "/v1/sessions/"+url.PathEscape(id)+"/finalize", body, jsonBody,
			map[string]string{"X-Lenny-Upload-Token": token})
	})
	postByID("lenny/terminate_session", "End a session gracefully (marks completed).", "/terminate", GeneratedTerminateSessionInputSchema)
	postByID("lenny/resume_session", "Resume a suspended or paused session.", "/resume", GeneratedResumeSessionInputSchema)
	// spec: §15.2, §15.2.1 (REST/MCP Consistency Contract) — the interrupt
	// runs the REST POST /v1/sessions/{id}/interrupt route, the one write of
	// an interrupt into suspended, and answers its session body.
	postByID("lenny/interrupt_session", "Interrupt the running agent in a session. REST equivalent: POST /v1/sessions/{id}/interrupt.", "/interrupt", GeneratedInterruptSessionInputSchema)

	// spec: §15.2 — per-session reads.
	getByID("lenny/get_session_status", "Query a session's state (including suspended).", "", GeneratedGetSessionStatusInputSchema)
	getByID("lenny/list_artifacts", "List the artifacts for a session.", "/artifacts", GeneratedListArtifactsInputSchema)
	getByID("lenny/get_token_usage", "Get the reconciled token usage for a session.", "/usage", GeneratedGetTokenUsageInputSchema)

	// spec: §15.2 — get_session_logs (paginated). `since` and
	// `limit` map to the GET /v1/sessions/{id}/logs query parameters.
	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/get_session_logs",
		Description: "Get a session's logs (paginated).",
		InputSchema: GeneratedGetSessionLogsInputSchema,
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			SessionID string `json:"sessionId"`
			Since     string `json:"since"`
			Limit     int    `json:"limit"`
		}
		if err := unmarshalArgs(args, &in); err != nil {
			return mcp.ToolResult{}, err
		}
		if in.SessionID == "" {
			return mcp.ToolResult{}, missingSessionID()
		}
		q := url.Values{}
		if in.Since != "" {
			q.Set("since", in.Since)
		}
		if in.Limit > 0 {
			q.Set("limit", strconv.Itoa(in.Limit))
		}
		target := "/v1/sessions/" + url.PathEscape(in.SessionID) + "/logs"
		if enc := q.Encode(); enc != "" {
			target += "?" + enc
		}
		return dispatch(ctx, "GET", target, nil, "", nil)
	})

	// spec: §15.2 — list_sessions (filterable). `state`,
	// `runtime`, and repeatable `label` map to the GET /v1/sessions query.
	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/list_sessions",
		Description: "List active or recent sessions, filterable by state, runtime, and labels.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"state":{"type":"string"},"runtime":{"type":"string"},"labels":{"type":"array","items":{"type":"string"},"description":"key=value label filters (AND-combined)."}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			State   string   `json:"state"`
			Runtime string   `json:"runtime"`
			Labels  []string `json:"labels"`
		}
		if err := unmarshalArgs(args, &in); err != nil {
			return mcp.ToolResult{}, err
		}
		q := url.Values{}
		if in.State != "" {
			q.Set("state", in.State)
		}
		if in.Runtime != "" {
			q.Set("runtime", in.Runtime)
		}
		for _, l := range in.Labels {
			if l != "" {
				q.Add("label", l)
			}
		}
		target := "/v1/sessions"
		if enc := q.Encode(); enc != "" {
			target += "?" + enc
		}
		return dispatch(ctx, "GET", target, nil, "", nil)
	})

	// spec: §15.2 — download_artifact. The blob bytes are
	// base64-encoded into a JSON result (MCP tool content is text-only),
	// alongside the blob's mime type and size so a client can reconstruct
	// the artifact.
	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/download_artifact",
		Description: "Download a specific artifact by its lenny-blob:// reference.",
		InputSchema: json.RawMessage(`{"type":"object","required":["ref"],"properties":{"ref":{"type":"string","description":"The lenny-blob:// artifact reference."}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			Ref string `json:"ref"`
		}
		if err := unmarshalArgs(args, &in); err != nil {
			return mcp.ToolResult{}, err
		}
		if in.Ref == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"ref is required", map[string]any{"field": "ref"})
		}
		res, svcErr := svc.ServiceCall(ctx, callerTenantID(ctx, tenant), "GET",
			"/v1/blobs/"+url.PathEscape(in.Ref), nil, "", nil)
		if svcErr != nil {
			return mcp.ToolResult{}, mcp.NewToolError(svcErr.Code, svcErr.Message, svcErr.Details)
		}
		out, _ := json.Marshal(map[string]any{
			"ref":        in.Ref,
			"mimeType":   res.ContentType,
			"sizeBytes":  len(res.Body),
			"dataBase64": base64.StdEncoding.EncodeToString(res.Body),
		})
		return textResult(string(out)), nil
	})

	// spec: §15.2 — upload_files. The file bytes arrive
	// base64-encoded; the §7.1 uploadToken minted at create authorizes the
	// POST /v1/sessions/{id}/upload, carried on the X-Lenny-Upload-Token
	// header the REST handler reads.
	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/upload_files",
		Description: "Upload a workspace file to a session using its upload token.",
		InputSchema: json.RawMessage(`{"type":"object","required":["sessionId","uploadToken","contentBase64"],"properties":{"sessionId":{"type":"string"},"uploadToken":{"type":"string","description":"The §7.1 uploadToken returned by create_session."},"contentBase64":{"type":"string","description":"base64-encoded file bytes."},"mimeType":{"type":"string","description":"Content type of the uploaded bytes. Defaults to application/octet-stream."}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			SessionID     string `json:"sessionId"`
			UploadToken   string `json:"uploadToken"`
			ContentBase64 string `json:"contentBase64"`
			MimeType      string `json:"mimeType"`
		}
		if err := unmarshalArgs(args, &in); err != nil {
			return mcp.ToolResult{}, err
		}
		switch {
		case in.SessionID == "":
			return mcp.ToolResult{}, missingSessionID()
		case strings.TrimSpace(in.UploadToken) == "":
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"uploadToken is required", map[string]any{"field": "uploadToken"})
		case in.ContentBase64 == "":
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"contentBase64 is required", map[string]any{"field": "contentBase64"})
		}
		body, err := base64.StdEncoding.DecodeString(in.ContentBase64)
		if err != nil {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"contentBase64 is not valid base64", map[string]any{"field": "contentBase64"})
		}
		ct := in.MimeType
		if ct == "" {
			ct = "application/octet-stream"
		}
		return dispatch(ctx, "POST", "/v1/sessions/"+url.PathEscape(in.SessionID)+"/upload",
			body, ct, map[string]string{"X-Lenny-Upload-Token": in.UploadToken})
	})
}

// unmarshalArgs decodes the tool arguments, mapping a decode failure to
// the canonical VALIDATION_ERROR envelope. An empty arguments object
// leaves the target zero-valued.
func unmarshalArgs(args json.RawMessage, v any) error {
	if len(args) == 0 || string(args) == "null" {
		return nil
	}
	if err := json.Unmarshal(args, v); err != nil {
		return errInvalidArgs(err)
	}
	return nil
}

// missingSessionID is the canonical VALIDATION_ERROR for an absent
// sessionId argument.
func missingSessionID() error {
	return mcp.NewToolError("VALIDATION_ERROR", "sessionId is required",
		map[string]any{"field": "sessionId"})
}

// requireSessionID decodes and validates the required `sessionId` field.
func requireSessionID(args json.RawMessage) (string, error) {
	var in struct {
		SessionID string `json:"sessionId"`
	}
	if err := unmarshalArgs(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return "", missingSessionID()
	}
	return in.SessionID, nil
}

// extractSessionID pulls the required `sessionId` out of the arguments and
// returns it alongside the remaining fields re-marshaled as the request
// body (nil when no other fields are present), so a per-session POST
// forwards only the body fields the REST handler expects.
func extractSessionID(args json.RawMessage) (string, []byte, error) {
	m := map[string]json.RawMessage{}
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &m); err != nil {
			return "", nil, errInvalidArgs(err)
		}
	}
	raw, ok := m["sessionId"]
	if !ok {
		return "", nil, missingSessionID()
	}
	var id string
	if err := json.Unmarshal(raw, &id); err != nil || strings.TrimSpace(id) == "" {
		return "", nil, missingSessionID()
	}
	delete(m, "sessionId")
	if len(m) == 0 {
		return id, nil, nil
	}
	body, err := json.Marshal(m)
	if err != nil {
		return "", nil, errInvalidArgs(err)
	}
	return id, body, nil
}

// extractFinalizeArgs splits the lenny/finalize_workspace arguments into the
// session id, the §7.1 uploadToken, and the remaining REST finalize body.
// A missing session id fails as extractSessionID does, and a missing or
// empty uploadToken fails with VALIDATION_ERROR naming the field, because
// the REST finalize refuses a request without it (§7.4 Upload
// authorization).
func extractFinalizeArgs(args json.RawMessage) (string, string, []byte, error) {
	id, rest, err := extractSessionID(args)
	if err != nil {
		return "", "", nil, err
	}
	m := map[string]json.RawMessage{}
	if len(rest) > 0 {
		if err := json.Unmarshal(rest, &m); err != nil {
			return "", "", nil, errInvalidArgs(err)
		}
	}
	var token string
	if raw, ok := m["uploadToken"]; ok {
		if err := json.Unmarshal(raw, &token); err != nil {
			token = ""
		}
	}
	if strings.TrimSpace(token) == "" {
		return "", "", nil, mcp.NewToolError("VALIDATION_ERROR",
			"uploadToken is required", map[string]any{"field": "uploadToken"})
	}
	delete(m, "uploadToken")
	if len(m) == 0 {
		return id, token, nil, nil
	}
	body, err := json.Marshal(m)
	if err != nil {
		return "", "", nil, errInvalidArgs(err)
	}
	return id, token, body, nil
}

// normalizeBody returns the arguments as a request body, collapsing an
// empty/absent object to nil so a no-body POST forwards no payload.
func normalizeBody(args json.RawMessage) []byte {
	if len(args) == 0 || string(args) == "null" || string(args) == "{}" {
		return nil
	}
	return args
}
