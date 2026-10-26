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
- `cmd/lenny-gateway/sessionsrv.go`, `redis_quota.go`, `httpsurface.go`, `coordination_seams.go`, `metricsbackfill.go`
- `cmd/lenny-gateway/flags.go`
- `cmd/lenny-gateway/holder_forward_listener.go` (new) and `runserver.go`
- `pkg/gateway/middleware/auth`
- `pkg/gateway/sessionserver/service.go`
- `pkg/gateway/sessionserver/holder_forward.go` (new), `holder_forward_listener.go` (new), and `sessionserver.go` (the `HolderForwardClient` option)
- `pkg/gateway/mcpfabric/mcp/mcp.go` (the exported tool-call response decoder)
- `pkg/gateway/mcpfabric/mcptools/mcptools.go` (`HolderForwarder` and `Deps.HolderForward`) and `cmd/lenny-gateway/mcpsurface.go`
- `pkg/gateway/middleware/idempotency/idempotency.go`, and the skip-reason doc comments in `pkg/gateway/metrics/gatewaymetrics`
- `docker-compose.yml`, `tests/testinfra/gateway/gateway.go`, `tests/testinfra/gatewaypki` (new), `tests/change-graph.json`
- `migrations/0164_coordination_lease.up.sql` and `pkg/gateway/coordination/coordlease/coordlease.go` (comment only)

1. **Flags.**
   - `--holder-forward-advertise-addr` / `LENNY_HOLDER_FORWARD_ADVERTISE_ADDR` (host:port) is the `<address>` this replica advertises.
   - `--holder-forward-listen-addr` / `LENNY_HOLDER_FORWARD_LISTEN_ADDR` (default `:50054`) is the forward listener's bind address.
   - The forward reuses the gateway replica certificate material already wired as `--adapter-tls-cert`, `--adapter-tls-key`, and `--adapter-ca` (the `lenny-gateway-tls` Secret). It adds no new certificate flags.
2. **Holder value.** Add the exported `HolderValue(replicaID, advertiseAddr string) (string, error)` to `pkg/gateway/sessionserver/holder_forward.go`. `coordination_identity.go` builds the startup holder of item 3 with it, and the CODE-2 item 7 harness builds its holder values with it.
   - It splits `advertiseAddr` at its last colon into host and port, strips one enclosing pair of brackets from the host, and requires a numeric port from 1 to 65535 and a non-empty host that contains no bracket.
   - It rebuilds the address with `net.JoinHostPort`, so the chart's unbracketed `$(LENNY_POD_IP):<port>` interpolation of an IPv6 pod IP gets brackets.
   - It returns `replicaID + "." + token + "@" + addr`, where `token` is 8 bytes from `crypto/rand`, hex-encoded, drawn on every call (the §10.1.1 `<process token>`; item 3 calls it once).

   Add the exported `HolderAddress(holder string) (string, bool)` beside it. It takes the text after the last `@` and returns false when there is no `@` or when that text fails `net.SplitHostPort`. `resolveReplicaID` stays unchanged.
3. **One value to every lease and mirror consumer.** Build the holder once at startup and pass that value wherever the replica identity currently reaches a `REG-COORDLEASE` or `REG-COORDMIRROR` consumer:
   - the sessionserver `ReplicaID` (`sessionsrv.go`);
   - `coordinationOptions` for the coordination Sweeper (`redis_quota.go`), which also writes it as the mirror's `CoordinatorReplica` and compares it as `priorHolder`;
   - the barrier mirror lister (`httpsurface.go`);
   - the coordination seams (`coordination_seams.go`);
   - the coordination fencer (`coordfence.New` in `metricsbackfill.go`), whose relinquish releases the lease by holder.

   The ops event `Source`, billing, `service.instance.id`, and the §25.3 nonce checkpoint keep `resolveReplicaID`. **Trap:** a consumer that is missed holds a bare identity against a composite holder, so its comparisons never match and its releases never release. TEST-1 item 2 asserts the full consumer set.
4. **Startup refusal** (§10.3 NET-072, §10.1.1 **Replica address:**). When a lease store is wired, which is whenever Redis is configured:
   - the gateway refuses to start when the advertise address is empty or fails item 2;
   - outside `--dev-mode`, the gateway refuses to start when any of the adapter certificate, key, or CA paths is empty.

   In dev mode with no certificate material, the gateway builds no listener and leaves `HolderForwardClient` nil, and the helper treats every forward as not delivered.
5. **Harness and compose.**
   - `docker-compose.yml` sets `LENNY_HOLDER_FORWARD_ADVERTISE_ADDR` to the gateway service's resolvable name and port 50054.
   - `tests/testinfra/gateway/gateway.go` draws a second free port per process, sets both `LENNY_HOLDER_FORWARD_LISTEN_ADDR` and `LENNY_HOLDER_FORWARD_ADVERTISE_ADDR` to `127.0.0.1:<that port>`, and exposes that address on `Process` beside `BaseURL`.

   Both run in dev mode. **Test certificates.** `tests/testinfra/gatewaypki` (new) exports `Mint(t testing.TB) *Material`. It mints a cluster CA and a gateway leaf certificate carrying the `gatewaycontrol.GatewayDNSName` SAN, writes them as PEM files under `t.TempDir()`, and returns their paths and the CA pool. `(*Material).Leaf(t, dnsNames, uris)` mints another leaf from the same CA for a negative case. `tests/change-graph.json` gains a `tests/testinfra/gatewaypki` key selecting the tests that import the package. **Trap:** a harness test that sets `LENNY_DEV_MODE=false` with Redis configured now fails at startup unless it passes this material.
6. **Listener.**
   - `pkg/gateway/sessionserver/holder_forward_listener.go` exports `HolderForwardServerTLS(reloader *certreload.Reloader, clientCAs *x509.CertPool) *tls.Config`, which is the `tls.Config` and the `VerifyConnection` callback below, and `NewHolderForwardHandler(inner http.Handler, authOpts auth.Options, sessions platformtools.SessionResolver) http.Handler`, which is the handler chain below. `cmd/lenny-gateway/holder_forward_listener.go` holds the listen address, the `http.Server`, and the drain, so the gateway and the CODE-2 item 7 harness serve one chain.
   - The listener runs an `http.Server` on the listen address with `tls.Config{GetCertificate: reloader.GetCertificate, ClientCAs: adapter CA pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS13}`, where `reloader` is one `certreload.New` over the adapter certificate and key that the client in item 8 also uses (§10.3).
   - A `VerifyConnection` callback rejects a peer leaf certificate whose `DNSNames` does not contain `gatewaycontrol.GatewayDNSName`.
   - The handler chain is the auth middleware, which `cmd/lenny-gateway` builds from the `authOpts` value of its REST listener (`httpsurface.go`, §29.3 **Forwarding.** rule 3), then a wrapper that marks the context as forwarded (`sessionserver.WithForwarded(ctx)`, an unexported context key), then the same session handler mux and `/mcp` handler (`mcp.Server.Handler`) the REST listener serves.
   - A request that carries `X-Lenny-Calling-Session` and no credential skips the auth middleware. The chain reads that session's row and installs the principal that `platformtools.Bridge.CallPlatformTool` installs, through one function both call (rule 3). **Trap:** only this listener reads the header, and the public listener never builds a principal from it.
   - The chain omits the §11.1 rate-limit and §11.5 idempotency middleware (§29.3 **Forwarding.** rule 4), and the result interceptor installed with `mcp.Server.SetResultInterceptor` returns the result unchanged when the exported `sessionserver.Forwarded(ctx)` is true.
   - `runserver.go` starts and drains the listener alongside the REST listener.
   - The forwarded mark is set by this listener alone. No header carries it.
