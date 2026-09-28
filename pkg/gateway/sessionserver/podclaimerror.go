// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordfence"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/upload"
)

// tokenServiceUnavailableRetryAfterSeconds is the §4.3 Retry-After
// header emitted with TOKEN_SERVICE_UNAVAILABLE: 5 seconds is the
// circuit-breaker open-state cool-down in pkg/gateway/subsystem.
// spec: §4.3.
const tokenServiceUnavailableRetryAfterSeconds = 5

// DefaultWarmupEstimateSeconds is the §5.2 fallback estimate
// for a pool's remaining warm-up time, used for the PoolWarmingUp 503's
// estimatedReadyIn detail and Retry-After header when no historical
// lenny_warmpool_pod_startup_duration_seconds p50 is available. The
// Retry-After header is max(30, estimate). Operators tune it through
// the gateway's WarmupEstimateSeconds option.
// spec: §5.2.
const DefaultWarmupEstimateSeconds = 120

// minWarmupRetryAfterSeconds is the §5.2 floor on the
// PoolWarmingUp Retry-After header: max(30, estimatedWarmupSeconds).
const minWarmupRetryAfterSeconds = 30

// writePodClaimError maps a startOnPod / resumeOnPod failure to its
// §15.1 error envelope. The §5.2 pool-warming and pod/slot exhaustion
// conditions take their spec-defined codes; a Token Service outage
// surfaces as TOKEN_SERVICE_UNAVAILABLE (§4.3). Any other failure
// falls back to a retryable 503 carrying fallbackCode + fallbackMsg
// plus a §15.1 header. Per §7.1 the
// atomic-creation paths (`POST /v1/sessions`, `POST /v1/sessions/start`)
// pass `SESSION_CREATION_FAILED` so a generic claim failure surfaces
// the spec-named code instead of the legacy `POD_CLAIM_FAILED`; the
// `POST /v1/sessions/{id}/start` two-step path passes `STARTING_FAILED`
// per §6.2.
//
// Pool exhaustion is code-split by lifecycle phase: a create-time claim
// exhaustion (wrapped as errCreateClaimExhausted by claimAtCreate)
// surfaces the fallback `SESSION_CREATION_FAILED` per the §7.1
// atomicity note and proposal §4.1, while a bare `podclaim.ErrNoIdlePod`
// from the two-step `/start` claim keeps the §5.2
// `WARM_POOL_EXHAUSTED` code.
//
// Either §4.7.1 slot-bind refusal answers the retryable fallback at every
// bind stage, ahead of every typed case, so a refusal wrapped in a setup or
// slot failure does not take a non-retryable envelope.
// spec: §5.2, §5.2
// (RUNTIME_UNAVAILABLE), §7.1,
// §15.1; §4.7.1 (role and gateway RPC contract).
func (s *Server) writePodClaimError(w http.ResponseWriter, err error, fallbackCode, fallbackMsg string) {
	var warming *podsession.PoolWarmingError
	var credAssign *podsession.CredentialAssignmentError
	var setupFail *podsession.SetupCommandFailure
	var slotFailed *podsession.SlotFailedError
	var demotionUnsupported *podsession.SDKDemotionNotSupported
	var proxyDialect *PoolProxyDialectError
	var deliveryIso *CredentialDeliveryIsolationError
	var levelUnderperforms *podsession.RuntimeLevelUnderperforms
	var archiveLimit *upload.ValidationError
	s.recordPodClaimFailure(err)
	switch {
	case adapterclient.IsSlotBindRefusal(err):
		// spec: §4.7.1 (role and gateway RPC contract); §15.1 (REST API).
		// Either refusal means another bind attempt or start of the same
		// session holds the slot on that pod, so the client's request did not
		// fail on its own terms and a fresh request binds a fresh attempt.
		// The answer is this endpoint's retryable fallback at every bind
		// stage. First in the switch: a refusal wrapped in *SetupCommandFailure
		// or *SlotFailedError would otherwise take the non-retryable
		// SETUP_COMMAND_FAILED or SLOT_FAILED envelope, and the setup case
		// would file a setup_command_failed audit row for a request that ran no
		// setup command.
		w.Header().Set("Retry-After", strconv.Itoa(sessionCreationFailedRetryAfterSeconds))
		s.writeError(w, http.StatusServiceUnavailable, fallbackCode,
			fallbackMsg+": "+err.Error(), nil)
	case errors.As(err, &deliveryIso):
		// spec: §4.9 — the session-start credential-delivery gate rejected a
		// resolved CredentialPool whose effective deliveryMode pairs with the
		// bound pod's isolationProfile/spiffeBinding in a cross-tenant-risky
		// combination. Permanent for this (pool, pod) pairing in multi-tenant
		// mode: a retry resolves the same forbidden combination, so the
		// envelope is the dedicated 422 carrying the guard's rejection code and
		// remediation message rather than the retryable atomic-unit fallback.
		s.writeError(w, http.StatusUnprocessableEntity, deliveryIso.Code,
			deliveryIso.Reason, nil)
	case errors.As(err, &levelUnderperforms):
		// spec: §5.1 — the runtime declares a higher integrationLevel
		// than the adapter handshake observed it deliver. Permanent for this
		// runtime: a retry against the same (unfixed) runtime fails
		// identically, so the envelope is the dedicated 422 rather than the
		// retryable atomic-unit fallback. F-5.1.11.
		s.writeError(w, http.StatusUnprocessableEntity, "RUNTIME_LEVEL_UNDERPERFORMS",
			levelUnderperforms.Error(),
			map[string]any{
				"runtime":       levelUnderperforms.Runtime,
				"declaredLevel": levelUnderperforms.Declared,
				"observedLevel": levelUnderperforms.Observed,
			})
	case errors.As(err, &proxyDialect):
		// spec: §4.9 — an assigned proxy-mode pool declares a
		// wire dialect the session runtime does not speak. Permanent for
		// this (runtime, pool) pairing: a retry against the same pool from
		// the same runtime fails identically, so the envelope is the
		// dedicated 422 rather than the retryable atomic-unit fallback.
		s.writeError(w, http.StatusUnprocessableEntity, "INVALID_POOL_PROXY_DIALECT",
			proxyDialect.Error(),
			map[string]any{"pool": proxyDialect.Pool, "proxyDialect": proxyDialect.Dialect})
	case errors.As(err, &demotionUnsupported):
		// spec: §6.1 — a preConnect pod whose adapter cannot
		// DemoteSDK fails the session with the dedicated permanent code
		// rather than serving it with stale SDK state. Not retryable on a
		// fresh pod from the same pool (every pod runs the same adapter).
		s.writeError(w, http.StatusUnprocessableEntity, "SDK_DEMOTION_NOT_SUPPORTED",
			"the runtime declares capabilities.preConnect but its adapter does not implement DemoteSDK; "+
				"the request includes sdkWarmBlockingPaths files that require demotion",
			map[string]any{"reason": "sdk_demotion_not_supported"})
	case errors.As(err, &setupFail):
		// spec: §7.5, §7.3, §16.1 — recordPodClaimFailure (called above)
		// recorded the setup_command_failed audit row + metric.
		s.writeSetupCommandError(w, setupFail, fallbackCode, fallbackMsg, err)
	case errors.Is(err, credassign.ErrTokenServiceUnavailable):
		s.writeTokenServiceUnavailable(w, err)
	case errors.As(err, &warming):
		s.writePoolWarming(w, warming)
	case errors.Is(err, credrouter.ErrUserCredentialNotFound):
		// spec: §4.9, §15.1 — a user-only policy with
		// no pre-registered credential for the user and provider.
		s.writeError(w, http.StatusNotFound, "USER_CREDENTIAL_NOT_FOUND",
			"no pre-registered credential found for the user and provider; "+
				"register one via POST /v1/credentials or configure pool fallback", nil)
	case errors.Is(err, credrouter.ErrNoCredentialAvailable):
		// spec: §4.9 — no provider in the intersection had an
		// assignable credential at the pre-claim check; no pod was claimed.
		s.writeCredentialPoolExhausted(w, "pre_claim")
	case errors.As(err, &credAssign):
		// spec: §4.9 — the pre-claim check passed but the lease
		// assignment failed (a credential became unavailable in the race
		// window). recordPodClaimFailure (called above) recorded the mismatch
		// so operators can tune pool sizing.
		s.writeCredentialPoolExhausted(w, "assignment_race")
	case errors.Is(err, errCreateClaimExhausted):
		// spec: §7.1 / §4.1 (proposal) — a
		// create-time claim exhaustion is part of the §7.1 atomic creation
		// unit, so it surfaces as the create-handler fallback envelope
		// (SESSION_CREATION_FAILED + Retry-After) rather than the §5.2
		// WARM_POOL_EXHAUSTED code. This case precedes the ErrNoIdlePod case
		// because errCreateClaimExhausted wraps that sentinel; the create
		// paths wrap it, the two-step `POST /v1/sessions/{id}/start` path
		// does not, so /start keeps WARM_POOL_EXHAUSTED.
		w.Header().Set("Retry-After", strconv.Itoa(sessionCreationFailedRetryAfterSeconds))
		s.writeError(w, http.StatusServiceUnavailable, fallbackCode,
			fallbackMsg+": "+err.Error(),
			map[string]any{"reason": "no_idle_pods"})
	case errors.Is(err, podclaim.ErrNoIdlePod):
		s.writeWarmPoolExhausted(w, "no_idle_pods")
	case errors.Is(err, podclaim.ErrNoConcurrentSlot), errors.Is(err, podclaim.ErrTenantMismatch):
		s.writeWarmPoolExhausted(w, "concurrent_slots_exhausted")
	case errors.As(err, &slotFailed):
		// spec: §5.2 "Client error on exhaustion" — a concurrent-workspace
		// slot failure that was non-retryable or whose single retry was
		// exhausted. The body carries error.category (the failure reason),
		// error.retryable=false (the client may resubmit as a new request),
		// and error.sessionId. It is checked after the typed setup/credential
		// cases so a SlotFailedError wrapping one of those still routes to
		// the specific handler via the unwrap chain.
		s.writeSlotFailed(w, slotFailed)
	case errors.As(err, &archiveLimit):
		// spec: §15.1 (UPLOAD_ARCHIVE_LIMIT_EXCEEDED, 413/PERMANENT) — an
		// over-limit or decompression-bomb uploadArchive raised by the §13.4
		// validator during workspace materialization (archive.Extract, wrapped
		// through Binder.Prepare's stageWorkspace) is a deterministic client
		// fault: the same non-conformant archive fails identically on retry. It
		// is the non-retryable 413 with no Retry-After, not the retryable 503
		// fallback that would loop a conforming client indefinitely. Placed
		// before the default so the typed validator error takes its dedicated
		// code; it cannot collide with the earlier typed cases because
		// extraction runs before setup commands or credential assignment. The
		// §13.4 sub-code rides on details.reason. F-CS1.
		s.writeUploadArchiveLimitExceeded(w, archiveLimit)
	default:
		// spec: §7.1 / §15.1 — the atomic-unit fallback
		// is always retryable; include Retry-After so a client backs off
		// with a deterministic budget rather than parsing the body.
		w.Header().Set("Retry-After", strconv.Itoa(sessionCreationFailedRetryAfterSeconds))
		s.writeError(w, http.StatusServiceUnavailable, fallbackCode,
			fallbackMsg+": "+err.Error(), nil)
	}
}

