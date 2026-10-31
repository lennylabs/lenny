// SPDX-License-Identifier: MIT

package translator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
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
// spec: §9.1; §15.1. F-9.1.6 / F-9.1.8.
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
	cont           *continuity
	forwarder      HolderForwarder
	terminal       SessionTerminal
	// devTenantHeader mirrors OpenResponsesOptions.DevTenantHeader.
	devTenantHeader bool
}

// OpenResponsesOptions configures the handler.
type OpenResponsesOptions struct {
	Clock          func() time.Time
	IDFunc         func() string
	DefaultRuntime string

	// SingleShotBinder runs the shared create-and-start service that
	// claims a warm pod, launches the runtime, and registers the pod
	// binding for each turn served cold, and reports whether the created
	// session is held after a turn that succeeds. Pass nil to fall back to
	// the no-op binder, which never holds a session (the §17.4 in-memory
	// mode and the in-process unit tests).
	// spec: §15 built-in adapter single-shot compute model; §15 (Open
	// Responses conversation sessions).
	SingleShotBinder SingleShotBinder

	// ReleaseTimeout bounds the detached context the deferred pod release
	// runs under. Zero defaults to defaultSingleShotReleaseTimeout.
	// Operator-tunable.
	ReleaseTimeout time.Duration

	// PrimaryReadTimeout bounds the primary read of the session row a
	// response id names. A value at or below zero selects
	// sessionstore.DefaultPrimaryReadTimeout. The gateway sets it from
	// --session-primary-read-timeout.
	// spec: §15 (Open Responses conversation sessions); §12.3 (Postgres HA
	// Requirements).
	PrimaryReadTimeout time.Duration

	// Transcripts is the §15.1 transcript registry the adapter records each
	// turn into and reads on a cold continuation to rehydrate the
	// conversation context onto the freshly-claimed pod. Pass nil only in
	// the in-process unit tests: record then appends nothing and returns end
	// sequence 0, and rehydrate still resolves previous_response_id
	// fail-closed against the session store but contributes no entries.
	// spec: §15 (Open Responses conversation sessions).
	Transcripts transcriptstore.Store

	// HolderForwarder forwards a warm turn to the coordinating replica of
	// the session that holds its pod, and reports whether a request arrived
	// forwarded. It is required: the gateway wires the session server's
	// forwarder, which reports a turn local on the replica that coordinates
	// the session, and NewOpenResponsesHandler panics on nil. There is no
	// local stand-in, so a wiring that omits it fails at construction
	// rather than serving forwarded or off-holder turns locally.
	// spec: §29.3 (Interactive message send) Forwarding.
	HolderForwarder HolderForwarder

	// Terminal opens a session's terminal claim and runs its post-terminal
	// hook when a turn fails and the handler ends the session. It is
	// required: the gateway wires the session server, and
	// NewOpenResponsesHandler panics on nil, so a failed held session is
	// never ended outside its terminal claim or without the post-terminal
	// hook.
	// spec: §15 (Open Responses conversation sessions); §7.2.
	Terminal SessionTerminal

	// SessionAccess decides whether the request principal may read the
	// session a response id names. GET /v1/responses/{id}, the resolution of
	// previous_response_id, and every hop of a cold continuation's lineage
	// walk ask it, and a session it refuses answers as an unknown id does.
	// The gateway wires the session server. A nil SessionAccess refuses
	// every session, so a wiring that omits it serves no continuation.
	// spec: §10.2 (Session ownership); §15 (Open Responses conversation
	// sessions).
	SessionAccess SessionAccess

	// DevTenantHeader, when true, lets a request with no authenticated
	// principal name its tenant through the X-Lenny-Tenant-ID dev header
	// and resolves a request with neither to `default`. The gateway sets
	// it only under --dev-mode. When false, the default, the tenant comes
	// only from the principal and a request without one is refused with
	// 401 before any session is created or read (F-10.2.21).
	// spec: §10.2 (Authentication); §17.4 (dev mode).
	DevTenantHeader bool
}

