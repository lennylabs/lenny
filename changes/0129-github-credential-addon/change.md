# GitHub credential add-on

Status: `Draft`, revision 7, 2026-11-13. Programs: [v1](../programs/v1.md).

Each factual claim about behaviour carries one of these tags where the source matters. They are the tags [`0152-user-git-identity`](../0152-user-git-identity/change.md) defines, stated here for this document's evidence.

| Tag | Meaning |
| :-- | :-- |
| **[measured]** | A hands-on result: a live spike on `github.com` listed under "Validation owed" in `steps.md`, a stub run from the spike of 2026-11-03 onward, or a validation round of [`0127-egress-and-git`](../0127-egress-and-git/change.md). A stub result shows what `netagent` forwarded and nothing about what a host would do with it. |
| **[captured]** | Seen at the stub or at the prototype `netagent` in the capture of 2026-11-03, which "Capture history" records. It shows what the client sent and what the filter did, and nothing about what a provider would do. |
| **[documented]** | Stated on a GitHub page under "Sources". The page was read and its behaviour was not tested. |
| **[source]** | Read in source code or published data that "Sources" lists: `gh` client source at cli/cli tag v2.103.3, `gh`'s release history, or the history of GitHub's public GraphQL schema. |
| **[design]** | What this design requires. Nothing implements it. |
| **[stub cannot show]** | A question a stub run cannot answer, because the answer depends on GitHub's own implementation. Each one has a spike under "Validation owed" in `steps.md`. |

**Evidence sources.** A *stub* is a test HTTP server running stock git that stands in for GitHub and records what reaches it. It shows what `netagent` forwards and nothing about what GitHub would do with it. A *spike* is a hands-on test, against a stub or a live host, and the labels S-n and V-n name the spikes and validation items under "Validation owed" in `steps.md`. *The capture* is the recording of `gh` traffic through a prototype `netagent` to a stub on 2026-11-03, which "Capture history" records. *Attack rows* are crafted requests, each trying to carry a refused operation past the rules, that a spike sends through `netagent` to the stub, and the attack table is their set. The capture's attack table had 522 rows per flavour under the allowlist, and the deny-mode run under S19 has 1,230 rows per host type. The bypass table lists routes around the rules, such as HTTP/2, HTTP/1.1 framing, absolute-form requests, and opaque tunnels, and "Capture history" records it under the allowlist. *Client images* are the Alpine and Debian images the `gh` client ran in. *Predicted rows* are rows whose outcome was written down before the run, so a match confirms the prediction. *The allowlist*, also called the filter, is the provider filter of [`0152-user-git-identity`](../0152-user-git-identity/change.md) revision 3, which forwarded only listed `gh` operations, headers, and input keys. The request rules of [UC-4](../0151-user-credential-delivery/change.md#uc-4-request-rules-and-presets) replaced it. *The earlier smaller preset* is the `github-deny` body before decision 8 extended it.

## Scope

This document is the GitHub add-on of the user credential mechanism. It implements the version control system (VCS) layer of [`0152-user-git-identity`](../0152-user-git-identity/change.md) for GitHub, GitHub Enterprise Cloud with data residency (`ghe.com`), and GitHub Enterprise Server (GHES): the `github` and `github-enterprise` link kinds and their repository check (GH-1), pull request authoring through per-session child tokens and pushes as the user (GH-2), and the `github-deny` preset with its evidence (GH-3). It also keeps the capture of `gh` 2.103.3 and the GitHub spike results.

Out of scope: the link flow, the binding, request rules, and the add-on model, which [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) owns, and repository authorization, the attach rule, the host policy (which other tokens may sit on a covered host), and the approval check, which [`0152-user-git-identity`](../0152-user-git-identity/change.md) owns.

This document calls [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) the generic design, [`0152-user-git-identity`](../0152-user-git-identity/change.md) the VCS layer, and [`0127-egress-and-git`](../0127-egress-and-git/change.md) the egress design.