// recordPodClaimFailure records the audit row and metrics a pod-claim failure
// carries, independent of the response the caller writes. It records for err
// exactly what writePodClaimError's switch would record, under the same arm
// precedence: a setup-command failure files the setup_command_failed audit
// row and metric, and a lease-assignment failure counts a pre-claim mismatch.
// Every arm writePodClaimError evaluates ahead of those two records nothing,
// so a slot-bind refusal wrapped in a *SetupCommandFailure files no audit row,
// and a *CredentialAssignmentError wrapping a Token Service outage, a pool
// warming condition, or a pre-claim credential miss counts no mismatch. The
// finalize handler calls it on a prepare failure that a terminal writer
// overtook, where the response is the 409 rather than writePodClaimError's
// envelope but the failure itself still happened. Keep the arm order below
// in step with writePodClaimError's switch.
// spec: §7.5 (setup commands), §4.9 (credential leasing), §16.1 (metrics)
func (s *Server) recordPodClaimFailure(err error) {
	var deliveryIso *CredentialDeliveryIsolationError
	var levelUnderperforms *podsession.RuntimeLevelUnderperforms
	var proxyDialect *PoolProxyDialectError
	var demotionUnsupported *podsession.SDKDemotionNotSupported
	var setupFail *podsession.SetupCommandFailure
	var warming *podsession.PoolWarmingError
	var credAssign *podsession.CredentialAssignmentError
	switch {
	case adapterclient.IsSlotBindRefusal(err),
		errors.As(err, &deliveryIso),
		errors.As(err, &levelUnderperforms),
		errors.As(err, &proxyDialect),
		errors.As(err, &demotionUnsupported):
		// Answered ahead of the recording arms; nothing is recorded.
	case errors.As(err, &setupFail):
		// spec: §7.5, §7.3, §16.1 — the gateway records the
		// setup_command_failed audit row + metric on both setup-failure
		// envelopes so the §16 alert can fire and operators can correlate the
		// rejection reason with the per-command stdout/stderr trail. F-7.5.9.
		s.recordSetupCommandFailed(setupFail)
	case errors.Is(err, credassign.ErrTokenServiceUnavailable),
		errors.As(err, &warming),
		errors.Is(err, credrouter.ErrUserCredentialNotFound),
		errors.Is(err, credrouter.ErrNoCredentialAvailable):
		// Answered ahead of the mismatch arm; nothing is recorded.
	case errors.As(err, &credAssign):
		// spec: §4.9 — the pre-claim check passed but the lease assignment
		// failed (a credential became unavailable in the race window).
		if s.preclaimMismatch != nil {
			s.preclaimMismatch(credAssign.Pool, credAssign.Provider)
		}
	}
}

