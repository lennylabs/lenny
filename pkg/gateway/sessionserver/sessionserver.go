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
	"net/http"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/billing/usagestore"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/resultrollup"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treerecovery"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/provisioning/envblock"
	"github.com/lennylabs/lenny/pkg/gateway/provisioning/vcscred"
	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota"
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
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/sandbox/slotstate"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// Server is the §15.1 session HTTP handler.
type Server struct {
	store sessionstore.Store
	clock func() time.Time
	idFn  func() string
	// serviceHandlerOnce/serviceHandler cache the routing handler the
	// §15.2.1 rule-1 in-process service layer (ServiceCall) dispatches
	// through, so the MCP tool surface reuses the exact REST routes and
	// handlers rather than a parallel table that could drift. spec:
	// §15.2.1 rule 1. F-15.2.3.
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
	// array then materializes empty. spec: §8.8. F-8.8.2.
	artifacts artifactcatalog.Store
	events    *sessionevents.Bus
	// activityStamper records §6.2 qualifying agent
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
	// spec: §7.2.
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
	// spec: §10.2, F-10.2.4.
	multiTenant bool
	// tenancyMode is the §4.9 platform tenancy.mode ("multi" or "single"),
	// wired from the --tenancy-mode flag (the same signal the layer-2
	// admission webhook and the warm-pool layer-1 registration check key
	// off). The session-start credential-delivery gate runs
	// direct_mode_isolation.Decide against this value so a resolved
	// CredentialPool whose effective deliveryMode pairs with the bound
	// pod's isolationProfile/spiffeBinding in a forbidden combination is
	// rejected before any lease is minted. Empty is treated as
	// single-tenant (the gate never rejects). spec: §4.9.
	tenancyMode string
	podBinder   *podsession.Binder
	podRegistry *podsession.Registry
	// claimQueue is the §4.6.1 per-pool claim FIFO that backs
	// sessionPolicy.onPoolExhausted: queue. On a `queue` pool the start path
	// holds an exhausted acquisition in this FIFO for up to
	// maxQueueWaitSeconds, re-entering acquisition as pods free; on a `reject`
	// pool the queue is bypassed and the acquisition returns
	// WARM_POOL_EXHAUSTED immediately. Always non-nil after New.
	// spec: §4.6.1 (Pool exhaustion behavior), §5.2 (onPoolExhausted).
	claimQueue *podClaimQueue
	fencer     CoordinationFencer
	// leaseStore is the §12.2 per-session coordination LeaseStore. The
	// bind funnel (registerBinding and the hoisted early-commit sites)
	// acquires the lease on this replica before it publishes the pod
	// binding, so the replica that holds the binding holds the lease from
	// bind time and a peer coordination sweep observes the held lease and
	// skips the session on ErrHeld. Nil disables the at-bind acquire (the
	// in-memory / dev posture with no Redis leasestore); the bind then
	// publishes as before. spec: §4.6.1 (coordinating replica holds the
	// lease), §10.1 (per-session coordination lease).
	leaseStore leasestore.LeaseStore
	// replicaID is the lease-holder identity the at-bind Acquire records.
	// It must match the coordination Sweeper's ReplicaID so the Sweeper
	// renews the leases this replica acquires at bind. Empty disables the
	// at-bind acquire. spec: §10.1.
	replicaID string
	// coordLeaseTTL is the TTL the at-bind Acquire stamps on the lease. The
	// coordination Sweeper renews it on its own cadence, so the TTL must
	// exceed the sweep interval for a held lease not to lapse between
	// renewals. Defaults to DefaultCoordinationLeaseTTL. spec: §10.1.
	coordLeaseTTL  time.Duration
	agentNamespace string
	// poolNameResolver resolves the §5.2 warm pool a (runtimeRef,
	// isolation profile) pair maps to, for the §15.1 pool-drain
	// admission gate. The pinnedPool argument carries the §14.1
	// CreateSessionRequest.pool selector so a client-pinned pool is the one
	// the gate resolves. It defaults to resolvePoolName (CRD-backed); tests
	// override it to exercise the gate without a Kubernetes client.
	poolNameResolver func(ctx context.Context, runtimeRef string, requested isolation.Profile, pinnedPool string) (string, bool)
	// playgroundCaps resolves the §27.6 idle/duration caps for a
	// §27.3 origin=playground session. Wired post-construction via
	// SetPlaygroundCaps (the playground bootstrap runs after the session
	// server is built). Nil leaves a playground session bounded only by
	// the runtime/platform caps. spec: §27.6. F-27.6.1 /
	// F-27.6.2.
	playgroundCaps PlaygroundCapResolver
	// incPlaygroundSessionCreated records the §27.8
	// lenny_playground_sessions_created_total metric once the origin claim
	// is read on the create path. Nil disables the metric. F-27.6.11.
	incPlaygroundSessionCreated func(runtime string)
	// admissionRL is the §11.1 per-minute counter used for the
	// per-runtime and per-pool admission scopes enforced at session
	// creation (the global/per-user/per-tenant scopes run in the §11.1
	// HTTP middleware). Nil disables the per-runtime/per-pool scopes.
	// F-11.1.2.
	admissionRL      ratelimit.Counter
	perRuntimePerMin int
	perPoolPerMin    int
	rlMetrics        AdmissionRateLimitMetrics
	// maxConcSessGlobal / maxConcSessPerUser / maxConcSessPerRuntime are
	// the §11.1 concurrent-session admission caps (live
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
	// rate limiting. spec: §10.7.
	evalRL ratelimit.Counter
	// evalPerSessionPerMin / evalPerTenantPerMin are the §10.7
	// `evalRateLimit.perSessionPerMinute` / `perTenantPerMinute` limits.
	// Non-positive disables the corresponding scope. spec: §10.7.
	evalPerSessionPerMin int
	evalPerTenantPerMin  int
	sealer               Sealer
	// sealMaxDuration bounds the §7.1 seal-and-export retry window
	// (maxWorkspaceSealDurationSeconds). A non-positive value falls
	// through to DefaultWorkspaceSealMaxDuration (300s).
	// spec: §7.1.
	sealMaxDuration time.Duration
	// sealSleep waits d before the next seal retry, returning false when
	// ctx is cancelled first. Nil selects a context-aware time.Sleep; the
	// seam lets tests drive the backoff loop without real delays.
	sealSleep func(ctx context.Context, d time.Duration) bool
	// observeSealDuration, when set, records the §7.1
	// lenny_workspace_seal_duration_seconds{pool,outcome} histogram. Nil
	// disables the emission.
	observeSealDuration func(pool, outcome string, seconds float64)
	// recordSessionTerminal, when set, records the §16.1 /
	// §10.7 rollback-trigger session metric family at terminal transition.
	// Nil disables the emission. spec: §10.7.
	recordSessionTerminal func(tenantID, sessionType, variantID string, isError bool, seconds float64)
	// observeEvalScore, when set, records the §16.1 lenny_eval_score
	// observation per submitted eval. Nil disables the emission.
	// spec: §10.7.
	observeEvalScore func(tenantID, scorer, variantID string, score float64)
	// targetingBreaker is the §10.7 SCL-023 per-tenant OpenFeature
	// targeting circuit breaker. Never nil after New; it is consulted on
	// the external-targeting hot path so sustained provider failures skip
	// the OpenFeature call entirely. spec: §10.7.
	targetingBreaker *targetingBreaker
	// partialManifestCleaner, when set, executes the §4.4
	// partial-manifest cleanup after the resume path completes.
	// Nil leaves the resume path unchanged (cleanup is deferred to
	// the §12.5 backstop sweep).
	partialManifestCleaner PartialManifestCleaner
	// evictionStateLookup, when set, classifies a resume as
	// conversation-only (workspace lost during eviction) so the
	// session.resumed event surfaces the correct ResumeMode per
	// §4.4.
	evictionStateLookup EvictionStateLookup
	// partialManifestLookup, when set, classifies a resume as
	// partial-workspace (reassembled from chunk objects) so the
	// session.resumed event surfaces the correct ResumeMode per
	// §10.1 partial-manifest path.
	partialManifestLookup PartialManifestLookup
	// resumeChunkResolver, when set, resolves the §10.1.7 reassembly
	// chunk set the resume path hands the adapter on ResumeRequest.Chunks.
	// Nil leaves the resume carrying no chunks (dev mode, or a checkpoint
	// that predates the chunked-object model), so the adapter restores
	// nothing beyond what a snapshotless rebuild recovers.
	resumeChunkResolver ResumeChunkResolver
	// checkpointRecoveryMetrics, when set, receives the §16.1
	// lenny_checkpoint_partial_total{recovered=true} emission once per
	// above-threshold partial reassembly on resume. *gatewaymetrics.Metrics
	// satisfies it. Nil leaves the resume recovering without observability.
	checkpointRecoveryMetrics CheckpointRecoveryMetrics
	// checkpointManifests, when set, reads §10.1 checkpoint_manifest rows
	// for the resume fallback (LatestFull) and the workspace-download path
	// (Get, to resolve chunk_count / chunk_encoding).
	checkpointManifests CheckpointManifestReader
	// checkpointManifestWriter, when set, writes the §7.1 derived-session
	// manifest row after the derive path copies the parent's chunks into
	// the derived prefix.
	checkpointManifestWriter CheckpointManifestWriter
	treeArchive              treearchive.Store
	taskUsage                *resultrollup.Builder
	treeBudgetReturner       TreeBudgetReturner
	// leaseRegistrar, when set, registers a newly created root session
	// with the §8.6 lease-extension budget source so a later in-process
	// budget-exhaustion extension (the gateway LLM Proxy's ExtendForBudget
	// trigger) resolves the tree instead of failing ErrSessionNotFound.
	// leaseExtDefaults carries the deployment-level configuration the root
	// tree's budget ceiling is resolved from. F-15.3.5.
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
	// capOverrides applies the §5.1 per-tenant capability override
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
	// resumeWindow is the §4.2 default resume-eligibility
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
	// sessionLogHook, when set, receives the §4.4 session-log
	// close-hook on every session transition to a terminal state.
	// Best-effort: a failure logs and discards rather than abort the
	// transition.
	sessionLogHook SessionLogHook
	// warmupEstimateSeconds is the §5.2 estimate used for the
	// PoolWarmingUp 503's estimatedReadyIn and Retry-After. A zero value
	// falls through to DefaultWarmupEstimateSeconds.
	warmupEstimateSeconds int
	// credRouter is the §4.9 CredentialRouter used at session creation
	// to resolve a credential source and pool per provider. Never nil
	// after New (defaults to credrouter.Default).
	credRouter credrouter.Router
	// preclaimMismatch, when set, increments the §4.9
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
	// a fresh Registry). spec: §6.2.
	slotStates *slotstate.Registry
	// slotReplacement, when set, increments
	// lenny_slot_pod_replacement_total{pool} when the slot retry policy
	// drains an unhealthy concurrent-mode pod for replacement. Nil disables
	// the emission. spec: §5.2 "whole-pod replacement trigger".
	slotReplacement func(pool string)
	// slotLeakGauge, when set, publishes the §6.2
	// lenny_adapter_leaked_slots{pod_id,pool} gauge to leaked: the count of
	// the pod's leaked slots, which remain counted in active_slots until the
	// pod terminates. Nil disables the emission.
	slotLeakGauge func(pod, pool string, leaked int)
	// slotAccountLocks orders, per session, the failure funnel's slot
	// accounting before a same-replica resume releases the session's earlier
	// binding, so a drain the accounting requests is stamped before the
	// release can reach the occupancy-zero recycle edge. The zero value is
	// ready to use. spec: §5.2 "whole-pod replacement trigger".
	slotAccountLocks sessionLocks
	// observeStartupDuration, when set, records the §6.3
	// end-to-end pod-warm startup latency on a successful start. Nil
	// disables the emission.
	observeStartupDuration func(pool, runtimeClass, isolationProfile string, seconds float64)
	// observeStartupPhase, when set, records the §6.3 latency
	// of one hot-path startup phase. Nil disables the emission.
	observeStartupPhase func(phase, runtimeClass string, seconds float64)
	// observeTimeToFirstToken, when set, records the §6.3 /
	// §16.1 end-to-end TTFT histogram on the first
	// agent-streamed response event of each session. Nil disables
	// the emission.
	observeTimeToFirstToken func(pool, runtimeClass, isolationProfile string, seconds float64)
	// firstTokenObserved tracks sessions that already recorded their
	// §6.3 / §16.1 TTFT observation. Entries are added on the first
	// qualifying response event and cleared on the terminal lifecycle
	// transition so the map size scales with concurrently-streaming
	// sessions, not lifetime sessions. Keyed by session id, value is
	// a sentinel struct{} placeholder. spec: §6.3.
	firstTokenObserved sync.Map
	// userCredChecker reports whether a usable user-scoped credential
	// exists for (tenant, user, provider) and is deliverable. The §4.9
	// router resolves user sources only when this reports true. Wired by
	// SetUserCredChecker from the usercreds.Materializer; nil leaves the
	// router unable to resolve a user source (it falls through to pool).
	// spec: §4.9.
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
	// way. spec: §7.2 table; §11.7; §16.7. F-7.2.8.
	interactionAudit InteractionAuditSink

	// toolApprovalWaits, when set, is the §7.2 tool-use approval waiter
	// registry the approve/deny endpoints signal so a blocked executor
	// read (the pod runtime awaiting a tool-call verdict) unblocks. Nil
	// leaves the resolution endpoints recording the interaction phase
	// only — the dev / non-pod posture where nothing blocks on the
	// verdict. spec: §7.2. F-7.2.18.
	toolApprovalWaits *toolapproval.Registry

	// treeCycleObserver, when set, receives a §8.9 cycle observation
	// whenever the /v1/sessions/{id}/tree walker hits a repeated node
	// in the ParentSessionID lineage. Nil disables the emission and
	// the walker still truncates the cycle so the response remains
	// well-formed. spec: §8.9; F-8.9.10.
	treeCycleObserver TreeCycleObserver

	// callbackValidator enforces the §14 callbackUrl SSRF mitigations at
	// admission (HTTPS-only, IP-literal / private-range rejection, DNS
	// pinning, optional deployer domain allowlist). Never nil after New
	// (defaults to a validator with no domain allowlist). spec: §14. F-14.1.11.
	callbackValidator *sessioncallback.Validator
	// callbackSeal KMS-envelope-encrypts a client callbackSecret under the
	// session tenant's KEK at admission. Nil disables callbackSecret
	// acceptance (a callbackUrl without a secret still delivers, unsigned).
	// spec: §14. F-14.1.11.
	callbackSeal func(ctx context.Context, tenantID string, plaintext []byte) ([]byte, error)
	// callbackDispatcher delivers validated §14 callbacks from an isolated
	// worker pool with the §14 retry budget. Nil leaves callbacks validated
	// and persisted but undelivered (the dev/test posture). spec: §14. F-14.1.11.
	callbackDispatcher *sessioncallback.Dispatcher

	// inputWaits, when set, makes the REST POST /v1/sessions/{id}/messages
	// handler honor §7.2 path 1: a request body whose `inReplyTo`
	// matches an outstanding `lenny/request_input` call resolves the
	// blocked tool call directly instead of being delivered to the
	// executor. Nil leaves the REST surface at path 2 only (executor
	// delivery), matching the pre-F-7.2.14 behaviour. The same
	// registry is shared with the MCP `lenny/send_message` /
	// `lenny/request_input` pair so the two transports route to the
	// same blocked tool call. spec: §7.2.
	inputWaits *inputwait.Registry

	// deriveLock, when set, serializes concurrent /v1/sessions/{id}/derive
	// calls on the same source session per §7.1. The session-
	// server holds the lock around the workspace-snapshot read; it is
	// released as soon as the snapshot reference resolves, mirroring the
	// spec's "release the lock before the copy is safe" guarantee.
	// Production wires a derivelock.Redis implementation backed by the
	// shared Redis client; tests and the minimal gateway fall back to
	// derivelock.Memory (per-source sync.Mutex). When nil, the in-memory
	// store mutex serializes within the running process and no cross-
	// replica protection is in force — the legacy minimal-gateway
	// posture. spec: §7.1.
	deriveLock derivelock.Lock

	// persistDeriveFailureRows is the §7.1 derive rule 2 opt-in
	// (`gateway.persistDeriveFailureRows`, default false). When true, a
	// `POST /v1/sessions/{id}/derive` that fails after the workspace copy
	// is attempted persists a terminal `failed` Session row with
	// `failureClass = derive_failure` for audit, reachable per the §15.1
	// derive-failure reachability table. When false the gateway writes
	// nothing on failure (the default roll-back-without-persist posture).
	// spec: §7.1 derive rule 2; §15.1. F-15.1.14.
	persistDeriveFailureRows bool

	// incDeriveFailureAudit, when set, increments the §16.1
	// `lenny_session_derive_failure_audit_total{outcome}` counter for each
	// derive-failure audit write attempt. Outcome is "persisted" when the
	// row was written, "fenced" when a coordinator handoff fenced the
	// write out, or "error" when the INSERT failed. Nil disables the
	// emission. spec: §7.1 derive rule 2; §16.1. F-15.1.14.
	incDeriveFailureAudit func(outcome string)

	// defaultRetention is the §7.1 default artifact-retention
	// window stamped on every session at create time and rolled forward
	// at the terminal transition. A non-positive value falls through to
	// DefaultArtifactRetention.
	// spec: §7.1 — "configurable TTL (default: 7 days ...)".
	defaultRetention time.Duration

	// retryPolicyCaps holds the §7.3 deployer caps applied to every
	// client-supplied RetryPolicy at admission. A zero field in the cap
	// disables that clamp so deployer "unlimited" semantics survive. The
	// gateway wires these to the watchdog config so a session's clamped
	// caps cannot exceed the platform-wide bounds the watchdog itself
	// enforces. F-7.3.1 / F-7.3.24.
	// spec: §7.3.
	retryPolicyCaps session.RetryPolicyCaps

	// envBlocklist is the §14 deployer-configured env-var blocklist
	// applied to a CreateSessionRequest's `env` field. Never nil after
	// New (defaults to the platform default blocklist alone). spec: §14. F-14.1.12.
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

	// incWarmpoolWarmupFailure, when set, increments the §16.1
	// lenny_warmpool_warmup_failure_total{error_type} counter for one
	// warm-pool startup failure. Nil disables the emission. spec: §16.1, §7.3 — F-7.5.9.
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

	// uploadTokenTTL is the §7.1 upload-token expiry stamped on
	// every minted token. The gateway sets this equal to
	// `maxCreatedStateTimeoutSeconds` so the token deadline matches the
	// `created` state deadline. A zero value falls through to
	// uploadtoken.DefaultTTL (300s). spec: §7.1. F-7.4.7.
	uploadTokenTTL time.Duration

	// uploadAborts is the §7.4 upload-abort registry. The
	// upload handler registers a per-session abort signal for the
	// duration of its body-read + blob.Put; the finalize handler closes
	// the signal after the row transitions out of the upload-admitting
	// state so any in-flight stream surfaces UPLOAD_CHANNEL_CLOSED.
	// Always non-nil after New. spec: §7.4. F-7.4.16.
	uploadAborts *uploadAbortRegistry

	// uploadLimits enforces the §11.1 per-session and global
	// concurrent-upload caps and the per-session cumulative upload-size
	// cap. Nil when no §11.1 upload cap is configured (the pass-through
	// posture); every call site tolerates a nil limiter. spec: §11.1. F-11.1.5, F-11.1.6.
	uploadLimits *uploadLimiter

	// midSessionUploadEnabled is the §7.4 deployer policy that,
	// together with the bound runtime's capabilities.midSessionUpload flag,
	// admits uploads into an already-running session via
	// POST /v1/sessions/{id}/upload-to-session. False (the default) keeps
	// mid-session uploads off platform-wide regardless of the runtime flag.
	// spec: §7.4 — F-7.4.6.
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
// spec: §10.7. F-10.7.4.
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

// DefaultResumeWindow is the §4.2 default resume-eligibility
// window. A session created without an explicit override is eligible
// for resume up to this duration after creation; once the deadline
// passes, the watchdog forces the session to a terminal state.
//
// The value mirrors watchdog.DefaultMaxSessionAgeSeconds (2 hours).
// Operators tuning the watchdog's lifetime cap should also override
// the resume window so the two budgets stay aligned; the option
// hook below lets the gateway plumb the watchdog-configured value.
// spec: §4.2 — "Resume eligibility and window".
const DefaultResumeWindow = 2 * time.Hour

// DefaultCoordinationLeaseTTL is the TTL the at-bind coordination-lease
// acquire stamps when the gateway does not override it. It matches the
// coordination Sweeper's default lease lifetime (four 15s sweep
// intervals) so a lease acquired at bind does not lapse before the first
// renewal. spec: §10.1 (per-session coordination lease).
const DefaultCoordinationLeaseTTL = 60 * time.Second

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
	// calls on the same source session per §7.1. Production
	// wires derivelock.NewRedis against the shared Redis client; tests
	// and the single-replica minimal gateway can wire derivelock.NewMemory
	// or leave this nil (the in-memory store mutex serializes within the
	// running process and is correct for a single replica). On
	// contention the handler returns 429 DERIVE_LOCK_CONTENTION.
	// spec: §7.1.
	DeriveLock derivelock.Lock

	// PersistDeriveFailureRows is the §7.1 derive rule 2 opt-in. When
	// true, a derive that fails after the workspace copy is attempted
	// persists a terminal `failed` Session row with
	// `failureClass = derive_failure` for audit (reachable per the §15.1
	// derive-failure reachability table). Default false keeps the
	// roll-back-without-persist posture. Wired from
	// `gateway.persistDeriveFailureRows`. spec: §7.1 derive rule 2;
	// §15.1. F-15.1.14.
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
	// spec: §7.2 table. F-7.2.8.
	InteractionAuditSink InteractionAuditSink

	// ToolApprovalWaits, when set, is the §7.2 tool-use approval waiter
	// registry shared with the ToolApprovalGate the pod executor calls.
	// The approve/deny endpoints deliver the verdict onto it so the
	// blocked runtime tool call unblocks. Nil leaves the endpoints
	// recording the interaction phase only. spec: §7.2.
	// F-7.2.18.
	ToolApprovalWaits *toolapproval.Registry

	// TreeCycleObserver, when set, receives a §8.9 cycle observation
	// when /v1/sessions/{id}/tree hits a repeated node in the
	// ParentSessionID lineage. Production wires this to the
	// `delegation.tree_cycle_detected` audit event plus the §16.1
	// `lenny_delegation_tree_cycle_detected_total` counter; nil
	// disables the emission and the walker still truncates the cycle.
	// spec: §8.9; F-8.9.10.
	TreeCycleObserver TreeCycleObserver

	// CallbackValidator validates client callbackUrls against the §14 SSRF
	// mitigations. Nil installs a default validator with no deployer
	// domain allowlist. spec: §14. F-14.1.11.
	CallbackValidator *sessioncallback.Validator
	// CallbackSeal KMS-envelope-encrypts a client callbackSecret under the
	// session tenant's KEK at admission. Nil disables callbackSecret
	// acceptance (a callbackUrl with no secret still delivers, unsigned).
	// spec: §14. F-14.1.11.
	CallbackSeal func(ctx context.Context, tenantID string, plaintext []byte) ([]byte, error)
	// CallbackDispatcher delivers validated §14 callbacks. Nil leaves
	// callbacks validated and persisted but undelivered (the dev/test
	// posture); the cmd wires a real dispatcher. spec: §14.
	// F-14.1.11.
	CallbackDispatcher *sessioncallback.Dispatcher

	// InputWaits is the shared §8.5 `lenny/request_input` pending-call
	// registry. When set, the REST POST /v1/sessions/{id}/messages
	// handler resolves a §7.2 path 1 `inReplyTo` directly against the
	// registry instead of routing through the executor. Production
	// wires the same `*inputwait.Registry` instance into both the
	// sessionserver and the MCP tools deps; tests can leave it nil for
	// pre-F-7.2.14 behaviour. spec: §7.2. F-7.2.14.
	InputWaits *inputwait.Registry

	// DefaultRetention overrides the §7.1 default artifact-
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
	// minted token. Per §7.1 the token TTL equals
	// `maxCreatedStateTimeoutSeconds`; the gateway threads the same
	// configured timeout through this field, the watchdog's
	// MaxCreatedSeconds, and the createdsweeper's Timeout so the three
	// budgets never drift. A non-positive value falls through to
	// uploadtoken.DefaultTTL (300s). spec: §7.1. F-7.4.7.
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
	// spec: §8.8. F-8.8.2.
	Artifacts artifactcatalog.Store

	// Events is the §15.1 session event bus backing the SSE stream.
	// When nil, `GET /v1/sessions/{id}/events` returns
	// `503 EVENT_STREAM_UNAVAILABLE` and message injection skips
	// event publication.
	Events *sessionevents.Bus

	// ActivityStamper records §6.2 qualifying agent
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
	// durability (the dev / no-Redis posture). spec: §7.2.
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
	// back (§10.7). Nil re-evaluates every experiment fresh, which is
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
	// §5.3.
	DefaultIsolationProfile isolation.Profile

	// DevMode is the platform global.devMode (LENNY_DEV_MODE=true). It
	// selects the §5.3 dev-mode fallback (`standard`) when no
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
	// spec: §10.2. F-10.2.4.
	MultiTenant bool

	// TenancyMode is the §4.9 platform tenancy.mode ("multi" or "single"),
	// wired from the gateway --tenancy-mode flag (the same .Values.tenancy.mode
	// the layer-2 admission webhook and the warm-pool layer-1 registration
	// check read). The session-start credential-delivery gate enforces the
	// two cross-tenant credential-delivery rejections only when this is
	// "multi". Empty is treated as single-tenant. spec: §4.9.
	TenancyMode string

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
	// disables fencing (dev / in-memory mode). spec: §10.1,
	// §11.3.
	CoordinationFencer CoordinationFencer

	// CoordinationLeaseStore is the §12.2 LeaseStore the at-bind acquire
	// claims the per-session coordination lease against, so the replica
	// that holds the pod binding holds the lease from bind time (§10.1
	// co-location). Nil disables the at-bind acquire (the in-memory / dev
	// posture with no Redis leasestore). spec: §4.6.1, §10.1.
	CoordinationLeaseStore leasestore.LeaseStore

	// ReplicaID is the lease-holder identity the at-bind Acquire records.
	// It must match the coordination Sweeper's ReplicaID so the Sweeper
	// renews the leases this replica acquires at bind. Empty disables the
	// at-bind acquire. spec: §10.1.
	ReplicaID string

	// CoordinationLeaseTTL is the TTL stamped on the at-bind lease acquire.
	// A non-positive value falls through to DefaultCoordinationLeaseTTL. It
	// must exceed the coordination Sweeper interval so a held lease does not
	// lapse between renewals. spec: §10.1.
	CoordinationLeaseTTL time.Duration

	// AgentNamespace is the namespace the warm pools and Sandboxes live
	// in. Required when PodBinder is set.
	AgentNamespace string

	// AdmissionRateLimitCounter is the §11.1 per-minute counter
	// used for the per-runtime and per-pool admission scopes enforced at
	// session creation. Nil disables both scopes (the global, per-user,
	// and per-tenant scopes run in the §11.1 HTTP middleware regardless).
	// Production wires the shared Redis-backed counter so the limit holds
	// across replicas. spec: §11.1. F-11.1.2.
	AdmissionRateLimitCounter ratelimit.Counter

	// PerRuntimePerMinute caps session-creation requests against a single
	// runtime per minute. Zero or less leaves the per-runtime scope
	// unlimited. spec: §11.1. F-11.1.2.
	PerRuntimePerMinute int

	// PerPoolPerMinute caps session-creation requests against a single
	// resolved warm pool per minute. The scope is skipped when no pool
	// resolves (the Postgres-only posture). Zero or less leaves the
	// per-pool scope unlimited. spec: §11.1. F-11.1.2.
	PerPoolPerMinute int

	// RateLimitMetrics, when set, receives the §11.1 rejection counter
	// and counter-failure bump for the per-runtime / per-pool gate.
	// *gatewaymetrics.Metrics satisfies it. Nil leaves the gate
	// enforcing without observability. spec: §11.1. F-11.1.2.
	RateLimitMetrics AdmissionRateLimitMetrics

	// MaxConcurrentSessionsGlobal caps the gateway-wide count of live
	// (non-terminal) sessions across every tenant. Zero or less leaves
	// the global concurrent-session scope unlimited. Operator-tunable via
	// the gateway Helm value `gateway.maxConcurrentSessionsGlobal`.
	// spec: §11.1. F-11.1.3.
	MaxConcurrentSessionsGlobal int

	// MaxConcurrentSessionsPerUser caps the count of live (non-terminal)
	// sessions a single user may hold within their tenant, so one user
	// cannot monopolize the tenant's concurrent-session capacity. Zero or
	// less leaves the per-user scope unlimited. Operator-tunable via
	// `gateway.maxConcurrentSessionsPerUser`.
	// spec: §11.1. F-11.1.3.
	MaxConcurrentSessionsPerUser int

	// MaxConcurrentSessionsPerRuntime caps the count of live
	// (non-terminal) sessions targeting a single runtime within a tenant,
	// so one runtime cannot be flooded with concurrent sessions. Zero or
	// less leaves the per-runtime scope unlimited. Operator-tunable via
	// `gateway.maxConcurrentSessionsPerRuntime`.
	// spec: §11.1. F-11.1.3.
	MaxConcurrentSessionsPerRuntime int

	// EvalRateLimitCounter is the §10.7 per-minute counter for the
	// eval-submission rate limit (per-session and per-tenant scopes on
	// POST /v1/sessions/{id}/eval). Nil disables eval rate limiting.
	// Production wires the same Redis-backed counter as
	// AdmissionRateLimitCounter so the limit holds across replicas.
	// spec: §10.7. F-10.7.4.
	EvalRateLimitCounter ratelimit.Counter

	// EvalPerSessionPerMinute caps eval submissions against a single
	// session per minute (§10.7 `evalRateLimit.perSessionPerMinute`).
	// Zero selects DefaultEvalPerSessionPerMin (100); a negative value
	// disables the per-session scope. spec: §10.7. F-10.7.4.
	EvalPerSessionPerMinute int

	// EvalPerTenantPerMinute caps eval submissions across all of a
	// tenant's sessions per minute (§10.7 `evalRateLimit.perTenantPerMinute`).
	// Zero selects DefaultEvalPerTenantPerMin (10000); a negative value
	// disables the per-tenant scope. spec: §10.7. F-10.7.4.
	EvalPerTenantPerMinute int

	// Sealer, when set, takes the §7.1 final workspace snapshot when a
	// session reaches a terminal state. Nil disables seal-and-export.
	Sealer Sealer

	// WorkspaceSealMaxDuration bounds the §7.1 seal-and-export retry
	// window (maxWorkspaceSealDurationSeconds). A seal that does not
	// succeed within this window transitions the session to failed with
	// reason workspace_seal_timeout. A non-positive value selects
	// DefaultWorkspaceSealMaxDuration (300s, the spec default).
	// spec: §7.1.
	WorkspaceSealMaxDuration time.Duration

	// ObserveWorkspaceSealDuration, when set, records the §7.1
	// lenny_workspace_seal_duration_seconds{pool,outcome} histogram.
	// outcome is "success" or "timeout". Nil disables the emission.
	ObserveWorkspaceSealDuration func(pool, outcome string, seconds float64)

	// RecordSessionTerminal, when set, records the §16.1 /
	// §10.7 rollback-trigger metric family at every terminal session
	// transition (lenny_session_total, lenny_session_error_total, and
	// lenny_session_duration_seconds). sessionType is the §5.2
	// ExecutionMode; variantID is the §10.7 enrollment. Nil disables the
	// emission. spec: §10.7, §16.1.
	RecordSessionTerminal func(tenantID, sessionType, variantID string, isError bool, seconds float64)

	// ObserveEvalScore, when set, records one §16.1
	// lenny_eval_score observation per submitted eval run. Nil disables
	// the emission. spec: §10.7, §16.1.
	ObserveEvalScore func(tenantID, scorer, variantID string, score float64)

	// SetExperimentTargetingCircuitOpen, when set, reports the §10.7
	// SCL-023 targeting circuit-breaker open/closed transitions through
	// the lenny_experiment_targeting_circuit_open gauge. Nil disables the
	// gauge emission; the breaker still gates the OpenFeature call.
	// spec: §10.7, §16.1.
	SetExperimentTargetingCircuitOpen func(tenantID, provider string, open bool)

	// SealSleep overrides the seal-retry backoff wait. Production leaves
	// it nil (a context-aware time.Sleep); tests inject a no-op so the
	// bounded-backoff loop runs without real delays.
	SealSleep func(ctx context.Context, d time.Duration) bool

	// PartialManifestCleaner, when set, executes the §4.4
	// partial-manifest cleanup after the resume path completes. Nil
	// leaves the resume path unchanged; the §12.5 backstop sweep
	// remains the only cleanup path.
	PartialManifestCleaner PartialManifestCleaner

	// EvictionStateLookup, when set, lets the resume path classify a
	// resume as conversation-only (workspace lost during eviction)
	// per §4.4, so the session.resumed event carries
	// resumeMode: "conversation_only" and workspaceLost: true. Nil
	// leaves the resume defaulting to the snapshot-source-derived
	// ResumeMode (ResumeFull when a workspace snapshot is present).
	EvictionStateLookup EvictionStateLookup

	// PartialManifestLookup, when set, lets the resume path classify
	// a resume as partial-workspace (reassembled from chunk objects)
	// per §10.1 partial-manifest path. Nil leaves the resume
	// defaulting to ResumeFull when a workspace snapshot is present.
	PartialManifestLookup PartialManifestLookup

	// ResumeChunkResolver, when set, resolves the §10.1.7 reassembly
	// chunk set the resume path hands the adapter. Nil leaves the resume
	// carrying no chunks.
	ResumeChunkResolver ResumeChunkResolver

	// CheckpointRecoveryMetrics, when set, receives the §16.1
	// lenny_checkpoint_partial_total{recovered=true} emission once per
	// above-threshold partial reassembly on resume. *gatewaymetrics.Metrics
	// satisfies it. Nil leaves the resume recovering without observability.
	// spec: §16.1.
	CheckpointRecoveryMetrics CheckpointRecoveryMetrics

	// CheckpointManifestReader, when set, reads §10.1 checkpoint_manifest
	// rows for the resume fallback (LatestFull) and the workspace-download
	// path (Get). Nil disables the contiguity fallback and serves the
	// workspace download from the legacy single-snapshot ref.
	CheckpointManifestReader CheckpointManifestReader

	// CheckpointManifestWriter, when set, writes the §7.1 derived-session
	// manifest row after the derive path copies the parent's chunks into
	// the derived prefix. Nil leaves the derive on the legacy
	// single-snapshot copy.
	CheckpointManifestWriter CheckpointManifestWriter

	// TreeArchive, when set, receives a §8.10 archive record for every
	// child session (a session with a parent) that reaches a terminal
	// state, so a resumed parent can replay the outcome. Nil disables
	// delegation-tree archiving.
	TreeArchive treearchive.Store

	// TaskUsage, when set, assembles the §8.8 TaskResult.usage and
	// TaskResult.treeUsage rollups stamped on every materialized result.
	// Nil leaves both absent (the pre-metering behaviour).
	// spec: §8.8.
	TaskUsage *resultrollup.Builder

	// TreeBudgetReturner, when set, releases the §12.4 delegation tree
	// budget a settled child consumed: the §8.2 maxTreeMemoryBytes
	// offload decrement and the per-parent parallel_children decrement
	// fire once per child as it reaches a terminal state. Nil disables
	// the decrement (developer mode without Redis-backed counters).
	TreeBudgetReturner TreeBudgetReturner

	// LeaseRegistrar, when set, registers each newly created root session
	// with the §8.6 lease-extension budget source (RegisterTree) so an
	// in-process budget-exhaustion extension (the gateway LLM Proxy's
	// ExtendForBudget trigger) from the root or its delegated descendants
	// resolves the tree instead of failing ErrSessionNotFound. Nil leaves
	// the tree unregistered (the in-process gateway with no GatewayControl
	// listener). LeaseExtensionDefaults supplies the §8.6 deployment-level
	// ceiling the root tree is registered with. F-15.3.5.
	LeaseRegistrar         LeaseTreeRegistrar
	LeaseExtensionDefaults LeaseExtensionDefaults

	// QuotaCheckpointer, when set, persists the §11.2 final
	// token-usage checkpoint for the session's (tenant, user) when the
	// session reaches a terminal state. Nil disables the final write
	// (developer mode without the Postgres checkpoint store).
	QuotaCheckpointer QuotaFinalCheckpointer

	// HighWatermarkReader and HighWatermarkObserver wire the §8.3 per-tree parallel-children high-watermark observation: when a
	// delegation tree's root session settles, the gateway reads the
	// recorded maximum simultaneous in-flight children and observes it
	// onto the §16.1 histogram. Both nil disables the observation.
	// F-8.9.6.
	HighWatermarkReader   DelegationHighWatermarkReader
	HighWatermarkObserver DelegationHighWatermarkObserver

	// MaxOrphanTasksPerTenant caps a tenant's active orphan tasks per
	// §8.10. A non-positive value selects DefaultMaxOrphanTasksPerTenant.
	MaxOrphanTasksPerTenant int

	// ResumeWindow is the §4.2 resume-eligibility duration
	// stamped onto each session at create. A non-positive value
	// selects DefaultResumeWindow (2 hours, mirroring the watchdog's
	// MaxSessionAgeSeconds default). Operators tuning the watchdog
	// budget should pass the matching value here so the two budgets
	// stay aligned.
	// spec: §4.2 — "Resume eligibility and window".
	ResumeWindow time.Duration

	// TreeRecoveryLevelTimeout and TreeRecoveryTreeTimeout are the §8.10
	// maxLevelRecoverySeconds / maxTreeRecoverySeconds budgets the
	// bottom-up delegation-tree recovery applies when a tree is resumed.
	// A non-positive value selects the §8.10 recovery-package default
	// (120s / 600s). spec: §8.10.
	TreeRecoveryLevelTimeout time.Duration
	TreeRecoveryTreeTimeout  time.Duration

	// TreeRecoveryMetrics records the §16.1 tree-recovery
	// telemetry. *gatewaymetrics.Metrics satisfies it. Nil drops the
	// observations.
	TreeRecoveryMetrics treerecovery.Metrics

	// Runtimes is the §5.1 runtime registry. Optional — when nil, the
	// §9.1 GET /v1/runtimes discovery endpoint returns an empty list.
	Runtimes runtimestore.Store

	// CapabilityOverrides is the §5.1 per-tenant runtime
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

	// MidSessionUploadEnabled is the §7.4 deployer policy that
	// admits mid-session uploads (POST /v1/sessions/{id}/upload-to-session)
	// when the bound runtime also declares capabilities.midSessionUpload.
	// False (the default) keeps the surface closed platform-wide. spec:
	// §7.4 — F-7.4.6.
	MidSessionUploadEnabled bool

	// MaxConcurrentUploadsPerSession is the §11.1 per-session
	// concurrent-upload admission cap: the gateway rejects a new upload
	// with 429 RATE_LIMITED once a session already holds this many
	// in-flight uploads on the replica. Zero leaves the per-session
	// concurrency scope unlimited. spec: §11.1. F-11.1.5.
	MaxConcurrentUploadsPerSession int

	// MaxConcurrentUploadsGlobal is the §11.1 global
	// concurrent-upload admission cap: the gateway rejects a new upload
	// with 429 RATE_LIMITED once the replica already holds this many
	// in-flight uploads across all sessions. Zero leaves the global
	// concurrency scope unlimited. spec: §11.1. F-11.1.5.
	MaxConcurrentUploadsGlobal int

	// MaxUploadBytesPerSession is the §11.1 per-session
	// cumulative upload-size cap: the gateway rejects an upload with 429
	// QUOTA_EXCEEDED once the sum of all uploads in a session would
	// exceed this value. The per-file (per-blob) cap is the separate
	// UploadMaxBodyBytes ceiling. Zero leaves the per-session size scope
	// unlimited. spec: §11.1. F-11.1.6.
	MaxUploadBytesPerSession int64

	// SessionLogHook, when set, receives the §4.4 close-hook
	// on every session transition to a terminal state. The production
	// wiring lives in pkg/gateway/sessionlogstore (CloseHook). Nil
	// disables the session-log persistence path; the transition still
	// fires.
	// spec: §4.4.
	SessionLogHook SessionLogHook

	// WarmupEstimateSeconds overrides the §5.2 PoolWarmingUp
	// warm-up estimate (estimatedReadyIn and the Retry-After floor's
	// input). Zero selects DefaultWarmupEstimateSeconds (120s), the
	// spec's no-historical-data fallback.
	// spec: §5.2.
	WarmupEstimateSeconds int

	// CredentialRouter is the §4.9 pluggable CredentialRouter used at
	// session creation to resolve a credential source and pool per
	// provider in the intersection of the runtime's supportedProviders
	// and the tenant's credentialPolicy. Nil selects the built-in
	// strategy-and-fallback-order router (credrouter.Default).
	// spec: §4.9.
	CredentialRouter credrouter.Router

	// PreclaimMismatch, when set, increments
	// lenny_credential_preclaim_mismatch_total{pool,provider} on the
	// §4.9 race: the pre-claim availability check passed but
	// the lease assignment failed. Nil disables the emission.
	// spec: §4.9.
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

	// SlotLeakGauge, when set, publishes the §6.2
	// lenny_adapter_leaked_slots{pod_id,pool} gauge to leaked: the count of
	// a pod's leaked concurrent-workspace slots, which remain counted in
	// active_slots until the pod terminates. Nil disables the
	// emission. spec: §6.2.
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

	// ObserveStartupDuration, when set, records the §6.3
	// end-to-end pod-warm session startup latency (pod claim through
	// agent session ready, excluding upload and workspace
	// materialization) for each successful start. Nil disables it.
	// spec: §16.1, §6.3.
	ObserveStartupDuration func(pool, runtimeClass, isolationProfile string, seconds float64)

	// ObserveStartupPhase, when set, records the §6.3 latency
	// of one hot-path startup phase (pod_claim,
	// workspace_materialization, setup_commands, credential_assignment,
	// agent_session_start). Nil disables it. spec: §6.3.
	ObserveStartupPhase func(phase, runtimeClass string, seconds float64)

	// ObserveTimeToFirstToken, when set, records the §6.3 /
	// §16.1 TTFT histogram on the first agent-streamed
	// response event of each session: session start request to first
	// streaming event emitted to the client. Nil disables the
	// emission. spec: §16.1, §6.3.
	ObserveTimeToFirstToken func(pool, runtimeClass, isolationProfile string, seconds float64)

	// RetryPolicyCaps holds the §7.3 deployer caps applied to a
	// client-supplied RetryPolicy at admission. The gateway clamps each
	// populated client field against the matching cap and falls through
	// to the cap as the effective value when the client supplied nothing.
	// A zero field skips that clamp so deployer "unlimited" semantics
	// survive. Production wires these to the watchdog config so the
	// per-session cap can never exceed the platform-wide bound. F-7.3.1.
	// spec: §7.3.
	RetryPolicyCaps session.RetryPolicyCaps

	// EnvVarBlocklist extends the §14 platform default env-var blocklist
	// with deployer-supplied entries (exact names or `*` globs). The
	// platform default is always merged in first so an operator can
	// extend but not reduce it. A nil slice leaves the platform default
	// in force. spec: §14. F-14.1.12.
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

	// IncWarmpoolWarmupFailure, when set, increments the §16.1
	// lenny_warmpool_warmup_failure_total{error_type} counter for a
	// warm-pool startup failure. error_type is the §7.3
	// non-retryable failure category the gateway classified
	// (`setup_command_failed`, etc.). Nil disables the emission.
	// spec: §16.1, §7.3 — F-7.5.9.
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
		tenancyMode:                opts.TenancyMode,
		podBinder:                  opts.PodBinder,
		podRegistry:                opts.PodRegistry,
		fencer:                     opts.CoordinationFencer,
		leaseStore:                 opts.CoordinationLeaseStore,
		replicaID:                  opts.ReplicaID,
		coordLeaseTTL:              opts.CoordinationLeaseTTL,
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
		resumeChunkResolver:        opts.ResumeChunkResolver,
		checkpointRecoveryMetrics:  opts.CheckpointRecoveryMetrics,
		checkpointManifests:        opts.CheckpointManifestReader,
		checkpointManifestWriter:   opts.CheckpointManifestWriter,
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
		// spec: §14 — the SSRF validator needs no external
		// config; default it so callbackUrl validation always runs even
		// when a deployer configured no domain allowlist.
		s.callbackValidator = sessioncallback.NewValidator(nil, nil)
	}
	if s.defaultRetention <= 0 {
		// spec: §7.1 — default the artifact-retention window to
		// 7 days when the deployer leaves it unset.
		s.defaultRetention = DefaultArtifactRetention
	}
	if s.sealMaxDuration <= 0 {
		// spec: §7.1 — maxWorkspaceSealDurationSeconds default 300s.
		s.sealMaxDuration = DefaultWorkspaceSealMaxDuration
	}
	if s.sealSleep == nil {
		s.sealSleep = sleepWithContext
	}
	if s.warmupEstimateSeconds <= 0 {
		s.warmupEstimateSeconds = DefaultWarmupEstimateSeconds
	}
	if s.credRouter == nil {
		// spec: §4.9 — the built-in strategy-and-
		// fallback-order CredentialRouter is the default.
		s.credRouter = credrouter.NewDefault()
	}
	if s.clock == nil {
		s.clock = func() time.Time { return time.Now().UTC() }
	}
	if s.coordLeaseTTL <= 0 {
		// spec: §10.1 — the at-bind lease TTL must exceed the coordination
		// sweep interval so a held lease does not lapse between renewals.
		s.coordLeaseTTL = DefaultCoordinationLeaseTTL
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
	// spec: §8.10 — the bottom-up delegation-tree
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
		// spec: §5.3 — honor the dev-mode fallback to `standard`
		// (runc) when no explicit default isolation profile is configured.
		s.defaultIsoProf = isolation.DefaultForMode(s.devMode)
	}
	// spec: §10.7 (SCL-023) — the per-tenant targeting
	// circuit breaker shares the server clock so tests drive the open /
	// half-open transitions deterministically.
	s.targetingBreaker = newTargetingBreaker(s.clock, opts.SetExperimentTargetingCircuitOpen)
	return s
}

// randomSessionID returns a fresh §12.6 UUIDv8 session identifier.
func randomSessionID() string {
	return session.NewID()
}

// Now exposes the configured clock so callers that hold a reference
// to the Server can compose with the same time source. Useful for
// tests that need to verify timestamp behaviour.
func (s *Server) Now() time.Time { return s.clock() }
