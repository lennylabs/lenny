# Decision 52 review: extending O10 to `userAuthorization: required` repositories

Read-only adversarial review, 2026-11-15. Designs read: `0152-user-git-identity` (revision 42 in its status line), `0151-user-credential-delivery` (revision 36), `0124-connector-removal`, `0127-egress-and-git` (revision 88), `0145-session-isolation`, `0118-agent-pod-layout`, `0153-workspace-materialization`, `0147-tenant-derived-runtimes`, and `0150-tenant-security-settings`. Citations name the file and heading.

## Recommendation

**(c) Do not extend.** If the owner extends anyway, take **(b)** with the conditions listed under "Option (b)". Plain **(a)** opens a hole even on the honest path, because three other rules depend on decision 52 by cross-reference.

The reasons are as follows.

1. **The recorded ground of decision 52 is the compromised pod-wide component, and inject-only delivery does not address it.** Decision 52 kept the reused-pod refusals "whose ground is the pod-wide state outside a slot that SI-3's slot release does not reach: `netagent`, the adapter, the network and IPC namespaces, and the sandbox kernel". It rejected option (b) for reused pods because that option "lets a session that compromises `netagent` or the sandbox kernel wait on the pod for a later user's token". The reasons O10 gives are that the agent never holds the token and that `netagent` flushes the token at release. Both reasons hold only against an honest `netagent`. O10 states them as "the grounds of those two refusals", but the decisions record a different ground. O10 moved the actual ground into a residual risk (`0151-user-credential-delivery`, "Residual risks", **`delivery: inject` on recycled and used pods.**).
2. **For git, the same ground carries larger consequences than for a generic `userCredential` binding.** A foothold that waits on a reused pod obtains the following from every later session of the tenant on that pod, until `maxPodUptimeSeconds`:
   - the App installation tokens minted for that later session's repository set, which breaks UR-2 even when no user token exists (`prAuthoring` off);
   - the later user's child tokens under `prAuthoring: user`, which allow approval and, with the git child, merging as that user, so the attacker defeats two-person review (UR-4 and UR-6), the property UG-3 exists to provide;
   - the later session's workspace, its restored seed, and its checkpoint spool, through the adapter or the sandbox kernel, which hold private repository content the attacker's user cannot read at the provider.
3. **Reuse turns one compromise into a pool-wide infection that grows over time.** Without reuse, a compromised pod dies with its session. With reuse, and with `placement.allowReusedPod` defaulting to `true` (`0150-tenant-security-settings`, "TSS-6"), every pod an injected session compromises returns to the tenant's pinned inventory and harvests later sessions. Decision 52's acceptance for attributing multi-slot pods covers only sessions that overlap in time on that pod, which is a narrower exposure.
4. **No honest-path hole was found in the slot release itself.** The release sweeps, the per-slot `netcred` volume, the provisional binding, and the per-session cache keys all hold. The honest-path hole sits in the warm-store rules, which assume the refusal (see "Hole 2").

## 1. Every refusal and what it cites