// writeSetupCommandError chooses the §15.1 envelope for a setup-command
// failure by inspecting the adapter-reported gRPC status of the wrapped
// cause. A deterministic non-zero exit (or hard timeout) is reported by
// the adapter as codes.FailedPrecondition (pkg/adapter/staging.go), which
// the gateway surfaces as the non-retryable 422 SETUP_COMMAND_FAILED with
// no Retry-After: per §7.3 setup_command_failed is in
// retryPolicy.nonRetryableFailures, so a retry against the same workspace
// plan fails identically. Every other code (the complement of
// FailedPrecondition: a crashed pod surfaced as Unavailable or
// DeadlineExceeded, a wrapped or non-status cause that status.Code reports
// as Unknown, and Internal/Aborted/ResourceExhausted) is a transient
// setup-window transport failure that §6.2 recovers on a fresh pod, so it
// keeps the retryable 503 fallback + Retry-After. The same
// codes.FailedPrecondition-versus-complement boundary governs the /resume
// row-state demotion in isTransientPodClaimError, so the wire envelope and
// the row state cannot disagree.
// spec: §7.3 (setup_command_failed non-retryable), §15.1 (SETUP_COMMAND_FAILED),
// §6.2 (transient setup failure retried on a fresh pod).
func (s *Server) writeSetupCommandError(
	w http.ResponseWriter, setupFail *podsession.SetupCommandFailure, fallbackCode, fallbackMsg string, err error,
) {
	if status.Code(setupFail.Cause) == codes.FailedPrecondition {
		s.writeError(w, http.StatusUnprocessableEntity, "SETUP_COMMAND_FAILED",
			fallbackMsg+": "+err.Error(),
			map[string]any{"reason": "setup_command_failed"})
		return
	}
	w.Header().Set("Retry-After", strconv.Itoa(sessionCreationFailedRetryAfterSeconds))
	s.writeError(w, http.StatusServiceUnavailable, fallbackCode,
		fallbackMsg+": "+err.Error(),
		map[string]any{"reason": "setup_command_failed"})
}