// NewOpenResponsesHandler returns a configured handler. It panics when
// opts.HolderForwarder or opts.Terminal is nil, because both are always wired
// and the handler has no mode that runs without them.
// spec: §15 (Open Responses conversation sessions); §29.3 (Interactive
// message send) Forwarding.
func NewOpenResponsesHandler(store sessionstore.Store, exec executor.Executor, opts OpenResponsesOptions) *OpenResponsesHandler {
	if opts.HolderForwarder == nil {
		panic("translator: NewOpenResponsesHandler: OpenResponsesOptions.HolderForwarder is required")
	}
	if opts.Terminal == nil {
		panic("translator: NewOpenResponsesHandler: OpenResponsesOptions.Terminal is required")
	}
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
		forwarder:      opts.HolderForwarder,
		terminal:       opts.Terminal,
		// The continuity helper tolerates a nil transcripts backing; response
		// resolution always reads the session store.
		// spec: §15 (Open Responses conversation sessions).
		cont: &continuity{
			sessions:           store,
			transcripts:        opts.Transcripts,
			primaryReadTimeout: opts.PrimaryReadTimeout,
			access:             opts.SessionAccess,
		},
		devTenantHeader: opts.DevTenantHeader,
	}
}

// Handler returns the http.Handler routing /v1/responses.
//
// The adapter serves create and read only. Each response is a record on its
// conversation session's row, which audit and billing rely on, so no route
// deletes it; a DELETE on /v1/responses/{id} reaches the ServeMux's method
// mismatch and answers 405 with an Allow header naming GET.
// spec: §15 (Open Responses conversation sessions), §15.1 (REST API)
func (h *OpenResponsesHandler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/responses", h.handleCreate)
	mux.HandleFunc("GET /v1/responses/{id}", h.handleGet)
	return mux
}

// responsesTurn is one parsed POST /v1/responses request: the decoded body,
// the raw body a forward relays, the normalized input, the caller's tenant,
// and the environment and runtime ref the model names.
type responsesTurn struct {
	req        OpenResponsesRequest
	body       []byte
	msgs       []executor.Message
	tenantID   string
	subject    string
	envScope   string
	runtimeRef string
	now        time.Time
}

// errWarmLost marks a reservation whose re-check under the row lock failed a
// warm condition. The mutator writes nothing and the turn goes cold.
// spec: §15 (Open Responses conversation sessions).
var errWarmLost = errors.New("open responses: warm conditions no longer hold")

// errGenerationAdvanced marks a completion write whose session's
// recovery_generation is no longer the value captured for the turn.
// spec: §15 (Open Responses conversation sessions).
var errGenerationAdvanced = errors.New("open responses: recovery generation advanced")

// handleCreate serves POST /v1/responses in a fixed order: resolve, then
// forward, then reserve, then dispatch. It resolves previous_response_id
// before any store write, bind, or forward, so an unresolvable id answers the
// native 404 with no side effect. A continuation that meets the warm
// conditions, and every request that arrived forwarded from another replica,
// goes to the holder forwarder, which serves it at the coordinating replica
// or refuses it with the native 409; neither falls through to a local path.
// A local warm-eligible turn runs on the held session, and every other turn,
// including a warm turn that loses its reservation or whose dispatch the
// executor refuses before delivery, runs cold on a new session.
// spec: §15 (Open Responses conversation sessions); §29.3 (Interactive
// message send) Forwarding.
func (h *OpenResponsesHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	if h.exec == nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "server_error",
			"gateway has no executor wired")
		return
	}
	t, ok := h.parseTurn(w, r)
	if !ok {
		return
	}
	if t.req.PreviousResponseID == "" {
		h.serveCold(w, r, t, nil)
		return
	}
	row, n, err := h.cont.resolve(r.Context(), t.tenantID, t.req.PreviousResponseID)
	if err != nil {
		writeResolveError(w, err)
		return
	}
	warm := warmEligible(row, n, t)
	if warm || h.forwarder.Forwarded(r.Context()) {
		if !h.forwarder.ForwardResponsesTurn(w, r, row, t.body, writeTargetNotReady) {
			return
		}
	}
	if warm && h.serveWarm(w, r, t, row, n) {
		return
	}
	h.serveCold(w, r, t, &row)
}

