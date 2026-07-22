// SPDX-License-Identifier: MIT

// Package translator implements the §15.2 / §15.3 protocol adapters
// that translate external API requests (OpenAI Chat Completions, MCP,
// Open Responses) into the gateway's internal session + message
// surface. Each translator is a thin adapter over the existing
// sessionserver, blobstore, and executor packages so the wire shapes
// stay in lockstep with the source-of-truth REST API.
//
// This file ships the OpenAI Chat Completions translator
// (`POST /v1/chat/completions`) per §15.1.
package translator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// OpenAIChatAdapterCapabilities reports the §15 AdapterCapabilities of
// the OpenAI Chat Completions translator that owns `/v1/chat/completions`.
// Lenny session continuity, delegation, elicitation, and interrupt
// surfaces are not part of the OpenAI Chat wire shape — a consumer of
// this adapter is treated as stateless across calls and cannot resolve
// elicitations or approve tool use through the OpenAI envelope.
// spec: §9.1 line 35; §15.1 line 575. F-9.1.6 / F-9.1.8.
func OpenAIChatAdapterCapabilities() adapter.Capabilities {
	return adapter.Capabilities{
		PathPrefix:                "/v1/chat/completions",
		Protocol:                  "openai-completions",
		SupportsSessionContinuity: false,
		SupportsDelegation:        false,
		SupportsElicitation:       false,
		SupportsInterrupt:         false,
	}
}

// OpenAIChatCompletionsRequest is the OpenAI request shape this
// translator accepts. Only the v1 essential fields are modelled;
// extension fields (`temperature`, `top_p`, `n`, `tools`, etc.) are
// preserved on the raw map for future translators.
type OpenAIChatCompletionsRequest struct {
	Model    string              `json:"model"`
	Messages []OpenAIChatMessage `json:"messages"`
	Stream   bool                `json:"stream,omitempty"`
	User     string              `json:"user,omitempty"`
}

// OpenAIChatMessage is one entry in the OpenAI messages array, and
// also the response message embedded in each OpenAIChoice.
//
// Refusal is a required key on the published
// ChatCompletionResponseMessage schema (nullable string); the
// translator never produces a model refusal, so it is always `null`
// on a response. It has no meaning on an inbound request message and
// is simply left unset (nil) there. spec: §15.1
// (OpenAICompletionsAdapter is OpenAI Chat Completions compatible).
type OpenAIChatMessage struct {
	Role    string  `json:"role"`
	Content string  `json:"content"`
	Name    string  `json:"name,omitempty"`
	Refusal *string `json:"refusal"`
}

// OpenAIChatCompletionsResponse is the non-streaming OpenAI response.
type OpenAIChatCompletionsResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []OpenAIChoice `json:"choices"`
	Usage   OpenAIUsage    `json:"usage"`
}

// OpenAIChoice is one choice in the response.
//
// Logprobs is always emitted as `null`: the translator never
// requests or computes per-token log probabilities. The field is a
// required key on the published OpenAI Chat Completions Choice
// schema (nullable, but the key must be present), so it is a pointer
// that marshals to JSON `null` rather than an omitted key. spec:
// §15.1 (OpenAICompletionsAdapter is OpenAI Chat Completions
// compatible).
type OpenAIChoice struct {
	Index        int               `json:"index"`
	Message      OpenAIChatMessage `json:"message"`
	FinishReason string            `json:"finish_reason"`
	Logprobs     *struct{}         `json:"logprobs"`
}

// OpenAIUsage is the usage block.
type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// OpenAIError is the OpenAI-compatible error envelope.
type OpenAIError struct {
	Error OpenAIErrorBody `json:"error"`
}

// OpenAIErrorBody carries the per-error fields.
type OpenAIErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
	Param   string `json:"param,omitempty"`
}

