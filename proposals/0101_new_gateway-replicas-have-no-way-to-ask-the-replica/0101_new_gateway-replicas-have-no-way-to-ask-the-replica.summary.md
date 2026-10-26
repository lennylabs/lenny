# Summary: Off-holder request forwarding on CH-HOLDERFORWARD with the replica address in the REG-COORDLEASE value, and drain reuse of the binding-release fan-out (CH-BINDRELEASE)

## Summary

**Problem statement.** The default chart runs two gateway replicas, and sticky routing is only an optimization. A session-scoped request therefore routinely lands on a replica that does not coordinate the session. The specification names no usable carrier or address for forwarding such a request, and the code has no forward. The result is the following:
- A message to a `running` session answers 500.
- A reply to an `input_required` session is lost in the serving replica's inbox.
- A held-pod resume answers 409.
- Terminate and delete write the terminal state on the wrong replica, so the holder-side seal is skipped.
- A drained session bound on another replica keeps its pod after erasure.

The §11.4 binding-release fan-out that could carry that release is in no §28 register.

**What changes.**
- §28.3, §28.5.4, §28.6, and §28.8 change. The new HTTP channel `CH-HOLDERFORWARD` replaces the `LNK-INTERREPLICA` link entry, which §28.3 does not admit for a connection only one channel row refers to, and the `REG-COORDLEASE` holder value carries the replica address (SPEC-1, SPEC-3, SPEC-4).
- §10.1.1 and §10.3 gain the replica address and gateway-to-gateway mTLS peer validation, and §13.2.4 gains the self-peer NetworkPolicy (SPEC-2, SPEC-7, CHART-1).
- §29.3 gains the numbered **Forwarding.** rules. The message fallback moves from the inbox to the DLQ, and the interaction rows are restated as store-and-wake. §27.3.1 **Silent refresh.** gains the forwarded-call sentence (SPEC-5, D20).
- §28.5.7 registers `CH-BINDRELEASE`, and the §12.8 drain publishes on it (SPEC-4, SPEC-6).
- The gateway gains the forward listener, the forward helper, a forward call at every forwarded route and MCP site, and the drain publish, and the propagator package is renamed to `bindrelease` (CODE-1 to CODE-4).

