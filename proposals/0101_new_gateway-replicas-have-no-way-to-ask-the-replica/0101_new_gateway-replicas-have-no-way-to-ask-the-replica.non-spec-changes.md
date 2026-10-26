# Non-spec changes — Gateway replicas have no inter-replica request-forwarding carrier

## Design (implementation-facing)

The normative rules live in the staged spec text. The holder value and the dial bound are in §10.1.1 **Replica address:** (SPEC-2). The forwarding rules are §29.3 **Forwarding.** rules 1 to 8 (SPEC-5). Peer validation and the startup refusal are in §10.3 NET-072 (SPEC-7). The drain publish is in §12.8 (SPEC-6). Each code item below cites the rule it implements and adds only what the code needs: the symbols, the files, the order, and the traps.

The work splits by plan step:
- R18 builds the transport and the address: CODE-1 and CHART-1.
- R19 wires every forward site: CODE-2.
- R21 publishes the drain's release: CODE-4 (the package rename), then CODE-3.

The design adds no proto, no RPC, and no new error code. Its one wire field is the calling-session header of a forwarded platform tool call (CODE-1 item 8).

## Staged code changes

### CODE-1 · (R18) Coordination holder value, forward listener, mTLS, credential carriage, and forward helper core

Targets:
- `cmd/lenny-gateway/coordination_identity.go`
- `cmd/lenny-gateway/sessionsrv.go`, `redis_quota.go`, `httpsurface.go`, `coordination_seams.go`
- `cmd/lenny-gateway/flags.go`
- `cmd/lenny-gateway/interreplica_listener.go` (new) and `runserver.go`
- `pkg/gateway/middleware/auth`
- `pkg/gateway/sessionserver/service.go`
- `pkg/gateway/sessionserver/holder_forward.go` (new)
- `docker-compose.yml`, `tests/testinfra/gateway/gateway.go`
- `migrations/0164_coordination_lease.up.sql` and `pkg/gateway/coordination/coordlease/coordlease.go` (comment only)

1. **Flags.**
   - `--inter-replica-advertise-addr` / `LENNY_INTER_REPLICA_ADVERTISE_ADDR` (host:port) is the `<address>` this replica advertises.
   - `--inter-replica-listen-addr` / `LENNY_INTER_REPLICA_LISTEN_ADDR` (default `:50054`) is the forward listener's bind address.
   - The forward reuses the gateway replica certificate material already wired as `--adapter-tls-cert`, `--adapter-tls-key`, and `--adapter-ca` (the `lenny-gateway-tls` Secret). It adds no new certificate flags.
2. **Holder value.** Add `coordinationHolder(replicaID, advertiseAddr string) (string, error)` to `coordination_identity.go`.
   - It splits `advertiseAddr` at its last colon into host and port and requires a numeric port from 1 to 65535 and a non-empty host.
   - It rebuilds the address with `net.JoinHostPort`, so the chart's unbracketed `$(LENNY_POD_IP):<port>` interpolation of an IPv6 pod IP gets brackets.
   - It returns `replicaID + "." + token + "@" + addr`, where `token` is 8 bytes from `crypto/rand`, hex-encoded, drawn on every call (the §10.1.1 `<process token>`; item 3 calls it once).

   Add `holderAddress(holder string) (string, bool)`. It takes the text after the last `@` and returns false when there is no `@` or when that text fails `net.SplitHostPort`. `resolveReplicaID` stays unchanged.
3. **One value to every lease and mirror consumer.** Build the holder once at startup and pass that value wherever the replica identity currently reaches a `REG-COORDLEASE` or `REG-COORDMIRROR` consumer:
   - the sessionserver `ReplicaID` (`sessionsrv.go`);
   - `coordinationOptions` for the coordination Sweeper (`redis_quota.go`), which also writes it as the mirror's `CoordinatorReplica` and compares it as `priorHolder`;
   - the barrier mirror lister (`httpsurface.go`);
   - the coordination seams (`coordination_seams.go`).

   The ops event `Source`, billing, `service.instance.id`, and the §25.3 nonce checkpoint keep `resolveReplicaID`. **Trap:** a consumer that is missed compares a bare identity against a composite holder and decides that it never coordinates anything. TEST-1 item 2 asserts the full consumer set.
