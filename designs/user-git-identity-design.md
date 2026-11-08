# User-scoped repository access and pull request authoring

Status: design for owner review, revision 29, 2026-11-08. Nothing in this document is built or staged. This is the VCS (version control system) layer of the user credential mechanism. It reads against [`egress-and-git-design.md`](egress-and-git-design.md) revision 59, [`user-credential-delivery-design.md`](user-credential-delivery-design.md) revision 19, [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) revision 21, [`github-credential-addon-design.md`](github-credential-addon-design.md) revision 1, and [`gitlab-credential-addon-design.md`](gitlab-credential-addon-design.md) revision 1. The owner has settled decisions 1, 3, 8 to 11 (with 9 superseded by 8), 35, 36, and 40 under "Decisions", and adopted the rule in decision 49. Decision 47 is open. The live GitHub spikes and the stub capture this layer relies on are recorded in [`github-credential-addon-design.md`](github-credential-addon-design.md), and **the stub proves forwarding only**. Rules changed after the last run are **[design]**. Statements about the tree were checked by reading the code and the specification on 2026-11-06. Statements about Coder come from the page under "Sources". Proposals carry the label UG-n. "Review history" at the end records what each revision changed and the reviews behind it.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[measured]** | A hands-on result from a validation round of [`egress-and-git-design.md`](egress-and-git-design.md), or from the spike of 2026-11-03 on the git path candidates L, T, and P ("Git remotes and client tools") and the deny preset. That spike's origin was stock `git http-backend` behind a recording stub. **The stub proves forwarding only:** a row that reached it says nothing about whether a host would accept the spelling, grant the permission, or perform the action. |
| **[captured]** | Seen at the stub or at the prototype `netagent` in the capture of 2026-11-03. It shows what the client sent and what the filter did. It shows nothing about what a provider would do. |
| **[stub cannot show]** | A question only the provider can answer. Each one has a spike under "Validation owed" in the add-on design for that provider. |
| **[live]** | Read from a provider's host, anonymously, with no write. |
| **[documented]** | Stated on a vendor page under "Sources", here or in an add-on design. The vendor's page was read and its behaviour was not tested. |
| **[source]** | Read in the Lenny tree, the specification, or client tool source at cli/cli tag v2.103.3. |
| **[pending]** | Waits on a measurement or decision named beside it. |
| **[design]** | A statement of what this design requires. Nothing implements it. |