// parseTurn reads and validates the request body and resolves the caller's
// tenant and the runtime the model names. It writes the native 400 or 401
// and reports false on a request it rejects.
func (h *OpenResponsesHandler) parseTurn(w http.ResponseWriter, r *http.Request) (responsesTurn, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			"request body could not be read: "+err.Error())
		return responsesTurn{}, false
	}
	t := responsesTurn{body: body}
	if err := json.Unmarshal(body, &t.req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			"request body is not valid JSON: "+err.Error())
		return responsesTurn{}, false
	}
	if t.req.Input == nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			"input is required")
		return responsesTurn{}, false
	}
	t.msgs, err = normalizeInput(t.req.Input)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return responsesTurn{}, false
	}
	tenantID, ok := resolveTenant(r, h.devTenantHeader)
	if !ok {
		writeUnauthenticated(w)
		return responsesTurn{}, false
	}
	t.tenantID = tenantID
	t.subject = principalSubject(r.Context())
	// spec: §10.6 — a model named "environments/{name}/{model}"
	// scopes the implicit session to the named environment; the bare
	// model is the runtime reference. F-10.6.11.
	t.envScope, t.runtimeRef = splitEnvModel(t.req.Model)
	if t.runtimeRef == "" {
		t.runtimeRef = h.defaultRuntime
	}
	t.now = h.clock()
	return t, true
}

// writeResolveError maps a previous_response_id resolution failure to the
// native 404 for an id that names no completed response of the caller's
// tenant, and to a native 500 for any other store error.
// spec: §15 (Open Responses conversation sessions); §4.2 session-store tenant isolation.
func writeResolveError(w http.ResponseWriter, err error) {
	if errors.Is(err, errContinuationNotFound) {
		writeOpenAIError(w, http.StatusNotFound, "not_found_error",
			"previous_response_id not found")
		return
	}
	writeOpenAIError(w, http.StatusInternalServerError, "server_error",
		fmt.Sprintf("continuation resolution failed: %v", err))
}

// warmEligible reports whether a continuation of response n on row meets the
// warm conditions: the session is running, response n is its newest response
// and has completed, the session's recovery_generation equals the value
// recorded for response n, the request principal is the session's owner, and
// the request asks for the runtime and environment the creating request asked
// for. The adapter's `user` request field plays no part: a principal that may
// read another user's session continues it cold, on a session it owns. The runtime comparison reads
// RequestedRuntimeRef, never RuntimeRef, which session creation rewrites.
// spec: §15 (Open Responses conversation sessions); §10.2 (Session
// ownership).
func warmEligible(row sessionstore.Session, n int, t responsesTurn) bool {
	if row.State != session.StateRunning || n != len(row.Responses) || n < 1 {
		return false
	}
	head := row.Responses[n-1]
	return head.Completed &&
		row.RecoveryGeneration == head.RecoveryGeneration &&
		t.subject != "" && row.UserID == t.subject &&
		row.RequestedRuntimeRef == t.runtimeRef &&
		row.Environment == t.envScope
}