4. **Startup refusal** (§10.3 NET-072, §10.1.1 **Replica address:**). When a lease store is wired, which is whenever Redis is configured:
   - the gateway refuses to start when the advertise address is empty or fails item 2;
   - outside `--dev-mode`, the gateway refuses to start when any of the adapter certificate, key, or CA paths is empty.

   In dev mode with no certificate material, the gateway builds no listener and no forwarder, and the helper treats every forward as not delivered.
5. **Harness and compose.**
   - `docker-compose.yml` sets `LENNY_INTER_REPLICA_ADVERTISE_ADDR` to the gateway service's resolvable name and port 50054.
   - `tests/testinfra/gateway/gateway.go` sets it to `127.0.0.1:<the harness port>`.

   Both run in dev mode. **Trap:** a harness test that sets `LENNY_DEV_MODE=false` with Redis configured now also needs gateway certificate material, or it fails at startup. Give the harness a helper that mints a cluster CA and a gateway leaf certificate carrying the `lenny-gateway.lenny-system.svc` SAN.
6. **Listener** (`interreplica_listener.go`).
   - The listener runs an `http.Server` on the listen address with `tls.Config{Certificates: gateway cert, ClientCAs: adapter CA pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS13}`.
   - A `VerifyConnection` callback rejects a peer leaf certificate whose `DNSNames` does not contain `gatewaycontrol.GatewayDNSName`.
   - The handler chain is the auth middleware, then a wrapper that marks the context as forwarded (`sessionserver.WithForwarded(ctx)`, an unexported context key), then the same session handler mux and `/mcp` handler (`mcp.Server.Handler`) the REST listener serves.
   - A request that carries `X-Lenny-Calling-Session` and no credential skips the auth middleware. The chain reads that session's row and installs the principal that `platformtools.Bridge.CallPlatformTool` installs, through one function both call (rule 3). **Trap:** only this listener reads the header, and the public listener never builds a principal from it.
   - The chain omits the §11.1 rate-limit and §11.5 idempotency middleware (§29.3 **Forwarding.** rule 4), and the result interceptor installed with `mcp.Server.SetResultInterceptor` returns the result unchanged when the exported `sessionserver.Forwarded(ctx)` is true.
   - `runserver.go` starts and drains the listener alongside the REST listener.
   - The forwarded mark is set by this listener alone. No header carries it.
7. **Credential carriage.**
   - The auth middleware records the credential it verified in the request context: `auth.WithCredential(ctx, auth.Credential{Authorization string, DevHeaders http.Header})`, read with `auth.CredentialFrom(ctx)`.
   - `ServiceCall` builds its in-process request from a context that already carries the credential recorded on the MCP request, or at WebSocket upgrade. It keeps that context, so the helper finds the credential on both paths.
   - The credential never appears in a log line.
