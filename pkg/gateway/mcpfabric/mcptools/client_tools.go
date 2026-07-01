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
// spec: §15.2.1 rule 1 line 1380. F-15.2.3.
type SessionService interface {
	ServiceCall(ctx context.Context, tenantID, method, target string, body []byte, contentType string, headers map[string]string) (sessionserver.ServiceResult, *sessionserver.ServiceError)
}

// registerClientFacingTools installs the §15.2 lines 1284-1306
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
	// the remainder to the per-session path.
	postByID := func(name, description, subpath string) {
		srv.RegisterTool(mcp.Tool{
			Name:        name,
			Description: description,
			InputSchema: sessionIDInputSchema,
		}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
			id, body, err := extractSessionID(args)
			if err != nil {
				return mcp.ToolResult{}, err
			}
			return dispatch(ctx, "POST", "/v1/sessions/"+url.PathEscape(id)+subpath, body, jsonBody, nil)
		})
	}

	// getByID registers a GET tool that targets one session subresource.
	getByID := func(name, description, subpath string) {
		srv.RegisterTool(mcp.Tool{
			Name:        name,
			Description: description,
			InputSchema: sessionIDInputSchema,
		}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
			id, err := requireSessionID(args)
			if err != nil {
				return mcp.ToolResult{}, err
			}
			return dispatch(ctx, "GET", "/v1/sessions/"+url.PathEscape(id)+subpath, nil, "", nil)
		})
	}

	// spec: §15.2 line 1289 — create, upload inline files, and start in one
	// call. The full POST /v1/sessions/start body is forwarded verbatim.
	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/create_and_start_session",
		Description: "Create a session, upload inline files, and start the runtime in one call.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"runtimeRef":{"type":"string"},"userId":{"type":"string"},"environment":{"type":"string"}},"additionalProperties":true}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		body := normalizeBody(args)
		return dispatch(ctx, "POST", "/v1/sessions/start", body, jsonBody, nil)
	})

	// spec: §15.2 lines 1291-1304 — per-session lifecycle transitions.
	postByID("lenny/start_session", "Start the agent runtime for a created session.", "/start")
	postByID("lenny/finalize_workspace", "Seal the session workspace and run setup.", "/finalize")
	postByID("lenny/terminate_session", "End a session gracefully (marks completed).", "/terminate")
	postByID("lenny/resume_session", "Resume a suspended or paused session.", "/resume")

	// spec: §15.2 lines 1296-1300 — per-session reads.
	getByID("lenny/get_session_status", "Query a session's state (including suspended).", "")
	getByID("lenny/list_artifacts", "List the artifacts for a session.", "/artifacts")
	getByID("lenny/get_token_usage", "Get the reconciled token usage for a session.", "/usage")

	// spec: §15.2 line 1298 — get_session_logs (paginated). `since` and
	// `limit` map to the GET /v1/sessions/{id}/logs query parameters.
	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/get_session_logs",
		Description: "Get a session's logs (paginated).",
		InputSchema: json.RawMessage(`{"type":"object","required":["sessionId"],"properties":{"sessionId":{"type":"string"},"since":{"type":"string","description":"RFC3339 lower bound; only log lines at or after this time are returned."},"limit":{"type":"integer","description":"Maximum number of log lines to return."}}}`),
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

	// spec: §15.2 line 1305 — list_sessions (filterable). `state`,
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

	// spec: §15.2 line 1301 — download_artifact. The blob bytes are
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

	// spec: §15.2 line 1290 — upload_files. The file bytes arrive
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
		case in.UploadToken == "":
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

// sessionIDInputSchema is the shared JSON schema for the per-session
// lifecycle/read tools that take a single required `sessionId`.
var sessionIDInputSchema = json.RawMessage(`{"type":"object","required":["sessionId"],"properties":{"sessionId":{"type":"string","description":"The target session id."}}}`)

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

// normalizeBody returns the arguments as a request body, collapsing an
// empty/absent object to nil so a no-body POST forwards no payload.
func normalizeBody(args json.RawMessage) []byte {
	if len(args) == 0 || string(args) == "null" || string(args) == "{}" {
		return nil
	}
	return args
}