// writeUploadArchiveLimitExceeded writes the §15.1 UPLOAD_ARCHIVE_LIMIT_EXCEEDED
// envelope (413, PERMANENT, non-retryable) for an uploadArchive that violated a
// §13.4 archive-extraction ceiling (an over-limit entry, a decompression bomb,
// a path escape, or a forbidden entry kind). No Retry-After is set: the
// rejection is deterministic for the supplied archive, so a retry of the same
// bytes fails identically; the client must supply a conformant archive. The
// §13.4 sub-code (max_decompressed_size, max_entry_size, max_entry_count,
// path_escapes_root, etc.) rides on details.reason so the client and operators
// can tie the rejection to the specific ceiling without parsing the message.
// spec: §15.1 (UPLOAD_ARCHIVE_LIMIT_EXCEEDED, 413/PERMANENT), §13.4 (upload
// security validator). F-CS1.
func (s *Server) writeUploadArchiveLimitExceeded(w http.ResponseWriter, ve *upload.ValidationError) {
	s.writeError(w, http.StatusRequestEntityTooLarge, "UPLOAD_ARCHIVE_LIMIT_EXCEEDED",
		"archive extraction aborted: the upload violates a §13.4 archive-safety ceiling: "+ve.Error(),
		map[string]any{"reason": string(ve.Reason)})
}