7. **Credential carriage.**
   - The auth middleware records the credential it verified in the request context: `auth.WithCredential(ctx, auth.Credential{Authorization string, DevHeaders http.Header})`, read with `auth.CredentialFrom(ctx)`.
   - `ServiceCall` builds its in-process request from a context that already carries the credential recorded on the MCP request, or at WebSocket upgrade. It keeps that context, so the helper finds the credential on both paths.
   - The credential never appears in a log line.
8. **Forward helper core** (`holder_forward.go`). `func (s *Server) forwardToHolder(w http.ResponseWriter, r *http.Request, row sessionstore.Session, kind ForwardKind, body io.Reader) ForwardOutcome` returns `ForwardLocal`, `ForwardDelivered`, `ForwardNotDelivered`, or `ForwardUnknown`. The kind type, every kind constant, and the outcome type are exported, because CODE-2 item 6 calls `ForwardToolCall` from package mcptools.
   - **State gate.** The table `forwardStates[kind]` holds the states of CODE-2 item 2. When the row's state is not in it, or the state is `suspended` and `holdsPod(row)` is false, the helper returns `ForwardLocal`.
   - **Forwarded request** (§29.3 **Forwarding.** rule 5). When the context is marked forwarded, the helper returns `ForwardLocal` when `coordinatesSession` is true. Otherwise it returns `ForwardNotDelivered` without dialling.
   - **Lease read.** When the guard that opens `coordinatesSession` holds, the helper returns `ForwardLocal` without a read, and both call one method for that guard. Otherwise the helper reads the lease once. When the holder equals `s.replicaID`, it returns `ForwardLocal`. A read error, no holder, a false result from `HolderAddress` (item 2), or a nil forwarder returns `ForwardNotDelivered`.
   - **Outbound request.** The request carries the method, path, and raw query of `r`, the body the caller passes, `Content-Type`, `Content-Length`, `Accept`, and the credential from `auth.CredentialFrom`. It carries no other client header. When the context carries no credential and its principal's `CallerType` is `agent`, the request carries the principal's session identifier in `X-Lenny-Calling-Session` instead (rule 3). The caller passes either the bytes it already read or the unread `r.Body`. A site must not consume part of the body before the call.
   - **Client.** `NewHolderForwardClient(reloader *certreload.Reloader, roots *x509.CertPool) *http.Client` builds the one shared client, and `sessionserver.Options.HolderForwardClient` carries it. A nil value is the nil forwarder of **Lease read.** The client uses a transport whose `DialTLSContext` derives one context with a 2-second timeout from its dial context and runs both the TCP dial and `tls.Client(conn, cfg).HandshakeContext` under it, where `cfg` is `tls.Config{GetClientCertificate: reloader.GetClientCertificate, RootCAs: adapter CA pool, ServerName: gatewaycontrol.GatewayDNSName, MinVersion: tls.VersionTLS13}`. This is the bound §10.1.1 **Replica address:** states. **Trap:** a transport with `DialTLSContext` set ignores `TLSClientConfig` and `TLSHandshakeTimeout`, so `cfg` is applied inside the dial function. A `net.Dialer.Timeout` paired with `TLSHandshakeTimeout` bounds each step separately and allows 4 seconds in total. The client sets no `Timeout`. The outbound request runs on the caller's context, `r.Context()` in `forwardToHolder` and `ctx` in `ForwardToolCall`, and the helper adds no deadline of its own (§29.3 **Forwarding.** rule 8), because a request served on the coordinating replica runs on its request context with no deadline either. The helper never retries.
   - **Classification** (rules 6 and 7). An `httptrace.ClientTrace` `GotConn` callback marks the attempt. A failure before `GotConn` is `ForwardNotDelivered`. A failure after it is `ForwardUnknown`, including a failure on a reused keep-alive connection. **Trap:** classifying a reused-connection write failure as not delivered would let a message be held after the coordinator may already have received it.
   - **Responses.** On success, the helper copies the status, the headers other than hop-by-hop headers, and the streamed body to `w`. It writes the 409 `TARGET_NOT_READY` response with `details.currentState` for `ForwardUnknown`, and for `ForwardNotDelivered` on every kind except `ForwardKindMessageNoReplyTo`, whose caller holds the message (CODE-2 item 4).
   - **Idempotency gate** (§11.5). `pkg/gateway/middleware/idempotency` exports `WithGate(ctx)` and `DoNotStore(ctx)`. `DoNotStore` sets the request's gate and does nothing when no gate is installed. `ServeHTTP` installs a gate with `r.WithContext` before it calls the inner handler, and `maybePersist` checks the gate first and releases the pending row as it does for a 5xx, with skip reason `forward_refused`. For every outcome **Responses** answers with 409, the core calls `DoNotStore` before it writes or returns the refusal, in both `forwardToHolder` and `ForwardToolCall`. The forward listener (item 6) installs a gate with `WithGate` and adds a gateway-internal no-store mark, in a form the implementor chooses, to a response whose gate is set when its header is written. A helper that receives a delivered response carrying the mark does not copy the mark to `w` and calls `DoNotStore`. **Trap:** recognising every delivered 409 `TARGET_NOT_READY` instead also leaves a coordinator's own §15.1 pre-running refusal uncached.
   - **MCP variant.** `func (s *Server) ForwardToolCall(ctx context.Context, row sessionstore.Session, kind ForwardKind, tool string, arguments json.RawMessage) (mcp.ToolResult, ForwardOutcome, error)` runs the same core for the two MCP sites in CODE-2 item 6 and writes nothing. It posts `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":<tool>,"arguments":<arguments>}}` to the coordinator's `/mcp` with `Content-Type` and `Accept` set to `application/json`. The credential and `X-Lenny-Calling-Session` follow **Outbound request**. Neither forwarded tool reads the §10.6 explicit environment, so the post goes to `/mcp` whichever MCP path the client used. The returned error is nil or a `*mcp.ToolError`, and these rules decide the return:
     1. `ForwardLocal`, and `ForwardNotDelivered` for `ForwardKindMessageNoReplyTo`, return a zero result and a nil error.
     2. An outcome **Responses** answers with 409 returns the tool error `TARGET_NOT_READY` with `details.currentState` set to `row.State`.
     3. A delivered HTTP 200 JSON-RPC `result` whose `isError` is false returns `ForwardDelivered`, that result, and a nil error.
     4. A delivered HTTP 200 JSON-RPC `result` whose `isError` is true, or a delivered JSON-RPC `error`, returns `ForwardDelivered` and a tool error carrying the `code`, `message`, and `details` of its §15.2.1 lenny error envelope, read from the `lenny/error` content block or from `error.data`. The serving replica's server writes it as it writes a local handler error, so `PreToolResult` does not run over it.
     5. A delivered non-2xx response carrying a §15.1 error envelope, which on `/mcp` is a refusal by the forward listener's auth middleware, returns `ForwardDelivered` and a tool error carrying the envelope's `code`, `message`, and `details`, decoded with the `errorEnvelope` type `ServiceCall` uses. The client receives a tool error rather than an HTTP status because its own `/mcp` response or WebSocket frame is already a JSON-RPC exchange, as `dispatch` in `client_tools.go` projects a `ServiceCall` refusal.
     6. A delivered response that rules 3 to 5 cannot decode is `ForwardUnknown` and takes rule 2.

     `pkg/gateway/mcpfabric/mcp` exports the decoder rules 3 and 4 use, beside the `writeResult`, `WriteLennyError`, and `toolErrorResult` encoders it inverts. Package mcptools reaches `ForwardToolCall` through a one-method consumer interface, `HolderForwarder`, on a new `Deps.HolderForward` field. `*sessionserver.Server` satisfies it, and `cmd/lenny-gateway/mcpsurface.go` sets it to the server it already passes as `SessionService`. A nil `HolderForward` makes each site run its local path, so tier-1 harnesses that leave it unset keep their behavior.
