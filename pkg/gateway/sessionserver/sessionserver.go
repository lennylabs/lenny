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
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/envblock"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/errorclassify"
	"github.com/lennylabs/lenny/pkg/gateway/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/pagination"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/resultrollup"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimecapoverride"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/slothealth"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioncallback"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/toolapproval"
	"github.com/lennylabs/lenny/pkg/gateway/storage/derivelock"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/treerecovery"
	"github.com/lennylabs/lenny/pkg/gateway/usagestore"
	"github.com/lennylabs/lenny/pkg/gateway/vcscred"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/sandbox/slotstate"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
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
	store sessionstore.Store
	clock func() time.Time
	idFn  func() string
	// serviceHandlerOnce/serviceHandler cache the routing handler the
	// §15.2.1 rule-1 in-process service layer (ServiceCall) dispatches
	// through, so the MCP tool surface reuses the exact REST routes and
	// handlers rather than a parallel table that could drift. spec:
	// §15.2.1 rule 1 line 1380. F-15.2.3.
	serviceHandlerOnce sync.Once
	serviceHandler     http.Handler
	deriveAuditSink    DeriveAuditSink
	uploadIssuer       *uploadtoken.Issuer
	uploadVerifier     *uploadtoken.Verifier
	blobs              blobstore.Store
	executor           executor.Executor
	transcripts        transcriptstore.Store
	// artifacts is the §12.5 artifact catalog. The §8.10 archive
	// materialization reads it (ListBySession) to populate the §8.8
	// TaskResult.output.artifactRefs for a completed child. Nil when the
	// catalog is not wired (in-memory / dev posture); the artifactRefs
	// array then materializes empty. spec: §8.8 lines 888-896. F-8.8.2.
	artifacts artifactcatalog.Store
	events    *sessionevents.Bus
	// activityStamper records §6.2 lines 273-300 qualifying agent
	// activity (agent_output / tool_use events) onto the session's
	// last_agent_activity_at so the §11.3 idle watchdog does not reap an
	// actively-streaming session. Nil is a no-op. F-11.3.7.
	activityStamper ActivityStamper
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
	multiTenant bool
	podBinder   *podsession.Binder
	podRegistry *podsession.Registry
	// claimQueue is the §4.6.1 per-pool claim FIFO that backs
	// sessionPolicy.onPoolExhausted: queue. On a `queue` pool the start path
	// holds an exhausted acquisition in this FIFO for up to
	// maxQueueWaitSeconds, re-entering acquisition as pods free; on a `reject`
	// pool the queue is bypassed and the acquisition returns
	// WARM_POOL_EXHAUSTED immediately. Always non-nil after New.
	// spec: §4.6.1 (Pool exhaustion behavior), §5.2 (onPoolExhausted).
	claimQueue     *podClaimQueue
	fencer         CoordinationFencer
	agentNamespace string
	// poolNameResolver resolves the §5.2 warm pool a (runtimeRef,
	// isolation profile) pair maps to, for the §15.1 line 797 pool-drain
	// admission gate. The pinnedPool argument carries the §14.1
	// CreateSessionRequest.pool selector so a client-pinned pool is the one
	// the gate resolves. It defaults to resolvePoolName (CRD-backed); tests
	// override it to exercise the gate without a Kubernetes client.
	poolNameResolver func(ctx context.Context, runtimeRef string, requested isolation.Profile, pinnedPool string) (string, bool)
	// playgroundCaps resolves the §27.6 idle/duration caps for a
	// §27.3 origin=playground session. Wired post-construction via
	// SetPlaygroundCaps (the playground bootstrap runs after the session
	// server is built). Nil leaves a playground session bounded only by
	// the runtime/platform caps. spec: §27.6 lines 200-201. F-27.6.1 /
	// F-27.6.2.
	playgroundCaps PlaygroundCapResolver
	// incPlaygroundSessionCreated records the §27.8
	// lenny_playground_sessions_created_total metric once the origin claim
	// is read on the create path. Nil disables the metric. F-27.6.11.
	incPlaygroundSessionCreated func(runtime string)
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
	taskUsage             *resultrollup.Builder
	treeBudgetReturner    TreeBudgetReturner
	// leaseRegistrar, when set, registers a newly created root session
	// with the §8.6 lease-extension budget source so a later adapter
	// ExtendLease resolves the tree instead of failing
	// ErrSessionNotFound. leaseExtDefaults carries the deployment-level
	// configuration the root tree's budget ceiling is resolved from.
	// F-15.3.5.
	leaseRegistrar     LeaseTreeRegistrar
	leaseExtDefaults   LeaseExtensionDefaults
	quotaCheckpointer  QuotaFinalCheckpointer
	hwmObserver        DelegationHighWatermarkObserver
	hwmReader          DelegationHighWatermarkReader
	maxOrphanTasks     int
	evals              evalstore.Store
	memory             memorystore.Store
	experiments        experimentstore.Store
	pools              poolstore.Store
	experimentReporter ExperimentRejectionReporter
	stickyCache        StickyCache
	// externalProviders resolves the §10.7 built-in OpenFeature SDK
	// providers (launchdarkly, statsig, unleash) for mode:external
	// targeting. Nil disables the SDK-provider path; only OFREP-targeted
	// experiments evaluate. F-10.7.3.
	externalProviders ExternalProviderResolver
	runtimes          runtimestore.Store
	// capOverrides applies the §5.1 line 49 per-tenant capability override
	// on top of a resolved runtime at every capability consumer. Optional;
	// nil falls back to the platform-default capabilities. F-5.1.20.
	capOverrides       runtimecapoverride.Store
	environments       environmentstore.Store
	tenantAccess       tenantaccessstore.Store
	opsEmitter         events.EventEmitter
	budgetForget       func(sessionID string)
	refResolver        workspaceplan.RefResolver
	credPools          credentialpoolstore.Store
	vcsCreds           vcscred.Resolver
	defaultNoEnvPolicy string
	customRoles        customrolestore.Store
	interceptors       *interceptor.Chain
	policyAuditSink    *policy.AuditSink
	uploadSubsystem    *subsystem.Subsystem
	// uploadMetrics, when set, receives the §16.1 upload-handler
	// byte-count and queue-depth observations. Nil drops them. F-13.4.12.
	uploadMetrics UploadHandlerMetrics
	// resumeWindow is the §4.2 line 159 default resume-eligibility
	// duration stamped onto each session at create time. A non-zero
	// value falls through to DefaultResumeWindow.
	resumeWindow time.Duration
	// treeRecovery drives the §8.10 bottom-up delegation-tree recovery
	// from the resume path. Nil leaves resume's per-session behavior
	// unchanged (no descendant reattach traversal).
	treeRecovery *treerecovery.Orchestrator
	// treeRecoveryHook, when set, is called with the tree root id once a
	// detached recoverDelegationTree goroutine finishes. Tests use it to
	// await the async recovery deterministically; nil in production.
	treeRecoveryHook func(rootID string)
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
	// slotStates tracks each concurrent-workspace slot's §6.2 per-slot
	// sub-state so the gateway can report the per-pod leaked-slot count when
	// a slot's cleanup does not reclaim it. Never nil after New (defaults to
	// a fresh Registry). spec: §6.2 lines 170-176, line 179.
	slotStates *slotstate.Registry
	// slotReplacement, when set, increments
	// lenny_slot_pod_replacement_total{pool} when the slot retry policy
	// drains an unhealthy concurrent-mode pod for replacement. Nil disables
	// the emission. spec: §5.2 "whole-pod replacement trigger".
	slotReplacement func(pool string)
	// slotLeakGauge, when set, publishes the §6.2 line 179
	// lenny_adapter_leaked_slots{pod_id,pool} gauge to leaked: the count of
	// the pod's slots whose cleanup timed out and remain counted in
	// active_slots until the pod terminates. Nil disables the emission.
	slotLeakGauge func(pod, pool string, leaked int)
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
	// exists for (tenant, user, provider) and is deliverable. The §4.9
	// router resolves user sources only when this reports true. Wired by
	// SetUserCredChecker from the usercreds.Materializer; nil leaves the
	// router unable to resolve a user source (it falls through to pool).
	// spec: §4.9 lines 1347-1351, 1368-1372.
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

	// callbackValidator enforces the §14 callbackUrl SSRF mitigations at
	// admission (HTTPS-only, IP-literal / private-range rejection, DNS
	// pinning, optional deployer domain allowlist). Never nil after New
	// (defaults to a validator with no domain allowlist). spec: §14 lines
	// 108-112. F-14.1.11.
	callbackValidator *sessioncallback.Validator
	// callbackSeal KMS-envelope-encrypts a client callbackSecret under the
	// session tenant's KEK at admission. Nil disables callbackSecret
	// acceptance (a callbackUrl without a secret still delivers, unsigned).
	// spec: §14 line 139. F-14.1.11.
	callbackSeal func(ctx context.Context, tenantID string, plaintext []byte) ([]byte, error)
	// callbackDispatcher delivers validated §14 callbacks from an isolated
	// worker pool with the §14 retry budget. Nil leaves callbacks validated
	// and persisted but undelivered (the dev/test posture). spec: §14 lines
	// 111, 150. F-14.1.11.
	callbackDispatcher *sessioncallback.Dispatcher

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

	// persistDeriveFailureRows is the §7.1 derive rule 2 opt-in
	// (`gateway.persistDeriveFailureRows`, default false). When true, a
	// `POST /v1/sessions/{id}/derive` that fails after the workspace copy
	// is attempted persists a terminal `failed` Session row with
	// `failureClass = derive_failure` for audit, reachable per the §15.1
	// derive-failure reachability table. When false the gateway writes
	// nothing on failure (the default roll-back-without-persist posture).
	// spec: §7.1 derive rule 2; §15.1 lines 647-663. F-15.1.14.
	persistDeriveFailureRows bool

	// incDeriveFailureAudit, when set, increments the §16.1
	// `lenny_session_derive_failure_audit_total{outcome}` counter for each
	// derive-failure audit write attempt. Outcome is "persisted" when the
	// row was written, "fenced" when a coordinator handoff fenced the
	// write out, or "error" when the INSERT failed. Nil disables the
	// emission. spec: §7.1 derive rule 2; §16.1. F-15.1.14.
	incDeriveFailureAudit func(outcome string)

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

	// incSessionExpiry, when set, increments the §16.1
	// lenny_session_expiry_total{pool, reason} counter when the watchdog
	// expires a session on a platform expiry clock. Nil disables the
	// emission. spec: §16.1 catalog; §16.1.1 reason vocabulary. F-11.3.7.
	incSessionExpiry func(pool, reason string)

	// incWarmpoolWarmupFailure, when set, increments the §16.1 line 124
	// lenny_warmpool_warmup_failure_total{error_type} counter for one
	// warm-pool startup failure. Nil disables the emission. spec: §16.1
	// line 124, §7.3 line 387 — F-7.5.9.
	incWarmpoolWarmupFailure func(errorType string)

	// incInjectionGateFailClosed, when set, increments the
	// lenny_injection_gate_failclosed_total{cause} counter once per §5.1
	// injection-gate fail-closed occurrence. cause is "runtime_store" when
	// the runtime-registry read failed and "override_store" when the
	// per-tenant capability-override read failed, so the granular
	// transient-store cause behind the coarse SERVICE_UNAVAILABLE client
	// code is recorded as a metric alongside the gateway log line. Nil
	// disables the emission. spec: §5.1 (injection fail-closed),
	// §15.1 (SERVICE_UNAVAILABLE) — F-5.1.20.
	incInjectionGateFailClosed func(cause string)

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

	// midSessionUploadEnabled is the §7.4 line 433 deployer policy that,
	// together with the bound runtime's capabilities.midSessionUpload flag,
	// admits uploads into an already-running session via
	// POST /v1/sessions/{id}/upload-to-session. False (the default) keeps
	// mid-session uploads off platform-wide regardless of the runtime flag.
	// spec: §7.4 line 433 — F-7.4.6.
	midSessionUploadEnabled bool
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

