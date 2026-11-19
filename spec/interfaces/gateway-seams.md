---
code: GWSEAM
type: interface
summary: >-
  The boundary between each gateway seam and the session core: the operations each side calls on the other, the values they carry, and the route registration interface of the API front.
owns: [operations]
depends-on: [GW, GWSTREAM, GWUPLOAD, GWTOOLS, GWLLM, GWAPI, GWCOORD, GWCLAIM]
---

# Gateway seams

## Terms

| Term | Meaning |
| :-- | :-- |
| <a id="seam-interface"></a>Seam interface | The set of operations through which a gateway seam and the session core reach each other. |
| <a id="holder-replica"></a>Holder replica | The gateway replica that holds a session's coordination lease and therefore coordinates the session. |
| Seam | A part of the gateway that can be extracted into its own service, as the [gateway index](../components/gateway/index.md) describes. |
| Session core | The part of the gateway that coordinates sessions, claims pods, and runs the checkpoint barrier, as the [gateway index](../components/gateway/index.md) describes. |
| API front | The replica-level part that owns the request pipeline, described on the [API front](../components/gateway/api-front.md) page. |
| Coordination lease | The lease that names the replica coordinating a session, described on the [session coordination](../components/gateway/session-coordination.md) page. |

## Overview

The gateway runs as one process. Its seams (the stream proxy, the upload handler, the platform tools seam, and the metering proxy) share that process with the session core and with the parts that apply to the whole replica. This page states every call that crosses the boundary between a seam and the core, in either direction, and the interface through which each part registers its HTTP routes with the API front. Today each operation is an in-process method call. Extracting a seam into its own service puts a transport under the operations this page states, and changes none of them.

The operations are grouped by the party that serves them and the parties that call them. They cover the calls a part makes by importing another part's package and the calls it makes through an interface that `cmd/lenny-gateway` satisfies with another part's value. The metering proxy calls the core to stamp session activity, to expire a session whose token budget is exhausted, and to rotate a faulted upstream credential, and calls the platform tools seam to request a budget extension. It serves the per-session budget state it enforces to both.

## Conventions