9. **Comments.** In `migrations/0164_coordination_lease.up.sql`, change the comment that describes `coordinator_replica` as the OTel `service.instance.id`, so that it describes the column as the coordination holder value (§10.1.1 **Replica address:**). Replace `$this_replica_id` with `$this_holder_value` in the §10.1.8 query copies in that file's header comment and in the `pkg/gateway/coordination/coordlease/coordlease.go` package doc comment. The edit changes no SQL.

### CODE-2 · (R19) Forward at every forwarded route and both MCP tool sites, DLQ fallback, and receiver check

Targets:
- `pkg/gateway/sessionserver/interrupt.go` `handleInterrupt`
- `session_transitions.go` `handleTerminalWrite`
- `resume.go` `handleResume` and `resumeHeldPodRequest`
- `upload_to_session.go`
- `messages_delivery.go` `parseMessageBatch`
- `messages.go` (comments only)
- `pkg/gateway/mcpfabric/mcptools/mcptools_register.go`: the `lenny/send_message` and `lenny/cancel_session` handlers
- `send_delivery.go` (the hold on not-delivered)
- `messaging.go` `messagingLimiter.allow` (the sender-keyed and target-keyed split)
- `tests/testinfra/lifecycleharness/harness.go` and `cells.go`, and one subtest name in `tests/tier7a_load_local/suspension_interleaving_test.go` (item 7)
- `tests/tier5_e2e_kind/ha_streaming_replica_test.go` and `gateway_replica_continuity_test.go` (item 8)

`route_table.go` is not a target. `upload.go` is not a target, because its §15.1 precondition check passes no `midSessionUpload` lift and so refuses `running`, the state the §29.3 upload row forwards, before the forward point (summary **Upload route name.**).

1. **Placement.** Each handler calls `forwardToHolder` itself, after its existing entry read and §15.1 precondition check and before any write, pod RPC, or inbox or DLQ write (§29.3 **Forwarding.** rule 1). It returns at once on any outcome other than `ForwardLocal`. The call sites are:
   - `handleTerminalWrite`, after `session.Validate`;
   - `handleInterrupt`;
   - the upload-to-session handler;
   - `resumeHeldPodRequest`;
   - `parseMessageBatch`, after it decodes and validates the batch and before its `inReplyTo` resolve loop. It reads the body into bytes through `jsonReader`, decodes the batch from those bytes, and passes the same bytes to `forwardToHolder`. A call in `restDelivery.route` comes after that loop, so a reply it resolved would be forwarded and delivered again.

   No mux-level wrapper is added, and no second row read is made.
2. **State gate** (`forwardStates`). These are the states of the §29.3 rows in which the session holds `REG-COORDLEASE` (§10.1.1), as persisted row values. **Trap:** the lookup matches the row value literally, so `input_required` and `resuming` are listed where a §29.3 row names only `running` or `resume_pending`.
   - terminate and delete: `starting`, `running`, `input_required`, `resume_pending`, `resuming`, `awaiting_client_action`, and `suspended` while the row names a pod;
   - message: `running` and `input_required`;
   - interrupt: `running` and `input_required`;
   - upload: `running`;
   - resume: `suspended` with a pod.

   Terminate and delete in `created`, `finalizing`, `ready`, and `suspended` without a pod keep the local `handleTerminalWrite` path unchanged.
3. **Receiver check.** CODE-1 item 8's forwarded-request branch is the receiver check. Reword the existing `coordinatesSession` check in `resumeHeldPodRequest` in place, so that it calls the helper's outcome rather than answering its own 409. Its refusal carries `currentState` and no replica identity.
4. **Not delivered and unknown.**
   - At the message sites, `ForwardNotDelivered` for a request none of whose messages carries `inReplyTo` (the only request that takes `ForwardKindMessageNoReplyTo`) calls `holdMessage` with the TTL of the recovering-state row of the §7.2 dead-letter table, and answers the receipt that `holdMessage` returns: `queued`, `dropped` with `dlq_overflow`, or `error` with `inbox_unavailable`.
   - Every other request, a batch in which any message carries `inReplyTo` included, gets the refusal of CODE-1 item 8 **Responses**, or at an MCP site **MCP variant.** rule 2.
5. **What stays.** `ActionBufferInbox` (§7.2 path 3), `executorFailure`, and `handleTerminalWrite` stay. Once the forward runs first, the coordinator, or a single-gateway deployment, is the only place these paths run. Update their doc comments to say so.
   - **Trap:** `ActionBufferInbox` is the path-3 classification, which a forwarded `input_required` message reaches on the coordinator. It is not an off-holder branch, and deleting it breaks path 3.
   - In `messages.go`, delete only the elided `ForwardMessage` and "cross-replica coordinator routing" comment lines.