| Location | Rule | Stated reason |
| :-- | :-- | :-- |
| `0152-user-git-identity`, "Security properties", UP-5 | "A user-derived token never reaches a pod that can serve a later session or that has served one" (decision 52). | Held by the binding validator, the session gateway check, and the Token Service. |
| Same, "High-level design", UG-2 paragraph | "Pod models that could carry one user's state into another session are refused." | The design's own summary. |
| Same, "UG-2", "Pod models" (opening paragraph) | Refuses these repositories with `422 USER_AUTHORIZATION_UNSUPPORTED_POD`. After O10, "the rows for repositories of a VCS pool with `userAuthorization: required` are unchanged, because decision 52 is this design's and the owner's answer to O10 named `userCredential` bindings alone." `retireOnRelease` "applies to these repositories too". | |
| Same, "Pod models" table, `recycle.enabled: true` row | Kept by decision 52. | The release ends processes, sweeps the slot volumes, and checks that they are empty, so "residue from Alice's repositories in the slot's volumes cannot reach Bob". What persists is "`netagent`, which holds the next user's token in memory and answers on its unauthenticated loopback port, the adapter, the pod's network and IPC namespaces, and the sandbox kernel". "A session whose agent compromises one of these keeps the foothold for every later session on the pod, and the next user's token is then within its reach. A fresh pod for each affected session bounds such a compromise to the session that made it." |
| Same, served-pod row | Kept. A multi-slot pod with a released slot counts as served. | "The ground of `recycle.enabled`." |
| Same, "What lifts and what remains (2026-11-13)" | "The `recycle.enabled` and served-pod refusals remain on the restated ground." | |
| Same, "Decisions", 14 and 52 | Decision 52 was settled in the owner's word "ok" to item 4. It takes "(a) for reused pods". | The rejected alternative "(b) for reused pods as well … lets a session that compromises `netagent` or the sandbox kernel wait on the pod for a later user's token." |
| Same, "Threats", compromised `netagent` row | The attacker "cannot … mint after the pod is quarantined or the binding ends". | The row assumes the pod does not serve a later session. |
| Same, "Mirror, warm fills, and the 6-hour promise", **Warm fills.** | "A tenant entry under a VCS pool with `userAuthorization: required` is refused at write on a pool that reuses pods." | No per-user check runs where a store cannot be withheld. |
| `0151-user-credential-delivery`, "UC-3", "Pods that can serve someone else are refused." | At bind, for a session "declaring a repository under a VCS pool with `userAuthorization: required`, the gateway writes `retireOnRelease`". | The mark keeps a later pool edit from recycling the pod. |
| `0147-tenant-derived-runtimes`, default clone write rules | "On a VCS pool with `userAuthorization: required`, the owned pool is one [`0152-user-git-identity`] accepts, so a recycling or concurrent owned pool is refused at write." | A direct cross-reference to decision 52. |
| Same, **One session per pod.** | A clone or a `gitRepository` asset under such a pool needs `ReusesPods` false and a one-way `oneSessionPerPod: true`. | It cites "the ground on which [UGI] refuses reused pods under its decision 52". The field is one-way because "`rebindReserved`, `ClaimSlot`'s two passes, and the idle scan all read the pool's current policy". |
| Same, **Reused pods.** under "Warm stores" | "`ClaimSlot`'s first pass and `rebindReserved` place a session on a running or held pod with no claim check, so `coveredStores` does not reach them … Such a pool cannot serve a clone under `userAuthorization: required`, so … no per-user check is lost." | The rule rests explicitly on the refusal. The review row "High, a recycled pod skips `coveredStores`" records that `claimer.go` runs `rebindReserved` before any admission check. |
| `0145-session-isolation`, "SI-3", **User git identity (UGI decision 14).**; "SI-10"; D29 | These restate D29: user git identity is "refused on a fallback pool and on a recycled or used pod". | SI-10's pod-wide state. |

`0118-agent-pod-layout` holds no refusal of its own. It supplies the layout behind the grounds: `/lenny/git-stores` is read-only in slots and writable only by `lenny-store-fill` and the adapter, `lenny-spool` is adapter-only, and `HOME` sits under `/tmp`.

## 2. Each candidate reason, with or without the refusal

| Candidate | Honest path under the extension | Covering mechanism, or hole |
| :-- | :-- | :-- |
| Git credentials persisting outside `netagent` | Covered. Under git path T no credential helper exists, and on a covered host a client-supplied credential is dropped and the mint is used (`0127-egress-and-git`, "Git and hosts"). Under `inject` the agent never sees a token. `HOME`, `.git/config`, and `.gitconfig` sit under `/tmp` or `/workspace`, and SI-3's two Sweeps and the emptiness check remove them. The session's git configuration and its commit identity files sit on `slot-<n>-netcred`, which `netagent` "empties … at the slot's release" ("Git configuration"). | No hole on the honest path. The exception is a `delivery: file` binding under `allowRuntimeHeldCredentials`, where the agent holds the user's token. UC-3's file rule still refuses that binding on reused pods. |
| The git mirror and the store fill | Covered. `lenny-git-mirror` is central and authorizes every request through the egress decision API, which names the repository and reads the entry state, whatever the pod model. `lenny-store-fill` runs only at pod start with the App and no user, and the stores are read-only to slots. | No pod-model dependence. |
| Reflink and prestaged checkouts | Covered. `prestage` rebuilds from the pod's own stores at the end of each release (SI-3 step 9, "WM-15"). It reads nothing a slot could not already read. | See Hole 2, which concerns the stores themselves. |
| The user's objects, commits, or fetched private refs in slot volumes | Covered. Fetched objects go into the slot repository under `/workspace`, which SI-3 sweeps and checks for emptiness. Pushes go upstream through `netagent`. | No honest-path hole. |
| Content in adapter-only state (`lenny-spool` checkpoint and export spool, restore staging) | Unreadable by a later slot unless the adapter is compromised. | **Hole 1** under compromise, because the adapter persists across sessions on a reused pod. |
| Private warm stores (`gitRepository` assets) under `userAuthorization: required` | These are refused today on reused pools at write, through the cross-reference to decision 52. | **Hole 2** if the extension carries that cross-reference. |
| Push enforcement and commit identity per session | Covered. The git child is minted per session and attached only to the write-scope path. The commit identity file sits on `slot-<n>-netcred` and is emptied at release. `credential.mint` carries the `sessionId`. | No honest-path hole. Under compromise, see Hole 1. |
| Provisional binding on resume | Covered. The provisional binding withholds write tokens and every `userCredential` binding. "Every exit from `resuming` … retires it in the same write that releases the pod, so a released pod waiting idle for its tenant … carries no session grant" (`0127-egress-and-git`, "Git remotes and client tools"). The UG-2 check runs at resume. | No hole. |
| Write tokens compared with read tokens | On the honest path, both are flushed per session (cache keyed on binding generation and session). | Under compromise a later reviewer's `contents: write` and `pull_requests: write` children are the most valuable loot (Hole 1). |
| Per-slot attribution | Irrelevant on a one-slot recycled pod, where sessions run in turn and the binding change flushes. On a multi-slot pod the served-pod row is the reused case. | — |
| Threat tables | The compromised-`netagent` row says the attacker "cannot … mint after … the binding ends". On a reused pod a new binding for another user starts on the same compromised `netagent`. | The row becomes false under the extension (Hole 1). |

