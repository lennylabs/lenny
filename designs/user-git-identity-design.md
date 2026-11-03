# User-scoped repository access and pull request authoring

Status: design for owner review, revision 2, 2026-11-03. It reads against `egress-and-git-design.md` revision 42. Nothing in this document is built or staged. Revision 2 folds in a traffic capture of `gh` 2.103.3 and `glab` 1.127.0 through a prototype filtering `netagent` and a recording stub (see "Capture results"). The stub executes nothing and enforces nothing, so a 200 at the stub says only that a request was forwarded. No request reached a provider, and no live spike has run. Statements about the tree were checked by reading the code and the specification on this date. Statements about GitHub, GitHub Enterprise Server (GHES), GitLab, and Coder come from the pages under "Sources". Proposals carry the label UG-n.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[measured]** | A hands-on result from a validation round of `egress-and-git-design.md`. |
| **[captured]** | Seen at the stub or at the prototype `netagent` in the capture of 2026-11-03. It shows what the client sent and what the filter did. It shows nothing about what a provider would do. |
| **[stub cannot show]** | A question only the provider can answer. Each one has a spike under "Validation owed". |
| **[documented]** | Stated on a vendor page under "Sources". The vendor's page was read and its behaviour was not tested. |
| **[source]** | Read in the Lenny tree, the specification, or client tool source at cli/cli tag v2.103.3. |
| **[design]** | A statement of what this design requires. Nothing implements it. |

## Problem

Feedback on Lenny asked for user-scoped repository access and pull request authoring, similar to what Coder offers. These gaps stand behind it.

- **Authorization.** At session bind, the egress design checks a repository against the tenant's allowlist and the VCS pool's `allowWrite` ("Credentials", **Write scope**). It does not ask whether the requesting user can access the repository. Any user in a tenant reaches any allowlisted repository through the App.
- **Authorship.** A pull request created with an App token has the App as its author. GitHub then sees no relationship between the requesting user and the pull request, so that user can approve the agent's work, which defeats two-person review.

These are separate features with different risk. Authorization keeps the user's token inside the Token Service. Pull request authoring lets an agent act with a token derived from a user's authority. This document therefore separates them, and either one's proposal can be rejected without affecting the other's dependencies, except that UG-3 needs UG-2's link and check.

## Scope

This design covers account linking (UG-1), repository authorization on GitHub, GHES, and GitLab (UG-2), pull request authoring on GitHub and GHES with the filter on every host (UG-3), an approval check for tenants that refuse user tokens (UG-4), and user-authored merge requests on GitLab (UG-5).

Out of scope: tenant-wide session content reads, the repository visibility rule, and EG-12 defaults. `egress-and-git-design.md` owns egress and git, and this document lists what it needs under "Changes requested of egress-and-git-design.md". `security-oversight-design.md` owns repository rules and guardrails, `tenant-scoping-identity-design.md` owns user lifecycle and Environments, `tenant-outbound-connector-design.md` owns private hosts, and `forensics-evidence-preservation-design.md` owns preservation.

## Terms

| Term | Meaning |
| :-- | :-- |
| Link provider | A tenant object naming one host's provider kind and OAuth client. A tenant has at most one per host. |
| Link | One Lenny user's stored authorization on one host, keyed on tenant, user, and host. |
| Parent token | The user access token and refresh token a link holds. The parent token stays in the Token Service. |
| Child token | On GitHub and GHES, a token derived from the parent by `POST /applications/{client_id}/token/scoped`, limited to named repositories and permissions. |
| Verdict | The result of a repository check: `allowed`, `refused`, or `unavailable`. |
| Request filter | The allowlist `netagent` applies to API requests before it attaches a token under `inject`. |

The word "connector" stays bound to §9.3 and to the outbound connector design's `outbound` identifiers. No new link, channel, or register enters §28: the new Token Service calls sit on `schemas/lenny-tokenservice.proto`, which [§28.7 Wire-contract artifact register](../spec/28_communication-channels.md#287-wire-contract-artifact-register) records as a surface the channel register holds no entry for, and the I8 fields are additions to an interface the egress design defines.

## Personas and requirements

Alice is a developer at `acme` and Bob is her reviewer. Carol is `acme`'s tenant admin and Dave is its `security-admin`. Tenant `globex` runs on GitHub with a shared App. Tenant `initech` runs self-managed GitLab.

- UR-1. Alice links her account once per host, and she sees the prompt before her session starts rather than a failure in the middle of it.
- UR-2. A session reaches a repository under a VCS pool that requires user authorization only when the requesting user can reach it at the access level the session declared.
- UR-3. When Alice loses access, her session stops reading and writing that repository within a stated bound.
- UR-4. Alice's pull request has Alice as its author, and Alice cannot supply the approval it needs.
- UR-5. An agent running `gh pr create` or `glab mr create` works with the tool unmodified and with a placeholder as its token.
- UR-6. No agent, prompt-injected or otherwise, can approve or merge through the host's API with a credential that Lenny attaches. Text that a repository's own automation reads as a command is a separate path, which "Comment-triggered actions" treats.
- UR-7. Automated sessions keep working under a named service identity, and a person cannot use that path to skip the check.
- UR-8. A tenant can refuse user tokens in pods and still get a two-person review check.

## Threat model

### Security properties

- UP-1. A parent token and a refresh token never leave the Token Service. A gateway replica, `netagent`, and the mirror never hold one. The Token Service API and the `user_links` store hold this.
- UP-2. A child token is limited at the host to the session's `granted` writable repositories and to `pull_requests: write` and `contents: read`. The host enforces this.
- UP-3. `netagent` attaches a user-derived token only to the binding's API host and only to a request the request filter admits. A credential whose required handling `netagent` does not implement is refused. `netagent` holds this, and the Token Service sets the requirement.
- UP-4. A session with a requesting user never receives an App token for a repository under a pool that requires user authorization unless a fresh verdict says the user holds that access. The Token Service holds this before its cache lookup, and `lenny-egress-authz` holds it independently from the recorded entry state.
- UP-5. A user-derived token never reaches a pod that can serve another session or a later session. The binding validator, the session gateway check, and the Token Service each hold this.
- UP-6. A session with no link, or a failed or unavailable check, never falls through to an App token on such a pool.
- UP-7. An impersonation bearer cannot cause a linked token to be used. The gateway holds this.
- UP-8. One tenant's link, check result, or token never reaches another tenant. Link rows sit under row-level security, and every cache key carries the tenant.

### Threats

| Attacker | Can | Cannot | Why |
| :-- | :-- | :-- | :-- |
| Prompt-injected agent on Alice's session | Create, edit, close, and comment on pull requests as Alice on a session repository, including Bob's. Read what the child token reads. Request reviewers. Waste Alice's rate limit up to the session's quota. Write text a repository's bot treats as a command, within what the tenant's stated patterns leave open. | Approve, merge, set auto-merge, dismiss a review, push through the API, act outside the session's repositories, or read the token. | The filter refuses, the host limits the child, and `inject` keeps the token in `netagent`. |
| Two injected sessions, Alice's and Bob's | Each opens a pull request as its user. Each can comment on the other's, which reaches any comment-driven approval bot the tenant failed to state. | Approve the other's pull request through the host's review API. | `addPullRequestReview` and the REST review route are refused on every session. A tenant closes the bot path with `commentAutomation`. |
| Compromised `netagent` on Alice's pod | Use the child token and any App token it holds until they expire, including approving within the session's repositories, because GitHub has no permission narrower than `pull_requests: write` for this. | Obtain the parent or refresh token. Reach repositories outside the child's list on GitHub or GHES. Mint after the pod is quarantined or the binding ends. | UP-1 and UP-2. "Residual risks" states this plainly. |
| Compromised `lenny-egress-authz` | Misdecide a CONNECT or a scope decision. | Mint, or remove the Token Service's own check and its filter requirement. | The egress design's two gates, and `handling` coming from the Token Service on I8. |
| Attacker who tricks Bob into completing a link flow | Send Bob an authorization URL or a start URL. | Bind Bob's GitHub account to the attacker's Lenny user, or the attacker's account to Bob's Lenny user. | The start page requires a browser login matching the flow's subject, and the callback checks a cookie bound to that login. |
| Prompt-injected agent crafting bytes for a parser difference | Send documents and JSON that the filter and a host read differently. | Have those bytes reach the host. | `netagent` forwards a document and body it generated from the checked structure. See "What reaches the host". |
| Hostile tenant registering a host | Run a fake GHES or GitLab that answers checks and token calls as it likes, for its own users. Return hostile bodies and redirects to the Token Service. | Affect another tenant. Shadow a platform-reserved host. Receive another tenant's client secret, token, or cached verdict. | The egress design's mint path bounds, connector C8 and C9, and tenant-keyed caches. |
| Tenant admin of a shared App | Delete links in the tenant and read their metadata. | Read a token. | T4 envelope encryption, and no read route returns a token. |

## Capture results

A capture ran on 2026-11-03: `gh` 2.103.3 and `glab` 1.127.0, against a recording stub on a Docker `--internal` network, through a Go `netagent` prototype with `filter.go` and gqlparser 2.5.33, on a laptop under other load. Results and raw output are in the session scratchpad under `spike-gh/RESULTS.md`. The github.com flavour used `api.github.com` as a network alias for the stub.

### What moved from design to measured

| Revision 1 claim | Revision 2 status |
| :-- | :-- |
| `gh` sends `pr create`, `pr review`, and `pr merge` over GraphQL | **[captured]**. A REST allowlist does not cover `gh`. |
| `gh pr create` works through the filter with a placeholder | **[captured]**. 10 of 10 variants per flavour, with the stub receiving the same requests with and without the filter, carrying the injected token and no placeholder. |
| The filter refuses review, merge, aliasing, batches, fragments, encodings, and path tricks | **[captured]**. 522 attack rows per flavour under the final policy: 52 allowed controls reached the stub, 467 refused rows sent nothing to the stub, and 3 opaque-route rows carried no credential. 24 of 25 attack commands run with the `gh` binary exited non-zero with the refusal, and the 25th is the allowed control. |
| HTTP/2, HTTP/1.1 framing, absolute-form, and another bound host do not bypass the filter | **[captured]**, by the rows above. |
| A filter check is cheap on `gh` traffic | **[captured]**. 11 µs on `pr create` and 88 µs on the largest document. `gh pr create` wall time differed inside its run-to-run spread. |
| `glab` uses REST for its merge request commands, with its token in `Private-Token` | **[captured]** for `mr create`, `mr view`, `mr list`, `mr approve`, and `mr merge`. Other `glab` commands were not captured. |

### Corrections to revision 1

- **Flavours differ.** `gh` selects login-based mutations on github.com and node-ID ones on GHES **[source]**, and an allowlist from one capture breaks `--reviewer` and `--assignee` on the other **[captured]**.
- **Rule 6 of revision 1 would have broken `gh`.** `gh pr create --project` sends aliased root fields with `$input_000` variables (`add_000: addProjectV2ItemById(input: $input_000)`) **[captured]**. Several root fields, each checked, are admitted.
- **`gh pr create --project` uses `addProjectV2ItemById`**, where revision 1 named another field.
- **Revision 1's bounds were wrong.** Its 256 KiB document bound admitted a document that measured 40 MB, and streaming REST bodies cannot carry the body-key check `merge_when_pipeline_succeeds` needs. See "Bounds and cost".
- **The first filter had a bypass, and a second stays open in the prototype.** See "What reaches the host".

### What the capture cannot show

These stay **[stub cannot show]** and are spike S15 unless noted:

- Whether `pull_requests: write` permits approval on a provider-issued token. The permission table says yes **[documented]**, which is why the filter is required.
- Whether merging fails without `contents: write`. The table says it needs that permission for REST **[documented]**. The GraphQL mutations' checks are unknown.
- Whether a child's repository limit binds node-ID mutations, since `createPullRequest` takes an opaque `repositoryId` and others take node IDs. The filter checks operations and leaves targets to the host.
- Whether GitHub honours batches, GET queries, form bodies, or method overrides, and which GraphQL parser it runs.
- What GitLab does with `/merge` in a description for a given token, and how it routes an encoded project path (S17).
- `gh`'s behaviour against an older GHES, since the stub answers `__type` as if every field exists, which selects `gh`'s newest code paths.
- Whether any field fails under a narrow child on `gh pr view`, because the stub answers everything.

## Verification of the review findings

Each finding was checked against the code and the documents. The table states what holds and what differed.

| # | Result | What was checked |
| :-- | :-- | :-- |
| 1 | Holds. The body takes `target` or `target_id` (one is required), `repositories` or `repository_ids`, and `permissions`, on GitHub and on GHES 3.22. The response has `expires_at`, and the page does not say what sets it or how it relates to the parent **[documented]**. | "UG-3", S1 |
| 2 | Holds. The permissions table lists `POST /repos/{owner}/{repo}/pulls/{pull_number}/reviews` under `pull_requests: write` **[documented]**. The capture confirms the document for approve, comment, and request changes is the same, with the event in variables **[captured]**. Whether the permission works is **[stub cannot show]**. **One premise is wrong:** the egress design states no request buffer limit for `netagent`. It states a 64 Mi example request and an 11 MB RSS while streaming, and its scrub distinguishes "any body `netagent` does not buffer". The filter therefore introduces the bounds, under "Bounds and cost". | "UG-3" |
| 3 | Holds as a question. GitHub says the push must be approved "by someone other than the person who pushed it" and does not say how an App's push with a requesting user is treated **[documented]**. **One proposal premise is wrong:** `allowedRefPatterns` and path enforcement sit in `netagent` and read the plain git request, so they do not depend on the push using an App token. | "Git pushes and the most recent push rule", S3 |
| 4 | Holds in full. The cache paragraph sits under "Egress decision service" directly before **The I8 listener**, and the mint cache key is under "Credentials", **Minting**. The egress design already has a Token-Service-assigned cache lifetime for stored secrets (15 minutes), which is the precedent the fix generalizes. | "Revocation" |
| 5 | Holds. Recycled pods add a stronger case: §5.2 says the scrub "is **best-effort** and is not a security boundary", and on a `keep` pod the runtime process survives it **[source]**. | "Pod models" |
| 6 | Holds for today's tree: `Materializer.Available` returns false when `credential.UserProxyDialect` has no dialect, which covers `github`, and `credential_preclaim.go` then takes the pool path **[source]**. In the target design, VCS tokens come from I8 and never pass that router, so the larger fact is different: **the App token is the default on I8 rather than a fallback.** A requirement has to be stated on the VCS pool, and the `usercreds` change in the proposal is the wrong place. A further defect: `POST /v1/credentials` accepts `provider: github`, which is a known provider, stores it, and never delivers it. | "UG-2", "Single store" |
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

## Current state