8. **Forward helper core** (`holder_forward.go`). `func (s *Server) forwardToHolder(w http.ResponseWriter, r *http.Request, row sessionstore.Session, kind forwardKind, body io.Reader) forwardOutcome` returns `outcomeLocal`, `outcomeForwarded`, `outcomeNotDelivered`, or `outcomeUnknown`.
   - **State gate.** The table `forwardStates[kind]` holds the states of CODE-2 item 2. When the row's state is not in it, the helper returns `outcomeLocal`.
   - **Forwarded request** (§29.3 **Forwarding.** rule 5). When the context is marked forwarded, the helper returns `outcomeLocal` when `coordinatesSession` is true. Otherwise it returns `outcomeNotDelivered` without dialling.
   - **Lease read.** The helper reads the lease once. When the holder equals `s.replicaID`, it returns `outcomeLocal`. A read error, no holder, a false result from `holderAddress`, or a nil forwarder returns `outcomeNotDelivered`.
   - **Outbound request.** The request carries the method, path, and raw query of `r`, the body the caller passes, `Content-Type`, `Content-Length`, `Accept`, and the credential from `auth.CredentialFrom`. It carries no other client header. When the context carries no credential and its principal's `CallerType` is `agent`, the request carries the principal's session identifier in `X-Lenny-Calling-Session` instead (rule 3). The caller passes either the bytes it already read or the unread `r.Body`. A site must not consume part of the body before the call.
   - **Client.** One shared `http.Client` uses a transport with `TLSClientConfig{Certificates, RootCAs: adapter CA pool, ServerName: gatewaycontrol.GatewayDNSName, MinVersion: tls.VersionTLS13}`. The dial and handshake share one 2-second bound (§10.1.1). The client sets no `Timeout`, so the request runs under `r.Context()` (rule 8). The helper never retries.
   - **Classification** (rules 6 and 7). An `httptrace.ClientTrace` `GotConn` callback marks the attempt. A failure before `GotConn` is `outcomeNotDelivered`. A failure after it is `outcomeUnknown`, including a failure on a reused keep-alive connection. **Trap:** classifying a reused-connection write failure as not delivered would let a message be held after the coordinator may already have received it.
   - **Responses.** On success, the helper copies the status, the headers other than hop-by-hop headers, and the streamed body to `w`. It writes the 409 `TARGET_NOT_READY` response with `details.currentState` for `outcomeUnknown`, and for `outcomeNotDelivered` on every kind except `kindMessageNoReplyTo`, whose caller holds the message (CODE-2 item 4).
   - **MCP variant.** `func (s *Server) ForwardToolCall(ctx context.Context, row sessionstore.Session, kind forwardKind, tool string, arguments json.RawMessage) (json.RawMessage, forwardOutcome)` runs the same core for the two MCP sites in CODE-2 item 6. It posts the `tools/call` request to the coordinator's `/mcp` and returns the JSON-RPC response, which the site returns as its tool result or tool error with the same code and category.
9. **Comments.** In `migrations/0164_coordination_lease.up.sql`, change the comment that describes `coordinator_replica` as the OTel `service.instance.id`, so that it describes the column as the coordination holder value (§10.1.1 **Replica address:**). Replace `$this_replica_id` with `$this_holder_value` in the §10.1.8 query copies in that file's header comment and in the `pkg/gateway/coordination/coordlease/coordlease.go` package doc comment. The edit changes no SQL.

### CODE-2 · (R19) Forward at every forwarded route and both MCP tool sites, DLQ fallback, and receiver check

Targets:
- `pkg/gateway/sessionserver/interrupt.go` `handleInterrupt`
- `session_transitions.go` `handleTerminalWrite`
- `resume.go` `handleResume` and `resumeHeldPodRequest`
- `upload_to_session.go`
- `messages_delivery.go` `restDelivery.route`
- `messages.go` (comments only)
- `pkg/gateway/mcpfabric/mcptools/mcptools_register.go`: the `lenny/send_message` handler and `cancelSession`
- `send_delivery.go` (the hold on not-delivered)
- the elicitation wait in `pkg/gateway/mcpfabric/mcptools/elicitation.go`

`route_table.go` is not a target. `/upload` in `upload.go` stays local, because it is a blob-store write under the §29.3 store-only row.

1. **Placement.** Each handler calls `forwardToHolder` itself, after its existing entry read and §15.1 precondition check and before any write, pod RPC, or inbox or DLQ write (§29.3 **Forwarding.** rule 1). It returns at once on any outcome other than `outcomeLocal`. The call sites are:
   - `handleTerminalWrite`, after `session.Validate`;
   - `handleInterrupt`, for `running`;
   - the upload-to-session handler, for `running`;
   - `resumeHeldPodRequest`, for `suspended` with a pod;
   - `restDelivery.route`, before the `ActionDeliver` and `ActionBufferInbox` cases, for `running` and `input_required`.

   No mux-level wrapper is added, and no second row read is made.
2. **State gate** (`forwardStates`). These are the §29.3 rows whose sessions hold `REG-COORDLEASE`:
   - terminate and delete: `starting`, `running`, `resume_pending`, `awaiting_client_action`, and `suspended` while the row names a pod;
   - message: `running` and `input_required`;
   - interrupt and upload: `running`;
   - resume: `suspended` with a pod.

   Terminate and delete in `created`, `finalizing`, `ready`, and `suspended` without a pod keep the local `handleTerminalWrite` path unchanged.
