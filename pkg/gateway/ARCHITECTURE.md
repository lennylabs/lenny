# `pkg/gateway` group taxonomy

This file is the navigational index for `pkg/gateway`. It records the
intermediate group directories the gateway subpackages are organized under and
maps each group to the specification section that defines its concern. It is the
design artifact authored by proposal 0020 Part B C1; the move from the prior flat
layout to these groups is executed by Part B C3 from the machine-readable
manifest at `scripts/refactor/manifest`.

The gateway is one component under Section 4. It is internally partitioned into
subsystem boundaries that are Go interfaces within a single binary rather than
separate services (§4.1). The grouping preserves that component boundary: every
package stays inside `pkg/gateway`, and each §4.1 subsystem and the §4.8 policy
engine occupies one group subtree, so a future per-pod extraction of a
subsystem (the LLM Proxy is the named first target, per §02 and §4.1) is a
directory-subtree move rather than a scattered cherry-pick. The remaining
packages group by their §10, §11, §12, and §25 concern beneath those boundaries.

## How to use this index

To locate a concern, find its group below and read the spec section the group
maps to. Package names are unchanged by the regroup; only the import path gains
the group segment (`pkg/gateway/<pkg>` becomes `pkg/gateway/<group>/<pkg>`). A
package's own `// spec:` citations remain the authoritative tie to the behavior
it implements; this index is the coarse map from concern to directory.

## Packages that do not move into a group

- `sessionserver` stays whole at the gateway root. It realizes the §4.1 Stream
  Proxy and Upload Handler behind their interfaces, so it is not split across
  groups.
- `middleware` is already an intermediate group directory and stays at the
  gateway root. The §11.6 operator-managed circuit-breaker store (`breakerstore`)
  nests under its existing `middleware/circuitbreaker` package, because §1.3 of
  the proposal anchors it to the gateway-core circuit breaker it depends on
  rather than to the persistence cluster.
- `events` is split by Part B C2 rather than moved by C3. The shared event-type
  vocabulary (the CloudEvents envelope, the `source` discriminator, and the
  §16.6 event-type catalog) moves to the neutral `pkg/events` package, out of the
  gateway, because ~18 external files consume it. The §25.3 in-memory ring buffer
  stays in the gateway at `pkg/gateway/eventbuffer`.

## Subsystem groups (§4.1 subsystems and the §4.8 policy engine)

These are the primary group boundaries. Each maps to a §4.1 gateway subsystem or
to the §4.8 policy engine, and each is kept cohesive in one subtree.

### `mcpfabric/` — §4.1 MCP Fabric subsystem

Delegation orchestration, virtual child MCP interfaces, and elicitation chain
management. §4.1 assigns delegation to this subsystem, so the delegation
orchestration packages group here rather than in a separate top-level group.

`delegation`, `delegationbudget`, `delegationpolicystore`, `elicitationfloor`,
`mcp`, `mcpruntimes`, `mcpschemagen`, `mcptools`, `playground`.

The delegation-tree lifecycle packages nest one level deeper under
`mcpfabric/delegationtree/`, because §4.1 assigns the deep-delegation-tree
concern (its extraction trigger is `lenny_mcp_fabric_active_delegations`) to the
MCP Fabric subsystem. Keeping them inside the `mcpfabric/` subtree keeps the
whole subsystem one extractable subtree rather than splitting it across two
top-level groups.

`delegationtree/deadlock`, `delegationtree/leasecontrol`,
`delegationtree/orphancleanup`, `delegationtree/resultrollup`,
`delegationtree/treearchive`, `delegationtree/treebudget`,
`delegationtree/treerecovery`.

The §8 delegation-tree lifecycle concerns these packages implement: the §8.8
result rollup and per-session usage accumulation, §8.10 orphan cleanup, tree
archive and recovery, tree budget, and the §8.6 lease control.

### `llmproxy/` — §4.1 LLM Proxy subsystem

