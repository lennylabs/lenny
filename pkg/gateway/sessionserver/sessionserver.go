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
	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/errorclassify"
	"github.com/lennylabs/lenny/pkg/gateway/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
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
	events          *sessionevents.Bus
	interactions    interactionstore.Store
	usage           usagestore.Store
	users           userstore.Store
	billing         billingstore.Store
	tenants         tenantstore.Store
	storageQuota    storagequota.Counter
	defaultIsoProf  isolation.Profile
	devMode         bool
	podBinder       *podsession.Binder
	podRegistry     *podsession.Registry
	agentNamespace  string
	sealer          Sealer
	// partialManifestCleaner, when set, executes the §4.4 line 236
	// partial-manifest cleanup after the resume path completes.
	// Nil leaves the resume path unchanged (cleanup is deferred to
	// the §12.5 backstop sweep).
	partialManifestCleaner PartialManifestCleaner
	// evictionStateLookup, when set, classifies a resume as
	// conversation-only (workspace lost during eviction) so the
	// session.resumed event surfaces the correct ResumeMode per
	// §4.4 line 263.
	evictionStateLookup EvictionStateLookup
	// partialManifestLookup, when set, classifies a resume as
	// partial-workspace (reassembled from chunk objects) so the
	// session.resumed event surfaces the correct ResumeMode per
	// §10.1 partial-manifest path.
	partialManifestLookup PartialManifestLookup
	treeArchive           treearchive.Store
	maxOrphanTasks        int
	evals                 evalstore.Store
	memory                memorystore.Store
	experiments           experimentstore.Store
	pools                 poolstore.Store
	experimentReporter    ExperimentRejectionReporter
	runtimes              runtimestore.Store
	environments          environmentstore.Store
	tenantAccess          tenantaccessstore.Store
	opsEmitter            events.EventEmitter
	refResolver           workspaceplan.RefResolver
	credPools             credentialpoolstore.Store
	defaultNoEnvPolicy    string
	customRoles           customrolestore.Store
	interceptors          *interceptor.Chain
	policyAuditSink       *policy.AuditSink
	uploadSubsystem       *subsystem.Subsystem
	// resumeWindow is the §4.2 line 159 default resume-eligibility
	// duration stamped onto each session at create time. A non-zero
	// value falls through to DefaultResumeWindow.
	resumeWindow time.Duration
	// sessionLogHook, when set, receives the §4.4 line 226 session-log
	// close-hook on every session transition to a terminal state.
	// Best-effort: a failure logs and discards rather than abort the
	// transition.
	sessionLogHook SessionLogHook
	// warmupEstimateSeconds is the §5.2 line 625 estimate used for the
	// PoolWarmingUp 503's estimatedReadyIn and Retry-After. A zero value
	// falls through to DefaultWarmupEstimateSeconds.
	warmupEstimateSeconds int
	// credRouter is the §4.9 CredentialRouter used at session creation
	// to resolve a credential source and pool per provider. Never nil
	// after New (defaults to credrouter.Default).
	credRouter credrouter.Router
	// preclaimMismatch, when set, increments the §4.9 line 1220
	// pre-claim mismatch metric.
	preclaimMismatch func(pool, provider string)
	// userCredChecker reports whether a usable user-scoped credential
	// exists for (tenant, user, provider). Nil in v1 because user-source
	// lease delivery (the §4.9 materializedConfig path) is not yet wired;
	// the §4.9 router resolves user sources only when this reports true.
	userCredChecker func(ctx context.Context, tenantID, userID, provider string) bool
}

// DefaultMaxOrphanTasksPerTenant is the §8.10 cap on a tenant's active
// orphan tasks. When a `detach` cascade would push the tenant over the
// cap, the gateway falls back to `cancel_all` so orphans cannot
// accumulate without bound.
const DefaultMaxOrphanTasksPerTenant = 100

// DefaultResumeWindow is the §4.2 line 159 default resume-eligibility
// window. A session created without an explicit override is eligible
// for resume up to this duration after creation; once the deadline
// passes, the watchdog forces the session to a terminal state.
//
// The value mirrors watchdog.DefaultMaxSessionAgeSeconds (2 hours).
// Operators tuning the watchdog's lifetime cap should also override
// the resume window so the two budgets stay aligned; the option
// hook below lets the gateway plumb the watchdog-configured value.
// spec: §4.2 line 159 — "Resume eligibility and window".
const DefaultResumeWindow = 2 * time.Hour