### Hole 1: a persistent foothold harvests later users' repository access (compromised path)

This is the ground decision 52 recorded, so the extension undoes it directly.

**Scenario.** Tenant `acme` runs a recycling pool on gVisor or Kata, and the VCS pool `github.com/acme/*` has `userAuthorization: required` and `prAuthoring: user`. Alice's agent is prompt-injected and exploits `netagent` (a hostile-input parser in the TLS-terminating proxy), the adapter, or the sandbox kernel. The foothold survives the release because SI-10 names exactly these components as pod-wide state. The pod returns to `acme`'s pinned inventory or reserved hold. Bob, Alice's reviewer, starts a session declaring `acme/payments` as writable, and the session lands on the pod.

The foothold then:

1. takes Bob's child tokens from `netagent`, approves Alice's pull request as Bob, and merges it with Bob's git child, which defeats UR-4 and UR-6;
2. takes App installation tokens minted for Bob's repository set, including `acme/payments-secrets`, which Alice cannot read at the provider, so UR-2 breaks with no user token involved;
3. reads Bob's restored seed and workspace through the adapter or kernel.

Alice's agent repeats the exploit in each new session, so the compromised share of the pool grows. Without reuse, each compromised pod dies with its session.

The O10 analogue for a generic `userCredential` binding exposes Bob's token to a service. The git case also defeats the two controls this design exists to provide, the per-user repository boundary and two-person review, and it applies to App tokens as well.

**What would close it.** The current refusal closes it. A partial alternative is a short required `maxPodUptimeSeconds` on such pools together with the `microvm` class. Neither removes the hole, because the design has no mechanism that attests `netagent` or the adapter is uncompromised between sessions.

### Hole 2: warm stores on reused pools if the cross-reference is carried along (honest path)

`0147-tenant-derived-runtimes` admits a recycling owned pool for a clone or `gitRepository` asset under `userAuthorization: required` only when `0152-user-git-identity` accepts it. Its **Reused pods.** paragraph states that `rebindReserved` and `ClaimSlot`'s first pass run with no claim check, so the pod-versus-covered-set refusal does not apply on those paths. A repository removed from the runtime or the allowlist "stays readable to new sessions on such a pod until it retires". The paragraph then says no per-user check is lost only because "such a pool cannot serve a clone under `userAuthorization: required`".

**Scenario.** A recycling owned pool holds a private store for `acme/infra-secrets`. Alice, who is authorized, uses pod P. The tenant admin then removes the asset, or the entry leaves the rendered list through a deny or a suspension. Bob, who is not authorized, is placed on P by `rebindReserved`. The entry is off the current list, so no read check covers it, and the pod is not refused. Bob's slot reads the store's full history at `/lenny/git-stores/<hash>`, which every slot mounts read-only.

**What would close it.** Keep the write refusal for clones and `gitRepository` assets under `userAuthorization: required` on reused owned pools, independent of the operator setting. Alternatively, add the pod-versus-lists refusal to `rebindReserved` and `ClaimSlot`, which the review row "High, a recycled pod skips `coveredStores`" records as absent in `claimer.go`.

### Bookkeeping that the extension must change (not a hole)

`retireOnRelease` is written for every session that declares such a repository, and the pod credential API refuses a release without the mark. Under the extension the gateway must not write the mark, or the pod never recycles. UP-5 also becomes false.

## 3. Comparison with O10's reasoning

| O10 premise | `userCredential` under `inject` | `userAuthorization: required` repositories |
| :-- | :-- | :-- |
| The agent never holds the token | True | True for App tokens and children under git path T. False under `allowRuntimeHeldCredentials`, which UC-3's file rule already keeps refused. |
| `netagent` flushes the token at release | True | True, keyed on binding generation and session. |
| What a waiting foothold gains | The later user's token for one service | App tokens for later users' repository sets (no link needed), the later reviewer's approve and merge children, and later private workspace and seed content |
| Promise broken | A token's confidentiality | UR-2, UR-4, and UR-6, the properties the tenant enabled `userAuthorization: required` to get |
| Other rules resting on the refusal | None found | The warm-store write refusal, `oneSessionPerPod`'s one-way rule, and the **Reused pods.** argument (Hole 2) |
| Content beyond the token | Service data passes through the slot and is swept | Repository content can also sit in pod-wide warm stores that outlive the session. This is safe only while the store rules above hold. |