6. **MCP sites.**
   - The `lenny/send_message` handler applies the sender-keyed messaging limits, `msg:out` and the lifetime cap, right after its target row read and cross-tenant check, except when `sessionserver.Forwarded(ctx)` is true, and then calls `ForwardToolCall` in the message states. `messagingLimiter.allow` splits into a sender-keyed and a target-keyed method. Its tier-1 callers in `messaging_internal_test.go` move to the two methods in the same change: the sender-keyed method counts `msg:out` and the lifetime cap and counts nothing for an empty sender, and the target-keyed method counts only `msg:in`. The call comes before the `messagingScope` check, the target-keyed `msg:in` limit, the `inReplyTo` resolution, and `runPreMessageDelivery`, so that the coordinator runs each of them once. On `ForwardNotDelivered` for a message without `inReplyTo`, the handler runs those steps locally and then holds the message as item 4 states, instead of classifying it in `mcpSend.route`. On every other outcome except `ForwardLocal`, the handler returns `ForwardToolCall`'s result and error (CODE-1 item 8 **MCP variant.**). **Trap:** the lifetime counter is process-local, so counting it on each target's coordinator gives a sender one cap per coordinating replica.
   - The `lenny/cancel_session` handler calls it the same way, after its row read and `session.Validate` and before `cancelSession`, in the terminate-and-delete states, and returns its result and error on every outcome except `ForwardLocal`, under every reason. **Trap:** under reason `playground_client_closed` the handler answers `accepted: true` to its other refusals, so the return comes before that path.
   - The MCP client tools that dispatch through `ServiceCall` to REST paths reach item 1's handler sites and need no code of their own.
7. **Harness forward lane** (`tests/testinfra/lifecycleharness`). Every harness replica forwards as a gateway replica does. This item lands with item 1, because from then on every multi-replica scenario sends its off-holder requests through the forward.
   1. **Certificates.** `New` mints one `gatewaypki` material (CODE-1 item 5), and every replica presents its gateway leaf.
   2. **Holder value.** `AddReplica` opens the replica's forward listener on `127.0.0.1:0` before it builds the session server, and builds its holder value for that address with `sessionserver.HolderValue` (CODE-1 item 2). It sets `Replica.ID` to that value and passes it as the session server's and the Sweeper's `ReplicaID` in place of the bare name. The harness's other lease consumers already read `Replica.ID`. The subtest of `suspension_interleaving_test.go` named from `sender.ID` is named from the replica's position instead, because the holder value differs on every run.
   3. **Client.** The session server's `HolderForwardClient` is `NewHolderForwardClient` over the material (CODE-1 item 8 **Client.**).
   4. **One MCP registration per replica.** `AddReplica` builds `Replica.MCP`, one `mcp.Server` on which `mcptools.Register` runs once. Its deps are the ones `MCPCall` passes today, `HolderForward: r.Server` (CODE-1 item 8 **MCP variant.**), and the values of two new harness `Options` fields, `MessagingRateLimit` and `Interceptors`. `MCPCall` and the listener both use it, so the lifetime counter and the result interceptor persist across calls.
   5. **Listener.** The listener serves `HolderForwardServerTLS` and `NewHolderForwardHandler` (CODE-1 item 6) over an inner mux that routes `/mcp` to `Replica.MCP` and every other path to the session server, with `auth.Options{AllowDevHeaders: true, DevHeaderNamesTenant: true}` and `h.Inner` as the session resolver.
   6. **Public path.** `Do` and `MCPCall` send through the same mux behind `auth.Wrap` with the same options, carrying the `X-Lenny-Tenant-ID` header `Do` sends today, so that header is the credential CODE-1 item 7 records. `PlatformCall(r, sessionID, tool, args)` dispatches through `platformtools.New(r.MCP, h.Inner)`.
   7. **Faults.** `SetForwardRefuse(on bool)` makes the listener close each accepted connection before the TLS handshake. `SetForwardFault(f func(*http.Request) bool)` runs f after the listener has read a request and before the chain, and closes the connection with no response when f returns true. A nil f disarms it. Arming `SetForwardRefuse` also closes the listener's open connections and calls `CloseIdleConnections` on every replica's `HolderForwardClient`, so the next forward dials. `Crash` also closes the replica's listener.
   8. **Lease store.** `Options.Leases func(leasestore.LeaseStore) leasestore.LeaseStore`, when set, wraps the harness's Redis lease store.
8. **Kind tests.** The Kind e2e install forwards nothing (see Edge cases). `ha_streaming_replica_test.go` therefore terminates through replica A, and `terminateWithHandoffRetry` in `gateway_replica_continuity_test.go` tries the terminate through each Ready gateway pod in turn. Update the comments that say replica B coordinates the terminate.

### CODE-3 · (R21) The drain publishes its transitioned sessions on `CH-BINDRELEASE`

Targets:
- `pkg/gateway/externalapi/admin/drain.go`
- `pkg/gateway/externalapi/admin/tenant_suspend.go`
- `cmd/lenny-gateway/adminrouter.go`
- `cmd/lenny-gateway/tenantdeletion.go`
- `pkg/gateway/externalapi/admin/drain_test.go`
- `tests/testinfra/lifecyclefence/world.go`
- `tests/tier7a_load_local/creation_fence_race_test.go`
- `tests/tier7a_load_local/replay_transcript_erasure_race_test.go`

The type names below are the CODE-4 names.

1. `DrainSessions` gains the parameter `pods UserPodTerminator`. This is the existing interface in package `admin`. The change adds no new seam. The callers in the test files among the targets pass `nil`.
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
- Update the imports and uses in `cmd/lenny-gateway/adminrouter.go`, `billing_security_workers.go`, `wiring_fields.go`, `user_revocation.go`, `user_revocation_test.go`, and `release_wiring_test.go`, in `tests/tier3_contract/fullrevoke_release/request_contract_test.go`, and the moved `propagator_test.go` path in `tests/spec-map.json`.
- In `scripts/refactor/manifest`, the `pkg/gateway/podterminate` entry's destination becomes `pkg/gateway/podlifecycle/bindrelease`, because `TestCommittedManifestDestinationsExist` fails on a missing destination. In `pkg/gateway/ARCHITECTURE.md`, `bindrelease` replaces `podterminate` in the pod-lifecycle package list.
- Doc comments name `CH-BINDRELEASE`.

The Redis channel string `lenny:session:terminate`, the message fields, and `admin.UserPodTerminator` stay unchanged. The change alters no behavior.

## Staged schema, chart, and migration changes

### CHART-1 · (R18) Port, advertise address, listener, and NetworkPolicy self-peer

- `charts/lenny/values.yaml` sets `gateway.holderForwardPort: 50054`, with a comment citing §10.1.1 and §13.2.4.
- `charts/lenny/templates/gateway-deployment.yaml`:
  - adds env `LENNY_POD_IP` from `fieldRef: status.podIP`;
  - adds `LENNY_HOLDER_FORWARD_ADVERTISE_ADDR: "$(LENNY_POD_IP):{{ .Values.gateway.holderForwardPort }}"`, declared after `LENNY_POD_IP`;
  - adds `LENNY_HOLDER_FORWARD_LISTEN_ADDR: ":{{ .Values.gateway.holderForwardPort }}"`;
  - adds the container port `holder-forward`;
  - fails rendering, citing §10.3 NET-072, when `mtls.enabled` is false, `global.devMode` is false, and Redis is configured (`$hasRedis`, `$hasRedisCluster`, or `$useSentinel`), because that gateway refuses to start (CODE-1 item 4).