// Sealer takes the §7.1 final workspace snapshot of a session that has
// reached a terminal state. The gateway invokes it as the
// seal-and-export step of session completion.
type Sealer interface {
	// Seal snapshots the session's final workspace. An implementation
	// is expected to no-op for a session that never ran on a pod.
	Seal(ctx context.Context, tenantID, sessionID string) error
}

// PartialManifestCleaner executes the §4.4 line 236 partial-manifest
// cleanup after the resume path completes, regardless of whether the
// reassembly succeeded or failed. An implementation walks the latest
// active partial manifest for (tenant, session), deletes the chunks
// under its `partial_object_key_prefix`, and soft-deletes the row.
// Best-effort: a failure leaves the row active for the §12.5
// backstop sweep to clean up on the next cycle.
type PartialManifestCleaner interface {
	// CleanupAfterResume runs the cleanup for the session's latest
	// active partial manifest. A no-op (returns nil) when no active
	// manifest exists.
	CleanupAfterResume(ctx context.Context, tenantID, sessionID string) error
}

// EvictionStateLookup reports whether the (tenant, session) carries
// the §4.4 minimal-state record written during the eviction-fallback
// path. The §7.2 resume path uses it to derive the
// `resumeMode: "conversation_only"` value carried on the
// `session.resumed` event when the workspace was lost during
// eviction.
//
// spec: §4.4 line 263 — "the client receives a session.resumed event
// with resumeMode: \"conversation_only\" and workspaceLost: true".
type EvictionStateLookup interface {
	// HasEvictionState returns true when the session has a
	// minimal-state record (workspace was lost during eviction).
	// Returns false when no record exists; an error reading the store
	// returns the error so the resume path can degrade gracefully
	// (the gateway falls back to ResumeFull rather than block on a
	// transient lookup failure).
	HasEvictionState(ctx context.Context, tenantID, sessionID string) (bool, error)
}

// PartialManifestLookup reports whether the (tenant, session) carries
// an active §4.4 / §10.1 partial-checkpoint manifest. The §7.2 resume
// path uses it to derive the `resumeMode: "partial_workspace"` value
// carried on the `session.resumed` event when the resume reassembled
// the workspace from partial chunks rather than from a full
// checkpoint.
//
// spec: §10.1 partial-manifest path — `session.resumed` carries
// `resumeMode: "partial_workspace"` when the manifest selected by
// MAX(coordination_generation) yielded a reassembled workspace.
type PartialManifestLookup interface {
	// HasActivePartialManifest returns true when an active partial
	// manifest exists for (tenant, session). Returns false when none
	// exists; a store error returns the error so the resume path can
	// degrade gracefully (falls back to ResumeFull).
	HasActivePartialManifest(ctx context.Context, tenantID, sessionID string) (bool, error)
}