**Decisions.**
- D1. The forward replays the request the serving replica's handler received, a §15.1 REST request or a §15.2 `tools/call`, once over HTTP with mTLS, and the coordinator runs its existing handler. A tool call is forwarded as itself rather than as its REST route, because its own authorization differs from the route's. The design adds no proto and no RPC. A proto would wrap the same body and return the same response.
- D2. `REG-COORDLEASE` is the only authority for the forward target. Its holder value carries the coordinator's address (§10.1.1 **Replica address:**, SPEC-2), and `REG-COORDMIRROR` is never read for routing, because the Sweeper is its only writer and it lags the lease.
- D3. Only the coordination holder value carries the address. `resolveReplicaID` stays unchanged, because changing it would change the ops `Source`, billing, `service.instance.id`, and the pinned identity the §25.3 nonce checkpoint relies on.
- D4. The forward runs on a dedicated port, `gateway.holderForwardPort`, default 50054. The key carries the `CH-HOLDERFORWARD` stem in the camelCase form N7 fixes, so every carrier spells the channel's one identifier (channel naming N4). Port 50051 admits agent pods. The NET-070 listener has no code, and its default port collides with `llmProxyPort`.
- D5. Both ends present the existing gateway certificate, with the `lenny-gateway.lenny-system.svc` SAN. The coordinator authenticates the relayed client credential and authorizes the request, and it accepts no principal asserted by the peer other than the calling session of a forwarded platform tool call, because the pod mTLS identity that authenticated that call on `LNK-GWCONTROL` cannot be relayed. Outside dev mode, a gateway with a lease store and no certificate material refuses to start, so a deployment cannot silently lose its forward. CHART-1 refuses to render that configuration, which rules out a non-dev service-mesh install (`mtls.enabled: false`) with Redis.
- D6. The auth middleware records the verified credential in the request context, so MCP requests built by `ServiceCall`, which carry no `Authorization` header, forward with their credential.
- D7. The dedicated listener sets the forwarded mark in the request context. No header carries it. A forwarded request is never forwarded again and skips the §11.1 and §11.5 middleware.
- D8. A stale forward is answered by the receiver's own lease check together with the never-forward-twice rule, and the pod's generation stamp covers the race after that check. The design adds no expected-holder field, no retry, and no new error code.
- D9. A forward has two failure classes, not delivered and outcome unknown. An outcome-unknown forward answers 409 for every kind and is never held, because holding it could deliver a message twice before R20.
- D10. A not-delivered message carrying no `inReplyTo` is held in the session DLQ, which the coordinator already drains, under the existing §7.2 path-6 bullets. The proposal adds no new §7.2 row, because §29 owns off-holder behavior and the DLQ rules already exist. A not-delivered message carrying `inReplyTo` fails closed, because the serving replica cannot tell whether it answers a pending `lenny/request_input` call, which waits on the replica the pod's `LNK-GWCONTROL` connection reached, and a held message never resolves that call.
- D11. Held-pod resume is forwarded. A durable resume request would need a new marker and a new trigger, and it would make a synchronous response asynchronous.
- D12. Terminate and delete are forwarded, so the coordinator writes the terminal state and runs the seal (F-10.1.33). The refusal no longer carries a replica identity, because the code never sent one and the holder value now contains a pod IP.
- D13. Interaction resolution leaves the forwarded set and is restated as store-and-wake. Tool approval and elicitation already work this way.
- D14. The drain's release reuses the §11.4 propagator. The drain waits for no response, erasure removes any per-session target, and the propagator already exists. A gRPC release was rejected for these reasons.
- D15. The fan-out is registered as `CH-BINDRELEASE` rather than `CH-PODTERMINATE`. Channel naming law N3 forbids a stem the specification already binds to pod termination, and under recycling pools a release keeps the pod. N4 then requires the package rename (CODE-4).
- D16. `ForwardMessage` gets no naming-table row. It was never a channel identifier, a row with no canonical spelling fails the naming-table parser, and its spec occurrences are deleted.
- D17. The `CH-BINDRELEASE` card and the §12.8 sentence do not name the coordination Sweeper as the backstop, because the specification does not state that release. §28.5 forbids a card from supplying a value that no section states. The gap is listed below as an unstaged defect.
- D18. The 2-second dial and handshake bound is stated in §10.1.1. §28.5 forbids a card from stating a value that no section owns.
- D19. The §10.1.1 **Fallback:** bullet gains a sentence saying that the Postgres fallback stores the same holder value, which matches `pgstore`. As a result, the address survives a Redis outage.
- D20. A forwarded credential gets the coordinator's §13.3 `exp` check (§29.3 **Forwarding.** rule 3, SPEC-5 (g)). A coordinator that waived it would honor a revoked playground bearer once its `pg:revoked` marker lapses at the bearer's `exp` plus 5 seconds (§27.3.1). Relaying the serving replica's upgrade-time authentication instead would make the peer assert the principal, which D5 rules out.

**Watch out for.**
- A lease or mirror consumer that misses the composite holder value fails silently (CODE-1 item 3, TEST-1 item 2).
- `ActionBufferInbox` is §7.2 path 3 on the coordinator, and no off-holder branch exists to delete there (CODE-2 item 5).
- A failure on a reused keep-alive connection is outcome-unknown, never not-delivered (CODE-1 item 8).
- The §29.3 upload row names `/upload`, but the pod-touching route in code is `/upload-to-session`, so the forward goes on `/upload-to-session` (CODE-2).
- Gateway tests run with `LENNY_DEV_MODE=false` and Redis now need certificate material (CODE-1 item 5).
- The `lifecycleharness` multi-replica suites at tiers 2, 4, 7a, and 8 send off-holder requests through the forward once CODE-2 item 7 lands with item 1 (CODE-2 item 7).
- The `lenny/cancel_session` handler's `playground_client_closed` path would swallow a forward refusal (CODE-2 item 6).
- Order matters: the checklist **Depends on** fields state the order.

## Goals

- Give every §29.3 forwarding row a carrier, an address, and a coordinator-performed effect.
- Close F-7.2.38, F-29.3.2, F-29.3.1, and F-10.1.33, and close F-12.8.43 together with proposal 0100.
- Register the inter-replica forward channel and the binding-release fan-out in §28, under the channel naming law.
- Keep one authority for the forward target and one normative home for the forwarding rules.