3. **Receiver check.** CODE-1 item 8's forwarded-request branch is the receiver check. Reword the existing `coordinatesSession` check in `resumeHeldPodRequest` in place, so that it calls the helper's outcome rather than answering its own 409. Its refusal carries `currentState` and no replica identity.
4. **Not delivered and unknown.**
   - At the message sites, `outcomeNotDelivered` for a message carrying no `inReplyTo` calls `holdMessage` with the TTL of the recovering-state row of the §7.2 dead-letter table, and answers the receipt that `holdMessage` returns: `queued`, `dropped` with `dlq_overflow`, or `error` with `inbox_unavailable`.
   - A message carrying `inReplyTo` gets 409 `TARGET_NOT_READY` with `currentState` on `outcomeNotDelivered` and on `outcomeUnknown`.
   - Every other kind gets the 409 the helper writes.
5. **What stays.** `ActionBufferInbox` (§7.2 path 3), `executorFailure`, and `handleTerminalWrite` stay. Once the forward runs first, the coordinator, or a single-gateway deployment, is the only place these paths run. Update their doc comments to say so.
   - **Trap:** `ActionBufferInbox` is the path-3 classification, which a forwarded `input_required` message reaches on the coordinator. It is not an off-holder branch, and deleting it breaks path 3.
   - In `messages.go`, delete only the elided `ForwardMessage` and "cross-replica coordinator routing" comment lines.
6. **MCP sites.**
   - The `lenny/send_message` handler calls `ForwardToolCall` in the message states right after its target row read and cross-tenant check. The call comes before the `messagingScope` check, the messaging rate limit, the `inReplyTo` resolution, and `runPreMessageDelivery`, so that the coordinator runs each of them once. On `outcomeNotDelivered` for a message without `inReplyTo`, the handler runs those steps locally and then holds the message as item 4 states, instead of classifying it in `mcpSend.route`.
   - `cancelSession` calls it the same way, after its row read, in the terminate-and-delete states.
   - The MCP client tools that dispatch through `ServiceCall` to REST paths reach item 1's handler sites and need no code of their own.
7. **Elicitation wait.** The §29.3 resolution row requires the coordinating replica's elicitation wait to read a respond or dismiss recorded on another replica. First write TEST-2 item 6. If it fails, add a store poll to the wait that blocks `lenny/request_elicitation`, at the cadence of the tool-approval gate's poll in `pkg/gateway/sessionserver/toolapproval.go`. **Trap:** a comment in `toolapproval.go` says that the elicitation await already polls the store. The draft's challenge found no such poll, so do not take the comment as evidence.

### CODE-3 · (R21) The drain publishes its transitioned sessions on `CH-BINDRELEASE`

Targets:
- `pkg/gateway/externalapi/admin/drain.go`
- `pkg/gateway/externalapi/admin/tenant_suspend.go`
- `cmd/lenny-gateway/adminrouter.go`
- `cmd/lenny-gateway/tenantdeletion.go`

The type names below are the CODE-4 names.

1. `DrainSessions` gains the parameter `pods UserPodTerminator`. This is the existing interface in package `admin`. The change adds no new seam.
2. In the transition loop, append `row.ID` when the transition happened. After the loop and before the verifying listing, the drain calls `pods.TerminateUserSessions(context.WithoutCancel(ctx), tenantID, userID, ids)` when `pods` is non-nil and `ids` is non-empty (§12.8 as SPEC-6 stages it). `userID` is empty for a tenant-wide drain. The result never changes `DrainResult` or the returned error.
3. In `adminrouter.go`, move the construction of `bindReleaseFanOut` and its propagator above the erasure runner, so that the same instance reaches:
   - the erasure `WithSessionDrain` closure;
   - `newTenantSessionTerminator`, whose `tenantSessionTerminator` gains a `pods` field;
   - `WithUserRevocation`.

   `drainTenant` passes `r.userPods`. There must be exactly one subscriber. When `podRegistry` is nil, `pods` is nil and the drain publishes nothing.
4. Rewrite the `DrainSessions` doc comment so it describes only the publish. A remote binding is released by the peer's subscriber, and the drain replica's own bindings are already released by the forced transition's post-terminal hook. Delete the orphan-GC sentence once proposal 0100's CODE-7 item 6 has landed.

### CODE-4 · (R21) Rename the binding-release propagator to the `CH-BINDRELEASE` stem