// SessionLogHook is the §4.4 line 226 close-hook the gateway invokes
// on every transition to a terminal state. Implementations capture
// the buffered runtime stderr bytes and persist them best-effort.
// The default production wiring lives in pkg/gateway/sessionlogstore
// (CloseHook.OnSessionTerminal).
//
// spec: §4.4 line 226 — "Session logs and runtime stderr".
type SessionLogHook interface {
	// OnSessionTerminal records the session log for (tenant, session).
	// Implementations are best-effort: a failure must not be
	// propagated as a fatal error to the caller.
	OnSessionTerminal(ctx context.Context, tenantID, sessionID string, body []byte, truncated bool) error
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
	Events *sessionevents.Bus

	// Interactions is the §6/§9.2 pending tool-call + elicitation
	// store backing the §15.1 tool-use and elicitation endpoints.
	// When nil those endpoints return
	// `503 INTERACTIONS_UNAVAILABLE`.
	Interactions interactionstore.Store

	// Evals is the §10.7 built-in eval-result store backing
	// POST /v1/sessions/{id}/eval. When nil the endpoint returns
	// `503 EVAL_UNAVAILABLE`.
	Evals evalstore.Store

	// Memory is the §9.4 MemoryStore backing the
	// /v1/sessions/{id}/memory REST surface. When nil those
	// endpoints return `503 MEMORY_UNAVAILABLE`.
	Memory memorystore.Store

	// Experiments is the §10.7 experiment registry. When set, the
	// ExperimentRouter assigns a variant at session creation; when nil
	// no session is enrolled in an experiment.
	Experiments experimentstore.Store

	// Pools is the §5.2 warm-pool registry. The §10.7 ExperimentRouter
	// consults it to enforce the isolation-monotonicity rule: a variant
	// pool weaker than the session's profile fails the session closed.
	// When nil the router skips the isolation check.
	Pools poolstore.Store

	// ExperimentRejections, when set, receives a report each time the
	// §10.7 ExperimentRouter fails a session closed on the isolation
	// monotonicity check. The gateway wires an implementation that
	// emits the `experiment.isolation_mismatch` event and increments
	// `lenny_experiment_isolation_rejections_total`. Nil disables
	// reporting; the 422 rejection still fires.
	ExperimentRejections ExperimentRejectionReporter

	// Usage is the §15.1 usage / metering accumulator. When set, the
	// gateway records a session-created event on create and the
	// `GET /v1/usage` endpoint serves the aggregated report. Nil
	// disables metering (GET /v1/usage returns an empty report).
	Usage usagestore.Store

	// DefaultIsolationProfile is the §5.3 fallback profile applied to
	// a session whose pool resolution did not name one. When unset the
	// server falls back to the dev-mode-aware default: `sandboxed`
	// (gVisor) normally, or `standard` (runc) when DevMode is true per
	// §5.3 line 677.
	DefaultIsolationProfile isolation.Profile

	// DevMode is the platform global.devMode (LENNY_DEV_MODE=true). It
	// selects the §5.3 line 677 dev-mode fallback (`standard`) when no
	// DefaultIsolationProfile is configured.
	DevMode bool

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

	// PartialManifestCleaner, when set, executes the §4.4 line 236
	// partial-manifest cleanup after the resume path completes. Nil
	// leaves the resume path unchanged; the §12.5 backstop sweep
	// remains the only cleanup path.
	PartialManifestCleaner PartialManifestCleaner

	// EvictionStateLookup, when set, lets the resume path classify a
	// resume as conversation-only (workspace lost during eviction)
	// per §4.4 line 263, so the session.resumed event carries
	// resumeMode: "conversation_only" and workspaceLost: true. Nil
	// leaves the resume defaulting to the snapshot-source-derived
	// ResumeMode (ResumeFull when a workspace snapshot is present).
	EvictionStateLookup EvictionStateLookup

	// PartialManifestLookup, when set, lets the resume path classify
	// a resume as partial-workspace (reassembled from chunk objects)
	// per §10.1 partial-manifest path. Nil leaves the resume
	// defaulting to ResumeFull when a workspace snapshot is present.
	PartialManifestLookup PartialManifestLookup

	// TreeArchive, when set, receives a §8.10 archive record for every
	// child session (a session with a parent) that reaches a terminal
	// state, so a resumed parent can replay the outcome. Nil disables
	// delegation-tree archiving.
	TreeArchive treearchive.Store

	// MaxOrphanTasksPerTenant caps a tenant's active orphan tasks per
	// §8.10. A non-positive value selects DefaultMaxOrphanTasksPerTenant.
	MaxOrphanTasksPerTenant int

	// ResumeWindow is the §4.2 line 159 resume-eligibility duration
	// stamped onto each session at create. A non-positive value
	// selects DefaultResumeWindow (2 hours, mirroring the watchdog's
	// MaxSessionAgeSeconds default). Operators tuning the watchdog
	// budget should pass the matching value here so the two budgets
	// stay aligned.
	// spec: §4.2 line 159 — "Resume eligibility and window".
	ResumeWindow time.Duration

	// Runtimes is the §5.1 runtime registry. Optional — when nil, the
	// §9.1 GET /v1/runtimes discovery endpoint returns an empty list.
	Runtimes runtimestore.Store

	// Environments is the §10.6 environment registry. Optional — when
	// set together with Tenants, GET /v1/runtimes applies §10.6
	// transparent filtering so a caller sees only the runtimes its
	// environment membership authorizes.
	Environments environmentstore.Store

	// TenantAccess is the §4 runtime tenant-access registry. Optional —
	// when nil, GET /internal/runtimes/{name}/meta/{key} cannot serve a
	// tenant-visibility entry and fails closed.
	TenantAccess tenantaccessstore.Store

	// OpsEmitter records §25.3 operational events into the event
	// buffer or §25.5 Redis stream. Optional — when nil, the gateway
	// emits no operational events for session transitions.
	OpsEmitter events.EventEmitter

	// RefResolver pins each §14 gitClone source's ref to an immutable
	// commit SHA at session creation. Optional — when nil, the gateway
	// stores the submitted plan without resolving git refs.
	RefResolver workspaceplan.RefResolver

	// CredentialPools is the §4.9 credential-pool registry. When set,
	// session creation runs the §14 gitClone auth host-to-pool binding
	// check. Optional — when nil, the binding check is skipped.
	CredentialPools credentialpoolstore.Store

	// DefaultNoEnvironmentPolicy is the §10.6 platform-wide
	// noEnvironmentPolicy applied when a caller's tenant has set none.
	DefaultNoEnvironmentPolicy string

	// CustomRoles is the §10.2 tenant custom-role registry. When set,
	// the §10.2 session-endpoint authorization gate resolves a caller's
	// custom roles against it so a custom role that grants
	// manage_own_sessions / read_own_sessions is honored. When nil only
	// built-in roles are consulted.
	CustomRoles customrolestore.Store

	// Interceptors is the §4.8 RequestInterceptor chain. When set, the
	// session-creation path runs the chain at the PostAuth phase after
	// the concurrent-session quota check, so the built-in QuotaEvaluator
	// (and any registered external interceptor) admits or rejects the
	// create. When nil the session-creation path runs no interceptors.
	Interceptors *interceptor.Chain

	// PolicyAuditSink, when set, receives the §16.7 `interceptor.rejected`
	// audit row whenever the PostAuth interceptor chain REJECTs a
	// session create. The append is synchronous per §11.7. Nil disables
	// the emission; the rejection still fires.
	PolicyAuditSink *policy.AuditSink

	// UploadSubsystem, when set, gates POST /v1/sessions/{id}/upload
	// through the §4.1 Upload Handler subsystem (max-concurrent
	// semaphore + per-replica circuit breaker). A saturated subsystem
	// returns 503 SUBSYSTEM_UNAVAILABLE for new uploads while the
	// Stream Proxy and MCP Fabric handlers continue serving normally —
	// the §4.1 partial-degradation contract. When nil, uploads run
	// without subsystem gating (tests and the minimal gateway do not
	// configure a limit).
	UploadSubsystem *subsystem.Subsystem

	// SessionLogHook, when set, receives the §4.4 line 226 close-hook
	// on every session transition to a terminal state. The production
	// wiring lives in pkg/gateway/sessionlogstore (CloseHook). Nil
	// disables the session-log persistence path; the transition still
	// fires.
	// spec: §4.4 line 226.
	SessionLogHook SessionLogHook

	// WarmupEstimateSeconds overrides the §5.2 line 625 PoolWarmingUp
	// warm-up estimate (estimatedReadyIn and the Retry-After floor's
	// input). Zero selects DefaultWarmupEstimateSeconds (120s), the
	// spec's no-historical-data fallback.
	// spec: §5.2 line 625.
	WarmupEstimateSeconds int

	// CredentialRouter is the §4.9 pluggable CredentialRouter used at
	// session creation to resolve a credential source and pool per
	// provider in the intersection of the runtime's supportedProviders
	// and the tenant's credentialPolicy. Nil selects the built-in
	// strategy-and-fallback-order router (credrouter.Default).
	// spec: §4.9 lines 1558-1591.
	CredentialRouter credrouter.Router

	// PreclaimMismatch, when set, increments
	// lenny_credential_preclaim_mismatch_total{pool,provider} on the
	// §4.9 line 1220 race: the pre-claim availability check passed but
	// the lease assignment failed. Nil disables the emission.
	// spec: §4.9 line 1220.
	PreclaimMismatch func(pool, provider string)
}

