// SPDX-License-Identifier: MIT

package translator

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// OpenResponsesAdapterCapabilities reports the §15 AdapterCapabilities
// of the Open Responses translator that owns `/v1/responses`. The Open
// Responses wire shape carries `previous_response_id` so a consumer can
// thread a multi-turn conversation through the same Lenny session — the
// adapter therefore reports session continuity. Delegation, elicitation,
// and interrupt surfaces are not native to the Open Responses envelope.
// spec: §9.1 line 35; §15.1 line 575. F-9.1.6 / F-9.1.8.
func OpenResponsesAdapterCapabilities() adapter.Capabilities {
	return adapter.Capabilities{
		PathPrefix:                "/v1/responses",
		Protocol:                  "open-responses",
		SupportsSessionContinuity: true,
		SupportsDelegation:        false,
		SupportsElicitation:       false,
		SupportsInterrupt:         false,
	}
}

// OpenResponsesRequest is the §15.1 POST /v1/responses body. Only
// v1 essential fields are modelled; extension fields such as
// `metadata`, `tools`, `reasoning`, `temperature`, etc. are
// preserved for the executor but not strictly validated.
//
// `OpenResponsesAdapter` covers both Open Responses-compliant
// clients and OpenAI Responses API clients (§15.1: the OpenAI
// Responses API is a proper superset of Open Responses).
type OpenResponsesRequest struct {
	Model              string `json:"model"`
	Input              any    `json:"input"`
	PreviousResponseID string `json:"previous_response_id,omitempty"`
	Stream             bool   `json:"stream,omitempty"`
	User               string `json:"user,omitempty"`
}

// OpenResponsesResponse is the §15.1 successful response.
type OpenResponsesResponse struct {
	ID                 string             `json:"id"`
	Object             string             `json:"object"`
	CreatedAt          int64              `json:"created_at"`
	Status             string             `json:"status"`
	Model              string             `json:"model"`
	PreviousResponseID string             `json:"previous_response_id,omitempty"`
	Output             []OpenResponseItem `json:"output"`
	Usage              OpenResponsesUsage `json:"usage"`
}

// OpenResponseItem is one item in the output array.
type OpenResponseItem struct {
	Type    string                `json:"type"`
	ID      string                `json:"id,omitempty"`
	Status  string                `json:"status,omitempty"`
	Role    string                `json:"role,omitempty"`
	Content []OpenResponseContent `json:"content,omitempty"`
}

// OpenResponseContent is one content block inside an output item.
type OpenResponseContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// OpenResponsesUsage is the usage block.
type OpenResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// OpenResponsesHandler is the §15.1 POST /v1/responses handler.
type OpenResponsesHandler struct {
	store          sessionstore.Store
	exec           executor.Executor
	clock          func() time.Time
	idFn           func() string
	defaultRuntime string
}

// OpenResponsesOptions configures the handler.
type OpenResponsesOptions struct {
	Clock          func() time.Time
	IDFunc         func() string
	DefaultRuntime string
}

// NewOpenResponsesHandler returns a configured handler.
func NewOpenResponsesHandler(store sessionstore.Store, exec executor.Executor, opts OpenResponsesOptions) *OpenResponsesHandler {
	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	idFn := opts.IDFunc
	if idFn == nil {
		idFn = defaultChatID
	}
	rt := opts.DefaultRuntime
	if rt == "" {
		rt = "echo"
	}
	return &OpenResponsesHandler{
		store:          store,
		exec:           exec,
		clock:          clock,
		idFn:           idFn,
		defaultRuntime: rt,
	}
}

// Handler returns the http.Handler routing /v1/responses.
func (h *OpenResponsesHandler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/responses", h.handleCreate)
	mux.HandleFunc("GET /v1/responses/{id}", h.handleGet)
	mux.HandleFunc("DELETE /v1/responses/{id}", h.handleDelete)
	return mux
}

func (h *OpenResponsesHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	if h.exec == nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "server_error",
			"gateway has no executor wired")
		return
	}
	body := http.MaxBytesReader(w, r.Body, 1<<20)
	defer body.Close()

	var req OpenResponsesRequest
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			"request body is not valid JSON: "+err.Error())
		return
	}
	if req.Input == nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			"input is required")
		return
	}
	tenantID := resolveTenant(r)
	// spec: §10.6 line 557 — a model named "environments/{name}/{model}"
	// scopes the implicit session to the named environment; the bare
	// model is the runtime reference. F-10.6.11.
	envScope, runtimeRef := splitEnvModel(req.Model)
	if runtimeRef == "" {
		runtimeRef = h.defaultRuntime
	}
	now := h.clock()
	sessionID := h.idFn()

	row := sessionstore.Session{
		ID:              sessionID,
		TenantID:        tenantID,
		UserID:          req.User,
		RuntimeRef:      runtimeRef,
		Environment:     envScope,
		State:           session.StateRunning,
		ParentSessionID: req.PreviousResponseID, // lineage pointer per §7.1 derive copy semantics
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := h.store.Create(r.Context(), row); err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error",
			fmt.Sprintf("session create failed: %v", err))
		return
	}

	msgs, err := normalizeInput(req.Input)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	sendResp, err := h.exec.Send(r.Context(), sessionID, msgs)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error",
			fmt.Sprintf("executor failure: %v", err))
		return
	}
	out := sendResp.Parts

	_, _ = h.store.Update(r.Context(), tenantID, sessionID, func(s *sessionstore.Session) error {
		s.State = session.StateCompleted
		return nil
	})
	if err := h.exec.Close(r.Context(), sessionID); err != nil && !errors.Is(err, executor.ErrUnsupported) {
		_ = err
	}

	if req.Stream {
		writeOpenResponsesStream(w, sessionID, req.PreviousResponseID, now, out)
		return
	}
	resp := buildOpenResponsesResponse(sessionID, runtimeRef, req.PreviousResponseID, now, out)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// writeOpenResponsesStream emits the Open Responses streaming form —