Channel naming law N4 applies.
- Move `pkg/gateway/podlifecycle/podterminate/propagator` to `pkg/gateway/podlifecycle/bindrelease`, as package `bindrelease`, and remove the empty `podterminate` directory.
- Rename `podTerminateFanOut` to `bindReleaseFanOut`, and the wiring field `userPodTerminateProp` to `bindReleaseProp`.
- Update the imports and uses in `cmd/lenny-gateway/adminrouter.go`, `wiring_fields.go`, `user_revocation.go`, and `user_revocation_test.go`, and in `tests/tier3_contract/fullrevoke_release/request_contract_test.go`.
- Doc comments name `CH-BINDRELEASE`.

The Redis channel string `lenny:session:terminate`, the message fields, and `admin.UserPodTerminator` stay unchanged. The change alters no behavior.

## Staged schema, chart, and migration changes

### CHART-1 · (R18) Port, advertise address, listener, and NetworkPolicy self-peer

- `charts/lenny/values.yaml` sets `gateway.interReplicaPort: 50054`, with a comment citing §10.1.1 and §13.2.4.
- `charts/lenny/templates/gateway-deployment.yaml`:
  - adds env `LENNY_POD_IP` from `fieldRef: status.podIP`;
  - adds `LENNY_INTER_REPLICA_ADVERTISE_ADDR: "$(LENNY_POD_IP):{{ .Values.gateway.interReplicaPort }}"`, declared after `LENNY_POD_IP`;
  - adds `LENNY_INTER_REPLICA_LISTEN_ADDR: ":{{ .Values.gateway.interReplicaPort }}"`;
  - adds the container port `interreplica`.
- `charts/lenny/templates/system-network-policies.yaml`:
  - `allow-gateway-egress` gains `to: [{podSelector: {matchLabels: {lenny.dev/component: gateway}}}]` with `ports: [{protocol: TCP, port: {{ .Values.gateway.interReplicaPort }}}]`;
  - `allow-gateway-ingress` gains the mirror `from` rule. No other source is admitted on that port.
- Regenerate `pkg/embedded/manifests/manifests.yaml` with `make generate`.

## Staged docs changes

### DOCS-1 · Operator security guide: gateway-to-gateway forwarding

In `docs/operator-guide/security.md`:
- Under `### Gateway mTLS Identity`, add a bullet stating that replicas forward session requests to each other over mutual TLS on port 50054. Each replica presents its gateway certificate, accepts only a peer presenting the gateway certificate, and re-checks the client's credential on the forwarded request.
- Under `### Allowed Communication Paths`, add rows for `allow-gateway-ingress` (Ingress, gateway pods, 50054) and `allow-gateway-egress` (Egress, gateway pods, 50054).

The page cites no spec section numbers.

### REC-1 · Claim register and BUILD-GAPS records

**Claim register.** `tests/claim-map.json` is generated by `scripts/seed-claim-register.py`. Edit the script (its `EXPLICIT` rows and the override map that holds "Session inbox redelivery") and regenerate. Do not hand-edit the JSON.

| Row | Change | Status at S-spec landing | Flipped to `WIRED` by |
|:--|:--|:--|:--|
| "Cross-replica message forward" | Rename to "Off-holder request forward on `CH-HOLDERFORWARD`". The surface becomes `holder_forward.go` `forwardToHolder` | ABSENT, R19 | CODE-2 |
| "`coordinator_address` population" | Replace with "Replica address in the `REG-COORDLEASE` holder value", anchor `#1011-stateless-replicas-and-per-session-coordination` | ABSENT, R18 | CODE-1 |
| New: "Inter-replica forward listener with gateway peer validation (NET-072)" | Anchor `#2854-inter-replica` | ABSENT, R18 | CODE-1 and CHART-1 |
| "Session inbox redelivery" | The surface and note become the DLQ hold of §29.3 **Forwarding.** rule 6 | ABSENT, R19 | CODE-2 |
| "Input-wait resolution across replicas" | Unchanged. The reply reaches the coordinator, which owns the wait | ABSENT, R19 | CODE-2 |
| "Lifecycle drain releases the pod of a session bound on another gateway replica before the drained scope's rows are erased" (the R21 row, not the R16 row "Lease or mirror release on gateway drain") | Anchor becomes `#2857-gateway-to-store`. The surface becomes the `DrainSessions` publish through `bindReleaseFanOut.TerminateUserSessions` | ABSENT, R21 | CODE-3, after proposal 0100's CODE-7 item 6 |
| New: "`CH-BINDRELEASE` binding-release fan-out" | Surface `cmd/lenny-gateway/adminrouter.go` propagator construction | WIRED | already true; CODE-4 updates the surface path |
| New: "Elicitation resolution across replicas" | Anchor `#293-interactive-message-send` | ABSENT, R19 | CODE-2 item 7 |
| "Tool-approval resolution across replicas" | Unchanged | WIRED | — |

