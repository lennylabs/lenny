// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/coordfence"
	"github.com/lennylabs/lenny/pkg/gateway/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/slothealth"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/sandbox/slotstate"
	"github.com/lennylabs/lenny/pkg/upload"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// tokenServiceUnavailableRetryAfterSeconds is the §4.3 Retry-After
// header emitted with TOKEN_SERVICE_UNAVAILABLE: 5 seconds is the
// circuit-breaker open-state cool-down in pkg/gateway/subsystem.
// spec: §4.3 line 214.
const tokenServiceUnavailableRetryAfterSeconds = 5

// DefaultWarmupEstimateSeconds is the §5.2 line 625 fallback estimate
// for a pool's remaining warm-up time, used for the PoolWarmingUp 503's
// estimatedReadyIn detail and Retry-After header when no historical
// lenny_warmpool_pod_startup_duration_seconds p50 is available. The
// Retry-After header is max(30, estimate). Operators tune it through
// the gateway's WarmupEstimateSeconds option.
// spec: §5.2 line 625.
const DefaultWarmupEstimateSeconds = 120

// minWarmupRetryAfterSeconds is the §5.2 line 625 floor on the
// PoolWarmingUp Retry-After header: max(30, estimatedWarmupSeconds).
const minWarmupRetryAfterSeconds = 30

// writePodClaimError maps a startOnPod / resumeOnPod failure to its
// §15.1 error envelope. The §5.2 pool-warming and pod/slot exhaustion
// conditions take their spec-defined codes; a Token Service outage
// surfaces as TOKEN_SERVICE_UNAVAILABLE (§4.3). Any other failure
// falls back to a retryable 503 carrying fallbackCode + fallbackMsg
// plus a §15.1 line 1138 `Retry-After` header. Per §7.1 line 28 the
// atomic-creation paths (`POST /v1/sessions`, `POST /v1/sessions/start`)
// pass `SESSION_CREATION_FAILED` so a generic claim failure surfaces
// the spec-named code instead of the legacy `POD_CLAIM_FAILED`; the
// `POST /v1/sessions/{id}/start` two-step path passes `STARTING_FAILED`
// per §6.2 line 303.
//
// Pool exhaustion is code-split by lifecycle phase: a create-time claim
// exhaustion (wrapped as errCreateClaimExhausted by claimAtCreate)
// surfaces the fallback `SESSION_CREATION_FAILED` per the §7.1 line 23
// atomicity note and proposal §4.1, while a bare `podclaim.ErrNoIdlePod`
// from the two-step `/start` claim keeps the §5.2 line 519
// `WARM_POOL_EXHAUSTED` code.
// spec: §5.2 line 519 (WARM_POOL_EXHAUSTED), §5.2 lines 602-625
// (RUNTIME_UNAVAILABLE), §7.1 line 28 (SESSION_CREATION_FAILED),
// §15.1 line 1138 (Retry-After).
func (s *Server) writePodClaimError(w http.ResponseWriter, err error, fallbackCode, fallbackMsg string) {
	var warming *podsession.PoolWarmingError
	var credAssign *podsession.CredentialAssignmentError
	var setupFail *podsession.SetupCommandFailure
	var slotFailed *podsession.SlotFailedError
	var demotionUnsupported *podsession.SDKDemotionNotSupported
	var proxyDialect *PoolProxyDialectError
	var levelUnderperforms *podsession.RuntimeLevelUnderperforms
	var archiveLimit *upload.ValidationError
	switch {
	case errors.As(err, &levelUnderperforms):
		// spec: §5.1 line 42 — the runtime declares a higher integrationLevel
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
		// spec: §4.9 line 1476 — an assigned proxy-mode pool declares a
		// wire dialect the session runtime does not speak. Permanent for
		// this (runtime, pool) pairing: a retry against the same pool from
		// the same runtime fails identically, so the envelope is the
		// dedicated 422 rather than the retryable atomic-unit fallback.
		s.writeError(w, http.StatusUnprocessableEntity, "INVALID_POOL_PROXY_DIALECT",
			proxyDialect.Error(),
			map[string]any{"pool": proxyDialect.Pool, "proxyDialect": proxyDialect.Dialect})
	case errors.As(err, &demotionUnsupported):
		// spec: §6.1 line 40 — a preConnect pod whose adapter cannot
		// DemoteSDK fails the session with the dedicated permanent code
		// rather than serving it with stale SDK state. Not retryable on a
		// fresh pod from the same pool (every pod runs the same adapter).
		s.writeError(w, http.StatusUnprocessableEntity, "SDK_DEMOTION_NOT_SUPPORTED",
			"the runtime declares capabilities.preConnect but its adapter does not implement DemoteSDK; "+
				"the request includes sdkWarmBlockingPaths files that require demotion",
			map[string]any{"reason": "sdk_demotion_not_supported"})
	case errors.As(err, &setupFail):
		// spec: §7.5 line 475, §7.3 line 387, §16.1 line 124 — the
		// gateway records the setup_command_failed audit row + metric on
		// both branches so the §16 alert can fire and operators can
		// correlate the rejection reason with the per-command
		// stdout/stderr trail. F-7.5.9.
		s.recordSetupCommandFailed(setupFail)
		s.writeSetupCommandError(w, setupFail, fallbackCode, fallbackMsg, err)
	case errors.Is(err, credassign.ErrTokenServiceUnavailable):
		s.writeTokenServiceUnavailable(w, err)
	case errors.As(err, &warming):
		s.writePoolWarming(w, warming)
	case errors.Is(err, credrouter.ErrUserCredentialNotFound):
		// spec: §4.9 lines 1364, §15.1 line 993 — a user-only policy with
		// no pre-registered credential for the user and provider.
		s.writeError(w, http.StatusNotFound, "USER_CREDENTIAL_NOT_FOUND",
			"no pre-registered credential found for the user and provider; "+
				"register one via POST /v1/credentials or configure pool fallback", nil)
	case errors.Is(err, credrouter.ErrNoCredentialAvailable):
		// spec: §4.9 line 1218 — no provider in the intersection had an
		// assignable credential at the pre-claim check; no pod was claimed.
		s.writeCredentialPoolExhausted(w, "pre_claim")
	case errors.As(err, &credAssign):
		// spec: §4.9 line 1220 — the pre-claim check passed but the lease
		// assignment failed (a credential became unavailable in the race
		// window). Record the mismatch so operators can tune pool sizing.
		if s.preclaimMismatch != nil {
			s.preclaimMismatch(credAssign.Pool, credAssign.Provider)
		}
		s.writeCredentialPoolExhausted(w, "assignment_race")
	case errors.Is(err, errCreateClaimExhausted):
		// spec: §7.1 line 23 (atomicity note) / §4.1 (proposal) — a
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
		// and error.slotId. It is checked after the typed setup/credential
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
		// spec: §7.1 line 28 / §15.1 line 1138 — the atomic-unit fallback
		// is always retryable; include Retry-After so a client backs off
		// with a deterministic budget rather than parsing the body.
		w.Header().Set("Retry-After", strconv.Itoa(sessionCreationFailedRetryAfterSeconds))
		s.writeError(w, http.StatusServiceUnavailable, fallbackCode,
			fallbackMsg+": "+err.Error(), nil)
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
// spec: §7.1 line 28; §15.1 line 1138.
const sessionCreationFailedRetryAfterSeconds = 5

// writeSessionCreationFailed writes the §7.1 line 28 SESSION_CREATION_FAILED
// 503 envelope used by the create paths (POST /v1/sessions, POST
// /v1/sessions/start) when the atomic unit (steps 2-8) fails outside
// the classified errors (CREDENTIAL_POOL_EXHAUSTED, WARM_POOL_EXHAUSTED,
// RUNTIME_UNAVAILABLE, TOKEN_SERVICE_UNAVAILABLE). reason is echoed
// under details.reason so an operator can distinguish
// upload_token_issuance_failed from row_persistence_failed without
// parsing the human message. The §15.1 line 1138 Retry-After header is
// always included so clients back off with a deterministic budget.
// spec: §7.1 line 28; §15.1 line 1138.
func (s *Server) writeSessionCreationFailed(w http.ResponseWriter, reason, message string) {
	w.Header().Set("Retry-After", strconv.Itoa(sessionCreationFailedRetryAfterSeconds))
	s.writeError(w, http.StatusServiceUnavailable, "SESSION_CREATION_FAILED",
		message, map[string]any{"reason": reason})
}

// writeCredentialPoolExhausted writes the §4.9 CREDENTIAL_POOL_EXHAUSTED
// envelope (category POLICY, HTTP 503). details.reason distinguishes
// the pre-claim miss ("pre_claim") from the assignment-race miss
// ("assignment_race"). spec: §4.9 lines 1218, 1220; §15.1 line 990.
func (s *Server) writeCredentialPoolExhausted(w http.ResponseWriter, reason string) {
	s.writeError(w, http.StatusServiceUnavailable, "CREDENTIAL_POOL_EXHAUSTED",
		"no provider has an assignable credential; retry once the pool frees up",
		map[string]any{"reason": reason})
}

// writeSlotFailed writes the §5.2 "Client error on exhaustion" envelope
// for a concurrent-workspace slot failure that was not (or no longer)
// retried. The body carries error.category (the §5.2 failure reason),
// error.retryable=false (the platform will not retry; the client may
// resubmit a new request), and error.slotId. HTTP 422 is used because the
// failure is not transient: a non-retryable category (oom,
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
			"slotId":    e.SlotID,
		})
}

// writeWarmPoolExhausted writes the §5.2 line 519 WARM_POOL_EXHAUSTED
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
// spec: §5.2 line 519, §15.2.1 line 1017 (Retry-After), §4.6.1 (queue-wait
// timeout carries Retry-After).
func (s *Server) writeWarmPoolExhausted(w http.ResponseWriter, reason string) {
	w.Header().Set("Retry-After", strconv.Itoa(sessionCreationFailedRetryAfterSeconds))
	s.writeError(w, http.StatusServiceUnavailable, "WARM_POOL_EXHAUSTED",
		"no warm pod or concurrent slot is available; retry with backoff",
		map[string]any{"reason": reason})
}

// writePoolWarming writes the §5.2 lines 602-625 "Pool Not Ready"
// response: 503 RUNTIME_UNAVAILABLE with Retry-After max(30,
// estimatedWarmupSeconds) and a details block carrying the pool name,
// the PoolWarmingUp condition, the warm-up estimate, and the count of
// pods still warming.
// spec: §5.2 lines 602-625.
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
// spec: §4.3 line 214.
func (s *Server) writeTokenServiceUnavailable(w http.ResponseWriter, cause error) {
	w.Header().Set("Retry-After", "5")
	msg := "Token Service is unavailable; retry in a few seconds"
	if cause != nil {
		msg = msg + ": " + cause.Error()
	}
	s.writeError(w, http.StatusServiceUnavailable, "TOKEN_SERVICE_UNAVAILABLE", msg, nil)
}

// CreateAndStartRequest is the §15.1 POST /v1/sessions/start body —
// the convenience surface that bundles create + finalize + start.
type CreateAndStartRequest struct {
	// Inherits the same shape as CreateSessionRequest.
	RuntimeRef       string            `json:"runtimeRef"`
	UserID           string            `json:"userId,omitempty"`
	WorkspacePlan    json.RawMessage   `json:"workspacePlan,omitempty"`
	IsolationProfile isolation.Profile `json:"isolationProfile,omitempty"`
	Environment      string            `json:"environment,omitempty"`

	// Pool is the §14.1 line 311 client-requested target pool selector.
	// The combined create-and-start body is the same CreateSessionRequest
	// envelope (§14.1), so the pool pin is honored or rejected identically
	// to the two-step create path: a pin that does not exist, is not backed
	// by the runtime, or is isolation-inconsistent is rejected, and a pin
	// the tenant is not granted is forbidden. spec: §7.1 / §14.1. F-CS2.
	Pool string `json:"pool,omitempty"`

	// CallbackURL is the §15.1 line 690 optional completion-notification
	// webhook. It is validated against the §14 SSRF mitigations at
	// admission and rejected with 400 INVALID_CALLBACK_URL on failure.
	// spec: §15.1 line 690; §14 lines 108-112. F-15.1.11.
	CallbackURL string `json:"callbackUrl,omitempty"`
	// CallbackSecret is the §14 write-only HMAC signing secret for the
	// callback. spec: §14 line 139. F-15.1.11.
	CallbackSecret string `json:"callbackSecret,omitempty"`
}

// CreateAndStartResponse is the convenience reply. Mirrors the
// CreateSessionResponse plus an explicit running-state confirmation.
type CreateAndStartResponse = CreateSessionResponse

