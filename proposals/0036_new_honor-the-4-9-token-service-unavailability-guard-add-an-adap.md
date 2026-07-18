# Proposal: Honor the §4.9 Token Service unavailability guard: add an adapter ExtendCredentialLease surface, a breaker-open hold-and-reschedule branch in the credential renewal worker, and a proxy-mode lease-deadline extension, so a transient Token Service outage extends the still-valid lease instead of driving checkpoint-and-restart into a loop

- **Status:** Applied to spec (2026-07-17). Converged after 6 adversarial review rounds (6 findings fixed). Code not yet implemented. Land as a coordinated pair with proposal 0037 (both touch `credrenewal` / `cmd/lenny-gateway/revocation.go`).
- **Date:** 2026-07-14.
- **Scope:** A new capability that wires the §4.9 "Token Service unavailability guard" (`spec/04_system-components.md:1470`), which the shipped implementation lacks entirely. The change adds a gateway-initiated `ExtendCredentialLease` RPC on the Adapter service (`schemas/lenny-adapter.proto`, regenerated `pkg/proto/adapter/v1`), an adapter-side `Server.ExtendCredentialLease` that re-arms the direct-mode expiry timer to a later deadline without re-delivering credential material (`pkg/adapter/credexpiry.go`, `pkg/adapter/credentials.go`), a breaker-open hold-and-reschedule branch and an `OnExtend` hook in the credential renewal worker (`pkg/gateway/credentials/credrenewal/credrenewal.go`), and the gateway wiring that maps the breaker-open sentinel and dispatches the extension by delivery mode over the `credleasestore.LeaseStore` interface the LLM Proxy reads (`cmd/lenny-gateway/cred_renewal.go`, `cmd/lenny-gateway/revocation.go`, `pkg/gateway/runtime/adapterclient/client.go`). The cumulative-extension cap terminates a capped session through a new `sessionbudget.Terminator` dependency on the renewal wiring, force-transitioning the session to its §8.8 `expired` terminal state with the existing `expired:lease` §8.8 `FailureReason` (`session.FailureExpiredLease`, which already denotes a §4.9 credential-lease expiry), so no new client-facing error code is introduced. The §4.9 spec paragraph is amended to name the mechanism and settle the deadline-redefinition invariant. Every extension path reuses locally held lease state and calls the Token Service on no path. The change reaches tier 0 (proto and codegen), tier 1 (worker branch, adapter timer extension, proxy-store extension), tier 3 (the `ExtendCredentialLease` wire contract), tier 4 (the gateway-to-adapter extension flow), tier 8 (the transient Token Service outage and recovery), and tier 10 (adapter conformance). It runs on the `runc` RuntimeClass and carries no operator-hardware dependency.

This document stages the proposed spec, code, and test changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