// New returns a Server bound to the supplied store.
func New(store sessionstore.Store, opts Options) *Server {
	s := &Server{
		store:                  store,
		clock:                  opts.Clock,
		idFn:                   opts.IDFunc,
		deriveAuditSink:        opts.DeriveAuditSink,
		uploadIssuer:           opts.UploadTokenIssuer,
		uploadVerifier:         opts.UploadTokenVerifier,
		blobs:                  opts.Blobs,
		executor:               opts.Executor,
		transcripts:            opts.Transcripts,
		evals:                  opts.Evals,
		memory:                 opts.Memory,
		experiments:            opts.Experiments,
		pools:                  opts.Pools,
		experimentReporter:     opts.ExperimentRejections,
		events:                 opts.Events,
		interactions:           opts.Interactions,
		usage:                  opts.Usage,
		users:                  opts.Users,
		billing:                opts.Billing,
		tenants:                opts.Tenants,
		storageQuota:           opts.StorageQuota,
		defaultIsoProf:         opts.DefaultIsolationProfile,
		devMode:                opts.DevMode,
		podBinder:              opts.PodBinder,
		podRegistry:            opts.PodRegistry,
		agentNamespace:         opts.AgentNamespace,
		sealer:                 opts.Sealer,
		partialManifestCleaner: opts.PartialManifestCleaner,
		evictionStateLookup:    opts.EvictionStateLookup,
		partialManifestLookup:  opts.PartialManifestLookup,
		treeArchive:            opts.TreeArchive,
		maxOrphanTasks:         opts.MaxOrphanTasksPerTenant,
		runtimes:               opts.Runtimes,
		environments:           opts.Environments,
		tenantAccess:           opts.TenantAccess,
		opsEmitter:             opts.OpsEmitter,
		refResolver:            opts.RefResolver,
		credPools:              opts.CredentialPools,
		defaultNoEnvPolicy:     opts.DefaultNoEnvironmentPolicy,
		customRoles:            opts.CustomRoles,
		interceptors:           opts.Interceptors,
		policyAuditSink:        opts.PolicyAuditSink,
		uploadSubsystem:        opts.UploadSubsystem,
		resumeWindow:           opts.ResumeWindow,
		sessionLogHook:         opts.SessionLogHook,
		warmupEstimateSeconds:  opts.WarmupEstimateSeconds,
		credRouter:             opts.CredentialRouter,
		preclaimMismatch:       opts.PreclaimMismatch,
	}
	if s.warmupEstimateSeconds <= 0 {
		s.warmupEstimateSeconds = DefaultWarmupEstimateSeconds
	}
	if s.credRouter == nil {
		// spec: §4.9 lines 1583-1589 — the built-in strategy-and-
		// fallback-order CredentialRouter is the default.
		s.credRouter = credrouter.NewDefault()
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
	if s.resumeWindow <= 0 {
		s.resumeWindow = DefaultResumeWindow
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
		// spec: §5.3 line 677 — honor the dev-mode fallback to `standard`
		// (runc) when no explicit default isolation profile is configured.
		s.defaultIsoProf = isolation.DefaultForMode(s.devMode)
	}
	return s
}

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
	read := func(next http.HandlerFunc) http.HandlerFunc {
		return s.requireSessionPermission(auth.PermReadOwnSessions, next)
	}
	mux.HandleFunc("POST /v1/sessions", manage(s.handleCreate))
	mux.HandleFunc("GET /v1/runtimes", s.handleListRuntimes)
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
	mux.HandleFunc("POST /v1/sessions/{id}/interrupt",
		manage(s.handleTransition(session.EndpointInterrupt, transitionInterrupt)))
	mux.HandleFunc("POST /v1/sessions/{id}/terminate",
		manage(s.handleTransition(session.EndpointTerminate, transitionTerminate)))
	mux.HandleFunc("POST /v1/sessions/{id}/resume", manage(s.handleResume))
	mux.HandleFunc("POST /v1/sessions/{id}/derive", manage(s.handleDerive))
	mux.HandleFunc("POST /v1/sessions/{id}/replay", manage(s.handleReplay))
	mux.HandleFunc("POST /v1/sessions/{id}/extend-retention", manage(s.handleExtendRetention))
	mux.HandleFunc("POST /v1/sessions/{id}/eval", manage(s.handleEval))
	mux.HandleFunc("POST /v1/sessions/{id}/memory", manage(s.handleMemoryWrite))
	mux.HandleFunc("GET /v1/sessions/{id}/memory", read(s.handleMemoryQuery))
	mux.HandleFunc("DELETE /v1/sessions/{id}/memory/{memoryId}", manage(s.handleMemoryDelete))
	mux.HandleFunc("POST /v1/sessions/{id}/upload", manage(s.handleUpload))
	mux.HandleFunc("POST /v1/sessions/{id}/messages", manage(s.handleMessages))
	mux.HandleFunc("GET /v1/sessions/{id}/transcript", read(s.handleTranscript))
	mux.HandleFunc("GET /v1/sessions/{id}/tree", read(s.handleTree))
	mux.HandleFunc("GET /v1/usage", s.handleUsage)
	mux.HandleFunc("GET /v1/metering/events", s.handleMeteringEvents)
	mux.HandleFunc("GET /v1/sessions/{id}/events", read(s.handleEvents))
	mux.HandleFunc("POST /v1/sessions/{id}/tool-use/{tool_call_id}/approve", manage(s.handleToolUseApprove))
	mux.HandleFunc("POST /v1/sessions/{id}/tool-use/{tool_call_id}/deny", manage(s.handleToolUseDeny))
	mux.HandleFunc("POST /v1/sessions/{id}/elicitations/{elicitation_id}/respond", manage(s.handleElicitationRespond))
	mux.HandleFunc("POST /v1/sessions/{id}/elicitations/{elicitation_id}/dismiss", manage(s.handleElicitationDismiss))
	mux.HandleFunc("GET /v1/blobs/{ref...}", s.handleBlob)
	return mux
}

