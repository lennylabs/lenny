# User-scoped repository access and pull request authoring

Status: design for owner review, revision 25, 2026-11-06, applying the owner's settling of decisions 7 and 10 after the live spikes: per-session child tokens with no Lenny limit, and pushes as the user with no branch namespace, and adding decision 46 for the email finding of S6. Revision 24 recorded the live GitHub spikes S1, S2, S3, S5, S6, S15, and S16 under "Validation owed", and the owner's settling of decisions 1, 2, 4 to 6, 9 (superseded by 8), and 11, with decision 4 allowing several link providers on one host, applying the requests in `tenant-derived-runtimes-design.md`. It reads against `egress-and-git-design.md` revision 59, `user-credential-delivery-design.md` revision 17, and `tenant-scoping-identity-design.md` revision 21. Revisions 12 to 17 change only the sentences that last design made false. Revisions 5 to 10 resolve six rounds of review (see the "Review of revision" sections), and the owner's decisions 3, 35, 36, 40, and 42 are recorded, in the words "go with T and your recommendations". The spikes' stub proves forwarding only. Rules changed after the last run are **[design]**. Nothing in this document is built or staged. **Revision 4 applies owner decisions D1 and D2: the `vcs-authoring` allowlist filter is replaced by one generic binding with optional request rules held as data, with a GitHub deny preset on by default. The mechanism now lives in `user-credential-delivery-design.md`, and this document configures it for VCS hosts.** The capture of `gh` 2.103.3 and `glab` 1.127.0 stays here as history, and text it supported is marked superseded where the allowlist is gone. **The capture used plain git remotes. Under the prior loopback config `gh` and `glab` stopped before any network step, and under T, which the owner settled, they work [measured], so UR-5 is met on a stub, and live hosts are owed.** The stub executes nothing and enforces nothing, so a 200 at the stub says only that a request was forwarded. No request reached a provider, and no live spike has run. The bypass analysis under deny mode ran on the stub **[measured]**. Statements about the tree were checked by reading the code and the specification on this date. Statements about GitHub, GitHub Enterprise Server (GHES), GitLab, and Coder come from the pages under "Sources". Proposals carry the label UG-n. This revision followed independent security and consistency reviews whose final stop check found no Major or higher issue. That check traced the last round's changes and was not an end-to-end read, and no production component or live provider write has run.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[measured]** | A hands-on result from a validation round of `egress-and-git-design.md`, or from the spike of 2026-11-03 on candidates L, T, and P and the deny preset. That spike's origin was stock `git http-backend` behind a recording stub. **The stub proves forwarding only:** a row that reached it says nothing about whether a host would accept the spelling, grant the permission, or perform the action. |
| **[captured]** | Seen at the stub or at the prototype `netagent` in the capture of 2026-11-03. It shows what the client sent and what the filter did. It shows nothing about what a provider would do. |
| **[stub cannot show]** | A question only the provider can answer. Each one has a spike under "Validation owed". |
| **[live]** | Read from a provider's host, anonymously, with no write. |
| **[documented]** | Stated on a vendor page under "Sources". The vendor's page was read and its behaviour was not tested. |
| **[source]** | Read in the Lenny tree, the specification, or client tool source at cli/cli tag v2.103.3. |
| **[pending]** | Waits on a measurement or decision named beside it. |
| **[design]** | A statement of what this design requires. Nothing implements it. |

## Problem

Feedback on Lenny asked for user-scoped repository access and pull request authoring, similar to what Coder offers. These gaps stand behind it.

- **Authorization.** At session bind, the egress design checks a repository against the tenant's allowlist and the VCS pool's `allowWrite` ("Credentials", **Write scope**). It does not ask whether the requesting user can access the repository. Any user in a tenant reaches any allowlisted repository through the App.
- **Authorship.** A pull request created with an App token has the App as its author. GitHub then sees no relationship between the requesting user and the pull request, so that user can approve the agent's work, which defeats two-person review.

These are separate features with different risk. Authorization keeps the user's token inside the Token Service. Pull request authoring lets an agent act with a token derived from a user's authority. This document therefore separates them, and either one's proposal can be rejected without affecting the other's dependencies, except that UG-3 needs UG-2's link and check.

Pull request authoring is not a special case in the pod any more. It is a `userCredential` binding of `user-credential-delivery-design.md`, with an `oauth2Link` source, a narrowing template, and a preset, all held as data.

## Scope

This design covers account linking (UG-1), repository authorization on GitHub, GHES, and GitLab (UG-2), pull request authoring on GitHub and GHES as a configuration of the generic mechanism (UG-3), an approval check for tenants that refuse user tokens (UG-4), and user-authored merge requests on GitLab (UG-5).

Out of scope: tenant-wide session content reads, the repository visibility rule, and EG-12 defaults. `egress-and-git-design.md` owns egress and git, and this document lists what it needs under "Changes requested of egress-and-git-design.md". `user-credential-delivery-design.md` owns the `userCredential` binding, the `registered` and `oauth2Link` sources, `file` delivery, request rules, and presets. `security-oversight-design.md` owns repository rules and guardrails, `tenant-scoping-identity-design.md` owns user lifecycle and environments, `tenant-outbound-connector-design.md` owns private hosts, and `forensics-evidence-preservation-design.md` owns preservation.

## Terms

| Term | Meaning |
| :-- | :-- |
| Link provider | A tenant object naming a provider kind and OAuth client. A tenant can have several on one host, and each `required` VCS pool and each `oauth2Link` type names the one it uses. |
| Link | One Lenny user's stored authorization under one link provider, keyed on tenant, user, and link provider name. |
| Parent token | The user access token and refresh token a link holds. On a VCS kind, both stay in the Token Service. `user-credential-delivery-design.md` states the `kind: oauth2` exception. |
| Child token | On GitHub and GHES, a token derived from the parent by `POST /applications/{client_id}/token/scoped`, limited to named repositories and permissions. |
| Verdict | The result of a repository check: `allowed`, `refused`, or `unavailable`. |
| Request rules | Data on an `inject` binding, optionally from a preset, that refuses requests before the token is attached. `user-credential-delivery-design.md` defines them. |
| Token trust record | Everything that decides where a stored token can go and who can read it, with a digest on each stored token. `user-credential-delivery-design.md` defines it. |
| `user_act_required` | The one state of a stored token that needs a new user act: a relink here. It is derived from a trust record mismatch, and stored when a provider refuses a refresh. |
| Request filter | Revision 3's provider-specific allowlist, `vcs-authoring`. **Superseded by request rules.** The name survives in the history below. |

The word "connector" stays bound to §9.3 and to the outbound connector design's `outbound` identifiers. No new link, channel, or register enters §28: the new Token Service calls sit on `schemas/lenny-tokenservice.proto`, which [§28.7 Wire-contract artifact register](../spec/28_communication-channels.md#287-wire-contract-artifact-register) records as a surface the channel register holds no entry for, and the I8 fields are additions to an interface the egress design defines.

## Personas and requirements

Alice is a developer at `acme` and Bob is her reviewer. Carol is `acme`'s tenant admin and Dave is its `security-admin`. Tenant `globex` runs on GitHub with its own App. Tenant `initech` runs self-managed GitLab.

- UR-1. Alice links her account once per link provider, and she sees the prompt before her session starts rather than a failure in the middle of it. This applies to a `required` pool, which `prAuthoring: user` needs.
- UR-2. A session reaches a repository under a VCS pool that requires user authorization only when the requesting user can reach it at the access level the session declared.
- UR-3. When Alice loses access, her session stops reading and writing that repository within a stated bound.
- UR-4. Alice's pull request has Alice as its author, and Alice cannot supply the approval it needs. On GitHub this holds for work her agent pushes to anyone's pull request when the repository requires approval of the most recent push, because her agent pushes as her (S3). Without that repository rule, GitHub lets a pusher approve, as it does for a person pushing by hand ("Residual risks").
- UR-5. An agent running any `gh` or `glab` command works with the tool unmodified and with a placeholder as its token, **within the token's permissions**, except what the preset refuses, which a tenant can change. The host refuses what the token does not permit: under the default `pull_requests: write` and `contents: read`, that includes `gh issue create` and `gh run`, and likely `gh issue view` and `gh pr checks` (S15). `permissions` is data a tenant can widen. **This is met on a stub under T, and live hosts are owed:** `gh` and `glab` worked on every command listed under "Git remotes and client tools" **[measured]**. `gh` needs git in the image even with `-R`, and `gh release upload` needs a binding on `uploads.github.com`. A new `gh` release needs no change here, because a new operation passes by default.
- UR-6. With the preset on, no agent can approve or merge through an operation the preset names, with a credential Lenny attaches under `inject`. **This is a deny list, and the owner accepted that a new operation with that power passes until the preset is updated.** A tenant can clear the preset unless a `security-admin` requires it. It does not hold for a token the runtime holds under `file`, or against text a repository's own automation reads as a command, which "Comment-triggered actions" treats.
- UR-7. Automated sessions keep working under a named service identity, and a person cannot use that path to skip the check.
- UR-8. A tenant can refuse user tokens in pods and still get a two-person review check.

## Threat model

### Security properties

- UP-1. A refresh token never leaves the Token Service, and it goes only where its token trust record says. On a VCS kind a parent access token never leaves either, because a delivering VCS provider must name a platform-shipped narrowing template. On `kind: oauth2` with no template, `netagent` receives the parent access token under the record's delivery ceiling, as `user-credential-delivery-design.md` states, and a replica can obtain it under `leakScan` on a `file` binding. The mirror never holds a parent token. The Token Service API and the `user_links` store hold this.
- UP-2. Under a platform-shipped narrowing template, a child token is limited at the host to the session's `granted` writable repositories and to the template's `permissions`, by default `pull_requests: write` and `contents: read`, and the git child under `pushIdentity: user` to `contents: write` alone. The host enforces this, which S15 measured live for the repository limit and the merge refusal. The write path requires a shipped template on a VCS kind whose `delivery` is not `none`.
- UP-3. Under `inject`, `netagent` attaches a user-derived token only to the binding's hosts and only to a request the binding's request rules admit, with `default: allow` and the preset as this configuration's default. A credential whose required handling `netagent` does not implement is refused. `netagent` holds this, and the Token Service sets the rules and the requirement. Under `file` none of this applies.
- UP-4. A session with a requesting user never receives an App token for a repository under a pool that requires user authorization unless a fresh verdict says the user holds that access. The Token Service holds this before its cache lookup, and `lenny-egress-authz` holds it independently from the recorded entry state.
- UP-5. A user-derived token never reaches a pod that can serve another session or a later session. The binding validator, the session gateway check, and the Token Service each hold this.
- UP-6. A session with no link, or a failed or unavailable check, never falls through to an App token on such a pool.
- UP-7. An impersonation bearer cannot cause a linked token to be used. The gateway's session input check holds this, over the session's delegation tree, under `user-credential-delivery-design.md`.
- UP-8. One tenant's link, check result, or token never reaches another tenant. Link rows sit under row-level security, and every cache key carries the tenant.
- UP-9. A link's provider account is one the tenant's identity source ties to the Lenny user, unless the tenant records that it does not. The link flow and the sweep hold this.
- UP-10. No token held under this design's links leaves the Token Service except through I8, under the binding's delivery and the provider's `delivery`, or to a session's coordinating gateway replica under `leakScan`, and a compromised replica can acquire any such session's lease, as `user-credential-delivery-design.md` states. No gateway connector path receives one. Decision 3 refuses a connector of its own on these hosts when a pool on the host is `required`.

### Threats

| Attacker | Can | Cannot | Why |
| :-- | :-- | :-- | :-- |
| Prompt-injected agent on Alice's session | Create, edit, close, retarget, and comment on pull requests as Alice on a session repository, including Bob's, and push as Alice wherever she can push there. Read what the child token reads. Waste Alice's rate limit up to the session's quota. Write text a repository's bot treats as a command. Use a new operation with merge or review power that the preset does not yet name. | Use an operation the preset names, unless a tenant cleared it. Act outside the session's repositories under a narrowing template. Read the token under `inject`. | Request rules refuse, the host limits the child, and `inject` keeps the token in `netagent`. Deny mode's bypass analysis ran on the stub **[measured]**, which proves forwarding only. |
| Two injected sessions, Alice's and Bob's | Each opens a pull request as its user. Each can comment on the other's, which reaches any comment-driven approval bot a tenant has not addressed. | Approve the other's pull request through the review operations the preset names. | The preset names `addPullRequestReview` and the REST review route. A tenant adds `bodyPatterns` or denies `addComment` for its bots. |
| Compromised `netagent` on Alice's pod | Use the session's children and any App token it holds until they expire, including approving within the session's repositories, because GitHub has no permission narrower than `pull_requests: write` for this, and merging an approved pull request as Alice with the git child. | Obtain the parent or refresh token. Reach repositories outside the child's list on GitHub or GHES. Mint after the pod is quarantined or the binding ends. | UP-1 and UP-2. "Residual risks" states this plainly. |
| Alice herself, against review | Link a second account, have her agent author under it, and approve as herself. | Do so when the link provider sets an `identityBinding`, since the flow refuses an account that is not hers. | UP-9. Under `unverified` she can, and posture says so. |
| Prompt-injected agent calling a connector tool | Reach a GitHub MCP server through a tenant's connector, where one is allowed, and call its tools under that connector's token. | Reach it with a token from a link. | UP-10. Decision 3 refuses a connector on these hosts when a pool on the host is `required`. |
| Prompt-injected agent using git push options | Ask GitLab to create and auto-merge a merge request, or skip CI and secret scanning, on a push that passes ref checks. | Send a push option on a covered host. | `netagent` refuses them at `git-receive-pack`, under a requested change. |
| Compromised `lenny-egress-authz` | Misdecide a CONNECT or a scope decision, or remove a host from the covered set, so git to it goes as an opaque tunnel and a token the agent holds reaches it, with no mint on that path, as the egress design states. | Mint, or remove the Token Service's own check and its rules. | The egress design's two gates, and `handling` and resolved rules coming from the Token Service on I8. |
| Attacker who tricks Bob into completing a link flow | Send Bob an authorization URL or a start URL. | Bind Bob's GitHub account to the attacker's Lenny user, or the attacker's account to Bob's Lenny user. | The start page requires a browser login matching the flow's subject, and the callback checks a cookie bound to that login. |
| Prompt-injected agent crafting bytes for a parser difference | Send documents and JSON that a rule evaluator and a host read differently. | Have those bytes reach the host while rules are in force. | `netagent` forwards a document and body it generated from the checked structure. See "What reaches the host". |
| Prompt-injected agent holding a `file` token | Everything that token permits, from anywhere. | Be limited by anything in this table. | A tenant chose this under `user-credential-delivery-design.md`'s opt-in, and a `required` pool refuses it unless flagged. |
| Hostile tenant registering a host | Run a fake GHES or GitLab that answers checks and token calls as it likes, for its own users. Return hostile bodies and redirects to the Token Service. | Affect another tenant. Shadow a platform-reserved host. Receive another tenant's client secret, token, or cached verdict. | The egress design's mint path bounds, connector C8 and C9, and tenant-keyed caches. |
| Tenant admin | Delete links in the tenant and read their metadata. Edit a token trust record, which makes every stored token under it unusable. **Trick a user past the page into granting a new token to a host she runs.** Change a runtime and read a `file` token. | Read a link's stored token through any change to the record, because a mismatched token is sent nowhere. | T4 envelope encryption, no read route, and UCP-5 of that document. The future grant has no per-field fix, because she configures the tenant. That document's decision 4 records the owner's settlement, and `tokenTrust.allowedRecords` is the enforcing control. |

## Capture results

**This section is history.** It records what was measured against revision 3's allowlist, and the measurements stand. Revision 4 replaced that allowlist with the preset model. Where a row below names the filter, it describes that filter. Deny mode's bypass analysis ran later on the stub, under S19 **[measured]**.

A capture ran on 2026-11-03: `gh` 2.103.3 and `glab` 1.127.0, against a recording stub on a Docker `--internal` network, through a Go `netagent` prototype with `filter.go` and gqlparser 2.5.33, on a laptop under other load. Results and raw output are under `evidence/user-git-identity-design/spike-gh/`, in `RESULTS.md` and `raw/`. The github.com flavour used `api.github.com` as a network alias for the stub.

### What moved from design to measured

| Revision 1 claim | Revision 2 status |
| :-- | :-- |
| `gh` sends `pr create`, `pr review`, and `pr merge` over GraphQL | **[captured]**. A REST allowlist does not cover `gh`. |
| `gh pr create` works through the filter with a placeholder | **[captured]**, **with plain remotes only**. 10 of 10 variants per flavour, with the stub receiving the same requests with and without the filter, carrying the injected token and no placeholder. |
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

### Why the allowlist was replaced

The owner's conditions for keeping a GitHub-specific injection were that it survive `gh` updates and work for all `gh` commands. Both were measured false for the allowlist.

- **All commands.** 25 of 42 `gh` cases exited 0 under the prototype's policy and 17 failed, including `pr comment`, `pr edit`, `pr ready`, and `pr close` by policy **[captured]**.
- **Headers.** On a Debian image `gh` sent `Time-Zone`, and the forwarded-header allowlist refused every GraphQL request with 403 **[captured]**. The earlier capture ran on Alpine only.
- **Updates.** Of 68 `gh` releases since 2024-10-01, 4 changed the mutation set. v2.73.0 added `replaceActorsForAssignable` and `deleteIssueComment`, v2.83.0 added `revertPullRequest`, v2.87.0 added `requestReviewsByLogin`, and v2.94.0 added the discussions and sub-issue set. 16 of the 66 mutations in `gh` 2.103.3 were on the old table and 50 were off **[source]**. Those are names only and a lower bound on drift, read from source and not run.
- **The server side.** GitHub's schema went from 122 mutations (2021-01) to 278 (now), and in one reading about one a year needs only `pull_requests: write` and carries merge or review power **[source]**. That reading is what the owner accepted as the deny preset's cost.

### What the capture did not test

It used plain git remotes. Under the prior `insteadOf` config, `gh` and `glab` stopped earlier, which was reproduced afterwards (see "Git remotes and client tools"). It also ran no `git push`.

### What the capture cannot show

These stay **[stub cannot show]** and are spike S15 unless noted:

- Whether `pull_requests: write` permits approval on a provider-issued token. The permission table says yes **[documented]**, which is why the preset names approval.
- Whether merging fails without `contents: write`. The table says it needs that permission for REST **[documented]**. The GraphQL mutations' checks are unknown.
- Whether a child's repository limit binds node-ID mutations, since `createPullRequest` takes an opaque `repositoryId` and others take node IDs. The filter checks operations and leaves targets to the host.
- Whether GitHub honours batches, GET queries, form bodies, or method overrides, and which GraphQL parser it runs.
- What GitLab does with `/merge` in a description for a given token, and how it routes an encoded project path (S17).
- `gh`'s behaviour against an older GHES, since the stub answers `__type` as if every field exists, which selects `gh`'s newest code paths.
- Whether any field fails under a narrow child on `gh pr view`, because the stub answers everything.

## Review of revision 2

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

## Verification of the review findings

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

## Current state

| Claim | Status | Source |
| :-- | :-- | :-- |
| §9.3's flow starts inside a session, through URL-mode elicitation on a tool call | True for the specification. The code also has an admin route any authenticated caller can use. | [§9.3 OAuth/OIDC Flow](../spec/09_mcp-integration.md#oauthoidc-flow), `pkg/gateway/externalapi/admin/connector_oauth.go` |
| Connector tokens are keyed on tenant, connector, user, and environment | True | `connectorcredstore.go`, migration 0059 |
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
- §4.3 scopes connector tokens by environment. A link records a fact about a person on a host, so a member of two environments would otherwise link twice for one account.
- The connector store is decrypted in the gateway (F-4.3.1). UP-1 keeps parent tokens away from the gateway.
- Connector refresh has no caller, and no store has a refresh lease.
- The callback is open to the account-link attack in "Defects found while verifying" (F-9.3.18).

### User tokens on a VCS host

Revision 3 said a host has one store of user tokens per tenant and refused VCS providers on `/v1/credentials`. The owner's generic mechanism must not be tied to or excluded from GitHub, so this is reworked.

- **The defect stays refused.** `provider: github` on `POST /v1/credentials` can never deliver, so it is refused with `VALIDATION_ERROR`, as are `aws_bedrock` and `vault_transit`, which have the same defect **[source]**. `user-credential-delivery-design.md` does this in UC-1.
- **A tenant-defined type on a GitHub host is possible.** Under that design, a tenant can define type `gh-pat` with `source: registered` and bind it to `api.github.com` or `github.com`, subject to the refusals "Other tokens on these hosts" states. A type's hosts, rather than a binding's, are what every rule evaluates. Its `file` delivery needs `userCredentialDelivery`, a ceiling of `file`, and a binding opt-in. "What a `required` pool still gives" in that document states the result, and the summary is under "Other tokens on these hosts" here.
- **The check names one account.** A `required` pool's check uses its link provider and no other row, so a `registered` token on the same host never satisfies UG-2.