- The comments that offer `mtls.enabled: false` to a service-mesh install add that the render refusal above applies, citing §10.3 NET-072. They are the `mtls.enabled` comment in `charts/lenny/values.yaml`, the header comment of `charts/lenny/templates/mtls-pki.yaml`, and the `/etc/lenny/mtls/` mount comment in `charts/lenny/templates/gateway-deployment.yaml`.
- `charts/lenny/templates/system-network-policies.yaml`:
  - `allow-gateway-egress` gains `to: [{podSelector: {matchLabels: {lenny.dev/component: gateway}}}]` with `ports: [{protocol: TCP, port: {{ .Values.gateway.holderForwardPort }}}]`;
  - `allow-gateway-ingress` gains the mirror `from` rule. No other source is admitted on that port.
- Regenerate `pkg/embedded/manifests/manifests.yaml` with `make generate`.

## Staged docs changes

### DOCS-1 · Operator security guide: gateway-to-gateway forwarding

In `docs/operator-guide/security.md`:
- Under `### Gateway mTLS Identity`, add a bullet stating that replicas forward session requests to each other over mutual TLS on port 50054. Each replica presents its gateway certificate, accepts only a peer presenting the gateway certificate, and re-checks the client's credential on the forwarded request. A forwarded platform tool call from an agent carries no client credential, so the receiving replica runs it as the calling session that the forwarding replica names. In the `lenny-system` namespace, the `allow-gateway-ingress` and `allow-gateway-egress` policies admit only gateway pods on port 50054.

### REC-1 · Claim register and BUILD-GAPS records

**Claim register.** `tests/claim-map.json` is generated by `scripts/seed-claim-register.py`; regenerate it after each edit and do not hand-edit the JSON. `SURFACE_OVERRIDES` cannot rename or re-anchor a row generated from the frozen reference table, so a generated row the table renames or replaces is listed in `RETIRED`, whose comment is widened to cover a claim an `EXPLICIT` row restates, and its replacement is added to `EXPLICIT` with the status and step the table gives and the anchor it gives, else the retired row's anchor. "Session inbox redelivery" and "Input-wait resolution across replicas" change through `SURFACE_OVERRIDES`. The drain row and the new rows are `EXPLICIT` rows.

| Row | Change | Status at S-spec landing | Flipped to `WIRED` by |
|:--|:--|:--|:--|
| "Cross-replica message forward" | Rename to "Off-holder request forward on `CH-HOLDERFORWARD`". The surface becomes `holder_forward.go` `forwardToHolder` | ABSENT, R19 | CODE-2 |
| "`coordinator_address` population" | Replace with "Replica address in the `REG-COORDLEASE` holder value", anchor `#2854-inter-replica` | ABSENT, R18 | CODE-1 |
| New: "Inter-replica forward listener with gateway peer validation (NET-072)" | Replaces the generated row "Gateway-to-gateway transport". Anchor `#2854-inter-replica` | ABSENT, R18 | CODE-1 and CHART-1 |
| "Session inbox redelivery" | The surface and note become the DLQ hold of §29.3 **Forwarding.** rule 6 | ABSENT, R19 | CODE-2 |
| "Input-wait resolution across replicas" | Deferral becomes R20 | ABSENT, R20 | R20, outside this proposal |
| "Lifecycle drain releases the pod of a session bound on another gateway replica before the drained scope's rows are erased" (the R21 row, not the R16 row "Lease or mirror release on gateway drain") | Anchor becomes `#2857-gateway-to-store`. The surface becomes the `DrainSessions` publish through `bindReleaseFanOut.TerminateUserSessions` | ABSENT, R21 | CODE-3, after proposal 0100's CODE-7 item 6 |
| New: "`CH-BINDRELEASE` binding-release fan-out" | Surface `cmd/lenny-gateway/adminrouter.go` propagator construction. Anchor `#2857-gateway-to-store` | WIRED | — |
| "Tool-approval resolution across replicas" | Unchanged | WIRED | — |

**BUILD-GAPS.md.** Record a resolution on each finding when its step lands:
- F-29.3.2, F-29.3.1, and F-10.1.33 when CODE-2 lands;
- F-7.2.38 when TEST-3 un-skips its case;
- F-12.8.43 when CODE-3 and proposal 0100's CODE-7 item 6 have both landed.

**Trap: currency tests.** Each tier-11 test below pins a row or finding REC-1 changes; update it in the commit that makes the change:
- `TestClaimRegisterCarriesTheDrainTerminalTailResidual` (`build_gaps_data_lifecycle_records_currency_test.go`) selects the drain row by `#2854-inter-replica`, so the REC-1 commit that moves the anchor changes its filter and diagnosis to `#2857-gateway-to-store`. With the F-12.8.43 resolution and the drain-row flip, delete that test and set the F-12.8.43 entry of `secondHalfNotes` to open false.
- `TestSessionInboxRedeliveryRowStaysAbsentUnderForwarding_spec_29_3` (`build_gaps_suspend_resume_records_currency_test.go`) pins the "Session inbox redelivery" row's status and surface, so it is rewritten in each commit that changes that row.
- `TestBlockedSkipsNameOpenFindings_spec_7_2` (same file) fails when no test skips on F-7.2.38, so the TEST-3 commit drops its F-7.2.38 probe and its 0092 deviations check.
- `TestReleaseOwnershipDeparturesStayRecordedOnOpenFindings` (`build_gaps_release_ownership_records_currency_test.go`) requires F-10.1.33 open, so the F-10.1.33 resolution removes F-10.1.33 from `annotatedReleaseFindings`.

## Testing

Every test carries `// spec:` with section numbers and headings. Every test at tier 2 or above carries a `// diagnosis:` comment. Each item names the assertion that discriminates the behavior.

### TEST-1 · (R18) Holder value, listener, credential carriage, and helper classification

Tiers 1, 2, and 3.

1. **Holder value** (`pkg/gateway/sessionserver/holder_forward_test.go`). Table cases over `HolderValue` and `HolderAddress` (CODE-1 item 2):
   - IPv4;
   - unbracketed IPv6, which is bracketed in the result;
   - already-bracketed IPv6, which is returned unchanged;
   - a DNS name;
   - a missing port, a port of 0, a port of 65536, an empty host, and an unmatched bracket, each rejected;
   - `HolderAddress` on a bare identity, which returns false;
   - an identity that contains `@`, which splits at the last `@`;
   - two calls with the same identity and address, which return different values.

   `// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination)`.
