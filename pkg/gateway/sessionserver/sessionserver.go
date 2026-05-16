// SPDX-License-Identifier: MIT

// Package sessionserver implements the §15.1 REST session endpoints
// as an http.Handler. The handler is backed by a sessionstore.Store
// and uses pkg/api/v1/session.Validate to enforce the §15.1
// precondition table on every state-mutating endpoint.
//
// This is the minimal Lenny gateway: no auth, no Postgres, no
// Kubernetes. The tenant_id is taken from a development header
// (X-Lenny-Tenant-ID) or, when absent, defaults to "default" — the
// single-tenant mode from §10.2. Future phases swap in the OIDC
// middleware that produces a validated tenant via pkg/auth.
//
// The handler implements the §15.1 endpoints that drive the
// session lifecycle state machine (create, finalize, start,
// interrupt, terminate, resume, derive, delete, list, get).
// Upload, message-injection, derive-failure auditing, and the
// elicitation/respond / tool-call approve paths are deferred to the
// phases that ship workspace materialisation, the inter-session
// inbox, and the elicitation chain.
package sessionserver

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/usagestore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// getPrincipal exposes the auth middleware's Principal lookup so the
// session handlers stay decoupled from the middleware package's
// internal context key naming.
func getPrincipal(r *http.Request) (authmw.Principal, bool) {
	return authmw.FromContext(r.Context())
}

// authValidateTenantID re-exports auth.ValidateTenantID under a name
// that does not collide with the local `auth` middleware alias.
func authValidateTenantID(s string) error { return auth.ValidateTenantID(s) }

// MaxJSONBodyBytes is the platform cap on JSON request bodies for
// every endpoint that decodes JSON (create, derive, extend-retention,
// admin mutations). Spec §13.4 fixes the per-archive ceilings; this
// constant covers the smaller per-request control plane and
// matches the typical CRD admission body size. 1 MiB is well above
// realistic envelopes (a populous workspacePlan is ~32 KiB) while
// preventing memory-exhaustion DoS on the gateway.
const MaxJSONBodyBytes int64 = 1024 * 1024

// jsonReader returns r.Body wrapped in http.MaxBytesReader so JSON
// decoders see io.EOF / *http.MaxBytesError on oversize inputs
// before any allocation. Handlers using json.Decoder must wrap their
// body with this helper.
func jsonReader(w http.ResponseWriter, r *http.Request) interface {
	Read(p []byte) (int, error)
	Close() error
} {
	return http.MaxBytesReader(w, r.Body, MaxJSONBodyBytes)
}

// Server is the §15.1 session HTTP handler.
type Server struct {
	store           sessionstore.Store
	clock           func() time.Time
	idFn            func() string
	deriveAuditSink DeriveAuditSink
	uploadIssuer    *uploadtoken.Issuer
	uploadVerifier  *uploadtoken.Verifier
	blobs           blobstore.Store
	executor        executor.Executor
	transcripts     transcriptstore.Store
	events          *events.Bus
	interactions    interactionstore.Store
	usage           usagestore.Store
	users           userstore.Store
	billing         billingstore.Store
	tenants         tenantstore.Store
	storageQuota    storagequota.Counter
	defaultIsoProf  isolation.Profile
	podBinder       *podsession.Binder
	podRegistry     *podsession.Registry
	agentNamespace  string
	sealer          Sealer
	treeArchive     treearchive.Store
	maxOrphanTasks  int
	evals           evalstore.Store
	experiments     experimentstore.Store
}

// DefaultMaxOrphanTasksPerTenant is the §8.10 cap on a tenant's active
// orphan tasks. When a `detach` cascade would push the tenant over the
// cap, the gateway falls back to `cancel_all` so orphans cannot
// accumulate without bound.
const DefaultMaxOrphanTasksPerTenant = 100

