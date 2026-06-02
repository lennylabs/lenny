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
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/derivelock"
	"github.com/lennylabs/lenny/pkg/gateway/envblock"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/errorclassify"
	"github.com/lennylabs/lenny/pkg/gateway/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/slothealth"
	"github.com/lennylabs/lenny/pkg/gateway/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/toolapproval"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/usagestore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/task"
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
	// artifacts is the §12.5 artifact catalog. The §8.10 archive
	// materialization reads it (ListBySession) to populate the §8.8
	// TaskResult.output.artifactRefs for a completed child. Nil when the
	// catalog is not wired (in-memory / dev posture); the artifactRefs
	// array then materializes empty. spec: §8.8 lines 888-896. F-8.8.2.
	artifacts artifactcatalog.Store
	events    *sessionevents.Bus
	// dualStore is the §10.1 dual-store degraded-mode gate consulted at
	// session.create. Nil leaves the gate open. spec: §10.1 item 2.
	dualStore DualStoreGate
	// messaging is the §7.2 session-inbox + DLQ coordinator. It drives
	// the inbox-to-DLQ migration on resume_pending and the inbox+DLQ
	// drain on terminal transition. Nil when messaging durability is not
	// wired (no Redis); every call site no-ops on a nil coordinator.
	// spec: §7.2 lines 305-311 (migration), 343 (terminal drain).
	messaging      *sessioninbox.Coordinator
	interactions   interactionstore.Store
	usage          usagestore.Store
	users          userstore.Store
	billing        billingstore.Store
	tenants        tenantstore.Store
	storageQuota   storagequota.Counter
	defaultIsoProf isolation.Profile
	devMode        bool
	// multiTenant mirrors the §10.2 auth.multiTenant Helm value. When
	// true, the §10.2 RBAC gate fails closed for an authenticated
	// principal that carries no roles (the matrix is unconditional in
	// multi-tenant deployments). When false (single-tenant or no-OIDC
	// dev), the gate retains the historical fall-through.
	// spec: §10.2 lines 256–264, F-10.2.4.
	multiTenant    bool
	podBinder      *podsession.Binder
	podRegistry    *podsession.Registry
	agentNamespace string
	// admissionRL is the §11.1 line 7 per-minute counter used for the
	// per-runtime and per-pool admission scopes enforced at session
	// creation (the global/per-user/per-tenant scopes run in the §11.1
	// HTTP middleware). Nil disables the per-runtime/per-pool scopes.
	// F-11.1.2.
	admissionRL      ratelimit.Counter
	perRuntimePerMin int
	perPoolPerMin    int
	rlMetrics        AdmissionRateLimitMetrics
	// maxConcSessGlobal / maxConcSessPerUser / maxConcSessPerRuntime are
	// the §11.1 line 8 concurrent-session admission caps (live
	// non-terminal session counts) for the global, per-user, and
	// per-runtime scopes. The per-tenant scope is enforced separately by
	// requireSessionQuota against the tenant record. A non-positive value
	// leaves the corresponding scope unlimited. F-11.1.3.
	maxConcSessGlobal     int
	maxConcSessPerUser    int
	maxConcSessPerRuntime int
	// evalRL is the §10.7 eval-submission rate-limit counter (per-session
	// and per-tenant). It shares the §11.1 Counter type with admissionRL;
	// production wires the same Redis-backed instance. Nil disables eval
	// rate limiting. spec: §10.7 line 938.
	evalRL ratelimit.Counter
	// evalPerSessionPerMin / evalPerTenantPerMin are the §10.7
	// `evalRateLimit.perSessionPerMinute` / `perTenantPerMinute` limits.
	// Non-positive disables the corresponding scope. spec: §10.7 line 938.
	evalPerSessionPerMin int
	evalPerTenantPerMin  int
	sealer               Sealer
	// sealMaxDuration bounds the §7.1 seal-and-export retry window
	// (maxWorkspaceSealDurationSeconds). A non-positive value falls
	// through to DefaultWorkspaceSealMaxDuration (300s).
	// spec: §7.1 line 112.
	sealMaxDuration time.Duration
	// sealSleep waits d before the next seal retry, returning false when
	// ctx is cancelled first. Nil selects a context-aware time.Sleep; the
	// seam lets tests drive the backoff loop without real delays.
	sealSleep func(ctx context.Context, d time.Duration) bool
	// observeSealDuration, when set, records the §7.1 line 112
	// lenny_workspace_seal_duration_seconds{pool,outcome} histogram. Nil
	// disables the emission.
	observeSealDuration func(pool, outcome string, seconds float64)
	// recordSessionTerminal, when set, records the §16.1 lines 161-163 /
	// §10.7 rollback-trigger session metric family at terminal transition.
	// Nil disables the emission. spec: §10.7 lines 1120-1132.
	recordSessionTerminal func(tenantID, sessionType, variantID string, isError bool, seconds float64)
	// observeEvalScore, when set, records the §16.1 line 164 lenny_eval_score
	// observation per submitted eval. Nil disables the emission.
	// spec: §10.7 line 1128.
	observeEvalScore func(tenantID, scorer, variantID string, score float64)
	// targetingBreaker is the §10.7 SCL-023 per-tenant OpenFeature
	// targeting circuit breaker. Never nil after New; it is consulted on
	// the external-targeting hot path so sustained provider failures skip
	// the OpenFeature call entirely. spec: §10.7 lines 835-844.
	targetingBreaker *targetingBreaker
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
	treeBudgetReturner    TreeBudgetReturner
	hwmObserver           DelegationHighWatermarkObserver
	hwmReader             DelegationHighWatermarkReader
	maxOrphanTasks        int
	evals                 evalstore.Store
	memory                memorystore.Store
	experiments           experimentstore.Store
	pools                 poolstore.Store
	experimentReporter    ExperimentRejectionReporter
	stickyCache           StickyCache
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
	// uploadMetrics, when set, receives the §16.1 upload-handler
	// byte-count and queue-depth observations. Nil drops them. F-13.4.12.
	uploadMetrics UploadHandlerMetrics
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
	// slotHealth tracks §5.2 concurrent-workspace slot failures and leaks
	// per pod over the rolling 5-minute window so the slot retry policy can
	// drain a pod that crosses the ceil(maxConcurrent/2) unhealthy
	// threshold. Never nil after New (defaults to a fresh Tracker).
	// spec: §5.2 "whole-pod replacement trigger".
	slotHealth *slothealth.Tracker
	// slotReplacement, when set, increments
	// lenny_slot_pod_replacement_total{pool} when the slot retry policy
	// drains an unhealthy concurrent-mode pod for replacement. Nil disables
	// the emission. spec: §5.2 "whole-pod replacement trigger".
	slotReplacement func(pool string)
	// observeStartupDuration, when set, records the §6.3 line 348
	// end-to-end pod-warm startup latency on a successful start. Nil
	// disables the emission.
	observeStartupDuration func(pool, runtimeClass, isolationProfile string, seconds float64)
	// observeStartupPhase, when set, records the §6.3 line 372 latency
	// of one hot-path startup phase. Nil disables the emission.
	observeStartupPhase func(phase, runtimeClass string, seconds float64)
	// observeTimeToFirstToken, when set, records the §6.3 line 356 /
	// §16.1 line 15 end-to-end TTFT histogram on the first
	// agent-streamed response event of each session. Nil disables
	// the emission.
	observeTimeToFirstToken func(pool, runtimeClass, isolationProfile string, seconds float64)
	// firstTokenObserved tracks sessions that already recorded their
	// §6.3 / §16.1 TTFT observation. Entries are added on the first
	// qualifying response event and cleared on the terminal lifecycle
	// transition so the map size scales with concurrently-streaming
	// sessions, not lifetime sessions. Keyed by session id, value is
	// a sentinel struct{} placeholder. spec: §6.3 line 356.
	firstTokenObserved sync.Map
	// userCredChecker reports whether a usable user-scoped credential
	// exists for (tenant, user, provider). Nil in v1 because user-source
	// lease delivery (the §4.9 materializedConfig path) is not yet wired;
	// the §4.9 router resolves user sources only when this reports true.
	userCredChecker func(ctx context.Context, tenantID, userID, provider string) bool

	// lifecycleAudit, when set, receives the §7.1 / §16.6 session
	// lifecycle audit events (session.created and the terminal
	// session.{completed,failed,cancelled,expired}) the gateway writes
	// to the §11.7 hash-chained audit log. Nil disables the emission;
	// the billing and operational-event side effects are unaffected.
	// spec: §7.1, §16.6.
	lifecycleAudit LifecycleAuditSink

	// interactionAudit, when set, receives the §7.2 / §11.7 / §16.7
	// interaction-resolution audit events emitted by the §15.1
	// tool-use approve/deny and elicitation respond/dismiss endpoints.
	// Nil disables the emission; the resolution itself proceeds either
	// way. spec: §7.2 table lines 124-127; §11.7; §16.7. F-7.2.8.
	interactionAudit InteractionAuditSink

	// toolApprovalWaits, when set, is the §7.2 tool-use approval waiter
	// registry the approve/deny endpoints signal so a blocked executor
	// read (the pod runtime awaiting a tool-call verdict) unblocks. Nil
	// leaves the resolution endpoints recording the interaction phase
	// only — the dev / non-pod posture where nothing blocks on the
	// verdict. spec: §7.2 lines 124-125. F-7.2.18.
	toolApprovalWaits *toolapproval.Registry

	// treeCycleObserver, when set, receives a §8.9 cycle observation
	// whenever the /v1/sessions/{id}/tree walker hits a repeated node
	// in the ParentSessionID lineage. Nil disables the emission and
	// the walker still truncates the cycle so the response remains
	// well-formed. spec: §8.9 line 1003; F-8.9.10.
	treeCycleObserver TreeCycleObserver

	// inputWaits, when set, makes the REST POST /v1/sessions/{id}/messages
	// handler honor §7.2 path 1: a request body whose `inReplyTo`
	// matches an outstanding `lenny/request_input` call resolves the
	// blocked tool call directly instead of being delivered to the
	// executor. Nil leaves the REST surface at path 2 only (executor
	// delivery), matching the pre-F-7.2.14 behaviour. The same
	// registry is shared with the MCP `lenny/send_message` /
	// `lenny/request_input` pair so the two transports route to the
	// same blocked tool call. spec: §7.2 line 317.
	inputWaits *inputwait.Registry

	// deriveLock, when set, serializes concurrent /v1/sessions/{id}/derive
	// calls on the same source session per §7.1 line 92. The session-
	// server holds the lock around the workspace-snapshot read; it is
	// released as soon as the snapshot reference resolves, mirroring the
	// spec's "release the lock before the copy is safe" guarantee.
	// Production wires a derivelock.Redis implementation backed by the
	// shared Redis client; tests and the minimal gateway fall back to
	// derivelock.Memory (per-source sync.Mutex). When nil, the in-memory
	// store mutex serializes within the running process and no cross-
	// replica protection is in force — the legacy minimal-gateway
	// posture. spec: §7.1 line 92.
	deriveLock derivelock.Lock

	// defaultRetention is the §7.1 line 77 default artifact-retention
	// window stamped on every session at create time and rolled forward
	// at the terminal transition. A non-positive value falls through to
	// DefaultArtifactRetention.
	// spec: §7.1 line 77 — "configurable TTL (default: 7 days ...)".
	defaultRetention time.Duration

	// retryPolicyCaps holds the §7.3 deployer caps applied to every
	// client-supplied RetryPolicy at admission. A zero field in the cap
	// disables that clamp so deployer "unlimited" semantics survive. The
	// gateway wires these to the watchdog config so a session's clamped
	// caps cannot exceed the platform-wide bounds the watchdog itself
	// enforces. F-7.3.1 / F-7.3.24.
	// spec: §7.3 lines 377-393.
	retryPolicyCaps session.RetryPolicyCaps

	// envBlocklist is the §14 deployer-configured env-var blocklist
	// applied to a CreateSessionRequest's `env` field. Never nil after
	// New (defaults to the platform default blocklist alone). spec: §14
	// line 105. F-14.1.12.
	envBlocklist *envblock.Matcher

	// incSessionResumeAttempt, when set, increments the §16.1
	// lenny_session_resume_attempts_total{pool, outcome} counter for the
	// resume call. Nil disables the emission. spec: §16.1 catalog.
	// F-7.3.10.
	incSessionResumeAttempt func(pool, outcome string)

	// incSessionRetry, when set, increments the §16.1
	// lenny_session_retry_total{failure_class} counter for the retry.
	// Nil disables the emission. spec: §16.1 catalog. F-7.3.10.
	incSessionRetry func(failureClass string)

	// incWarmpoolWarmupFailure, when set, increments the §16.1 line 124
	// lenny_warmpool_warmup_failure_total{error_type} counter for one
	// warm-pool startup failure. Nil disables the emission. spec: §16.1
	// line 124, §7.3 line 387 — F-7.5.9.
	incWarmpoolWarmupFailure func(errorType string)

	// uploadTokenTTL is the §7.1 line 58 upload-token expiry stamped on
	// every minted token. The gateway sets this equal to
	// `maxCreatedStateTimeoutSeconds` so the token deadline matches the
	// `created` state deadline. A zero value falls through to
	// uploadtoken.DefaultTTL (300s). spec: §7.1 line 58. F-7.4.7.
	uploadTokenTTL time.Duration

	// uploadAborts is the §7.4 line 463 upload-abort registry. The
	// upload handler registers a per-session abort signal for the
	// duration of its body-read + blob.Put; the finalize handler closes
	// the signal after the row transitions out of the upload-admitting
	// state so any in-flight stream surfaces UPLOAD_CHANNEL_CLOSED.
	// Always non-nil after New. spec: §7.4 line 463. F-7.4.16.
	uploadAborts *uploadAbortRegistry

	// uploadLimits enforces the §11.1 line 10-11 per-session and global
	// concurrent-upload caps and the per-session cumulative upload-size
	// cap. Nil when no §11.1 upload cap is configured (the pass-through
	// posture); every call site tolerates a nil limiter. spec: §11.1
	// lines 10-11. F-11.1.5, F-11.1.6.
	uploadLimits *uploadLimiter
}