2. **Consumer set** (`cmd/lenny-gateway/coordination_holder_wiring_test.go`). Every consumer CODE-1 item 3 lists receives the same composite value. The fencer case asserts that a relinquish drops a lease held under that value, because `coordfence.Fencer` does not expose its holder. The ops `Source`, billing, and `service.instance.id` keep `resolveReplicaID`. Add a pgstore round-trip of a composite holder string against a real Postgres container, at tier 2 in its own file with the `component` build tag. Cover the startup refusals: no advertise address with a lease store, and no certificate outside dev mode. Cover the dev-mode start without certificates, which leaves `HolderForwardClient` nil. `// spec: 10.1.1, 10.3 (mTLS PKI)`.
3. **Listener mTLS** (`pkg/gateway/sessionserver/holder_forward_listener_test.go`, over `gatewaypki` material). The listener accepts a gateway-SAN client certificate. It rejects plaintext, a TLS client with no client certificate, a cluster-CA certificate with a different DNS SAN, and an agent SPIFFE certificate, each at handshake. After the certificate and key files are replaced, a new handshake presents the new leaf on both the listener and the client. A forwarded request is not rate-limited or idempotency-cached: a repeated `Idempotency-Key` reaches the handler twice. `// spec: 10.3, 29.3 (Interactive message send)`.
4. **Helper classification** (`holder_forward_test.go`).
   - Holder equal to self, and a server with no lease store, run locally with no dial.
   - A lease read error, no holder, a holder without an address, and a nil forwarder are not delivered.
   - A dial refused, a dial that hangs, and a handshake with a server that presents a cluster-CA certificate with a different DNS SAN or an agent SPIFFE certificate are not delivered. The hanging-dial case runs under a caller context with no deadline and asserts that the attempt ends at 2 seconds. A dial that completes after 1.5 seconds followed by a hanging handshake also ends at 2 seconds.
   - A server that closes after reading headers, and a server that resets mid-body, give an unknown outcome. This is the discriminating case for rule 7.
   - A reused keep-alive connection that fails on write gives an unknown outcome.
   - A coordinator that answers 35 seconds after it reads the request, for a message and for an interrupt, returns its response to the caller with no 409. The two rows run in parallel. Cancelling the caller's context after the coordinator has read the request gives an unknown outcome.
   - A forwarded-context request on a non-holder is not delivered and makes no dial.
   - Under the §11.5 middleware, a test handler at `/v1/sessions/{id}/resume` that calls `forwardToHolder`, and one at `/mcp` that calls `ForwardToolCall`, store no idempotency row for a keyed request when the forward is not delivered, has an unknown outcome, or reaches a non-holder. A same-key retry reaches the helper again, and the client response carries no mark. A delivered coordinator 409 without the mark is stored.
   - `ForwardToolCall`, against a coordinator that is an `mcp.Server` behind the auth middleware, returns for each response of CODE-1 item 8 **MCP variant.** rules 3 to 6 what that rule gives, and a test tool returning that return delivers the coordinator's code to its client. The rule 4 rows are a tool error and a pre-dispatch JSON-RPC error. The rule 5 rows are 401 `TOKEN_EXPIRED`, 401 `TOKEN_INVALID`, and 503 `token_validation_unavailable`. The rule 6 row is a 200 body that is not JSON-RPC.
   - A state outside the gate, and a terminate on a `suspended` row that names no pod, run locally with no lease read. The suspended case is the one that discriminates, because `suspended` is in the terminate set.

   `// spec: 29.3, 10.1.1, 11.5`.
5. **Credential carriage.** Through test handlers that call `forwardToHolder` and a test tool that calls `ForwardToolCall`, as in item 4, a REST bearer, MCP through `ServiceCall`, and a WebSocket-upgraded connection each send the recorded credential. A request with no credential reaches the coordinator with none and is refused with `UNAUTHORIZED`. A WebSocket-upgraded connection whose credential has expired since the upgrade forwards it and is refused with `TOKEN_EXPIRED`, while a call on the same connection that the serving replica coordinates succeeds. `TOKEN_EXPIRED` is the code that discriminates against a listener that waives expiry. The REST client receives the coordinator's 401, and the MCP client receives the tool error of CODE-1 item 8 **MCP variant.** rule 5. No client header other than the content headers, `Accept`, and the credential is copied. `// spec: 29.3, 15.2 (MCP API), 27.3.1, 13.3 (Credential Flow)`.
6. **Tier 3 contract** (`tests/tier3_contract/holderforward/`). The forwarded HTTP request is byte-equal to the client's request in method, path, query, and body. The coordinator's status, headers, and body come back unchanged, including a 4xx body and a streamed upload response. A test tool calling `ForwardToolCall`, as in item 4, posts the request CODE-1 item 8 **MCP variant.** gives: from an MCP-client context it carries the credential and no `X-Lenny-Calling-Session`, from a context `platformtools.Bridge.CallPlatformTool` built it carries `X-Lenny-Calling-Session` and no credential, and the coordinator's test tool sees the principal that call installs. The test compares the posted body and the header name with string literals rather than the Go identifiers, and that comparison is the one that discriminates. `// spec: 28.5.4 (Inter-replica), 29.3`.

### TEST-2 · (R19) Off-holder routes on two replicas

Tiers 1 and 4. Tier 1 runs each wired site against an `httptest` TLS server that presents `gatewaypki` material (CODE-1 item 5) and is reached through `NewHolderForwardClient`. Every tier-4 case runs in `tests/tier4_integration` on the CODE-2 item 7 harness, except the item 5 case assigned to the subprocess lane. On the harness, "the coordinator unreachable" is `SetForwardRefuse`, and "a forward whose connection fails after it is established" is `SetForwardFault`. The subprocess lane is one `cmd/lenny-gateway` subprocess (CODE-1 item 5) started with the `gatewaypki` files as `--adapter-tls-cert`, `--adapter-tls-key`, and `--adapter-ca`, and with `--redis-url` pointing at a dev Redis. It takes only requests sent straight to its forward-listener address, because it has no pod binder and so never leaves a session off-holder.

