# Decisions: 0141 Runtime routing

## Decisions

Settled by the owner on 2026-11-08:

| ID | Decision |
| :-- | :-- |
| D1 | Eval storage is removed. |
| D2 | Every feature specific to experimentation is removed, and Lenny keeps no experiment object. |
| D3 | Routes are built in, implemented as a `PreRoute` interceptor against the contract external interceptors follow, and can be turned off and replaced. |
| D4 | Interceptor registration stays with `platform-admin`. Tenant-written interceptors are out of scope. |
| D5 | Interceptors run in the cluster. A deployer reaches a remote service through an in-cluster interceptor or relay of their own. |
| D6 | Interceptor calls do not pass through `lenny-egress`. |
| D7 | An interceptor registration may be scoped to a list of tenants. |
| D8 | Routes match on the requested runtime alone. A deployer who wants rules on session labels writes an interceptor (formerly O1). |
| D9 | A delegated child starts with its parent's routing labels, and the child's chain may change any key (formerly O2). |
| D10 | A `tenant-admin` writes routes for their own tenant, and a `platform-admin` writes routes for any tenant (formerly O3). |
| D11 | `evalRef` is retired. A derive runs the `PreRoute` chain with `kind: derive`, the route interceptor treats it like any creation, and derive's own checks run after the chain (formerly O4). |
| D12 | This design adds no route that applies across tenants. Operator rollouts of a new version of one runtime belong to [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) (formerly O5). |
| D13 | The interceptor changes listed in "RT-2" and "RT-6" beyond the `PreRoute` payload are part of this design: session context on every phase, the payload audit, the last modifier, the scope check on `interceptorRef`, and routing reuse on retry and resume. |

Settled by the owner on 2026-11-09, during the review that compared every design with the others:

| ID | Decision |
| :-- | :-- |
| D14 | A delegated child that crosses into another environment through a sharing path keeps its parent's routing labels, as any delegated child does under D9 (review question Q8). |
| D15 | A tenant can see an interceptor that a `platform-admin` registers for it: [`0142-security-oversight`](../0142-security-oversight/change.md) mirrors the registration to each listed tenant, lists it in the tenant's security posture, and states whether operator access approval applies to it (review question Q7). |

Settled by the owner on 2026-11-10:

| ID | Decision |
| :-- | :-- |
| D17 | A route's requested name may name no runtime, which makes the route an alias, and the models list shows a tenant's aliases. Since 2026-11-14 Lenny's discovery routes carry the aliases and the provider adapters build their models lists from them (R8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md)). This settles O9 of [`0120-anthropic-messages-api`](../0120-anthropic-messages-api/change.md) in the owner's words "go with the recommendation". A separate alias table would duplicate routes. The per-runtime admission limits move after the chain, so neither an alias nor a route moves a session past them ("RT-4", **Aliases.**, "Before the chain", and "Discovery"). |

Settled by the owner on 2026-11-13:

| ID | Decision |
| :-- | :-- |
| D18 | A write that names a field this design removes, the tenant's `experimentTargeting` or a request's `evalRef`, is refused with `400 VALIDATION_ERROR` naming the field ("RT-1", **Removed request fields.**). The owner answered "6 ok" to one rule for removed fields everywhere, which D6 of [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/decisions.md#decisions) records. |

Open:

| ID | Question | Recommendation |
| :-- | :-- | :-- |
| D16 | The plaintext interceptor dial in "Current state" is filed as its own `BUILD-GAPS.md` finding, F-10.3.27, because it contradicts NET-063 today, independent of this design. | **Settled**, in the owner's words "render the chart and file it" (2026-11-09). A render with an interceptor namespace confirmed that the chart passes none of the three TLS flags. |

**Gateway seam operations (0117, decision 8).** Settled by the owner on 2026-11-17 in [0117](../0117-gateway-package-layout/decisions.md), which raised the conflict between its seam interface page and this change: a member change that removes, reshapes, or adds a mechanism behind a gateway seam operation edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` in the step that changes the mechanism. In this change that is 0141.S2, whose entries in `steps.md` name the operations.