The §4.9 "Token Service unavailability guard" (`spec/04_system-components.md:1470`) mandates that when the Token Service circuit breaker is open ([§4.3](#43-token-service)) and the lease has not yet expired (`now < lease.expiresAt`), the proactive renewal worker MUST NOT trigger the Standard/Basic Fallback Flow. Instead it extends the adapter-side lease timer by one additional `renewBeforeBuffer` interval and reschedules. `renewBeforeBuffer` defaults to 300 seconds and is configurable per pool via `renewBeforeBufferSeconds` (`spec/04_system-components.md:1181`). The guard exists only as prose; no mechanism honors it.

### (A) The worker treats a breaker-open failure like any other renewal failure

`credrenewal.Worker.Tick` treats every `Renewer` error uniformly. On any non-nil `err` it calls `recordFailure`, and at `MaxRenewalRetries` (=3) it calls `exhaust` (`pkg/gateway/credentials/credrenewal/credrenewal.go:231-236`, `:253-272`). The breaker-open sentinel `credassign.ErrTokenServiceUnavailable`, mapped from `subsystem.ErrCircuitOpen` in `callTokenService` (`pkg/gateway/credentials/credassign/client.go:145-146`) and propagated raw through `credRenewalWiring.Renew` (`cmd/lenny-gateway/cred_renewal.go:143-145`), reaches the `Renew` error site but is not distinguished. A transient outage while `now < expiresAt` therefore exhausts the lease. `exhaust` drops the lease's pool binding and emits `credential_pool_exhausted` (`cmd/lenny-gateway/cred_renewal.go:225-234`); the Standard/Basic Fallback Flow it forbids performs checkpoint-and-restart whose replacement pod calls `AssignCredentials` against the same down Token Service, which is the restart loop `spec/04_system-components.md:1470` names.

### (B) No adapter surface moves a direct-mode lease deadline without a re-mint

There is no adapter surface to move a direct-mode lease's expiry deadline without re-delivering credential material. `pkg/adapter/credexpiry.go` only arms, re-arms, cancels, and expires per-provider timers. `armExpiryTimer` early-returns when the `lease_id` is unchanged (`pkg/adapter/credexpiry.go:96-99`), so re-delivering the same lease cannot move its deadline. The only deadline move is `RotateCredentials` delivering a replacement lease, which re-mints via `AssignCredentials`. `schemas/lenny-adapter.proto` carries no expiry-only extension surface.

### (C) Proxy-mode leases have no adapter timer at all

Proxy-mode leases arm no adapter timer (`pkg/adapter/credexpiry.go:89-94`). The gateway LLM Proxy rejects expired proxy requests server-side by resolving the lease from the `credleasestore` and checking `lease.ExpiresAt` (`pkg/gateway/llmproxy/llmproxy/handler.go:319-338`, via `ValidateProxyRequest`). The adapter-timer remedy for (B) cannot cover proxy mode, so a proxy-mode lease under a breaker-open outage needs a distinct extension path.

## 2. Decisions

- **Wire mechanism (CHG-2/CHG-3): a dedicated gateway-initiated `ExtendCredentialLease` RPC on the Adapter service.** It carries only `session_id`, `provider`, `lease_id`, a new `expires_at_unix_ms`, and optional `slot_id`, and re-arms the direct-mode expiry timer to the new deadline without rewriting the credential file or running the §4.7 rebind handshake. `RotateCredentials` is rejected as the carrier on the merits: it rewrites credential material and runs the Full-level lifecycle handshake, and its `armExpiryTimer` path early-returns when `lease_id` is unchanged (`credexpiry.go:96-99`), so it cannot move a live lease's deadline without delivering a new `lease_id`, which is a re-mint against the down Token Service. `ExtendCredentialLease` carries only a timestamp and needs no Token Service call because the gateway already holds the lease record locally. A boolean discriminator on `RotateCredentialsRequest` is rejected because it overloads one RPC with two disjoint behaviors. The `GatewayControl` "lease extension is not on this service" comment (`schemas/lenny-adapter.proto:212-214`) refers to the pod-initiated §8.6 budget path and does not bar a gateway-initiated credential-expiry extension on the Adapter service.
- **Breaker-open signal (CHG-4/CHG-5): a sentinel `credrenewal.ErrRenewInfraUnavailable` defined in the credrenewal package, branched on with `errors.Is` in `Tick`.** The credrenewal package imports nothing from `pkg/gateway/credentials` today, and credassign does not import credrenewal, so the sentinel is defined locally to keep the Worker's `Renewer` abstraction self-contained and unit-testable; the gateway wiring (`credRenewalWiring.Renew`) maps `credassign.ErrTokenServiceUnavailable` to it at the package boundary. An injected breaker-state probe is rejected: the sentinel already reaches the `Renew` error site unwrapped, so no new failure-path plumbing is needed and the Worker stays testable with a fake `Renewer` returning the sentinel.
- **Invariant (CHG-1): extending the adapter-enforced timer past the original `expiresAt` is acceptable because the adapter timer is the enforced lease deadline.** `onLeaseExpired` deletes the provider's credential-file entry when it fires (`credexpiry.go:133-159`, the "key must not outlive the lease" half), so advancing the timer advances the enforced deadline in lockstep and the direct-mode key never outlives the current lease. `renewBeforeBuffer` needs no new Worker field: it is derived as `lease.ExpiresAt - lease.RenewBefore`, matching `spec/04:1181` (`renewBefore = expiresAt - renewBeforeBuffer`). The extension sets new `ExpiresAt = old ExpiresAt + renewBeforeBuffer` and new `RenewBefore = old ExpiresAt`, so the rescheduled renewal fires one buffer later.
- **The extension is applied only when it takes effect at the enforcement point (CHG-4).** `Options.OnExtend` returns an error. `Tick` advances `tl.lease.ExpiresAt = old ExpiresAt + buffer` and `tl.lease.RenewBefore = old ExpiresAt` only when `OnExtend` succeeds; on failure it falls through to the existing `recordFailure`/`exhaust` path so a genuinely unreachable enforcement point still reaches fault rotation rather than diverging gateway scheduling state from the adapter-enforced deadline. The Token Service breaker being open does not imply the per-pod adapter is unreachable (the breaker is central to the Token Service subsystem; the adapter is per-pod), so an adapter-reachable-while-Token-Service-down outage is the common case, but the RPC can still fail independently.
- **Proxy mode (CHG-5): the extension updates the gateway `credleasestore` the LLM Proxy reads, with no adapter round-trip and no Token Service call.** Under breaker-open with `now < expiresAt`, the worker's `OnExtend` callback dispatches by delivery mode in the wiring: a direct-mode lease extends via the `ExtendCredentialLease` RPC to the adapter; a proxy-mode lease extends the lease's `ExpiresAt`/`RenewBefore` directly in the `credleasestore` (the record the proxy's `ValidateProxyRequest` reads, `handler.go:319-338`).
- **Cumulative-TTL extension cap (CHG-1/CHG-4): total extension may not exceed the lease's original `leaseTTLSeconds`.** A permanently-open breaker would otherwise extend a direct-mode credential's usable life without bound. The worker counts consecutive breaker-open extensions on a lease and stops once cumulative extension (extension count times `renewBeforeBuffer`) reaches the original TTL, so a credential's total usable life is capped at its original TTL plus at most one further TTL. Reaching the cap does not drop the lease into the Fallback Flow: that would re-mint against the still-open breaker and reintroduce the restart loop line 1470 forbids (a naive cap delays the loop rather than avoiding it). Instead, at the cap the worker terminates the session by force-transitioning it to the §8.8 `expired` state with the existing `expired:lease` FailureReason (`session.FailureExpiredLease`, the §8.8 reason for a §4.9 credential-lease expiry) and attempts no re-mint; the adapter expiry timer armed at the capped deadline deletes the credential-file entry when it fires, so the key does not outlive the capped lease (the fail-closed teardown). Reusing `expired:lease` keeps the client-facing task error code within the constrained §8.8 `expired:*` set the surfacing contract mandates (`spec/08_recursive-delegation.md:869`) and introduces no new error code, because the existing reason already covers a §4.9 credential-lease expiry (`pkg/api/v1/session/retry_policy.go:256-260`); the terminal teardown is new machinery wired from a `sessionbudget.Terminator` dependency rather than a reuse of an existing credential-failure teardown. A successful renewal after the breaker recovers resets the count, so a transient outage shorter than one `leaseTTLSeconds` never reaches the cap. Both the value and the terminal-at-cap behavior are settled in §4.9 normative text (CHG-1) so the worker counter (CHG-4) rests on a decided contract rather than being invented at implementation time.
- **No new Token Service RPC and no Token Service call on any extension path.** Every extension reuses locally held lease state.
- **Classified `new`.** The spec prose mandates behavior the implementation lacks entirely: a new adapter RPC, a new worker branch, a new sentinel, and a proxy-mode extension path. The §4.9 spec edit (CHG-1) is a reconciliation that names the mechanism and settles the invariant, but the dominant action adds capabilities the code does not have.
- **No coverage-tracker finding id is referenced in spec, code, tests, comments, or commits;** only durable spec sections such as `// spec: §4.9` are cited.

## 3. How the pieces fit at the breaker-open boundary

A tracked lease reaches its `renewBefore` deadline and `Tick` calls `credRenewalWiring.Renew` (`cmd/lenny-gateway/cred_renewal.go:128`). `Renew` calls the §4.9 `AssignCredentials` path (`:143`), which routes through the Token Service breaker (`callTokenService`, `credassign/client.go:140-148`).

When the breaker is closed, `Renew` returns a fresh lease and the worker rotates onto it, unchanged by this proposal. When the breaker is open, `callTokenService` returns `credassign.ErrTokenServiceUnavailable` (`credassign/client.go:145-146`) and `Renew` wraps it as `credrenewal.ErrRenewInfraUnavailable` (CHG-5). In `Tick`, the lease has already passed the `now >= expiresAt` guard at `credrenewal.go:227` (an actually-expired lease still exhausts and falls through to fault rotation, unchanged), so the lease reaching the `Renew` call is still valid. `Tick` recognizes the sentinel with `errors.Is` (CHG-4) and calls `OnExtend` with the new deadline (`old ExpiresAt + renewBeforeBuffer`).

`OnExtend` dispatches by delivery mode (CHG-5). For a direct-mode lease it sends the adapter an `ExtendCredentialLease` RPC (CHG-2/CHG-3), which re-arms the provider's expiry timer to the new deadline without rewriting `/run/lenny/credentials.json` and without running the §4.7 handshake. For a proxy-mode lease it re-`Put`s the lease into the `credleasestore` with the advanced `ExpiresAt`/`RenewBefore`, so the LLM Proxy's server-side check (`handler.go:330-338`) honors the extension.

When `OnExtend` returns nil, `Tick` advances the worker's tracked `ExpiresAt`/`RenewBefore` in lockstep with the enforcement point and reschedules, so the next sweep re-attempts the renewal one buffer later. If the Token Service is still down then, the cycle repeats and the session stays alive on its current credential. When `OnExtend` returns an error (the enforcement point is genuinely unreachable), `Tick` falls through to `recordFailure`/`exhaust`, so the lease still reaches fault rotation rather than silently believing itself valid while the adapter timer fires at the old deadline and deletes the credential file.

## 4. Proposed changes

### CHG-1. Amend the §4.9 Token Service unavailability guard to name the mechanism and settle the deadline-redefinition invariant

**Target:** `spec/04_system-components.md`, the **Token Service unavailability guard** paragraph (`spec/04_system-components.md:1470`) and the §4.7 Gateway → Adapter RPC table (`spec/04_system-components.md:645-663`).

**Anchor and change (spec/04:1470).** The paragraph currently reads:

```
**Token Service unavailability guard.** When the Token Service circuit breaker is open ([Section 4.3](#43-token-service)), the proactive renewal worker MUST NOT trigger the standard Fallback Flow for Standard/Basic-level runtimes. The Fallback Flow for these levels includes checkpoint-and-restart, but the replacement pod also requires `AssignCredentials` (which calls the Token Service), creating a restart loop. Instead, when the Token Service circuit breaker is open and the existing lease has not yet expired (i.e., `now < lease.expiresAt`), the renewal worker extends the adapter-side lease timer by one additional `renewBeforeBuffer` interval and reschedules the renewal attempt. This keeps the session alive on its current (still-valid) credential until the Token Service recovers. The Fallback Flow is triggered only when the credential has actually expired (`now >= lease.expiresAt`) or has been rejected by the upstream provider (`AUTH_EXPIRED`, `RATE_LIMITED`), not when renewal infrastructure is transiently unavailable.
```

Reword to name the concrete extension mechanism for each delivery mode, state the extension arithmetic, and settle the deadline-redefinition invariant, while keeping the MUST-NOT-Fallback rule and the actually-expired carve-out. For example:

```
**Token Service unavailability guard.** When the Token Service circuit breaker is open ([Section 4.3](#43-token-service)), the proactive renewal worker MUST NOT trigger the standard Fallback Flow for Standard/Basic-level runtimes. The Fallback Flow for these levels includes checkpoint-and-restart, but the replacement pod also requires `AssignCredentials` (which calls the Token Service), creating a restart loop. Instead, when the Token Service circuit breaker is open and the existing lease has not yet expired (i.e., `now < lease.expiresAt`), the renewal worker extends the lease deadline by one additional `renewBeforeBuffer` interval and reschedules the renewal attempt: it sets the new `expiresAt` to the old `expiresAt` plus `renewBeforeBuffer` and the new `renewBefore` to the old `expiresAt`, so the next renewal fires one buffer later. The worker extends the enforced deadline through the delivery mode's enforcement point, calling the Token Service on neither path because it reuses the lease record it already holds. In direct delivery mode the worker calls the adapter's `ExtendCredentialLease` RPC, which re-arms the adapter-side expiry timer to the new deadline without re-delivering credential material and without running the credential-rebind handshake; because the adapter expiry timer is the enforced lease deadline (its expiry deletes the provider's credential-file entry), advancing the timer advances the enforced deadline in lockstep, and the direct-mode key never outlives the current lease. In proxy delivery mode the worker extends the lease's `expiresAt` and `renewBefore` in the gateway's own lease store, which the LLM Proxy reads when it rejects expired proxy requests server-side, so no adapter round-trip is required. If the enforcement-point extension fails, the worker does not advance its own view of the deadline; the lease falls through to the retry and Fallback path so gateway scheduling state cannot diverge from the enforced deadline. This keeps the session alive on its current (still-valid) credential until the Token Service recovers. **Bounded extension.** Cumulative extension of a lease MUST NOT exceed its original `leaseTTLSeconds`: the renewal worker counts consecutive breaker-open extensions and stops once cumulative extension (the extension count times `renewBeforeBuffer`) reaches the original TTL, so a permanently-unavailable Token Service cannot keep a credential usable indefinitely and a credential's total usable life is capped at its original TTL plus at most one further TTL of extension. When this cap is reached while the breaker is still open, the renewal worker MUST NOT enter the Fallback Flow (no checkpoint-and-restart and no `AssignCredentials`), because a re-mint against the still-open breaker would re-enter the restart loop this guard prevents; instead it terminates the session by driving it to the terminal `expired` state, which [Section 8.8](08_recursive-delegation.md) surfaces to external clients as a `failed` task carrying the `expired:lease` error code, and the adapter expiry timer armed at the capped deadline deletes the credential-file entry when it fires, so the key does not outlive the capped lease. A successful renewal after the breaker recovers resets the count, so an outage shorter than one `leaseTTLSeconds` never reaches the cap. The Fallback Flow is triggered only when the credential has actually expired before the cap (`now >= lease.expiresAt`) or has been rejected by the upstream provider (`AUTH_EXPIRED`, `RATE_LIMITED`), not when renewal infrastructure is transiently unavailable.
```

**Rationale:** The guard exists only as prose today; the reword names the surfaces the code introduces (the adapter `ExtendCredentialLease` RPC and the gateway lease-store extension), so the spec and the shipped mechanism agree. The deadline-redefinition sentence settles the invariant that advancing the adapter timer past the original `expiresAt` is acceptable precisely because the adapter timer is the enforced deadline. The failure-fall-through sentence records that a genuinely unreachable enforcement point still reaches fault rotation. The **Bounded extension** sentences settle the total-extension bound normatively: cumulative extension is capped at the original `leaseTTLSeconds`, and at the cap the session terminates with a terminal error rather than entering the Fallback Flow, which is what keeps a permanently-open breaker from either extending the key without bound or re-entering the restart loop. Fixing both the value and the terminal-at-cap behavior in the spec is what lets the CHG-4 worker counter rest on a decided contract. The terminal `expired` state the reword names is surfaced to external clients per §8.8 with the existing `expired:lease` error code (`session.FailureExpiredLease`, `pkg/api/v1/session/retry_policy.go:256-260`), which already denotes a §4.9 credential-lease expiry, so the reword names no code the platform does not already define and CHG-5 introduces no new client-facing error code.

**Anchor and change (spec/04:645-663, the §4.7 Gateway → Adapter RPC table).** The §4.7 table is the spec's authoritative enumeration of every Gateway → Adapter RPC; `spec/15_external-api-surface.md:1433` points to it as the RPC table for the proto's service surface (`schemas/lenny-adapter.proto`). CHG-2 adds `ExtendCredentialLease` to the `Adapter` service, so the table gains a matching row directly below the `RotateCredentials` row (`spec/04:660`):

```
| `ExtendCredentialLease`        | Re-arm a still-valid direct-mode credential lease's expiry timer to a later deadline without delivering credential material (§4.9 Token Service unavailability guard) |
```

**Rationale (§4.7 row):** Adding `ExtendCredentialLease` to the proto service without the table row would leave the §4.7 enumeration incomplete and inconsistent with `schemas/lenny-adapter.proto`. The row keeps the spec's authoritative RPC table in step with the wire contract. The RPC is named `ExtendCredentialLease` rather than `ExtendLease` because the bare name `ExtendLease` for a gateway↔adapter gRPC was trimmed under ADR-0014 (F-15.3.6) when the token-budget lease-extension trigger moved into the gateway LLM Proxy as an in-process operation. `tests/tier11_docs/budget_extension_trigger_consistency_test.go:174-177` enforces that decision by failing on the substring `ExtendLease` anywhere in the whole §4.7 section (`spec/04:637-967`), and the same test bans the substring in `docs/reference/adapter-contract.md:230-231` and rejects `lease extension` in §9.1. The name `ExtendCredentialLease` does not contain the `ExtendLease` substring, so the §4.7 row enumerates the new credential-expiry RPC without tripping the ban and without reopening the trimmed §8.6 budget surface. This proposal therefore edits no tier-11 test and adds no tier-11 edit site; the rename is the reconciliation.

### CHG-2. Add the `ExtendCredentialLease` RPC and its request and response messages to the Adapter service

**Target:** `schemas/lenny-adapter.proto`, the `Adapter` service (near `RotateCredentials`, `schemas/lenny-adapter.proto:85-90`; the `RotateCredentialsRequest` message, `:959-974`); regenerated `pkg/proto/adapter/v1`.

**Change.** Add an `ExtendCredentialLease` RPC to the `Adapter` service next to `RotateCredentials` (`:90`):

```
  // ExtendCredentialLease re-arms the direct-mode credential-expiry timer for a
  // still-valid lease to a later deadline, without delivering credential
  // material and without running the credentials_rotated handshake.
  // Gateway-initiated; used by the §4.9 Token Service unavailability
  // guard when the Token Service circuit breaker is open and the lease
  // has not yet expired. Requires no Token Service call: the gateway
  // supplies the new deadline from the lease record it already holds.
  // spec: §4.9 line 1470.
  rpc ExtendCredentialLease(ExtendCredentialLeaseRequest) returns (ExtendCredentialLeaseResponse) {}
```

Add the request and response messages near `RotateCredentialsRequest`:

```
message ExtendCredentialLeaseRequest {
  SessionId session_id = 1;
  string provider = 2;
  string lease_id = 3;
  // expires_at_unix_ms is the new, later expiry deadline the adapter
  // re-arms the direct-mode expiry timer to. spec: §4.9 line 1470.
  int64 expires_at_unix_ms = 4;
  // slot_id, when set, extends this slot's §6.1 per-slot credential
  // lease timer independently of sibling slots. Empty when
  // `maxConcurrentSessions: 1`. spec: §6.1 line 28.
  SlotId slot_id = 5;
}

message ExtendCredentialLeaseResponse {}
```

`slot_id` mirrors `RotateCredentialsRequest.slot_id` (`:973`) for the §6.1 per-slot credential file. Regenerate the Go stubs (`pkg/proto/adapter/v1`).

**Rationale:** No adapter wire surface moves a direct-mode lease's expiry deadline without a re-mint. `RotateCredentials` cannot be reused: it rewrites material, runs the §4.7 handshake, and `armExpiryTimer` early-returns on an unchanged `lease_id`. `ExtendCredentialLease` is a timestamp-only surface, gateway-initiated on the Adapter service where the gateway already initiates every RPC. The RPC is deliberately named `ExtendCredentialLease` and not `ExtendLease`: the bare name for a gateway↔adapter gRPC was trimmed under ADR-0014 (F-15.3.6), and `tests/tier11_docs/budget_extension_trigger_consistency_test.go:174-177` fails on the substring `ExtendLease` anywhere in spec §4.7. A credential-specific name keeps the new RPC distinct on the wire from the removed §8.6 token-budget surface and lets the §4.7 table enumerate it (CHG-1). Every request, response, method, client stub, and per-slot helper name carries the same `ExtendCredentialLease`/`extendCredentialLeaseSlot` form so the wire contract, the SDKs, and the tests agree.

### CHG-3. Implement `Server.ExtendCredentialLease` in the adapter to re-arm a direct-mode expiry timer without rewriting credential material

**Target:** `pkg/adapter/credexpiry.go` (a new `extendExpiryTimer` helper near `armExpiryTimer` `:83-105`); `pkg/adapter/credentials.go` (the `ExtendCredentialLease` RPC handler, near `RotateCredentials` `:108-123`); `pkg/adapter/slotcreds.go` (the per-slot analogue, near `rotateCredentialsSlot` `:61`).

**Change (timer helper).** Add `extendExpiryTimer` holding `s.mu`:

```
// extendExpiryTimer re-arms the direct-mode expiry timer for one
// provider to a later deadline, without rewriting the credential file
// and without touching s.credLeases. It is the §4.9 Token Service
// unavailability guard's direct-mode enforcement point. If no timer is
// armed for the provider, or its lease id differs from leaseID (the
// lease was replaced or is proxy-mode with no timer), it is a no-op.
// Callers hold s.mu.
func (s *Server) extendExpiryTimer(provider, leaseID string, newExpiresAt time.Time) {
	existing, ok := s.expiryTimers[provider]
	if !ok || existing.leaseID != leaseID {
		return
	}
	existing.handle.Stop()
	delay := newExpiresAt.Sub(s.expiryClockNow())
	handle := s.expiryAfter(delay, func() { s.onLeaseExpired(provider, leaseID) })
	s.expiryTimers[provider] = &expiryTimer{leaseID: leaseID, handle: handle}
}
```

The helper does not call `writeCredentialFile` and does not touch `s.credLeases`, so no material is re-delivered. The re-armed timer still targets `onLeaseExpired(provider, leaseID)`, so a later expiry deletes the provider's credential-file entry exactly as before, at the extended deadline.

**Change (RPC handler).** Add `Server.ExtendCredentialLease` mirroring `RotateCredentials`'s session-id guard (`credentials.go:110-112`):

```
func (s *Server) ExtendCredentialLease(ctx context.Context, req *adapterv1.ExtendCredentialLeaseRequest) (*adapterv1.ExtendCredentialLeaseResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "ExtendCredentialLease requires a session id")
	}
	// spec: §4.9 line 1470 — expiry-timer extension only; no material
	// re-delivered, no Token Service call.
	newExpiresAt := time.UnixMilli(req.GetExpiresAtUnixMs())
	if slotID := req.GetSlotId().GetValue(); s.useSlot(slotID) {
		return s.extendCredentialLeaseSlot(slotID, req.GetProvider(), req.GetLeaseId(), newExpiresAt)
	}
	s.mu.Lock()
	s.extendExpiryTimer(req.GetProvider(), req.GetLeaseId(), newExpiresAt)
	s.mu.Unlock()
	return &adapterv1.ExtendCredentialLeaseResponse{}, nil
}
```

Add `extendCredentialLeaseSlot` in `pkg/adapter/slotcreds.go` mirroring `rotateCredentialsSlot`'s per-slot dispatch (`slotcreds.go:61`), re-arming the slot's own expiry timer to `newExpiresAt` without rewriting the slot credential file.

**Rationale:** `armExpiryTimer` early-returns when `existing.leaseID == leaseID` (`credexpiry.go:96-99`), so re-delivering the same lease cannot move its deadline; a dedicated extend path is required. The extension must not rewrite the credential file (no new material) and must not run the §4.7 rebind handshake.

### CHG-4. Add a breaker-open hold-and-reschedule branch to `credrenewal.Worker`

**Target:** `pkg/gateway/credentials/credrenewal/credrenewal.go` (the `ErrRenewInfraUnavailable` sentinel; `DefaultMaxExtensions` and the `maxExtensions` helper; `Options.OnExtend` and `Options.OnExtensionCapReached`; the `Worker.onExtend`/`onExtensionCapReached` fields; the `Lease.LeaseTTL` field; the `trackedLease.extensions` counter; the `Tick` renewal branch `:231-246`).

**Change.** Define the sentinel and the `OnExtend` option, and add the breaker-open branch to `Tick`.

Add the exported sentinel near `MaxRenewalRetries` (`:29`):

```
// ErrRenewInfraUnavailable signals that a renewal could not proceed
// because the renewal infrastructure (the §4.3 Token Service circuit
// breaker) is transiently open, not because the lease's credential is
// bad. Per the §4.9 Token Service unavailability guard, a lease failing
// with this error while it has not yet expired is held and rescheduled,
// not exhausted into the Fallback Flow. The gateway wiring maps
// credassign.ErrTokenServiceUnavailable to this sentinel at the package
// boundary so credrenewal need not import credassign.
// spec: §4.9 line 1470.
var ErrRenewInfraUnavailable = errors.New("credrenewal: renewal infrastructure transiently unavailable")
```

Add to `Options` (near `OnExhausted` `:73`):

```
	// OnExtend, when set, is called under the §4.9 Token Service
	// unavailability guard to extend a still-valid lease's enforced
	// deadline to newExpiresAt through the delivery mode's enforcement
	// point (the adapter expiry timer for direct mode, the gateway lease
	// store for proxy mode). It returns an error when the enforcement
	// point could not be reached, in which case the worker does not
	// advance its own view of the deadline and the lease falls through to
	// the retry and Fallback path. spec: §4.9 line 1470.
	OnExtend func(lease Lease, newExpiresAt time.Time) error

	// OnExtensionCapReached, when set, is called under the §4.9 Token
	// Service unavailability guard when a lease's cumulative breaker-open
	// extension has reached its original leaseTTLSeconds and the breaker
	// is still open. The gateway wires it to a terminal session teardown
	// that drives the session to the §8.8 expired state (surfaced to
	// clients as expired:lease) and does NOT enter the Fallback Flow,
	// because a re-mint against the still-open breaker would re-enter
	// the restart loop the guard prevents. The worker
	// drops the lease from tracking before invoking it. spec: §4.9 line
	// 1470.
	OnExtensionCapReached func(lease Lease)
```

Store `OnExtend` and `OnExtensionCapReached` as `onExtend func(Lease, time.Time) error` and `onExtensionCapReached func(Lease)` fields on `Worker`, wired in `New` alongside `onExhausted`. Add a `LeaseTTL time.Duration` field to the `Lease` struct (the lease's original TTL, populated by the wiring from the pool's `leaseTTLSeconds`) and an `extensions int` counter to `trackedLease` (consecutive breaker-open extensions of that lease). Define `DefaultMaxExtensions` near `MaxRenewalRetries` (`:29`) as the fallback used when a lease carries no `LeaseTTL`, and a helper `maxExtensions(ttl, buffer time.Duration) int` returning `int(ttl / buffer)` (floored at 1) when both are positive and `DefaultMaxExtensions` otherwise. Because `buffer = ExpiresAt - RenewBefore` is invariant across extensions (each extension sets `RenewBefore = old ExpiresAt` and `ExpiresAt = old + buffer`), `maxExtensions` is stable for the life of the lease, so the cap is exactly the number of `renewBeforeBuffer` intervals that fit in one `leaseTTLSeconds`.

In `Tick`'s renewal loop (`:231-237`), branch on the sentinel before the uniform failure path:

```
		next, err := w.renewer.Renew(ctx, tl.lease)
		if err != nil {
			// §4.9 Token Service unavailability guard: the breaker is open
			// and the lease is still valid (the now >= ExpiresAt guard at
			// line 227 already handled the expired case). Extend the
			// enforced deadline by one renewBeforeBuffer and reschedule
			// instead of exhausting into the Fallback Flow.
			if errors.Is(err, ErrRenewInfraUnavailable) && w.onExtend != nil {
				buffer := tl.lease.ExpiresAt.Sub(tl.lease.RenewBefore)
				// §4.9 cumulative-extension cap: once total extension
				// reaches the lease's original TTL, stop extending. A
				// permanently-open breaker must not keep the key alive
				// forever. At the cap, terminate the session without
				// re-minting against the still-open breaker (which would
				// loop) rather than dropping into the Fallback Flow.
				if tl.extensions >= maxExtensions(tl.lease.LeaseTTL, buffer) {
					if w.onExtensionCapReached != nil {
						w.mu.Lock()
						delete(w.tracked, tl.lease.LeaseID)
						w.mu.Unlock()
						w.onExtensionCapReached(tl.lease)
						continue
					}
					// No terminal callback wired (a narrow unit-test
					// worker): fall through to recordFailure/exhaust rather
					// than extend past the cap.
				} else {
					newExpiresAt := tl.lease.ExpiresAt.Add(buffer)
					if extErr := w.onExtend(tl.lease, newExpiresAt); extErr == nil {
						w.mu.Lock()
						tl.lease.RenewBefore = tl.lease.ExpiresAt
						tl.lease.ExpiresAt = newExpiresAt
						tl.extensions++
						tl.retries = 0
						w.mu.Unlock()
						continue
					}
					// The enforcement point was unreachable; fall through.
				}
			}
			if w.recordFailure(tl) {
				w.exhaust(tl.lease)
			}
			continue
		}
```

The extension advances `tl.lease.ExpiresAt`/`RenewBefore` only when `OnExtend` succeeds; on failure it falls through to the existing `recordFailure`/`exhaust` path so a genuinely unreachable enforcement point still reaches fault rotation rather than silently diverging gateway scheduling state from the adapter-enforced deadline. `renewBeforeBuffer` is derived as `ExpiresAt - RenewBefore` (matching `spec/04:1181`); no new Worker field is needed. The branch does not re-check `now.Before(tl.lease.ExpiresAt)` because line 227 already exhausts any lease with `now >= ExpiresAt` before the `Renew` call. The branch applies uniformly across runtime levels; although `spec/04:1470` scopes the guard prose to Standard/Basic-level runtimes, a level-independent branch aligns with the no-tier-dependent-code-paths principle and is a superset of the required behavior.

The cumulative-extension cap bounds total extension at the lease's original `leaseTTLSeconds`. `tl.extensions` counts consecutive breaker-open extensions and is incremented only on a successful `OnExtend`; a successful normal renewal replaces the tracked lease with a fresh `trackedLease` whose count is zero, so the cap resets when the breaker recovers and a transient outage shorter than one TTL never reaches it. When `tl.extensions` reaches `maxExtensions(LeaseTTL, buffer)` with the breaker still open, the worker drops the lease from tracking and calls `OnExtensionCapReached` rather than `OnExtend`; the gateway wires that callback to a terminal session teardown that drives the session to the §8.8 `expired` state (surfaced to clients as `expired:lease`) with no re-mint, so the cap does not re-enter the Fallback Flow's restart loop. The adapter expiry timer, still armed at the capped deadline, deletes the credential file when it fires, preserving the key-must-not-outlive-the-lease invariant even if the terminal teardown races the deadline. When `OnExtensionCapReached` is unset (a worker constructed without it, e.g. in a narrow unit test), the cap branch is skipped and the lease follows the existing `recordFailure`/`exhaust` path, so the cap is a no-op rather than a silent hang.

**Rationale:** `Tick` collapses every `Renew` error into `recordFailure`/`exhaust` (`credrenewal.go:231-236`). The worker must distinguish a transient breaker-open failure while `now < ExpiresAt` and, instead of exhausting, extend the enforced deadline and reschedule. Making `OnExtend` return an error and advancing the tracked deadline only on success keeps the worker's scheduling state consistent with the enforcement point: if the adapter `ExtendCredentialLease` RPC fails, the adapter timer still fires at the old `expiresAt` and deletes the credential file, so the worker must not believe the lease is valid until the extended deadline.

### CHG-5. Wire the breaker-open sentinel mapping and the delivery-mode-dispatching `OnExtend` hook into the gateway

**Target:** `cmd/lenny-gateway/cred_renewal.go` (map `credassign.ErrTokenServiceUnavailable` to `credrenewal.ErrRenewInfraUnavailable` in `Renew` `:143-145`; add `onExtend`; add a `credleasestore.LeaseStore` dependency to `credRenewalWiring` `:62-82`); `cmd/lenny-gateway/revocation.go` (pass `OnExtend` into `credrenewal.New` Options `:131-144`; wire the wiring's lease store to the same `w.llmLeases` instance the LLM Proxy reads, `stores.go:1535-1612`); `pkg/gateway/runtime/adapterclient/client.go` (add `Client.ExtendCredentialLease` near `RotateCredentials` `:213-220`). The proxy-mode extension reuses the existing `LeaseStore` `GetByID`/`Put` methods, which both the in-memory `credleasestore.Store` and the Postgres-backed `pgstore.Store` implement, so no store package is edited.

**Change (sentinel mapping).** In `credRenewalWiring.Renew`, wrap the assign error at the boundary (`cred_renewal.go:143-145`):

```
	next, err := w.assign.Assign(rp.pool, lease.SessionID, "", rp.tenantID)
	if err != nil {
		if errors.Is(err, credassign.ErrTokenServiceUnavailable) {
			// §4.9 Token Service unavailability guard: surface the
			// breaker-open failure as the credrenewal sentinel so the
			// worker holds and reschedules the still-valid lease instead
			// of exhausting it. The underlying cause is preserved.
			return credrenewal.Lease{}, fmt.Errorf("%w: %w", credrenewal.ErrRenewInfraUnavailable, err)
		}
		return credrenewal.Lease{}, err
	}
```

The mapping lives at this boundary so credrenewal need not import credassign.

**Change (`OnExtend` dispatch).** Add `credRenewalWiring.onExtend` dispatching by delivery mode:

```
// onExtend is the §4.9 Token Service unavailability guard's extension
// hook. It extends a still-valid lease's enforced deadline to
// newExpiresAt without any Token Service call, dispatching by delivery
// mode: a direct-mode lease re-arms the adapter expiry timer over
// ExtendCredentialLease; a proxy-mode lease's ExpiresAt/RenewBefore is advanced in
// the gateway lease store the LLM Proxy reads. spec: §4.9 line 1470.
func (w *credRenewalWiring) onExtend(lease credrenewal.Lease, newExpiresAt time.Time) error {
	if w == nil {
		return nil
	}
	rec, ok := w.leases.GetByID(lease.LeaseID)
	if !ok {
		return fmt.Errorf("§4.9 extend: lease %s not in store", lease.LeaseID)
	}
	newRenewBefore := lease.ExpiresAt // the pre-extension expiry
	switch rec.DeliveryMode {
	case credential.DeliveryProxy:
		rec.ExpiresAt = newExpiresAt
		rec.RenewBefore = newRenewBefore
		return w.leases.Put(rec) // both backends implement Put on the LeaseStore interface
	default: // direct
		w.mu.Lock()
		rp := w.pools[lease.LeaseID]
		w.mu.Unlock()
		bind, ok := w.registry.Get(lease.SessionID)
		if !ok || bind.Adapter == nil {
			return fmt.Errorf("§4.9 extend: no pod binding for session %s", lease.SessionID)
		}
		ctx, cancel := context.WithTimeout(context.Background(), credRotateRPCTimeout)
		defer cancel()
		return bind.Adapter.ExtendCredentialLease(ctx, lease.SessionID, rp.provider, lease.LeaseID, newExpiresAt)
	}
}
```

This adds a `leases credleasestore.LeaseStore` dependency to `credRenewalWiring`, wired in `revocation.go` to the same `w.llmLeases` instance the LLM Proxy reads and the credassign service mirrors leases into (`stores.go:1535-1612`). The field is typed as the `LeaseStore` interface rather than the concrete in-memory `*credleasestore.Store`, because `w.llmLeases` is swapped to the Postgres-backed `pgstore.Store` whenever Postgres is configured (`stores.go:1540-1544`, the durable Postgres-backed deployment); the concrete type cannot hold the pgstore instance, and a proxy-mode extension written to a different in-memory store than the proxy reads would leave the durable record at its original deadline, so the LLM Proxy's server-side check rejects the still-valid proxy request with `LEASE_EXPIRED`. Both the delivery-mode read (`GetByID`) and the proxy-mode advance (`Put`) go through `LeaseStore` interface methods that both backends implement, so no new store method and no type-assert is introduced. `newRenewBefore` is the pre-extension `ExpiresAt` the worker passed the lease with, matching CHG-4's arithmetic.

**Change (adapter client).** Add `Client.ExtendCredentialLease` mirroring `RotateCredentials` (`adapterclient/client.go:213-220`):

```
// ExtendCredentialLease re-arms the direct-mode credential-expiry timer for a
// still-valid lease to a later deadline, without delivering material.
// spec: §4.9 line 1470.
func (c *Client) ExtendCredentialLease(ctx context.Context, sessionID, provider, leaseID string, newExpiresAt time.Time) error {
	_, err := c.rpc.ExtendCredentialLease(ctx, &adapterv1.ExtendCredentialLeaseRequest{
		SessionId:       &adapterv1.SessionId{Value: sessionID},
		Provider:        provider,
		LeaseId:         leaseID,
		ExpiresAtUnixMs: newExpiresAt.UnixMilli(),
	})
	return err
}
```

Add `ExtendCredentialLease` to the adapter-client interface the `podsession.Registry` binding exposes (`bind.Adapter`) alongside `RotateCredentials`.

**Change (lease store).** The proxy-mode extension reuses the existing `LeaseStore` interface methods rather than adding a new store method. The lease store the LLM Proxy reads is `w.llmLeases`, typed as the `credleasestore.LeaseStore` interface and swapped to the Postgres-backed `pgstore.Store` whenever Postgres is configured (`stores.go:1535-1544`, the durable Postgres-backed deployment). Both `GetByID` and `Put` are on that interface and are implemented by the in-memory `credleasestore.Store` (`credleasestore.go:95`, `:62`) and the Postgres-backed `pgstore.Store` (`pgstore.go:181`, `:129`), so the wiring resolves the record with `GetByID`, advances `ExpiresAt`/`RenewBefore` on the returned value, and writes it back with `Put`, working identically against whichever backend the proxy reads:

```
	rec.ExpiresAt = newExpiresAt
	rec.RenewBefore = newRenewBefore
	return w.leases.Put(rec)
```

No new store surface is added because the existing interface already covers the advance. `pgstore.Store.Put` is an `INSERT ... ON CONFLICT (lease_id) DO UPDATE` that re-seals the envelope-encrypted lease body (`pgstore.go:129-163`), so the durable record the proxy resolves against carries the advanced deadline; a plain-column `UPDATE` is not available because the lease body (including `expiresAt`/`renewBefore`) is stored as a single envelope-encrypted blob rather than as separate columns. The renewal worker holds the tracked lease exclusively and is the only writer of its deadline while the breaker is open, so the `GetByID`-then-`Put` window has no competing writer. See §9 (OD-2).

**Change (options wiring and the new terminal teardown).** In `revocation.go` where `credrenewal.New` is constructed with `OnExhausted` (`revocation.go:131-144`), also set `OnExtend: credRenewal.onExtend` and `OnExtensionCapReached: credRenewal.onExtensionCapReached`. The terminal teardown the cap routes to is new machinery, because no existing credential-renewal path emits a terminal credential error: `credRenewalWiring.onExhausted` (`cred_renewal.go:225-234`) only drops the pool binding and emits `credential_pool_exhausted`, and the §4.9 fall-through it composes with is the standard Fallback Flow (re-mint), which is the path the cap must not take. This proposal therefore adds a new `onExtensionCapReached` method on `credRenewalWiring` and a new session-terminator dependency to the wiring: a `sessionbudget.Terminator` field (`pkg/gateway/session/sessionbudget/sessionbudget.go:86`, the interface whose `TerminateSession(sessionID, reason)` the gateway's `budgetSessionTerminator` already satisfies, `cmd/lenny-gateway/session_budget.go:44`), wired from `w.budgetTerminator` in `revocation.go`. `onExtensionCapReached` resolves the session from the capped `Lease`'s `SessionID` and calls `TerminateSession(lease.SessionID, string(session.FailureExpiredLease))`, force-transitioning the session to its terminal `expired` state with the existing `expired:lease` §8.8 `FailureReason` (`pkg/api/v1/session/retry_policy.go:256-260`, which already denotes a §4.9 credential-lease expiry). `budgetSessionTerminator.TerminateSession` always forces the session to `expired` and stores the reason verbatim as the §8.8 `FailureReason` (`cmd/lenny-gateway/session_budget.go:74-76`), which the §8.8 MCP adapter surfaces verbatim as the `failed` task's error code (`pkg/gateway/mcpfabric/mcptools/mcptools.go:1696-1707`); §8.8 requires an `expired`-state session's code to carry the `expired:*` prefix (`spec/08_recursive-delegation.md:869`), so a non-prefixed code such as `CREDENTIAL_RENEWAL_UNAVAILABLE` cannot be the teardown reason, and the existing `expired:lease` reason is both the correct cause and prefix-conformant. It does NOT invoke the Fallback Flow, so the cap tears the session down without re-minting against the still-open breaker. Distinguishing the two callbacks at the wiring is the essential difference: `OnExhausted` is the fault-rotation fall-through (re-mint), `OnExtensionCapReached` is the new terminal teardown (no re-mint).

**Change (no new error code).** The cap teardown reuses the existing `expired:lease` §8.8 `FailureReason` and introduces no new client-facing error code. The §8.8 task error code and the §15.4 REST error-catalog code are distinct surfaces: the cap teardown emits on the former (the session `FailureReason` the MCP adapter surfaces verbatim as the task error code), which is constrained to the `expired:*` set (`pkg/api/v1/session/retry_policy.go:240-271`, `spec/08_recursive-delegation.md:869`), and `expired:lease` already covers a §4.9 credential-lease expiry. No §15.4 row, `errorclassify` entry, or docs error-catalog row is added, because the sibling `expired:*` reasons (`expired:budget`, `expired:deadline`, `expired:idle`) likewise carry no `errorclassify` table entry and resolve through the shared classifier's default path (`errorclassify.go:44-48`); the cap teardown inherits that established §8.8 behavior rather than inventing a parallel code.

**Change (LeaseTTL population).** Populate the new `LeaseTTL` field on both sites that place a lease into worker tracking, so the cap is TTL-derived across a normal renewal:

- The `OnAssigned`/track path that constructs the initial `credrenewal.Lease` (`cred_renewal.go:114-120`) sets `LeaseTTL` from the assigned lease's own lifetime, `lease.ExpiresAt.Sub(lease.IssuedAt)` (the `credential.Lease` carries `IssuedAt` and `ExpiresAt`, `pkg/credential/lease.go:130-159`, so no separate `leaseTTLSeconds` lookup is needed; this equals `min(leaseTTLSeconds, providerMaxTTL)` per `spec/04:1145`).
- The `credRenewalWiring.Renew` return (`cred_renewal.go:153-159`) sets `LeaseTTL` on the replacement lease from `next.ExpiresAt.Sub(next.IssuedAt)`. This is required because `Tick`'s success path replaces the tracked entry with the `Renew` return value directly (`credrenewal.go:238-240`: `w.tracked[next.LeaseID] = &trackedLease{lease: next}`), so a renewed lease whose return struct carried no `LeaseTTL` would reset the cap to `DefaultMaxExtensions` rather than the TTL-derived `N`, defeating the reset-to-full-budget behavior the §6 cap test asserts. Populating `LeaseTTL` on the `Renew` return makes the reset grant the renewed lease's own TTL-derived budget.

A lease that reaches tracking without a positive `LeaseTTL` falls back to `DefaultMaxExtensions`.

**Rationale:** `credRenewalWiring.Renew` returns the assign error raw (`cred_renewal.go:143-145`), so the mapping belongs at that boundary to keep credrenewal free of a credassign import. `OnExtend` must dispatch by delivery mode: direct leases go to the adapter over `ExtendCredentialLease`; proxy leases are extended in the gateway `credleasestore` the LLM Proxy reads.

### CHG-6. Tests: worker holds-and-reschedules under breaker-open; adapter `extendExpiryTimer` moves the deadline without rewriting material; delivery-mode dispatch

**Target:** `pkg/gateway/credentials/credrenewal/credrenewal_test.go`; `pkg/adapter/credexpiry_test.go`; a `cmd/lenny-gateway` credential-renewal wiring test.

**Change (worker test, reuse the existing placeholder).** Un-skip and adapt `TestTickHoldsLeaseWhenTokenServiceBreakerOpen_spec_4_9` (`credrenewal_test.go:242-270`). Remove the `t.Skip` (`:243`). Rewire the stand-in error so the `fakeRenewer` returns `credrenewal.ErrRenewInfraUnavailable` (retiring the local `errTokenServiceBreakerOpen` at `:222` or repointing it at the exported sentinel), because the placeholder's `errors.New` value (`:222`) does not satisfy `errors.Is` against the new sentinel. Provide an `OnExtend` that records its call and returns nil, and add assertions: `OnExtend` is called once with `newExpiresAt == ExpiresAt + (ExpiresAt - RenewBefore)`, the tracked lease's `RenewBefore` is advanced to the old `ExpiresAt`, retries are not incremented, no `OnExhausted` fires across `MaxRenewalRetries+2` ticks, and `Tracked() == 1`. Do not add a separate parallel test. Do not add an ordinary-error control case: `TestTickRetriesFailedRenewalThenExhausts` (`:133-153`) already drives a non-sentinel error to exhaustion at `MaxRenewalRetries`.

Add one net-new assertion in the same file for the enforcement-point-failure path: an `OnExtend` returning a non-nil error leaves the worker on the uniform `recordFailure`/`exhaust` path, so after `MaxRenewalRetries` breaker-open ticks the lease is exhausted (the deadline never advances). `// spec: §4.9 (Token Service unavailability guard; extension applied only when the enforcement point is reachable).`

**Change (adapter test, net-new).** In `pkg/adapter/credexpiry_test.go`, using the existing `fakeExpiryClock`/`ExpiryAfterFunc` seam (`:82-83`) and the `fileProviders` credential-file reader (`:111-123`): assign a direct-mode lease, capture its armed timer, call `ExtendCredentialLease` (or `extendExpiryTimer`) with a later deadline, and assert the timer fires at the new deadline rather than the old one, that the credential file is unchanged at extension time (no material rewrite, `fileProviders` still reports the provider), and that when the extended timer fires `onLeaseExpired` still deletes the provider entry. Assert the no-op paths: an `ExtendCredentialLease` for an absent provider, and for a provider whose current `leaseID` differs, leaves the armed timer untouched. Assert a proxy-mode lease (which arms no timer) is a no-op. This single-slot case carries `// spec: §4.9 (adapter ExtendCredentialLease re-arms the direct-mode timer without rewriting material). // diagnosis: a failure means the adapter either did not move the enforced deadline or re-delivered credential material on an extension.`

**Change (adapter per-slot test, net-new).** Add a per-slot case (in `pkg/adapter/credexpiry_test.go` or an adjacent `slotcreds` test) that exercises the `extendCredentialLeaseSlot` dispatch CHG-3 adds. With `maxConcurrentSessions > 1`, assign a direct-mode lease to a slot, call `ExtendCredentialLease` with `slot_id` set, and assert the slot's own `st.timers` entry re-arms to the new deadline (fires at the new deadline rather than the old one) while a sibling slot's timer is left untouched, establishing the §6.1 isolation invariant that `onSlotLeaseExpired` scopes deletion to the slot (`slotcreds.go:195`). Assert the slot no-op paths: an `ExtendCredentialLease` for a slot with an absent provider, and for a slot provider whose current `leaseID` differs, leaves the slot timer untouched. `// spec: §4.9, §6.1 (per-slot ExtendCredentialLease re-arms one slot's timer, sibling slots unaffected). // diagnosis: a failure means the slot dispatch re-armed the wrong slot or broke slot isolation.`

**Change (wiring test, net-new).** In a `cmd/lenny-gateway` wiring test (mirroring the established pattern, e.g. `leaseextendseam_wiring_test.go`), assert `credRenewalWiring.onExtend` dispatches by delivery mode: a direct-mode lease drives `bind.Adapter.ExtendCredentialLease` with the new deadline and does not touch the lease store; a proxy-mode lease advances the stored record's `ExpiresAt`/`RenewBefore` through `GetByID` + `Put` and sends no adapter RPC; and `Renew` wraps `credassign.ErrTokenServiceUnavailable` as `credrenewal.ErrRenewInfraUnavailable` (`errors.Is` matches, the cause is preserved). Run the proxy-mode case against a `credRenewalWiring.leases` typed as the `credleasestore.LeaseStore` interface and backed by the Postgres-backed `pgstore.Store` (the durable topology `w.llmLeases` is swapped to when Postgres is configured), so the extension is verified against the backend the proxy actually reads in a durable deployment and cannot regress by writing to a store the proxy does not consult. Also assert the two `LeaseTTL` population sites CHG-5 adds, because the TTL-derived cap depends on the wiring stamping `LeaseTTL` and the worker unit test cannot observe the wiring: the `credRenewalWiring.Renew` return carries `LeaseTTL == next.ExpiresAt.Sub(next.IssuedAt)` for a breaker-closed renewal, and the `OnAssigned`/track path (`Track`) records the lease with `LeaseTTL == lease.ExpiresAt.Sub(lease.IssuedAt)`. Without these assertions a wiring regression that omits `LeaseTTL` on the `Renew` return silently degrades the post-renewal cap from the TTL-derived `N` to `DefaultMaxExtensions`, letting a direct-mode key stay usable beyond the one-TTL grace bound the §4.9 **Bounded extension** text sets, and no worker unit test would catch it because that test populates `LeaseTTL` on its own fake `Renewer`. `// spec: §4.9 (breaker-open sentinel mapping, delivery-mode extension dispatch, and TTL-derived cap population at the wiring). // diagnosis: a failure means the breaker-open sentinel is not recognized, the extension went to the wrong enforcement point for the lease's delivery mode, or the wiring did not stamp the lease's TTL so the cap silently fell back to DefaultMaxExtensions.`

**Rationale:** The behavior is genuinely new and the seams it relies on all exist, so test work is warranted. The worker test surface is not greenfield: a skipped placeholder for exactly this behavior already exists, so the correct move is to un-skip and adapt it rather than add a parallel test and leave dead code. The adapter `extendExpiryTimer` test and the delivery-mode dispatch wiring test have no existing equivalent and are net-new.

## 5. Non-goals

- **No change to the Fallback Flow, the §4.3 breaker configuration, or the actually-expired path.** A lease with `now >= expiresAt` still falls through to fault rotation with `CREDENTIAL_RENEWAL_FAILED`, unchanged.
- **No new Token Service RPC and no Token Service call on any extension path.** Every extension path is deliberately Token-Service-free.
- **No change to `maxRotationsPerSession` accounting.** A breaker-open extension is neither a proactive renewal completion nor a fault rotation and consumes no rotation budget.
- **No rework of Full-level `hotRotation` credential rebinding.** `ExtendCredentialLease` is timer-only and delivers no material, so the runtime never rebinds on an extension regardless of `hotRotation`.
- **No per-pool configurable extension cap knob.** The cap is derived from the lease's own original `leaseTTLSeconds` (cumulative extension may not exceed one TTL); no separate per-pool cap-tuning field is introduced. `renewBeforeBuffer` remains derived from the lease record.
- **No new session-teardown state machine.** The cap's terminal teardown reuses the existing `sessionbudget.Terminator` interface and the gateway's `budgetSessionTerminator` to force the session terminal (CHG-5); it introduces no new terminal-state transition. It does add new wiring: the `credRenewalWiring.onExtensionCapReached` method and a `sessionbudget.Terminator` dependency on the wiring. The teardown reuses the existing `expired:lease` §8.8 FailureReason and adds no new client-facing error code. This proposal does not modify the Fallback Flow, which the cap deliberately avoids.
- **No reconciliation of the pre-existing §8.6 delegation-tree `ExtendLease`** (`pkg/gateway/mcpfabric/delegationtree/leasecontrol`), which is an unrelated gateway-internal token-budget surface. The new adapter `ExtendCredentialLease` RPC is distinct from it, and its distinct name is what keeps the two apart on the wire and in the tier-11 doc-consistency ban (see CHG-2).

## 6. Testing

The change reaches tier 0 and tier 1 for the touched packages plus higher tiers per `.claude/rules/test-coverage.md`: tier 0 for the proto and codegen, tier 1 for the worker branch and the adapter timer extension, tier 3 for the `ExtendCredentialLease` wire contract, tier 4 for the gateway-to-adapter extension flow, tier 8 for the transient Token Service outage and recovery, and tier 10 for the adapter conformance. Each test pins one behavior the change introduces and asserts the non-happy path.

- **tier-1 worker breaker-open hold-and-reschedule (spec-named-failure path, CHG-4):** In `pkg/gateway/credentials/credrenewal/credrenewal_test.go`, the un-skipped `TestTickHoldsLeaseWhenTokenServiceBreakerOpen_spec_4_9` asserts that a `Renewer` failing with `credrenewal.ErrRenewInfraUnavailable` on a still-valid lease (`now < ExpiresAt`) across `MaxRenewalRetries+2` ticks never exhausts the lease, calls `OnExtend` once with `newExpiresAt == ExpiresAt + (ExpiresAt - RenewBefore)`, advances `RenewBefore` to the old `ExpiresAt`, and leaves `Tracked() == 1`. The non-happy path is a breaker-open failure exhausted into the Fallback Flow, the restart loop §4.9 forbids. `// spec: §4.9 (Token Service unavailability guard).`
- **tier-1 worker extension-failure fall-through (error path, CHG-4):** In the same file, an `OnExtend` returning a non-nil error leaves the worker on the uniform `recordFailure`/`exhaust` path, so a still-valid lease under a genuinely unreachable enforcement point is exhausted after `MaxRenewalRetries` and its tracked deadline never advances. The non-happy path is a worker that advances its own deadline while the adapter timer fires at the old `expiresAt` and deletes the credential file, believing the lease valid past its enforced deadline. `// spec: §4.9 (extension applied only when the enforcement point is reachable).`
- **tier-1 worker cumulative-TTL cap and terminal teardown (spec-named-failure path, CHG-4):** In the same file, drive a lease with a `LeaseTTL` of `N * buffer` under a breaker-open `Renewer` and assert `OnExtend` is called exactly `N` times (the deadline advancing by one `buffer` each time), then on the `N+1`th sweep `OnExtend` is not called, `OnExtensionCapReached` fires exactly once with the capped lease, `OnExhausted` never fires, and `Tracked()` drops to 0 (the lease is dropped from tracking rather than exhausted into the Fallback Flow). Assert that a successful normal renewal before the cap resets the count (a lease that extends `N-1` times, renews once onto a fresh lease, then re-enters breaker-open gets a fresh full budget of `N` extensions), so a transient outage shorter than one TTL never caps. Assert that a worker with `OnExtensionCapReached` unset falls through to `recordFailure`/`exhaust` at the cap rather than hanging. The non-happy path is a permanently-open breaker that extends without bound (the key outlives one TTL of grace), or a cap that drops the lease into the Fallback Flow and re-enters the restart loop. `// spec: §4.9 (cumulative extension capped at the original leaseTTLSeconds; the cap terminates the session without re-minting). // diagnosis: a failure means the guard extended a key past its TTL cap or looped the session through checkpoint-and-restart at the cap.`
- **tier-1 adapter `extendExpiryTimer` moves the deadline without rewriting material (boundary path, CHG-3):** In `pkg/adapter/credexpiry_test.go`, using the `fakeExpiryClock` seam, assert `ExtendCredentialLease` re-arms a single-slot direct-mode timer to fire at the new deadline rather than the old, that `fileProviders` still reports the provider at extension time (no material rewrite), and that the extended timer's `onLeaseExpired` still deletes the provider entry when it fires; assert the no-op paths (absent provider, mismatched `leaseID`, proxy-mode lease with no timer). The non-happy path is an extension that rewrites `/run/lenny/credentials.json`, runs the rebind handshake, or fails to move the enforced deadline. `// spec: §4.9 (adapter ExtendCredentialLease re-arms the direct-mode timer without rewriting material). // diagnosis: a failure means the adapter re-delivered material or did not advance the enforced deadline.`
- **tier-1 adapter per-slot `extendCredentialLeaseSlot` isolation (boundary path, CHG-3):** In `pkg/adapter/credexpiry_test.go` (or an adjacent `slotcreds` test), with `maxConcurrentSessions > 1`, assign a direct-mode lease to a slot, call `ExtendCredentialLease` with `slot_id` set, and assert the slot's own timer re-arms to the new deadline while a sibling slot's timer is untouched, and that the slot no-op paths hold (absent provider, mismatched slot `leaseID`). The non-happy path is a slot extension that re-arms the wrong slot or advances a sibling slot's deadline, breaking the §6.1 per-slot isolation invariant. `// spec: §4.9, §6.1 (per-slot). // diagnosis: a failure means the slot dispatch re-armed the wrong slot or broke slot isolation.`
- **tier-1 delivery-mode dispatch, sentinel mapping, and TTL population (boundary path, CHG-5):** In a `cmd/lenny-gateway` wiring test, assert `onExtend` routes a direct-mode lease to `bind.Adapter.ExtendCredentialLease` and a proxy-mode lease to a `GetByID` + `Put` advance of the lease store record, exercised against the Postgres-backed `pgstore.Store` the proxy reads under the durable topology, and that `Renew` maps `credassign.ErrTokenServiceUnavailable` to `credrenewal.ErrRenewInfraUnavailable` (`errors.Is` matches, cause preserved). Assert the wiring populates `Lease.LeaseTTL` on both tracking sites, since the TTL-derived cap depends on it and the worker unit test stamps `LeaseTTL` on its own fake `Renewer` and so cannot observe the wiring: the `Renew` return carries `LeaseTTL == next.ExpiresAt.Sub(next.IssuedAt)` for a breaker-closed renewal, and the `Track` path records `LeaseTTL == lease.ExpiresAt.Sub(lease.IssuedAt)`. The non-happy path is an unrecognized breaker-open sentinel, an extension sent to the wrong enforcement point for the lease's delivery mode, a proxy-mode extension written to a store the proxy does not read so the durable record keeps its original deadline, or a `Renew` return that omits `LeaseTTL` so the post-renewal cap silently degrades from the TTL-derived `N` to `DefaultMaxExtensions` and a direct-mode key outlives its one-TTL grace bound. `// spec: §4.9. // diagnosis: a failure means the guard is unwired end to end for one delivery mode or durable backend, or the wiring did not stamp the lease TTL so the cap fell back to DefaultMaxExtensions.`
- **tier-1 cap teardown emits a prefix-conformant §8.8 reason (boundary path, CHG-5):** In a `cmd/lenny-gateway` wiring test, assert `onExtensionCapReached` for a capped lease calls the wiring's `sessionbudget.Terminator` with the session id and the `expired:lease` reason (`session.FailureExpiredLease`), so the cap teardown drives the session to `expired` with a `FailureReason` inside the constrained §8.8 `expired:*` set the MCP adapter surfaces verbatim as the task error code (`spec/08_recursive-delegation.md:869`, `pkg/gateway/mcpfabric/mcptools/mcptools.go:1696-1707`). The non-happy path is a teardown reason that lacks the `expired:*` prefix, which the §8.8 adapter would surface as a non-conformant client-facing task error code. `// spec: §4.9, §8.8 (the cap teardown reason is a valid expired:* FailureReason). // diagnosis: a failure means the cap teardown stamped a non-§8.8 reason onto an expired session.`
- **tier-3 `ExtendCredentialLease` wire contract (boundary path, CHG-2/CHG-3):** In `tests/tier3_contract`, assert a gateway `ExtendCredentialLease` carrying `session_id`, `provider`, `lease_id`, and `expires_at_unix_ms` is accepted by the adapter and returns `ExtendCredentialLeaseResponse{}`; assert a request that also carries `slot_id` is accepted and routes to the slot dispatch, so the slot-routing field on the wire is covered; and assert a request with an empty `session_id` is rejected `InvalidArgument` (matching `RotateCredentials`), so a malformed extension is diagnosed rather than silently dropping the guard. The non-happy path is an empty-session extension the handler accepts, or a schema drift between the gateway request and the adapter handler. `// spec: §4.9 (ExtendCredentialLease wire contract).`
- **tier-4 breaker-open extension flow, both delivery modes (spec-named-failure path, CHG-2/3/4/5):** On the compose stack, with the Token Service breaker forced open and a still-valid lease, assert a direct-mode session's adapter expiry timer is re-armed to a later deadline (the credential file survives past the original `expiresAt`) and the session stays alive, and a proxy-mode session's `credleasestore` record is advanced so the LLM Proxy keeps honoring proxy requests past the original `expiresAt`; assert neither path issues a Token Service call. The non-happy path is a session terminated by checkpoint-and-restart under a transient outage, or a proxy request rejected `LEASE_EXPIRED` while the guard should have extended the lease. `// spec: §4.9. // diagnosis: a failure means a transient Token Service outage terminated a still-valid session instead of extending its lease.`
- **tier-8 transient outage and recovery (failure/recovery path, CHG-4/CHG-5):** Inject a Token Service breaker-open interval spanning several renewal sweeps on a session whose lease would otherwise renew during the interval, and assert the lease is extended once per sweep across the outage (deadline advancing by one `renewBeforeBuffer` each time) and that once the breaker closes the next sweep renews normally onto a fresh credential with no lingering extension state. The non-happy path is a session exhausted mid-outage, or a session that fails to renew after the breaker recovers. `// spec: §4.9 (keeps the session alive until the Token Service recovers). // diagnosis: a failure means the guard did not bridge the outage or did not resume normal renewal on recovery.`
- **tier-8 prolonged outage reaches the cap and terminates without looping (spec-named-failure path, CHG-4/CHG-5):** Hold the Token Service breaker open past one full `leaseTTLSeconds` of extension on a direct-mode session, and assert the session is extended up to the cap and then terminated into the §8.8 `expired` state (surfaced to clients as a `failed` task with the `expired:lease` error code), the credential file is deleted at the capped deadline (the key does not outlive the capped lease), and no checkpoint-and-restart or `AssignCredentials` is issued after the cap (no restart loop). The non-happy path is a session that keeps extending past one TTL of grace, or one that drops into checkpoint-and-restart at the cap and loops against the still-open breaker. `// spec: §4.9 (cumulative extension capped at one leaseTTLSeconds; terminal teardown at the cap, no re-mint). // diagnosis: a failure means the cap did not bound key life or re-entered the Fallback restart loop.`
- **tier-10 adapter `ExtendCredentialLease` conformance (spec-named-failure path, CHG-3):** In `tests/tier10_conformance`, assert a conforming adapter, on `ExtendCredentialLease` for a live direct-mode lease, moves the enforced expiry deadline without emitting a `credentials_rotated` lifecycle event and without altering the credential file contents. The non-happy path is an adapter that treats `ExtendCredentialLease` as a rotation (re-delivering material or firing the rebind handshake). `// spec: §4.9, §4.7 (runtime adapter). // diagnosis: a failure means a conforming adapter mishandled a timer-only extension as a material rotation.`

## 7. Findings closed on application

- No coverage-tracker finding id is referenced. The proposal honors the §4.9 "Token Service unavailability guard" (`spec/04_system-components.md:1470`), which the shipped implementation lacks entirely, by adding the adapter `ExtendCredentialLease` surface (CHG-2/CHG-3), the breaker-open worker branch and sentinel (CHG-4), the gateway wiring and proxy-mode extension (CHG-5), the §4.9 spec reconciliation (CHG-1), and the tests (CHG-6). The mechanism runs on the `runc` RuntimeClass and needs no operator hardware.

## 8. Resolved in adversarial review

Subsequent adversarial review rounds populate this section. The drafting pass applied the following convergence revisions before first review:

- **CHG-4 dropped the consecutive-extension cap (`MaxRenewalExtensions` and `trackedLease.extensions`).** The current §4.9 text mandates unbounded extend-until-recovery, and a cap that, once reached with the breaker still open, drops the lease into the Fallback Flow does not avoid the restart loop line 1470 forbids; it delays it, because the replacement pod still calls `AssignCredentials` against the still-down Token Service. The minimal change stages only the sentinel branch (extend-and-reschedule, no cap), matching the current spec text exactly. The key-lifetime bound is deferred to a §9 open decision; when it is settled in §4.9 normative text (CHG-1), the counter can land then. The decisions bullet, CHG-4, the non-goals entry, and the testing list were propagated to the no-cap design.
- **CHG-4 made `Options.OnExtend` return an error and gated the tracked-deadline advance on success.** An unconditional advance of `tl.lease.ExpiresAt`/`RenewBefore` right after calling `OnExtend` decouples the worker's view of the deadline from the enforcement point. The Token Service breaker being open does not imply the per-pod adapter is unreachable, but the `ExtendCredentialLease` RPC can still fail independently; when it does, the adapter timer fires at the old `expiresAt` and deletes `/run/lenny/credentials.json` while the gateway believes the lease valid until the extended deadline, which is strictly worse than the status-quo exhaust path. `OnExtend` now returns an error, and the worker advances its deadline only when the extension took effect at the enforcement point; on failure the lease falls through to `recordFailure`/`exhaust` and still reaches fault rotation.
- **CHG-4 dropped the redundant `now.Before(tl.lease.ExpiresAt)` re-check.** Line 227-229 already exhausts any lease with `now >= ExpiresAt` before the `Renew` call, so re-checking it in the breaker-open branch is redundant.
- **CHG-6 reuses the existing skipped placeholder rather than adding a parallel test.** `TestTickHoldsLeaseWhenTokenServiceBreakerOpen_spec_4_9` (`credrenewal_test.go:242-270`) already asserts the core contract; the adaptation is mandatory because the placeholder's `errors.New` stand-in (`:222`) does not satisfy `errors.Is` against the new exported sentinel, so the `fakeRenewer` must be rewired and the `t.Skip` removed, then extended with the `OnExtend`-called-once and `RenewBefore`-advanced assertions. The proposed separate `TestTickExtendsLeaseWhenBreakerOpen` and the ordinary-error control case were dropped: the latter already exists verbatim as `TestTickRetriesFailedRenewalThenExhausts` (`:133-153`). The net-new adapter `extendExpiryTimer` test and the delivery-mode dispatch wiring test are kept.

### Pass 1 (2026-07-14, automated)

- **CHG-5 proxy-mode extension now targets the `LeaseStore` interface the LLM Proxy reads, covering the Postgres backend.** The prior draft added a dedicated `Extend` only to the in-memory `credleasestore.Store` and typed the wiring dependency as that concrete type. Under Postgres, `w.llmLeases` is swapped to `pgstore.Store` (`stores.go:1540-1544`), which the concrete type cannot hold and which has no `Extend`, so the proxy-mode guard would fail to compile or write to a store the proxy never reads, leaving the durable record at its original `expiresAt` and driving the LLM Proxy to reject the still-valid request with `LEASE_EXPIRED`. CHG-5 now types `credRenewalWiring.leases` as `credleasestore.LeaseStore`, wires it to the same `w.llmLeases` instance, and performs the proxy-mode advance with `GetByID` + `Put`, which both the in-memory `Store` and `pgstore.Store` implement. A dedicated per-backend `Extend` was rejected because the durable body is a single envelope-encrypted blob with no `expiresAt` column, so a `pgstore.Extend` would duplicate the whole `Put` seal-and-write path. The wiring-test change and OD-2 were propagated to run the proxy-mode case against the Postgres-backed store, and the Scope, CHG-5 targets, and Files-touched list were updated to drop the store-package edit.
- **CHG-1 now amends the §4.7 Gateway → Adapter RPC table alongside the §4.9 paragraph.** Adding `ExtendCredentialLease` to the `Adapter` service (CHG-2) left the spec's authoritative RPC enumeration at `spec/04:645-663` incomplete and inconsistent with `schemas/lenny-adapter.proto`, which `spec/15:1433` names as the surface the §4.7 table governs. CHG-1 inserts an `ExtendCredentialLease` row below `RotateCredentials`, and §10 Files-touched records the §4.7 edit site.
- **CHG-6 and §6 add per-slot `ExtendCredentialLease` coverage and drop the mismatched §6.1 tag from the single-slot test.** The single-slot adapter test carried the `§6.1 (per-slot)` tag but exercised no slot dispatch, claiming coverage its assertions did not provide, while `extendCredentialLeaseSlot` (CHG-3) is genuinely new code guarding the §6.1 slot-isolation invariant. A net-new per-slot adapter test now asserts the slot's own timer re-arms to the new deadline, a sibling slot's timer is untouched, and the slot no-op paths hold; the single-slot test's tag was narrowed to `§4.9`. The tier-3 contract test now also asserts a request carrying `slot_id` is accepted and routes to the slot dispatch, so the slot-routing wire field is covered.

### Pass 2 (2026-07-14, automated)

- **The new adapter RPC was renamed from `ExtendLease` to `ExtendCredentialLease` so CHG-1's §4.7 table edit no longer breaks the build.** The prior draft inserted an `ExtendLease` row into the §4.7 Gateway → Adapter RPC table. `tests/tier11_docs/budget_extension_trigger_consistency_test.go:174-177` reads the whole §4.7 section (`spec/04:637-967`) and fails on the substring `ExtendLease` anywhere in it, because the bare gateway↔adapter `ExtendLease` gRPC was trimmed under ADR-0014 (F-15.3.6) when the token-budget extension trigger moved into the gateway LLM Proxy as an in-process operation; the same test also bans the substring in `docs/reference/adapter-contract.md:230-231` and rejects `lease extension` in §9.1. Applying the draft would have turned that green test red. The rename was chosen over narrowing the ban because the trimmed name and direction are a deliberate, test-enforced invariant, and a credential-specific name keeps the new §4.9 credential-expiry RPC distinct on the wire from the removed §8.6 token-budget surface. `ExtendCredentialLease` does not contain the `ExtendLease` substring, so the §4.7 row enumerates it cleanly and no tier-11 test is edited. The rename was propagated through the title, Scope, Decisions, CHG-1 through CHG-6, §6 Testing, and §10 Files-touched, covering the RPC, its `ExtendCredentialLeaseRequest`/`ExtendCredentialLeaseResponse` messages, `Server.ExtendCredentialLease`, `Client.ExtendCredentialLease`, the `bind.Adapter.ExtendCredentialLease` interface method, and the `extendCredentialLeaseSlot` per-slot helper; the pre-existing §8.6 `leasecontrol.ExtendLease` reference in the non-goals is left unchanged because it names the distinct existing surface. The CHG-1 and CHG-2 rationales now record the reconciliation with ADR-0014/F-15.3.6 and note that no tier-11 edit site is required.

### Pass 3 (2026-07-16, automated)

- **`CREDENTIAL_RENEWAL_UNAVAILABLE` is registered as a new client-facing error code instead of being cited as pre-existing.** The OD-1 revision named `CREDENTIAL_RENEWAL_UNAVAILABLE` in the §4.9 normative reword (CHG-1) and the CHG-5 wiring, and §5 asserted the cap "reuses the gateway's existing terminal credential-failure teardown", but the code exists on no surface today (`grep -rn CREDENTIAL_RENEWAL_UNAVAILABLE spec/ pkg/ cmd/ docs/ schemas/ tests/` returns zero matches). Its sibling `CREDENTIAL_RENEWAL_FAILED` is single-sourced across the §15.4 catalog (`spec/15_external-api-surface.md:1027`), the shared classifier table (`pkg/gateway/externalapi/errorclassify/errorclassify.go:417`), and the docs error-catalog (`docs/reference/error-catalog.md:147`), and `errorclassify.Classify` falls back to `(CategoryTransient, true)` for any code with no table entry (`errorclassify.go:44-48`), so an emitted-but-unregistered terminal code would be advertised to clients as retryable, the opposite of its semantics. CHG-5 now adds a `Change (new error code registration)` block staging the three edit sites (a §15.4 `PERMANENT`/503 non-retryable row, an `errorclassify` `{CategoryPermanent, false}` entry, and a docs error-catalog row), §2, CHG-1, §5, the Scope, and §10 now describe the code as new, and §6 adds a tier-1 `errorclassify_test.go` case pinning `Classify("CREDENTIAL_RENEWAL_UNAVAILABLE")` to `(CategoryPermanent, false)`.
- **The cap's terminal teardown is described as new machinery rather than a reuse.** CHG-5 claimed the cap terminates "through the same session-teardown path a terminal credential failure already uses" and §5 claimed it "adds no new teardown machinery", but no credential-renewal path emits a terminal credential error: `credRenewalWiring.onExhausted` (`cred_renewal.go:225-234`) only drops the pool binding and emits `credential_pool_exhausted`, and `credRenewalWiring` holds no session terminator (`cred_renewal.go:62-82`). CHG-5, §5, §2, and §10 now state that the teardown adds a new `onExtensionCapReached` method and a new `sessionbudget.Terminator` dependency (`pkg/gateway/session/sessionbudget/sessionbudget.go:86`) wired from the gateway's `budgetSessionTerminator` (`session_budget.go:44`) in `revocation.go`, reusing only the existing terminator interface and no new terminal-state transition.
- **`Lease.LeaseTTL` is populated on the `Renew` return so the cap stays TTL-derived across a normal renewal.** CHG-5 populated `LeaseTTL` only on the track path, but `Tick`'s success path replaces the tracked entry with the `Renew` return value directly (`credrenewal.go:238-240`), and that struct (`cred_renewal.go:153-159`) carried no `LeaseTTL`, so after any normal renewal the cap would reset to `DefaultMaxExtensions` rather than the TTL-derived `N` the §6 cap test asserts as the post-renewal budget. CHG-5's `Change (LeaseTTL population)` block now sets `LeaseTTL` on the `Renew` return from `next.ExpiresAt.Sub(next.IssuedAt)` (the `credential.Lease` carries `IssuedAt`/`ExpiresAt`, `pkg/credential/lease.go:130-159`) and on the track path from the initial lease's own lifetime, so a renewed lease's cap derives from its own TTL and the reset-to-`N` assertion holds; §10 records both population sites.
- **A concrete classification test pins the new terminal code as non-retryable.** The revision's only test reference to `CREDENTIAL_RENEWAL_UNAVAILABLE` was the tier-8 chaos assertion that the code is used as the teardown reason, which pins neither its category nor its retryable classification. §6 now adds a tier-1 `errorclassify_test.go` case asserting `Classify("CREDENTIAL_RENEWAL_UNAVAILABLE")` returns `(CategoryPermanent, false)` rather than the `(CategoryTransient, true)` unknown-code fallback, matching the classification-locking pattern the peer `CREDENTIAL_RENEWAL_FAILED` carries; §10 lists the `errorclassify_test.go` edit.

### Pass 4 (2026-07-16, automated)

- **The cap teardown now emits the existing `expired:lease` §8.8 `FailureReason` instead of the non-prefixed `CREDENTIAL_RENEWAL_UNAVAILABLE` code, superseding the Pass 3 new-error-code registration.** CHG-5 wired `onExtensionCapReached` to `budgetSessionTerminator.TerminateSession(sessionID, "CREDENTIAL_RENEWAL_UNAVAILABLE")`, but that terminator always force-transitions the session to `expired` and stores the reason verbatim as its §8.8 `FailureReason` (`cmd/lenny-gateway/session_budget.go:74-76`), which the MCP adapter surfaces verbatim as the `failed` task's error code (`pkg/gateway/mcpfabric/mcptools/mcptools.go:1696-1707`). §8.8 requires an `expired`-state session's client-facing code to carry the `expired:*` prefix (`spec/08_recursive-delegation.md:869`), and the reason is constrained to the `expired:*` set (`pkg/api/v1/session/retry_policy.go:240-271`); a prefix-conformant reason is also enforced by test (`pkg/gateway/mcpfabric/mcptools/tasktree_tools_test.go:968`). `CREDENTIAL_RENEWAL_UNAVAILABLE` has no prefix and is not a member of that set, so applying the proposal would have violated the §8.8 surfacing contract. The Pass 3 registration also targeted the wrong namespace: it registered the code on the §15.4 REST catalog, the `errorclassify` table, and the docs error-catalog, but the cap teardown emits on the distinct §8.8 session `FailureReason` surface, and no request path emits `CREDENTIAL_RENEWAL_UNAVAILABLE`. The fix reuses the existing `FailureExpiredLease` (`expired:lease`), which already denotes a §4.9 credential-lease expiry and is exactly the cap condition (the capped lease reaches its enforced deadline and the key is deleted), following the project principle that an existing surface covering the concern is preferred over a new one. CHG-5's `Change (new error code registration)` block was replaced with a `Change (no new error code)` block, the §15.4 / `errorclassify.go` / `docs/reference/error-catalog.md` edit sites were dropped from CHG-5 and §10, the §4.9 reword (CHG-1), §2, §5, §9 OD-1, and the CHG-4 comments now describe the terminal `expired` state surfaced as `expired:lease`, and the §6 `errorclassify_test.go` classification case was replaced with a wiring-test assertion that `onExtensionCapReached` calls the terminator with the `expired:lease` reason. The §6 tier-8 cap test now asserts the session is surfaced as `failed` with the `expired:lease` code.

### Pass 5 (2026-07-16, automated)

- **The `cmd/lenny-gateway` wiring test now asserts the two `LeaseTTL` population sites, closing the gap where no listed test exercised the Pass-3 wiring fix.** CHG-5 stamps `Lease.LeaseTTL` on the `Renew` return (`cred_renewal.go:153-159`, from `next.ExpiresAt.Sub(next.IssuedAt)`) and on the `Track` path (`cred_renewal.go:114-120`, from `lease.ExpiresAt.Sub(lease.IssuedAt)`), which is what keeps the cumulative-TTL cap TTL-derived across a normal renewal, but the only test that pinned the cap magnitude was the tier-1 worker unit test (§6, CHG-6), whose `fakeRenewer` populates `LeaseTTL` itself and therefore cannot detect a wiring that omits it; the tier-8 cases either hold the breaker continuously open or assert only that renewal resumes, so neither exercises a normal-renewal-then-breaker-open handoff whose cap magnitude depends on the wiring. A regression that dropped `LeaseTTL` from the `Renew` return would silently degrade the post-renewal cap from the TTL-derived `N` to `DefaultMaxExtensions`, letting a direct-mode key stay usable beyond the one-TTL grace bound the §4.9 **Bounded extension** text sets, and no listed test would fail. The CHG-6 wiring test and the §6 tier-1 dispatch bullet now assert the `Renew` return carries `LeaseTTL == next.ExpiresAt.Sub(next.IssuedAt)` and the `Track` path records `LeaseTTL == lease.ExpiresAt.Sub(lease.IssuedAt)`; §10 records the added assertions.

## 9. Open decisions for review

### OD-1. The concrete bound on total extension (resolved: cumulative-TTL cap staged)

Resolved and staged. Cumulative extension is capped at the lease's original `leaseTTLSeconds`: the worker counts consecutive breaker-open extensions and stops once cumulative extension reaches one TTL, so a credential's total usable life is at most its original TTL plus one further TTL. The loop-reintroduction tension is settled by the terminal-at-cap behavior: at the cap the worker does NOT enter the Fallback Flow (which would re-mint against the still-open breaker and loop); it terminates the session by driving it to the §8.8 `expired` state (surfaced to clients as `expired:lease`), and the adapter timer at the capped deadline deletes the key fail-closed. Both the value and the terminal behavior are in the §4.9 normative reword (CHG-1), so the CHG-4 worker counter rests on decided spec text. A per-pool configurable cap knob was considered and rejected as premature: deriving the cap from the lease's own TTL needs no new configuration surface and keeps the bound proportional to the credential's intended lifetime. The reviewer decision that remains is whether one TTL of grace is the right magnitude, or whether a shorter multiple (e.g. half a TTL) better bounds a compromised direct-mode key's post-expiry exposure; the mechanism is TTL-derived either way, so this is a single-constant choice in the spec text.

### OD-2. `GetByID` + `Put` over the `LeaseStore` interface versus a dedicated per-backend `Extend` method

CHG-5 performs the proxy-mode extension with `GetByID` + `Put` over the existing `credleasestore.LeaseStore` interface, so the same code path advances the deadline whether `w.llmLeases` is the in-memory `credleasestore.Store` or the Postgres-backed `pgstore.Store` (the durable Postgres-backed deployment, `stores.go:1540-1544`). The alternative, a dedicated `Extend` method, would have to exist on both backends (an in-memory advance under the store lock and a durable write). The durable side cannot issue a plain-column `UPDATE`, because the lease body including `expiresAt`/`renewBefore` is stored as a single envelope-encrypted blob (`pgstore.go:129-163`), so a dedicated `pgstore.Extend` would duplicate the whole seal-and-write path of `Put` for no behavioral gain. `GetByID` + `Put` reuses the existing surface and satisfies the "minimal new protocol surface" principle. The `GetByID`-then-`Put` window has no competing writer while the breaker is open, because the renewal worker holds the tracked lease exclusively and is the only writer of its deadline during the guard. Recommendation: keep `GetByID` + `Put` over the interface and add no new store method.

**Reconciliation with proposal 0037 (retained-lease revocation).** The one writer that could race the `GetByID` + `Put` window is a concurrent revocation removing or mutating the same lease. The safety here rests primarily on an already-shipped mechanism, independent of 0037: the credential-lease revocation propagator drops the renewal worker's tracked leases bound to a revoked credential on `Revoke` (`cmd/lenny-gateway/revocation.go`), and a renewal that re-resolves a now-revoked backing credential is treated as not-found, so the worker stops tracking (and therefore stops extending) a revoked-credential lease before it could re-`Put` it. Proposal 0037 (cluster C-05) strengthens this rather than weakening it: 0037 makes the two credential-revocation paths RETAIN the lease and deny it via the source-aware deny list, so those paths no longer issue a `leases.Remove` that could race the extension `Put` at all, and a re-`Put` of a retained-but-revoked lease is still rejected at the proxy by the deny-list check `CREDENTIAL_REVOKED` that 0037 makes reachable. The only path that still removes the lease under 0037 is the §11.4 full_revoke revoker, which also cancels the user's sessions; a full_revoke `Remove` racing an extension `Put` would re-insert an orphaned row, but the session is already being torn down and the row is reclaimed by session teardown or lapses at its (extended) `ExpiresAt`, and the worker's tracked lease is dropped on session end, so it does not persistently keep a full-revoked session alive. Net: keep `GetByID` + `Put`; no new store method. Because 0036 and 0037 both touch `cmd/lenny-gateway/revocation.go` and `credrenewal`, they should be reviewed and merged as a coordinated pair (see the integrator note), and the tier-4 delivery-mode dispatch test (CHG-6) should include a revoke-races-extend case once both land.

## 10. Files touched on application

- `spec/04_system-components.md`: CHG-1 (the §4.9 **Token Service unavailability guard** paragraph `:1470`, reworded to name the adapter `ExtendCredentialLease` timer extension for direct mode and the gateway lease-store extension for proxy mode, state the `expiresAt`/`renewBefore` extension arithmetic, settle the deadline-redefinition invariant, record the enforcement-point-failure fall-through, and add the **Bounded extension** paragraph (cumulative extension capped at the original `leaseTTLSeconds`, with a terminal `expired` teardown at the cap, surfaced to clients as `expired:lease`, instead of the Fallback Flow), while keeping the MUST-NOT-Fallback rule and the actually-expired carve-out; and the §4.7 Gateway → Adapter RPC table `:645-663`, inserting an `ExtendCredentialLease` row below `RotateCredentials` so the spec's authoritative RPC enumeration matches the new proto surface).
- `schemas/lenny-adapter.proto`: CHG-2 (the `ExtendCredentialLease` RPC on the `Adapter` service near `RotateCredentials` `:90`, and the `ExtendCredentialLeaseRequest`/`ExtendCredentialLeaseResponse` messages near `RotateCredentialsRequest` `:959`).
- `pkg/proto/adapter/v1`: CHG-2 (regenerated Go stubs for the new RPC and messages).
- `pkg/adapter/credexpiry.go`: CHG-3 (the `extendExpiryTimer` helper near `armExpiryTimer` `:83`, re-arming the direct-mode timer to a later deadline without rewriting the credential file or touching `s.credLeases`).
- `pkg/adapter/credentials.go`: CHG-3 (the `Server.ExtendCredentialLease` RPC handler near `RotateCredentials` `:108`, with the empty-session-id `InvalidArgument` guard and the per-slot routing).
- `pkg/adapter/slotcreds.go`: CHG-3 (the `extendCredentialLeaseSlot` per-slot analogue near `rotateCredentialsSlot` `:61`).
- `pkg/gateway/credentials/credrenewal/credrenewal.go`: CHG-4 (the `ErrRenewInfraUnavailable` sentinel near `MaxRenewalRetries` `:29`; `DefaultMaxExtensions` and the `maxExtensions` helper; `Options.OnExtend` returning an error near `:73` and `Options.OnExtensionCapReached`; the `onExtend`/`onExtensionCapReached` `Worker` fields wired in `New`; the `Lease.LeaseTTL` field and the `trackedLease.extensions` counter; the breaker-open hold-and-reschedule branch in `Tick`'s renewal loop `:231-237`, advancing `ExpiresAt`/`RenewBefore` and incrementing `extensions` only on `OnExtend` success, invoking `OnExtensionCapReached` and dropping the lease at the cumulative-TTL cap, and falling through to `recordFailure`/`exhaust` on enforcement-point failure).
- `cmd/lenny-gateway/cred_renewal.go`: CHG-5 (map `credassign.ErrTokenServiceUnavailable` to `credrenewal.ErrRenewInfraUnavailable` in `Renew` `:143-145`; populate `Lease.LeaseTTL` on the `Renew` return `:153-159` from `next.ExpiresAt.Sub(next.IssuedAt)` and on the `track` path `:114-120` from `lease.ExpiresAt.Sub(lease.IssuedAt)`; add the delivery-mode-dispatching `onExtend`; add the new `onExtensionCapReached` method that calls the wiring's session terminator with the existing `expired:lease` §8.8 reason (`session.FailureExpiredLease`); add a `credleasestore.LeaseStore` interface dependency and a `sessionbudget.Terminator` dependency to `credRenewalWiring` `:62-82`, the `LeaseStore` typed as the interface so it can hold the Postgres-backed `pgstore.Store` the proxy reads under Postgres).
- `cmd/lenny-gateway/revocation.go`: CHG-5 (set `OnExtend: credRenewal.onExtend` and `OnExtensionCapReached: credRenewal.onExtensionCapReached` in the `credrenewal.New` Options `:131-144`, the latter routing a capped lease to the terminal `expired:lease` session teardown rather than the Fallback Flow; wire the wiring's session terminator from `w.budgetTerminator` (`session_budget.go:44`, a `sessionbudget.Terminator`) and its `leases` field to the same `w.llmLeases` instance the LLM Proxy reads and credassign mirrors into, `stores.go:1535-1612`).
- `pkg/gateway/runtime/adapterclient/client.go`: CHG-5 (`Client.ExtendCredentialLease` near `RotateCredentials` `:213`, and `ExtendCredentialLease` added to the adapter-client interface `bind.Adapter` exposes).
- No credential-lease store package is edited: the proxy-mode extension reuses the existing `credleasestore.LeaseStore` `GetByID`/`Put` methods, which both the in-memory `credleasestore.Store` and the Postgres-backed `pgstore.Store` already implement (OD-2).
- New and extended tests: `pkg/gateway/credentials/credrenewal/credrenewal_test.go` (un-skip and adapt `TestTickHoldsLeaseWhenTokenServiceBreakerOpen_spec_4_9` `:242`, rewire to the sentinel, add the `OnExtend`-once and `RenewBefore`-advanced assertions, the extension-failure fall-through case, and the cumulative-TTL cap and reset case), `pkg/adapter/credexpiry_test.go` (the net-new `extendExpiryTimer` test), a `cmd/lenny-gateway` wiring test (the net-new delivery-mode dispatch and sentinel-mapping test, the assertions that the `Renew` return and the `Track` path stamp `Lease.LeaseTTL` from the lease's own `ExpiresAt - IssuedAt` so the TTL-derived cap survives a normal renewal, plus the assertion that `onExtensionCapReached` calls the session terminator with the `expired:lease` reason), `tests/tier3_contract`, `tests/tier4_integration`, `tests/tier8_chaos`, and `tests/tier10_conformance` (per §6).