**BUILD-GAPS.md.** Record a resolution on each finding when its step lands:
- F-7.2.38, F-29.3.2, F-29.3.1, and F-10.1.33 when CODE-2 lands;
- F-12.8.43 when CODE-3 and proposal 0100's CODE-7 item 6 have both landed.

**Trap:** `tests/tier11_docs/build_gaps_data_lifecycle_records_currency_test.go` pins the owner text of F-12.8.43. Update its expectation in the same commit as the resolution.

## Testing

Every test carries `// spec:` with section numbers and headings. Every test at tier 2 or above carries a `// diagnosis:` comment. Each item names the assertion that discriminates the behavior.

### TEST-1 · (R18) Holder value, listener, credential carriage, and helper classification

Tiers 1 and 3.

1. **Holder value** (`cmd/lenny-gateway/coordination_identity_test.go`). Table cases:
   - IPv4;
   - unbracketed IPv6, which is bracketed in the result;
   - already-bracketed IPv6;
   - a DNS name;
   - a missing port, a port of 0, a port of 65536, and an empty host, each rejected;
   - `holderAddress` on a bare identity, which returns false;
   - an identity that contains `@`, which splits at the last `@`;
   - two calls with the same identity and address, which return different values.

   `// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination)`.
2. **Consumer set** (`cmd/lenny-gateway/coordination_holder_wiring_test.go`). The sessionserver `ReplicaID`, the Sweeper holder, the barrier mirror lister, and the coordination seams all receive the same composite value. The ops `Source`, billing, and `service.instance.id` keep `resolveReplicaID`. Add a pgstore round-trip of a composite holder string against a real Postgres container. Cover the startup refusals: no advertise address with a lease store, and no certificate outside dev mode. Cover the dev-mode start without certificates, which builds no forwarder. `// spec: 10.1.1, 10.3 (mTLS PKI)`.
3. **Listener mTLS** (`interreplica_listener_test.go`). The listener accepts a gateway-SAN client certificate. It rejects plaintext, a TLS client with no client certificate, a cluster-CA certificate with a different DNS SAN, and an agent SPIFFE certificate, each at handshake. A forwarded request is not rate-limited or idempotency-cached: a repeated `Idempotency-Key` reaches the handler twice. `// spec: 10.3, 29.3 (Interactive message send)`.
4. **Helper classification** (`holder_forward_test.go`).
   - Holder equal to self runs locally with no dial.
   - No holder, a holder without an address, and a nil forwarder are not delivered.
   - A dial refused, a dial that hangs past 2 seconds, and a handshake failure are not delivered. The hanging-dial case asserts that the bound is 2 seconds and not the request deadline.
   - A server that closes after reading headers, and a server that resets mid-body, give an unknown outcome. This is the discriminating case for rule 7.
   - A reused keep-alive connection that fails on write gives an unknown outcome.
   - A forwarded-context request on a non-holder is not delivered and makes no dial.
   - A state outside the gate runs locally.

   `// spec: 29.3, 10.1.1`.
5. **Credential carriage.** For a REST bearer, MCP through `ServiceCall`, and a WebSocket-upgraded connection, the outbound request carries the recorded credential. A request with no credential reaches the coordinator with none and gets 401. No client header other than the content headers, `Accept`, and the credential is copied. `// spec: 29.3, 15.2 (MCP API)`.
6. **Tier 3 contract** (`tests/tier3_contract/holderforward/`). The forwarded HTTP request is byte-equal to the client's request in method, path, query, and body. The coordinator's status, headers, and body come back unchanged, including a 4xx body and a streamed upload response. `// spec: 28.5.4 (Inter-replica), 29.3`.

### TEST-2 · (R19) Off-holder routes on two replicas