**Connectors.** Revision 3 refused a connector on a token-holding link provider's hosts because a connector's token reaches the host with "no request filter, repository list, pod model check, sweep, or `cacheUntil`". Part of that reason has weakened and part has not:

| Reason | Status now |
| :-- | :-- |
| No request filter on the connector path | Weaker. The filter is now an optional preset that a tenant can clear on the pod path, and §10.6's `connectorSelector` already filters a connector's tools by capability, with `deniedCapabilities`, inferred from MCP `ToolAnnotations`, plus `PreConnectorRequest` interceptors **[source]**. Whether those annotations mark a review tool as write is not established. |
| No repository list | Holds. A connector's token is not narrowed per session. |
| No user authorization check or sweep | Holds, against UR-2, wherever the pool is `required`. |
| No `cacheUntil` | Holds, and §9.3's refresh has no caller at all **[source]**. |
| No pod model check | No longer a reason. The connector path is on the gateway, which attributes each call to its session. |

Revision 2's `hostLink`, which would have given a connector a link's token, stays withdrawn, because it is the one version of this that breaks UP-1. **The owner settled decision 3: a connector is refused on a link provider's host set only when a pool on that host is `required`,** since UR-2 is the property a connector skips.

### Data model

**`link_providers`** (tenant object, written through EG-9's single write path, with an optional TS-1 `environments` list). `user-credential-delivery-design.md` generalizes this to any OAuth2 provider with `kind: oauth2`, and a user credential type names a link provider through `source: oauth2Link`. The fields below are those of the VCS kinds.

| Field | Meaning |
| :-- | :-- |
| `name` | The key, unique per tenant. Links and routes name it, from UG-1 onward, so UC-2 reworks no stored key or route. |
| `host` | On a VCS kind, the canonical host, covered by one of the tenant's VCS pools. Several VCS-kind providers can share a host, such as a `metadata: read` App for the check and an authoring App. |
| `delivery` | The delivery ceiling, in the token trust record: `identifier`, which keeps the identity and discards the token, for UG-4; `none`, which keeps the token for a check; `inject`; or `file`. The pages show it. A `required` pool needs `none` or above, and `prAuthoring: user` needs `inject` or above. |
| `apiHosts` | The hosts a type on this provider can reach. On a VCS kind the table in `user-credential-delivery-design.md`, "Selecting the flavour", fixes them. |
| `attach` | In the token trust record. On a VCS kind the kind fixes it. |
| `narrowing` | A platform-shipped template, with `permissions` as the one tenant override. Required on a VCS kind that delivers. |
| `kind` | `github`, `github-enterprise`, or `gitlab` here. The kind fixes the endpoint paths, so a tenant supplies no OAuth URL. |
| `clientId`, `clientSecret` | The tenant's own GitHub App or GitLab application. `clientSecret` is a write-only value the Token Service stores under a tenant-scoped key and re-checks at read, so no tenant can name another's. It is sealed with the provider's record digest, so any record change leaves it unusable until a write re-supplies it, and a revert restores it. |
| `allowedAccounts` | The organizations, GitLab groups, or login patterns a linked account must belong to. Empty means any account the OAuth client admits. |
| `identityBinding` | `directory`, `claim`, `verifiedEmail`, or `unverified`. No default: a link provider write must set it. See "Identity binding". |
| `scopes` | GitLab only: `read_api` and `read_user` by default. |

**`user_links`** (`TokenStore`, T4, row-level security, tenant guard, and §4.9.1 re-encryption):

| Column | Meaning |
| :-- | :-- |
| `tenant_id`, `user_id`, `link_provider` | The primary key. |
| `link_ref` | A random identifier, regenerated on each link, used in evidence. It carries no meaning outside this row. |
| `provider_user_id`, `provider_login`, `provider_is_admin` | From the provider's user endpoint. Bounded in length and limited to the provider's login syntax, because a registered host is hostile input. Unique on tenant, link provider, and `provider_user_id`. Across a VCS kind's providers on one host, a `provider_user_id` belongs to one Lenny user: a link that names an account another user holds through any provider on that host is refused, so decision 5 holds whichever App was used, and one person can link the same account through several providers. Removing or revoking a person's link on a host covers each of their links there. |
| `trust_digest` | The digest of the provider's token trust record at link time, sealed as additional data on each envelope with tenant, role, user, provider name, and the fields written with the value, under the encoding that document states. A mismatch makes the row unusable and sends it nowhere. |
| `access_token_ct`, `refresh_token_ct`, `scopes` | Envelope-encrypted tokens. |
| `access_expires_at`, `refresh_expires_at`, `generation` | Expiry, and a counter that changes on refresh and relink. |
| `state` | `active`, `user_act_required`, `suspended`, or `revoking`. A mismatch reports `user_act_required` with no write. |
| `refresh_lease_holder`, `refresh_lease_until` | The refresh lease. |
| `verified_at`, `last_used_at`, `created_at` | Times. |

A user with accounts on two hosts holds two rows, and Lenny draws no connection between them. A user who wants a different account on one provider relinks, which revokes the old grant first.

Two Lenny users cannot link one provider account within a tenant. The unique index refuses the second with `409 LINK_ACCOUNT_IN_USE`, naming neither user, and the tenant admin can remove the first. A shared account makes `credential.mint` attribution false and gives one person a way around two-person review. The same account in two tenants is permitted, since the tenants are unrelated.

### Identity binding

Nothing else proves that a linked account belongs to the Lenny user. Alice can link a second or machine account, have her agent author under it, and approve as herself, which breaks UR-4. The check then evaluates that account's access, which breaks UR-2. The unique index stops two Lenny users sharing an account and says nothing about one user holding two. A link provider therefore states how a link is tied to a person.

| `identityBinding` | How the link flow decides | Cost |
| :-- | :-- | :-- |
| `directory` | A tenant's sync worker or admin writes the expected `provider_user_id` for a user through `PUT /v1/admin/users/{userId}/provider-identities/{provider}`, under `manage_user_lifecycle`. The link commits only when the provider's numeric identifier matches. | The tenant runs a mapping. The directory must hold provider accounts. |
| `claim` | The link provider names an OIDC claim, such as `github_login`. The start page reads it from the browser login in step 3, and the provider's login must match. The broker contract, or a customer source's claim mapping, gains that mapping. | A broker mapper or a source's claim, and login renames need the mapping to follow. After the first match, the numeric identifier is the key. |
| `verifiedEmail` | The provider's verified email must equal the `email` claim of the browser login. GitHub: `GET /user/emails`, which has `verified` and `primary` fields **[documented]**. Its permission for an App token is not stated, and neither is uniqueness across accounts (S6). GitLab: `GET /api/v4/user`. | It needs an authoritative IdP email, and the App gains an email permission. Self-managed instances can change a user's email by administrator. |
| `unverified` | No check. | UP-9 and part of UR-4 do not hold against the user. It still stops a prompt-injected agent, which needs no identity claim, and posture reports the value. |

Each binding re-evaluates on the sweep: when a directory row or claim source changes, the link moves to `user_act_required`. Lenny reads the claim at the start page alone and does not store it. `Principal` gains nothing **[source]**.

Binding covers the account Lenny uses. It does not cover who approves at the host. A person with a second account in the organization can still approve from it, and an organization's single sign-on and managed-user settings address that. "Residual risks" states it.

### App choice

A link provider's App need not be the App in the VCS pool, because the check needs only the user's access. A tenant that wants UG-2 alone should register a separate App with `metadata: read` and no other permission, installed on the same organizations, which leaves the stored parent token with no write power at all. UG-3 needs an App with `pull_requests: write` and `contents: read`. GitHub limits a user token to the App's permissions and to accounts where the App is installed **[documented]**, so a repository the linking App cannot see answers as `refused`. The App's user token expiry must stay on. A token response with no `expires_in` or `refresh_token` is refused at link with `LINK_TOKEN_NON_EXPIRING`, because GitHub lets an App opt out **[documented]**.

A link provider's client belongs to its tenant. An operator's App shared across tenants is out of scope: the only write path is the tenant's own, so each tenant admin would hold a client other tenants' users rely on, and the record's sealed secret would need a platform-only path. Decision 45 records this.

### Flow

The user experience follows Coder's: a named provider instance per host, self-hosted URLs, and an authorization step before the workspace exists.

1. Alice calls `POST /v1/sessions` with a plan under a VCS pool that requires user authorization and has no link. The gateway answers `403 USER_LINK_REQUIRED` with `details.linkUrl`, before any pod claim. MCP clients receive the same URL as a URL-mode elicitation. She can also call `POST /v1/user-links` with `{"provider": "github-acme"}` from the playground or `lenny-ctl`.
2. `linkUrl` is a Lenny URL holding a random one-time flow identifier, with a 10-minute lifetime, naming the subject that requested it.
3. The browser opens it. The start page requires an OIDC login at the tenant's identity source (`auth.oidc.issuerUrl` by default, and a customer's issuer under `tenant-scoping-identity-design.md` TS-6 and TS-7) through that design's "Browser login", whose shared resolver supplies the tenant and subject, and it refuses the flow when the logged-in tenant and subject differ from the flow's. This defeats an attacker who sends a victim a start URL. **Before any redirect**, the page names every endpoint host and the delivery ceiling, and after a token trust record change it states that they changed, naming the old and new hosts. The flow records the current digest, and step 6 refuses if it has moved.
4. The page sets a `__Host-` cookie, `HttpOnly`, `Secure`, and `SameSite=Lax`, holding a random nonce, and redirects to the provider with PKCE and a `state` that signs the flow identifier and the nonce's hash. A `SameSite=Lax` cookie is sent on the provider's top-level redirect back.
5. The provider redirects to `GET /v1/user-links/callback/{provider}`, a path per provider, and `iss` is checked where the provider returns it (RFC 9207). A path whose provider differs from the flow's is refused before any token request. The gateway verifies the signature, consumes the flow once, and requires the cookie's nonce to match. A missing or mismatched cookie aborts the flow. This defeats a victim's browser completing an attacker's flow, in either direction.
6. The gateway passes the code and verifier to the Token Service over mTLS and never sees a token. The Token Service compares the digest, exchanges them through the mint path, reads the provider's user endpoint, applies `identityBinding`, and applies `allowedAccounts`: GitHub through `GET /user/installations` **[documented]**, and GitLab through group membership with `read_api`. A refused binding answers `403 LINK_IDENTITY_MISMATCH`, and the token is revoked at the provider at once.
7. The browser shows a confirmation page naming the provider login, the Lenny user, the endpoint hosts, and the delivery ceiling. Alice confirms, and the row commits with its `trust_digest`. An audit event, `user_link.created`, carries the tenant, user, provider name, `link_ref`, and provider login, and it is delivered to Alice as a notification.

`DELETE /v1/user-links/{provider}` lets Alice remove her link, and a tenant admin holding `manage_user_links` can remove anyone's. Neither route returns a token. Impersonation bearers are refused on every link route, using the same check as session create.

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

- **Trigger.** A check or mint that finds fewer than 15 minutes on the parent, or a 401 from the host with the parent, requests a refresh. Refresh is on demand, so an idle link costs nothing. A refresh token that lapses (6 months idle on GitHub) moves the link to `user_act_required`.
- **Lease.** A conditional update sets `refresh_lease_holder` and `refresh_lease_until` (30 seconds) when the lease is free. Session-level advisory locks are unavailable through the §12 transaction-mode pooler, and a transaction open across a provider call would hold a connection, so the lease is a row rather than a lock. Other replicas wait up to 5 seconds by re-reading the row, and otherwise answer `unavailable`.
- **Commit.** The holder calls the provider with a 10-second bound and writes the new tokens and the incremented `generation` before anything else. A store failure after the provider's answer is retried from memory for the lease's remainder.
- **A lost response.** A timeout or a crash between the provider's rotation and the commit loses both tokens. A retry of the old refresh token that the provider refuses moves the link to `user_act_required`. Neither provider's page describes an idempotent refresh, so this cost cannot be designed out. Decision 6 records it, and a metric and alert report the relink rate.
- **Sessions.** A session holds a child token on GitHub and GHES, and on GitLab whatever the configuration under "GitLab" selects. The parent never reaches a pod, so rotating it cannot break a tool mid-request except through the unknown in S1. If S1 shows a child dies with its parent, `netagent` sees a 401, flushes, and asks I8 once under the `reauth-on-401` handling. A 401 means the host did not act, so one retry of a buffered request is safe, including a mutation. A streamed write's 401 goes back to the client. The Token Service also caps `cacheUntil` so a child is re-requested before the parent's scheduled refresh.
- **`user_act_required`** refuses new sessions with `USER_LINK_REQUIRED` and moves running sessions as a refused verdict does, with the reason `user_act_required`.

### Hostile hosts

A self-managed host is tenant-registered input, as `tenant-outbound-connector-design.md` states in C9.

- The kind fixes endpoint paths, so a tenant supplies a host and no OAuth URL. The authorization redirect targets that host over HTTPS only, and the §9.2 URL domain check applies.
- Every Token Service call to a link provider endpoint goes through `lenny-egress`, with the tenant asserted, its CA, and the mint path's bounds on time, response size, and redirects. The egress design's direct dial excepts these endpoints. The provider's declared mint endpoints gain the token path on `github.com`, since today they name `api.github.com` **[design]**.
- Connector C8 holds: a registered host cannot shadow a platform-reserved one, so a tenant cannot register `github.com` behind its own network.
- The provider login and identifier are syntax-limited before they are stored or shown, and a hostile host that answers `allowed` to everything harms only that tenant.
- Verdict caches, child caches, and refresh leases all key on tenant and `link_ref`, following the egress design's mint cache rule.

### Lifecycle and revocation at the provider