**Evidence sources.** A *stub* is a test HTTP server running stock git that stands in for a provider and records what reaches it. It shows what `netagent` forwards and nothing about what a provider would do with it. A *spike* is a hands-on test, against a stub or a live provider, recorded under "Validation owed" in the design that ran it with an S-number or a V-number. A *validation round* is one of the numbered hands-on test runs that [`egress-and-git-design.md`, "Validation record"](egress-and-git-design.md#validation-record) lists, cited as "round 3" and so on. *The capture* is the recording of traffic from `gh` 2.103.3 and `glab` 1.127.0, the GitHub and GitLab command-line clients, through a prototype `netagent` to a stub, which [`github-credential-addon-design.md`, "Capture history"](github-credential-addon-design.md#capture-history) records. *The filter* and *the allowlist* are the provider filter of this document's revision 3, `vcs-authoring`, which forwarded only listed `gh` operations, headers, and input keys. Request rules replaced it.

## Problem

Feedback on Lenny, which this document calls the feedback proposal, asked for user-scoped repository access and pull request authoring, similar to what Coder offers. These gaps stand behind it.

- **Authorization.** At session bind, [the egress design](egress-and-git-design.md) checks a repository against the tenant's allowlist and the VCS pool's `allowWrite` ([`egress-and-git-design.md`, "Credentials"](egress-and-git-design.md#credentials), **Write scope**). It does not ask whether the requesting user can access the repository. Any user in a tenant reaches any allowlisted repository through the App.
- **Authorship.** A pull request created with an App token has the App as its author. GitHub then sees no relationship between the requesting user and the pull request, so that user can approve the agent's work, which defeats two-person review.

These are separate features with different risk. Authorization keeps the user's token inside the Token Service. Pull request authoring lets an agent act with a token derived from a user's authority. This document therefore separates them, and either one's proposal can be rejected without affecting the other's dependencies, except that UG-3 needs UG-2's link and check.

Pull request authoring is not a special case in the pod any more. It is a `userCredential` binding of [`user-credential-delivery-design.md`](user-credential-delivery-design.md), the generic mechanism that gives a session its own user's token, with an `oauth2Link` source, a narrowing template, and a preset, all held as data. "Terms" below defines each of these.

## Scope

This design is the VCS layer of the user credential mechanism in [`user-credential-delivery-design.md`](user-credential-delivery-design.md). It covers the rules every VCS link kind follows (UG-1), repository authorization on VCS hosts (UG-2), pull request authoring as a configuration of the generic binding (UG-3), and an approval check for tenants that refuse user tokens (UG-4). [`github-credential-addon-design.md`](github-credential-addon-design.md) implements it for GitHub and GitHub Enterprise Server (GHES), and [`gitlab-credential-addon-design.md`](gitlab-credential-addon-design.md) implements it for GitLab, where user-authored merge requests, formerly UG-5, are [GL-2](gitlab-credential-addon-design.md#gl-2-user-authored-merge-requests).

Out of scope: tenant-wide session content reads, in which someone other than a session's user reads its content across a tenant, the repository visibility rule, which decides which repositories a user can see, and the defaults of the egress design's exfiltration controls, [EG-12](egress-and-git-design.md#proposal-split). [`egress-and-git-design.md`](egress-and-git-design.md) owns egress and git, and this document lists what it asked of that design under "Changes requested of egress-and-git-design.md" near the end. [`user-credential-delivery-design.md`](user-credential-delivery-design.md) owns the `userCredential` binding, the `registered` and `oauth2Link` sources, the link flow and stored links, `file` delivery, request rules, the preset format, and the add-on model. [`security-oversight-design.md`](security-oversight-design.md) owns repository rules and guardrails, [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) owns user lifecycle and environments, [`tenant-outbound-connector-design.md`](tenant-outbound-connector-design.md) owns private hosts, and [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md) owns preservation.

## Terms

The generic design, [`user-credential-delivery-design.md`](user-credential-delivery-design.md), defines most of the credential terms this layer uses, in its [Terms](user-credential-delivery-design.md#terms). The rows marked "generic" give the meaning this layer relies on and point there for the full definition. The other rows are this layer's own.

| Term | Meaning |
| :-- | :-- |
| User credential type | A tenant object naming a kind of per-user token, its source, and the hosts where it works. Its source is `registered`, a token a tenant backend or the user writes, or `oauth2Link`, a token from a link. Its type hosts are the hosts that every rule about where the type may be bound evaluates, such as the one-binding rule and the refusals under "Other tokens on these hosts": for `registered`, the hosts the type declares, and for `oauth2Link`, the link provider's API hosts. Generic. |
| `userCredential` binding | A credential binding whose `secret` names a user credential type, so each session receives its own user's token. Generic, [UC-3](user-credential-delivery-design.md#uc-3-the-usercredential-binding). |
| Link provider | A tenant object naming a provider kind and the tenant's OAuth2 client for it. A tenant can have several on one host. Each VCS pool with `userAuthorization: required` names the one its check uses in `userAuthorization.linkProvider`, and each `oauth2Link` type names the one it uses. Generic, [UC-2](user-credential-delivery-design.md#uc-2-the-oauth2link-source-and-account-linking). |
| Kind, add-on, and extension point | A link provider's `kind` selects the add-on that defines its configuration block (its kind block) and its behaviour. An add-on is a compiled-in package that registers kinds and implements extension points, which are named interfaces the generic design calls at defined moments, such as the release check, the request layout, the host policy, and the attach. Generic, [UC-7](user-credential-delivery-design.md#uc-7-add-ons). |
| VCS kind | A link provider kind that a VCS add-on registers: `github` and `github-enterprise` in [`github-credential-addon-design.md`](github-credential-addon-design.md), and `gitlab` in [`gitlab-credential-addon-design.md`](gitlab-credential-addon-design.md). A link provider for any other OAuth2 service has `kind: oauth2`, which [`user-credential-delivery-design.md`](user-credential-delivery-design.md) defines. |
| VCS add-on | An add-on, under [UC-7](user-credential-delivery-design.md#uc-7-add-ons) of the generic design, that registers a VCS kind and implements this layer's extension points for its provider. |
| Link | One Lenny user's stored authorization under one link provider, keyed on tenant, user, and link provider name. [UC-2](user-credential-delivery-design.md#uc-2-the-oauth2link-source-and-account-linking) of the generic design defines links and the browser flow that makes them, the link flow. |
| `user_links` and `link_ref` | The table that stores links, and a random identifier for one link, regenerated on each link, that evidence and caches carry in place of the user. `credential.mint` records carry it as `linkRef` ("Attribution"). Generic. |
| `identityBinding` | A link provider field that decides how the linked provider account is tied to the Lenny user. Under `identityBinding: unverified`, the tenant records that its identity source does not tie them. Generic, ["Identity binding"](user-credential-delivery-design.md#identity-binding). |
| Parent token | The user access token and refresh token a link holds. On a link provider of a VCS kind, both stay in the Token Service. [`user-credential-delivery-design.md`](user-credential-delivery-design.md) states the exception for a link provider with `kind: oauth2`. |
| Narrowing template | Data on a link provider describing how to derive a per-session token from the parent token. A platform-shipped template, such as `github-scoped`, ships with an add-on, and its `permissions` field lists the provider permissions the derived token gets. Generic, ["Narrowing templates"](user-credential-delivery-design.md#narrowing-templates). |
| Link provider's App | On GitHub and GHES, a link provider's OAuth2 client is a GitHub App, and the user tokens and child tokens of its links are GitHub App user tokens. That App can differ from the App a VCS pool mints installation tokens from, and "the App" alone in this document means the pool's App. |
| Child token | On GitHub and GHES, a token derived from the parent by `POST /applications/{client_id}/token/scoped`, limited to named repositories and permissions. [`github-credential-addon-design.md`](github-credential-addon-design.md) defines it. It is unrelated to a delegated child session. The *git child* is the child token that carries git pushes on a VCS pool with `pushIdentity: user`. |
| Delivery | A `userCredential` binding's `delivery` field. Under `delivery: inject`, `netagent` attaches the token to requests and the runtime holds only a placeholder. Under `delivery: file`, the runtime receives the token in a file. Generic. |
| Delivery ceiling | The highest delivery a stored token may take, set by the `delivery` field on a link provider or on a `registered` type and held in the token trust record. Its values, in increasing order, are `identifier`, which keeps the linked account's identity and discards the token; `none`, which keeps the token in the Token Service for a check and delivers it nowhere; `inject`; and `file`. Generic. |
| Token trust record | Everything that decides where a stored token can go and who can read it, with a digest on each stored token. A change to the record makes the stored token unusable. Generic, ["Token trust record"](user-credential-delivery-design.md#token-trust-record). |
| `user_act_required` | The one state of a stored token that needs a new user act: a relink here. It is derived from a trust record mismatch, and stored when a provider refuses a refresh. |
| Request rules | Data on a binding with `delivery: inject`, optionally from a preset, that refuses requests before the token is attached. Under `requestRules.default: allow`, the default, a request passes unless a rule denies it, so the rules are a deny list. Rule kinds include REST path rules, `graphql.denyMutations` (GraphQL mutation names), and `bodyPatterns` (patterns over free text in a body). Generic, [UC-4](user-credential-delivery-design.md#uc-4-request-rules-and-presets). |
| Preset | A named, versioned set of request rules and request forms that an add-on ships as data, with paths per API flavour, such as `github-deny` and `gitlab-deny`. A preset's request forms say which query strings, body types, encoded path segments, and body keys a write may use. A binding's `preset: none` clears it. Generic, ["Presets"](user-credential-delivery-design.md#presets). |
| Request layout | What an add-on registers for a host's API: the API flavour, the API host, `apiBase`, `restPrefix` (the path prefix of the REST API), the GraphQL paths, the uploads path, and `attach`. Generic, [UC-7](user-credential-delivery-design.md#uc-7-add-ons). |
| API flavour | The API dialect of a host, `github`, `github-enterprise`, or `gitlab`, which selects a preset's paths. This layer derives it from a VCS pool's provider and host, under "Request rules on VCS hosts". |
| Release and pod release | A release is the Token Service handing a binding's token to `netagent`, or to the mirror, on the pod credential API. A *pod release* is different: the end of a session's use of its pod, after which the pod returns to its pool or is retired. "A release" in this document means the first. |
| Release check and verdict | An add-on's check that the Token Service runs before each release and each mint for a session the add-on governs. This layer's release check is the repository check of UG-2. Its result, the verdict, is `allowed`, `refused`, or `unavailable`. Generic, [UC-7](user-credential-delivery-design.md#uc-7-add-ons). |
| `retireOnRelease` | A mark the gateway writes on a session's pod claim at bind when the session names a `userCredential` binding, or here a repository under a VCS pool with `userAuthorization: required`. Every pod release path then retires the pod rather than returning it to its pool. Generic, [UC-3](user-credential-delivery-design.md#uc-3-the-usercredential-binding). |
| Session input check | The generic design's check at the session input layer that refuses an impersonation bearer's input, such as a message or a resume, to a session in a delegation tree that holds a `userCredential` binding or declares a repository under a VCS pool with `userAuthorization: required` ([UCP-3](user-credential-delivery-design.md#security-properties)). |
| Binding validator | The check at a binding or pool write that refuses a `userCredential` binding whose definition can reach only pods that can serve another session ([UCP-2](user-credential-delivery-design.md#security-properties)). |
| `agentHeldCredential` | The generic design's mark on a session that holds a binding with `delivery: file`, which posture reports ([UC-5](user-credential-delivery-design.md#uc-5-delivery-file)). |
| `leakScan` | The generic design's optional leak scanning ([UC-6](user-credential-delivery-design.md#leak-scanning-uc-6)), under which a session's coordinating gateway replica holds a `delivery: file` token to redact it from traffic the gateway carries. |
| `userCredentialDelivery` grant | A per-tenant grant a `platform-admin` gives before a tenant can opt in to `delivery: file` for user credentials. Generic, [UC-5, "Gate"](user-credential-delivery-design.md#gate). |
| `tokenSource` | A field of `credential.mint` records naming the kind of token released: `app`, `userRegistered`, `userOauth2`, `userChild`, or `userCheck`. Generic. |
| `userAuthorization` | A VCS pool setting, `off` by default or `required`. Under `required`, a session reaches the pool's repositories only after a check of the requesting user's own access to each one (UG-2). |
| Repository check | The UG-2 check: the Token Service asks the provider, with the user's parent token, whether the user holds the declared access level on a repository. "The check" in this document means this check. |
| Sweep | A gateway background job that repeats the repository check for running sessions every `recheckIntervalSeconds`, 300 by default (UG-2, "Revocation"). |
| `userRefused` and `verifiedAt` | A state of a repository entry, written when the check returns `refused` during a session, that denies reads and writes of the repository; and the time of the entry's last `allowed` verdict (UG-2, "Revocation"). |
| Service identity | A non-human caller listed on a VCS pool's `serviceIdentities`, whose sessions use the App binding and skip the user check (UG-2, "Service identities"). |
| `prAuthoring` | A VCS pool setting, `off` by default, `user`, or `bot`. Under `user`, the pool attaches a `userCredential` binding to a human session so that its pull requests are authored as the requesting user (UG-3). `user` requires `userAuthorization: required` on the same pool. |
| `pushIdentity` | A VCS pool setting that applies when the pool sets `prAuthoring: user`. Under `user`, the default on GitHub, git pushes use a token derived from the requesting user's link. Under `app`, git pushes use App tokens. |
| `allowRuntimeHeldCredentials` | A VCS pool setting, `false` by default, that admits a binding with `delivery: file` on the hosts the pool covers, under "Other tokens on these hosts" and "Git and hosts". |
| Covered host | A git host for which `netagent` terminates TLS and runs git through itself: a host of a VCS pool's repository prefixes or of a git route of the tenant, as [`egress-and-git-design.md`, "Git on covered hosts"](egress-and-git-design.md#git-on-covered-hosts-netagent) defines it. A VCS kind's API hosts are named separately. |
| The one-binding rule | [The egress design](egress-and-git-design.md)'s rule that a session holds at most one binding with `delivery: inject` per host, extended here to refuse a binding with `delivery: file` beside one with `delivery: inject` on one host, evaluated on type hosts. |
| Host policy and the attach rule | This layer's implementation of two extension points. The host policy decides which other tokens may sit on a covered host ("Other tokens on these hosts"), and the attach rule decides which sessions receive a VCS pool's binding ("The attach rule"). |
| Git path T | The git configuration the owner settled under decision 36. Git's `http.proxy` points at `netagent`, which terminates TLS for VCS hosts, `http.sslCAInfo` names a bundle of the per-pod CA and the public roots, and remotes keep their upstream URLs. L and P are the alternatives measured beside it. "Git remotes and client tools" states all three. |
| GitLab options A to F | The ways of authoring merge requests on GitLab that [`gitlab-credential-addon-design.md`, "Options"](gitlab-credential-addon-design.md#options) compares. E is the recommendation, and D is the project token path the egress design has today. |
| Request filter | Revision 3's provider-specific allowlist, `vcs-authoring`. **Superseded by request rules.** The name survives in the history below. |

The word "connector" stays bound to §9.3, where a connector is an external MCP (Model Context Protocol) server registered with the gateway, which the gateway calls with the user's OAuth token, and to [the outbound connector design](tenant-outbound-connector-design.md)'s `outbound` identifiers. No new link (in §28's sense, a transport connection between two components), channel, or register enters §28: the new Token Service calls sit on `schemas/lenny-tokenservice.proto`, which [§28.7 Wire-contract artifact register](../spec/28_communication-channels.md#287-wire-contract-artifact-register) records as a surface the channel register holds no entry for, and the pod credential API fields are additions to an interface the [egress design](egress-and-git-design.md) defines.

### Terms from other designs and the platform

This design builds on mechanisms other designs and the specification define. The table gives each one the meaning this document relies on.

| Term | Meaning |
| :-- | :-- |
| Gateway replica | One instance of Lenny's API gateway. One replica coordinates each session under a lease. |
| Token Service | The Lenny service that stores user and provider credentials, mints short-lived tokens from them, and answers the pod credential API ([§4.3](../spec/04_system-components.md#43-token-service)). |
| `netagent` | A container in every agent pod that acts as the pod's egress proxy. It holds the pod's service account token, terminates TLS for hosts that carry a credential, attaches credentials, and applies request rules. |
| `lenny-egress` | The central egress proxy, built on Envoy, through which agent traffic leaves the cluster. |
| Decision service | `lenny-egress-authz`, which decides whether each connection is allowed, from the session's effective policy, and returns the route to use. |
| The two gates | The Token Service and the decision service, which check a session's access to a repository independently of each other. "Both gates" in this document means these two. |
| Git mirror | `lenny-git-mirror`, which serves git fetches from per-tenant stores. Its "Upstream outage" rule in the egress design serves reads from its store while a host is down, for up to 6 hours after the host last confirmed the refs. |
| Pod credential API | The gRPC API on which `netagent`, and the git mirror, ask the Token Service for a git token or a binding's token. A response carries the token with the binding's hosts, attach template, `cacheUntil`, `handling`, and resolved request rules. [`egress-and-git-design.md`, "Interfaces"](egress-and-git-design.md#interfaces) defines it as I8. |
| Pod binding API | The gRPC API between `netagent` and the decision service that carries the pod's binding subscription, so a *binding change* pushed on it reaches `netagent` at once. [`egress-and-git-design.md`, "Interfaces"](egress-and-git-design.md#interfaces) defines it as I7. |
| Egress decision API | The API on which a proxy, the git endpoint, the mirror, or the Token Service asks the decision service whether a connection or a repository access is allowed. [`egress-and-git-design.md`, "Interfaces"](egress-and-git-design.md#interfaces) defines it as I3. |
| `cacheUntil` and `handling` | Fields of a pod credential API response. `cacheUntil` bounds how long `netagent` holds the credential before asking again. `handling` lists named steps that `netagent` must implement to use the credential. An image that does not implement a listed step refuses the credential. |
| `pkg/binding` | The package the Token Service, the decision service, and the mirror use to resolve a pod to its session, user, tenant, and bindings. |
| Credential binding | An egress-design object that gives sessions a secret for named hosts. Its fields include `secret`, which names what is delivered, `hosts`, `delivery`, `scope`, `required`, and `requestRules`. |
| Binding `scope` | When a binding attaches to a session: `always`, to every session of the tenant; `onRequest`, to a session whose plan names it; or `poolAttach`, to a session on a pool whose add-on attaches it. |
| `attach` template | The header a credential is attached as, with `{{secret}}` standing for the value, such as `Authorization: Bearer {{secret}}`. |
| Stored secret | A credential binding whose `secret` is a token the tenant stores in Lenny, such as a personal access token (PAT), rather than one minted from an App or taken from a user's credential. |
| Effective policy | The hosts and bindings a session may use, which the decision service computes from pool, tenant, and session settings. Its readable set is the repositories a session may fetch. |
| Route and git route | A tenant setting that sends traffic for matching hosts to a target. Git routes carry git traffic, to a git endpoint, the mirror, or `direct`. |
| Mint, mint path, and mint cache | A mint is a call in which the Token Service obtains a short-lived token from a provider, such as a GitHub App installation token or a child token. The mint path is the route those calls take, through `lenny-egress` under a decision. The mint cache holds minted App tokens for reuse across sessions. |
| VCS pool | A credential pool whose provider serves git hosts. It names repository prefixes, a repository allowlist, `allowWrite`, and a provider credential such as a GitHub App, and this layer adds the fields in "Configuration by scope" ([`egress-and-git-design.md`, "VCS pool fields"](egress-and-git-design.md#vcs-pool-fields)). |
| The App, App token, and project token | The App is the tenant's GitHub App, whose installation tokens (App tokens) the egress design mints for git and API access today. A project token is a GitLab project access token. Both act as a bot account. |
| Repository entries | The repositories a session declares, each with a `state`. Writable entries are the repositories the session may write, and `granted` marks one it may write now. |
| Workspace plan fields | `gitClone` sources are the repositories a session's plan clones, and `writableRepositories` lists the repositories it may push to ([§14](../spec/14_workspace-plan-schema.md)). |
| Path enforcement and `allowedRefPatterns` | `netagent` refuses git requests for repositories outside the session's readable or writable sets, and refuses pushes to refs outside the VCS pool's `allowedRefPatterns`. |
| Smart HTTP, `ls-refs`, and `git-receive-pack` | Git's HTTP protocol. `ls-refs` is the request that lists a repository's refs before a fetch, and `git-receive-pack` is the service that receives a push. |
| `mint-scoped` | A provider capability level of the egress design: the provider can mint a token limited to named repositories. |
| Pin (`credentialPinned`) | A setting from [EG-12](egress-and-git-design.md#proposal-split) under which `netagent` terminates TLS for a host, strips credentials other than the binding's, and refuses a request when the host has no binding, so an agent cannot use a credential of its own there. |
| `egressProfile` and `gitAccess` | A pool setting: `restricted`, `proxied`, or `internet`. Under `internet`, the pod also has raw sockets that bypass `netagent` and `lenny-egress`. `gitAccess` gives a `restricted` pool git access to a computed set of hosts. |
| Platform warm-up asset | A platform-defined repository a warm pod fills before it has a tenant, whose host is covered for that pod until bind. |
| `suspendedBy` | A field on every opt-in object. A guardrail, a withdrawn grant, a pin, or a VCS pool sets it, and every component then treats the object as absent. |
| Evidence streams | Append-only records the egress design defines, including `credential.mint` for each token release or mint, `egress.local` for refusals `netagent` makes itself, and `git.operation` for git operations, with refs and new commit SHAs. Records that `netagent` writes are marked `self_reported`, because a compromised `netagent` can omit them. |
| Claim, bind, and pod models | A claim reserves a warm pod for a session, and bind attaches the session and its bindings to that pod. A pool's pod model decides whether a pod serves one session or several: `maxConcurrentSessions` above 1, service mode, and `recycle.enabled` let a pod serve more than one ([§5.2](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). The pool recycle scrub is the §5.2 cleaning of a pod between sessions. |
| Warm pool, warm fill, and SDK-warm | A warm pool holds pods started before any session. A warm fill is content, such as a `gitRepository` clone, placed in warm pods before bind. An SDK-warm pod has the agent process already started and waiting ([§6.1](../spec/06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)). |
| Create and finalize | A session create claims a pod and leaves the session in the `created` state, and the client then calls finalize after uploading content. The workspace plan binds at create when the create request carries it, and otherwise at finalize ([§7.1](../spec/07_session-lifecycle.md#71-normal-flow)). |
| Provisional binding | The state of a pod during a resume rebuild, before the session returns to `running` ([§7.3](../spec/07_session-lifecycle.md#73-retry-and-resume)), in which the pod receives only read tokens and the secrets of bindings with `delivery: inject`. |
| `caller_type` | The kind of caller behind a bearer: `human`, `service`, or `agent`. It is copied on token exchange and cannot be elevated ([§13.3](../spec/13_security-model.md#133-credential-flow)). This design records it on the session row. |
| Impersonation bearer | A token a `platform-admin` obtains through `/v1/admin/impersonation` to act as a user ([§13.3](../spec/13_security-model.md#133-credential-flow), **Platform-admin impersonation**). Its `issued_tokens` row marks it as an impersonation row. |
| Delegation and detached orphans | A session can start child sessions, which form a delegation tree under a root session. A delegation lease records what a child receives from its parent. A detached orphan is a child that keeps running after its parent ends, until `cascadeTimeoutSeconds` ([§8.10](../spec/08_recursive-delegation.md#810-delegation-tree-recovery)). |
| Roles | `tenant-admin` configures a tenant, `security-admin` writes the tenant's guardrails, and `platform-admin` operates the deployment. |
| Guardrail and posture | A guardrail is a setting a `security-admin` writes in the tenant security record ([SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)) that limits what a tenant admin can configure. Posture is that record's read-only report of a tenant's security-relevant settings. |
| Environment | A named scope within a tenant, with its own members and policies, in which a session runs ([TS-1](tenant-scoping-identity-design.md#ts-1-environment-scoping)). `visible(objects, E)` is the set of objects a session in environment E can use. "Unfiltered by environment" means across every environment of the tenant. |
| Invalidation level | A user's disable state under [TS-4](tenant-scoping-identity-design.md#user-lifecycle-ts-4) and [§11.4](../spec/11_policy-and-controls.md#114-user-invalidation): `soft_disable`, `hard_disable`, or `full_revoke`. `processing_restricted` is a separate flag set while an erasure of the user's data is pending ([§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces)). |
| Tenant-derived runtime and default clone | A runtime a tenant builds on a platform base, which can name default git clones that every session on it receives ([DR-9](tenant-derived-runtimes-design.md#dr-9-default-git-clones)). |
| `connectorSelector` | A §10.6 field that filters which of a connector's tools a session may call, by capability. |
| Kodiak | A third-party GitHub bot that merges pull requests when a label is applied. |

### Labels from other designs

Proposals, properties, requirements, and spikes in other designs carry a label whose prefix names the design. This document cites them by label and links each citation to its definition. The table names what each cited label stands for.

| Labels | Design | What they name |
| :-- | :-- | :-- |
| EG-1, EG-2, EG-3, EG-4, EG-5, EG-9, EG-10, and EG-12 | [`egress-and-git-design.md`, "Proposal split"](egress-and-git-design.md#proposal-split) | Proposals of the egress design: EG-1 git prerequisites, EG-2 the pod network agent and proxied egress, EG-3 the pod git path, EG-4 credential delivery, EG-5 the git mirror and warm fill, EG-9 egress policy objects with the single write path for tenant opt-ins, EG-10 evidence ingest, and EG-12 exfiltration controls. |
| P6 | [`egress-and-git-design.md`, "Security properties"](egress-and-git-design.md#security-properties) | The egress security property that a failure of any egress component blocks traffic. |
| UC-1 to UC-7 | [`user-credential-delivery-design.md`](user-credential-delivery-design.md) | Proposals of the generic design: [UC-1](user-credential-delivery-design.md#uc-1-credential-types-and-the-registered-source) credential types and the `registered` source, [UC-2](user-credential-delivery-design.md#uc-2-the-oauth2link-source-and-account-linking) the `oauth2Link` source and account linking, [UC-3](user-credential-delivery-design.md#uc-3-the-usercredential-binding) the `userCredential` binding, [UC-4](user-credential-delivery-design.md#uc-4-request-rules-and-presets) request rules and presets, [UC-5](user-credential-delivery-design.md#uc-5-delivery-file) `delivery: file`, [UC-6](user-credential-delivery-design.md#leak-scanning-uc-6) leak scanning, and [UC-7](user-credential-delivery-design.md#uc-7-add-ons) add-ons. |
| UCP-2, UCP-3, and UCP-5 | [`user-credential-delivery-design.md`, "Security properties"](user-credential-delivery-design.md#security-properties) | Properties of the generic design: UCP-2 refuses a `userCredential` binding on a pod that can serve another session, UCP-3 refuses an impersonation bearer's input to an affected session, and UCP-5 sends a stored token only where its token trust record says and makes it unusable after any change to that record. |
| D1 | [`user-credential-delivery-design.md`, "Decision history"](user-credential-delivery-design.md#decision-history) | The owner's decision to replace the allowlist with generic request rules held as data, which decision 8 here records. |
| GH-1, GH-2, and GH-3 | [`github-credential-addon-design.md`](github-credential-addon-design.md) | Proposals of the GitHub add-on: [GH-1](github-credential-addon-design.md#gh-1-the-github-and-github-enterprise-kinds) the `github` and `github-enterprise` kinds, [GH-2](github-credential-addon-design.md#gh-2-pull-request-authoring-on-github) pull request authoring on GitHub, and [GH-3](github-credential-addon-design.md#gh-3-the-github-deny-preset) the `github-deny` preset. |
| GL-1, GL-2, and GL-3 | [`gitlab-credential-addon-design.md`](gitlab-credential-addon-design.md) | Proposals of the GitLab add-on: [GL-1](gitlab-credential-addon-design.md#gl-1-the-gitlab-kind) the `gitlab` kind, [GL-2](gitlab-credential-addon-design.md#gl-2-user-authored-merge-requests) user-authored merge requests, and [GL-3](gitlab-credential-addon-design.md#gl-3-the-gitlab-deny-preset) the `gitlab-deny` preset. |
| S1, S2, S3, S3c, S5, S6, S15, and S16 | [`github-credential-addon-design.md`, "Validation owed"](github-credential-addon-design.md#validation-owed) | Spikes against GitHub. S1 asks what sets a child token's expiry, S3 and S3c what GitHub treats as the pusher, S5 whether revoking a child leaves its parent valid, S15 what a provider-issued child token can do, and S16 whether approvals survive a base change. |
| S12 and S13 | [`gitlab-credential-addon-design.md`, "Validation owed"](gitlab-credential-addon-design.md#validation-owed) | Spikes against GitLab. S12 asks how GitLab's committer rule matches a commit and which tiers offer external status checks, and S13 whether a per-session fine-grained token can be minted and can push. |
| SO-2, SO-4a, SO-5b, and SP11 | [`security-oversight-design.md`](security-oversight-design.md) | [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) the tenant security record, which holds guardrails and posture; [SO-4a](security-oversight-design.md#so-4a-mirror-to-the-tenant-chain) the mirror of operator access, including impersonation, to the tenant's audit chain; [SO-5b](security-oversight-design.md#so-5b-repository-write-approvals) repository write approvals; and [SP11](security-oversight-design.md#security-properties), the property that a repository marked for approval receives no write token until its approval exists. |
| TS-1 and TS-4 | [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) | [TS-1](tenant-scoping-identity-design.md#ts-1-environment-scoping) environment scoping, and [TS-4](tenant-scoping-identity-design.md#user-lifecycle-ts-4) user lifecycle and invalidation. |
| DR-9 | [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#dr-9-default-git-clones) | Default git clones on a tenant-derived runtime. |
| FE-5 | [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md#fe-5-capture-runbook-and-abuse-workflow) | The capture runbook and abuse workflow. |
| C6, C8, C9, C10, and OR-3 | [`tenant-outbound-connector-design.md`](tenant-outbound-connector-design.md#security-properties) | Properties and a requirement of the outbound connector: C6 keeps traffic TLS end to end, C8 stops an outbound connector host shadowing a platform-reserved host, C9 treats content from a customer network as hostile, C10 fails closed when an outbound connector fails, and OR-3 requires every private destination feature to work on a connector route. |

## High-level design

Lenny agents reach git repositories today through a credential the tenant owns, such as a GitHub App, so any user of a tenant can have an agent read any repository the tenant allows, and every pull request an agent opens is authored by the App. A user can therefore reach a repository through Lenny that the provider would not open to her. Because the provider cannot tell who asked for the agent's pull request, that person can also approve her own agent's work, which defeats two-person review.

This design adds repository authorization and pull request authoring for git hosts, and a tenant turns each one on separately. With repository authorization, each user links her own provider account once in a browser, and Lenny asks the provider, before her session starts and every few minutes while it runs, whether she can still reach each repository the session declares. With pull request authoring, Lenny derives a short-lived token from her linked account, limited to the session's repositories, and `netagent`, a proxy in the agent's pod, adds it to the agent's requests, so the provider records her as the author while the agent never sees the token, and default rules refuse approving and merging. Tenants that refuse user tokens in pods can instead run an approval check that fails a pull request approved only by the people whose sessions pushed to it. The main limit is that two-person review on work the agent pushed depends on the repository's own rule about the most recent push. This document is the VCS layer of the generic [user credential mechanism](user-credential-delivery-design.md), and separate add-ons implement it for GitHub and GitLab.

The rest of this section states each part in the design's terms, which "Terms" above defines.

The design is split into the proposals UG-1 to UG-4 here and the add-ons' GH-n and GL-n proposals. It rests on [`user-credential-delivery-design.md`](user-credential-delivery-design.md), which owns links, the link flow, the binding, request rules, and the add-on model. This layer is an add-on to that design, and the GitHub and GitLab add-ons build on this layer. UG-2 and UG-3 build on UG-1's link kinds, UG-3 also needs UG-2's check, and UG-4 needs a link alone.

**VCS link kinds (UG-1).** A VCS kind is a link provider kind that a VCS add-on registers. Its block names a host that a VCS pool covers, it fixes the endpoints, the API hosts, and `attach`, and it must name a shipped narrowing template when its delivery ceiling is `inject` or `file`. A provider account belongs to one Lenny user across every link provider of a VCS kind on that host.

**Repository authorization (UG-2).** A VCS pool that sets `userAuthorization: required` names a link provider in `userAuthorization.linkProvider`. For each repository a session declares under that pool, the Token Service asks the provider, with the user's parent token, whether the user holds the declared access level. The check runs at session bind, at resume, and before every token mint, and a gateway job repeats it for running sessions every 300 seconds. A `refused` or `unavailable` verdict refuses the session or the mint, and no path falls back to an App token. A refusal during a session marks the repository's entry `userRefused` and pushes a binding change that flushes `netagent`'s cached tokens, so an honest pod loses access within 6 minutes: the 300-second interval, jitter, and the time to flush, which "Revocation" totals. Under such a pool, a session's readable set holds only the repositories it declared. Pod models that could carry one user's state into another session are refused, and service identities listed on the pool give automated sessions a path with no user.

**Pull request authoring (UG-3).** Under pull request authoring, the agent's pull requests are made with a token derived from the requesting user's own link, so the provider records that user as their author. A VCS pool that sets `prAuthoring: user` attaches a `userCredential` binding, through the attach extension point, to each human session that declares a writable repository under it. The binding uses the `oauth2Link` source with `delivery: inject`, and it replaces the tenant's App binding on the API host for that session. The kind's shipped template derives a per-session token limited to the session's writable repositories whose entry `state` is `granted`, and the kind's default preset refuses the operations that approve or merge. The pool's `pushIdentity` decides whose token carries git pushes. This layer's host policy decides which other tokens may sit on a covered host: a stored secret is refused, and a binding with `delivery: file` is admitted only where every VCS pool covering the host sets `allowRuntimeHeldCredentials`.

**Independent approval check (UG-4).** An optional status check from the tenant's App fails a pull request when every approver is its opener or a user whose session pushed to it. It serves tenants that refuse user tokens in pods, and it is required on a VCS pool that sets `prAuthoring: user` with `pushIdentity: app`.

**Add-ons.** The GitHub add-on supplies the `github` and `github-enterprise` kinds, the repository check against the GitHub API, the `github-scoped` template with its per-session child tokens, pushes as the user, and the `github-deny` preset. The GitLab add-on supplies the `gitlab` kind, its repository check, the `gitlab-deny` preset, and user-authored merge requests from per-session fine-grained tokens once spike [S13](gitlab-credential-addon-design.md#validation-owed) confirms them.

```
 Browser ──(link flow)──────> Gateway ──(code exchange, checks)──> Token Service ──> provider OAuth and API
 Client ──(create session)──> Gateway                                    ▲          (through lenny-egress)
                                                                         │ pod credential API: derived token, request rules
 Pod: gh, glab, git ──(placeholder token)──> netagent ───────────────────┘
                                                │ token attached, request rules applied
                                                └──> lenny-egress ──> provider API and git
```

Two limits frame the result. Two-person review on pushed work holds where the repository requires approval of the most recent push, and UG-4 extends it to every pusher where a tenant runs it. A compromised `netagent` holds the session's child tokens and can approve and merge with them, as "Residual risks" states.

## Personas and requirements

Alice is a developer at `acme` and Bob is her reviewer. Carol is `acme`'s tenant admin and Dave is its `security-admin`. Tenant `globex` runs on GitHub with its own App. Tenant `initech` runs self-managed GitLab, and Erin is a developer there.

- UR-1. Alice links her account once per link provider, and she sees the prompt before her session starts rather than a failure in the middle of it. This applies to repositories under a VCS pool with `userAuthorization: required`, which every VCS pool with `prAuthoring: user` also sets.
- UR-2. A session reaches a repository under a VCS pool with `userAuthorization: required` only when the requesting user can reach it at the access level the session declared.
- UR-3. When Alice loses access, her session stops reading and writing that repository within a stated bound.
- UR-4. Alice's pull request has Alice as its author, and Alice cannot supply the approval it needs. On GitHub, when the repository requires approval of the most recent push, her approval does not count while a push her agent made is the most recent one, because her agent pushes as her ([S3](github-credential-addon-design.md#validation-owed)). GitHub's rule reads the latest push alone, so once someone else pushes after her, her approval counts again for a pull request that holds her agent's commits, as it would after a push she made by hand. Without that repository rule, GitHub lets a pusher approve. UG-4 counts every pusher and closes both gaps where a tenant runs it ("Residual risks").
- UR-5. An agent running any `gh` or `glab` command works with the tool unmodified and with a placeholder as its token, **within the token's permissions**, except what the preset refuses, which a tenant can change. The host refuses what the token does not permit: under the child token's default permissions, `pull_requests: write` and `contents: read`, that includes `gh issue create` and `gh run`, and likely `gh issue view` and `gh pr checks` ([S15](github-credential-addon-design.md#validation-owed)). The narrowing template's `permissions` is data a tenant can widen. **This is met on a stub under git path T, and live hosts are owed:** `gh` and `glab` worked on every command listed under "Git remotes and client tools" **[measured]**. `gh` needs git in the image even with `-R`, and `gh release upload` needs a binding on `uploads.github.com`. A new `gh` release needs no change here, because a new operation passes by default.
- UR-6. With the preset on, no agent can approve or merge through an operation the preset names, with a credential Lenny attaches under a binding with `delivery: inject`. **This is a deny list, and the owner accepted that a new operation with that power passes until the preset is updated.** A tenant can clear the preset unless a `security-admin` requires it. It does not hold for a token the runtime holds under a binding with `delivery: file`, or against text a repository's own automation reads as a command, which "Comment-triggered actions" treats.
- UR-7. Automated sessions keep working under a named service identity, and a person cannot use that path to skip the check.
- UR-8. A tenant can refuse user tokens in pods and still get a two-person review check.

## Threat model

### Security properties

- UP-1. A refresh token never leaves the Token Service, and it goes only where its token trust record says. On a link provider of a VCS kind, a parent access token never leaves either, because such a link provider whose `delivery` ceiling is `inject` or `file` must name a platform-shipped narrowing template. On a link provider with `kind: oauth2` and no template, `netagent` receives the parent access token under the record's `delivery` ceiling, as [`user-credential-delivery-design.md`](user-credential-delivery-design.md) states, and a replica can obtain it under `leakScan` on a binding with `delivery: file`. The mirror never holds a parent token. The Token Service API and the `user_links` store hold this.
- UP-2. Under a platform-shipped narrowing template, a child token is limited at the host to the session's writable repositories whose entry `state` is `granted` and to the template's `permissions`, by default `pull_requests: write` and `contents: read`. The git child, which exists on a VCS pool with `pushIdentity: user`, is limited to `contents: write` alone. The host enforces this, which [S15](github-credential-addon-design.md#validation-owed) measured live for the repository limit and the merge refusal. The write path requires a shipped template on every link provider of a VCS kind whose `delivery` ceiling is `inject` or `file`.
- UP-3. Under a binding with `delivery: inject`, `netagent` attaches a user-derived token only to the binding's hosts and only to a request the binding's request rules admit, with `requestRules.default: allow` and the preset as this configuration's default. A credential whose required handling `netagent` does not implement is refused. `netagent` holds this, and the Token Service sets the rules and the requirement. Under `delivery: file` none of this applies.
- UP-4. A session with a requesting user never receives an App token for a repository under a VCS pool with `userAuthorization: required` unless a fresh verdict, one no older than the 60-second verdict cache (`verdictCacheSeconds`), says the user holds that access. The Token Service holds this before its cache lookup, and `lenny-egress-authz` holds it independently from the recorded entry state.
- UP-5. A user-derived token never reaches a pod that can serve another session or a later session. The binding validator, the session gateway check, and the Token Service each hold this.
- UP-6. A session with no link, or a failed or unavailable check, never falls through to an App token on such a pool.
- UP-7. An impersonation bearer cannot cause a linked token to be used. The gateway's session input check holds this, over the session's delegation tree, under [`user-credential-delivery-design.md`](user-credential-delivery-design.md).
- UP-8. One tenant's link, check result, or token never reaches another tenant. Link rows sit under row-level security, and every cache key carries the tenant.
- UP-9. A link's provider account is one the tenant's identity source ties to the Lenny user, unless the tenant records that it does not. The link flow and the sweep hold this.
- UP-10. No token held under this design's links leaves the Token Service except through the pod credential API, under the binding's `delivery` and the link provider's `delivery` ceiling, or to a session's coordinating gateway replica under `leakScan`, and a compromised replica can acquire any such session's lease, as [`user-credential-delivery-design.md`](user-credential-delivery-design.md) states. No gateway connector path receives one. Decision 3 refuses a connector of its own on these hosts when a VCS pool on the host sets `userAuthorization: required`.

### Threats

| Attacker | Can | Cannot | Why |
| :-- | :-- | :-- | :-- |
| Prompt-injected agent on Alice's session | Create, edit, close, retarget, and comment on pull requests as Alice on a session repository, including Bob's, and push as Alice wherever she can push there. Read what the child token reads. Waste Alice's rate limit up to the session's quota. Write text a repository's bot treats as a command. Use a new operation with merge or review power that the preset does not yet name. | Use an operation the preset names, unless a tenant cleared it. Act outside the session's repositories under a narrowing template. Read the token under `delivery: inject`. | Request rules refuse, the host limits the child, and `delivery: inject` keeps the token in `netagent`. The bypass analysis of the deny-list rules, `requestRules.default: allow` with the preset, ran on the stub **[measured]**, which proves forwarding only. |
| Two injected sessions, Alice's and Bob's | Each opens a pull request as its user. Each can comment on the other's, which reaches any comment-driven approval bot a tenant has not addressed. | Approve the other's pull request through the review operations the preset names. | The preset names `addPullRequestReview` and the REST review route. A tenant adds `bodyPatterns` or denies `addComment` for its bots. |
| Compromised `netagent` on Alice's pod | Use the session's child tokens and any App token it holds until they expire, including approving within the session's repositories, because GitHub has no permission narrower than `pull_requests: write` for this, and merging an approved pull request as Alice with the git child. | Obtain the parent or refresh token. Reach repositories outside the child's list on GitHub or GHES. Mint after the pod is quarantined or the binding ends. | UP-1 and UP-2. "Residual risks" states this plainly. |
| Alice herself, against review | Link a second account, have her agent author under it, and approve as herself. | Do so when the link provider's `identityBinding` is other than `unverified`, since the flow refuses an account that is not hers. | UP-9. Under `identityBinding: unverified` she can, and posture says so. |
| Prompt-injected agent calling a connector tool | Reach a GitHub MCP server through a tenant's connector, where one is allowed, and call its tools under that connector's token. | Reach it with a token from a link. | UP-10. Decision 3 refuses a connector on these hosts when a VCS pool on the host sets `userAuthorization: required`. |
| Prompt-injected agent using git push options | Ask GitLab to create and auto-merge a merge request, or skip CI and secret scanning, on a push that passes ref checks. | Send a push option on a covered host. | `netagent` refuses them at `git-receive-pack`, under a requested change. |
| Compromised `lenny-egress-authz` | Misdecide a CONNECT (the HTTP request that opens a tunnel to a host) or a scope decision (whether a git request may read or write a repository), or remove a host from the covered set, so git to it goes as an opaque tunnel and a token the agent holds reaches it, with no mint on that path, as [the egress design](egress-and-git-design.md) states. | Mint, or remove the Token Service's own check and its rules. | The two gates, and `handling` and resolved rules coming from the Token Service on the pod credential API. |
| Prompt-injected agent crafting bytes for a parser difference | Send documents and JSON that a rule evaluator and a host read differently. | Have those bytes reach the host while rules are in force. | `netagent` forwards a document and body it generated from the checked structure. See [`user-credential-delivery-design.md`, "Canonical forwarding"](user-credential-delivery-design.md#canonical-forwarding). |
| Prompt-injected agent holding a token under `delivery: file` | Everything that token permits, from anywhere. | Be limited by anything in this table. | A tenant chose this under [`user-credential-delivery-design.md`](user-credential-delivery-design.md)'s opt-in, and a VCS pool with `userAuthorization: required` on that host refuses it unless every VCS pool covering the host sets `allowRuntimeHeldCredentials`. |
| Hostile tenant registering a host | Run a fake GHES or GitLab that answers checks and token calls as it likes, for its own users. Return hostile bodies and redirects to the Token Service. | Affect another tenant. Shadow a platform-reserved host. Receive another tenant's client secret, token, or cached verdict. | The [egress design](egress-and-git-design.md)'s mint path bounds on time, response size, and redirects, the outbound connector's properties [C8](tenant-outbound-connector-design.md#security-properties) and [C9](tenant-outbound-connector-design.md#security-properties), and tenant-keyed caches. |
| Tenant admin | Delete links in the tenant and read their metadata. Edit a token trust record, which makes every stored token under it unusable. **Trick a user past the link flow's confirmation page into granting a new token to a host she runs.** Change a runtime and read a token under `delivery: file`. | Read a link's stored token through any change to the record, because a mismatched token is sent nowhere. | Envelope encryption for data class T4, the specification's Restricted class for credentials ([§12.9](../spec/12_storage-architecture.md#129-data-classification)), no read route, and [UCP-5](user-credential-delivery-design.md#security-properties) of the generic design. A new token she tricks a user into granting has no per-field fix, because she configures the tenant. [The generic design's decision 4](user-credential-delivery-design.md#decisions) records the owner's settlement, and the `tokenTrust.allowedRecords` guardrail, which lists the token trust records a `security-admin` has approved, is the enforcing control. |

## Current state

| Claim | Status | Source |
| :-- | :-- | :-- |
| §9.3's flow starts inside a session, through URL-mode elicitation (a request asking the client to open a URL, such as an OAuth page) on a tool call | True for the specification. The code also has an admin route any authenticated caller can use. | [§9.3 OAuth/OIDC Flow](../spec/09_mcp-integration.md#oauthoidc-flow), `pkg/gateway/externalapi/admin/connector_oauth.go` |
| Connector tokens are keyed on tenant, connector, user, and environment | True | `connectorcredstore.go`, migration 0059 |
| §4.3 forbids the gateway holding key management service (KMS) decrypt for downstream OAuth tokens | Stated, and the gateway holds that access today under the deferred F-4.3.1, one of the findings filed in `BUILD-GAPS.md` at the repository root | [§4.3 Token Service](../spec/04_system-components.md#43-token-service), `cmd/lenny-gateway/credentialsurface.go` |
| Pods never receive downstream OAuth tokens | Stated three times, in §4.3, §9.3 **Key invariants**, and §13.3 | [§13.3 Credential Flow](../spec/13_security-model.md#133-credential-flow) |
| `github` is a known provider with no user delivery | True | `pkg/credential/credential.go`, `pkg/credential/lease.go` |
| An impersonation bearer cannot create a session | True through `callercheck.Check` | `pkg/gateway/sessionserver/user_gate.go`, `pkg/gateway/callercheck/callercheck.go` |
| A delegated child and a detached orphan carry the parent's user | True, on the session row | [§8.2 **Child identity.**](../spec/08_recursive-delegation.md#82-delegation-mechanism), [§8.10 Delegation Tree Recovery](../spec/08_recursive-delegation.md#810-delegation-tree-recovery) |
| The repository readable set includes the tenant's whole allowlist | True in the design, under "Effective policy": "the session's `gitClone` sources plus the tenant's repository allowlist" | [`egress-and-git-design.md`, "Effective policy"](egress-and-git-design.md#effective-policy) |

## UG-1: VCS link kinds

[UC-2](user-credential-delivery-design.md#uc-2-the-oauth2link-source-and-account-linking) of [`user-credential-delivery-design.md`](user-credential-delivery-design.md) owns the link flow, the stored links, identity binding, refresh, and revocation at the provider, for every kind. This proposal states what every VCS kind adds, and the GitHub and GitLab add-ons implement it.

- **Host.** A VCS kind's block names `host`, the canonical host, which one of the tenant's VCS pools covers. Several link providers of a VCS kind can share a host, such as one whose App has `metadata: read` for the check and one whose App authors pull requests.
- **Fixed endpoints.** The kind fixes the endpoint paths from the host, so a tenant supplies no OAuth URL, and it fixes `apiHosts` and `attach` from the host's flavour under "Request rules on VCS hosts".
- **Templates.** A VCS kind takes shipped templates alone, with the tenant override limited to the fields each template names, and a link provider of a VCS kind whose delivery ceiling is `inject` or `file` must name one.
- **One account per host.** Across the link providers of a VCS kind on one host, a `provider_user_id` belongs to one Lenny user. A link that names an account another user holds through any such link provider on that host is refused, so [`user-credential-delivery-design.md` decision 31](user-credential-delivery-design.md#decisions) holds whichever link provider's App was used, and one person can link the same account through several link providers. That decision refuses two Lenny users on one provider account within a tenant. Removing or revoking a person's link on a host covers each of their links there.
- **Ceilings.** The link provider that a VCS pool with `userAuthorization: required` names for its check needs a delivery ceiling of `none` or above. The link provider behind the binding that a VCS pool with `prAuthoring: user` attaches needs `inject` or above.
- **`oauth2` on VCS hosts.** A link provider with `kind: oauth2` whose endpoint sits on a host a VCS pool covers, or on a VCS kind's API host, is refused, so that kind cannot stand in for a VCS kind, and a VCS pool added later suspends such a provider. This is part of this layer's host policy.
- **Identity.** A VCS kind supports every `identityBinding` value, and a link provider write must set one. Decision 3 and **Connectors** under "User tokens on a VCS host" cover connectors on these hosts.

### User tokens on a VCS host

Revision 3 said a host has one store of user tokens per tenant and refused VCS providers on `/v1/credentials`, the §4.9 route on which a user registers her own credential for the LLM reverse proxy. The owner's generic mechanism must not be tied to or excluded from GitHub, so this is reworked.

- **The defect stays refused.** The defect is that `POST /v1/credentials` stores a credential with `provider: github`, which can never be delivered, because that route delivers only to the LLM reverse proxy. Such a write is refused with `VALIDATION_ERROR`, as are `aws_bedrock` and `vault_transit`, which have the same defect **[source]**. [`user-credential-delivery-design.md`](user-credential-delivery-design.md) does this in [UC-1](user-credential-delivery-design.md#uc-1-credential-types-and-the-registered-source).
- **A tenant-defined type on a GitHub host is possible.** Under that design, a tenant can define type `gh-pat` with `source: registered` and bind it to `api.github.com` or `github.com`, subject to the refusals "Other tokens on these hosts" states. A type's hosts, rather than a binding's, are what every rule evaluates. A binding of it with `delivery: file` needs the `userCredentialDelivery` grant, a `delivery` ceiling of `file` in the type's token trust record, and a binding opt-in, which is the tenant admin writing `delivery: file` on that binding. "Other tokens on these hosts" states the result.
- **The check names one account.** The check on a VCS pool with `userAuthorization: required` uses the link provider that `userAuthorization.linkProvider` names and no other row, so a `registered` token on the same host never satisfies UG-2.

**Connectors.** Revision 3 refused a connector on a token-holding link provider's hosts because a connector's token reaches the host with "no request filter, repository list, pod model check, sweep, or `cacheUntil`". Part of that reason has weakened and part has not:

| Reason | Status now |
| :-- | :-- |
| No request filter on the connector path | Weaker. The filter is now an optional preset that a tenant can clear on the pod path, and §10.6's `connectorSelector` already filters a connector's tools by capability, with `deniedCapabilities`, inferred from MCP `ToolAnnotations`, plus `PreConnectorRequest` interceptors, which see each tool call before the gateway sends it **[source]**. Whether those annotations mark a review tool as write is not established. |
| No repository list | Holds. A connector's token is not narrowed per session. |
| No user authorization check or sweep | Holds, against UR-2, wherever the VCS pool sets `userAuthorization: required`. |
| No `cacheUntil` | Holds, and §9.3's refresh has no caller at all **[source]**. |
| No pod model check | No longer a reason. The connector path is on the gateway, which attributes each call to its session. |

Revision 2's `hostLink`, which would have given a connector a link's token, stays withdrawn, because it is the one version of this that breaks UP-1. **The owner settled decision 3: a connector is refused on a link provider's host set only when a VCS pool on that host sets `userAuthorization: required`,** since UR-2 is the property a connector skips.

## UG-2: Repository authorization

### The requirement sits on the VCS pool

`userAuthorization` on a VCS credential pool takes `off` (the default) or `required`. "Pool" in the feedback proposal is ambiguous, and the VCS pool is the correct object: it is a tenant object, it names the repository prefixes, [TS-1](tenant-scoping-identity-design.md#ts-1-environment-scoping) environment scoping can scope it, and a shared warm pool is platform-global. A tenant's `security-admin` can force the value through a new `userAuthorization.require` guardrail in the tenant security record of [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture), which sets `suspendedBy` on non-compliant pools.

This work belongs in the Token Service and the pod credential API. `usercreds` is the §4.9 LLM reverse proxy path, and the feedback proposal's change there would alter nothing the design's git path reads, so `pkg/gateway/credentials/usercreds/usercreds.go` is left as written.

### Declared repositories

Under [the egress design](egress-and-git-design.md), a session's readable set includes the tenant's whole allowlist, so an agent can fetch an undeclared allowlisted repository at run time. For repositories under a VCS pool with `userAuthorization: required`, the readable set holds only what the session declared: its `gitClone` sources, a new `readableRepositories` plan field, its `writableRepositories`, and any private `gitRepository` warm fill on the warm pool it runs on. A default clone on a tenant-derived runtime is a `gitClone` source once composed, so it is declared, and the check runs on the session's user. There a tenant admin named the repository, so a refusal omits its name ([DR-9](tenant-derived-runtimes-design.md#dr-9-default-git-clones) of [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md)). `netagent` refuses others with 403 and the decision identifier. Decision 22 records the cost and the on-demand alternative.

Public repositories under no VCS pool with `userAuthorization: required` are unaffected, because nothing is authorized about them.

### The check

The Token Service makes one call with the parent token per repository and level. It compares the provider's numeric identifier with the one the VCS pool stores, because GitHub serves a renamed repository under its old name and the egress design keys on the identifier.

Each VCS add-on states the read and write calls of its kind, and which fields report inherited and shared access.

Classification, which every caller uses:

- `allowed`: the provider answered and the level holds.
- `refused`: 404, 403 without a rate-limit header or a header the kind classifies, or a level below the declared one.
- `unavailable`: 5xx, 429, a 403 with a rate-limit header, a timeout, a CONNECT failure, an outbound connector being offline, or a refresh lease not obtained, which is the 30-second row lease one gateway replica holds while it refreshes a link's parent token.
- A 401 attempts refresh once and then counts as `refused` with the reason `user_act_required`.
- A kind can classify a provider's own header, as the GitHub kind does for single sign-on with the reason `sso_required`.

A provider `refused` is `USER_REPOSITORY_ACCESS_DENIED`, naming the repository Alice declared. It reveals nothing she did not name.

### When the check runs

| Moment | Who asks | Refused or unavailable means |
| :-- | :-- | :-- |
| Session bind, before `ls-refs`, and before any pod claim when the plan binds at create. A plan bound at finalize follows the create-time claim, so a refusal there leaves the session in `created` holding its pod until the session ends. | Gateway, for every declared repository at its level | Create or finalize refused. `unavailable` is a retryable `USER_AUTHORIZATION_UNAVAILABLE`. An unauthorized user therefore learns no commit. |
| Resume, and the provisional binding of §7.3 | Gateway | Resume refused, and §6.2's failure path applies |
| Each mint on the pod credential API for git or a binding | Token Service, before its cache lookup | No token, and `netagent` answers 403 or 503 |
| Timer, each session | Gateway job calling the Token Service | The entry changes state (below) |
| Mirror read | The decision service, from the recorded entry | Mirror refuses |

The order on the mint path is fixed: verify pod, read the session, run the check, ask the decision service on the egress decision API, and only then read the mint cache. The cache key keeps its form, because under this rule a cached App token is reused only by a session that has already passed. The check's result has its own cache, keyed on tenant, `link_ref`, repository identifier, and level, for 60 seconds, which is the "short-lived cache" the feedback proposal asked for.

### Revocation

Mid-session revocation needs a writer and a route. The route exists: `netagent` flushes on every binding change on the pod binding API, and a binding push makes each decision service replica re-read the session row. This design adds the writer and closes the paths that skip it.

**Entry state.** Writable entries are already records with a `state`. Readable entries become records too (see "Changes requested of egress-and-git-design.md" near the end). A new state, `userRefused`, on a writable entry denies both writes and reads of that repository, at both gates and in `netagent`, and an unknown state on a readable entry denies access, as the hook row in [`egress-and-git-design.md`, "Reserved for other designs"](egress-and-git-design.md#reserved-for-other-designs) states. Each entry records `verifiedAt`, the time of its last `allowed`. A value per session would let one repository's success mask another's `unavailable`.

**Sweep.** §4.3 has the gateway calling the Token Service, and the Token Service has no route to gateway replicas, so a gateway background job does this, as orphan cleanup does. Every `recheckIntervalSeconds` (300 by default, with up to 10% jitter) it calls the Token Service for each distinct link and repository among bound sessions, bypassing the verdict cache. A `refused` writes `userRefused` on the matching entries in the row's transaction and pushes a binding change. `allowed` updates that entry's `verifiedAt`, and every update for a session is one write to its row. Deduplication makes the provider load per link and repository rather than per session, and conditional requests lower it further.

**What a binding change does.** Every `lenny-egress-authz` replica re-reads the row and drops the pod's decisions. `netagent` flushes cached tokens and closes that session's tunnels at once. The next pod credential API call is refused, and the mirror's next egress decision API call is refused.

**`cacheUntil`.** The pod credential API response gains this field, the earlier of the credential's expiry and a value the Token Service chooses. `netagent` re-asks at that time. For a repository under a VCS pool with `userAuthorization: required`, the Token Service sets 300 seconds, on App tokens as well as child tokens, so a lost subscription cannot extend use past that. This generalizes the 15-minute lifetime stored secrets have.

**Latency, by path:**

| Path | Bound | Tag |
| :-- | :-- | :-- |
| PR authoring token at the host, after the host removes Alice's access | At once, since a user token is limited to what the user can reach | **[documented]** |
| Sweep detection | 330 s (300 s plus jitter) | **[design]** |
| Detection to `netagent` flush | One gRPC round trip, or 30 s when a push is lost (the egress design's re-read) | **[design]**, on the egress design's mechanism |
| Honest `netagent` holding an App token, by the sweep | 360 s | **[design]** |
| Honest `netagent` holding an App token, by `cacheUntil` with a cached positive verdict | 360 s (300 s and 60 s) | **[design]** |
| Mirror read, or git fetch through `netagent` | 360 s, by the same binding change | **[design]** |
| User deletes a link, or `full_revoke` | One round trip, or 30 s | **[design]** |
| Compromised `netagent` holding an App token | The token's remaining life, up to 1 hour, because an installation token is shared by the mint cache across sessions | **[documented]** for 1 hour |
| Compromised `netagent` holding a child token | Until Lenny revokes it at the host or it expires. On a verdict of `refused`, the Token Service calls `DELETE /applications/{client_id}/token` on the session's child tokens, which [S5](github-credential-addon-design.md#validation-owed) showed leaves the parent and other children valid **[measured]**. | **[design]** |
| Git operations in flight | A running push or fetch ends when its tunnel closes | **[design]** |

The stated figure for an honest pod is **6 minutes**. A tenant can lower `recheckIntervalSeconds` to 60 at a cost in provider calls. Webhooks would shorten this and are excluded from the first version, because Lenny has no provider webhook ingress and a repository-level `member` event would need one on every organization; UG-4 builds that ingress, and a later change can feed the sweep from it.

Once an entry is refused, `onAccessLost` on the VCS pool decides what happens to the session. The default, `deny`, keeps the session running without that repository and tells the client with a `repository_access_lost` event. `terminate` ends it. Content already in the workspace stays where it is under `deny`. See decision 13.

### Mirror, warm fills, and the 6-hour promise

- **Mirror reads.** The mirror authorizes each request through the egress decision API. For a repository under a VCS pool with `userAuthorization: required`, that check names the repository and reads the entry's state, so a store-served fetch with no mint still stops.
- **Warm fills.** A warm fill has no user, so the mirror's warm fill keeps its App path. Its contents enter a pod an agent reads. A private `gitRepository` warm fill of a repository under a VCS pool with `userAuthorization: required` therefore counts as a declared read for every session bound to that warm pool, and a user lacking access is refused at bind with a reason that names the warm pool. A tenant needing per-user variance uses a warm pool with no such fill. A warm default clone on a tenant-derived runtime is such a fill, and its clone is already a declared read, so the same check covers both. A resume checks the runtime's default clones as well as the stored plan, because a clone added after the session bound can put a store the plan does not declare on the session's next pod. A default clone's `write` value adds a writable entry at bind, and a user who can read and not write gets a read-only session on that entry, as [DR-9](tenant-derived-runtimes-design.md#dr-9-default-git-clones)'s "Write scope" states. A repository the client names keeps this design's refusal. A default clone's `write: always`, under which every session on the runtime can push to the clone with no statement from the client, is refused for a repository under a VCS pool with `prAuthoring: user`. The trigger of "The attach rule" under UG-3, a writable repository the session declares, therefore always comes from the client.
- **Outage rule.** The mirror's "Upstream outage" row in [`egress-and-git-design.md`, "Git mirror"](egress-and-git-design.md#git-mirror-lenny-git-mirror), and its 6-hour confirmation, conflict with fail-closed authorization. The rule is:
  - A new session, a resume, and a mint need a current `allowed` from the provider. `unavailable` refuses them, with a retryable error. Writes need the host anyway.
  - A running session's reads of a repository continue from the mirror's store while that entry's `verifiedAt` is within `verdictMaxAgeSeconds` (900 by default, and a ceiling of 3600). Past that, both gates refuse reads of that repository until a sweep succeeds, and other repositories are unaffected.
  - The 6-hour fallback therefore never applies to a repository under a VCS pool with `userAuthorization: required`. It keeps its meaning under every other VCS pool. A tenant that values outage reads above per-user limits leaves the pool's `userAuthorization` at `off`.
  - A stalled sweep, whatever its cause, has the same effect after 900 s, which is the direction of the egress security property [P6](egress-and-git-design.md#security-properties), that a failure of any egress component blocks traffic. `UserAuthorizationSweepStalled` alerts well before that.

### Pod models

UP-5 refuses a `userCredential` binding, with `422 USER_CREDENTIAL_UNSUPPORTED_POD` as [`user-credential-delivery-design.md`](user-credential-delivery-design.md) states for every type, and the repositories of a VCS pool with `userAuthorization: required`, with `422 USER_AUTHORIZATION_UNSUPPORTED_POD`, on these pods. The binding validator refuses a definition that can only reach such pods. The generic design's `retireOnRelease` keeps a later pool edit from making the pod eligible to serve another session, and the pod credential API refuses a release without it. It applies to these repositories too, and [UC-3](user-credential-delivery-design.md#uc-3-the-usercredential-binding) builds it.

| Pod | Why |
| :-- | :-- |
| `maxConcurrentSessions > 1` | `netagent` cannot attribute a request to a session slot, one of the concurrent sessions the pod serves, and the design already gives such pods the union of readable and writable sets. Alice's token would serve Bob's slot. |
| Service mode | One process serves several sessions, and §5.2 gives it no session binding. |
| `recycle.enabled: true` | §5.2 states the pool recycle scrub is best-effort and "is not a security boundary". On a pod whose `recycle.runtimeProcess` is `keep`, the runtime process, and any state an injected agent planted in it, runs into the next session, whose user's token then answers on `netagent`'s unauthenticated loopback port. Residue from Alice's repositories would also reach Bob. |
| A binding with `scope: always` | Its cache keys on the tenant pin, the pod label that ties a pod to one tenant, and it attaches to every session. A user source is refused at write. |
| A pod that has served a session | The same reason as `recycle.enabled`. It catches a pool whose `recycle.enabled` was edited back to `false` with used pods in it. |

SDK-warm pods remain allowed, because the process that exists before bind runs deployer image code and no earlier agent session. The refusal costs concurrent and recycled pools their efficiency for these tenants, which decision 14 records. Service-mode runtimes cannot use a user identity at all under this design.

### Identity cases

The requesting user is read from the session row by both gates, through `pkg/binding`, and never from a token or a pod. A running session does not depend on its creator's bearer.

- **Impersonation.** A bearer whose `issued_tokens` row is an impersonation row already cannot create, derive (start a session from another session's workspace snapshot), replay (re-run a session's prompt history against another runtime version), or create-and-start. For a session with a repository under a VCS pool with `userAuthorization: required`, or holding a `userCredential` binding, [`user-credential-delivery-design.md`](user-credential-delivery-design.md)'s session input check refuses it at `executor.Executor.Send`, `sessioninbox.Inbox.Enqueue`, and the non-message session routes, with `403 FORBIDDEN` and the reason `impersonation_user_credential`, and memory writes (`lenny/memory_write` to the memory store of [§9.4](../spec/09_mcp-integration.md#94-memory-store)) refuse it on every session. A test covers every mounted mux. An operator can read the session and cannot drive it into using Alice's token. [SO-4a](security-oversight-design.md#so-4a-mirror-to-the-tenant-chain)'s `impersonated_by` claim is the same signal once it exists.
- **Delegated children.** A child carries its parent's user on its row, so the same link and check apply to its own declared set for App tokens. It receives the pool's `userCredential` binding only when the delegation lease names it, and `poolAttach` does not bypass that, under [`user-credential-delivery-design.md`](user-credential-delivery-design.md). A child with no lease entry has no API token, and no App binding stands in for it, so its identity does not change silently. Its pod must pass the same pod model test. `credential.mint` records its `sessionId`, and the session row gives its `root_session_id`.
- **Detached orphans.** An orphan "carries the originating user's identity" ([§8.10](../spec/08_recursive-delegation.md#810-delegation-tree-recovery)), keeps being swept, and ends at `cascadeTimeoutSeconds`, 3600 by default. It keeps authorization and keeps what it was granted, because `detach` is what the client asked for, and §11.4's listing by owner reaches it. Decision 18 records this.
- **Disabled users.** Each gate reads the user's invalidation level and `processing_restricted` through `pkg/binding`. Any of `soft_disable`, `hard_disable`, or `full_revoke` refuses every user-derived mint and every App mint for a repository under a VCS pool with `userAuthorization: required`, including the running sessions §11.4 otherwise leaves up on the lesser levels. A store error fails closed, as [TS-4](tenant-scoping-identity-design.md#user-lifecycle-ts-4) does.
- **Cross-environment.** Under [`tenant-scoping-identity-design.md` decision 14](tenant-scoping-identity-design.md#cross-environment-sharing-decision-14), a child in its parent's environment needs no new rule, and a child reached through a sharing path runs in the target environment, written T here, so `visible(bindings, T)` and `visible(VCS pools, T)` apply. It keeps its parent's user, so a VCS pool in T with `userAuthorization: required` checks that user. A user-sourced binding crosses only when the delegation lease names it. The link itself has no environment, so no team needs its own link.
- **Environment-less pods.** Under [`tenant-scoping-identity-design.md` decision 15](tenant-scoping-identity-design.md#decisions), concurrent and service-mode pods evaluate with no environment, and a session is refused there when its environment has anything of its own. The refused pod models above already refuse those pods for the repositories and bindings of this design.

### Service identities

An automated session has no person to check. A VCS pool with `userAuthorization: required` lists service identities in its `serviceIdentities` field, each with a `subject`, its own repository allowlist, and its own `allowWrite`. A session uses that path only when all of these hold:

- The caller's `caller_type` is `service`, which [§13.3](../spec/13_security-model.md#133-credential-flow) makes impossible to elevate on exchange, and which the session row now records, so both gates read it through `pkg/binding`.
- The bearer's `sub` is listed on the pool.
- The session is a root session under that subject. A human's delegated child stays under the human, and its row copies the human's `caller_type`.

Who may hold one: a system principal authenticating through the client credentials path of §10.2, whose credential no person handles as a routine. A `tenant-admin` writes the list through the single write path for tenant opt-ins, which the egress design's proposal [EG-9](egress-and-git-design.md#proposal-split) (egress policy objects) adds, each write emits an audit event, and a `security-admin` can restrict or forbid the list through a new `userAuthorization.serviceIdentities` guardrail. A person cannot move onto this path by presenting a token with a chosen `caller_type`, because the subject must be listed. Lenny cannot detect a person holding a listed client secret, which "Residual risks" states.

A listed service session takes the App binding on that host, under "The attach rule", so its pull requests have the App as author. Any other root session whose `caller_type` is not `human` is refused for repositories under a VCS pool with `userAuthorization: required`, with no check to run. The flavour's preset applies to that binding too, because `pull_requests: write` on an App token can approve any human's pull request.

### Attribution

`credential.mint` carries "a session identifier and no user identifier", and §12.8 treats it accordingly: `DeleteByUser` deletes nothing from it. This design keeps that.

- `credential.mint` gains `tokenSource` and `linkRef`. `tokenSource` is one of `app`, `userRegistered`, `userOauth2`, `userChild`, and `userCheck`, as [`user-credential-delivery-design.md`](user-credential-delivery-design.md) defines. It carries no login, email, or `user_id`.
- `linkRef` is a pseudonym in the same class as the session identifier. Its join lives in `user_links`, which erasure deletes, so a stored record stops resolving to a person, and the erasure statement in [`egress-and-git-design.md`, "Evidence streams"](egress-and-git-design.md#evidence-streams) stays true once it says so. The class statement needs that one sentence when UG-2 lands.
- The token hash field stays. For a child token, the hash also joins to the provider's audit log, which GitHub describes as listing the user with "`programmatic_access_type`" of "GitHub App user-to-server token" **[documented]**. That join lies with the customer's provider log rather than a Lenny store.
- The provider login appears only in `user_links` and in §11.7 audit events, whose treatment §12.8 already defines through step 13 and step 14.
- A tenant's security team asks "who" by joining `sessionId` to the session row while it exists.
- A request rule refusal goes to `egress.local` with the decision identifier and the operation name, with no variables or body. Repeated refusals on one session raise a `user_credential_rule_refusals` alert, because an agent trying to approve is a prompt injection signal. These records are `self_reported`, as the stream's table says.

### Failure behaviour

| Failure | Behaviour |
| :-- | :-- |
| No link, for a repository under a VCS pool with `userAuthorization: required` | Session create refused with `USER_LINK_REQUIRED`, before any pod claim when the plan binds at create, as "When the check runs" states. Nothing falls through to the App. |
| Provider `unavailable` at bind | `USER_AUTHORIZATION_UNAVAILABLE`, retryable. |
| Token Service down | As the egress design states, and bind also fails with the retryable error. |
| Postgres down | Bind fails. Running reads continue until `verdictMaxAgeSeconds`, the existing §12 behaviour applies, and no new mint happens. |
| Refresh token rejected | `user_act_required`. |
| Rate limited at the provider | `unavailable`. The sweep backs off per link. A metric reports it. |

## UG-3: Pull request authoring

### The binding

Revision 3 had a third `secret` kind, `userLink`, with a filter a tenant could not clear. **`userLink` is now the `oauth2Link` source of a `userCredential` binding**, and setting `prAuthoring: user` on a VCS pool writes this record:

```yaml
userCredentialTypes:
  - {name: github-user, source: oauth2Link, linkProvider: github-acme}
---
secret: { kind: userCredential, type: github-user }
hosts: [api.github.com]
delivery: inject
scope: poolAttach
# no attach: the kind fixes it, in the token trust record
# no preset key: resolved on the pod credential API from the flavour of the pool's provider and host
```

- A VCS pool's `prAuthoring: user` requires `userAuthorization: required` on the same pool, and an `oauth2Link` type whose named link provider is on the pool's host with a `delivery` ceiling of `inject` or above. That link provider can differ from the one `userAuthorization.linkProvider` names for the check. A write without them is refused, so the check and `USER_LINK_REQUIRED` always hold on such a pool. Decision 44 records the cost. A write of `userAuthorization: required` whose `userAuthorization.linkProvider` is absent or names no VCS-kind provider on the pool's host is refused too.
- `hosts` is the API host from the kind's request layout: `api.github.com` on GitHub, `api.<name>.ghe.com` on `ghe.com`, and the host itself on GHES and GitLab. On GHES, the same hostname serves git and the web, and a smart HTTP path there takes [the egress design](egress-and-git-design.md)'s git path rather than this binding.
- The link provider names its kind's shipped template, which on GitHub is `github-scoped`, whose `permissions` is `pull_requests: write` and `contents: read` by default. That is data a tenant can widen, which the owner settled under [`github-credential-addon-design.md` decision 5](github-credential-addon-design.md#decisions).
- The preset resolves on the pod credential API from the flavour of the VCS pool's provider and host, and a tenant can change it. A `security-admin` can require it through `requestRules.requiredPresets`. See [`user-credential-delivery-design.md`, "Who can change a preset"](user-credential-delivery-design.md#who-can-change-a-preset).
- `netagent` holds no provider code for this. The add-on's code runs in the Token Service.

### The attach rule

This is the one place the attach is defined, and the [egress design](egress-and-git-design.md)'s `scope`, [`user-credential-delivery-design.md`](user-credential-delivery-design.md), and [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) point here.

- **The trigger.** A human root session that declares at least one writable repository under a VCS pool with `prAuthoring: user` receives that pool's binding, with `scope: poolAttach`, without naming it. The binding's own `required: true` field makes a missing link give `USER_LINK_REQUIRED`. A session declaring only read-only repositories there does not attach.
- **It replaces a same-host App binding, on that host alone, for that session.** A tenant-wide App binding on `api.github.com` can exist beside it, because service sessions need one. It keeps its other hosts, such as `uploads.github.com`. Replacement holds whatever the user binding's state, so a user binding with `suspendedBy` set restores no App binding (UP-6). It also holds when the session's other entries sit under a VCS pool with `prAuthoring: off` on that host. A session naming a second binding with `delivery: inject` on that host explicitly is refused at bind under the one-binding rule.
- **A listed service session** takes the App binding on that host, and never the pool's. Any other caller whose `caller_type` is not `human` is refused on such a pool.
- **A delegated child** receives the pool's binding only when the lease names it, as "Identity cases" states.

### Other tokens on these hosts

Under [`user-credential-delivery-design.md`](user-credential-delivery-design.md), a tenant can also define a type of its own on these hosts, and rules evaluate that type's hosts. Such a type does not bypass the UG-2 check on App tokens, and it does sit beside it. **What follows is by host, for every session of the tenant.** On the API host of a VCS pool with `userAuthorization: required`, the only permitted bindings with `delivery: inject` are an App-sourced binding, which the check governs, and the pool's link. A stored secret has no check, so it is refused at write, setting `userAuthorization: required` on a VCS pool suspends one that exists, and `userAuthorization.require` makes that hold whatever a tenant admin writes. **The owner also adopted this rule: a binding with `delivery: file` whose type hosts include a covered host is refused unless every VCS pool covering that host sets `allowRuntimeHeldCredentials`, whatever the pool's `userAuthorization` value,** because on a VCS pool with `userAuthorization: off` the push would otherwise silently act as the App. A VCS pool added later without `allowRuntimeHeldCredentials`, or that setting cleared later, suspends the binding, and a running session that named it gets a 401 on git rather than the App. On a host covered by a git route alone, the binding is admitted, the held token is dropped on the git path, and a push is refused. A platform warm-up asset covers its host only until bind, except on a pool whose `egressProfile` is `restricted` with `gitAccess`, so it does not bring this rule to a tenant with no pool there. A plan naming a suspended binding with `delivery: file` is rejected at create with `422 USER_CREDENTIAL_BINDING_SUSPENDED`. This layer's host policy states these, and `allowRuntimeHeldCredentials` builds on [UC-5](user-credential-delivery-design.md#uc-5-delivery-file) of [`user-credential-delivery-design.md`](user-credential-delivery-design.md).

| On the hosts of a VCS pool with `userAuthorization: required` | Result |
| :-- | :-- |
| A stored secret binding, under either delivery | Refused at write. |
| Any other `userCredential` type with `delivery: inject` on the API host, whether its source is `registered` or a link provider with `kind: oauth2` | Refused at write. |
| A binding with `delivery: file` whose type hosts include these hosts, where some VCS pool covering them lacks `allowRuntimeHeldCredentials` | Refused at write. |
| That binding, where every VCS pool covering the host sets `allowRuntimeHeldCredentials: true`, this pool's `prAuthoring` is not `user`, and the session has no App binding on the API host | **UP-4 still holds for App tokens: no App token without a verdict. UP-9, UR-2 for what the agent's token reaches, UR-4, and UR-6 do not hold for that session,** since its token leaves every Lenny control. Posture reports the pool, and the session is marked `agentHeldCredential`, the generic design's mark for a session that holds a token under `delivery: file`. **Other sessions on the pool are unchanged:** the runtime-held attach, under which `netagent` attaches the value the runtime holds to its git requests ("Git and hosts" below), applies to a session naming that binding, whatever its release or suspension state, and with no current value the host answers 401. The pin exception, under which such a session is excepted from the pool's pin on that host ("Other hosts", **Pinned hosts**), needs a release, and suspension ends it. Without `allowRuntimeHeldCredentials`, a held token is dropped for git and the mint is used **[measured]**. With it, `netagent` attaches the current value it holds on every git request and no mint is used to `netagent` for that host, under "Git and hosts", and with no current value the host answers 401 **[measured]**. |
| That binding on a VCS pool with `prAuthoring: user`, or beside an App binding on the API host | Refused at bind by the one-binding rule. |
| A binding with `delivery: file` on a type whose source is this pool's link provider | Refused unless `allowRuntimeHeldCredentials` applies as above and the link provider's `delivery` ceiling is `file`. |

A tenant that needs `userAuthorization: required` to mean what it says leaves `allowRuntimeHeldCredentials` off and takes the [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) guardrail to forbid it.

A `registered` type can lie, by declaring an innocent host and holding a GitHub token. That passes the checks above, and the enforcing control is the pin on the host the token works on, which waits on the egress design's exfiltration controls, [EG-12](egress-and-git-design.md#proposal-split).

**A smuggled token.** A client can also place a token through an `inlineFile` workspace plan source, whose content the client supplies, through the plan's `env` variables under a name the deployer's blocklist of denied variable names does not match, or in a message **[source]**, and input checks cannot recognize one. For repositories under a VCS pool with `userAuthorization: required`, this bypasses the check on any unpinned host, and revision 3 pinned nothing. **The rule, which the owner settled under decision 40: when any VCS pool with `userAuthorization: required` covers a host, that host and the kind's API hosts are `credentialPinned` for every session of the tenant, once [EG-12](egress-and-git-design.md#proposal-split) has landed.** A token reaching that host reaches the pool's repositories whichever pool the session is on, so the pin source reads every VCS pool of the tenant, unfiltered by environment and whether or not a guardrail has suspended it. UG-2 carries the wiring, and [EG-12](egress-and-git-design.md#proposal-split)'s pin takes a VCS pool as a source. A pinned host strips other credentials and refuses a host with no binding. A VCS pool's `pinHosts: false` opts out, and posture reports it. A session holding a binding with `delivery: file` that `allowRuntimeHeldCredentials` applies to is excepted, and no other session is. A pool with `egressProfile: internet` gives its sessions raw sockets that bypass `netagent`, so they stay outside the pin, and posture reports that too. Until [EG-12](egress-and-git-design.md#proposal-split) lands, under `pinHosts: false`, or on a pool with `egressProfile: internet`, this is a stated residual. Under git path T, a held token on a covered host is dropped for git and the mint is used **[measured]**, so the residual there is the API host. Decision 40 records the costs.

### Git and hosts

The owner settled the git path as git path T, under decision 36, and [`egress-and-git-design.md`, "Git on covered hosts"](egress-and-git-design.md#git-on-covered-hosts-netagent) states it. **Only the T column below is normative. L, the prior loopback rewrite, and P, an `http://` rewrite with a loopback proxy, stay as the evidence behind that decision.** Rows are **[measured]** with an agent that owns its config and environment, on a stub running stock git, unless marked. **The stub proves forwarding only.** "Git remotes and client tools" below states L and P in full. The helper and header rows sent the held token through a git credential helper or an `http.extraHeader`, and a URL token is a token written into the remote URL.

| A token the runtime holds, without `allowRuntimeHeldCredentials` | T, normative | L, evidence | P, evidence |
| :-- | :-- | :-- | :-- |
| `git push` on a covered host by helper, header, or `gh auth git-credential` | Dropped, the mint is used, and patterns hold | Replaced by the mint in the header rows. The helper rows did not run on L. | Same as T on the rewrite path |
| The same with a URL token | Replaced by the mint | The request fails with no token at the origin | Replaced by the mint |
| Rewrite or `http.proxy` config removed, covered host | Enforced, as the rows above | Refused with an API binding. With none, an opaque tunnel with the held token reaching the origin. | Refused with an API binding. With none, **an opaque tunnel: the token reached the host, and a push to `main` was accepted** |
| A host no pool, route, or binding covers | **The token reaches the host in an opaque tunnel.** The run's push ended non-fast-forward, so a push completing is inferred from P. | Dropped on the rewrite path | Dropped on the rewrite path |
| `gh` on the API host, no binding | The token passes with no rule, and `pr merge` reaches the stub | Did not run. `gh` stops at its remote check. | As T |

**On a covered host without `allowRuntimeHeldCredentials`, a held token does not reach the host for git:** the push acts as the minted identity. Rules apply where a binding names them, so a token under `delivery: file` on an API host has no rule. The prototype's filter-only binding, which applied request rules to a token the runtime sent without attaching one of its own, refused `pr merge` for such a token, and this design offers no such binding, because the one-binding rule keeps `delivery: file` and `delivery: inject` bindings off one host.

**`allowRuntimeHeldCredentials`.** This layer owns the VCS pool field, the refusal below, and the runtime-held attach here, and they build on [UC-5](user-credential-delivery-design.md#uc-5-delivery-file) of [`user-credential-delivery-design.md`](user-credential-delivery-design.md). A session qualifies on a covered host when it names a binding with `delivery: file` that `allowRuntimeHeldCredentials` applies to, whose type hosts include that host, whatever the release or suspension state, and the gateway records those hosts on the claim at bind, beside `retireOnRelease`, so a type deleted later does not change them. A binding the owner's rule later suspends, or a tenant deletes, still counts for the running session that named it, and a plan naming a suspended binding with `delivery: file` is rejected at create with `422 USER_CREDENTIAL_BINDING_SUSPENDED`. **A qualifying session gets the current value `netagent` holds for that host on every git request, whatever the client sent, in the form the mint uses on that provider, and no mint is used for that host. This is the runtime-held attach. `netagent` asks the pod credential API before it concludes there is none **[design]**, since the prototype run recorded under V4 held its state in a file and made 0 Token Service requests. With no current value, nothing attaches, and the host answers 401, so no state silently pushes as the App.** A Token Service outage gives the stated 503 rather than a 401 **[design]**. On the pod credential API, the release names a `handling` value, so an image without this rule refuses the credential. The Token Service refuses a mint to `netagent` for that session and host. The mint refusal does not apply to a read mint in the provisional state, which grants no write token, so a rebuild's clone reads as the App, as a mirror route does. It does not refuse the mirror's own pod credential API request, so a read on a mirror route is served from the mirror's store under its App mint, with `netagent`'s service account token at the mirror, and the session's one identity holds for what `netagent` sends to the host. The agent gains nothing, since it already holds the value. A delegated child with no lease entry names no binding, so it does not qualify, and its push follows "Push identity" below. Path enforcement, `allowedRefPatterns`, the push option refusal, and `git.operation` stay. Setting `allowRuntimeHeldCredentials` on a VCS pool changes no other session. `allowRuntimeHeldCredentials` defaults to `false`, and a `security-admin` can forbid it.

**Evidence for the runtime-held attach [measured],** on the stub, which proves forwarding only. With all conditions holding, the origin saw the current value on clone and on push, on GHES, whatever the client sent: nothing, a helper, a URL token, a header, or a different token. A client with no helper cloned. A rotated value took effect with no action by the runtime, and a different token appeared 0 times at the origin. A push to `main`, a mixed push, a deletion, a push option, a push outside the set, a push to a read-only repository, and a read outside the set were refused, on GHES and github.com. A Git Large File Storage (LFS) push and smudge through a separate store host passed with the store seeing no credential, and an out-of-scope redirect got 403. A reflected value was masked. In a Token Service stand-in, a qualifying clone and push made 0 requests, a direct mint got 403, an image without the rule got 403 so git failed, and an image older than the `handling` value refused before asking. On a mirror route the fetch carried `MIRROR-SA`, the stand-in for the mirror's service account credential, and the push advert, the ref advertisement git reads before a push, carried the current value. **A suspended binding, a cleared `allowRuntimeHeldCredentials`, no release, and two VCS pools with the setting on one each gave no credential and a 401, on all 5 client forms on clone and on push, with 0 Token Service requests and 0 pushes succeeding.** A session that released but named nothing, the delegated child's case, took the mint. A push to `main` in those states stops at the advert's 401, so the ref check is not reached. On a mirror route when suspended, a read carried `MIRROR-SA`, and a push got 401 at the host. An earlier run had these states fall to the mint and push as the App.

**The owner's rule on a binding with `delivery: file` beside a VCS pool.** Such a binding whose type hosts include a host a tenant VCS pool or git route covers is refused unless every VCS pool covering that host, read unfiltered by environment, sets `allowRuntimeHeldCredentials`, whatever the pool's `userAuthorization` value. This closes the case where a VCS pool with `userAuthorization: off` accepts the binding and the push silently acts as the App.

| Case | Outcome |
| :-- | :-- |
| A VCS pool without `allowRuntimeHeldCredentials` is added later on that host | The binding is suspended at the pool write, naming the pool. A running session that named it gets a 401 on git rather than the App. |
| `allowRuntimeHeldCredentials` is cleared later | The same. A write removing the pool or setting it again clears the mark it set, as [`security-oversight-design.md`](security-oversight-design.md) states for guardrails. A plan naming a suspended binding with `delivery: file` is rejected at create with `422 USER_CREDENTIAL_BINDING_SUSPENDED`. |
| A host covered by a git route alone, with no pool | The binding is admitted, because no VCS pool can set `allowRuntimeHeldCredentials`. The session does not qualify, the held token is dropped on the git path, and a push is refused, since no pool covers it. |
| A platform warm-up asset on the host | No effect. An asset's host is covered for a warm pod until bind, and after bind only the tenant's pools and routes count, except on a pool whose `egressProfile` is `restricted` with `gitAccess`, where every host of the computed `gitAccess` host set stays covered for that pool's pods **[design]**, because a host held as data ran and the computed host set has not. This rule reads tenant pools and routes alone. |

### Other hosts

- **Stored token on a covered git host.** [The egress design](egress-and-git-design.md) refuses a stored token binding, under either delivery, on a git host a VCS pool covers, because the token would reach that host's REST write routes with no path enforcement. A `registered` type under `delivery: inject` is a stored token, so the refusal applies, evaluated on type hosts. The one exception is a `userCredential` binding under `delivery: file` when `allowRuntimeHeldCredentials` applies.
- **A type a link uses.** A binding with `delivery: file` is refused on a type whose link provider a VCS pool's `prAuthoring: user` binding or its UG-2 check uses, unless `allowRuntimeHeldCredentials` applies and the record's delivery ceiling is `file`.
- **Pinned hosts.** `credentialPinned` strips other credentials, so a token under `delivery: file` sent to a pinned host breaks: `netagent` removes it. A binding with `delivery: file` whose type hosts include a pinned host is refused at write, and a pin added later suspends that binding, naming the pin, rather than leaving one that silently fails. A session that `allowRuntimeHeldCredentials` applies to is excepted from the pin of a VCS pool with `userAuthorization: required`, on that host alone, once it has received a release, and suspension ends the exception.
- **A host with a binding under `delivery: inject` and no VCS pool.** `netagent` classifies smart HTTP on every terminated host, so git to it carries no credential, the secret stays off the git request, and a push is refused **[measured]**. The host of a binding with `delivery: file` is not terminated for that reason, so with no VCS pool or route it stays opaque.
- **A host with a binding under `delivery: inject`.** `netagent` overwrites the `attach` header, and its credential list strips others, so a runtime token sent there is replaced. The one-binding rule refuses this at bind.
- **Hosts of a VCS pool with `userAuthorization: required`.** See "Other tokens on these hosts".

### Request rules on VCS hosts

[`user-credential-delivery-design.md`](user-credential-delivery-design.md) defines request rules, presets, and who can change them. This section states what they mean here.

**The model.** Each VCS add-on ships its presets as data, with paths per API flavour and request forms: `github-deny` in [`github-credential-addon-design.md`](github-credential-addon-design.md#preset-body) and `gitlab-deny` in [`gitlab-credential-addon-design.md`](gitlab-credential-addon-design.md#preset-body). The flavour of the VCS pool's provider and host selects the preset when the pod credential API answers. A binding that adds rules keeps the preset, and only the binding field `preset: none` clears it. The `github-deny` body is the extended preset, which the owner settled under [`github-credential-addon-design.md` decision 8](github-credential-addon-design.md#decisions). A request matching no rule passes, so a new `gh` release keeps working, and `gh pr comment`, `edit`, `ready`, `close`, `reopen`, and `edit --base` all pass the rules by default. What the preset refuses stays refused unless a tenant changes it.

**Selecting the flavour.** The flavour derives from a VCS pool's provider and host, with no stored field, and the pod credential API reads it. Each VCS add-on registers the request layout for its kinds: the flavour, the API host, `apiBase`, `restPrefix`, the GraphQL paths, the uploads path, and `attach`. [`github-credential-addon-design.md`, "Request layout"](github-credential-addon-design.md#request-layout) and [`gitlab-credential-addon-design.md`, "Request layout"](gitlab-credential-addon-design.md#request-layout) list them.

A tenant admin has no field to set, so she cannot satisfy the `requestRules.requiredPresets` guardrail with rules that match nothing. Resolution reads every VCS pool of the tenant, unfiltered by environment and whether or not a guardrail has suspended it, and a write refuses two VCS pools on one host whose flavours differ. `/api/graphql` is a listed path for both GHES and GitLab. On a host no pool covers, each preset therefore carries the other's denied mutation names. REST there fails closed on the prefix: a path under `/graphql*` or `/api/*` that is not the resolved flavour's own GraphQL, uploads, or REST prefix is refused **[measured]**, with 13 of the 36 names tried. A link provider's `kind` must agree with the host's flavour at write.

**On by default, resolved on the pod credential API.** A binding whose `requestRules` has no `preset` key, whatever its source, on the API host of a VCS pool, from the add-on's request layout, resolves on the pod credential API to that flavour's preset. It covers an App-sourced binding, a project token, and a `registered` type, because an App token with `pull_requests: write` can also approve. `preset: none` clears it explicitly. On a host no VCS pool covers, no preset applies unless the binding names one, and then its family, `github-deny` or `gitlab-deny`, and the host give the flavour: `github-deny` is `github` on `api.github.com` and `api.<name>.ghe.com`, and `github-enterprise` elsewhere, and `gitlab-deny` is `gitlab`. No field can select another. Resolution on the pod credential API means a VCS pool added later takes effect at the next `cacheUntil`.

**What remains unbound.** Targets (which pull request or object an operation acts on), reads (`repository`, `node`, and `search` read whatever the token reads), free text, and git, which sits under the [egress design](egress-and-git-design.md)'s rules, "Push identity", and the push option refusal.

### Keeping presets current

An unlisted operation passes under `requestRules.default: allow`, so a new `gh` release breaks nothing. The risk moves to the server side and to presets.

- A CI job diffs GitHub's public GraphQL schema and REST OpenAPI description, and GitLab's quick action page and fine-grained tables, on a schedule. A new name in the review, merge, queue, content, ref, deployment, or policy families raises a preset change for review. The preset names REST twins and policy mutations that exist today, which the accepted loss of UR-6, a new operation passing until the preset names it, does not cover. The rate is about one a year on GitHub, in one reading **[source]**.
- Rules reach `netagent` resolved on the pod credential API, so a preset update reaches a pod at its next `cacheUntil` with no image change.
- The window in which a new mutation with that power passes is the schedule, the review, and `cacheUntil`. "Residual risks" states it.

### Comment-triggered actions

An input something else treats as a command carries Alice's identity. Each add-on states its provider's automation: [`github-credential-addon-design.md`](github-credential-addon-design.md#comment-triggered-actions-on-github) covers comment bots and label triggers on GitHub, and [`gitlab-credential-addon-design.md`](gitlab-credential-addon-design.md#quick-actions) covers GitLab's quick actions, commands such as `/merge` that GitLab runs from a comment or description. The body of `createPullRequest` and `updatePullRequest` carries the same risk as a comment, so refusing comments never closed this path, and revision 3's capture showed that it cost `gh pr comment` **[captured]**.

**Under the preset model.** Revision 3's required `repositoryAutomation` statement is retired, along with its label-key logic, because those were allowlist input checks. A tenant with such automation adds rules:

- `graphql.denyMutations: [addComment, addLabelsToLabelable, ...]` refuses an operation outright.
- `bodyPatterns` with the bot's grammar refuses per line on comments, PR bodies, and titles, including every string and block string literal in a GraphQL document, such as `createNote(input: {body: "ok\n/merge"})`, a variable default, or a directive argument **[measured]**. A pattern is a deny list over free text, which the capture found weak: refusing every `/word` line also refused `/usr/bin/env` **[captured]**. It is offered because the tenant's list targets a grammar it controls.
- The stronger control sits with the bot: a comment made through a GitHub App user token carries a non-null `performed_via_github_app` in the REST schema **[documented]**, and the UI shows the App's badge, so a bot or workflow can refuse commands from it. The tenant page recommends that first, and whether bots see that field in webhook payloads is [S15](github-credential-addon-design.md#validation-owed).

The owner settled decision 35: `repositoryAutomation` is retired, and the setup flow and posture prompt a tenant about bots.

**CI (continuous integration).** A pushed branch triggers the repository's existing workflows. GitHub's page says secrets are withheld "when a workflow is triggered from a forked repository" **[documented]**, and it describes no such withholding for a branch in the same repository, which is where an agent pushes. The default token omits `workflows: write`, so an agent cannot edit a workflow file, and a workflow still runs the branch's build scripts. Whether an App token's push triggers these is not stated on that page ([S15](github-credential-addon-design.md#validation-owed)). GitLab behaves the same way for unprotected variables, and it has no permission like `workflows: write`, so an agent can also push a `.gitlab-ci.yml`, as the egress design states. A tenant limits this with required reviewers on environments and protected variables, and the repository setting that lets Actions approve pull requests stays off. This exists under the egress design's App-token pushes today, and "Residual risks" states it.

### Git remotes and client tools

**The owner settled the git path as T (decision 36) on VCS-covered hosts: git's `http.proxy` points at `netagent`, which terminates TLS for VCS hosts, and `http.sslCAInfo` names a bundle of the per-pod CA and the public roots. [`egress-and-git-design.md`, "Git on covered hosts"](egress-and-git-design.md#git-on-covered-hosts-netagent), states it.** Git keeps its remote URLs, so `gh` and `glab` read the upstream host. UR-5 is met on a stub, and live hosts are owed.

**The problem T closes.** The prior config rewrote every remote with `insteadOf`, and `git remote -v` printed the rewritten URL. `gh` and `glab` read their host from those remotes, so they saw `127.0.0.1:3129` and stopped.

**Evidence [measured].** Only the T column is the design. L, the prior path, and P stay as the evidence for the decision. The origin ran stock `git http-backend` behind a recording stub, so git behaviour is that of stock git, and **the stub proves forwarding only**. Clients had a route to `netagent` alone.

| | T: `http.proxy` at `netagent`, VCS hosts terminated, and `http.sslCAInfo` at the per-pod CA and public roots | L: `insteadOf` to the loopback endpoint, as a control | P: `https://` to `http://`, with a loopback `http.proxy` |
| :-- | :-- | :-- | :-- |
| Git on OpenSSL and GnuTLS: clone, fetch, shallow, partial, push, 64 MiB pushes, LFS on a separate host, and submodules | Passes. With the per-pod CA alone, LFS and the unbound-host submodule fail. | Passes | Passes |
| `gh pr create`, `view`, `list`, `status`, `checks`, `checkout`, `repo view`, `clone`, and `set-default`, and `glab mr create`, `view`, and `list`, on Alpine and Debian, GHES and github.com | Exit 0, with the placeholder replaced at the stub | Every `gh` command reading remotes stops with "none of the git remotes". `glab mr create` and `list` fail, and `mr view` and `repo view` exit 0. | Exit 0. `git remote -v` prints `http://host/o/r`. `gh` and `glab` store and print `https://`. |
| Other clients | go-git, JGit, and libgit2 with a proxy clone. libgit2 hangs on a CA constraint mismatch, where git, `gh`, and go-git return an error. JGit does not enforce constraints. | go-git and JGit refused, libgit2 clones by default | go-git and JGit refused, libgit2 fails on DNS |
| Git in `golang:1.26`, `node:20`, `maven`, and `gradle` images | Clones | Clones | Clones |
| `http.proxy` unset | No route | Still loopback, refused | No route |
| Config removed, VCS host with an API binding | Enforced | Refused | Refused |
| Config removed, VCS host with no API binding | Enforced | An opaque tunnel, with an agent's token reaching the origin | **Opaque: an agent's token reached the origin, and a push to `main` was accepted** |
| Outside the set, a read-only push, a ref outside patterns, deletion, a push cert, gzip, and path spellings | Refused | Refused | Refused |
| A held token on a covered host, for git | **Dropped, the mint is used, and patterns hold** | Replaced by the mint in the evasion rows | Same as T, on the rewrite path |
| A held token on an uncovered host | **Reaches the host in an opaque tunnel.** The run's push ended non-fast-forward, so a push completing is inferred from P. | Dropped on the rewrite path, in the evasion rows | Dropped on the rewrite path |
| After a 401 on a covered host | git asks twice, and the origin sees only the mint | The same | The same |
| `netagent` CPU per measured block of the benchmark | 689 to 714 ms | 520 to 541 ms | 515 to 517 ms |
| 10 `ls-remote` | 115 to 116 ms | 87 to 101 ms | 87 to 90 ms |
| `memory.peak` | 28.7 to 29.1 MB | 27.9 to 30.4 MB | 27.9 MB |

The egress design states the remote forms, CA coverage, and the loss on uncovered hosts. **A VCS pool's `allowRuntimeHeldCredentials`, which UG-3 owns under "Git and hosts", building on [UC-5](user-credential-delivery-design.md#uc-5-delivery-file), makes `netagent` attach the current value on every git request with no mint [measured].** With no current value the host answers 401 **[measured]**, on a suspended, flag-cleared, or unreleased session, with 0 pushes succeeding.

**Alternatives that keep the loopback endpoint** did not work unchanged: `GH_REPO`, `gh repo set-default`, a wrapper with no git config, `pushInsteadOf` alone, and a loopback fetch URL with the upstream on the push line **[measured]**. A hostname with a port passes `gh`'s parser, and git against that form did not run. A hybrid of T on covered hosts and the prior path elsewhere did not run, and the design has none.

**Client requirements.** `gh pr create` needs git in the image even with `-R`, and 6 of the 10 images tried have none. `gh release upload` goes to `uploads.github.com`, which needs a binding, and `gh browse` sends `HEAD`. macOS, Windows, and amd64 did not run. A wrapper or patched tool stays rejected, because it is a Lenny-specific binary, against the egress design's requirement that "Lenny adds no platform tool" ([`egress-and-git-design.md`, "Requirements"](egress-and-git-design.md#requirements)).

Path enforcement, `allowedRefPatterns`, and the push option refusal hold on covered hosts.

### Git push options

`git push -o` reaches GitLab inside `git-receive-pack`, past everything else here. Under option D of [`gitlab-credential-addon-design.md`](gitlab-credential-addon-design.md#options), git uses the project token and `netagent` reads only ref commands, so `git push -o merge_request.create -o merge_request.auto_merge -o merge_request.target=main` requests a bot-authored merge request with auto-merge, and Erin can approve a bot's merge request. `merge_request.description` also carries text past `netagent`'s ref checks. GitLab's page lists these along with `ci.skip`, `ci.no_pipeline`, `ci.variable`, `ci.input`, `integrations.skip_ci`, `secret_push_protection.skip_all`, and `security_policy.bypass_reason` **[documented]**.

The rule, on every covered host: **`netagent` refuses a `git-receive-pack` request that uses the `push-options` capability, and strips that capability from the ref advertisement, so git reports the failure before sending a pack.** The VCS pool's `allowedPushOptions` list can admit a specific option, and it is empty by default. An agent using `-o ci.skip` is refused. The capture did not examine `git push`, and the worked `gh pr create` and `glab mr create` flows need no push option. Because a push option's section sits between the ref commands and the pack, `netagent` needs bounded parsing past the commands it reads today, and that is a requested change on the egress design's pod git path, [EG-3](egress-and-git-design.md#proposal-split). **[design]**

### Push identity

A VCS pool with `prAuthoring: user` sets `pushIdentity`, which decides whose token carries a human session's git pushes. Under `user`, a push carries a token the kind's add-on derives from the requesting user's link, so the provider records the user as the pusher. Under `app`, pushes keep repository-limited App tokens, and a pool write with `prAuthoring: user` and `pushIdentity: app` is refused unless the tenant runs UG-4, which reasons about sessions rather than pushers. A listed service session has no user and keeps the App write token (UR-7). Lenny writes no branch rules for user pushes, by decision 10, so the session pushes wherever its user can push on the session's writable repositories, and the repository's own branch protection and rulesets apply to the agent as they apply to the user. A tenant can still set `allowedRefPatterns` on a VCS pool under the egress design, and none is written by default.

`user` is the default on GitHub, and [`github-credential-addon-design.md`, "Pushes as the user"](github-credential-addon-design.md#pushes-as-the-user), states how the GitHub add-on implements it and what GitHub's most-recent-push rule then gives. On GitLab, pushes use the project token until spike [S13](gitlab-credential-addon-design.md#validation-owed) shows a per-session token can push, and decision 47 asks how to bound App-identity pushes.

### Commit authorship

[The egress design](egress-and-git-design.md#push-limits-and-commit-identity) lets a VCS pool set the author and committer. On a VCS pool with `prAuthoring: user`, the gateway sets the author to the linked user: on GitHub `<id>+<login>@users.noreply.github.com` and on GitLab the link's verified primary email, read at link with `read_user`. The committer stays the pool's agent identity, and a `Lenny-Session` trailer is added by the commit template in the git configuration Lenny renders for the session. This serves GitLab's "Prevent approvals by users who add commits", which excludes "the Git author and Git committer" **[documented]**, and it makes commit history truthful about who requested the work.

These fields are text the agent can change with `git -c`, and `netagent` does not read pack contents, so they are advisory on GitHub and a soft signal on GitLab ([S12](gitlab-credential-addon-design.md#validation-owed) tests how GitLab matches them). Signing is out of scope, because the agent holds no user key.

### Delivery

The binding that a VCS pool's `prAuthoring: user` attaches uses `delivery: inject`, and its link provider's `delivery` ceiling is `inject` or `file`. A runtime that needs its token as a file uses `delivery: file` under [`user-credential-delivery-design.md`](user-credential-delivery-design.md), behind the `userCredentialDelivery` grant and a binding opt-in. Revision 3 dropped `delivery: file` from the first version here. The owner's direction now is that the mechanism exists, generically, because Lenny "can't possibly handle all injection scenarios". Decision 16 records the change. `gh` and `glab` accept a placeholder **[measured]** (rounds 3 and 10) **[captured]**, so this configuration does not need it.

A token in a file readable by the agent UID can leave the pod through any allowed host and stay valid for up to 8 hours, a child token's lifetime from its creation ([S1](github-credential-addon-design.md#validation-owed)), and unlike a JSON Web Token (JWT) under the egress design's `federation` delivery, which gives a runtime a short-lived identity token that it exchanges at a cloud provider for that provider's credentials, it has no trust condition binding it to an address.

**Why Coder's delivery does not fit as a default.** Coder exposes the token inside the workspace through `GIT_ASKPASS` and `coder external-auth access-token` **[documented]**. For a person at a workspace, the person is the principal, and handing them a token they could get anyway removes nothing. An autonomous agent reads untrusted content, so any process it starts can run that command and send the result to any allowed host, after which no rule applies and no later revocation reaches it. Lenny keeps Coder's linking experience and defaults to a different delivery: the placeholder stays in the pod and the token stays in `netagent`, and `gh` runs with `GH_TOKEN=lenny-injected` as the egress design validated.

### Repository rules

[`security-oversight-design.md` decision 25](security-oversight-design.md#decisions) and [SP11](security-oversight-design.md#security-properties), the property that a repository marked for approval receives no write token until its approval exists, apply as written:

- A child token from a platform-shipped template with `followsEntryState`, a template flag under which each mint names only the session's writable repositories whose entry `state` is `granted`, which is `github-scoped` today, is a binding mint from a provider in the egress design's `mint-scoped` class, so it names only entries in state `granted`, and a repository rule added during a session moves the entry and flushes the cache by binding change.
- A GitLab project token or any non-`mint-scoped` binding on a repository rule's API host is refused or suspended under [`security-oversight-design.md`](security-oversight-design.md#so-5b-repository-write-approvals)'s `approvals.repositoryWrites` setting. `gitlab-fine-grained`, the narrowing template the GitLab add-on ships, is narrower than those, and it does not set `followsEntryState` until [GL-2](gitlab-credential-addon-design.md#gl-2-user-authored-merge-requests) is settled, so it is treated as non-`mint-scoped` for now. PR authoring on GitLab is therefore absent on a host with a repository rule, which that decision's cost already names.
- Pushes follow the repository write approvals of [SO-5b](security-oversight-design.md#so-5b-repository-write-approvals) unchanged, and the git child, like the API child, names only entries in state `granted`.
- A child minted before a repository rule moved its entry is revoked and reminted for the new set, which [S5](github-credential-addon-design.md#validation-owed) makes safe. Until the revocation lands it stays valid at the host, and [SP11](security-oversight-design.md#security-properties)'s residual covers this.

## UG-4: Independent approval check

A required status check from the App fails when the only approver is the session's requesting user. This is offered on any VCS pool, for tenants that refuse user tokens in pods or that want every pusher excluded rather than the latest alone, and it is required for a VCS pool with `prAuthoring: user` and `pushIdentity: app`, where GitHub sees the App as the pusher. It needs no token in a pod.

**How it works.** Lenny's App reports a status `lenny/independent-approval` on each pull request head:

- A webhook receiver listens for `pull_request` and `pull_request_review` events and verifies the signature. It needs `statuses: write` or `checks: write`.
- For a pull request's commits, it collects the sessions that pushed them from `git.operation`, which records refs and new SHAs, and maps each session to a user and then to a provider identity through `user_links`. The opener comes from the pull request's author when that is a person. When the App authored it, GitHub does not say which token made the request, so the check uses the pushers alone. Earlier revisions read the opener from the head branch's namespace, which decision 10 removed.
- It passes when at least one approver's provider identifier is outside that set, and it fails otherwise. A commit it cannot attribute counts as having no person, which leaves the approver free.

It catches the case the native rule misses, including Alice's agent pushing to Bob's pull request, because it reasons about sessions rather than pushers.

**Limits.**

- A pull request still shows the App as author.
- "No user token" is true at run time, and the check needs a proven mapping from Lenny user to provider identity. A link runs with the link provider's `delivery` ceiling at `identifier`, which keeps the numeric identifier and login and discards the token, so an attacker cannot claim someone else's identity. A tenant that refuses even that gets a mapping it must supply itself, which then rests on the tenant's own directory.
- The tenant configures the check as required on each protected branch. Lenny can verify that with a read and report it in posture, and it cannot force it.
- It adds a public webhook ingress to the gateway and App permissions, and a Lenny outage leaves the status pending, which blocks merges. That is fail-closed, at the cost of a merge outage.
- `git.operation` from `netagent` is `self_reported`, so a compromised `netagent` can omit a record.
- It needs `git.operation` retention at least as long as a pull request stays open.
- An App-authored pull request whose opener pushed nothing to it does not count the opener. An agent that opens a pull request for commits only another user's session pushed leaves its own user free to approve.

On GitLab, external status checks are the corresponding feature, on the tiers GitLab offers it, and the same logic applies. [S12](gitlab-credential-addon-design.md#validation-owed) confirms tier and behaviour.

## Configuration by scope

| Setting | Scope | Who sets it | Default |
| :-- | :-- | :-- | :-- |
| `allowedPushOptions` | VCS pool | `tenant-admin` | Empty |
| `userAuthorization` on a VCS pool | Tenant | `tenant-admin`, and [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) can force it | `off` |
| `userAuthorization.linkProvider` on a VCS pool | Tenant | `tenant-admin` | Required when `userAuthorization` is `required`. It names the link provider whose link the check reads, which must be a VCS kind on the pool's host. |
| `userAuthorization.require` guardrail | Tenant security record | `security-admin` | Absent |
| `userAuthorization.serviceIdentities` guardrail | Tenant security record | `security-admin` | Absent |
| `serviceIdentities` on a VCS pool | Tenant | `tenant-admin` | Empty |
| `onAccessLost` | VCS pool | `tenant-admin` | `deny` |
| `prAuthoring` (`off`, `user`, or `bot`) | VCS pool | `tenant-admin` | `off`. `user` requires `userAuthorization: required` and an `oauth2Link` type whose link provider has a `delivery` ceiling of `inject` or `file`. On GitLab, `user` needs [GL-2](gitlab-credential-addon-design.md#gl-2-user-authored-merge-requests) and a host at 19.2 or later. `bot` selects option D of [`gitlab-credential-addon-design.md`](gitlab-credential-addon-design.md#options) on GitLab, and on GitHub it behaves as `off`. |
| `requestRules` on the binding | Binding | `tenant-admin`, and [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) can require a preset | Resolved on the pod credential API from the flavour of the VCS pool's provider and host, unless `preset: none` |
| `pinHosts` | VCS pool with `userAuthorization: required` | `tenant-admin` | `true`, once [EG-12](egress-and-git-design.md#proposal-split) lands |
| `allowRuntimeHeldCredentials` | VCS pool, on every pool covering the host, whatever its `userAuthorization` value | `tenant-admin`, and [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) can forbid it. UG-3 owns it, building on [UC-5](user-credential-delivery-design.md#uc-5-delivery-file). | `false` |
| `allowedRefPatterns` | VCS pool, under [the egress design](egress-and-git-design.md) | `tenant-admin` | None. Lenny writes no branch rules. |
| `pushIdentity` (`user` or `app`) | VCS pool, with `prAuthoring: user` | `tenant-admin` | `user` on GitHub, and `app` requires UG-4. On GitLab pushes use the project token until [S13](gitlab-credential-addon-design.md#validation-owed), which decision 47 treats. |
| `recheckIntervalSeconds` | Helm, with a VCS pool narrowing | Platform and `tenant-admin` | 300, with a floor of 60 |
| `verdictCacheSeconds` | Helm | Platform | 60 |
| `verdictMaxAgeSeconds` | Helm, with a VCS pool narrowing | Platform and `tenant-admin` | 900, with a ceiling of 3600 |

Every non-spec default is operator-tunable and documented as such. Link providers, request rule bounds, types, and the `userCredentialDelivery` grant are under [`user-credential-delivery-design.md`](user-credential-delivery-design.md), and each add-on states its kind block and its own settings. A VCS pool with `userAuthorization: off` and no `userCredential` binding behaves as the [egress design](egress-and-git-design.md) says, with no check, link, or extra call.

## Worked examples

### Alice's access revoked in the middle of a session

Carol removes Alice from `acme/web` at 10:00:00, while Alice's agent is working.

| Time | Event | Tag |
| :-- | :-- | :-- |
| 10:00:00 | GitHub refuses the child immediately on anything needing Alice's access. A `gh pr create` returns 404 from GitHub. | **[documented]** |
| 10:00:30 | With the VCS pool's `pushIdentity: user`, `git push` is refused by GitHub on the git child, which carries Alice's access, as the row above states for her user token. With `pushIdentity: app`, it still passes on a cached App token and a cached verdict, inside `netagent`'s `cacheUntil`. | **[design]**, and [S3c](github-credential-addon-design.md#validation-owed) owes a child's push after access is lost |
| by 10:05:30 | The sweep calls `GET /repos/acme/web`, which answers 404, and it writes `userRefused`. | **[design]** |
| The same moment | The binding change flushes `netagent`, closes tunnels, and drops decisions. `repository_access_lost` reaches the client. Lost push: by 10:06:00. | **[design]** |
| After | `git push` gets 403 and no mint. `git fetch` through the mirror is refused by the decision service. A resume is refused. | **[design]** |

The workspace still holds what was already cloned. When the VCS pool sets `onAccessLost: terminate`, the session ends at the same moment.

### A GitHub token beside `userAuthorization: required`

VCS pool `github.com/acme/` sets `userAuthorization: required` and `prAuthoring: off`. Carol adds type `gh-pat`, with `source: registered` and `hosts: [github.com, api.github.com]`, and a binding with `delivery: file`. The write is refused, because the pool lacks `allowRuntimeHeldCredentials`. Declaring `hosts: [docs.acme.com]` passes that check, since a `registered` type can lie, and the agent then holds the token anyway. The enforcing control on that path is the pin on `github.com`, which strips it. She sets `allowRuntimeHeldCredentials: true` on the pool instead, and posture reports that this design's UP-9, UR-2, UR-4, and UR-6 no longer hold for sessions naming this binding. Alice's agent runs `git push` with the token:

- Without `allowRuntimeHeldCredentials`, the mint replaces it on a covered host **[measured]**, under "Git and hosts".
- With `allowRuntimeHeldCredentials`, `netagent` attaches the current value it holds on every git request and keeps path enforcement and `git.operation`, with no mint for that host **[measured]**.
- App mints stay checked. A session that does not name the binding keeps the pin, and the mint replaces any token it sends.

[`github-credential-addon-design.md`, "Worked examples"](github-credential-addon-design.md#worked-examples) and [`gitlab-credential-addon-design.md`, "Worked examples"](gitlab-credential-addon-design.md#worked-examples) walk through linking, pull request authoring, and a prompt-injected agent on their providers.

## Dependencies

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| UG-1 (VCS link kinds) | [UC-2](user-credential-delivery-design.md#uc-2-the-oauth2link-source-and-account-linking), [UC-7](user-credential-delivery-design.md#uc-7-add-ons), [EG-1](egress-and-git-design.md#proposal-split), [EG-2](egress-and-git-design.md#proposal-split), and [EG-9](egress-and-git-design.md#proposal-split) | The link flow and the kind registry, VCS pool hosts, and the single write path. |
| UG-2 (authorization) | UG-1, [UC-3](user-credential-delivery-design.md#uc-3-the-usercredential-binding), [UC-7](user-credential-delivery-design.md#uc-7-add-ons)'s release check point, [EG-1](egress-and-git-design.md#proposal-split), [EG-2](egress-and-git-design.md#proposal-split), [EG-3](egress-and-git-design.md#proposal-split), and [EG-5](egress-and-git-design.md#proposal-split) for mirror rules. [TS-4](tenant-scoping-identity-design.md#user-lifecycle-ts-4) for the lifecycle hook, the user invalidation level the gates read. | The session input check and `retireOnRelease`, the mint path, `pkg/binding`, the pod credential API, writable entries, and the mirror. |
| UG-2's pin wiring | [EG-12](egress-and-git-design.md#proposal-split) | A VCS pool with `userAuthorization: required` as a source of pins. UG-2 does not wait for it, and the residual stands until then. |
| UG-3 (PR authoring) | UG-2, [UC-2](user-credential-delivery-design.md#uc-2-the-oauth2link-source-and-account-linking), [UC-3](user-credential-delivery-design.md#uc-3-the-usercredential-binding), [UC-4](user-credential-delivery-design.md#uc-4-request-rules-and-presets), and [UC-7](user-credential-delivery-design.md#uc-7-add-ons). [EG-3](egress-and-git-design.md#proposal-split) with git path T. | The binding, the template and preset mechanisms, the attach and host policy points, and `gh`'s remotes. Each add-on's authoring proposal, [GH-2](github-credential-addon-design.md#gh-2-pull-request-authoring-on-github) and [GL-2](gitlab-credential-addon-design.md#gl-2-user-authored-merge-requests), implements it for its provider. |
| UG-4 (approval check) | [UC-2](user-credential-delivery-design.md#uc-2-the-oauth2link-source-and-account-linking) with a link provider `delivery` ceiling of `identifier`, [EG-3](egress-and-git-design.md#proposal-split), and [EG-10](egress-and-git-design.md#proposal-split) | `git.operation` and `credential.mint`. Independent of UG-3. |

| Other design | Interaction |
| :-- | :-- |
| [`security-oversight-design.md`](security-oversight-design.md) | [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) gains the guardrail rows for `userAuthorization`, and [`user-credential-delivery-design.md`](user-credential-delivery-design.md) adds `requestRules.requiredPresets`, `tokenTrust.allowedRecords`, `userCredential.file` (the value of the `credentialDelivery.forbid` guardrail that forbids user credentials under `delivery: file`), and `allowRuntimeHeldCredentials`. [SO-4a](security-oversight-design.md#so-4a-mirror-to-the-tenant-chain)'s `impersonated_by` feeds "Identity cases". [SO-5b](security-oversight-design.md#so-5b-repository-write-approvals) and its [decision 25](security-oversight-design.md#decisions) apply unchanged, and it now states them for this design. |
| [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) | [TS-1](tenant-scoping-identity-design.md#ts-1-environment-scoping) scopes `link_providers`, types, and `userCredential` bindings. [TS-4](tenant-scoping-identity-design.md#user-lifecycle-ts-4)'s upsert, which creates an unknown user's row in the invalidated state when that user is invalidated, and its refusal apply, and §11.4 gains the revocation step. It now cross-references this. |
| [`tenant-outbound-connector-design.md`](tenant-outbound-connector-design.md) | [OR-3](tenant-outbound-connector-design.md#requirements) covers the Token Service's calls, and a VCS host behind an outbound connector follows [C6](tenant-outbound-connector-design.md#security-properties), [C8](tenant-outbound-connector-design.md#security-properties), [C9](tenant-outbound-connector-design.md#security-properties), and [C10](tenant-outbound-connector-design.md#security-properties). [`github-credential-addon-design.md`](github-credential-addon-design.md) states the GHES case. |
| [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md) | The `quarantined` binding state, which the forensics design sets on a pod under quarantine, stops child mints with no new work. [FE-5](forensics-evidence-preservation-design.md#fe-5-capture-runbook-and-abuse-workflow)'s "Rotate credentials" step gains removing a user's link, which revokes its grant, because revoking a child needs the token value and no route takes a hash. The forensics design's reconciler rule R-USER-CREDENTIAL-POD compares mints with the pod model. It now states these, with delivered tokens in evidence. |
| [`user-credential-delivery-design.md`](user-credential-delivery-design.md) | The mechanism this document configures. It owns the binding, the sources, the link flow, `delivery: file`, request rules, the preset format, and the add-on model. |
| [`github-credential-addon-design.md`](github-credential-addon-design.md) | The GitHub add-on, whose [GH-1](github-credential-addon-design.md#gh-1-the-github-and-github-enterprise-kinds) to [GH-3](github-credential-addon-design.md#gh-3-the-github-deny-preset) depend on UG-1 to UG-3. |
| [`gitlab-credential-addon-design.md`](gitlab-credential-addon-design.md) | The GitLab add-on, whose [GL-1](gitlab-credential-addon-design.md#gl-1-the-gitlab-kind) to [GL-3](gitlab-credential-addon-design.md#gl-3-the-gitlab-deny-preset) depend on UG-1 to UG-3. |

No proposal in the [egress design](egress-and-git-design.md) depends on one here.

## Specification sections affected

- [§4.3 Token Service](../spec/04_system-components.md#43-token-service): a third credential category, and "Pods never hold downstream OAuth tokens" gains a stated exception for a child token in `netagent` memory.
- [§12.8 Compliance Interfaces](../spec/12_storage-architecture.md#128-compliance-interfaces): the erasure statement names `linkRef`. [`user-credential-delivery-design.md`](user-credential-delivery-design.md) states the link store's deletion and classification.
- [§13.3 Credential Flow](../spec/13_security-model.md#133-credential-flow): "Pod never receives: client tokens, downstream OAuth tokens" gains the same exception, with `netagent` named. Session input refuses an impersonation bearer on affected sessions.
- [§4.2 Session Manager](../spec/04_system-components.md#42-session-manager): `caller_type` on the session row, under [`user-credential-delivery-design.md`](user-credential-delivery-design.md).
- [§14](../spec/14_workspace-plan-schema.md): `readableRepositories`. [§15.1](../spec/15_external-api-surface.md#151-rest-api): the link routes and new error codes. [§16.5](../spec/16_observability.md#165-alerting-rules-and-slos) and [§16.7](../spec/16_observability.md#167-section-25-audit-events): alerts and audit events.
- [§5.2](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes): the refused pod models. §7.1 and [§7.3 Retry and Resume](../spec/07_session-lifecycle.md#73-retry-and-resume): checks at bind and resume.
- [§9.3 OAuth/OIDC Flow](../spec/09_mcp-integration.md#oauthoidc-flow): the refusal of a connector on a link provider's host set when a VCS pool on the host sets `userAuthorization: required`.

[`user-credential-delivery-design.md`](user-credential-delivery-design.md) lists the sections the link flow and the binding change.

## Documentation

- A tenant admin page for `userAuthorization`, `prAuthoring`, and the refused pod models, stating the revocation bound and the outage rule, and how to add rules for its own bots.
- A runtime author section stating that pushes act as the session's user, the refusal format, and which `gh` and `glab` operations the presets refuse.
- A security page stating the residual risks below in the same words.
- Doc tests pin each error code to its page, and tier 11 keeps the preset page generated from the data.

## Tests

Every test carries a `// spec:` annotation once the specification sections land. Tiers follow the repository's model.

### Tier 0: static

A lint refuses a VCS add-on that registers a kind without a host field, a fixed endpoint set, and a request layout.

### Tier 1: unit

- `git-receive-pack` with each push option refused, and the capability stripped from the advertisement.
- Verdict classification for each status and header.

### Tier 2: component

The tests run the sweep job against a fake Token Service, including a stall and jitter, and the pod model validators against envtest where they read pool records.

### Tier 3: contract

- The pod credential API with `cacheUntil` and `handling`, including an old `netagent` stand-in refusing an unknown handling.
- A test that fails when a schema diff adds a name in the review, merge, queue, or ref families with no decision recorded.
- `gh` and `glab` commands run in a repository under the rendered git config, on each flavour, with unchanged remotes. This is the case the capture missed.
- `credential.mint` with the new fields.

### Tier 4: integration

- A connector write refused on a link provider's host set when a VCS pool on the host sets `userAuthorization: required`, and accepted otherwise.
- "The attach rule": a human session with a writable repository replacing an App binding on that host alone, a read-only session not attaching, a suspended user binding restoring nothing, a listed service session taking the App binding, an unlisted caller with `caller_type: agent` refused, and a child with and without a lease entry.
- A VCS pool write of `prAuthoring: user` refused with `userAuthorization: off`, with no `oauth2Link` type on the pool's host, with that type's link provider at a `delivery` ceiling of `none`, or with `pushIdentity: app` unless UG-4 is on.
- A listed service session on a VCS pool with `pushIdentity: user` pushing with the App write token.
- A session with a repository under a VCS pool with `userAuthorization: required`, under each row of "Other tokens on these hosts", and that pool's hosts pinned.
- Two VCS pools on one host, with and without `allowRuntimeHeldCredentials` on each.
- Bind, resume, and mint refusals. No link never reaches an App token.
- Mid-session revocation against the fake provider: flush, tunnel close, pod credential API refusal, mirror refusal, and each bound in "Latency, by path".
- An impersonation bearer on each session input route.
- A warm fill of a repository under a VCS pool with `userAuthorization: required`.

### Tier 5: end to end on Kind

The pod model refusals on Kind pods, the mirror path, and `gh` and `glab` binaries in a pod against a fake GitHub and GitLab. Network policy stays unchanged by these features.

### Tier 6: end to end on cloud

Against a test GitHub organization, GHES, and GitLab: a link, a child token, `gh pr create`, GitHub's enforcement of the child's limits, and refusal of review and merge by the preset.

### Tier 7: load

The tests measure sweep cost at 10,000 sessions, including deduplication and the entry `verifiedAt` write rate, refresh lease contention, and rule evaluation latency and memory at the concurrency cap with bodies at the bound, with `-race`.

### Tier 8: chaos

The tests cover a host outage for repositories under VCS pools with `userAuthorization: required`, with new sessions refused and store reads ending at `verdictMaxAgeSeconds`, a stopped sweep, the loss of the Token Service, Redis, and Postgres, a `netagent` restart with a lost cache, and a lost binding push.

### Tier 9: security

- A token never appears in a log, span, or record.
- Cross-tenant: a fake GHES copying another tenant's identifiers receives no cached verdict or child.
- Each row under "Git and hosts", including a qualifying session where a rotated, different, or absent client credential gets the current value and never a mint, a suspended, unreleased, or `allowRuntimeHeldCredentials`-cleared session that gets a 401 and never a mint, a binding deleted mid-session the same, a plan naming a suspended binding with `delivery: file` rejected at create, a refused mint to `netagent` with the mirror's own request served, a rebuild's read mint served in the provisional state with no write mint, a Token Service outage giving 503, and a delegated child with no lease entry.

### Tier 10: conformance

Adapter contracts do not change. The runtime battery gains `gh` and `glab` cases under `delivery: inject`.

### Tier 11: documentation

The tier checks error codes, alert runbooks, and the generated preset page.

## Validation owed

The spikes against GitHub and GitLab, and the stub capture, are under "Validation owed" in [`github-credential-addon-design.md`](github-credential-addon-design.md#validation-owed) and [`gitlab-credential-addon-design.md`](gitlab-credential-addon-design.md#validation-owed), which keep their S-numbers. This layer owes the git path and the runtime-held token rows.

| Spike | Question | Decides | State |
| :-- | :-- | :-- | :-- |
| S18 | The git path: candidates L, T, and P, which "Git remotes and client tools" defines | UR-5 | **Closed on the stub [measured], and T is settled**, as "Git remotes and client tools" lists. Not run: macOS, Windows, amd64, the .NET SDK image, a `glab` fork flow, read after push, git against `ghe.com` beyond the stub, a T and P hybrid, and a decision service, which ran as a stand-in. |
| V4 | The git path T column of "Git and hosts", and `allowRuntimeHeldCredentials` | The git path | Column **[measured]**, on a stub. The runtime-held attach and each 401 state are **[measured]**. A decision service, the fall-through between a tenant's route lists, and the Token Service's assertion of the expected route on its CONNECT have not run, and `netagent`'s route assertion ran on a stand-in. A pin exception, a guardrail, and posture have no prototype. |

## Residual risks

- **Token Service compromise.** It holds parent tokens, and refresh tokens, for every linked user. It already holds every App's private key and every stored secret. On GitHub a parent is limited by its link provider's App, which a read-only App narrows to nothing, and on GitLab with `read_api` a parent cannot write. Under [GL-2](gitlab-credential-addon-design.md#gl-2-user-authored-merge-requests), if [S13](gitlab-credential-addon-design.md#validation-owed) requires it, it holds `api` parents, which widens what its compromise exposes and is why that case needs a tenant opt-in.
- **The revocation window.** A revoked user keeps git access through an honest pod for up to 6 minutes, and for up to 15 minutes of reads during a host outage.
- **Workspace content.** What a session has cloned stays in the workspace, and in its checkpoints and artifacts, after access is lost. A VCS pool's `onAccessLost: terminate` ends the session and leaves stored artifacts under the owner's existing access.
- **Allowed mutations on other people's work.** On a session repository, Alice's agent can change the title, body, base, or state of Bob's pull request, close it, comment on it, and request reviewers, because rules do not see authorship or input keys and the host allows it. These are attributed, and most are reversible. A base change can move an approved pull request onto another branch ([S16](github-credential-addon-design.md#validation-owed)).
- **New operations.** Under `requestRules.default: allow`, a new GitHub or GitLab operation with approval or merge power passes until the preset names it. The owner accepted this. About one a year needs only `pull_requests: write` on GitHub, in one reading **[source]**.
- **Pushes with the user's rights.** On a VCS pool with `pushIdentity: user`, an agent pushes wherever its user can on the session's writable repositories, including a protected branch the user is allowed to bypass. Lenny writes no branch rules, by the owner's decision 10, and the repository's own protection is the control.
- **Two-person review depends on the repository.** UR-4 holds for pushed work only where the repository requires approval of the most recent push, and only while the agent's push is the latest. Without that rule, or after Bob or anyone else pushes again, Alice's agent's commits can sit in Bob's pull request and Alice's approval counts, as it would after a push she made by hand. UG-4 closes this where a tenant runs it, and on a VCS pool with `pushIdentity: app` it is required.
- **App-identity pushes have no branch bound.** On a VCS pool with `pushIdentity: app` on GitHub, and on GitLab, where git stays on the project token until [S13](gitlab-credential-addon-design.md#validation-owed), an agent pushes as the App with the App's rights rather than its user's. It can push to another user's merge request branch, which the per-user branch namespace of earlier revisions prevented, and to any branch the App may push to, including protected branches where the App is a bypass actor or a Maintainer-role project token can push where its user cannot. Decision 47 asks how to bound this.
- **`delivery: file` tokens on these hosts.** Where every VCS pool covering a host sets `allowRuntimeHeldCredentials: true`, a session holding a binding with `delivery: file` for that host has a token that leaves every control here. A `registered` type can also lie about its hosts, which the pin addresses on the host the token works on.
- **Uncovered hosts.** Under git path T, a token the agent holds reaches an uncovered git host in an opaque tunnel, and a push is not refused, as [the egress design](egress-and-git-design.md) states.
- **A smuggled token** reaches an unpinned host through an opaque tunnel, until [EG-12](egress-and-git-design.md#proposal-split) lands, on a VCS pool with `pinHosts: false`, or on a pool with `egressProfile: internet`.
- **PR content.** An agent's pull request body or comment can carry anything, including text aimed at a human reviewer or at a reviewer's agent.
- **Preset-only limits.** Against a Developer-level GitLab project token, `gitlab-deny` is all that stops approval and merge, and its route families are unmeasured beyond approve, unapprove, merge, rebase, `reset_approvals`, and `cancel_merge_when_pipeline_succeeds`. Against a GitHub child, nothing at the host distinguishes creating a pull request from approving one.
- **Service identities.** A person holding a listed client secret can start App-authored work and then approve it. Lenny cannot distinguish that person from the system. The audit trail shows the subject, and UG-4 cannot name a person who is absent from the session.
- **Comment-driven and other automation.** A comment, PR body, label, or other allowed event acts with Alice's standing on any bot that reads it. If a tenant adds no rule, or its `bodyPatterns` miss a grammar, or its bot acts on an event no rule gates, a single injected agent can approve or merge through such a bot wherever it trusts her. Request rules cannot close this, and only the tenant's rules and the bot's own check of `performed_via_github_app` bear on it.
- **CI on pushed branches.** An agent's branch runs the repository's existing workflows or pipelines with the secrets they get for a same-repository branch, and its build scripts are agent-written. This allows secret exfiltration and anything those secrets can do, including a workflow token permitted to approve. It exists wherever pods can push, under App tokens too.
- **Connectors.** Under decision 3, a connector on a host where no VCS pool sets `userAuthorization: required` has no narrowing, and an injected agent can call whatever tools `connectorSelector` admits. A connector to an MCP server outside the provider's host set, which holds its own credential to the same repositories, is undetectable here under any option.
- **A second account as approver.** Identity binding covers the linked account. A person with a second account in the organization can approve from it unless the organization's sign-on rules prevent that.
- **Provider and client behaviour.** The presets and checks depend on behaviour listed under "Validation owed". A new `gh` or `glab` release passes rather than breaks, which is the reverse of revision 3's failure mode.
- **Identifier-only linking under UG-4** proves an identity at one time. An account later transferred at the provider is not noticed.

## Decisions

| # | Decision | Recommendation | Cost of the recommendation | Alternative |
| :-- | :-- | :-- | :-- | :-- |
| 1 | Separating the features | **Settled**, in the owner's word "ok". UG-1, UG-2, and UG-3 as separate proposals in that order | UR-4 waits for UG-2, and tenants see partial value in between. | One proposal, which ties low-risk authorization to the pod-exposed token. |
| 2 | §9.3 reuse | Moved to [`user-credential-delivery-design.md` decision 29](user-credential-delivery-design.md#decisions) in revision 28. | | |
| 3 | Connectors on link hosts | **Settled by the owner.** Refuse a connector on a link provider's host set only when a VCS pool on that host sets `userAuthorization: required`, since UR-2 is the property a connector skips, in both write orders: setting `userAuthorization: required` suspends a connector that exists there. §9.3 connectors are written on the platform-admin route, outside [EG-9](egress-and-git-design.md#proposal-split), so UG-2 adds a suspended field to `connectorstore`, the gateway's store of §9.3 connector records, and the invoker (`connectorinvoke.Invoker`, which calls a connector's tools) refuses a suspended connector. `hostLink` stays withdrawn. | A connector's token on such a host reaches the provider with no narrowing, and an injected agent can call whatever tools `connectorSelector` admits, on VCS pools whose `userAuthorization` is `off`. | Refuse on every token-holding link provider's hosts, as revision 3 did, which costs a tenant its GitHub MCP tools wherever a link exists. Or allow everywhere, which leaves `userAuthorization: required` bypassable through a tool. |
| 4 | Link key and App | Moved to [`user-credential-delivery-design.md` decision 30](user-credential-delivery-design.md#decisions) in revision 28. | | |
| 5 | Account sharing | Moved to [`user-credential-delivery-design.md` decision 31](user-credential-delivery-design.md#decisions) in revision 28. | | |
| 6 | Refresh | Moved to [`user-credential-delivery-design.md` decision 32](user-credential-delivery-design.md#decisions) in revision 28. | | |
| 7 | Scoped tokens | Moved to [`github-credential-addon-design.md` decision 1](github-credential-addon-design.md#decisions) in revision 28. | | |
| 8 | Request rules | **Settled by the owner ([D1](user-credential-delivery-design.md#decision-history)), revision 4.** [`user-credential-delivery-design.md` decision 1](user-credential-delivery-design.md#decisions) records the same decision for the mechanism. Generic rules as data, with the GitHub deny preset on by default, replacing the allowlist. Strict decoding, parsed GraphQL, and canonical forwarding stay. | A new mutation with approve or merge power passes until the preset is updated, and input-key and target checks are gone. A parser dependency (0.54 MB of binary **[captured]**) and buffering of inspected bodies stay. | The allowlist, which failed both of the owner's conditions, that it survive `gh` updates and work for all `gh` commands: 17 of 42 `gh` cases failed under it **[captured]**, and 50 of 66 current mutations were off it **[source]**. |
| 9 | Comment, edit, ready, and close | **Settled** through decision 8, which supersedes it. Superseded in revision 4 by decision 8. All pass the rules, on the session's repositories under a template. Reviews, merges, auto-merge, and branch updates are refused by name. | An injected agent can edit, retarget, close, and comment on Bob's pull request, and a comment acts on any bot that trusts Alice. | Revision 3's allowlist, which held `baseRefName` and `state` back, at the cost in decision 8. |
| 10 | Push identity | **Settled**, in the owner's word "ok", after [S3](github-credential-addon-design.md#validation-owed) showed that a user's or child's push makes GitHub treat the user as pusher: the VCS pool setting `pushIdentity` defaults to `user`, through a git child with `contents: write` alone that `netagent` attaches only to the write-scope path (`git-receive-pack`, its advertisement, and the LFS upload batch), never to fetches or the mirror, and no branch rules from Lenny. A human session with no git child cannot push, and a listed service session keeps the App write token. A VCS pool's `pushIdentity: app` remains and requires UG-4. | `netagent` holds a person's `contents: write` next to `pull_requests: write`, so a compromised `netagent` can merge as the user. An agent pushes wherever its user can. Two-person review on pushed work rests on the repository requiring approval of the most recent push. | App pushes with a per-user branch namespace, which tells users which branches their agents can push to, against the owner's direction, and leaves GitHub's rule seeing the App. |
| 11 | Commit author | **Settled**, in the owner's word "ok". The linked user as author, and the pool's identity as committer | A user's address appears in history for work an agent did, and it is agent-editable. | The pool's identity alone, which is simple and loses GitLab's committer rule. |
| 12 | Recheck interval | 300 s, with `cacheUntil` of 300 s and a 60-s verdict cache | A 6-minute window, and provider calls per link and repository. | Webhooks, which shorten it and require a public ingress and organization-level configuration. Or 60 s, at five times the calls. |
| 13 | After access is lost | The VCS pool's `onAccessLost: deny`, with an event, and `onAccessLost: terminate` as a tenant choice | An agent keeps the content it already has until the session ends. | `onAccessLost: terminate` by default, which loses a user's work on a transient provider error that survives classification. |
| 14 | Pod models | Refuse pods with `maxConcurrentSessions > 1`, service-mode pods, recycled pods, and bindings with `scope: always` | These tenants pay for one session per pod on affected repositories, and service-mode runtimes have no user identity. | Allow recycling with `recycle.runtimeProcess: restart`, which still rests on the pool recycle scrub, which the specification calls no security boundary. |
| 15 | Mirror outage | New sessions, resumes, and mints fail closed, and running reads continue for 15 minutes | A host outage stops new sessions on repositories under VCS pools with `userAuthorization: required` despite the mirror, which withdraws the 6-hour promise for them. | Honour 6 hours, which gives a revoked user 6 hours of reads during an outage. |
| 16 | `file` delivery | Changed in revision 4. Not used by the binding that `prAuthoring: user` attaches. Available generically under [`user-credential-delivery-design.md`](user-credential-delivery-design.md), behind `userCredentialDelivery` and a binding opt-in, and refused on the hosts of a VCS pool with `userAuthorization: required` unless every VCS pool covering the host sets `allowRuntimeHeldCredentials`. | A person's token reaches a prompt-injected process on any pool that opts in, and every limit that document lists applies. | Dropped, as revision 3 had it, which leaves the cases injection cannot serve with no mechanism, against the owner's direction. |
| 17 | Attribution | `tokenSource` and an erasable `linkRef` on `credential.mint`, with logins in `user_links` and the audit log | A reader joins through a row that erasure removes, so old records stop naming a person, which is the intent. | The login on the record, which changes the §12.8 basis for every stream. Or a new stream, which adds volume for one field. |
| 18 | Detached orphans | Keep authorization, swept, and keep a user token only when the delegation lease named it, until `cascadeTimeoutSeconds` | A child holding one can open a pull request as Alice up to an hour after her session ended. | Refuse user tokens once an ancestor is terminal, which breaks the purpose of `detach` for these tenants. |
| 19 | Impersonation | Moved to [`user-credential-delivery-design.md` decision 33](user-credential-delivery-design.md#decisions) in revision 28. | | |
| 20 | Service identities | A list of subjects in the VCS pool's `serviceIdentities`, with `caller_type: service`, admin-written and guardrail-limited. Other callers whose `caller_type` is not `human` are refused on a VCS pool with `userAuthorization: required`. | Each automation is registered per pool. A person holding a listed secret stays undetectable. | Trust `caller_type` alone, which lets a mapping error or a person skip the check. |
| 21 | Disables | Moved to [`user-credential-delivery-design.md` decision 34](user-credential-delivery-design.md#decisions) in revision 28. | | |
| 22 | Undeclared repositories | Declared-only for repositories under a VCS pool with `userAuthorization: required`, with a new `readableRepositories` plan field | An agent cannot fetch a repository it discovers mid-session, and a plan must list dependencies. | On-demand checks through the pod credential API at first access, which adds a mid-session store write, a binding change per repository, and a hot path. |
| 23 | Sessions across owners | Moved to [`github-credential-addon-design.md` decision 2](github-credential-addon-design.md#decisions) in revision 28. | | |
| 24 | Read-only repositories on the child | Moved to [`github-credential-addon-design.md` decision 3](github-credential-addon-design.md#decisions) in revision 28. | | |
| 25 | Preset on App-sourced bindings | Resolve the flavour's preset on the pod credential API on any binding on the pool's API host, including service sessions | Those App bindings lose approval and merge through the agent, and a tenant workflow relying on that clears the preset. | Leave App bindings without it, which lets an agent approve any human's pull request. |
| 26 | GitLab | Moved to [`gitlab-credential-addon-design.md` decision 1](gitlab-credential-addon-design.md#decisions) in revision 28. | | |
| 27 | Status check | An option (UG-4) for tenants refusing user tokens, and required for a VCS pool with `prAuthoring: user` and `pushIdentity: app` | A webhook ingress, App permissions, repository configuration by the tenant, and merges blocked during a Lenny outage. | The default for everyone, which removes user tokens from pods and leaves the App as author everywhere, against the request. |
| 28 | Sweep owner | A gateway background job calling the Token Service | The gateway needs a new job with per-tenant sharding, and it writes each session's entry `verifiedAt` values each interval. | A Token Service worker, which needs a new route to gateway replicas and a write to the session row from a component that reads it today. |
| 29 | Parser differences | Moved to [`user-credential-delivery-design.md` decision 35](user-credential-delivery-design.md#decisions) in revision 28. | | |
| 30 | Bounds | Moved to [`user-credential-delivery-design.md` decision 36](user-credential-delivery-design.md#decisions) in revision 28. | | |
| 31 | Quick actions | Moved to [`gitlab-credential-addon-design.md` decision 2](gitlab-credential-addon-design.md#decisions) in revision 28. | | |
| 32 | Query root fields | Moved to [`user-credential-delivery-design.md` decision 37](user-credential-delivery-design.md#decisions) in revision 28. | | |
| 33 | Flavours | Superseded in revision 4. With a deny list there is no per-flavour mutation list to keep, and prefixes come from the preset's flavour, which the VCS pool's provider and host select. | A preset names mutations on both flavours whether or not a release has them. | A list per flavour, which only the allowlist needed. |
| 34 | Review comments | Moved to [`github-credential-addon-design.md` decision 4](github-credential-addon-design.md#decisions) in revision 28. | | |
| 35 | Required statement on automation | **Settled by the owner.** Retire `repositoryAutomation` with the allowlist's label and comment checks. The setup flow and posture prompt a tenant about bots, with `bodyPatterns` and `denyMutations` as the tools. | A tenant that says nothing gets comments on, with no required answer in the API. | Keep it required when `prAuthoring` is written, setting rules from the answer. |
| 36 | Git remotes and client tools | **Settled by the owner: git path T, with its CA bundle, on VCS-covered hosts.** Applied in egress revision 47. T is the only candidate where `gh`, `glab`, go-git, JGit, and proxied libgit2 all work, and its enforcement on covered hosts holds with the config removed and for a held token **[measured]**. | About 35% more `netagent` CPU on git, public roots in git's trust, CA coverage before a warm fill, a libgit2 hang on a constraint mismatch, and no enforcement on uncovered hosts, where an agent's token reaches the host. | P, which gives `gh` and `glab` the same results, holds enforcement only with a second CONNECT rule that needs the CA anyway, and fails go-git, JGit, and libgit2. L, which leaves `gh` stopped. `GH_REPO`, a wrapper, and the others listed, none of which worked unchanged. |
| 37 | Identity binding | Moved to [`user-credential-delivery-design.md` decision 38](user-credential-delivery-design.md#decisions) in revision 28. | | |
| 38 | Push options | Refuse every push option on every covered host, with the VCS pool's `allowedPushOptions` as an allowlist | An agent or tool using `-o ci.skip` or `merge_request.*` breaks, and `netagent` parses further into the push. | Refuse `merge_request.*` alone, which leaves `secret_push_protection.skip_all` and `ci.skip`. Or read options per provider, which adds provider knowledge to the git path. |
| 39 | Labels under stated automation | Retired with `repositoryAutomation` under decision 35. Labels pass by default, and a tenant can deny the mutation. | Kodiak and label-triggered workflows act on an agent's label, wherever a tenant has not added a rule. | Revision 3's refusal of label keys, which needs an input check of the kind owner decision D1 (decision 8) removed. |
| 40 | Hosts of a VCS pool with `userAuthorization: required` | **Settled by the owner.** Pin the git host and API hosts for every session of the tenant, unfiltered by environment, whenever such a pool covers that host, once [EG-12](egress-and-git-design.md#proposal-split) lands, with the VCS pool's `pinHosts: false` as a recorded opt-out, and a stated residual until then | An API host with no binding is refused, so such a pool without `prAuthoring: user` loses unauthenticated API reads unless it adds a read-only App binding. A pin on `github.com` with no binding also refuses public release and archive downloads for every session of the tenant. Other VCS pools on that host are pinned too, and pools with `egressProfile: internet` stay outside. [EG-12](egress-and-git-design.md#proposal-split) becomes a dependency for this guarantee. | Pin only that pool's sessions, which leaves a session on another pool holding a token that reaches these repositories. A stated residual alone, which leaves `userAuthorization: required` bypassable by anyone who can place a token through `inlineFile`, `env`, or a message. |
| 41 | Tokens beside a VCS pool with `userAuthorization: required` | By host: refuse a stored secret outright, and, as the owner adopted, a binding with `delivery: file` whose type hosts include a covered host unless every VCS pool covering it sets `allowRuntimeHeldCredentials`, whatever its `userAuthorization` value. The runtime-held attach applies to a session naming that binding, whatever its state, and the pin exception needs a release. Report it in posture. | `allowRuntimeHeldCredentials` removes most of what `userAuthorization: required` promised, for sessions holding the binding. A session whose binding has no current value gets a 401 on git. A `registered` type can lie, and the pin is the enforcing control. | No `allowRuntimeHeldCredentials` setting, which excludes GitHub from the generic mechanism. Or allowed on a VCS pool with `userAuthorization: off`, where the push silently acts as the App. |
| 42 | The child's permissions | Moved to [`github-credential-addon-design.md` decision 5](github-credential-addon-design.md#decisions) in revision 28. | | |
| 43 | A same-host App binding beside the attach | Replace it per session: a human session takes the user binding, and a service session takes the App binding | Two bindings exist on one host at the tenant level, and a reader of a session's list sees the effective one. | Refuse at write, which leaves service sessions on that host with no binding. |
| 44 | A VCS pool with `prAuthoring: user` and `userAuthorization: off` | Refused | A tenant wanting user authorship with no read limits on its repositories cannot have it, and it takes the declared-only rule too. | Define the unchecked case, which needs its own namespace, error, and residuals for a token with no check. |
| 45 | Shared App across tenants | Moved to [`user-credential-delivery-design.md` decision 39](user-credential-delivery-design.md#decisions) in revision 28. | | |
| 46 | The user's email addresses | Moved to [`github-credential-addon-design.md` decision 6](github-credential-addon-design.md#decisions) in revision 28. | | |
| 47 | Bounding App-identity pushes | Open, found in the review of revision 25. On a VCS pool with `pushIdentity: app`, and on GitLab until [S13](gitlab-credential-addon-design.md#validation-owed), pushes carry the App's rights. Options: (a) require UG-4 on every such pool, GitLab included, which fixes approval counting alone and bounds no push; (b) keep a Lenny-written per-user branch namespace for App-identity pushes alone; or (c) move GitLab pushes to the session's fine-grained user token once [S13](gitlab-credential-addon-design.md#validation-owed) shows it can push, and accept the residual until then. Recommendation: (b). | (a) needs a webhook ingress and, on GitLab, external status checks on a paid tier, and still lets an agent push to another user's branch or past a protection the App bypasses. (b) tells App-identity agents where they may push and leaves user pushes unbounded. (c) leaves GitLab with the residual until [S13](gitlab-credential-addon-design.md#validation-owed) and depends on its result. | Accept the residual, which lets an agent push to another user's branch and, with a Maintainer-role project token or a bypassing App, past protections its user cannot pass. |
| 48 | A per-person limit on child mints | Moved to [`github-credential-addon-design.md` decision 7](github-credential-addon-design.md#decisions) in revision 28. | | |
| 49 | `delivery: file` near a VCS host (formerly [`user-credential-delivery-design.md` decision 7](user-credential-delivery-design.md#decisions)) | **Adopted by the owner.** Evaluate on type hosts, and refuse unless every VCS pool covering the host sets `allowRuntimeHeldCredentials`, whatever its `userAuthorization` value. A session that the attach of a VCS pool's `prAuthoring: user` reaches is refused at bind. | A tenant wanting a PAT there sets `allowRuntimeHeldCredentials`, which removes most of what `userAuthorization: required` promised for sessions naming the binding, and the posture read says so. A `registered` type can lie. | Allow on a VCS pool with `userAuthorization: off`, where the push silently acts as the App. Or never, which excludes GitHub from the mechanism, against the owner's direction. |
| 50 | Runtime-held credentials on the git path (formerly [`user-credential-delivery-design.md` decision 8](user-credential-delivery-design.md#decisions)) | A session naming a binding with `delivery: file` that `allowRuntimeHeldCredentials` applies to gets the current value `netagent` holds for that host on every git request, whatever the client sent, and no mint is used to `netagent`. With no current value, nothing attaches, and the host answers 401. **[measured]**, including the suspended and no-release states. Without `allowRuntimeHeldCredentials`, a held token is dropped on a covered host and the mint is used **[measured]**. | What `netagent` sends to the host carries one identity, the file's, and a suspension stops git rather than falling to the App. Reads on a mirror route and a rebuild's clone come from an App mint. | The comparison form, where a rotated or different token silently pushes as the App, which the owner ruled out. Or a fall to the mint in the non-qualifying states, which ran and silently pushed as the App. |
| 51 | API flavour (formerly [`user-credential-delivery-design.md` decision 26](user-credential-delivery-design.md#decisions)) | Derived from the VCS pool's provider and host, or from a preset's family and the host where no pool covers it, with no stored field or key. Revision 3's stored `apiFlavour` and revision 4's `flavour` key are removed. | A GHES host under an unusual name is GHES by default, and a GitLab under a relative URL root has its writes refused under a preset. | A stored `apiFlavour` or a `flavour` key, which let a tenant admin satisfy the `requestRules.requiredPresets` guardrail with rules that match nothing. Or a `github-enterprise` provider value, which changes §14's lease scopes and the mint's provider switch. |

## Sources

Read on 2026-11-02 and 2026-11-03. These pages were read and their behaviour was not tested.

- Coder: [external authentication](https://coder.com/docs/admin/external-auth.md). Kodiak: [config reference](https://kodiakhq.com/docs/config-reference), read through search results.
- GitHub and GitLab pages, the capture, and the client source are listed in [`github-credential-addon-design.md`](github-credential-addon-design.md#sources) and [`gitlab-credential-addon-design.md`](gitlab-credential-addon-design.md#sources).
- Reproduction under "Review history", "Review of revision 2": the scripts are in `evidence/user-git-identity-design/review-repro/`, and their output was not kept.

## Changes requested of egress-and-git-design.md

`egress-and-git-design.md` revision 51 applied these, adjusted for the preset model, and lists them under "Reserved for other designs". They remain additive, and they do nothing until a UG or UC proposal lands.

| Applied request | Proposal |
| :-- | :-- |
| I8 responses carry `cacheUntil` | EG-2 and EG-4 |
| I8 responses carry `handling`, and `netagent` refuses a credential naming anything it does not implement at that version | EG-4 |
| A request inspection stage in `inject`, reached by both transports, that can replace the forwarded path, headers, body, and query string. Under the preset model it evaluates `requestRules` from I8, with no provider code, and strips credential headers rather than holding a header allowlist. | EG-4 |
| A session holds at most one binding with `delivery: inject` per host, extended to refuse a `delivery: file` binding beside a `delivery: inject` binding on one host, evaluated on type hosts | EG-4 |
| `scope: poolAttach` under "The attach rule", and a stored token refusal with its one `file` exception | EG-4 |
| `caller_type` on the session row, through `pkg/binding`, copied to a child, and a VCS pool as a source of pins, unfiltered by environment | EG-2 and EG-12 |
| A refusal of differing flavours on one host, an expected route asserted on the CONNECT and in the level 2 contract, and a covered set read unfiltered | EG-1, EG-2, EG-3, and EG-4 |
| A `handling` value and a mint refusal for a session whose `delivery: file` binding is admitted under `allowRuntimeHeldCredentials`, which UG-3 owns, building on UC-5 | EG-3 and EG-4 |
| A stored secret refused on the hosts of a VCS pool with `userAuthorization: required`, and an unknown or `userRefused` state denying reads | EG-2, EG-3, and EG-9 |
| `secret` is a typed union, with `userCredential` | EG-4 |
| A pre-mint step before the mint cache lookup | EG-1 and EG-2 |
| Readable and writable entries as records, with `verifiedAt`, and a declared-only input on the readable formula | EG-2, EG-3, and EG-9 |
| The mirror's second-gate I3 check names the repository | EG-5 and EG-2 |
| `pkg/binding` returns the session's user, root session, caller type, and the user's invalidation level | EG-2 |
| The provisional resume binding excludes a `userCredential` binding until `running` | EG-2 |
| Push option refusal on `git-receive-pack`, on the terminated connection | EG-3 |
| Head read deadline and bare LF refusal | EG-2 and EG-4 |

**Also applied:** a client tool sees the upstream URL from `git remote -v`, because egress revision 51 states git path T in its mechanism text.

Not requested, because each is additive with no rework: `tokenSource` and `linkRef` on `credential.mint`, restated in **Erasure and holds** when UG-2 lands, the token path on `github.com` among declared mint endpoints, the git path's mint returning the session's git child on a VCS pool with `pushIdentity: user`, which changes what the Token Service returns on I8 and leaves `netagent`'s git path as it is, and the `prAuthoring`, `userAuthorization`, and `onAccessLost` fields through EG-9's write path.

## Review history

This section records the reviews of earlier revisions and what each revision changed. Each finding was resolved in the revision after the one its subsection names, and the current text above supersedes any statement here that differs from it.

### Revision notes

These notes were carried in the status line of earlier revisions.

- Revision 4 applied owner decisions D1 and D2: the `vcs-authoring` allowlist filter was replaced by one generic binding with optional request rules held as data, with a GitHub deny preset on by default. The mechanism moved to `user-credential-delivery-design.md`, and this document configures it for VCS hosts. The capture of `gh` 2.103.3 and `glab` 1.127.0 stays as history under "Capture history", and text it supported is marked superseded where the allowlist is gone.
- The capture used plain git remotes. Under the prior loopback config `gh` and `glab` stopped before any network step, and under git path T they work **[measured]**, so UR-5 is met on a stub and live hosts are owed.
- Revisions 5 to 10 resolved the review rounds recorded below, and recorded the owner's decisions 3, 35, 36, 40, and 42, in the owner's words "go with T and your recommendations".
- Revisions 12 to 17 changed only the sentences that `tenant-scoping-identity-design.md` made false.
- Revision 24 recorded the live GitHub spikes S1, S2, S3, S5, S6, S15, and S16 under "Validation owed", and the owner's settling of decisions 1, 2, 4 to 6, 9 (superseded by 8), and 11, with decision 4 allowing several link providers on one host. It also applied the requests in `tenant-derived-runtimes-design.md`.
- Revision 25 applied the owner's settling of decisions 7 and 10 after the live spikes: per-session child tokens with no Lenny limit, and pushes as the user with no branch namespace. It added decision 46 for the email finding of S6. An independent review of that change found three Major issues, all fixed in that revision: no App fallback for a push without a git child, UR-4 restated to GitHub's latest-push rule, and App-identity pushes opened as decision 47. Decision 48 reopened a per-person limit because GitHub's App limit is shared. The revision followed independent security and consistency reviews whose final stop check found no Major or higher issue. That check traced the last round's changes and was not an end-to-end read.
- Revision 26 named the object and the field behind each configuration value the text cites, so that a value such as `required` or `user` always appears with its setting and the pool or binding that holds it. It changed no rule.
- Revision 27 added "High-level design", moved the review records into this section, and moved the capture results and the revision 3 filter into "Capture history". It changed no rule.
- Revision 28 made this document the VCS layer. It moved the link flow, the stored links, identity binding, refresh, revocation at the provider, canonical forwarding, the bounds evidence, and the defects found while verifying to `user-credential-delivery-design.md`, with decisions 2, 4, 5, 6, 19, 21, 29, 30, 32, 37, and 45. It moved the GitHub kinds, the repository check against GitHub, child tokens, pushes as the user, the GitHub request rule results, the capture history, and decisions 7, 23, 24, 34, 42, 46, and 48 to `github-credential-addon-design.md`. It moved the GitLab section, UG-5 as GL-2, and decisions 26 and 31 to `gitlab-credential-addon-design.md`. It took the VCS pool interactions and decisions 7, 8, and 26 from `user-credential-delivery-design.md` as decisions 49, 50, and 51. It changed no rule.
- Revision 29 followed a pass for a reader new to the project. It added plain-language opening paragraphs to "High-level design", moved "Terms" above it, added "Terms from other designs and the platform", "Labels from other designs", and the evidence sources under the tag table, and named Erin among the personas. It linked each citation of another design and each label from another design to its definition, replaced the egress design's interface labels with the names "pod credential API", "pod binding API", and "egress decision API", and moved "Changes requested of egress-and-git-design.md" to the end unchanged. It corrected the "Covered host" entry in "Terms" to match the egress design's definition, which this document's own rules for hosts covered by a git route alone already followed. A reader review of that pass then added glosses and forward pointers, defined a release, a pod release, the runtime-held attach, and the link provider's App, and qualified words used in more than one sense. It changed no rule. A validation pass then corrected the condition under which decision 27 requires UG-4, pointed the TS-4 citations at "User lifecycle (TS-4)", and linked the "GitLab options A to F" row in "Terms" to the add-on's "Options", and changed no rule.

### Review of revision 2

An independent review made these findings. Each was checked against the code, the specification, and provider documentation, and none was rejected. Where this design went further than the reviewer's suggestion, the row says so. Resolutions are as of revision 3. Revision 4 changed the connector and comment rows, as decision 3 and "Comment-triggered actions" state.

| Finding | Result | Resolution |
| :-- | :-- | :-- |
| `hostLink` connectors skip every control | Holds, by the design's own text: the token would reach `connectorinvoke`, on the gateway, with no filter, list, or sweep. | `hostLink` is withdrawn. "Single store" refuses a connector on a token-holding link provider's hosts, in both write orders. No connector is grandfathered. |
| `gh` and `glab` fail in a Lenny pod | Holds. Reproduced in a no-network container with `gh` 2.103.3 and `glab` 1.127.0 **[captured]**. | "Git remotes and client tools", an EG-3 request, and decision 36. The worked examples are corrected. |
| GitLab push options skip the filter and guard | Holds. The egress design's `netagent` reads "the ref update commands", and GitLab's page lists `merge_request.auto_merge`, `merge_request.description`, and more **[documented]**. | Wider than suggested: `netagent` refuses every push option on every host, because the same list has `ci.skip`, `secret_push_protection.skip_all`, and `security_policy.bypass_reason`. An EG-3 request. |
| GitLab query parameters skip body checks | Holds. GitLab's page shows `POST .../notes?body=note`, and quick actions run on notes "sent through any API" **[documented]**. | REST writes refuse a query string and admit only a JSON body. |
| Nothing proves a linked account is the user's | Holds. `Principal` carries no email or provider identity **[source]**, and the unique index covers two Lenny users only. | "Identity binding", UP-9, and decision 37. |
| The comment setting misses non-text triggers | Holds. `labeled` is a documented `pull_request` activity type, and secrets are described as withheld for forks **[documented]**. Kodiak's config reference names an automerge label. | The setting becomes `repositoryAutomation`, gates label keys under `present` and `refuse`, and the CI path is a stated residual. |
| `user_verified_at` is per session | Holds: one repository's success would refresh a session while another stays `unavailable`. | `verifiedAt` sits on each entry. |
| The hourly budget does not cap live children | Holds: a child can outlive the hour. | A live cap beside the hourly one. |
| `credential.mint` cannot name a PR's opener | Holds: a child is per repository set and shared by sessions. | UG-4 reads the head branch's namespace and `git.operation`. |
| The namespace rule omits REST `head` | Holds. | It covers `head`, and `source_branch` on GitLab. |

### Verification of the review findings

Each finding was checked against the code and the documents. The table states what holds and what differed.

| # | Result | What was checked |
| :-- | :-- | :-- |
| 1 | Holds. The body takes `target` or `target_id` (one is required), `repositories` or `repository_ids`, and `permissions`, on GitHub and on GHES 3.22. The response has `expires_at`, and the page does not say what sets it or how it relates to the parent **[documented]**. | "UG-3", S1 |
| 2 | Holds. The permissions table lists `POST /repos/{owner}/{repo}/pulls/{pull_number}/reviews` under `pull_requests: write` **[documented]**. The capture confirms the document for approve, comment, and request changes is the same, with the event in variables **[captured]**. Whether the permission works is **[stub cannot show]**. **One premise is wrong:** the egress design states no request buffer limit for `netagent`. It states a 64 Mi example request and an 11 MB RSS while streaming, and its scrub distinguishes "any body `netagent` does not buffer". The filter therefore introduces the bounds, under "Bounds and cost". | "UG-3" |
| 3 | Holds as a question. GitHub says the push must be approved "by someone other than the person who pushed it" and does not say how an App's push with a requesting user is treated **[documented]**. **One proposal premise is wrong:** `allowedRefPatterns` and path enforcement sit in `netagent` and read the plain git request, so they do not depend on the push using an App token. | "Git pushes and the most recent push rule", S3 |
| 4 | Holds in full. The cache paragraph sits under "Egress decision service" directly before **The I8 listener**, and the mint cache key is under "Credentials", **Minting**. The egress design already has a Token-Service-assigned cache lifetime for stored secrets (15 minutes), which is the precedent the fix generalizes. | "Revocation" |
| 5 | Holds. Recycled pods add a stronger case: §5.2 says the scrub "is **best-effort** and is not a security boundary", and on a `keep` pod the runtime process survives it **[source]**. | "Pod models" |
| 6 | Holds for today's tree: `Materializer.Available` returns false when `credential.UserProxyDialect` has no dialect, which covers `github`, and `credential_preclaim.go` then takes the pool path **[source]**. In the target design, VCS tokens come from I8 and never pass that router, so the larger fact is different: **the App token is the default on I8 rather than a fallback.** A requirement has to be stated on the VCS pool, and the `usercreds` change in the proposal is the wrong place. A further defect: `POST /v1/credentials` accepts `provider: github`, which is a known provider, stores it, and never delivers it. | "UG-2", "User tokens on a VCS host" |
| 7 | Partly already holds. `requireCreatingBearerAllowed` refuses an impersonation bearer on session create, derive, create-and-start, and replay **[source]**. `handleMessages` and `handleResume` have no such check, so an impersonator can drive an existing session. That is the case to close. | "Identity cases" |
| 8 | Holds, including GitLab **[documented]**. In the tree, `RefreshAccessToken` has no non-test caller and `Invoker.bearerFor` returns the stored token with no refresh, so §9.3 connector refresh is absent today **[source]**. | "Refresh" |
| 9 | Agreed. Recorded as decision 16. | "Delivery" |
| 10 | Holds. "Evidence streams", **Erasure and holds**, gives that basis. | "Attribution" |
| 11 | Holds with a correction: the current REST page says "No more than 2,000 OAuth access token requests per hour are allowed for GitHub Apps and OAuth apps", where the finding and the egress design say "for user access tokens". The ten-token text sits on "Authorizing OAuth apps" and adds detail: past ten, GitHub revokes in order an unused token older than a minute, then the least recently used, then the oldest, and the hourly limit causes a browser re-authorization prompt rather than a revocation **[documented]**. | "Child token cache and budget", S2 |
| 12 | Holds, and the attack exists in the §9.3 code: the callback carries no bearer and no cookie, and the comment in `handleConnectorOAuthCallback` says the `state` parameter is the only binding. A caller can start a flow, send the URL to a victim, and receive the victim's token under the caller's own user **[source]**. | "Defects found while verifying", "Flow" |

Provider facts that differ from the request:

- **GitLab fine-grained personal access tokens.** The cited post was updated on 2026-08-21 and now says the feature is "generally available in GitLab 19.2", "ready for production use", with "100% REST API coverage". The beta statement and the 75% figure are gone. The documentation page confirms Free, Premium, and Ultimate, and GitLab.com, Self-Managed, and Dedicated. This changes the GitLab analysis, because the REST permission table separates Create, Approve, Merge, and Update on merge requests, which GitHub's `pull_requests` permission does not, and `POST /user/personal_access_tokens` accepts `granular_scopes` from 19.2 **[documented]**. See "GitLab".
- **Copilot.** The cited page says "your approval of a Copilot pull request won't count" and does not mention the most recent push rule. GitHub's rulesets page describes "Require an additional approval for unattributed Copilot pull requests", enabled by default **[documented]**. The precedent stands, under a different citation: a host treats an agent's requester as ineligible to approve.
- **Coder.** The page describes a button on the workspace setup page added by `coder_external_auth`, and does not describe a prompt that blocks start or cover refresh **[documented]**. The user experience this design copies is the one the finding describes, with the blocking made explicit.

### Review of revision 4

Two independent reviews read revision 4, one adversarial on `user-credential-delivery-design.md` with this document's related parts, and one for consistency across documents. Each finding was verified against the text and the tree. Those on the mechanism are recorded in that document's "Review of revision 1", and these concern this one.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| H1, link key and routes | Holds. | Keyed on the provider name from UG-1. |
| H3, implicit attach | Holds. | "The attach rule", with replacement chosen. |
| H4, H5, M16, and S11, presets | Holds. | Flavours, rule kinds, and resolution at I8, by provider kind. |
| M1, M2, M3 | Hold. | One code per case, one reason, and one `tokenSource` list. |
| M4 and S19 | Hold. | Only the pool's named link on a `required` API host. |
| M5, option F | Holds. | **Withdrawn.** An exception would have to read cleanly in egress P2, its `hosts` rule, and "Other tokens", and it read cleanly in none. |
| M6 | Holds. | `followsEntryState`. |
| M7 and S7, UP-1 and UP-10 | Hold. | Both corrected, and `delivery` added. |
| M8 | Holds. | Rows limited, with the refusal stated. |
| M12 | Holds. | Lease rule stated. |
| M17 and S4 | Hold as a gap. | Fail closed on spelling. Rows are **[pending]**. |
| M18 | Holds. | UR-5 reworded, the table separates rules from the host, `permissions` is data, and decision 42 asks the owner. |
| S2 | Holds. | Lifecycle row and origin binding. |
| S3 | Holds. | Type hosts. |
| S5 | Holds. | Default-deny gate. |
| S8 | Holds. | Per session. |
| S9 | Holds. | Literals scanned. |
| Minors | Hold. | Stale step references, the retired list, "filter", the `edit` setting, the bare "decision 25", `Single store`, UR-1 and UR-4, and `OC-1 and OC-2`. |

None was rejected. New while verifying: a connector's `clientSecretRef` has the same flaw in today's code, under "Defects found while verifying".

### Review of revision 5

Two independent reviews read revision 5's design, and each finding was verified against the text and the tree. "H" and "M" are the security review, and "N" the sync review. Those on the mechanism are in `user-credential-delivery-design.md`, "Review of revision 2", and these concern this document.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| H2, relink before the confirmation page | Holds. Step 7 named no host, and the login comes from an endpoint she controls. | A page before the redirect, and a recorded digest step 6 compares. |
| H4 | Holds. | "Identity cases" follows the session input layer. |
| M8, N5, N38, attach | Hold. | The trigger is a declared writable repository, service paths need a listing, and replacement holds per host whatever the state. |
| M9, shared App | Holds. | Removed, which reworks less than a platform-only write. Decision 45. |
| N3, link key | Holds. | Terms, decision 4, caches, and the audit event. |
| N6, empty set | Holds. | No child, and a stated answer. |
| N11, gate and dependency | Holds. | UG-2 depends on UC-3, with no cycle. |
| N15, forwarding | Holds. | The rule is limited to inspected requests, and decision 29 follows. |
| N21, N23, N25, N28, N31, N37, N39, N40 | Hold. | Corrected. `identifier` is a ceiling for UG-4, `bot` is defined, and decision 40 states the download cost. |
| N26 and the connector defect | **The earlier entry was wrong.** | Restated. The route admits only `platform-admin`, so it is no tenant escalation, and the resolver's missing `get` is the defect. |

None was rejected.

**Spike results folded in.** S18 and S19 close on the stub, and S8, S9, and S10 advance. "Request rules on VCS hosts", "What reaches the host", and "Git remotes and client tools" hold the rows. Not run: any GitHub or GitLab host, permission check, or quick action; `gh` on amd64, macOS, or Windows, the .NET SDK image, and drift on the Debian client; a `glab` fork flow, read after push, and git against `ghe.com` beyond the stub; a T and P hybrid, and a GitLab route inventory.

### Review of revision 6

Round 3 of the reviews read revision 6, and each finding was verified against the text and the tree. Those on the mechanism are in `user-credential-delivery-design.md`, "Review of revision 3", and these concern this document.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| R3-5, `attach` | Holds. | The binding has none, and the kind fixes it. |
| R3-6, revocation after deletion | Holds. | The lifecycle row and the durable record. Deletion after a mismatch revokes nothing. |
| R3-7 and M1, `apiFlavour` | Holds. | Removed, and the flavour derives. |
| R3-8, delegation tree | Holds. | UP-7 follows. |
| M3 and M4, UG-1's row | Hold. | UC-1 and an unconditional EG-2, and the "no pod-side component" sentence is gone. |
| M5, `userRefused` | Holds. | The sentence now states the egress rule. |
| m1, stale deny-mode text | Holds. | The capture section and example step 7. |
| m2, m4 | Hold. | The request row and the residual's list of measured routes. |
| m12, L cells | Holds. `raw/gitopt/t11/` holds T and P alone. | The push to `main` is attributed to P, and L is marked as inferred. |
| m13, retags | Holds. The deny-mode scripts cover `pr view`, `pr list`, and `auth status`, and no `--project`. | Those rows now state what ran. `status`, `checks`, `diff`, `repo view`, and `issue view` are **[captured]** under the allowlist. |
| m15, unlabelled preset | Holds. | "Command result" labels both. |
| m16, settled wording | Closed by the owner's decisions. | Decisions 3, 35, 36, 40, and 42 read as settled. |

None was rejected.

**Owner's decisions applied.** Decision 36 is settled as T, and egress revision 47 applies it. UR-5 reads as met on a stub with live hosts owed.

### Review of revision 7

Round 4 of the reviews and the spike follow-up read revision 7, and each finding was verified against the text, the tree, and the raw output. Those on the mechanism are in `user-credential-delivery-design.md`, "Review of revision 4", and these concern this document.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| R4-1, the child cache | Holds. | Sealed with the full key and the digest. |
| R4-6, deleted provider | Holds. | The lifecycle row states `deleting` and the bound. |
| M4, flag owner | Holds. | UC-5 is named, with the attach rule. |
| m3, push options on every host | Holds. | Covered hosts. |
| m8, m9, m10, m22, m23 | Hold. | Corrected. m10: setting `required` suspends a connector. m22: the push on an uncovered host is inferred from P. m23: `t4/table.txt` shows L's tunnel. |

None was rejected.

**Spike follow-up folded in.** "Request rules on VCS hosts", "What reaches the host", "Bounds and cost", S8, S9, S10, and S19 hold its numbers. The re-encoding that S8 owed ran. A bare LF refused at once did not run as stated, because the prototype waits for the deadline, and that part is **[design]**. Not run: any GitHub or GitLab host, permission check, or quick action; `gh` on amd64, macOS, or Windows; a second pod's `cacheUntil` timing; a pin exception, a guardrail, posture, and `required` pool write refusals; `memory.peak` under the new bounds; and this revision's added rules.

### Review of revision 8

Round 5 of the reviews and the revision 5 run read revision 8, and each finding was verified against the text and the raw output. Those on the mechanism are in `user-credential-delivery-design.md`, "Review of revision 5", and these concern this document.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| Security M2 | Holds. The run has a suspended binding push as the App. | The attach rule and its 401 state, in "Other tokens" and decision 41. |
| Sync M1, LFS on GitLab | Holds. | "GitLab" states the loss. |
| m3, m4, m9, m12, m14, m16 | Hold. | Corrected. m9: UG-2 carries a suspended field on connectors. |
| m19 | Holds. | The bound claim is limited to the size and depth bounds. |
| m20 | Holds. | `gpg-key delete` did not confirm. |
| m21 | Holds. | Eight names ran, `lgtm` is named, and the whitespace is ASCII. The alias list is the unverified part. |

None was rejected.

**Revision 5 run folded in.** S19 and "Request rules on VCS hosts" hold its numbers, and the GHES uploads path and the `--env` commands pass. Live reads are **[live]**: GitHub's one anonymous download action is on `github-cloud.githubusercontent.com` with no `header` key, and GitLab's one object returned an error.

### Review of revision 9

A final regression review and a further run read revision 9. Minor 9, the warm-up asset's `restricted` exception, holds, and "Other tokens" has it. N3 holds, and the plan rejection is stated. The 401 states moved to **[measured]** on stand-ins, and the stub proves forwarding only. None was rejected.