1. **Message to `running` off-holder.** The receipt comes from the coordinator. No inbox write happens on the serving replica, and no 500 is returned. `// spec: 29.3, 7.2 (Interactive Session Model)`.
2. **Message carrying no `inReplyTo` to `input_required` off-holder.** The coordinator buffers it under path 3. With the coordinator unreachable, the message is in the DLQ with the recovering-row TTL, and the coordinator delivers it at its next path-2 delivery. The only serving-replica write is that DLQ enqueue. Cover DLQ overflow (`dropped`, `dlq_overflow`) and a DLQ write error (`error`, `inbox_unavailable`). Cover an enqueue that races a terminal transition (§7.2 **Enqueue racing a terminal transition.**). `// spec: 29.3, 7.2`.
3. **`inReplyTo` reply off-holder.** With the pod's `LNK-GWCONTROL` connection on the coordinator, the forwarded reply resolves its `lenny/request_input`. With the coordinator unreachable, the result is 409 and the DLQ stays empty, including for an `inReplyTo` that matches no outstanding call and for a batch in which one message of several carries `inReplyTo`. At tier 1, on an off-holder server whose inputwait registry holds a pending call for a session in `input_required`, an `inReplyTo` message naming that call is forwarded with a body byte-equal to the client's, and the wait stays pending; the pending wait is the discriminating assertion. `// spec: 29.3`.
4. **Held-pod resume off-holder.** The result is 200 `running`, written by the coordinator. With the coordinator unreachable, the result is 409 with no state write. `// spec: 29.3, 7.2`.
5. **Terminate and delete off-holder in every gated state.** The coordinator writes `completed` or `cancelled` and runs its termination sequence. No terminal write happens on the serving replica. The `input_required` cases and the `resuming` cases are the ones that discriminate the gate's literal lookup. With the coordinator unreachable, and with a forward whose connection fails after it is established, a terminate and a delete in `running` get the refusal of CODE-1 item 8 **Responses**, and the serving replica writes no state, which is the assertion that discriminates. In the subprocess lane, a terminate sent to the subprocess's forward-listener address with a `gatewaypki` gateway client certificate, naming an `acme` session and carrying a valid `globex` credential, gets 404 `RESOURCE_NOT_FOUND` and writes nothing (rule 3). In `created`, `ready`, `finalizing`, and `suspended` without a pod, the write is local with no 409. `// spec: 29.3, 15.1 (REST API)`.
6. **Interaction resolution.** Approve, deny, respond, and dismiss posted to the non-coordinator resolve the coordinator's pending wait with no forward. A second resolution of the same call, posted to the other replica, is refused, and the blocked call keeps the first resolution. `// spec: 29.3`.
7. **Stale forward.** A forward that reaches a former holder returns 409 or a DLQ hold, with no write and no pod RPC. `// spec: 29.3, 28.6 (Exclusivity and concurrency model)`.
8. **Upload.** `/upload-to-session` off-holder is forwarded. **Interrupt.** Off-holder interrupt in each gated state is forwarded, and the serving replica writes nothing. The `input_required` case discriminates. With the coordinator unreachable, and with a forward whose connection fails after it is established, an interrupt in `running` gets the refusal of CODE-1 item 8 **Responses** and the serving replica writes nothing. `// spec: 29.3, 7.4 (Upload Safety)`.
9. **MCP.** An off-holder `lenny/send_message` or `lenny/cancel_session` from an MCP client forwards the client's `tools/call` with its credential. An off-holder `lenny/send_message` platform call forwards with the calling session. The coordinator refuses a target outside the sender's `messagingScope` with `SCOPE_DENIED`, and it delivers an in-scope message with `from.kind` `agent`; these two assertions fail under a REST-route forward. With the coordinator unreachable, an out-of-scope `lenny/send_message` is refused with `SCOPE_DENIED` and the DLQ stays empty, and an in-scope one, from an MCP client and as a platform call, is held in the DLQ with the recovering-row TTL and returns `queued`. With a forward whose connection fails after it is established, and with the coordinator unreachable for a message carrying `inReplyTo`, an off-holder `lenny/send_message` returns the tool error `TARGET_NOT_READY` with `currentState` and the DLQ stays empty. With the coordinator unreachable, and with a forward whose connection fails after it is established, an off-holder `lenny/cancel_session`, with and without reason `playground_client_closed`, returns the tool error `TARGET_NOT_READY` with `currentState`, and the serving replica writes nothing. With `maxPerSession` 3, one sender's sends to a target its serving replica coordinates and to a target the other replica coordinates, one of them held while the other replica is unreachable, are refused with `RATE_LIMITED` at the fourth send and not before. `PreToolResult` fires once per forwarded call. At tier 1, in the `messaging_handler_test.go` harness with `maxPerMinute` 1 and `maxPerSession` 1, two `lenny/send_message` calls from one sender (set by `fromSessionId`) under a context marked by `sessionserver.WithForwarded` are both delivered, and the next unmarked send from that sender is delivered and the one after it is refused with `RATE_LIMITED`. `// spec: 29.3, 15.2, 7.2, 8.3 (Delegation Policy and Lease)`.
10. **Idempotency.** At tier 1, the TEST-1 item 4 idempotency cases run against the wired `resumeHeldPodRequest` and `lenny/cancel_session` sites. `// spec: 29.3, 11.5`.

### TEST-3 · (R19) Concurrency

Tier 7a, in `tests/tier7a_load_local`.

- Un-skip the F-7.2.38 case in `suspension_interleaving_test.go`.
- Add randomized interleavings of off-holder forwards against coordinator handoff and lease expiry. The invariant checker asserts that no send is delivered twice, that no send is both delivered and held, and that every 2xx receipt corresponds to a delivery or a DLQ hold. A 409 or 500 refusal is accepted with or without delivery (§29.3 **Forwarding.** rules 5 and 7).
- Alternate CODE-2 item 7's `SetForwardRefuse` and `SetForwardFault` on the coordinator across the interleavings.

`// spec: 29.3, 10.1.2 (Coordinator Handoff Protocol)`.

### TEST-4 · (R21) Drain publish and rename

Tiers 1 and 4.

- **`drain_test.go`.** Only transitioned IDs reach the fake `UserPodTerminator`. Rows already terminal, `ErrNotFound` rows, and failed rows are excluded. The terminator is not called for an empty set or a nil terminator. A cancelled context still delivers the call. `DrainResult` does not change with the terminator's outcome.
- **`release_wiring_test.go`.** The erasure drain, the tenant-deletion drain, and the admin router receive the same `bindReleaseFanOut`.
- **Subscriber case.** `CH-BINDRELEASE` releases only bindings whose primary row is terminal, and it returns their credential leases.
- **Tier 4.** On the lifecycleharness, with the `harnessPodTerminator` of `full_revoke_two_replica_test.go` as the drain's terminator, user erasure on replica A releases a binding held on replica B.

`// spec: 12.8 (Compliance Interfaces), 11.4 (User Invalidation), 28.5.7 (Gateway-to-store)`.

### TEST-5 · Chaos

Tier 8, in `tests/tier8_chaos`.

- On the CODE-2 item 7 harness, a `SetForwardFault` function that calls `Crash` on the coordinator and returns true kills it after it has read a forwarded message. The result is 409 and nothing is held. `// spec: 29.3`.
- On the CODE-2 item 7 harness over `lifecycleharness.PostgresStore`, with `Options.Leases` returning `leasestore.NewFailover` over the Postgres lease store, make Redis fail with `Redis.SetError` while two replicas serve a `running` session. After a replica holds the session's lease in the Postgres fallback, a message sent to the other replica gets the coordinator's receipt with no 409. `// spec: 10.1.1, 29.3`.
- Give the drain a terminator that releases only replica A's own bindings, as `terminateOn` does for one replica in TEST-4's tier-4 case, so nothing reaches replica B. With proposal 0100's gate landed, erasure waits for the post-terminal window, and the binding is released or the window cap passes. This case lands with CODE-3 after 0100's CODE-7 item 6. `// spec: 28.5.7, 12.8`.

### TEST-6 · Cluster and security

Tiers 1, 5, and 9.