| Event | Stops use | Revokes at the provider |
| :-- | :-- | :-- |
| User deletes the link | At once, by binding change | Yes |
| Relink | Yes | Yes, before the new flow commits |
| `soft_disable`, `hard_disable`, or `full_revoke` ([§11.4](../spec/11_policy-and-controls.md#114-user-invalidation), TS-4) | At once. A new step in §11.4 sets `state = revoking` and pushes binding changes for the user's sessions. | Yes. Reinstate restores no link, and the user relinks. |
| [§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces) erasure, or `DeleteByTenant` | The Article 18 restriction and the drain stop use | Yes, then the row is deleted |
| App or application removed by the user at the provider | At the next check, as a 401 that refresh cannot cure | Already done |
| Link provider deleted | At once for its links. The provider enters `deleting`, keeping it and its record until links drain or `linkProviderDrainSeconds` ends, and a recreate under that name is refused. | Yes, for each link, through the durable record below, under the ordinary comparison. At the bound, or after a mismatch, each remaining row ends with an audit event and a user notification saying its grant was not revoked. |
| A token trust record changes, by any field | At once, by digest comparison at I8 and at each provider call. | **No.** Lenny sends the stored tokens to neither the old nor the new endpoint, and the audit event and the user's notification say so. A revert to the identical record restores use. |

Each "Yes" above applies when the row's digest matches, because a mismatched token is sent nowhere. Revocation runs through a durable record retried with backoff, following the egress design's pending-revocation record for GitLab project tokens, because a host can be unreachable. Erasure has a limit: T4 data must be erased within 1 hour under [§12.9](../spec/12_storage-architecture.md#129-data-classification). The job retries for up to 30 minutes, then destroys the ciphertext regardless, and the erasure receipt records `provider_revocation_failed`. Lenny then holds no copy, and the access token lapses by its own expiry at the host. The tenant admin is told, so the user can revoke the authorization at the provider.

## UG-2: Repository authorization

### The requirement sits on the VCS pool

`userAuthorization` on a VCS credential pool takes `off` (the default) or `required`. "Pool" in the proposal is ambiguous, and the VCS pool is the correct object: it is a tenant object, it names the repository prefixes, TS-1 can scope it by environment, and a shared warm pool is platform-global. A tenant's `security-admin` can force the value through a new `userAuthorization.require` guardrail under SO-2, which sets `suspendedBy` on non-compliant pools.

This work belongs in the Token Service and I8. `usercreds` is the §4.9 LLM reverse proxy path, and the proposal's change there would alter nothing the design's git path reads, so `pkg/gateway/credentials/usercreds/usercreds.go` is left as written.

### Declared repositories

Under the egress design, a session's readable set includes the tenant's whole allowlist, so an agent can fetch an undeclared allowlisted repository at run time. For repositories under a `required` pool, the readable set holds only what the session declared: its `gitClone` sources, a new `readableRepositories` plan field, its `writableRepositories`, and any private `gitRepository` warm fill on the pool it runs on. A default clone on a tenant-derived runtime is a `gitClone` source once composed, so it is declared, and the check runs on the session's user. There a tenant admin named the repository, so a refusal omits its name (`tenant-derived-runtimes-design.md`, DR-9). `netagent` refuses others with 403 and the decision identifier. Decision 22 records the cost and the on-demand alternative.

Public repositories under no `required` pool are unaffected, because nothing is authorized about them.

### The check

The Token Service makes one call with the parent token per repository and level. It compares the provider's numeric identifier with the one the pool stores, because GitHub serves a renamed repository under its old name and the egress design keys on the identifier.

| Host | Read | Write | Tag |
| :-- | :-- | :-- | :-- |
| GitHub and GHES | `GET /repos/{owner}/{repo}` returning 200, with `permissions.pull` | `permissions.push` | **[documented]** for the fields. S6 measured that they report the user's role, including an organization base permission, whatever the token's narrowing **[measured]**. That is what the check needs, because it runs with the parent. Nothing in Lenny reads these fields to learn what a child can do. `GET /user/installations/{installation_id}/repositories` documents "the access the user has to each repository" and is the fallback. |
| GitLab | `GET /api/v4/projects/:id` returning 200 with `read_api` | Effective access level of at least Developer | **[documented]** for the scopes. The field that reports inherited and shared access is S11. |

Classification, which every caller uses:

- `allowed`: the provider answered and the level holds.
- `refused`: 404, 403 without a rate-limit or SSO header, or a level below the declared one.
- `unavailable`: 5xx, 429, a 403 with a rate-limit header, a timeout, a CONNECT failure, a connector being offline, or a refresh lease not obtained.
- A 401 attempts refresh once and then counts as `refused` with the reason `user_act_required`.
- A 403 with GitHub's SSO header is `refused` with the reason `sso_required`, which the error shows so Alice can start a SAML session and retry (S4).

A provider `refused` is `USER_REPOSITORY_ACCESS_DENIED`, naming the repository Alice declared. It reveals nothing she did not name.

### When the check runs

| Moment | Who asks | Refused or unavailable means |
| :-- | :-- | :-- |
| Session bind, before `ls-refs`, and before any pod claim when the plan binds at create. A plan bound at finalize follows the create-time claim, so a refusal there leaves the session in `created` holding its pod until the session ends. | Gateway, for every declared repository at its level | Create or finalize refused. `unavailable` is a retryable `USER_AUTHORIZATION_UNAVAILABLE`. An unauthorized user therefore learns no commit. |
| Resume, and the provisional binding of §7.3 | Gateway | Resume refused, and §6.2's failure path applies |
| Each mint on I8 for git or a binding | Token Service, before its cache lookup | No token, and `netagent` answers 403 or 503 |
| Timer, each session | Gateway job calling the Token Service | The entry changes state (below) |
| Mirror read | The decision service, from the recorded entry | Mirror refuses |

The order on the mint path is fixed: verify pod, read the session, run the check, ask I3, and only then read the mint cache. The cache key keeps its form, because under this rule a cached App token is reused only by a session that has already passed. The check's result has its own cache, keyed on tenant, `link_ref`, repository identifier, and level, for 60 seconds, which is the "short-lived cache" the proposal asked for.

### Revocation

Mid-session revocation needs a writer and a route. The route exists: `netagent` flushes on every I7 binding change, and a binding push makes each decision service replica re-read the session row. This design adds the writer and closes the paths that skip it.

**Entry state.** Writable entries are already records with a `state`. Readable entries become records too (see the requested changes). A new state, `userRefused`, on a writable entry denies both writes and reads of that repository, at both gates and in `netagent`, and an unknown state on a readable entry denies access, as the egress design's hook states. Each entry records `verifiedAt`, the time of its last `allowed`. A value per session would let one repository's success mask another's `unavailable`.

**Sweep.** §4.3 has the gateway calling the Token Service, and the Token Service has no route to gateway replicas, so a gateway background job does this, as orphan cleanup does. Every `recheckIntervalSeconds` (300 by default, with up to 10% jitter) it calls the Token Service for each distinct link and repository among bound sessions, bypassing the verdict cache. A `refused` writes `userRefused` on the matching entries in the row's transaction and pushes a binding change. `allowed` updates that entry's `verifiedAt`, and every update for a session is one write to its row. Deduplication makes the provider load per link and repository rather than per session, and conditional requests lower it further.

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
- **Warm fills.** A warm fill has no user, so the mirror's warm fill keeps its App path. Its contents enter a pod an agent reads. A private `gitRepository` warm fill under a `required` VCS pool therefore counts as a declared read for every session bound to that warm pool, and a user lacking access is refused at bind with a reason that names the warm pool. A tenant needing per-user variance uses a warm pool with no such fill. A warm default clone on a tenant-derived runtime is such a fill, and its clone is already a declared read, so the same check covers both. A resume checks the runtime's default clones as well as the stored plan, because a clone added after the session bound can put a store the plan does not declare on the session's next pod. A default clone's `write` value adds a writable entry at bind, and a user who can read and not write gets a read-only session on that entry, as DR-9's "Write scope" states. A repository the client names keeps this design's refusal. `write: always` is refused under `prAuthoring: user`, so the attach rule's trigger always has a statement from the client behind it.
- **Outage rule.** The mirror's "Upstream outage" row, and its 6-hour confirmation, conflict with fail-closed authorization. The rule is:
  - A new session, a resume, and a mint need a current `allowed` from the provider. `unavailable` refuses them, with a retryable error. Writes need the host anyway.
  - A running session's reads of a repository continue from the mirror's store while that entry's `verifiedAt` is within `verdictMaxAgeSeconds` (900 by default, and a ceiling of 3600). Past that, both gates refuse reads of that repository until a sweep succeeds, and other repositories are unaffected.
  - The 6-hour fallback therefore never applies to a session on a `required` pool. It keeps its meaning for every other pool. A tenant that values outage reads above per-user limits leaves `userAuthorization` off.
  - A stalled sweep, whatever its cause, has the same effect after 900 s, which is P6's direction. `UserAuthorizationSweepStalled` alerts well before that.

### Pod models

UP-5 refuses a `userCredential` binding, with `422 USER_CREDENTIAL_UNSUPPORTED_POD` as `user-credential-delivery-design.md` states for every type, and a `required` VCS pool's repositories, with `422 USER_AUTHORIZATION_UNSUPPORTED_POD`, on these pods. The binding validator refuses a definition that can only reach such pods. That document's `retireOnRelease` keeps a pod's eligibility from following a later pool edit, and I8 refuses a release without it. It applies to these repositories too, and UC-3 builds it.

| Pod | Why |
| :-- | :-- |
| `maxConcurrentSessions > 1` | `netagent` cannot attribute a request to a slot, and the design already gives such pods the union of readable and writable sets. Alice's token would serve Bob's slot. |
| Service mode | One process serves several sessions, and §5.2 gives it no session binding. |
| `recycle.enabled: true` | §5.2 states the scrub is best-effort and "is not a security boundary". On a `keep` pod the runtime process, and any state an injected agent planted in it, runs into the next session, whose user's token then answers on `netagent`'s unauthenticated loopback port. Residue from Alice's repositories would also reach Bob. |
| A binding with `scope: always` | Its cache keys on the tenant pin and it attaches to every session. A user source is refused at write. |
| A pod that has served a session | The same reason as `recycle.enabled`. It catches a pool edited back to `false` with used pods in it. |

SDK-warm pods remain allowed, because the process that exists before bind runs deployer image code and no earlier agent session. The refusal costs concurrent and recycled pools their efficiency for these tenants, which decision 14 records. Service-mode runtimes cannot use a user identity at all under this design.

### Identity cases

The requesting user is read from the session row by both gates, through `pkg/binding`, and never from a token or a pod. A running session does not depend on its creator's bearer.

- **Impersonation.** A bearer whose `issued_tokens` row is an impersonation row already cannot create, derive, replay, or create-and-start. For a session on a `required` pool or holding a `userCredential` binding, `user-credential-delivery-design.md`'s session input check refuses it at `executor.Executor.Send`, `sessioninbox.Inbox.Enqueue`, and the non-message session routes, with `403 FORBIDDEN` and the reason `impersonation_user_credential`, and memory writes refuse it on every session. A test covers every mounted mux. An operator can read the session and cannot drive it into using Alice's token. SO-4a's `impersonated_by` claim is the same signal once it exists.
- **Delegated children.** A child carries its parent's user on its row, so the same link and check apply to its own declared set for App tokens. It receives the pool's `userCredential` binding only when the delegation lease names it, and `poolAttach` does not bypass that, under `user-credential-delivery-design.md`. A child with no lease entry has no API token, and no App binding stands in for it, so its identity does not change silently. Its pod must pass the same pod model test. `credential.mint` records its `sessionId`, and the session row gives its `root_session_id`.
- **Detached orphans.** An orphan "carries the originating user's identity" ([§8.10](../spec/08_recursive-delegation.md#810-delegation-tree-recovery)), keeps being swept, and ends at `cascadeTimeoutSeconds`, 3600 by default. It keeps authorization and keeps what it was granted, because `detach` is what the client asked for, and §11.4's listing by owner reaches it. Decision 18 records this.
- **Disabled users.** Each gate reads the user's invalidation level and `processing_restricted` through `pkg/binding`. Any of `soft_disable`, `hard_disable`, or `full_revoke` refuses every user-derived mint and every App mint under a `required` pool, including the running sessions §11.4 otherwise leaves up on the lesser levels. A store error fails closed, as TS-4 does.
- **Cross-environment.** Under `tenant-scoping-identity-design.md` decision 14, a child in its parent's environment needs no new rule, and a child reached through a sharing path runs in the target environment, so `visible(bindings, T)` and `visible(VCS pools, T)` apply. It keeps its parent's user, so a `required` pool in T checks that user. A user-sourced binding crosses only when the delegation lease names it. The link itself has no environment, so no team needs its own link.
- **Environment-less pods.** The refused pod models above cover what TS decision 15 addresses.

### Service identities

An automated session has no person to check. A `required` VCS pool lists `serviceIdentities`, each with a `subject`, its own repository allowlist, and its own `allowWrite`. A session uses that path only when all of these hold:

- The caller's `caller_type` is `service`, which [§13.3](../spec/13_security-model.md#133-credential-flow) makes impossible to elevate on exchange, and which the session row now records, so both gates read it through `pkg/binding`.
- The bearer's `sub` is listed on the pool.
- The session is a root session under that subject. A human's delegated child stays under the human, and its row copies the human's `caller_type`.

Who may hold one: a system principal authenticating through the client credentials path of §10.2, whose credential no person handles as a routine. A `tenant-admin` writes the list through EG-9's path, each write emits an audit event, and a `security-admin` can restrict or forbid the list through a new `userAuthorization.serviceIdentities` guardrail. A person cannot move onto this path by presenting a token with a chosen `caller_type`, because the subject must be listed. Lenny cannot detect a person holding a listed client secret, which "Residual risks" states.

A listed service session takes the App binding on that host, under "The attach rule", so its pull requests have the App as author. Any other root session whose `caller_type` is not `human` is refused on a `required` pool, with no check to run. The flavour's preset applies to that binding too, because `pull_requests: write` on an App token can approve any human's pull request.

### Attribution

`credential.mint` carries "a session identifier and no user identifier", and §12.8 treats it accordingly: `DeleteByUser` deletes nothing from it. This design keeps that.

- `credential.mint` gains `tokenSource` and `linkRef`. `tokenSource` is one of `app`, `userRegistered`, `userOauth2`, `userChild`, and `userCheck`, as `user-credential-delivery-design.md` defines. It carries no login, email, or `user_id`.
- `linkRef` is a pseudonym in the same class as the session identifier. Its join lives in `user_links`, which erasure deletes, so a stored record stops resolving to a person, and the erasure statement in "Evidence streams" stays true once it says so. The class statement needs that one sentence when UG-2 lands.
- The token hash field stays. For a child token, the hash also joins to the provider's audit log, which GitHub describes as listing the user with "`programmatic_access_type`" of "GitHub App user-to-server token" **[documented]**. That join lies with the customer's provider log rather than a Lenny store.
- The provider login appears only in `user_links` and in §11.7 audit events, whose treatment §12.8 already defines through step 13 and step 14.
- A tenant's security team asks "who" by joining `sessionId` to the session row while it exists.
- A request rule refusal goes to `egress.local` with the decision identifier and the operation name, with no variables or body. Repeated refusals on one session raise a `user_credential_rule_refusals` alert, because an agent trying to approve is a prompt injection signal. These records are `self_reported`, as the stream's table says.

### Failure behaviour

| Failure | Behaviour |
| :-- | :-- |
| No link on a `required` pool | Session create refused with `USER_LINK_REQUIRED`, before any pod claim when the plan binds at create, as "When the check runs" states. Nothing falls through to the App. |
| Provider `unavailable` at bind | `USER_AUTHORIZATION_UNAVAILABLE`, retryable. |
| Token Service down | As the egress design states, and bind also fails with the retryable error. |
| Postgres down | Bind fails. Running reads continue until `verdictMaxAgeSeconds`, the existing §12 behaviour applies, and no new mint happens. |
| Refresh token rejected | `user_act_required`. |
| Rate limited at the provider | `unavailable`. The sweep backs off per link. A metric reports it. |

## UG-3: Pull request authoring

### The binding

Revision 3 had a third `secret` kind, `userLink`, with a filter a tenant could not clear. **`userLink` is now the `oauth2Link` source of a `userCredential` binding**, and `prAuthoring: user` on the pool writes this record:

```yaml
userCredentialTypes:
  - {name: github-user, source: oauth2Link, linkProvider: github-acme}
---
secret: { kind: userCredential, type: github-user }
hosts: [api.github.com]
delivery: inject
scope: poolAttach
# no attach: the kind fixes it, in the token trust record
# no preset key: resolved at I8 from the flavour of the pool's provider and host
```

- `prAuthoring: user` requires `userAuthorization: required`, and an `oauth2Link` type whose named link provider is on that host with a ceiling of `inject` or above. That provider can differ from the one the check names. A write without them is refused, so the check and `USER_LINK_REQUIRED` always hold on such a pool. Decision 44 records the cost. A `required` write whose `userAuthorization.linkProvider` is absent or names no VCS-kind provider on the pool's host is refused too.
- `hosts` is the API host from the flavour table: `api.github.com` on GitHub, `api.<name>.ghe.com` on `ghe.com`, and the host itself on GHES and GitLab. On GHES, the same hostname serves git and the web, and a smart HTTP path there takes the egress design's git path rather than this binding.
- The link provider names the template `github-scoped`, whose `permissions` is `pull_requests: write` and `contents: read` by default. That is data a tenant can widen, which the owner settled under decision 42.
- The preset resolves at I8 from the flavour of the VCS pool's provider and host, and a tenant can change it. A `security-admin` can require it through `requestRules.requiredPresets`. See `user-credential-delivery-design.md`, "Who can change a preset".
- `netagent` holds no provider code for this.

### The attach rule

This is the one place the attach is defined, and the egress design's `scope`, `user-credential-delivery-design.md`, and `tenant-scoping-identity-design.md` point here.

- **The trigger.** A human root session that declares at least one writable repository under a pool with `prAuthoring: user` receives that pool's binding, with `scope: poolAttach`, without naming it. The binding has `required: true`, so a missing link gives `USER_LINK_REQUIRED`. A session declaring only read-only repositories there does not attach.
- **It replaces a same-host App binding, on that host alone, for that session.** A tenant-wide App binding on `api.github.com` can exist beside it, because service sessions need one. It keeps its other hosts, such as `uploads.github.com`. Replacement holds whatever the user binding's state, so a `suspendedBy` user binding restores no App binding (UP-6). It also holds when the session's other entries sit under a pool with `prAuthoring: off` on that host. A session naming a second `inject` binding on that host explicitly is refused at bind under the one-binding rule.
- **A listed service session** takes the App binding on that host, and never the pool's. Any other caller that is not `human` is refused on such a pool.
- **A delegated child** receives the pool's binding only when the lease names it, as "Identity cases" states.

### Other tokens on these hosts

Under `user-credential-delivery-design.md`, a tenant can also define a type of its own on these hosts, and rules evaluate that type's hosts. **What follows is by host, for every session of the tenant.** On the API host of a `required` pool, the only permitted bindings under `inject` are an App-sourced binding, which the check governs, and the pool's link. A stored secret has no check, so it is refused at write, setting `required` on a pool suspends one that exists, and `userAuthorization.require` makes that hold whatever a tenant admin writes. **The owner also adopted this rule: a `file` binding whose type hosts include a covered host is refused unless every pool covering that host sets `allowRuntimeHeldCredentials`, whatever the pool's mode,** because on an `off` pool the push would otherwise silently act as the App. A pool added later without the flag, or the flag cleared later, suspends the binding, and a running session that named it gets a 401 on git rather than the App. On a host covered by a git route alone, the binding is admitted, the held token is dropped on the git path, and a push is refused. A platform warm-up asset covers its host only until bind, except on a `restricted` pool with `gitAccess`, so it does not bring this rule to a tenant with no pool there. A plan naming a suspended `file` binding is rejected at create with `422 USER_CREDENTIAL_BINDING_SUSPENDED`. `user-credential-delivery-design.md` states these, and UC-5 owns them.

| On a `required` pool's hosts | Result |
| :-- | :-- |
| A stored secret binding, under either delivery | Refused at write. |
| Any other `userCredential` type under `inject` on the API host, `registered` or `kind: oauth2` | Refused at write. |
| A `file` binding whose type hosts include these hosts, with no flag | Refused at write. |
| That binding with `allowRuntimeHeldCredentials: true`, on a pool with no `prAuthoring: user`, every pool covering the host flagged, and no App binding on the API host for that session | **UP-4 still holds for App tokens: no App token without a verdict. UP-9, UR-2 for what the agent's token reaches, UR-4, and UR-6 do not hold for that session,** since its token leaves every Lenny control. Posture reports the pool, and the session is marked `agentHeldCredential`. **Other sessions on the pool are unchanged:** the attach applies to a session naming that binding, whatever its release or suspension state, and with no current value the host answers 401. The pin exception needs a release, and suspension ends it. With no flag a held token is dropped for git and the mint is used **[measured]**. With the flag, `netagent` attaches the current value it holds on every git request and no mint is used to `netagent` for that host, under UC-5, and with no current value the host answers 401 **[measured]**. |
| That binding on a pool with `prAuthoring: user`, or with an App binding on the API host | Refused at bind by the one-binding rule. |
| A `file` binding on a type this pool's link provider serves | Refused unless the flag applies and the record's ceiling is `file`. |

A `registered` type can lie, by declaring an innocent host and holding a GitHub token. That passes the checks above, and the enforcing control is the pin on the host the token works on, which waits on EG-12.

**A smuggled token.** A client can also place a token through `inlineFile`, through `env` under a name the blocklist does not match, or in a message **[source]**, and input checks cannot recognize one. Under a `required` pool this is a bypass of the check on any unpinned host, and revision 3 pinned nothing. **The rule, which the owner settled under decision 40: when any `required` pool covers a host, that host and the kind's API hosts are `credentialPinned` for every session of the tenant, once EG-12 has landed.** A token reaching that host reaches the pool's repositories whichever pool the session is on, so the pin source reads every VCS pool of the tenant, unfiltered by environment. UG-2 carries the wiring, and EG-12's pin takes a VCS pool as a source. A pinned host strips other credentials and refuses a host with no binding. `pinHosts: false` opts out, and posture reports it. An `internet` pool's raw sockets bypass `netagent`, so its sessions stay outside the pin, and posture reports that too. Until EG-12 lands, under the opt-out, or on an `internet` pool, this is a stated residual. Under T, a held token on a covered host is dropped for git and the mint is used **[measured]**, so the residual there is the API host. Decision 40 records the costs.

### Child token and the session's repositories

The Token Service holds the parent and mints one child for each session's use, through the `github-scoped` narrowing template of `user-credential-delivery-design.md`. These steps are that template's run on this provider.

1. `netagent` asks on I8 for the binding, on its first request to that host.
2. The Token Service verifies the pod and session, rejects a refused pod model or an impersonated session, runs the check for each repository, and asks I3.
3. It selects the session's `granted` writable entries. Per `security-oversight-design.md` decision 25, a `pendingApproval` entry is absent, so SP11 holds, and a repository the session declared read-only is absent too, because one token has one permission set. **An empty set mints no child:** `netagent` answers the request with 403, a decision identifier, and the reason `no_granted_repositories`.
4. It calls `POST /applications/{client_id}/token/scoped` through the mint path with Basic authentication using the client ID and secret, `access_token`, `target_id`, `repository_ids`, and `permissions` **[documented]**.
5. It answers with the secret, `expiresAt` from the response's `expires_at`, `cacheUntil`, the authoritative `hosts` and `attach`, the resolved request rules, and `handling` naming the rule evaluator's minimum version and `reauth-on-401`.

A token has one `target`, so a session whose `granted` entries span two owners is refused on the second owner's binding with `USER_TOKEN_MULTI_OWNER`. `gh pr create --head alice:feature` still works when that head already exists, because it sends `headRefName: "alice:feature"` to `createPullRequest` on the base **[captured]**, and the child needs only the base. A branch pushed to a `fork` remote produced a head of `alice:<branch>` under T and P **[measured]**. `gh` offers an automatic fork at an interactive prompt alone, and a non-interactive run stops with "you must first push the current branch to a remote, or use the --head flag" **[source]**, and whether a child permits a fork is not established (S15).

### Child tokens per session

The owner settled decision 7 after S2: each session gets its own children, and Lenny sets no limit of its own on how many a person holds. S2 found no per-user limit on this endpoint: 50 children of one parent in one hour all stayed valid, with no revocation and no browser prompt **[measured]**. GitHub's "ten tokens per user, application, and scope" rule does not apply to these tokens.

- **Children per session.** A session holds at most two children per binding: an API child under the template's `permissions`, and, under `pushIdentity: user`, a git child with `contents: write` alone on the same repositories, minted at the session's first push. `netagent` attaches the git child only to smart HTTP requests (`info/refs`, `git-upload-pack`, and `git-receive-pack`) on the session's writable repositories, and the API child to everything else the binding covers. On `github.com` the two sit on different hosts. On GHES and `ghe.com` they share a host and `netagent` selects by path, as the egress design's git path already does. The API child never holds `contents: write` unless a tenant widens the template, so the host keeps refusing merges through the API (S15). A delegated child session is its own session and mints its own children, under "The attach rule".
- **Cache.** Keyed on tenant, session, `link_ref`, `generation`, sorted repository identifiers, permissions, and target, so no two sessions share a child. Stored as §4.3 caches access tokens, encrypted in Redis, so replicas share it, and sealed with the full key and the record digest as additional data, so a replica copying an entry to another key produces a value that fails to open. A session whose granted set changes mints a new child for the new set and revokes the old one.
- **Limits.** Lenny has no per-person or per-link budget. When GitHub refuses a mint, the refusal is the result: a rate-limit answer surfaces as a retryable `USER_TOKEN_RATE_LIMITED`, and any other refusal as a 503 with a decision identifier and GitHub's status. The Token Service counts token requests per App, refreshes included, against GitHub's 2,000 per hour and alerts near it, beside the egress design's counter. The counter is per App, and each tenant registers its own App (decision 45), so it is a per-tenant figure. A tenant that starts more than about 1,000 PR-authoring sessions an hour that each push reaches it, and S2 did not test what GitHub does there.
- **Revocation.** Session end revokes the session's children at the provider, which S5 showed leaves the parent and other children valid **[measured]**. A failed revocation is retried from a durable record and logged, and the child expires on its own 8 hours after creation (S1).
- **On 401.** A child revoked outside Lenny, by the person, an organization owner, or GitHub, appears as a 401, which `reauth-on-401` covers once with a new mint, then surfaces as a 503 with a decision identifier and an alert.

### Request rules on VCS hosts

`user-credential-delivery-design.md` defines request rules, presets, and who can change them. This section states what they mean here.

**The model.** `github-deny` and `gitlab-deny` are data, with paths per API flavour and request forms. The flavour of the VCS pool's provider and host selects the preset at I8. A binding that adds rules keeps the preset, and only `preset: none` clears it. The preset body is the extended preset, which the owner settled under that document's decision 24. A request matching no rule passes, so a new `gh` release keeps working, and `gh pr comment`, `edit`, `ready`, `close`, `reopen`, and `edit --base` all pass the rules by default. What the preset refuses stays refused unless a tenant changes it.

**Passing the rules is not the same as working.** The host then refuses what the token does not permit, and this table separates the two.

| Command | Rules under `github-deny` | The host then, under the default child | Evidence |
| :-- | :-- | :-- | :-- |
| `pr create` | Passes **[measured]** for the flags the 103 commands use, which exclude `--project` | Documented as permitted. `--project` needs a projects permission the child lacks. | The host is S15. |
| `pr view`, `pr list`, `auth status`, and `gh api` reads | Pass **[measured]** | Within `contents: read` and `pull_requests`. Fields on `pr view` are likely refused or partial. | The host is S15. |
| `pr status`, `checks`, `diff`, `repo view`, and `issue view` | Pass, as they name no denied operation. **[captured]** under the allowlist, and `issue view` in the earlier capture alone. Not run under deny mode. | Within `contents: read` and `pull_requests`. `pr checks` and `issue view` are likely refused or partial. | The host is S15. |
| `pr comment`, `edit`, `ready`, `close`, and `reopen` | Pass **[measured]** | Documented under `pull_requests: write` for PR routes. | The host is S15. |
| `issue create`, `run`, `release`, `gist`, and other commands outside the child's permissions | Pass **[measured]**. `release upload` goes to `uploads.github.com`, which needs a binding, and on GHES to the flavour's uploads path, which passes **[measured]**. | Refused, until `permissions` is widened | The host is S15. |
| `secret set --env`, `secret delete --env`, `variable set --env`, and `variable delete --env` | Pass **[measured]** | As the token permits | |
| `issue develop`, `repo sync`, `repo deploy-key add` and `delete`, `ssh-key add` and `delete`, `gpg-key add`, `gh api` contents writes, and `gh api` branch protection writes | Refused **[measured]** under the extended preset, and passing under the earlier smaller one (decision 24 there). `gpg-key delete` did not confirm, because the stub has no key to list, and its raw `DELETE` is refused. | Refused under `contents: read`, as documented | The host is S15. |
| `pr review` with any event | Refused by `addPullRequestReview` **[measured]** | Would permit it, as documented | The stub proves forwarding only. |
| `pr merge`, `--auto`, merge queue, and `--disable-auto` | Refused by `mergePullRequest`, `enablePullRequestAutoMerge`, and `disablePullRequestAutoMerge` | Merge needs `contents: write`, as documented for REST | **[measured]**, on all 14 merge rows across versions and flavours. |
| `pr merge --delete-branch` | Refused at the mutation first **[captured]** under the allowlist | Not reached | Not run under deny mode. |
| `pr update-branch`, `pr revert` | Refused by `updatePullRequestBranch` and `revertPullRequest` | Not reached | **[source]**. |
| `gh api graphql` with an aliased refused mutation | Refused **[measured]** | Not reached | |
| A new `gh` command using a mutation the preset does not name | Passes | As the token permits | **[design]**. This is the accepted cost. |

**Command result [measured].** Under the final preset, 103 `gh` commands on each of Alpine and Debian, GHES and github.com: **63 forwarded, 23 denied actions refused, 0 denied actions missed, 6 others refused, and 11 with no write at the stub**, of which 4 are reads and 7 are stub limits (6 `project` commands and `discussion create`). The 6 are `gh issue develop`, `gh repo sync`, `gh repo deploy-key add`, `gh repo deploy-key delete`, `gh api -X PUT .../contents/...`, and `gh api -X DELETE .../branches/main/protection`. The earlier smaller preset gave 69, 23, 0, 0, and 11. Coverage is of the commands listed rather than every flag. **The stub proves forwarding only.**

**Version drift [measured].** Under the final preset, all 7 versions on Alpine and Debian, on both flavours, 12 rows each: 8 writes forwarded, merge refused, and 0 others refused at every version. The only non-zero exit other than merge is `pr edit` with metadata on 2.86.0 and 2.87.0 on github.com, on a stub limit with no refusal. An earlier run against the allowlist, on the Alpine client:

| `gh` | Deny: refused other than merge | Allowlist: refused other than merge |
| :-- | :-- | :-- |
| 2.72.0, 2.73.0 | 0 | 8, including `pr create --reviewer --assignee` |
| 2.86.0, 2.87.0, 2.93.0, 2.94.0, 2.103.3 | 0 | 6 (`pr edit`, `pr comment`) |

Two rows on github.com, 2.86.0 and 2.87.0 `pr edit` with metadata, exit 1 on a stub limit with no filter refusal. The allowlist here includes the `Time-Zone` fix. 

**Widening `permissions`.** A tenant that wants `gh issue` or `gh run` widens the template. **Once `contents: write` is granted, the host no longer refuses merges, `PUT contents`, `createCommitOnBranch`, or ref writes, and the deny preset alone stands between an agent and those operations.** The same holds for `actions: write`, and any new operation in those families passes until the preset names it. The owner settled decision 42 with this stated.

**What went away.** Owner decision D1 accepted these:

- A new GitHub mutation that can approve or merge passes until the preset is updated.
- The input-key check is gone, so `updatePullRequest` with `baseRefName` or `state` passes. `gh pr edit --base` works, and it can move an approved pull request onto another branch. Whether GitHub keeps approvals across a base change is S16.
- The target check is gone. A REST path naming a repository outside the session is forwarded, and under `github-scoped` the host answers 404 (S15). A VCS kind that delivers must name a template.
- `pr edit`'s partial writes no longer arise from the rules, since its mutations all pass.

**Headers.** The forwarded-header allowlist is gone. `netagent` strips credential headers only, which `Time-Zone` required on any image with `/etc/localtime` **[measured]**. With it fixed, 10 images and `TZ` and `LANG` variants gave 0 refusals **[measured]**.

**What remains unbound.** Targets, reads (`repository`, `node`, and `search` read whatever the token reads), free text, and git, which sits under the egress design's rules, "Git pushes and the most recent push rule", and the push option refusal.

### Keeping presets current

An unlisted operation passes under `default: allow`, so a new `gh` release breaks nothing. The risk moves to the server side and to presets.

- A CI job diffs GitHub's public GraphQL schema and REST OpenAPI description, and GitLab's quick action page and fine-grained tables, on a schedule. A new name in the review, merge, queue, content, ref, deployment, or policy families raises a preset change for review. The preset names REST twins and policy mutations that exist today, which the accepted loss does not cover. The rate is about one a year on GitHub, in one reading **[source]**.
- Rules reach `netagent` resolved on I8, so a preset update reaches a pod at its next `cacheUntil` with no image change.
- The window in which a new mutation with that power passes is the schedule, the review, and `cacheUntil`. "Residual risks" states it.

### What reaches the host

This rule carries over to inspected requests, on any binding with rules: a listed GraphQL path, and a write under `bodyPatterns` or `denyBodyKeys`. Other writes stream as `user-credential-delivery-design.md` states. The first filter parsed the client's bytes and forwarded those same bytes. A parser difference then becomes a bypass, and one was found. This document passed the filter and reached the stub with the injected token, on both protocols **[captured]**:

```
# x\rquery{viewer(a:"""\nmutation{addPullRequestReview(...){clientMutationId}} #"""){login}}
```

gqlparser, graphql-go, graphql-js 17.0.2, and graphql-ruby's C parser read one query. graphql-ruby 2.6.14's default lexer ends a comment only at LF and reads one mutation, `addPullRequestReview` **[captured]**, because its reading of the bare CR differs. Which parser GitHub runs is **[stub cannot show]**. The corpus also shows other disagreements, including a NUL and a BOM between definitions. A design that has to know the host's parser is the failure here, because that parser is unknown and can change.

**The rule: on an inspected request, the host receives bytes that `netagent` generated from the checked structure. No byte of the client's document, JSON envelope, or JSON body reaches the host's parser.**

- **Document.** `query` is `netagent`'s own printing of the parsed tree, which removes comments and odd whitespace. `netagent` parses the printed text again and requires the same operation kind and root field names. If that fails, it refuses the request. Printed documents read the same under gqlparser and graphql-ruby on all 24 corpus documents **[captured]**, and the 159 documents of the later corpus were all idempotent under printing, with 0 differences under graphql-js and definitions reordered in 6 **[measured]**. `gh` kept the same exit codes and operations with the printer on, on GHES **[captured]**, and the attack table passed with it on **[captured]**.
- **Lexical rule.** It stays beside the printer, because it refuses a suspicious document with a clear message rather than transforming it, and it is cheap. Either one closes the bare CR **[captured]**.
- **Envelope.** It must be an object, with `query` a string and `variables` an object or null. `netagent` writes a new JSON object holding them. It drops `operationName`, which the one-operation rule makes redundant. `Content-Type` and `Content-Length` are its own.
- **Variables and REST bodies.** The input-key check described here is gone with the allowlist, and the duplicate-key rule stays, because `bodyPatterns` read those values. The prototype copied `variables` verbatim, and that left the open defect: `{"i":{"pullRequestId":"P","state":"CLOSED"},"i":{"pullRequestId":"P"}}` passed the `updatePullRequest` input check and reached the stub, because the filter read the last value **[captured]**. A first-wins origin would close the pull request. Ruby's standard JSON reads the last value **[captured]**, and GitHub's reader is **[stub cannot show]**. The effect stops at a listed mutation's fields, so it never reached approval or merge. The JSON rule now refuses a duplicate key at every depth, and `netagent` re-encodes the decoded tree, so the host sees one value whatever it would have chosen **[measured]**: `{ "a" : 1e2 , "b" : "A\/x" }` reached the stub as `{"a":1e2,"b":"A/x"}`, with number text preserved.
- **URL and query string.** `netagent` forwards the canonical path, so a host never decodes what a rule did not. Any escape outside a declared encoded segment is refused on every method, empty segments and trailing slashes are dropped, the final segment is also compared with anything after its first `.` removed for a refusal, and under a preset an unmatched `/api/*` or `/graphql*` path is refused **[measured]**. That design states the rule. A preset's write form has no query string. On `GET` and `HEAD`, a query string is rebuilt from parsed pairs.
- **Framing.** `netagent` writes its own HTTP/1.1 or HTTP/2 upstream.

Canonical forwarding costs 1.9 µs on `pr create`, 44 µs on the largest `gh` document, and 14 ms on the token document **[captured]**. Printing is also what the first out-of-memory kill came from, so the bounds below come first.

**Check cost under the final preset [measured],** at 2 CPUs and 200 iterations, so noisier than the earlier table: `pr create`, 353 B, 19.6 µs and 11 KB; the `largest` case, 2,388 B, 120 µs and 106 KB; a 65 KB PR body, 600 µs and 584 KB; 258 KB of `<`, 2.7 ms and 1.7 MB; 4,096 tokens, 1.09 ms and 1.4 MB; a 512 KiB body, 3.65 ms and 5.2 MB; 40,000 JSON nodes in variables, 10.5 ms and 7.5 MB; and REST `GET`, 2.1 µs. An earlier run gave a 24% to 49% rise over the allowlist on `gh` documents, and paired end-to-end, a p50 difference from an unfiltered host of 0.50 ms at 209 B, 1.48 ms at 2,388 B, and 2.45 ms at 65,784 B, with `memory.peak` at 41.7 MB under a 15,000 token limit. `memory.peak` under the final bounds has not run. A separate result stands: 65,536 `<` characters print to more than 256 KiB, and 512 KiB admits them.

### Bounds and cost

Every one of these came from a defect or a measurement in the capture. The size and depth bounds each passed at their limit and were refused one past it on GHES, github.com, and GitLab, and the fragment chain ran 15 plus 15 against 20 plus 20. Body read, concurrency, the check budget, and head read ran on github.com, with no one-past row **[measured]**. They are now data defaults on any binding with rules, within platform ceilings, under `user-credential-delivery-design.md`.

| Bound | Value | Why |
| :-- | :-- | :-- |
| Body, inspected requests | 512 KiB | `gh` JSON-encodes with HTML escaping, so `<` costs 6 bytes. The prototype's 256 KiB bound refused 44,000 `<` (264,352 bytes), 65,536 `<` (393,568 bytes), and 65,536 emoji (262,496 bytes) **[captured]**. 65,536 characters is GitHub's body limit as recalled, unverified (S15). 512 KiB admits the worst `gh` encoding of that limit. |
| Document, within the body | 64 KiB | The largest `gh` body in the capture is 2,388 bytes **[captured]**. A PR body travels in variables, so this bound does not limit it. |
| Tokens | 4,096 | The largest of 166 corpus documents has 698 by the parser's lexer **[measured]**. The prototype had no token limit, and a 256 KiB document of tokens cost 36 ms and 40 MB. |
| GraphQL selection depth | 32, with fragment spreads followed and a cycle refused | The deepest corpus document has 14 levels **[measured]**. An 80 KB document nested 40,000 levels killed the prototype's printer at a 256 MiB limit, which stops egress for the whole pod **[captured]**. |
| GraphQL value depth | 32, counting the leaf | The deepest corpus document has 2 **[measured]**. |
| JSON depth | 32 | The same defect class, through `variables`. 33 and 40,000 were refused **[measured]**. |
| Root fields per mutation | 16 | The corpus has 1, and `gh`'s project batch sends one per project change. A query has no root bound, and 200 passed **[measured]**. |
| Head read, on `netagent`'s listeners rather than per rule set | 10 s, with a bare LF refused at once with 400 | A request with bare LF line endings got no answer, and the prototype held the connection for 20 s with no deadline **[captured]**. A partial head got 400 at 10.0 s **[measured]**. A bare LF head also waited 10.0 s, because the prototype does not implement "at once", so that part is **[design]**. |
| Body read | 30 s | A body at 2 KB/s got 408 **[measured]**. |
| Concurrent checks per pod | 4 inspected requests, then 429 with `Retry-After` | Holds buffered bytes to 2 MiB. With 6 slow bodies, 4 got 200 and 2 got 429 **[measured]**. |
| Check time | 100 ms budget, refused with 503 past it | The parser cannot be preempted, so the bounds above are what limit it, and this catches a defect. It was never reached at 100 ms, and the worst case benchmarked is 10.5 ms **[measured]**. |

**Against `netagent`'s memory.** The egress design's example request is 64 Mi. Measured `memory.peak` was 15.5 MiB after `pr create`, 17.9 after the largest `gh` document, and 18.9 after a 258 KB body **[captured]**, cumulative in that order on one process. The 256 KiB token document took it to 74.9 MiB **[captured]**, past that request, which is the case the token and document bounds remove. With a linear extrapolation from the measured 5.5 ms and 1.7 MB at 258 KB, a 512 KiB body costs about 11 ms and 3.4 MB, about double that with printing and re-encoding, and four at once stay under 30 MiB of transient allocation. Scaling the measured costs by token count puts a document at the token bound near 1 ms and under 2 MB. These figures are estimates **[design]**, and S9 measures them. The chart sets `GOMEMLIMIT` on `netagent`, so the collector works before the limit.

| Document | Check alone | End to end, paired, on 1 CPU |
| :-- | :-- | :-- |
| `pr create`, 353 B | 11 µs, 6.8 KB | Below noise |
| Largest `gh`, 2,388 B | 88 µs, 78 KB | 0.06 ms |
| 65,784 B, a full PR body | 606 µs, 559 KB | 0.94 ms |
| 258,185 B of escapes | 5.5 ms, 1.7 MB | 4.2 ms |
| 260,023 B of tokens (now refused) | 36 ms, 40 MB | 17 ms |

All of these are **[captured]** on a laptop under other load, so small differences are not resolved. The binary grew from 10.87 MB to 11.41 MB.

**What the user sees past a bound.** `gh` exits non-zero with `HTTP 413: refused by request rules: body of N bytes is over the 524288 byte bound` and nothing is written **[captured for the format, under the allowlist's wording]**. An ordinary body stays far under it. Past the host's own limit the host's error applies.

### Comment-triggered actions

An input something else treats as a command carries Alice's identity. The body of `createPullRequest` and `updatePullRequest` carries the same risk as a comment, so refusing comments never closed this path, and revision 3's capture showed that it cost `gh pr comment` **[captured]**.

**GitHub and GHES.** The host has no native comment commands. Repositories run bots that do: Prow's `/lgtm` and `/approve`, merge bots such as Mergify and Bors, Atlantis's `atlantis apply`, and `issue_comment` workflows that gate on `author_association`. (Dependabot retired its merge commands in January 2026 **[documented]**.) A single injected agent commenting `/approve` on Bob's pull request produces an approval under Alice's name wherever such a bot trusts her. Lenny cannot detect these bots, and no regular expression covers their grammars.

Text is not the only trigger. Kodiak merges a pull request carrying its automerge label once branch protection passes, and `labeled` is a documented `pull_request` activity type a workflow can select **[documented]**. `ready_for_review`, `review_requested`, `assigned`, `closed`, and `edited` are selectable too **[documented]**.

**Under the preset model.** Revision 3's required `repositoryAutomation` statement is retired, along with its label-key logic, because those were allowlist input checks. A tenant with such automation adds rules:

- `graphql.denyMutations: [addComment, addLabelsToLabelable, ...]` refuses an operation outright.
- `bodyPatterns` with the bot's grammar refuses per line on comments, PR bodies, and titles, including every string and block string literal in a GraphQL document, such as `createNote(input: {body: "ok\n/merge"})`, a variable default, or a directive argument **[measured]**. A pattern is a deny list over free text, which the capture found weak: refusing every `/word` line also refused `/usr/bin/env` **[captured]**. It is offered because the tenant's list targets a grammar it controls.
- The stronger control sits with the bot: a comment made through a GitHub App user token carries a non-null `performed_via_github_app` in the REST schema **[documented]**, and the UI shows the App's badge, so a bot or workflow can refuse commands from it. The tenant page recommends that first, and whether bots see that field in webhook payloads is S15.

The owner settled decision 35: `repositoryAutomation` is retired, and the setup flow and posture prompt a tenant about bots.

**CI.** A pushed branch triggers the repository's existing workflows. GitHub's page says secrets are withheld "when a workflow is triggered from a forked repository" **[documented]**, and it describes no such withholding for a branch in the same repository, which is what the namespace rule produces. The default token omits `workflows: write`, so an agent cannot edit a workflow file, and a workflow still runs the branch's build scripts. Whether an App token's push triggers these is not stated on that page (S15). GitLab behaves the same way for unprotected variables, and it has no permission like `workflows: write`, so an agent can also push a `.gitlab-ci.yml`, as the egress design states. A tenant limits this with required reviewers on environments and protected variables, and the repository setting that lets Actions approve pull requests stays off. This exists under the egress design's App-token pushes today, and "Residual risks" states it.

**GitLab.** GitLab itself runs quick actions, whatever the tenant has installed: "Run automatically when you save descriptions or comments" **[documented]**. `glab mr create --description /merge` carries a merge inside an allowed route, and a route-only policy sent it to the stub with the injected token **[captured]**. Whether GitLab would execute it for that token is **[stub cannot show]**, and it is S17.

The prototype refused every line starting with `/word`, which caught `/merge`, a following-line `/approve`, and a CR-separated `/merge`, and also refused an ordinary `/usr/bin/env` line **[captured]**. That breaks legitimate descriptions. `gitlab-deny` carries a narrower `bodyPatterns` set: lines split on LF, CR, VT, FF, U+0085, U+2028, and U+2029, and refused when, after ASCII whitespace, they name `approve`, `lgtm`, `unapprove`, `merge`, `rebase`, `submit_review`, `target_branch`, or `run_pipeline`, compared case-insensitively and regardless of code fences **[documented for the names, apart from `lgtm`]**. `/usr/bin/env` passes. A write with a query string is refused, because GitLab's page shows `POST .../notes?body=note`, and quick actions run on notes **[documented]**. The pattern with those eight names ran **[measured]**: it refused `/merge` and `/approve` in JSON, escaped, CR-separated, query, form, note, and GraphQL forms, also refused prose in a line starting `/merge the two lists` and a `/merge` inside a code block, and left `/usr/bin` and `/assign_reviewer` alone. `/target_branch` and a commit comment are refused too, because the pattern applies to every write, and ` /merge` after a non-ASCII space passes, because RE2's `\s` is ASCII. Whether GitLab would execute any of these is unknown. The alias list is unverified.

This list is a deny list, so a future GitLab command with merge power fails open until the preset changes. S17 tests each spelling against a live instance. On the bot path, a tenant lowers the risk further with a project token whose role cannot merge the target branch.

### History: the revision 3 filter

This is superseded and kept because it records what was measured. Nothing below is a requirement.

**Bypass analysis under the allowlist.** These results are **[captured]** against the allowlist with the printer on. **The deny-mode run is under "Validation owed" as S19 [measured].**

| Route | Result under the allowlist | Why |
| :-- | :-- | :-- |
| HTTP/2 | Filtered | `gh` and `glab` both speak it. Each stream is one request. Six attack rows ended in protocol errors from the server library, with nothing at the stub. |
| HTTP/1.1 framing and pipelining | Filtered | `netagent` writes its own upstream request, so conflicting framing cannot reach the host. |
| Absolute-form | Filtered | One authority is derived, and it enters the same stage. A differing `Host` is refused. |
| Another bound host, and reuse after an allowed request | Refused | A filtered connection refuses any other authority, and each request is checked. |
| Opaque tunnel | No credential | 2 rows reached the stub with no credential, and 1 did not connect. |
| GET, form, `application/graphql`, gzip, zstd, and deflate | Refused | The endpoint admits one form. |

Three routes do not depend on the rules: `uploads.github.com` or `github.com` carry no token because neither is bound, another binding on the host is refused, and `netagent` offers `h2` and `http/1.1` alone in ALPN with the proxy carrying CONNECT only. Reflection of a token is scrubbed under the egress design's stated limit, and a compromised `netagent` applies no rules.

**Operations `gh` 2.103.3 sends [captured].** These are facts about the client, and they feed the preset's tests.

| Field | `gh` operation and command | `gh` uses it on |
| :-- | :-- | :-- |
| `createPullRequest` | `PullRequestCreate`, `pr create` | Both |
| `updatePullRequest` | `PullRequestCreateMetadata`, `PullRequestUpdate`, `pr create` and `pr edit` | Both |
| `requestReviews` | `PullRequestCreateRequestReviews`, `--reviewer` | GHES |
| `requestReviewsByLogin` | `RequestReviewsByLogin`, `--reviewer` and `pr edit` | github.com |
| `replaceActorsForAssignable` | `ReplaceActorsForAssignable`, `--assignee` and `pr edit` | github.com |
| `addProjectV2ItemById` | `UpdateProjectV2Items`, `--project` and `pr edit` | Both |
| `addLabelsToLabelable` | `LabelAdd`, `pr edit` | Both |
| `markPullRequestReadyForReview` | `pr ready` | Both |
| `closePullRequest` | `pr close` | Both |
| `addComment` | `CommentCreate`, `pr comment` | Both |
| `addPullRequestReview` | `PullRequestReviewAdd`, `pr review` with all three events, the same document | Both |
| `mergePullRequest` | `PullRequestMerge`, `pr merge` | Both |
| `enablePullRequestAutoMerge`, `disablePullRequestAutoMerge` | `pr merge --auto`, merge queue, and `--disable-auto` | Both |

Inline `input` literals through `gh api`, and `$input_000` variables, both appeared **[captured]**. No `gh` document held more than one operation **[captured]**. REST routes `gh` used: `GET /` and `/user`, `GET /meta` on GHES, `GET /repos/{o}/{r}/pulls/{n}` with a diff `Accept`, `GET /repos/{o}/{r}/readme`, `POST` and `DELETE .../pulls/{n}/requested_reviewers` on GHES, and `DELETE /repos/{o}/{r}/git/refs/heads%2F...` **[captured]**.

**`glab` 1.127.0 [captured].** REST alone for `mr create`, `mr view`, `mr list`, `mr approve`, and `mr merge`. `Private-Token` carries the token, on all 234 requests. A body with `title`, `description`, `source_branch`, `target_branch`, `labels`, `assignee_ids`, `reviewer_ids`, `target_project_id`, and `milestone_id`. `acme%2Fwidgets%2Fmerge_requests%2F7%2Fapprove` and `acme%2F..%2Fwidgets` reached the stub on the first pass, and `merge_when_pipeline_succeeds` passed a policy with no body-key list.

**The allowlist's position.** Revision 3 listed 12 mutation rows with input keys, per-flavour notes, a required `repositoryAutomation` statement, and `gitlabTextGuard`. Its reasoning on `pr review --comment` stays: the preset refuses `addPullRequestReview` for every event, and a tenant can change that with its own rules.

### Git remotes and client tools

**The owner settled the git path as T, with the bundle, on VCS-covered hosts. `egress-and-git-design.md`, "Git on covered hosts", states it.** Git keeps its remote URLs, so `gh` and `glab` read the upstream host. UR-5 is met on a stub, and live hosts are owed.

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
| `netagent` CPU per block | 689 to 714 ms | 520 to 541 ms | 515 to 517 ms |
| 10 `ls-remote` | 115 to 116 ms | 87 to 101 ms | 87 to 90 ms |
| `memory.peak` | 28.7 to 29.1 MB | 27.9 to 30.4 MB | 27.9 MB |

The egress design states the remote forms, CA coverage, and the loss on uncovered hosts. **`allowRuntimeHeldCredentials`, which UC-5 owns, attaches the current value on every git request with no mint [measured].** With no current value the host answers 401 **[measured]**, on a suspended, flag-cleared, or unreleased session, with 0 pushes succeeding.

**Alternatives that keep the loopback endpoint** did not work unchanged: `GH_REPO`, `gh repo set-default`, a wrapper with no git config, `pushInsteadOf` alone, and a loopback fetch URL with the upstream on the push line **[measured]**. A hostname with a port passes `gh`'s parser, and git against that form did not run. A hybrid of T on covered hosts and the prior path elsewhere did not run, and the design has none.

**Client requirements.** `gh pr create` needs git in the image even with `-R`, and 6 of the 10 images tried have none. `gh release upload` goes to `uploads.github.com`, which needs a binding, and `gh browse` sends `HEAD`. macOS, Windows, and amd64 did not run. A wrapper or patched tool stays rejected, because it is a Lenny-specific binary, against "Lenny adds no platform tool".

Path enforcement, `allowedRefPatterns`, and the push option refusal hold on covered hosts.

### Git push options

`git push -o` reaches GitLab inside `git-receive-pack`, past everything else here. Under option D of "GitLab", git uses the project token and `netagent` reads only ref commands, so `git push -o merge_request.create -o merge_request.auto_merge -o merge_request.target=main` requests a bot-authored merge request with auto-merge, and Erin can approve a bot's merge request. `merge_request.description` also carries text past the guard. GitLab's page lists these along with `ci.skip`, `ci.no_pipeline`, `ci.variable`, `ci.input`, `integrations.skip_ci`, `secret_push_protection.skip_all`, and `security_policy.bypass_reason` **[documented]**.

The rule, on every covered host: **`netagent` refuses a `git-receive-pack` request that uses the `push-options` capability, and strips that capability from the ref advertisement, so git reports the failure before sending a pack.** A named allowlist on the VCS pool can admit a specific option, and it is empty by default. An agent using `-o ci.skip` is refused. The capture did not examine `git push`, and the worked `gh pr create` and `glab mr create` flows need no push option. Because a push option's section sits between the ref commands and the pack, `netagent` needs bounded parsing past the commands it reads today, and that is a requested change on EG-3. **[design]**

### Git pushes and the most recent push rule

`require_last_push_approval` means "the most recent push must be approved by someone other than the person who pushed it" **[documented]**. If pushes come from the App, GitHub sees the App as the pusher. The failure is this: Alice's agent pushes to Bob's pull request, and Alice approves it, with her approval counting because she is not the pusher and not the author. She has approved work she caused. The same applies to Bob approving Alice's pull request after his own agent pushes to it.

S3 measured the rule live **[measured]**. A push with an installation token leaves the person who requested it unseen as pusher, so she can approve afterwards and GitHub reports the pull request mergeable. A push with her user token, or with a scoped child of it, makes GitHub treat her as the pusher, and her approval no longer satisfies the rule. A scoped child can push.

**The design, which the owner settled under decision 10:**

1. **A session pushes as its user by default.** On a pool with `prAuthoring: user`, `pushIdentity` defaults to `user`, and pushes use the session's git child from "Child tokens per session". GitHub then enforces `require_last_push_approval` natively: if Alice's agent pushes to Bob's pull request, Alice's approval does not count for it.
2. **Lenny sets no branch rules.** The session pushes wherever its user can push on the session's writable repositories, and the repository's own branch protection and rulesets apply to the agent as they apply to her. A tenant can still set `allowedRefPatterns` on a VCS pool under the egress design, and none is written by default.
3. Revision 3 had the filter refuse a create whose head branch lay in another user's namespace. That input check went with the allowlist, and with user pushes it is unneeded: a colluding session that opens a pull request from a branch Alice's agent pushed leaves Alice as the most recent pusher, so her approval does not count when the repository requires approval of the most recent push.
4. **`pushIdentity: app`** keeps pushes on repository-limited App tokens, for a tenant that does not want a user-derived token with `contents: write` in `netagent`. GitHub then sees the App as the pusher, so a pool with `prAuthoring: user` and `pushIdentity: app` requires UG-4, which reasons about sessions rather than pushers. A write without it is refused.

Two-person review on pushed work rests on the repository requiring approval of the most recent push. Lenny cannot set that rule, and a read of branch protection needs repository administration. The setup flow states the dependency, and posture reports it where the App can read the rule.

**Commit authorship.** The egress design lets a VCS pool set the author and committer. Under PR authoring on, the gateway sets the author to the linked user: on GitHub `<id>+<login>@users.noreply.github.com` and on GitLab the link's verified primary email, read at link with `read_user`. The committer stays the pool's agent identity, and a `Lenny-Session` trailer is added by the template. This serves GitLab's "Prevent approvals by users who add commits", which excludes "the Git author and Git committer" **[documented]**, and it makes commit history truthful about who requested the work.

These fields are text the agent can change with `git -c`, and `netagent` does not read pack contents, so they are advisory on GitHub and a soft signal on GitLab (S12 tests how GitLab matches them). Signing is out of scope, because the agent holds no user key.

### Delivery

`prAuthoring: user` uses `inject`, and its provider's ceiling is `inject` or `file`. A runtime that needs its token as a file uses `delivery: file` under `user-credential-delivery-design.md`, behind the `userCredentialDelivery` grant and a binding opt-in. Revision 3 dropped `file` from the first version here. The owner's direction now is that the mechanism exists, generically, because Lenny "can't possibly handle all injection scenarios". Decision 16 records the change. `gh` and `glab` accept a placeholder **[measured]** (rounds 3 and 10) **[captured]**, so this configuration does not need it.

A token in a file readable by the agent UID can leave the pod through any allowed host and stay valid for up to 8 hours, the parent's lifetime and the upper figure until S1 settles the child's, and unlike a `federation` JWT it has no trust condition binding it to an address.

**Why Coder's delivery does not fit as a default.** Coder exposes the token inside the workspace through `GIT_ASKPASS` and `coder external-auth access-token` **[documented]**. For a person at a workspace, the person is the principal, and handing them a token they could get anyway removes nothing. An autonomous agent reads untrusted content, so any process it starts can run that command and send the result to any allowed host, after which no rule applies and no later revocation reaches it. Lenny keeps Coder's linking experience and defaults to a different delivery: the placeholder stays in the pod and the token stays in `netagent`, and `gh` runs with `GH_TOKEN=lenny-injected` as the egress design validated.

### Repository rules

`security-oversight-design.md` decision 25 and SP11 apply as written:

- A child token from a platform-shipped template with `followsEntryState`, which is `github-scoped` today, is a `mint-scoped` binding mint, so it names only `granted` entries, and a rule added during a session moves the entry and flushes the cache by binding change.
- A GitLab project token or any non-`mint-scoped` binding on a rule's API host is refused or suspended under `approvals.repositoryWrites`. `gitlab-fine-grained` is narrower than those, and it does not set `followsEntryState` until UG-5 is settled, so it is treated as non-`mint-scoped` for now. PR authoring on GitLab is therefore absent on a host with a rule, which that decision's cost already names.
- Pushes follow SO-5b unchanged, and the git child, like the API child, names only `granted` entries.
- A child minted before a rule moved its entry is revoked and reminted for the new set, which S5 makes safe. Until the revocation lands it stays valid at the host, and SP11's residual covers this.

## GitLab

### What authorization needs

UG-2 on GitLab uses `read_api` and `read_user`, which grant no write **[documented]**. The token stays in the Token Service. It ships regardless of the choices below, on GitLab.com and on self-managed, with the same 2-hour refresh rules.

### What PR authoring has to face

GitLab describes `api` as "complete read/write access to the API, including all groups and projects", and no OAuth scope limits a token to a project **[documented]**. **A user's `api` token under `inject` gives the agent the user's full power across every project and group if `netagent`'s request rules are its only limit, and a compromised `netagent` holds that token.** On self-managed, an administrator's token holds administrator power over the instance.

`glab` needs nothing unusual: REST alone for the commands captured, `Private-Token` carrying the placeholder on all 234 requests, and no `Content-Encoding` **[captured]**. With default settings it also opened one `CONNECT gitlab.com:443` **[captured]**, and the profile's `GLAB_CHECK_UPDATE=false` and `GLAB_SEND_TELEMETRY=false` stopped it **[captured]**.

### Options

| Option | `glab` unchanged | MR author | What a compromised `netagent` holds | Approve and merge stopped by |
| :-- | :-- | :-- | :-- | :-- |
| A. `api` OAuth token under `inject`, with request rules | Yes | User | The user's full API power, for up to 2 hours | Request rules alone |
| B. `write_repository` with `merge_request.create` on push | No. `glab mr create` needs the API. | Unstated by the docs (S11), and the pusher is expected | Read and write on every repository the user reaches, with no API | The token lacks API access. It relies on `merge_request.*` push options, which every other option refuses, so it would need its own allowlist entry, and then `netagent` must read `merge_request.description` for quick actions and refuse `merge_request.auto_merge`. |
| C. A or B behind a tenant opt-in, off by default | As chosen | As chosen | As chosen | As chosen |
| D. Project access token bots, as the egress design has today | Yes **[captured]** with a stub | The bot | One project's token, at that project's role | Request rules and `gitlab-deny`'s `bodyPatterns`, since a Developer-level project token can approve |
| E. A per-session fine-grained token from a parent held in the Token Service | Yes, if `glab`'s routes fit (S13) | **User** | One session's projects, with `Read` and `Create` and optionally `Update` on merge requests | **On routes, the host**, because the permission table separates `Approve` and `Merge` from `Create` and `Update` **[documented]**, then request rules. **On quick actions, `gitlab-deny`'s `bodyPatterns`**, until S13 shows whether the host applies the token's permissions to a command in a description. |
| F. A fine-grained token the user creates and registers, as a `registered` type. **Withdrawn in revision 5.** | Yes, if the same fit holds | **User** | The user's selected projects and permissions, for up to 365 days by default | The egress design refuses a stored token under `inject` on a git host a VCS pool covers, and GitLab serves API and git on one host. An exception would read cleanly in none of the three texts, and a `file` delivery of the same token is available under the flag. |

Facts behind E:

- Fine-grained tokens are generally available from GitLab 19.2 on every tier and offering **[documented]**.
- `POST /user/personal_access_tokens` accepts `granular_scopes` from 19.2, with `selected_memberships` and `project_ids` **[documented]**. "You must be authenticated" is the only stated prerequisite, and the page does not say whether an OAuth token can call it or which scope it needs.
- The REST table lists `Personal Access Token`, `Create`, on that path, so a fine-grained token holding that permission can be the parent, if GitLab lets a token create one no wider than itself.
- `expires_at` has day granularity, so revocation at session end uses the egress design's durable record.
- Whether each creation notifies the user is unknown.

### Recommendation

**LFS is refused on GitLab until a live run passes.** GitLab's project tokens and stored tokens are `mint-coarse` and `stored`, so the egress design refuses `info/lfs` on them, and `git push` of an LFS object fails there **[measured]**. GitLab's form ran on a stub **[measured]**, and the one live object tried returned an error rather than an action **[live]**, so no live GitLab action has been read.

User attribution was half of the request, and revision 1's answer left GitLab users with a bot author. With fine-grained tokens at 19.2, that no longer needs to be the answer.

**On GitLab 19.2 and later, the target is E: user-authored merge requests from a per-session fine-grained token, with no `api` token in any pod.** S13 settles it, and it needs a free account and an hour rather than any Lenny code, so it runs before UG-3 starts. The token holds `selected_memberships` with the session's `granted` projects, and `Read` on the project and merge requests, and `Create` and `Update` on merge requests, all as the template's `permissions` data. `Approve` and `Merge` are never in the default set.

The outcome of S13 selects the parent:

| S13 result | Parent in the Token Service | Cost |
| :-- | :-- | :-- |
| An OAuth token with a low scope can create the child | The link's OAuth token | The link needs that scope, and users relink once |
| An OAuth token needs `api` | The link's `api` OAuth token, on the tenant's opt-in. `provider_is_admin` refuses a link for an instance administrator. | `api` in the Token Service for those links, which widens Token Service compromise and never touches a pod |
| No OAuth token can, and a fine-grained parent can | A fine-grained token the user registers with the Token Service as the link's parent, holding only that create permission, and never delivered | A manual step per user, up to 365 days |
| No programmatic path | D remains, and the MR author stays the bot. A tenant that accepts `file`'s limits can use it under `allowRuntimeHeldCredentials`. | UR-4 stays unmet on GitLab |

On Free, GitLab's approval settings are absent **[documented]**, so E gives attribution there and the preset gives the rest.

**D remains for GitLab below 19.2 and for `prAuthoring: bot`.** There the MR author is the bot, and two-person review rests on the commit author and a paid setting, as "Residual risks" states, or on UG-4.

**A is not this configuration's recommendation, and the pool's `prAuthoring: user` never selects it.** Its residual is the user's whole account, and E reaches the same `glab` with a token whose power GitLab limits. A tenant can still define an `oauth2Link` type with `api` scope under `user-credential-delivery-design.md`, whose documentation says "no template means full reach". Revision 3 said A never ships. Under the generic mechanism, Lenny can no longer refuse that by provider without tying the mechanism to GitLab. B stays unused, since it breaks `glab` and E has a smaller reach.

Whichever token is in use, `gitlab-deny` stays on by default and the push option refusal applies, because S13 has not shown that a command in a description is limited by the token rather than by the user's own permissions. Commit author and committer follow "Git pushes and the most recent push rule", and git itself stays on the project token until S13 shows a fine-grained `Git` permission works per session.

Under `security-oversight-design.md` decision 25, these tokens are refused on the API host of a repository rule for now.

## UG-4: Independent approval check

A required status check from the App fails when the only approver is the session's requesting user. This is offered to tenants that refuse user tokens in pods, and required for a pool with `prAuthoring: user` and `pushIdentity: app`, where GitHub sees the App as the pusher. It needs no token in a pod.

**How it works.** Lenny's App reports a status `lenny/independent-approval` on each pull request head:

- A webhook receiver listens for `pull_request` and `pull_request_review` events and verifies the signature. It needs `statuses: write` or `checks: write`.
- For a pull request's commits, it collects the sessions that pushed them from `git.operation`, which records refs and new SHAs, and maps each session to a user and then to a provider identity through `user_links`. The opener comes from the pull request's author when that is a person. When the App authored it, GitHub does not say which token made the request, so the check uses the pushers alone. Earlier revisions read the opener from the head branch's namespace, which decision 10 removed.
- It passes when at least one approver's provider identifier is outside that set, and it fails otherwise. A commit it cannot attribute counts as having no person, which leaves the approver free.

It catches the case the native rule misses, including Alice's agent pushing to Bob's pull request, because it reasons about sessions rather than pushers.

**Limits.**

- A pull request still shows the App as author.
- "No user token" is true at run time, and the check needs a proven mapping from Lenny user to provider identity. UG-1 runs in a mode that keeps the numeric identifier and login and discards the token, so an attacker cannot claim someone else's identity. A tenant that refuses even that gets a mapping it must supply itself, which then rests on the tenant's own directory.
- The tenant configures the check as required on each protected branch. Lenny can verify that with a read and report it in posture, and it cannot force it.
- It adds a public webhook ingress to the gateway and App permissions, and a Lenny outage leaves the status pending, which blocks merges. That is fail-closed, at the cost of a merge outage.
- `git.operation` from `netagent` is `self_reported`, so a compromised `netagent` can omit a record.
- It needs `git.operation` retention at least as long as a pull request stays open.
- An App-authored pull request whose opener pushed nothing to it does not count the opener. An agent that opens a pull request for commits only another user's session pushed leaves its own user free to approve.

On GitLab, external status checks are the corresponding feature, on the tiers GitLab offers it, and the same logic applies. S12 confirms tier and behaviour.

## Configuration by scope

| Setting | Scope | Who sets it | Default |
| :-- | :-- | :-- | :-- |
| `link_providers` rows | Tenant, with optional TS-1 environments | `tenant-admin` | None |
| `delivery`, `apiHosts`, `clientSecret`, `narrowing`, and its `permissions` | Link provider | `tenant-admin`, with shipped templates alone on a VCS kind | `none`. A delivering VCS provider must name a shipped template. Any change to the token trust record makes stored tokens unusable. |
| `tokenTrust.allowedRecords` guardrail | Tenant security record | `security-admin`, under SO-2's delay | Absent |
| `allowedAccounts` | Link provider | `tenant-admin` | Empty |
| `identityBinding` | Link provider | `tenant-admin`, and SO-2 can forbid `unverified` | None. A write must set it. |
| `allowedPushOptions` | VCS pool | `tenant-admin` | Empty |
| `userAuthorization` on a VCS pool | Tenant | `tenant-admin`, and SO-2 can force it | `off` |
| `userAuthorization.linkProvider` on a VCS pool | Tenant | `tenant-admin` | Required with `required`. It names the link provider whose link the check reads, which must be a VCS kind on the pool's host. |
| `userAuthorization.require` guardrail | Tenant security record | `security-admin` | Absent |
| `userAuthorization.serviceIdentities` guardrail | Tenant security record | `security-admin` | Absent |
| `serviceIdentities` on a VCS pool | Tenant | `tenant-admin` | Empty |
| `onAccessLost` | VCS pool | `tenant-admin` | `deny` |
| `prAuthoring` (`off`, `user`, or `bot`) | VCS pool | `tenant-admin` | `off`. `user` requires `userAuthorization: required` and a delivering link provider. On GitLab, `user` needs UG-5 and a host at 19.2 or later. `bot` selects option D on GitLab, and on GitHub it behaves as `off`. |
| `requestRules` on the binding | Binding | `tenant-admin`, and SO-2 can require a preset | Resolved at I8 from the flavour of the VCS pool's provider and host, unless `preset: none` |
| `pinHosts` | VCS pool, when `required` | `tenant-admin` | `true`, once EG-12 lands |
| `allowRuntimeHeldCredentials` | VCS pool, on every pool covering the host, whatever its mode | `tenant-admin`, and SO-2 can forbid it. UC-5 owns it. | `false` |
| `allowedRefPatterns` | VCS pool, under the egress design | `tenant-admin` | None. Lenny writes no branch rules. |
| `pushIdentity` (`user` or `app`) | VCS pool, with `prAuthoring: user` | `tenant-admin` | `user`. `app` requires UG-4. |
| `recheckIntervalSeconds` | Helm, with a VCS pool narrowing | Platform and `tenant-admin` | 300, with a floor of 60 |
| `verdictCacheSeconds` | Helm | Platform | 60 |
| `verdictMaxAgeSeconds` | Helm, with a VCS pool narrowing | Platform and `tenant-admin` | 900, with a ceiling of 3600 |
| `appTokenRequestsAlertPerHour` | Helm | Platform | 1,600, against GitHub's 2,000 per App |

Every non-spec default is operator-tunable and documented as such. Request rule bounds, types, link provider generalization, and the `userCredentialDelivery` grant are under `user-credential-delivery-design.md`. A pool with `userAuthorization: off` and no `userCredential` binding behaves as the egress design says, with no check, link, or extra call.

## Worked examples

### Alice: link to `gh pr create` to Bob's approval

Tenant `acme`, App `acme-lenny` installed on the `acme` organization, VCS pool `github.com/acme/` with `userAuthorization: required` and `prAuthoring: user`. Pool `coding` has `maxConcurrentSessions: 1` and no recycling.

1. Alice calls `POST /v1/sessions` with `acme/web` declared writable. The pool's binding attaches, by "The attach rule", and it replaces the tenant's App binding on that host for her session. She has no link, so she receives `403 USER_LINK_REQUIRED` and a `linkUrl`, and no pod is claimed. **[design]**
2. Her browser opens the URL, logs in through the broker, and receives the cookie. The link provider sets `identityBinding: directory`, and `acme`'s worker has recorded her provider identifier. It goes to `github.com/login/oauth/authorize` and returns. The gateway hands the code to the Token Service, which exchanges it and reads `GET /user`, and it checks `GET /user/installations` against `allowedAccounts`. The identifier matches, and she confirms `@alice-acme`. **[design]**, on **[documented]** endpoints
3. Alice retries. The Token Service runs `GET /repos/acme/web` with the parent. `permissions.push` is true, and the identifier matches. The gateway writes a `granted` entry, then runs `ls-refs` and claims a pod. **[design]**
4. The adapter clones through `netagent`. On a mirror route, I3 reads the entry and allows. A mint-scoped read token comes from I8, with the check before the cache lookup. **[design]**
5. The agent runs `git checkout -b fix-login`, commits as Alice's noreply address, and runs `git push`. `netagent` reads the ref update, and its I8 call mints the session's git child, a child of Alice's link with `contents: write` on `acme/web` alone, under `pushIdentity: user`. The push goes through `lenny-egress`, and GitHub records Alice as the pusher (S3). A `git.operation` record is written. **[design]**, the push path is **[measured]** under T on a stub, with no user check, and a child's push is **[measured]** live
6. The agent runs `gh pr create --title ... --body ... --head fix-login`. The remote reads `https://github.com/acme/web.git`, as T leaves it. `gh` runs because `GH_TOKEN=lenny-injected` **[measured]** **[captured]**. `netagent` terminates TLS for `api.github.com`. Its first I8 call for the API host mints the session's API child with `repository_ids: [acme/web]` and `pull_requests: write, contents: read`, and it returns `github-deny` resolved. **[design]**
7. `gh`'s traffic **[captured]**: over HTTP/2, `POST /graphql` with queries `RepositoryInfo` and `PullRequestForBranch`, and on GHES also `GET /api/v3/meta` and `Issue_fields` (`__type` feature detection). Each query passes. Then `mutation PullRequestCreate($input: CreatePullRequestInput!) { createPullRequest(input: $input) { pullRequest { id url } } }` has one root field the preset does not name, so it passes **[measured]**. `netagent` forwards its own printing and re-encoding, with `Authorization` overwritten. The stub received the injected token and no placeholder **[captured]**, and the printing and token overwrite left `gh`'s exit code and operations unchanged **[captured]**. That GitHub creates the pull request with Alice's avatar and the App's badge is **[documented]** and **[stub cannot show]**.
8. `credential.mint` records `tokenSource: userChild`, the `linkRef`, and the hash. **[design]**
9. Alice tries to approve in the GitHub UI, and GitHub refuses because she is the author. Bob approves as a person. The most recent push came from Alice, and Bob is neither the author nor the pusher, so `require_last_push_approval` passes. Bob merges. Two people took part in this merge, because the preset refuses the agent's approval and Alice cannot approve her own.
10. Later, Alice's session pushes a fix to Bob's pull request `bob-cleanup`, which her push rights allow. GitHub records Alice as its most recent pusher, so her approval of Bob's pull request no longer satisfies the rule, and someone other than Alice must approve after her push (S3 **[measured]**).

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
- The bare CR document from "What reaches the host". The lexical rule refuses it, and were it to pass, the printer would forward the query alone **[captured]**.
- `gh api -X POST repos/acme/payments/pulls -f title=x -f head=y -f base=main` is forwarded, since target checks are gone. GitHub answers 404, because the child covers only `acme/web` (S15). **Under no template, this would succeed.**
- `gh api -X POST repos/acme/web/pulls/7/reviews -f event=APPROVE`. The preset's REST rule refuses it, `r%65views` is refused on the escape, and `reviews.json` and `reviews.x` are caught by the first-dot stem **[measured]**.
- `gh api graphql` with `createCommitOnBranch`, and `PUT contents` through `gh api`: named by the extended preset, and refused by the host under `contents: read`.
- `gh pr comment 7 --body "/approve"` on a repository running an approval bot posts under Alice's name, unless the tenant added a rule. The bot acts if it trusts her.
- `gh pr close 7` on Bob's pull request succeeds. The action is attributed to Alice with the App's badge, and Bob can reopen it. `gh pr edit 7 --base release` succeeds too, which is what input-key checks used to refuse.
- A mutation GitHub adds next year with merge power passes until the preset names it.
- Repeated refusals raise `user_credential_rule_refusals`, which Dave's subscription receives.

### The same on GHES behind an outbound connector

Tenant `acme` runs `ghes.acme.internal` behind connector `acme-dc1`. The link provider has `kind: github-enterprise`, and the outbound host has Acme's CA and no `allowMirror`.

- Alice's browser reaches GHES on Acme's network, and the provider's redirect comes back to Lenny's public URL, so no inbound rule is needed. The Token Service exchanges the code through `lenny-egress` with `acme` asserted, under a grant, and validates against the CA before it sends the client secret. **[design]**, on connector C6 and C9
- The VCS pool's host makes it GHES, with the API on the host under `/api/v3/`. `GH_HOST=ghes.acme.internal` and `GH_ENTERPRISE_TOKEN=lenny-injected` are set through the client proxy profile on a tenant-owned pool, and the remote stays `https://ghes.acme.internal/...`. `gh` posts to `https://<host>/api/graphql` and `/api/v3/...` **[captured]**, uses `requestReviews` rather than the login form, and sends `GET /api/v3/meta` **[captured]**. The stub was no connector, so the connector leg is **[design]**.
- The binding's single host also serves the web and git, so under `default: allow` the token attaches to every path on it, including web routes, whose behaviour with that token is not established (S15). Smart HTTP paths take the egress design's git path, which reaches the host through the connector. The flavour names the API prefixes, and `/graphql`, `/api/foo`, and `/api/v3/graphql` are refused on it **[measured]**.
- `gh pr review`, the aliased mutation, and the REST calls behave as above. The scoped endpoint exists on GHES 3.22 **[documented]**, and S14 fixes the oldest supported version.
- **Connector offline.** Sweep and mints answer `unavailable`. New sessions get the retryable error, and running sessions have no store reads, since `allowMirror` is off and a connector host's git stops anyway. Links are untouched. No traffic takes the default route (C10).
- GHES disables rate limits by default per the egress design. S2 ran on `github.com` alone, so whether GHES limits children per user is untested, and a refusal there surfaces as GitHub's error under "Child tokens per session".

### GitLab

Tenant `initech` runs `gitlab.initech.internal` at 19.4, Premium, and S13 has passed with an OAuth parent. Erin is a developer at `initech`, and `prAuthoring: user` is on.

- Linking uses the OAuth application with the scopes S13 settled. The link holds `provider_user_id`, the verified email, and `is_admin`. The token stays in the Token Service. **[design]**
- Bind: `GET /api/v4/projects/:id` and the access level check pass. **[design]**, S11
- The agent runs `git push` under the project token. The commit author is Erin's verified email. `git push -o merge_request.create -o merge_request.auto_merge` is refused **[design]**.
- On its first API request, `netagent` asks I8. The Token Service creates a fine-grained token with `selected_memberships` on the session's project, `Read` and `Create` on merge requests, and tomorrow's `expires_at`. **[design]**, S13
- `glab mr create` with `GITLAB_HOST` and `GITLAB_TOKEN=lenny-injected` sends `GET /api/v4/projects/initech%2Fweb?license=true&with_custom_attributes=true` and `POST /api/v4/projects/initech%2Fweb/merge_requests` over HTTP/2 **[captured]** with a body the preset's `denyBodyKeys` admits. `netagent` re-encodes the body when `gitlab-deny`'s patterns apply, and it overwrites `Private-Token`. The stub received the injected value **[captured]**. On GitLab the MR's author is Erin, and GitLab's creator rule stops her approving **[documented]**, **[stub cannot show]**.
- `glab mr approve` sends `GET .../merge_requests/{n}` and `POST .../approve`, and `glab mr merge` sends `PUT .../merge`. Both are on `gitlab-deny`, and they were refused with nothing written under the allowlist **[captured]**. Under the token, the host refuses too **[documented]** (S13).
- `glab mr create --description $'Fix login\n/merge'`, and `/approve` on a following line or after a bare CR, are refused by the preset's `bodyPatterns` **[captured under the prototype's wider pattern]**. `--description $'See /usr/bin/env\n/usr/bin/env'` passes **[measured]**, where the earlier wider pattern refused it **[captured]**.
- `glab api graphql -f query='mutation { mergeRequestAccept(...) }'` and an aliased form are refused by the preset. `glab api` with each refused route is refused in every spelling tried **[measured]**.
- `glab api -X POST 'projects/initech%2Fweb/merge_requests/7/notes?body=%2Fmerge'` is refused, since a write takes no query string. **[design]**
- `glab api -X POST 'projects/initech%2Fweb%2Fmerge_requests%2F7%2Fapprove/merge_requests'` is refused by `projects/{encoded}`, whose `refuseDecoded` holds route words **[design]**, while `initech%2Fweb`, nested groups, and lower-case `%2f` pass **[measured]**.
- Erin leaves the project: the sweep refuses, the binding change flushes, and the Token Service revokes the session's token through the durable record.
- On GitLab 18.x or under `prAuthoring: bot`, the same `glab` commands run under a project token, and the MR author is the bot. There the preset alone stops approval and merge, and Erin can approve the bot's MR unless "Prevent approvals by users who add commits" excludes her through a commit author the agent can edit.

## Changes requested of egress-and-git-design.md

`egress-and-git-design.md` revision 51 applied these, adjusted for the preset model, and lists them under "Reserved for other designs". They remain additive, and they do nothing until a UG or UC proposal lands.

| Applied request | Proposal |
| :-- | :-- |
| I8 responses carry `cacheUntil` | EG-2 and EG-4 |
| I8 responses carry `handling`, and `netagent` refuses a credential naming anything it does not implement at that version | EG-4 |
| A request inspection stage in `inject`, reached by both transports, that can replace the forwarded path, headers, body, and query string. Under the preset model it evaluates `requestRules` from I8, with no provider code, and strips credential headers rather than holding a header allowlist. | EG-4 |
| A session holds at most one `inject` binding per host, extended to refuse a `file` and an `inject` binding on one host, evaluated on type hosts | EG-4 |
| `scope: poolAttach` under "The attach rule", and a stored token refusal with its one `file` exception | EG-4 |
| `caller_type` on the session row, through `pkg/binding`, copied to a child, and a VCS pool as a source of pins, unfiltered by environment | EG-2 and EG-12 |
| A refusal of differing flavours on one host, an expected route asserted on the CONNECT and in the level 2 contract, and a covered set read unfiltered | EG-1, EG-2, EG-3, and EG-4 |
| A `handling` value and a mint refusal for a session under `allowRuntimeHeldCredentials`, which UC-5 owns | EG-3 and EG-4 |
| A stored secret refused on a `required` pool's hosts, and an unknown or `userRefused` state denying reads | EG-2, EG-3, and EG-9 |
| `secret` is a typed union, with `userCredential` | EG-4 |
| A pre-mint step before the mint cache lookup | EG-1 and EG-2 |
| Readable and writable entries as records, with `verifiedAt`, and a declared-only input on the readable formula | EG-2, EG-3, and EG-9 |
| The mirror's second-gate I3 check names the repository | EG-5 and EG-2 |
| `pkg/binding` returns the session's user, root session, caller type, and the user's invalidation level | EG-2 |
| The provisional resume binding excludes a `userCredential` binding until `running` | EG-2 |
| Push option refusal on `git-receive-pack`, on the terminated connection | EG-3 |
| Head read deadline and bare LF refusal | EG-2 and EG-4 |

**Also applied:** a client tool sees the upstream URL from `git remote -v`, because egress revision 51 states T in its mechanism text.

Not requested, because each is additive with no rework: `tokenSource` and `linkRef` on `credential.mint`, restated in **Erasure and holds** when UG-2 lands, the token path on `github.com` among declared mint endpoints, the git path's mint returning the session's git child under `pushIdentity: user`, which changes what the Token Service returns on I8 and leaves `netagent`'s git path as it is, and the `prAuthoring`, `userAuthorization`, and `onAccessLost` fields through EG-9's write path.

## Dependencies

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| UG-1 (linking, whose browser binding addresses F-9.3.18) | UC-1, EG-1, EG-2, EG-9, TS-6 for the login path, and OC-1 and OC-2 for hosts behind a connector. | The digest format and `token_trust_records`, VCS pool hosts, the single write path, and the route to every link provider endpoint. It keys on a provider name, so UC-2 generalizes it with no rework. |
| UG-2 (authorization) | UG-1, UC-3, EG-1, EG-2, EG-3, and EG-5 for mirror rules. TS-4 for the lifecycle hook. | The session input check and `retireOnRelease`, the mint path, `pkg/binding`, I8, writable entries, and the mirror. |
| UG-2's pin wiring | EG-12 | A `required` pool as a source of pins. UG-2 does not wait for it, and the residual stands until then. |
| UG-3 (PR authoring on GitHub and GHES) | UG-2, UC-2, UC-3, and UC-4 with its presets. S1, S2, S3, and S15 passing, and EG-3 with T. | The binding, the template, the rules, and `gh`'s remotes. |
| UG-4 (approval check) | UG-1 in identifier-only mode, EG-3, EG-10 | `git.operation` and `credential.mint`. Independent of UG-3. |
| UG-5 (GitLab user authorship) | UG-3, and S13 and S17 passing. S13 runs before UG-3 starts, so UG-5 lands with UG-3 when it passes. | The token path and the preset's behaviour on a live host. |

| Other design | Interaction |
| :-- | :-- |
| `security-oversight-design.md` | SO-2 gains the guardrail rows for `userAuthorization`, and `user-credential-delivery-design.md` adds `requestRules.requiredPresets`, `tokenTrust.allowedRecords`, `userCredential.file`, and `allowRuntimeHeldCredentials`. SO-4a's `impersonated_by` feeds "Identity cases". SO-5b and decision 25 apply unchanged, and it now states them for this design. |
| `tenant-scoping-identity-design.md` | TS-1 scopes `link_providers`, types, and `userCredential` bindings. TS-4's upsert and refusal apply, and §11.4 gains the revocation step. It now cross-references this. |
| `tenant-outbound-connector-design.md` | OR-3 covers the Token Service's calls, and the GHES example follows C6, C8, C9, and C10. This adds the token and scoped endpoints to what a connector carries, with no design change. It now states the OAuth link case. |
| `forensics-evidence-preservation-design.md` | The `quarantined` state stops child mints with no new work. FE-5's "Rotate credentials" step gains removing a user's link, which revokes its grant, because revoking a child needs the token value and no route takes a hash. FE's rule R-USER-CREDENTIAL-POD compares mints with the pod model. It now states these, with delivered tokens in evidence. |
| `user-credential-delivery-design.md` | The mechanism this document configures. It owns the binding, the sources, `file`, request rules, and presets. |

No proposal in the egress design depends on one here.

## Specification sections affected

- [§4.3 Token Service](../spec/04_system-components.md#43-token-service): a third credential category, and "Pods never hold downstream OAuth tokens" gains a stated exception for a child token in `netagent` memory.
- [§4.9 Pre-Authorized Credential Flow](../spec/04_system-components.md#pre-authorized-credential-flow): providers with no delivery refused, and the `type` field, under `user-credential-delivery-design.md`.
- [§9.3 OAuth/OIDC Flow](../spec/09_mcp-integration.md#oauthoidc-flow): the host-overlap refusal, and the browser binding on the callback.
- [§10.2 Authentication](../spec/10_gateway-internals.md#102-authentication): the permission matrix gains link rows and `manage_user_links`, and the broker contract and a customer source's claim mapping gain the provider identity claim.
- [§11.4 User Invalidation](../spec/11_policy-and-controls.md#114-user-invalidation): the revocation step at every level.
- [§12.8 Compliance Interfaces](../spec/12_storage-architecture.md#128-compliance-interfaces): `TokenStore` deletion gains provider revocation with the 30-minute limit, and the erasure statement names `linkRef`. [§12.9 Data Classification](../spec/12_storage-architecture.md#129-data-classification) classifies `user_links` as T4.
- [§13.3 Credential Flow](../spec/13_security-model.md#133-credential-flow): "Pod never receives: client tokens, downstream OAuth tokens" gains the same exception, with `netagent` named. Session input refuses an impersonation bearer on affected sessions.
- [§4.2 Session Manager](../spec/04_system-components.md#42-session-manager): `caller_type` on the session row, under `user-credential-delivery-design.md`.
- [§14](../spec/14_workspace-plan-schema.md): `readableRepositories`. [§15.1](../spec/15_external-api-surface.md#151-rest-api): the link routes and new error codes. [§16.5](../spec/16_observability.md#165-alerting-rules-and-slos) and [§16.7](../spec/16_observability.md#167-section-25-audit-events): alerts and audit events.
- [§5.2](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes): the refused pod models. §7.1 and [§7.3 Retry and Resume](../spec/07_session-lifecycle.md#73-retry-and-resume): checks at bind and resume.

## Defects found while verifying

These exist today and are recorded here because this document edits nothing else. Each is filed in `BUILD-GAPS.md` under the identifier its entry names.

- **Account linking in §9.3 (F-9.3.18).** `handleAuthorizeConnector` returns an authorization URL and state to any authenticated caller, and `handleConnectorOAuthCallback` carries no bearer and no cookie. An attacker sends the URL to a victim, who approves, and the victim's token lands under the attacker's tenant, connector, and user. The specification's "bound to the initiating session ID and connector ID" also differs from the code, which binds tenant, user, environment, and session. Severity is High wherever a confidential connector is registered, and a public connector is exposed as well, because the gateway supplies the PKCE verifier itself. The fix is this document's browser binding.
- **Refresh absent (F-9.3.20).** `connectoroauth.RefreshAccessToken` has no caller, and `Invoker.bearerFor` returns an expired token, so a GitHub or GitLab connector stops working at its expiry.
- **User-scoped credentials decrypted in the gateway (F-4.3.1, its deferred cleanup).** `credentialpg.New(w.pgPool, w.kmsProvider)` runs in `cmd/lenny-gateway/credentialsurface.go`, against §4.3's reservation of KMS decrypt for the Token Service and §4.9's "Token Service stores it" **[source]**.
- **Inert registration (F-4.9.46).** `POST /v1/credentials` stores `provider: github`, `aws_bedrock`, or `vault_transit`, which can never be delivered.
- **Session input under impersonation (F-13.3.26).** `handleMessages`, `handleResume`, `handleMemoryWrite`, `lenny/send_message`, `lenny/memory_write`, and the `/v1/responses` and `/v1/chat/completions` translators admit an impersonation bearer, and §13.3 limits only mints. This matters beyond this design.
- **Connector client secret resolution (F-9.3.19).** A connector's `clientSecretRef` is `namespace/name`, and `connectorsecret.KubeResolver` resolves it with no tenant check **[source]**. Every `/v1/admin/connectors` route uses `requireAdmin`, which admits only `platform-admin` (`pkg/gateway/externalapi/admin/routes.go` and `tenants.go`), and the outbound connector design's tenant-admin routes are a different resource, so this is no tenant-to-tenant escalation in today's tree. **The defect is that the resolver cannot read there.** The chart gives the gateway's service account `create`, `update`, `delete`, and `deletecollection` on Secrets in the credentials namespace (`lenny-gateway-credential-writer`), and `get` on one named admin token Secret in its release namespace (`lenny-gateway-admin-token`). The resolver uses an uncached client, so under the chart's defaults a `Get` on §9.3's own example, `lenny-system/github-client-secret`, is forbidden, and a confidential connector's token exchange fails at the callback. This comes from reading the chart and has not run.

## Documentation

- A new operator page for link providers, covering App registration for each host, the callback URL, and the recommended read-only App for UG-2.
- A tenant admin page for `userAuthorization`, `prAuthoring`, and the refused pod models, stating the revocation bound and the outage rule, and how to add rules for its own bots.
- A runtime author section stating that pushes act as the session's user, the refusal format, and which `gh` and `glab` operations the presets refuse.
- A security page stating the residual risks below in the same words.
- Doc tests pin each error code to its page, and tier 11 keeps the preset page generated from the data.

## Tests

Every test carries a `// spec:` annotation once the specification sections land. Tiers follow the repository's model.

### Tier 0: static

Presets pass a schema check. A lint refuses `user_links` access outside the Token Service.

### Tier 1: unit

- `github-deny` and `gitlab-deny`: each named operation refused on each spelling, and an unnamed operation passing. Rule evaluation itself is tested under `user-credential-delivery-design.md`.
- The capture's attack rows as a fixture under deny mode, including aliases, multiple operations, root fragments, batches, duplicate keys at every depth, a bare CR, and deep nesting.
- Canonical forwarding: the printed document re-parses to the same operation and fields, the variables re-encode to one value per key, and the corpus documents read the same under gqlparser and graphql-ruby after printing.
- Each bound, at its limit and one past it, including the 40,000-level document and a bare-LF head.
- `gitlab-deny`'s `bodyPatterns`: each command, alias, case, whitespace prefix, line separator, and fence, with `/usr/bin/env` admitted, and a write with a query string refused. `glab`'s `%2F` project paths pass.
- `git-receive-pack` with each push option refused, and the capability stripped from the advertisement.
- Verdict classification for each status and header.
- The per-session cache key, the git child attached only to smart HTTP paths and the API child to the rest, including on a GHES host serving both, the per-App counter and alert, link state transitions, and the page before the redirect naming changed hosts.

### Tier 2: component

The tests run the sweep job against a fake Token Service, including a stall and jitter, and the pod model validators against envtest where they read pool records.

### Tier 3: contract

- I8 with `cacheUntil` and `handling`, including an old `netagent` stand-in refusing an unknown handling.
- The Token Service link API.
- Presets replayed against recorded `gh` and `glab` traffic per pinned version, on both GitHub flavours, on Alpine and Debian images, and with `__type` answers per supported GHES, with the capture of 2026-11-03 as the first fixture.
- A test that fails when a schema diff adds a name in the review, merge, queue, or ref families with no decision recorded.
- `gh` and `glab` commands run in a repository under the rendered git config, on each flavour, with unchanged remotes. This is the case the capture missed.
- `gh pr edit` with every flag leaves no partial write under the preset.
- `credential.mint` with the new fields.

### Tier 4: integration

- Each `identityBinding` value, with a second account refused and a directory change moving a link to `user_act_required`.
- A connector write refused on a link provider's host set when a pool on the host is `required`, and accepted otherwise.
- "The attach rule": a human session with a writable repository replacing an App binding on that host alone, a read-only session not attaching, a suspended user binding restoring nothing, a listed service session taking the App binding, an unlisted `agent` caller refused, and a child with and without a lease entry.
- `prAuthoring: user` refused with `userAuthorization: off`, with no provider, or with a ceiling of `none`, and with `pushIdentity: app` unless UG-4 is on.
- Session end revoking both children, a failed revocation retried, and a changed granted set reminting.
- An empty `granted` set minting no child.
- A session on a `required` pool under each row of "Other tokens on these hosts", and a `required` pool's hosts pinned.
- Link flow against a fake provider, with a mismatched browser login, a missing cookie, a replayed state, a second user on one account, and a refused `allowedAccounts`.
- Bind, resume, and mint refusals. No link never reaches an App token.
- Mid-session revocation against the fake provider: flush, tunnel close, I8 refusal, mirror refusal, and each bound in "Latency, by path".
- Refresh: a race across replicas produces one provider call, a lost response moves to `user_act_required`, and a store failure after the response recovers.
- Provider revocation for each row of the lifecycle table, including a host unreachable during erasure, with the destroyed ciphertext and the receipt.
- An impersonation bearer on each session input route.
- Warm fill on a `required` pool.

### Tier 5: end to end on Kind

The pod model refusals on Kind pods, the mirror path, and `gh` and `glab` binaries in a pod against a fake GitHub and GitLab. Network policy stays unchanged by these features.

### Tier 6: end to end on cloud

Against a test GitHub organization, GHES, and GitLab: a link, a child token, `gh pr create`, GitHub's enforcement of the child's limits, and refusal of review and merge by the preset.

### Tier 7: load

The tests measure sweep cost at 10,000 sessions, including deduplication and the entry `verifiedAt` write rate, refresh lease contention, and rule evaluation latency and memory at the concurrency cap with bodies at the bound, with `-race`.

### Tier 8: chaos

The tests cover a host outage with `required` pools, with new sessions refused and store reads ending at `verdictMaxAgeSeconds`, a stopped sweep, the loss of the Token Service, Redis, and Postgres, a `netagent` restart with a lost cache, and a lost binding push.

### Tier 9: security

- Every example under "A prompt-injected agent", and the 522-row attack table and the bypass table re-derived for deny mode, against shipped `netagent` builds.
- Fuzzing of the rule evaluator, with a corpus differential across gqlparser, graphql-go, graphql-js, and graphql-ruby in both lexers on each parser upgrade.
- A token never appears in a log, span, or record.
- `acme` cannot name `globex`'s client secret, and a change to any token trust record field, including a route or CA, sends no old token anywhere.
- Cross-tenant: a fake GHES copying another tenant's identifiers receives no cached verdict or child.
- A hostile host: redirects, oversized bodies, hostile logins, and an SSRF target at registration.
- Account-link attacks in both directions.

### Tier 10: conformance

Adapter contracts do not change. The runtime battery gains `gh` and `glab` cases under `inject`.

### Tier 11: documentation

The tier checks error codes, alert runbooks, and the generated preset page.

## Validation owed

Live spikes S1, S2, S3, S5, S6, S15, and S16 ran on 2026-11-06 against a test organization, under `evidence/user-git-identity-design/spike-ghlive/`. The capture answered what a client sends and what the allowlist did with it, on plain remotes. S1, S2, S3, and S15 must resolve before UG-3 starts, and S13 runs before it as well.

| Spike | Question | Decides | State |
| :-- | :-- | :-- | :-- |
| S1 | Does a child survive a refresh of its parent, and what sets `expires_at`? | `reauth-on-401` and `cacheUntil` caps | **Done, live** **[measured]**: a child survives its parent's refresh, the old parent answers 401 after a refresh, and a child's `expires_at` is 8 hours from its own creation (`evidence/user-git-identity-design/spike-ghlive/`). |
| S2 | Does the ten-token and ten-per-hour rule apply to GitHub App user tokens and to the scoped endpoint, and what happens past it with no browser? Does the scoped endpoint count toward the 2,000 per hour? | Decision 7: per-session children with no Lenny limit | **Done, live** **[measured]**: no per-user cap on children. 50 children of one parent in one hour stayed valid, with no revocation and no re-authorization prompt, and the endpoint counts against the `core` rate limit. The 2,000-per-hour App limit was not reached. |
| S3 | With `require_last_push_approval`, what does GitHub treat as the pusher, across a push from an App token, a user token, and a scoped child, each with author and approver combinations? Can a pull request's author approve when the App pushed? Can a scoped user token push? | Decision 10: `pushIdentity: user` by default, with no branch namespace | **Done, live** **[measured]**: an installation-token push leaves the approver unseen as pusher (`CLEAN` after the pusher-requester approves), while a push by the person's user token or by its scoped child makes GitHub treat that person as the pusher (`BLOCKED`). A scoped child can push. `require_extra_approval_for_unattributed_changes` did not change the App case. |
| S4 | SAML-enforced organizations: does a user token need an active SAML session, does a child inherit that, and what does expiry of that session look like to the check? GitHub says to "start an active SAML session" before reauthorizing **[documented]**. | The `sso_required` handling | Open, live |
| S5 | Does deleting a child leave its parent and siblings valid? | Child revocation | **Done, live** **[measured]**: revoking one child leaves the parent and siblings valid. |
| S6 | What do `permissions` fields report for an App user token with a restricted App? Do org base permissions and teams appear? Which App permission reads `GET /user/emails`, and can one verified email sit on two accounts? | The check, and `verifiedEmail` | **Done, live** **[measured]**: the repository `permissions` object reports the user's role, including a base permission, for a metadata-only child too, so it does not reflect narrowing. The collaborator permission endpoint answers for a metadata-only child. `GET /user/emails` answers for every child, whatever its permissions. The two-account email question was not tested. |
| S7 | Is `gqlparser` suitable? | Parser choice | **Done for the rule evaluator's parsing** **[captured]** at 2.5.33. A black-box test sending the corpus to a scratch repository on GitHub would name its parser, and the design no longer depends on the answer. |
| S8 | What does `gh` send? | Preset tests | **Done for `gh` 2.103.3 on GHES and github.com** **[captured]**, and for `ghe.com`, 7 versions, and both clients **[measured]**. The history of 68 releases is **[source]**, names only. Owed: `__type` answers per GHES version, since the stub selects `gh`'s newest code paths. |
| S9 | `netagent` memory at the rule bounds. | The concurrency cap and `GOMEMLIMIT` | **[measured]** to 41.7 MB `memory.peak`, under a 15,000 token limit. Check allocation at the final bounds is **[measured]**, and `memory.peak` under them is owed. |
| S10 | What does `glab` send? | Preset tests | **Done** for `mr create`, `view`, `list`, `note`, `update`, `close`, `revoke`, `approve`, `merge`, `rebase`, and `issue create` **[measured]**. 67 rows gave 8 forwarded, 37 denied refused, 20 refused beyond the expected list, and 2 with no write. Owed: `mr reopen`, a fork flow, and whether `glab` names a branch holding `/` with `%2F`, which `repository/branches/{encoded}` admits. |
| S11 | GitLab: which field reports effective access including shared groups, and who authors a merge request from `merge_request.create`? | The check, and option B's record | Open, live |
| S12 | GitLab: how does the committer rule match a commit's author, and on which tiers are external status checks offered? | Commit authorship and UG-4 on GitLab | Open, live |
| S13 | Can an OAuth token call `POST /user/personal_access_tokens` with `granular_scopes`, and with which scope? Can a fine-grained token create one? Does each creation notify the user? Does `glab mr create` run under `Read` and `Create` alone, and `mr note` and `mr update` under which additions? **Under a `Create`-only token, does a description holding `/approve` or `/merge` execute?** | The GitLab recommendation and UG-5 | Open, live. Needs a free account and no Lenny code. |
| S14 | The oldest GHES release with the scoped endpoint and each endpoint this design uses. | The support statement | Open, live |
| S15 | With a provider-issued token against a scratch repository: does a child approve, and does `pull_requests: write` alone merge, through REST and GraphQL? Does the child's repository limit bind node-ID mutations? Does GitHub honour batches, GET queries, form bodies, or method overrides? What is the true PR body limit? Which fields fail under a narrow child on `gh pr view`? What does the host do with an empty repository list, which this design never sends? Do bots see `performed_via_github_app`? Does an App token's or child's push trigger `pull_request` workflows, with which secrets? | Whether request rules have a second layer at the host, and the CI residual | **Done in part, live** **[measured]**: a `pull_requests: write` child approves and cannot merge through REST or GraphQL; the repository limit binds node-ID mutations; batches, form bodies, and method overrides are refused or ignored, and `GET /graphql` returns the schema; a 4 MiB body was accepted; `performed_via_github_app` names the App; an empty repository list is refused. `gh pr view` and workflow triggering were not run. |
| S16 | Does GitHub keep approvals when a pull request's base changes? | How serious `baseRefName` passing is, and whether a tenant should add a pattern for it | **Done, live** **[measured]**: an approval survives a base change to an unprotected branch, and a change back to a branch whose rules require review dismisses it. |
| S17 | On a live GitLab: do descriptions and notes created through the API run quick actions, under which permissions, with what case and whitespace rules, inside fences, and after a bare CR? How is an encoded project path routed? | `gitlab-deny`'s patterns | Open, live |
| S18 | The git path: candidates L, T, and P | UR-5 | **Closed on the stub [measured], and T is settled**, as "Git remotes and client tools" lists. Not run: macOS, Windows, amd64, the .NET SDK image, a `glab` fork flow, read after push, git against `ghe.com` beyond the stub, a T and P hybrid, and and a decision service, which ran as a stand-in. |
| S19 | The attack table and bypass analysis under `default: allow`, spellings, literals, REST twins, `gh` and `glab` under the presets, and override headers | UR-5's "any command" within the token, UR-6, and whether deny mode needs more refusals | **Closed on the stub [measured].** Under the final preset, 1,230 rows on each flavour: 1,069 refused, 160 reached with no denied action, 0 denied actions with the credential, and 1 with no credential through an opaque tunnel. 3,033 of 3,045 predicted rows matched, and the 12 were harness errors. **The stub proves forwarding only.** Host decoding has evidence for GitLab's framework alone: Grape routed `appr%6Fve`, a trailing slash, `//`, and `approve.json` to the handler and answered 404 for `merge.foo`, and a generic Rails router routed a trailing slash, `//`, `.json`, and `merge.foo`. GitHub's stack is unknown. Owed: every provider's behaviour. |

## Residual risks

- **Compromised `netagent` on GitHub and GHES.** It holds a child with `pull_requests: write`, and GitHub has no narrower permission. It can approve other users' pull requests on the session's repositories until the child expires or is revoked. Under `pushIdentity: user` it also holds the session's git child with `contents: write`, which it can send to the API to merge an approved pull request as the user. It also holds App tokens, including write. Request rules are enforced in `netagent`, so they provide nothing here. Compromise of `netagent` remains the boundary where two-person review ends, for those repositories.
- **Compromised `netagent` on GitLab.** It holds one session's fine-grained token under E, or one project's token under D. `api` tokens are absent from pods, so a user's power over other projects is out of reach. 
- **Token Service compromise.** It holds parent tokens, and refresh tokens, for every linked user. It already holds every App's private key and every stored secret. On GitHub a parent is limited by the App, which a read-only App narrows to nothing, and on GitLab with `read_api` a parent cannot write. Under UG-5, if S13 requires it, it holds `api` parents, which widens what its compromise exposes and is why that case needs a tenant opt-in.
- **The revocation window.** A revoked user keeps git access through an honest pod for up to 6 minutes, and for up to 15 minutes of reads during a host outage.
- **Workspace content.** What a session has cloned stays in the workspace, and in its checkpoints and artifacts, after access is lost. `onAccessLost: terminate` ends the session and leaves stored artifacts under the owner's existing access.
- **Allowed mutations on other people's work.** On a session repository, Alice's agent can change the title, body, base, or state of Bob's pull request, close it, comment on it, and request reviewers, because rules do not see authorship or input keys and the host allows it. These are attributed, and most are reversible. A base change can move an approved pull request onto another branch (S16).
- **New operations.** Under `default: allow`, a new GitHub or GitLab operation with approval or merge power passes until the preset names it. The owner accepted this. About one a year needs only `pull_requests: write` on GitHub, in one reading **[source]**.
- **`permissions` widened.** With `contents: write` or `actions: write` on the child, merges, content writes, and ref writes rest on the preset alone.
- **Pushes with the user's rights.** Under `pushIdentity: user` an agent pushes wherever its user can on the session's writable repositories, including a protected branch the user is allowed to bypass. Lenny writes no branch rules, by the owner's decision 10, and the repository's own protection is the control.
- **Two-person review depends on the repository.** UR-4 holds for pushed work only where the repository requires approval of the most recent push. Without that rule, Alice's agent can push to Bob's pull request and Alice's approval counts, as it would for a push she made by hand. Under `pushIdentity: app`, UG-4 carries this instead.
- **The user's email addresses.** `GET /user/emails` answers for every child, whatever its permissions **[measured]** (S6), so an agent can read its user's addresses, private ones included, through the API child. Decision 46 asks whether the preset refuses it.
- **A cleared preset.** A tenant admin can clear a preset, audited and in posture, unless a `security-admin` requires it.
- **Future grants.** A tenant admin who controls configuration can trick a user, past the page, into granting a new token to a host she runs. `tokenTrust.allowedRecords` under a separate `security-admin` is the enforcing control. Tokens stored before her edit are unusable and sent nowhere.
- **`file` tokens on these hosts.** Under `allowRuntimeHeldCredentials: true`, a session holding that binding has a token that leaves every control here. A `registered` type can also lie about its hosts, which the pin addresses on the host the token works on.
- **Uncovered hosts.** Under T a token the agent holds reaches an uncovered git host in an opaque tunnel, and a push is not refused, as the egress design states.
- **A smuggled token** reaches an unpinned host through an opaque tunnel, until EG-12 lands, under `pinHosts: false`, or on an `internet` pool.
- **PR content.** An agent's pull request body or comment can carry anything, including text aimed at a human reviewer or at a reviewer's agent.
- **Parsers.** The rule under "What reaches the host" closes differences in how the client's bytes are read. It rests on gqlparser's printer emitting text the host reads as printed, which the corpus shows under graphql-ruby and graphql-js **[measured]** and nothing shows on a live host.
- **Preset-only limits.** Against a Developer-level GitLab project token, `gitlab-deny` is all that stops approval and merge, and its route families are unmeasured beyond approve, unapprove, merge, rebase, `reset_approvals`, and `cancel_merge_when_pipeline_succeeds`. Against a GitHub child, nothing at the host distinguishes creating a pull request from approving one.
- **Service identities.** A person holding a listed client secret can start App-authored work and then approve it. Lenny cannot distinguish that person from the system. The audit trail shows the subject, and UG-4 cannot name a person who is absent from the session.
- **GitLab under D.** Below 19.2 or under `prAuthoring: bot`, the MR author is a bot, and two-person review rests on an agent-editable commit author and a paid setting, so an injected agent defeats it, and Free has no such setting. UG-4 covers this where a tenant runs it.
- **GitLab quick actions.** `gitlab-deny`'s patterns are a deny list over free text. A command they do not name, or a spelling GitLab accepts and they miss, runs under whatever permissions GitLab applies (S13, S17).
- **Comment-driven and other automation.** A comment, PR body, label, or other allowed event acts with Alice's standing on any bot that reads it. If a tenant adds no rule, or its `bodyPatterns` miss a grammar, or its bot acts on an event no rule gates, a single injected agent can approve or merge through such a bot wherever it trusts her. Request rules cannot close this, and only the tenant's rules and the bot's own check of `performed_via_github_app` bear on it.
- **CI on pushed branches.** An agent's branch runs the repository's existing workflows or pipelines with the secrets they get for a same-repository branch, and its build scripts are agent-written. This allows secret exfiltration and anything those secrets can do, including a workflow token permitted to approve. It exists wherever pods can push, under App tokens too.
- **Connectors.** Under decision 3, a connector on a host with no `required` pool has no narrowing, and an injected agent can call whatever tools `connectorSelector` admits. A connector to an MCP server outside the provider's host set, which holds its own credential to the same repositories, is undetectable here under any option.
- **A second account as approver.** Identity binding covers the linked account. A person with a second account in the organization can approve from it unless the organization's sign-on rules prevent that.
- **`identityBinding: unverified`.** UP-9 does not hold, and a user can author under another account of their own.
- **Provider and client behaviour.** The presets and checks depend on behaviour listed under "Validation owed". A new `gh` or `glab` release passes rather than breaks, which is the reverse of revision 3's failure mode.
- **A lost refresh response** forces Alice to relink.
- **Identifier-only linking under UG-4** proves an identity at one time. An account later transferred at the provider is not noticed.

## Decisions

| # | Decision | Recommendation | Cost of the recommendation | Alternative |
| :-- | :-- | :-- | :-- | :-- |
| 1 | Separating the features | **Settled**, in the owner's word "ok". UG-1, UG-2, and UG-3 as separate proposals in that order | UR-4 waits for UG-2, and tenants see partial value in between. | One proposal, which ties low-risk authorization to the pod-exposed token. |
| 2 | §9.3 reuse | **Settled**, in the owner's word "ok". Reuse `pkg/connectoroauth`, with a new record in the Token Service | A second table and flow beside §9.3, and new routes. | Rows in `connector_credentials`, which brings the MCP requirement, environment key, gateway decryption, and the callback defect. |
| 3 | Connectors on link hosts | **Settled by the owner.** Refuse a connector on a link provider's host set only when a pool on that host is `required`, since UR-2 is the property a connector skips, in both write orders: setting `required` suspends a connector that exists there. §9.3 connectors are written on the platform-admin route, outside EG-9, so UG-2 adds a suspended field to `connectorstore`, and the invoker refuses a suspended connector. `hostLink` stays withdrawn. | A connector's token on such a host reaches the provider with no narrowing, and an injected agent can call whatever tools `connectorSelector` admits, on pools that are not `required`. | Refuse on every token-holding link provider's hosts, as revision 3 did, which costs a tenant its GitHub MCP tools wherever a link exists. Or allow everywhere, which leaves `required` bypassable through a tool. |
| 4 | Link key and App | **Settled**, in the owner's word "yes", after leaning towards adding the App to the key. A link is keyed on tenant, user, and link provider name, and a tenant can have several link providers on one host. A `required` pool names its check's provider in `userAuthorization.linkProvider`, an `oauth2Link` type names its own, and a provider account belongs to one Lenny user across a host's providers. | A person links once per App, when an App first needs it, and pools and types name their provider. | One VCS link provider per host, which makes a tenant that wants both the check and authoring hold write power in every stored token. |
| 5 | Account sharing | **Settled**, in the owner's word "ok". Refuse two Lenny users on one provider account within a tenant | A team with a shared account cannot link it, and a first linker holds it until removed. | Allow it, which makes attribution false and gives a route around two-person review. |
| 6 | Refresh | **Settled**, in the owner's word "ok". One refresh at a time per link through a row lease, parent in the Token Service, on demand | A lost response forces a relink, and a waiting replica can answer `unavailable`. | A long transaction with a lock, which holds a pooled connection across a provider call. Neither provider offers an idempotent refresh to avoid this. |
| 7 | Scoped tokens | **Settled**, in the owner's word "ok", after S2 found no per-user limit: leave limits to GitHub. Each session mints its own children, an API child and, under `pushIdentity: user`, a git child, and revokes them at session end (S5). Lenny sets no per-person or per-link budget, a GitHub refusal is the result, and the per-App counter and alert near 2,000 per hour stay. | Each session costs one or two token requests against the App's 2,000 per hour, and a large tenant can reach that limit, whose behaviour S2 did not test. GHES is untested. | A child shared across a user's sessions with the same repositories under an 8-per-hour budget, which guarded against a ten-token rule that S2 showed does not apply. Or a user-wide child, which fails finding 1. |
| 8 | Request rules | **Settled by the owner (D1), revision 4.** Generic rules as data, with the GitHub deny preset on by default, replacing the allowlist. Strict decoding, parsed GraphQL, and canonical forwarding stay. | A new mutation with approve or merge power passes until the preset is updated, and input-key and target checks are gone. A parser dependency (0.54 MB of binary **[captured]**) and buffering of inspected bodies stay. | The allowlist, which failed both of the owner's conditions: 17 of 42 `gh` cases failed under it **[captured]**, and 50 of 66 current mutations were off it **[source]**. |
| 9 | Comment, edit, ready, and close | **Settled** through decision 8, which supersedes it. Superseded in revision 4 by decision 8. All pass the rules, on the session's repositories under a template. Reviews, merges, auto-merge, and branch updates are refused by name. | An injected agent can edit, retarget, close, and comment on Bob's pull request, and a comment acts on any bot that trusts Alice. | Revision 3's allowlist, which held `baseRefName` and `state` back, at the cost in decision 8. |
| 10 | Push identity | **Settled**, in the owner's word "ok", after S3 showed that a user's or child's push makes GitHub treat the user as pusher: `pushIdentity: user` by default, through a git child with `contents: write` alone that `netagent` attaches only to smart HTTP paths, and no branch rules from Lenny. `pushIdentity: app` remains and requires UG-4. | `netagent` holds a person's `contents: write` next to `pull_requests: write`, so a compromised `netagent` can merge as the user. An agent pushes wherever its user can. Two-person review on pushed work rests on the repository requiring approval of the most recent push. | App pushes with a per-user branch namespace, which tells users which branches their agents can push to, against the owner's direction, and leaves GitHub's rule seeing the App. |
| 11 | Commit author | **Settled**, in the owner's word "ok". The linked user as author, and the pool's identity as committer | A user's address appears in history for work an agent did, and it is agent-editable. | The pool's identity alone, which is simple and loses GitLab's committer rule. |
| 12 | Recheck interval | 300 s, with `cacheUntil` of 300 s and a 60-s verdict cache | A 6-minute window, and provider calls per link and repository. | Webhooks, which shorten it and require a public ingress and organization-level configuration. Or 60 s, at five times the calls. |
| 13 | After access is lost | `deny`, with an event, and `terminate` as a tenant choice | An agent keeps the content it already has until the session ends. | `terminate` by default, which loses a user's work on a transient provider error that survives classification. |
| 14 | Pod models | Refuse concurrent, service-mode, recycled, and `always` | These tenants pay for one session per pod on affected repositories, and service-mode runtimes have no user identity. | Allow recycling with `restart`, which still rests on a scrub the specification calls no security boundary. |
| 15 | Mirror outage | New sessions, resumes, and mints fail closed, and running reads continue for 15 minutes | A host outage stops new sessions on `required` pools despite the mirror, which withdraws the 6-hour promise for them. | Honour 6 hours, which gives a revoked user 6 hours of reads during an outage. |
| 16 | `file` delivery | Changed in revision 4. Not used by `prAuthoring`. Available generically under `user-credential-delivery-design.md`, behind `userCredentialDelivery` and a binding opt-in, and refused on a `required` pool unless flagged. | A person's token reaches a prompt-injected process on any pool that opts in, and every limit that document lists applies. | Dropped, as revision 3 had it, which leaves the cases injection cannot serve with no mechanism, against the owner's direction. |
| 17 | Attribution | `tokenSource` and an erasable `linkRef` on `credential.mint`, with logins in `user_links` and the audit log | A reader joins through a row that erasure removes, so old records stop naming a person, which is the intent. | The login on the record, which changes the §12.8 basis for every stream. Or a new stream, which adds volume for one field. |
| 18 | Detached orphans | Keep authorization, swept, and keep a user token only when the delegation lease named it, until `cascadeTimeoutSeconds` | A child holding one can open a pull request as Alice up to an hour after her session ended. | Refuse user tokens once an ancestor is terminal, which breaks the purpose of `detach` for these tenants. |
| 19 | Impersonation | Refuse session input on affected sessions at the session input layer, and memory writes on every session | An operator cannot reproduce a support case by messaging the user's session or writing her memory, and must start one under its own identity. | Taint the session and stop its user tokens for the rest of its life, which leaves the session running and adds state. |
| 20 | Service identities | A pool-level list of subjects, with `caller_type: service`, admin-written and guardrail-limited. Other callers that are not `human` are refused on such a pool. | Each automation is registered per pool. A person holding a listed secret stays undetectable. | Trust `caller_type` alone, which lets a mapping error or a person skip the check. |
| 21 | Disables | Stop use and revoke at the provider at every level | A mistaken `soft_disable` forces relinking after reinstate. | Revoke only at `full_revoke`, which leaves a live grant for a disabled person. |
| 22 | Undeclared repositories | Declared-only under a `required` pool, with a new `readableRepositories` field | An agent cannot fetch a repository it discovers mid-session, and a plan must list dependencies. | On-demand checks through I8 at first access, which adds a mid-session store write, a binding change per repository, and a hot path. |
| 23 | Sessions across owners | Refuse the second owner on the binding | A session cannot author pull requests in two organizations. | One child per target, with `netagent` selecting by request path, which adds path parsing to the attach step. |
| 24 | Read-only repositories on the child | Absent, per `security-oversight-design.md` decision 25's reasoning | `gh pr view` on a read-only declared repository gets 404 from GitHub. | A second read-only child selected by path, at the cost that decision names. |
| 25 | Preset on App-sourced bindings | Resolve the flavour's preset at I8 on any binding on the pool's API host, including service sessions | Those App bindings lose approval and merge through the agent, and a tenant workflow relying on that clears the preset. | Leave App bindings without it, which lets an agent approve any human's pull request. |
| 26 | GitLab | E on 19.2 and later, per-session fine-grained tokens, with S13 selecting the parent. D below 19.2, under `prAuthoring: bot`, and if S13 finds no programmatic path. F is withdrawn. Authorization ships regardless. `prAuthoring` never selects A. | UG-5 waits on S13, and depending on it the Token Service holds `api` parents, or users take a manual step. Each session mints and revokes a token, and S13 decides whether each one notifies the user. Older instances keep the bot author. A tenant can still build A itself under the generic mechanism. | D for everyone, which gives up user attribution, half of the request. B, which breaks `glab`. F, which three texts refuse. |
| 27 | Status check | An option (UG-4) for tenants refusing user tokens, and required for a widened namespace | A webhook ingress, App permissions, repository configuration by the tenant, and merges blocked during a Lenny outage. | The default for everyone, which removes user tokens from pods and leaves the App as author everywhere, against the request. |
| 28 | Sweep owner | A gateway background job calling the Token Service | The gateway needs a new job with per-tenant sharding, and it writes each session's entry `verifiedAt` values each interval. | A Token Service worker, which needs a new route to gateway replicas and a write to the session row from a component that reads it today. |
| 29 | Parser differences | Forward a document and body `netagent` generated from the checked structure, with the lexical rule beside it, for each inspected request | A printing step, a second parse, and a body that differs in bytes from what the client sent, in 6 of 159 corpus documents **[measured]**. A printer defect becomes a refusal or a wrong request, which the re-parse narrows. | Forward the client's bytes after checking, which is the design that the bare CR bypassed **[captured]**. Or identify the host's parser, which cannot be done from outside and can change. |
| 30 | Bounds | 512 KiB body, 64 KiB document, 4,096 tokens, depth 32, 10 s head, and 4 concurrent checks per pod | A `gh api graphql` query past these fails with a named bound. Concurrent large writes wait or get 429. | The prototype's 256 KiB body, which refuses a legal PR body. Or no token limit, which measured 74.9 MiB against a 64 Mi example request **[captured]**. |
| 31 | Quick actions | `gitlab-deny`'s `bodyPatterns` refuse lines naming commands that reach review or merge state, whatever the context. A tenant's rules can tighten them. | It is a deny list that fails open on a new command, and a literal `/merge` line in a code block is refused. | The prototype's refusal of every `/word` line, which rejects `/usr/bin/env` **[captured]**. Or no check, relying on the token, which S13 has not shown to bind quick actions. |
| 32 | Query root fields | Admit any query, within the bounds | A query can read whatever the child reads, under any root field. | A root list, as the prototype had, which breaks `gh api graphql` on roots such as `rateLimit` and adds no write protection. |
| 33 | Flavours | Superseded in revision 4. With a deny list there is no per-flavour mutation list to keep, and prefixes come from the preset's flavour, which the VCS pool's provider and host select. | A preset names mutations on both flavours whether or not a release has them. | A list per flavour, which only the allowlist needed. |
| 34 | Review comments | `gh pr review --comment` stays refused by default, because the preset names `addPullRequestReview` | An agent loses inline review comments, and `gh pr comment` is the substitute. A tenant can change that by dropping the name and accepting approval with it, since rules no longer read the event. | Have the preset read `event: COMMENT`, which needs an input check of the kind D1 removed. |
| 35 | Required statement on automation | **Settled by the owner.** Retire `repositoryAutomation` with the allowlist's label and comment checks. The setup flow and posture prompt a tenant about bots, with `bodyPatterns` and `denyMutations` as the tools. | A tenant that says nothing gets comments on, with no required answer in the API. | Keep it required when `prAuthoring` is written, setting rules from the answer. |
| 36 | Git remotes and client tools | **Settled by the owner: T with the bundle on VCS-covered hosts.** Applied in egress revision 47. T is the only candidate where `gh`, `glab`, go-git, JGit, and proxied libgit2 all work, and its enforcement on covered hosts holds with the config removed and for a held token **[measured]**. | About 35% more `netagent` CPU on git, public roots in git's trust, CA coverage before a warm fill, a libgit2 hang on a constraint mismatch, and no enforcement on uncovered hosts, where an agent's token reaches the host. | P, which gives `gh` and `glab` the same results, holds enforcement only with a second CONNECT rule that needs the CA anyway, and fails go-git, JGit, and libgit2. L, which leaves `gh` stopped. `GH_REPO`, a wrapper, and the others listed, none of which worked unchanged. |
| 37 | Identity binding | A required `identityBinding` on each link provider, with `directory` or `claim` recommended and `unverified` reported in posture | A tenant runs a mapping or an issuer claim, and a user cannot link until it exists. | No binding, which lets a user author under a second account and approve as themselves. A fixed email match, which needs an authoritative IdP email everywhere. |
| 38 | Push options | Refuse every push option on every covered host, with a per-pool allowlist | An agent or tool using `-o ci.skip` or `merge_request.*` breaks, and `netagent` parses further into the push. | Refuse `merge_request.*` alone, which leaves `secret_push_protection.skip_all` and `ci.skip`. Or read options per provider, which adds provider knowledge to the git path. |
| 39 | Labels under stated automation | Retired with `repositoryAutomation` under decision 35. Labels pass by default, and a tenant can deny the mutation. | Kodiak and label-triggered workflows act on an agent's label, wherever a tenant has not added a rule. | Revision 3's refusal of label keys, which needs an input check of the kind D1 removed. |
| 40 | Hosts of a `required` pool | **Settled by the owner.** Pin the git host and API hosts for every session of the tenant, unfiltered by environment, whenever a `required` pool covers that host, once EG-12 lands, with `pinHosts: false` as a recorded opt-out, and a stated residual until then | An API host with no binding is refused, so a `required` pool without `prAuthoring` loses unauthenticated API reads unless it adds a read-only App binding. A pin on `github.com` with no binding also refuses public release and archive downloads for every session of the tenant. Other pools on that host are pinned too, and `internet` pools stay outside. EG-12 becomes a dependency for this guarantee. | Pin only that pool's sessions, which leaves a session on another pool holding a token that reaches these repositories. A stated residual alone, which leaves `required` bypassable by anyone who can place a token through `inlineFile`, `env`, or a message. |
| 41 | Tokens beside a `required` pool | By host: refuse a stored secret outright, and, as the owner adopted, a `file` binding whose type hosts include a covered host unless every pool covering it sets `allowRuntimeHeldCredentials`, whatever its mode. The attach rule applies to a session naming that binding, whatever its state, and the pin exception needs a release. Report it in posture. | The flag removes most of what `required` promised, for sessions holding the binding. A session whose binding has no current value gets a 401 on git. A `registered` type can lie, and the pin is the enforcing control. | No flag, which excludes GitHub from the generic mechanism. Or allowed on an `off` pool, where the push silently acts as the App. |
| 42 | The child's permissions | **Settled by the owner.** `permissions` is template data a tenant can widen, defaulting to `pull_requests: write` and `contents: read` | At the default, `gh issue`, `gh run`, and more are refused by the host, so "all `gh` commands", the owner's condition, holds only within the token. Once widened to `contents: write`, merges and content writes rest on the deny preset alone, with no host check behind it. | Fix the default wider, which makes `gh` work out of the box and removes the host's limit by default. Or fix permissions as revision 3 did, which keeps the limit and fails the condition. |
| 43 | A same-host App binding beside the attach | Replace it per session: a human session takes the user binding, and a service session takes the App binding | Two bindings exist on one host at the tenant level, and a reader of a session's list sees the effective one. | Refuse at write, which leaves service sessions on that host with no binding. |
| 44 | `prAuthoring: user` without `required` | Refused | A tenant wanting user authorship with no read limits on its repositories cannot have it, and it takes the declared-only rule too. | Define the unchecked case, which needs its own namespace, error, and residuals for a token with no check. |
| 45 | Shared App across tenants | Removed. A link provider's client belongs to one tenant. | An operator cannot offer one App to many tenants, and each tenant registers its own. | A platform-only write with fixed endpoints, which is a second write path and a record no tenant admin can touch. |
| 46 | The user's email addresses | Add `GET /user/emails` and `GET /user/public_emails` to `github-deny`, which a tenant can drop. Found by S6 in revision 25. | A tool or script that reads the user's addresses through the API fails inside a session. | Leave it, which lets an agent read its user's private addresses. |

## Review of revision 4

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

## Review of revision 9

A final regression review and a further run read revision 9. Minor 9, the warm-up asset's `restricted` exception, holds, and "Other tokens" has it. N3 holds, and the plan rejection is stated. The 401 states moved to **[measured]** on stand-ins, and the stub proves forwarding only. None was rejected.

## Review of revision 8

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

## Review of revision 7

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

## Review of revision 6

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

## Review of revision 5

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

## Sources

Read on 2026-11-02 and 2026-11-03. These pages were read and their behaviour was not tested.

- GitHub: [authenticating on behalf of a user](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-with-a-github-app-on-behalf-of-a-user), [refreshing user access tokens](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/refreshing-user-access-tokens), [apps REST, scoped token](https://docs.github.com/en/rest/apps/apps), [the same on GHES 3.22](https://docs.github.com/en/enterprise-server@latest/rest/apps/apps), [OAuth application REST](https://docs.github.com/en/enterprise-server@latest/rest/apps/oauth-applications), [installations REST](https://docs.github.com/en/rest/apps/installations), [permissions required for GitHub Apps](https://docs.github.com/en/rest/authentication/permissions-required-for-github-apps), [REST rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api), [authorizing OAuth apps](https://docs.github.com/en/apps/oauth-apps/using-oauth-apps/authorizing-oauth-apps), [protected branches](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches), [branch protection REST](https://docs.github.com/en/rest/branches/branch-protection), [repositories REST](https://docs.github.com/en/rest/repos/repos), [reviewing Copilot pull requests](https://docs.github.com/en/copilot/how-tos/use-copilot-agents/coding-agent/review-copilot-prs), [issue comments REST](https://docs.github.com/en/rest/issues/comments), [emails REST](https://docs.github.com/en/rest/users/emails), [events that trigger workflows](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows), [the Dependabot comment command change](https://github.blog/changelog/2026-01-27-changes-to-github-dependabot-pull-request-comment-commands/), and [available rules for rulesets](https://docs.github.com/en/enterprise-cloud@latest/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets).
- GitLab: [OAuth 2.0 API](https://docs.gitlab.com/api/oauth2/), [OAuth provider and scopes](https://docs.gitlab.com/integration/oauth_provider/), [fine-grained tokens post](https://about.gitlab.com/blog/fine-grained-pats/), [fine-grained tokens](https://docs.gitlab.com/auth/tokens/fine_grained_access_tokens/), [fine-grained REST permissions](https://docs.gitlab.com/auth/tokens/fine_grained_access_tokens_rest/), [user tokens API](https://docs.gitlab.com/api/user_tokens/), [push options](https://docs.gitlab.com/topics/git/commit/#push-options-for-merge-requests), [approval settings](https://docs.gitlab.com/user/project/merge_requests/approvals/settings/), [notes API](https://docs.gitlab.com/api/notes/), and [quick actions](https://docs.gitlab.com/user/project/quick_actions/).
- Coder: [external authentication](https://coder.com/docs/admin/external-auth.md). Kodiak: [config reference](https://kodiakhq.com/docs/config-reference), read through search results.
- Capture: `evidence/user-git-identity-design/spike-gh/RESULTS.md`, with `raw/`, of `gh` 2.103.3 (cli/cli tag v2.103.3) and `glab` 1.127.0, gqlparser 2.5.33, graphql-go, graphql-js 17.0.2, graphql-ruby 2.6.14, and `graphql-c_parser` 1.1.4. The folder's `README.md` lists what was left out, which includes binaries, downloads, keys, and bulk logs. Citations in this document such as `raw/gitopt/t11/` are relative to `spike-gh/`, and `t4/table.txt` is at `raw/gitopt/t4/table.txt`.
- `gh` release history: `evidence/user-git-identity-design/gh-history/out/`, read from cli/cli source across 68 releases and from GitHub's public schema. The schema counts under "Why the allowlist was replaced" are in `gh-history/out/schema-history.txt`, from `gh-history/scripts/schema2.py`, which reads octokit/graphql-schema history and `docs.github.com/public/fpt/schema.docs.graphql` as fetched on 2026-11-03. Those sources are not copied here. The "about one a year" figure is a reading of that list by name and is not a measurement.
- Reproduction under "Review of revision 2": the scripts are in `evidence/user-git-identity-design/review-repro/`, and their output was not kept.
- Client source for revision 1: [cli/cli](https://github.com/cli/cli) at `c4513d446bf98cfcbe72ed83f17cd3220174d88f`, in `api/queries_pr.go`, `api/queries_pr_review.go`, `api/queries_user.go`, `pkg/cmd/pr/create/create.go`, `pkg/cmd/pr/merge/http.go`, `pkg/cmd/api/http.go`, `internal/featuredetection/feature_detection.go`, and `internal/ghinstance/host.go`. [gitlab-org/cli](https://gitlab.com/gitlab-org/cli) on `main`, in `internal/commands/mr/create/mr_create.go` and `internal/commands/mr/approve/mr_approve.go`.