// LeaseTreeRegistrar registers a root session's §8.6 lease-extension
// budget tree so a later adapter ExtendLease from the root session or
// its delegated descendants resolves the tree instead of failing
// ErrSessionNotFound. *leasecontrol.MemoryBudgetSource satisfies it.
// F-15.3.5.
// spec: §8.6 line 660 (configuration layering)
type LeaseTreeRegistrar interface {
	RegisterTree(rootSessionID string, cfg leasecontrol.TreeConfig)
}

// LeaseExtensionDefaults is the §8.6 deployment-level lease-extension
// configuration (Helm `leaseExtension.defaults` / `leaseExtension.max`)
// the gateway registers each root tree with. The token dimension's
// effective ceiling is resolved from DeploymentBudget and
// DeploymentMaxBudget through leaseextension.ResolveEffectiveMax; the
// remaining §8.6 line 643 dimensions have no deployment-level config
// surface and are registered without extension headroom. F-15.3.5.
// spec: §8.6 lines 660-678
type LeaseExtensionDefaults struct {
	// DeploymentBudget is the §8.6 deployment-default maxExtendableBudget
	// (Helm leaseExtension.defaults.maxExtendableBudget). Zero registers a
	// tree with no token-extension headroom.
	DeploymentBudget int64
	// DeploymentMaxBudget is the §8.6 absolute ceiling no override may
	// exceed (Helm leaseExtension.max.maxExtendableBudget).
	DeploymentMaxBudget int64
	// ApprovalMode is the §8.6 deployment-default extensionApproval mode.
	// Unspecified resolves to leasecontrol.DefaultApprovalMode.
	ApprovalMode leasecontrol.ApprovalMode
	// SuccessCoolOff is the §8.6 line 675 coolOffSeconds post-approval
	// window. Zero applies leasecontrol.DefaultSuccessCoolOff.
	SuccessCoolOff time.Duration
	// RejectionCoolOff is the §8.6 line 734 rejectionCoolOffSeconds. Zero
	// applies leasecontrol.DefaultRejectionCoolOff.
	RejectionCoolOff time.Duration
	// AutoMaxPerMinute is the §8.6 line 712 autoModeRateLimit
	// maxAutoExtensionsPerMinute. Zero means no limit.
	AutoMaxPerMinute int
}