- **Tier 5** (`tests/tier5_e2e_kind`). `lifecycle_drain_kind_test.go` asserts that the remote binding is released after erasure. `// spec: 12.8 (Compliance Interfaces), 28.5.7 (Gateway-to-store)`.
- **Tier 9** (`tests/tier9_security`, the `network_policy_test.go` probe pattern). All probes target one gateway pod IP, selected by both gateway selector labels, on port 50054. A probe in `lenny-system` that carries only the `lenny.dev/component: gateway` label, so that it is not a `lenny-gateway` Service endpoint, does not time out. Any curl exit other than 28 passes, because the Kind install runs no forward listener (CODE-1 item 4). A probe in the unrestricted `default` namespace, after the egress positive control of `TestOpsDenyAllIngress_spec_25_4` in `ops_network_policy_test.go`, times out with curl exit 28, and that timeout is the assertion that discriminates. A request to the public listener carrying `X-Lenny-Calling-Session` is not run as that session. `// spec: 13.2.4, 29.3`.
- **Chart helm-unittest.** Cover the env, the port, and both policy rules, rendered with `gateway.holderForwardPort` set to a value other than 50054 so that each one is shown to read the key, and the render refusal, including a mesh render without Redis that still succeeds. `// spec: 13.2.4, 10.3 (mTLS PKI)`.

### TEST-7 · Tier-11 pins of the replaced spec text

Tier 11. Each item updates a test or comment that pins text a staged spec edit replaces, so it lands in the step that follows the spec steps.

1. **`tests/tier11_docs/spec_28_register_writers_test.go`** (SPEC-1 (a) and (b), SPEC-3 (c)). Delete the `interReplicaLink` constant, its `unreferencedLinkEntries` entry, the "inter-replica link" subtest with `assertInterReplicaLinkMatchesSessionLifecycle`, and the `forwardMessageTransportSentence` and `forwardingReplicaSentence` constants, and drop `LNK-INTERREPLICA` and §28.5.4 from the comments, the diagnosis, and the `spec:` lines. Keep `unreferencedLinkEntries` as an empty map. `underDeclaredLinkEntries` takes the exemption set as a parameter, and the stale-exemption case of `assertSingleReferenceLinkEntryIsRejected` passes a set exempting the twice-referred link and asserts that the report names it, so the exempt branch stays exercised.
2. **`tests/tier11_docs/off_holder_matrix_stated_outcome_test.go`** (SPEC-5 (a) and (d)). The resolution-row case requires the SPEC-5 (d) row's no-forward, first-resolution-wins, and no-unrecorded-resolution clauses, and adds "Forward the resolution to the coordinator" to the phrases the row must not contain. The preamble case scopes from `**Forwarding.**` to the first matrix row, because SPEC-5 (a) spans several paragraphs. It requires the rule 6 fail-closed clause and the closing paragraph's events-rows and resolution-row clauses, and it adds `LNK-INTERREPLICA` to the phrases the scope must not contain.
3. **`tests/tier11_docs/redis_key_prefix_registry_test.go`** (SPEC-6 (b) and (d)). Add `lenny:session:terminate` to `table12_4Prefixes` and to the exception-summary list of `TestRedisKeyPrefixExceptionSummaryAgreesWithTable_0032`. In assertion (c), the §11.4 note requires `` `CH-BINDRELEASE` `` in place of "Redis pub/sub channels". Rewrite the comments and the diagnosis that place `lenny:session:terminate` outside §12.4.
4. **`pkg/gateway/storage/rediskeys/rediskeys.go`** (SPEC-6 (d)). Add `lenny:session:terminate` to the package comment's list of prefixes the wrapper passes through unvalidated, as SPEC-6 (d) adds it to §12.4.

## Edge cases and accepted failure modes

- In dev mode without certificates, the two-replica Kind e2e install (`global.devMode: true`, `mtls.enabled: false`) refuses every off-holder forward with the not-delivered outcome. Single-gateway compose is unaffected, because the holder is always the gateway itself.

## Files touched on application (non-spec)

- cmd/lenny-gateway/coordination_identity.go, sessionsrv.go, redis_quota.go, httpsurface.go, coordination_seams.go, metricsbackfill.go, flags.go, holder_forward_listener.go (new), runserver.go, mcpsurface.go, adminrouter.go, billing_security_workers.go, tenantdeletion.go, wiring_fields.go, user_revocation.go, user_revocation_test.go, release_wiring_test.go
- pkg/gateway/middleware/auth
- pkg/gateway/middleware/idempotency/idempotency.go, pkg/gateway/metrics/gatewaymetrics/gatewaymetrics.go, gatewaymetrics_audit.go
- pkg/gateway/sessionserver/service.go, sessionserver.go, holder_forward.go (new), holder_forward_listener.go (new), interrupt.go, session_transitions.go, resume.go, upload_to_session.go, messages_delivery.go, messages.go
- pkg/gateway/mcpfabric/mcptools/send_delivery.go, mcptools_register.go, mcptools.go, messaging.go, messaging_internal_test.go, messaging_handler_test.go
- pkg/gateway/mcpfabric/mcp/mcp.go
- pkg/gateway/gatewaycontrol/platformtools/platformtools.go
- pkg/gateway/externalapi/admin/drain.go, tenant_suspend.go
- tests/testinfra/lifecyclefence/world.go, tests/tier7a_load_local/creation_fence_race_test.go, replay_transcript_erasure_race_test.go
- pkg/gateway/podlifecycle/podterminate/propagator (moved to pkg/gateway/podlifecycle/bindrelease), pkg/gateway/ARCHITECTURE.md, scripts/refactor/manifest
- migrations/0164_coordination_lease.up.sql, pkg/gateway/coordination/coordlease/coordlease.go (comment), and pkg/gateway/storage/rediskeys/rediskeys.go (comment)
- docker-compose.yml, tests/testinfra/gateway/gateway.go, tests/testinfra/gatewaypki (new), tests/testinfra/lifecycleharness/harness.go, cells.go, tests/tier7a_load_local/suspension_interleaving_test.go, tests/tier5_e2e_kind/ha_streaming_replica_test.go, gateway_replica_continuity_test.go
- charts/lenny/values.yaml, charts/lenny/templates/gateway-deployment.yaml, charts/lenny/templates/mtls-pki.yaml, charts/lenny/templates/system-network-policies.yaml, charts/lenny/tests, pkg/embedded/manifests/manifests.yaml
- docs/operator-guide/security.md
- scripts/seed-claim-register.py, tests/claim-map.json, tests/spec-map.json, tests/change-graph.json, BUILD-GAPS.md, tests/tier11_docs/build_gaps_data_lifecycle_records_currency_test.go, tests/tier11_docs/build_gaps_suspend_resume_records_currency_test.go, tests/tier11_docs/build_gaps_release_ownership_records_currency_test.go
- tests/tier3_contract/fullrevoke_release/request_contract_test.go and the test files TEST-1 to TEST-7 name