// serveWarm runs a continuation of response n on the held session row. It
// reserves ordinal n+1 in one write that re-checks the warm conditions under
// the row lock and captures the session's recovery_generation, sends only
// the new input, records the turn, and completes the response in one write
// conditioned on the captured generation. It reports false, having written
// nothing to the client, when the turn goes cold instead: the reservation
// lost its re-check, or the executor refused the dispatch before any input
// reached the runtime. A reserved response is never withdrawn. Every failure
// after input may have reached the runtime ends the session and answers the
// native 500.
// spec: §15 (Open Responses conversation sessions).
func (h *OpenResponsesHandler) serveWarm(w http.ResponseWriter, r *http.Request, t responsesTurn, row sessionstore.Session, n int) bool {
	ctx := r.Context()
	resGen, err := h.reserve(ctx, t, row.ID, n)
	if err != nil {
		return false
	}
	sendResp, err := h.exec.Send(ctx, row.ID, t.msgs)
	if notDelivered(err) {
		return false
	}
	if err != nil {
		h.failTurn(w, r, t.tenantID, row.ID, fmt.Errorf("executor failure: %w", err))
		return true
	}
	endSeq, err := h.cont.record(ctx, t.tenantID, row.ID, t.msgs, sendResp.Parts)
	if err != nil {
		h.failTurn(w, r, t.tenantID, row.ID, fmt.Errorf("transcript record failed: %w", err))
		return true
	}
	if err := h.commitWarm(ctx, t.tenantID, row.ID, n+1, resGen, endSeq); err != nil {
		h.failTurn(w, r, t.tenantID, row.ID, fmt.Errorf("response completion failed: %w", err))
		return true
	}
	writeResponsesTurn(w, t, formatResponseID(row.ID, n+1), sendResp.Parts)
	return true
}

// notDelivered reports whether a Send error means no input reached the
// runtime: the replica holds no binding for the session, or the adapter
// refused the batch as sealed before delivering any of it.
// spec: §15 (Open Responses conversation sessions); §4.7.1 Session seal.
func notDelivered(err error) bool {
	if errors.Is(err, executor.ErrNotBound) {
		return true
	}
	var sealed *executor.SealedError
	return errors.As(err, &sealed) && sealed.Delivered == 0
}