// Sealer takes the §7.1 final workspace snapshot of a session that has
// reached a terminal state. The gateway invokes it as the
// seal-and-export step of session completion.
type Sealer interface {
	// Seal snapshots the session's final workspace. An implementation
	// is expected to no-op for a session that never ran on a pod.
	Seal(ctx context.Context, tenantID, sessionID string) error
}

// Options configures the Server at construction.
type Options struct {
	// Clock overrides time.Now. Tests inject a fixed clock; production
	// leaves this nil.
	Clock func() time.Time

	// IDFunc overrides the session-id generator. Tests inject a
	// deterministic generator; production leaves this nil and the
	// server uses a crypto/rand-backed hex generator.
	IDFunc func() string

	// DeriveAuditSink, when set, receives the
	// `derive.isolation_downgrade` audit event per §7.1 derive rule 5
	// whenever a platform-admin exercises the
	// `allowIsolationDowngrade: true` override. Production wires this
	// to the §11.7 audit pipeline; nil disables the emission (and the
	// override still applies).
	DeriveAuditSink DeriveAuditSink

	// UploadTokenIssuer mints the §7.1 uploadToken stamped on every
	// successful POST /v1/sessions response. When nil, the server
	// constructs a default issuer backed by a freshly-generated
	// random key — production callers always supply their own issuer
	// so tokens survive a process restart.
	UploadTokenIssuer *uploadtoken.Issuer

	// UploadTokenVerifier validates the X-Lenny-Upload-Token header
	// on POST /v1/sessions/{id}/upload calls. When nil, the upload
	// handler skips validation — useful only in tests that pre-create
	// session rows directly. Production wires this to the same
	// KeyRing that backs UploadTokenIssuer.
	UploadTokenVerifier *uploadtoken.Verifier

	// Blobs is the §4.5 blob store backing
	// `POST /v1/sessions/{id}/upload` and `GET /v1/blobs/{ref}`.
	// When nil the upload + blob handlers return
	// `503 BLOBSTORE_UNAVAILABLE`.
	Blobs blobstore.Store

	// Executor routes session messages to a runtime. When nil the
	// /v1/sessions/{id}/messages handler returns
	// `503 EXECUTOR_UNAVAILABLE`. The minimal gateway wires an
	// in-process echo executor; production swaps in the
	// adapter-protocol-backed executor that dispatches to claimed
	// pods.
	Executor executor.Executor

	// Transcripts records the §15.1 session conversation history.
	// When nil, message injection still works but
	// `GET /v1/sessions/{id}/transcript` returns
	// `404 RESOURCE_NOT_FOUND` for every session.
	Transcripts transcriptstore.Store

	// Events is the §15.1 session event bus backing the SSE stream.
	// When nil, `GET /v1/sessions/{id}/events` returns
	// `503 EVENT_STREAM_UNAVAILABLE` and message injection skips
	// event publication.
	Events *events.Bus

	// Interactions is the §6/§9.2 pending tool-call + elicitation
	// store backing the §15.1 tool-use and elicitation endpoints.
	// When nil those endpoints return
	// `503 INTERACTIONS_UNAVAILABLE`.
	Interactions interactionstore.Store

	// Evals is the §10.7 built-in eval-result store backing
	// POST /v1/sessions/{id}/eval. When nil the endpoint returns
	// `503 EVAL_UNAVAILABLE`.
	Evals evalstore.Store

	// Experiments is the §10.7 experiment registry. When set, the
	// ExperimentRouter assigns a variant at session creation; when nil
	// no session is enrolled in an experiment.
	Experiments experimentstore.Store

	// Usage is the §15.1 usage / metering accumulator. When set, the
	// gateway records a session-created event on create and the
	// `GET /v1/usage` endpoint serves the aggregated report. Nil
	// disables metering (GET /v1/usage returns an empty report).
	Usage usagestore.Store

	// DefaultIsolationProfile is the §5.3 fallback profile applied to
	// a session whose pool resolution did not name one. When unset
	// the server uses isolation.Default() (sandboxed/gVisor) per §5.3.
	DefaultIsolationProfile isolation.Profile

	// Users is the §10.2 user registry consulted to enforce §11.4 user
	// invalidation on the session-creation path: a soft-disabled,
	// hard-disabled, or fully-revoked user is denied new sessions.
	// When nil the check is skipped (unit tests that do not provision
	// a user registry); the gateway always wires it.
	Users userstore.Store

	// Billing is the §11.2.1 billing event ledger. When set, the
	// gateway appends a session.created event on every create. Nil
	// disables billing emission.
	Billing billingstore.Store

	// Tenants is the tenant registry consulted to enforce the §11.2
	// per-tenant concurrent-session quota. When nil the quota check is
	// skipped; the gateway always wires it.
	Tenants tenantstore.Store

	// StorageQuota is the §11.2 per-tenant storage byte counter. When
	// set, the upload handler reserves the declared upload size against
	// the tenant's storageQuotaBytes limit. Nil disables the storage
	// quota.
	StorageQuota storagequota.Counter

	// PodBinder, when set, makes the §15.1 start path place each session
	// on a Kubernetes warm pod: it resolves the pool, claims a pod, and
	// starts the session on the pod's §4.7 adapter. Nil keeps the
	// gateway on the in-process executor.
	PodBinder *podsession.Binder

	// PodRegistry holds the per-session pod bindings the message and
	// teardown paths read. Required when PodBinder is set.
	PodRegistry *podsession.Registry

	// AgentNamespace is the namespace the warm pools and Sandboxes live
	// in. Required when PodBinder is set.
	AgentNamespace string

	// Sealer, when set, takes the §7.1 final workspace snapshot when a
	// session reaches a terminal state. Nil disables seal-and-export.
	Sealer Sealer

	// TreeArchive, when set, receives a §8.10 archive record for every
	// child session (a session with a parent) that reaches a terminal
	// state, so a resumed parent can replay the outcome. Nil disables
	// delegation-tree archiving.
	TreeArchive treearchive.Store

	// MaxOrphanTasksPerTenant caps a tenant's active orphan tasks per
	// §8.10. A non-positive value selects DefaultMaxOrphanTasksPerTenant.
	MaxOrphanTasksPerTenant int
}