// DefaultMaxOrphanTasksPerTenant is the §8.10 cap on a tenant's active
// orphan tasks. When a `detach` cascade would push the tenant over the
// cap, the gateway falls back to `cancel_all` so orphans cannot
// accumulate without bound.
const DefaultMaxOrphanTasksPerTenant = 100

// DefaultEvalPerSessionPerMin and DefaultEvalPerTenantPerMin are the
// §10.7 eval-submission rate-limit defaults: 100 submissions per minute
// keyed by session_id and 10000 per minute across a tenant's sessions.
// Both are operator-tunable via the gateway flags
// `--eval-rate-limit-per-session-per-min` / `-per-tenant-per-min`.
// spec: §10.7 line 938. F-10.7.4.
const (
	DefaultEvalPerSessionPerMin = 100
	DefaultEvalPerTenantPerMin  = 10000
)

// resolveEvalLimit maps an Options eval rate-limit value onto the
// effective per-minute limit: zero selects def (the spec default), a
// negative value disables the scope (returned as 0), and a positive
// value is used verbatim.
func resolveEvalLimit(v, def int) int {
	if v == 0 {
		return def
	}
	if v < 0 {
		return 0
	}
	return v
}

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

// TreeBudgetReturner releases the §12.4 delegation tree budget a
// settled child consumed. The §8.2 line 130 completed-subtree offload
// decrements the tree's maxTreeMemoryBytes counter when a node is
// archived and the per-parent parallel_children counter when the child
// stops running, so a long-running tree's freed concurrency slot and
// in-memory footprint are returned to the budget. *treebudget.Reserver
// implements it. A nil returner on the Server disables the decrement.
type TreeBudgetReturner interface {
	Return(ctx context.Context, r treebudget.Reservation) error
}

// DelegationHighWatermarkReader reads and clears the §8.3 line 379
// per-tree parallel-children high-watermark when a delegation tree
// completes. *treebudget.Reserver implements it. Nil on the Server
// disables the §16.1 high-watermark observation (the in-process
// minimal path with no Redis-backed budget). F-8.9.6.
type DelegationHighWatermarkReader interface {
	ObserveHighWatermark(ctx context.Context, rootSessionID string) (value int64, found bool, err error)
}