// registerLeaseTree registers a newly created root session with the
// §8.6 lease-extension budget source. It is a no-op when no registrar
// is wired or the row is a delegated child (children are registered by
// the delegation Service, keyed to their root's tree). The token
// dimension's current value is seeded from a granted DelegationLease
// when the row carries one; the deployment-level ceiling comes from the
// configured defaults. F-15.3.5.
// spec: §8.6 lines 643-678
func (s *Server) registerLeaseTree(row sessionstore.Session) {
	if s.leaseRegistrar == nil || row.ParentSessionID != "" {
		return
	}
	cfg := leasecontrol.TreeConfig{
		TenantID:         row.TenantID,
		DeploymentBase:   s.leaseExtDefaults.DeploymentBudget,
		DeploymentMax:    s.leaseExtDefaults.DeploymentMaxBudget,
		ApprovalMode:     s.leaseExtDefaults.ApprovalMode,
		SuccessCoolOff:   s.leaseExtDefaults.SuccessCoolOff,
		RejectionCoolOff: s.leaseExtDefaults.RejectionCoolOff,
		AutoMaxPerMinute: s.leaseExtDefaults.AutoMaxPerMinute,
	}
	if l := row.DelegationLease; l != nil {
		cfg.CurrentTokenBudget = l.MaxTokenBudget
		cfg.CurrentChildren = int64(l.MaxChildrenTotal)
		cfg.CurrentParallelChildren = int64(l.MaxParallelChildren)
		cfg.CurrentTreeSize = int64(l.MaxTreeSize)
		cfg.CurrentMaxAgeSeconds = int64(l.PerChildMaxAge)
	}
	s.leaseRegistrar.RegisterTree(row.ID, cfg)
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

// ActivityStamper records §6.2 lines 273-300 qualifying agent activity for
// a session so the §11.3 idle watchdog does not reap it as idle. The
// gateway wires *sessionidle.Stamper here. Implementations coalesce the
// durable write (≤1/s per session) and are non-blocking. F-11.3.7.
type ActivityStamper interface {
	Stamp(tenantID, sessionID string)
}

// QuotaFinalCheckpointer writes the §11.2 line 44 "final reconciliation"
// token-usage checkpoint for a (tenant, user) when a session reaches a
// terminal state: the final cumulative window total is persisted to
// Postgres as the authoritative value so a subsequent Redis-recovery
// reconstruction has an accurate baseline. The default production wiring
// is quotacheckpoint.Service.CheckpointSubject. Best-effort: a failure
// must not abort the terminal-state transition.
//
// spec: §11.2 line 44 ("on session completion as final reconciliation";
// "the final cumulative token usage is always written to Postgres as an
// authoritative value").
type QuotaFinalCheckpointer interface {
	CheckpointSubject(ctx context.Context, tenantID, userID string) error
}

// CoordinationFencer issues the §10.1 / §4.2 CoordinatorFence to a
// resumed session's pod, announcing the session's current
// coordination_generation so the pod rejects any straggler RPC from a
// prior coordinator. relinquished is true when the coordinator gave up
// leadership after exhausting its §11.3 fence retries (the lease was
// released); the resume must then be aborted so another replica takes
// over. *coordfence.Fencer satisfies it.
//
// spec: §10.1 lines 33-37, §11.3 line 209.
type CoordinationFencer interface {
	Fence(ctx context.Context, adapter *adapterclient.Client, tenantID, sessionID string) (relinquished bool, err error)
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

	// PersistDeriveFailureRows is the §7.1 derive rule 2 opt-in. When
	// true, a derive that fails after the workspace copy is attempted
	// persists a terminal `failed` Session row with
	// `failureClass = derive_failure` for audit (reachable per the §15.1
	// derive-failure reachability table). Default false keeps the
	// roll-back-without-persist posture. Wired from
	// `gateway.persistDeriveFailureRows`. spec: §7.1 derive rule 2;
	// §15.1 lines 647-663. F-15.1.14.
	PersistDeriveFailureRows bool

	// IncDeriveFailureAudit, when set, increments the §16.1
	// `lenny_session_derive_failure_audit_total{outcome}` counter for each
	// derive-failure audit write. spec: §16.1. F-15.1.14.
	IncDeriveFailureAudit func(outcome string)

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

	// CallbackValidator validates client callbackUrls against the §14 SSRF
	// mitigations. Nil installs a default validator with no deployer
	// domain allowlist. spec: §14 lines 108-112. F-14.1.11.
	CallbackValidator *sessioncallback.Validator
	// CallbackSeal KMS-envelope-encrypts a client callbackSecret under the
	// session tenant's KEK at admission. Nil disables callbackSecret
	// acceptance (a callbackUrl with no secret still delivers, unsigned).
	// spec: §14 line 139. F-14.1.11.
	CallbackSeal func(ctx context.Context, tenantID string, plaintext []byte) ([]byte, error)
	// CallbackDispatcher delivers validated §14 callbacks. Nil leaves
	// callbacks validated and persisted but undelivered (the dev/test
	// posture); the cmd wires a real dispatcher. spec: §14 lines 111, 150.
	// F-14.1.11.
	CallbackDispatcher *sessioncallback.Dispatcher

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

	// ActivityStamper records §6.2 lines 273-300 qualifying agent
	// activity onto the session's last_agent_activity_at so the §11.3
	// idle watchdog (sweepIdle) sees an actively-working session as
	// non-idle. The gateway wires *sessionidle.Stamper here; nil is a
	// no-op (the in-memory / dev posture). F-11.3.7.
	ActivityStamper ActivityStamper

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

	// ExternalProviders resolves the §10.7 built-in OpenFeature SDK
	// providers (launchdarkly, statsig, unleash) for mode:external
	// targeting. The gateway wires an adapter over *experimentprovider.Cache.
	// Nil disables the SDK-provider path; only OFREP-targeted experiments
	// evaluate. F-10.7.3.
	ExternalProviders ExternalProviderResolver

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

	// CoordinationFencer, when set, issues the §10.1 / §4.2
	// CoordinatorFence to a resumed session's pod after the resume
	// re-bind, announcing the session's current coordination_generation
	// so the pod rejects any straggler RPC from a prior coordinator. Nil
	// disables fencing (dev / in-memory mode). spec: §10.1 lines 33-37,
	// §11.3 line 209.
	CoordinationFencer CoordinationFencer

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

	// TaskUsage, when set, assembles the §8.8 TaskResult.usage and
	// TaskResult.treeUsage rollups stamped on every materialized result.
	// Nil leaves both absent (the pre-metering behaviour).
	// spec: §8.8 lines 897-917.
	TaskUsage *resultrollup.Builder

	// TreeBudgetReturner, when set, releases the §12.4 delegation tree
	// budget a settled child consumed: the §8.2 line 130 maxTreeMemoryBytes
	// offload decrement and the per-parent parallel_children decrement
	// fire once per child as it reaches a terminal state. Nil disables
	// the decrement (developer mode without Redis-backed counters).
	TreeBudgetReturner TreeBudgetReturner

	// LeaseRegistrar, when set, registers each newly created root session
	// with the §8.6 lease-extension budget source (RegisterTree) so an
	// adapter ExtendLease from the root or its delegated descendants
	// resolves the tree instead of failing ErrSessionNotFound. Nil leaves
	// the tree unregistered (the in-process gateway with no GatewayControl
	// listener). LeaseExtensionDefaults supplies the §8.6 deployment-level
	// ceiling the root tree is registered with. F-15.3.5.
	LeaseRegistrar         LeaseTreeRegistrar
	LeaseExtensionDefaults LeaseExtensionDefaults

	// QuotaCheckpointer, when set, persists the §11.2 line 44 final
	// token-usage checkpoint for the session's (tenant, user) when the
	// session reaches a terminal state. Nil disables the final write
	// (developer mode without the Postgres checkpoint store).
	QuotaCheckpointer QuotaFinalCheckpointer

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

	// TreeRecoveryLevelTimeout and TreeRecoveryTreeTimeout are the §8.10
	// maxLevelRecoverySeconds / maxTreeRecoverySeconds budgets the
	// bottom-up delegation-tree recovery applies when a tree is resumed.
	// A non-positive value selects the §8.10 recovery-package default
	// (120s / 600s). spec: §8.10 lines 1022-1023.
	TreeRecoveryLevelTimeout time.Duration
	TreeRecoveryTreeTimeout  time.Duration

	// TreeRecoveryMetrics records the §16.1 line 144-145 tree-recovery
	// telemetry. *gatewaymetrics.Metrics satisfies it. Nil drops the
	// observations.
	TreeRecoveryMetrics treerecovery.Metrics

	// Runtimes is the §5.1 runtime registry. Optional — when nil, the
	// §9.1 GET /v1/runtimes discovery endpoint returns an empty list.
	Runtimes runtimestore.Store

	// CapabilityOverrides is the §5.1 line 49 per-tenant runtime
	// capability override store. Optional — when set, the gateway overlays
	// a tenant's override onto the resolved runtime at every §5.1
	// capability consumer (injection gate, SDK-warm decision, mid-session
	// upload gate, and the GET /v1/runtimes discovery exposure). F-5.1.20.
	CapabilityOverrides runtimecapoverride.Store

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

	// BudgetForget drops a settled session's §11.2 mid-session
	// token-budget accounting from the LLM-proxy enforcer so the
	// per-session map does not grow without bound. Optional — when nil,
	// the terminal pipeline performs no budget cleanup. spec: §11.2.
	BudgetForget func(sessionID string)

	// RefResolver pins each §14 gitClone source's ref to an immutable
	// commit SHA at session creation. Optional — when nil, the gateway
	// stores the submitted plan without resolving git refs.
	RefResolver workspaceplan.RefResolver

	// CredentialPools is the §4.9 credential-pool registry. When set,
	// session creation runs the §14 gitClone auth host-to-pool binding
	// check. Optional — when nil, the binding check is skipped.
	CredentialPools credentialpoolstore.Store

	// VCSCredentials materializes the §14 gitClone VCS token at session
	// creation, so the ls-remote that pins a private repo's ref
	// authenticates with the same credential the clone will. Optional —
	// when nil, every gitClone ref is resolved unauthenticated and a
	// private repo fails with GIT_CLONE_REF_UNRESOLVABLE.
	VCSCredentials vcscred.Resolver

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

	// MidSessionUploadEnabled is the §7.4 line 433 deployer policy that
	// admits mid-session uploads (POST /v1/sessions/{id}/upload-to-session)
	// when the bound runtime also declares capabilities.midSessionUpload.
	// False (the default) keeps the surface closed platform-wide. spec:
	// §7.4 line 433 — F-7.4.6.
	MidSessionUploadEnabled bool

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

	// SlotHealth is the §5.2 per-pod fail/leak rolling-window tracker the
	// slot retry policy reads to apply the ceil(maxConcurrentSessions/2)
	// whole-pod replacement trigger. The gateway constructs a single Tracker
	// and shares it with the §4.7 scrub-report drain ledger so adapter-reported
	// slot-scrub leaks and gateway-observed slot-bind failures accumulate in
	// one rolling window: a pod crossing the unhealthy threshold on the
	// combined failed+leaked count drains regardless of which path observed the
	// degradation. A nil tracker defaults to a fresh per-server Tracker (the
	// standalone test path with no scrub-report ledger). spec: §5.2 (combined
	// failed+leaked unhealthy threshold), §6.2 (leaked-slot semantics).
	SlotHealth *slothealth.Tracker

	// SlotReplacement, when set, increments
	// lenny_slot_pod_replacement_total{pool} when the §5.2 concurrent-
	// workspace slot retry policy drains an unhealthy pod (ceil(maxConcurrent
	// /2) slots failed or leaked within the rolling window) for replacement.
	// Nil disables the emission. spec: §5.2 "whole-pod replacement trigger".
	SlotReplacement func(pool string)

	// SlotLeakGauge, when set, publishes the §6.2 line 179
	// lenny_adapter_leaked_slots{pod_id,pool} gauge to leaked: the count of
	// a pod's concurrent-workspace slots whose cleanup timed out and remain
	// counted in active_slots until the pod terminates. Nil disables the
	// emission. spec: §6.2 line 179.
	SlotLeakGauge func(pod, pool string, leaked int)

	// QueuePollInterval is the cadence at which a §4.6.1 onPoolExhausted:queue
	// request re-enters acquisition while it waits for a pod to free. Zero
	// selects DefaultQueuePollInterval. Operator-tunable; the spec fixes the
	// wait bound (maxQueueWaitSeconds), not the poll cadence. spec: §4.6.1.
	QueuePollInterval time.Duration

	// SetPodClaimQueueDepth, when set, publishes the §16.1
	// lenny_pod_claim_queue_depth{pool} gauge as the per-pool claim FIFO grows
	// and shrinks. Nil disables the emission. spec: §4.6.1, §16.1.
	SetPodClaimQueueDepth func(pool string, depth int)
	// ObservePodClaimQueueWait, when set, observes the §16.1
	// lenny_pod_claim_queue_wait_seconds{pool} histogram when a queued request
	// leaves the FIFO (acquired or timed out). Nil disables it. spec: §4.6.1,
	// §16.1.
	ObservePodClaimQueueWait func(pool string, seconds float64)
	// IncPodClaimTimeout, when set, increments the §16.1
	// lenny_pod_claim_timeout_total{pool} counter when a queued request
	// exhausts its maxQueueWaitSeconds bound. Nil disables it. spec: §4.6.1,
	// §16.1.
	IncPodClaimTimeout func(pool string)

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

	// IncSessionExpiry, when set, increments the §16.1
	// lenny_session_expiry_total{pool, reason} counter when the watchdog
	// terminates a session on a platform expiry clock. reason is the
	// §16.1.1 vocabulary value the watchdog resolved from the expiry edge
	// ("max_idle_time" for the §6.2 idle clock, "max_session_age" for the
	// §11.3 age cap and the §7.3 awaiting_client_action deadline). Nil
	// disables the emission. spec: §16.1 catalog; §16.1.1. F-11.3.7.
	IncSessionExpiry func(pool, reason string)

	// IncWarmpoolWarmupFailure, when set, increments the §16.1 line 124
	// lenny_warmpool_warmup_failure_total{error_type} counter for a
	// warm-pool startup failure. error_type is the §7.3 line 387
	// non-retryable failure category the gateway classified
	// (`setup_command_failed`, etc.). Nil disables the emission.
	// spec: §16.1 line 124, §7.3 line 387 — F-7.5.9.
	IncWarmpoolWarmupFailure func(errorType string)

	// IncInjectionGateFailClosed, when set, increments the
	// lenny_injection_gate_failclosed_total{cause} counter once per §5.1
	// injection-gate fail-closed occurrence. cause is "runtime_store" or
	// "override_store" depending on which backing-store read returned a
	// transient error, so the granular cause behind the coarse
	// SERVICE_UNAVAILABLE client code is observable as a metric. Nil
	// disables the emission. spec: §5.1 (injection fail-closed),
	// §15.1 (SERVICE_UNAVAILABLE) — F-5.1.20.
	IncInjectionGateFailClosed func(cause string)
}

// New returns a Server bound to the supplied store.
func New(store sessionstore.Store, opts Options) *Server {
	s := &Server{
		store:                      store,
		clock:                      opts.Clock,
		idFn:                       opts.IDFunc,
		deriveAuditSink:            opts.DeriveAuditSink,
		deriveLock:                 opts.DeriveLock,
		persistDeriveFailureRows:   opts.PersistDeriveFailureRows,
		incDeriveFailureAudit:      opts.IncDeriveFailureAudit,
		uploadIssuer:               opts.UploadTokenIssuer,
		uploadVerifier:             opts.UploadTokenVerifier,
		blobs:                      opts.Blobs,
		executor:                   opts.Executor,
		transcripts:                opts.Transcripts,
		artifacts:                  opts.Artifacts,
		activityStamper:            opts.ActivityStamper,
		evals:                      opts.Evals,
		memory:                     opts.Memory,
		experiments:                opts.Experiments,
		pools:                      opts.Pools,
		experimentReporter:         opts.ExperimentRejections,
		stickyCache:                opts.StickyCache,
		externalProviders:          opts.ExternalProviders,
		events:                     opts.Events,
		dualStore:                  opts.DualStore,
		messaging:                  opts.Messaging,
		interactions:               opts.Interactions,
		usage:                      opts.Usage,
		users:                      opts.Users,
		billing:                    opts.Billing,
		tenants:                    opts.Tenants,
		storageQuota:               opts.StorageQuota,
		defaultIsoProf:             opts.DefaultIsolationProfile,
		devMode:                    opts.DevMode,
		multiTenant:                opts.MultiTenant,
		podBinder:                  opts.PodBinder,
		podRegistry:                opts.PodRegistry,
		fencer:                     opts.CoordinationFencer,
		agentNamespace:             opts.AgentNamespace,
		admissionRL:                opts.AdmissionRateLimitCounter,
		perRuntimePerMin:           opts.PerRuntimePerMinute,
		perPoolPerMin:              opts.PerPoolPerMinute,
		rlMetrics:                  opts.RateLimitMetrics,
		maxConcSessGlobal:          opts.MaxConcurrentSessionsGlobal,
		maxConcSessPerUser:         opts.MaxConcurrentSessionsPerUser,
		maxConcSessPerRuntime:      opts.MaxConcurrentSessionsPerRuntime,
		evalRL:                     opts.EvalRateLimitCounter,
		evalPerSessionPerMin:       resolveEvalLimit(opts.EvalPerSessionPerMinute, DefaultEvalPerSessionPerMin),
		evalPerTenantPerMin:        resolveEvalLimit(opts.EvalPerTenantPerMinute, DefaultEvalPerTenantPerMin),
		sealer:                     opts.Sealer,
		sealMaxDuration:            opts.WorkspaceSealMaxDuration,
		sealSleep:                  opts.SealSleep,
		observeSealDuration:        opts.ObserveWorkspaceSealDuration,
		recordSessionTerminal:      opts.RecordSessionTerminal,
		observeEvalScore:           opts.ObserveEvalScore,
		partialManifestCleaner:     opts.PartialManifestCleaner,
		evictionStateLookup:        opts.EvictionStateLookup,
		partialManifestLookup:      opts.PartialManifestLookup,
		treeArchive:                opts.TreeArchive,
		taskUsage:                  opts.TaskUsage,
		treeBudgetReturner:         opts.TreeBudgetReturner,
		leaseRegistrar:             opts.LeaseRegistrar,
		leaseExtDefaults:           opts.LeaseExtensionDefaults,
		quotaCheckpointer:          opts.QuotaCheckpointer,
		hwmReader:                  opts.HighWatermarkReader,
		hwmObserver:                opts.HighWatermarkObserver,
		maxOrphanTasks:             opts.MaxOrphanTasksPerTenant,
		runtimes:                   opts.Runtimes,
		capOverrides:               opts.CapabilityOverrides,
		environments:               opts.Environments,
		tenantAccess:               opts.TenantAccess,
		opsEmitter:                 opts.OpsEmitter,
		budgetForget:               opts.BudgetForget,
		refResolver:                opts.RefResolver,
		credPools:                  opts.CredentialPools,
		vcsCreds:                   opts.VCSCredentials,
		defaultNoEnvPolicy:         opts.DefaultNoEnvironmentPolicy,
		customRoles:                opts.CustomRoles,
		interceptors:               opts.Interceptors,
		policyAuditSink:            opts.PolicyAuditSink,
		uploadSubsystem:            opts.UploadSubsystem,
		uploadMetrics:              opts.UploadMetrics,
		midSessionUploadEnabled:    opts.MidSessionUploadEnabled,
		resumeWindow:               opts.ResumeWindow,
		sessionLogHook:             opts.SessionLogHook,
		warmupEstimateSeconds:      opts.WarmupEstimateSeconds,
		credRouter:                 opts.CredentialRouter,
		preclaimMismatch:           opts.PreclaimMismatch,
		slotHealth:                 opts.SlotHealth,
		slotStates:                 slotstate.NewRegistry(),
		slotReplacement:            opts.SlotReplacement,
		slotLeakGauge:              opts.SlotLeakGauge,
		observeStartupDuration:     opts.ObserveStartupDuration,
		observeStartupPhase:        opts.ObserveStartupPhase,
		observeTimeToFirstToken:    opts.ObserveTimeToFirstToken,
		lifecycleAudit:             opts.LifecycleAuditSink,
		interactionAudit:           opts.InteractionAuditSink,
		toolApprovalWaits:          opts.ToolApprovalWaits,
		treeCycleObserver:          opts.TreeCycleObserver,
		callbackValidator:          opts.CallbackValidator,
		callbackSeal:               opts.CallbackSeal,
		callbackDispatcher:         opts.CallbackDispatcher,
		inputWaits:                 opts.InputWaits,
		defaultRetention:           opts.DefaultRetention,
		retryPolicyCaps:            opts.RetryPolicyCaps,
		envBlocklist:               envblock.New(opts.EnvVarBlocklist),
		incSessionResumeAttempt:    opts.IncSessionResumeAttempt,
		incSessionRetry:            opts.IncSessionRetry,
		incSessionExpiry:           opts.IncSessionExpiry,
		incWarmpoolWarmupFailure:   opts.IncWarmpoolWarmupFailure,
		incInjectionGateFailClosed: opts.IncInjectionGateFailClosed,
		uploadTokenTTL:             opts.UploadTokenTTL,
		uploadAborts:               newUploadAbortRegistry(),
		uploadLimits: newUploadLimiter(
			opts.MaxConcurrentUploadsPerSession,
			opts.MaxConcurrentUploadsGlobal,
			opts.MaxUploadBytesPerSession,
		),
	}
	if s.slotHealth == nil {
		// spec: §5.2 — default to a fresh per-server fail/leak tracker when no
		// shared tracker is injected (the standalone test path with no §4.7
		// scrub-report drain ledger). The gateway wiring injects a single
		// Tracker so the slot-bind-failure and adapter-leak windows are one.
		s.slotHealth = slothealth.New()
	}
	if s.callbackValidator == nil {
		// spec: §14 lines 108-112 — the SSRF validator needs no external
		// config; default it so callbackUrl validation always runs even
		// when a deployer configured no domain allowlist.
		s.callbackValidator = sessioncallback.NewValidator(nil, nil)
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
	// §4.6.1 per-pool claim FIFO backing sessionPolicy.onPoolExhausted: queue.
	// It shares the server clock so a test can drive the wait deadline, and it
	// carries the §16.1 queue-metric callbacks. A `reject` pool never reaches
	// the FIFO; the queue is consulted only after an acquisition exhausts both
	// the claim-path timeout and the Postgres fallback.
	s.claimQueue = newPodClaimQueue(opts.QueuePollInterval, s.clock)
	s.claimQueue.onDepth = opts.SetPodClaimQueueDepth
	s.claimQueue.onWait = opts.ObservePodClaimQueueWait
	s.claimQueue.onTimeout = opts.IncPodClaimTimeout
	if s.idFn == nil {
		s.idFn = randomSessionID
	}
	if s.poolNameResolver == nil {
		s.poolNameResolver = s.resolvePoolName
	}
	if s.maxOrphanTasks <= 0 {
		s.maxOrphanTasks = DefaultMaxOrphanTasksPerTenant
	}
	if s.resumeWindow <= 0 {
		s.resumeWindow = DefaultResumeWindow
	}
	// spec: §8.10 lines 1014-1027 — the bottom-up delegation-tree
	// recovery driver. The required seams (lister, reattacher, terminal
	// marker) are always available here, so the orchestrator is built
	// unconditionally; its work is gated to genuinely orphaned nodes by
	// nodeNeedsRecovery, which is a no-op when the pod registry is
	// unwired (dev / unit-test) so resume keeps its per-session
	// behavior.
	s.treeRecovery = treerecovery.New(treerecovery.Config{
		Lister:       store,
		Reattacher:   sessionNodeReattacher{s: s},
		Terminal:     sessionTerminalMarker{s: s},
		Metrics:      opts.TreeRecoveryMetrics,
		Recoverable:  s.nodeNeedsRecovery,
		LevelTimeout: opts.TreeRecoveryLevelTimeout,
		TreeTimeout:  opts.TreeRecoveryTreeTimeout,
	})
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
	// §15.1 line 703 — session-facing pool discovery. Mounted bare like
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
	mux.HandleFunc("POST /v1/sessions/{id}/upload-to-session", manage(s.handleUploadToSession))
	mux.HandleFunc("POST /v1/sessions/{id}/messages", manage(s.handleMessages))
	// spec: §15.1 line 692 — the §15.4.1 MessageDAG list over the durable
	// session_messages store, the read side of the message endpoint. Shares
	// the transcript backing; projects each row to a message node with its
	// stable id, derived `from`, and delivery state. F-15.1.3.
	mux.HandleFunc("GET /v1/sessions/{id}/messages", read(s.handleMessagesList))
	mux.HandleFunc("GET /v1/sessions/{id}/transcript", read(s.handleTranscript))
	mux.HandleFunc("GET /v1/sessions/{id}/tree", read(s.handleTree))
	mux.HandleFunc("GET /v1/usage", s.handleUsage)
	mux.HandleFunc("GET /v1/metering/events", s.handleMeteringEvents)
	mux.HandleFunc("GET /v1/sessions/{id}/events", read(s.handleEvents))
	// spec: §15.1 line 673 / §24.17 line 220 — the `lenny session logs`
	// target; session logs over the durable event store, content-
	// negotiated SSE / JSON envelope with the `--since` filter.
	mux.HandleFunc("GET /v1/sessions/{id}/logs", read(s.handleLogs))
	// spec: §15.1 line 598 — per-session artifact listing (the §15.2
	// list_artifacts tool's REST equivalent) and reconciled per-session
	// token usage (the §15.2 get_token_usage tool's REST equivalent). The
	// usage route self-gates on view_usage like GET /v1/usage. F-15.2.3.
	mux.HandleFunc("GET /v1/sessions/{id}/artifacts", read(s.handleListArtifacts))
	mux.HandleFunc("GET /v1/sessions/{id}/usage", s.handleSessionUsage)
	// spec: §15.1 lines 671, 674 — workspace snapshot download (tar.gz)
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

	// Labels is the §14 line 311 client-supplied session label set — a
	// flat string→string map of caller tags the `GET /v1/sessions` list
	// endpoint filters on (§15.1 line 598). Keys must be non-empty. The
	// §15.1 GET envelope echoes them back. F-15.1.15.
	// spec: §14 line 311; §15.1 line 598.
	Labels map[string]string `json:"labels,omitempty"`

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

	// CallbackURL is the §14 optional session-terminal webhook. The
	// gateway validates it against the §14 SSRF mitigations at admission
	// (HTTPS-only, IP-literal/private-range rejection, DNS pinning, and
	// the optional deployer domain allowlist) and rejects a failing URL
	// with 400 INVALID_CALLBACK_URL. spec: §14 lines 73, 108-112. F-14.1.11.
	CallbackURL string `json:"callbackUrl,omitempty"`

	// CallbackSecret is the §14 HMAC signing secret for callback
	// deliveries. It is write-only: the gateway KMS-envelope-encrypts it
	// at admission and never returns the plaintext on any API. spec: §14
	// line 139. F-14.1.11.
	CallbackSecret string `json:"callbackSecret,omitempty"`
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

	// Labels echoes the §14 line 311 client-supplied session labels the
	// session was created with. Omitted when the client submitted none.
	// These are the values the `GET /v1/sessions?label=k=v` filter matches
	// against. spec: §14 line 311; §15.1 line 598. F-15.1.15.
	Labels map[string]string `json:"labels,omitempty"`

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

	// Origin echoes the §27.3 origin label recorded on the session row.
	// It is "playground" for a /playground/*-originated session and
	// omitted otherwise, so a §25.9 audit-log query and the §27.8
	// dashboards can slice on origin. spec: §27.6 line 203. F-27.6.8.
	Origin string `json:"origin,omitempty"`

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
	TaskRecord *sessionrecord.Record `json:"taskRecord,omitempty"`
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
	// ConversationContinuity is the §7.1 line 74 contract field:
	// "platform" for session mode (the platform binds the session to a pod
	// and preserves conversation context across messages for the session's
	// lifetime) or "none" for service mode (the gateway routes each message
	// to any ready replica and keeps no conversation context between
	// messages, so clients of multi_turn runtimes re-inject context into
	// each message's input). spec: §5.2 (service-mode session contract),
	// §7.1 line 74.
	ConversationContinuity string `json:"conversationContinuity"`
}

// conversationContinuityFor maps a §5.2 execution mode to its §7.1 line 74
// conversationContinuity contract value: "none" for service mode (no
// cross-message continuity, each message routes to any ready replica) and
// "platform" for session mode (the session is pinned to a pod that
// preserves context for its lifetime). An empty mode resolves to the
// session-mode default, mirroring the executionMode fallbacks elsewhere so
// the field never understates continuity. spec: §5.2, §7.1 line 74.
func conversationContinuityFor(mode string) string {
	if mode == string(runtimestore.ExecutionModeService) {
		return continuityNone
	}
	return continuityPlatform
}

// persistedContinuity returns the §7.1 line 74 conversationContinuity for a
// read off the persisted row. The stored value (the S25a
// conversation_continuity column) is authoritative when non-empty, so a
// GET / List after a coordinator handoff returns the exact value the create
// response carried. An empty column (a pre-migration row, or a row created
// before the gateway resolved a pool) falls back to the mode-derived value
// so the field never understates continuity. spec: §7.1 line 74.
func persistedContinuity(stored, mode string) string {
	if stored != "" {
		return stored
	}
	return conversationContinuityFor(mode)
}

const (
	// continuityPlatform is the §7.1 line 74 conversationContinuity value
	// for session mode.
	continuityPlatform = "platform"
	// continuityNone is the §7.1 line 74 conversationContinuity value for
	// service mode.
	continuityNone = "none"
)

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
	// spec: §16.3 line 336 — open the gateway-side `session.create` span on
	// the request context so the create flow (quota/admission gates, the
	// store INSERT, the §7.1 uploadToken mint) rides one trace. The tracer
	// resolves the process-global OTel provider; constructing it here keeps
	// the span site self-contained. Downstream store calls inherit the span
	// through the rebound request context. Correlation attributes are
	// projected from the context by Start.
	ctx, span := tracing.NewTracer(nil).Start(r.Context(), tracing.SpanSessionCreate)
	defer span.End()
	r = r.WithContext(ctx)

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
	if !s.requireAdmissionRateLimit(w, r, tenantID, req.RuntimeRef, rlProfile, req.Pool) {
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

	// spec: §27.5 line 190 / §27.9 line 250 — an origin=playground caller may
	// only create a session against a runtime its playground.allowedRuntimes
	// list exposes. This closes the §27.4 "see and select" gap so the
	// allowedRuntimes filter is an authorization boundary, not just a picker
	// display filter. A non-playground caller is unaffected. F-27.4.1.
	if !s.requirePlaygroundRuntimeVisible(w, r, req.RuntimeRef) {
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

	// spec: §7.1 line 18 / line 75 — when a pool is pinned and the client
	// omits isolationProfile, the named pool's own profile governs, so every
	// pool resolution on this create defers to the pool (effective requested
	// profile empty) rather than the deployment default. F-CS2 (0018).
	effProf := effectiveRequestedProfile(req.IsolationProfile, isoProf, req.Pool)

	// spec: §15.1 line 797 — reject a create that would select a pool in
	// the `draining` phase with 503 POOL_DRAINING + Retry-After before any
	// pod claim. The gate resolves the same pool the session would bind
	// to; it is inert in the Postgres-only posture (no pool binding). F-15.1.8.
	if !s.requirePoolNotDraining(w, r, req.RuntimeRef, effProf, req.Pool) {
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
		// spec: §16.3 line 336 — a malformed retryPolicy is a caller error
		// (PERMANENT: the same request will not validate on retry).
		tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryPermanent))
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
	// spec: §7.1 line 18 / line 75 — resolve the level against effProf so a
	// pinned pool's own profile governs, and persist the pool-derived profile
	// on the row. The later claim re-resolves from row.IsolationProfile, so
	// persisting the pool's profile keeps the claim consistent with the pin.
	// F-CS2 (0018).
	level := s.resolveIsolationLevel(r.Context(), req.RuntimeRef, effProf, req.Pool)
	row := sessionstore.Session{
		ID:                     s.idFn(),
		TenantID:               tenantID,
		UserID:                 req.UserID,
		RuntimeRef:             req.RuntimeRef,
		Environment:            req.Environment,
		State:                  session.StateCreated,
		IsolationProfile:       persistedRowProfile(level, isoProf),
		ExecutionMode:          level.ExecutionMode,
		ScrubPolicy:            level.ScrubPolicy,
		ConversationContinuity: level.ConversationContinuity,
		WorkspacePlan:          planJSON,
		Metadata:               cloneMetadata(req.Metadata),
		RetryPolicy:            effectiveRetry,
		CreatedAt:              s.clock(),
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

	// spec: §27.3 line 63 / §27.6 lines 200-203 — when the caller's session
	// bearer carries the origin=playground claim, stamp the §27.6 idle and
	// duration caps (min-wins over any §14 timeout the client requested) and
	// the origin=playground audit label onto the row before persist. Reads
	// the §14 timeouts validateRequestEnvelope copied above so a tighter
	// client value is preserved. F-27.3.3 / F-27.6.1 / F-27.6.2 / F-27.6.8.
	s.applyPlaygroundCaps(r.Context(), req.RuntimeRef, &row)

	// §10.7: the ExperimentRouter may enroll the session in a variant,
	// rewriting its runtime/pool before the row is persisted. It fails
	// the creation closed when the variant pool is less isolated than
	// the session's profile.
	if !s.routeExperiment(w, r, &row) {
		return
	}

	// spec: §7.1 steps 3-5 — when the gateway is wired with a pod binder,
	// the create atomic unit runs the credential availability pre-check
	// (step 3) and claims an idle warm pod (step 4) synchronously, before
	// the row persist (step 5). The claim surfaces pool exhaustion
	// immediately so the client learns of it before uploading, and the
	// claimed pod's binding (PodAssignment + PoolRef) is persisted on the
	// row so a later /finalize and /start reconnect to it (§4.6). A claim
	// failure leaves no row behind per the §7.1 line 28 atomicity contract.
	// A service-mode pool is claimless (a nil claim); a concurrent-workspace
	// pool claims a per-session slot at create like every non-service-mode
	// pool (claimAtCreate returns the reserved slot's binding), so the §15.1
	// created-state pod-claim invariant holds uniformly.
	var createClaim *podsession.ClaimResult
	if s.podBinder != nil {
		outcome, err := s.claimAtCreate(r.Context(), row, parsedPlan)
		if err != nil {
			// spec: §7.1 line 28 — the pre-check or claim failed before any
			// row was persisted; surface SESSION_CREATION_FAILED (or the
			// credential / pool-warming envelope) with no session_id. No pod
			// is held: the pre-check is claimless, and the exclusive Claim and
			// the concurrent ClaimSlot reclaim their own pod/slot on failure.
			tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryTransient))
			s.writePodClaimError(w, err, "SESSION_CREATION_FAILED",
				"could not place the session on a warm pod")
			return
		}
		// spec: §7.1 line 75 — the returned level reflects the actual resolved
		// pool's profile, tightening the create-response accuracy guarantee.
		level = outcome.Level
		row.ExecutionMode = level.ExecutionMode
		row.ScrubPolicy = level.ScrubPolicy
		row.ConversationContinuity = level.ConversationContinuity
		if outcome.Claim != nil {
			createClaim = outcome.Claim
			// spec: §4.6 (proposal) — persist the durable binding so the claim
			// survives a coordinator handoff during the create → finalize →
			// start window; PodAssignment + PoolRef plus the session id
			// reconstruct the deterministic claim and lease key.
			row.PodAssignment = createClaim.SandboxName
			row.PoolRef = createClaim.Pool
		}
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
		// spec: §7.1 line 28 — the mint failed before the row persist, so roll
		// back the create-time pod claim rather than leak it past a "no
		// session_id returned" failure.
		s.rollbackClaim(r.Context(), createClaim, row.ID)
		// spec: §16.3 line 336 — the uploadToken mint failed before any row
		// was persisted; record it on the create span (PERMANENT: a bad
		// session id does not become valid on retry).
		tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryPermanent))
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
		// (non-existent) row. Roll back the create-time pod claim so no pod
		// leaks past the failure, then return SESSION_CREATION_FAILED so the
		// client retries.
		// spec: §16.3 line 336 — a store INSERT failure is retryable
		// (TRANSIENT: the client receives a 503 + Retry-After).
		s.rollbackClaim(r.Context(), createClaim, row.ID)
		tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryTransient))
		s.writeSessionCreationFailed(w, "row_persistence_failed", err.Error())
		return
	}
	s.recordSessionCreated(r.Context(), row)
	// §8.6: register the root tree's lease-extension budget so a later
	// adapter ExtendLease resolves it instead of ErrSessionNotFound. F-15.3.5.
	s.registerLeaseTree(row)
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