Tiers 1 and 4. Tier 1 runs with a fake forwarder. Tier 4 runs in `tests/tier4_integration`, extending the `full_revoke_two_replica_test.go` pattern, plus a lane of two `cmd/lenny-gateway` subprocesses with real certificates.

1. **Message to `running` off-holder.** The receipt comes from the coordinator. No inbox write happens on the serving replica, and no 500 is returned. This closes F-7.2.38. `// spec: 29.3, 7.2 (Interactive Session Model)`.
2. **Message carrying no `inReplyTo` to `input_required` off-holder.** The coordinator buffers it under path 3. With the coordinator unreachable, the message is in the DLQ with the recovering-row TTL, and the coordinator delivers it at its next path-2 delivery. The only serving-replica write is that DLQ enqueue. Cover DLQ overflow (`dropped`, `dlq_overflow`) and a DLQ write error (`error`, `inbox_unavailable`). Cover an enqueue that races a terminal transition (§7.2 **Enqueue racing a terminal transition.**). `// spec: 29.3, 7.2`.
3. **`inReplyTo` reply off-holder.** The reply resolves the coordinator's `lenny/request_input`. With the coordinator unreachable, the result is 409 and the DLQ stays empty, including for an `inReplyTo` that matches no outstanding call. This closes F-29.3.2. `// spec: 29.3`.
4. **Held-pod resume off-holder.** The result is 200 `running`, written by the coordinator. With the coordinator unreachable, the result is 409 with no state write. This closes F-29.3.1. `// spec: 29.3, 7.2`.
5. **Terminate and delete off-holder in every gated state.** The coordinator writes `completed` or `cancelled` and runs its seal. No terminal write happens on the serving replica. In `created`, `ready`, `finalizing`, and `suspended` without a pod, the write is local with no 409. This closes F-10.1.33. `// spec: 29.3, 15.1 (REST API)`.
6. **Interaction resolution.** Approve, deny, respond, and dismiss posted to the non-coordinator resolve the coordinator's pending wait with no forward. A second resolution of the same call, posted to the other replica, is refused, and the blocked call keeps the first resolution. This case drives CODE-2 item 7. `// spec: 29.3`.
7. **Stale forward.** A forward that reaches a former holder returns 409 or a DLQ hold, with no write and no pod RPC. `// spec: 29.3, 28.6 (Exclusivity and concurrency model)`.
8. **Upload.** `/upload-to-session` off-holder is forwarded, and `/upload` stays local. **Interrupt.** Off-holder interrupt is forwarded, and the serving replica writes nothing. `// spec: 29.3, 7.4 (Upload Safety)`.
9. **MCP.** An off-holder `lenny/send_message` or `lenny/cancel_session` from an MCP client forwards the client's `tools/call` with its credential. An off-holder `lenny/send_message` platform call forwards with the calling session. The coordinator refuses a target outside the sender's `messagingScope` with `SCOPE_DENIED`, and it delivers an in-scope message with `from.kind` `agent`; these two assertions fail under a REST-route forward. With the coordinator unreachable, an out-of-scope `lenny/send_message` is refused with `SCOPE_DENIED` and the DLQ stays empty. `PreToolResult` fires once per forwarded call. `// spec: 29.3, 15.2, 7.2`.

### TEST-3 · (R19) Concurrency

Tier 7a, in `tests/tier7a_load_local`.

- Un-skip the F-7.2.38 case in `suspension_interleaving_test.go`.
- Add randomized interleavings of off-holder forwards against coordinator handoff and lease expiry. The invariant checker asserts that every message is delivered exactly once or refused with 409, and never both delivered and held.
- Add a fault-injected forwarder that alternates pre-connection failures and post-connection failures.

`// spec: 29.3, 10.1.2 (Coordinator Handoff Protocol)`.

### TEST-4 · (R21) Drain publish and rename

Tiers 1 and 4.

- **`drain_test.go`.** Only transitioned IDs reach the fake `UserPodTerminator`. Rows already terminal, `ErrNotFound` rows, and failed rows are excluded. The terminator is not called for an empty set or a nil terminator. A cancelled context still delivers the call. `DrainResult` does not change with the terminator's outcome.
- **`release_wiring_test.go`.** The erasure drain, the tenant-deletion drain, and the admin router receive the same `bindReleaseFanOut`.
- **Subscriber case.** `CH-BINDRELEASE` releases only bindings whose primary row is terminal, and it returns their credential leases.
- **Tier 4.** User erasure on replica A releases a binding held on replica B through the publish.