// DelegationHighWatermarkObserver records the §8.3 line 379 per-tree
// parallel-children high-watermark onto the
// `lenny_delegation_parallel_children_high_watermark` histogram.
// *gatewaymetrics.Metrics implements it. Nil drops the observation.
// F-8.9.6.
type DelegationHighWatermarkObserver interface {
	ObserveDelegationParallelChildrenHighWatermark(pool, tenantID string, value int64)
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

	// DeriveLock, when set, serializes concurrent /v1/sessions/{id}/derive
	// calls on the same source session per §7.1 line 92. Production
	// wires derivelock.NewRedis against the shared Redis client; tests
	// and the single-replica minimal gateway can wire derivelock.NewMemory
	// or leave this nil (the in-memory store mutex serializes within the
	// running process and is correct for a single replica). On
	// contention the handler returns 429 DERIVE_LOCK_CONTENTION.
	// spec: §7.1 line 92.
	DeriveLock derivelock.Lock

	// LifecycleAuditSink, when set, receives the §7.1 / §16.6 session
	// lifecycle audit events (session.created and the terminal
	// session.{completed,failed,cancelled,expired}) for the §11.7
	// hash-chained audit log. Production wires this to the audit
	// appender; nil disables the emission.
	LifecycleAuditSink LifecycleAuditSink

	// InteractionAuditSink, when set, receives the §7.2 / §11.7 /
	// §16.7 tool-use approve/deny and elicitation respond/dismiss
	// audit events emitted by the §15.1 resolution endpoints.
	// Production wires this to the audit appender; nil disables the
	// emission and the resolution still proceeds.
	// spec: §7.2 table lines 124-127. F-7.2.8.
	InteractionAuditSink InteractionAuditSink

	// ToolApprovalWaits, when set, is the §7.2 tool-use approval waiter
	// registry shared with the ToolApprovalGate the pod executor calls.
	// The approve/deny endpoints deliver the verdict onto it so the
	// blocked runtime tool call unblocks. Nil leaves the endpoints
	// recording the interaction phase only. spec: §7.2 lines 124-125.
	// F-7.2.18.
	ToolApprovalWaits *toolapproval.Registry

	// TreeCycleObserver, when set, receives a §8.9 cycle observation
	// when /v1/sessions/{id}/tree hits a repeated node in the
	// ParentSessionID lineage. Production wires this to the
	// `delegation.tree_cycle_detected` audit event plus the §16.1
	// `lenny_delegation_tree_cycle_detected_total` counter; nil
	// disables the emission and the walker still truncates the cycle.
	// spec: §8.9 line 1003; F-8.9.10.
	TreeCycleObserver TreeCycleObserver

	// InputWaits is the shared §8.5 `lenny/request_input` pending-call
	// registry. When set, the REST POST /v1/sessions/{id}/messages
	// handler resolves a §7.2 path 1 `inReplyTo` directly against the
	// registry instead of routing through the executor. Production
	// wires the same `*inputwait.Registry` instance into both the
	// sessionserver and the MCP tools deps; tests can leave it nil for
	// pre-F-7.2.14 behaviour. spec: §7.2 line 317. F-7.2.14.
	InputWaits *inputwait.Registry

	// DefaultRetention overrides the §7.1 line 77 default artifact-
	// retention window. A non-positive value selects
	// DefaultArtifactRetention (7 days). Deployers tune it via the
	// gateway --session-artifact-retention-seconds flag.
	DefaultRetention time.Duration

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

	// UploadTokenTTL overrides the upload-token expiry stamped on every
	// minted token. Per §7.1 line 58 the token TTL equals
	// `maxCreatedStateTimeoutSeconds`; the gateway threads the same
	// configured timeout through this field, the watchdog's
	// MaxCreatedSeconds, and the createdsweeper's Timeout so the three
	// budgets never drift. A non-positive value falls through to
	// uploadtoken.DefaultTTL (300s). spec: §7.1 line 58. F-7.4.7.
	UploadTokenTTL time.Duration

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

	// Artifacts is the §12.5 artifact catalog. When set, the §8.10
	// archive materialization lists a settled child's catalogued
	// `lenny-blob://` artifacts to populate the §8.8
	// TaskResult.output.artifactRefs. Nil leaves artifactRefs empty.
	// spec: §8.8 lines 888-896. F-8.8.2.
	Artifacts artifactcatalog.Store

	// Events is the §15.1 session event bus backing the SSE stream.
	// When nil, `GET /v1/sessions/{id}/events` returns
	// `503 EVENT_STREAM_UNAVAILABLE` and message injection skips
	// event publication.
	Events *sessionevents.Bus

	// DualStore is the §10.1 dual-store degraded-mode gate. When it
	// reports Unavailable (Postgres and Redis simultaneously
	// unreachable), `session.create` is rejected with 503 +
	// `Retry-After: 10` because the create requires a Postgres INSERT.
	// Nil leaves the gate open (the in-memory / single-store posture
	// never enters dual-store degraded mode). spec: §10.1 item 2.
	DualStore DualStoreGate

	// Messaging is the §7.2 session-inbox + DLQ coordinator. When set,
	// the gateway migrates a session's in-memory inbox to the DLQ on
	// resume_pending and drains the inbox+DLQ (emitting
	// message_expired) on terminal transition. Nil disables messaging
	// durability (the dev / no-Redis posture). spec: §7.2 lines
	// 305-311, 343.
	Messaging *sessioninbox.Coordinator

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

	// StickyCache is the §10.7 `sticky: user` variant-assignment cache. When
	// set, the ExperimentRouter reads a `mode: external` assignment from the
	// cache before calling the OpenFeature provider and writes fresh results
	// back (§10.7 line 831). Nil re-evaluates every experiment fresh, which is
	// also the §12.4 Redis-outage fail-open path.
	StickyCache StickyCache

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

	// MultiTenant mirrors the gateway's `--multi-tenant` flag /
	// `auth.multiTenant` Helm value. When true, the §10.2 RBAC gate on
	// session-mutating and session-read endpoints fails closed for an
	// authenticated principal that carries no roles (the §10.2
	// permission matrix is unconditional in multi-tenant deployments).
	// When false, the historical no-role fall-through is preserved so
	// the single-tenant minimal gateway (no OIDC, dev-header path) and
	// pre-RBAC service tokens still reach the handler.
	// spec: §10.2 lines 256–264. F-10.2.4.
	MultiTenant bool

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

	// AdmissionRateLimitCounter is the §11.1 line 7 per-minute counter
	// used for the per-runtime and per-pool admission scopes enforced at
	// session creation. Nil disables both scopes (the global, per-user,
	// and per-tenant scopes run in the §11.1 HTTP middleware regardless).
	// Production wires the shared Redis-backed counter so the limit holds
	// across replicas. spec: §11.1 line 7. F-11.1.2.
	AdmissionRateLimitCounter ratelimit.Counter

	// PerRuntimePerMinute caps session-creation requests against a single
	// runtime per minute. Zero or less leaves the per-runtime scope
	// unlimited. spec: §11.1 line 7. F-11.1.2.
	PerRuntimePerMinute int

	// PerPoolPerMinute caps session-creation requests against a single
	// resolved warm pool per minute. The scope is skipped when no pool
	// resolves (the Postgres-only posture). Zero or less leaves the
	// per-pool scope unlimited. spec: §11.1 line 7. F-11.1.2.
	PerPoolPerMinute int

	// RateLimitMetrics, when set, receives the §11.1 rejection counter
	// and counter-failure bump for the per-runtime / per-pool gate.
	// *gatewaymetrics.Metrics satisfies it. Nil leaves the gate
	// enforcing without observability. spec: §11.1 line 7. F-11.1.2.
	RateLimitMetrics AdmissionRateLimitMetrics

	// MaxConcurrentSessionsGlobal caps the gateway-wide count of live
	// (non-terminal) sessions across every tenant. Zero or less leaves
	// the global concurrent-session scope unlimited. Operator-tunable via
	// the gateway Helm value `gateway.maxConcurrentSessionsGlobal`.
	// spec: §11.1 line 8 (Concurrency limits — global). F-11.1.3.
	MaxConcurrentSessionsGlobal int

	// MaxConcurrentSessionsPerUser caps the count of live (non-terminal)
	// sessions a single user may hold within their tenant, so one user
	// cannot monopolize the tenant's concurrent-session capacity. Zero or
	// less leaves the per-user scope unlimited. Operator-tunable via
	// `gateway.maxConcurrentSessionsPerUser`.
	// spec: §11.1 line 8 (Concurrency limits — per-user). F-11.1.3.
	MaxConcurrentSessionsPerUser int

	// MaxConcurrentSessionsPerRuntime caps the count of live
	// (non-terminal) sessions targeting a single runtime within a tenant,
	// so one runtime cannot be flooded with concurrent sessions. Zero or
	// less leaves the per-runtime scope unlimited. Operator-tunable via
	// `gateway.maxConcurrentSessionsPerRuntime`.
	// spec: §11.1 line 8 (Concurrency limits — per-runtime). F-11.1.3.
	MaxConcurrentSessionsPerRuntime int

	// EvalRateLimitCounter is the §10.7 per-minute counter for the
	// eval-submission rate limit (per-session and per-tenant scopes on
	// POST /v1/sessions/{id}/eval). Nil disables eval rate limiting.
	// Production wires the same Redis-backed counter as
	// AdmissionRateLimitCounter so the limit holds across replicas.
	// spec: §10.7 line 938. F-10.7.4.
	EvalRateLimitCounter ratelimit.Counter

	// EvalPerSessionPerMinute caps eval submissions against a single
	// session per minute (§10.7 `evalRateLimit.perSessionPerMinute`).
	// Zero selects DefaultEvalPerSessionPerMin (100); a negative value
	// disables the per-session scope. spec: §10.7 line 938. F-10.7.4.
	EvalPerSessionPerMinute int

	// EvalPerTenantPerMinute caps eval submissions across all of a
	// tenant's sessions per minute (§10.7 `evalRateLimit.perTenantPerMinute`).
	// Zero selects DefaultEvalPerTenantPerMin (10000); a negative value
	// disables the per-tenant scope. spec: §10.7 line 938. F-10.7.4.
	EvalPerTenantPerMinute int

	// Sealer, when set, takes the §7.1 final workspace snapshot when a
	// session reaches a terminal state. Nil disables seal-and-export.
	Sealer Sealer

	// WorkspaceSealMaxDuration bounds the §7.1 seal-and-export retry
	// window (maxWorkspaceSealDurationSeconds). A seal that does not
	// succeed within this window transitions the session to failed with
	// reason workspace_seal_timeout. A non-positive value selects
	// DefaultWorkspaceSealMaxDuration (300s, the spec default).
	// spec: §7.1 line 112.
	WorkspaceSealMaxDuration time.Duration

	// ObserveWorkspaceSealDuration, when set, records the §7.1 line 112
	// lenny_workspace_seal_duration_seconds{pool,outcome} histogram.
	// outcome is "success" or "timeout". Nil disables the emission.
	ObserveWorkspaceSealDuration func(pool, outcome string, seconds float64)

	// RecordSessionTerminal, when set, records the §16.1 lines 161-163 /
	// §10.7 rollback-trigger metric family at every terminal session
	// transition (lenny_session_total, lenny_session_error_total, and
	// lenny_session_duration_seconds). sessionType is the §5.2
	// ExecutionMode; variantID is the §10.7 enrollment. Nil disables the
	// emission. spec: §10.7 lines 1120-1132, §16.1 lines 161-163.
	RecordSessionTerminal func(tenantID, sessionType, variantID string, isError bool, seconds float64)

	// ObserveEvalScore, when set, records one §16.1 line 164
	// lenny_eval_score observation per submitted eval run. Nil disables
	// the emission. spec: §10.7 line 1128, §16.1 line 164.
	ObserveEvalScore func(tenantID, scorer, variantID string, score float64)

	// SetExperimentTargetingCircuitOpen, when set, reports the §10.7
	// SCL-023 targeting circuit-breaker open/closed transitions through
	// the lenny_experiment_targeting_circuit_open gauge. Nil disables the
	// gauge emission; the breaker still gates the OpenFeature call.
	// spec: §10.7 line 838, §16.1 line 64.
	SetExperimentTargetingCircuitOpen func(tenantID, provider string, open bool)

	// SealSleep overrides the seal-retry backoff wait. Production leaves
	// it nil (a context-aware time.Sleep); tests inject a no-op so the
	// bounded-backoff loop runs without real delays.
	SealSleep func(ctx context.Context, d time.Duration) bool

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

	// TreeBudgetReturner, when set, releases the §12.4 delegation tree
	// budget a settled child consumed: the §8.2 line 130 maxTreeMemoryBytes
	// offload decrement and the per-parent parallel_children decrement
	// fire once per child as it reaches a terminal state. Nil disables
	// the decrement (developer mode without Redis-backed counters).
	TreeBudgetReturner TreeBudgetReturner

	// HighWatermarkReader and HighWatermarkObserver wire the §8.3 line
	// 379 per-tree parallel-children high-watermark observation: when a
	// delegation tree's root session settles, the gateway reads the
	// recorded maximum simultaneous in-flight children and observes it
	// onto the §16.1 histogram. Both nil disables the observation.
	// F-8.9.6.
	HighWatermarkReader   DelegationHighWatermarkReader
	HighWatermarkObserver DelegationHighWatermarkObserver

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

	// UploadMetrics, when set, receives the §16.1 upload-handler
	// observations (lenny_upload_bytes_total, lenny_upload_queue_depth).
	// *PromUploadMetrics satisfies it. Nil drops the observations (tests
	// and the minimal gateway). spec: §16.1 — F-13.4.12.
	UploadMetrics UploadHandlerMetrics

	// MaxConcurrentUploadsPerSession is the §11.1 line 10 per-session
	// concurrent-upload admission cap: the gateway rejects a new upload
	// with 429 RATE_LIMITED once a session already holds this many
	// in-flight uploads on the replica. Zero leaves the per-session
	// concurrency scope unlimited. spec: §11.1 line 10. F-11.1.5.
	MaxConcurrentUploadsPerSession int

	// MaxConcurrentUploadsGlobal is the §11.1 line 10 global
	// concurrent-upload admission cap: the gateway rejects a new upload
	// with 429 RATE_LIMITED once the replica already holds this many
	// in-flight uploads across all sessions. Zero leaves the global
	// concurrency scope unlimited. spec: §11.1 line 10. F-11.1.5.
	MaxConcurrentUploadsGlobal int

	// MaxUploadBytesPerSession is the §11.1 line 11 per-session
	// cumulative upload-size cap: the gateway rejects an upload with 429
	// QUOTA_EXCEEDED once the sum of all uploads in a session would
	// exceed this value. The per-file (per-blob) cap is the separate
	// UploadMaxBodyBytes ceiling. Zero leaves the per-session size scope
	// unlimited. spec: §11.1 line 11. F-11.1.6.
	MaxUploadBytesPerSession int64

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

	// SlotReplacement, when set, increments
	// lenny_slot_pod_replacement_total{pool} when the §5.2 concurrent-
	// workspace slot retry policy drains an unhealthy pod (ceil(maxConcurrent
	// /2) slots failed or leaked within the rolling window) for replacement.
	// Nil disables the emission. spec: §5.2 "whole-pod replacement trigger".
	SlotReplacement func(pool string)

	// ObserveStartupDuration, when set, records the §6.3 line 348
	// end-to-end pod-warm session startup latency (pod claim through
	// agent session ready, excluding upload and workspace
	// materialization) for each successful start. Nil disables it.
	// spec: §16.1 line 14, §6.3 line 348.
	ObserveStartupDuration func(pool, runtimeClass, isolationProfile string, seconds float64)

	// ObserveStartupPhase, when set, records the §6.3 line 372 latency
	// of one hot-path startup phase (pod_claim,
	// workspace_materialization, setup_commands, credential_assignment,
	// agent_session_start). Nil disables it. spec: §6.3 line 372.
	ObserveStartupPhase func(phase, runtimeClass string, seconds float64)

	// ObserveTimeToFirstToken, when set, records the §6.3 line 356 /
	// §16.1 line 15 TTFT histogram on the first agent-streamed
	// response event of each session: session start request to first
	// streaming event emitted to the client. Nil disables the
	// emission. spec: §16.1 line 15, §6.3 line 356.
	ObserveTimeToFirstToken func(pool, runtimeClass, isolationProfile string, seconds float64)

	// RetryPolicyCaps holds the §7.3 deployer caps applied to a
	// client-supplied RetryPolicy at admission. The gateway clamps each
	// populated client field against the matching cap and falls through
	// to the cap as the effective value when the client supplied nothing.
	// A zero field skips that clamp so deployer "unlimited" semantics
	// survive. Production wires these to the watchdog config so the
	// per-session cap can never exceed the platform-wide bound. F-7.3.1.
	// spec: §7.3 lines 377-393.
	RetryPolicyCaps session.RetryPolicyCaps

	// EnvVarBlocklist extends the §14 platform default env-var blocklist
	// with deployer-supplied entries (exact names or `*` globs). The
	// platform default is always merged in first so an operator can
	// extend but not reduce it. A nil slice leaves the platform default
	// in force. spec: §14 line 105. F-14.1.12.
	EnvVarBlocklist []string

	// IncSessionResumeAttempt, when set, increments the §16.1
	// lenny_session_resume_attempts_total{pool, outcome} counter on
	// every POST /v1/sessions/{id}/resume call (after the precondition
	// check passes). outcome is "success" when the row transitions to
	// running, "failure" when the pod-claim step fails. Nil disables
	// the emission. spec: §16.1 catalog. F-7.3.10.
	IncSessionResumeAttempt func(pool, outcome string)

	// IncSessionRetry, when set, increments the §16.1
	// lenny_session_retry_total{failure_class} counter on every retry
	// of a logical session (a successful resume that bumps the
	// recovery_generation counter is the v1 retry path). The failure
	// class label echoes the row's §7.1 FailureClass at retry time —
	// "unknown" for a session that has no recorded class. Nil disables
	// the emission. spec: §16.1 catalog. F-7.3.10.
	IncSessionRetry func(failureClass string)

	// IncWarmpoolWarmupFailure, when set, increments the §16.1 line 124
	// lenny_warmpool_warmup_failure_total{error_type} counter for a
	// warm-pool startup failure. error_type is the §7.3 line 387
	// non-retryable failure category the gateway classified
	// (`setup_command_failed`, etc.). Nil disables the emission.
	// spec: §16.1 line 124, §7.3 line 387 — F-7.5.9.
	IncWarmpoolWarmupFailure func(errorType string)
}