| Claim | Status | Source |
| :-- | :-- | :-- |
| §9.3's flow starts inside a session, through URL-mode elicitation on a tool call | True for the specification. The code also has an admin route any authenticated caller can use. | [§9.3 OAuth/OIDC Flow](../spec/09_mcp-integration.md#oauthoidc-flow), `pkg/gateway/externalapi/admin/connector_oauth.go` |
| Connector tokens are keyed on tenant, connector, user, and Environment | True | `connectorcredstore.go`, migration 0059 |
| §4.3 forbids the gateway holding KMS decrypt for downstream OAuth tokens | Stated, and the gateway holds that access today under the deferred F-4.3.1 | [§4.3 Token Service](../spec/04_system-components.md#43-token-service), `cmd/lenny-gateway/credentialsurface.go` |
| Pods never receive downstream OAuth tokens | Stated three times, in §4.3, §9.3 **Key invariants**, and §13.3 | [§13.3 Credential Flow](../spec/13_security-model.md#133-credential-flow) |
| `github` is a known provider with no user delivery | True | `pkg/credential/credential.go`, `pkg/credential/lease.go` |
| An impersonation bearer cannot create a session | True through `callercheck.Check` | `pkg/gateway/sessionserver/user_gate.go`, `pkg/gateway/callercheck/callercheck.go` |
| A delegated child and a detached orphan carry the parent's user | True, on the session row | [§8.2 **Child identity.**](../spec/08_recursive-delegation.md#82-delegation-mechanism), [§8.10 Delegation Tree Recovery](../spec/08_recursive-delegation.md#810-delegation-tree-recovery) |
| The repository readable set includes the tenant's whole allowlist | True in the design, under "Effective policy": "the session's `gitClone` sources plus the tenant's repository allowlist" | `egress-and-git-design.md` |

## UG-1: Account linking

### Reuse of §9.3

The design reuses the `pkg/connectoroauth` library (PKCE, signed state, the Redis state store, code exchange, and the refresh call) and leaves the §9.3 connector record and handlers alone. A link is a new record owned by the Token Service. §9.3 does not fit for these reasons:

- `ConnectorDefinition` requires an MCP server. A repository host is not one.
- The §9.3 flow runs from a tool call inside a session. UR-1 needs a link before a session exists.
- §4.3 scopes connector tokens by Environment. A link records a fact about a person on a host, so a member of two teams would otherwise link twice for one account.
- The connector store is decrypted in the gateway (F-4.3.1). UP-1 keeps parent tokens away from the gateway.
- Connector refresh has no caller, and no store has a refresh lease.
- The callback is open to the account-link attack in "Defects found while verifying".

### Single store

A host has one store of user tokens per tenant.

- `POST /v1/credentials` refuses `provider: github` and any other VCS provider with `VALIDATION_ERROR` once UG-1 lands, since [§4.9 Pre-Authorized Credential Flow](../spec/04_system-components.md#pre-authorized-credential-flow) cannot deliver them and the registration otherwise appears to work.
- The connector write validator refuses an `oauth2` connector whose `tokenEndpoint` host equals a link provider's host, unless its `auth.type` is `hostLink`. A `hostLink` connector names the link provider, and `connectorinvoke` asks the Token Service over the existing mTLS API for a short-lived access token. This is the path §4.3 already describes: "Gateway replicas request tokens via the Token Service API; they receive short-lived access tokens, never refresh tokens or KMS keys".
- A connector that exists when a link provider is added keeps working until its next write, which the tenant admin's write response names.

### Data model

**`link_providers`** (tenant object, written through EG-9's single write path, with an optional TS-1 `environments` list):

| Field | Meaning |
| :-- | :-- |
| `host` | The canonical host, which must be covered by one of the tenant's VCS pools. Unique per tenant. |
| `kind` | `github`, `github-enterprise`, or `gitlab`. The kind fixes the endpoint paths, so a tenant supplies no OAuth URL. |
| `clientId`, `clientSecretRef` | The GitHub App's or GitLab application's client. The secret sits in `lenny-credentials`. |
| `allowedAccounts` | The organizations, GitLab groups, or login patterns a linked account must belong to. Empty means any account the OAuth client admits. |
| `scopes` | GitLab only: `read_api` and `read_user` by default. |

**`user_links`** (`TokenStore`, T4, row-level security, tenant guard, and §4.9.1 re-encryption):

| Column | Meaning |
| :-- | :-- |
| `tenant_id`, `user_id`, `host` | The primary key. |
| `link_ref` | A random identifier, regenerated on each link, used in evidence. It carries no meaning outside this row. |
| `provider_user_id`, `provider_login`, `provider_is_admin` | From the provider's user endpoint. Bounded in length and limited to the provider's login syntax, because a registered host is hostile input. Unique on tenant, host, and `provider_user_id`. |
| `access_token_ct`, `refresh_token_ct`, `scopes` | Envelope-encrypted tokens. |
| `access_expires_at`, `refresh_expires_at`, `generation` | Expiry, and a counter that changes on refresh and relink. |
| `state` | `active`, `relink_required`, `suspended`, or `revoking`. |
| `refresh_lease_holder`, `refresh_lease_until` | The refresh lease. |
| `verified_at`, `last_used_at`, `created_at` | Times. |

A user with accounts on two hosts holds two rows, and Lenny draws no connection between them. A user who wants a different account on one host relinks, which revokes the old grant first.

Two Lenny users cannot link one provider account within a tenant. The unique index refuses the second with `409 LINK_ACCOUNT_IN_USE`, naming neither user, and the tenant admin can remove the first. A shared account makes `credential.mint` attribution false and gives one person a way around two-person review. The same account in two tenants is permitted, since the tenants are unrelated.

### App choice

A link provider's App need not be the App in the VCS pool, because the check needs only the user's access. A tenant that wants UG-2 alone should register a separate App with `metadata: read` and no other permission, installed on the same organizations, which leaves the stored parent token with no write power at all. UG-3 needs an App with `pull_requests: write` and `contents: read`. GitHub limits a user token to the App's permissions and to accounts where the App is installed **[documented]**, so a repository the linking App cannot see answers as `refused`. The App's user token expiry must stay on. A token response with no `expires_in` or `refresh_token` is refused at link with `LINK_TOKEN_NON_EXPIRING`, because GitHub lets an App opt out **[documented]**.

A tenant-owned App is the recommended arrangement. An operator's shared App across tenants works with two differences: `DELETE /applications/{client_id}/grant` would end the same person's link in another tenant, so a shared App revokes by token, and the ten-token limit is shared by the same person across tenants.

### Flow

The user experience follows Coder's: a named provider instance per host, self-hosted URLs, and an authorization step before the workspace exists.

1. Alice calls `POST /v1/sessions` with a plan under a VCS pool that requires user authorization and has no link. The gateway answers `403 USER_LINK_REQUIRED` with `details.linkUrl`, before any pod claim. MCP clients receive the same URL as a URL-mode elicitation. She can also call `POST /v1/user-links` with `{"host": "github.com"}` from the playground or `lenny-ctl`.
2. `linkUrl` is a Lenny URL holding a random one-time flow identifier, with a 10-minute lifetime, naming the subject that requested it.
3. The browser opens it. The start page requires an OIDC login at `auth.oidc.issuerUrl` through the path §27.3.1 uses, and it refuses the flow when the logged-in tenant and subject differ from the flow's. This defeats an attacker who sends a victim a start URL.
4. The page sets a `__Host-` cookie, `HttpOnly`, `Secure`, and `SameSite=Lax`, holding a random nonce, and redirects to the provider with PKCE and a `state` that signs the flow identifier and the nonce's hash. A `SameSite=Lax` cookie is sent on the provider's top-level redirect back.
5. The provider redirects to `GET /v1/user-links/callback`. The gateway verifies the signature, consumes the flow once, and requires the cookie's nonce to match. A missing or mismatched cookie aborts the flow. This defeats a victim's browser completing an attacker's flow, in either direction.
6. The gateway passes the code and verifier to the Token Service over mTLS and never sees a token. The Token Service exchanges them through the mint path, reads the provider's user endpoint, and applies `allowedAccounts`: GitHub through `GET /user/installations` **[documented]**, and GitLab through group membership with `read_api`.
7. The browser shows a confirmation page naming the provider login and the Lenny user. Alice confirms, and the row commits. An audit event, `user_link.created`, carries the tenant, user, host, `link_ref`, and provider login, and it is delivered to Alice as a notification.

`DELETE /v1/user-links/{host}` lets Alice remove her link, and a tenant admin holding `manage_user_links` can remove anyone's. Neither route returns a token. Impersonation bearers are refused on every link route, using the same check as session create.

| Step | GitHub and GHES **[documented]** | GitLab **[documented]** |
| :-- | :-- | :-- |
| Authorize | `https://<host>/login/oauth/authorize` | `https://<host>/oauth/authorize` with PKCE |
| Exchange and refresh | `https://<host>/login/oauth/access_token` | `https://<host>/oauth/token` |
| Access token lifetime | 8 hours, and the refresh token 6 months | 2 hours by default, and configurable on self-managed and Dedicated |
| User | `GET /user`, and `/api/v3/user` on GHES | `GET /api/v4/user` |
| Revoke | `DELETE /applications/{client_id}/grant` or `/token`, with Basic authentication | `POST /oauth/revoke` with client ID, secret, and token |
| Application registration | A GitHub App with Lenny's callback URL | A user, group, or instance application |

### Refresh

GitHub says: "Once you use a refresh token, that refresh token and the old user access token will no longer work." GitLab refresh "Invalidates the existing `access_token` and `refresh_token`" **[documented]**. Refresh therefore has one writer per link, and it is a state change rather than a cache fill.

- **Trigger.** A check or mint that finds fewer than 15 minutes on the parent, or a 401 from the host with the parent, requests a refresh. Refresh is on demand, so an idle link costs nothing. A refresh token that lapses (6 months idle on GitHub) moves the link to `relink_required`.
- **Lease.** A conditional update sets `refresh_lease_holder` and `refresh_lease_until` (30 seconds) when the lease is free. Session-level advisory locks are unavailable through the §12 transaction-mode pooler, and a transaction open across a provider call would hold a connection, so the lease is a row rather than a lock. Other replicas wait up to 5 seconds by re-reading the row, and otherwise answer `unavailable`.
- **Commit.** The holder calls the provider with a 10-second bound and writes the new tokens and the incremented `generation` before anything else. A store failure after the provider's answer is retried from memory for the lease's remainder.
- **A lost response.** A timeout or a crash between the provider's rotation and the commit loses both tokens. A retry of the old refresh token that the provider refuses moves the link to `relink_required`. Neither provider's page describes an idempotent refresh, so this cost cannot be designed out. Decision 6 records it, and a metric and alert report the relink rate.
- **Sessions.** A session holds a child token on GitHub and GHES, and nothing on GitLab under the first version. The parent never reaches a pod, so rotating it cannot break a tool mid-request except through the unknown in S1. If S1 shows a child dies with its parent, `netagent` sees a 401, flushes, and asks I8 once under the `reauth-on-401` handling. A 401 means the host did not act, so one retry of the request is safe, including a mutation. The Token Service also caps `cacheUntil` so a child is re-requested before the parent's scheduled refresh.
- **`relink_required`** refuses new sessions with `USER_LINK_REQUIRED` and moves running sessions as a refused verdict does, with the reason `relink_required`.

### Hostile hosts

A self-managed host is tenant-registered input, as `tenant-outbound-connector-design.md` states in C9.

- The kind fixes endpoint paths, so a tenant supplies a host and no OAuth URL. The authorization redirect targets that host over HTTPS only, and the §9.2 URL domain check applies.
- Every Token Service call goes through the egress design's mint path: `lenny-egress` for a host whose route is not `direct`, with the tenant asserted, its CA, and bounds on time, response size, and redirects. The provider's declared mint endpoints gain the token path on `github.com`, since today they name `api.github.com` **[design]**.
- Connector C8 holds: a registered host cannot shadow a platform-reserved one, so a tenant cannot register `github.com` behind its own network.
- The provider login and identifier are syntax-limited before they are stored or shown, and a hostile host that answers `allowed` to everything harms only that tenant.
- Verdict caches, child caches, and refresh leases all key on tenant and host, following the egress design's mint cache rule.

### Lifecycle and revocation at the provider

| Event | Stops use | Revokes at the provider |
| :-- | :-- | :-- |
| User deletes the link | At once, by binding change | Yes |
| Relink | Yes | Yes, before the new flow commits |
| `soft_disable`, `hard_disable`, or `full_revoke` ([§11.4](../spec/11_policy-and-controls.md#114-user-invalidation), TS-4) | At once. A new step in §11.4 sets `state = revoking` and pushes binding changes for the user's sessions. | Yes. Reinstate restores no link, and the user relinks. |
| [§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces) erasure, or `DeleteByTenant` | The Article 18 restriction and the drain stop use | Yes, then the row is deleted |
| App or application removed by the user at the provider | At the next check, as a 401 that refresh cannot cure | Already done |
| Link provider deleted | At once for its links | Yes, for each link |

Revocation runs through a durable record retried with backoff, following the egress design's pending-revocation record for GitLab project tokens, because a host can be unreachable. Erasure has a limit: T4 data must be erased within 1 hour under [§12.9](../spec/12_storage-architecture.md#129-data-classification). The job retries for up to 30 minutes, then destroys the ciphertext regardless, and the erasure receipt records `provider_revocation_failed`. Lenny then holds no copy, and the access token lapses by its own expiry at the host. The tenant admin is told, so the user can revoke the authorization at the provider.

## UG-2: Repository authorization

### The requirement sits on the VCS pool

`userAuthorization` on a VCS credential pool takes `off` (the default) or `required`. "Pool" in the proposal is ambiguous, and the VCS pool is the correct object: it is a tenant object, it names the repository prefixes, TS-1 can scope it by Environment, and a shared warm pool is platform-global. A tenant's `security-admin` can force the value through a new `userAuthorization.require` guardrail under SO-2, which sets `suspendedBy` on non-compliant pools.

This work belongs in the Token Service and I8. `usercreds` is the §4.9 LLM reverse proxy path, and the proposal's change there would alter nothing the design's git path reads, so `pkg/gateway/credentials/usercreds/usercreds.go` is left as written.

### Declared repositories

Under the egress design, a session's readable set includes the tenant's whole allowlist, so an agent can fetch an undeclared allowlisted repository at run time. For repositories under a `required` pool, the readable set holds only what the session declared: its `gitClone` sources, a new `readableRepositories` plan field, its `writableRepositories`, and any private `gitRepository` warm fill on the pool it runs on. `netagent` refuses others with 403 and the decision identifier. Decision 22 records the cost and the on-demand alternative.

Public repositories under no `required` pool are unaffected, because nothing is authorized about them.

### The check

The Token Service makes one call with the parent token per repository and level. It compares the provider's numeric identifier with the one the pool stores, because GitHub serves a renamed repository under its old name and the egress design keys on the identifier.

| Host | Read | Write | Tag |
| :-- | :-- | :-- | :-- |
| GitHub and GHES | `GET /repos/{owner}/{repo}` returning 200, with `permissions.pull` | `permissions.push` | **[documented]** for the fields. What they report for an App user token is S6. `GET /user/installations/{installation_id}/repositories` documents "the access the user has to each repository" and is the fallback. |
| GitLab | `GET /api/v4/projects/:id` returning 200 with `read_api` | Effective access level of at least Developer | **[documented]** for the scopes. The field that reports inherited and shared access is S11. |

Classification, which every caller uses:

- `allowed`: the provider answered and the level holds.
- `refused`: 404, 403 without a rate-limit or SSO header, or a level below the declared one.
- `unavailable`: 5xx, 429, a 403 with a rate-limit header, a timeout, a CONNECT failure, a connector being offline, or a refresh lease not obtained.
- A 401 attempts refresh once and then counts as `refused` with the reason `relink_required`.
- A 403 with GitHub's SSO header is `refused` with the reason `sso_required`, which the error shows so Alice can start a SAML session and retry (S4).

A provider `refused` is `USER_REPOSITORY_ACCESS_DENIED`, naming the repository Alice declared. It reveals nothing she did not name.

### When the check runs

| Moment | Who asks | Refused or unavailable means |
| :-- | :-- | :-- |
| Session bind, before `ls-refs` and before any pod claim | Gateway, for every declared repository at its level | Create refused. `unavailable` is a retryable `USER_AUTHORIZATION_UNAVAILABLE`. An unauthorized user therefore learns no commit. |
| Resume, and the provisional binding of §7.3 | Gateway | Resume refused, and §6.2's failure path applies |
| Each mint on I8 for git or a binding | Token Service, before its cache lookup | No token, and `netagent` answers 403 or 503 |
| Timer, each session | Gateway job calling the Token Service | The entry changes state (below) |
| Mirror read | The decision service, from the recorded entry | Mirror refuses |

The order on the mint path is fixed: verify pod, read the session, run the check, ask I3, and only then read the mint cache. The cache key keeps its form, because under this rule a cached App token is reused only by a session that has already passed. The check's result has its own cache, keyed on tenant, `link_ref`, repository identifier, and level, for 60 seconds, which is the "short-lived cache" the proposal asked for.

### Revocation

Mid-session revocation needs a writer and a route. The route exists: `netagent` flushes on every I7 binding change, and a binding push makes each decision service replica re-read the session row. This design adds the writer and closes the paths that skip it.

**Entry state.** Writable entries are already records with a `state`. Readable entries become records too (see the requested changes). A new state, `userRefused`, is read only by both gates and `netagent`, as the hook already provides for unknown states. A session column `user_verified_at` records the last successful sweep.

**Sweep.** §4.3 has the gateway calling the Token Service, and the Token Service has no route to gateway replicas, so a gateway background job does this, as orphan cleanup does. Every `recheckIntervalSeconds` (300 by default, with up to 10% jitter) it calls the Token Service for each distinct link and repository among bound sessions, bypassing the verdict cache. A `refused` writes `userRefused` on the matching entries in the row's transaction and pushes a binding change. `allowed` updates `user_verified_at`. Deduplication makes the provider load per link and repository rather than per session, and conditional requests lower it further.

**What a binding change does.** Every `lenny-egress-authz` replica re-reads the row and drops the pod's decisions. `netagent` flushes cached tokens and closes that session's tunnels at once. The next I8 call is refused, and the mirror's next I3 is refused.

**`cacheUntil`.** The I8 response gains this field, the earlier of the credential's expiry and a value the Token Service chooses. `netagent` re-asks at that time. Under a `required` pool the Token Service sets 300 seconds, on App tokens as well as child tokens, so a lost subscription cannot extend use past that. This generalizes the 15-minute lifetime stored secrets have.

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
| Compromised `netagent` holding a child token | Until Lenny revokes it at the host or it expires. On a verdict of `refused`, the Token Service calls `DELETE /applications/{client_id}/token` on the child when S5 shows that leaves siblings alone. | **[design]** |
| Git operations in flight | A running push or fetch ends when its tunnel closes | **[design]** |

The stated figure for an honest pod is **6 minutes**. A tenant can lower `recheckIntervalSeconds` to 60 at a cost in provider calls. Webhooks would shorten this and are excluded from the first version, because Lenny has no provider webhook ingress and a repository-level `member` event would need one on every organization; UG-4 builds that ingress, and a later change can feed the sweep from it.

Once an entry is refused, `onAccessLost` on the VCS pool decides what happens to the session. The default, `deny`, keeps the session running without that repository and tells the client with a `repository_access_lost` event. `terminate` ends it. Content already in the workspace stays where it is under `deny`. See decision 13.

### Mirror, warm fills, and the 6-hour promise

- **Mirror reads.** The mirror authorizes each request through I3. For a session on a `required` pool, that check names the repository and reads the entry's state, so a store-served fetch with no mint still stops.
- **Warm fills.** A warm fill has no user, so the mirror's warm fill keeps its App path. Its contents enter a pod an agent reads. A private `gitRepository` warm fill under a `required` VCS pool therefore counts as a declared read for every session bound to that warm pool, and a user lacking access is refused at bind with a reason that names the warm pool. A tenant needing per-user variance uses a warm pool with no such fill.
- **Outage rule.** The mirror's "Upstream outage" row, and its 6-hour confirmation, conflict with fail-closed authorization. The rule is:
  - A new session, a resume, and a mint need a current `allowed` from the provider. `unavailable` refuses them, with a retryable error. Writes need the host anyway.
  - A running session's reads continue from the mirror's store while `user_verified_at` is within `verdictMaxAgeSeconds` (900 by default, and a ceiling of 3600). Past that, both gates refuse reads until a sweep succeeds.
  - The 6-hour fallback therefore never applies to a session on a `required` pool. It keeps its meaning for every other pool. A tenant that values outage reads above per-user limits leaves `userAuthorization` off.
  - A stalled sweep, whatever its cause, has the same effect after 900 s, which is P6's direction. `UserAuthorizationSweepStalled` alerts well before that.

### Pod models

UP-5 refuses a user-sourced binding, and a `required` VCS pool's repositories, on these pods. The gateway refuses at session create with `422 USER_AUTHORIZATION_UNSUPPORTED_POD`, and the binding validator refuses a definition that can only reach such pods.

| Pod | Why |
| :-- | :-- |
| `maxConcurrentSessions > 1` | `netagent` cannot attribute a request to a slot, and the design already gives such pods the union of readable and writable sets. Alice's token would serve Bob's slot. |
| Service mode | One process serves several sessions, and §5.2 gives it no session binding. |
| `recycle.enabled: true` | §5.2 states the scrub is best-effort and "is not a security boundary". On a `keep` pod the runtime process, and any state an injected agent planted in it, runs into the next session, whose user's token then answers on `netagent`'s unauthenticated loopback port. Residue from Alice's repositories would also reach Bob. |
| A binding with `scope: always` | Its cache keys on the tenant pin and it attaches to every session. A user source is refused at write. |

SDK-warm pods remain allowed, because the process that exists before bind runs deployer image code and no earlier agent session. The refusal costs concurrent and recycled pools their efficiency for these tenants, which decision 14 records. Service-mode runtimes cannot use a user identity at all under this design.

### Identity cases

The requesting user is read from the session row by both gates, through `pkg/binding`, and never from a token or a pod. A running session does not depend on its creator's bearer.

- **Impersonation.** A bearer whose `issued_tokens` row is an impersonation row already cannot create, derive, replay, or create-and-start. The gateway adds the same check to `handleMessages`, `handleResume`, interaction resolution, and any other route that gives a running session input, for a session on a `required` pool or holding a user-sourced binding. The refusal is `403 FORBIDDEN` with the reason `impersonation_user_token`, so an operator can read the session and cannot drive it into using Alice's token. SO-4a's `impersonated_by` claim is the same signal once it exists. A running session is otherwise unaffected by an impersonation that reads it.
- **Delegated children.** A child carries its parent's user on its row, so the same link and check apply to its own declared set. Its pod must pass the same pod model test. `credential.mint` records its `sessionId`, and the session row gives its `root_session_id`.
- **Detached orphans.** An orphan "carries the originating user's identity" ([§8.10](../spec/08_recursive-delegation.md#810-delegation-tree-recovery)), keeps being swept, and ends at `cascadeTimeoutSeconds`, 3600 by default. It keeps authorization and keeps PR authoring, because `detach` is what the client asked for, and §11.4's listing by owner reaches it. Decision 18 records this.
- **Disabled users.** Each gate reads the user's invalidation level and `processing_restricted` through `pkg/binding`. Any of `soft_disable`, `hard_disable`, or `full_revoke` refuses every user-derived mint and every App mint under a `required` pool, including the running sessions §11.4 otherwise leaves up on the lesser levels. A store error fails closed, as TS-4 does.
- **Cross-Environment.** Under `tenant-scoping-identity-design.md` decision 14's recommended option, a child stays in its parent's Environment, so `visible(bindings, E)` and `visible(VCS pools, E)` need no new rule. Under the alternative there, a user-sourced binding survives a crossing only when the bound's `bindings` names it, and the child is checked against its own Environment's pools. The link itself has no Environment, so neither option needs a link per team.
- **Environment-less pods.** The refused pod models above cover what TS decision 15 addresses.

### Service identities

An automated session has no person to check. A `required` VCS pool lists `serviceIdentities`, each with a `subject`, its own repository allowlist, and its own `allowWrite`. A session uses that path only when all of these hold:

- The caller's `caller_type` is `service`, which [§13.3](../spec/13_security-model.md#133-credential-flow) makes impossible to elevate on exchange.
- The bearer's `sub` is listed on the pool.
- The session is a root session under that subject. A human's delegated child stays under the human.

Who may hold one: a system principal authenticating through the client credentials path of §10.2, whose credential no person handles as a routine. A `tenant-admin` writes the list through EG-9's path, each write emits an audit event, and a `security-admin` can restrict or forbid the list through a new `userAuthorization.serviceIdentities` guardrail. A person cannot move onto this path by presenting a token with a chosen `caller_type`, because the subject must be listed. Lenny cannot detect a person holding a listed client secret, which "Residual risks" states.

A service session's pull requests have the App as author, under the egress design's existing binding. The filter applies to that binding too, because `pull_requests: write` on an App token can approve any human's pull request.

### Attribution

`credential.mint` carries "a session identifier and no user identifier", and §12.8 treats it accordingly: `DeleteByUser` deletes nothing from it. This design keeps that.

- `credential.mint` gains `tokenSource` (`app`, `userChild`, or `userCheck`) and `linkRef`. It carries no login, email, or `user_id`.
- `linkRef` is a pseudonym in the same class as the session identifier. Its join lives in `user_links`, which erasure deletes, so a stored record stops resolving to a person, and the erasure statement in "Evidence streams" stays true once it says so. The class statement needs that one sentence when UG-2 lands.
- The token hash field stays. For a child token, the hash also joins to the provider's audit log, which GitHub describes as listing the user with "`programmatic_access_type`" of "GitHub App user-to-server token" **[documented]**. That join lies with the customer's provider log rather than a Lenny store.
- The provider login appears only in `user_links` and in §11.7 audit events, whose treatment §12.8 already defines through step 13 and step 14.
- A tenant's security team asks "who" by joining `sessionId` to the session row while it exists.
- A filter refusal goes to `egress.local` with the decision identifier and the operation name, with no variables or body. Repeated refusals on one session raise a `user_token_filter_refusals` alert, because an agent trying to approve is a prompt injection signal. These records are `self_reported`, as the stream's table says.

### Failure behaviour

| Failure | Behaviour |
| :-- | :-- |
| No link on a `required` pool | Session create refused with `USER_LINK_REQUIRED`, before any pod claim. Nothing falls through to the App. |
| Provider `unavailable` at bind | `USER_AUTHORIZATION_UNAVAILABLE`, retryable. |
| Token Service down | As the egress design states, and bind also fails with the retryable error. |
| Postgres down | Bind fails. Running reads continue until `verdictMaxAgeSeconds`, the existing §12 behaviour applies, and no new mint happens. |
| Refresh token rejected | `relink_required`. |
| Rate limited at the provider | `unavailable`. The sweep backs off per link. A metric reports it. |

## UG-3: Pull request authoring

### Binding source

A credential binding's `secret` gains a third kind, `userLink`, naming a link provider. It has these limits, each enforced at write and again by the Token Service:

- `hosts` is exactly the host's API hostname: `api.github.com` on GitHub, and the GHES host on GHES. On GHES, the same hostname serves git and the web, and the filter's default denial covers the rest.
- `delivery` is `inject` and no other value.
- `permissions` is `pull_requests: write` and `contents: read` at most. `workflows` and `contents: write` are refused.
- `scope` is `onRequest`, or an implicit attach when the pool sets `prAuthoring: user`.
- `requestFilter` is `vcs-authoring`, which a tenant cannot clear on this kind. The filter's per-binding inputs are the session's repository list and slug, and the pool's `commentAutomation` and `commandPatterns`.

A session holds at most one `inject` binding per host, or an App-sourced binding on `api.github.com` would answer in its place. The egress design has no rule for two bindings on one host, so this is a requested change.

### Child token and the session's repositories

The Token Service holds the parent and mints one child for each session's use.

1. `netagent` asks on I8 for the binding, on its first request to that host.
2. The Token Service verifies the pod and session, rejects a refused pod model or an impersonated session, runs the check for each repository, and asks I3.
3. It selects the session's `granted` writable entries. Per `security-oversight-design.md` decision 25, a `pendingApproval` entry is absent, so SP11 holds, and a repository the session declared read-only is absent too, because one token has one permission set.
4. It calls `POST /applications/{client_id}/token/scoped` through the mint path with Basic authentication using the client ID and secret, `access_token`, `target_id`, `repository_ids`, and `permissions` **[documented]**.
5. It answers with the secret, `expiresAt` from the response's `expires_at`, `cacheUntil`, the authoritative `hosts` and `attach`, the session's repository list for the filter, and `handling: [vcs-authoring-filter, reauth-on-401]`.

A token has one `target`, so a session whose `granted` entries span two owners is refused on the second owner's binding with `USER_TOKEN_MULTI_OWNER`. `gh pr create --head alice:feature` still works when that head already exists, because it sends `headRefName: "alice:feature"` to `createPullRequest` on the base **[captured]**, and the child needs only the base. `gh` offers an automatic fork at an interactive prompt alone, and a non-interactive run stops with "you must first push the current branch to a remote, or use the --head flag" **[source]**, so `POST .../forks` stays refused.

### Child token cache and budget

The cache exists for one reason: the limits on token creation are unknown for this endpoint (S2). If the "ten tokens per user, application, and scope" rule applies, a token per session means Alice's eleventh session silently revokes one of her older sessions' tokens, and a new one past ten per hour hits a browser prompt no server can answer.

- **Key.** Tenant, `link_ref`, `generation`, sorted repository identifiers, permissions, and target. Two of Alice's sessions with the same repositories share a child. They have the same user and the same limits, so sharing widens nothing. Stored as §4.3 caches access tokens, encrypted in Redis, so replicas share it.
- **Budget.** A per-link counter over a rolling hour, 8 mints by default, with a Helm override that S2 sets. Past the budget a mint returns a retryable `USER_TOKEN_RATE_LIMITED`. The Token Service also counts token requests per App against GitHub's 2,000 per hour and alerts near it, beside the egress design's counter.
- **Revocation.** Session end revokes the child only when no other live session holds that key, and only once S5 shows that a child's revocation leaves siblings and the parent intact. Until then a child lives to its own expiry in `netagent`'s flushed memory and the Token Service's cache.
- **On 401.** A child revoked by the ten-token rule appears as a 401, which `reauth-on-401` covers once, then surfaces as a 503 with a decision identifier and an alert naming the rule.

### The request filter

The filter is required from the first version of PR authoring. It is an allowlist over REST method and path, and over GraphQL mutation fields with their input keys, and a request it cannot classify is refused. The owner's requirement is that `gh` works, so every `gh pr` command except review, merge, and branch updates stays on the list.

**Placement.** It is a stage between the authority check and the attach step in `inject`, reached by both transports (the terminated connection and the absolute-form plain HTTP request), so one code path decides. The prototype ran it there, and a request naming a filtered host is checked wherever it arrives **[captured]**. Because the egress design says `metadata` is a provider plugin in `netagent` with a stated interface, the filter is the same kind of plugin, one per provider. This is a stated departure, for these bindings alone, from "it knows hosts, headers, and files, and nothing about git, GitHub, or any other product". A generic mechanism cannot tell a pull request creation from an approval, because GitHub puts both under `pull_requests: write`.

**Refusal.** `netagent` answers with JSON `{"message": "refused by the egress operation allowlist: ...", "lenny": {"decisionId": "..."}}`. `gh` prints the status and message **[captured]**, for example `pull request create failed: HTTP 413: refused by the egress operation allowlist: body of 393568 bytes is over the 262144 byte bound`. It sends nothing upstream and attaches nothing.

**Request rules:**

| Rule | Value |
| :-- | :-- |
| Path handling | Raw path segments. Refused: any `%` outside GitLab's project segment, doubled slashes, dot segments, a trailing slash, `;`, `#`, backslash, and control bytes. Fixed parts match exactly and case-sensitively. The filter also refuses a request whose forwarded target would differ from the checked text. |
| GitLab project segment | `{project}` accepts one encoded form. It is decoded once and must equal a project on the session's list from I8, or be that project's numeric identifier. Revision 2 replaces the prototype's refusal of route words with this comparison. `acme%2Fwidgets%2Fmerge_requests%2F7%2Fapprove` and `acme%2F..%2Fwidgets` reached the stub on the first pass and were closed **[captured]**. Under the list comparison neither can match. How GitLab would route them is **[stub cannot show]**, and the rule no longer depends on it. |
| Prefix | `/` on github.com, `/api/v3/` and `/api/graphql` on GHES, and `/api/v4/` and `/api/graphql` on GitLab, from the provider kind. Everything else on the host is refused, including `/login/`, web routes, and git paths. `gh pr merge --delete-branch` also runs `git`, and on GHES the API and git share a host name **[captured]**. Pod git goes to the local git endpoint under the egress design's rewrite, so this stage never sees it. No git request ran under the filter. |
| Method override | `X-HTTP-Method-Override`, `X-HTTP-Method`, `X-Method-Override`, and a `_method` query parameter are refused **[captured]**. |
| Upgrades | `Upgrade`, `h2c`, WebSocket, extended CONNECT, and trailers are refused. |
| Headers | Forwarded headers are an allowlist per provider, and `netagent` drops others. GitHub: `Accept`, `Accept-Encoding`, `Authorization`, `Content-Type`, `Content-Length`, `User-Agent`, `X-Github-Api-Version`, `Graphql-Features`, and `X-Gh-Cache-Ttl`, which is `gh`'s cache hint and reaches the origin **[captured]**. GitLab: `Accept`, `Accept-Encoding`, `Private-Token`, `User-Agent`, `Content-Type`, and `Content-Length` **[captured]**. |
| Credentials | `netagent` overwrites the binding's attach header, which is `Authorization` for `gh` and `Private-Token` for `glab` **[captured]**. A request with `?access_token=` or `?private_token=` is refused, so a query cannot select another identity. |
| Repository scope | A `/repos/{owner}/{repo}` or `/projects/{project}` path must name a repository on the list from I8, compared in canonical form. GraphQL mutations take node IDs, so the host's limit on the child binds their targets (S15). |
| REST writes | A listed write has a body-key allowlist, and its JSON body passes the same strict decode and re-encoding as GraphQL. `GET` and `HEAD` with a body are refused. |

**GraphQL rules.** The endpoint admits `POST` alone, with one `Content-Type: application/json`, no query string, and no `Content-Encoding`. No `gh` request in 314 carried `Content-Encoding` **[captured]**. `netagent` applies these in order, and any failure refuses the request:

1. Read the whole body up to the body bound before anything goes upstream. A body past the bound gets 413, whether stated by length or reached by chunking **[captured]**.
2. Decode JSON at the token level, at every depth. Reject an array (a batch), any top-level key other than `query`, `variables`, and `operationName`, a duplicate key anywhere, a key whose exact spelling differs, invalid UTF-8, a lone surrogate, and nesting past the JSON depth bound. Numbers keep their literal text. `encoding/json` is unusable here: it matches struct fields case-insensitively and keeps the last duplicate.
3. Apply the lexical rule to `query`: refuse a bare CR, any other control character except tab and LF, and U+0085, U+2028, U+2029, and U+FEFF **[captured]**. The CRLF pair is normalized.
4. Parse with gqlparser's `ParseQueryWithTokenLimit`, under the token bound. A parser panic is recovered and refuses the request.
5. Check depth by an iterative walk before anything recurses over the tree.
6. Require exactly one operation. No `gh` document in the capture had more than one **[captured]**, and this removes the `operationName` differential. A present `operationName` must equal it.
7. An operation is a query (including the shorthand) or a mutation. `subscription` is refused. A query's root fields are not listed: a query cannot write, the child limits its reach, and `gh api graphql` reads roots such as `rateLimit` that a list would break. The prototype held a root list. This revision drops it, and S8 reruns the attack table without it.
8. A mutation may have several root selections up to the bound, and each must be a field. A fragment spread or inline fragment at the root is refused, so `... on Mutation` and `fragment F on Mutation` cannot hide a field. Directives at the root are refused.
9. Each field's **name** must be on the list. Its alias is ignored, so `createPullRequest: addPullRequestReview(...)` reads as `addPullRequestReview` **[captured]**.
10. Each field's only argument is `input`. Its value is a variable, as every `gh` subcommand sends, or an object literal, as `gh api graphql -f query='mutation { addComment(input: {...}) ... }'` sends **[captured]**. The filter resolves it, including variables nested in a literal, into one object and applies the field's input-key list.
11. Apply the text rules to every string in that object ("Comment-triggered actions").
12. Generate the forwarded body ("What reaches the host").

`gh api` falls under the same filter because it sends through the same proxy to the same paths, and `gh api graphql --input` reaches step 1 like anything else **[captured]**.

### What reaches the host

The first filter parsed the client's bytes and forwarded those same bytes. A parser difference then becomes a bypass, and one was found. This document passed the filter and reached the stub with the injected token, on both protocols **[captured]**:

```
# x\rquery{viewer(a:"""\nmutation{addPullRequestReview(...){clientMutationId}} #"""){login}}
```

gqlparser, graphql-go, graphql-js 17.0.2, and graphql-ruby's C parser read one query. graphql-ruby 2.6.14's default lexer ends a comment only at LF and reads one mutation, `addPullRequestReview` **[captured]**, because its reading of the bare CR differs. Which parser GitHub runs is **[stub cannot show]**. The corpus also shows other disagreements, including a NUL and a BOM between definitions. A design that has to know the host's parser is the failure here, because that parser is unknown and can change.

**The rule: the host receives bytes that `netagent` generated from the checked structure. No byte of the client's document, JSON envelope, or JSON body reaches the host's parser.**

- **Document.** `query` is the filter's own printing of the parsed tree, which removes comments and odd whitespace. `netagent` parses the printed text again and requires the same operation kind and root field names. If that fails, it refuses the request. Printed documents read the same under gqlparser and graphql-ruby on all 24 corpus documents **[captured]**. `gh` kept the same exit codes and operations with the printer on, on GHES **[captured]**, and the attack table passed with it on **[captured]**.
- **Lexical rule.** It stays beside the printer, because it refuses a suspicious document with a clear message rather than transforming it, and it is cheap. Either one closes the bare CR **[captured]**.
- **Envelope.** `netagent` writes a new JSON object holding `query` and `variables`. It drops `operationName`, which step 6 has made redundant. `Content-Type` and `Content-Length` are its own.
- **Variables and REST bodies.** The prototype copied `variables` verbatim, and that left the open defect: `{"i":{"pullRequestId":"P","state":"CLOSED"},"i":{"pullRequestId":"P"}}` passed the `updatePullRequest` input check and reached the stub, because the filter read the last value **[captured]**. A first-wins origin would close the pull request. Ruby's standard JSON reads the last value **[captured]**, and GitHub's reader is **[stub cannot show]**. The effect stops at a listed mutation's fields, so it never reached approval or merge. This revision closes it twice: step 2 refuses a duplicate key at every depth, and `netagent` re-encodes the decoded tree with sorted keys, so the host sees one value whatever it would have chosen. **[design]**. The re-encoding has not run, so S8 repeats the matrix with it on.
- **URL and query string.** The path is forwarded as checked. A query string is rebuilt from parsed pairs.
- **Framing.** `netagent` writes its own HTTP/1.1 or HTTP/2 upstream.

Canonical forwarding costs 1.9 µs on `pr create`, 44 µs on the largest `gh` document, and 14 ms on the token document **[captured]**. Printing is also what the first out-of-memory kill came from, so the bounds below come first.

### Bounds and cost

Every one of these came from a defect or a measurement in the capture.

| Bound | Value | Why |
| :-- | :-- | :-- |
| Body, `/graphql` and listed REST writes | 512 KiB | `gh` JSON-encodes with HTML escaping, so `<` costs 6 bytes. The prototype's 256 KiB bound refused 44,000 `<` (264,352 bytes), 65,536 `<` (393,568 bytes), and 65,536 emoji (262,496 bytes) **[captured]**. 65,536 characters is GitHub's body limit as recalled, unverified (S15). 512 KiB admits the worst `gh` encoding of that limit. |
| Document, within the body | 64 KiB | The largest `gh` body in the capture is 2,388 bytes **[captured]**. A PR body travels in variables, so this bound does not limit it. |
| Tokens | 4,096 | The largest `gh` document has about 608 by a regex count **[captured]**. The prototype had no token limit, and a 256 KiB document of tokens cost 36 ms and 40 MB. |
| GraphQL depth | 32 | The deepest `gh` document has 14 levels **[captured]**. An 80 KB document nested 40,000 levels killed the prototype's printer at a 256 MiB limit, which stops egress for the whole pod **[captured]**. |
| JSON depth | 32 | The same defect class, through `variables`. **[design]** |
| Root fields per mutation | 16 | `gh`'s project batch sends one per project change. **[design]** |
| Head read | 10 s, with a bare LF refused at once with 400 | A request with bare LF line endings got no answer, and the prototype held the connection for 20 s with no deadline **[captured]**. |
| Body read | 30 s | A slow client must not hold a buffer. **[design]** |
| Concurrent checks per pod | 4, then 429 with `Retry-After` | Holds buffered bytes to 2 MiB. **[design]** |
| Check time | 100 ms budget, refused with 503 past it | The parser cannot be preempted, so the bounds above are what limit it, and this catches a defect. **[design]** |

**Against `netagent`'s memory.** The egress design's example request is 64 Mi. Measured `memory.peak` was 15.5 MiB after `pr create`, 17.9 after the largest `gh` document, and 18.9 after a 258 KB body **[captured]**, cumulative in that order on one process. The 256 KiB token document took it to 74.9 MiB **[captured]**, past that request, which is the case the token and document bounds remove. With a linear extrapolation from the measured 5.5 ms and 1.7 MB at 258 KB, a 512 KiB body costs about 11 ms and 3.4 MB, about double that with printing and re-encoding, and four at once stay under 30 MiB of transient allocation. Scaling the measured costs by token count puts a document at the token bound near 1 ms and under 2 MB. These figures are estimates **[design]**, and S9 measures them. The chart sets `GOMEMLIMIT` on `netagent`, so the collector works before the limit.

| Document | Check alone | End to end, paired, on 1 CPU |
| :-- | :-- | :-- |
| `pr create`, 353 B | 11 µs, 6.8 KB | Below noise |
| Largest `gh`, 2,388 B | 88 µs, 78 KB | 0.06 ms |
| 65,784 B, a full PR body | 606 µs, 559 KB | 0.94 ms |
| 258,185 B of escapes | 5.5 ms, 1.7 MB | 4.2 ms |
| 260,023 B of tokens (now refused) | 36 ms, 40 MB | 17 ms |

All of these are **[captured]** on a laptop under other load, so small differences are not resolved. The binary grew from 10.87 MB to 11.41 MB.

**What the user sees past a bound.** `gh` exits non-zero with `HTTP 413: refused by the egress operation allowlist: body of N bytes is over the 524288 byte bound` and nothing is written **[captured for the format]**. An ordinary body stays far under it. Past the host's own limit the host's error applies.

### Bypass analysis

| Route | Result | Why |
| :-- | :-- | :-- |
| HTTP/2 | Filtered **[captured]** | `gh` and `glab` both speak it. Each stream is one request. Six attack rows ended in protocol errors from the server library, with nothing at the stub. |
| HTTP/1.1 framing and pipelining | Filtered **[captured]** | `netagent` writes its own upstream request, so conflicting framing cannot reach the host. |
| Absolute-form | Filtered **[captured]** | One authority is derived, and it enters the same stage. A differing `Host` is refused. |
| Another bound host, and reuse after an allowed request | Refused **[captured]** | A filtered connection refuses any other authority, and each request is checked. |
| `uploads.github.com` or `github.com` | No token | Neither is bound. On GHES a single host exists and the prefix rule applies. |
| Opaque tunnel | No credential **[captured]** | 2 rows reached the stub with no credential, and 1 did not connect. |
| Another binding on the host | Refused | One `inject` binding per host and session. |
| HTTP/3 | Absent | `netagent` offers `h2` and `http/1.1` in ALPN, and the proxy carries CONNECT only. |
| GET, form, `application/graphql`, gzip, zstd, and deflate | Refused **[captured]** | The endpoint admits one form. |
| Parser or JSON differences | Closed by the rule | "What reaches the host". |
| Reflection of the token | Scrubbed | The egress design's scrub covers the bound host's responses, with its stated re-encoding limit. |
| Compromised `netagent` | Filter is absent | See "Residual risks". UP-2 still limits it on GitHub and GHES. |

### Allowlists

The operation names below come from the capture of `gh` 2.103.3. **The prototype's policy listed only the first six fields, so the rest are [captured] as operations and have not run through a filter that lists them.** S8 runs that matrix, and it must show that `gh pr comment`, `edit`, `ready`, `close`, and `reopen` exit 0.

**GitHub and GHES mutations:**

| Field | `gh` operation and command | `gh` uses it on | Input keys |
| :-- | :-- | :-- | :-- |
| `createPullRequest` | `PullRequestCreate`, `pr create` | Both | `repositoryId`, `title`, `body`, `draft`, `baseRefName`, `headRefName`, `maintainerCanModify` |
| `updatePullRequest` | `PullRequestCreateMetadata`, `PullRequestUpdate`, `pr create` and `pr edit` | Both | `pullRequestId`, `title`, `body`, `assigneeIds`, `labelIds`, `milestoneId`, `projectIds`. `baseRefName` and `state` are refused. |
| `requestReviews` | `PullRequestCreateRequestReviews`, `--reviewer` | GHES | `pullRequestId`, `userIds`, `teamIds`, `union` |
| `requestReviewsByLogin` | `RequestReviewsByLogin`, `--reviewer` and `pr edit` | github.com | `pullRequestId`, `userLogins`, `botLogins`, `teamSlugs`, `union` |
| `replaceActorsForAssignable` | `ReplaceActorsForAssignable`, `--assignee` and `pr edit` | github.com | `assignableId`, `actorLogins` |
| `addProjectV2ItemById` | `UpdateProjectV2Items`, `--project` and `pr edit` | Both | `contentId`, `projectId` |
| `deleteProjectV2Item` | `UpdateProjectV2Items`, `pr edit --remove-project` **[source]** | Both | `itemId`, `projectId` |
| `addLabelsToLabelable`, `removeLabelsFromLabelable` | `LabelAdd` **[captured]**, and label removal, which S8 confirms | Both | `labelableId`, `labelIds` |
| `markPullRequestReadyForReview`, `convertPullRequestToDraft` | `pr ready` | Both | `pullRequestId` |
| `closePullRequest`, `reopenPullRequest` | `pr close`, `pr reopen` | Both | `pullRequestId` |
| `addComment` | `CommentCreate`, `pr comment` | Both | `subjectId`, `body`. Under `commentAutomation`. |
| `updateIssueComment`, `deleteIssueComment` | `CommentUpdate`, `CommentDelete`, `pr comment --edit-last` and `--delete-last` **[source]** | Both | `id`, `body`. Under `commentAutomation`. |

The filter admits these on both flavours, whichever one `gh` uses today. Neither form grants more than the other, and `gh`'s own source says the ID-based path "can be removed once GHES supports requestReviewsByLogin", so a GHES upgrade can change the selection through `__type`. A table split by flavour would break `gh` on that upgrade. Prefixes and `GET meta` differ by flavour, as stated.

The permissions on the child are `pull_requests: write` and `contents: read`, so a field needing more fails at the host. `addProjectV2ItemById` needs a projects permission the child lacks, and `gh pr create --project` then creates the pull request and reports the project failure. That is the App's configuration to decide, and it is **[stub cannot show]**.

**Refused.** Every other mutation, with no list needed. `gh` commands this stops, all **[captured]** refused: `pr review` with each event (`addPullRequestReview`, the same document for all three), `pr merge` with `--merge`, `--rebase`, `--squash`, and `--admin` (`mergePullRequest`), `pr merge --auto` and merge-queue pull requests (`enablePullRequestAutoMerge`), and `pr merge --disable-auto` (`disablePullRequestAutoMerge`). Also refused: `submitPullRequestReview`, `dismissPullRequestReview`, `addPullRequestReviewThread`, `enqueuePullRequest`, `dequeuePullRequest`, `updatePullRequestBranch`, `revertPullRequest`, `createCommitOnBranch`, `mergeBranch`, and `deleteRef`.

**Why review comments stay refused.** `gh pr review --comment` differs from an approval only in the `event` variable, so the filter can check `event: COMMENT` now that variables are decoded strictly. It refuses that too, because a review with no event becomes a pending review, and each adjacent mutation would then need its own reasoning. An agent uses `gh pr comment` instead, which loses inline comments.

**`pr edit`.** `gh pr edit` runs its mutations concurrently **[source]**, so a refused one left others applied: on GHES, `updatePullRequest` and `addProjectV2ItemById`, and on github.com also `requestReviewsByLogin` and `replaceActorsForAssignable` **[captured]**. Listing the whole set above removes the partial write for every flag except `--base`. `--base` is refused as `baseRefName`, since retargeting can move an approved pull request onto a protected branch. Whether GitHub keeps approvals across a base change is S16, and a pass there lets this key onto the list. Until then `gh pr edit --base main --title x` fails with the title unwritten if they share one mutation, and with others applied if not.

**`pr close --delete-branch`** closes the pull request and then fails on `DELETE .../git/refs/...`. The close stays.

**GitHub and GHES REST (below the provider's prefix):**

| Method | Path | From | Tag |
| :-- | :-- | :-- | :-- |
| `GET`, `HEAD` | The root path, `user`, `rate_limit`, and on GHES `meta` | `auth status`, feature detection | **[captured]** for root, `user`, and `meta` |
| `GET`, `HEAD` | `repos/{owner}/{repo}` and below, for a listed repository | `pr diff` with a diff `Accept`, `repo view` through `readme`, `gh api` | **[captured]** for the routes `gh` used |
| `POST` | `repos/{owner}/{repo}/pulls`, for a listed repository, with `title`, `body`, `head`, `base`, `draft`, and `maintainer_can_modify` | `gh api`, and other tools | **[captured]** as a route |
| `POST`, `DELETE` | `repos/{owner}/{repo}/pulls/{n}/requested_reviewers`, with `reviewers` and `team_reviewers` | `pr edit` on GHES | **[captured]** |
| `POST` | `repos/{owner}/{repo}/issues/{n}/comments`, with `body`, under `commentAutomation` | `gh api` | **[design]** |

Refused and named because the review asked: `POST .../pulls/{n}/reviews` and `.../events`, `PUT .../pulls/{n}/merge`, `PUT .../pulls/{n}/update-branch`, `PATCH .../pulls/{n}`, `POST .../merges`, `POST .../forks`, `PATCH` and `DELETE .../git/refs/...`, all **[captured]** refused where tried. `GET` passes through a broad path because the child limits what it can read, and `gh` reads many routes. The prototype listed exact `GET` routes and listed query keys, and this revision widens that for `gh api` **[design]**.

**GitLab REST (below `/api/v4/`), with `{project}` as above.** `glab` used REST alone for the commands captured **[captured]**, and `/api/graphql` admits queries and no mutation:

| Method | Path | From | Tag |
| :-- | :-- | :-- | :-- |
| `GET`, `HEAD` | `user`, `users`, `version` | `mr create` with metadata | **[captured]** for `user` and `users` |
| `GET`, `HEAD` | `projects/{project}` and below | `mr create`, `mr view`, `mr view --comments`, `mr list` | **[captured]** |
| `POST` | `projects/{project}/merge_requests`, with `title`, `description`, `source_branch`, `target_branch`, `labels`, `assignee_ids`, `reviewer_ids`, `target_project_id`, `milestone_id`, `squash`, and `remove_source_branch` | `mr create` | **[captured]** for the first nine keys. A `merge_when_pipeline_succeeds` field passed the prototype, which had no body-key list, and this list closes it. |
| `PUT` | `projects/{project}/merge_requests/{n}`, with `title`, `description`, `labels`, `add_labels`, `remove_labels`, `assignee_ids`, `reviewer_ids`, `milestone_id`, and `state_event` | `mr update`, `mr close`, `mr reopen` | **[design]**. S10 captures them. `target_branch` is refused, as `baseRefName` is. |
| `POST` | `projects/{project}/merge_requests/{n}/notes`, with `body` | `mr note` | **[design]**. S10 captures it. |

Refused and named: `POST .../approve` and `.../unapprove`, `PUT .../merge`, `PUT .../rebase`, `PUT .../reset_approvals`, `POST .../cancel_merge_when_pipeline_succeeds`, and every mutation (`mergeRequestAccept`, `mergeRequestSetDraft`), **[captured]** where tried. `glab mr create --auto-merge` creates the merge request and then fails on `PUT .../merge`, and `--create-source-branch` fails on `POST .../repository/branches` **[source]**.

### Comment-triggered actions

Allowing comments reopens a path the route filter cannot see: text that something else treats as a command, carrying Alice's identity. The body of `createPullRequest` and `updatePullRequest` carries the same risk, as a PR body also reaches bots, so **refusing comments never closed this path**, and it cost `gh pr comment`.

**GitHub and GHES.** The host has no native comment commands. Repositories run bots that do: Prow's `/lgtm` and `/approve`, merge bots such as Mergify and Bors, Atlantis's `atlantis apply`, and `issue_comment` workflows that gate on `author_association`. (Dependabot retired its merge commands in January 2026 **[documented]**.) A single injected agent commenting `/approve` on Bob's pull request produces an approval under Alice's name wherever such a bot trusts her. Lenny cannot detect these bots, and no regular expression covers their grammars.

The tenant knows its bots, so the design has it say. **`commentAutomation` has no default, and a write of `prAuthoring: user` or `bot` must set it:**

| Value | `addComment` and friends | Text checks |
| :-- | :-- | :-- |
| `none` | Allowed on the session's repositories | None. The tenant states no automation reads comments. |
| `present` | Allowed on the session's repositories | `commandPatterns` is required: RE2 patterns the tenant writes for its bots, applied per line to comment bodies, PR bodies, and titles. A match refuses with 403, naming the pattern. |
| `refuse` | Refused | The patterns still apply to PR bodies and titles when listed. |

A `security-admin` can force `refuse` or `present` through an SO-2 guardrail. Text patterns are weak evidence, as the capture found: free-text refusal is a deny list over free text. They are offered because the tenant's list targets a grammar it controls. The stronger control sits with the bot: a comment made through a GitHub App user token carries a non-null `performed_via_github_app` in the REST schema **[documented]**, and the UI shows the App's badge, so a bot or workflow can refuse commands from it. The tenant page recommends that first. Whether bots see that field in their webhook payloads is S15.

**GitLab.** GitLab itself runs quick actions, whatever the tenant has installed: "Run automatically when you save descriptions or comments" **[documented]**. `glab mr create --description /merge` carries a merge inside an allowed route, and a route-only policy sent it to the stub with the injected token **[captured]**. Whether GitLab would execute it for that token is **[stub cannot show]**.

The prototype refused every line starting with `/word`, which caught `/merge`, a following-line `/approve`, and a CR-separated `/merge`, and also refused an ordinary `/usr/bin/env` line **[captured]**. That breaks legitimate descriptions. This revision narrows it:

- On GitLab, the filter decodes every string in a write body and splits it into lines on LF, CR, VT, FF, U+0085, U+2028, and U+2029.
- A line is refused when, after any Unicode whitespace, it is `/` plus a name from a table of commands that reach review or merge state, compared case-insensitively: `approve`, `unapprove`, `merge`, `rebase`, `submit_review`, `target_branch`, and `run_pipeline`, with their documented aliases **[documented for the names]**. The page does not say whether command names are case-sensitive, or how code blocks behave, so the filter assumes the worst on both, and refuses inside a fence.
- Other commands (`close`, `reopen`, `draft`, `ready`, `label`, `assign`, `title`, and so on) pass, because their routes are allowed anyway. `/usr/bin/env` passes, since `usr` is no command. The tenant's `commandPatterns` also apply.
- `gitlabTextGuard: strict` keeps the prototype's behaviour, refusing any `/word` line, for a tenant that prefers that cost.
- The message names the line, so an agent can put the text in inline code or rephrase.

This table is a deny list, so a future GitLab command with merge power fails open until the table changes. The docs page is diffed per GitLab release under "Tracking releases", and S17 tests each spelling against a live instance. On the bot path, a tenant lowers the risk further with a project token whose role cannot merge the target branch.

### Tracking releases

An unlisted operation fails closed with an error naming it, so a new `gh` release cannot widen anything. It can break something, and the owner's requirement is that `gh` works, so this needs a process:

- **Per `gh` and `glab` release.** A CI job reruns the capture harness against the new tag and diffs the set of mutation fields, routes, and input keys against the table. A static grep over cli/cli's `mutation ` strings and `graphql:"` tags flags new names sooner. A new field on the review, merge, or auto-merge family fails the job. Any other new field gets a table change in the next `netagent` release, with its reasoning recorded.
- **Per server version.** `gh` selects documents from `__type` answers, and the stub answers as if every field exists. The harness replays with `__type` answers derived from the schema of each supported GHES release and of github.com, and tier 6 runs against one live GHES where a deployer offers one. ghe.com takes the login-based paths **[source]** and was not captured (S8).
- **Per GitLab release.** The quick actions page and fine-grained permission tables are diffed.
- **Run time.** `filter_unlisted_operation_total` with the field name and `User-Agent` shows operators which release broke what, before a ticket does. The table ships in the `netagent` image, so a change reaches a pod when a new pod starts. `handling` can name a minimum table version, such as `vcs-authoring:v7`, so the Token Service can stop releasing tokens to an image with a known defect.
- **Runtime images** pin their `gh` and `glab`, and tier 3 runs the plugin against those pinned versions.

### What the allowlist does not bind

- **Targets.** `updatePullRequest`, `closePullRequest`, `addComment`, and the others take node IDs, so on a session repository they apply to Bob's pull request as readily as Alice's. The child limits them to the session's repositories (S15). A later change can limit them to IDs `createPullRequest` returned in the session, at the cost of response parsing and state in `netagent`, and of breaking the agent-as-reviewer use of `gh pr comment`.
- **Reads.** `repository`, `node`, and `search` read whatever the child reads.
- **Free text.** A body can carry anything for a person or a reviewer's agent.
- **Git.** It sits outside this filter, under the egress design's rules and "Git pushes and the most recent push rule".

### Git pushes and the most recent push rule

`require_last_push_approval` means "the most recent push must be approved by someone other than the person who pushed it" **[documented]**. If pushes come from the App, GitHub sees the App as the pusher. The failure is this: Alice's agent pushes to Bob's pull request, and Alice approves it, with her approval counting because she is not the pusher and not the author. She has approved work she caused. The same applies to Bob approving Alice's pull request after his own agent pushes to it.

**The design:**

1. Git stays on repository-limited App tokens in the first version, as the proposal says, for a different reason from the one it gives: a host-side limit per operation, and no write power in the user-derived token. `allowedRefPatterns` and path enforcement would hold under either token.
2. **A session pushes only to its user's branch namespace.** Under a `required` pool with PR authoring on, the gateway writes the session's writable entries with `allowedRefPatterns: refs/heads/lenny/<slug>/*`, where the slug derives from `provider_login`, and `netagent` enforces it on `git-receive-pack`, so Alice's agent cannot reach Bob's branch and Bob's agent cannot reach hers. The 403 message states the required prefix. A tenant can widen the pattern per pool, and a widened pool must turn on UG-4, because the rule alone no longer holds.
3. The filter also refuses a `createPullRequest` whose `headRefName`, after any `owner:` prefix, lies under `lenny/` in another user's namespace, using the session's slug from I8, so a colluding agent cannot adopt a branch Alice's agent pushes to.
4. S3 tests the actual rule. If GitHub treats a user-token push as Alice's and a scoped user token works for git, a tenant can set `pushIdentity: user` to get GitHub's native enforcement, using a separate child with `contents: write` alone. That option is additive, and decision 10 records why it is second.

An agent asked to fix Bob's pull request opens a pull request against Bob's branch or asks Bob to take the commit. That is the cost of step 2.

**Commit authorship.** The egress design lets a VCS pool set the author and committer. Under PR authoring on, the gateway sets the author to the linked user: on GitHub `<id>+<login>@users.noreply.github.com` and on GitLab the link's verified primary email, read at link with `read_user`. The committer stays the pool's agent identity, and a `Lenny-Session` trailer is added by the template. This serves GitLab's "Prevent approvals by users who add commits", which excludes "the Git author and Git committer" **[documented]**, and it makes commit history truthful about who requested the work.

These fields are text the agent can change with `git -c`, and `netagent` does not read pack contents, so they are advisory on GitHub and a soft signal on GitLab (S12 tests how GitLab matches them). Signing is out of scope, because the agent holds no user key.

### Delivery

`file` delivery behind `agentReadableDelivery` is dropped from the first version. A token in a file readable by the agent UID can leave the pod through any allowed host and stay valid for up to 8 hours, the parent's lifetime and the upper figure until S1 settles the child's, and unlike a `federation` JWT it has no trust condition binding it to an address. Its value would be tools that reject the placeholder, and `gh` and `glab` accept it **[measured]** (rounds 3 and 10) **[captured]**. Decision 16 records this. `metadata` and `federation` do not apply.

**Why Coder's delivery does not fit.** Coder exposes the token inside the workspace through `GIT_ASKPASS` and `coder external-auth access-token` **[documented]**. For a person at a workspace, the person is the principal, and handing them a token they could get anyway removes nothing. An autonomous agent reads untrusted content, so any process it starts can run that command and send the result to any allowed host, after which no filter applies and no later revocation reaches it. Lenny keeps Coder's linking experience and replaces the delivery: the placeholder stays in the pod and the token stays in `netagent`, and `gh` runs with `GH_TOKEN=lenny-injected` as the egress design validated.

### Repository rules

Security oversight decision 25 and SP11 apply as written:

- A child token is a `mint-scoped` binding mint, so it names only `granted` entries, and a rule added during a session moves the entry and flushes the cache by binding change.
- A GitLab project token or any non-`mint-scoped` binding on a rule's API host is refused or suspended under `approvals.repositoryWrites`. A per-session fine-grained token is narrower than those, but its permission set does not follow entry state per mint until UG-5 is settled, so it is treated as non-`mint-scoped` for now. PR authoring on GitLab is therefore absent on a host with a rule, which that decision's cost already names.
- Pushes follow SO-5b unchanged.
- A child already minted stays valid at the host until it expires or S5 lets Lenny revoke it. SP11's residual covers this.

## GitLab

### What authorization needs

UG-2 on GitLab uses `read_api` and `read_user`, which grant no write **[documented]**. The token stays in the Token Service. It ships regardless of the choices below, on GitLab.com and on self-managed, with the same 2-hour refresh rules.

### What PR authoring has to face

GitLab describes `api` as "complete read/write access to the API, including all groups and projects", and no OAuth scope limits a token to a project **[documented]**. **A user's `api` token under `inject` gives the agent the user's full power across every project and group if `netagent`'s filter is its only limit, and a compromised `netagent` holds that token.** On self-managed, an administrator's token holds administrator power over the instance.

`glab` needs nothing unusual: REST alone for the commands captured, `Private-Token` carrying the placeholder on all 234 requests, and no `Content-Encoding` **[captured]**. With default settings it also opened one `CONNECT gitlab.com:443` **[captured]**, and the profile's `GLAB_CHECK_UPDATE=false` and `GLAB_SEND_TELEMETRY=false` stopped it **[captured]**.

### Options

| Option | `glab` unchanged | MR author | What a compromised `netagent` holds | Approve and merge stopped by |
| :-- | :-- | :-- | :-- | :-- |
| A. Filtered `api` OAuth token under `inject` | Yes | User | The user's full API power, for up to 2 hours | The filter and the quick action guard, alone |
| B. `write_repository` with `merge_request.create` on push | No. `glab mr create` needs the API. | Unstated by the docs (S11), and the pusher is expected | Read and write on every repository the user reaches, with no API | The token lacks API access. `netagent` must refuse `merge_request.auto_merge` in push options, and quick actions in `merge_request.description`. |
| C. A or B behind a tenant opt-in, off by default | As chosen | As chosen | As chosen | As chosen |
| D. Project access token bots, as the egress design has today | Yes **[captured]** with a stub | The bot | One project's token, at that project's role | The filter and the guard, since a Developer-level project token can approve |
| E. A per-session fine-grained token from a parent held in the Token Service | Yes, if `glab`'s routes fit (S13) | **User** | One session's projects, with `Read` and `Create` and optionally `Update` on merge requests | **On routes, the host**, because the permission table separates `Approve` and `Merge` from `Create` and `Update` **[documented]**, then the filter. **On quick actions, the filter and the guard**, until S13 shows whether the host applies the token's permissions to a command in a description. |
| F. A fine-grained token the user creates and registers | Yes, if the same fit holds | **User** | The user's selected projects and permissions, for up to 365 days by default | As E |

Facts behind E and F:

- Fine-grained tokens are generally available from GitLab 19.2 on every tier and offering **[documented]**.
- `POST /user/personal_access_tokens` accepts `granular_scopes` from 19.2, with `selected_memberships` and `project_ids` **[documented]**. "You must be authenticated" is the only stated prerequisite, and the page does not say whether an OAuth token can call it or which scope it needs.
- The REST table lists `Personal Access Token`, `Create`, on that path, so a fine-grained token holding that permission can be the parent, if GitLab lets a token create one no wider than itself.
- `expires_at` has day granularity, so revocation at session end uses the egress design's durable record.
- Whether each creation notifies the user is unknown.

### Recommendation

User attribution was half of the request, and revision 1's answer left GitLab users with a bot author. With fine-grained tokens at 19.2, that no longer needs to be the answer.

**On GitLab 19.2 and later, the target is E: user-authored merge requests from a per-session fine-grained token, with no `api` token in any pod.** S13 settles it, and it needs a free account and an hour rather than any Lenny code, so it runs before UG-3 starts. The token holds `selected_memberships` with the session's `granted` projects, and `Read` on the project and merge requests, `Create` on merge requests, and `Update` when `edit` is on. `Approve` and `Merge` are never in the set.

The outcome of S13 selects the parent:

| S13 result | Parent in the Token Service | Cost |
| :-- | :-- | :-- |
| An OAuth token with a low scope can create the child | The link's OAuth token | The link needs that scope, and users relink once |
| An OAuth token needs `api` | The link's `api` OAuth token, on the tenant's opt-in. `provider_is_admin` refuses a link for an instance administrator. | `api` in the Token Service for those links, which widens Token Service compromise and never touches a pod |
| No OAuth token can, and a fine-grained parent can | A fine-grained token the user registers, holding only that create permission | A manual step per user, up to 365 days |
| No programmatic path | F, behind a platform grant in the `storedTokenPush` class and a P2 exception, with a project list the user chose and no per-session narrowing | Manual, long-lived, wider than a session, and user-chosen |

On Free, GitLab's approval settings are absent **[documented]**, so E gives attribution there and the filter gives the rest.

**D remains for GitLab below 19.2 and for `prAuthoring: bot`.** There the MR author is the bot, and two-person review rests on the commit author and a paid setting, as "Residual risks" states, or on UG-4.

**A never ships.** Its residual is the user's whole account, and E or F reaches the same `glab` with a token whose power GitLab limits. B stays unused, since it breaks `glab` and E has a smaller reach.

Whichever token is in use, the filter and the quick action guard are required, because S13 has not shown that a command in a description is limited by the token rather than by the user's own permissions. Commit author and committer follow "Git pushes and the most recent push rule", and git itself stays on the project token under the branch namespace until S13 shows a fine-grained `Git` permission works per session.

Under decision 25, these tokens are refused on the API host of a repository rule for now.

## UG-4: Independent approval check

A required status check from the App fails when the only approver is the session's requesting user. This is offered to tenants that refuse user tokens in pods, and required for a pool that widens the branch namespace. It needs no token in a pod.

**How it works.** Lenny's App reports a status `lenny/independent-approval` on each pull request head:

- A webhook receiver listens for `pull_request` and `pull_request_review` events and verifies the signature. It needs `statuses: write` or `checks: write`.
- For a pull request's commits, it collects the sessions that pushed them from `git.operation` and the sessions that opened it from `credential.mint`, and maps each to a user and then to a provider identity through `user_links`.
- It passes when at least one approver's provider identifier is outside that set, and it fails otherwise. A commit it cannot attribute counts as having no person, which leaves the approver free.

It catches the case the native rule misses, including Alice's agent pushing to Bob's pull request, because it reasons about sessions rather than pushers.

**Limits.**

- A pull request still shows the App as author.
- "No user token" is true at run time, and the check needs a proven mapping from Lenny user to provider identity. UG-1 runs in a mode that keeps the numeric identifier and login and discards the token, so an attacker cannot claim someone else's identity. A tenant that refuses even that gets a mapping it must supply itself, which then rests on the tenant's own directory.
- The tenant configures the check as required on each protected branch. Lenny can verify that with a read and report it in posture, and it cannot force it.
- It adds a public webhook ingress to the gateway and App permissions, and a Lenny outage leaves the status pending, which blocks merges. That is fail-closed, at the cost of a merge outage.
- `git.operation` from `netagent` is `self_reported`, so a compromised `netagent` can omit a record.
- It needs `git.operation` and `credential.mint` retention at least as long as a pull request stays open.

On GitLab, external status checks are the corresponding feature, on the tiers GitLab offers it, and the same logic applies. S12 confirms tier and behaviour.

## Configuration by scope

| Setting | Scope | Who sets it | Default |
| :-- | :-- | :-- | :-- |
| `link_providers` rows | Tenant, with optional TS-1 Environments | `tenant-admin` | None |
| `allowedAccounts` | Link provider | `tenant-admin` | Empty |
| `userAuthorization` on a VCS pool | Tenant | `tenant-admin`, and SO-2 can force it | `off` |
| `userAuthorization.require` guardrail | Tenant security record | `security-admin` | Absent |
| `userAuthorization.serviceIdentities` guardrail | Tenant security record | `security-admin` | Absent |
| `serviceIdentities` on a VCS pool | Tenant | `tenant-admin` | Empty |
| `onAccessLost` | VCS pool | `tenant-admin` | `deny` |
| `prAuthoring` (`off`, `user`, or `bot`) | VCS pool | `tenant-admin` | `off`. On GitLab, `user` needs UG-5 and a host at 19.2 or later. |
| `commentAutomation` (`none`, `present`, or `refuse`) | VCS pool | `tenant-admin`, and SO-2 can force it | None. A write enabling `prAuthoring` must set it. |
| `commandPatterns` | VCS pool | `tenant-admin` | Empty, and required under `present` |
| `gitlabTextGuard` (`commands` or `strict`) | VCS pool | `tenant-admin` | `commands` |
| Filter bounds | Helm | Platform | As under "Bounds and cost", with the depth and token bounds tunable within a ceiling. |
| `disabledOperations` (`edit`, `lifecycle`) | Credential binding | `tenant-admin` | Empty. It narrows the table and cannot add to it. |
| `allowedRefPatterns` widening | VCS pool, requires UG-4 | `tenant-admin` | Namespace |
| `pushIdentity` (`app`, or `user` once S3 passes) | VCS pool | `tenant-admin` | `app` |
| `recheckIntervalSeconds` | Helm, with a VCS pool narrowing | Platform and `tenant-admin` | 300, with a floor of 60 |
| `verdictCacheSeconds` | Helm | Platform | 60 |
| `verdictMaxAgeSeconds` | Helm, with a VCS pool narrowing | Platform and `tenant-admin` | 900, with a ceiling of 3600 |
| `childTokensPerLinkPerHour` | Helm | Platform | 8, until S2 |
| `userTokens.grant` | Per tenant, under the egress design's per-class gates | `platform-admin` | Absent. It gates `userLink` bindings, because the token enters `netagent` memory. |

Every non-spec default is operator-tunable and documented as such. A pool with `userAuthorization: off` and no `userLink` binding behaves as the egress design says, with no check, link, or extra call.

## Worked examples

### Alice: link to `gh pr create` to Bob's approval

Tenant `acme`, App `acme-lenny` installed on the `acme` organization, VCS pool `github.com/acme/` with `userAuthorization: required` and `prAuthoring: user`. Pool `coding` has `maxConcurrentSessions: 1` and no recycling.

1. Alice calls `POST /v1/sessions` with `acme/web` declared writable. She has no link, so she receives `403 USER_LINK_REQUIRED` and a `linkUrl`, and no pod is claimed. **[design]**
2. Her browser opens the URL, logs in through the broker, and receives the cookie. It goes to `github.com/login/oauth/authorize` and returns. The gateway hands the code to the Token Service, which exchanges it and reads `GET /user`, and it checks `GET /user/installations` against `allowedAccounts`. She confirms `@alice-acme`. **[design]**, on **[documented]** endpoints
3. Alice retries. The Token Service runs `GET /repos/acme/web` with the parent. `permissions.push` is true, and the identifier matches. The gateway writes a `granted` entry with `allowedRefPatterns: refs/heads/lenny/alice-acme/*`, then runs `ls-refs` and claims a pod. **[design]**
4. The adapter clones through `netagent`. On a mirror route, I3 reads the entry and allows. A mint-scoped read token comes from I8, with the check before the cache lookup. **[design]**
5. The agent runs `git checkout -b lenny/alice-acme/fix-login`, commits as Alice's noreply address, and runs `git push`. `netagent` reads the ref update, the pattern matches, I8 mints `contents: write` for `acme/web`, and the push goes through `lenny-egress`. A `git.operation` record is written. **[design]**, and the push path is **[measured]** by egress rounds with no user check
6. The agent runs `gh pr create --title ... --body ... --head lenny/alice-acme/fix-login`. `gh` runs because `GH_TOKEN=lenny-injected` **[measured]** **[captured]**. `netagent` terminates TLS for `api.github.com`. Its first I8 call mints a child with `repository_ids: [acme/web]` and `pull_requests: write, contents: read`, and `handling` names the filter. **[design]**
7. `gh`'s traffic **[captured]**: over HTTP/2, `POST /graphql` with queries `RepositoryInfo` and `PullRequestForBranch`, and on GHES also `GET /api/v3/meta` and `Issue_fields` (`__type` feature detection). Each query passes. Then `mutation PullRequestCreate($input: CreatePullRequestInput!) { createPullRequest(input: $input) { pullRequest { id url } } }` has one root field, on the list, with `input` as a variable, and the input keys are all on the list, so it passes. `netagent` forwards its own printing and re-encoding, with `Authorization` overwritten. The stub received the injected token and no placeholder **[captured]**, and the printing and token overwrite left `gh`'s exit code and operations unchanged **[captured]**. That GitHub creates the pull request with Alice's avatar and the App's badge is **[documented]** and **[stub cannot show]**.
8. `credential.mint` records `tokenSource: userChild`, the `linkRef`, and the hash. **[design]**
9. Alice tries to approve in the GitHub UI, and GitHub refuses because she is the author (S3 confirms). Bob approves as a person. The most recent push came from the App, and Bob is neither, so `require_last_push_approval` passes (S3 confirms). Bob merges. Two people took part in this merge, because no agent can approve and Alice cannot.

### Alice's access revoked in the middle of a session

Carol removes Alice from `acme/web` at 10:00:00, while Alice's agent is working.

| Time | Event | Tag |
| :-- | :-- | :-- |
| 10:00:00 | GitHub refuses the child immediately on anything needing Alice's access. A `gh pr create` returns 404 from GitHub. | **[documented]** |
| 10:00:30 | `git push` still passes on a cached App token and a cached verdict, inside `netagent`'s `cacheUntil`. | **[design]** |
| by 10:05:30 | The sweep calls `GET /repos/acme/web`, which answers 404, and it writes `userRefused`. | **[design]** |
| The same moment | The binding change flushes `netagent`, closes tunnels, and drops decisions. `repository_access_lost` reaches the client. Lost push: by 10:06:00. | **[design]** |
| After | `git push` gets 403 and no mint. `git fetch` through the mirror is refused by I3. A resume is refused. | **[design]** |

The workspace still holds what was already cloned. Under `onAccessLost: terminate`, the session ends at the same moment.

### A prompt-injected agent

A web page tells Alice's agent to approve PR 7 and merge it. These refusals are **[captured]** for the `gh` binary on both flavours, with nothing written at the stub. What the provider would do without the filter is **[stub cannot show]**.

- `gh pr review 7 --approve`. `gh` reads the PR with a query, which passes. It sends `mutation PullRequestReviewAdd($input: AddPullRequestReviewInput!) { addPullRequestReview(input: $input) { clientMutationId } }`, with the event in variables. Step 9 finds the field off the list, and `gh` exits 1 with the refusal text. Nothing goes upstream, and `egress.local` records the operation name.
- `gh pr merge 7`, with any mode, sends `PullRequestMerge` with `mergePullRequest`, which is refused the same way. `--auto` and a merge-queue pull request send `enablePullRequestAutoMerge`, which is refused. As documented, the host would also refuse REST merge, since it needs `contents: write` and the child has `contents: read` (S15 tests that and the GraphQL equivalent).
- `gh api graphql -f query='mutation { ok: addPullRequestReview(input: {pullRequestId: "PR_x", event: APPROVE}) { clientMutationId } }'`. The alias is ignored at step 9. An alias named `createPullRequest` is ignored the same way. Variants the attack table refused:
  - `mutation A { ...F } fragment F on Mutation { addPullRequestReview(...) }` at step 8.
  - Two operations with `operationName` on either one at step 6.
  - `[{"query": "..."}]` and `{"query": "query{viewer{login}}", "QUERY": "mutation{...}"}` at step 2.
  - Gzip, zstd, deflate, form, and `application/graphql` bodies, by the endpoint's single form.
  - `GET /graphql?query=mutation...`, and `query` as a URL parameter on POST.
  - `POST /graphql/` and `POST //graphql`, on path handling.
- The bare CR document from "What reaches the host". Step 3 refuses it, and were it to pass, the printer would forward the query alone **[captured]**.
- `gh api -X POST repos/acme/payments/pulls -f title=x -f head=y -f base=main`. `acme/payments` is not on the I8 list, so the filter answers 403 **[design]**, since the prototype's owner and repo names were free. Without the filter, GitHub would answer 404, because the child covers only `acme/web` (S15).
- `gh api -X POST repos/acme/web/pulls/7/reviews -f event=APPROVE`. The path is off the REST list, and the filter answers 403 **[captured]**.
- `gh pr comment 7 --body "/approve"` on a repository running an approval bot. Under `commentAutomation: present` with a matching pattern, the filter refuses it **[design]**. Under `none`, the comment posts under Alice's name, and the bot acts if it trusts her. That is what the tenant stated could not happen.
- `gh pr close 7` on Bob's pull request succeeds. The action is attributed to Alice with the App's badge, and Bob can reopen it.
- Repeated refusals raise `user_token_filter_refusals`, which Dave's subscription receives.

### The same on GHES behind an outbound connector

Tenant `acme` runs `ghes.acme.internal` behind connector `acme-dc1`. The link provider has `kind: github-enterprise`, and the outbound host has Acme's CA and no `allowMirror`.

- Alice's browser reaches GHES on Acme's network, and the provider's redirect comes back to Lenny's public URL, so no inbound rule is needed. The Token Service exchanges the code through `lenny-egress` with `acme` asserted, under a grant, and validates against the CA before it sends the client secret. **[design]**, on connector C6 and C9
- `GH_HOST=ghes.acme.internal` and `GH_ENTERPRISE_TOKEN=lenny-injected` are set through the client proxy profile on a tenant-owned pool. `gh` posts to `https://<host>/api/graphql` and `/api/v3/...` **[captured]**, uses `requestReviews` rather than the login form, and sends `GET /api/v3/meta` **[captured]**. The stub was no connector, so the connector leg is **[design]**.
- The binding's single host also serves the web and git, so the filter's prefix rule is what keeps the token on API paths. `POST /acme/web/pull/7/reviews` and `/login/oauth/access_token` are refused by default, and smart HTTP paths are refused as the egress design states.
- `gh pr review`, the aliased mutation, and the REST calls behave as above. The scoped endpoint exists on GHES 3.22 **[documented]**, and S14 fixes the oldest supported version.
- **Connector offline.** Sweep and mints answer `unavailable`. New sessions get the retryable error, and running sessions have no store reads, since `allowMirror` is off and a connector host's git stops anyway. Links are untouched. No traffic takes the default route (C10).
- GHES disables rate limits by default per the egress design, so the ten-token rule is the remaining unknown there.

### GitLab

Tenant `initech` runs `gitlab.initech.internal` at 19.4, Premium, and S13 has passed with an OAuth parent. Erin is a developer at `initech`, and `prAuthoring: user` is on.

- Linking uses the OAuth application with the scopes S13 settled. The link holds `provider_user_id`, the verified email, and `is_admin`. The token stays in the Token Service. **[design]**
- Bind: `GET /api/v4/projects/:id` and the access level check pass. **[design]**, S11
- The agent runs `git push` under the project token and the branch namespace. The commit author is Erin's verified email.
- On its first API request, `netagent` asks I8. The Token Service creates a fine-grained token with `selected_memberships` on the session's project, `Read` and `Create` on merge requests, and tomorrow's `expires_at`. **[design]**, S13
- `glab mr create` with `GITLAB_HOST` and `GITLAB_TOKEN=lenny-injected` sends `GET /api/v4/projects/initech%2Fweb?license=true&with_custom_attributes=true` and `POST /api/v4/projects/initech%2Fweb/merge_requests` over HTTP/2 **[captured]** with a body whose keys are on the list. `netagent` re-encodes the body and overwrites `Private-Token`. The stub received the injected value **[captured]**. On GitLab the MR's author is Erin, and GitLab's creator rule stops her approving **[documented]**, **[stub cannot show]**.
- `glab mr approve` sends `GET .../merge_requests/{n}` and `POST .../approve`, and `glab mr merge` sends `PUT .../merge`. Both are refused and exit 1 with nothing written **[captured]**. Under the token, the host refuses too **[documented]** (S13).
- `glab mr create --description $'Fix login\n/merge'`, and `/approve` on a following line or after a bare CR, are refused by the guard **[captured under the prototype's wider pattern]**. `--description $'See /usr/bin/env\n/usr/bin/env'` passes **[design]**, where the prototype's pattern refused it **[captured]**.
- `glab api graphql -f query='mutation { mergeRequestAccept(...) }'`, an aliased form, and `glab api` with each refused route are refused **[captured]**.
- `glab api -X POST 'projects/initech%2Fweb%2Fmerge_requests%2F7%2Fapprove/merge_requests'` is refused, since that decoded segment is on no list. **[design]**, on a case **[captured]** closed differently in the prototype.
- Erin leaves the project: the sweep refuses, the binding change flushes, and the Token Service revokes the session's token through the durable record.
- On GitLab 18.x or under `prAuthoring: bot`, the same `glab` commands run under a project token, and the MR author is the bot. There the filter and guard alone stop approval and merge, and Erin can approve the bot's MR unless "Prevent approvals by users who add commits" excludes her through a commit author the agent can edit.

## Changes requested of egress-and-git-design.md

All of these are additive and do nothing until a UG proposal lands. Each must land early because the egress proposal it names ships a stored record format, a wire contract, or an enforcement point that adding it later would force to change.

| Request | Proposal | Why now |
| :-- | :-- | :-- |
| I8 responses carry `cacheUntil`, and `netagent` re-requests at the earlier of it and `expiresAt`. Absent means `expiresAt`. | EG-2 and EG-4 | I8 is a wire contract, and the cache rule is in `netagent`. An image that ignores the field would hold a token past revocation with no error, across a rollout. |
| I8 responses carry `handling`, a list of names, each with an optional minimum version, and `netagent` refuses a credential naming anything it does not implement at that version. Empty today. | EG-4 | A `netagent` built before this field would attach a user token with no filter, silently. The refusal has to exist in the first shipped image. This makes the filter itself a later, additive change. |
| A request inspection stage in `inject`, between the authority check and the attach step, reached by both the terminated and absolute-form transports, receiving method, raw path, headers, and a bounded body reader. **It can replace the forwarded body, query string, and header set**, and `netagent`'s upstream request is written from what it returns. It admits everything unchanged until a plugin is registered. | EG-4 | `inject` is a shipped enforcement point, and streaming bodies on both transports would need rework to hold a request back. Revision 2 adds replacement, because canonical forwarding is what closes the parser differential the capture found. A stage that can only admit or refuse would have to be rebuilt. The `metadata` plugin interface is the precedent. |
| A session holds at most one `inject` binding per host, refused at bind. | EG-4 | Which binding attaches on a shared host is unspecified today. Fixing it after tenants have bindings changes a shipped decision and could expose an unfiltered App binding beside a filtered one. |
| `secret` on a credential binding is a typed union with a `kind`, and an unknown kind is treated as absent by the Token Service, `netagent`, and the decision service, as `suspendedBy` is. | EG-4 | Stored record format. Typed route targets are the precedent. |
| The Token Service mint path runs a pre-mint step before its cache lookup, taking the verified session and returning allow, refuse, or unavailable. It is empty until UG-2. | EG-1 and EG-2 | The order of the check and the cache is a shipped enforcement point, and a check placed after the lookup is the defect finding 4 describes. |
| Readable set entries are records with a `state` and optional `expiresAt`, like writable entries, in the binding record and feed. The readable formula gains a per-VCS-pool input that a `required` pool sets to declared-only. Its default keeps today's formula. | EG-2, EG-3, and EG-9 | The binding record and feed are stored and on the wire. |
| The mirror's second-gate I3 check names the repository, and the decision reads that entry's state. | EG-5 and EG-2 | The mirror is a shipped enforcement point, and it is the path with no mint. TS-1's mirror filter needs the same field. |
| `pkg/binding` returns the session's user, root session, caller type, and `user_verified_at`, and the user's invalidation level. | EG-2 | The same enforcement points and the same reason as the Environment hook already reserved. |
| The provisional resume binding excludes a `userLink` binding, until `running`, and treats it as a write token. | EG-2 | The §7.3 edit is staged in that proposal and enumerates what the provisional state grants. |
| `netagent`'s proxy listeners set a head read deadline and refuse a bare LF in a request head at once, on every `inject` and plain-HTTP path. | EG-2 and EG-4 | Revision 2 adds this. The egress design has `netagent` read absolute-form heads directly, and the prototype held a bare-LF request for 20 s with no deadline **[captured]**. It is a defect of the shipped listener with or without a filter, and a pod-wide connection budget is at stake. |

Not requested, because each is additive with no rework:

- `tokenSource` and `linkRef` on `credential.mint`, since the envelope states that payloads change by adding fields. The sentence in **Erasure and holds** is restated when UG-2 lands.
- P2 and P3 text naming the child token, and the sentence that `netagent` needs no knowledge of provider paths, when UG-3 lands.
- The token path on `github.com` among the provider's declared mint endpoints.
- Validators refusing a `userLink` binding on refused pod models, and the refusals in "Pod models".
- The `prAuthoring`, `userAuthorization`, and `onAccessLost` fields, through EG-9's write path.

`netagent` running out of memory stops egress for the whole pod, which the egress design accepts under P6. The filter's bounds keep it clear of that, and nothing further is requested.

The GitHub rate limit sentence under **Minting** says the 2,000-per-hour limit is "documented for user access tokens". The page now says "for GitHub Apps and OAuth apps". This is a text correction for the owner of that document.

## Dependencies

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| UG-1 (linking) | EG-1, EG-9. EG-2's mint path for private hosts, and OC-1 for hosts behind a connector. | VCS pool hosts, the single write path, and the route to a private token endpoint. GitHub.com linking needs no pod-side component. |
| UG-2 (authorization) | UG-1, EG-1, EG-2, EG-3, and EG-5 for mirror rules. TS-4 for the lifecycle hook. | The mint path, `pkg/binding`, I8, writable entries, and the mirror. |
| UG-3 (PR authoring on GitHub and GHES, and the filter on every host) | UG-2, EG-4 with the requests above, and S1, S2, S3, S8, and S15 passing. | Child tokens need the link, and the filter needs the stage. |
| UG-4 (approval check) | UG-1 in identifier-only mode, EG-3, EG-10 | `git.operation` and `credential.mint`. Independent of UG-3. |
| UG-5 (GitLab user authorship) | UG-3, and S13 and S17 passing. S13 runs before UG-3 starts, so UG-5 lands with UG-3 when it passes. | The token path and the guard's behaviour on a live host. |

| Other design | Interaction |
| :-- | :-- |
| `security-oversight-design.md` | SO-2 gains the guardrail rows above. SO-1's role reads `user_link.*` audit. SO-4a's `impersonated_by` feeds "Identity cases". SO-5b and decision 25 apply unchanged. |
| `tenant-scoping-identity-design.md` | TS-1 scopes `link_providers` and `userLink` bindings. TS-4's upsert and refusal apply, and §11.4 gains the revocation step. |
| `tenant-outbound-connector-design.md` | OR-3 covers the Token Service's calls, and the GHES example follows C6, C8, C9, and C10. This adds the token and scoped endpoints to what a connector carries, with no design change. |
| `forensics-evidence-preservation-design.md` | The `quarantined` state stops child mints with no new work. FE-5's "Rotate credentials" step gains revoking child tokens by hash through the App's client, and FE-4 can add a rule pairing `userChild` mints with a live link. That design's owner adds these when UG-3 lands. |

No proposal in the egress design depends on one here.

## Specification sections affected

- [§4.3 Token Service](../spec/04_system-components.md#43-token-service): a third credential category, and "Pods never hold downstream OAuth tokens" gains a stated exception for a child token in `netagent` memory.
- [§4.9 Pre-Authorized Credential Flow](../spec/04_system-components.md#pre-authorized-credential-flow): VCS providers refused on `POST /v1/credentials`.
- [§9.3 OAuth/OIDC Flow](../spec/09_mcp-integration.md#oauthoidc-flow): `auth.type: hostLink`, the host-overlap validator, and the browser binding on the callback.
- [§10.2 Authentication](../spec/10_gateway-internals.md#102-authentication): the permission matrix gains link rows, and `manage_user_links`.
- [§11.4 User Invalidation](../spec/11_policy-and-controls.md#114-user-invalidation): the revocation step at every level.
- [§12.8 Compliance Interfaces](../spec/12_storage-architecture.md#128-compliance-interfaces): `TokenStore` deletion gains provider revocation with the 30-minute limit, and the erasure statement names `linkRef`. [§12.9 Data Classification](../spec/12_storage-architecture.md#129-data-classification) classifies `user_links` as T4.
- [§13.3 Credential Flow](../spec/13_security-model.md#133-credential-flow): "Pod never receives: client tokens, downstream OAuth tokens" gains the same exception, with `netagent` named. Session input routes refuse an impersonation bearer on affected sessions.
- [§14](../spec/14_workspace-plan-schema.md): `readableRepositories`. [§15.1](../spec/15_external-api-surface.md#151-rest-api): the link routes and new error codes. [§16.5](../spec/16_observability.md#165-alerting-rules-and-slos) and [§16.7](../spec/16_observability.md#167-section-25-audit-events): alerts and audit events.
- [§5.2](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes): the refused pod models. §7.1 and [§7.3 Retry and Resume](../spec/07_session-lifecycle.md#73-retry-and-resume): checks at bind and resume.

## Defects found while verifying

These exist today and are recorded here because this document edits nothing else. Each is owed a BUILD-GAPS entry by whoever takes this design forward.

- **Account linking in §9.3.** `handleAuthorizeConnector` returns an authorization URL and state to any authenticated caller, and `handleConnectorOAuthCallback` carries no bearer and no cookie. An attacker sends the URL to a victim, who approves, and the victim's token lands under the attacker's tenant, connector, and user. The specification's "bound to the initiating session ID and connector ID" also differs from the code, which binds tenant, user, Environment, and session. Severity is High wherever a confidential connector is registered. The fix is this document's browser binding.
- **Refresh absent.** `connectoroauth.RefreshAccessToken` has no caller, and `Invoker.bearerFor` returns an expired token, so a GitHub or GitLab connector stops working at its expiry.
- **Inert registration.** `POST /v1/credentials` stores `provider: github`, which can never be delivered.
- **Session input under impersonation.** `handleMessages` and `handleResume` admit an impersonation bearer, and §13.3 limits only mints. This matters beyond this design.

## Documentation

- A new operator page for link providers, covering App registration for each host, the callback URL, and the recommended read-only App for UG-2.
- A tenant admin page for `userAuthorization`, `prAuthoring`, and the refused pod models, stating the revocation bound and the outage rule.
- A runtime author section stating the branch namespace, the filter's refusal format, and which `gh` and `glab` operations are refused.
- A security page stating the residual risks below in the same words.
- Doc tests pin each error code to its page, and tier 11 keeps the allowlist page generated from the plugin's table.

## Tests

Every test carries a `// spec:` annotation once the specification sections land. Tiers follow the repository's model.

### Tier 0: static

The filter table is data with a schema check. A lint refuses a `userLink` binding source outside the Token Service's mint path, and refuses `user_links` access outside the Token Service.

### Tier 1: unit

- Filter path handling: dot segments, doubled and encoded slashes, method overrides, each prefix on each provider, and GitLab's project segment compared against the list.
- GraphQL: the capture's attack table as a fixture, covering aliases, multiple operations, `operationName` mismatch, root fragments, inline fragments, shorthand, batches, duplicate and case-variant keys at every depth, a BOM, a bare CR, NUL, comments, block strings, Unicode escapes in names, deep nesting, oversized input, and parser panics. Fuzz targets on the JSON and GraphQL stages run under tier 9.
- Canonical forwarding: the printed document re-parses to the same operation and fields, the variables re-encode to one value per key, and the corpus documents read the same under gqlparser and graphql-ruby after printing.
- Each bound, at its limit and one past it, including the 40,000-level document and a bare-LF head.
- Quick action guard: each command, alias, case, whitespace prefix, line separator, and fence, with `/usr/bin/env` admitted. `commandPatterns` on comment bodies, PR bodies, and titles.
- Input key restriction on `updatePullRequest`, and `headRefName` in another namespace.
- Verdict classification for each status and header.
- Budget and cache key behaviour, and link state transitions.

### Tier 2: component

The tests run the sweep job against a fake Token Service, including a stall and jitter, and the pod model validators against envtest where they read pool records.

### Tier 3: contract

- I8 with `cacheUntil` and `handling`, including an old `netagent` stand-in refusing an unknown handling.
- The Token Service link API.
- Plugin tables replayed against recorded `gh` and `glab` traffic per pinned version, on both GitHub flavours and with `__type` answers per supported GHES, with the capture of 2026-11-03 as the first fixture.
- A test that fails when a field of the review, merge, or auto-merge family appears on the list.
- `gh pr edit` with every flag leaves no partial write under the listed set.
- `credential.mint` with the new fields.

### Tier 4: integration

- Link flow against a fake provider, with a mismatched browser login, a missing cookie, a replayed state, a second user on one account, and a refused `allowedAccounts`.
- Bind, resume, and mint refusals. No link never reaches an App token.
- Mid-session revocation against the fake provider: flush, tunnel close, I8 refusal, mirror refusal, and each bound in "Latency, by path".
- Refresh: a race across replicas produces one provider call, a lost response moves to `relink_required`, and a store failure after the response recovers.
- Provider revocation for each row of the lifecycle table, including a host unreachable during erasure, with the destroyed ciphertext and the receipt.
- An impersonation bearer on each session input route.
- Warm fill on a `required` pool.

### Tier 5: end to end on Kind

The pod model refusals on Kind pods, the mirror path, and `gh` and `glab` binaries in a pod against a fake GitHub and GitLab. Network policy stays unchanged by these features.

### Tier 6: end to end on cloud

Against a test GitHub organization, GHES, and GitLab: a link, a child token, `gh pr create`, GitHub's enforcement of the child's limits, and refusal of review and merge at the filter.

### Tier 7: load

The tests measure sweep cost at 10,000 sessions, including deduplication and the `user_verified_at` write rate, refresh lease contention, and filter latency and memory at the concurrency cap with bodies at the bound, with `-race`.

### Tier 8: chaos

The tests cover a host outage with `required` pools, with new sessions refused and store reads ending at `verdictMaxAgeSeconds`, a stopped sweep, the loss of the Token Service, Redis, and Postgres, a `netagent` restart with a lost cache, and a lost binding push.

### Tier 9: security

- Every example under "A prompt-injected agent", the 522-row attack table, and the bypass table, against shipped `netagent` builds.
- Fuzzing of the filter, with a corpus differential across gqlparser, graphql-go, graphql-js, and graphql-ruby in both lexers on each parser upgrade.
- A token never appears in a log, span, or record.
- Cross-tenant: a fake GHES copying another tenant's identifiers receives no cached verdict or child.
- A hostile host: redirects, oversized bodies, hostile logins, and an SSRF target at registration.
- Account-link attacks in both directions.

### Tier 10: conformance

Adapter contracts do not change. The runtime battery gains `gh` and `glab` cases under `inject`.

### Tier 11: documentation

The tier checks error codes, alert runbooks, and the generated allowlist page.

## Validation owed

No live spike has run. The capture answered what a client sends and what a filter does with it. S1, S2, S3, S8, and S15 must pass before UG-3 starts, and S13 runs before it as well.

| Spike | Question | Decides | State |
| :-- | :-- | :-- | :-- |
| S1 | Does a child survive a refresh of its parent, and what sets `expires_at`? | `reauth-on-401` and `cacheUntil` caps | Open, live |
| S2 | Does the ten-token and ten-per-hour rule apply to GitHub App user tokens and to the scoped endpoint, and what happens past it with no browser? Does the scoped endpoint count toward the 2,000 per hour? | `childTokensPerLinkPerHour` and the cache key | Open, live |
| S3 | With `require_last_push_approval`, what does GitHub treat as the pusher, across a push from an App token, a user token, and a scoped child, each with author and approver combinations? Can a pull request's author approve when the App pushed? Can a scoped user token push? | `pushIdentity`, and the reasoning in "Git pushes and the most recent push rule" | Open, live |
| S4 | SAML-enforced organizations: does a user token need an active SAML session, does a child inherit that, and what does expiry of that session look like to the check? GitHub says to "start an active SAML session" before reauthorizing **[documented]**. | The `sso_required` handling | Open, live |
| S5 | Does deleting a child leave its parent and siblings valid? | Child revocation | Open, live |
| S6 | What do `permissions` fields report for an App user token with a restricted App? Do org base permissions and teams appear? | The check | Open, live |
| S7 | Is `gqlparser` suitable? | Parser choice | **Done for the filter** **[captured]** at 2.5.33. A black-box test sending the corpus to a scratch repository on GitHub would name its parser, and the design no longer depends on the answer. |
| S8 | Correct the allowlist against `gh`. | The tables | **Done for `gh` 2.103.3 on GHES and github.com** **[captured]**. Owed: rerun with the final table, which adds `comment`, `edit`, `ready`, `close`, `reopen`, and label and project removal; with printing and variable re-encoding on both flavours, since the printed run was GHES with verbatim variables; with `__type` answers per GHES version; and on ghe.com. |
| S9 | `netagent` memory with the filter at its bounds. | The concurrency cap and `GOMEMLIMIT` | Partly **[captured]**, up to 258 KB and with no token limit. Owed: rerun at the final bounds with a filter-off baseline. |
| S10 | The same capture for `glab`. | The GitLab table | **Done for `mr create`, `mr view`, `mr list`, `mr approve`, and `mr merge`** **[captured]**. Owed: `mr note`, `mr update`, `mr close`, and `mr reopen`, the body-key list, and the narrowed guard. |
| S11 | GitLab: which field reports effective access including shared groups, and who authors a merge request from `merge_request.create`? | The check, and option B's record | Open, live |
| S12 | GitLab: how does the committer rule match a commit's author, and on which tiers are external status checks offered? | Commit authorship and UG-4 on GitLab | Open, live |
| S13 | Can an OAuth token call `POST /user/personal_access_tokens` with `granular_scopes`, and with which scope? Can a fine-grained token create one? Does each creation notify the user? Does `glab mr create` run under `Read` and `Create` alone, and `mr note` and `mr update` under which additions? **Under a `Create`-only token, does a description holding `/approve` or `/merge` execute?** | The GitLab recommendation and UG-5 | Open, live. Needs a free account and no Lenny code. |
| S14 | The oldest GHES release with the scoped endpoint and each endpoint this design uses. | The support statement | Open, live |
| S15 | With a provider-issued token against a scratch repository: does a child approve, and does `pull_requests: write` alone merge, through REST and GraphQL? Does the child's repository limit bind node-ID mutations? Does GitHub honour batches, GET queries, form bodies, or method overrides? What is the true PR body limit? Which fields fail under a narrow child on `gh pr view`? Do bots see `performed_via_github_app`? | Whether the filter has a second layer, and what `gh` needs beyond the table | Open, live |
| S16 | Does GitHub keep approvals when a pull request's base changes? | Whether `baseRefName` joins the list | Open, live |
| S17 | On a live GitLab: do descriptions and notes created through the API run quick actions, under which permissions, with what case and whitespace rules, inside fences, and after a bare CR? How is an encoded project path routed? | The guard's table and `gitlabTextGuard` | Open, live |

## Residual risks

- **Compromised `netagent` on GitHub and GHES.** It holds a child with `pull_requests: write`, and GitHub has no narrower permission. It can approve other users' pull requests on the session's repositories until the child expires or is revoked. It also holds App tokens, including write. The filter is `netagent` code, so it provides nothing here. Compromise of `netagent` remains the boundary where two-person review ends, for those repositories.
- **Compromised `netagent` on GitLab.** It holds one session's fine-grained token under E, or one project's token under D. `api` tokens are absent from pods, so a user's power over other projects is out of reach. Under F it holds a token that lives up to 365 days by default until the user or Lenny revokes it.
- **Token Service compromise.** It holds parent tokens, and refresh tokens, for every linked user. It already holds every App's private key and every stored secret. On GitHub a parent is limited by the App, which a read-only App narrows to nothing, and on GitLab with `read_api` a parent cannot write. Under UG-5, if S13 requires it, it holds `api` parents, which widens what its compromise exposes and is why that case needs a tenant opt-in.
- **The revocation window.** A revoked user keeps git access through an honest pod for up to 6 minutes, and for up to 15 minutes of reads during a host outage.
- **Workspace content.** What a session has cloned stays in the workspace, and in its checkpoints and artifacts, after access is lost. `onAccessLost: terminate` ends the session and leaves stored artifacts under the owner's existing access.
- **Allowed mutations on other people's work.** On a session repository, Alice's agent can change the title or body of Bob's pull request, close it, comment on it, and request reviewers, because the filter cannot see authorship and the host allows it. These are attributed and reversible, and they do not touch approvals.
- **PR content.** An agent's pull request body or comment can carry anything, including text aimed at a human reviewer or at a reviewer's agent.
- **Parsers.** The rule under "What reaches the host" closes differences in how the client's bytes are read. It rests on gqlparser's printer emitting text the host reads as printed, which 24 corpus documents show under graphql-ruby **[captured]** and nothing shows on a live host.
- **Filter-only limits.** Against a Developer-level GitLab project token, the filter and guard are all that stop approval and merge. Against a GitHub child, nothing at the host distinguishes creating a pull request from approving one.
- **Service identities.** A person holding a listed client secret can start App-authored work and then approve it. Lenny cannot distinguish that person from the system. The audit trail shows the subject, and UG-4 cannot name a person who is absent from the session.
- **GitLab under D.** Below 19.2 or under `prAuthoring: bot`, the MR author is a bot, and two-person review rests on an agent-editable commit author and a paid setting, so an injected agent defeats it, and Free has no such setting. UG-4 covers this where a tenant runs it.
- **GitLab quick actions.** The guard is a deny list over free text. A command it does not name, or a spelling GitLab accepts and the guard does not, runs under whatever permissions GitLab applies (S13, S17).
- **Comment-driven and other automation.** A comment or PR body acts with Alice's standing on any bot that reads it. If a tenant states `none` wrongly, or its `commandPatterns` miss a grammar, a single injected agent can approve or merge through such a bot wherever it trusts her. The filter cannot close this, and only the tenant's statement, its patterns, and the bot's own check of `performed_via_github_app` bear on it.
- **Provider and client behaviour.** The tables and checks depend on behaviour listed under "Validation owed". A new `gh` or `glab` release can break a command until the table catches up, by the process under "Tracking releases". The filter fails closed in that window.
- **A lost refresh response** forces Alice to relink.
- **Identifier-only linking under UG-4** proves an identity at one time. An account later transferred at the provider is not noticed.

## Decisions

| # | Decision | Recommendation | Cost of the recommendation | Alternative |
| :-- | :-- | :-- | :-- | :-- |
| 1 | Separating the features | UG-1, UG-2, and UG-3 as separate proposals in that order | UR-4 waits for UG-2, and tenants see partial value in between. | One proposal, which ties low-risk authorization to the pod-exposed token. |
| 2 | §9.3 reuse | Reuse `pkg/connectoroauth`, with a new record in the Token Service | A second table and flow beside §9.3, and new routes. | Rows in `connector_credentials`, which brings the MCP requirement, Environment key, gateway decryption, and the callback defect. |
| 3 | One store per host | A host-overlap validator, `auth.type: hostLink`, and VCS providers refused on `/v1/credentials` | A tenant with a GitHub MCP connector must move it to `hostLink`, and that MCP server receives a token from the link's App. | Allow both, which holds two tokens for one person and doubles revocation paths and the ten-token risk. |
| 4 | Link key and App | Tenant, user, and host, with one link provider per host | A tenant with two Apps on a host must install the linking App wherever `required` pools point. | Add the client to the key, which makes a person link once per App and adds selection logic. |
| 5 | Account sharing | Refuse two Lenny users on one provider account within a tenant | A team with a shared account cannot link it, and a first linker holds it until removed. | Allow it, which makes attribution false and gives a route around two-person review. |
| 6 | Refresh | One refresh at a time per link through a row lease, parent in the Token Service, on demand | A lost response forces a relink, and a waiting replica can answer `unavailable`. | A long transaction with a lock, which holds a pooled connection across a provider call. Neither provider offers an idempotent refresh to avoid this. |
| 7 | Scoped tokens | A child per session's repository set, shared across a user's sessions with the same set, with a budget of 8 per hour until S2 | Sessions with many distinct sets per user hit `USER_TOKEN_RATE_LIMITED`. A child outlives a session unless S5 allows revocation. | One child per session, which hits any ten-token limit sooner. Or a user-wide child, which fails finding 1. |
| 8 | Filter | Required from the first version: an allowlist with strict decoding, parsed GraphQL, input-key lists, and canonical forwarding | A provider plugin in `netagent`, one parser dependency (0.54 MB of binary **[captured]**), buffering of write bodies, and a `netagent` release for each new operation. Costs are under "Bounds and cost". | Accept approval as a risk, which the review rejected. Or a block list, which fails open on the next mutation. |
| 9 | Comment, edit, ready, and close | Allow all of them on the session's repositories, refuse reviews, merges, auto-merge, and branch updates, with `edit` lacking `baseRefName` and `state`. Comments follow a required `commentAutomation` statement. | Revision 1 had these off, which broke `gh` and left `pr edit` with partial writes **[captured]**. On is the cost of `gh` working: an injected agent can edit, close, and comment on Bob's pull request, and a comment acts on any bot that trusts Alice, up to what the tenant stated. | Off by default, which breaks `gh` and does not close the bot path, since a PR body reaches bots too. Or limit them to pull requests the session created, which breaks `gh pr comment` on Bob's and adds response parsing and state. Or comment under the App's identity, which sidesteps most bots and needs a second token selected per operation, and loses attribution to Alice. |
| 10 | Push identity | App tokens with a per-user branch namespace, and `pushIdentity: user` as an option if S3 passes | The namespace blocks an agent from pushing to another user's branch, and agents must use the prefix. GitHub's rule keeps seeing the App. | User-token pushes by default, which gets native enforcement. It waits on S3, and it puts `contents: write` from a person's identity in `netagent` next to `pull_requests: write`. |
| 11 | Commit author | The linked user as author, and the pool's identity as committer | A user's address appears in history for work an agent did, and it is agent-editable. | The pool's identity alone, which is simple and loses GitLab's committer rule. |
| 12 | Recheck interval | 300 s, with `cacheUntil` of 300 s and a 60-s verdict cache | A 6-minute window, and provider calls per link and repository. | Webhooks, which shorten it and require a public ingress and organization-level configuration. Or 60 s, at five times the calls. |
| 13 | After access is lost | `deny`, with an event, and `terminate` as a tenant choice | An agent keeps the content it already has until the session ends. | `terminate` by default, which loses a user's work on a transient provider error that survives classification. |
| 14 | Pod models | Refuse concurrent, service-mode, recycled, and `always` | These tenants pay for one session per pod on affected repositories, and service-mode runtimes have no user identity. | Allow recycling with `restart`, which still rests on a scrub the specification calls no security boundary. |
| 15 | Mirror outage | New sessions, resumes, and mints fail closed, and running reads continue for 15 minutes | A host outage stops new sessions on `required` pools despite the mirror, which withdraws the 6-hour promise for them. | Honour 6 hours, which gives a revoked user 6 hours of reads during an outage. |
| 16 | `file` delivery | Dropped from the first version | A tool that rejects a placeholder and needs a token file cannot author pull requests. | Offer it behind `agentReadableDelivery`, which places an 8-hour user token within reach of a prompt-injected process. |
| 17 | Attribution | `tokenSource` and an erasable `linkRef` on `credential.mint`, with logins in `user_links` and the audit log | A reader joins through a row that erasure removes, so old records stop naming a person, which is the intent. | The login on the record, which changes the §12.8 basis for every stream. Or a new stream, which adds volume for one field. |
| 18 | Detached orphans | Keep authorization and PR authoring, swept, until `cascadeTimeoutSeconds` | A child can open a pull request as Alice up to an hour after her session ended. | Refuse user tokens once an ancestor is terminal, which breaks the purpose of `detach` for these tenants. |
| 19 | Impersonation | Refuse session input routes on affected sessions | An operator cannot reproduce a support case by messaging the user's session, and must start one under its own identity. | Taint the session and stop its user tokens for the rest of its life, which leaves the session running and adds state. |
| 20 | Service identities | A pool-level list of subjects, with `caller_type: service`, admin-written and guardrail-limited | Each automation is registered per pool. A person holding a listed secret stays undetectable. | Trust `caller_type` alone, which lets a mapping error or a person skip the check. |
| 21 | Disables | Stop use and revoke at the provider at every level | A mistaken `soft_disable` forces relinking after reinstate. | Revoke only at `full_revoke`, which leaves a live grant for a disabled person. |
| 22 | Undeclared repositories | Declared-only under a `required` pool, with a new `readableRepositories` field | An agent cannot fetch a repository it discovers mid-session, and a plan must list dependencies. | On-demand checks through I8 at first access, which adds a mid-session store write, a binding change per repository, and a hot path. |
| 23 | Sessions across owners | Refuse the second owner on the binding | A session cannot author pull requests in two organizations. | One child per target, with `netagent` selecting by request path, which adds path parsing to the attach step. |
| 24 | Read-only repositories on the child | Absent, per decision 25's reasoning | `gh pr view` on a read-only declared repository gets 404 from GitHub. | A second read-only child selected by path, at the cost decision 25 names. |
| 25 | Filter on App-sourced bindings | Apply `vcs-authoring` to any binding with PR write on a `required` or `prAuthoring` pool, including service sessions | Those App bindings lose approval and merge through the agent, and a tenant workflow relying on that breaks. | Leave App bindings unfiltered, which lets an agent approve any human's pull request. |
| 26 | GitLab | E on 19.2 and later, per-session fine-grained tokens, with S13 selecting the parent and F as the last fallback. D below 19.2 and under `prAuthoring: bot`. Authorization ships regardless. A never ships. | UG-5 waits on S13, and depending on it the Token Service holds `api` parents, or users take a manual step. Each session mints and revokes a token, and S13 decides whether each one notifies the user. Older instances keep the bot author. | D for everyone, which is what revision 1 chose and gives up user attribution, half of the request. A behind an opt-in, which meets UR-4 and places full user power in `netagent`. B, which breaks `glab`. |
| 27 | Status check | An option (UG-4) for tenants refusing user tokens, and required for a widened namespace | A webhook ingress, App permissions, repository configuration by the tenant, and merges blocked during a Lenny outage. | The default for everyone, which removes user tokens from pods and leaves the App as author everywhere, against the request. |
| 28 | Sweep owner | A gateway background job calling the Token Service | The gateway needs a new job with per-tenant sharding, and it writes `user_verified_at` per session each interval. | A Token Service worker, which needs a new route to gateway replicas and a write to the session row from a component that reads it today. |
| 29 | Parser differences | Forward a document and body `netagent` generated from the checked structure, with the lexical rule beside it | A printing step, a second parse, and a body that differs in bytes from what the client sent. A printer defect becomes a refusal or a wrong request, which the re-parse narrows. | Forward the client's bytes after checking, which is the design that the bare CR bypassed **[captured]**. Or identify the host's parser, which cannot be done from outside and can change. |
| 30 | Bounds | 512 KiB body, 64 KiB document, 4,096 tokens, depth 32, 10 s head, and 4 concurrent checks per pod | A `gh api graphql` query past these fails with a named bound. Concurrent large writes wait or get 429. | The prototype's 256 KiB body, which refuses a legal PR body. Or no token limit, which measured 74.9 MiB against a 64 Mi example request **[captured]**. |
| 31 | Quick actions | Decode bodies and refuse lines naming commands that reach review or merge state, whatever the context, with `strict` as an option | It is a deny list that fails open on a new command, and a literal `/merge` line in a code block is refused. | The prototype's refusal of every `/word` line, which rejects `/usr/bin/env` **[captured]**. Or no check, relying on the token, which S13 has not shown to bind quick actions. |
| 32 | Query root fields | Admit any query, within the bounds | A query can read whatever the child reads, under any root field. | A root list, as the prototype had, which breaks `gh api graphql` on roots such as `rateLimit` and adds no write protection. |
| 33 | Flavours | One mutation list on both GitHub flavours, with prefixes and `meta` per flavour | Each flavour admits mutations `gh` does not currently send there. | A list per flavour, which breaks `gh` when a GHES upgrade flips its feature detection. |
| 34 | Review comments | `gh pr review --comment` stays refused | An agent loses inline review comments, and `gh pr comment` is the substitute. | Admit `event: COMMENT`, which the strict decoding makes checkable, at the cost of reasoning about pending reviews and their submission. |
| 35 | `commentAutomation` | Required, with no default, when `prAuthoring` is written | A tenant admin must answer a question at setup and can answer it wrongly. | Default to `none`, which silently exposes tenants with bots. Default to `refuse`, which breaks `gh pr comment` for the common tenant with none. |

## Sources

Read on 2026-11-02 and 2026-11-03. These pages were read and their behaviour was not tested.

- GitHub: [authenticating on behalf of a user](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-with-a-github-app-on-behalf-of-a-user), [refreshing user access tokens](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/refreshing-user-access-tokens), [apps REST, scoped token](https://docs.github.com/en/rest/apps/apps), [the same on GHES 3.22](https://docs.github.com/en/enterprise-server@latest/rest/apps/apps), [OAuth application REST](https://docs.github.com/en/enterprise-server@latest/rest/apps/oauth-applications), [installations REST](https://docs.github.com/en/rest/apps/installations), [permissions required for GitHub Apps](https://docs.github.com/en/rest/authentication/permissions-required-for-github-apps), [REST rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api), [authorizing OAuth apps](https://docs.github.com/en/apps/oauth-apps/using-oauth-apps/authorizing-oauth-apps), [protected branches](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches), [branch protection REST](https://docs.github.com/en/rest/branches/branch-protection), [repositories REST](https://docs.github.com/en/rest/repos/repos), [reviewing Copilot pull requests](https://docs.github.com/en/copilot/how-tos/use-copilot-agents/coding-agent/review-copilot-prs), [issue comments REST](https://docs.github.com/en/rest/issues/comments), [the Dependabot comment command change](https://github.blog/changelog/2026-01-27-changes-to-github-dependabot-pull-request-comment-commands/), and [available rules for rulesets](https://docs.github.com/en/enterprise-cloud@latest/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets).
- GitLab: [OAuth 2.0 API](https://docs.gitlab.com/api/oauth2/), [OAuth provider and scopes](https://docs.gitlab.com/integration/oauth_provider/), [fine-grained tokens post](https://about.gitlab.com/blog/fine-grained-pats/), [fine-grained tokens](https://docs.gitlab.com/auth/tokens/fine_grained_access_tokens/), [fine-grained REST permissions](https://docs.gitlab.com/auth/tokens/fine_grained_access_tokens_rest/), [user tokens API](https://docs.gitlab.com/api/user_tokens/), [push options](https://docs.gitlab.com/topics/git/commit/#push-options-for-merge-requests), [approval settings](https://docs.gitlab.com/user/project/merge_requests/approvals/settings/), and [quick actions](https://docs.gitlab.com/user/project/quick_actions/).
- Coder: [external authentication](https://coder.com/docs/admin/external-auth.md).
- Capture: `spike-gh/RESULTS.md` in the session scratchpad, with `raw/`, of `gh` 2.103.3 (cli/cli tag v2.103.3) and `glab` 1.127.0, gqlparser 2.5.33, graphql-go, graphql-js 17.0.2, graphql-ruby 2.6.14, and `graphql-c_parser` 1.1.4. It should move into the repository's validation records if this design proceeds, because the scratchpad does not persist.
- Client source for revision 1: [cli/cli](https://github.com/cli/cli) at `c4513d446bf98cfcbe72ed83f17cd3220174d88f`, in `api/queries_pr.go`, `api/queries_pr_review.go`, `api/queries_user.go`, `pkg/cmd/pr/create/create.go`, `pkg/cmd/pr/merge/http.go`, `pkg/cmd/api/http.go`, `internal/featuredetection/feature_detection.go`, and `internal/ghinstance/host.go`. [gitlab-org/cli](https://gitlab.com/gitlab-org/cli) on `main`, in `internal/commands/mr/create/mr_create.go` and `internal/commands/mr/approve/mr_approve.go`.