// New returns a Server bound to the supplied store.
func New(store sessionstore.Store, opts Options) *Server {
	s := &Server{
		store:           store,
		clock:           opts.Clock,
		idFn:            opts.IDFunc,
		deriveAuditSink: opts.DeriveAuditSink,
		uploadIssuer:    opts.UploadTokenIssuer,
		uploadVerifier:  opts.UploadTokenVerifier,
		blobs:           opts.Blobs,
		executor:        opts.Executor,
		transcripts:     opts.Transcripts,
		evals:           opts.Evals,
		experiments:     opts.Experiments,
		events:          opts.Events,
		interactions:    opts.Interactions,
		usage:           opts.Usage,
		users:           opts.Users,
		billing:         opts.Billing,
		tenants:         opts.Tenants,
		storageQuota:    opts.StorageQuota,
		defaultIsoProf:  opts.DefaultIsolationProfile,
		podBinder:       opts.PodBinder,
		podRegistry:     opts.PodRegistry,
		agentNamespace:  opts.AgentNamespace,
		sealer:          opts.Sealer,
		treeArchive:     opts.TreeArchive,
		maxOrphanTasks:  opts.MaxOrphanTasksPerTenant,
	}
	if s.clock == nil {
		s.clock = func() time.Time { return time.Now().UTC() }
	}
	if s.idFn == nil {
		s.idFn = randomSessionID
	}
	if s.maxOrphanTasks <= 0 {
		s.maxOrphanTasks = DefaultMaxOrphanTasksPerTenant
	}
	if s.uploadIssuer == nil {
		// Default to a freshly-generated random key so the server is
		// useful in tests. Production callers always wire their own
		// keyring with the §7.1 rotation timers.
		var seed [32]byte
		_, _ = rand.Read(seed[:])
		ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{
			KeyID:  "default",
			Secret: seed[:],
		})
		s.uploadIssuer = uploadtoken.NewIssuer(ring, s.clock)
	}
	if !isolation.IsValid(s.defaultIsoProf) {
		s.defaultIsoProf = isolation.Default()
	}
	return s
}