// sessionCreationFailedRetryAfterSeconds is the default Retry-After
// budget written on §7.1 atomic-unit failures (SESSION_CREATION_FAILED,
// STARTING_FAILED). Five seconds matches the §4.3 TOKEN_SERVICE_UNAVAILABLE
// floor and is short enough that a client retry sees a freshly idle
// warm pod under the typical §5.2 fill cadence.
// spec: §7.1; §15.1.
const sessionCreationFailedRetryAfterSeconds = 5

// writeSessionCreationFailed writes the §7.1 SESSION_CREATION_FAILED
// 503 envelope used by the create paths (POST /v1/sessions, POST
// /v1/sessions/start) when the atomic unit (steps 2-8) fails outside
// the classified errors (CREDENTIAL_POOL_EXHAUSTED, WARM_POOL_EXHAUSTED,
// RUNTIME_UNAVAILABLE, TOKEN_SERVICE_UNAVAILABLE). reason is echoed
// under details.reason so an operator can distinguish
// upload_token_issuance_failed from row_persistence_failed without
// parsing the human message. The §15.1 Retry-After header is
// always included so clients back off with a deterministic budget.
// spec: §7.1; §15.1.
func (s *Server) writeSessionCreationFailed(w http.ResponseWriter, reason, message string) {
	w.Header().Set("Retry-After", strconv.Itoa(sessionCreationFailedRetryAfterSeconds))
	s.writeError(w, http.StatusServiceUnavailable, "SESSION_CREATION_FAILED",
		message, map[string]any{"reason": reason})
}

// writeCredentialPoolExhausted writes the §4.9 CREDENTIAL_POOL_EXHAUSTED
// envelope (category POLICY, HTTP 503). details.reason distinguishes
// the pre-claim miss ("pre_claim") from the assignment-race miss
// ("assignment_race"). spec: §4.9; §15.1.
func (s *Server) writeCredentialPoolExhausted(w http.ResponseWriter, reason string) {
	s.writeError(w, http.StatusServiceUnavailable, "CREDENTIAL_POOL_EXHAUSTED",
		"no provider has an assignable credential; retry once the pool frees up",
		map[string]any{"reason": reason})
}

// writeSlotFailed writes the §5.2 "Client error on exhaustion" envelope
// for a concurrent-workspace slot failure that was not (or no longer)
// retried. The body carries error.category (the §5.2 failure reason),
// error.retryable=false (the platform will not retry; the client may
// resubmit a new request), and error.sessionId naming the session whose
// slot failed. HTTP 422 is used because the failure is not transient: a non-retryable category (oom,
// workspace_validation, policy_rejection) fails identically on resubmit,
// and an exhausted retry has already consumed the §5.2 retry budget, so no
// Retry-After is offered.
// spec: §5.2 "Client error on exhaustion".
func (s *Server) writeSlotFailed(w http.ResponseWriter, e *podsession.SlotFailedError) {
	s.writeError(w, http.StatusUnprocessableEntity, "SLOT_FAILED",
		"concurrent-workspace slot failed and was not retried; resubmit as a new request",
		map[string]any{
			"category":  e.Category,
			"retryable": false,
			"sessionId": e.SessionID,
		})
}

// writeWarmPoolExhausted writes the §5.2 WARM_POOL_EXHAUSTED
// envelope. details.reason distinguishes "no_idle_pods" (the pool holds
// no pods) from "concurrent_slots_exhausted" (pods exist but every slot
// is full). The code is the same one session-mode pod exhaustion uses.
//
// The response carries a Retry-After header per the §15.2.1 catalog row, so a
// client backs off with a deterministic budget. This holds for both the
// onPoolExhausted: reject path (the immediate exhaustion) and the
// onPoolExhausted: queue path: the §4.6.1 "Pool exhaustion behavior" paragraph
// requires the queue-wait timeout to return WARM_POOL_EXHAUSTED with a
// Retry-After header.
// spec: §5.2, §15.2.1, §4.6.1 (queue-wait
// timeout carries Retry-After).
func (s *Server) writeWarmPoolExhausted(w http.ResponseWriter, reason string) {
	w.Header().Set("Retry-After", strconv.Itoa(sessionCreationFailedRetryAfterSeconds))
	s.writeError(w, http.StatusServiceUnavailable, "WARM_POOL_EXHAUSTED",
		"no warm pod or concurrent slot is available; retry with backoff",
		map[string]any{"reason": reason})
}