// New returns a Server bound to the supplied store.
func New(store sessionstore.Store, opts Options) *Server {
	s := &Server{
		store:                    store,
		clock:                    opts.Clock,
		idFn:                     opts.IDFunc,
		deriveAuditSink:          opts.DeriveAuditSink,
		deriveLock:               opts.DeriveLock,
		uploadIssuer:             opts.UploadTokenIssuer,
		uploadVerifier:           opts.UploadTokenVerifier,
		blobs:                    opts.Blobs,
		executor:                 opts.Executor,
		transcripts:              opts.Transcripts,
		artifacts:                opts.Artifacts,
		evals:                    opts.Evals,
		memory:                   opts.Memory,
		experiments:              opts.Experiments,
		pools:                    opts.Pools,
		experimentReporter:       opts.ExperimentRejections,
		stickyCache:              opts.StickyCache,
		events:                   opts.Events,
		dualStore:                opts.DualStore,
		messaging:                opts.Messaging,
		interactions:             opts.Interactions,
		usage:                    opts.Usage,
		users:                    opts.Users,
		billing:                  opts.Billing,
		tenants:                  opts.Tenants,
		storageQuota:             opts.StorageQuota,
		defaultIsoProf:           opts.DefaultIsolationProfile,
		devMode:                  opts.DevMode,
		multiTenant:              opts.MultiTenant,
		podBinder:                opts.PodBinder,
		podRegistry:              opts.PodRegistry,
		agentNamespace:           opts.AgentNamespace,
		admissionRL:              opts.AdmissionRateLimitCounter,
		perRuntimePerMin:         opts.PerRuntimePerMinute,
		perPoolPerMin:            opts.PerPoolPerMinute,
		rlMetrics:                opts.RateLimitMetrics,
		maxConcSessGlobal:        opts.MaxConcurrentSessionsGlobal,
		maxConcSessPerUser:       opts.MaxConcurrentSessionsPerUser,
		maxConcSessPerRuntime:    opts.MaxConcurrentSessionsPerRuntime,
		evalRL:                   opts.EvalRateLimitCounter,
		evalPerSessionPerMin:     resolveEvalLimit(opts.EvalPerSessionPerMinute, DefaultEvalPerSessionPerMin),
		evalPerTenantPerMin:      resolveEvalLimit(opts.EvalPerTenantPerMinute, DefaultEvalPerTenantPerMin),
		sealer:                   opts.Sealer,
		sealMaxDuration:          opts.WorkspaceSealMaxDuration,
		sealSleep:                opts.SealSleep,
		observeSealDuration:      opts.ObserveWorkspaceSealDuration,
		recordSessionTerminal:    opts.RecordSessionTerminal,
		observeEvalScore:         opts.ObserveEvalScore,
		partialManifestCleaner:   opts.PartialManifestCleaner,
		evictionStateLookup:      opts.EvictionStateLookup,
		partialManifestLookup:    opts.PartialManifestLookup,
		treeArchive:              opts.TreeArchive,
		treeBudgetReturner:       opts.TreeBudgetReturner,
		hwmReader:                opts.HighWatermarkReader,
		hwmObserver:              opts.HighWatermarkObserver,
		maxOrphanTasks:           opts.MaxOrphanTasksPerTenant,
		runtimes:                 opts.Runtimes,
		environments:             opts.Environments,
		tenantAccess:             opts.TenantAccess,
		opsEmitter:               opts.OpsEmitter,
		refResolver:              opts.RefResolver,
		credPools:                opts.CredentialPools,
		defaultNoEnvPolicy:       opts.DefaultNoEnvironmentPolicy,
		customRoles:              opts.CustomRoles,
		interceptors:             opts.Interceptors,
		policyAuditSink:          opts.PolicyAuditSink,
		uploadSubsystem:          opts.UploadSubsystem,
		uploadMetrics:            opts.UploadMetrics,
		resumeWindow:             opts.ResumeWindow,
		sessionLogHook:           opts.SessionLogHook,
		warmupEstimateSeconds:    opts.WarmupEstimateSeconds,
		credRouter:               opts.CredentialRouter,
		preclaimMismatch:         opts.PreclaimMismatch,
		slotHealth:               slothealth.New(),
		slotReplacement:          opts.SlotReplacement,
		observeStartupDuration:   opts.ObserveStartupDuration,
		observeStartupPhase:      opts.ObserveStartupPhase,
		observeTimeToFirstToken:  opts.ObserveTimeToFirstToken,
		lifecycleAudit:           opts.LifecycleAuditSink,
		interactionAudit:         opts.InteractionAuditSink,
		toolApprovalWaits:        opts.ToolApprovalWaits,
		treeCycleObserver:        opts.TreeCycleObserver,
		inputWaits:               opts.InputWaits,
		defaultRetention:         opts.DefaultRetention,
		retryPolicyCaps:          opts.RetryPolicyCaps,
		envBlocklist:             envblock.New(opts.EnvVarBlocklist),
		incSessionResumeAttempt:  opts.IncSessionResumeAttempt,
		incSessionRetry:          opts.IncSessionRetry,
		incWarmpoolWarmupFailure: opts.IncWarmpoolWarmupFailure,
		uploadTokenTTL:           opts.UploadTokenTTL,
		uploadAborts:             newUploadAbortRegistry(),
		uploadLimits: newUploadLimiter(
			opts.MaxConcurrentUploadsPerSession,
			opts.MaxConcurrentUploadsGlobal,
			opts.MaxUploadBytesPerSession,
		),
	}
	if s.defaultRetention <= 0 {
		// spec: §7.1 line 77 — default the artifact-retention window to
		// 7 days when the deployer leaves it unset.
		s.defaultRetention = DefaultArtifactRetention
	}
	if s.sealMaxDuration <= 0 {
		// spec: §7.1 line 112 — maxWorkspaceSealDurationSeconds default 300s.
		s.sealMaxDuration = DefaultWorkspaceSealMaxDuration
	}
	if s.sealSleep == nil {
		s.sealSleep = sleepWithContext
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
	// spec: §10.7 lines 835-844 (SCL-023) — the per-tenant targeting
	// circuit breaker shares the server clock so tests drive the open /
	// half-open transitions deterministically.
	s.targetingBreaker = newTargetingBreaker(s.clock, opts.SetExperimentTargetingCircuitOpen)
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
	// §10.7 line 936 — eval submission is gated on the dedicated
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
	// spec: §7.2 lines 168-169 — the interrupt path signals the runtime
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

	// Metadata is the §7.1 line 6 client-supplied
	// CreateSession(..., metadata) payload — a flat string→string map
	// of caller annotations preserved verbatim for the session
	// lifetime. Non-string values rejected at decode with
	// 400 VALIDATION_ERROR so the on-row shape stays bounded. The §15.1
	// GET envelope echoes this back so a client that lost the create
	// response can retrieve its own annotations. F-7.3.20.
	// spec: §7.1 line 6 — "CreateSession(runtime, pool, retryPolicy,
	// metadata)".
	Metadata map[string]string `json:"metadata,omitempty"`

	// RetryPolicy is the §7.3 client-supplied retry policy. The gateway
	// clamps each field against the deployer caps (RetryPolicyCaps) at
	// admission so a client cannot grow its budget past the platform
	// bounds; an unset/zero value falls through to the corresponding
	// cap as the effective value. Negative values reject as
	// 400 VALIDATION_ERROR. The §15.1 GET envelope echoes the clamped
	// policy back. F-7.3.1.
	// spec: §7.3 lines 377-393.
	RetryPolicy *session.RetryPolicy `json:"retryPolicy,omitempty"`

	// Env is the §14 client-supplied environment-variable map injected
	// into the agent session. Every key is validated against the deployer
	// blocklist at admission; a blocked key rejects with
	// 400 ENV_VAR_BLOCKLISTED. The §15.1 GET envelope echoes it back.
	// spec: §14 lines 47-50, 105. F-14.1.12.
	Env map[string]string `json:"env,omitempty"`

	// Pool is the §14 / §14.1 line 311 client-requested target pool. The
	// minimal gateway records it for echo and admission pool-scope; the
	// resolved pool the gateway schedules against is reported separately.
	// spec: §14 example; §14.1 line 311. F-14.1.14.
	Pool string `json:"pool,omitempty"`

	// Timeouts is the §14 per-session timeout override block. The gateway
	// rejects a maxSessionAge that exceeds the runtime's
	// limits.maxSessionAge. spec: §14 line 154. F-14.1.14.
	Timeouts *sessionstore.SessionTimeouts `json:"timeouts,omitempty"`

	// CredentialPolicy is the §14 per-session credentialPolicy override.
	// A per-session override can only restrict, never expand, the tenant
	// policy. spec: §14 credentialPolicy; §4.9 lines 1310, 1336. F-14.1.14.
	CredentialPolicy *sessionstore.CredentialPolicyOverride `json:"credentialPolicy,omitempty"`

	// DelegationLease is the §14 client-requested delegation lease bounds
	// {maxDepth, maxChildrenTotal, delegationPolicyRef}. spec: §14 lines
	// 75-79. F-14.1.14.
	DelegationLease *sessionstore.DelegationLeaseRequest `json:"delegationLease,omitempty"`

	// RuntimeOptions is the §14 per-runtime discriminated-union options
	// blob (≤64 KB). Validated against the target runtime's
	// runtimeOptionsSchema when registered; when no schema is registered
	// a RuntimeOptionsUnschematized warning is emitted. spec: §14 line
	// 155. F-14.1.14 / F-14.1.15.
	RuntimeOptions json.RawMessage `json:"runtimeOptions,omitempty"`
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

	// SessionIsolationLevel echoes the §7.1 sessionIsolationLevel object
	// so a client that lost the create response can inspect the session's
	// isolation posture through GET /v1/sessions/{id} and the list. The
	// field is populated from the persisted §5.3 isolation profile and is
	// stable for the lifetime of the session (the profile never changes
	// after creation).
	// spec: §7.1 line 75 — "GET /v1/sessions/{id} also returns
	// sessionIsolationLevel in the session metadata ... does not change
	// for the lifetime of the session".
	SessionIsolationLevel SessionIsolationLevel `json:"sessionIsolationLevel"`

	// Metadata echoes the §7.1 line 6 client-supplied metadata payload
	// the session was created with. Omitted from the envelope when the
	// client submitted no metadata. F-7.3.20.
	// spec: §7.1 line 6.
	Metadata map[string]string `json:"metadata,omitempty"`

	// RetryPolicy echoes the §7.3 effective retry policy resolved at
	// session creation (the client-supplied object after clamp). Omitted
	// when the session was created with no override. F-7.3.1.
	// spec: §7.3 lines 377-393.
	RetryPolicy *session.RetryPolicy `json:"retryPolicy,omitempty"`

	// Env echoes the §14 client-supplied env map (which passed the
	// deployer blocklist at admission). Omitted when the client supplied
	// none. spec: §14 lines 47-50. F-14.1.12.
	Env map[string]string `json:"env,omitempty"`

	// Pool echoes the §14 / §14.1 client-requested target pool. Omitted
	// when the request named no pool. spec: §14.1 line 311. F-14.1.14.
	Pool string `json:"pool,omitempty"`

	// Timeouts echoes the §14 per-session timeout overrides. Omitted when
	// the client supplied none. spec: §14 line 154. F-14.1.14.
	Timeouts *sessionstore.SessionTimeouts `json:"timeouts,omitempty"`

	// CredentialPolicy echoes the §14 per-session credentialPolicy
	// override. Omitted when the client supplied none. spec: §14
	// credentialPolicy. F-14.1.14.
	CredentialPolicy *sessionstore.CredentialPolicyOverride `json:"credentialPolicy,omitempty"`

	// DelegationLease echoes the §14 client-requested delegation lease
	// bounds. Omitted when the client supplied none. spec: §14 lines
	// 75-79. F-14.1.14.
	DelegationLease *sessionstore.DelegationLeaseRequest `json:"delegationLease,omitempty"`

	// RuntimeOptions echoes the §14 per-runtime options blob the session
	// was created with. Omitted when the client supplied none. spec: §14
	// line 155. F-14.1.14.
	RuntimeOptions json.RawMessage `json:"runtimeOptions,omitempty"`

	// SetupOutput is the §7.5 line 475 captured per-command output the
	// adapter returned at setup time, plus any §7.5 line 488 synthetic
	// rejection-reason entries the gateway recorded when it rejected a
	// command at admission. Omitted when no setup commands ran and the
	// gateway never rejected one. F-7.5.4 / F-7.5.11.
	// spec: §7.5 lines 475, 488.
	SetupOutput []SetupOutputEntry `json:"setupOutput,omitempty"`

	// TaskRecord is the §8.8 TaskRecord envelope projected from the
	// session row plus its transcript: the durable, protocol-bridging
	// task-level record (schemaVersion, taskId, sessionId, state, the
	// caller/agent messages array, usage, treeUsage). Populated only on
	// the single-session read (GET /v1/sessions/{id}); the list endpoint
	// omits it to avoid a transcript fetch per row. Absent when the
	// gateway has no transcript store wired. F-8.8.1.
	// spec: §8.8 lines 806-823.
	TaskRecord *task.Record `json:"taskRecord,omitempty"`
}

// SetupOutputEntry is one §7.5 setup-command record on the §15.1 session
// envelope. spec: §7.5 lines 475, 488 — F-7.5.4 / F-7.5.11.
type SetupOutputEntry struct {
	Cmd             string `json:"cmd"`
	ExitCode        int32  `json:"exitCode"`
	Stdout          string `json:"stdout,omitempty"`
	Stderr          string `json:"stderr,omitempty"`
	DurationMs      int64  `json:"durationMs,omitempty"`
	Truncated       bool   `json:"truncated,omitempty"`
	Rejected        bool   `json:"rejected,omitempty"`
	RejectionReason string `json:"rejectionReason,omitempty"`
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
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return CreateSessionRequest{}, false
	}
	return req, true
}