// an SSE sequence of typed events:
//
//	response.created          — the response envelope opens.
//	response.output_text.delta — one per whitespace-delimited token.
//	response.completed        — the response envelope closes.
//
// The synchronous executor produces a complete response; the
// translator chunks the text so streaming clients observe
// incremental output.
func writeOpenResponsesStream(w http.ResponseWriter, id, prev string, now time.Time, out []executor.MessagePart) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error",
			"response writer does not support streaming")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	emit := func(eventType string, payload any) {
		b, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, b)
		flusher.Flush()
	}

	emit("response.created", map[string]any{
		"type":     "response.created",
		"response": map[string]any{"id": id, "status": "in_progress"},
	})

	full := ""
	for _, p := range out {
		if p.Type == "text" {
			if full != "" {
				full += "\n"
			}
			full += p.Text
		}
	}
	for i, tok := range strings.Fields(full) {
		chunk := tok
		if i > 0 {
			chunk = " " + tok
		}
		emit("response.output_text.delta", map[string]any{
			"type":  "response.output_text.delta",
			"delta": chunk,
		})
	}

	emit("response.completed", map[string]any{
		"type":     "response.completed",
		"response": buildOpenResponsesResponse(id, "", prev, now, out),
	})
}

func (h *OpenResponsesHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	tenantID := resolveTenant(r)
	id := r.PathValue("id")
	row, err := h.store.Get(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			writeOpenAIError(w, http.StatusNotFound, "not_found_error", "response not found")
			return
		}
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	// Minimal envelope — the gateway does not yet persist the raw
	// model output, so the response carries metadata only.
	resp := OpenResponsesResponse{
		ID:                 row.ID,
		Object:             "response",
		CreatedAt:          row.CreatedAt.Unix(),
		Status:             mapStateToResponsesStatus(row.State),
		Model:              row.RuntimeRef,
		PreviousResponseID: row.ParentSessionID,
		Output:             nil,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *OpenResponsesHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	tenantID := resolveTenant(r)
	id := r.PathValue("id")
	if err := h.store.Delete(r.Context(), tenantID, id); err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			writeOpenAIError(w, http.StatusNotFound, "not_found_error", "response not found")
			return
		}
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// normalizeInput accepts either a string (single user message) or an
// array of message objects per the Open Responses spec.
func normalizeInput(in any) ([]executor.Message, error) {
	switch v := in.(type) {
	case string:
		return []executor.Message{{Role: "user", Content: v}}, nil
	case []any:
		var out []executor.Message
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, errors.New("input array items must be objects")
			}
			role, _ := m["role"].(string)
			if role == "" {
				role = "user"
			}
			content, _ := m["content"].(string)
			out = append(out, executor.Message{Role: role, Content: content})
		}
		if len(out) == 0 {
			return nil, errors.New("input array must contain at least one entry")
		}
		return out, nil
	default:
		return nil, errors.New("input must be a string or an array of message objects")
	}
}

// buildOpenResponsesResponse maps executor output to the Open
// Responses response shape.
func buildOpenResponsesResponse(id, model, prev string, now time.Time, out []executor.MessagePart) OpenResponsesResponse {
	resp := OpenResponsesResponse{
		ID:                 id,
		Object:             "response",
		CreatedAt:          now.Unix(),
		Status:             "completed",
		Model:              model,
		PreviousResponseID: prev,
		Output:             []OpenResponseItem{},
	}
	if len(out) > 0 {
		item := OpenResponseItem{
			Type:   "message",
			ID:     "msg_" + id,
			Status: "completed",
			Role:   "assistant",
		}
		for _, p := range out {
			if p.Type == "text" {
				item.Content = append(item.Content, OpenResponseContent{
					Type: "output_text",
					Text: p.Text,
				})
			}
		}
		resp.Output = append(resp.Output, item)
	}
	return resp
}

func mapStateToResponsesStatus(s session.State) string {
	switch s {
	case session.StateCompleted:
		return "completed"
	case session.StateFailed:
		return "failed"
	case session.StateCancelled:
		return "cancelled"
	case session.StateRunning, session.StateStarting:
		return "in_progress"
	default:
		return "queued"
	}
}