// writePoolWarming writes the §5.2
// response: 503 RUNTIME_UNAVAILABLE with Retry-After max(30,
// estimatedWarmupSeconds) and a details block carrying the pool name,
// the PoolWarmingUp condition, the warm-up estimate, and the count of
// pods still warming.
// spec: §5.2.
func (s *Server) writePoolWarming(w http.ResponseWriter, warming *podsession.PoolWarmingError) {
	estimate := s.warmupEstimateSeconds
	if estimate <= 0 {
		estimate = DefaultWarmupEstimateSeconds
	}
	retryAfter := estimate
	if retryAfter < minWarmupRetryAfterSeconds {
		retryAfter = minWarmupRetryAfterSeconds
	}
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	s.writeError(w, http.StatusServiceUnavailable, "RUNTIME_UNAVAILABLE",
		fmt.Sprintf("Pool '%s' is warming up — no idle pods are available yet. "+
			"Retry after the indicated interval.", warming.Pool),
		map[string]any{
			"poolName":         warming.Pool,
			"poolCondition":    "PoolWarmingUp",
			"estimatedReadyIn": estimate,
			"podsWarming":      int(warming.PodsWarming),
		})
}

// writeTokenServiceUnavailable writes the §4.3 retryable-503 envelope
// when the Token Service circuit-breaker is open or the Token Service
// is otherwise unavailable. The Retry-After header lets a client back
// off with a deterministic budget rather than parsing the body.
// spec: §4.3.
func (s *Server) writeTokenServiceUnavailable(w http.ResponseWriter, cause error) {
	w.Header().Set("Retry-After", "5")
	msg := "Token Service is unavailable; retry in a few seconds"
	if cause != nil {
		msg = msg + ": " + cause.Error()
	}
	s.writeError(w, http.StatusServiceUnavailable, "TOKEN_SERVICE_UNAVAILABLE", msg, nil)
}