// DualStoreGate reports whether this replica currently observes the
// §10.1 dual-store degraded mode (Postgres and Redis both unreachable).
// dualstore.Monitor satisfies it.
type DualStoreGate interface {
	Unavailable() bool
}

// createSession runs the §15.1 session-creation flow over an
// already-decoded request: the active-user and quota gates, the
// runtime, isolation-profile, and workspace-plan validation, the
// session-row persist, the §7.1 uploadToken mint, and the
// CreateSessionResponse.
func (s *Server) createSession(w http.ResponseWriter, r *http.Request, req CreateSessionRequest) {
	// spec: §10.1 item 2 — while both Postgres and Redis are unreachable
	// a new session.create cannot complete its Postgres INSERT, so reject
	// it with 503 + Retry-After: 10 before consuming any quota, rate, or
	// token budget. In-progress sessions are unaffected (they continue on
	// cached coordination state); only creation is suspended. F-10.1.3.
	if s.dualStore != nil && s.dualStore.Unavailable() {
		w.Header().Set("Retry-After", "10")
		s.writeError(w, http.StatusServiceUnavailable, "PLATFORM_DEGRADED",
			"session creation is suspended: the platform's coordination stores are temporarily unavailable",
			map[string]any{"reason": "dual_store_unavailable", "retryAfter": 10})
		return
	}
	if !s.requireActiveUser(w, r) {
		return
	}
	tenantID := s.resolveTenant(r)
	if !s.requireSessionQuota(w, r, tenantID) {
		return
	}
	// spec: §11.1 line 8 — global, per-user, and per-runtime
	// concurrent-session admission caps. Enforced before the rate-limit
	// and policy gates so an over-limit create consumes no rate budget
	// and reserves no token budget. The caller's subject is the per-user
	// scope key; an unauthenticated principal leaves the per-user scope
	// inert. F-11.1.3.
	concUser := ""
	if p, ok := getPrincipal(r); ok {
		concUser = p.Subject
	}
	if !s.requireConcurrencyLimits(w, r, tenantID, concUser, req.RuntimeRef) {
		return
	}
	// spec: §11.1 line 7 — per-runtime and per-pool requests-per-minute
	// admission limits. Enforced before the §4.8 policy chain (so an
	// over-limit create never reserves token budget) using the requested
	// isolation profile to resolve the pool. An empty RuntimeRef is left
	// to the required-field check below; an invalid profile resolves to
	// no pool and the per-pool scope is skipped. F-11.1.2.
	rlProfile := req.IsolationProfile
	if rlProfile == "" {
		rlProfile = s.defaultIsoProf
	}
	if !s.requireAdmissionRateLimit(w, r, tenantID, req.RuntimeRef, rlProfile) {
		return
	}
	if !s.requirePolicyChain(w, r, tenantID) {
		return
	}
	if req.RuntimeRef == "" {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "runtimeRef is required", map[string]any{"field": "runtimeRef"})
		return
	}

	// §11.1 line 13 / §10.6 — a session create that names no
	// environment is admitted only when the caller is a member of at
	// least one environment (transparent filter applies) or when the
	// tenant's noEnvironmentPolicy resolves to allow-all. The platform
	// default deny-all rejects with 403 so an empty Environment field
	// no longer bypasses the §10.6 access-path default.
	if !s.requireEnvironmentAdmission(w, r, req.Environment, req.RuntimeRef) {
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
	parsedPlan, planJSON, planWarnings, planOK := s.resolvePlanForCreate(w, r, req.WorkspacePlan)
	if !planOK {
		return
	}
	// spec: §7.5 line 477 / §5.1 line 76 — runtime setupCommandPolicy.maxCommands
	// is a per-session cap the gateway enforces before pod claim so a
	// buggy or malicious client cannot DoS the setup phase. F-7.5.5.
	if !s.enforceSetupCommandPolicy(w, r, req.RuntimeRef, parsedPlan) {
		return
	}

	// spec: §7.3 lines 377-393 — validate the client-supplied retry
	// policy before any side effect, then clamp against the deployer
	// caps so the persisted value is the effective upper bound. A nil
	// input stays nil; a non-nil input always lands on the row with
	// the deployer cap as the floor for unset fields. F-7.3.1.
	if err := session.ValidateRetryPolicy(req.RetryPolicy); err != nil {
		var rpErr *session.RetryPolicyValidationError
		if errors.As(err, &rpErr) {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
				map[string]any{"field": "retryPolicy." + rpErr.Field, "reason": rpErr.Reason})
			return
		}
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	var effectiveRetry *session.RetryPolicy
	if req.RetryPolicy != nil {
		clamped := session.ClampRetryPolicy(req.RetryPolicy, s.retryPolicyCaps)
		effectiveRetry = &clamped
	}

	// spec: §7.1 line 75 — resolve the pool-derived isolation level
	// once at create time so the executionMode / scrubPolicy halves can
	// be persisted on the row alongside isolationProfile. GET / List
	// return the same envelope across the session's lifetime
	// (persistedIsolationLevel in toResponse), so a client that lost
	// the create response or hits a different replica still sees the
	// rich level the pool resolved to.
	level := s.resolveIsolationLevel(r.Context(), req.RuntimeRef, isoProf)
	row := sessionstore.Session{
		ID:               s.idFn(),
		TenantID:         tenantID,
		UserID:           req.UserID,
		RuntimeRef:       req.RuntimeRef,
		Environment:      req.Environment,
		State:            session.StateCreated,
		IsolationProfile: isoProf,
		ExecutionMode:    level.ExecutionMode,
		ScrubPolicy:      level.ScrubPolicy,
		WorkspacePlan:    planJSON,
		Metadata:         cloneMetadata(req.Metadata),
		RetryPolicy:      effectiveRetry,
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
	// spec: §7.1 line 77 / §12.9 line 1043 — stamp the tier-keyed default
	// artifact-retention deadline at create so a session that never reaches
	// a terminal state is still eligible for GC (the retention GC treats a
	// zero deadline as ineligible, which would otherwise let the row live
	// forever). The deadline is the §12.9 per-tier default (T4 24h, T2 90d)
	// or the deployer-configured window for T3. The terminal transition
	// rolls this forward to terminal_time + the same window.
	row.RetentionExpiresAt = row.CreatedAt.Add(s.retentionForTier(r.Context(), tenantID, req.Environment))

	// spec: §14 lines 47-79, 154-155 — validate the §14 request-envelope
	// fields (env blocklist, pool, timeouts cap, credentialPolicy
	// restrict-only, delegationLease bounds, runtimeOptions schema) and
	// copy the accepted values onto the row. Rejection writes the §15.1
	// error envelope (400 ENV_VAR_BLOCKLISTED / RUNTIME_OPTIONS_INVALID /
	// VALIDATION_ERROR) and returns. F-14.1.12 / F-14.1.14 / F-14.1.15.
	envWarnings, ok := s.validateRequestEnvelope(w, r, req, tenantID, &row)
	if !ok {
		return
	}

	// §10.7: the ExperimentRouter may enroll the session in a variant,
	// rewriting its runtime/pool before the row is persisted. It fails
	// the creation closed when the variant pool is less isolated than
	// the session's profile.
	if !s.routeExperiment(w, r, &row) {
		return
	}

	// spec: §7.1 line 28 — atomicity. Mint the §7.1 step 8 uploadToken
	// BEFORE the row is persisted: on failure no session row exists, so
	// the client receives no session_id (matching the "does NOT persist
	// the session row" rule). The token's digest + expiry are stamped
	// directly on the row that will be persisted, replacing the legacy
	// "Create then Update with digest" sequence that left an orphan
	// `created`-state row when the mint failed.
	// spec: §7.1 line 58 — TTL = maxCreatedStateTimeoutSeconds; the
	// gateway threads the configured value through s.uploadTokenTTL so
	// the token deadline matches the watchdog's MaxCreatedSeconds and the
	// createdsweeper's Timeout. F-7.4.7.
	tok, parsed, err := s.uploadIssuer.IssueDetailed(row.ID, s.uploadTokenTTL)
	if err != nil {
		s.writeSessionCreationFailed(w, "upload_token_issuance_failed",
			"upload token issuance failed: "+err.Error())
		return
	}
	row.UploadTokenDigest = parsed.Digest
	row.UploadTokenExpiry = parsed.Expiry

	if err := s.store.Create(r.Context(), row); err != nil {
		// spec: §7.1 line 28 — persistence failure leaves no row behind;
		// the minted upload token's digest is never referenced because
		// the finalize/upload paths look up the digest off the
		// (non-existent) row. Return SESSION_CREATION_FAILED so the
		// client retries.
		s.writeSessionCreationFailed(w, "row_persistence_failed", err.Error())
		return
	}
	s.recordSessionCreated(r.Context(), row)
	// spec: §14 lines 100, 334, 338 — each plan warning is an "event"
	// the gateway emits, not just an echo-in-response. Publish the
	// parse-time `workspace_plan_unknown_source_type` and
	// `workspace_plan_path_collision` warnings on the same per-session
	// SSE bus that the materializer's `workspace_plan_strip_components_skip`
	// warnings ride, so Ops/audit consumers see all three async.
	// F-14.1.17. The §14 line 155 RuntimeOptionsUnschematized warning the
	// envelope validation raised rides the same plane. F-14.1.15.
	allWarnings := append(append([]workspaceplan.Warning(nil), planWarnings...), envWarnings...)
	s.publishParsePlanWarnings(row.TenantID, row.ID, allWarnings)

	base := toResponse(row)
	// spec: §7.1 line 75 — the pool-resolved level is now persisted on
	// the row, so toResponse returns it on every read. The local
	// resolved-at-admission value still wins over the persisted column
	// for the create response itself (covers a future code path that
	// might write executionMode after the row insert).
	base.SessionIsolationLevel = level
	resp := CreateSessionResponse{
		SessionResponse:       base,
		UploadToken:           tok,
		WorkspacePlanWarnings: allWarnings,
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

// persistedIsolationLevel returns the §7.1 line 75 sessionIsolationLevel
// derived from the persisted row. ExecutionMode + ScrubPolicy are
// resolved against the assigned pool at create time and frozen for the
// session lifetime; reading them off the row makes GET / List return
// the same envelope a client received from POST /v1/sessions even after
// a coordinator handoff. Rows whose ExecutionMode is empty (gateway
// never resolved a pool, or pre-migration-0084 rows) fall back to the
// session-mode default, mirroring resolveIsolationLevel's fallback
// posture so the field never understates the isolation level.
func persistedIsolationLevel(row sessionstore.Session) SessionIsolationLevel {
	mode := row.ExecutionMode
	if mode == "" {
		return defaultIsolationLevel(row.IsolationProfile)
	}
	level := SessionIsolationLevel{
		ExecutionMode:    mode,
		IsolationProfile: string(row.IsolationProfile),
	}
	switch mode {
	case string(runtimestore.ExecutionModeTask), string(runtimestore.ExecutionModeConcurrent):
		level.PodReuse = true
		level.ResidualStateWarning = true
		level.ScrubPolicy = row.ScrubPolicy
	}
	return level
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
// into the §15.1 `400 WORKSPACE_PLAN_INVALID` envelope. The one
// exception is an unsupported schemaVersion: per §14.1 line 326 the
// gateway is a live consumer that MUST reject a plan whose schemaVersion
// it does not understand with `422 WORKSPACE_PLAN_SCHEMA_UNSUPPORTED`,
// carrying `details.knownVersion` / `details.encounteredVersion` so a
// client can tell "bad plan" apart from "gateway too old." F-14.1.1.
func (s *Server) writeWorkspacePlanError(w http.ResponseWriter, err error) {
	var ve *workspaceplan.ValidationError
	if !errors.As(err, &ve) {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if ve.Reason == workspaceplan.ReasonUnsupportedSchemaVersion {
		details := map[string]any{"reason": ve.Reason}
		if ve.Field != "" {
			details["field"] = ve.Field
		}
		// spec: §14.1 line 326 — `details.knownVersion` /
		// `details.encounteredVersion` are mandatory on this envelope.
		if ve.KnownVersion != nil {
			details["knownVersion"] = *ve.KnownVersion
		}
		if ve.EncounteredVersion != nil {
			details["encounteredVersion"] = *ve.EncounteredVersion
		}
		s.writeError(w, http.StatusUnprocessableEntity, "WORKSPACE_PLAN_SCHEMA_UNSUPPORTED", ve.Error(), details)
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
		// spec: §15.1 line 979. F-14.1.19. The multi-violation report
		// rides under details.fields (plural) per the WORKSPACE_PLAN_INVALID
		// error-catalog row; details.field (singular) carries the offending
		// plan path of the first violation.
		details["fields"] = subs
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
	// spec: §8.8 lines 806-823 — the single-session read materializes the
	// §8.8 TaskRecord envelope (projected from the row + transcript) so a
	// consumer expecting §8.8 semantics can read it off GET
	// /v1/sessions/{id}. F-8.8.1.
	resp := toResponse(row)
	resp.TaskRecord = s.buildTaskRecord(r.Context(), row)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
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
	fromState := row.State
	updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
		row.State = session.StateCancelled
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §7.2 line 214 (a) — DELETE during the internal `resuming`
	// transient is the canonical resuming → cancelled snapshot-close
	// edge (§7.2 line 197). Bump coordination_generation in the same
	// logical write so any stale coordinator's subsequent RPC fails
	// the §4.2 CoordinatorFence check. F-7.1.14.
	if fromState == session.StateResuming {
		s.bumpCoordinationGenerationOnSnapshotClose(r.Context(), tenantID, id)
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
		fromState := row.State
		updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
			transition(row)
			return nil
		})
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		if session.IsTerminal(updated.State) {
			// spec: §7.2 line 214 (a) — when the terminal write
			// collapses an in-flight resume (resuming → cancelled /
			// completed / failed), bump coordination_generation in the
			// same logical write so any stale coordinator's subsequent
			// RPC fails the CoordinatorFence check. The pre-attach
			// counterparts (resume_pending → cancelled / completed,
			// §7.2 lines 219-225) intentionally do NOT bump because no
			// pod is attached and no CoordinatorFence round-trip is
			// pending. F-7.1.14.
			if fromState == session.StateResuming {
				s.bumpCoordinationGenerationOnSnapshotClose(r.Context(), tenantID, id)
			}
			s.recordSessionCompleted(r.Context(), updated)
		} else {
			// spec: §7.2 line 137 — surface a non-terminal transition
			// (e.g. interrupt → suspended) on the SSE stream. Terminal
			// transitions emit status_change from recordSessionCompleted
			// so every terminal caller is covered uniformly.
			s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
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
	// §7.4 line 463: close the upload channel — abort any in-flight
	// /upload stream for this session so it surfaces
	// UPLOAD_CHANNEL_CLOSED and its staged blob is rolled back. A
	// late /upload register that races finalize gets an
	// already-closed abort signal on the next Read. F-7.4.16.
	s.uploadAborts.closeSession(updated.ID)
	// §11.1 line 11: the upload window has closed, so drop the
	// per-session cumulative upload-byte total. In-flight concurrency
	// slots self-release; only the byte total is freed here. F-11.1.6.
	s.uploadLimits.closeSession(updated.ID)
	// spec: §7.2 line 137 — surface the created → ready transition.
	s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
	// F-7.4.17: §16.6 session.finalize_workspace audit row. The row
	// records the finalize transition and the consumption of the
	// single-use uploadToken so SIEM post-incident review can join the
	// upload-token consumption event to the session lifecycle. Detail
	// carries the persisted digest so SOC analysts can correlate the
	// audit row with the rejected /upload calls that follow it.
	// spec: §16.6 line 339; §7.1 line 60 (single-use); §11.7. F-7.4.17.
	if s.lifecycleAudit != nil {
		s.lifecycleAudit.EmitSessionLifecycle(r.Context(), SessionLifecycleEvent{
			EventType:  auditSessionWorkspaceFinalized,
			TenantID:   updated.TenantID,
			SessionID:  updated.ID,
			UserID:     updated.UserID,
			RuntimeRef: updated.RuntimeRef,
			State:      string(updated.State),
			Detail:     updated.UploadTokenDigest,
			At:         s.clock(),
		})
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
		// spec: §7.1 line 75 — surface the isolation level on every read.
		// The execution-mode + scrub-policy halves are persisted on the
		// row at create time (migration 0084) so a client that lost the
		// create response, or a GET issued against a coordinator-handed-
		// off replica, returns the same rich envelope. Rows persisted
		// before migration 0084 (or by a code path that never resolved a
		// pool) carry empty ExecutionMode — they fall back to the
		// session-mode default so the field never understates the
		// isolation posture.
		SessionIsolationLevel: persistedIsolationLevel(row),
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
	// spec: §7.1 line 6 — echo the client metadata so a client that
	// lost the create response can recover its own annotations.
	// F-7.3.20.
	if len(row.Metadata) > 0 {
		out.Metadata = cloneMetadata(row.Metadata)
	}
	// spec: §7.3 lines 377-393 — echo the effective retry policy so a
	// client can confirm what was clamped. F-7.3.1.
	if row.RetryPolicy != nil {
		out.RetryPolicy = cloneRetryPolicy(row.RetryPolicy)
	}
	// spec: §14 — echo the request envelope so a client that lost the
	// create response can recover its own env / pool / timeouts /
	// credentialPolicy / delegationLease / runtimeOptions. The env map
	// is the gateway-accepted set (every key passed the blocklist).
	// F-14.1.12 / F-14.1.14.
	if len(row.Env) > 0 {
		out.Env = cloneMetadata(row.Env)
	}
	out.Pool = row.Pool
	if row.Timeouts != nil {
		t := *row.Timeouts
		out.Timeouts = &t
	}
	if row.CredentialPolicyOverride != nil {
		c := *row.CredentialPolicyOverride
		out.CredentialPolicy = &c
	}
	if row.DelegationLeaseRequest != nil {
		out.DelegationLease = cloneDelegationLeaseRequest(row.DelegationLeaseRequest)
	}
	if len(row.RuntimeOptions) > 0 {
		out.RuntimeOptions = append(json.RawMessage(nil), row.RuntimeOptions...)
	}
	// spec: §7.5 lines 475, 488 — echo the captured / rejected setup
	// outputs. F-7.5.4 / F-7.5.11.
	if len(row.SetupOutput) > 0 {
		out.SetupOutput = make([]SetupOutputEntry, 0, len(row.SetupOutput))
		for _, e := range row.SetupOutput {
			out.SetupOutput = append(out.SetupOutput, SetupOutputEntry{
				Cmd:             e.Cmd,
				ExitCode:        e.ExitCode,
				Stdout:          e.Stdout,
				Stderr:          e.Stderr,
				DurationMs:      e.DurationMs,
				Truncated:       e.Truncated,
				Rejected:        e.Rejected,
				RejectionReason: e.RejectionReason,
			})
		}
	}
	return out
}

// cloneMetadata returns a defensive copy of the §7.1 line 6 metadata
// payload so mutations in the request or row never leak across the
// gateway/store boundary. A nil input maps to nil so the wire envelope
// honours `omitempty`. F-7.3.20.
func cloneMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// cloneRetryPolicy returns a defensive copy of the §7.3 RetryPolicy so
// the wire envelope cannot mutate the persisted row or the in-flight
// request through a shared pointer. A nil input maps to nil so the
// envelope honours `omitempty`. F-7.3.1.
func cloneRetryPolicy(in *session.RetryPolicy) *session.RetryPolicy {
	if in == nil {
		return nil
	}
	out := *in
	if len(in.RetryableFailures) > 0 {
		out.RetryableFailures = append([]string(nil), in.RetryableFailures...)
	}
	if len(in.NonRetryableFailures) > 0 {
		out.NonRetryableFailures = append([]string(nil), in.NonRetryableFailures...)
	}
	return &out
}

// cloneDelegationLeaseRequest returns a defensive copy of the §14
// delegation-lease request so the wire envelope cannot mutate the
// persisted row through a shared pointer. A nil input maps to nil so the
// envelope honours `omitempty`. F-14.1.14.
func cloneDelegationLeaseRequest(in *sessionstore.DelegationLeaseRequest) *sessionstore.DelegationLeaseRequest {
	if in == nil {
		return nil
	}
	out := *in
	if in.MaxDepth != nil {
		v := *in.MaxDepth
		out.MaxDepth = &v
	}
	if in.MaxChildrenTotal != nil {
		v := *in.MaxChildrenTotal
		out.MaxChildrenTotal = &v
	}
	return &out
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