**About O10 itself.** O10 states that its two reasons are "the grounds of those two refusals". The grounds that decisions 41 and 52 record are the pod-wide state outside a slot ("the per-slot release of SI-3 does not reach `netagent`'s caches, pod-wide state, or kernel state"), and decision 52 rejected reuse explicitly on the waiting-foothold argument. The UCD residual risk records that the owner accepted the compromised case. The owner's "ok" answered a question framed on the honest-path reasons. Confirm with the owner that the waiting-foothold case was accepted knowingly before reusing O10 as precedent here.

## 4. Options and the text each would change

### Option (c): do not extend (recommended)

- `0152-user-git-identity`, "UG-2", "Pod models" opening paragraph: replace "because decision 52 is this design's and the owner's answer to O10 named `userCredential` bindings alone" with a sentence stating that the O10 operator setting does not reach these repositories, and why (Hole 1 and Hole 2).
- `0152-user-git-identity`, "Decisions", 52: add a dated note recording that the extension was considered and declined on Hole 1 and Hole 2.
- `0151-user-credential-delivery`, "UC-3", **Pods that can serve someone else are refused.**, and decision 41's amendment: when the owner's operator-setting edit lands, state that the setting covers `userCredential` bindings alone. Restate the ground as the compromised-component residual rather than "the grounds of those two refusals".
- `0124-connector-removal`, "Owner decisions", O10: the same correction of the stated grounds, plus the operator setting.

### Option (b): extend with conditions

Conditions:

1. **Two keys.** The operator setting stays off by default. A tenant `security-admin` guardrail, such as `userAuthorization.allowReusedPods` (absent, which means refuse), must also allow reuse for the tenant. UR-2 is a promise the tenant chose, and `placement.allowReusedPod` defaults to `true`.
2. **Repositories with no user token only.** Keep refusing a session whose VCS pool sets `prAuthoring: user` and that declares a writable repository (it holds child tokens), or that names any `delivery: file` binding on a covered host. This leaves App-read sessions in scope, where Hole 1's item 2 still applies.
3. **Warm stores decoupled.** Keep the write refusal of clones and `gitRepository` assets under `userAuthorization: required` on reused owned pools, and keep `oneSessionPerPod` one-way, whatever the setting says. Alternatively, add the pod-versus-lists refusal to `rebindReserved` and both `ClaimSlot` passes first.
4. **A bounded foothold.** Require `maxPodUptimeSeconds`, for example at most 3600, on a pool admitted under the setting, and consider requiring the `microvm` node class.
5. **Visible to the tenant.** Report the setting in posture and in `sessionIsolationLevel`. Add a residual bullet and a threat row.

Text that would change:

- `0152-user-git-identity`:
  - "Security properties", UP-5 (add the exception);
  - "Threats", the compromised-`netagent` row (later sessions on a reused pod);
  - "High-level design", UG-2 paragraph ("Pod models that could carry one user's state into another session are refused");
  - "Pod models": the opening paragraph, the `recycle.enabled` and served-pod rows, and "What lifts and what remains";
  - "Identity cases", **Environment-less pods.** ("on a pod that has served no session");
  - "Residual risks", a new bullet beside **Concurrent sessions on an attributing pod.**;
  - "Configuration by scope", the new setting and guardrail rows;
  - "Documentation", **Settings and defaults.** and **Pod and cluster requirements.**;
  - "Tests", the Tier 4 "Each row of 'Pod models'" item;
  - "Decisions", amendments to 14 and 52;
  - the `retireOnRelease` row in "Terms".
- `0151-user-credential-delivery`, "UC-3": the `retireOnRelease` sentence ("or declaring a repository under a VCS pool with `userAuthorization: required`") and the mint refusal without the mark.
- `0147-tenant-derived-runtimes`: the default clone bullet "the owned pool is one [UGI] accepts", **One session per pod.**, and **Reused pods.**, worded so that they stay refusing under condition 3.
- `0145-session-isolation`: "SI-3" **User git identity (UGI decision 14).**, the D29 sentence in "SI-10", and D29.
- `0142-security-oversight`, the guardrail table (the new guardrail).
- `0124-connector-removal`, O10.

### Option (a): extend under the same setting

All of the text under option (b) changes, without conditions 1 to 5. Through the cross-reference, the `0147-tenant-derived-runtimes` write refusal and **Reused pods.** argument lift with it, which opens Hole 2 on the honest path. Hole 1 becomes a design-accepted residual that covers App tokens and two-person review. Not recommended.