// Handler returns the http.Handler that routes the §15.1 session
// endpoints.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sessions", s.handleCreate)
	mux.HandleFunc("POST /v1/sessions/start", s.handleCreateAndStart)
	mux.HandleFunc("GET /v1/sessions", s.handleList)
	mux.HandleFunc("GET /v1/sessions/{id}", s.handleGet)
	mux.HandleFunc("DELETE /v1/sessions/{id}", s.handleDelete)
	mux.HandleFunc("POST /v1/sessions/{id}/finalize", s.handleFinalize)
	mux.HandleFunc("POST /v1/sessions/{id}/start", s.handleStart)
	mux.HandleFunc("POST /v1/sessions/{id}/interrupt", s.handleTransition(session.EndpointInterrupt, transitionInterrupt))
	mux.HandleFunc("POST /v1/sessions/{id}/terminate", s.handleTransition(session.EndpointTerminate, transitionTerminate))
	mux.HandleFunc("POST /v1/sessions/{id}/resume", s.handleResume)
	mux.HandleFunc("POST /v1/sessions/{id}/derive", s.handleDerive)
	mux.HandleFunc("POST /v1/sessions/{id}/replay", s.handleReplay)
	mux.HandleFunc("POST /v1/sessions/{id}/extend-retention", s.handleExtendRetention)
	mux.HandleFunc("POST /v1/sessions/{id}/eval", s.handleEval)
	mux.HandleFunc("POST /v1/sessions/{id}/upload", s.handleUpload)
	mux.HandleFunc("POST /v1/sessions/{id}/messages", s.handleMessages)
	mux.HandleFunc("GET /v1/sessions/{id}/transcript", s.handleTranscript)
	mux.HandleFunc("GET /v1/sessions/{id}/tree", s.handleTree)
	mux.HandleFunc("GET /v1/usage", s.handleUsage)
	mux.HandleFunc("GET /v1/metering/events", s.handleMeteringEvents)
	mux.HandleFunc("GET /v1/sessions/{id}/events", s.handleEvents)
	mux.HandleFunc("POST /v1/sessions/{id}/tool-use/{tool_call_id}/approve", s.handleToolUseApprove)
	mux.HandleFunc("POST /v1/sessions/{id}/tool-use/{tool_call_id}/deny", s.handleToolUseDeny)
	mux.HandleFunc("POST /v1/sessions/{id}/elicitations/{elicitation_id}/respond", s.handleElicitationRespond)
	mux.HandleFunc("POST /v1/sessions/{id}/elicitations/{elicitation_id}/dismiss", s.handleElicitationDismiss)
	mux.HandleFunc("GET /v1/blobs/{ref...}", s.handleBlob)
	return mux
}

// CreateSessionRequest is the §15.1 POST /v1/sessions body. Each
// optional field is validated when present; only `runtimeRef` is
// required by the minimal gateway. Future phases add `env`,
// `timeouts`, `credentialPolicy`, `delegationPolicy`, etc.
type CreateSessionRequest struct {
	RuntimeRef    string          `json:"runtimeRef"`
	UserID        string          `json:"userId,omitempty"`
	WorkspacePlan json.RawMessage `json:"workspacePlan,omitempty"`

	// IsolationProfile is an optional override that pins the session
	// to a specific §5.3 profile. Production resolves this from the
	// `targetPool`'s pool definition; the minimal gateway accepts it
	// from the body so SEC-001 monotonicity tests have a knob to drive.
	IsolationProfile isolation.Profile `json:"isolationProfile,omitempty"`
}