Examples name the people of [`0152-user-git-identity`, "Personas and requirements"](../0152-user-git-identity/change.md#personas-and-requirements): Alice is a developer at `acme`, Bob is another developer and her reviewer, and Dave is `acme`'s `security-admin`.

## Terms

| Term | Meaning |
| :-- | :-- |
| Child token | A token derived from a link's parent by `POST /applications/{client_id}/token/scoped`, limited to named repositories and permissions. A child token is bound to one `target`, the user or organization account that owns those repositories. It is a GitHub token and is unrelated to a delegated child session. |
| API child | A session's child under the template's `permissions`, which `netagent` attaches to API requests. |
| Git child | A session's child with `contents: write` alone, which `netagent` attaches to the git write path on a VCS pool with `pushIdentity: user`. |
| Flavour | The API flavour of a GitHub host: `github` for `github.com` and `ghe.com`, and `github-enterprise` for GHES. It selects the request layout under "Request layout" and the paths `github-deny` matches. The evidence counts `ghe.com` as its own host type, so "3 host types" means GHES, `github.com`, and `ghe.com`. |
| `github-scoped` | The narrowing template this add-on ships, which mints the session's children. "The `github-scoped` template" under GH-2 states it. |
| `github-deny` | The preset this add-on ships, which refuses review, merge, content and ref, deployment, policy, and credential operations by name. GH-3 states it. |
| GH-n | This add-on's parts, GH-1 to GH-3, which "Scope" lists and "Detailed design" states. Each part lands in the step of `steps.md` that names its label as an alias. |

### GitHub terms

| Term | Meaning |
| :-- | :-- |
| GitHub App | An integration registered on GitHub with its own identity, a set of permissions, and an OAuth client. An organization installs it to grant those permissions on chosen repositories. Each tenant registers its own App for Lenny. |
| Installation token (App token) | A short-lived token a GitHub App obtains for one of its installations. It acts as the App, so GitHub records the App as the author or pusher. The egress design's App write token is one. |
| User access token | A token a GitHub App receives when a person authorizes it. It acts as that person, within both the App's permissions and the person's own access. A link's parent token is one. |
| GHES | GitHub Enterprise Server, the self-hosted GitHub product, served on the customer's own host with its REST API under `/api/v3/`. |
| `ghe.com` | GitHub Enterprise Cloud with data residency, which serves each enterprise at `<name>.ghe.com` with its API at `api.<name>.ghe.com`. |
| `gh` | GitHub's command-line client (cli/cli), which an agent runs to open, view, review, and merge pull requests. It sends most pull request operations as GraphQL mutations. `glab` is GitLab's counterpart. |
| GraphQL mutation | A write operation in GitHub's GraphQL API, sent as a document to the GraphQL path and named by its root field, such as `mergePullRequest`. `github-deny` matches these names. |
| `require_last_push_approval` | A branch protection setting under which the most recent push must be approved by someone other than the person who pushed it. "Pushes as the user" states how this add-on relies on it. |
| SAML single sign-on (SSO) | An organization setting that requires members to authenticate through the organization's identity provider over the Security Assertion Markup Language (SAML). GitHub marks a request it refuses for that reason with the `X-GitHub-SSO` response header. |
| LFS | Git Large File Storage, which moves large files through a batch API and separate transfer requests beside git. |

### Terms from other designs and the platform

This add-on builds on mechanisms the generic design, the VCS layer, the egress design, and the specification define. The table gives each one the meaning this document relies on and links its definition.

| Term | Meaning | Defined in |
| :-- | :-- | :-- |
| Gateway | Lenny's API gateway, which clients and browsers call. It creates sessions, serves the link flow's pages, and records a session's repository entries. | [`0151-user-credential-delivery`, "Terms from other designs and the platform"](../0151-user-credential-delivery/change.md#terms-from-other-designs-and-the-platform) |
| Roles | `platform-admin` operates the Lenny deployment, `tenant-admin` administers one tenant's configuration, and `security-admin` is a tenant role that manages the tenant security record, which holds the tenant's guardrails. | [`0142-security-oversight`, "SO-1: security roles"](../0142-security-oversight/change.md#so-1-security-roles) |
| Token Service | The Lenny service that stores user and provider credentials, mints short-lived tokens from them, and answers the pod credential API. It holds each link's parent and mints the children. | [§4.3](../../spec/04_system-components.md#43-token-service), and [`0151-user-credential-delivery`, "Terms from other designs and the platform"](../0151-user-credential-delivery/change.md#terms-from-other-designs-and-the-platform) |
| `netagent` | A Lenny container, under its own UID, in every agent pod that uses the local egress endpoint, the pod's HTTP proxy on loopback. It holds the pod's service account tokens for egress, terminates TLS for the hosts it must inspect, attaches credentials, runs the git path, and applies request rules. On a pod with several slots it attributes each connection to its slot and uses that slot's session's credentials and scopes, under the egress design's decision 60. | [`0127-egress-and-git`, "Terms"](../0127-egress-and-git/change.md#terms) |
| `lenny-egress` | The central egress proxy through which agent traffic, and the Token Service's mints, leave the cluster. | [`0151-user-credential-delivery`, "Terms from other designs and the platform"](../0151-user-credential-delivery/change.md#terms-from-other-designs-and-the-platform) |
| Git mirror | `lenny-git-mirror`, which serves git fetches from per-tenant stores. A mirror route sends a host's git fetches there. | [`0127-egress-and-git`, "Git mirror"](../0127-egress-and-git/change.md#git-mirror-lenny-git-mirror) |
| Pod credential API | The API on which `netagent` asks the Token Service for a binding's token, with its hosts, `attach` template, `cacheUntil`, `handling`, and resolved request rules. The egress design numbers it I8. | [`0127-egress-and-git`, "Interfaces"](../0127-egress-and-git/change.md#interfaces) |
| Egress decision API | The API on which a proxy, the git mirror, the gateway at level 1, or the Token Service asks `lenny-egress-authz`, the decision service, whether a connection or a token scope is allowed. The egress design numbers it I3. | [`0127-egress-and-git`, "Interfaces"](../0127-egress-and-git/change.md#interfaces) |
| Credential binding and `userCredential` binding | A credential binding is an egress-design object that gives sessions a secret for named hosts. A `userCredential` binding names a user credential type, so each session receives its own user's token. The tenant's App binding gives sessions App tokens on a host. | [`0151-user-credential-delivery`, "Terms"](../0151-user-credential-delivery/change.md#terms), and [`0127-egress-and-git`, "Credential bindings"](../0127-egress-and-git/change.md#credential-bindings) |
| Link provider, link, and parent token | A link provider is a tenant object naming a kind and the tenant's OAuth client. A link is one user's stored authorization under it, and the parent token is the access token and refresh token the link holds. | [`0151-user-credential-delivery`, "Terms"](../0151-user-credential-delivery/change.md#terms) |
| Link provider fields | `kind` selects the add-on, `delivery` is the delivery ceiling, `identityBinding` decides how the provider account is tied to the Lenny user (`directory`, `claim`, `verifiedEmail`, or `unverified`), `allowedAccounts` lists the accounts a linked account must belong to, and `narrowing` names the narrowing template. | [`0151-user-credential-delivery`, "Link providers"](../0151-user-credential-delivery/change.md#link-providers) |
| Kind, kind block, add-on, and extension point | A kind selects the add-on that defines a link provider's configuration block, the kind block, and its behaviour through named extension points. | [`0151-user-credential-delivery`, "Terms"](../0151-user-credential-delivery/change.md#terms) |
| Narrowing template | Data on a link provider describing how to derive a per-session token from the stored one. `followsEntryState` makes it narrow on each mint to the set an add-on's template variable supplies. | [`0151-user-credential-delivery`, "Narrowing templates"](../0151-user-credential-delivery/change.md#narrowing-templates) |
| Request rules, preset, and `requestRules.default` | Request rules are data on a binding with `delivery: inject` that refuse requests before the token is attached. A preset is a named, versioned set of them. Under `requestRules.default: allow`, the default, a request passes unless a rule denies it. | [`0151-user-credential-delivery`, "Terms"](../0151-user-credential-delivery/change.md#terms) |
| Deny mode and allow-list mode | Deny mode is request rules under `requestRules.default: allow`, the default, in which a request passes unless a rule or the preset refuses it. Allow-list mode, `requestRules.default: deny`, passes a write only when an allow rule admits it. | [`0151-user-credential-delivery`, "Allow-list mode"](../0151-user-credential-delivery/change.md#allow-list-mode) |
| Rule evaluator | The part of `netagent` that checks a request against request rules. `handling` can name a minimum version of it. | [`0151-user-credential-delivery`, "Terms from other designs and the platform"](../0151-user-credential-delivery/change.md#terms-from-other-designs-and-the-platform) |
| Canonical forwarding and path matching | `netagent` checks each inspected request in a canonical form and forwards bytes it generated from the checked request. It forwards its own printing of the parsed GraphQL document, refuses control characters under a lexical rule, admits one JSON envelope and one request form per endpoint (the JSON rules and the endpoint's single form), and matches paths with a strict matcher that refuses escapes, drops trailing and doubled slashes, and also compares a final segment with anything after its first `.` removed (the first-dot stem). | [`0151-user-credential-delivery`, "Canonical forwarding"](../0151-user-credential-delivery/change.md#canonical-forwarding) and ["What every rule set forces"](../0151-user-credential-delivery/change.md#what-every-rule-set-forces) |
| Request layout | What an add-on registers for a host's API: the flavour, `apiBase`, `restPrefix`, the GraphQL paths, the uploads path, and the default preset. | [`0151-user-credential-delivery`, "Terms"](../0151-user-credential-delivery/change.md#terms) |
| Token trust record | The one record of everything that decides where a stored token can go and who can read it. Any change to it makes the stored token unusable until the user acts again. | [`0151-user-credential-delivery`, "Token trust record"](../0151-user-credential-delivery/change.md#token-trust-record) |
| Delivery and delivery ceiling | Under `delivery: inject`, `netagent` attaches the token and the runtime holds only a placeholder. Under `delivery: file`, the runtime reads the token from a file. A link provider's `delivery` is the ceiling its bindings may use. | [`0151-user-credential-delivery`, "Terms"](../0151-user-credential-delivery/change.md#terms) |
| `user_act_required` | The state of a stored token that needs a new user act, here a relink. | [`0151-user-credential-delivery`, "Terms"](../0151-user-credential-delivery/change.md#terms) |
| Mint and mint path | A mint is a call in which the Token Service obtains a short-lived token from a provider. The mint path is the route those calls take, through `lenny-egress` under a decision, with bounds on time, response size, and redirects. Mint endpoints are the hosts each provider declares for its mints. | [`0151-user-credential-delivery`, "Terms from other designs and the platform"](../0151-user-credential-delivery/change.md#terms-from-other-designs-and-the-platform) |
| `mint-scoped` | A provider capability level of the egress design: the provider can mint a token limited to named repositories. A mint-scoped read token is one such token with read access. | [`0151-user-credential-delivery`, "Terms from other designs and the platform"](../0151-user-credential-delivery/change.md#terms-from-other-designs-and-the-platform) |
| `attach`, `cacheUntil`, and `handling` | `attach` is the header a credential is attached as. `cacheUntil` bounds how long `netagent` holds a credential before asking again. `handling` lists steps `netagent` must implement to use it, such as `reauth-on-401`, a single retry with a new mint after a 401. | [`0151-user-credential-delivery`, "Terms from other designs and the platform"](../0151-user-credential-delivery/change.md#terms-from-other-designs-and-the-platform) |
| Evidence streams and decision identifier | Append-only records, including `egress.local` for refusals `netagent` makes itself, `credential.mint` for each token release or mint, and `git.operation` for git operations. A refusal returned to the agent carries a decision identifier that finds its record. | [`0127-egress-and-git`, "Evidence streams"](../0127-egress-and-git/change.md#evidence-streams) |
| VCS pool | A pool that names repository prefixes and a provider credential. `userAuthorization: required` makes each repository need the user's own access, `prAuthoring: user` has pull requests authored as the user, and `pushIdentity` decides whose token carries git pushes (`user` or `app`). | [`0152-user-git-identity`, "Terms"](../0152-user-git-identity/change.md#terms) |
| VCS kind | A link provider kind that a VCS add-on registers, such as `github`, `github-enterprise`, or `gitlab`. | [`0152-user-git-identity`, "Terms"](../0152-user-git-identity/change.md#terms) |
| Repository entries | The repositories a session declares, each with a `state`. `granted` marks one the session may write, and `pendingApproval` one that waits on a repository write approval. | [`0151-user-credential-delivery`, "Terms from other designs and the platform"](../0151-user-credential-delivery/change.md#terms-from-other-designs-and-the-platform) |
| Release, release check, and verdict | A release is the Token Service handing a token to a session over the pod credential API. A release check is an add-on's check that the Token Service runs before each release and each mint. | [`0151-user-credential-delivery`, "Terms"](../0151-user-credential-delivery/change.md#terms) |
| The check and verdict | The VCS layer's repository check, its release check, which asks the provider with the user's parent token whether the user holds the declared access. Its verdict is `allowed`, `refused`, or `unavailable`. | [`0152-user-git-identity`, "The check"](../0152-user-git-identity/change.md#the-check) |
| Covered host and git paths T and P | A covered host is a git host for which `netagent` terminates TLS and runs the git path: a host that a VCS pool or a git route of the tenant names, read across all of the tenant's environments, or an alias host of that provider that shipped host data lists, such as `gist.github.com` ([`0127-egress-and-git`, "Terms"](../0127-egress-and-git/change.md#terms)). A VCS kind's API hosts are named separately, as [`0152-user-git-identity`, "Terms"](../0152-user-git-identity/change.md#terms) states. Git path T is the settled git configuration, in which git's proxy points at `netagent` and remotes keep their upstream URLs. P is an alternative measured beside it, which rewrites `https://` remotes to `http://` through a loopback proxy. | [`0152-user-git-identity`, "Git remotes and client tools"](../0152-user-git-identity/change.md#git-remotes-and-client-tools) |
| Smart HTTP and its classification | Git's HTTP protocol (`info/refs`, `git-upload-pack`, and `git-receive-pack`). `netagent` classifies these paths, and LFS paths, on every terminated host before anything else and sends them down the git path. | [`0127-egress-and-git`, "Git on covered hosts"](../0127-egress-and-git/change.md#git-on-covered-hosts-netagent) |
| Read path and upstream route | Under the egress design, a git fetch takes the read path, to the git endpoint the tenant's git routes name for the host: the mirror, a deployer git service, or the upstream host through `lenny-egress`. The upstream route is the route to the host itself. | [`0127-egress-and-git`, "Git on covered hosts"](../0127-egress-and-git/change.md#git-on-covered-hosts-netagent) |
| LFS rule | `netagent` removes credential headers from each action in an LFS batch response and holds their values, and it attaches a held value only to a transfer that matches the live action's method, host, repository, and path. | [`0127-egress-and-git`, "Git on covered hosts"](../0127-egress-and-git/change.md#git-on-covered-hosts-netagent) |
| Write scope and the App write token | The egress design's write path, on which a push carries a write token minted from the tenant's App for repositories the session declared writable, while fetches take the read path. | [`0127-egress-and-git`, "Credentials"](../0127-egress-and-git/change.md#credentials) |
| `caller_type` | The field on a session row that records whether the caller is `human`. A delegated child's row copies its root's value. | [`0151-user-credential-delivery`, "Security properties"](../0151-user-credential-delivery/change.md#security-properties) |
| Delegation and delegation lease | A session can start child sessions, which form a delegation tree. A delegation lease records what a child receives from its parent. | [`0151-user-credential-delivery`, "Terms from other designs and the platform"](../0151-user-credential-delivery/change.md#terms-from-other-designs-and-the-platform) |
| Service identities | Identities a VCS pool lists for automated sessions, which have no user and keep the App write token. | [`0152-user-git-identity`, "Service identities"](../0152-user-git-identity/change.md#service-identities) |
| Impersonation bearer | A token a `platform-admin` obtains to act as a user. | [`0151-user-credential-delivery`, "Terms from other designs and the platform"](../0151-user-credential-delivery/change.md#terms-from-other-designs-and-the-platform) |
| Workspace plan, adapter, and materialization helper | The workspace plan is the part of a session request that declares its sources, such as the repositories to clone and the ref to check out. The adapter is Lenny's process in each agent pod, in its own container beside the slot containers that run the agent runtime. It runs no git on a session's repositories: `lenny-materialize`, the materialization helper, clones the plan's repositories as the slot's UID, fetching through `netagent` under a fetch grant the adapter registers. | [§14](../../spec/14_workspace-plan-schema.md#14-workspace-plan-schema) for the plan, [§3](../../spec/03_high-level-architecture.md) for the adapter, and [`0153-workspace-materialization`](../0153-workspace-materialization/change.md) for the helper |
| Warm pool and recycling | A warm pool keeps pods started ahead of sessions. Recycling returns a pod to its pool for a later session after one ends. | [§6](../../spec/06_warm-pod-model.md#6-warm-pod-model), and [`0151-user-credential-delivery`, "Limits under both deliveries"](../0151-user-credential-delivery/change.md#limits-under-both-deliveries) for why a `userCredential` binding refuses recycled pods |
| Posture | The read-only report of a tenant's security-relevant settings. | [`0151-user-credential-delivery`, "Terms from other designs and the platform"](../0151-user-credential-delivery/change.md#terms-from-other-designs-and-the-platform) |
| Outbound connector and outbound host | An outbound connector is a tenant-owned record naming one customer network, and an outbound host is a host it serves, with the certificate authority (CA) that validates it and `allowMirror`, which decides whether the mirror may fetch it. | [`0148-tenant-outbound-connector`, "Terms"](../0148-tenant-outbound-connector/change.md#terms) |

### Labels from other designs

Proposals, properties, and requirements in other designs carry a label whose prefix names the design. This document cites them by label and links each citation to its definition. The table names what each cited label stands for.

| Labels | Design | What they name |
| :-- | :-- | :-- |
| UC-2, UC-4, and UC-7 | [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) | Proposals of the generic design: UC-2 the `oauth2Link` source and account linking, UC-4 request rules and presets, and UC-7 add-ons. |
| UCP-2 and UCP-8 | [`0151-user-credential-delivery`, "Security properties"](../0151-user-credential-delivery/change.md#security-properties) | Security properties of the generic design: UCP-2 refuses a `userCredential` binding on a pod that can serve another session, and UCP-8 gives a delegated child a `userCredential` binding only when its delegation lease names it. |
| UG-1, UG-2, UG-3, and UG-4 | [`0152-user-git-identity`](../0152-user-git-identity/change.md) | Proposals of the VCS layer: UG-1 VCS link kinds, UG-2 repository authorization, UG-3 pull request authoring, and UG-4 the independent approval check. |
| UR-5, UR-6, and UR-7 | [`0152-user-git-identity`, "Personas and requirements"](../0152-user-git-identity/change.md#personas-and-requirements) | Requirements of the VCS layer: UR-5 any `gh` command works within the token, UR-6 no agent approves or merges through an operation the preset names, and UR-7 automated sessions keep working under a service identity. |
| SP11 | [`0142-security-oversight`, "Security properties"](../0142-security-oversight/change.md#security-properties) | A repository marked for approval receives no write token until its approval exists. |
| C6, C9, and C10 | [`0148-tenant-outbound-connector`, "Security properties"](../0148-tenant-outbound-connector/change.md#security-properties) | Outbound connector properties: C6 keeps traffic TLS end to end, C9 treats content from a customer network as hostile, and C10 fails closed when a connector fails. |
| D1 | [`0151-user-credential-delivery`, "Decisions"](../0151-user-credential-delivery/decisions.md#decisions) | The owner decision, recorded as that design's decision 1, that replaced the allowlist with generic bindings and request rules held as data. |

## High-level design

An agent in a Lenny session can open pull requests on GitHub. When it does so with the tenant's GitHub App, GitHub sees the App as the author and the pusher, so the person who started the session can approve the agent's work as if someone else had written it, which defeats two-person review. This add-on lets the agent act on GitHub as that person instead. The person links their GitHub account once. For each session, Lenny asks GitHub for short-lived tokens derived from that link that reach only the repositories the session may write, and `netagent`, the proxy in the agent's pod, attaches them to the agent's requests, so the agent never sees them. One token carries the agent's pull request work through GitHub's API, and by default a second carries its `git push`, so GitHub records the person as the author and the pusher and applies its own review rules to them. Before any of this, the add-on checks with the person's own token that the person can reach each repository the session declares.

The token that opens pull requests can also approve them, and GitHub offers no narrower permission. Lenny therefore refuses, by name and before the request leaves the pod, the GitHub operations that review, merge, or change protections. That list is a deny list: an operation GitHub adds later passes until the list names it. The same design covers GitHub Enterprise Server, where it is untested.

The rest of this section states each part in the design's terms, which "Terms" above defines. The add-on builds on the generic mechanism of [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) and implements the VCS layer of [`0152-user-git-identity`](../0152-user-git-identity/change.md).

**Kinds and the check (GH-1).** The add-on registers the `github` kind for `github.com` and `ghe.com` and the `github-enterprise` kind for GHES, as VCS link kinds under [UG-1](../0152-user-git-identity/change.md#ug-1-vcs-link-kinds). Each link provider names a tenant's own GitHub App, and the kind block holds the host. The kind fixes the OAuth endpoints, the API hosts, and `attach` from the host, requires expiring user tokens, checks `allowedAccounts` against the App's installations, and reads verified email addresses for a link provider with `identityBinding: verifiedEmail`. Its release check, the repository check of [UG-2](../0152-user-git-identity/change.md#ug-2-repository-authorization), reads `GET /repos/{owner}/{repo}` with the user's parent token and classifies GitHub's single sign-on header.

**Pull request authoring (GH-2).** The `github-scoped` template derives a child token from the parent through `POST /applications/{client_id}/token/scoped`, limited to the session's writable repositories whose entry `state` is `granted` and to `pull_requests: write` and `contents: read` by default. Each session mints its own API child, and, on a VCS pool with `pushIdentity: user`, a second git child with `contents: write` alone that carries pushes, so GitHub counts the user as the pusher. Session end revokes both.

**The `github-deny` preset (GH-3).** The preset refuses review, merge, content and ref, deployment, policy, and credential operations by GraphQL mutation name and REST route, on GitHub's request layouts. The host refuses what the child does not permit, so the preset and the child's permissions form two layers, except where a tenant widens the permissions.

```
 Browser ──(link flow, once)──> Gateway ──> Token Service ──(exchange, check, mint children)──> GitHub
                                                 ▲                                (through lenny-egress)
                                                 │ pod credential API: API child, git child, github-deny
 Pod: gh ──(placeholder)──> netagent ──(API child attached, github-deny applied)──> lenny-egress ──> GitHub API
      git push ───────────> netagent ──(git child attached)───────────────────────> lenny-egress ──> GitHub git
```

Two limits frame the result. `github-deny` is a deny list, so a GitHub operation that can approve or merge and that the preset does not yet name passes until the preset is updated. A compromised `netagent` holds the session's children and can approve and merge with them, as "Residual risks" states.

## Detailed design

### GH-1: The `github` and `github-enterprise` kinds

#### Kind block and endpoints

```yaml
linkProviders:
  - name: github-acme
    kind: github                   # or github-enterprise for GHES
    delivery: inject
    clientId: "..."                # the tenant's GitHub App
    clientSecret: "..."
    identityBinding: directory
    allowedAccounts: [acme]        # organizations, checked through the App's installations
    narrowing: {name: github-scoped, permissions: {pull_requests: write, contents: read}}
    github:                        # the block key matches the kind
      host: github.com
```

The block holds `host` alone, and it is in the token trust record. The kind derives the rest from the host:

| Step | GitHub and GHES **[documented]** |
| :-- | :-- |
| Authorize | `https://<host>/login/oauth/authorize` |
| Exchange and refresh | `https://<host>/login/oauth/access_token` |
| Access token lifetime | 8 hours, and the refresh token 6 months |
| User | `GET /user`, and `/api/v3/user` on GHES |
| Revoke | `DELETE /applications/{client_id}/grant` or `/token`, with Basic authentication |
| Application registration | A GitHub App with Lenny's callback URL |

- **Refresh.** GitHub says: "Once you use a refresh token, that refresh token and the old user access token will no longer work." **[documented]**, which is the case the generic design's refresh lease, a row lease under which one Token Service replica at a time refreshes a link, serves ([`0151-user-credential-delivery`, "Refresh"](../0151-user-credential-delivery/change.md#refresh)). A refresh token idle for 6 months lapses, and the link moves to `user_act_required`.
- **`allowedAccounts`.** The kind checks the listed organizations through `GET /user/installations` **[documented]**.
- **Verified email.** The kind reads `GET /user/emails`, which has `verified` and `primary` fields **[documented]**. S6 found that it answers for every child of an App holding the email addresses permission, and whether one verified email can sit on two accounts was not tested.
- **Mint endpoints.** The kind declares the token path on `github.com` among the mint endpoints of [`0127-egress-and-git`](../0127-egress-and-git/change.md), since today they name `api.github.com` **[design]**.

On GitHub, a VCS pool's provider credential is the tenant's GitHub App, which mints installation tokens. A link provider's App need not be the App in the VCS pool, because the check needs only the user's access. A tenant that wants repository authorization ([UG-2](../0152-user-git-identity/change.md#ug-2-repository-authorization)) alone should register a separate App with `metadata: read` and no other permission, installed on the same organizations, which leaves the stored parent token with no write power at all. Pull request authoring ([UG-3](../0152-user-git-identity/change.md#ug-3-pull-request-authoring)) needs an App with `pull_requests: write` and `contents: read`. GitHub limits a user token to the App's permissions and to accounts where the App is installed **[documented]**, so a repository the linking App cannot see answers as `refused`. The App's user token expiry must stay on. A token response with no `expires_in` or `refresh_token` is refused at link with `LINK_TOKEN_NON_EXPIRING`, because GitHub lets an App opt out **[documented]**.

#### The repository check on GitHub

| Host | Read | Write | Tag |
| :-- | :-- | :-- | :-- |
| GitHub and GHES | `GET /repos/{owner}/{repo}` returning 200, with `permissions.pull` | `permissions.push` | **[documented]** for the fields. S6 measured that they report the user's role, including an organization base permission, whatever the token's narrowing **[measured]**. That is what the check needs, because it runs with the parent. Nothing in Lenny reads these fields to learn what a child can do. `GET /user/installations/{installation_id}/repositories` documents "the access the user has to each repository" and is the fallback. |

The kind adds one classification to the list in [`0152-user-git-identity`, "The check"](../0152-user-git-identity/change.md#the-check):

- A 403 with GitHub's `X-GitHub-SSO` header is `refused` with the reason `sso_required`, which the error shows so Alice can start a SAML session and retry (spike S4 under "Validation owed" in `steps.md`).

### GH-2: Pull request authoring on GitHub

#### The `github-scoped` template

```yaml
narrowing:
  name: github-scoped
  followsEntryState: true
  request:
    method: POST
    url: "{{apiBase}}/applications/{{clientId}}/token/scoped"
    auth: basicClient
    json:
      access_token: "{{parentToken}}"
      target_id: "{{targetId}}"
      repository_ids: "{{sessionRepositoryIds}}"
      permissions: "{{permissions}}"
  permissions: {pull_requests: write, contents: read}   # the one field a tenant overrides on a VCS kind
  response: {token: "$.token", expiresAt: "$.expires_at"}
  revoke: {method: DELETE, url: "{{apiBase}}/applications/{{clientId}}/token", auth: basicClient, json: {access_token: "{{token}}"}}
```

- The template rests on documented endpoints **[documented]**, and S1, S2, S5, and S15 measured it live.
- The template sets `followsEntryState: true`, so the add-on's template variables `{{targetId}}` and `{{sessionRepositoryIds}}` carry the session's writable repositories whose entry `state` is `granted` on each mint.
- `permissions` is the one field a tenant overrides, and it is exempt from the token trust record, because it limits what a child can do at the host within what the App holds. A widening is audited and reported in posture, and the owner settled it under [decision 5](decisions.md#decisions), which states what then relies on the deny preset alone.

#### Child token and the session's repositories

The Token Service holds the parent and mints one child for each session's use, through the `github-scoped` template above. These steps are that template's run on this provider.

1. `netagent` asks the pod credential API for the binding, on its first request to that host.
2. The Token Service verifies the pod and session, rejects a refused pod model (one that [`0152-user-git-identity`, "Pod models"](../0152-user-git-identity/change.md#pod-models) refuses because it could carry one user's state into another session) or an impersonated session, runs the check for each repository, and asks the egress decision API.
3. It selects the session's writable entries whose `state` is `granted`. Per [`0142-security-oversight` decision 25](../0142-security-oversight/decisions.md#decisions), an entry in state `pendingApproval` is absent, so [SP11](../0142-security-oversight/change.md#security-properties) holds, and a repository the session declared read-only is absent too, because one token has one permission set. **An empty set mints no child:** `netagent` answers the request with 403, a decision identifier, and the reason `no_granted_repositories`.
4. It calls `POST /applications/{client_id}/token/scoped` through the mint path with Basic authentication using the client ID and secret, `access_token`, `target_id`, `repository_ids`, and `permissions` **[documented]**.
5. It answers with the secret, `expiresAt` from the response's `expires_at`, `cacheUntil`, the authoritative `hosts` and `attach`, the resolved request rules, and `handling` naming the rule evaluator's minimum version and `reauth-on-401`.

A token has one `target`, so a session whose writable entries in state `granted` span two owners is refused on the second owner's binding with `USER_TOKEN_MULTI_OWNER`. `gh pr create --head alice:feature` still works when that head already exists, because it sends `headRefName: "alice:feature"` to `createPullRequest` on the base **[captured]**, and the child needs only the base. A branch pushed to a `fork` remote produced a head of `alice:<branch>` under git paths T and P **[measured]**. `gh` offers an automatic fork at an interactive prompt alone, and a non-interactive run stops with "you must first push the current branch to a remote, or use the --head flag" **[source]**, and whether a child permits a fork is not established (S15).

#### Child tokens per session

The owner settled decision 1 after S2: each session gets its own children, and Lenny sets no limit of its own on how many a person holds. S2 found no per-user limit on this endpoint: 50 children of one parent in one hour all stayed valid, with no revocation and no browser prompt **[measured]**. GitHub's "ten tokens per user, application, and scope" rule does not apply to these tokens.

- **Children per session.** A session holds at most two children per binding: an API child under the template's `permissions`, and, on a VCS pool with `pushIdentity: user`, a git child with `contents: write` alone on the same repositories, minted at the session's first write. The git child replaces the App write token on the write-scope path of [`0127-egress-and-git`, "Credentials"](../0127-egress-and-git/change.md#credentials) and on nothing else: `git-receive-pack`, its `info/refs?service=git-receive-pack` advertisement, and an LFS `upload` batch request, on the upstream route to the session's writable repositories. Fetches keep the egress design's read path, and the mirror never receives a git child. An LFS transfer carries only the value held from the batch response's action, under the egress design's LFS rule, so the git child never reaches a storage host. The API child goes on everything else the binding covers. On `github.com` and `ghe.com` the git host and the API host differ. On GHES they share a host, and the egress design's classification of smart HTTP paths, which runs before anything else and refuses a non-canonical marker path, keeps each child on its own requests. The API child never holds `contents: write` unless a tenant widens the template, so the host keeps refusing merges through the API (S15). That a child with `contents: write` alone can push and makes GitHub count the user as pusher is **[design]**: S3 measured a child holding `contents: write` and `pull_requests: write`, and S3c owes the narrower one.
- **No fallback to the App.** On a VCS pool with `pushIdentity: user`, a write from a session whose `caller_type` is `human` that holds no git child is refused with 403 and a decision identifier, and never takes an App write token. That covers a delegated child session whose lease does not name the user binding, which keeps its parent's writable set under the egress design and has no user token under [UCP-8](../0151-user-credential-delivery/change.md#security-properties), and a session whose link cannot mint. A delegated child session whose lease names the binding mints its own child tokens, under [`0152-user-git-identity`, "The attach rule"](../0152-user-git-identity/change.md#the-attach-rule). A listed service session has no user and keeps the App write token, as that document's ["Service identities"](../0152-user-git-identity/change.md#service-identities) states, so automated sessions keep pushing ([UR-7](../0152-user-git-identity/change.md#personas-and-requirements)).
- **What the user sees from the split.** Under the default `github-scoped` permissions, every API request that needs `contents: write` is refused by the host, even though the same session can push the same change through git. That covers merging, which is the purpose, and also `gh release create` and release uploads, file writes through `gh api` contents routes, `createCommitOnBranch` (which some tools use to make commits through the API instead of git), and branch creation or deletion through the refs API, including `gh pr merge --delete-branch` and a head branch a tool creates through the API. `gh pr update-branch` is refused by the preset already. A push that adds or changes a file under `.github/workflows/` is refused by GitHub because neither child holds `workflows: write`, and whether a branch that merges in someone else's workflow change is refused too is owed under S3c. A human session whose git child cannot be minted, such as a delegated child session whose lease omits the user binding, can call the API yet cannot push. Each refusal carries GitHub's message or Lenny's decision identifier, and a tenant that needs one of these operations widens the template under decision 5, at the cost that decision names. **[design]**, on **[documented]** permissions
- **Cache.** Keyed on tenant, session, `link_ref`, the link's `generation` (which each refresh increments), sorted repository identifiers, permissions, and target, so no two sessions share a child. Stored as [§4.3](../../spec/04_system-components.md#43-token-service) caches access tokens, encrypted in Redis, so replicas share it, and sealed with the full key and the token trust record's digest as additional data, so a replica copying an entry to another key produces a value that fails to open. A session whose granted set changes mints a new child for the new set and revokes the old one.
- **Limits.** Lenny has no per-person or per-link budget, which decision 7 reopens because GitHub's limit of 2,000 per App is shared by all of a tenant's users. When GitHub refuses a mint, the refusal is the result: a rate-limit answer surfaces as a retryable `USER_TOKEN_RATE_LIMITED`, and any other refusal as a 503 with a decision identifier and GitHub's status. The Token Service counts token requests per App, refreshes included, against GitHub's 2,000 per hour and alerts near it, beside the egress design's counter of installation token mints per App. The counter is per App, and each tenant registers its own App ([`0151-user-credential-delivery` decision 39](../0151-user-credential-delivery/decisions.md#decisions)), so it is a per-tenant figure. A tenant that starts more than about 1,000 pull request (PR) authoring sessions an hour that each push reaches it, and so can one person whose injected agent fans out through delegation, which stops every other user's mints for the rest of the hour. S2 did not test what GitHub does there.
- **Revocation.** Session end revokes the session's children at the provider, which S5 showed leaves the parent and other children valid **[measured]**. `DELETE /applications/{client_id}/token` takes the child itself, so a failed revocation is retried from a durable record in the Token Service's store that holds the child sealed as a link's tokens are (data class Secret under [§12.9](../../spec/12_storage-architecture.md#129-data-classification), the class [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) names, with envelope encryption), deleted once the revocation succeeds or the child's 8 hours have passed (S1). Each failure is logged.
- **On 401.** A child revoked outside Lenny, by the person, an organization owner, or GitHub, appears as a 401, which `reauth-on-401` covers once with a new mint, then surfaces as a 503 with a decision identifier and an alert.

#### Pushes as the user

`require_last_push_approval` means "the most recent push must be approved by someone other than the person who pushed it" **[documented]**. If pushes come from the App, GitHub sees the App as the pusher. The failure is this: Alice's agent pushes to Bob's pull request, and Alice approves it, with her approval counting because she is not the pusher and not the author. She has approved work she caused. The same applies to Bob approving Alice's pull request after his own agent pushes to it.

S3 measured the rule live **[measured]**. A push with an installation token leaves the person who requested it unseen as pusher, so she can approve afterwards and GitHub reports the pull request mergeable. A push with her user token, or with a scoped child of it, makes GitHub treat her as the pusher, and her approval no longer satisfies the rule. A scoped child can push.

**The design, which the owner settled under [`0152-user-git-identity` decision 10](../0152-user-git-identity/decisions.md#decisions):**

1. **A session pushes as its user by default.** On a VCS pool with `prAuthoring: user`, the pool's `pushIdentity` setting defaults to `user`, and pushes use the session's git child from "Child tokens per session". GitHub then enforces `require_last_push_approval` natively: if Alice's agent pushes to Bob's pull request, Alice's approval does not count while that push is the most recent. A later push by anyone else restores her approval's effect, because GitHub's rule reads the latest push alone.
2. **Lenny sets no branch rules**, as [`0152-user-git-identity`, "Push identity"](../0152-user-git-identity/change.md#push-identity), states, so the repository's own branch protection and rulesets apply to the agent as they apply to her.
3. Revision 3 of the VCS layer had the filter refuse a pull request create whose head branch lay in another user's namespace. That input check went with the allowlist, and with user pushes it is unneeded: a colluding session that opens a pull request from a branch Alice's agent pushed leaves Alice as the most recent pusher, so her approval does not count when the repository requires approval of the most recent push.
4. **A VCS pool's `pushIdentity: app`** keeps pushes on repository-limited App tokens, for a tenant that does not want a user-derived token with `contents: write` in `netagent`. GitHub then sees the App as the pusher, which is why the VCS layer requires [UG-4](../0152-user-git-identity/change.md#ug-4-independent-approval-check), the independent approval check, for that setting.

Two-person review on pushed work rests on the repository requiring approval of the most recent push, and holds only while the agent's push is the latest. A tenant that needs every pusher excluded runs UG-4, which is available on any VCS pool. Lenny cannot set that rule, and a read of branch protection needs repository administration. The setup flow states the dependency, and posture reports it where the App can read the rule.

### GH-3: The `github-deny` preset

#### Request layout

The first column names a VCS pool's `provider` field and host. The egress design defines `provider` as "The provider, such as `github`, whose capability level decides what Lenny mints and allows", and a pool on any GitHub host has `provider: github`. The flavour derives from the host, and the link provider's kind follows the same split: `kind: github` for `github.com` and `ghe.com`, and `kind: github-enterprise` for GHES.

| Provider and host | Flavour | API host | `apiBase` | `restPrefix` | `graphqlPaths` | Uploads | `attach` |
| :-- | :-- | :-- | :-- | :-- | :-- | :-- | :-- |
| `github`, `github.com` | `github` | `api.github.com` | `https://api.github.com` | `/` | `/graphql` | `uploads.github.com`, which needs its own binding | `Authorization: token {{secret}}` |
| `github`, `<name>.ghe.com` | `github` | `api.<name>.ghe.com` | `https://api.<name>.ghe.com` | `/` | `/graphql` | The host `upload_url` names, which is owed | `Authorization: token {{secret}}` |
| `github`, any other host | `github-enterprise` | The host | `https://<host>/api/v3` | `/api/v3/` | `/api/graphql` | `/api/uploads/repos/*/*/releases/*/assets`, admitting a query string and a binary body **[measured]** | `Authorization: token {{secret}}` |

This covers GHES with no stored input, because every `provider: github` host outside `github.com` and `*.ghe.com` is GHES. The `vcs.github.write` lease scope of [§14](../../spec/14_workspace-plan-schema.md#14-workspace-plan-schema), which a workspace plan's `gitClone` source names when the session will push back to the remote, still covers it, and §14 is unchanged. When a binding names `github-deny` explicitly, the preset uses the `github` flavour's paths on `api.github.com` and `api.<name>.ghe.com`, and the `github-enterprise` flavour's paths on any other host.

#### Preset body

```yaml
- name: github-deny
  version: 8
  flavours: [github, github-enterprise]
  repoRoots: [/repos/*/*, /repositories/*]   # rules below name {repo}
```

The properties below are request form fields of the preset format, which [`0151-user-credential-delivery`, "Request forms belong to a preset"](../0151-user-credential-delivery/change.md#request-forms-belong-to-a-preset) defines.

| Property | Meaning | `github-deny` |
| :-- | :-- | :-- |
| `writeForm` | Query strings and body types admitted on a write | No query string, and JSON alone |
| `encodedSegments` | Segments where `%` is admitted | `contents`, `branches`, `labels`, `compare`, `git/ref`, `git/refs`, `git/matching-refs`, `releases/tags`, `commits`, and `environments` values |
| `refuseFamilies` | Whether the GraphQL and API family rule applies | Yes |
| `denyBodyKeys` | JSON keys refused on a write, at any depth | None |
| `undecodableBody` | What a write `bodyPatterns` cannot decode does | Refused |

**The body is the extended preset, which the owner settled under decision 8.** Each REST rule below `{repo}` applies under both root forms, because GitHub serves `/repositories/{id}/...` as well.

| Family | `github-deny`, GraphQL mutations | `github-deny`, REST writes |
| :-- | :-- | :-- |
| Review | `addPullRequestReview`, `submitPullRequestReview`, `updatePullRequestReview`, `deletePullRequestReview`, `dismissPullRequestReview` | `{repo}/pulls/*/reviews/**` |
| Merge | `mergePullRequest`, `enablePullRequestAutoMerge`, `disablePullRequestAutoMerge`, `enqueuePullRequest`, `dequeuePullRequest`, `mergeBranch`, `updatePullRequestBranch`, `revertPullRequest` | `{repo}/pulls/*/merge`, `merge-async`, `update-branch`, `{repo}/merges`, `{repo}/merge-upstream` |
| Content and ref | `createCommitOnBranch`, `createRef`, `updateRef`, `updateRefs`, `deleteRef`, `createLinkedBranch` | `{repo}/contents/**`, `{repo}/git/refs/**`, `{repo}/git/commits`, `trees`, `tags` |
| Deployment | `approveDeployments`, `rejectDeployments`, `createEnvironment`, `updateEnvironment`, `deleteEnvironment`, `pinEnvironment`, `reorderEnvironment` | `{repo}/actions/runs/*/pending_deployments`, `approve`, `deployment_protection_rule`, `force-cancel`, `{repo}/environments/*` on `PUT` and `DELETE`, `{repo}/environments/*/deployment-branch-policies/**`, and `{repo}/environments/*/deployment_protection_rules/**` |
| Policy | `createBranchProtectionRule`, `updateBranchProtectionRule`, `deleteBranchProtectionRule`, `createRepositoryRuleset`, `updateRepositoryRuleset`, `deleteRepositoryRuleset` | `{repo}/branches/**`, `{repo}/rulesets/**`, `/orgs/*/rulesets/**`, `{repo}/actions/permissions/**` |
| Credential | None named | `/user/keys/**`, `/user/gpg_keys/**`, `/user/ssh_signing_keys/**`, and `{repo}/keys/**` |

**Evidence for `github-deny` [measured].** A name search over GitHub's OpenAPI description (`rest-api-description`, `info.version` 1.1.4) and `schema.docs.graphql`, followed by manual reading, listed 72 write routes and 48 mutations. It is a starting list rather than a proof of completeness. **This table refused 94 of the 120 on each flavour, and 26 reach,** as the list predicted. The 5 environment secret and variable routes reach, `POST .../issues/{n}/suggestions/{id}/approve` reaches, and `createEnvironment`, `pinEnvironment`, and `reorderEnvironment` are refused. `branches/a/b/protection`, `branches/main%2Fprotection`, and `POST .../branches` are refused, with `GET` passing. Writes to each credential tail were refused, with `GET` passing. `POST` and `PATCH .../environments/prod` and writes under `secrets` and `variables` pass, while `PUT` and `DELETE .../environments/prod` and writes under `deployment-branch-policies` and `deployment_protection_rules` are refused. Every REST rule below `{repo}` refused under both roots, on 30 concrete routes, 4 write methods, and 3 host types. `PUT .../contents/pulls/7/merge` is refused by the content family and no longer by the merge rule. The stub proves forwarding only.

**Commands [measured].** 103 `gh` commands on Alpine and Debian. In the table, *Forwarded* counts commands whose writes reached the stub, *Denied refused* those whose denied action was refused, *Other refused* commands refused with no denied action, *Missed* denied actions that reached the stub, and *No write* commands that sent no write to the stub:

| Preset | Forwarded | Denied refused | Other refused | Missed | No write |
| :-- | :-- | :-- | :-- | :-- | :-- |
| Earlier smaller | 69 | 23 | 0 | 0 | 11 |
| **This table, GHES and github.com** | **63** | **23** | **6** | **0** | **11** |

The 6 are `gh issue develop`, `gh repo sync`, `gh repo deploy-key add`, `gh repo deploy-key delete`, `gh api -X PUT .../contents/...`, and `gh api -X DELETE .../branches/main/protection`. Of the 11 with no write, 4 are reads and 7 are stub limits (6 `project` commands and `discussion create`). Coverage is of the commands listed rather than every flag. Outside the matrix, `gh ssh-key add`, `gh ssh-key delete`, and `gh gpg-key add` are refused, and `gh gpg-key delete` did not confirm, because the stub has no key to list, while its raw `DELETE` is refused. **`gh release upload` on GHES and `gh secret` and `gh variable` with `--env`, and their deletes, pass.** `writeForm` refused no `gh` or `glab` command in the matrix.

**Attack rows [measured].** The same 1,230 rows on each of GHES, github.com, and `ghe.com`: **1,069 refused, 160 reached with no denied action, 0 denied actions with the credential**, and 1 denied action with no credential through an opaque tunnel (a connection `netagent` passes through without terminating TLS), which is refused when that host is bound to a generic rule set (rules a binding sets with no preset). With 280 added rows for the added mutations, all refused, the totals are 1,350, 160, 0, and 0 of 1,510. The spike follow-up's 4 denied actions were a `query` naming a denied field or spreading a `Mutation` fragment and `POST /repos/acme/widgets/graphql`, and the rules above closed them. No value reached a wrong host in the audit.

**What the owner accepted.** Under `requestRules.default: allow`, the request rules' default, a new GitHub mutation that can approve or merge passes until the preset is updated. GitHub's schema went from 122 mutations (2021-01) to 278 (now), and in one reading about one a year needs only `pull_requests: write` and carries merge or review power **[source]**. The input-key and target checks are gone, as "What went away" below states, so `updatePullRequest` with `baseRefName` or `state` passes. **Operations that exist today are in the preset's scope and are not part of this acceptance**, which the owner settled with the extended preset under decision 8.

#### Commands against the host

**Passing the rules is not the same as working.** The host then refuses what the token does not permit, and this table separates the two.

| Command | Rules under `github-deny` | The host then, under the default child | Evidence |
| :-- | :-- | :-- | :-- |
| `pr create` | Passes **[measured]** for the flags the 103 commands use, which exclude `--project` | Documented as permitted. `--project` needs a projects permission the child lacks. | The host is S15. |
| `pr view`, `pr list`, `auth status`, and `gh api` reads | Pass **[measured]** | Within `contents: read` and `pull_requests`. Fields on `pr view` are likely refused or partial. | The host is S15. |
| `pr status`, `checks`, `diff`, `repo view`, and `issue view` | Pass, as they name no denied operation. **[captured]** under the allowlist, and `issue view` in the earlier capture alone. Not run under deny mode. | Within `contents: read` and `pull_requests`. `pr checks` and `issue view` are likely refused or partial. | The host is S15. |
| `pr comment`, `edit`, `ready`, `close`, and `reopen` | Pass **[measured]** | Documented under `pull_requests: write` for PR routes. | The host is S15. |
| `issue create`, `run`, `release`, `gist`, and other commands outside the child's permissions | Pass **[measured]**. `release upload` goes to `uploads.github.com`, which needs a binding, and on GHES to the flavour's uploads path, which passes **[measured]**. | Refused, until the template's `permissions` is widened | The host is S15. |
| `secret set --env`, `secret delete --env`, `variable set --env`, and `variable delete --env` | Pass **[measured]** | As the token permits | |
| `issue develop`, `repo sync`, `repo deploy-key add` and `delete`, `ssh-key add` and `delete`, `gpg-key add`, `gh api` contents writes, and `gh api` branch protection writes | Refused **[measured]** under the extended preset, and passing under the earlier smaller one (decision 8). `gpg-key delete` did not confirm, because the stub has no key to list, and its raw `DELETE` is refused. | Refused under `contents: read`, as documented | The host is S15. |
| `pr review` with any event | Refused by `addPullRequestReview` **[measured]** | Would permit it, as documented | The stub proves forwarding only. |
| `pr merge`, `--auto`, merge queue, and `--disable-auto` | Refused by `mergePullRequest`, `enablePullRequestAutoMerge`, and `disablePullRequestAutoMerge` | Merge needs `contents: write`, as documented for REST | **[measured]**, on all 14 merge rows across versions and flavours. |
| `pr merge --delete-branch` | Refused at the mutation first **[captured]** under the allowlist | Not reached | Not run under deny mode. |
| `pr update-branch`, `pr revert` | Refused by `updatePullRequestBranch` and `revertPullRequest` | Not reached | **[source]**. |
| `gh api graphql` with an aliased refused mutation | Refused **[measured]** | Not reached | |
| A new `gh` command using a mutation the preset does not name | Passes | As the token permits | **[design]**. This is the accepted cost. |

**Command result [measured].** The counts under the final preset are under "Preset body", **Commands [measured]**. **The stub proves forwarding only.**

**Version drift [measured].** Under the final preset, all 7 versions on Alpine and Debian, on both flavours, 12 rows each: 8 writes forwarded, merge refused, and 0 others refused at every version. The only non-zero exit other than merge is `pr edit` with metadata on 2.86.0 and 2.87.0 on github.com, on a stub limit with no refusal. An earlier run against the allowlist, on the Alpine client:

| `gh` | Deny: refused other than merge | Allowlist: refused other than merge |
| :-- | :-- | :-- |
| 2.72.0, 2.73.0 | 0 | 8, including `pr create --reviewer --assignee` |
| 2.86.0, 2.87.0, 2.93.0, 2.94.0, 2.103.3 | 0 | 6 (`pr edit`, `pr comment`) |

The same `pr edit` rows exited 1 on the stub limit in that run. The allowlist there includes the `Time-Zone` fix, which forwarded the `Time-Zone` header `gh` sends on any image with `/etc/localtime` after the allowlist's forwarded-header list had refused every GraphQL request carrying it.

**Widening `permissions`.** A tenant that wants `gh issue` or `gh run` widens the template. **Once `contents: write` is granted, the host no longer refuses merges, `PUT contents`, `createCommitOnBranch`, or ref writes, and the deny preset alone stands between an agent and those operations.** The same holds for `actions: write`, and any new operation in those families passes until the preset names it. The owner settled decision 5 with this stated.

**What went away.** Owner decision D1, recorded as [`0151-user-credential-delivery` decision 1](../0151-user-credential-delivery/decisions.md#decisions), accepted these:

- A new GitHub mutation that can approve or merge passes until the preset is updated.
- The input-key check is gone, so `updatePullRequest` with `baseRefName` or `state` passes. `gh pr edit --base` works, and it can move an approved pull request onto another branch. Whether GitHub keeps approvals across a base change is S16.
- The target check is gone. A REST path naming a repository outside the session is forwarded, and under `github-scoped` the host answers 404 (S15). A link provider of a VCS kind whose `delivery` ceiling is `inject` or `file` must name a template.
- `pr edit`'s partial writes no longer arise from the rules, since its mutations all pass.

#### Comment-triggered actions on GitHub

**GitHub and GHES.** The host has no native comment commands. Repositories run bots that do: Prow's `/lgtm` and `/approve`, merge bots such as Mergify and Bors, Atlantis's `atlantis apply`, and `issue_comment` workflows that gate on `author_association`. (Dependabot retired its merge commands in January 2026 **[documented]**.) A single injected agent commenting `/approve` on Bob's pull request produces an approval under Alice's name wherever such a bot trusts her. Lenny cannot detect these bots, and no regular expression covers their grammars.

Text is not the only trigger. Kodiak merges a pull request carrying its automerge label once branch protection passes, and `labeled` is a documented `pull_request` activity type a workflow can select **[documented]**. `ready_for_review`, `review_requested`, `assigned`, `closed`, and `edited` are selectable too **[documented]**.

[`0152-user-git-identity`, "Comment-triggered actions"](../0152-user-git-identity/change.md#comment-triggered-actions), states the tenant's rules and the bot's own check of `performed_via_github_app`, the field in which GitHub names the App through which an action was performed.

### Configuration by scope

| Setting | Scope | Who sets it | Default |
| :-- | :-- | :-- | :-- |
| `github.host` on a `github` or `github-enterprise` link provider | Link provider | `tenant-admin` | None. A write must set it. |
| `narrowing.permissions` on `github-scoped` | Link provider | `tenant-admin` | `pull_requests: write` and `contents: read` |
| `appTokenRequestsAlertPerHour` | Helm | Platform | 1,600, against GitHub's 2,000 per App |

## Worked examples

### Alice: link to `gh pr create` to Bob's approval

Tenant `acme`, App `acme-lenny` installed on the `acme` organization, VCS pool `github.com/acme/` with `userAuthorization: required` and `prAuthoring: user`. Warm pool `coding` (see "Warm pool and recycling" under "Terms"), which runs Alice's sessions, has `sessionPolicy.maxConcurrentSessionsPerPod: 1` and no recycling, so each pod serves one session. [UCP-2](../0151-user-credential-delivery/change.md#security-properties) refuses a `userCredential` binding on a recycling pool on every runtime class, and admits one on a pool with several slots only where the pool's attribution status records per-slot attribution.

1. Alice calls `POST /v1/sessions` with `acme/web` declared writable. The VCS pool's binding attaches, by [`0152-user-git-identity`, "The attach rule"](../0152-user-git-identity/change.md#the-attach-rule), and it replaces the tenant's App binding on that host for her session. She has no link, so she receives `403 LINKS_REQUIRED` with the link provider and its `linkUrl` under `details.userLinks`, and no pod is claimed. **[design]**
2. Her browser opens the URL, logs in through the deployment's identity broker, which signs her in to Lenny, and receives the link flow's cookie. The link provider sets `identityBinding: directory`, under which the link commits only when the GitHub account's numeric identifier matches the one the tenant recorded for her, and `acme`'s sync worker, the tenant process that records those identifiers, has recorded her provider identifier. It goes to `github.com/login/oauth/authorize` and returns. The gateway hands the code to the Token Service, which exchanges it and reads `GET /user`, and it checks `GET /user/installations` against `allowedAccounts`. The identifier matches, and she confirms `@alice-acme`. **[design]**, on **[documented]** endpoints
3. Alice retries. The Token Service runs `GET /repos/acme/web` with the parent. `permissions.push` is true, and the identifier matches. The gateway writes a writable entry in state `granted`, then runs `ls-refs` to resolve the plan's ref and claims a pod. **[design]**
4. The materialization helper clones as the slot's UID through `netagent`, under the fetch grant the adapter registered for that run. On a mirror route, the egress decision API reads the entry and allows. A mint-scoped read token comes from the pod credential API, with the check run as the Token Service's pre-mint step, before its token cache lookup. **[design]**
5. The agent runs `git checkout -b fix-login`, commits as Alice's noreply address, and runs `git push`. `netagent` reads the ref update, and its pod credential API call mints the session's git child, a child of Alice's link with `contents: write` on `acme/web` alone, under the VCS pool's default `pushIdentity: user`. The push goes through `lenny-egress`, and GitHub records Alice as the pusher (S3). A `git.operation` record is written. **[design]**, the push path is **[measured]** under git path T on a stub, with no user check, and a child's push is **[measured]** live
6. The agent runs `gh pr create --title ... --body ... --head fix-login`. The remote reads `https://github.com/acme/web.git`, as git path T leaves it. `gh` runs because `GH_TOKEN=lenny-injected` **[measured]** **[captured]**. `netagent` terminates TLS for `api.github.com`. Its first pod credential API call for the API host mints the session's API child with `repository_ids: [acme/web]` and `pull_requests: write, contents: read`, and it returns `github-deny` resolved. **[design]**
7. `gh`'s traffic **[captured]**: over HTTP/2, `POST /graphql` with queries `RepositoryInfo` and `PullRequestForBranch`, and on GHES also `GET /api/v3/meta` and `Issue_fields` (`__type` feature detection). Each query passes. Then `mutation PullRequestCreate($input: CreatePullRequestInput!) { createPullRequest(input: $input) { pullRequest { id url } } }` has one root field the preset does not name, so it passes **[measured]**. `netagent` forwards its own printing and re-encoding, with `Authorization` overwritten. The stub received the injected token and no placeholder **[captured]**, and the printing and token overwrite left `gh`'s exit code and operations unchanged **[captured]**. That GitHub creates the pull request with Alice's avatar and the App's badge is **[documented]** and **[stub cannot show]**.
8. `credential.mint` records `tokenSource: userChild`, the `linkRef`, and the hash. **[design]**
9. Alice tries to approve in the GitHub UI, and GitHub refuses because she is the author. Bob approves as a person. The most recent push came from Alice, and Bob is neither the author nor the pusher, so `require_last_push_approval` passes. Bob merges. Two people took part in this merge, because the preset refuses the agent's approval and Alice cannot approve her own.
10. Later, Alice's session pushes a fix to Bob's pull request `bob-cleanup`, which her push rights allow. GitHub records Alice as its most recent pusher, so her approval of Bob's pull request no longer satisfies the rule, and someone other than Alice must approve after her push (S3 **[measured]**).

### A prompt-injected agent

A web page tells Alice's agent to approve PR 7 and merge it. Results under `github-deny` are **[measured]** on the stub, on both flavours and on `ghe.com`, except as marked. What the provider would do without a rule is **[stub cannot show]**.

- `gh pr review 7 --approve`. `gh` reads the PR with a query, which passes. It sends `mutation PullRequestReviewAdd($input: AddPullRequestReviewInput!) { addPullRequestReview(input: $input) { clientMutationId } }`, with the event in variables. The name is on the preset, and `gh` exits 1 with the refusal text. Nothing goes upstream, and `egress.local` records the operation name.
- `gh pr merge 7`, with any mode, sends `PullRequestMerge` with `mergePullRequest`, which is refused the same way. `--auto` and a merge-queue pull request send `enablePullRequestAutoMerge`, which is refused. As documented, the host would also refuse REST merge, since it needs `contents: write` and the child has `contents: read` (S15 tests that and the GraphQL equivalent).
- `gh api graphql -f query='mutation { ok: addPullRequestReview(input: {pullRequestId: "PR_x", event: APPROVE}) { clientMutationId } }'`. The alias is ignored, and an alias named `createPullRequest` is ignored the same way. These variants carry over from the allowlist's rules:
  - `mutation A { ...F } fragment F on Mutation { addPullRequestReview(...) }`, because a mutation's root selections must be fields.
  - Two operations with `operationName` on either one, because a document holds one operation.
  - `[{"query": "..."}]` and `{"query": "query{viewer{login}}", "QUERY": "mutation{...}"}`, on the JSON rules.
  - Gzip, zstd, deflate, form, and `application/graphql` bodies, by the endpoint's single form.
  - `GET /graphql?query=mutation...`, and `query` as a URL parameter on POST.
  - `POST /graphql/`, `POST //graphql`, `POST /graphq%6C`, `POST /graphql.json`, and `POST /graphql/x`: a trailing slash and a doubled slash normalize to the listed path and the document is parsed, the escape is refused, and an unmatched `/graphql*` path is refused under the preset. Each row with a denied mutation was refused, under the strict matcher.
- The bare carriage return (CR) document from [`0151-user-credential-delivery`, "Canonical forwarding"](../0151-user-credential-delivery/change.md#canonical-forwarding). The lexical rule refuses it, and were it to pass, the printer would forward the query alone **[captured]**.
- `gh api -X POST repos/acme/payments/pulls -f title=x -f head=y -f base=main` is forwarded, since target checks are gone. GitHub answers 404, because the child covers only `acme/web` (S15). **Under no template, this would succeed.**
- `gh api -X POST repos/acme/web/pulls/7/reviews -f event=APPROVE`. The preset's REST rule refuses it, `r%65views` is refused on the escape, and `reviews.json` and `reviews.x` are caught by the first-dot stem **[measured]**.
- `gh api graphql` with `createCommitOnBranch`, and `PUT contents` through `gh api`: named by the extended preset, and refused by the host under `contents: read`.
- `gh pr comment 7 --body "/approve"` on a repository running an approval bot posts under Alice's name, unless the tenant added a rule. The bot acts if it trusts her.
- `gh pr close 7` on Bob's pull request succeeds. The action is attributed to Alice with the App's badge, and Bob can reopen it. `gh pr edit 7 --base release` succeeds too, which is what input-key checks used to refuse.
- A mutation GitHub adds next year with merge power passes until the preset names it.
- Repeated refusals on one session raise the `user_credential_rule_refusals` alert, which Dave's alert subscription receives.

### The same on GHES behind an outbound connector

Tenant `acme` runs `ghes.acme.internal` behind connector `acme-dc1`. The link provider has `kind: github-enterprise`, and the outbound host record names Acme's CA and leaves `allowMirror` off.

- Alice's browser reaches GHES on Acme's network, and the provider's redirect comes back to Lenny's public URL, so no inbound rule is needed. The Token Service exchanges the code through `lenny-egress` with `acme` asserted, under a grant (the signed authorization the decision service issues for a connection to an outbound host), and validates against the CA before it sends the client secret. **[design]**, on connector properties [C6](../0148-tenant-outbound-connector/change.md#security-properties) and [C9](../0148-tenant-outbound-connector/change.md#security-properties)
- The VCS pool's host makes it GHES, with the API on the host under `/api/v3/`. `GH_HOST=ghes.acme.internal` and `GH_ENTERPRISE_TOKEN=lenny-injected` are set through the client proxy profile of [`0127-egress-and-git`](../0127-egress-and-git/change.md), the environment and files Lenny injects into the pod, on a tenant-owned pool, and the remote stays `https://ghes.acme.internal/...`. `gh` posts to `https://<host>/api/graphql` and `/api/v3/...` **[captured]**, requests reviewers through `requestReviews`, which takes node identifiers, where on `github.com` it uses `requestReviewsByLogin`, which takes logins, and sends `GET /api/v3/meta` **[captured]**. The stub was no connector, so the connector leg is **[design]**.
- The binding's single host also serves the web and git, so under `requestRules.default: allow` the token attaches to every path on it, including web routes, whose behaviour with that token is not established (S15). Smart HTTP paths take the egress design's git path, which reaches the host through the connector. The flavour names the API prefixes, and `/graphql`, `/api/foo`, and `/api/v3/graphql` are refused on it **[measured]**.
- `gh pr review`, the aliased mutation, and the REST calls behave as above. The scoped endpoint exists on GHES 3.22 **[documented]**, and S14 fixes the oldest supported version.
- **Connector offline.** The check's periodic sweep, a gateway job that repeats the check for running sessions every 300 seconds, and mints answer `unavailable`. New sessions get the retryable error, and running sessions have no mirror store reads, since `allowMirror` is off and a connector host's git stops anyway. Links are untouched. No traffic takes the default route ([C10](../0148-tenant-outbound-connector/change.md#security-properties)).
- GHES disables rate limits by default per the egress design. S2 ran on `github.com` alone, so whether GHES limits children per user is untested, and a refusal there surfaces as GitHub's error under "Child tokens per session".

### Refusals

- `POST /repos/a/b/pulls/1/r%65views`, `.../reviews.json`, `POST /graphq%6C`, and `POST /graphql/x` on a host under `github-deny`: refused **[measured]**, and the stub proves forwarding only. `.../reviews.x` and `POST /repos/a/b/graphql`: refused **[measured]**.

## Documentation

The tenant admin and runtime author pages of [`0152-user-git-identity`, "Documentation"](../0152-user-git-identity/change.md#documentation) carry a GitHub section, and the operator guide carries the Helm value. Together they state every item of this add-on that a reader meets, as decision D13 of [`0131-incremental-checkpoint`](../0131-incremental-checkpoint/decisions.md#decisions) requires of every design:

- **Settings and defaults.** `github.host` on a `github` or `github-enterprise` link provider (no default; a write must set it), `narrowing.permissions` on `github-scoped` (`pull_requests: write` and `contents: read`), what widening it to `contents: write` or `actions: write` gives up, and `appTokenRequestsAlertPerHour` (1,600, against GitHub's 2,000 per App).
- **Provider requirements.** The GitHub App each tenant registers, with the permissions the kinds need, the email addresses permission that `identityBinding: verifiedEmail` needs, and the user access tokens it must issue; `require_last_push_approval` on the repositories where two-person review matters; and that GHES is untested.
- **Limits.** One target account per session's binding (decision 2), each session's own API child and git child revoked at session end, GitHub's 2,000 token requests per hour per App shared by every user of the tenant, and that a child does not narrow account permissions such as the user's email addresses.
- **Failure modes and their answers.** A request `github-deny` refuses, with its decision identifier in `egress.local`; `403 LINKS_REQUIRED` with `details.userLinks`; a relink under `user_act_required`; a refusal GitHub returns past the template's permissions, such as 404 on a read-only declared repository; a SAML SSO refusal marked by `X-GitHub-SSO`; `USER_TOKEN_MULTI_OWNER` when a session's granted writable entries span two owners; `USER_TOKEN_RATE_LIMITED`, retryable, when GitHub rate-limits a child mint, and a 503 with a decision identifier and GitHub's status for any other refused mint; a 503 with a decision identifier and an alert when a child is still refused after `reauth-on-401`'s one new mint; `LINK_TOKEN_NON_EXPIRING` at link when the App has opted out of user token expiry; and the App request alert.
- **Runtime-facing rules.** `gh` runs with `GH_TOKEN=lenny-injected` and `netagent` attaches the real child; the `gh` operations `github-deny` refuses by name, among them review, merge, and branch protection writes, and the substitutes, such as `gh pr comment` for `gh pr review --comment`; pushes and pull requests act as the session's user; and remotes keep their upstream URLs under git path T.

## Tests

### Tier 1: unit

- `github-deny`: each named operation refused on each spelling, and an unnamed operation passing.
- The capture's attack rows as a fixture under deny mode, including aliases, multiple operations, root fragments, batches, duplicate keys at every depth, a bare CR, and deep nesting.
- The per-session cache key, the git child attached only to smart HTTP paths and the API child to the rest, including on a GHES host serving both, the per-App counter and alert, link state transitions, and the page the link flow shows before its redirect to GitHub, which names every endpoint host and states which changed.

### Tier 3: contract

- `github-deny` replayed against recorded `gh` traffic per pinned version, on both flavours, on Alpine and Debian images, and with `__type` answers per supported GHES, with the capture of 2026-11-03 as the first fixture.
- `gh pr edit` with every flag leaves no partial write under the preset.

### Tier 4: integration

- Session end revoking both children, a failed revocation retried from the sealed record, and a changed granted set reminting.
- On a VCS pool with `pushIdentity: user`, a push from a delegated child session whose lease omits the user binding, and from a session whose link cannot mint, refused with no App write token minted.
- The git child never reaching the mirror, a fetch, or the API child's requests, on a GHES host serving git and the API, and an LFS transfer carrying only the action's held value.
- An empty set of writable entries in state `granted` minting no child.

### Tier 6: end to end on cloud

Against a test GitHub organization and GHES: a link, a child token, `gh pr create`, GitHub's enforcement of the child's limits, and refusal of review and merge by the preset.

### Tier 9: security

- Every example under "A prompt-injected agent", and the 522-row attack table and the bypass table re-derived for deny mode (522 rows per flavour under the allowlist, as "Capture history" records, where the deny-mode run under S19 has 1,230 rows per host type), against shipped `netagent` builds.

## Residual risks

- **Compromised `netagent` on GitHub and GHES.** It holds a child with `pull_requests: write`, and GitHub has no narrower permission. It can approve other users' pull requests on the session's repositories until the child expires or is revoked. On a VCS pool with `pushIdentity: user`, it also holds the session's git child with `contents: write`, which it can send to the API to merge an approved pull request as the user. It also holds App tokens, including write. Request rules are enforced in `netagent`, so they provide nothing here. Compromise of `netagent` remains the boundary where two-person review ends, for those repositories.
- **`permissions` widened.** When a tenant widens the narrowing template's `permissions` with `contents: write` or `actions: write`, merges, content writes, and ref writes rest on the preset alone.
- **The user's email addresses.** `GET /user/emails` answers for every child, whatever its permissions **[measured]** (S6) on an App holding the email addresses permission, which a link provider with `identityBinding: verifiedEmail` needs, so an agent can read its user's addresses, private ones included, through the API child. [Decision 6](decisions.md#decisions) asks whether the preset refuses it.
- **Targets and inputs.** Without input-key and target checks, `updatePullRequest` with `baseRefName` or `state`, and any allowed mutation on someone else's pull request, pass where the token permits. Under a binding with `delivery: inject`, the child, when a template exists, limits this to the session's repositories.

## Sources

Read on 2026-11-02 and 2026-11-03. These pages were read and their behaviour was not tested.

- GitHub: [authenticating on behalf of a user](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-with-a-github-app-on-behalf-of-a-user), [refreshing user access tokens](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/refreshing-user-access-tokens), [apps REST, scoped token](https://docs.github.com/en/rest/apps/apps), [the same on GHES 3.22](https://docs.github.com/en/enterprise-server@latest/rest/apps/apps), [OAuth application REST](https://docs.github.com/en/enterprise-server@latest/rest/apps/oauth-applications), [installations REST](https://docs.github.com/en/rest/apps/installations), [permissions required for GitHub Apps](https://docs.github.com/en/rest/authentication/permissions-required-for-github-apps), [REST rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api), [authorizing OAuth apps](https://docs.github.com/en/apps/oauth-apps/using-oauth-apps/authorizing-oauth-apps), [protected branches](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches), [branch protection REST](https://docs.github.com/en/rest/branches/branch-protection), [repositories REST](https://docs.github.com/en/rest/repos/repos), [reviewing Copilot pull requests](https://docs.github.com/en/copilot/how-tos/use-copilot-agents/coding-agent/review-copilot-prs), [issue comments REST](https://docs.github.com/en/rest/issues/comments), [emails REST](https://docs.github.com/en/rest/users/emails), [events that trigger workflows](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows), [the Dependabot comment command change](https://github.blog/changelog/2026-01-27-changes-to-github-dependabot-pull-request-comment-commands/), and [available rules for rulesets](https://docs.github.com/en/enterprise-cloud@latest/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets).
- Capture: `evidence/user-git-identity-design/spike-gh/RESULTS.md`, with `raw/`, of `gh` 2.103.3 (cli/cli tag v2.103.3) and `glab` 1.127.0, gqlparser 2.5.33, graphql-go, graphql-js 17.0.2, graphql-ruby 2.6.14, and `graphql-c_parser` 1.1.4. The folder's `README.md` lists what was left out, which includes binaries, downloads, keys, and bulk logs. Citations in this document such as `raw/gitopt/t11/` are relative to `spike-gh/`, and `t4/table.txt` is at `raw/gitopt/t4/table.txt`.
- `gh` release history: `evidence/user-git-identity-design/gh-history/out/`, read from cli/cli source across 68 releases and from GitHub's public schema. The schema counts under "Capture history", "Why the allowlist was replaced", are in `gh-history/out/schema-history.txt`, from `gh-history/scripts/schema2.py`, which reads octokit/graphql-schema history and `docs.github.com/public/fpt/schema.docs.graphql` as fetched on 2026-11-03. Those sources are not copied here. The "about one a year" figure is a reading of that list by name and is not a measurement.
- Client source for revision 1: [cli/cli](https://github.com/cli/cli) at `c4513d446bf98cfcbe72ed83f17cd3220174d88f`, in `api/queries_pr.go`, `api/queries_pr_review.go`, `api/queries_user.go`, `pkg/cmd/pr/create/create.go`, `pkg/cmd/pr/merge/http.go`, `pkg/cmd/api/http.go`, `internal/featuredetection/feature_detection.go`, and `internal/ghinstance/host.go`. [gitlab-org/cli](https://gitlab.com/gitlab-org/cli) on `main`, in `internal/commands/mr/create/mr_create.go` and `internal/commands/mr/approve/mr_approve.go`.