// handleCreateAndStart implements POST /v1/sessions/start per §15.1:
// the gateway runs the create → finalize → start chain in one call.
// The response is the regular CreateSessionResponse with State =
// "running" so callers receive the uploadToken + sessionIsolationLevel
// in the same envelope they would from POST /v1/sessions.
//
// Workspace plan validation, isolation profile resolution, upload
// token minting, and the role check all run as in handleCreate; the
// extra work here is just to advance the row through the §15.1
// precondition table to running before returning.
func (s *Server) handleCreateAndStart(w http.ResponseWriter, r *http.Request) {
	if !s.requireActiveUser(w, r) {
		return
	}
	tenantID := s.resolveTenant(r)
	// spec: §12.8 lines 865-873 — a tenant that has left the `active`
	// TenantState (disabling/deleting/deleted) rejects new session
	// creation before any other admission work.
	if !s.requireTenantState(w, r, tenantID) {
		return
	}
	// spec: §12.9 line 1048 — the gateway policy engine validates tenant
	// data classification before any pool/credential work, so a
	// misconfigured workspaceTier fails the create up front.
	if !s.requireTenantClassification(w, r, tenantID) {
		return
	}
	if !s.requireSessionQuota(w, r, tenantID) {
		return
	}
	if !s.requirePolicyChain(w, r, tenantID) {
		return
	}

	var req CreateAndStartRequest
	body := jsonReader(w, r)
	defer body.Close()
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	if req.RuntimeRef == "" {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "runtimeRef is required",
			map[string]any{"field": "runtimeRef"})
		return
	}

	// spec: §27.5 line 190 / §27.9 line 250 — parity with handleCreate: an
	// origin=playground caller may only create against a playground-visible
	// runtime, so the create-and-start ingress enforces the same §27.4
	// allowedRuntimes boundary. F-27.4.1.
	if !s.requirePlaygroundRuntimeVisible(w, r, req.RuntimeRef) {
		return
	}

	isoProf := req.IsolationProfile
	if isoProf == "" {
		isoProf = s.defaultIsoProf
	}
	if !isolation.IsValid(isoProf) {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"isolationProfile is not a recognised §5.3 profile",
			map[string]any{"fields": []map[string]string{{"field": "isolationProfile"}}})
		return
	}

	// spec: §7.1 step 1, §7.1 step 4, §14.1 — honor or reject the
	// client-pinned pool on the combined create-and-start path on parity
	// with the two-step create path's validateRequestEnvelope gate. Runs
	// before the claim so an unsatisfiable, unauthorized, or
	// isolation-inconsistent pin fails fast before any pod is claimed.
	// F-CS2 (0018).
	//
	// spec: §7.1 line 18 / line 75 — pass the client's raw request profile
	// (empty when omitted), not the defaulted isoProf, so a pinned pool's own
	// profile governs the session's isolation and only an explicitly-requested
	// inconsistent profile is rejected. See validateRequestEnvelope for the
	// matching two-step-path rationale. F-CS2 (0018).
	if !s.requirePoolSelectable(w, r, tenantID, req.RuntimeRef, string(req.IsolationProfile), req.Pool) {
		return
	}

	parsedPlan, planJSON, planWarnings, planOK := s.resolvePlanForCreate(w, r, req.WorkspacePlan)
	if !planOK {
		return
	}
	// spec: §7.5 line 477 / §5.1 line 76 — runtime setupCommandPolicy.maxCommands
	// cap, enforced at the create-and-start ingress for parity with the
	// two-step create path. F-7.5.5.
	if !s.enforceSetupCommandPolicy(w, r, req.RuntimeRef, parsedPlan) {
		return
	}

	// §4.8 PreRoute (below the ExperimentRouter pivot): run the PreRoute
	// interceptors with priority < 300 over the TaskSpec after
	// authentication and before runtime selection. A REJECT blocks the
	// create; a MODIFY may rewrite runtime hints (the requested runtime)
	// but not the authenticated identity, which the chain enforces. This
	// segment runs before routeExperiment so a priority 101–299 MODIFY of
	// the runtime hint affects which variant the ExperimentRouter assigns
	// (spec: §4.8 line 115).
	preRoute, ok := s.runRouteChainRange(w, r, interceptor.PhasePreRoute, routeTaskSpec{
		TenantID:         tenantID,
		UserID:           req.UserID,
		RequestedRuntime: req.RuntimeRef,
	}, math.MinInt32, ExperimentRouterPriority)
	if !ok {
		return
	}
	runtimeRef := req.RuntimeRef
	if preRoute.RequestedRuntime != "" {
		runtimeRef = preRoute.RequestedRuntime
	}

	// spec: §7.1 line 75 — resolve the pool-derived isolation level
	// once so executionMode + scrubPolicy can be persisted on the row
	// (same path as the two-step create flow). GET / List read the
	// persisted values via toResponse so the rich envelope survives a
	// coordinator handoff.
	//
	// spec: §7.1 line 18 / line 75 — when the client pins a pool and omits
	// isolationProfile, the named pool's own profile governs, so resolve the
	// level against the pool (effective requested profile empty) and persist
	// the pool-derived profile on the row so the same-call claim
	// (claimAtCreate re-resolves from row.IsolationProfile) is consistent
	// with the pin. F-CS2 (0018).
	effProf := effectiveRequestedProfile(req.IsolationProfile, isoProf, req.Pool)
	level := s.resolveIsolationLevel(r.Context(), runtimeRef, effProf, req.Pool)
	row := sessionstore.Session{
		ID:               s.idFn(),
		TenantID:         tenantID,
		UserID:           req.UserID,
		RuntimeRef:       runtimeRef,
		Environment:      req.Environment,
		State:            session.StateRunning, // skip directly to running per §15.1
		IsolationProfile: persistedRowProfile(level, isoProf),
		// spec: §14.1 line 311 — persist the client-pinned pool so the
		// same-call claim constrains resolution to it and a client that lost
		// the response can recover its requested pool. F-CS2 (0018).
		Pool:                   req.Pool,
		ExecutionMode:          level.ExecutionMode,
		ScrubPolicy:            level.ScrubPolicy,
		ConversationContinuity: level.ConversationContinuity,
		WorkspacePlan:          planJSON,
		CreatedAt:              s.clock(),
	}
	row.UpdatedAt = row.CreatedAt
	// spec: §15.1 line 690 / §14 lines 108-139 — validate the optional
	// completion-notification callbackUrl against the SSRF mitigations and
	// seal the callbackSecret before any pod side effects. F-15.1.11.
	if !s.validateCallback(w, r, req.CallbackURL, req.CallbackSecret, tenantID, &row) {
		return
	}
	// spec: §7.1 line 77 / §12.9 line 1043 — stamp the tier-keyed default
	// artifact-retention deadline at create (mirrors the plain create path)
	// so the GC can reclaim this session's artifacts; the terminal
	// transition rolls it forward.
	row.RetentionExpiresAt = row.CreatedAt.Add(s.retentionForTier(r.Context(), tenantID, req.Environment))
	// spec: §4.2 line 159 — stamp the resume-eligibility deadline so the
	// row reaching `running` carries the same per-session resume window
	// as the two-step `POST /v1/sessions` + `POST /start` path.
	row.ResumeEligibleUntil = row.CreatedAt.Add(s.resumeWindow)
	// spec: §27.3 line 63 / §27.6 lines 200-203 — apply the playground idle /
	// duration caps + origin=playground label for a /playground/*-originated
	// create-and-start, on parity with the two-step create path. F-27.3.3 /
	// F-27.6.1 / F-27.6.2 / F-27.6.8.
	s.applyPlaygroundCaps(r.Context(), runtimeRef, &row)
	// §10.7: the ExperimentRouter may enroll the session in a variant,
	// rewriting its runtime/pool before the row is persisted. It fails
	// the creation closed when the variant pool is less isolated than
	// the session's profile.
	if !s.routeExperiment(w, r, &row) {
		return
	}
	// §4.8 PreRoute (at or above the ExperimentRouter pivot): run the
	// PreRoute interceptors with priority ≥ 300 after experiment routing,
	// so an external interceptor at priority ≥ 300 orders after the
	// ExperimentRouter built-in per the §4.8 line-12 ascending-priority
	// rule. It sees the experiment-assigned runtime as the requested
	// runtime; a runtime-hint MODIFY here gets the final say on the
	// runtime before runtime selection completes, with the isolation
	// level re-resolved so executionMode/scrubPolicy stay consistent.
	afterRoute, ok := s.runRouteChainRange(w, r, interceptor.PhasePreRoute, routeTaskSpec{
		TenantID:         tenantID,
		UserID:           req.UserID,
		RequestedRuntime: row.RuntimeRef,
	}, ExperimentRouterPriority, math.MaxInt32)
	if !ok {
		return
	}
	if afterRoute.RequestedRuntime != "" && afterRoute.RequestedRuntime != row.RuntimeRef {
		row.RuntimeRef = afterRoute.RequestedRuntime
		// spec: §7.1 line 18 / line 75 — re-resolve against the effective
		// requested profile so a pinned pool's own profile still governs after
		// a runtime-hint MODIFY. F-CS2 (0018).
		afterLevel := s.resolveIsolationLevel(r.Context(), row.RuntimeRef, effProf, req.Pool)
		row.ExecutionMode = afterLevel.ExecutionMode
		row.ScrubPolicy = afterLevel.ScrubPolicy
		row.ConversationContinuity = afterLevel.ConversationContinuity
	}
	// §4.8 PostRoute: run the interceptor chain after runtime selection
	// with the resolved runtime metadata. A REJECT blocks the create; a
	// MODIFY may rewrite runtime-specific parameters but not the resolved
	// runtime or credential assignment, which the chain enforces.
	if _, ok := s.runRouteChain(w, r, interceptor.PhasePostRoute, routeTaskSpec{
		TenantID:            tenantID,
		UserID:              req.UserID,
		ResolvedRuntimeName: row.RuntimeRef,
	}); !ok {
		return
	}

	// spec: §7.1 line 28 — atomicity. Mint the §7.1 step 8 uploadToken
	// and run the pod claim BEFORE the row is persisted: a failure in any
	// step of the create-and-start atomic unit (mint, pre-claim,
	// claim, bind) returns `SESSION_CREATION_FAILED` to the client with
	// no session row left behind, matching the §7.1 "does NOT persist
	// the session row" contract. On store.Create failure the bound pod
	// is released to the §6.2 reclaim path so no pod or credential
	// lease leaks past the failure.
	// spec: §7.1 line 58 — TTL = maxCreatedStateTimeoutSeconds. F-7.4.7.
	tok, parsed, err := s.uploadIssuer.IssueDetailed(row.ID, s.uploadTokenTTL)
	if err != nil {
		s.writeSessionCreationFailed(w, "upload_token_issuance_failed",
			"upload token issuance failed: "+err.Error())
		return
	}
	row.UploadTokenDigest = parsed.Digest
	row.UploadTokenExpiry = parsed.Expiry

	// When the gateway is wired with a pod binder, the §15.1
	// create-and-start path runs the §7.1 Claim → Prepare → Launch sequence
	// in one call, reusing the same phases the decomposed create → finalize
	// → start lifecycle drives (§4.7 of the proposal). claimAtCreate runs
	// the credential pre-check and claims the pod (persisting the §4.6
	// binding on the row); startOnPod then sees that binding and runs the
	// prepare barrier plus the launch against it without re-claiming. A
	// claim, prepare, or launch failure leaves no row behind per the §7.1
	// line 28 atomicity contract; the binder reclaims the pod (and any
	// lease) on the prepare/launch failure path. A Token Service outage
	// during credential assignment surfaces as TOKEN_SERVICE_UNAVAILABLE
	// with Retry-After per §4.3 line 214.
	var (
		bound       *podsession.BindResult
		createClaim *podsession.ClaimResult
	)
	if s.podBinder != nil {
		outcome, err := s.claimAtCreate(r.Context(), row, parsedPlan)
		if err != nil {
			s.writePodClaimError(w, err, "SESSION_CREATION_FAILED",
				"could not place the session on a warm pod")
			return
		}
		level = outcome.Level
		row.ExecutionMode = level.ExecutionMode
		row.ScrubPolicy = level.ScrubPolicy
		row.ConversationContinuity = level.ConversationContinuity
		// spec: §4.1 / §5 (proposal) — carry the create-time credential
		// resolution and pod_claim duration into the same-call startOnPod so
		// the combined path runs the §7.1-step-3 pre-check exactly once before
		// the step-4 claim (no re-resolve at the prepare dispatch) and the
		// single end-to-end startup observation spans pod claim through ready.
		startCtx := &startContext{
			CredPools:         outcome.CredPools,
			UserCredProviders: outcome.UserCredProviders,
		}
		if outcome.Claim != nil {
			createClaim = outcome.Claim
			row.PodAssignment = createClaim.SandboxName
			row.PoolRef = createClaim.Pool
			startCtx.PodClaim = createClaim.PodClaim
		}
		result, err := s.startOnPod(r.Context(), row, parsedPlan, startCtx)
		if err != nil {
			// spec: §7.1 line 28 — a create-step failure rolls back without
			// persisting the row, releasing the create-time claim. The
			// combined path claims at /create and then runs prepare/launch
			// against that claim in the same call, so on a startOnPod error
			// the create-time claim is released here unless the binder already
			// owns its release (see createClaimNeedsRollback).
			if createClaimNeedsRollback(createClaim, err) {
				s.rollbackClaim(r.Context(), createClaim, row.ID)
			}
			s.writePodClaimError(w, err, "SESSION_CREATION_FAILED",
				"could not place the session on a warm pod")
			return
		}
		bound = result
		if bound != nil {
			row.PodAssignment = bound.SandboxName
			row.SetupOutput = setupOutputsFromBind(bound.SetupOutputs)
		}
	}

	if err := s.store.Create(r.Context(), row); err != nil {
		// spec: §7.1 line 28 — persistence failure after the bind must
		// roll back the claimed pod so the gateway does not leak a pod
		// or its credential lease past a "no session_id returned"
		// failure.
		s.rollbackBinding(r.Context(), bound)
		s.writeSessionCreationFailed(w, "row_persistence_failed", err.Error())
		return
	}
	s.recordSessionCreated(r.Context(), row)
	// §8.6: register the root tree's lease-extension budget so a later
	// adapter ExtendLease resolves it instead of ErrSessionNotFound. F-15.3.5.
	s.registerLeaseTree(row)
	s.registerBinding(r.Context(), bound)
	// spec: §14 lines 100, 334, 338 — publish parse-time
	// `workspace_plan_unknown_source_type` / `workspace_plan_path_collision`
	// warnings on the per-session SSE bus so Ops/audit subscribers see
	// them asynchronously, parity with the two-step create path.
	// F-14.1.17.
	s.publishParsePlanWarnings(row.TenantID, row.ID, planWarnings)

	// spec: §7.2 line 137 — the create-and-start path lands the session
	// directly in running, so emit status_change(running) for SSE
	// subscribers (e.g. a parent watching a delegated child) on parity
	// with the explicit POST /start transition.
	s.emitStatusChange(row.TenantID, row.ID, row.State)

	base := toResponse(row)
	// spec: §7.1 line 75 — the pool-resolved level was stamped onto the
	// row before persist; persistedIsolationLevel inside toResponse
	// already returns it. Override with the local copy for parity with
	// the two-step create path (covers the no-pool-resolved fallback).
	base.SessionIsolationLevel = level
	resp := CreateSessionResponse{
		SessionResponse:       base,
		UploadToken:           tok,
		WorkspacePlanWarnings: planWarnings,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// handleStart implements POST /v1/sessions/{id}/start per §15.1: the
// explicit launch transition of the two-step create → finalize → start
// lifecycle. It transitions a ready session to running and, when the gateway
// is wired with a pod binder, launches the runtime on the pod prepared at
// /finalize using the §14 WorkspacePlan stored at create.
//
// Per the proposal §4.4, /start is launch-only: the §4.3 preparation barrier
// (staging, workspace materialization, setup commands, and credential-lease
// assignment) already ran at /finalize, so /start neither claims a pod nor
// re-runs any of that work. It reconnects to the prepared pod and runs only
// the §6.3 agent_session_start phase (StartSession for a pod-warm pod,
// ConfigureWorkspace for an SDK-warm one) through launchOnPod. The exception
// is a concurrent-workspace pool, whose reserved slot materializes and
// launches together at /start (§5.2); launchOnPod owns that distinction.
//
// handleStart is a dedicated handler rather than a generic handleTransition
// because the start transition carries the extra launch step — the same
// reason handleFinalize is dedicated for the finalize transition. The launch
// runs before the row transitions: a launch failure leaves the row ready so
// the client can retry POST /start.
//
// spec: §4.4, §4.6 (proposal); §15.1 (/start precondition); §6.1 lines 30-34.
func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
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
		Endpoint:     session.EndpointStart,
		CurrentState: row.State,
	}); err != nil {
		s.writePreconditionError(w, err)
		return
	}

	if s.podBinder != nil {
		plan, err := storedWorkspacePlan(row)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"stored workspace plan could not be parsed: "+err.Error(), nil)
			return
		}
		// spec: §4.4, §4.6 (proposal) — /start is launch-only: launchOnPod
		// reconnects to the pod prepared at /finalize and runs only the §6.3
		// agent_session_start phase (StartSession or ConfigureWorkspace),
		// without re-staging, re-materializing, re-running setup, or
		// re-assigning the credential lease, and without any §4.9 credential
		// resolution. The exclusive-pool launch needs no credential inputs (the
		// lease was assigned at /finalize); only the concurrent-pool slot
		// launch, which materializes at /start, resolves them.
		result, err := s.launchOnPod(r.Context(), row, plan)
		if err != nil {
			// spec: §7.1 line 28 / §6.2 line 303 — `POST /v1/sessions/{id}/start`
			// returns `STARTING_FAILED` when the §7.1 atomic-creation unit
			// fails on the explicit launch half. The row stays `ready` so the
			// client can retry; the binder already reclaimed the prepared pod
			// (and the lease assigned at /finalize) on the launch failure path.
			s.writePodClaimError(w, err, "STARTING_FAILED", "could not place the session on a warm pod")
			return
		}
		s.registerBinding(r.Context(), result)
		// spec: §7.5 line 475 — persist the per-command setup output a
		// concurrent-workspace slot launch captured so a subsequent GET
		// /v1/sessions/{id} can surface it. The exclusive-pool launch-only path
		// produces no setup output (setup ran at /finalize, persisted there by
		// applyFinalizePrepareResult), so this fires only for the concurrent
		// slot launch that materializes at /start. F-7.5.4.
		if result != nil && len(result.SetupOutputs) > 0 {
			outs := setupOutputsFromBind(result.SetupOutputs)
			if _, uerr := s.store.Update(r.Context(), tenantID, id, func(rr *sessionstore.Session) error {
				rr.SetupOutput = outs
				return nil
			}); uerr != nil {
				// Persistence failure is non-fatal; the §7.5 trail is
				// best-effort. Log via the diagnostics path.
				s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", uerr.Error(), nil)
				return
			}
		}
	}

	updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
		transitionStart(row)
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §7.2 line 137 — surface the ready → running transition.
	s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
	s.writeSession(w, http.StatusOK, updated)
}

// storedWorkspacePlan re-parses the §14 WorkspacePlan recorded on the
// session row at create. It returns the zero Plan when the session was
// created without a plan. The plan was validated at create, so a parse
// failure here indicates gateway-version skew against the stored plan.
// ParseStored is used because the stored plan carries the
// gateway-written resolvedCommitSha that Parse rejects as client input.
func storedWorkspacePlan(row sessionstore.Session) (workspaceplan.Plan, error) {
	if len(row.WorkspacePlan) == 0 || isJSONNull(row.WorkspacePlan) {
		return workspaceplan.Plan{}, nil
	}
	plan, _, err := workspaceplan.ParseStored(row.WorkspacePlan)
	return plan, err
}

// resolvePlanForCreate parses a client-submitted §14 WorkspacePlan and,
// when a RefResolver is wired and the plan has a gitClone source, pins
// each gitClone ref to an immutable commit SHA per §14. It returns the
// parsed plan, the canonical JSON to persist on the session row (the
// pinned form when pinning occurred, the submitted bytes otherwise),
// and the consumer-advisory warnings. On a validation or
// ref-resolution failure it writes the §15.1 error response and
// returns ok=false; the caller must abort.
func (s *Server) resolvePlanForCreate(w http.ResponseWriter, r *http.Request, rawPlan json.RawMessage) (
	plan workspaceplan.Plan, storedJSON json.RawMessage, warnings []workspaceplan.Warning, ok bool,
) {
	if len(rawPlan) == 0 || isJSONNull(rawPlan) {
		return workspaceplan.Plan{}, nil, nil, true
	}
	parsed, warns, err := workspaceplan.Parse(rawPlan)
	if err != nil {
		s.writeWorkspacePlanError(w, err)
		return workspaceplan.Plan{}, nil, nil, false
	}
	storedJSON = rawPlan
	if !s.checkGitCloneAuthBindings(w, r, parsed) {
		return workspaceplan.Plan{}, nil, nil, false
	}
	if s.refResolver != nil && hasGitClone(parsed) {
		if err := workspaceplan.PinCommitSHAs(r.Context(), &parsed, s.refResolver, s.vcsCredentialFunc(r)); err != nil {
			s.writeRefResolveError(w, err)
			return workspaceplan.Plan{}, nil, nil, false
		}
		pinned, err := workspaceplan.Marshal(parsed)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"could not serialize the resolved workspace plan: "+err.Error(), nil)
			return workspaceplan.Plan{}, nil, nil, false
		}
		storedJSON = pinned
	}
	return parsed, storedJSON, warns, true
}

// hasGitClone reports whether the plan has at least one gitClone
// source — the only source type whose ref the gateway must pin.
func hasGitClone(plan workspaceplan.Plan) bool {
	for _, src := range plan.Sources {
		if src.Type == workspaceplan.TypeGitClone {
			return true
		}
	}
	return false
}

// vcsCredentialFunc returns the §14 credential materializer PinCommitSHAs
// uses to authenticate the ls-remote that pins a private gitClone source.
// It binds to the request's tenant and resolves each source through the
// wired VCS resolver, so a private repo's ref resolution uses the same
// credential the clone will (§14 line 102). A source with no auth block
// resolves to a zero credential (public). It returns nil when no resolver
// is wired, leaving public-only resolution unchanged.
func (s *Server) vcsCredentialFunc(r *http.Request) workspaceplan.VCSCredentialFunc {
	if s.vcsCreds == nil {
		return nil
	}
	tenantID := s.resolveTenant(r)
	return func(ctx context.Context, gc workspaceplan.GitClone) (workspaceplan.VCSCredential, error) {
		if gc.Auth == nil {
			return workspaceplan.VCSCredential{}, nil
		}
		c, err := s.vcsCreds.Resolve(ctx, tenantID, gc.URL, gc.Auth.LeaseScope)
		if err != nil {
			return workspaceplan.VCSCredential{}, err
		}
		return workspaceplan.VCSCredential{Username: c.Username, Token: c.Token}, nil
	}
}

// checkGitCloneAuthBindings runs the §14 gitClone auth host-to-pool
// check for every gitClone source carrying an auth block. When a
// CredentialPools store is wired, each such source's URL host must
// bind to exactly one of the tenant's VCS credential pools whose
// provider matches the leaseScope; a binding failure writes the §15.1
// GIT_CLONE_AUTH_UNSUPPORTED_HOST or GIT_CLONE_AUTH_HOST_AMBIGUOUS
// response and returns false. With no store wired the check is
// skipped, so a gateway without one is unchanged.
func (s *Server) checkGitCloneAuthBindings(w http.ResponseWriter, r *http.Request, plan workspaceplan.Plan) bool {
	if s.credPools == nil {
		return true
	}
	var pools []credentialpoolstore.CredentialPool
	loaded := false
	for i, src := range plan.Sources {
		gc, ok := src.Variant.(workspaceplan.GitClone)
		if !ok || gc.Auth == nil {
			continue
		}
		host, hostOK := workspaceplan.GitCloneHost(gc)
		provider, _, scopeOK := workspaceplan.ParseLeaseScope(gc.Auth.LeaseScope)
		if !hostOK || !scopeOK {
			// validateGitClone already guaranteed a parseable HTTPS URL
			// and a well-formed leaseScope; nothing to bind otherwise.
			continue
		}
		if !loaded {
			ps, err := s.credPools.List(r.Context(), s.resolveTenant(r), credentialpoolstore.ListFilter{})
			if err != nil {
				s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
					"could not load credential pools: "+err.Error(), nil)
				return false
			}
			pools, loaded = ps, true
		}
		if _, err := credentialpoolstore.ResolveVCSPool(pools, provider, host); err != nil {
			s.writeVCSResolveError(w, err, i)
			return false
		}
	}
	return true
}