The credential-injecting reverse proxy for LLM provider traffic (§4.1, §4.9),
plus the proxy-side cache wiring. This is the named first extraction target, so
it is kept cohesive in one subtree.

`credrouter`, `llmproxy`, `proxycache`, `semanticcache`.

### `policy/` — §4.8 Gateway Policy Engine

The policy engine is physically embedded in the gateway and is not yet split out
(§4.8), so it stays in the gateway tree. The `RequestInterceptor` extension point
(§4.8) and the §11.1 request-rate limiter group here. `interceptorstore` nests
under `interceptor` because it anchors onto that gateway-core dependency (§1.3).

`interceptor`, `interceptor/interceptorstore`, `policy`, `ratelimit`.

## Concern groups (§10, §11, §12, and §25 concerns)

These group the remaining packages by the spec subsection that defines their
concern, beneath the subsystem boundaries above.

### `credentials/` — §4.9 Credential Leasing Service

End-user and pool credential storage, lease lifecycle, renewal, fallback, the
deny list, and the §13.3 platform-admin impersonation path.

`credassign`, `credcache`, `credentialpoolstore`, `credentialserver`,
`credentialstore`, `credfallback`, `credleasestore`, `credrenewal`, `denylist`,
`impersonation`, `revocation`, `usercreds`.

### `connectors/` — §9.3 connector access

ConnectorDefinition and connector-credential registries, the connector-access
authorization boundary, secret resolution, invocation, and tool bridging.

`connectorauthz`, `connectorcredstore`, `connectorinvoke`, `connectorsecret`,
`connectorstore`, `connectortools`.

### `session/` — §4.2 Session Manager

Session state, routing cache, session-scoped lifecycle workers, interactive
input and tool-approval registries, message routing, and §9.4 agent memory.

`createdsweeper`, `executor`, `inputwait`, `interactionstore`, `memorystore`,
`messagerouting`, `orphansession`, `recycle`, `routingcache`, `sessionage`,
`sessionbudget`, `sessioncallback`, `sessioncheckpointmeta`, `sessionevents`,
`sessionidle`, `sessioninbox`, `sessionlogstore`, `sessionstore`, `sessionusage`,
`toolapproval`.

### `runtime/` — §5 runtime and data plane

Runtime registry and capability inference, the §5.2 concurrent-stateless data
plane and its routing, pool store, adapter contract and client, and the SDK-warm
and watchdog workers.

`adapter`, `adapterclient`, `adapterregistry`, `agentcard`, `capabilityinference`,
`externaladapterstore`, `poolstore`, `runtimecapoverride`, `runtimestore`,
`sdkwarm`, `slothealth`, `statelessproxy`, `statelessrouting`, `tenantaffinity`,
`watchdog`.

### `podlifecycle/` — §4.6 pod lifecycle

Pod claim and session placement, termination, the PreStop path, and the
drain-readiness endpoint.

`drainreadiness`, `podclaim`, `podsession`, `podterminate`, `prestop`.

### `checkpoint/` — §4.4 Event / Checkpoint Store

Workspace checkpoint drivers, checkpoint retention, and the partial-manifest
store.

`checkpointer`, `checkpointretention`, `partialmanifeststore`.

### `quota/` — §11.2 and §12.4 quota

Quota store, in-memory quota budget, quota checkpointing, quota erasure, the
quota fail-open path, and storage quota.

`quotabudget`, `quotacheckpoint`, `quotaerasure`, `quotafailopen`, `quotastore`,
`storagequota`.

### `billing/` — §11.2.1 billing events

The billing event ledger, checkpoint emission, fan-out, retention, delivery sink,
pending corrections, and the usage accumulator.

`billingcheckpoint`, `billingfanout`, `billingretention`, `billingsink`,
`billingstore`, `correctionstore`, `usagestore`.

### `audit/` — §11.7 and §16.4 audit

The Postgres audit hash chain, the write-time tenant-scope guard, audit-log
retention, and JWT signing-key rotation audit.