// poolPolicyReader adapts the §5.2 poolstore onto the
// podsession.PoolPolicyReader the CRD resolver folds the gateway-enforced
// sessionPolicy mirror through. It returns nil when no pool store is
// wired, so ResolvePool keeps its CRD-derived dispatch defaults.
func (s *Server) poolPolicyReader() podsession.PoolPolicyReader {
	if s.pools == nil {
		return nil
	}
	return poolPolicyMirror{pools: s.pools}
}

// poolPolicyMirror reads a pool's gateway-enforced §5.2 sessionPolicy
// fields (maxConcurrentSessions, the service-mode maxConcurrent,
// recycle.allowCrossTenantReuse, and recycle.maxPodUptimeSeconds) from
// the poolstore so ResolvePool can fold them into the PoolMatch. The CRD
// pair does not carry these; the poolstore is the source of truth.
//
// spec: §5.2 (sessionPolicy block, gateway-enforced subset).
type poolPolicyMirror struct {
	pools poolstore.Store
}

// PoolPolicy implements podsession.PoolPolicyReader. found is false for a
// missing or soft-deleted pool, leaving the CRD-derived dispatch fields
// unchanged.
func (m poolPolicyMirror) PoolPolicy(ctx context.Context, name string) (podsession.PoolPolicyMirror, bool, error) {
	p, err := m.pools.Get(ctx, name)
	if err != nil {
		if errors.Is(err, poolstore.ErrNotFound) {
			return podsession.PoolPolicyMirror{}, false, nil
		}
		return podsession.PoolPolicyMirror{}, false, fmt.Errorf("sessionserver: get pool %s: %w", name, err)
	}
	mirror := podsession.PoolPolicyMirror{MaxConcurrent: int32(p.MaxConcurrent)}
	if sp := p.SessionPolicy; sp != nil {
		mirror.MaxConcurrentSessions = int32(sp.MaxConcurrentSessions)
		// §5.2 / §4.6.1 pool-exhaustion disposition: fold the queue-vs-reject
		// choice and its wait bound from the session policy so the start
		// path's claim queue reads the gateway-enforced values.
		mirror.OnPoolExhausted = string(sp.OnPoolExhausted)
		mirror.MaxQueueWaitSeconds = sp.MaxQueueWaitSeconds
		if r := sp.Recycle; r != nil {
			mirror.AllowCrossTenantReuse = r.AllowCrossTenantReuse
			mirror.MaxPodUptimeSeconds = int64(r.MaxPodUptimeSeconds)
		}
	}
	return mirror, true, nil
}