// writeVCSResolveError maps a §14 VCS-pool binding failure to its
// §15.1 response: GIT_CLONE_AUTH_UNSUPPORTED_HOST or
// GIT_CLONE_AUTH_HOST_AMBIGUOUS, both HTTP 422.
func (s *Server) writeVCSResolveError(w http.ResponseWriter, err error, sourceIndex int) {
	var ve *credentialpoolstore.VCSResolveError
	if !errors.As(err, &ve) {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	details := map[string]any{"host": ve.Host, "sourceIndex": sourceIndex}
	if ve.Reason == credentialpoolstore.VCSHostAmbiguous {
		details["matchingPools"] = ve.MatchingPools
		s.writeError(w, http.StatusUnprocessableEntity, "GIT_CLONE_AUTH_HOST_AMBIGUOUS",
			"the gitClone URL host matches multiple VCS credential pools", details)
		return
	}
	s.writeError(w, http.StatusUnprocessableEntity, "GIT_CLONE_AUTH_UNSUPPORTED_HOST",
		"the gitClone URL host matches no VCS credential pool", details)
}

// writeRefResolveError maps a §14 gitClone ref-resolution failure to
// its §15.1 response: a transient failure is GIT_CLONE_REF_RESOLVE_TRANSIENT
// (503, retryable), a permanent one is GIT_CLONE_REF_UNRESOLVABLE (422).
func (s *Server) writeRefResolveError(w http.ResponseWriter, err error) {
	var re *workspaceplan.ResolveError
	if !errors.As(err, &re) {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	details := map[string]any{
		"url":         re.URL,
		"ref":         re.Ref,
		"sourceIndex": re.SourceIndex,
		"reason":      string(re.Reason),
	}
	if re.Reason.Transient() {
		s.writeError(w, http.StatusServiceUnavailable, "GIT_CLONE_REF_RESOLVE_TRANSIENT",
			"could not resolve a gitClone ref: "+re.Err.Error(), details)
		return
	}
	s.writeError(w, http.StatusUnprocessableEntity, "GIT_CLONE_REF_UNRESOLVABLE",
		"could not resolve a gitClone ref: "+re.Err.Error(), details)
}

// enforceSetupCommandPolicy runs the §5.1 / §7.5 setupCommandPolicy
// validation against the client-supplied workspace plan before the row is
// persisted. The §7.5 line 477 contract names maxCommands as a per-session
// cap the gateway enforces; §7.5 lines 481-488 add the allowlist /
// blocklist prefix gate. Without these, the worst-case input is Go's slice
// limit and any setup command at all, which lets a buggy or malicious
// client DoS the setup phase or run an arbitrary command in the pod. The
// cap and prefix lists come from the runtime's effective (base→derived
// merged) setupCommandPolicy. A missing policy block declares no cap and
// no list-based gate, preserving the pre-F-7.5.1 admit-everything path.
//
// On any violation the helper writes the §15.1 WORKSPACE_PLAN_INVALID
// envelope with a structured details payload and returns ok=false; the
// caller MUST abort. The §7.5 line 488 "rejection reason included in the
// session's setup output" contract is also recorded on the request's
// rejected setup-output sink (F-7.5.4 / F-7.5.11) when one is wired —
// callers that already write the error to the response carry the same
// reason string via the WORKSPACE_PLAN_INVALID envelope.
//
// spec: §5.1 line 76, §7.5 lines 477, 481-490 — F-7.5.1 / F-7.5.5.
func (s *Server) enforceSetupCommandPolicy(w http.ResponseWriter, r *http.Request,
	runtimeRef string, plan workspaceplan.Plan,
) bool {
	if s.runtimes == nil || runtimeRef == "" {
		return true
	}
	rt, err := runtimestore.Resolve(r.Context(), s.runtimes, runtimeRef)
	if err != nil {
		// A missing or unresolvable runtime is surfaced by the §7.1
		// session-creation path's own validation; enforceSetupCommandPolicy
		// stays out of that error envelope and just admits the request.
		return true
	}
	policy := rt.SetupCommandPolicy
	if policy == nil {
		return true
	}
	if policy.MaxCommands > 0 {
		if got := len(plan.SetupCommands); got > policy.MaxCommands {
			s.writeError(w, http.StatusBadRequest, "WORKSPACE_PLAN_INVALID",
				fmt.Sprintf("setupCommands count %d exceeds the runtime setupCommandPolicy.maxCommands cap %d",
					got, policy.MaxCommands),
				map[string]any{
					"field":       "setupCommands",
					"reason":      "setup_commands_max_exceeded",
					"maxCommands": policy.MaxCommands,
					"count":       got,
				})
			return false
		}
	}
	if policy.Mode == "" {
		return true
	}
	for i, c := range plan.SetupCommands {
		if policy.PermitsCommand(c.Cmd) {
			continue
		}
		// spec: §7.5 line 488 — the rejection reason carries the offending
		// command, its position, and the active mode so an operator can
		// reconcile against the runtime's setupCommandPolicy without
		// parsing the human message. The reason string is identical to the
		// audit-trail / setup-output payload (F-7.5.4 / F-7.5.11).
		s.writeError(w, http.StatusBadRequest, "WORKSPACE_PLAN_INVALID",
			fmt.Sprintf("setupCommands[%d] %q rejected by runtime setupCommandPolicy (mode=%s)",
				i, c.Cmd, policy.Mode),
			map[string]any{
				"field":   "setupCommands",
				"reason":  "setup_command_policy_violation",
				"mode":    string(policy.Mode),
				"index":   i,
				"command": c.Cmd,
			})
		return false
	}
	return true
}

// experimentContextToProto converts a session's stored §10.7
// experimentContext into the adapter-protocol message delivered in the
// StartSession manifest. It returns nil for an unenrolled session.
func experimentContextToProto(ec *sessionstore.ExperimentContext) *adapterv1.ExperimentContext {
	if ec == nil {
		return nil
	}
	return &adapterv1.ExperimentContext{
		ExperimentId: ec.ExperimentID,
		VariantId:    ec.VariantID,
		Inherited:    ec.Inherited,
	}
}

// runtimeSetupPolicy resolves the effective §5.1 setupPolicy of the
// named runtime into the adapter-protocol message the adapter uses to
// bound the setup phase. It returns nil when no runtime store is
// wired, the runtime is unresolvable, or the runtime declares no
// setupPolicy.
// runtimeManifestFields resolves the runtime definition and returns the
// §15.4 adapter-manifest fields sourced from it (§4.7): the agentInterface
// descriptor JSON-encoded (nil when the runtime declares none, so the
// manifest field is null) and minPlatformVersion. A resolve failure yields
// zero values so a missing descriptor never blocks session start — the
// gateway already enforces minPlatformVersion at registration, and
// agentInterface is informational.
func (s *Server) runtimeManifestFields(ctx context.Context, runtimeName string) (agentInterface []byte, minPlatformVersion string) {
	if s.runtimes == nil {
		return nil, ""
	}
	rt, err := runtimestore.Resolve(ctx, s.runtimes, runtimeName)
	if err != nil {
		return nil, ""
	}
	if rt.AgentInterface != nil {
		if b, err := json.Marshal(rt.AgentInterface); err == nil {
			agentInterface = b
		}
	}
	return agentInterface, rt.MinPlatformVersion
}

// setupOutputsFromBind converts the §7.5 line 475 adapter-side setup
// outputs into the sessionstore row form so the gateway can persist the
// trail. F-7.5.4 / F-7.5.11.
func setupOutputsFromBind(outs []*adapterv1.SetupCommandOutput) []sessionstore.SetupCommandOutput {
	if len(outs) == 0 {
		return nil
	}
	row := make([]sessionstore.SetupCommandOutput, 0, len(outs))
	for _, o := range outs {
		row = append(row, sessionstore.SetupCommandOutput{
			Cmd:        o.GetCmd(),
			ExitCode:   o.GetExitCode(),
			Stdout:     o.GetStdout(),
			Stderr:     o.GetStderr(),
			DurationMs: o.GetDurationMs(),
			Truncated:  o.GetTruncated(),
		})
	}
	return row
}

// DefaultSetupPolicyTimeoutSeconds is the §6.4 / §26 inferable default
// aggregate cap on the setup phase: 300 seconds. The gateway applies it
// when the runtime declares no setupPolicy block or declares one with
// timeoutSeconds == 0 so a runtime cannot pin a warm pod through an
// unbounded setup phase by omission alone. The §6.4 line 260 invariant
// (`maxFinalizingTimeoutSeconds` ≥ `setupTimeoutSeconds`) and the §26.2
// reference catalog (every reference runtime ships
// `setupPolicy.timeoutSeconds: 300`) both reflect the 300s floor. spec:
// §6.4 line 260 — F-7.5.12.
const DefaultSetupPolicyTimeoutSeconds = 300

func (s *Server) runtimeSetupPolicy(ctx context.Context, runtimeName string) *adapterv1.SetupPolicy {
	timeout := int32(DefaultSetupPolicyTimeoutSeconds)
	onTimeout := string(runtimestore.SetupTimeoutFail)
	// spec: §7.5 line 490 — shell defaults to true so a runtime that
	// declares no setupCommandPolicy keeps the legacy `/bin/sh -c` path. A
	// runtime that explicitly declares `setupCommandPolicy.shell: false`
	// flips the adapter into argv-mode. F-7.5.2.
	shell := true
	if s.runtimes != nil {
		if rt, err := runtimestore.Resolve(ctx, s.runtimes, runtimeName); err == nil {
			if rt.SetupPolicy != nil {
				if rt.SetupPolicy.TimeoutSeconds > 0 {
					timeout = int32(rt.SetupPolicy.TimeoutSeconds)
				}
				if rt.SetupPolicy.OnTimeout != "" {
					onTimeout = string(rt.SetupPolicy.OnTimeout)
				}
			}
			if rt.SetupCommandPolicy != nil {
				shell = rt.SetupCommandPolicy.Shell
			}
		}
	}
	return &adapterv1.SetupPolicy{
		TimeoutSeconds: timeout,
		OnTimeout:      onTimeout,
		Shell:          shell,
	}
}

// runtimeIntegrationLevel resolves the runtime's §5.1 author-declared
// integrationLevel for the §5.1 lines 41-44 first-assignment
// observed-vs-declared admission check. It returns the empty string (the
// §5.1 default "basic", which the binder never rejects) when the runtime
// registry is unwired, the runtime is unresolvable, or the runtime
// declares no level. spec: §5.1 lines 41-44.
func (s *Server) runtimeIntegrationLevel(ctx context.Context, runtimeName string) string {
	if s.runtimes == nil {
		return ""
	}
	rt, err := runtimestore.Resolve(ctx, s.runtimes, runtimeName)
	if err != nil {
		return ""
	}
	return string(rt.IntegrationLevel)
}

// resolveCredentialPools runs the §4.9 pre-claim credential
// availability check for a session and returns the provider→pool map the
// binder mints pool leases from plus the list of providers that resolved
// to the user source (the §4.9 Pre-Authorized Credential Flow), which the
// binder materializes into proxy-mode user leases. It computes the §4.9
// intersection of
// the session runtime's supportedProviders and the tenant's
// credentialPolicy.providerPools, builds a pool descriptor per provider
// from the credential-pool registry, and asks the CredentialRouter to
// resolve a source for each provider. The check passes when at least
// one provider has an assignable credential; on miss it returns the
// router's typed error (credrouter.ErrNoCredentialAvailable →
// CREDENTIAL_POOL_EXHAUSTED, credrouter.ErrUserCredentialNotFound →
// USER_CREDENTIAL_NOT_FOUND), surfaced by writePodClaimError before any
// pod is claimed.
//
// When the tenant configures no credentialPolicy, or the tenant /
// runtime / credential-pool registries are not all wired, the
// intersection is empty and the session assigns no upstream LLM
// credentials — preserving the pre-§4.9 behavior for deployments
// without credential pools.
//
// spec: §4.9 lines 1216-1218 (Pre-Claim check), 1326 (intersection).
func (s *Server) resolveCredentialPools(ctx context.Context, row sessionstore.Session) (map[string]string, []string, error) {
	if s.tenants == nil || s.runtimes == nil || s.credPools == nil {
		return nil, nil, nil
	}
	tenant, err := s.tenants.Get(ctx, row.TenantID)
	if err != nil {
		// The §10.2 tenant-claim extractor already gated the request; an
		// unresolvable tenant row here means no credentialPolicy applies.
		return nil, nil, nil
	}
	policy := tenant.CredentialPolicy
	if !policy.Configured() {
		return nil, nil, nil
	}
	rt, err := runtimestore.Resolve(ctx, s.runtimes, row.RuntimeRef)
	if err != nil {
		// Runtime-resolution failure is surfaced by the pool-resolution
		// path; the §4.9 layer contributes no credentials.
		return nil, nil, nil
	}
	intersection := credrouter.Intersection(rt.SupportedProviders, policy)

	allPools, err := s.credPools.List(ctx, row.TenantID, credentialpoolstore.ListFilter{})
	if err != nil {
		return nil, nil, fmt.Errorf("sessionserver: load credential pools for pre-claim: %w", err)
	}
	byName := make(map[string]credentialpoolstore.CredentialPool, len(allPools))
	for _, p := range allPools {
		byName[p.Name] = p
	}

	// spec: §14 credentialPolicy; §4.9 line 1362 — a per-session
	// credentialPolicy override narrows the tenant policy's
	// preferredSource (the gateway validated at admission that the
	// override only restricts, never expands). When the session carries
	// one, it wins over the tenant default for this session's credential
	// resolution. F-14.1.14.
	preferred := policy.PreferredSource
	if row.CredentialPolicyOverride != nil && row.CredentialPolicyOverride.PreferredSource != "" {
		preferred = credential.PreferredSource(row.CredentialPolicyOverride.PreferredSource)
	}
	in := credrouter.PreClaimInput{
		TenantID:        row.TenantID,
		UserID:          row.UserID,
		PreferredSource: preferred,
	}
	for _, provider := range intersection {
		var descs []credrouter.PoolDescriptor
		for _, poolName := range policy.PoolOrderFor(provider) {
			if p, ok := byName[poolName]; ok {
				descs = append(descs, poolDescriptor(p))
			}
		}
		userAvail := s.userCredChecker != nil &&
			s.userCredChecker(ctx, row.TenantID, row.UserID, provider)
		in.Providers = append(in.Providers, credrouter.ProviderInput{
			Provider:                provider,
			AllowedPools:            descs,
			UserCredentialAvailable: userAvail,
		})
	}

	res, err := credrouter.PreClaim(ctx, s.credRouter, in)
	if err != nil {
		return nil, nil, err
	}

	// spec: §4.9 line 1476 — the proxy-dialect admission boundary at the
	// runtime↔pool join. A proxy-mode pool declares the wire dialect its
	// lease exposes (`proxyDialect`); the agent pod's SDK can only speak a
	// dialect the runtime declares in credentialCapabilities.proxyDialect.
	// A credential pool carries no static runtime binding, so the runtime
	// and pool first meet concretely here, at the session's pre-claim
	// provider intersection. Reject the session with
	// INVALID_POOL_PROXY_DIALECT before a pod is claimed when an assigned
	// proxy-mode pool declares a dialect the session runtime does not
	// speak (a direct-mode pool declares no dialect and is skipped).
	for _, poolName := range res.PoolAssignments {
		p, ok := byName[poolName]
		if !ok || p.ProxyDialect == "" {
			continue
		}
		if !rt.CredentialCapabilities.AllowsProxyDialect(p.ProxyDialect) {
			return nil, nil, &PoolProxyDialectError{Pool: poolName, Dialect: p.ProxyDialect}
		}
	}

	// spec: §4.9 line 1476 — the same runtime↔dialect boundary applies to a
	// user-source provider. A user credential is delivered in proxy mode
	// (the secret stays gateway-side), so the agent pod's SDK must speak the
	// provider's canonical proxy dialect. Reject the session when the
	// resolved runtime does not declare it, before a pod is claimed.
	for _, provider := range res.UserProviders {
		dialect, ok := credential.UserProxyDialect(credential.Provider(provider))
		if !ok {
			// The userCredChecker only reports a provider available when it
			// has a canonical dialect, so this is unreachable; treat a
			// dialect-less provider defensively as not deliverable.
			return nil, nil, &PoolProxyDialectError{Pool: "user:" + provider, Dialect: ""}
		}
		if !rt.CredentialCapabilities.AllowsProxyDialect(string(dialect)) {
			return nil, nil, &PoolProxyDialectError{Pool: "user:" + provider, Dialect: string(dialect)}
		}
	}
	return res.PoolAssignments, res.UserProviders, nil
}

// PoolProxyDialectError is the §4.9 line 1476 runtime↔pool proxy-dialect
// mismatch surfaced at session-creation credential resolution: an
// assigned proxy-mode pool declares a wire dialect the session runtime
// does not declare in credentialCapabilities.proxyDialect.
// writePodClaimError maps it to 422 INVALID_POOL_PROXY_DIALECT carrying
// the spec-verbatim message.
type PoolProxyDialectError struct {
	Pool    string
	Dialect string
}

func (e *PoolProxyDialectError) Error() string {
	return fmt.Sprintf(
		"pool proxyDialect %s is not declared in runtime credentialCapabilities.proxyDialect",
		e.Dialect,
	)
}

// poolDescriptor maps a §4.9 credential pool to the router's pool
// descriptor. A pool is assignable when it holds at least one
// non-revoked credential; cooldown is rotation-time state and is false
// at session creation. The live active-leases-versus-maxConcurrent
// refinement (spec §4.9 line 1218) tightens HasCapacity once a lease-
// utilization reader is wired; until then a pool with a usable
// credential is treated as having capacity.
func poolDescriptor(p credentialpoolstore.CredentialPool) credrouter.PoolDescriptor {
	assignable := false
	for _, c := range p.Credentials {
		if !c.IsRevoked() {
			assignable = true
			break
		}
	}
	return credrouter.PoolDescriptor{
		PoolID:      p.Name,
		Healthy:     assignable,
		HasCapacity: assignable,
	}
}

// claimOutcome reports the result of the §7.1-step-4 pod claim run at
// session create. ClaimResult is nil for the one claimless disposition, a
// service-mode pool; it is non-nil for every other pool, carrying the
// exclusive pod claim or the concurrent-workspace slot reservation. Level
// always carries the §7.1 sessionIsolationLevel derived from the resolved
// pool so the create response reports the actual pod's profile rather than a
// pre-resolved estimate. spec: §7.1 step 4, line 75; §5.2.
type claimOutcome struct {
	// Claim is the persisted §4.6 binding (sandbox name, pool, pod IP,
	// and either the negotiated workspace root for an exclusive claim or the
	// reserved SlotID for a concurrent-workspace claim). Nil only for the
	// claimless service-mode disposition.
	Claim *podsession.ClaimResult
	// Level is the §7.1 sessionIsolationLevel for the resolved pool.
	Level SessionIsolationLevel
	// CredPools and UserCredProviders are the §7.1-step-3 pre-claim
	// credential resolution claimAtCreate already computed. The combined
	// one-call POST /v1/sessions/start path threads them into startOnPod so
	// the step-3 pre-check runs exactly once before the step-4 claim rather
	// than re-resolving at the prepare dispatch (the proposal's "pre-check
	// runs once, before the claim" placement). The two-step create → start
	// path discards them: its /start request re-resolves from the persisted
	// row because the resolution is not persisted across the create window.
	CredPools         map[string]string
	UserCredProviders []string
}

// startContext carries the create-time resolution claimAtCreate produced
// into a same-call startOnPod, so the combined one-call POST
// /v1/sessions/start path neither re-runs the §7.1-step-3 credential
// pre-check nor drops the create-time pod-claim duration from the
// end-to-end startup envelope. It is nil for the two-step POST
// /v1/sessions/{id}/start and the resume-rebuild paths, where startOnPod
// resolves credentials itself and the claim ran in a separate request.
type startContext struct {
	// CredPools and UserCredProviders are the already-resolved §4.9
	// pre-claim credential map and user-source providers; startOnPod reuses
	// them instead of calling resolveCredentialPools again.
	CredPools         map[string]string
	UserCredProviders []string
	// PodClaim is the create-time §6.3 pod_claim phase duration the same
	// call measured at claimAtCreate. prepareAndLaunch threads it into the
	// end-to-end lenny_session_startup_duration_seconds so the single
	// combined-call observation spans pod claim through ready (§6.3).
	PodClaim time.Duration
}

// claimAtCreate runs the §7.1 step-3 credential availability pre-check and
// the step-4 pod claim inside the create atomic unit, before the session
// row is persisted. It resolves the pool serving the row's runtime and
// §5.3 profile, derives the §7.1 sessionIsolationLevel from the actual
// resolved pool, runs the §4.9 pre-claim credential availability check for
// every pool class, and runs the §4.1 (proposal) claim for every
// non-service-mode pool (the exclusive Claim for a one-session-per-pod pool,
// the ClaimSlot reservation for a concurrent-workspace pool), returning the
// durable binding for the caller to persist on row.PodAssignment +
// row.PoolRef.
//
// The §7.1 step-3 credential availability pre-check runs for every pool
// class ahead of the service-mode short-circuit, so an exhausted-credential
// session is rejected at create regardless of the pool's session policy (the
// §2 fail-fast contract).
//
// A service-mode pool is claimless (§5.2): no pod is claimed and the
// returned ClaimResult is nil. A concurrent-workspace pool
// (maxConcurrentSessions>1) claims at create like every non-service-mode
// pool: its claim is a per-session slot reservation (ClaimSlot) rather than
// an exclusive whole-pod claim, so the §15.1 created-state invariant (a warm
// pod has been claimed) and the §4.6 durable binding hold uniformly. The
// returned ClaimResult carries the reserved slot's SandboxName, Pool, and
// SlotID for the caller to persist on row.PodAssignment + row.PoolRef; /start
// reconnects to the reserved slot rather than re-reserving. Pool warming,
// pool/slot exhaustion, and a credential-availability miss are returned as
// their typed errors for writePodClaimError to map (503
// SESSION_CREATION_FAILED + Retry-After, or the credential-specific
// envelopes), so the client learns of the failure before uploading and no
// row is persisted.
//
// spec: §4.1 (proposal), §7.1 steps 3-5, line 75; §4.9 lines 1216-1218; §5.2.
func (s *Server) claimAtCreate(ctx context.Context, row sessionstore.Session, plan workspaceplan.Plan) (*claimOutcome, error) {
	// spec: §7.1 / §14.1 — row.Pool carries the client-pinned pool selector.
	// validateRequestEnvelope already rejected an unsatisfiable, unauthorized,
	// or isolation-inconsistent pin before claim, so here it constrains
	// resolution to the named pool (empty leaves resolution by runtime + §5.3
	// profile). F-CS2 (0018).
	match, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.poolPolicyReader(), s.agentNamespace,
		row.RuntimeRef, string(row.IsolationProfile), row.Pool)
	if err != nil {
		return nil, err
	}
	level := isolationLevelForPool(match, row.IsolationProfile)
	// spec: §5.2 lines 602-625 — reject a create whose pool is still
	// bootstrapping with 503 RUNTIME_UNAVAILABLE + Retry-After before any
	// claim, so the client retries rather than burning a claim attempt.
	if match.PoolWarmingUp {
		return nil, &podsession.PoolWarmingError{Pool: match.Pool, PodsWarming: match.PodsWarming}
	}
	// spec: §7.1 step 3 / §4.9 lines 1216-1218 — the §7.1 step-3 credential
	// availability pre-check runs at create ahead of the step-4 claim, for
	// every pool class. It is ordered before the service-mode and
	// concurrent-workspace short-circuits below (the same ordering startOnPod
	// uses at /start), so a session that would fail at credential assignment
	// is rejected (CREDENTIAL_POOL_EXHAUSTED / USER_CREDENTIAL_NOT_FOUND)
	// before the client wastes an upload, regardless of the pool's session
	// policy. Surfacing the gate at create rather than at /start is the §2
	// fail-fast contract this proposal establishes.
	credPools, userCredProviders, err := s.resolveCredentialPools(ctx, row)
	if err != nil {
		return nil, err
	}
	// spec: §5.2 — service mode is claimless: a service-mode session is a
	// connection handle routed through the pool's Service/EndpointSlice, with
	// no SandboxClaim and no workspace materialization. No pod is claimed at
	// create; the row persists with execution_mode=service.
	if match.ExecutionMode == string(runtimestore.ExecutionModeService) {
		return &claimOutcome{Level: level, CredPools: credPools, UserCredProviders: userCredProviders}, nil
	}
	agentInterface, minPlatformVersion := s.runtimeManifestFields(ctx, row.RuntimeRef)
	// spec: §4.1, §5.2 (proposal) — a concurrent-workspace pool
	// (maxConcurrentSessions>1) claims at create like every non-service-mode
	// pool, so the §15.1 created-state invariant (a warm pod has been claimed)
	// and the §4.6 durable binding hold uniformly. The concurrent-pool claim
	// is a per-session slot reservation (ClaimSlot): it lands the session on a
	// shared pod's slot and persists SandboxName + Pool, and /start reconnects
	// to that reserved slot (BindReservedSlot) rather than re-reserving. The
	// SlotID equals the session id, so the binding is reconstructable from the
	// persisted columns. Pool/slot exhaustion at the reservation surfaces as
	// the §7.1 SESSION_CREATION_FAILED atomicity envelope before the client
	// uploads, the same fail-fast contract the exclusive claim gives.
	if match.MaxConcurrentSessions > 1 {
		slotReq := s.slotBindRequest(ctx, row, match, plan, credPools, userCredProviders, agentInterface, minPlatformVersion)
		// spec: §4.6.1 / §5.2 — a `queue` pool holds an exhausted slot
		// reservation in the per-pool FIFO and re-enters as slots free; a
		// `reject` pool returns WARM_POOL_EXHAUSTED on the first exhaustion. The
		// queued reservation holds no slot between attempts, so the §7.1
		// atomicity contract holds, exactly as the exclusive claim queue does.
		claim, err := runWithQueue(ctx, s.claimQueue, match.Pool, match.OnPoolExhausted, match.MaxQueueWaitSeconds,
			func(ctx context.Context) (*podsession.ClaimResult, error) {
				return s.podBinder.ClaimSlot(ctx, slotReq)
			})
		if err != nil {
			// spec: §7.1 line 23 (atomicity note) / §4.1 (proposal) — slot
			// exhaustion at the create-time reservation surfaces as the §7.1
			// SESSION_CREATION_FAILED envelope, not the §5.2 WARM_POOL_EXHAUSTED
			// code the two-step start path returns. Translate the exhaustion
			// sentinels (no concurrent slot, tenant mismatch, no idle pod) to
			// errCreateClaimExhausted so writePodClaimError routes them to the
			// create-handler SESSION_CREATION_FAILED fallback.
			if errors.Is(err, podclaim.ErrNoConcurrentSlot) ||
				errors.Is(err, podclaim.ErrTenantMismatch) ||
				errors.Is(err, podclaim.ErrNoIdlePod) {
				return nil, fmt.Errorf("%w: %w", errCreateClaimExhausted, err)
			}
			return nil, err
		}
		// spec: §6.3 — record the pod_claim phase timing at /create, its new
		// boundary in the decomposed lifecycle, for the concurrent path too.
		s.recordStartupPhases(match, podsession.BindTimings{PodClaim: claim.PodClaim})
		return &claimOutcome{Claim: claim, Level: level, CredPools: credPools, UserCredProviders: userCredProviders}, nil
	}
	// The Claim phase only needs the pool, session, and tenant; the plan and
	// credential inputs are consumed by Prepare/Launch at /start. Build the
	// full request so the create-time claim and the start-time prepare/launch
	// read identical inputs.
	bindReq := s.exclusiveBindRequest(ctx, row, match, plan, nil, nil, agentInterface, minPlatformVersion)
	// spec: §4.6.1 / §5.2 — a `queue` pool holds an exhausted claim in the
	// per-pool FIFO and re-enters as pods free; a `reject` pool returns
	// WARM_POOL_EXHAUSTED on the first exhaustion. The queued request holds
	// no pod between attempts, so the §7.1 atomicity contract holds.
	claim, err := runWithQueue(ctx, s.claimQueue, match.Pool, match.OnPoolExhausted, match.MaxQueueWaitSeconds,
		func(ctx context.Context) (*podsession.ClaimResult, error) {
			return s.podBinder.Claim(ctx, bindReq)
		})
	if err != nil {
		// spec: §7.1 line 23 (atomicity note) / §4.1 (proposal) — pool
		// exhaustion at the step-4 create-time claim surfaces as the §7.1
		// SESSION_CREATION_FAILED atomicity envelope, not the §5.2
		// WARM_POOL_EXHAUSTED code the two-step `POST /v1/sessions/{id}/start`
		// path returns. Translate the exhaustion sentinel to errCreateClaimExhausted
		// so writePodClaimError routes it to the create-handler fallback
		// (SESSION_CREATION_FAILED) while pool-warming and credential misses
		// keep their dedicated codes.
		if errors.Is(err, podclaim.ErrNoIdlePod) {
			return nil, fmt.Errorf("%w: %w", errCreateClaimExhausted, err)
		}
		return nil, err
	}
	// spec: §6.3 — record only the pod_claim phase timing at /create, its
	// new boundary in the decomposed lifecycle (§5 of the proposal). The
	// end-to-end lenny_session_startup_duration_seconds is emitted once at
	// the launch boundary (recordStartupDuration), not here, so a single
	// logical start does not double-count the envelope.
	s.recordStartupPhases(match, podsession.BindTimings{PodClaim: claim.PodClaim})
	return &claimOutcome{Claim: claim, Level: level, CredPools: credPools, UserCredProviders: userCredProviders}, nil
}

// errCreateClaimExhausted marks a create-time pod-claim exhaustion so
// writePodClaimError routes it to the §7.1 SESSION_CREATION_FAILED
// atomicity envelope (proposal §4.1, atomicity note at §7.1 line 23)
// rather than the §5.2 WARM_POOL_EXHAUSTED code the two-step start path
// uses. The wrapped podclaim.ErrNoIdlePod stays inspectable for any
// caller that branches on the underlying sentinel.
var errCreateClaimExhausted = errors.New("create-time warm-pod claim exhausted")

// startOnPod places a started session on a Kubernetes warm pod. It
// resolves the warm pool serving the session's runtime and §5.3
// isolation profile, then dispatches by the pool's sessionPolicy and by
// whether a pod was already claimed at /create:
//
//   - An exclusive pool (maxConcurrentSessions=1) whose pre-running row
//     carries a live §4.6 durable binding (PodAssignment set by the
//     create-time claim, the session not in a recovery state) reconnects
//     to the bound pod and runs the §4.3 prepare barrier plus the §4.4
//     launch (prepareAndLaunch); it does not re-claim.
//   - An exclusive pool with no live create-time claim runs the whole
//     Claim → Prepare → Launch sequence through podBinder.Bind. This is
//     the §7.3 snapshotless resume-rebuild path (resumeOnPod invokes
//     startOnPod on a recovery-state row that still carries the dead pod's
//     stale PodAssignment).
//   - A concurrent-workspace pool (maxConcurrentSessions>1) whose row
//     carries a live §4.6 binding reconnects to the slot reserved at
//     create through podBinder.BindReservedSlot (§5.2), the slot analog of
//     prepareAndLaunch; it does not re-reserve. A recovery-state row with a
//     stale binding re-reserves a fresh slot through podBinder.BindSlot.
//
// The pod's §4.7 adapter runs the per-mode assignment sequence; the
// BindResult is returned for the caller to register and persist after its
// own atomicity gates pass.
//
// startOnPod no longer registers the binding or persists the
// SandboxName field itself: the §7.1 line 28 atomicity contract
// requires the gateway to skip the persist write entirely when an
// earlier step in steps 2-8 fails, so the caller decides when to
// publish the binding. The session-mode and concurrent-slot paths
// both surface their result the same way; the caller distinguishes
// them by inspecting BindResult.SlotID when it has to roll back.
//
// claimed is the create-time resolution from a same-call claimAtCreate,
// passed only on the combined one-call POST /v1/sessions/start path. When
// it is non-nil, startOnPod reuses its §7.1-step-3 credential resolution
// rather than calling resolveCredentialPools again (so the combined path
// runs the pre-check exactly once before the step-4 claim, per the
// proposal's "pre-check runs once, before the claim" placement) and threads
// its create-time pod_claim duration into the end-to-end startup envelope.
// It is nil on the resume-rebuild path, where startOnPod resolves credentials
// itself because the claim ran in a separate request.
//
// The two-step `POST /v1/sessions/{id}/start` exclusive-pool launch does NOT
// flow through startOnPod: per the proposal §4.4, /start is launch-only and
// does no credential work, so handleStart calls launchOnPod, which resolves
// the pool and dispatches to launchPrepared without running the §4.9
// pre-claim credential resolution. The credential lease was assigned at
// /finalize. A concurrent-pool two-step /start still flows through
// launchOnPod, which resolves credentials only on the BindReservedSlot
// reconnect (a concurrent pool materializes and launches together at /start
// per §5.2, so it needs the assignment inputs there).
//
// startOnPod, after this split, resolves credentials because both its
// remaining callers consume them: the combined create-and-start path runs
// the prepare barrier (which assigns the lease), and the resume-rebuild path
// runs the whole Claim → Prepare → Launch sequence.
// spec: §4.1, §4.4, §5 (proposal); §4.2 line 160 — "Pod-to-session binding";
// §7.1 line 28 — atomic-creation rollback.
func (s *Server) startOnPod(ctx context.Context, row sessionstore.Session, plan workspaceplan.Plan, claimed *startContext) (*podsession.BindResult, error) {
	// spec: §4.9 lines 1216-1218 — run the pre-claim credential
	// availability check and resolve the per-provider pool map BEFORE a
	// pod is claimed, so a session that would fail at credential
	// assignment is rejected without wasting a warm pod. On the combined
	// one-call path the same-call claimAtCreate already ran this step-3
	// pre-check, so reuse its resolution rather than re-running it (proposal
	// §4.1: the pre-check runs once, before the claim).
	var (
		credPools         map[string]string
		userCredProviders []string
	)
	if claimed != nil {
		credPools, userCredProviders = claimed.CredPools, claimed.UserCredProviders
	} else {
		var err error
		credPools, userCredProviders, err = s.resolveCredentialPools(ctx, row)
		if err != nil {
			return nil, err
		}
	}
	// spec: §7.1 / §14.1 — constrain resolution to the client-pinned pool
	// (row.Pool); empty resolves by runtime + §5.3 profile. F-CS2 (0018).
	match, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.poolPolicyReader(), s.agentNamespace,
		row.RuntimeRef, string(row.IsolationProfile), row.Pool)
	if err != nil {
		return nil, err
	}
	// spec: §5.2 lines 602-625 — a session targeting a pool in the
	// PoolWarmingUp bootstrap state returns 503 RUNTIME_UNAVAILABLE
	// before a claim is attempted, so the client receives a retry hint
	// rather than burning a claim attempt that would surface as a less
	// informative SESSION_CREATION_FAILED.
	if match.PoolWarmingUp {
		return nil, &podsession.PoolWarmingError{Pool: match.Pool, PodsWarming: match.PodsWarming}
	}
	// spec: §5.2 — service mode is claimless: there is no workspace
	// materialization and no SandboxClaim. A service-mode session is a
	// connection handle; the gateway routes each message through the
	// pool's Kubernetes Service / EndpointSlice with tenant-affinity
	// routing (statelessproxy + tenantaffinity), pinning each pod to one
	// tenant. The start path therefore returns a nil BindResult without
	// touching the claim or slot acquisition paths, so no Sandbox is
	// claimed and no pod is bound to the session. The session row persists
	// with execution_mode=service; message routing reads that mode and
	// dispatches to the stateless data plane rather than a bound pod.
	if match.ExecutionMode == string(runtimestore.ExecutionModeService) {
		return nil, nil
	}
	agentInterface, minPlatformVersion := s.runtimeManifestFields(ctx, row.RuntimeRef)
	// spec: §5.2 — a session-mode pool with maxConcurrentSessions > 1 routes
	// the bind through the slot-claim path; the one-session-per-pod default
	// uses the session-claim path.
	if match.MaxConcurrentSessions > 1 {
		slotReq := s.slotBindRequest(ctx, row, match, plan, credPools, userCredProviders, agentInterface, minPlatformVersion)
		return s.bindConcurrentSlot(ctx, row, match, slotReq)
	}
	bindReq := s.exclusiveBindRequest(ctx, row, match, plan, credPools, userCredProviders, agentInterface, minPlatformVersion)
	// spec: §4.7, §7.1 steps 4-13 (proposal) — the combined one-call
	// `POST /v1/sessions/start` path (claimed is non-nil) claimed the pod in
	// the same call but never went through /finalize, so it reconnects to the
	// bound pod and runs the §4.3 prepare barrier plus the §4.4 launch together
	// (prepareAndLaunch) without re-claiming. The two-step
	// `POST /v1/sessions/{id}/start` launch-only path does not reach startOnPod
	// (handleStart calls launchOnPod), so a live create-time binding with
	// claimed == nil here is only the resume-rebuild path, which falls through
	// to the whole-sequence Bind below.
	//
	// The reconnect is gated on a live create-time claim, signalled by a
	// non-empty PodAssignment on a non-recovery row, rather than by a non-empty
	// PodAssignment alone. A session that lost its pod and entered a recovery
	// state (resume_pending / resuming / awaiting_client_action) retains the
	// dead pod's name in PodAssignment (failure.go), and no resume or
	// tree-recovery path clears it. The §7.3 snapshotless resume-rebuild path
	// (resumeOnPod → startOnPod with a recovery-state row) must claim a fresh
	// pod through the whole Claim → Prepare → Launch sequence below rather than
	// reconnect to the dead binding; reconnecting would fail Prepare against the
	// no-longer-existing pod and fail the resume instead of recovering it.
	if claimed != nil && row.PodAssignment != "" && !session.IsRecovery(row.State) {
		// spec: §5 / §6.3 line 348 (proposal) — the same call measured the
		// create-time pod_claim duration; thread it into the launch-boundary
		// end-to-end envelope so the single
		// lenny_session_startup_duration_seconds observation spans pod claim
		// through ready.
		return s.prepareAndLaunch(ctx, row, match, bindReq, claimed.PodClaim)
	}
	// spec: §4.6.1 / §5.2 — on a `queue` pool, hold an exhausted session-claim
	// acquisition in the per-pool claim FIFO and re-enter the claim path as
	// pods free; a `reject` pool returns WARM_POOL_EXHAUSTED on the first
	// exhaustion (ErrNoIdlePod after both the claim-path timeout and the
	// Postgres fallback). A queued request holds no pod or claim between
	// attempts, so the §7.1 atomicity contract is preserved.
	result, err := runWithQueue(ctx, s.claimQueue, match.Pool, match.OnPoolExhausted, match.MaxQueueWaitSeconds,
		func(ctx context.Context) (*podsession.BindResult, error) {
			return s.podBinder.Bind(ctx, bindReq)
		})
	if err != nil {
		return nil, err
	}
	// The whole-sequence Bind (resume-rebuild) claims and launches in one
	// call, so result.Timings carries every §6.3 phase including PodClaim;
	// record the phases and the end-to-end envelope once at this boundary.
	s.recordStartupPhases(match, result.Timings)
	s.recordStartupDuration(match, result.Timings)
	return result, nil
}