- **Package.** Each operation is a method of a Go interface in the package `pkg/gateway/seam`, which imports no other package of the gateway.
- **Identifiers.** Each operation is identified `GWSEAM-<method>`, where the method is the Go method that declares it. The `path` field of each operation's block names the Go interface that declares it. Package documentation holds the Go signatures.
- **Context.** Every operation that reads or writes state, blocks, or reaches another replica takes the caller's context and honors its cancellation. An operation that returns at once and runs its work on the serving part's own context, such as [`GWSEAM-StampActivity`](#gwseam-stampactivity) and [`GWSEAM-ExpireSession`](#gwseam-expiresession), takes no caller context.
- **Errors.** An operation reports a failure the caller branches on as one of the errors the `returns` field of its block names. The package `pkg/gateway/seam` declares each of them. A `Refusal` carries a client API error code with its message and details. It is not transient: the caller reports it as it came and does not retry the operation. Any other error is a transient failure the caller reports as such.
- **Authorization.** An operation trusts the identity that its caller's context carries. A seam that serves a client request resolves the caller's authorization through [`GWSEAM-AuthorizeSession`](#gwseam-authorizesession) before it reads or writes the session.
- **Values.** The operations carry values that the package `pkg/gateway/seam` declares or that packages outside the gateway declare, and no value that a package of another part declares. A session crosses the boundary as a session record: its identifier, tenant, owning user, parent and root sessions, runtime, pool, state, the sequence number of its last event, its depth in its delegation tree, the pod it is bound to, and the times it was created, last written, and moved to a terminal state. A seam that read or wrote the session's stored row hands the row back in the record, unread by the seam, so an operation that runs the session core's logic on that row, such as the terminal pipeline after the seam's terminal write, reads the row the seam acted on rather than reading the session again; a record that carries no row is read as the row its fields describe, and the terminal pipeline then reads the row back from the primary.

## Operations served by the session core to every seam

The session core serves these operations. Every seam calls them.

<a id="gwseam-lookupsession"></a>

```lenny-def
kind: operation
name: LookupSession
method: Go method
path: seam.Sessions
returns: [ErrSessionNotFound]
requirements: [GW-R1]
summary: Returns the session record of a session in a tenant.
```

<a id="gwseam-authorizesession"></a>

```lenny-def
kind: operation
name: AuthorizeSession
method: Go method
path: seam.Sessions
returns: [ErrUnauthenticated, ErrForbidden, ErrSessionNotFound]
requirements: [GW-R1]
summary: Resolves whether the caller in the context may read or manage a session, and returns its session record when it may; a session the caller may not see is reported as not found.
```

<a id="gwseam-forwardrequest"></a>

```lenny-def
kind: operation
name: ForwardRequest
method: Go method
path: seam.RequestForwarding
requirements: [GW-R1]
summary: Delivers an HTTP request for a session to the session's holder replica, or reports that the request runs on this replica, and returns the forward outcome.
```

<a id="gwseam-forwardtoolcall"></a>

```lenny-def
kind: operation
name: ForwardToolCall
method: Go method
path: seam.Forwarding
returns: [Refusal]
requirements: [GW-R1]
summary: Delivers a platform tool call that addresses a session to the session's holder replica, and returns the tool result with the forward outcome; a refusal by the holder, such as its UNAUTHORIZED envelope, comes back as a refusal.
```

<a id="gwseam-forwarded"></a>

```lenny-def
kind: operation
name: Forwarded
method: Go method
path: seam.Forwarding
requirements: [GW-R1]
summary: Reports whether the request in the context arrived from another replica through the forward listener.
```

<a id="gwseam-holdsundelivered"></a>

```lenny-def
kind: operation
name: HoldsUndelivered
method: Go method
path: seam.Forwarding
requirements: [GW-R1]
summary: Reports whether a message whose forward to the holder replica was not delivered is held for later delivery, for a session record and a forward kind.
```

The forward outcome is one of: the request runs on this replica; the holder replica received it; it was not delivered, so no replica acted on it; or the delivery failed after the request was sent, so whether the holder replica acted on it is unknown. The forward kind names the request a forward carries, and the core's forwarding rules decide from it which session states are forwarded. The upload handler forwards a mid-session upload with the upload kind. The platform tools seam forwards a call that ends a session with the terminate kind, a message that answers an input request with the message kind, and any other message with the message kind for no reply, which the caller holds when its forward to a running session is not delivered. A forward the holder replica could not take is refused with TARGET_NOT_READY and the session's current state, and the platform tools seam returns the same refusal for a message that a replica which lost the coordination lease did not deliver. [`GWSEAM-ForwardRequest`](#gwseam-forwardrequest) is declared on an interface of its own, `seam.RequestForwarding`, which the seams that serve HTTP requests hold, apart from the operations through which the platform tools seam forwards a tool call.

## Operations served by the session core to the platform tools seam

The session core serves these operations, except [`GWSEAM-InvokeClientAPI`](#gwseam-invokeclientapi), which the API front serves. The platform tools seam calls them.

<a id="gwseam-createsession"></a>

```lenny-def
kind: operation
name: CreateSession
method: Go method
path: seam.ChildSessions
returns: [Refusal]
requirements: [GW-R1]
summary: Creates a session in a tenant from a client API session-creation body, and returns its identifier, its state, and its upload token; a body that names no environment takes the explicit environment of the caller's connection, and a refusal carries the client API error code, such as VALIDATION_ERROR for a body that does not decode or names no runtime, UNAUTHORIZED, or a quota, policy, or environment access code.
```

<a id="gwseam-materializechild"></a>

```lenny-def
kind: operation
name: MaterializeChild
method: Go method
path: seam.ChildSessions
returns: [ErrChildCredentialAssignment, ErrChildCredentialUnavailable, ErrChildUserCredentialNotFound, ErrPoolWarming, ErrTokenServiceUnavailable]
requirements: [GW-R1]
summary: Claims a pod for a delegated child session, assigns its credentials, binds it, and starts it, and returns the child's state; a failure names an assignment race, a pool with no assignable credential, a user with no registered credential, a pool still warming, or a Token Service that could not mint the credential.
```

<a id="gwseam-checkchildcredentials"></a>

```lenny-def
kind: operation
name: CheckChildCredentials
method: Go method
path: seam.ChildSessions
returns: [ErrChildCredentialUnavailable, ErrChildUserCredentialNotFound]
requirements: [GW-R1]
summary: Checks, before a delegation, that the credentials a child session of a runtime would need are available to it.
```

<a id="gwseam-applyexperimentrouting"></a>

```lenny-def
kind: operation
name: ApplyExperimentRouting
method: Go method
path: seam.ChildSessions
requirements: [GW-R1]
summary: Applies experiment routing to the record of an independent child session before it is created, which may change its runtime and pool.
```

<a id="gwseam-effectivemaxretries"></a>

```lenny-def
kind: operation
name: EffectiveMaxRetries
method: Go method
path: seam.ChildSessions
requirements: [GW-R1]
summary: Returns the retry budget that applies to a session record.
```

<a id="gwseam-invokeclientapi"></a>

```lenny-def
kind: operation
name: InvokeClientAPI
method: Go method
path: seam.ClientAPI
requirements: [GW-R1]
summary: Runs one client API request in process for a tenant, given its method, target, body, content type, and headers, and returns its status, body, and content type; every response whose status is outside 2xx also carries a non-empty error code with its message and details.
```

<a id="gwseam-sendmessages"></a>

```lenny-def
kind: operation
name: SendMessages
method: Go method
path: seam.MessageDelivery
returns: [ErrSessionSealed, ErrNotBound]
requirements: [GW-R1]
summary: Delivers messages to a running session and returns the session's response; a session sealed against delivery reports how many messages it accepted first.
```

<a id="gwseam-enqueueinbox"></a>

```lenny-def
kind: operation
name: EnqueueInbox
method: Go method
path: seam.MessageDelivery
requirements: [GW-R1]
summary: Places a message in a session's inbox and returns the message the inbox dropped to make room, if any.
```

<a id="gwseam-lockdelivery"></a>

```lenny-def
kind: operation
name: LockDelivery
method: Go method
path: seam.MessageDelivery
requirements: [GW-R1]
summary: Takes this replica's delivery lock for a session and returns the function that releases it.
```

<a id="gwseam-holdmessage"></a>

```lenny-def
kind: operation
name: HoldMessage
method: Go method
path: seam.MessageDelivery
requirements: [GW-R1]
summary: Holds a message for later delivery to a session that cannot take it now, and returns whether it was held, refused because the session is terminal, or dropped, with the session's state.
```

<a id="gwseam-holdundeliveredforward"></a>

```lenny-def
kind: operation
name: HoldUndeliveredForward
method: Go method
path: seam.MessageDelivery
requirements: [GW-R1]
summary: Holds a message whose forward to the holder replica was not delivered, and returns the same outcome as GWSEAM-HoldMessage.
```

<a id="gwseam-deliverheld"></a>

```lenny-def
kind: operation
name: DeliverHeld
method: Go method
path: seam.MessageDelivery
returns: [ErrHeldDeliveryPreempted]
requirements: [GW-R1]
summary: Delivers the messages held for a session.
```

<a id="gwseam-resumesuspended"></a>

```lenny-def
kind: operation
name: ResumeSuspended
method: Go method
path: seam.MessageDelivery
requirements: [GW-R1]
summary: Resumes a suspended session so that a message can be delivered to it.
```

<a id="gwseam-admitresume"></a>

```lenny-def
kind: operation
name: AdmitResume
method: Go method
path: seam.MessageDelivery
returns: [CapRefusal]
requirements: [GW-R1]
summary: Checks that resuming a session stays within its tenant's limits, and returns the refusal with its message and details when it does not.
```

<a id="gwseam-routemessage"></a>

```lenny-def
kind: operation
name: RouteMessage
method: Go method
path: seam.MessageDelivery
requirements: [GW-R1]
summary: Returns the delivery path the session core's routing rules give a message from another session, for the session's state and whether it waits for input, which is to deliver it now, to place it in the inbox, to hold it, to refuse it because the session is terminal, or to hold it and resume the session.
```

<a id="gwseam-sessionaccessible"></a>

```lenny-def
kind: operation
name: SessionAccessible
method: Go method
path: seam.SessionAccess
requirements: [GW-R1]
summary: Reports whether the caller in the context may read or manage a session, for a session record the platform tools seam read itself; a context that carries no caller subject is refused.
```

<a id="gwseam-handlebudgetkeysexpired"></a>

```lenny-def
kind: operation
name: HandleBudgetKeysExpired
method: Go method
path: seam.TreeBudgetExpiry
requirements: [GW-R1]
summary: Fails a delegation tree whose budget keys expired, ending the tree's nodes through the terminal pipeline, when the delegation service's reservation finds the keys gone for a tree that already holds a delegated session.
```

<a id="gwseam-exportpaths"></a>

```lenny-def
kind: operation
name: ExportPaths
method: Go method
path: seam.PodFiles
returns: [ErrNoLiveBinding]
requirements: [GW-R1]
summary: Reads the files that export specifications select from the pod of a session bound on this replica, and returns each file's path, content, digest, and size.
```

The scrub report service of the platform tools seam reaches the pod recycle state of the session core through these operations.

<a id="gwseam-incrementsessionsserved"></a>

```lenny-def
kind: operation
name: IncrementSessionsServed
method: Go method
path: seam.PodRecycle
requirements: [GW-R1]
summary: Increments the count of sessions a pod has served and returns the new count, or reports that the pod has no counter.
```

<a id="gwseam-incrementscrubfailurecount"></a>

```lenny-def
kind: operation
name: IncrementScrubFailureCount
method: Go method
path: seam.PodRecycle
requirements: [GW-R1]
summary: Increments the count of failed scrubs of a pod and returns the new count, or reports that the pod has no counter.
```

<a id="gwseam-recyclecounters"></a>

```lenny-def
kind: operation
name: RecycleCounters
method: Go method
path: seam.PodRecycle
requirements: [GW-R1]
summary: Returns the counts of sessions served and of failed scrubs of a pod.
```

<a id="gwseam-recordleak"></a>

```lenny-def
kind: operation
name: RecordLeak
method: Go method
path: seam.PodRecycle
requirements: [GW-R1]
summary: Records that a slot of a pod leaked, so that the pod drains.
```

<a id="gwseam-retireonsessioncount"></a>

```lenny-def
kind: operation
name: RetireOnSessionCount
method: Go method
path: seam.PodRecycle
requirements: [GW-R1]
summary: Retires a pod that has served its maximum count of sessions.
```

<a id="gwseam-inspectforrecycle"></a>

```lenny-def
kind: operation
name: InspectForRecycle
method: Go method
path: seam.PodRecycle
requirements: [GW-R1]
summary: Returns the recycle policy of a pod and the facts its recycle decision reads, or reports that the pod is not found.
```

<a id="gwseam-recyclepod"></a>

```lenny-def
kind: operation
name: RecyclePod
method: Go method
path: seam.PodRecycle
requirements: [GW-R1]
summary: Returns a pod to its pool after a successful scrub.
```

<a id="gwseam-retirepod"></a>

```lenny-def
kind: operation
name: RetirePod
method: Go method
path: seam.PodRecycle
requirements: [GW-R1]
summary: Retires a pod with a retirement reason, its lifetime counts, and a detail.
```

<a id="gwseam-recordscrubfailure"></a>

```lenny-def
kind: operation
name: RecordScrubFailure
method: Go method
path: seam.PodRecycle
requirements: [GW-R1]
summary: Counts one failed whole-pod scrub of a pod, with the pod's cumulative count of failed scrubs, by pool and runtime class, in the session core's pod recycle metrics.
```

<a id="gwseam-recordpodretirement"></a>

```lenny-def
kind: operation
name: RecordPodRetirement
method: Go method
path: seam.PodRecycle
requirements: [GW-R1]
summary: Counts one pod retirement that the gateway decided, by its reason, pool, and runtime class, in the session core's pod recycle metrics.
```

The scrub report service decides which retirements it counts, and the session core owns the pod recycle metrics, which its own per-release retirement also counts.

The bridge of the platform tool calls that a session's pod makes over `LNK-GWCONTROL` resolves the calling session through [`GWSEAM-AttachCallingSession`](#gwseam-attachcallingsession). The session core's forward listener resolves the calling session of a forwarded tool call through the same rule.

<a id="gwseam-attachcallingsession"></a>

```lenny-def
kind: operation
name: AttachCallingSession
method: Go method
path: seam.CallingSessions
returns: [ErrSessionNotFound]
requirements: [GW-R1]
summary: Returns a context that carries the principal of a session's agent caller, for a platform tool call that the session's pod made or that another replica forwarded on its behalf.
```

## Operations served by the session core to the platform tools seam for policy, credentials, and tenants

The session core serves these operations. The platform tools seam calls them, because the interceptor chain, the caller check, the VCS credential pools, the tenant store, environment access, and the rate limiter stay with the session core under [GW-R2](../components/gateway/index.md#gw-r2). The platform tools seam imports the delegation policy store itself, because it evaluates delegation policies.

<a id="gwseam-intercept"></a>

```lenny-def
kind: operation
name: Intercept
method: Go method
path: seam.Interception
requirements: [GW-R1]
summary: Runs the interceptor chain at a phase the platform tools seam runs, for a tenant, a session, a content, and its metadata, optionally running of the external interceptors only the one a content policy names, and returns whether the content is allowed, modified, or rejected, with the rejection's reason, code, rejecting interceptor, and violated immutable fields; a rejection is recorded as an interceptor rejection audit event before the operation returns.
```

<a id="gwseam-intercepts"></a>

```lenny-def
kind: operation
name: Intercepts
method: Go method
path: seam.Interception
requirements: [GW-R1]
summary: Reports whether any interceptor is registered at a phase.
```

<a id="gwseam-failopencooldown"></a>

```lenny-def
kind: operation
name: FailOpenCooldown
method: Go method
path: seam.InterceptorCooldowns
requirements: [GW-R1]
summary: Returns, for a named external interceptor inside a fail-open weakening cooldown, the time of its fail-open transition and the cooldown seconds in force at that transition, or reports that no cooldown applies.
```

<a id="gwseam-resolveexportscan"></a>

```lenny-def
kind: operation
name: ResolveExportScan
method: Go method
path: seam.ExportScans
returns: [ErrExportScanUnavailable]
requirements: [GW-R1]
summary: Returns the per-file export scan of the interceptor a content policy names, which is the interceptor registered at the pre-delegation phase, or reports that no such interceptor is registered so the export fails closed.
```

<a id="gwseam-scanexportfiles"></a>

```lenny-def
kind: operation
name: ScanExportFiles
method: Go method
path: seam.ExportScan
requirements: [GW-R1]
summary: Runs the pre-export-materialization phase of a resolved export scan over the files exported for a delegated child, with the content policy's name, the pool, and the largest file the policy admits, and returns the files as the scan left them with the digest of each final content, or the refusal of the first file the scan refused.
```

<a id="gwseam-checkcreatingcaller"></a>

```lenny-def
kind: operation
name: CheckCreatingCaller
method: Go method
path: seam.CallerChecks
returns: [ErrCallerRefused]
requirements: [GW-R1]
summary: Checks the bearer of the caller in the context against its issued-token record before the caller creates a session, and reports a refusal with its reason only once the refusal's audit row is durable.
```

<a id="gwseam-issuevcstoken"></a>

```lenny-def
kind: operation
name: IssueVCSToken
method: Go method
path: seam.VCSTokens
requirements: [GW-R1]
summary: Issues a short-lived credential for a VCS host from a tenant's VCS credential pool under a lease scope, and returns it, no credential, or a failure naming a host no pool serves, a host several pools serve with the pools that match, or a pool with no usable credential.
```

<a id="gwseam-tenantelicitationintegrity"></a>

```lenny-def
kind: operation
name: TenantElicitationIntegrity
method: Go method
path: seam.TenantFacts
requirements: [GW-R1]
summary: Returns the elicitation content integrity mode a tenant stored, empty when it stored none.
```

<a id="gwseam-tenantcredentialproviders"></a>

```lenny-def
kind: operation
name: TenantCredentialProviders
method: Go method
path: seam.TenantFacts
requirements: [GW-R1]
summary: Returns the providers a tenant's credential policy names.
```

<a id="gwseam-delegatorrefusal"></a>

```lenny-def
kind: operation
name: DelegatorRefusal
method: Go method
path: seam.TenantFacts
requirements: [GW-R1]
summary: Returns the refusal the session creation fence answers for a delegated child of a user in a tenant, from the user's and the tenant's lifecycle markers, or reports that the fence admits it.
```

<a id="gwseam-listtenants"></a>

```lenny-def
kind: operation
name: ListTenants
method: Go method
path: seam.TenantDirectory
requirements: [GW-R1]
summary: Returns the tenants the platform tools seam's sweeps visit.
```

<a id="gwseam-resolveenvironments"></a>

```lenny-def
kind: operation
name: ResolveEnvironments
method: Go method
path: seam.EnvironmentResolver
requirements: [GW-R1]
summary: Resolves the environment membership of the caller in the context within the caller's tenant, and returns the context carrying the resolution in the form the shared environment conventions read, or the context unchanged when it carries no caller or environment filtering is not in effect.
```

<a id="gwseam-incrementrate"></a>

```lenny-def
kind: operation
name: IncrementRate
method: Go method
path: seam.RateCounter
requirements: [GW-R1]
summary: Records one request against a key's current one-minute window and returns the window's running count, from the counter the gateway's admission limits share.
```

## Operations served by the session core to the platform tools seam, the metering proxy, and the API front

A part that moves a session to a terminal state, such as a cancel tool, the orphan and deadlock sweeps, or an operator's force-terminate, runs the session core's terminal pipeline through these operations.

<a id="gwseam-beginterminal"></a>

```lenny-def
kind: operation
name: BeginTerminal
method: Go method
path: seam.Terminals
requirements: [GW-R1]
summary: Records that terminal work for a session started on this replica, so the replica's shutdown waits for it, and returns the function that records its end.
```

<a id="gwseam-onsessionterminal"></a>

```lenny-def
kind: operation
name: OnSessionTerminal
method: Go method
path: seam.Terminals
requirements: [GW-R1]
summary: Runs the terminal pipeline for a session that the caller's write moved to a terminal state from a given state.
```

<a id="gwseam-afterterminalwrite"></a>

```lenny-def
kind: operation
name: AfterTerminalWrite
method: Go method
path: seam.Terminals
requirements: [GW-R1]
summary: Runs the terminal pipeline after the caller's terminal write, with the caller's own terminal signals, and returns the session record after the pipeline.
```

<a id="gwseam-forceterminate"></a>

```lenny-def
kind: operation
name: ForceTerminate
method: Go method
path: seam.ForceTerminations
returns: [ErrSessionNotFound]
requirements: [GW-R1]
summary: Moves a session to a terminal state on an operator request and runs the terminal pipeline, and returns the session record, the state it left, and whether this call moved it.
```

<a id="gwseam-stampactivity"></a>

```lenny-def
kind: operation
name: StampActivity
method: Go method
path: seam.Activity
requirements: [GW-R1]
summary: Records activity on a session, so that the idle watchdog does not count a session that works through platform tools or the metering proxy as idle.
```

## Operations served by the session core to the metering proxy

The session core serves these operations. The metering proxy's budget enforcement calls [`GWSEAM-ExpireSession`](#gwseam-expiresession) when a session exhausts its token budget, and its credential fallback calls [`GWSEAM-RotateCredential`](#gwseam-rotatecredential) when an upstream credential faults and the fallback chain names another pool.

<a id="gwseam-expiresession"></a>

```lenny-def
kind: operation
name: ExpireSession
method: Go method
path: seam.SessionExpiry
requirements: [GW-R1]
summary: Moves a session to the expired state with the failure reason the caller gives and runs the terminal pipeline, and returns without waiting for either. A session already in a terminal state is left as it is.
```

<a id="gwseam-rotatecredential"></a>

```lenny-def
kind: operation
name: RotateCredential
method: Go method
path: seam.CredentialRotations
requirements: [GW-R1]
summary: Issues a replacement credential lease from a given pool for a session whose upstream credential faulted, and pushes it to the session's pod when this replica holds the pod's binding.
```

## Operations served by the session core to the API front

<a id="gwseam-afterrevokewrite"></a>

```lenny-def
kind: operation
name: AfterRevokeWrite
method: Go method
path: seam.Revocations
requirements: [GW-R1]
summary: Drains a session after the API front's revocation write.
```

<a id="gwseam-reclaimrevokedprerunning"></a>

```lenny-def
kind: operation
name: ReclaimRevokedPreRunning
method: Go method
path: seam.Revocations
requirements: [GW-R1]
summary: Reclaims the pod of a revoked session that had not reached running.
```

<a id="gwseam-terminateusersessions"></a>

```lenny-def
kind: operation
name: TerminateUserSessions
method: Go method
path: seam.Revocations
requirements: [GW-R1]
summary: Releases the pods of a user's sessions in a tenant, and returns the count of pods released and the sessions whose release failed.
```

## Operations served by the session core to the upload handler

The upload handler verifies a client's upload token, holds an upload's size against its tenant's storage quota, and records the upload audit event through the session core, because the upload token, quota, tenant, and audit packages stay with the session core under [GW-R2](../components/gateway/index.md#gw-r2).

<a id="gwseam-writeworkspace"></a>

```lenny-def
kind: operation
name: WriteWorkspace
method: Go method
path: seam.Workspaces
returns: [ErrNoLiveBinding, ErrWorkspaceWrite]
requirements: [GW-R1]
summary: Writes uploaded files, each with its workspace-relative path, content, and optional file mode, into the workspace of a session whose pod is bound on this replica, and returns the count of files written; a failure of the pod's adapter names whether staging or promotion failed.
```

<a id="gwseam-verifyuploadtoken"></a>

```lenny-def
kind: operation
name: VerifyUploadToken
method: Go method
path: seam.UploadTokens
returns: [ErrUploadTokenInvalid, ErrUploadTokenExpired, ErrUploadTokenMismatch, ErrUploadTokenConsumed]
requirements: [GW-R1]
summary: Verifies the upload token a client presents for a session, and reports a token that is absent or invalid, expired, issued for another session, or already consumed.
```

<a id="gwseam-reservestorage"></a>

```lenny-def
kind: operation
name: ReserveStorage
method: Go method
path: seam.StorageQuota
returns: [ErrStorageQuotaExceeded, ErrStorageQuotaUnavailable, ErrContentLengthRequired]
requirements: [GW-R1]
summary: Reserves an upload's declared size against its tenant's storage quota, and returns the bytes reserved and the headroom the upload's stream may not exceed, or no reservation when the tenant has no storage quota; a refusal by the quota carries the tenant's current usage and limit.
```

<a id="gwseam-adjuststorage"></a>

```lenny-def
kind: operation
name: AdjustStorage
method: Go method
path: seam.StorageQuota
requirements: [GW-R1]
summary: Adds a signed byte count to a tenant's storage usage, which releases a reservation that GWSEAM-ReserveStorage made or reconciles it to the bytes an upload wrote.
```

<a id="gwseam-auditupload"></a>

```lenny-def
kind: operation
name: AuditUpload
method: Go method
path: seam.UploadAudit
requirements: [GW-R1]
summary: Records the session upload audit event for a session record, with the upload's outcome, its rejection reason, and a detail, under the session's own tenant.
```

## Operations served by the session core to the stream proxy

<a id="gwseam-resumedframe"></a>

```lenny-def
kind: operation
name: ResumedFrame
method: Go method
path: seam.StreamFrames
requirements: [GW-R1]
summary: Returns the frame that tells a reader attaching after a coordinator handoff that the session resumed, or reports that there is none.
```

<a id="gwseam-childrenreattachedframe"></a>

```lenny-def
kind: operation
name: ChildrenReattachedFrame
method: Go method
path: seam.StreamFrames
requirements: [GW-R1]
summary: Returns the frame that lists a parent session's children reattached after a handoff, for a reader attaching after a sequence number, or reports that there is none.
```

## Operations served by the upload handler to the session core

<a id="gwseam-closeuploads"></a>

```lenny-def
kind: operation
name: CloseUploads
method: Go method
path: seam.UploadWindows
requirements: [GW-R1]
summary: Ends a session's upload window, aborting its uploads in progress and releasing its upload limits.
```

<a id="gwseam-recordextractionabort"></a>

```lenny-def
kind: operation
name: RecordExtractionAbort
method: Go method
path: seam.UploadWindows
requirements: [GW-R1]
summary: Counts an archive extraction that the session core's workspace materialization aborted, by error type, in the upload handler's metrics.
```

## Operations served by the platform tools seam to the session core

The platform tools seam serves these operations. The session core calls them. The session core's watchdog also calls [`GWSEAM-ArchiveNode`](#gwseam-archivenode).

Each delegation tree operation is declared on one of the narrower interfaces `seam.TreeBudgets`, `seam.TreeArchive`, `seam.TreeRollups`, `seam.TreeOrphans`, and `seam.TreeRecovery`, which `seam.DelegationTree` combines. The session core holds each one it calls, and a gateway that does not run a part, such as the tree budget counters without Redis, hands the core none for that part.

<a id="gwseam-returnbudget"></a>

```lenny-def
kind: operation
name: ReturnBudget
method: Go method
path: seam.TreeBudgets
requirements: [GW-R1]
summary: Returns the budget a delegation tree node held to its tree when the node reaches a terminal state.
```

<a id="gwseam-rearmbudget"></a>

```lenny-def
kind: operation
name: RearmBudget
method: Go method
path: seam.TreeBudgets
requirements: [GW-R1]
summary: Restores the expiry of the budget keys of a delegation tree and its parent nodes.
```

<a id="gwseam-observehighwatermark"></a>

```lenny-def
kind: operation
name: ObserveHighWatermark
method: Go method
path: seam.TreeBudgets
requirements: [GW-R1]
summary: Returns the highest count of parallel children a delegation tree reached, or reports that none is recorded.
```

<a id="gwseam-archivenode"></a>

```lenny-def
kind: operation
name: ArchiveNode
method: Go method
path: seam.TreeArchive
requirements: [GW-R1]
summary: Records a settled delegation tree node in the tree archive.
```

<a id="gwseam-archivednodes"></a>

```lenny-def
kind: operation
name: ArchivedNodes
method: Go method
path: seam.TreeArchive
requirements: [GW-R1]
summary: Returns the archived nodes of a delegation tree in the order they settled.
```

<a id="gwseam-archivednodefor"></a>

```lenny-def
kind: operation
name: ArchivedNodeFor
method: Go method
path: seam.TreeArchive
returns: [ErrSessionNotFound]
requirements: [GW-R1]
summary: Returns the archived node of one session.
```

<a id="gwseam-nodeusage"></a>

```lenny-def
kind: operation
name: NodeUsage
method: Go method
path: seam.TreeRollups
requirements: [GW-R1]
summary: Returns the usage of one delegation tree node.
```

<a id="gwseam-treeusage"></a>

```lenny-def
kind: operation
name: TreeUsage
method: Go method
path: seam.TreeRollups
requirements: [GW-R1]
summary: Returns the usage of a delegation subtree rolled up from its live and archived nodes.
```

<a id="gwseam-subtreetokens"></a>

```lenny-def
kind: operation
name: SubtreeTokens
method: Go method
path: seam.TreeRollups
requirements: [GW-R1]
summary: Returns the tokens the live and archived descendants of a session used.
```

<a id="gwseam-orphaned"></a>

```lenny-def
kind: operation
name: Orphaned
method: Go method
path: seam.TreeOrphans
requirements: [GW-R1]
summary: Reports whether a session's chain of ancestors, among a set of session records, is terminal or broken.
```

<a id="gwseam-recovertree"></a>

```lenny-def
kind: operation
name: RecoverTree
method: Go method
path: seam.TreeRecovery
requirements: [GW-R1]
summary: Recovers a delegation tree from its leaves to its root through the tree node operations the session core serves, and returns a summary of the recovery.
```

The session core serves these operations to [`GWSEAM-RecoverTree`](#gwseam-recovertree).

<a id="gwseam-listtree"></a>

```lenny-def
kind: operation
name: ListTree
method: Go method
path: seam.TreeNodes
requirements: [GW-R1]
summary: Returns the session records of every node of a delegation tree.
```

<a id="gwseam-startnode"></a>

```lenny-def
kind: operation
name: StartNode
method: Go method
path: seam.TreeNodes
requirements: [GW-R1]
summary: Starts a delegation tree node that had not started when the tree's coordinator was lost.
```

<a id="gwseam-reattachnode"></a>

```lenny-def
kind: operation
name: ReattachNode
method: Go method
path: seam.TreeNodes
requirements: [GW-R1]
summary: Reattaches a running delegation tree node to this replica.
```

<a id="gwseam-failnode"></a>

```lenny-def
kind: operation
name: FailNode
method: Go method
path: seam.TreeNodes
requirements: [GW-R1]
summary: Moves a delegation tree node that cannot be recovered to a failed state with a reason.
```

The platform tools seam also serves the registries of the waits a session's runtime opens through platform tools.

<a id="gwseam-pendinginputwaits"></a>

```lenny-def
kind: operation
name: PendingInputWaits
method: Go method
path: seam.InputWaits
requirements: [GW-R1]
summary: Returns the identifiers of the input requests a session has open.
```

<a id="gwseam-resolveinputwait"></a>

```lenny-def
kind: operation
name: ResolveInputWait
method: Go method
path: seam.InputWaits
requirements: [GW-R1]
summary: Answers one open input request of a session.
```

<a id="gwseam-forgetinputwaits"></a>

```lenny-def
kind: operation
name: ForgetInputWaits
method: Go method
path: seam.InputWaits
requirements: [GW-R1]
summary: Discards every open input request of a session.
```

<a id="gwseam-registerapproval"></a>

```lenny-def
kind: operation
name: RegisterApproval
method: Go method
path: seam.ToolApprovals
requirements: [GW-R1]
summary: Opens a wait for the approval of one tool call of a session and returns the channel on which its decision arrives.
```

<a id="gwseam-cancelapproval"></a>

```lenny-def
kind: operation
name: CancelApproval
method: Go method
path: seam.ToolApprovals
requirements: [GW-R1]
summary: Withdraws the wait for the approval of one tool call of a session.
```

<a id="gwseam-resolveapproval"></a>

```lenny-def
kind: operation
name: ResolveApproval
method: Go method
path: seam.ToolApprovals
requirements: [GW-R1]
summary: Delivers the decision, approved or denied with a reason, on the approval wait of one tool call of a session.
```

<a id="gwseam-awaitapproval"></a>

```lenny-def
kind: operation
name: AwaitApproval
method: Go method
path: seam.ToolApprovalGate
requirements: [GW-R1]
summary: Holds a runtime's tool call until the session's user approves or denies it, and returns the decision; the session core's executor calls it.
```


## Operations served by the platform tools seam to the metering proxy and the API front

<a id="gwseam-extendforbudget"></a>

```lenny-def
kind: operation
name: ExtendForBudget
method: Go method
path: seam.BudgetExtensions
requirements: [GW-R1]
summary: Requests a budget extension for a session whose budget the metering proxy found exhausted, waits for it within the wait the caller bounds, and returns whether it was granted, is still pending, or ended the session.
```

<a id="gwseam-clearsubtreedenial"></a>

```lenny-def
kind: operation
name: ClearSubtreeDenial
method: Go method
path: seam.DelegationAdmin
requirements: [GW-R1]
summary: Clears the recorded denial of budget extensions for a delegation subtree, on an operator request through the admin API.
```

<a id="gwseam-elicitationfloor"></a>

```lenny-def
kind: operation
name: ElicitationFloor
method: Go method
path: seam.DelegationAdmin
requirements: [GW-R1]
summary: Returns the deployment floor that elicitation settings may not go below, which the admin API checks a tenant setting against.
```

## Operations served by the metering proxy

The metering proxy serves these operations. The session core calls [`GWSEAM-ForgetBudget`](#gwseam-forgetbudget) and [`GWSEAM-TrackedBudgetSessions`](#gwseam-trackedbudgetsessions), and the platform tools seam calls [`GWSEAM-RaiseBudget`](#gwseam-raisebudget) and [`GWSEAM-TerminateForBudget`](#gwseam-terminateforbudget) when a budget extension resolves. Each pair is an interface of its own, so neither caller holds the other's operations.

<a id="gwseam-forgetbudget"></a>

```lenny-def
kind: operation
name: ForgetBudget
method: Go method
path: seam.BudgetTracking
requirements: [GW-R1]
summary: Discards the metering proxy's budget state for a session that ended.
```

<a id="gwseam-trackedbudgetsessions"></a>

```lenny-def
kind: operation
name: TrackedBudgetSessions
method: Go method
path: seam.BudgetTracking
requirements: [GW-R1]
summary: Returns the sessions for which the metering proxy holds budget state.
```

<a id="gwseam-raisebudget"></a>

```lenny-def
kind: operation
name: RaiseBudget
method: Go method
path: seam.BudgetResolutions
requirements: [GW-R1]
summary: Raises a session's budget by a granted amount.
```

<a id="gwseam-terminateforbudget"></a>

```lenny-def
kind: operation
name: TerminateForBudget
method: Go method
path: seam.BudgetResolutions
requirements: [GW-R1]
summary: Ends a session whose budget extension was refused.
```

## Operations served by the API front to every seam that serves client requests

<a id="gwseam-requesttenant"></a>

```lenny-def
kind: operation
name: RequestTenant
method: Go method
path: seam.RequestIdentity
requirements: [GW-R1]
summary: Returns the tenant that a client request runs as, which the API front's tenant check resolved before the handler ran.
```

## Operations served by the API front to the platform tools seam

The session core serves [`GWSEAM-RequestCaller`](#gwseam-requestcaller) until the API front holds the request pipeline.

<a id="gwseam-requestcaller"></a>

```lenny-def
kind: operation
name: RequestCaller
method: Go method
path: seam.CallerIdentity
requirements: [GW-R1]
summary: Returns the authenticated caller that the context of a request or a platform tool call carries, with its subject, its tenant, the session a session-scoped caller acts for, its caller type, its groups, and the identifier and origin of the bearer it presented, or reports that the context carries no caller.
```

<a id="gwseam-isbearerrevoked"></a>

```lenny-def
kind: operation
name: IsBearerRevoked
method: Go method
path: seam.BearerRevocations
requirements: [GW-R1]
summary: Reports whether a bearer token of a tenant, by its identifier, has been revoked, so that a tool call made with it is refused.
```

## Operations served by the stream proxy

The stream proxy serves these operations. The session core and every seam call them, and no other path reaches the session event bus.

<a id="gwseam-publish"></a>

```lenny-def
kind: operation
name: Publish
method: Go method
path: seam.Events
requirements: [GW-R1]
summary: Publishes an event of a type with its data on a session's stream, and returns the event with the sequence number it was given.
```

<a id="gwseam-subscribe"></a>

```lenny-def
kind: operation
name: Subscribe
method: Go method
path: seam.Events
requirements: [GW-R1]
summary: Opens a subscription to a session's stream in a tenant from after a sequence number, and returns the channel of events and the function that closes the subscription.
```

<a id="gwseam-oldestretainedseq"></a>

```lenny-def
kind: operation
name: OldestRetainedSeq
method: Go method
path: seam.Events
requirements: [GW-R1]
summary: Returns the sequence number of the oldest event the stream proxy retains for a session, or reports that it retains none.
```

<a id="gwseam-broadcast"></a>

```lenny-def
kind: operation
name: Broadcast
method: Go method
path: seam.Events
requirements: [GW-R1]
summary: Publishes an event on every open stream, such as the platform degradation notice the session core sends when both of its stores fail, and returns the count of streams it reached.
```

<a id="gwseam-streamstats"></a>

```lenny-def
kind: operation
name: StreamStats
method: Go method
path: seam.Events
requirements: [GW-R1]
summary: Returns the count of open subscriptions and the highest replay-buffer utilization, which the replica's metrics report for scaling.
```

## Route registration

Each part registers its own HTTP routes with the API front, so the API front imports no part, and a seam extracted into its own service takes its routes with it.

<a id="gwseam-handleroute"></a>

```lenny-def
kind: operation
name: HandleRoute
method: Go method
path: seam.Routes
returns: [ErrInvalidRoute]
requirements: [GW-R1]
summary: Registers one route, given its listener, its method, its path pattern, the access check it requires, and its handler, and refuses a route whose listener or access check is zero or unknown or that lacks a method, a pattern, or a handler.
```

<a id="gwseam-registerroutes"></a>

```lenny-def
kind: operation
name: RegisterRoutes
method: Go method
path: seam.RouteProvider
returns: [ErrInvalidRoute]
requirements: [GW-R1]
summary: Registers every route of one part through GWSEAM-HandleRoute.
```

A route names the listener that serves it:

- the client listener, which serves the client and admin APIs;
- the pod proxy listener, the pod-facing listener of the metering proxy;
- the forward listener, on which another replica forwards a request to the session's holder replica.

The metering proxy serves its routes on its own listener and server, as it does today. The API front never mounts a route of the pod proxy listener on the client listener, so the pod-facing endpoint is never reachable from a client.

A route names the access check the API front applies before the handler runs, through [`GWSEAM-AuthorizeSession`](#gwseam-authorizesession) where the check reads a session:

- a resolved tenant;
- read access to the session the path names;
- manage access to the session the path names;
- the `session:eval:write` permission, as the evaluation route requires, whose handler applies its own rule for the caller of the session;
- a resolved tenant and the `view_usage` permission, as the usage route requires;
- no check by the API front, for a route whose handler authenticates the request itself.

No route takes a default check: a zero or unknown access check is refused at registration.

The upload handler registers its upload routes, the plain, archive, and mid-session uploads, with manage access to the session the path names, and its blob read route with a resolved tenant, because the blob read checks the blob's tenant against the caller's itself.

## Crossings without an operation

- Each part emits its metrics into the gateway's metric registry. A seam registers its own metric collectors on the registry, because a seam holds no value of a replica-level part. The session core and the replica-level parts register their own collectors or emit through a metrics interface they declare, which the gateway's metric set satisfies. Neither form is an operation of the seam interface.
- The API front reads pool status through the session core's pool status reader, and authenticates service-account tokens through the platform tools seam's token verifier. Neither is an operation of the seam interface: each is domain logic that belongs in a concern package outside the gateway.

## Compatibility

The seam interface has no caller outside the gateway binary and carries no version. An operation changes together with the parts on both sides of it.