// SessionResponse is the §15.1 GET /v1/sessions/{id} envelope.
type SessionResponse struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenantId"`
	UserID     string `json:"userId,omitempty"`
	RuntimeRef string `json:"runtimeRef,omitempty"`
	State      string `json:"state"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`

	FailureClass string `json:"failureClass,omitempty"`

	// WorkspacePlan is the §14 WorkspacePlan stored at session
	// creation, echoed per §15.1. Absent when the session was created
	// without a plan.
	WorkspacePlan json.RawMessage `json:"workspacePlan,omitempty"`
}

// CreateSessionResponse is the §15.1 POST /v1/sessions response
// envelope. Carries the regular session fields plus the §7.1
// uploadToken and the §7.1 sessionIsolationLevel.
type CreateSessionResponse struct {
	SessionResponse

	// UploadToken is the §7.1 single-use HMAC uploadToken the client
	// supplies on every `POST /v1/sessions/{id}/upload` and
	// `POST /v1/sessions/{id}/upload-archive` until the session is
	// finalized. Treat as a secret per §7.1.
	UploadToken string `json:"uploadToken"`

	// SessionIsolationLevel echoes the §7.1 isolation-level object
	// (executionMode, isolationProfile, podReuse, scrubPolicy,
	// residualStateWarning). The minimal gateway populates
	// isolationProfile + executionMode + residualStateWarning;
	// scrubPolicy/podReuse default to the §7.1 single-session values.
	SessionIsolationLevel SessionIsolationLevel `json:"sessionIsolationLevel"`

	// WorkspacePlanWarnings echoes any §14 consumer-advisory
	// warnings (unknown source type, path collisions) the parser
	// raised. Empty when the plan is omitted or pristine.
	WorkspacePlanWarnings []workspaceplan.Warning `json:"workspacePlanWarnings,omitempty"`
}

// SessionIsolationLevel mirrors the §7.1 sessionIsolationLevel object.
type SessionIsolationLevel struct {
	ExecutionMode        string `json:"executionMode"`
	IsolationProfile     string `json:"isolationProfile"`
	PodReuse             bool   `json:"podReuse"`
	ScrubPolicy          string `json:"scrubPolicy,omitempty"`
	ResidualStateWarning bool   `json:"residualStateWarning"`
}