// launchOnPod runs the §4.4 launch-only path for the two-step
// `POST /v1/sessions/{id}/start` against the pod prepared at /finalize. Per
// the proposal §4.4, /start performs only the §6.3 agent_session_start phase:
// it transitions ready → starting (the caller writes the row), reconnects to
// the prepared pod from the row's persisted §4.6 binding, invokes
// StartSession (pod-warm) or ConfigureWorkspace (SDK-warm), and lets the
// caller transition the row to running. The heavy preparation — staging,
// materialization, setup commands, and credential-lease assignment — already
// ran at /finalize, so launchOnPod runs NO §4.9 pre-claim credential
// resolution: the launch RPCs consume no credential inputs (the lease was
// pushed to the pod at finalize), so resolving them here would be wasted work
// the proposal moves off the /start path.
//
// It returns (nil, nil) for the dispositions that launch nothing at /start:
//
//   - the binder is not wired (the minimal gateway, which starts by a plain
//     ready → running transition);
//   - a service-mode pool, which is claimless and is routed through the
//     pool's Service/EndpointSlice rather than a bound pod (§5.2).
//
// A concurrent-workspace pool (maxConcurrentSessions>1) reserves a slot at
// /create and materializes + launches it together at /start via
// BindReservedSlot (§5.2), so its launch DOES need the §4.9 assignment
// inputs; launchOnPod resolves credentials on that branch alone. The
// exclusive launch-only path needs none.
//
// spec: §4.4, §4.6 (proposal); §6.1 lines 30-34 (SDK-warm ConfigureWorkspace);
// §15.1 (/start precondition); §5.2; §6.3 line 372.
func (s *Server) launchOnPod(ctx context.Context, row sessionstore.Session, plan workspaceplan.Plan) (*podsession.BindResult, error) {
	// spec: §7.1 / §14.1 — constrain resolution to the client-pinned pool
	// (row.Pool); empty resolves by runtime + §5.3 profile. F-CS2 (0018).
	match, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.poolPolicyReader(), s.agentNamespace,
		row.RuntimeRef, string(row.IsolationProfile), row.Pool)
	if err != nil {
		return nil, err
	}
	// spec: §5.2 lines 602-625 — a session targeting a pool in the
	// PoolWarmingUp bootstrap state returns 503 RUNTIME_UNAVAILABLE rather than
	// reconnecting to a pod that may not be ready. A two-step /start should not
	// hit this in practice (the pod was claimed at /create and prepared at
	// /finalize), but the gate is kept fail-closed against a pool that warmed
	// down across the window.
	if match.PoolWarmingUp {
		return nil, &podsession.PoolWarmingError{Pool: match.Pool, PodsWarming: match.PodsWarming}
	}
	// spec: §5.2 — service mode is claimless: no SandboxClaim, no workspace,
	// and no bound pod. The session row persists with execution_mode=service
	// and message routing dispatches through the stateless data plane, so
	// /start returns a nil BindResult with no launch RPC.
	if match.ExecutionMode == string(runtimestore.ExecutionModeService) {
		return nil, nil
	}
	agentInterface, minPlatformVersion := s.runtimeManifestFields(ctx, row.RuntimeRef)
	// spec: §4.1, §5.2 (proposal) — a concurrent-workspace pool reserved a slot
	// at /create and materializes + launches it together at /start. That launch
	// runs the §4.9 AssignCredentials, so it needs the per-provider pool map;
	// resolve it here, on this branch alone. A live create-time reservation
	// (non-empty PodAssignment on a non-recovery row) reconnects to the reserved
	// slot via BindReservedSlot; a row without one is a misuse (a two-step
	// /start with no claimed pod), surfaced as the queue's exhaustion path.
	if match.MaxConcurrentSessions > 1 {
		credPools, userCredProviders, cerr := s.resolveCredentialPools(ctx, row)
		if cerr != nil {
			return nil, cerr
		}
		slotReq := s.slotBindRequest(ctx, row, match, plan, credPools, userCredProviders, agentInterface, minPlatformVersion)
		return s.bindConcurrentSlot(ctx, row, match, slotReq)
	}
	// spec: §4.4, §4.6 (proposal) — the exclusive-pool launch-only path. The
	// pod was claimed at /create and prepared at /finalize, so reconnect to the
	// bound pod and run only the launch (StartSession or ConfigureWorkspace).
	// No credential inputs are needed: the lease was assigned at /finalize.
	// launchPrepared recomputes the §6.1 SDK-warm demotion decision from the
	// persisted plan rather than re-running the prepare phase to learn it.
	bindReq := s.exclusiveBindRequest(ctx, row, match, plan, nil, nil, agentInterface, minPlatformVersion)
	return s.launchPrepared(ctx, row, match, bindReq)
}