// OpenAIChatHandler is the §15.1 POST /v1/chat/completions handler.
// It maps the OpenAI request shape to a Lenny session message dispatch
// and re-shapes the executor response into OpenAI's expected format.
type OpenAIChatHandler struct {
	store          sessionstore.Store
	exec           executor.Executor
	clock          func() time.Time
	defaultRuntime string
	binder         SingleShotBinder
	releaseTimeout time.Duration
}

// OpenAIChatOptions configures OpenAIChatHandler.
type OpenAIChatOptions struct {
	// Clock overrides time.Now. Pass nil for production.
	Clock func() time.Time

	// IDFunc overrides the chatcmpl ID generator. Pass nil to use
	// the default crypto/rand-backed generator.
	IDFunc func() string

	// DefaultRuntime is the runtimeRef the translator pins on the
	// underlying Lenny session when the OpenAI `model` field does
	// not map to a registered runtime. Empty defaults to `echo` so
	// the in-memory gateway round-trips against the EchoExecutor.
	DefaultRuntime string

	// SingleShotBinder runs the shared create-and-start service that
	// claims a warm pod, launches the runtime, and registers the pod
	// binding for each request. Pass nil to fall back to the no-op
	// binder that persists a running row through store without claiming
	// a pod (the §17.4 in-memory mode and the in-process unit tests).
	// spec: §15 built-in adapter single-shot compute model.
	SingleShotBinder SingleShotBinder

	// ReleaseTimeout bounds the detached context the deferred pod release
	// runs under. Zero defaults to defaultSingleShotReleaseTimeout.
	// Operator-tunable so a deployment can widen or narrow the drain
	// window that survives a request-context timeout or client disconnect.
	ReleaseTimeout time.Duration
}

// NewOpenAIChatHandler returns a configured handler.
func NewOpenAIChatHandler(store sessionstore.Store, exec executor.Executor, opts OpenAIChatOptions) *OpenAIChatHandler {
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
	return &OpenAIChatHandler{
		store:          store,
		exec:           exec,
		clock:          clock,
		defaultRuntime: rt,
		binder:         binder,
		releaseTimeout: releaseTimeout,
	}
}

// Handler returns the http.Handler that routes the OpenAI endpoint.
func (h *OpenAIChatHandler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", h.handleCreateCompletion)
	return mux
}

func (h *OpenAIChatHandler) handleCreateCompletion(w http.ResponseWriter, r *http.Request) {
	if h.exec == nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "server_error",
			"gateway has no executor wired")
		return
	}
	// Cap the request body at 1 MiB to mirror the JSON body cap
	// applied to the §15.1 REST endpoints.
	body := http.MaxBytesReader(w, r.Body, 1<<20)
	defer body.Close()

	var req OpenAIChatCompletionsRequest
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			"request body is not valid JSON: "+err.Error())
		return
	}
	if len(req.Messages) == 0 {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			"messages must contain at least one entry")
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
	// through the shared §15.2.1 create-and-start service (the injected
	// binder), returning the session id for the dispatch. Against the
	// §17.4 in-memory wiring the no-op binder persists the row without
	// claiming a pod, so the one code path serves both.
	// spec: §15 built-in adapter single-shot compute model; §7.1 atomicity.
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
	// recording the §6.2 terminal disposition. disp starts failed and
	// flips to completed only after a successful dispatch and completion
	// update, so a dispatch error or request-context timeout records a
	// failed disposition. The release runs on a context detached from the
	// request context and bounded by a fresh timeout so the pod drain and
	// lease release survive a request-context timeout or client disconnect,
	// where r.Context() is already cancelled.
	// spec: §15 built-in adapter single-shot compute model; §6.2 release.
	disp := executor.DispositionFailed
	defer releaseSingleShot(r.Context(), h.exec, h.releaseTimeout, sessionID, &disp)

	// Translate the OpenAI messages array into executor messages.
	// System/user/assistant roles round-trip unchanged.
	msgs := make([]executor.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, executor.Message{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	sendResp, err := h.exec.Send(r.Context(), sessionID, msgs)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error",
			fmt.Sprintf("executor failure: %v", err))
		return
	}
	out := sendResp.Parts

	// Mark the session completed — the translator runs a single
	// request/response cycle and does not maintain conversation
	// state across calls.
	_, _ = h.store.Update(r.Context(), tenantID, sessionID, func(s *sessionstore.Session) error {
		s.State = session.StateCompleted
		return nil
	})
	disp = executor.DispositionCompleted

	if req.Stream {
		writeOpenAIStream(w, sessionID, runtimeRef, now, out)
		return
	}
	resp := buildOpenAIResponse(sessionID, runtimeRef, now, out)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// chatCompletionChunk is the OpenAI `chat.completion.chunk` SSE