// errorEnvelope is the §15.1 error response shape.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// handleCreate implements POST /v1/sessions. Returns 201 with the
// CreateSessionResponse envelope on success.
func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireActiveUser(w, r) {
		return
	}
	tenantID := s.resolveTenant(r)
	if !s.requireSessionQuota(w, r, tenantID) {
		return
	}

	var req CreateSessionRequest
	body := jsonReader(w, r)
	defer body.Close()
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	if req.RuntimeRef == "" {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "runtimeRef is required", map[string]any{"field": "runtimeRef"})
		return
	}

	// §5.3 isolation profile: explicit override > §5.3 default. The
	// minimal gateway does not yet resolve pools, so any explicit
	// value is taken at face value (production validates against the
	// resolved pool's profile).
	isoProf := req.IsolationProfile
	if isoProf == "" {
		isoProf = s.defaultIsoProf
	}
	if !isolation.IsValid(isoProf) {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			fmt.Sprintf("isolationProfile %q is not a recognised §5.3 profile", isoProf),
			map[string]any{"fields": []map[string]string{{"field": "isolationProfile"}}})
		return
	}

	// §14 workspace plan: parse + validate when present. Absent plan
	// is admitted (the session starts with an empty workspace, the
	// minimal gateway uses this for tests that exercise pure
	// state-machine paths). The validated plan is stored on the row so
	// the start handler can materialize it onto the claimed pod and
	// GET /v1/sessions/{id} can return it per §15.1.
	var planWarnings []workspaceplan.Warning
	var planJSON json.RawMessage
	if len(req.WorkspacePlan) > 0 && !isJSONNull(req.WorkspacePlan) {
		_, warnings, err := workspaceplan.Parse(req.WorkspacePlan)
		if err != nil {
			s.writeWorkspacePlanError(w, err)
			return
		}
		planJSON = req.WorkspacePlan
		planWarnings = warnings
	}

	row := sessionstore.Session{
		ID:               s.idFn(),
		TenantID:         tenantID,
		UserID:           req.UserID,
		RuntimeRef:       req.RuntimeRef,
		State:            session.StateCreated,
		IsolationProfile: isoProf,
		WorkspacePlan:    planJSON,
		CreatedAt:        s.clock(),
	}
	row.UpdatedAt = row.CreatedAt
	// §10.7: the ExperimentRouter may enroll the session in a variant,
	// rewriting its runtime/pool before the row is persisted.
	s.applyExperimentRouting(r.Context(), &row)
	if err := s.store.Create(r.Context(), row); err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	s.recordSessionCreated(r.Context(), row)

	// §7.1 step 8: mint the single-use uploadToken stamped on the
	// session creation response. TTL = maxCreatedStateTimeoutSeconds
	// (uploadtoken.DefaultTTL — 300 s). The digest + expiry are
	// stored on the row so the finalize handler can consume the
	// token through the §7.1 single-use tracker.
	tok, parsed, err := s.uploadIssuer.IssueDetailed(row.ID, 0)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			fmt.Sprintf("upload token issuance failed: %v", err), nil)
		return
	}
	if _, err := s.store.Update(r.Context(), tenantID, row.ID, func(row *sessionstore.Session) error {
		row.UploadTokenDigest = parsed.Digest
		row.UploadTokenExpiry = parsed.Expiry
		return nil
	}); err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			fmt.Sprintf("session row update failed: %v", err), nil)
		return
	}
	row.UploadTokenDigest = parsed.Digest
	row.UploadTokenExpiry = parsed.Expiry

	resp := CreateSessionResponse{
		SessionResponse:       toResponse(row),
		UploadToken:           tok,
		SessionIsolationLevel: defaultIsolationLevel(isoProf),
		WorkspacePlanWarnings: planWarnings,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// defaultIsolationLevel returns the §7.1 sessionIsolationLevel
// envelope the minimal gateway populates. The minimal gateway runs
// in `executionMode: session` (no pod reuse) so podReuse is false,
// scrubPolicy is empty, and residualStateWarning is false. Future
// phases that ship the §5.2 task / concurrent modes recompute these
// fields from the resolved pool configuration.
func defaultIsolationLevel(p isolation.Profile) SessionIsolationLevel {
	return SessionIsolationLevel{
		ExecutionMode:        "session",
		IsolationProfile:     string(p),
		PodReuse:             false,
		ScrubPolicy:          "",
		ResidualStateWarning: false,
	}
}

// writeWorkspacePlanError translates a workspaceplan.ValidationError
// into the §15.1 `400 WORKSPACE_PLAN_INVALID` envelope.
func (s *Server) writeWorkspacePlanError(w http.ResponseWriter, err error) {
	var ve *workspaceplan.ValidationError
	if !errors.As(err, &ve) {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	details := map[string]any{}
	if ve.Field != "" {
		details["field"] = ve.Field
	}
	if ve.Reason != "" {
		details["reason"] = ve.Reason
	}
	if len(ve.SubErrs) > 0 {
		subs := make([]map[string]any, 0, len(ve.SubErrs))
		for _, se := range ve.SubErrs {
			subs = append(subs, map[string]any{
				"sourceIndex": se.SourceIndex,
				"field":       se.Field,
				"reason":      se.Reason,
				"message":     se.Message,
			})
		}
		details["subErrors"] = subs
	}
	s.writeError(w, http.StatusBadRequest, "WORKSPACE_PLAN_INVALID", ve.Error(), details)
}

// isJSONNull reports whether the supplied raw JSON is the literal
// `null` token (RFC 8259 §3) ignoring leading / trailing whitespace.
// Used to distinguish `{"workspacePlan": null}` from an omitted
// field.
func isJSONNull(raw json.RawMessage) bool {
	return string(raw) == "null"
}

// handleGet implements GET /v1/sessions/{id}.
func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")
	row, err := s.store.Get(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	s.writeSession(w, http.StatusOK, row)
}

// handleList implements GET /v1/sessions. Supports the §15.1 ?state=
// and ?runtime= filters in their basic form.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	filter := sessionstore.ListFilter{
		State:        session.State(r.URL.Query().Get("state")),
		RuntimeRef:   r.URL.Query().Get("runtime"),
		FailureClass: session.FailureClass(r.URL.Query().Get("failureClass")),
	}
	rows, err := s.store.List(r.Context(), tenantID, filter)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out := make([]SessionResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toResponse(row))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"sessions": out})
}