// exclusiveBindRequest assembles the §4.7 BindRequest for an exclusive
// (one-session-per-pod) session-mode bind from the persisted row, the
// resolved pool, and the §4.9 pre-claim credential resolution. The same
// request drives the whole-sequence Bind (resume-rebuild) and the
// decomposed Claim / Prepare / Launch phases (the create → finalize →
// start lifecycle), so the create-time claim and the start-time
// prepare/launch read identical runtime, plan, and credential inputs.
func (s *Server) exclusiveBindRequest(ctx context.Context, row sessionstore.Session, match podsession.PoolMatch, plan workspaceplan.Plan, credPools map[string]string, userCredProviders []string, agentInterface []byte, minPlatformVersion string) podsession.BindRequest {
	preConnect, sdkWarmBlockingPaths := s.runtimeSDKWarm(ctx, row.TenantID, row.RuntimeRef)
	return podsession.BindRequest{
		Pool:                     match.Pool,
		SessionID:                row.ID,
		TenantID:                 row.TenantID,
		Runtime:                  row.RuntimeRef,
		DeclaredIntegrationLevel: s.runtimeIntegrationLevel(ctx, row.RuntimeRef),
		Plan:                     podsession.WorkspacePlanToProto(plan),
		ExperimentContext:        experimentContextToProto(row.ExperimentContext),
		TracingContext:           row.TracingContext,
		SetupPolicy:              s.runtimeSetupPolicy(ctx, row.RuntimeRef),
		CredentialPools:          credPools,
		UserID:                   row.UserID,
		UserCredentialProviders:  userCredProviders,
		AgentInterface:           agentInterface,
		MinPlatformVersion:       minPlatformVersion,
		PreConnect:               preConnect,
		SDKWarmBlockingPaths:     sdkWarmBlockingPaths,
		// spec: §3.4 — carry the pool's recycle.enabled flag so Release applies
		// the §3.4 recycle disposition (patch claim bound → recycling, signal
		// the whole-pod scrub) on a clean release rather than draining the pod.
		Recycle: match.Recycle,
	}
}

// slotBindRequest builds the §5.2 SlotBindRequest for a concurrent-workspace
// pool (maxConcurrentSessions > 1) from the session row and resolved pool,
// so the create-time ClaimSlot reservation and the start-time slot
// materialize-and-launch read identical inputs (the same pattern
// exclusiveBindRequest follows for the session-mode path).
func (s *Server) slotBindRequest(ctx context.Context, row sessionstore.Session, match podsession.PoolMatch, plan workspaceplan.Plan, credPools map[string]string, userCredProviders []string, agentInterface []byte, minPlatformVersion string) podsession.SlotBindRequest {
	return podsession.SlotBindRequest{
		Pool:                    match.Pool,
		SessionID:               row.ID,
		TenantID:                row.TenantID,
		Runtime:                 row.RuntimeRef,
		MaxConcurrentSessions:   match.MaxConcurrentSessions,
		MaxPodUptimeSeconds:     match.MaxPodUptimeSeconds,
		Plan:                    podsession.WorkspacePlanToProto(plan),
		ExperimentContext:       experimentContextToProto(row.ExperimentContext),
		TracingContext:          row.TracingContext,
		SetupPolicy:             s.runtimeSetupPolicy(ctx, row.RuntimeRef),
		CredentialPools:         credPools,
		UserID:                  row.UserID,
		UserCredentialProviders: userCredProviders,
		AgentInterface:          agentInterface,
		MinPlatformVersion:      minPlatformVersion,
		// spec: §3.4 / §6.30 — carry the pool's recycle.enabled flag so the
		// last-slot-drain edge of a recycling concurrent pool patches the
		// claim bound → recycling and signals the whole-pod scrub (the §3.1
		// "Concurrent" preset) rather than deleting the claim outright.
		Recycle: match.Recycle,
	}
}

// bindConcurrentSlot dispatches a §5.2 concurrent-workspace pool bind once the
// caller has built the slotReq. It is shared by the combined create-and-start
// path (startOnPod) and the two-step launch path (launchOnPod), which build
// the same request and dispatch identically: a concurrent pool materializes
// and launches the slot's workspace together at /start (it is not decomposed
// across finalize and start like the exclusive pool), so both callers reach
// here at launch time.
//
//   - When the row carries a slot reserved at /create (the §4.6 durable
//     binding: a non-empty PodAssignment on a non-recovery row, SlotID ==
//     SessionID == the reserved pod) the slot is not re-reserved. The gateway
//     reconnects to the reserved slot's pod and runs the materialize-and-launch
//     sequence against it (BindReservedSlot), the slot analog of the exclusive
//     reconnect.
//   - Otherwise (the §7.3 resume-rebuild path with a stale dead-pod
//     PodAssignment, or a slotless row) it reserves a fresh slot through the
//     §5.2 retry policy, holding an exhausted acquisition in the per-pool claim
//     FIFO on a `queue` pool and returning WARM_POOL_EXHAUSTED on the first
//     exhaustion of a `reject` pool. The slot retry policy surfaces the §5.2
//     exhaustion sentinels unwrapped, which is the queue's retry signal.
//
// spec: §4.1, §5.2 (proposal); §4.6.1 (pool exhaustion queue); §4.6 (durable
// binding reconnect).
func (s *Server) bindConcurrentSlot(ctx context.Context, row sessionstore.Session, match podsession.PoolMatch, slotReq podsession.SlotBindRequest) (*podsession.BindResult, error) {
	if row.PodAssignment != "" && !session.IsRecovery(row.State) {
		return s.podBinder.BindReservedSlot(ctx, slotReq, row.PodAssignment, row.ID)
	}
	return runWithQueue(ctx, s.claimQueue, match.Pool, match.OnPoolExhausted, match.MaxQueueWaitSeconds,
		func(ctx context.Context) (*podsession.BindResult, error) {
			return s.bindSlotWithRetry(ctx, slotReq)
		})
}

// prepareAndLaunch runs the §4.3 prepare barrier and the §4.4 launch
// against the pod a session claimed at /create, reconnecting from the
// row's persisted §4.6 binding (PodAssignment + PoolRef) rather than
// re-claiming. It reassembles the monolithic BindResult the caller
// registers and persists, mirroring Binder.Bind's claim → prepare →
// launch composition without re-running the claim. A prepare or launch
// failure is surfaced for writePodClaimError to map; the binder already
// reclaimed the bound pod (and any lease) on the failure path.
//
// createPodClaim is the create-time §6.3 pod_claim phase duration the
// same call measured at claimAtCreate, or zero when the claim ran in a
// separate request (the two-step `POST /v1/sessions/{id}/start` path).
// It is threaded into the end-to-end lenny_session_startup_duration_seconds
// so a single combined-call observation includes the pod-claim component
// per §6.3 line 348, while the per-phase pod_claim histogram is still
// emitted exactly once (at /create, not re-emitted here).
// spec: §4.3, §4.4, §4.6, §5 (proposal); §6.3 line 348; §7.1 steps 11-13.
func (s *Server) prepareAndLaunch(ctx context.Context, row sessionstore.Session, match podsession.PoolMatch, bindReq podsession.BindRequest, createPodClaim time.Duration) (*podsession.BindResult, error) {
	bindReq.SandboxName = row.PodAssignment
	if row.PoolRef != "" {
		bindReq.Pool = row.PoolRef
	}
	prep, err := s.podBinder.Prepare(ctx, bindReq)
	if err != nil {
		return nil, err
	}
	bindReq.Demoted = prep.Demoted
	result, err := s.podBinder.Launch(ctx, bindReq)
	if err != nil {
		return nil, err
	}
	// Reassemble the BindResult the whole-sequence Bind would have returned:
	// the live adapter and launch timing come from Launch, the prepared
	// workspace and setup trail from Prepare. Launch never claims, so its
	// PodClaim is zero; the create-time claim duration is carried separately
	// (createPodClaim) and attributed to the end-to-end envelope below rather
	// than to the per-phase pod_claim histogram, which was recorded once at
	// /create.
	result.Timings.WorkspaceMaterialization = prep.Timings.WorkspaceMaterialization
	result.Timings.SetupCommands = prep.Timings.SetupCommands
	result.Timings.CredentialAssignment = prep.Timings.CredentialAssignment
	result.WorkspacePlanWarnings = prep.WorkspacePlanWarnings
	result.SetupOutputs = prep.SetupOutputs
	result.WorkspaceRoot = prep.WorkspaceRoot
	// spec: §6.3 / §5 (0007 proposal) — record the prepare/launch phases
	// measured here (workspace_materialization, setup_commands,
	// credential_assignment, agent_session_start). result.Timings.PodClaim is
	// zero from Launch, so recordStartupPhases skips it rather than re-emitting
	// a spurious 0s pod_claim sample; the per-phase pod_claim histogram was
	// recorded once at /create.
	s.recordStartupPhases(match, result.Timings)
	// spec: §6.3 line 348 / §5 (proposal) — the end-to-end
	// lenny_session_startup_duration_seconds spans pod claim through ready, so
	// attribute the create-time pod_claim duration to the end-to-end total
	// (without re-emitting the per-phase sample). On the combined one-call path
	// createPodClaim is the same call's claim duration so the single
	// observation covers the whole envelope; on the two-step /start path it is
	// zero by construction and the create-time pod_claim was already
	// observed by the separate /create request.
	end := result.Timings
	end.PodClaim = createPodClaim
	s.recordStartupDuration(match, end)
	return result, nil
}

// launchPrepared runs only the §4.4 launch against the pod a two-step
// session prepared at /finalize, reconnecting from the row's persisted §4.6
// binding (PodAssignment + PoolRef). The §4.3 prepare barrier
// (PrepareWorkspace, FinalizeWorkspace, RunSetup, AssignCredentials) already
// ran at /finalize, so /start neither re-stages, re-materializes, re-runs
// setup, nor re-assigns the credential lease; it only starts the runtime
// (StartSession for a pod-warm or demoted pod, ConfigureWorkspace for a
// still-SDK-warm pod). The §6.1 SDK-warm demotion decision the prepare phase
// made is a pure function of the persisted plan and the runtime's
// sdkWarmBlockingPaths, so launchPrepared recomputes it through
// podsession.RequiresDemotion rather than re-running the prepare phase to
// learn it. A launch failure is surfaced for writePodClaimError; the binder
// already reclaimed the bound pod (and the lease assigned at finalize) on the
// failure path.
//
// The /start end-to-end span is launch-only by construction: the create-time
// pod_claim phase was recorded at /create and the materialization / setup /
// credential phases at /finalize, each once, so launchPrepared records only
// the agent_session_start phase and the end-to-end envelope here.
// spec: §4.4, §4.6 (proposal); §6.1 lines 30-34; §6.3 lines 348, 372.
func (s *Server) launchPrepared(ctx context.Context, row sessionstore.Session, match podsession.PoolMatch, bindReq podsession.BindRequest) (*podsession.BindResult, error) {
	bindReq.SandboxName = row.PodAssignment
	if row.PoolRef != "" {
		bindReq.Pool = row.PoolRef
	}
	bindReq.Demoted = podsession.RequiresDemotion(bindReq)
	result, err := s.podBinder.Launch(ctx, bindReq)
	if err != nil {
		return nil, err
	}
	// spec: §6.3 — Launch measured only the agent_session_start phase; record
	// it and the launch-only end-to-end envelope. The pod_claim and
	// workspace_materialization / setup_commands / credential_assignment phases
	// were recorded at /create and /finalize, so they are not re-emitted here.
	s.recordStartupPhases(match, result.Timings)
	s.recordStartupDuration(match, result.Timings)
	return result, nil
}

// maxSlotRetries is the §5.2 concurrent-workspace slot retry budget: one
// retry, two total attempts including the original. The retry always lands
// on a fresh slot (BindSlot re-reserves, materializing a fresh workspace),
// satisfying the §5.2 "fresh workspace guarantee".
const maxSlotRetries = 1

// slotBinder is the subset of *podsession.Binder the §5.2 slot retry
// policy drives. The interface seam lets applySlotRetryPolicy be unit
// tested with a fake binder rather than an envtest cluster.
type slotBinder interface {
	BindSlot(ctx context.Context, req podsession.SlotBindRequest) (*podsession.BindResult, error)
	ReleaseSlotReservation(ctx context.Context, sandboxName, slotID string) error
	DrainSandbox(ctx context.Context, sandboxName string) error
}

// bindSlotWithRetry applies the §5.2 concurrent-workspace slot retry
// policy around the gateway's pod binder. It is the production entry point
// that threads the server's binder, slot-health tracker, and replacement
// metric into applySlotRetryPolicy.
func (s *Server) bindSlotWithRetry(ctx context.Context, req podsession.SlotBindRequest) (*podsession.BindResult, error) {
	return applySlotRetryPolicy(ctx, s.podBinder, s.slotHealth, s.slotStates, s.slotReplacement, s.slotLeakGauge, req)
}