// effectiveRequestedProfile is the §5.3 profile to resolve a session's
// pool against. spec: §7.1 line 18 / line 75 — a client-pinned pool
// overrides the default pool selection and the resolved level is
// populated from the assigned pool's configuration, so when the client
// pins a pool and omits isolationProfile the pool's own profile governs.
// In that case the effective requested profile is empty, which lets
// ResolvePool's `isolationProfile != ""` short-circuit defer to the named
// pool rather than reject a pool whose profile differs from the deployment
// default. When the client states a profile explicitly (clientRequested is
// non-empty) that profile governs and an inconsistent pin is rejected;
// when no pool is pinned the defaulted profile is used so the level still
// resolves. clientRequested is the raw request field (empty when omitted);
// defaulted is the validated profile with the deployment default applied.
func effectiveRequestedProfile(clientRequested, defaulted isolation.Profile, pinnedPool string) isolation.Profile {
	if pinnedPool != "" && clientRequested == "" {
		return ""
	}
	return defaulted
}

// persistedRowProfile is the §5.3 profile to persist on the session row.
// spec: §7.1 line 75 — the row.IsolationProfile is the source of truth the
// same-call and later claim re-resolve the pool against, so it must reflect
// the assigned pool's profile. The resolved level carries the pool's
// profile when a pool was resolved (including a pinned pool whose profile
// differs from the deployment default); when no pool resolved cleanly the
// level falls back to the requested profile, which can be empty if the
// client deferred to a pinned pool, so fall back to the validated defaulted
// profile rather than persist an empty profile. F-CS2 (0018).
func persistedRowProfile(level SessionIsolationLevel, defaulted isolation.Profile) isolation.Profile {
	if level.IsolationProfile == "" {
		return defaulted
	}
	return isolation.Profile(level.IsolationProfile)
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
func (s *Server) resolveIsolationLevel(ctx context.Context, runtimeRef string, requested isolation.Profile, pinnedPool string) SessionIsolationLevel {
	if s.podBinder == nil || s.podBinder.Client == nil {
		return defaultIsolationLevel(requested)
	}
	// spec: §7.1 / §14.1 — when the client pinned a pool, derive the level
	// from that named pool so the persisted sessionIsolationLevel reflects
	// the pool the session will bind to. F-CS2 (0018).
	match, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.poolPolicyReader(), s.agentNamespace, runtimeRef, string(requested), pinnedPool)
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
		// spec: §7.1 line 74 — a session-mode pod binds the session to one
		// pod for its lifetime and preserves conversation context across
		// messages.
		ConversationContinuity: continuityPlatform,
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
		// spec: §7.1 line 74 — derive conversationContinuity from the frozen
		// execution mode so a GET / List after a coordinator handoff returns
		// "none" for a service-mode row and "platform" otherwise, matching
		// the create response. The stored ConversationContinuity column
		// (migration from S25a) is authoritative when present; an empty
		// column falls back to the mode-derived value so a pre-migration or
		// never-resolved row still reports the correct continuity.
		ConversationContinuity: persistedContinuity(row.ConversationContinuity, mode),
	}
	// spec: §5.2 / §7.1 — a service-mode pod serves successive requests with
	// no scrub, and a session-mode pod that recorded a non-empty scrubPolicy
	// at create time (recycle.enabled or maxConcurrentSessions > 1) reuses a
	// pod across more than one session. Both report podReuse and the
	// residual-state warning; the one-session-per-pod default does neither.
	if mode == string(runtimestore.ExecutionModeService) || row.ScrubPolicy != "" {
		level.PodReuse = true
		level.ResidualStateWarning = true
		level.ScrubPolicy = row.ScrubPolicy
	}
	return level
}