// frame shape.
type chatCompletionChunk struct {
	ID      string                      `json:"id"`
	Object  string                      `json:"object"`
	Created int64                       `json:"created"`
	Model   string                      `json:"model"`
	Choices []chatCompletionChoiceDelta `json:"choices"`
}

type chatCompletionChoiceDelta struct {
	Index        int       `json:"index"`
	Delta        chatDelta `json:"delta"`
	FinishReason *string   `json:"finish_reason"`
}

type chatDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// writeOpenAIStream emits the OpenAI Chat Completions streaming
// response: an SSE sequence of `chat.completion.chunk` frames
// terminated by `data: [DONE]`. The synchronous executor produces a
// complete response; the translator chunks the text into
// whitespace-delimited deltas so streaming clients observe
// incremental output.
func writeOpenAIStream(w http.ResponseWriter, sessionID, model string, now time.Time, out []executor.MessagePart) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error",
			"response writer does not support streaming")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	id := "chatcmpl-" + sessionID
	emit := func(c chatCompletionChunk) {
		b, _ := json.Marshal(c)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	// First frame: the assistant role delta.
	emit(chatCompletionChunk{
		ID: id, Object: "chat.completion.chunk", Created: now.Unix(), Model: model,
		Choices: []chatCompletionChoiceDelta{{Index: 0, Delta: chatDelta{Role: "assistant"}}},
	})
	// Content frames: one per whitespace-delimited token.
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
		emit(chatCompletionChunk{
			ID: id, Object: "chat.completion.chunk", Created: now.Unix(), Model: model,
			Choices: []chatCompletionChoiceDelta{{Index: 0, Delta: chatDelta{Content: chunk}}},
		})
	}
	// Final frame: the finish_reason.
	stop := "stop"
	emit(chatCompletionChunk{
		ID: id, Object: "chat.completion.chunk", Created: now.Unix(), Model: model,
		Choices: []chatCompletionChoiceDelta{{Index: 0, Delta: chatDelta{}, FinishReason: &stop}},
	})
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// buildOpenAIResponse maps the executor output parts to an OpenAI
// chat completion response. Multi-part outputs are concatenated; the
// translator surfaces only the first `text` part directly. Future
// commits add tool_call → OpenAI tool_calls translation.
func buildOpenAIResponse(sessionID, model string, now time.Time, out []executor.MessagePart) OpenAIChatCompletionsResponse {
	text := ""
	for _, p := range out {
		if p.Type == "text" {
			if text != "" {
				text += "\n"
			}
			text += p.Text
		}
	}
	return OpenAIChatCompletionsResponse{
		ID:      "chatcmpl-" + sessionID,
		Object:  "chat.completion",
		Created: now.Unix(),
		Model:   model,
		Choices: []OpenAIChoice{{
			Index: 0,
			Message: OpenAIChatMessage{
				Role:    "assistant",
				Content: text,
			},
			FinishReason: "stop",
		}},
		Usage: OpenAIUsage{
			PromptTokens:     0,
			CompletionTokens: 0,
			TotalTokens:      0,
		},
	}
}