`// spec: 12.8 (Compliance Interfaces), 11.4 (User Invalidation), 28.5.7 (Gateway-to-store)`.

### TEST-5 · Chaos

Tier 8, in `tests/tier8_chaos`.

- Kill the coordinator after the request was written. The result is 409 and nothing is held, and a client retry with the same message `id` is deduplicated. `// spec: 29.3`.
- Drop the `CH-BINDRELEASE` publish. With proposal 0100's gate landed, erasure waits for the post-terminal window, and the binding is released or the window cap passes. This case lands with CODE-3 after 0100's CODE-7 item 6. `// spec: 28.5.7, 12.8`.

### TEST-6 · Cluster and security

Tiers 5, 9, and 11.

- **Tier 5** (`tests/tier5_e2e_kind`). A lane pinned to the non-holder under the rendered NetworkPolicy completes message, resume, and terminate. `lifecycle_drain_kind_test.go` asserts that the remote binding is released after erasure. `// spec: 13.2.4 (lenny-system Namespace NetworkPolicies), 29.3`.
- **Tier 9** (`tests/tier9_security`). Port 50054 refuses a connection from an agent pod and from `lenny-ops`. A cluster-CA certificate with a non-gateway SAN is rejected. A forwarded request carrying another tenant's credential is refused by the coordinator's authorization. A request to the public listener carrying `X-Lenny-Calling-Session` is not run as that session. `// spec: 10.3, 13.2.4, 29.3`.
- **Chart helm-unittest.** Cover the env, the port, and both policy rules. **Tier 11.** Cover the DOCS-1 page and the REC-1 currency test. `// spec: 13.2.4`.

## Edge cases and accepted failure modes

- **Trap:** a message handler reads the request body before routing, so the bytes it read are what it passes to the forward. The upload-to-session handler passes the unread `r.Body` and must not read any part of it before the call.
- **Trap:** a `GotConn` on a reused connection followed by a write failure is classified as unknown and answered 409 (§29.3 **Forwarding.** rule 7).
- In dev mode without certificates, a two-gateway compose stack refuses every off-holder forward with the not-delivered outcome. Single-gateway compose is unaffected, because the holder is always the gateway itself.
- **Trap:** any gateway test run that sets `LENNY_DEV_MODE=false` with Redis configured needs certificate material (CODE-1 item 5).

## Files touched on application (non-spec)

- cmd/lenny-gateway/coordination_identity.go, sessionsrv.go, redis_quota.go, httpsurface.go, coordination_seams.go, flags.go, interreplica_listener.go (new), runserver.go, adminrouter.go, tenantdeletion.go, wiring_fields.go, user_revocation.go, user_revocation_test.go
- pkg/gateway/middleware/auth
- pkg/gateway/sessionserver/service.go, holder_forward.go (new), interrupt.go, session_transitions.go, resume.go, upload_to_session.go, messages_delivery.go, messages.go
- pkg/gateway/mcpfabric/mcptools/send_delivery.go, mcptools_register.go, mcptools.go, elicitation.go
- pkg/gateway/gatewaycontrol/platformtools/platformtools.go
- pkg/gateway/externalapi/admin/drain.go, tenant_suspend.go
- pkg/gateway/podlifecycle/podterminate/propagator (moved to pkg/gateway/podlifecycle/bindrelease)
- migrations/0164_coordination_lease.up.sql and pkg/gateway/coordination/coordlease/coordlease.go (comment)
- docker-compose.yml, tests/testinfra/gateway/gateway.go
- charts/lenny/values.yaml, charts/lenny/templates/gateway-deployment.yaml, charts/lenny/templates/system-network-policies.yaml, charts/lenny/tests, pkg/embedded/manifests/manifests.yaml
- docs/operator-guide/security.md
- scripts/seed-claim-register.py, tests/claim-map.json, BUILD-GAPS.md, tests/tier11_docs/build_gaps_data_lifecycle_records_currency_test.go
- tests/tier3_contract/fullrevoke_release/request_contract_test.go and the test files TEST-1 to TEST-6 name