// applySlotRetryPolicy is the §5.2 "Concurrent-workspace slot retry
// policy":
//
//   - A reservation-exhaustion sentinel (no slot was reserved) is returned
//     unchanged so the handler maps it to WARM_POOL_EXHAUSTED.
//   - A slot failure after reservation is released (so the retry lands on a
//     genuinely fresh slot and the pod's active_slots is not leaked) and
//     recorded against the pod's rolling fail/leak window. When the pod
//     crosses the ceil(maxConcurrent/2) unhealthy threshold it is drained
//     as a whole and the replacement counter is incremented.
//   - A non-retryable reason (oom, workspace_validation, policy_rejection)
//     or an exhausted retry returns the §5.2 structured SlotFailedError.
//   - A transient reason retries once on a fresh slot.
//
// spec: §5.2 "Concurrent-workspace slot retry policy"; §6.2
// (claimed → draining on the unhealthy-slot threshold); §6.2
// (a slot whose cleanup does not reclaim it is leaked and feeds the per-pod
// lenny_adapter_leaked_slots gauge).
func applySlotRetryPolicy(ctx context.Context, binder slotBinder, health *slothealth.Tracker, slots *slotstate.Registry, replacement func(pool string), leakGauge func(pod, pool string, leaked int), req podsession.SlotBindRequest) (*podsession.BindResult, error) {
	var lastErr error
	for attempt := 0; attempt <= maxSlotRetries; attempt++ {
		result, err := binder.BindSlot(ctx, req)
		if err == nil {
			return result, nil
		}
		// Exhaustion sentinels are not slot failures (no slot was reserved):
		// surface them unchanged for the WARM_POOL_EXHAUSTED mapping.
		if errors.Is(err, podclaim.ErrNoConcurrentSlot) ||
			errors.Is(err, podclaim.ErrTenantMismatch) ||
			errors.Is(err, podclaim.ErrNoIdlePod) {
			return nil, err
		}
		var sbe *podsession.SlotBindError
		if !errors.As(err, &sbe) {
			// A failure outside the reserved-slot window (e.g. pool resolve)
			// is not subject to the slot retry policy.
			return nil, err
		}
		lastErr = err
		reason := sbe.Reason()
		// Release the failed slot so a retry re-reserves a fresh one and the
		// pod's active_slots is not leaked by the failed attempt.
		if relErr := binder.ReleaseSlotReservation(ctx, sbe.Pod, sbe.SlotID); relErr != nil {
			log.Printf("sessionserver: §5.2 release failed slot %s on pod %s: %v", sbe.SlotID, sbe.Pod, relErr)
			// spec: §6.2 line 176/179 — the reservation could not be reclaimed,
			// so the slot is leaked: it remains counted in active_slots until
			// the pod terminates. Record it for the per-pod
			// lenny_adapter_leaked_slots gauge. The slot is already counted
			// toward the ceil(maxConcurrent/2) unhealthy threshold via
			// RecordFailure below, so it is not also recorded in the rolling
			// fail/leak window — a single bad slot counts once.
			leaked := slots.MarkLeaked(sbe.SlotID, sbe.Pod, req.Pool)
			if leakGauge != nil {
				leakGauge(sbe.Pod, req.Pool, leaked)
			}
		}
		// Account the failure toward the pod's §5.2 rolling fail/leak window
		// and retire the whole pod when it crosses the unhealthy threshold.
		health.RecordFailure(sbe.Pod)
		if health.Unhealthy(sbe.Pod, req.MaxConcurrentSessions) {
			if drainErr := binder.DrainSandbox(ctx, sbe.Pod); drainErr != nil {
				log.Printf("sessionserver: §5.2 drain unhealthy pod %s: %v", sbe.Pod, drainErr)
			}
			if replacement != nil {
				replacement(req.Pool)
			}
			health.Forget(sbe.Pod)
			// spec: §6.2 line 179 — the drained pod is being replaced; its
			// leaked slots are reclaimed with it, so drop the per-pod leaked
			// tracking and zero the gauge series.
			slots.ForgetPod(sbe.Pod)
			if leakGauge != nil {
				leakGauge(sbe.Pod, req.Pool, 0)
			}
		}
		if reason.NonRetryable() || attempt == maxSlotRetries {
			return nil, &podsession.SlotFailedError{
				Category: string(reason),
				SlotID:   sbe.SlotID,
				Pool:     req.Pool,
				Err:      sbe.Err,
			}
		}
		// Transient with a retry remaining: loop to re-claim a fresh slot.
	}
	return nil, lastErr
}

// registerBinding publishes a successful startOnPod / resumeOnPod
// result so the message and teardown paths can reach the pod, and
// persists the bound pod's SandboxName to sessions.pod_assignment so
// a fresh gateway replica can recover the binding after a coordinator
// handoff. The persist is best-effort: a failure leaves the in-memory
// Registry authoritative and the next coordination sweep re-publishes
// the assignment. spec: §4.2 line 160 — "Pod-to-session binding".
func (s *Server) registerBinding(ctx context.Context, result *podsession.BindResult) {
	if result == nil {
		return
	}
	s.podRegistry.Put(result)
	s.persistPodAssignment(ctx, result.TenantID, result.SessionID, result.SandboxName)
	// spec: §7.3 line 408 — capture the adapter's reported WorkspaceRoot
	// from the §15.5 handshake (carried through BindResult) on the first
	// non-empty bind so a subsequent Resume can assert the replacement
	// pod's WorkspaceRoot matches. The pgstore guard ignores an empty
	// payload so a later bind without the field never overwrites a
	// recorded value. F-7.3.15.
	s.persistWorkspaceRoot(ctx, result.TenantID, result.SessionID, result.WorkspaceRoot)
	// F-7.4.15: republish any §14 advisory warnings the adapter raised
	// during FinalizeWorkspace materialization. The
	// `workspace_plan_strip_components_skip` warning per §7.4 line 459
	// is the only producer in v1; SSE subscribers see one event per
	// skipped archive entry so a client can audit the strip-components
	// rule.
	s.publishWorkspaceWarnings(result)
}

// publishWorkspaceWarnings emits one §14 `workspace_plan_warning`
// frame per advisory the adapter returned from FinalizeWorkspace. The
// frame goes on the per-session SSE bus (so clients subscribing to
// /v1/sessions/{id}/events see it) and on the §16.6 / §25.3
// operational-event stream (so Ops console and AI DevOps agents see
// it asynchronously). The emitted payload carries the §14 line 100
// per-warning structured fields (`entryPath`, `segmentCount`,
// `stripComponents`) so a consumer that matches on these can extract
// them without parsing the free-form message.
//
// spec: §7.4 line 459; §14 line 100; §16.6 catalogue. F-7.4.15,
// F-14.1.17, F-14.1.18.
func (s *Server) publishWorkspaceWarnings(result *podsession.BindResult) {
	if result == nil {
		return
	}
	s.publishWorkspacePlanWarnings(result.TenantID, result.SessionID, result.WorkspacePlanWarnings)
}

// publishWorkspacePlanWarnings emits one §14 `workspace_plan_warning` frame
// per advisory the §4.7 materialization raised, on the per-session SSE bus
// and the §16.6 / §25.3 operational-event stream. The /start launch path
// (via publishWorkspaceWarnings on the BindResult) and the §4.3 finalize
// prepare barrier (on the PrepareResult) both republish their warnings
// through it, so the strip-components-skip advisory reaches subscribers
// regardless of which boundary materialized the workspace. spec: §7.4 line
// 459; §14 lines 100/334/338; §16.6 catalogue. F-7.4.15, F-14.1.17, F-14.1.18.
func (s *Server) publishWorkspacePlanWarnings(tenantID, sessionID string, warnings []*adapterv1.WorkspacePlanWarning) {
	for _, w := range warnings {
		if w == nil {
			continue
		}
		payload := map[string]any{
			"code":            w.GetCode(),
			"sourceIndex":     w.GetSourceIndex(),
			"entryPath":       w.GetEntryPath(),
			"segmentCount":    w.GetSegmentCount(),
			"stripComponents": w.GetStripComponents(),
			"message":         w.GetMessage(),
		}
		// spec: §14 line 334 — the materializer-raised
		// `workspace_plan_unknown_source_type` warning carries
		// `unknownType` + `schemaVersion`; surface them only when set so
		// the other warning codes keep their existing payload. F-14.1.2.
		if w.GetUnknownType() != "" {
			payload["unknownType"] = w.GetUnknownType()
		}
		if w.GetSchemaVersion() != 0 {
			payload["schemaVersion"] = w.GetSchemaVersion()
		}
		// spec: §14 line 338 — the materialization-time
		// `workspace_plan_path_collision` warning carries `path`,
		// `winningSourceIndex`, `losingSourceIndex`. A non-empty path is
		// the discriminator: only collision warnings set it, so the other
		// codes keep their existing payload. F-14.1.9.
		if w.GetPath() != "" {
			payload["path"] = w.GetPath()
			payload["winningSourceIndex"] = w.GetWinningSourceIndex()
			payload["losingSourceIndex"] = w.GetLosingSourceIndex()
		}
		s.publishEvent(tenantID, sessionID, "workspace_plan_warning", payload)
		s.emitWorkspacePlanWarningOps(tenantID, sessionID, payload)
	}
}

// emitWorkspacePlanWarningOps publishes a §14 warning on the §16.6 /
// §25.3 operational-event stream so Ops/audit subscribers see the
// warning without having to subscribe to the per-session SSE feed.
// No-op when the OpsEmitter is not wired (tests).
//
// spec: §14 lines 100/334/338; §16.6 catalogue. F-14.1.17.
func (s *Server) emitWorkspacePlanWarningOps(tenantID, sessionID string, payload map[string]any) {
	if s.opsEmitter == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		data = []byte("{}")
	}
	subject := "session/" + sessionID
	_ = s.opsEmitter.Emit(context.Background(), events.OperationalEvent{
		Source:          "/v1/sessions",
		Subject:         subject,
		Type:            events.EventWorkspacePlanWarning.CloudEventsType(),
		Severity:        "info",
		DataContentType: "application/json",
		Data:            data,
	})
	_ = tenantID
}

// publishParsePlanWarnings emits one SSE `workspace_plan_warning`
// frame per §14 advisory the workspaceplan parser raised on
// CreateSession ingest (`workspace_plan_unknown_source_type`,
// `workspace_plan_path_collision`). The §14 spec calls each warning
// an "event" the gateway emits — the create response already echoes
// the same slice, but operators (Ops console, audit pipelines, AI
// DevOps agents per §25) cannot observe them asynchronously unless
// the gateway publishes them on the same per-session SSE bus the
// strip-components-skip warnings ride.
//
// spec: §14 lines 100, 334, 338; §14 WarningCode. F-14.1.17,
// F-14.1.18.
func (s *Server) publishParsePlanWarnings(tenantID, sessionID string, warnings []workspaceplan.Warning) {
	if sessionID == "" || len(warnings) == 0 {
		return
	}
	for _, w := range warnings {
		payload := map[string]any{
			"code":        string(w.Code),
			"sourceIndex": w.SourceIndex,
			"message":     w.Message,
		}
		if w.Field != "" {
			payload["field"] = w.Field
		}
		// spec: §14 line 334 — unknown_source_type fields:
		// `schemaVersion`, `unknownType`.
		if w.SchemaVersion != nil {
			payload["schemaVersion"] = *w.SchemaVersion
		}
		if w.UnknownType != "" {
			payload["unknownType"] = w.UnknownType
		}
		// spec: §14 line 338 — path_collision fields: `path`,
		// `winningSourceIndex`, `losingSourceIndex`.
		if w.Path != "" {
			payload["path"] = w.Path
		}
		if w.WinningSourceIndex != nil {
			payload["winningSourceIndex"] = *w.WinningSourceIndex
		}
		if w.LosingSourceIndex != nil {
			payload["losingSourceIndex"] = *w.LosingSourceIndex
		}
		s.publishEvent(tenantID, sessionID, "workspace_plan_warning", payload)
		s.emitWorkspacePlanWarningOps(tenantID, sessionID, payload)
	}
}

// rollbackBinding releases a successful startOnPod result whose
// caller has decided to abort the §7.1 line 28 atomic-creation flow
// before the session row was persisted. Exclusive session-mode
// bindings drop through Binder.Release with a `failed` disposition;
// concurrent-slot bindings drop through ReleaseSlot. Best-effort:
// the §6.2 reclaim path runs regardless, so a release error here is
// logged and swallowed. spec: §7.1 line 28 atomic-creation rollback.
func (s *Server) rollbackBinding(ctx context.Context, result *podsession.BindResult) {
	if result == nil || s.podBinder == nil {
		return
	}
	var err error
	if result.SlotID != "" {
		err = s.podBinder.ReleaseSlot(ctx, result)
	} else {
		err = s.podBinder.Release(ctx, result, "failed")
	}
	if err != nil {
		log.Printf("sessionserver: rollback binding for session %s: %v", result.SessionID, err)
	}
}

// createClaimNeedsRollback reports whether the create-and-start failure
// handler must release the create-time claim itself (via rollbackClaim)
// after a startOnPod error, or whether the binder already released it.
//
// The combined POST /v1/sessions/start path claims the pod at /create and
// then runs prepare/launch against that claim in the same call. On a
// startOnPod error the create-time claim must be released because the row is
// not persisted (§7.1 line 28). The disposition turns on the claim kind:
//
//   - No claim (nil) or a claimless service-mode disposition: nothing to
//     release.
//   - Exclusive claim (SlotID == ""): always release. rollbackClaim runs
//     ReclaimClaimed, a DELETE that is idempotent — a no-op when Prepare or
//     Launch already reclaimed the pod, and the only release on a pre-bind
//     ResolvePool/pool-warming/manifest failure inside startOnPod.
//   - Slot reservation (SlotID set): release only when the binder did not
//     reach BindReservedSlot. BindReservedSlot owns the non-idempotent
//     active_slots release on its own failures (slotbinder.go), surfaced as a
//     *podsession.SlotBindError, so releasing again would double-decrement the
//     counter. But startOnPod runs ResolvePool, the PoolWarmingUp gate, and
//     runtimeManifestFields BEFORE BindReservedSlot; a failure there (a
//     transient kube-API error on the second ResolvePool of the same request,
//     a pool that warmed down) returns a non-SlotBindError without ever
//     releasing the create-time reservation. Releasing it here keeps the
//     active_slots increment from leaking to the §4.6.1 orphan-GC backstop,
//     matching the §7.1 atomicity envelope the decomposed createSession path
//     already honors through its own rollbackClaim.
//
// spec: §7.1 line 28 (rollback releases the pod claim on a create-step
// failure); §4.7 (the combined path reuses the claim/prepare/launch phases);
// §5.2 (slot reservation release).
func createClaimNeedsRollback(claim *podsession.ClaimResult, err error) bool {
	if claim == nil {
		return false
	}
	if claim.SlotID == "" {
		// Exclusive claim: ReclaimClaimed is idempotent, so release
		// unconditionally regardless of where startOnPod failed.
		return true
	}
	// Slot reservation: BindReservedSlot already released it iff the failure
	// is a *podsession.SlotBindError (the binder ran). A pre-bind failure
	// returns some other error and leaks the reservation unless released here.
	var slotBindErr *podsession.SlotBindError
	return !errors.As(err, &slotBindErr)
}

// rollbackClaim releases a pod claimed at /create when a later create
// step fails before the session row is persisted (§7.1 line 28
// atomic-creation rollback). The claim holds no live connection, so the
// release runs against the persisted binding. The disposition follows the
// claim kind: an exclusive Claim is released through ReclaimClaimed, which
// deletes the per-pod SandboxClaim and revokes any lease; a §5.2
// concurrent-slot reservation (SlotID set) is released through
// ReleaseSlotReservation, which decrements the pod's active_slots without
// deleting the per-pod claim or disturbing sibling slots. No lease is
// assigned at create (§4.4 of the proposal), so the lease revoke is a
// no-op here. Best-effort: the §4.6.1 orphan-claim GC reclaims a claim a
// release error leaves behind, so the error is logged and swallowed.
// spec: §7.1 line 28 (rollback releases the pod claim); §4.5, §4.6
// (proposal); §5.2 (slot reservation release).
func (s *Server) rollbackClaim(ctx context.Context, claim *podsession.ClaimResult, sessionID string) {
	if claim == nil || s.podBinder == nil {
		return
	}
	if claim.SlotID != "" {
		if err := s.podBinder.ReleaseSlotReservation(ctx, claim.SandboxName, claim.SlotID); err != nil {
			log.Printf("sessionserver: rollback create-time slot reservation %s on pod %s for session %s: %v",
				claim.SlotID, claim.SandboxName, sessionID, err)
		}
		return
	}
	if err := s.podBinder.ReclaimClaimed(ctx, claim.SandboxName, sessionID); err != nil {
		log.Printf("sessionserver: rollback create-time claim %s for session %s: %v", claim.SandboxName, sessionID, err)
	}
}

// recordStartupPhases observes the §6.3 line 372 per-phase
// lenny_session_startup_phase_duration_seconds histogram for the phases
// the caller actually measured. The decomposed create → finalize →
// start lifecycle records each §6.3 phase at the boundary where it runs:
// pod_claim at /create, workspace_materialization / setup_commands /
// credential_assignment at the /finalize prepare barrier, and
// agent_session_start at /start (§5 of the 0007 proposal). A phase whose
// duration is zero was not measured at this boundary, so it is skipped
// rather than recorded as a spurious 0s sample that would skew that
// phase's distribution; this keeps each phase a single observation per
// logical start without changing the histogram structure.
// The first-prompt/TTFT phase is tracked separately (F-6.3.3,
// lenny_session_time_to_first_token_seconds) because it needs runtime
// streaming feedback the start path does not see.
// spec: §6.3 lines 358, 372.
func (s *Server) recordStartupPhases(match podsession.PoolMatch, t podsession.BindTimings) {
	runtimeClass, ok := isolation.RuntimeClassName(isolation.Profile(match.IsolationProfile))
	if !ok {
		// An unrecognized profile would mislabel the series; skip rather
		// than emit an empty runtime_class.
		return
	}
	if s.observeStartupPhase == nil {
		return
	}
	phases := []struct {
		name string
		d    time.Duration
	}{
		{"pod_claim", t.PodClaim},
		{"workspace_materialization", t.WorkspaceMaterialization},
		{"setup_commands", t.SetupCommands},
		{"credential_assignment", t.CredentialAssignment},
		{"agent_session_start", t.AgentSessionStart},
	}
	for _, p := range phases {
		if p.d <= 0 {
			// A zero duration means the phase did not run at this boundary
			// (for example pod_claim is recorded at /create, not re-attributed
			// at the launch boundary). Skip it so the phase histogram is not
			// polluted with a spurious 0s sample.
			continue
		}
		s.observeStartupPhase(p.name, runtimeClass, p.d.Seconds())
	}
}