// isolationLevelForPool maps a resolved §5.2 pool to the §7.1
// sessionIsolationLevel fields. spec: §5.2 / §7.1 lines 69-73 — a
// service-mode pool and a session-mode pool that reuses a pod
// (recycle.enabled or maxConcurrentSessions > 1) report podReuse and the
// residual-state warning; the one-session-per-pod default does neither.
func isolationLevelForPool(match podsession.PoolMatch, requested isolation.Profile) SessionIsolationLevel {
	profile := match.IsolationProfile
	if profile == "" {
		profile = string(requested)
	}
	mode := match.ExecutionMode
	if mode == "" {
		mode = string(runtimestore.ExecutionModeSession)
	}
	level := SessionIsolationLevel{
		ExecutionMode:    mode,
		IsolationProfile: profile,
		// spec: §7.1 line 74 — a service-mode pool provides no cross-message
		// continuity (each message routes to any ready replica); every other
		// mode binds the session to one pod that preserves context.
		ConversationContinuity: conversationContinuityFor(mode),
	}
	scrub := scrubPolicyForPool(match)
	if mode == string(runtimestore.ExecutionModeService) || scrub != "" {
		level.PodReuse = true
		level.ResidualStateWarning = true
		level.ScrubPolicy = scrub
	}
	return level
}