// handleDelete implements DELETE /v1/sessions/{id} per §15.1: every
// non-terminal state transitions to cancelled.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")
	row, err := s.store.Get(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if err := session.Validate(session.PreconditionRequest{
		Endpoint:     session.EndpointDelete,
		CurrentState: row.State,
	}); err != nil {
		s.writePreconditionError(w, err)
		return
	}
	updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
		row.State = session.StateCancelled
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	s.recordSessionCompleted(r.Context(), updated)
	s.writeSession(w, http.StatusOK, updated)
}

// handleTransition is the shared handler shape for every
// state-mutating endpoint that does not carry a body (finalize,
// start, interrupt, terminate, resume). The supplied transition
// function captures the next state.
func (s *Server) handleTransition(endpoint session.Endpoint, transition func(*sessionstore.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID := s.resolveTenant(r)
		id := r.PathValue("id")
		row, err := s.store.Get(r.Context(), tenantID, id)
		if err != nil {
			if errors.Is(err, sessionstore.ErrNotFound) {
				s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
				return
			}
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		if err := session.Validate(session.PreconditionRequest{
			Endpoint:     endpoint,
			CurrentState: row.State,
		}); err != nil {
			s.writePreconditionError(w, err)
			return
		}
		updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
			transition(row)
			return nil
		})
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		if session.IsTerminal(updated.State) {
			s.recordSessionCompleted(r.Context(), updated)
		}
		s.writeSession(w, http.StatusOK, updated)
	}
}

// transitionFinalize: per §15.1, /finalize transitions
// created → finalizing → ready. The minimal gateway short-circuits
// the materialisation step and goes straight to ready.
func transitionFinalize(row *sessionstore.Session) { row.State = session.StateReady }