// recordStartupDuration observes the §6.3 line 348 end-to-end pod-warm
// envelope on lenny_session_startup_duration_seconds exactly once per
// logical start, at the launch boundary, with the full assembled
// timings. Per §6.3 line 348 the metric is pod claim through agent
// session ready excluding workspace materialization; setup commands are
// also excluded because they are deployer-controlled (§6.3 line 363) and
// the 2s runc / 5s gVisor SLO budgets only the platform phases (claim
// ≤100ms + credential ≤100ms + agent start ≤1.5s/4.5s). The end-to-end
// total is therefore PodClaim + CredentialAssignment + AgentSessionStart.
// The /create boundary records only the pod_claim phase via
// recordStartupPhases and does not emit this end-to-end metric, so a
// single logical start observes lenny_session_startup_duration_seconds
// once rather than once at create and again at launch.
// spec: §6.3 lines 348, 358.
func (s *Server) recordStartupDuration(match podsession.PoolMatch, t podsession.BindTimings) {
	runtimeClass, ok := isolation.RuntimeClassName(isolation.Profile(match.IsolationProfile))
	if !ok {
		return
	}
	if s.observeStartupDuration == nil {
		return
	}
	total := t.PodClaim + t.CredentialAssignment + t.AgentSessionStart
	s.observeStartupDuration(match.Pool, runtimeClass, match.IsolationProfile, total.Seconds())
}

// persistPodAssignment writes the bound pod's SandboxName back to the
// session row so a fresh gateway replica can pick up the binding from
// Postgres after a coordinator handoff. Best-effort: an update failure
// is logged via the configured error handler but does not fail the
// claim — the in-memory Registry remains authoritative for this
// replica, and the next coordination sweep will re-publish the
// assignment. spec: §4.2 line 160 — "Pod-to-session binding".
func (s *Server) persistPodAssignment(ctx context.Context, tenantID, sessionID, podAssignment string) {
	if podAssignment == "" {
		return
	}
	_, err := s.store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		row.PodAssignment = podAssignment
		return nil
	})
	if err != nil {
		log.Printf("sessionserver: persist pod_assignment for session %s: %v", sessionID, err)
	}
}

// persistWorkspaceRoot records the adapter-reported absolute cwd path on
// the session row at the first non-empty bind. The pgstore-side write
// guard ignores empty payloads so a follow-on bind that did not capture
// a value (older adapter, replay path) cannot clobber a recorded one.
// The recorded value feeds the §7.3 line 408 "same absolute cwd path"
// assertion on a subsequent Resume — the gateway reads row.WorkspaceRoot
// and passes it via ResumeRequest.expected_workspace_root for the
// replacement pod's adapter to compare against its own WorkspaceRoot.
// Best-effort: a store failure logs and continues; the in-memory
// BindResult still carries the value for the current replica.
//
// spec: §7.3 line 408 step (d). F-7.3.15.
func (s *Server) persistWorkspaceRoot(ctx context.Context, tenantID, sessionID, root string) {
	if root == "" {
		return
	}
	_, err := s.store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		if row.WorkspaceRoot == "" {
			row.WorkspaceRoot = root
		}
		return nil
	})
	if err != nil {
		log.Printf("sessionserver: persist workspace_root for session %s: %v", sessionID, err)
	}
}

// failSession marks a session row failed after a start-path error. The
// update is best-effort: the start handler has already chosen the HTTP
// error it returns to the client, so a store failure here cannot change
// the reply. A failed child session is archived to the §8.10
// session_tree_archive so a resumed parent can replay the outcome.
// expireSession transitions a session to the §7.3 terminal `expired`
// state and runs the same archive / terminal-lifecycle teardown as
// failSession. The §8.10 tree-recovery driver uses it for a node whose
// individual `maxResumeWindowSeconds` elapsed before recovery reached
// it (spec: §8.10 line 1027 — "that node transitions to `expired`").
func (s *Server) expireSession(ctx context.Context, tenantID, sessionID string) {
	updated, err := s.store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		row.State = session.StateExpired
		return nil
	})
	if err == nil {
		s.archiveSettledChild(ctx, updated)
		s.emitTerminalLifecycle(ctx, updated)
	}
}

func (s *Server) failSession(ctx context.Context, tenantID, sessionID string) {
	updated, err := s.store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		row.State = session.StateFailed
		return nil
	})
	if err == nil {
		s.archiveSettledChild(ctx, updated)
		// spec: §7.2 lines 137, 141 / §11.7 / §7.1 line 77 — the
		// start-path failure is a terminal transition, so it emits the
		// same status_change/session_complete SSE events, the
		// session.failed audit event, and the retention-window roll as
		// any other terminal path. The heavier seal/executor-close
		// teardown stays in recordSessionCompleted: a start-path failure
		// never bound a workspace to seal.
		s.emitTerminalLifecycle(ctx, updated)
	}
}

// handleResume implements POST /v1/sessions/{id}/resume per §15.1 and
// §7.1. The endpoint is valid only from `awaiting_client_action` — the
// state a session reaches after automatic resume retries are exhausted
// or the resume window elapses (§7.2). A session in that state has no
// live pod, so the handler restores the session onto a fresh §5 warm
// pod from its latest §7.1 WorkspaceSnapshot before the row
// transitions to running. The API-reported transition is
// `resume_pending` → `running`; the `resume_pending` and `resuming`
// states between are internal transients.
//
// handleResume is a dedicated handler rather than a generic
// handleTransition because the resume carries the extra pod-claim and
// workspace-restore step.
func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
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
		Endpoint:     session.EndpointResume,
		CurrentState: row.State,
	}); err != nil {
		s.writePreconditionError(w, err)
		return
	}

	// A session in `awaiting_client_action` has no live pod: it reached
	// that state after its pod failed and automatic recovery was
	// abandoned. When the gateway is wired with a pod binder, restore
	// the session onto a fresh pod before the row transitions to
	// running. A claim failure is reported as a retryable 503. Transient
	// pool/credential exhaustion keeps the row in awaiting_client_action
	// so the client can re-issue `POST /resume` once pods are available;
	// only a non-retryable cause demotes the row to failed. F-7.3.23.
	//
	// spec: §7.3 lines 470-472 — internally the row traverses
	// `awaiting_client_action → resume_pending → resuming → running`;
	// the API view collapses to `awaiting_client_action → running`.
	// Writing the `resuming` transient before resumeOnPod makes the
	// §7.2 line 197 / 198 mid-resume terminal-collapse edges
	// (resuming → cancelled, resuming → completed) reachable by
	// concurrent DELETE / cascade / failure-report observers, and the
	// §6.2 line 249 watchdog uses it as the entry signal for the
	// resuming wall-clock timeout. F-7.3.8.
	if s.podBinder != nil {
		if _, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
			row.State = session.StateResuming
			return nil
		}); err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
	}
	var adapterReportedResumeMode string
	if s.podBinder != nil {
		mode, err := s.resumeOnPod(r.Context(), row)
		if err != nil {
			// spec: §16.1 catalog — record the failed resume attempt
			// before unwinding so the {pool, outcome="failure"} counter
			// advances even when the row transitions straight to failed.
			// F-7.3.10.
			if s.incSessionResumeAttempt != nil {
				s.incSessionResumeAttempt(row.PoolRef, "failure")
			}
			// spec: §7.2 line 214 (a) — the row was in `resuming` when
			// the resumeOnPod call started; bump the
			// coordination_generation before unwinding so any stale
			// coordinator's subsequent RPC fails the §4.2
			// CoordinatorFence check. F-7.1.14 / F-7.3.8.
			s.bumpCoordinationGenerationOnSnapshotClose(r.Context(), tenantID, id)
			s.holdOrFailOnResumeError(r.Context(), tenantID, id, err)
			// spec: §7.3 — a resume claim failure surfaces as a retryable
			// 503; the row was already persisted (resume requires it), so
			// `RESUME_FAILED` is the analogous spec-named fallback to
			// `SESSION_CREATION_FAILED` and `STARTING_FAILED`. The
			// underlying transient codes (WARM_POOL_EXHAUSTED,
			// CREDENTIAL_POOL_EXHAUSTED, etc.) are surfaced directly so
			// the client receives a spec-defined retry hint.
			s.writePodClaimError(w, err, "RESUME_FAILED", "could not resume the session on a warm pod")
			return
		}
		adapterReportedResumeMode = mode
	}

	updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
		transitionResume(row)
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §16.1 catalog — every resume call increments the
	// `lenny_session_resume_attempts_total{pool, outcome="success"}`
	// counter; the matching {outcome="failure"} branch fires above on
	// the resumeOnPod error path. F-7.3.10.
	if s.incSessionResumeAttempt != nil {
		s.incSessionResumeAttempt(updated.PoolRef, "success")
	}
	// spec: §11.7 / §16.7 — the session.resumed audit row is appended
	// after every successful resume so SIEM dashboards can filter on
	// the §7.3 recovery surface. The matching SSE `session.resumed`
	// event below is the client-facing signal. F-7.3.18.
	if s.lifecycleAudit != nil {
		s.lifecycleAudit.EmitSessionLifecycle(r.Context(), SessionLifecycleEvent{
			EventType:  auditSessionResumed,
			TenantID:   updated.TenantID,
			SessionID:  updated.ID,
			UserID:     updated.UserID,
			RuntimeRef: updated.RuntimeRef,
			State:      string(updated.State),
			At:         s.clock(),
		})
	}
	// spec: §7.2 line 137 — surface the resume transition (the
	// resume_pending → resuming → running chain collapses to the
	// resolved state) before the richer session.resumed event below.
	s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
	// spec: §7.2 line 284 — the resume completes the v1 coordinator
	// re-acquisition of a recovering session, so the gateway emits
	// `inbox_cleared` on the target's own stream: the in-memory inbox from
	// the prior coordinator is gone and the client learns how many messages
	// survived in the DLQ. F-7.2.12.
	s.clearInboxOnResume(r.Context(), updated)
	// spec: §4.4 line 236 — partial-manifest cleanup runs on every
	// resume regardless of whether the underlying reassembly
	// succeeded. The cleaner deletes the chunk objects and
	// soft-deletes the manifest row under the `deleted_at IS NULL`
	// guard; failures leave the row active for the §12.5 backstop
	// sweep to retry.
	if s.partialManifestCleaner != nil {
		if cerr := s.partialManifestCleaner.CleanupAfterResume(r.Context(), tenantID, id); cerr != nil {
			// Cleanup failure is non-fatal: the resume already
			// completed and the row stays active for the
			// backstop sweep. Surface the error in logs only.
			log.Printf("sessionserver: partial-manifest cleanup for session %s failed: %v", id, cerr)
		}
	}
	// spec: §7.2 line 138 — `session.resumed` precedes
	// `children_reattached`. The event fires from the resume handler
	// (rather than from resumeOnPod) so dev-mode / unit-test
	// deployments without a pod binder still emit the event. The
	// adapter-reported mode (when present) is fed into classifyResume so
	// gateway-side eviction / partial-manifest state can still upgrade
	// it to a stronger label, but a plain `full` adapter signal cannot
	// silently override a `conversation_only` gateway classification.
	// F-7.3.22.
	mode := s.classifyResumeWithAdapter(r.Context(), updated, adapterReportedResumeMode)
	s.emitResumedEvent(r.Context(), updated, mode)
	s.emitChildrenReattached(r.Context(), tenantID, id)
	// spec: §8.10 line 1016 — recover the resumed tree's orphaned
	// descendants bottom-up so that "by the time a parent resumes, its
	// children are already in a known state". Detached from the request
	// because the traversal is bounded by maxTreeRecoverySeconds, not by
	// the HTTP deadline. A leaf resume (no descendants) is a cheap
	// no-op.
	s.recoverDelegationTree(r.Context(), tenantID, s.treeRoot(r.Context(), updated))
	s.writeSession(w, http.StatusOK, updated)
}