## Non-goals

- A store-then-wake pub/sub channel (`CH-HOLDERWAKE`) for every kind. It leaves interrupt and upload failing closed, keeps the off-holder terminal write, and turns the propagator into a per-session command loop.
- A gRPC service with a proto, either one kind-dispatched RPC or per-kind messages. Each version wraps the same REST body and adds a proto, a generated package, a §28.7 artifact row, and client streaming for uploads.
- Asserting a client principal across the wire. The coordinator authenticates the relayed client credential instead (D5).
- Using the NET-070 internal TLS listener or the 50051 GatewayControl listener (D4).
- Resolving the target through `REG-COORDMIRROR`, or restoring `coordinator_address` (D2).
- A separate replica-address register or key, a JSON lease value, pod DNS through a headless Service, a StatefulSet, or an Endpoints lookup. Each adds a second source of truth or new RBAC.
- Changing `resolveReplicaID` to carry the address (D3).
- An expected-holder or generation field, a `NOT_COORDINATOR` code, a receiver-side in-memory binding check, or a retry after a not-holder answer (D8).
- Failing closed on every message row. The validated problem requires a store that the coordinator reads.
- Holding every off-holder message in the DLQ before the forward. That answers `queued` even when the coordinator is reachable.
- Holding an outcome-unknown message in the DLQ (D9).
- A DLQ fallback for an `inReplyTo` reply, or cross-replica input-wait storage. Cross-replica input-wait storage stays with R20.
- A `ready_for_input` DLQ delivery point. The residual applies only to a live but partitioned coordinator and is bounded by the idle timeout.
- Falling back to the Redis durable inbox when `durableInbox` is true. That would create two custody paths keyed on a mode flag.
- A durable resume request picked up by **Backlog resume.** (D11), or writing the held-pod resume on the serving replica.
- Forwarding admin force-terminate, or routing the drain's forced transition through the forward. These are not §29.3 client routes.
- Running the seal on a peer during the holder's terminal release.
- An absent-row clause in `ReleaseRevokedBinding`. Proposal 0100's post-terminal window keeps the drained row while a delivered request is applied.
- A gRPC or forwarded release for the drain (D14), and delaying erasure by a Sweeper interval. Proposal 0100's gate already makes deletion wait.
- Registering the other Redis pub/sub fan-outs (credential revocation, deny list, credential renewal, and the EventBus).
- Forward metrics, a latency budget, a circuit breaker, a retry policy, or a new alert.
- R21's concurrent-slot eviction leg.
- A hop or forwarded-by header (D7).
- Changing `interrupt.go`'s row-only fallback for a session with no binding. That path now runs only on the holder or in a single-gateway deployment.

## Open decisions for human to make

None.

## Defects in the shipped tree that this proposal does not stage