// reserve appends the not-completed record of ordinal n+1 to the session in
// one write whose mutator re-checks the warm conditions on the locked row,
// and returns the recovery_generation the record captured. A failed re-check
// returns errWarmLost and writes nothing.
// spec: §15 (Open Responses conversation sessions).
func (h *OpenResponsesHandler) reserve(ctx context.Context, t responsesTurn, sessionID string, n int) (int64, error) {
	var resGen int64
	_, err := h.store.Update(ctx, t.tenantID, sessionID, func(s *sessionstore.Session) error {
		if !warmEligible(*s, n, t) {
			return errWarmLost
		}
		resGen = s.RecoveryGeneration
		s.Responses = append(s.Responses, sessionstore.ConversationResponse{
			CreatedAt:          t.now,
			RecoveryGeneration: resGen,
		})
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("reserve response %d on session %s: %w", n+1, sessionID, err)
	}
	return resGen, nil
}

// commitWarm completes the reserved response ordinal in one write that takes
// effect only while the session's recovery_generation equals resGen. It never
// rewrites the record's RecoveryGeneration.
// spec: §15 (Open Responses conversation sessions).
func (h *OpenResponsesHandler) commitWarm(ctx context.Context, tenantID, sessionID string, ordinal int, resGen int64, endSeq uint64) error {
	_, err := h.store.Update(ctx, tenantID, sessionID, func(s *sessionstore.Session) error {
		if s.RecoveryGeneration != resGen {
			return errGenerationAdvanced
		}
		if ordinal > len(s.Responses) {
			return fmt.Errorf("response %d is not reserved", ordinal)
		}
		s.Responses[ordinal-1].EndSeq = endSeq
		s.Responses[ordinal-1].Completed = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("commit response %d on session %s: %w", ordinal, sessionID, err)
	}
	return nil
}

// serveCold runs a turn on a new session. On a continuation of response
// head it assembles the conversation context before the bind, so a lineage
// that no longer resolves answers without claiming a pod. It binds through
// the shared create-and-start service, sends the context ahead of the new
// input, records the turn, and completes the session's first response in one
// write conditioned on the recovery_generation at bind, recording the
// continuation parent and the requested runtime. A session the binder does
// not hold is written completed in that same write and released with the
// completed disposition; a held session stays running with its pod and
// lease. Every failure after the bind ends the session through
// endHeldSession, the only release on a failed exit, and answers the native
// 500.
//
// The single-shot path never threads previous_response_id into
// ParentSessionID, which the §8.2/§8.6 delegated-child lease machinery and
// the delegation-tree orphan-cleanup sweep own; the lineage is
// ContinuationParentID.
// spec: §15 (Open Responses conversation sessions); §15 (Built-in adapter
// single-shot compute model); §7.1 atomicity; §8.2 delegated-child lease.
func (h *OpenResponsesHandler) serveCold(w http.ResponseWriter, r *http.Request, t responsesTurn, head *sessionstore.Session) {
	ctx := r.Context()
	var prior []executor.Message
	if head != nil {
		var err error
		if prior, err = h.cont.rehydrate(ctx, t.tenantID, t.req.PreviousResponseID); err != nil {
			writeResolveError(w, err)
			return
		}
	}
	b, err := h.binder.BindSingleShot(ctx, SingleShotSpec{
		TenantID:    t.tenantID,
		RuntimeRef:  t.runtimeRef,
		Environment: t.envScope,
	})
	if err != nil {
		writeSingleShotError(w, err)
		return
	}
	sendResp, err := h.exec.Send(ctx, b.SessionID, append(prior, t.msgs...))
	if err != nil {
		h.failTurn(w, r, t.tenantID, b.SessionID, fmt.Errorf("executor failure: %w", err))
		return
	}
	endSeq, err := h.cont.record(ctx, t.tenantID, b.SessionID, t.msgs, sendResp.Parts)
	if err != nil {
		h.failTurn(w, r, t.tenantID, b.SessionID, fmt.Errorf("transcript record failed: %w", err))
		return
	}
	completedAt := h.clock()
	// completed reports whether this commit applied the completed terminal
	// write. It is reset on every mutator run so a retried mutator reports
	// only its own outcome.
	completed := false
	_, err = h.store.Update(ctx, t.tenantID, b.SessionID, func(s *sessionstore.Session) error {
		completed = false
		if s.RecoveryGeneration != b.RecoveryGeneration {
			return errGenerationAdvanced
		}
		s.Responses = []sessionstore.ConversationResponse{{
			CreatedAt:          t.now,
			EndSeq:             endSeq,
			RecoveryGeneration: b.RecoveryGeneration,
			Completed:          true,
		}}
		s.ContinuationParentID = t.req.PreviousResponseID
		s.RequestedRuntimeRef = t.runtimeRef
		if b.Hold || session.IsTerminal(s.State) {
			return nil
		}
		// spec: §7.2, §8.8 — the completion records the Terminated fact in
		// its own write. It runs no post-terminal hook: the release below
		// releases the executor.
		from := s.State
		s.State = session.StateCompleted
		sessionstore.MarkTerminated(s, from, completedAt)
		completed = true
		return nil
	})
	if err != nil {
		h.failTurn(w, r, t.tenantID, b.SessionID,
			fmt.Errorf("response completion failed on session %s: %w", b.SessionID, err))
		return
	}
	// A row another writer already ended was released by that writer's
	// post-terminal hook, so the handler releases only a session whose
	// completion it wrote itself; a second release would release the pod
	// twice, under a disposition that contradicts the row.
	// spec: §15 (Open Responses conversation sessions); §6.2 release.
	if completed {
		disp := executor.DispositionCompleted
		releaseSingleShot(ctx, h.release(), t.tenantID, b.SessionID, &disp)
	}
	writeResponsesTurn(w, t, formatResponseID(b.SessionID, 1), sendResp.Parts)
}

// failTurn ends the session of a turn that failed after its input may have
// reached the runtime and answers the native 500. The response stays not
// completed.
// spec: §15 (Open Responses conversation sessions).
func (h *OpenResponsesHandler) failTurn(w http.ResponseWriter, r *http.Request, tenantID, sessionID string, cause error) {
	h.endHeldSession(r.Context(), tenantID, sessionID)
	writeOpenAIError(w, http.StatusInternalServerError, "server_error", cause.Error())
}

// endHeldSession ends a session whose turn failed with the failed
// disposition, releasing its pod and lease before the response returns. It
// holds the session's terminal claim for its whole run and writes failed in
// one Update that is superseded on a row another writer already ended. The
// write and the hook run on a context detached from the request and bounded
// by the release timeout, so a client disconnect does not abandon them. On
// an applied write it runs the post-terminal hook inline, which releases the
// pod and lease. A superseded write leaves the release to the writer that
// ended the row. Any other write error runs no hook, so it releases the
// executor binding with the failed disposition itself, once. That includes a
// write that finds no row: a §12.8 erasure that deleted the row releases the
// binding only when its CH-BINDRELEASE publish reaches this replica, and a
// lost publish is not redelivered, so this release is the local backstop. When
// the publish did arrive, the executor no longer holds the binding and its
// idempotent Release makes this call a no-op.
// spec: §15 (Open Responses conversation sessions); §7.2 (Interactive
// Session Model); §6.2 release.
func (h *OpenResponsesHandler) endHeldSession(reqCtx context.Context, tenantID, sessionID string) {
	end := h.terminal.BeginTerminal(sessionID)
	defer end()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(reqCtx), h.releaseTimeout)
	defer cancel()
	var from session.State
	updated, err := h.store.Update(ctx, tenantID, sessionID, func(s *sessionstore.Session) error {
		if session.IsTerminal(s.State) {
			return sessionstore.ErrSuperseded
		}
		from = s.State
		s.State = session.StateFailed
		sessionstore.MarkTerminated(s, from, h.clock())
		return nil
	})
	switch {
	case err == nil:
		h.terminal.AfterTerminalWrite(ctx, from, updated, nil)
	case errors.Is(err, sessionstore.ErrSuperseded):
	default:
		disp := executor.DispositionFailed
		releaseSingleShot(ctx, h.release(), tenantID, sessionID, &disp)
	}
}

// release returns the executor, store, clock, and timeout a single-shot
// release runs with.
func (h *OpenResponsesHandler) release() singleShotRelease {
	return singleShotRelease{exec: h.exec, store: h.store, clock: h.clock, timeout: h.releaseTimeout}
}

// writeResponsesTurn writes a completed turn's response with id, in the
// streaming form when the request asked for it.
func writeResponsesTurn(w http.ResponseWriter, t responsesTurn, id string, out []executor.MessagePart) {
	if t.req.Stream {
		writeOpenResponsesStream(w, id, t.req.PreviousResponseID, t.now, out)
		return
	}
	resp := buildOpenResponsesResponse(id, t.runtimeRef, t.req.PreviousResponseID, t.now, out)
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

// handleGet answers GET /v1/responses/{id} with a completed response of a
// session in the caller's tenant. The id resolves through the same step a
// continuation's previous_response_id resolves through, so an id that names
// no completed response the caller can read (malformed, unknown,
// cross-tenant, past the newest ordinal, or not completed) answers the
// native 404. previous_response_id echoes the preceding ordinal on the same
// session, or, for ordinal 1, the response the session's first turn
// continued from.
// spec: §15 (Open Responses conversation sessions); §4.2 (session-store
// tenant isolation).
func (h *OpenResponsesHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := resolveTenant(r, h.devTenantHeader)
	if !ok {
		writeUnauthenticated(w)
		return
	}
	row, n, err := h.cont.resolve(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, errContinuationNotFound) {
			writeOpenAIError(w, http.StatusNotFound, "not_found_error", "response not found")
			return
		}
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	prev := row.ContinuationParentID
	if n > 1 {
		prev = formatResponseID(row.ID, n-1)
	}
	// The gateway does not persist the raw model output, so the response
	// carries metadata only.
	resp := buildOpenResponsesResponse(formatResponseID(row.ID, n), row.RuntimeRef, prev,
		row.Responses[n-1].CreatedAt, nil)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
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