// scrubPolicyForPool returns the §7.1 line 72 scrubPolicy string for a
// reuse pool. spec: §5.2 — a service-mode pod serves successive requests
// with no scrub (`none`); a session-mode pod that recycles a pod across
// sessions scrubs best-effort, with the cross-tenant microvm variants
// selecting the VM-level scrub, and concurrent slots scrub per slot. A
// one-session-per-pod session pool returns the empty string (the field is
// omitted on the wire). The PoolMatch recycle/concurrency signals are
// derived from the §5.2 sessionPolicy mirror by ResolvePool.
func scrubPolicyForPool(match podsession.PoolMatch) string {
	if match.ExecutionMode == string(runtimestore.ExecutionModeService) {
		return "none"
	}
	if match.MaxConcurrentSessions > 1 {
		// Concurrent sessions scrub per slot on completion or failure.
		return "best-effort-per-slot"
	}
	if match.Recycle {
		// Cross-tenant microvm sequential reuse selects a VM-level scrub
		// variant; same-tenant and non-microvm reuse uses the standard
		// best-effort scrub. A cross-tenant-reuse pool is validated to carry
		// scrubProfile vm-restart or in-place (the standard in-guest scrub is
		// rejected for cross-tenant reuse, §5.2), so in-place maps to the
		// in-place scrub and any other value (vm-restart) maps to vm-restart.
		if match.IsolationProfile == string(isolation.ProfileMicrovm) && match.AllowCrossTenantReuse {
			if match.MicrovmScrubMode == string(runtimestore.MicrovmScrubInPlace) {
				return "best-effort-in-place"
			}
			return "vm-restart"
		}
		return "best-effort"
	}
	return ""
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
	q := r.URL.Query()
	// spec: §15.1 line 598 — a platform-admin may scope the listing to a
	// specific tenant via `?tenant=<id>`. A non-admin's `?tenant=` is
	// ignored: their listing stays bound to their own tenant (and the
	// Postgres RLS context enforces that regardless). F-15.1.15.
	tenantID := s.resolveTenant(r)
	if want := q.Get("tenant"); want != "" {
		if p, ok := getPrincipal(r); ok && p.HasRole(auth.RolePlatformAdmin) {
			if err := authValidateTenantID(want); err == nil {
				tenantID = want
			}
		}
	}
	filter := sessionstore.ListFilter{
		State:        session.State(q.Get("state")),
		RuntimeRef:   q.Get("runtime"),
		FailureClass: session.FailureClass(q.Get("failureClass")),
		Labels:       parseLabelFilter(q["label"]),
	}
	// spec: §15.1 lines 652, 661 — derive_failure audit rows are included
	// by default; `?includeDeriveFailures=false` excludes them. Any other
	// value (absent, "true") preserves the default audit visibility.
	// F-15.1.14.
	if q.Get("includeDeriveFailures") == "false" {
		filter.ExcludeDeriveFailures = true
	}
	// spec: §15.1 lines 1228-1253 — the canonical cursor-paginated list
	// envelope. `?cursor`/`?limit`/`?sort` are parsed and validated here;
	// the default sort is created_at:desc (line 1236) and the supported
	// fields are created_at and updated_at. F-15.1.6.
	params, ferr := pagination.ParseRequest(r,
		sessionListSortFields, sessionListDefaultSort, s.clock())
	if ferr != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", ferr.Message, ferr.Details())
		return
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
	keyOf := func(sr SessionResponse) (string, string) {
		if params.Sort.Field == "updated_at" {
			return sr.UpdatedAt, sr.ID
		}
		return sr.CreatedAt, sr.ID
	}
	pagination.SortSlice(out, params.Sort.Direction, keyOf)
	env := pagination.Page(out, params, s.clock(), keyOf)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(env)
}

// sessionListSortFields / sessionListDefaultSort pin the §15.1 line 1236
// sort contract for GET /v1/sessions: created_at (default) and
// updated_at, descending by default.
var (
	sessionListSortFields  = []string{"created_at", "updated_at"}
	sessionListDefaultSort = pagination.Sort{Field: "created_at", Direction: pagination.DirectionDesc}
)