// handleFinalize wraps the §15.1 finalize transition with §7.1
// uploadToken single-use invalidation. After the row transitions to
// ready (the upload window closes), the digest stamped at create is
// marked consumed via the ConsumedTracker so a captured token cannot
// be replayed against /upload after finalize.
//
// The token consumption fires after the state mutation succeeds — if
// the mutation is rejected (precondition or store error), the token
// remains valid so the client can retry. Idempotent finalize calls
// (the row is already ready) hit the §15.1 precondition rejection
// before reaching the consume step.
func (s *Server) handleFinalize(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")
	row, err := s.store.Get(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if err := session.Validate(session.PreconditionRequest{
		Endpoint:     session.EndpointFinalize,
		CurrentState: row.State,
	}); err != nil {
		s.writePreconditionError(w, err)
		return
	}
	updated, err := s.store.Update(r.Context(), tenantID, id, func(r *sessionstore.Session) error {
		transitionFinalize(r)
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// §7.1 single-use uploadToken invalidation: once the upload
	// window closes, the digest cannot mint another upload.
	if s.uploadVerifier != nil && updated.UploadTokenDigest != "" {
		_ = s.uploadVerifier.ConsumeDigest(updated.UploadTokenDigest, updated.UploadTokenExpiry)
	}
	s.writeSession(w, http.StatusOK, updated)
}

// transitionStart: per §15.1, /start transitions ready → starting →
// running. Short-circuits to running.
func transitionStart(row *sessionstore.Session) { row.State = session.StateRunning }

// transitionInterrupt: per §15.1, /interrupt transitions running →
// suspended.
func transitionInterrupt(row *sessionstore.Session) { row.State = session.StateSuspended }

// transitionTerminate: per §15.1, /terminate transitions any
// non-terminal → completed.
func transitionTerminate(row *sessionstore.Session) { row.State = session.StateCompleted }

// transitionResume: per §15.1, /resume transitions
// awaiting_client_action → resume_pending → running. The minimal
// gateway short-circuits to running.
func transitionResume(row *sessionstore.Session) { row.State = session.StateRunning }

// resolveTenant returns the tenant id for this request, preferring
// the §10.2 authenticated Principal over any client-supplied header.
// The order is:
//
//  1. Principal.TenantID from auth middleware (canonical).
//  2. X-Lenny-Tenant-ID dev header — only honoured when its value
//     passes the §10.2 format check; rejected values fall through
//     so the request lands on the default tenant instead of
//     reaching the store with an attacker-controlled identifier.
//  3. "default" per §10.2 single-tenant mode.
//
// The returned tenant id is always either a §10.2-valid identifier
// or `default`. Handlers can therefore use it directly in store
// queries and §4.5 blob URIs without re-validating.
func (s *Server) resolveTenant(r *http.Request) string {
	if p, ok := getPrincipal(r); ok && p.TenantID != "" {
		return p.TenantID
	}
	if v := r.Header.Get("X-Lenny-Tenant-ID"); v != "" {
		if err := authValidateTenantID(v); err == nil {
			return v
		}
	}
	return "default"
}

// writeSession serialises a Session row as the §15.1 envelope and
// writes it with the supplied status code.
func (s *Server) writeSession(w http.ResponseWriter, code int, row sessionstore.Session) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(toResponse(row))
}

// writeError writes a §15.1 error envelope.
func (s *Server) writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: errorBody{
		Code:    code,
		Message: message,
		Details: details,
	}})
}

// writePreconditionError maps a session.PreconditionError to the
// §15.1 INVALID_STATE_TRANSITION envelope.
func (s *Server) writePreconditionError(w http.ResponseWriter, err error) {
	var pe *session.PreconditionError
	if !errors.As(err, &pe) {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	allowed := make([]string, 0, len(pe.AllowedStates))
	for _, st := range pe.AllowedStates {
		allowed = append(allowed, string(st))
	}
	s.writeError(w, pe.Code(), pe.ErrorCode(), pe.Error(), map[string]any{
		"currentState":  string(pe.CurrentState),
		"allowedStates": allowed,
	})
}

// toResponse converts a Session row into the §15.1 wire envelope.
func toResponse(row sessionstore.Session) SessionResponse {
	out := SessionResponse{
		ID:         row.ID,
		TenantID:   row.TenantID,
		UserID:     row.UserID,
		RuntimeRef: row.RuntimeRef,
		State:      string(row.State),
		CreatedAt:  row.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:  row.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if row.FailureClass != "" {
		out.FailureClass = string(row.FailureClass)
	}
	if len(row.WorkspacePlan) > 0 {
		out.WorkspacePlan = row.WorkspacePlan
	}
	return out
}

// randomSessionID returns a fresh §12.6 UUIDv8 session identifier.
func randomSessionID() string {
	return session.NewID()
}

// Now exposes the configured clock so callers that hold a reference
// to the Server can compose with the same time source. Useful for
// tests that need to verify timestamp behaviour.
func (s *Server) Now() time.Time { return s.clock() }

// Context-typed alias to satisfy go vet's pattern.
type ctxKey struct{}

func contextWithTenant(ctx context.Context, tenant string) context.Context {
	return context.WithValue(ctx, ctxKey{}, tenant)
}

func tenantFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}

var (
	_ = contextWithTenant
	_ = tenantFromContext
)