// writeOpenAIError emits the OpenAI-compatible error envelope with no
// machine-readable code and no Retry-After header.
func writeOpenAIError(w http.ResponseWriter, status int, typ, message string) {
	writeOpenAIErrorWithCode(w, status, typ, "", message, 0)
}

// writeOpenAIErrorWithCode emits the OpenAI-compatible error envelope,
// setting the machine-readable `error.code` field and, when
// retryAfterSeconds is positive, a Retry-After header. The built-in
// single-shot adapters map a *SingleShotError into this envelope so the
// carried code (SESSION_CREATION_FAILED, CREDENTIAL_POOL_EXHAUSTED, or an
// admission-gate code) and the retryable claim-exhaustion Retry-After reach
// the client, which the plain writeOpenAIError does not carry.
// spec: §15 built-in adapter single-shot compute model; §7.1 atomicity.
func writeOpenAIErrorWithCode(w http.ResponseWriter, status int, typ, code, message string, retryAfterSeconds int) {
	w.Header().Set("Content-Type", "application/json")
	if retryAfterSeconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(OpenAIError{
		Error: OpenAIErrorBody{
			Type:    typ,
			Message: message,
			Code:    code,
		},
	})
}

// writeSingleShotError maps a binder failure into the adapter's native
// error envelope. A *SingleShotError carries the sessionserver-classified
// HTTP status, error code, and (for the retryable pool-claim exhaustion
// case) Retry-After seconds; any other failure is an opaque 500
// server_error. The adapter fails closed rather than dispatching.
// spec: §15 built-in adapter single-shot compute model; §7.1 atomicity; §4.9.
func writeSingleShotError(w http.ResponseWriter, err error) {
	var sse *SingleShotError
	if errors.As(err, &sse) {
		writeOpenAIErrorWithCode(w, sse.HTTPStatus, openAIErrorType(sse.HTTPStatus),
			sse.Code, sse.Message, sse.RetryAfterSeconds)
		return
	}
	writeOpenAIError(w, http.StatusInternalServerError, "server_error",
		fmt.Sprintf("single-shot bind failed: %v", err))
}

// openAIErrorType maps an HTTP status onto the OpenAI error envelope's
// `type` discriminator: 429 is a rate-limit error, any 5xx is a server
// error, and everything else is treated as an invalid-request error.
func openAIErrorType(status int) string {
	switch {
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status >= http.StatusInternalServerError:
		return "server_error"
	default:
		return "invalid_request_error"
	}
}

// releaseSingleShot releases the single-shot pod and its §4.9 lease on a
// context detached from the request context, so the pod drain and lease
// release complete even when the request context is already cancelled (a
// request-context timeout or client disconnect). disp is read at defer time
// so it reflects the terminal disposition the handler set (completed on a
// successful turn, failed on a dispatch error or timeout). An ErrUnsupported
// release (the in-memory EchoExecutor, which is not a SessionReleaser) is
// tolerated.
// spec: §6.2 release; §15 built-in adapter single-shot compute model.
func releaseSingleShot(reqCtx context.Context, exec executor.Executor, timeout time.Duration, sessionID string, disp *executor.Disposition) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(reqCtx), timeout)
	defer cancel()
	if err := executor.ReleaseSession(ctx, exec, sessionID, *disp); err != nil && !errors.Is(err, executor.ErrUnsupported) {
		// Non-fatal; the gateway logs release failures in production.
		_ = err
	}
}

// resolveTenant reads the X-Lenny-Tenant-ID dev header, defaulting
// to "default" for single-tenant mode. Production resolves the
// tenant from the §10.2 Bearer token.
func resolveTenant(r *http.Request) string {
	if v := r.Header.Get("X-Lenny-Tenant-ID"); v != "" {
		return v
	}
	return "default"
}

// defaultChatID returns a fresh §12.6 UUIDv8 session identifier. The
// translator persists this as the session row id and echoes it to the
// client as the response id, so the id round-trips through Lenny's
// session store.
func defaultChatID() string {
	return session.NewID()
}