- **NET-070 internal TLS listener.** No gateway code serves the listener, and its chart default port, 8443, collides with `llmProxyPort`, also 8443, in `charts/lenny/values.yaml`. It is out of scope because the forward uses its own port. The defect is to be filed in BUILD-GAPS.
- **Unspecified backstop release.** The specification states no holder-side release for a binding whose session's terminal write ran on another replica. The code's coordination Sweeper performs that release without a spec basis, and `REG-COORDMIRROR` names the Sweeper only as the mirror's writer. It is out of scope because stating the Sweeper's contract is a §10.1.1 coordination change unrelated to the forward.
- **Postgres fallback locking.** §10.1.1 **Fallback:** and the §28.5.1 Exclusivity field describe a `SELECT ... FOR UPDATE SKIP LOCKED` on the session row. `pgstore` uses a transaction advisory lock and a `session_leases` table. SPEC-2 states only the holder value the fallback stores, and leaves the lock mechanism divergence for a coordination-fallback proposal.
- **Upload route name.** The §29.3 upload row names `POST /v1/sessions/{id}/upload`, while the pod-touching mid-session upload in code is `/upload-to-session`. CODE-2 forwards the code route. Reconciling the names is left to a §15.1 route proposal.
- **Unregistered fan-outs.** The credential revocation, deny list, credential renewal, and EventBus Redis pub/sub fan-outs appear in no §28 register. This is a register-completeness defect outside the forward problem.
- **Admin drain terminal write.** The admin drain's forced transition writes the terminal state on the replica that runs the drain, not on the holder. It remains a recorded departure for a later proposal.
- **Request deadline unenforced.** No gateway code enforces the Request timeout or the Upload timeout of §11.3 (Timeouts and Cancellation). The REST `http.Server` in `cmd/lenny-gateway/httpsurface.go` sets only `ReadHeaderTimeout`, and no handler or middleware bounds the request context. Separately, `PodExecutor.Send` in `pkg/gateway/session/executor/pod.go` returns only after the agent turn's `response`, which departs from path 2 of §7.2 **Message delivery routing — seven paths:**. Both defects affect a request served on the coordinating replica and a forwarded request alike. They are to be filed in BUILD-GAPS, and a gateway-wide request deadline is left to a later proposal.
- **Auth refusal codes outside the catalog.** The auth middleware answers `TOKEN_EXPIRED` and `TOKEN_INVALID`, which neither the §15.1 error catalog nor the `errorclassify` table lists, so `errorclassify.Classify` marks them TRANSIENT and retryable when a tool error carries them. CODE-1 item 8 **MCP variant.** rule 5 makes them reachable from an MCP tool call. Cataloguing the auth codes is left to a §15.1 error-catalog proposal.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0100 (terminal pipeline completeness, part A) | Approved, being implemented | 0101 takes over the F-10.1.33 and cross-replica F-12.8.43 work that 0100 deferred. 0101's §12.8 edit anchors on a sentence that 0100's SPEC-1 (e) leaves in place. | Nothing. 0100 lands as written, and 0101 sequences after it |
| 0092 (suspend and resume) | Implemented | 0101 stages the off-holder resume, off-holder terminate and delete, and the inbox fallback, which 0092 recorded as unstaged defects | Nothing. 0092 is not edited |
| 0093 (release ownership for pods) | Implemented | 0101 retires the off-holder local terminal write that 0093 accepted as a departure, and keeps 0093's rejection of a durable release record | Nothing. 0093 is not edited |
| 0052 (coordinator resolution and fenced lease takeover) | Implemented | 0101 delivers the forward-to-holder transport that 0052 deferred | Nothing. 0052 is not edited |
| 0097 (Lenny-issued bearer tokens verify only on the process that signed them) | Approved 2026-10-24 in this tree's status file; Implemented 2026-10-26 on the integration branch (merge d6830f0df) | 0101 SPEC-7 (b) and 0097 SPEC-10 Edit 1 both edit the Egress cell of the §13.2.4 Gateway row, which stays one physical line. 0101 inserts the gateway-pods peer after the Token Service clause, and 0097 replaces the text that begins with the CoreDNS clause. Neither edit changes the other's anchor text, so both apply in either order. CHART-1 and 0097's chart work each add a peer to `allow-gateway-egress`. CODE-4 moves only the binding-release propagator package, so 0097's `lenny-ops` revocation wiring keeps its subject. 0101 SPEC-5 (g) inserts a sentence into the §27.3.1 **Silent refresh.** bullet after "they continue running under the token they were upgraded with until the connection drops or the token's underlying session expiry is hit." 0097 SPEC-3 Edits 4, 5, and 6 edit other parts of §27.3.1 and leave that anchor intact, so both apply in either order. D20 relies on the §27.3.1 `pg:revoked` marker lifetime, which 0097 does not change. No 0097 deliverable loses its subject. 0097's CODE-4 is a prerequisite of CODE-2 (checklist **Depends on**), because until it lands a coordinator refuses a forwarded bearer that a peer replica minted. | Whichever proposal lands second re-reads the Gateway row, `allow-gateway-egress`, and §27.3.1 and confirms that both proposals' text is present. 0097's approval stands without re-review |

## Deliverable index