// CreateSessionRequest is the §15.1 POST /v1/sessions body. Each
// optional field is validated when present; only `runtimeRef` is
// required by the minimal gateway. Future phases add `timeouts`,
// `credentialPolicy`, `delegationPolicy`, etc.
type CreateSessionRequest struct {
	RuntimeRef    string          `json:"runtimeRef"`
	UserID        string          `json:"userId,omitempty"`
	WorkspacePlan json.RawMessage `json:"workspacePlan,omitempty"`

	// Environment is the optional §10.6 environment the session is
	// created in. Recorded on the session row; an empty value leaves
	// the session unscoped to any environment.
	Environment string `json:"environment,omitempty"`

	// IsolationProfile is an optional override that pins the session
	// to a specific §5.3 profile. Production resolves this from the
	// `targetPool`'s pool definition; the minimal gateway accepts it
	// from the body so SEC-001 monotonicity tests have a knob to drive.
	IsolationProfile isolation.Profile `json:"isolationProfile,omitempty"`
}

// SessionResponse is the §15.1 GET /v1/sessions/{id} envelope.
type SessionResponse struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenantId"`
	UserID      string `json:"userId,omitempty"`
	RuntimeRef  string `json:"runtimeRef,omitempty"`
	Environment string `json:"environment,omitempty"`
	State       string `json:"state"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`

	FailureClass string `json:"failureClass,omitempty"`

	// WorkspacePlan is the §14 WorkspacePlan stored at session
	// creation, echoed per §15.1. Absent when the session was created
	// without a plan.
	WorkspacePlan json.RawMessage `json:"workspacePlan,omitempty"`

	// Cwd is the §4.2 session working directory. Empty until the
	// runtime adapter materialises the workspace.
	// spec: §4.2 line 156.
	Cwd string `json:"cwd,omitempty"`

	// PodAssignment is the §4.2 pod-to-session binding the session is
	// currently bound to. Empty when the session has no live pod.
	// spec: §4.2 line 160.
	PodAssignment string `json:"podAssignment,omitempty"`

	// RecoveryGeneration is the §4.2 pod-recovery counter, visible to
	// clients per §4.2 line 156. Starts at zero and increments by one
	// on each pod recovery.
	// spec: §4.2 line 156 — "incremented on each pod recovery (visible
	// to clients via the session API ...)".
	RecoveryGeneration int64 `json:"recoveryGeneration"`

	// SchemaVersion is the §4.2 session-row schema version. v1
	// sessions report schema_version=1.
	// spec: §4.2 line 156.
	SchemaVersion int32 `json:"schemaVersion"`

	// RetryCount is the §4.2 line 158 retry counter the Session
	// Manager tracks across this logical session's lifetime.
	// spec: §4.2 line 158 — "Retry counters and policy enforcement".
	RetryCount int64 `json:"retryCount"`

	// PolicyEnforcementState is the §4.2 line 158 schemaless
	// policy-enforcement payload. Omitted from the JSON envelope
	// when empty.
	// spec: §4.2 line 158.
	PolicyEnforcementState json.RawMessage `json:"policyEnforcementState,omitempty"`

	// ResumeEligibleUntil is the §4.2 line 159 resume-window
	// deadline as RFC 3339 nanos. Empty when the session has no
	// resume budget.
	// spec: §4.2 line 159 — "Resume eligibility and window".
	ResumeEligibleUntil string `json:"resumeEligibleUntil,omitempty"`
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
	Code      string         `json:"code"`
	Category  string         `json:"category"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

// handleCreate implements POST /v1/sessions. Returns 201 with the
// CreateSessionResponse envelope on success.
func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeCreateRequest(w, r)
	if !ok {
		return
	}
	s.createSession(w, r, req)
}

// handleEnvironmentSessions implements POST /v1/environments/{name}/sessions
// — the §10.6 explicit-environment session-creation path. It runs the
// regular create flow with the session environment taken from the URL
// path, overriding any environment supplied in the request body.
func (s *Server) handleEnvironmentSessions(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeCreateRequest(w, r)
	if !ok {
		return
	}
	req.Environment = r.PathValue("name")
	s.createSession(w, r, req)
}

// decodeCreateRequest reads a CreateSessionRequest from the request
// body, writing the §15.1 INVALID_REQUEST envelope and returning
// ok=false on a malformed body.
func (s *Server) decodeCreateRequest(w http.ResponseWriter, r *http.Request) (CreateSessionRequest, bool) {
	var req CreateSessionRequest
	body := jsonReader(w, r)
	defer body.Close()
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return CreateSessionRequest{}, false
	}
	return req, true
}

// createSession runs the §15.1 session-creation flow over an
// already-decoded request: the active-user and quota gates, the
// runtime, isolation-profile, and workspace-plan validation, the
// session-row persist, the §7.1 uploadToken mint, and the
// CreateSessionResponse.
func (s *Server) createSession(w http.ResponseWriter, r *http.Request, req CreateSessionRequest) {
	if !s.requireActiveUser(w, r) {
		return
	}
	tenantID := s.resolveTenant(r)
	if !s.requireSessionQuota(w, r, tenantID) {
		return
	}
	if !s.requirePolicyChain(w, r, tenantID) {
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
	_, planJSON, planWarnings, planOK := s.resolvePlanForCreate(w, r, req.WorkspacePlan)
	if !planOK {
		return
	}

	row := sessionstore.Session{
		ID:               s.idFn(),
		TenantID:         tenantID,
		UserID:           req.UserID,
		RuntimeRef:       req.RuntimeRef,
		Environment:      req.Environment,
		State:            session.StateCreated,
		IsolationProfile: isoProf,
		WorkspacePlan:    planJSON,
		CreatedAt:        s.clock(),
	}
	row.UpdatedAt = row.CreatedAt
	// spec: §4.2 line 159 — stamp the resume-eligibility deadline
	// onto the row at create time. The watchdog can then expire a
	// session whose resume window has passed without consulting the
	// global watchdog budget; the per-session window also lets
	// individual sessions override the platform default by adjusting
	// this field on Update.
	row.ResumeEligibleUntil = row.CreatedAt.Add(s.resumeWindow)
	// §10.7: the ExperimentRouter may enroll the session in a variant,
	// rewriting its runtime/pool before the row is persisted. It fails
	// the creation closed when the variant pool is less isolated than
	// the session's profile.
	if !s.routeExperiment(w, r, &row) {
		return
	}
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
		SessionIsolationLevel: s.resolveIsolationLevel(r.Context(), row.RuntimeRef, isoProf),
		WorkspacePlanWarnings: planWarnings,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// resolveIsolationLevel computes the §7.1 sessionIsolationLevel for a
// session against its assigned pool. spec: §7.1 line 75 — the field is
// populated from the assigned pool's configuration at session creation
// time. When a pool resolver is wired, it resolves the pool from the
// runtime and §5.3 profile and derives the fields from the pool's §5.2
// execution mode and scrub policy. When no resolver is wired (the
// Postgres-only posture) or the pool does not resolve cleanly, it falls
// back to the session-mode level; a session-mode pod is the §5.2
// default and carries no pod reuse, so the fallback never understates
// the isolation posture a client would observe.
func (s *Server) resolveIsolationLevel(ctx context.Context, runtimeRef string, requested isolation.Profile) SessionIsolationLevel {
	if s.podBinder == nil || s.podBinder.Client == nil {
		return defaultIsolationLevel(requested)
	}
	match, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.agentNamespace, runtimeRef, string(requested))
	if err != nil {
		return defaultIsolationLevel(requested)
	}
	return isolationLevelForPool(match, requested)
}

// defaultIsolationLevel returns the §7.1 sessionIsolationLevel for a
// session-mode pod: no pod reuse, no scrub, no residual-state warning.
// It is the fallback when the gateway runs without a pool resolver or
// the resolved pool reports the default `executionMode: session`.
func defaultIsolationLevel(p isolation.Profile) SessionIsolationLevel {
	return SessionIsolationLevel{
		ExecutionMode:        string(runtimestore.ExecutionModeSession),
		IsolationProfile:     string(p),
		PodReuse:             false,
		ScrubPolicy:          "",
		ResidualStateWarning: false,
	}
}

// isolationLevelForPool maps a resolved §5.2 pool to the §7.1
// sessionIsolationLevel fields. spec: §7.1 lines 69-73 — task and
// concurrent pools reuse a pod (podReuse true) and may expose residual
// state from prior tasks or sibling slots (residualStateWarning true);
// session pools do neither.
func isolationLevelForPool(match podsession.PoolMatch, requested isolation.Profile) SessionIsolationLevel {
	profile := match.IsolationProfile
	if profile == "" {
		profile = string(requested)
	}
	mode := match.ExecutionMode
	if mode == "" {
		mode = string(runtimestore.ExecutionModeSession)
	}
	level := SessionIsolationLevel{ExecutionMode: mode, IsolationProfile: profile}
	switch mode {
	case string(runtimestore.ExecutionModeTask), string(runtimestore.ExecutionModeConcurrent):
		level.PodReuse = true
		level.ResidualStateWarning = true
		level.ScrubPolicy = scrubPolicyForPool(match)
	}
	return level
}

// scrubPolicyForPool returns the §7.1 line 72 scrubPolicy string for a
// reuse pool. It is meaningful only when podReuse is true; a session
// pool returns the empty string (the field is omitted on the wire).
func scrubPolicyForPool(match podsession.PoolMatch) string {
	switch match.ExecutionMode {
	case string(runtimestore.ExecutionModeTask):
		// Cross-tenant microvm task reuse selects a VM-level scrub
		// variant; same-tenant and non-microvm task reuse uses the
		// standard best-effort scrub. microvmScrubMode defaults to
		// `restart` (§5.2), so an empty value with cross-tenant reuse maps
		// to `vm-restart`.
		if match.IsolationProfile == string(isolation.ProfileMicrovm) && match.AllowCrossTenantReuse {
			if match.MicrovmScrubMode == string(runtimestore.MicrovmScrubInPlace) {
				return "best-effort-in-place"
			}
			return "vm-restart"
		}
		return "best-effort"
	case string(runtimestore.ExecutionModeConcurrent):
		// Concurrent-stateless performs no per-request scrub; concurrent-
		// workspace scrubs per slot on completion or failure.
		if match.ConcurrencyStyle == string(podclaim.StyleStateless) {
			return "none"
		}
		return "best-effort-per-slot"
	default:
		return ""
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

// writeError writes a §15.1 error envelope. The category and
// retryable fields are populated from the shared §15.2.1
// errorclassify table so REST and MCP report the same values for the
// same code.
func (s *Server) writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	cat, retryable := errorclassify.Classify(code)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: errorBody{
		Code:      code,
		Category:  string(cat),
		Message:   message,
		Retryable: retryable,
		Details:   details,
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
// spec: §4.2 line 156 (cwd, pod_assignment, recovery_generation,
// schema_version).
func toResponse(row sessionstore.Session) SessionResponse {
	schemaVersion := row.SchemaVersion
	if schemaVersion == 0 {
		schemaVersion = 1
	}
	out := SessionResponse{
		ID:                 row.ID,
		TenantID:           row.TenantID,
		UserID:             row.UserID,
		RuntimeRef:         row.RuntimeRef,
		Environment:        row.Environment,
		State:              string(row.State),
		CreatedAt:          row.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:          row.UpdatedAt.UTC().Format(time.RFC3339Nano),
		Cwd:                row.Cwd,
		PodAssignment:      row.PodAssignment,
		RecoveryGeneration: row.RecoveryGeneration,
		SchemaVersion:      schemaVersion,
		RetryCount:         row.RetryCount,
	}
	if row.FailureClass != "" {
		out.FailureClass = string(row.FailureClass)
	}
	if len(row.WorkspacePlan) > 0 {
		out.WorkspacePlan = row.WorkspacePlan
	}
	// spec: §4.2 line 158 — only surface the policy enforcement
	// state when the gateway has written something other than the
	// migration default `{}`. The omitempty json tag suppresses the
	// payload when nil; an explicit `{}` is preserved as-is.
	if len(row.PolicyEnforcementState) > 0 {
		out.PolicyEnforcementState = row.PolicyEnforcementState
	}
	// spec: §4.2 line 159 — emit the resume window only when set.
	if !row.ResumeEligibleUntil.IsZero() {
		out.ResumeEligibleUntil = row.ResumeEligibleUntil.UTC().Format(time.RFC3339Nano)
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
