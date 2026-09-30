// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// claimOutcome reports the result of the §7.1-step-4 pod claim run at
// session create. ClaimResult is nil for the one claimless disposition, a
// service-mode pool; it is non-nil for every other pool, carrying the
// exclusive pod claim or the concurrent-workspace slot reservation. Level
// always carries the §7.1 sessionIsolationLevel derived from the resolved
// pool so the create response reports the actual pod's profile rather than a
// pre-resolved estimate. spec: §7.1 step 4; §5.2.
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
	// PoolDeliveryModes maps each resolved provider pool's name to its
	// §4.9 effective deliveryMode (the authoritative CredentialPool value).
	// The combined one-call POST /v1/sessions/start path threads it into
	// startOnPod so the session-start credential-delivery gate evaluates the
	// delivery mode leasing actually uses without re-resolving. spec: §4.9.
	PoolDeliveryModes map[string]string
}

// claimRoute names the client route a create-time claim runs under. The
// §7.1 atomic-creation envelope covers a plain creation, while the one-call
// create-and-start route also starts the session and so answers a
// post-reservation slot failure with the §5.2 client error.
//
// spec: §7.1 (atomic creation); §5.2 "Client error on exhaustion".
type claimRoute int

const (
	// claimRouteCreate is a plain POST /v1/sessions, which starts nothing.
	claimRouteCreate claimRoute = iota
	// claimRouteStart is a create-and-start call, which binds the slot the
	// session then runs on.
	claimRouteStart
)

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
// The route argument names which client route drove the claim, because the
// two answer a post-reservation slot failure differently: a start-bearing
// route (the one-call create-and-start) answers the §5.2 "Client error on
// exhaustion" envelope naming the session whose slot failed, while a plain
// creation carries no start and keeps the §7.1 atomic-creation
// SESSION_CREATION_FAILED envelope for every claim failure.
//
// spec: §4.1 (proposal), §7.1 steps 3-5; §4.9; §5.2.
func (s *Server) claimAtCreate(ctx context.Context, row sessionstore.Session, plan workspaceplan.Plan, route claimRoute) (*claimOutcome, error) {
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
	// spec: §5.2 — reject a create whose pool is still
	// bootstrapping with 503 RUNTIME_UNAVAILABLE + Retry-After before any
	// claim, so the client retries rather than burning a claim attempt.
	if match.PoolWarmingUp {
		return nil, &podsession.PoolWarmingError{Pool: match.Pool, PodsWarming: match.PodsWarming}
	}
	// spec: §7.1 step 3 / §4.9 — the §7.1 step-3 credential
	// availability pre-check runs at create ahead of the step-4 claim, for
	// every pool class. It is ordered before the service-mode and
	// concurrent-workspace short-circuits below (the same ordering startOnPod
	// uses at /start), so a session that would fail at credential assignment
	// is rejected (CREDENTIAL_POOL_EXHAUSTED / USER_CREDENTIAL_NOT_FOUND)
	// before the client wastes an upload, regardless of the pool's session
	// policy. Surfacing the gate at create rather than at /start is the §2
	// fail-fast contract this proposal establishes.
	credPools, poolDeliveryModes, userCredProviders, err := s.resolveCredentialPools(ctx, row)
	if err != nil {
		return nil, err
	}
	// spec: §5.2 — service mode is claimless: a service-mode session is a
	// connection handle routed through the pool's Service/EndpointSlice, with
	// no SandboxClaim and no workspace materialization. No pod is claimed at
	// create; the row persists with execution_mode=service.
	if match.ExecutionMode == string(runtimestore.ExecutionModeService) {
		return &claimOutcome{Level: level, CredPools: credPools, PoolDeliveryModes: poolDeliveryModes, UserCredProviders: userCredProviders}, nil
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
	// uploads, the same fail-fast contract the exclusive claim gives. A
	// failure after the slot is reserved is a different boundary: a
	// non-retryable bind reason answers the §5.2 "Client error on exhaustion"
	// envelope naming the session whose slot failed, while a transient reason
	// keeps the retryable §15.1 creation fallback.
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
			// spec: §7.1 / §4.1 (proposal) — slot
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
			// A failure after the slot was reserved is the §5.2 slot failure
			// rather than an exhaustion. On a start-bearing route the handler
			// answers with the §5.2 "Client error on exhaustion" envelope
			// naming the session whose slot failed: the one-call
			// POST /v1/sessions/start carries no session identifier in its
			// path, so that body is the client's only source of one. A plain
			// POST /v1/sessions starts nothing, so its claim failure stays on
			// the §7.1 atomic-creation SESSION_CREATION_FAILED envelope.
			if route == claimRouteStart {
				return nil, classifySlotBindFailure(err, slotReq)
			}
			return nil, err
		}
		// spec: §6.3 — record the pod_claim phase timing at /create, its new
		// boundary in the decomposed lifecycle, for the concurrent path too.
		s.recordStartupPhases(match, podsession.BindTimings{PodClaim: claim.PodClaim})
		return &claimOutcome{Claim: claim, Level: level, CredPools: credPools, PoolDeliveryModes: poolDeliveryModes, UserCredProviders: userCredProviders}, nil
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
		// spec: §7.1 / §4.1 (proposal) — pool
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
	return &claimOutcome{Claim: claim, Level: level, CredPools: credPools, PoolDeliveryModes: poolDeliveryModes, UserCredProviders: userCredProviders}, nil
}

// errCreateClaimExhausted marks a create-time pod-claim exhaustion so
// writePodClaimError routes it to the §7.1 SESSION_CREATION_FAILED
// atomicity envelope (proposal §4.1, atomicity note at §7.1)
// rather than the §5.2 WARM_POOL_EXHAUSTED code the two-step start path
// uses. The wrapped podclaim.ErrNoIdlePod stays inspectable for any
// caller that branches on the underlying sentinel.
var errCreateClaimExhausted = errors.New("create-time warm-pod claim exhausted")