- SPEC-1 — spec/28_communication-channels.md — §28.3 `LNK-INTERREPLICA` row and paragraph deleted, `CH-HOLDERFORWARD` and `CH-BINDRELEASE` channel rows, and the `REG-COORDLEASE` Store and Semantics cells.
- SPEC-2 — spec/10_gateway-internals.md — §10.1.1 **Replica address:** bullet and **Fallback:** holder sentence, and the §10.1.8 barrier-target query.
- SPEC-3 — spec/28_communication-channels.md — §28.5.4 lead, diagram, and `CH-HOLDERFORWARD` card.
- SPEC-4 — spec/28_communication-channels.md — §28.5.7 lead, caption, and `CH-BINDRELEASE` card, the §28.6 sentences, and the §28.8 rows and closing paragraph.
- SPEC-5 — spec/29_communication-scenarios.md, spec/11_policy-and-controls.md, spec/27_web-playground.md — §29.3 **Forwarding.** rules and matrix rows, the §29.2 Session start step 9 boundary sentence, the §11.5 **Mechanism.** sentence, and the §27.3.1 **Silent refresh.** sentence.
- SPEC-6 — spec/12_storage-architecture.md, spec/11_policy-and-controls.md — §12.8 drain publish, §12.4 key-table row and enumerations, §12.6 EventBus exclusion, and the §11.4 note and step-2 sentence.
- SPEC-7 — spec/10_gateway-internals.md, spec/13_security-model.md — §10.3 NET-072 paragraph and **Certificate authority:** qualification, and the §13.2.4 Gateway row insertions.
- SPEC-8 — spec/18_build-sequence.md — §18.19 bullet replaces the `ForwardMessage` mention.
- CODE-1 — cmd/lenny-gateway, pkg/gateway/middleware/auth, pkg/gateway/middleware/idempotency, pkg/gateway/metrics/gatewaymetrics, pkg/gateway/sessionserver, pkg/gateway/mcpfabric/mcp/mcp.go, pkg/gateway/mcpfabric/mcptools/mcptools.go, pkg/gateway/gatewaycontrol/platformtools, docker-compose.yml, tests/testinfra/gateway, tests/testinfra/gatewaypki, tests/change-graph.json, migrations/0164_coordination_lease.up.sql, pkg/gateway/coordination/coordlease — holder value, listener, mTLS, credential carriage, and forward helper core (R18).
- CODE-2 — pkg/gateway/sessionserver, pkg/gateway/mcpfabric/mcptools, tests/testinfra/lifecycleharness, tests/tier7a_load_local, tests/tier5_e2e_kind — forward at every forwarded route and MCP tool site, DLQ fallback, and receiver check (R19).
- CODE-3 — pkg/gateway/externalapi/admin, cmd/lenny-gateway, tests/testinfra/lifecyclefence, tests/tier7a_load_local — the drain publishes its transitioned sessions on `CH-BINDRELEASE` (R21).
- CODE-4 — pkg/gateway/podlifecycle, cmd/lenny-gateway, tests/tier3_contract/fullrevoke_release, tests/spec-map.json, scripts/refactor/manifest, pkg/gateway/ARCHITECTURE.md — rename the propagator package to `bindrelease` (R21).
- CHART-1 — charts/lenny, pkg/embedded/manifests/manifests.yaml — port, advertise address, listener, and NetworkPolicy self-peer (R18).
- DOCS-1 — docs/operator-guide/security.md — gateway-to-gateway forwarding identity and its lenny-system policies.
- REC-1 — scripts/seed-claim-register.py, tests/claim-map.json, BUILD-GAPS.md, tests/tier11_docs/build_gaps_data_lifecycle_records_currency_test.go, tests/tier11_docs/build_gaps_suspend_resume_records_currency_test.go, tests/tier11_docs/build_gaps_release_ownership_records_currency_test.go — claim-register rows and finding resolutions.
- TEST-1 — cmd/lenny-gateway, pkg/gateway/sessionserver, tests/tier3_contract/holderforward — R18 holder value, listener, credential carriage, and helper classification tests.
- TEST-2 — pkg/gateway/sessionserver, pkg/gateway/mcpfabric/mcptools, tests/tier4_integration — off-holder route tests on two replicas.
- TEST-3 — tests/tier7a_load_local — concurrency and interleaving tests.
- TEST-4 — pkg/gateway/externalapi/admin, cmd/lenny-gateway, tests/tier4_integration — drain publish and rename tests.
- TEST-5 — tests/tier8_chaos — chaos cases.
- TEST-6 — tests/tier5_e2e_kind, tests/tier9_security, charts/lenny/tests — cluster, security, and chart tests.
- TEST-7 — tests/tier11_docs, pkg/gateway/storage/rediskeys — tier-11 pins of the spec text the staged edits replace.