// isTransientPodClaimError reports whether err is a known §5.2 / §4.9
// pool/credential exhaustion or a §4.3 Token Service outage — failures
// that the spec catalogues as retryable. The §7.3 `awaiting_client_action`
// holding state is preserved across these so the explicit client retry
// (`POST /v1/sessions/{id}/resume`) can succeed once the pool frees up.
//
// A setup-time failure is split on the same codes.FailedPrecondition
// boundary as the wire envelope (writeSetupCommandError): only the
// deterministic codes.FailedPrecondition setup-command exit (the
// non-retryable 422 SETUP_COMMAND_FAILED) demotes the row to terminal
// `failed`. Every other setup-window cause (a crashed pod surfaced as
// codes.Unavailable / codes.DeadlineExceeded, a wrapped or non-status
// cause that status.Code reports as codes.Unknown, and the remaining
// transient codes) is the retryable RESUME_FAILED envelope, so the row
// stays in `awaiting_client_action` and the explicit resume retry can
// succeed once the condition clears. Both decisions read
// status.Code(setupFail.Cause) and branch on codes.FailedPrecondition,
// so the wire retryability and the row state share one predicate and
// cannot drift. Both §4.7.1 slot-bind refusals are checked ahead of that
// predicate, in this function and in writePodClaimError alike, so a refusal
// wrapped in a *SetupCommandFailure holds the row rather than demoting it. A workspace_validation_failed or runtime-registry error
// is still non-retryable and demotes the row to failed. F-7.3.23.
//
// spec: §5.2, §4.9
// (CREDENTIAL_POOL_EXHAUSTED), §4.3,
// §5.2,
// §7.3 (awaiting_client_action holding state for a retryable resume failure),
// §6.2 (transient setup failure retried on a fresh pod).
func isTransientPodClaimError(err error) bool {
	if err == nil {
		return false
	}
	if status.Code(err) == codes.Aborted {
		// spec: §15.4 (runtime adapter specification); §5.2 (pool configuration
		// and execution modes). ABORTED is the adapter's transient wire
		// classification for a bind it refused rather than failed. It covers
		// the §5.2 reclaim-hold refusal, the identity gate's
		// SLOT_BIND_ATTEMPT_SUPERSEDED, and the start-confirmation rollback the
		// adapter answers on a failed start or resume. All three succeed on a
		// fresh attempt, so the row holds in awaiting_client_action for the
		// client's explicit resume retry rather than going terminal.
		return true
	}
	if errors.Is(err, adapterclient.ErrSlotBindAlreadyStarted) {
		// spec: §4.7.1 (role and gateway RPC contract), rule 6; §7.3 (retry and
		// resume). The refusal is a fact about this pod rather than the session,
		// and a resume retry makes a new claim.
		return true
	}
	var warming *podsession.PoolWarmingError
	var credAssign *podsession.CredentialAssignmentError
	var setupFail *podsession.SetupCommandFailure
	switch {
	case errors.As(err, &warming):
		return true
	case errors.As(err, &credAssign):
		return true
	case errors.Is(err, credassign.ErrTokenServiceUnavailable):
		return true
	case errors.Is(err, credrouter.ErrNoCredentialAvailable):
		return true
	case errors.Is(err, podclaim.ErrNoIdlePod):
		return true
	case errors.Is(err, podclaim.ErrNoConcurrentSlot), errors.Is(err, podclaim.ErrTenantMismatch):
		return true
	case errors.Is(err, coordfence.ErrRelinquished):
		// spec: §11.3 — the coordinator relinquished the session
		// after its fence retries; another replica owns it. Hold the row
		// in awaiting_client_action so the client's `POST /resume` retry
		// routes to the rightful coordinator rather than failing the row.
		return true
	case errors.As(err, &setupFail):
		// spec: §6.2 / §7.3 — only the deterministic codes.FailedPrecondition
		// setup exit demotes to failed; every other setup-window cause stays
		// resumable, the exact complement of the non-retryable wire envelope.
		return status.Code(setupFail.Cause) != codes.FailedPrecondition
	}
	return false
}

// recordSetupCommandFailed emits the §7.5 / §7.3 audit event
// and the §16.1 warm-pool warmup_failure metric for a
// setup-command-failed bind. The audit Detail carries the cmd, exit
// code, stderr excerpt, and command index pulled from the partial
// per-command outputs the adapter returned alongside the failure so
// operators can reconstruct what happened without parsing the gRPC
// error string. Best-effort: nil hooks degrade to a no-op.
//
// spec: §7.5, §7.3, §16.1 — F-7.5.9.
func (s *Server) recordSetupCommandFailed(failure *podsession.SetupCommandFailure) {
	if failure == nil {
		return
	}
	if s.incWarmpoolWarmupFailure != nil {
		s.incWarmpoolWarmupFailure("setup_command_failed")
	}
	if s.lifecycleAudit == nil {
		return
	}
	detail := setupCommandFailedDetail(failure)
	s.lifecycleAudit.EmitSessionLifecycle(context.Background(), SessionLifecycleEvent{
		EventType:    auditSessionSetupCommandFailed,
		FailureClass: "setup_command_failed",
		Detail:       detail,
		At:           s.clock(),
	})
}

// setupCommandFailedDetail formats the failure's partial per-command
// outputs into a one-line Detail string for the §11.7 audit row. The
// failing command is the last entry the adapter returned before aborting,
// so the helper reports its cmd / exit code / stderr excerpt.
// spec: §7.5 — F-7.5.9.
func setupCommandFailedDetail(failure *podsession.SetupCommandFailure) string {
	if failure == nil {
		return ""
	}
	if len(failure.Outputs) == 0 {
		if failure.Cause != nil {
			return failure.Cause.Error()
		}
		return ""
	}
	last := failure.Outputs[len(failure.Outputs)-1]
	stderr := last.GetStderr()
	if len(stderr) > 512 {
		stderr = stderr[:512] + "..."
	}
	return fmt.Sprintf("command %d (%q) exited %d: %s",
		len(failure.Outputs)-1, last.GetCmd(), last.GetExitCode(), stderr)
}