// parseLabelFilter turns the repeatable `?label=key=value` query values
// into the AND-containment map the store List honours. A value with no
// `=` is treated as a key match against an empty value; an empty key is
// skipped. spec: §15.1 line 598 — "filterable by ... labels". F-15.1.15.
func parseLabelFilter(raw []string) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for _, item := range raw {
		key, value, _ := strings.Cut(item, "=")
		if key == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
	s.recordSessionCompleted(r.Context(), fromState, updated)
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
			s.recordSessionCompleted(r.Context(), fromState, updated)
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

// transitionFinalizing: per §15.1, /finalize enters the preparation
// barrier by transitioning created → finalizing. The workspace
// materialization, setup commands, and credential-lease assignment then
// run while the row is `finalizing`; handleFinalize transitions
// finalizing → ready only once the session is fully prepared.
func transitionFinalizing(row *sessionstore.Session) { row.State = session.StateFinalizing }

// transitionReady: per §15.1, the finalize barrier transitions
// finalizing → ready once workspace materialization, setup commands, and
// credential assignment have completed; the session then awaits /start.
func transitionReady(row *sessionstore.Session) { row.State = session.StateReady }

// handleFinalize implements POST /v1/sessions/{id}/finalize as the §4.3
// preparation barrier. After binding the optional §14 WorkspacePlan and
// transitioning created → finalizing, it reconnects to the pod claimed at
// /create and runs the §7.1 step 11-13 prepare phase against it:
// PrepareWorkspace streams the buffered lenny-blob:// upload content into
// /workspace/staging, FinalizeWorkspace materializes /workspace/current
// with the §7.4 post-promotion symlink re-validation, RunSetup runs the
// plan's setup commands, and AssignCredentials delivers the §4.9
// credential lease. Only when the session is fully prepared does it
// transition finalizing → ready and return.
//
// A failure in any prepare step reclaims the claimed pod via the §6.2
// pre-attached disposition (the binder's lease-aware failPhase, which
// revokes the lease when AssignCredentials had already run) and surfaces
// the corresponding workspace-validation, setup-command, or credential
// error; a materialization or check-to-assignment credential failure
// surfaces as CREDENTIAL_POOL_EXHAUSTED. The row transitions
// finalizing → failed so a client cannot retry finalize against a pod
// that no longer exists.
//
// Gap 2: a failure in the finalizing → ready transition, or in the
// single-use upload-token consume, AFTER AssignCredentials has succeeded
// reclaims the pod and revokes the lease too, so a post-assignment
// finalize failure does not leak the lease.
//
// The single-use uploadToken invalidation, upload-channel/limits close,
// SSE status-change, plan-warning publish, and the §16.6 finalize audit
// row run once the row reaches ready, as before. The minimal gateway (no
// pod binder) finalizes by the plain created → finalizing → ready
// transition with no pod work.
//
// spec: §7.1 steps 11-13; §7.4 lines 434, 450, 459, 461; §15.1 (finalize
// precondition); §4.9 (finalize lease assignment); §4.3 (proposal).
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
	// spec: §7.1 step 11 (FinalizeWorkspace); §26.2 lines 95-114 — the
	// §14 WorkspacePlan referencing this session's staged uploadArchive
	// blob is bound here in the decomposed create → upload → finalize
	// flow, because the create-time plan is immutable and cannot name an
	// uploadRef minted only after the session exists. A no-body finalize
	// keeps the existing plan (or empty workspace). F-24.17.4 / F-26.2.4.
	planJSON, planWarnings, hasPlan, planOK := s.resolveFinalizePlan(w, r, tenantID, row)
	if !planOK {
		return
	}
	// spec: §15.1 — enter the §4.3 preparation barrier: created → finalizing,
	// binding the finalize plan in the same logical write. The prepare phase
	// runs while the row is `finalizing`.
	updated, err := s.store.Update(r.Context(), tenantID, id, func(r *sessionstore.Session) error {
		transitionFinalizing(r)
		if hasPlan {
			r.WorkspacePlan = planJSON
		}
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §4.3 / §7.1 steps 11-13 — run the prepare phase against the pod
	// claimed at /create: stream the buffered uploads into /workspace/staging,
	// materialize /workspace/current, run setup commands, and assign the §4.9
	// credential lease. prepareAtFinalize returns (nil, nil) for the
	// dispositions that materialize nothing at finalize (no binder, service
	// mode, a concurrent-workspace slot, or a row with no live binding).
	plan, perr := storedWorkspacePlanForFinalize(updated, hasPlan, planJSON)
	if perr != nil {
		// spec: §4.3 (proposal: any finalize-barrier failure reclaims the
		// create-time pod via the §6.2 pre-attached disposition) — the parse
		// failed before the prepare phase engaged the binder, so its internal
		// failPhase reclaim cannot run. Reclaim the claimed pod here (no lease
		// is assigned yet, so the revoke is a no-op) before failing the row, so
		// a finalize-barrier failure does not leak the pod claimed at /create.
		s.reclaimFinalizedPod(r.Context(), updated.PodAssignment, id)
		s.failSession(r.Context(), tenantID, id)
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"stored workspace plan could not be parsed: "+perr.Error(), nil)
		return
	}
	prep, err := s.prepareAtFinalize(r.Context(), updated, plan)
	if err != nil {
		// spec: §4.3 / §6.2 — the finalize barrier failed; the claimed pod has
		// already been reclaimed via the §6.2 pre-attached disposition. A
		// through-Prepare failure (workspace validation, setup command, or
		// lease assignment) is reclaimed by the binder's lease-aware failPhase,
		// which revokes the lease when AssignCredentials had already run; a
		// pre-Prepare failure (pool resolution, or the check-to-assignment
		// credential mismatch) is reclaimed by prepareAtFinalize itself before it
		// returns. Either way no pod leaks. Transition the row to the terminal
		// `failed` state (finalizing → failed) so a retry of finalize cannot run
		// against a pod that no longer exists, then surface the
		// workspace-validation, setup-command, or credential error. A
		// materialization or check-to-assignment credential failure surfaces as
		// CREDENTIAL_POOL_EXHAUSTED via writePodClaimError.
		s.failSession(r.Context(), tenantID, id)
		s.writePodClaimError(w, err, "SESSION_CREATION_FAILED",
			"workspace finalization failed")
		return
	}
	// spec: §4.3 — only after the prepare phase succeeds does the barrier
	// transition finalizing → ready and return. Persist the §7.5 setup-command
	// trail and the §7.3 negotiated workspace root the prepare phase produced.
	if prep != nil {
		s.applyFinalizePrepareResult(r.Context(), tenantID, id, updated.TenantID, updated.ID, prep)
	}
	// spec: §4.3 (Gap 2) — capture the pod↔session binding from the
	// finalizing-write result, which is populated, before the finalizing → ready
	// write below. A failed Update returns the zero Session, so reading
	// PodAssignment off the failed write's result would lose the binding and the
	// Gap-2 reclaim would no-op on an empty sandbox name, leaking the pod and the
	// finalize-assigned lease.
	podAssignment := updated.PodAssignment
	uploadTokenDigest := updated.UploadTokenDigest
	uploadTokenExpiry := updated.UploadTokenExpiry
	// spec: §15.1 — finalizing → ready. Gap 2: when AssignCredentials has
	// already run (prep != nil), a failure of this transition would leave the
	// lease assigned but the session not ready, so reclaim the pod and revoke
	// the lease before surfacing the error.
	updated, err = s.store.Update(r.Context(), tenantID, id, func(r *sessionstore.Session) error {
		transitionReady(r)
		return nil
	})
	if err != nil {
		// Gap 2: the prepare phase succeeded (the lease is assigned) but the
		// finalizing → ready write failed. Reclaim the pod and revoke the lease,
		// then mark the row failed so it reaches a terminal state rather than
		// stranding in `finalizing` (which no /finalize retry can leave, because
		// the finalize precondition requires `created`).
		if prep != nil {
			s.reclaimFinalizedPod(r.Context(), podAssignment, id)
		}
		s.failSession(r.Context(), tenantID, id)
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// §7.1 single-use uploadToken invalidation: once the upload
	// window closes, the digest cannot mint another upload. Gap 2: a consume
	// failure after AssignCredentials succeeded would leave the lease assigned
	// on a session whose finalize the client must treat as failed, so reclaim
	// the pod and revoke the lease rather than leaking them.
	if s.uploadVerifier != nil && uploadTokenDigest != "" {
		if cerr := s.uploadVerifier.ConsumeDigest(uploadTokenDigest, uploadTokenExpiry); cerr != nil {
			if prep != nil {
				s.reclaimFinalizedPod(r.Context(), podAssignment, id)
			}
			s.failSession(r.Context(), tenantID, id)
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"upload token could not be invalidated: "+cerr.Error(), nil)
			return
		}
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
	// spec: §7.2 line 137 — surface the finalizing → ready transition that
	// closed the §4.3 preparation barrier.
	s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
	// spec: §14 lines 100/334/338 — surface any consumer-advisory parse
	// warnings the finalize-bound plan raised on the same per-session SSE
	// bus the create path uses. F-24.17.4 / F-26.2.4.
	if hasPlan {
		s.publishParsePlanWarnings(updated.TenantID, updated.ID, planWarnings)
	}
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
// same code. An unmapped code resolves through the status-aware
// ClassifyStatus, so an unmapped non-5xx code classifies as
// (PERMANENT, false) here exactly as it does on the admin surface
// (ClassifyStatus in tenants.go), rather than the (TRANSIENT, true)
// fallback the code-only Classify returns. spec: §15.2.1
// (classification consistency).
func (s *Server) writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	cat, retryable := errorclassify.ClassifyStatus(code, status)
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
	// spec: §14 line 311 / §15.1 line 598 — echo the client labels so a
	// caller can confirm the filterable selector set on the row. F-15.1.15.
	if len(row.Labels) > 0 {
		out.Labels = cloneMetadata(row.Labels)
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
	// spec: §27.6 line 203 — surface the origin=playground label on every
	// read so §25.9 audit queries and §27.8 dashboards can slice on it.
	// F-27.6.8.
	out.Origin = row.Origin
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
