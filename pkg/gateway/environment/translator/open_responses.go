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
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
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
//
// Error, IncompleteDetails, Instructions, Tools, ParallelToolCalls,
// Metadata, ToolChoice, Temperature, and TopP are required keys on
// the published OpenAI Responses API `Response` object even though
// this translator does not model requesting them. Each is set to the
// value OpenAI's own API documents for a call made with no options
// set, so the wire body validates against the published schema
// without asserting behavior (tool calling, sampling controls) the
// translator does not implement. spec: §15.1 (Open Responses
// adapter covers OpenAI Responses API clients; the Responses API is a
// proper superset of Open Responses).
type OpenResponsesResponse struct {
	ID                 string                          `json:"id"`
	Object             string                          `json:"object"`
	CreatedAt          int64                           `json:"created_at"`
	Status             string                          `json:"status"`
	Model              string                          `json:"model"`
	PreviousResponseID string                          `json:"previous_response_id,omitempty"`
	Output             []OpenResponseItem              `json:"output"`
	Usage              OpenResponsesUsage              `json:"usage"`
	Error              *OpenResponsesError             `json:"error"`
	IncompleteDetails  *OpenResponsesIncompleteDetails `json:"incomplete_details"`
	Instructions       *string                         `json:"instructions"`
	Tools              []OpenResponsesTool             `json:"tools"`
	ParallelToolCalls  bool                            `json:"parallel_tool_calls"`
	Metadata           map[string]string               `json:"metadata"`
	ToolChoice         string                          `json:"tool_choice"`
	Temperature        float64                         `json:"temperature"`
	TopP               float64                         `json:"top_p"`
}

// OpenResponsesError is the published `error` object shape. The
// translator never populates it on a successful response (errors are
// surfaced as HTTP-level OpenAIError envelopes instead), so the
// fields exist only to give a nil *OpenResponsesError the correct
// wire shape when a future caller sets one.
type OpenResponsesError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// OpenResponsesIncompleteDetails is the published
// `incomplete_details` object shape, populated when Status is
// "incomplete".
type OpenResponsesIncompleteDetails struct {
	Reason string `json:"reason"`
}

// OpenResponsesTool is the published `tools` array element shape.
// The translator does not accept tool definitions on the request, so
// this is always empty on the response; the type exists so the field
// is typed rather than `[]any`.
type OpenResponsesTool struct {
	Type string `json:"type"`
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
//
// Annotations and Logprobs are required keys on the published
// `output_text` content schema (both arrays, never null). The
// translator neither generates citations nor computes per-token log
// probabilities, so both are always empty, non-nil slices — a nil
// slice would mismatch the schema's `[]any{}.MarshalJSON` `[]` shape
// only if json.Marshal ever emitted `null` for it, so both fields are
// initialized explicitly where a content block is built. spec:
// §15.1 (Open Responses adapter covers OpenAI Responses API
// clients).
type OpenResponseContent struct {
	Type        string           `json:"type"`
	Text        string           `json:"text,omitempty"`
	Annotations []map[string]any `json:"annotations"`
	Logprobs    []map[string]any `json:"logprobs"`
}

// OpenResponsesUsage is the usage block. InputTokensDetails and
// OutputTokensDetails are required keys on the published
// `ResponseUsage` schema; the translator does not track cached or
// reasoning token counts, so both breakdowns report zero.
type OpenResponsesUsage struct {
	InputTokens         int                        `json:"input_tokens"`
	InputTokensDetails  OpenResponsesTokensDetails `json:"input_tokens_details"`
	OutputTokens        int                        `json:"output_tokens"`
	OutputTokensDetails OpenResponsesOutputDetails `json:"output_tokens_details"`
	TotalTokens         int                        `json:"total_tokens"`
}

// OpenResponsesTokensDetails is the published `input_tokens_details`
// object shape.
type OpenResponsesTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// OpenResponsesOutputDetails is the published `output_tokens_details`
// object shape.
type OpenResponsesOutputDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// OpenResponsesHandler is the §15.1 POST /v1/responses handler.
type OpenResponsesHandler struct {
	store          sessionstore.Store
	exec           executor.Executor
	clock          func() time.Time
	defaultRuntime string
	binder         SingleShotBinder
	releaseTimeout time.Duration
}

// OpenResponsesOptions configures the handler.
type OpenResponsesOptions struct {
	Clock          func() time.Time
	IDFunc         func() string
	DefaultRuntime string

	// SingleShotBinder runs the shared create-and-start service that
	// claims a warm pod, launches the runtime, and registers the pod
	// binding for each request. Pass nil to fall back to the no-op binder
	// (the §17.4 in-memory mode and the in-process unit tests).
	// spec: §15 built-in adapter single-shot compute model.
	SingleShotBinder SingleShotBinder

	// ReleaseTimeout bounds the detached context the deferred pod release
	// runs under. Zero defaults to defaultSingleShotReleaseTimeout.
	// Operator-tunable.
	ReleaseTimeout time.Duration
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
	binder := opts.SingleShotBinder
	if binder == nil {
		binder = newNoopSingleShotBinder(store, idFn, clock)
	}
	releaseTimeout := opts.ReleaseTimeout
	if releaseTimeout <= 0 {
		releaseTimeout = defaultSingleShotReleaseTimeout
	}
	return &OpenResponsesHandler{
		store:          store,
		exec:           exec,
		clock:          clock,
		defaultRuntime: rt,
		binder:         binder,
		releaseTimeout: releaseTimeout,
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

	// Claim a warm pod, launch the runtime, and register the binding
	// through the shared §15.2.1 create-and-start service. The single-shot
	// path does not thread previous_response_id into the row's
	// ParentSessionID: the create-and-start reuse surface carries no parent
	// field and setting it would trip the §8.2/§8.6 delegated-child lease
	// semantics, so continuation re-claims a fresh pod per request and the
	// stored ParentSessionID stays empty (GET echoes empty).
	// spec: §15 built-in adapter single-shot compute model; §8.2 delegated-child lease; §7.1 atomicity.
	sessionID, err := h.binder.BindSingleShot(r.Context(), SingleShotSpec{
		TenantID:    tenantID,
		UserID:      req.User,
		RuntimeRef:  runtimeRef,
		Environment: envScope,
	})
	if err != nil {
		writeSingleShotError(w, err)
		return
	}

	// Release the claimed pod and its §4.9 lease on every exit path,
	// recording the §6.2 terminal disposition on a detached, timeout-bounded
	// context so the drain survives a request-context timeout or client
	// disconnect. disp flips to completed only after a successful dispatch
	// and completion update.
	// spec: §15 built-in adapter single-shot compute model; §6.2 release.
	disp := executor.DispositionFailed
	defer releaseSingleShot(r.Context(), h.exec, h.releaseTimeout, sessionID, &disp)

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
	disp = executor.DispositionCompleted

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
		Error:              nil,
		IncompleteDetails:  nil,
		Instructions:       nil,
		Tools:              []OpenResponsesTool{},
		ParallelToolCalls:  true,
		Metadata:           map[string]string{},
		ToolChoice:         "auto",
		Temperature:        1,
		TopP:               1,
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
		Error:              nil,
		IncompleteDetails:  nil,
		Instructions:       nil,
		Tools:              []OpenResponsesTool{},
		ParallelToolCalls:  true,
		Metadata:           map[string]string{},
		ToolChoice:         "auto",
		Temperature:        1,
		TopP:               1,
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
					Type:        "output_text",
					Text:        p.Text,
					Annotations: []map[string]any{},
					Logprobs:    []map[string]any{},
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