`auditretention`, `auditscope`, `auditstore`, `jwtaudit`.

### `storage/` — §12 storage substrate and reliability

The dual-store reliability layer, eviction state, the token-issuance and lease
stores, erasure and legal-hold workers, partition maintenance, retention GC, the
Postgres and Redis substrate helpers, and the embedded SQLite store.

`deadletterredaction`, `derivelock`, `dualstore`, `erasure`, `erasurejob`,
`eventbus`, `evictionfallback`, `evictionstatestore`, `failopen`,
`issuedtokenstore`, `leasestore`, `legalholdreconciler`, `partitionmaint`,
`pgnotify`, `pgtenant`, `pubsub`, `rediskeys`, `redistopology`, `retentiongc`,
`slotcounter`, `sqlitestore`.

### `coordination/` — §10.1 coordination and leader election

Session coordination, the checkpoint barrier and fence, the gateway-scoped leader,
and the PodDisruptionBudget watcher.

`barrier`, `coordfence`, `coordination`, `coordlease`, `gatewayleader`,
`pdbwatcher`.

### `pki/` — §10.3 mTLS PKI

The CA-rotation state machine and its store, and the startup TLS probe.

`carotation`, `carotationstore`, `tlsprobe`.

### `upgrade/` — §10.5 runtime upgrade and rollback

The RuntimeUpgrade state machine, its readiness guard, and its store.

`runtimeupgrade`, `runtimeupgradeguard`, `runtimeupgradestore`.

### `environment/` — §10.6 environment and §10.2 RBAC (gateway-internal)

The Environment registry and access computation, the custom-role and
tenant-access stores, tenant and user stores, the transcript store, the
deployment-config store, and the environment endpoint translator. §10.6 scopes
the environment model to the gateway, so these stay gateway-internal.

`customrolestore`, `deploymentconfigstore`, `envaccess`, `environmentstore`,
`tenantaccessstore`, `tenantstore`, `transcriptstore`, `translator`, `userstore`.

### `experiment/` — §10.7 experiment primitives (gateway-internal)

The experiment registry, the built-in OpenFeature provider, sticky variant
assignment, the eval store, and the OFREP client. §10.7 scopes the experiment
primitives to the gateway.

`evalstore`, `experimentprovider`, `experimentsticky`, `experimentstore`,
`ofrep`.

### `provisioning/` — §14 sandbox provisioning

The deployer-configured environment-variable block, the gitClone ref resolution
backend, and the VCS credential materialization.

`envblock`, `gitref`, `vcscred`.

### `gatewaycontrol/` — §9.1 GatewayControl platform tools

The bridge for the §9.1 GatewayControl platform tool surface.

`platformtools`.

### `externalapi/` — §15 external API surface

The OpenAPI specification endpoint, canonical cursor pagination, the §15.2.1
error classifier, the §15.4.1 output-part fidelity helper, the admin API router,
and the §17.6 initial-admin-token provisioner.

`admin`, `admintoken`, `errorclassify`, `openapi`, `outputpartfidelity`,
`pagination`.

### `metrics/` — §16.1 gateway metrics

The gateway metrics registry, the GC-pause gauge, the §4.1 extraction-threshold
reader, and the §17.8.2 capacity-tier carrier.

`capacityplanning`, `extractionthreshold`, `gatewaymetrics`, `gcpause`.

### `operability/` — §25.3 gateway-side ops endpoints

The in-process operability endpoints that read in-process state: the health
service and its runbook link table, and the capacity recommendations service.
The target paths `operability/health` and `operability/recommendations` are
pinned by §4.0 of the specification.

`health`, `recommendations`.

### `deployment/` — §17.4 deployment

The dev-mode TLS guard rails.

`devmode`.

### `core/` — gateway-core shared primitives

The shared subsystem concurrency primitives the §4.1 isolation guarantees rest
on, and the semver comparison helper.

`semver`, `subsystem`.