// holdOrFailOnResumeError reconciles the §7.2 `resuming` row with the
// resume failure: a transient cause (per isTransientPodClaimError) reverts
// the row to `awaiting_client_action` so the explicit `POST /resume` retry
// can succeed once the condition clears, while a non-retryable cause demotes
// the row to terminal `failed`. The boundary is the same
// codes.FailedPrecondition split writeSetupCommandError uses for the wire
// envelope, so a row that returns the retryable RESUME_FAILED envelope is
// never demoted to a terminal state the retry would be rejected against.
// spec: §7.3 line 423 (awaiting_client_action holding state), §6.2 (transient
// setup failure retried on a fresh pod). F-7.3.23.
func (s *Server) holdOrFailOnResumeError(ctx context.Context, tenantID, id string, err error) {
	if isTransientPodClaimError(err) {
		if _, uerr := s.store.Update(ctx, tenantID, id, func(row *sessionstore.Session) error {
			row.State = session.StateAwaitingClientAction
			return nil
		}); uerr != nil {
			log.Printf("sessionserver: revert resuming → awaiting_client_action for session %s: %v", id, uerr)
		}
		return
	}
	s.failSession(ctx, tenantID, id)
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
// cannot drift. A workspace_validation_failed or runtime-registry error
// is still non-retryable and demotes the row to failed. F-7.3.23.
//
// spec: §5.2 line 519 (WARM_POOL_EXHAUSTED), §4.9 lines 1218/1220
// (CREDENTIAL_POOL_EXHAUSTED), §4.3 line 214 (TOKEN_SERVICE_UNAVAILABLE),
// §5.2 lines 602-625 (RUNTIME_UNAVAILABLE pool-warming),
// §7.3 (awaiting_client_action holding state for a retryable resume failure),
// §6.2 (transient setup failure retried on a fresh pod).
func isTransientPodClaimError(err error) bool {
	if err == nil {
		return false
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
		// spec: §11.3 line 209 — the coordinator relinquished the session
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

// classifyResumeWithAdapter combines the gateway-side classification
// (eviction / partial-manifest lookups) with the §4.4 / §7.2 mode the
// adapter reported on its ResumeResponse. The gateway-side classifier
// remains authoritative for `conversation_only` (eviction record) and
// `partial_workspace` (partial-manifest record) because those signals
// are stored in Postgres and outlive the adapter's view of the resume.
// When the gateway classifier picked `full` and the adapter reported a
// distinct mode, the adapter's mode is preferred (e.g., the
// `coordinator_handoff` synthesis path). F-7.3.22.
func (s *Server) classifyResumeWithAdapter(
	ctx context.Context, row sessionstore.Session, adapterMode string,
) checkpoint.ResumeMode {
	gateway := s.classifyResume(ctx, row)
	if gateway != checkpoint.ResumeFull {
		return gateway
	}
	if adapterMode == "" {
		return gateway
	}
	m := checkpoint.ResumeMode(adapterMode)
	if m.IsValid() {
		return m
	}
	return gateway
}

// reattachedChild is the §7.1 ReattachedChild schema carried in the
// children_reattached event.
type reattachedChild struct {
	SessionID         string          `json:"session_id"`
	State             string          `json:"state"`
	PendingRequestID  string          `json:"pending_request_id,omitempty"`
	Result            json.RawMessage `json:"result,omitempty"`
	DelegationLeaseID string          `json:"delegation_lease_id"`
}

// emitChildrenReattached publishes the §7.1 / §8.10 children_reattached
// event on a resumed parent's event stream. Per §7.1 the event fires
// only when the parent has one or more active (non-terminal) children;
// it is a no-op when the parent has no children or every child has
// already settled. The children array carries every child — a settled
// child includes its §8.8 result. Best-effort: a failure to enumerate
// or publish never fails the resume.
//
// A non-terminal child carries `pending_request_id` when an outstanding
// `lenny/request_input` or §6/§9.2 pending interaction exists for the
// child — the parent needs the id to answer via `lenny/send_message`
// (inReplyTo) or the §15.1 interaction endpoints. spec: §7.2 line 153
// (ReattachedChild.pending_request_id). F-7.2.16.
func (s *Server) emitChildrenReattached(ctx context.Context, tenantID, parentID string) {
	if s.events == nil {
		return
	}
	all, err := s.store.List(ctx, tenantID, sessionstore.ListFilter{})
	if err != nil {
		return
	}
	// Collect this parent's direct children and locate the parent row so
	// the §8.10 archive can be replayed under the tree root.
	childRows := make([]sessionstore.Session, 0)
	var parent sessionstore.Session
	parentFound := false
	for _, row := range all {
		if row.ID == parentID {
			parent = row
			parentFound = true
		}
		if row.ParentSessionID == parentID {
			childRows = append(childRows, row)
		}
	}

	// spec: §8.10 line 1062 — the resumed parent sees already-settled
	// children "in original-settlement order". The archive's Replay
	// returns nodes sorted by (settled_at, completion_seq), so a settled
	// child's position in the reattach payload reflects when it actually
	// reached a terminal state, not the store's row order. F-8.10.4.
	children := make([]reattachedChild, 0, len(childRows))
	emitted := make(map[string]bool, len(childRows))
	if s.treeArchive != nil && parentFound {
		root := s.treeRoot(ctx, parent)
		if nodes, rerr := s.treeArchive.Replay(ctx, tenantID, root); rerr == nil {
			for _, n := range nodes {
				if n.ParentSessionID != parentID || emitted[n.NodeSessionID] {
					continue
				}
				emitted[n.NodeSessionID] = true
				children = append(children, reattachedChild{
					SessionID: n.NodeSessionID,
					State:     n.State,
					// spec: §8.8 lines 885-940 — replay the same §8.8
					// TaskResult body the archive captured at settle time so
					// the reattach payload matches the archived result. F-8.8.2.
					Result:            json.RawMessage(n.Result),
					DelegationLeaseID: n.NodeSessionID,
				})
			}
		}
	}

	// Append children the archive did not carry, in a deterministic
	// (session-id) order: terminal children when archiving is disabled or
	// a node was not yet written, then the still-active children that the
	// resumed parent re-awaits via lenny/await_children.
	sort.Slice(childRows, func(i, j int) bool { return childRows[i].ID < childRows[j].ID })
	anyActive := false
	for _, row := range childRows {
		if emitted[row.ID] {
			continue
		}
		child := reattachedChild{
			SessionID: row.ID,
			State:     string(row.State),
			// v1 has no separate delegation-lease id; the child session
			// id is the parent's correlation handle.
			DelegationLeaseID: row.ID,
		}
		if session.IsTerminal(row.State) {
			child.Result, _ = json.Marshal(s.materializeTaskResult(ctx, row, 0))
		} else {
			anyActive = true
			// spec: §7.2 line 153 — populate the pending_request_id when
			// the child has an outstanding request directed at the
			// parent. lenny/request_input wins over the interaction-store
			// entries because it carries a structured reply contract; an
			// interaction (tool-use / elicitation) is the fallback when
			// the child raised an approval. F-7.2.16.
			child.PendingRequestID = s.lookupPendingRequest(ctx, tenantID, row.ID)
		}
		children = append(children, child)
	}
	if !anyActive {
		return
	}
	data, _ := json.Marshal(struct {
		Children []reattachedChild `json:"children"`
	}{Children: children})
	s.events.PublishForTenant(tenantID, parentID, "children_reattached", string(data), s.clock())
}

// lookupPendingRequest returns the pending request id for a child
// session in `input_required`-equivalent state, or "" when none is
// outstanding. It prefers `lenny/request_input` registrations (the
// §8.5 structured-reply contract) and falls back to a §6/§9.2 pending
// interaction (tool-use approval / elicitation). When both sources are
// unwired the function is a no-op. spec: §7.2 line 153 (ReattachedChild
// schema). F-7.2.16.
func (s *Server) lookupPendingRequest(ctx context.Context, tenantID, sessionID string) string {
	if s.inputWaits != nil {
		if ids := s.inputWaits.PendingForSession(sessionID); len(ids) > 0 {
			sort.Strings(ids)
			return ids[0]
		}
	}
	if s.interactions != nil {
		pending, err := s.interactions.ListPending(ctx, tenantID, sessionID)
		if err == nil && len(pending) > 0 {
			return pending[0].ID
		}
	}
	return ""
}

// resumeOnPod restores a session onto a fresh §5 warm pod. When the
// session carries a §7.1 WorkspaceSnapshot it is restored from that
// checkpoint via the adapter Resume RPC. A session that never
// checkpointed has no snapshot to restore; it is rebuilt from the §14
// WorkspacePlan recorded at create by reusing the start path.
//
// On a successful resume the gateway publishes a §7.2 / §4.4
// `session.resumed` event with the derived `resumeMode` (full,
// partial_workspace, or conversation_only) and `workspaceLost`
// (derived from the resume mode) so clients can detect a degraded
// resume.
//
// The returned adapterReportedResumeMode is the §4.4 / §7.2 mode the
// adapter signalled (empty when the snapshot path was not taken or the
// adapter is on an older protocol). The caller passes it to
// `classifyResume` so a gateway-side eviction / partial-manifest record
// can upgrade `full` to `conversation_only` / `partial_workspace` while
// still letting the adapter signal a stronger classification when it
// has one. F-7.3.22.
//
// spec: §4.4 line 263, §7.2 line 138, §10.1 partial-manifest path.
func (s *Server) resumeOnPod(ctx context.Context, row sessionstore.Session) (string, error) {
	if row.WorkspaceSnapshot == nil || row.WorkspaceSnapshot.Ref == "" {
		plan, err := storedWorkspacePlan(row)
		if err != nil {
			return "", err
		}
		// spec: §7.3 (snapshotless resume-rebuild) — the resume path holds no
		// create-time claim or credential resolution (it claims a fresh pod via
		// the whole-sequence Bind), so startOnPod resolves credentials itself.
		result, err := s.startOnPod(ctx, row, plan, nil)
		if err != nil {
			return "", err
		}
		s.registerBinding(ctx, result)
		// spec: §5.2 — a service-mode session is claimless, so startOnPod
		// returns a nil BindResult with no pod to fence. Service mode has no
		// Lenny-managed lifecycle and never reaches a resumable state, so this
		// guard is defensive: skip the resumed-pod fence when no pod was bound.
		if result == nil {
			return "", nil
		}
		if ferr := s.fenceResumedPod(ctx, result.Adapter, row.TenantID, row.ID); ferr != nil {
			return "", ferr
		}
		return "", nil
	}
	// spec: §7.1 / §14.1 — constrain resolution to the client-pinned pool
	// (row.Pool) on the resume-rebuild path; empty resolves by runtime + §5.3
	// profile. F-CS2 (0018).
	match, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.poolPolicyReader(), s.agentNamespace,
		row.RuntimeRef, string(row.IsolationProfile), row.Pool)
	if err != nil {
		return "", err
	}
	agentInterface, minPlatformVersion := s.runtimeManifestFields(ctx, row.RuntimeRef)
	// spec: §7.3 line 397 — surface last_checkpoint_workspace_bytes and the
	// §4.4 hard workspace size cap so the adapter can refuse a restore
	// whose archive would exceed the pod's emptyDir budget before
	// quiescing the runtime. F-7.3.26.
	var expectedBytes int64
	if row.WorkspaceSnapshot != nil {
		expectedBytes = row.WorkspaceSnapshot.Bytes
	}
	result, err := s.podBinder.Resume(ctx, podsession.ResumeRequest{
		Pool:                    match.Pool,
		SessionID:               row.ID,
		TenantID:                row.TenantID,
		Runtime:                 row.RuntimeRef,
		CheckpointID:            row.WorkspaceSnapshot.Ref,
		ExperimentContext:       experimentContextToProto(row.ExperimentContext),
		TracingContext:          row.TracingContext,
		AgentInterface:          agentInterface,
		MinPlatformVersion:      minPlatformVersion,
		RecoveryGeneration:      row.RecoveryGeneration,
		ExpectedWorkspaceBytes:  expectedBytes,
		WorkspaceSizeLimitBytes: match.WorkspaceSizeLimitBytes,
		// spec: §7.3 line 408 step (d) — "Recreate same absolute `cwd`
		// path." The gateway carries the original session's adapter-
		// reported WorkspaceRoot (captured on the §15.5 handshake and
		// persisted at first bind) on every Resume. An empty value
		// (legacy row, adapter on an older protocol) disables the
		// assertion on the adapter side. F-7.3.15.
		ExpectedWorkspaceRoot: row.WorkspaceRoot,
	})
	if err != nil {
		return "", err
	}
	s.podRegistry.Put(result.Result)
	// spec: §4.2 line 156 — recovery_generation is incremented on each
	// pod recovery. Persist the new pod assignment in the same update
	// so a fresh replica picks up the recovered binding without
	// re-running resume.
	s.bumpRecoveryGeneration(ctx, row.TenantID, row.ID, result.Result.SandboxName)
	// spec: §10.1 lines 33-37 / §4.2 line 158 — announce the session's
	// coordination_generation to the (re-)bound pod so it rejects any
	// straggler RPC from a prior coordinator. A relinquish aborts the
	// resume; a best-effort failure is logged and swallowed.
	if ferr := s.fenceResumedPod(ctx, result.Result.Adapter, row.TenantID, row.ID); ferr != nil {
		return "", ferr
	}
	return result.Mode, nil
}

// fenceResumedPod issues the §10.1 / §4.2 CoordinatorFence to the pod a
// resume just (re-)bound, announcing the session's current
// coordination_generation so the pod rejects straggler RPCs from a prior
// coordinator. A relinquish (the coordinator gave up the session after
// exhausting its §11.3 fence retries, releasing the lease) is returned so
// the caller aborts the resume; a best-effort fence failure (the
// generation could not be read) is logged and swallowed because the
// coordination lease still guards exclusive ownership.
//
// spec: §10.1 lines 33-37, §4.2 line 158, §11.3 line 209.
func (s *Server) fenceResumedPod(ctx context.Context, adapter *adapterclient.Client, tenantID, sessionID string) error {
	if s.fencer == nil || adapter == nil {
		return nil
	}
	relinquished, err := s.fencer.Fence(ctx, adapter, tenantID, sessionID)
	if relinquished {
		return err
	}
	if err != nil {
		log.Printf("sessionserver: coordinator fence for session %s best-effort failed: %v", sessionID, err)
	}
	return nil
}

// classifyResume picks the §4.4 / §7.2 ResumeMode for a resume of the
// given session by combining (a) the workspace snapshot source, (b)
// the eviction-state-store lookup (conversation-only fallback), and
// (c) the partial-manifest lookup (partial-workspace reassembly). The
// precedence is:
//
//   - eviction-state record present → ResumeConversationOnly (the
//     workspace bytes are gone; the §4.4 fallback writer recorded
//     conversation cursor + last-message context only).
//   - active partial manifest present → ResumePartialWorkspace (the
//     §10.1 reassembly path applies; recovery fraction is carried on
//     the event when the manifest had a baseline full checkpoint
//     size).
//   - workspace snapshot present, no eviction / partial state →
//     ResumeFull.
//
// A nil lookup (production without the store wired, or dev mode)
// degrades to ResumeFull. A lookup error degrades to ResumeFull as
// well — the resume itself succeeded, so a transient store outage
// must not block the session from coming back online; the operator
// observes the degraded classification only by inspecting the gauge
// rather than by the event.
//
// spec: §4.4 line 263; §7.2 line 138; §10.1 partial-manifest path.
func (s *Server) classifyResume(ctx context.Context, row sessionstore.Session) checkpoint.ResumeMode {
	if s.evictionStateLookup != nil {
		has, err := s.evictionStateLookup.HasEvictionState(ctx, row.TenantID, row.ID)
		if err == nil && has {
			return checkpoint.ResumeConversationOnly
		}
	}
	if s.partialManifestLookup != nil {
		has, err := s.partialManifestLookup.HasActivePartialManifest(ctx, row.TenantID, row.ID)
		if err == nil && has {
			return checkpoint.ResumePartialWorkspace
		}
	}
	return checkpoint.ResumeFull
}

// resumedEventPayload is the §7.2 line 138 `session.resumed` event
// schema: `resumeMode`, `workspaceLost`, and an optional
// `workspaceRecoveryFraction` (populated by the §10.1 partial-manifest
// path; omitted on full and conversation-only resumes per the
// optional-fraction rule).
type resumedEventPayload struct {
	ResumeMode                string   `json:"resumeMode"`
	WorkspaceLost             bool     `json:"workspaceLost"`
	WorkspaceRecoveryFraction *float64 `json:"workspaceRecoveryFraction,omitempty"`
}

// emitResumedEvent publishes the §7.2 line 138 `session.resumed` event
// onto the session's event stream. Best-effort: when the gateway is
// not wired with an event bus (dev mode / unit tests without one) the
// emission is a no-op.
//
// spec: §7.2 line 138, §4.4 line 263, §10.1 partial-manifest path.
func (s *Server) emitResumedEvent(_ context.Context, row sessionstore.Session, mode checkpoint.ResumeMode) {
	if s.events == nil {
		return
	}
	payload := resumedEventPayload{
		ResumeMode:    string(mode),
		WorkspaceLost: mode.WorkspaceLost(),
	}
	s.publishEvent(row.TenantID, row.ID, "session.resumed", payload)
}

// handoffResumedFrame builds the §10.4 line 391 synthesized
// `session.resumed` payload for a coordinator-handoff reattach. The
// resume mode is fixed to `coordinator_handoff`; the handoff re-attaches
// the live pod, so the workspace is intact (workspaceLost: false) and
// workspaceRecoveryFraction is 1.0. ok is false only on a marshal error.
// spec: §10.4 lines 391-393; §7.2 line 138. F-7.2.13, F-10.4.2.
func (s *Server) handoffResumedFrame() ([]byte, bool) {
	full := 1.0
	payload := resumedEventPayload{
		ResumeMode:                string(checkpoint.ResumeCoordinatorHandoff),
		WorkspaceLost:             false,
		WorkspaceRecoveryFraction: &full,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, false
	}
	return data, true
}

// buildHandoffChildrenReattached builds the §10.4 lines 395-397
// synthesized `children_reattached` payload for a coordinator-handoff
// reattach. The §10.4 predicate fires the frame "if the session is a
// parent with archived children whose completion_seq is greater than the
// client's resumeFromSeq" — the child completions the client missed
// while the handoff was in flight. Those archived nodes are streamed in
// §8.10 original-settlement order, and the parent's still-active children
// are appended so the resumed parent re-establishes its await set (the
// §7.2 STR-007 symmetry with the single-coordinator resume path). Returns
// ok=false when the session is not a parent, has no missed completions,
// and has no active children. spec: §10.4 lines 395-397; §7.2 line 153.
// F-7.2.13, F-10.4.2.
func (s *Server) buildHandoffChildrenReattached(ctx context.Context, tenantID, parentID string, afterSeq uint64) ([]byte, bool) {
	all, err := s.store.List(ctx, tenantID, sessionstore.ListFilter{})
	if err != nil {
		return nil, false
	}
	childRows := make([]sessionstore.Session, 0)
	var parent sessionstore.Session
	parentFound := false
	for _, row := range all {
		if row.ID == parentID {
			parent = row
			parentFound = true
		}
		if row.ParentSessionID == parentID {
			childRows = append(childRows, row)
		}
	}

	children := make([]reattachedChild, 0, len(childRows))
	emitted := make(map[string]bool, len(childRows))
	missedCompletion := false
	if s.treeArchive != nil && parentFound {
		root := s.treeRoot(ctx, parent)
		if nodes, rerr := s.treeArchive.Replay(ctx, tenantID, root); rerr == nil {
			for _, n := range nodes {
				if n.ParentSessionID != parentID || emitted[n.NodeSessionID] {
					continue
				}
				// spec: §10.4 line 395 — only archived children whose
				// completion the client has not yet observed (CompletionSeq
				// strictly above the resume cursor). A v1 archive writer
				// that does not stamp a per-session sequence leaves
				// CompletionSeq at 0, which never exceeds a non-zero cursor,
				// so those nodes fall to the active-children pass below.
				if n.CompletionSeq <= 0 || uint64(n.CompletionSeq) <= afterSeq {
					continue
				}
				emitted[n.NodeSessionID] = true
				missedCompletion = true
				children = append(children, reattachedChild{
					SessionID:         n.NodeSessionID,
					State:             n.State,
					Result:            json.RawMessage(n.Result),
					DelegationLeaseID: n.NodeSessionID,
				})
			}
		}
	}

	sort.Slice(childRows, func(i, j int) bool { return childRows[i].ID < childRows[j].ID })
	anyActive := false
	for _, row := range childRows {
		if emitted[row.ID] || session.IsTerminal(row.State) {
			continue
		}
		anyActive = true
		children = append(children, reattachedChild{
			SessionID:         row.ID,
			State:             string(row.State),
			DelegationLeaseID: row.ID,
			// spec: §7.2 line 153 — surface the pending request id so the
			// resumed parent can answer a child blocked on
			// lenny/request_input or a §6/§9.2 interaction.
			PendingRequestID: s.lookupPendingRequest(ctx, tenantID, row.ID),
		})
	}
	if !missedCompletion && !anyActive {
		return nil, false
	}
	data, err := json.Marshal(struct {
		Children []reattachedChild `json:"children"`
	}{Children: children})
	if err != nil {
		return nil, false
	}
	return data, true
}

// bumpCoordinationGenerationOnSnapshotClose increments the §4.2
// coordination_generation counter on a session row as part of the §7.2
// line 214 snapshot-close terminal-collapse sequence. The bump runs in
// the same store update that writes the terminal state, fencing any
// stale coordinator still attempting resume against the prior
// generation — per §4.2 CoordinatorFence preconditions, any subsequent
// operational RPC carrying a lower coordination_generation is rejected.
// recovery_generation is intentionally left untouched: the interrupted
// resume attempt is recorded as failed-by-terminal and is not retried,
// so no new recovery is minted (§7.2 line 214 (b)).
//
// This helper is the gateway's authoritative CAS-fence primitive for
// the resuming → {cancelled, completed, failed} edges (§7.2 lines
// 209-216). The store's monotonicity floor (sessionstore/pgstore guards
// + memstore guards) blocks any update that tries to decrement the
// counter, so a duplicate concurrent transition observes the second
// generation rather than the first.
//
// The bump is best-effort: a store error is logged and the caller's
// terminal write is already durable, so the only consequence is that
// a stale coordinator's next RPC might pass the CG check (degrading to
// the next layer of defence). The session's state is the authoritative
// barrier; the bump is the §7.2 belt-and-braces fence.
//
// Returns whether the bump succeeded. Callers may use the boolean for
// metric / audit emission; v1 only logs on failure.
//
// spec: §7.2 line 214 (a) — snapshot-close coordination_generation bump.
// spec: §4.2 line 158 — CoordinatorFence preconditions. F-7.1.14.
func (s *Server) bumpCoordinationGenerationOnSnapshotClose(ctx context.Context, tenantID, sessionID string) bool {
	_, err := s.store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		row.CoordinationGeneration++
		return nil
	})
	if err != nil {
		log.Printf("sessionserver: bump coordination_generation for session %s: %v", sessionID, err)
		return false
	}
	return true
}

// bumpRecoveryGeneration increments the §4.2 recovery_generation
// counter and persists the recovered pod assignment in the same
// transaction. The store's monotonicity floor ensures the counter
// only advances; the in-memory Registry already holds the new BindResult
// on success.
//
// A pod recovery is also a §4.2 line 158 retry — the Session Manager
// is responsible for both counters, and a recovery onto a fresh pod
// is the v1 retry path. retry_count is bumped in the same
// transaction; the store enforces monotonicity on both columns.
//
// On a successful bump, the §16.1 lenny_session_retry_total counter is
// incremented with the row's FailureClass as the label (or "unknown"
// when no class is recorded), and the §11.7 / §16.7
// session.retry_attempted audit row is appended. Both side effects are
// best-effort and gated on their respective hooks being wired.
//
// spec: §4.2 line 156 — "incremented on each pod recovery".
// spec: §4.2 line 158 — "Retry counters and policy enforcement".
// spec: §16.1 catalog — lenny_session_retry_total. F-7.3.10.
// spec: §11.7 / §16.7 — session.retry_attempted audit. F-7.3.18.
func (s *Server) bumpRecoveryGeneration(ctx context.Context, tenantID, sessionID, podAssignment string) {
	updated, err := s.store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		row.RecoveryGeneration++
		row.RetryCount++
		if podAssignment != "" {
			row.PodAssignment = podAssignment
		}
		return nil
	})
	if err != nil {
		log.Printf("sessionserver: bump recovery_generation for session %s: %v", sessionID, err)
		return
	}
	s.recordSessionRetry(ctx, updated)
}

// recordSetupCommandFailed emits the §7.5 / §7.3 line 387 audit event
// and the §16.1 line 124 warm-pool warmup_failure metric for a
// setup-command-failed bind. The audit Detail carries the cmd, exit
// code, stderr excerpt, and command index pulled from the partial
// per-command outputs the adapter returned alongside the failure so
// operators can reconstruct what happened without parsing the gRPC
// error string. Best-effort: nil hooks degrade to a no-op.
//
// spec: §7.5 line 475, §7.3 line 387, §16.1 line 124 — F-7.5.9.
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
// spec: §7.5 line 475 — F-7.5.9.
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

// recordSessionRetry fires the §16.1 lenny_session_retry_total metric
// and the §11.7 / §16.7 session.retry_attempted audit row for one
// retry attempt. Best-effort: a nil hook degrades to a no-op without
// rolling back the row update that triggered it.
//
// spec: §16.1 catalog (F-7.3.10); §11.7 / §16.7 (F-7.3.18).
func (s *Server) recordSessionRetry(ctx context.Context, row sessionstore.Session) {
	failureClass := string(row.FailureClass)
	if failureClass == "" {
		failureClass = "unknown"
	}
	if s.incSessionRetry != nil {
		s.incSessionRetry(failureClass)
	}
	if s.lifecycleAudit != nil {
		s.lifecycleAudit.EmitSessionLifecycle(ctx, SessionLifecycleEvent{
			EventType:    auditSessionRetryAttempted,
			TenantID:     row.TenantID,
			SessionID:    row.ID,
			UserID:       row.UserID,
			RuntimeRef:   row.RuntimeRef,
			State:        string(row.State),
			FailureClass: failureClass,
			At:           s.clock(),
		})
	}
}
