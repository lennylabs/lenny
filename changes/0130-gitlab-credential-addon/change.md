# GitLab credential add-on

Status: `Draft`, revision 7, 2026-11-19. Programs: [v1](../programs/v1.md).

**Evidence sources.** A *stub* is a test HTTP server running stock git that stands in for a provider and records what reaches it. It shows what `netagent` forwards and nothing about what GitLab would do with it. A *spike* is a hands-on test, against a stub or a live provider. This document's spikes carry S-numbers and its validation rows carry V-numbers, and "Validation owed" in `steps.md` lists both. *The capture* is the recording of traffic from `gh` 2.103.3, GitHub's command-line client, and `glab` 1.127.0 through a prototype `netagent` to a stub on 2026-11-03, which [`0129-github-credential-addon`, "Capture history"](../0129-github-credential-addon/review-history.md#capture-history) records. *The prototype* is that Go `netagent`. *The allowlist* is the provider filter of [`0152-user-git-identity`](../0152-user-git-identity/change.md) revision 3, which forwarded only the operations, headers, and input keys it listed. [UC-4](../0151-user-credential-delivery/change.md#uc-4-request-rules-and-presets)'s request rules replaced it.

Each factual claim about behaviour carries one of these tags where the source matters. They are the tags [`0152-user-git-identity`](../0152-user-git-identity/change.md) defines.

| Tag | Meaning |
| :-- | :-- |
| **[measured]** | A hands-on result from a validation round of [`0127-egress-and-git`](../0127-egress-and-git/change.md), or from the spike of 2026-11-03 that [`0152-user-git-identity`](../0152-user-git-identity/change.md) and [`0129-github-credential-addon`](../0129-github-credential-addon/change.md) record. Its origin was a recording stub, except where the sentence names another target, such as the Grape and Rails apps under "Preset body". |
| **[captured]** | Seen at the stub or at the prototype `netagent` in the capture of 2026-11-03. It shows what the client sent and what the filter did. It shows nothing about what GitLab would do. |
| **[stub cannot show]** | A question only GitLab can answer. Each one has a spike under "Validation owed" in `steps.md`. |
| **[live]** | Read from a provider's host, anonymously, with no write. |
| **[documented]** | Stated on a GitLab page under "Sources". The page was read and its behaviour was not tested. |
| **[pending]** | Waits on a measurement or decision named beside it. |
| **[design]** | A statement of what this design requires. Nothing implements it. |

A tag can carry a qualifier that limits it, such as **[documented for the names, apart from `lgtm`]** or **[captured under the prototype's wider pattern]**.

## Scope

This document is the GitLab add-on of the user credential mechanism. It implements the VCS layer of [`0152-user-git-identity`](../0152-user-git-identity/change.md) for GitLab.com, self-managed GitLab, and GitLab Dedicated: the `gitlab` link kind and its repository check (GL-1), user-authored merge requests (GL-2, formerly UG-5 of the VCS layer), and the `gitlab-deny` preset with GitLab's quick actions (GL-3).

Out of scope: the link flow (the browser sign-in that creates a link), the binding, request rules, and the add-on model, which [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) owns, and repository authorization, the attach rule (when a VCS pool attaches a user's binding to a session), the host policy (which other tokens may sit on a host a VCS pool covers), push identity, and the approval check, which [`0152-user-git-identity`](../0152-user-git-identity/change.md) owns.

"Terms" below defines the VCS layer, link kinds, bindings, and the other terms these paragraphs use.

## Terms

| Term | Meaning |
| :-- | :-- |
| GitLab.com, GitLab Dedicated, and self-managed GitLab | The ways GitLab is offered. GitLab.com is the multi-tenant service GitLab runs. GitLab Dedicated is a single-tenant instance GitLab runs for one customer. Self-managed GitLab is an instance an organization installs and runs itself, such as `gitlab.initech.internal` under "Worked examples". |
| Free and Premium | GitLab subscription tiers. Free is the unpaid tier, and Premium is a paid tier. |
| Merge request (MR) | GitLab's counterpart of a GitHub pull request: a request to merge one branch into another, which reviewers can approve before it is merged. The VCS layer's "pull request authoring" covers both. |
| `glab` | GitLab's command-line client, which an agent runs to create, view, approve, and merge merge requests. It calls GitLab's REST API with its token in the `Private-Token` header. |
| Role and access level | GitLab gives each member of a project or group a role, such as Developer or Maintainer, and each role carries an access level. A member with Developer access can push to unprotected branches and open merge requests. |
| Project access token and project token bot | A GitLab token limited to one project and granted one role there. GitLab creates a bot user for each such token, and actions taken with the token have that bot as their author. The egress design mints one per session as its GitLab credential by default, and this document calls it the project token. Under the `storedTokenPush` grant a pool can reuse one token per tenant, pool, project, and scope, rotated every 7 days by default, as [`0127-egress-and-git`, "Push limits and commit identity"](../0127-egress-and-git/change.md#push-limits-and-commit-identity), **GitLab bot accounts.**, states. |
| OAuth scopes | The permissions a GitLab OAuth token carries. `api` grants complete read and write access to the API. `read_api` and `read_user` grant reads. `write_repository` grants git reads and writes without API access. |
| Fine-grained token | A GitLab personal access token whose permissions name projects and actions, generally available from GitLab 19.2. Its permissions include `Read`, `Create`, `Update`, `Approve`, and `Merge` on merge requests. |
| `gitlab-fine-grained` | The narrowing template this add-on ships for option E, which derives the per-session fine-grained token from the parent. "Fine-grained token per session" under GL-2 states it. |
| Quick action | A `/word` command, such as `/approve` or `/merge`, that GitLab runs from a description or comment when it saves one. |
| Note | GitLab's API term for a comment on a merge request, an issue, or a commit. |
| Push option | A `git push -o` value GitLab acts on, such as `merge_request.create`, which opens a merge request for the pushed branch, and `merge_request.auto_merge`, which sets it to merge once its pipeline succeeds. |
| Merge request approval rules and settings | Project settings that require approvals before a merge and limit who can approve. They include the rule that an author cannot approve their own merge request, which this document calls the creator rule, and "Prevent approvals by users who add commits", which excludes the Git author and Git committer of the merge request's commits and which S12 calls the committer rule. They are paid-tier features, and Free lacks them. |
| Options A to F | The ways of authoring merge requests compared under "Options". E is the recommendation, and D is the project token path the [egress design](../0127-egress-and-git/change.md) has today. |
| Spikes S10, S11, S12, S13, and S17, and validation row V5 | The validation this document owes, listed under "Validation owed" in `steps.md`. The spikes keep the numbers [`0152-user-git-identity`](../0152-user-git-identity/change.md) gave them. |
| Grape and Rails | Ruby frameworks. GitLab's REST API is built on Grape, and GitLab's web application on Ruby on Rails. "Preset body" cites routing tests against each. |
| RE2 | The regular expression engine the rule evaluator uses. Its `\s` matches ASCII whitespace alone. |
| GL-n | This add-on's parts, GL-1 to GL-3, which "Scope" lists and "Detailed design" states. Each part lands in the step of `steps.md` that names its label as an alias. |

### Terms from other designs and the platform

This design builds on mechanisms other designs define. The table gives each one the meaning this document relies on.

| Term | Meaning |
| :-- | :-- |
| Token Service | The Lenny service that stores user and provider credentials, mints short-lived tokens from them, and answers the pod credential API ([§4.3](../../spec/04_system-components.md#43-token-service)). Its component page is [`spec/components/token-service.md`](../../spec/components/token-service.md) (code `TOKSVC`), a placeholder today. |
| `netagent` | A Lenny container, under its own UID, in every agent pod that uses the local egress endpoint, the pod's HTTP proxy on loopback. It holds the pod's service account tokens for egress, terminates TLS for the hosts it must inspect, attaches credentials, runs the git path, and applies request rules ([`0127-egress-and-git`, "Terms"](../0127-egress-and-git/change.md#terms)). On a pod with several slots it attributes each connection to its slot and uses that slot's session's credentials and scopes, under the egress design's decision 60. |
| `lenny-egress` | The central egress proxy, built on Envoy, through which agent traffic leaves the cluster. |
| Pod credential API | The gRPC API on which `netagent`, and the git mirror, ask the Token Service for a binding's token. A response carries the token with the binding's hosts, attach template, and resolved request rules. [`0127-egress-and-git`, "Interfaces"](../0127-egress-and-git/change.md#interfaces) defines it as I8. |
| User credential type and source | A tenant object naming a kind of per-user token, its source, and the hosts where it works. Under `source: registered`, a tenant backend or the user writes the token to Lenny. Under `source: oauth2Link`, the user links an account at a link provider. [`0151-user-credential-delivery`, "UC-1"](../0151-user-credential-delivery/change.md#uc-1-credential-types-and-the-registered-source) defines types. |
| Egress design | [`0127-egress-and-git`](../0127-egress-and-git/change.md), which defines how agent pods reach the network and git hosts, credential bindings, and the interfaces this document names. |
| Credential binding | An object a tenant admin configures that gives sessions a secret for named hosts ([`0127-egress-and-git`, "Credential bindings"](../0127-egress-and-git/change.md#credential-bindings)). Its fields include `secret`, which names what is delivered, `hosts`, `delivery`, and `requestRules`. Its `hosts` rule refuses a binding whose `secret` is a stored token when any of its hosts is a git host a VCS pool covers, with one exception for `delivery: file` under `allowRuntimeHeldCredentials`. |
| `userCredential` binding | A credential binding whose `secret` names a user credential type, so each session receives its own user's token. |
| `attach` template | The header a credential is attached as, with `{{secret}}` standing for the value. On GitLab it is `Private-Token: {{secret}}`. |
| Git mirror | `lenny-git-mirror`, which serves git fetches from per-tenant stores. |
| Delivery and delivery ceiling | A binding's `delivery` field. The runtime is the agent program the pod runs. Under `delivery: inject`, `netagent` attaches the token to requests and the runtime holds only a placeholder. Under `delivery: file`, `netagent` writes the token to a file the runtime reads. A link provider's own `delivery` field is the ceiling on what its bindings use: `identifier`, `none`, `inject`, or `file`, in increasing order. Under `identifier`, the link keeps the linked account's identity and discards the token. Under `none`, the token stays in the Token Service for a release check and is delivered nowhere. |
| Link provider, link, and parent token | A link provider is a tenant object naming a provider kind and the tenant's OAuth2 client for it. A link is one Lenny user's stored authorization under one link provider. The parent token is the stored token a per-session token is derived from. For a link, it is the access token and refresh token the link holds, and under GL-2 it can also be a fine-grained token the user registers as the link's parent (see "Recommendation"). [`0151-user-credential-delivery`, "Link providers"](../0151-user-credential-delivery/change.md#link-providers) defines them. |
| Kind, kind block, and add-on | A link provider's `kind` selects the add-on that defines its configuration block and its behaviour. The kind block is that configuration block, keyed by the kind. An add-on is a compiled-in package that registers kinds and implements extension points ([UC-7](../0151-user-credential-delivery/change.md#uc-7-add-ons)). |
| `identityBinding`, `allowedAccounts`, and `provider_is_admin` | Link provider fields and a link field. `identityBinding` decides how the provider account is tied to the Lenny user, and its value `verifiedEmail` requires the provider's verified email to equal the `email` claim of the user's browser login. `allowedAccounts` names the accounts, organizations, or groups a linked account must belong to. `provider_is_admin` is set on a link when the kind can tell that the account is a provider administrator. See [`0151-user-credential-delivery`, "Identity binding"](../0151-user-credential-delivery/change.md#identity-binding). |
| Refresh lease | A row lease is a holder and an expiry written on a database row by a conditional update, which lets one writer act at a time. The refresh lease is the row lease on a link under which the Token Service refreshes the linked token on demand. A provider such as GitLab invalidates the old access token and refresh token when a refresh token is used, so a link needs a single writer during refresh ([`0151-user-credential-delivery`, "Refresh"](../0151-user-credential-delivery/change.md#refresh)). |
| Token trust record | Everything that decides where a stored token can go and who can read it, with a digest on each stored token. A change to the record makes the token unusable until the user acts again. [`0151-user-credential-delivery`, "Token trust record"](../0151-user-credential-delivery/change.md#token-trust-record) defines it. |
| Release, mint, release check, and verdict | A release is the Token Service handing a token to `netagent` on the pod credential API. A mint is a call in which the Token Service obtains a short-lived token from a provider, such as a project token or a per-session token derived from a link. A release check is an add-on's check that the Token Service runs before each release and each mint for a session the add-on governs. The repository check of the VCS layer is one: it asks the provider whether the user can still reach a repository. Its result is a verdict: `allowed`, `refused`, or `unavailable`. |
| Narrowing template | Data on a link provider describing how to derive a per-session token from the stored one. Its `permissions` lists what the derived token may do. See [`0151-user-credential-delivery`, "Narrowing templates"](../0151-user-credential-delivery/change.md#narrowing-templates). |
| Request rules | Data on a binding with `delivery: inject` that refuses requests before the token is attached. `bodyPatterns` are RE2 patterns applied per line to the string values of a write, and `denyBodyKeys` are JSON keys refused on a write at any depth. See [UC-4](../0151-user-credential-delivery/change.md#uc-4-request-rules-and-presets). |
| Preset and preset properties | A preset is a named, versioned set of request rules and request forms that an add-on ships as data. A request form is the encoding a request may use: its query string, its body type, and where its path may hold `%`-encoded segments. `writeForm` lists the query strings and body types a write may carry. `encodedSegments` names the path segments where `%` is admitted, and its `refuseDecoded` list names words a decoded segment must not contain. `refuseFamilies` says whether the rule that refuses unlisted `/graphql*` and `/api/*` paths applies. `undecodableBody` says what happens to a write whose body `bodyPatterns` cannot decode. [`0151-user-credential-delivery`, "Request forms belong to a preset"](../0151-user-credential-delivery/change.md#request-forms-belong-to-a-preset) defines these properties. |
| Request layout and API flavour | What an add-on registers for a host's API: the API flavour, `apiBase`, `restPrefix` (the path prefix of the REST API), the GraphQL paths, the uploads path, and the default preset. An API flavour names a request layout and selects a preset's paths. This add-on registers the `gitlab` layout with the kind in GL-1, and registers `gitlab-deny` as the flavour's default preset in GL-3. |
| VCS layer and VCS pool | VCS stands for version control system, such as GitHub or GitLab. The VCS layer is [`0152-user-git-identity`](../0152-user-git-identity/change.md), the add-on this one builds on. A pool is a set of pre-warmed agent pods that serves sessions under one configuration, and a VCS pool is a pool that also names repository prefixes and a provider credential. Its setting `userAuthorization: required` makes each repository need the user's own access at the provider, `prAuthoring: user` has pull requests authored as the user and `prAuthoring: bot` as a bot, `pushIdentity` decides whose token carries git pushes, and `allowRuntimeHeldCredentials` admits a binding with `delivery: file` on the pool's covered hosts. |
| Covered host | A git host for which `netagent` terminates TLS and runs the git path: a host that a VCS pool or a git route of the tenant names, read across all of the tenant's environments, or an alias host of that provider that shipped host data lists, as [`0127-egress-and-git`, "Terms"](../0127-egress-and-git/change.md#terms) and [`0152-user-git-identity`, "Terms"](../0152-user-git-identity/change.md#terms) state. A VCS kind's API hosts are named separately. On GitLab, git and the API share one host, so a covered GitLab host also serves the API. |
| Bind | The step that attaches a session and its bindings to a warm pod. The repository check runs at bind, at resume, and before every token mint. |
| Repository entries | The repositories a session declares, each with a `state`. `state: granted` marks one the session may write. |
| Sweep and binding change | The sweep is the VCS layer's gateway job that repeats the repository check for running sessions every 300 seconds by default. A refusal marks the repository's entry `userRefused` and pushes a binding change, which flushes `netagent`'s cached tokens. See [`0152-user-git-identity`, "Revocation"](../0152-user-git-identity/change.md#revocation). |
| Provider capability levels `mint-coarse` and `stored` | Levels the egress design assigns to a VCS provider by what it can mint. A `mint-coarse` provider mints tokens that are short-lived or revocable but wider than one repository, such as GitLab project access tokens. A `stored` provider uses a long-lived token the tenant supplies. See [`0127-egress-and-git`, "Target state"](../0127-egress-and-git/change.md#target-state) under "Credentials". |
| Durable pending-revocation record | The record of a token to revoke at session end, retried with backoff, because GitLab token expiry has day granularity and a private host can be unreachable at session end ([`0127-egress-and-git`, "Target state"](../0127-egress-and-git/change.md#target-state)). [`0151-user-credential-delivery`, "Lifecycle and revocation at the provider"](../0151-user-credential-delivery/change.md#lifecycle-and-revocation-at-the-provider) defines it once for every kind: data class Secret, envelope-sealed, retried with backoff, with a deadline each kind sets. This add-on cites that definition, and "Fine-grained token per session" under GL-2 sets the GitLab deadline. |
| Repository rule | An `approvals.repositoryWrites` rule of [SO-5b](../0142-security-oversight/change.md#so-5b-repository-write-approvals), under which a session's writes to a matching repository wait for an approver. |
| `tenant-admin` | The role that administers a tenant's configuration. |

### Labels from other designs

Proposals, properties, and requirements in other designs carry a label whose prefix names the design. This document cites them by label and links each citation to its definition. The table names what each cited label stands for.

| Labels | Design | What they name |
| :-- | :-- | :-- |
| UC-4 and UC-7 | [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) | Proposals of the generic design: [UC-4](../0151-user-credential-delivery/change.md#uc-4-request-rules-and-presets) request rules and presets, and [UC-7](../0151-user-credential-delivery/change.md#uc-7-add-ons) add-ons. |
| UG-1, UG-2, UG-3, and UG-4 | [`0152-user-git-identity`](../0152-user-git-identity/change.md) | Proposals of the VCS layer: [UG-1](../0152-user-git-identity/change.md#ug-1-vcs-link-kinds) VCS link kinds, [UG-2](../0152-user-git-identity/change.md#ug-2-repository-authorization) repository authorization, [UG-3](../0152-user-git-identity/change.md#ug-3-pull-request-authoring) pull request authoring, and [UG-4](../0152-user-git-identity/change.md#ug-4-independent-approval-check) the independent approval check. UG-5 was the VCS layer's label for what is now GL-2. |
| UR-4 | [`0152-user-git-identity`, "Personas and requirements"](../0152-user-git-identity/change.md#personas-and-requirements) | The VCS layer's requirement that a user's pull request has the user as its author and that the user cannot supply the approval it needs. |
| P2 | [`0127-egress-and-git`, "Security properties"](../0127-egress-and-git/change.md#security-properties) | The egress security property that keeps long-lived credentials out of pods. |
| SO-5b | [`0142-security-oversight`](../0142-security-oversight/change.md#so-5b-repository-write-approvals) | Repository write approvals. |

## High-level design

Lenny runs AI agents in pods, and an agent often works on a GitLab project: it pushes commits and opens merge requests with `glab`. Today it does so as a bot account that belongs to the tenant, so Lenny does not check whether the person who started the session may reach the project, and that person can approve the agent's merge request as though someone else wrote it. This add-on lets a person sign in to GitLab once, after which Lenny checks, for each project a session asks for, that the person can reach it. The token from that sign-in stays inside Lenny's Token Service. On GitLab 19.2 and later, the add-on also aims to have the agent open merge requests as the person, through a per-session token GitLab limits to the session's projects, so that where the project's approval settings apply, GitLab's own rule stops the person approving them.

GitLab runs text commands such as `/merge` that it finds in merge request descriptions and comments, so the add-on also refuses agent-written text that names a command able to approve or merge. The main limit is that no GitLab write, permission check, or quick action has run against a live GitLab. Whether the person-authored path works depends on a test, spike S13, that has not run, and until it passes, and on older GitLab, merge requests keep the bot as their author. The command refusal is a list of known commands, so a new GitLab command with merge power passes until the list is updated.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**The kind and the check (GL-1).** The add-on registers the `gitlab` kind. Each link provider of that kind names a tenant's own GitLab application, and the kind block holds the host. The kind fixes the OAuth endpoints, the API host, and the `attach` template from the host, and links with the OAuth scopes `read_api` and `read_user`, which grant no write. Its release check reads the project with the user's parent token and requires Developer access for a write.

**User-authored merge requests (GL-2).** On GitLab 19.2 and later, the target is a per-session fine-grained token minted from a parent the Token Service holds, so a merge request has the user as its author and no token with the `api` scope reaches a pod. Spike S13 decides which parent works. Below 19.2, on a VCS pool with `prAuthoring: bot`, and if S13 finds no programmatic path, merge requests keep a project token bot as author.

**The `gitlab-deny` preset and quick actions (GL-3).** The preset refuses approval, merge, content, policy, and credential routes, and its body patterns refuse lines that name GitLab quick actions with review or merge power, because GitLab runs those actions from descriptions and notes.

```
 Browser ──(link flow: read_api, read_user)──> Gateway ──> Token Service ──(repository check, GL-1; token mint, GL-2)──> GitLab API
                                                               ▲                     (through lenny-egress)
                                                               │ pod credential API
 Pod: glab ──(placeholder token)──> netagent ──────────────────┘
                                       │ token attached, gitlab-deny applied (GL-3)
                                       └──> lenny-egress ──> GitLab API
```

## Requirements

Each requirement lands on the page the "Specification and non-specification changes" section names for it, and receives that page's identifier there. A requirement marked **[pending]** waits on the spike or open decision named beside it.

| ID | Requirement |
| :-- | :-- |
| GLR-1 | The `gitlab` kind derives its OAuth endpoints, its API host, its request layout, and its `attach` template from the kind block's `host`, and a link provider write of that kind without `host` is refused (GL-1). |
| GLR-2 | A `gitlab` link reads the verified primary email and `provider_is_admin` from `GET /api/v4/user`, and checks `allowedAccounts` as group membership with `read_api` (GL-1). The scopes a `gitlab` link provider may name are **[pending]** open decision 3. |
| GLR-3 | The repository check answers `allowed` for a write only when the user's effective access level on the project is Developer or higher, `refused` when the project is unreadable with the user's token or the level is lower, and `unavailable` when GitLab cannot answer (GL-1). The field it reads is **[pending]** S11. |
| GLR-4 | On a VCS pool with `prAuthoring: user` on a GitLab host at 19.2 or later, merge request API requests carry a per-session fine-grained token minted through `gitlab-fine-grained`, limited to the session's projects whose repository entries have `state: granted`, holding `Read` on the project and merge requests and `Create` and `Update` on merge requests, and never `Approve` or `Merge` in the default set. No token with the `api` scope reaches a pod (GL-2). The parent is **[pending]** S13 and open decision 3. |
| GLR-5 | `prAuthoring: user` on a GitLab host below 19.2 is refused at the VCS pool write and at bind, and never falls back to option D (GL-2). |
| GLR-6 | An empty granted set mints no fine-grained token, and every minted token is revoked at session end through the durable pending-revocation record (GL-2). |
| GLR-7 | `gitlab-deny` is the default preset on a GitLab API host whatever token the binding carries, and the push option refusal applies (GL-2, GL-3). |
| GLR-8 | `gitlab-deny` refuses the routes and mutations of its families, a write carrying a `denyBodyKeys` key at any depth, a write with a query string, a write whose body is not JSON, a write whose body cannot be decoded, and an encoded `projects` or `groups` segment whose decoded value holds a `refuseDecoded` word (GL-3). |
| GLR-9 | `gitlab-deny`'s `bodyPatterns` refuse a write any of whose lines, split on LF, CR, VT, FF, U+0085, U+2028, and U+2029, names one of the review or merge quick actions after ASCII whitespace, compared case-insensitively and regardless of code fences, and pass other `/word` lines (GL-3). |
| GLR-10 | `netagent` removes the `Sudo` header from every request to a GitLab host (GL-3). |
| GLR-11 | The project token and the per-session fine-grained token are refused on the API host of a repository rule under [`0142-security-oversight` decision 25](../0142-security-oversight/decisions.md#decisions) (GL-2). Whether the fine-grained token stays refused is **[pending]** open decision 4. |

## Detailed design

### GL-1: The `gitlab` kind

#### Kind block and endpoints

```yaml
linkProviders:
  - name: gitlab-initech
    kind: gitlab
    delivery: none                 # a check alone; GL-2 needs inject
    clientId: "..."                # the tenant's GitLab application
    clientSecret: "..."
    scopes: [read_api, read_user]  # the default
    identityBinding: verifiedEmail
    gitlab:                        # the block key matches the kind
      host: gitlab.initech.internal
```

The `gitlab` kind block holds `host` alone, and the host is in the token trust record. The kind derives the rest from the host:

| Step | GitLab **[documented]** |
| :-- | :-- |
| Authorize | `https://<host>/oauth/authorize` with PKCE (Proof Key for Code Exchange) |
| Exchange and refresh | `https://<host>/oauth/token` |
| Access token lifetime | 2 hours by default, and configurable on self-managed and Dedicated |
| User | `GET /api/v4/user` |
| Revoke | `POST /oauth/revoke` with client ID, secret, and token |
| Application registration | A user, group, or instance application |

- **Refresh.** GitLab refresh "Invalidates the existing `access_token` and `refresh_token`" **[documented]**, which is the case the generic refresh lease of [`0151-user-credential-delivery`, "Refresh"](../0151-user-credential-delivery/change.md#refresh) serves.
- **`allowedAccounts`.** The kind checks group membership for the link provider's `allowedAccounts` with `read_api`.
- **Verified email.** The kind reads the verified primary email from `GET /api/v4/user`. A self-managed administrator can change a user's email.
- **Administrators.** The kind sets `provider_is_admin` from the user endpoint.
- **Scopes.** Which values `scopes` on a `gitlab` link provider admits, and whether the administrator refusal applies to every link of the kind, are open decision 3 in `decisions.md`.

#### Request layout

The kind registers the `gitlab` request layout on the link kind point of [UC-7](../0151-user-credential-delivery/change.md#uc-7-add-ons) together with the kind, so that the lint of [`0152-user-git-identity`, "Tests"](../0152-user-git-identity/change.md#tests), which refuses a VCS add-on that registers a kind without a host field, a fixed endpoint set, and a request layout, passes on this step, and so that `{{apiBase}}` renders for GL-2's template. GL-3 registers `gitlab-deny` as the flavour's default preset.

| Provider and host | Flavour | API host | `apiBase` | `restPrefix` | `graphqlPaths` | Uploads | `attach` |
| :-- | :-- | :-- | :-- | :-- | :-- | :-- | :-- |
| `gitlab`, any host | `gitlab` | The host | `https://<host>/api/v4` | `/api/v4/` | `/api/graphql` | None | `Private-Token: {{secret}}` |

#### The repository check on GitLab

| Host | Read | Write | Tag |
| :-- | :-- | :-- | :-- |
| GitLab | `GET /api/v4/projects/:id` returning 200 with `read_api` | Effective access level of at least Developer | **[documented]** for the scopes. The field that reports inherited and shared access is S11. |

Until S11 settles the field, the check reads the higher of `permissions.project_access.access_level` and `permissions.group_access.access_level` in the project response, and answers `refused` for a write when neither is present **[pending]** S11. Access granted only through a group the project is shared with, or through an inherited group membership, is therefore refused if GitLab reports it in neither field, which fails closed. S11 runs before 0130.S1 starts, and its answer replaces this rule.

#### What authorization needs

Repository authorization ([UG-2](../0152-user-git-identity/change.md#ug-2-repository-authorization)) on GitLab uses `read_api` and `read_user`, which grant no write **[documented]**. The token stays in the Token Service. It ships regardless of the choices below, on GitLab.com and on self-managed, with the same 2-hour refresh rules.

### GL-2: User-authored merge requests

#### What merge request authoring has to face

GitLab describes `api` as "complete read/write access to the API, including all groups and projects", and no OAuth scope limits a token to a project **[documented]**. **A user's `api` token in a binding with `delivery: inject` gives the agent the user's full power across every project and group if `netagent`'s request rules are its only limit, and a compromised `netagent` holds that token.** On self-managed, an administrator's token holds administrator power over the instance.

`glab` needs nothing unusual: REST alone for the commands captured, `Private-Token` carrying the placeholder on all 234 requests, and no `Content-Encoding` **[captured]**. With default settings it also opened one `CONNECT gitlab.com:443` **[captured]**, which is a connection to `gitlab.com` beside the configured host. The variables `GLAB_CHECK_UPDATE=false` and `GLAB_SEND_TELEMETRY=false` in the pool's profile, the environment a runtime receives at launch as [`0127-egress-and-git`, "Injected configuration"](../0127-egress-and-git/change.md#injected-configuration) sets it, stopped it **[captured]**. Lenny sets both: they are in the default set of convention variables that the controller adds to every runtime's launch environment under that section's **2. Convention variables at launch.**, so neither the operator nor the runtime author sets them, and this add-on adds no variable of its own.

#### Options

| Option | `glab` unchanged | MR author | What a compromised `netagent` holds | Approve and merge stopped by |
| :-- | :-- | :-- | :-- | :-- |
| A. `api` OAuth token in a binding with `delivery: inject`, with request rules | Yes | User | The user's full API power, for up to 2 hours | Request rules alone |
| B. `write_repository` with `merge_request.create` on push | No. `glab mr create` needs the API. | Unstated by the docs (S11), and the pusher is expected | Read and write on every repository the user reaches, with no API | The token lacks API access. It relies on `merge_request.*` push options, which every other option refuses, so it would need its own entry in the VCS pool's `allowedPushOptions` list ([`0152-user-git-identity`, "Git push options"](../0152-user-git-identity/change.md#git-push-options)), and then `netagent` must read `merge_request.description` for quick actions and refuse `merge_request.auto_merge`. |
| C. A or B behind a tenant opt-in, off by default | As chosen | As chosen | As chosen | As chosen |
| D. Project access token bots, as the [egress design](../0127-egress-and-git/change.md) has today | Yes **[captured]** with a stub | The bot | One project's token, at that project's role | Request rules and `gitlab-deny`'s `bodyPatterns`, since a Developer-level project token can approve |
| E. A per-session fine-grained token from a parent held in the Token Service | Yes, if `glab`'s routes fit (S13) | **User** | One session's projects, with `Read` and `Create` and optionally `Update` on merge requests | **On routes, the host**, because the permission table separates `Approve` and `Merge` from `Create` and `Update` **[documented]**, then request rules. **On quick actions, `gitlab-deny`'s `bodyPatterns`**, until S13 shows whether the host applies the token's permissions to a command in a description. |
| F. A fine-grained token the user creates and registers, as a user credential type with `source: registered`. **Withdrawn by [`0152-user-git-identity`](../0152-user-git-identity/change.md), as finding M5 under its ["Review of revision 4"](../0152-user-git-identity/review-history.md#review-of-revision-4) records.** | Yes, if the same fit holds | **User** | The user's selected projects and permissions, for up to 365 days by default | The egress design refuses a stored token under `delivery: inject` on a git host a VCS pool covers, and GitLab serves API and git on one host. An exception would read cleanly in none of the three texts that state the refusal: egress property [P2](../0127-egress-and-git/change.md#security-properties), the egress design's `hosts` rule, and [`0152-user-git-identity`, "Other tokens on these hosts"](../0152-user-git-identity/change.md#other-tokens-on-these-hosts). The same token under `delivery: file` is available where the VCS pool setting `allowRuntimeHeldCredentials` applies. |

Facts behind E:

- Fine-grained tokens are generally available from GitLab 19.2 on every tier and offering **[documented]**.
- `POST /user/personal_access_tokens` accepts `granular_scopes` from 19.2, with `selected_memberships` and `project_ids` **[documented]**. "You must be authenticated" is the only stated prerequisite, and the page does not say whether an OAuth token can call it or which scope it needs.
- The REST table lists `Personal Access Token`, `Create`, on that path, so a fine-grained token holding that permission can be the parent, if GitLab lets a token create one no wider than itself.
- `expires_at` has day granularity, so revocation at session end uses the durable pending-revocation record.
- Whether each creation notifies the user is unknown.
- Whether GitLab.com Free offers project access tokens is **[pending]** S13. The review of 2026-11-19 recalled GitLab's documentation as making them a paid-tier feature on GitLab.com, and no page under "Sources" states it. Where it does not, [`0127-egress-and-git`, "Target state"](../0127-egress-and-git/change.md#target-state) classes the host `stored` ("GitLab without project tokens"), so option D is unavailable there, a VCS pool on that host pushes only under stored-token push, and `prAuthoring: bot` has no bot to author with.

#### Recommendation

**LFS (Git Large File Storage, which moves large files through `info/lfs` paths) is refused on GitLab until a live run passes.** GitLab's project tokens and stored tokens sit at the provider capability levels `mint-coarse` and `stored` of [`0127-egress-and-git`, "Target state"](../0127-egress-and-git/change.md#target-state), so the egress design refuses `info/lfs` on them (["Git on covered hosts"](../0127-egress-and-git/change.md#git-on-covered-hosts-netagent)), and `git push` of an LFS object fails there **[measured]**. An action is the entry in an LFS batch response that tells the client where to upload or download an object and which headers to send. GitLab's form of that response ran on a stub **[measured]**, and the one live object tried returned an error rather than an action **[live]**, so no live GitLab action has been read.

User attribution was half of the request that [`0152-user-git-identity`, "Problem"](../0152-user-git-identity/change.md#problem) describes, and that design's revision 1 left GitLab users with a bot author. With fine-grained tokens at 19.2, that no longer needs to be the answer.

**On GitLab 19.2 and later, the target is option E: user-authored merge requests from a per-session fine-grained token, with no `api` token in any pod.** S13 settles it, and it needs a free account and an hour rather than any Lenny code, so it runs before GL-2 starts. The token holds `selected_memberships` with the session's projects whose repository entries have `state: granted`, and `Read` on the project and merge requests, and `Create` and `Update` on merge requests, all as the `permissions` data of `gitlab-fine-grained`, the narrowing template this add-on ships. `Approve` and `Merge` are never in the default set, and whether a tenant may override `permissions` is open decision 6. "Fine-grained token per session" below states the template and the token's lifecycle.

The outcome of S13 selects the parent. Whether rows 2 and 3 stay in scope, given that no change defines the opt-in of row 2 or a registered parent on a link for row 3, is open decision 3:

| S13 result | Parent in the Token Service | Cost |
| :-- | :-- | :-- |
| An OAuth token with a low scope can create the child | The link's OAuth token | The link needs that scope, and users relink once |
| An OAuth token needs `api` | The link's `api` OAuth token, on the tenant's opt-in. `provider_is_admin` refuses a link for an instance administrator. | `api` in the Token Service for those links, which widens Token Service compromise and never touches a pod |
| No OAuth token can, and a fine-grained parent can | A fine-grained token the user registers with the Token Service as the link's parent, holding only that create permission, and never delivered | A manual step per user, up to 365 days |
| No programmatic path | D remains, and the MR author stays the bot. A tenant that accepts the limits of `delivery: file` can use it where every VCS pool covering the host sets `allowRuntimeHeldCredentials`. | [UR-4](../0152-user-git-identity/change.md#personas-and-requirements) stays unmet on GitLab |

On Free, GitLab's approval settings are absent **[documented]**, so E gives attribution there and the preset gives the rest.

**The 19.2 requirement is checked, and option D is never a silent fallback.** A VCS pool write that sets `prAuthoring: user` on a GitLab host reads `GET /api/v4/version` through the mint path with the pool's provider credential, and refuses the write with `400 VALIDATION_ERROR` naming `prAuthoring` when the host is below 19.2 or its version cannot be read. At bind, before the first mint, the Token Service reads the version again with the user's parent token, and refuses the binding's release with a decision identifier and the reason `gitlab_version_unsupported` when the host is below 19.2, so a host downgraded after the pool write fails closed **[design]**. The scope `GET /version` needs is **[pending]** S13.

**Option D remains for GitLab below 19.2 and for a VCS pool with `prAuthoring: bot`.** There the MR author is the bot, and two-person review rests on the commit author and a paid setting, as ["Residual risks"](#residual-risks) states, or on the independent approval check ([UG-4](../0152-user-git-identity/change.md#ug-4-independent-approval-check)).

**Option A is not this configuration's recommendation, and a VCS pool's `prAuthoring: user` never selects it.** Its residual is the user's whole account, and E reaches the same `glab` with a token whose power GitLab limits. A tenant can still define a user credential type with `source: oauth2Link` and the `api` scope under [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md), whose documentation says "no template means full reach". Revision 3 of [`0152-user-git-identity`](../0152-user-git-identity/change.md) said option A never ships. Under the generic mechanism, Lenny can no longer refuse that by provider without tying the mechanism to GitLab. Option B stays unused, since it breaks `glab` and option E has a smaller reach.

Whichever token is in use, `gitlab-deny` stays on by default and the push option refusal applies, because S13 has not shown that a command in a description is limited by the token rather than by the user's own permissions. Commit author and committer follow [`0152-user-git-identity`, "Commit authorship"](../0152-user-git-identity/change.md#commit-authorship), and git itself stays on the project token until S13 shows a fine-grained `Git` permission works per session. The value of the VCS pool's `pushIdentity` on GitLab, which [`0152-user-git-identity` decision 10](../0152-user-git-identity/decisions.md#decisions) defaults to `user` through a git child that GitLab does not have yet, is open decision 5, which matches [`0152-user-git-identity` decision 57](../0152-user-git-identity/decisions.md#open-decisions) and is settled together with [`0152-user-git-identity` decision 47](../0152-user-git-identity/decisions.md#open-decisions). GL-2 states the value once those decisions are settled.

Under [`0142-security-oversight` decision 25](../0142-security-oversight/decisions.md#decisions), the GitLab project token and the per-session fine-grained token are refused on the API host of a repository rule ([SO-5b](../0142-security-oversight/change.md#so-5b-repository-write-approvals)) for now. The project token is not a `mint-scoped` token. The fine-grained token is narrowed at each mint to the session's projects in state `granted`, but `gitlab-fine-grained` does not set `followsEntryState`, so it does not follow a later change of an entry's state, and [`0151-user-credential-delivery`, "Narrowing templates"](../0151-user-credential-delivery/change.md#narrowing-templates) treats it as outside the `mint-scoped` class. Whether the template sets the flag is open decision 4, and [`0152-user-git-identity`](../0152-user-git-identity/change.md) keeps its interim refusal until that decision is settled.

#### Fine-grained token per session

The Token Service holds the parent and mints one fine-grained token for each session's binding through `gitlab-fine-grained`. The template's fields follow [`0151-user-credential-delivery`, "Narrowing templates"](../0151-user-credential-delivery/change.md#narrowing-templates). Every field that S13 decides is marked **[pending]** S13, and each mechanism that mirrors [`0129-github-credential-addon`, "Child tokens per session"](../0129-github-credential-addon/change.md#child-tokens-per-session) is **[design]**.

| Field | `gitlab-fine-grained` |
| :-- | :-- |
| `name` | `gitlab-fine-grained` |
| `followsEntryState` | Not set, pending open decision 4 |
| `request` | `POST {{apiBase}}/user/personal_access_tokens`, authenticated with `{{parentToken}}`, with a JSON body holding a unique token `name`, `expires_at` from `{{expiresAt}}`, `granular_scopes` from `{{permissions}}`, and `selected_memberships` with `project_ids` from `{{sessionProjectIds}}` **[documented]** for the fields, **[pending]** S13 for the authentication and the scope it needs |
| `permissions` | `Read` on the project and merge requests, and `Create` and `Update` on merge requests |
| Overridable fields | Open decision 6 |
| `response` | The token at `$.token`, its identifier at `$.id`, and its expiry at `$.expires_at` |
| `revoke` | `DELETE {{apiBase}}/personal_access_tokens/self`, authenticated with `{{token}}` **[pending]** S13 |

- **Template variables.** The add-on supplies `{{sessionProjectIds}}`, the numeric project identifiers of the session's writable repository entries in state `granted`, which the repository check has read, and `{{expiresAt}}`, the next calendar day in UTC. The template's URL stays on the link's host, which is in the token trust record.
- **Mint.** `netagent` asks the pod credential API for the binding on its first request to the API host. The Token Service runs the repository check for each project, selects the entries in state `granted`, and mints. **An empty set mints no token:** `netagent` answers the request with 403, a decision identifier, and the reason `no_granted_repositories`.
- **Cache.** The token is cached under a key of tenant, session, `link_ref`, the link's `generation`, the sorted project identifiers, and the permissions, so no two sessions share a token. It is stored encrypted in Redis as [§4.3](../../spec/04_system-components.md#43-token-service) caches access tokens, so replicas share it, and sealed with the full key and the token trust record's digest as additional data. `cacheUntil` is the earlier of the start of the `expires_at` day in UTC and the session's end, so a session that runs past that instant mints a new token and revokes the old one. A changed granted set mints a token for the new set and revokes the old one.
- **Concurrent first requests.** `glab` sends a `GET` and a `POST` together, and each mint creates a token on the user's own GitLab account. Concurrent requests for one cache key wait on a single mint under a lease on the key in Redis, so a session's first requests create one token.
- **Record before the mint.** The Token Service writes the durable pending-revocation record with the token's unique `name` before it calls GitLab, and adds the token's identifier from the response. The record holds the token sealed as a link's tokens are, under the definition in [`0151-user-credential-delivery`, "Lifecycle and revocation at the provider"](../0151-user-credential-delivery/change.md#lifecycle-and-revocation-at-the-provider). A record with no identifier, left by a replica that stopped after GitLab answered, is resolved by finding the user's token of that name and revoking it **[pending]** S13, which owes whether the parent can list and revoke the user's tokens. The GitLab deadline for the record is the token's `expires_at`, after which the token is invalid at the host and the record is deleted. Each failure is logged.
- **On 401.** A token revoked outside Lenny, by the user or a GitLab administrator, appears as a 401, which `reauth-on-401` covers once with a new mint, then surfaces as a 503 with a decision identifier and an alert.
- **Failed mints.** A template failure refuses the mint under 0151 and never falls back to the parent or to the project token. When GitLab refuses a mint, the refusal is the result: a rate-limit answer surfaces as the retryable `USER_TOKEN_RATE_LIMITED` that [`0129-github-credential-addon`, "Child tokens per session"](../0129-github-credential-addon/change.md#child-tokens-per-session) defines, and any other refusal as a 503 with a decision identifier and GitLab's status. GitLab's limits on token creation are **[pending]** S13.
- **Notifications.** Whether each mint notifies the user at GitLab is **[pending]** S13, and the documentation states the answer.

### GL-3: The `gitlab-deny` preset

#### Default preset

GL-1 registers the `gitlab` request layout with the kind. This part registers `gitlab-deny` as that layout's default preset, so a binding on a GitLab API host with no `preset` key resolves to it. A binding that names `gitlab-deny` in `requestRules.preset` uses the `gitlab` flavour on any host.

#### Preset body

```yaml
- name: gitlab-deny
  version: 3
  flavours: [gitlab]
```

| Property | `gitlab-deny` |
| :-- | :-- |
| `writeForm` | No query string, because GitLab's page shows `POST .../notes?body=note` **[documented]**, and JSON alone |
| `encodedSegments` | `projects` and `groups` values with `refuseDecoded: [merge_requests, issues, repository, protected_branches, deploy_keys, approval_rules, approvals, pipelines, hooks, members]`, the prototype's list (`RW` in [`uc4-pol.py`](../0152-user-git-identity/evidence/spike-gh/gitopt/t/uc4-pol.py)), and `repository/branches`, `files`, `tags`, `commits`, and `tree`, `labels`, and `wikis` values. A decoded dot segment is refused by [`0151-user-credential-delivery`, "What every rule set forces"](../0151-user-credential-delivery/change.md#what-every-rule-set-forces), **Encoded segments.**, whatever the list holds. |
| `refuseFamilies` | Yes |
| `denyBodyKeys` | `merge_when_pipeline_succeeds`, `auto_merge` |
| `undecodableBody` | Refused |

| Family | What `gitlab-deny` refuses, which no GitLab host has run |
| :-- | :-- |
| Review | `projects/*/merge_requests/*/approve`, `unapprove`, `reset_approvals`, and approval rule routes **[measured]**. No GraphQL mutation that approves or unapproves a merge request has been identified, and S17 owes a read of GitLab's GraphQL schema for one; any it finds joins this family. |
| Merge | `projects/*/merge_requests/*/merge`, `rebase`, `cancel_merge_when_pipeline_succeeds`, mutation `mergeRequestAccept`, and `denyBodyKeys`, at any depth and in a GraphQL variable **[measured]** |
| Content and ref | `projects/*/repository/commits`, `repository/files/**`, `repository/branches` writes, and mutations `commitCreate`, `branchCreate`, and `branchDelete` **[measured]** |
| Policy | `projects/*/protected_branches/**` writes **[measured]**. Whether the family also covers project settings, members, hooks, and CI variables, which a Maintainer-role project token can write under option D, is open decision 8. |
| Credential | `/personal_access_tokens/self/rotate`, `/user/keys/**`, `/deploy_keys/**`, `projects/*/deploy_keys/**`, `projects/*/access_tokens/**`, and `projects/*/deploy_tokens/**` **[measured]**. |
| Text | `(?i)^\s*/(approve\|lgtm\|unapprove\|merge\|rebase\|submit_review\|target_branch\|run_pipeline)\b`, per line. It refused these in JSON, escaped, CR-separated, query, form, note, and GraphQL forms **[measured]**, also refused prose in a line starting `/merge the two lists` and a `/merge` inside a code block, and passed `/merge` after a non-ASCII space, because RE2's `\s` is ASCII, and `see /merge` and `/merge_requests/1`. The alias list is unverified, and whether GitLab executes a command through a given token is **[pending]** S17. The `target_branch` body field and pipeline creation routes, which reach what `/target_branch` and `/run_pipeline` reach, are not refused, and open decision 7 asks how to align them. |

**Headers.** `netagent` removes the `Sudo` header from every request to a GitLab host, because an administrator's token with the `sudo` scope acts as the user that header names. The removal comes from the GitLab host's per-host credential list in the egress design's shipped host data ([`0127-egress-and-git`, "Git configuration"](../0127-egress-and-git/change.md#git-configuration), **Credential-pinned hosts.**), which [`0151-user-credential-delivery`, "Headers"](../0151-user-credential-delivery/change.md#headers) applies to every binding, so it holds under any preset. The capture records `Sudo` stripped under `gitlab-deny` ([`RESULTS.md`](../0152-user-git-identity/evidence/spike-gh/RESULTS.md), "Header stripping") and forwarded outside `strictext` in an earlier table of the same file, so the shipped list states it rather than relying on either run.

**Keeping the preset current.** A scheduled CI job diffs GitLab's quick action page, its fine-grained permission tables, and its GraphQL schema, and raises a preset change for review when a new name matches one of `gitlab-deny`'s families, as [`0151-user-credential-delivery`, "Presets"](../0151-user-credential-delivery/change.md#presets), **Keeping presets current.**, and [`0152-user-git-identity`, "Keeping presets current"](../0152-user-git-identity/change.md#keeping-presets-current) require of each add-on. It lands with 0130.S3.

GitLab has no route inventory like the OpenAPI description of GitHub's REST API, which [`0129-github-credential-addon`, "Preset body"](../0129-github-credential-addon/change.md#preset-body) searched for write routes. The spike ran 67 rows, each a `glab` command or a `glab api` request, through the prototype to a stub. Of those, 8 were forwarded, 37 denied refused, 20 refused beyond the table's expected list, and 2 had no write **[measured]**. "Denied refused" counts requests the preset is meant to deny, which it refused. "Refused beyond the expected list" counts refused requests that the test table did not list as denied. A row with no write sent no write request to the stub. Its other evidence is Grape 3.3.5, an app built to match GitLab's routes under GitLab's pinned `grape ~> 3.2`, which routed `appr%6Fve`, a trailing slash, `//`, and `approve.json` to the approve handler **[measured]**. That is the framework rather than GitLab's whole application. A generic Rails 8.1.4 router routed a trailing slash, `//`, and `.json`, and answered 404 for `%65` **[measured]**. GitHub's stack, the framework that routes its API, is unknown.

#### Quick actions

**GitLab.** GitLab itself runs quick actions, whatever the tenant has installed: "Run automatically when you save descriptions or comments" **[documented]**. `glab mr create --description /merge` carries a merge inside an allowed route, and a route-only policy sent it to the stub with the injected token **[captured]**. Whether GitLab would execute it for that token is **[stub cannot show]**, and it is S17.

The prototype refused every line starting with `/word`, which caught `/merge`, a following-line `/approve`, and a CR-separated `/merge`, and also refused an ordinary `/usr/bin/env` line **[captured]**. That breaks legitimate descriptions. `gitlab-deny` carries a narrower `bodyPatterns` set: lines split on LF (line feed), CR (carriage return), VT (vertical tab), FF (form feed), U+0085, U+2028, and U+2029, and refused when, after ASCII whitespace, they name `approve`, `lgtm`, `unapprove`, `merge`, `rebase`, `submit_review`, `target_branch`, or `run_pipeline`, compared case-insensitively and regardless of code fences **[documented for the names, apart from `lgtm`]**. `/usr/bin/env` passes. A write with a query string is refused, because GitLab's page shows `POST .../notes?body=note`, and quick actions run on notes **[documented]**. The pattern with those eight names ran **[measured]**: it refused `/merge` and `/approve` in JSON, escaped, CR-separated, query, form, note, and GraphQL forms, also refused prose in a line starting `/merge the two lists` and a `/merge` inside a code block, and left `/usr/bin` and `/assign_reviewer` alone. `/target_branch` and a commit comment are refused too, because the pattern applies to every write, and ` /merge` after a non-ASCII space passes, because RE2's `\s` is ASCII. Whether GitLab would execute any of these is unknown. The alias list is unverified.

This list is a deny list, so a future GitLab command with merge power fails open until the preset changes. S17 tests each spelling against a live instance. On the bot path, a tenant lowers the risk further with a project token whose role cannot merge the target branch.

### Configuration by scope

| Setting | Scope | Who sets it | Default |
| :-- | :-- | :-- | :-- |
| `gitlab.host` on a `gitlab` link provider | Link provider | `tenant-admin` | None. A configuration write of the link provider must set it. |
| `scopes` on a `gitlab` link provider | Link provider | `tenant-admin` | `read_api` and `read_user`. The values a write admits are open decision 3. |

## Worked examples

### Erin on a self-managed instance

Tenant `initech` runs `gitlab.initech.internal` at 19.4, Premium, and S13 has passed with an OAuth parent. Erin is a developer at `initech`, and the VCS pool for `gitlab.initech.internal` sets `prAuthoring: user`.

- Linking uses the OAuth application with the scopes S13 settled. The link holds `provider_user_id` (Erin's account identifier at GitLab), the verified email, and `provider_is_admin`. The token stays in the Token Service. **[design]**
- Bind: `GET /api/v4/projects/:id` and the access level check pass. **[design]**, S11
- The agent runs `git push` under the project token. The commit author is Erin's verified email. `git push -o merge_request.create -o merge_request.auto_merge` is refused **[design]**.
- On its first API request, `netagent` asks the pod credential API. The Token Service creates a fine-grained token with `selected_memberships` on the session's project, `Read` and `Create` on merge requests, and tomorrow's `expires_at`. **[design]**, S13
- `glab mr create` with `GITLAB_HOST` and `GITLAB_TOKEN=lenny-injected` sends `GET /api/v4/projects/initech%2Fweb?license=true&with_custom_attributes=true` and `POST /api/v4/projects/initech%2Fweb/merge_requests` over HTTP/2 **[captured]** with a body the preset's `denyBodyKeys` admits. `netagent` re-encodes the body when `gitlab-deny`'s patterns apply, so the host receives bytes generated from the request the rules checked ([`0151-user-credential-delivery`, "Canonical forwarding"](../0151-user-credential-delivery/change.md#canonical-forwarding)), and it overwrites `Private-Token`. The stub received the injected value **[captured]**. On GitLab the MR's author is Erin, and GitLab's creator rule stops her approving **[documented]**, **[stub cannot show]**.
- `glab mr approve` sends `GET .../merge_requests/{n}` and `POST .../approve`, and `glab mr merge` sends `PUT .../merge`. Both are on `gitlab-deny`, and they were refused with nothing written under the allowlist **[captured]**. Under the token, the host refuses too **[documented]** (S13).
- `glab mr create --description $'Fix login\n/merge'`, and `/approve` on a following line or after a bare CR, are refused by the preset's `bodyPatterns` **[captured under the prototype's wider pattern]**. `--description $'See /usr/bin/env\n/usr/bin/env'` passes **[measured]**, where the earlier wider pattern refused it **[captured]**.
- `glab api graphql -f query='mutation { mergeRequestAccept(...) }'` and an aliased form are refused by the preset. `glab api` with each refused route is refused in every spelling tried **[measured]**.
- `glab api -X POST 'projects/initech%2Fweb/merge_requests/7/notes?body=%2Fmerge'` is refused, since a write takes no query string. **[design]**
- `glab api -X POST 'projects/initech%2Fweb%2Fmerge_requests%2F7%2Fapprove/merge_requests'` is refused by `projects/{encoded}`, whose `refuseDecoded` holds route words **[design]**, while `initech%2Fweb`, nested groups, and lower-case `%2f` pass **[measured]**.
- Erin leaves the project: the sweep refuses, the binding change flushes, and the Token Service revokes the session's token through the durable pending-revocation record.
- On GitLab 18.x, where a VCS pool sets `prAuthoring: bot` because `user` is refused there, or on any VCS pool with `prAuthoring: bot`, the same `glab` commands run under a project token, and the MR author is the bot. There the preset alone stops approval and merge, and Erin can approve the bot's MR unless "Prevent approvals by users who add commits" excludes her through a commit author the agent can edit.

## Specification and non-specification changes

- **Specification.**
  - `spec/capabilities/credentials/credential-add-ons.md` (ADDON): the `gitlab` kind, its request layout, and its repository check (GL-1, GLR-1 to GLR-3), the `gitlab-deny` preset with its `Sudo` removal (GL-3, GLR-7 to GLR-10), "Configuration by scope", and "Residual risks". The page is a placeholder that names this change as a source.
  - `spec/capabilities/credentials/pull-request-authoring.md` (PRAUTH): user-authored merge requests, the `gitlab-fine-grained` template and the token's lifecycle, the 19.2 check, and the repository rule refusal (GL-2, GLR-4 to GLR-6 and GLR-11). The page is a placeholder that names this change as a source.
  - `spec/catalogs/settings.md`: the rows `gitlab.host` and `scopes` on a `gitlab` link provider.
  - `spec/catalogs/errors.md`: the reasons `no_granted_repositories` and `gitlab_version_unsupported`, and the `VALIDATION_ERROR` refusal of `prAuthoring: user` on a host below 19.2.
  - The GitLab host's per-host credential list in the egress design's shipped host data gains `Sudo`, which [`0127-egress-and-git`](../0127-egress-and-git/change.md) states and this change cites.
- **Code.** The `gitlab` add-on package that [UC-7](../0151-user-credential-delivery/change.md#uc-7-add-ons) compiles into the Token Service, the gateway, and `netagent`: the kind, its block schema, and its request layout (0130.S1); the repository check in the Token Service's release check (0130.S1); the `gitlab-fine-grained` template, its template variables, the version check, and the cache, lease, and revocation record of "Fine-grained token per session" in the Token Service, beside `pkg/tokenservice` (0130.S2); and the `gitlab-deny` preset as data (0130.S3). The package path follows the registry that 0151.S7 lands.
- **Tooling.** The scheduled CI job of "Keeping the preset current" (0130.S3).

## Documentation

The tenant admin and runtime author pages of [`0152-user-git-identity`, "Documentation"](../0152-user-git-identity/change.md#documentation) carry a GitLab section that states every item of this add-on that a reader meets, as decision D13 of [`0131-incremental-checkpoint`](../0131-incremental-checkpoint/decisions.md#decisions) requires of every design. The pages and their readers are these:

- The GitLab section of the tenant admin page that 0152 adds under `docs/operator-guide/`, for the operator acting as tenant admin: every bullet below except the runtime-facing rules. 0152 sets the page's file name, and 0129 places its GitHub section on the same page.
- The GitLab section of the runtime author section that 0152 adds under `docs/runtime-author-guide/`, for the runtime author: the runtime-facing rules below.
- `docs/reference/configuration.md`, for the operator: the rows `gitlab.host` and `scopes` on a `gitlab` link provider.
- `docs/reference/error-catalog.md`, for the client and the runtime author: the reasons `no_granted_repositories` and `gitlab_version_unsupported`, and the refusal of `prAuthoring: user` on a host below 19.2.
- `docs/reference/glossary.md`, for every reader: fine-grained token, quick action, project access token, and `gitlab-deny`.
- The security page that 0152 adds, for the operator: the residual risks below in the same words.

Each page states the following in full:

- **Settings and defaults.** `gitlab.host` on a `gitlab` link provider (no default; a configuration write must set it), `scopes` on a `gitlab` link provider (`read_api` and `read_user`), and the VCS pool's `prAuthoring`, under which `user` needs GitLab 19.2 or later and S13's result, and `bot` selects option D.
- **Provider requirements.** The OAuth application each tenant registers on its instance with the scopes S13 settles; GitLab 19.2 or later for user-authored merge requests; the paid approval settings that two-person review rests on under option D; that GitLab Free has none of them; and whether GitLab.com Free offers project access tokens, once S13 answers it.
- **Limits.** Under option E, one fine-grained token per session limited to the session's granted projects, created on the first API request, minted again when its `expires_at` day begins or the granted set changes, and revoked at session end; under option D, the project token and a bot author, and a reused project token that rotates every 7 days under the `storedTokenPush` grant; LFS refused on GitLab until a live run passes; every push option refused outside `allowedPushOptions`; and that `gitlab-deny`'s quick action patterns are a deny list over free text.
- **Failure modes and their answers.** A request `gitlab-deny` refuses, with its decision identifier in `egress.local`, including a quick action line in a description or note and a write that carries a query string; `403 LINKS_REQUIRED` with `details.userLinks`; a link refused for an instance administrator under `provider_is_admin`; the refusal of the project token and the fine-grained token on the API host of a repository rule; `400 VALIDATION_ERROR` naming `prAuthoring` on a host below 19.2, and the reason `gitlab_version_unsupported` at bind; 403 with the reason `no_granted_repositories` when no repository entry is in state `granted`; the retryable `USER_TOKEN_RATE_LIMITED` and the 503 with GitLab's status for a refused mint; and the 503 with an alert after `reauth-on-401`'s one new mint.
- **Automation on GitLab.** The quick actions `gitlab-deny` refuses do not cover GitLab automation that other triggers start, such as a merge bot that acts on an assignment (`/assign @<merge-bot>`) or a label (`/label ~<auto-merge>`). Under [`0152-user-git-identity` decision 35](../0152-user-git-identity/decisions.md#decisions), the tenant covers such bots with its own `bodyPatterns`, and the page names these GitLab forms as examples.
- **Runtime-facing rules.** `glab` runs with `GITLAB_HOST` and `GITLAB_TOKEN=lenny-injected`, and `netagent` attaches the real token; `GLAB_CHECK_UPDATE=false` and `GLAB_SEND_TELEMETRY=false` are set by Lenny in the launch environment; `glab mr approve`, `glab mr merge`, and the GraphQL merge mutations are refused; a description line naming a review or merge quick action is refused while other `/word` lines pass; the `Sudo` header is removed; git pushes use the project token until S13; and commit author and committer follow [`0152-user-git-identity`, "Commit authorship"](../0152-user-git-identity/change.md#commit-authorship).

## Tests

The tiers are the stages of the repository's test suite that [`.claude/rules/test-coverage.md`](../../.claude/rules/test-coverage.md) defines, by their names since change 0155. Each row names the requirement it verifies.

### unit

- `gitlab-deny`: each named operation refused on each spelling, and an unnamed operation passing (GLR-8).
- `gitlab-deny`'s `bodyPatterns`: each command, alias, case, whitespace prefix, line separator (LF, CR, VT, FF, U+0085, U+2028, and U+2029), and fence, with `/usr/bin/env` admitted, and a write with a query string refused. `glab`'s `%2F` project paths pass (GLR-8, GLR-9).
- `gitlab-deny`'s request forms: each `refuseDecoded` word in an encoded `projects` and `groups` segment refused, `denyBodyKeys` at depth and in a GraphQL variable refused, an undecodable body refused, and a multipart or form body refused (GLR-8).
- The kind: a kind block without `host` refused; the endpoints, the API host, the request layout, and `attach` derived from the host; the verified email read from `GET /api/v4/user`; `provider_is_admin` set from it; and `allowedAccounts` checked as group membership (GLR-1, GLR-2).
- The repository check: `allowed` at Developer and above, `refused` below it and on an unreadable project, `refused` when neither access field is present, and `unavailable` when GitLab cannot answer (GLR-3).
- The template: `gitlab-fine-grained` rendered with `{{sessionProjectIds}}` and `{{expiresAt}}`, an empty granted set minting nothing, the cache key, and `cacheUntil` at the start of the `expires_at` day (GLR-4, GLR-6).

### contract

- `gitlab-deny` replayed against recorded `glab` traffic per pinned version, with `%2F` project paths passing (GLR-8, GLR-9).
- The `gitlab-fine-grained` request and response against GitLab's documented `POST /user/personal_access_tokens` body and answer, and the revoke call (GLR-4, GLR-6).

### integration

- A mint through the Token Service and the pod credential API: concurrent first requests for one key creating one token, the revocation record written before the call, a record with no identifier resolved, a failed revocation retried from the sealed record, session end revoking the token, and a changed granted set minting again (GLR-4, GLR-6).
- A 401 from the host covered by one new mint, then a 503 with an alert (GLR-4).
- A VCS pool write with `prAuthoring: user` on a host below 19.2 refused, and a bind on such a host refused with `gitlab_version_unsupported` (GLR-5).
- A refresh of a `gitlab` link under the refresh lease, where GitLab invalidates the old access token and refresh token (GLR-2).

### e2e_cloud

Against a test GitLab instance: a link, the repository check, a per-session fine-grained token once S13 passes, `glab mr create`, and refusal of approval and merge by the preset (GLR-3, GLR-4, GLR-7).

### security

- The `Sudo` header removed from every request to a GitLab host, under `gitlab-deny`, under another preset, and with no preset (GLR-10).
- An encoded project path that decodes to a route word, a quick action in each line separator and in a GraphQL variable, and a write with a query string, each refused against shipped `netagent` builds (GLR-8, GLR-9).
- No token with the `api` scope reaching a pod, and the fine-grained token and the project token refused on the API host of a repository rule (GLR-4, GLR-11).
- A link for an instance administrator refused under `provider_is_admin`, on the links that open decision 3 names (GLR-2).

## Residual risks

- **Compromised `netagent` on GitLab.** It holds one session's fine-grained token under GitLab option E, or one project's token under option D. `api` tokens are absent from pods, so a user's power over other projects is out of reach. Where a pool reuses project tokens under the `storedTokenPush` grant, a reused token outlives the session and rotates every 7 days by default, so a compromised `netagent` can keep it until rotation or revocation, which is the exception to [P2](../0127-egress-and-git/change.md#security-properties) that [`0127-egress-and-git`, "Push limits and commit identity"](../0127-egress-and-git/change.md#push-limits-and-commit-identity), **GitLab bot accounts.**, states.
- **GitLab under option D.** Below 19.2 or on a VCS pool with `prAuthoring: bot`, the MR author is a bot, and two-person review rests on an agent-editable commit author and a paid setting, so an injected agent defeats it, and Free has no such setting. The independent approval check ([UG-4](../0152-user-git-identity/change.md#ug-4-independent-approval-check)) covers this where a tenant runs it.
- **GitLab quick actions.** `gitlab-deny`'s patterns are a deny list over free text. A command they do not name, or a spelling GitLab accepts and they miss, runs under whatever permissions GitLab applies (S13, S17).

## Sources

Read on 2026-11-02 and 2026-11-03. These pages were read and their behaviour was not tested.

- GitLab: [OAuth 2.0 API](https://docs.gitlab.com/api/oauth2/), [OAuth provider and scopes](https://docs.gitlab.com/integration/oauth_provider/), [fine-grained tokens post](https://about.gitlab.com/blog/fine-grained-pats/), [fine-grained tokens](https://docs.gitlab.com/auth/tokens/fine_grained_access_tokens/), [fine-grained REST permissions](https://docs.gitlab.com/auth/tokens/fine_grained_access_tokens_rest/), [user tokens API](https://docs.gitlab.com/api/user_tokens/), [push options](https://docs.gitlab.com/topics/git/commit/#push-options-for-merge-requests), [approval settings](https://docs.gitlab.com/user/project/merge_requests/approvals/settings/), [notes API](https://docs.gitlab.com/api/notes/), and [quick actions](https://docs.gitlab.com/user/project/quick_actions/).
