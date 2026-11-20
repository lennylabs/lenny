# Review history: 0129-github-credential-addon GitHub credential add-on

# Revision notes

- Revision 1, 2026-11-06, took its content from [`0152-user-git-identity`](../0152-user-git-identity/change.md) revision 27 and [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) revision 18.
- Revision 2 followed a pass for a reader new to the project. It added a tag table and an evidence note, "Terms" ahead of "High-level design" with GitHub terms, "Terms from other designs and the platform", and "Labels from other designs", a plain-language opening and a diagram in "High-level design", links to other designs and to the labels they define, and interface names in place of the egress design's interface numbers, and changed no rule.
- Revision 3 corrected the status line, which omitted that decisions 2, 3, 4, and 6 await owner review, and removed the command counts and the version drift result that GH-3 stated twice, that a validation pass found, and changed no rule.
- Revision 4 updated the "Covered host and git paths T and P" entry in "Terms from other designs and the platform" after a re-read of `0151-user-credential-delivery` and `0152-user-git-identity` at their current revisions, and changed no rule. A covered host is now a git host of a VCS pool's repository prefixes or of a git route, with a VCS kind's API hosts named separately, as `0152-user-git-identity` revision 29 corrected it.
- Revision 5, dated 2026-11-09, applies the design sync review of 2026-11-08, and changes no rule. The glosses of `netagent`, the covered host, and the egress decision API's callers in "Terms from other designs and the platform" now match the egress design's "Terms": `netagent` sits in every pod that uses the local egress endpoint and runs the git path, a covered host includes a provider's alias hosts and is read across the tenant's environments, and the decision API's callers include the git mirror and the gateway at level 1.
- Revision 6, dated 2026-11-09, applies the owner's decision N1 of that date, settled in the owner's word "ok", under which the combined missing-links response at session creation always carries `403 LINKS_REQUIRED`. Step 1 of the worked example names that code and `details.userLinks` in place of `USER_LINK_REQUIRED`. This design's own rules are unchanged.
- Revision 7, dated 2026-11-13, applies the cross-design review of that day and changes no rule of this add-on. The "Workspace plan, adapter, and materialization helper" term and step 4 of the worked example state that `lenny-materialize` clones as the slot's UID under a fetch grant and that the adapter runs no git on a session's repositories, as [`0153-workspace-materialization`](../0153-workspace-materialization/change.md) states. The `netagent` term states per-slot scopes under the egress design's decision 60, settled on 2026-11-13. The worked example names the renamed pool field `sessionPolicy.maxConcurrentSessionsPerPod`. "Documentation" is new and lists every setting, default, provider requirement, limit, failure mode, and runtime-facing rule, under the owner's extension of decision D13 to every design. The fix pass of the same day updated the revisions this design reads against, `0151-user-credential-delivery` revision 29 and `0127-egress-and-git` revision 77, and changed no rule. The check pass of the same review, which changes no rule, names the data class of the revocation record Secret in place of T4, and adds the failure codes `USER_TOKEN_MULTI_OWNER`, `USER_TOKEN_RATE_LIMITED`, and `LINK_TOKEN_NON_EXPIRING` and the 503 answers to "Documentation". It also updates the revisions this design reads against, `0151-user-credential-delivery` revision 30 and `0127-egress-and-git` revision 78.
  - The last round of the same review, which changes no rule, cites the designs this one reads against at their current revisions.
  - The owner's answers of 2026-11-13 to the consolidated list of open items change no rule of this add-on. `0151-user-credential-delivery` decision 41, settled that day, admits a `userCredential` binding on a pool with several slots whose attribution status records per-slot attribution, so the worked example no longer states that UCP-2 requires one session per pod, and the status line cites the designs at their current revisions.

## Move into changes (2026-11-16)

Change 0113 moved this design from `designs/github-credential-addon-design.md` into this folder. Its review history moved into this file, its proposal plan became "Steps", its evidence moved into `evidence/`, its status became `Draft` pending the owner's final cross-review, and every path to a design or its evidence, in links and in prose, now names its new location.

## Capture history

This section holds the results of the capture of 2026-11-03 against revision 3's allowlist, and that allowlist's measured behaviour. The measurements stand, and the design text above cites them. Revision 4 replaced the allowlist with request rules and presets, so nothing in this section is a requirement.

### Capture results

**This section is history.** It records what was measured against revision 3's allowlist, and the measurements stand. Revision 4 replaced that allowlist with the preset model. Where a row below names the filter, it describes that filter. Deny mode's bypass analysis ran later on the stub, under S19 **[measured]**.

A capture ran on 2026-11-03: `gh` 2.103.3 and `glab` 1.127.0, against a recording stub on a Docker `--internal` network, through a Go `netagent` prototype with `filter.go` and gqlparser 2.5.33, on a laptop under other load. Results and raw output are under `evidence/user-git-identity-design/spike-gh/`, in `RESULTS.md` and `raw/`. The github.com flavour used `api.github.com` as a network alias for the stub.

#### What moved from design to measured

| Revision 1 claim | Revision 2 status |
| :-- | :-- |
| `gh` sends `pr create`, `pr review`, and `pr merge` over GraphQL | **[captured]**. A REST allowlist does not cover `gh`. |
| `gh pr create` works through the filter with a placeholder | **[captured]**, **with plain remotes only**. 10 of 10 variants per flavour, with the stub receiving the same requests with and without the filter, carrying the injected token and no placeholder. |
| The filter refuses review, merge, aliasing, batches, fragments, encodings, and path tricks | **[captured]**. 522 attack rows per flavour under the final policy: 52 allowed controls reached the stub, 467 refused rows sent nothing to the stub, and 3 opaque-route rows carried no credential. 24 of 25 attack commands run with the `gh` binary exited non-zero with the refusal, and the 25th is the allowed control. |
| HTTP/2, HTTP/1.1 framing, absolute-form, and another bound host do not bypass the filter | **[captured]**, by the rows above. |
| A filter check is cheap on `gh` traffic | **[captured]**. 11 µs on `pr create` and 88 µs on the largest document. `gh pr create` wall time differed inside its run-to-run spread. |
| `glab` uses REST for its merge request commands, with its token in `Private-Token` | **[captured]** for `mr create`, `mr view`, `mr list`, `mr approve`, and `mr merge`. Other `glab` commands were not captured. |

#### Corrections to revision 1

- **Flavours differ.** `gh` selects login-based mutations on github.com and node-ID ones on GHES **[source]**, and an allowlist from one capture breaks `--reviewer` and `--assignee` on the other **[captured]**.
- **Rule 6 of revision 1 would have broken `gh`.** `gh pr create --project` sends aliased root fields with `$input_000` variables (`add_000: addProjectV2ItemById(input: $input_000)`) **[captured]**. Several root fields, each checked, are admitted.
- **`gh pr create --project` uses `addProjectV2ItemById`**, where revision 1 named another field.
- **Revision 1's bounds were wrong.** Its 256 KiB document bound admitted a document that measured 40 MB, and streaming REST bodies cannot carry the body-key check `merge_when_pipeline_succeeds` needs. See "Bounds and cost".
- **The first filter had a bypass, and a second stays open in the prototype.** See `0151-user-credential-delivery`, "Canonical forwarding".

#### Why the allowlist was replaced

The owner's conditions for keeping a GitHub-specific injection were that it survive `gh` updates and work for all `gh` commands. Both were measured false for the allowlist.

- **All commands.** 25 of 42 `gh` cases exited 0 under the prototype's policy and 17 failed, including `pr comment`, `pr edit`, `pr ready`, and `pr close` by policy **[captured]**.
- **Headers.** On a Debian image `gh` sent `Time-Zone`, and the forwarded-header allowlist refused every GraphQL request with 403 **[captured]**. The earlier capture ran on Alpine only.
- **Updates.** Of 68 `gh` releases since 2024-10-01, 4 changed the mutation set. v2.73.0 added `replaceActorsForAssignable` and `deleteIssueComment`, v2.83.0 added `revertPullRequest`, v2.87.0 added `requestReviewsByLogin`, and v2.94.0 added the discussions and sub-issue set. 16 of the 66 mutations in `gh` 2.103.3 were on the old table and 50 were off **[source]**. Those are names only and a lower bound on drift, read from source and not run.
- **The server side.** GitHub's schema went from 122 mutations (2021-01) to 278 (now), and in one reading about one a year needs only `pull_requests: write` and carries merge or review power **[source]**. That reading is what the owner accepted as the deny preset's cost.

#### What the capture did not test

It used plain git remotes. Under the prior `insteadOf` config, `gh` and `glab` stopped earlier, which was reproduced afterwards (see `0152-user-git-identity`, "Git remotes and client tools"). It also ran no `git push`.

#### What the capture cannot show

These stay **[stub cannot show]** and are spike S15 unless noted:

- Whether `pull_requests: write` permits approval on a provider-issued token. The permission table says yes **[documented]**, which is why the preset names approval.
- Whether merging fails without `contents: write`. The table says it needs that permission for REST **[documented]**. The GraphQL mutations' checks are unknown.
- Whether a child's repository limit binds node-ID mutations, since `createPullRequest` takes an opaque `repositoryId` and others take node IDs. The filter checks operations and leaves targets to the host.
- Whether GitHub honours batches, GET queries, form bodies, or method overrides, and which GraphQL parser it runs.
- What GitLab does with `/merge` in a description for a given token, and how it routes an encoded project path (S17).
- `gh`'s behaviour against an older GHES, since the stub answers `__type` as if every field exists, which selects `gh`'s newest code paths.
- Whether any field fails under a narrow child on `gh pr view`, because the stub answers everything.

### The revision 3 filter

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

## Status line before the split (2026-11-16)

Change 0113 shortened the status line of `change.md` to the status, revision, date, and program, and moved the rest here as it stood:

> Nothing in this document is built or staged. Revision 7 applies the cross-design review of 2026-11-13: it restates the clone and the pod terms for the materialization helper, per-slot `netagent` scopes, and the renamed pool field, and adds "Documentation" under the owner's extension of decision D13 of [`0131-incremental-checkpoint`](../0131-incremental-checkpoint/decisions.md#decisions) to every design, and changes no rule of this add-on. It reads against [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) revision 40, [`0152-user-git-identity`](../0152-user-git-identity/change.md) revision 45, and [`0127-egress-and-git`](../0127-egress-and-git/change.md) revision 92. The owner has settled [decisions](decisions.md#decisions) 1 (per-session child tokens), 5 (the child's permissions), and 8 (the preset body). Decision 7 (a per-person limit on child mints) is open, and decisions 2, 3, 4, and 6 carry a recommendation for owner review. The live GitHub spikes S1, S2, S3, S5, S6, S15, and S16 ran on `github.com` on 2026-11-06, and GitHub Enterprise Server (GHES) is untested. Every other measurement ran against a recording stub, and **the stub proves forwarding only**. Statements about GitHub and GHES come from the pages under "Sources". Proposals carry the label GH-n. "Review history" and "Capture history" at the end record how the design reached this form. The owner's final cross-review of the moved designs is pending, so the status is `Draft`.

## M3 readiness review (2026-11-19)

- 2026-11-19, review, M3 readiness review: revision 8, from the review of `change.md` revision 7 (findings 0129-F1 to 0129-F17, with no High finding and so no skeptic's verdict) and the conflicts credentials-git-C1, C2, C8, C9, and C13 of the credentials-git consistency report. The program report names 0129 only in its layer and milestone tables. Applied 19, raised as open decisions 2 (decision 9 new, and decision 2 extended), returned as issues 0, dropped 1. The status stays `Draft`.
  - Applied 0129-F1: decisions 2, 3, 4, 6, and 7, which have no owner answer, move from "Decisions" to a new "Open decisions" section of `decisions.md`, one list item each, so the board counts them. Decisions 1, 5, and 8 stay under "Decisions". The links from `change.md` to decisions 2 and 6 point to "Open decisions".
  - Applied 0129-F2 and credentials-git-C8 (this change's side): **On 401.** states that the `reauth-on-401` request names the hash of the refused token, that the Token Service evicts the cache entry only on a matching hash and at most once per child, then mints and revokes the evicted child, that a mismatched hash receives the current entry, and that `credential.mint` records the eviction. GHR-11, `integration` rows, and a `security` row cover it. The request format is 0151's and the pod credential API is 0127's, returned as edits needed elsewhere.
  - Applied 0129-F3 and credentials-git-C9 (this change's side): **Cache.** adds one mint at a time per cache key across replicas and states that a changed granted set, a parent refresh, and a `reauth-on-401` eviction each supersede the earlier child, which is revoked. **Revocation.** writes each child to the durable revocation record at mint and before release, cites the record of 0151 "Lifecycle and revocation at the provider", keeps the 8-hour bound for a GitHub child, and names 0129.S2 as the step that writes the entries. The cache key is unchanged. GHR-10, `integration`, `load_local`, and `chaos` rows cover it.
  - Applied 0129-F5 and credentials-git-C13 (this change's side): `steps.md` gains spike S2b, which measures which GitHub budget a scoped mint and a refresh consume and whether an installation token mint counts toward the 2,000 per hour, and 0129.S2 waits on it. **Limits.** states one counter per App identifier that counts installation token mints, child mints, and refreshes, names the metric `lenny_token_service_app_token_requests_total` and the alert `TokenServiceAppTokenRequestsHigh`, and states that decision 7 is settled against S2b. Decision 7 stays open.
  - Applied 0129-F6 and credentials-git-C1 (this change's side): the request layout registration (flavour, API host, `apiBase`, `restPrefix`, `graphqlPaths`, uploads, and `attach`) lands in 0129.S1, the preset body stays in 0129.S3, 0129.S3 depends on 0129.S1, and 0129.S2 depends on 0129.S3. "Request layout" states the order and its reason. `changes.py check` reports no problem.
  - Applied 0129-F7: GH-3 gains "Keeping `github-deny` current", the CI job 0151 "Presets" assigns to each add-on, with its inputs (GitHub's public GraphQL schema and REST OpenAPI description), the preset's families, a daily schedule, and review of the preset change it opens. 0129.S3 adds the job, and GHR-14 states it.
  - Applied 0129-F8: `change.md` gains "Requirements" (GHR-1 to GHR-14, each with its page among `ADDON`, `PRAUTH`, and `TOKSVC`) and "Specification and non-specification changes" (the three pages, the error, metric, alert, and settings catalogs, `pkg/tokenservice`, `cmd/lenny-token-service`, `netagent` of 0127.S2, the chart's preset data and Helm value, and the CI job).
  - Applied 0129-F9: "Tests" uses the tier names `unit`, `contract`, `integration`, `load_local`, `chaos`, `e2e_cloud`, `security`, and `docs` in place of the former numbers, adds the failure paths the review listed (`sso_required`, `LINK_TOKEN_NON_EXPIRING`, the `allowedAccounts` refusal, `USER_TOKEN_MULTI_OWNER`, `USER_TOKEN_RATE_LIMITED` and the 503, `reauth-on-401` ending in a 503 and an alert, the idle refresh token lapsing to `user_act_required`, and a kind and host mismatch at write), concurrent mints and the counter across replicas in `load_local`, revocation after a restart in `chaos`, and names the deny-mode fixture of 1,510 rows per host type as the `security` input. GHR-1 states that a kind and host mismatch is refused.
  - Applied 0129-F10: the evidence paths in "Sources" and in "Validation owed" point to `../0152-user-git-identity/evidence/spike-gh/`, `gh-history/out/`, and `spike-ghlive/`. The history sections of this file keep the paths they were written with.
  - Applied 0129-F11: "Documentation" names `docs/operator-guide/configuration.md` for the Helm value, `docs/reference/metrics.md` and `docs/reference/error-catalog.md`, the alert `TokenServiceAppTokenRequestsHigh`, and its runbook `docs/runbooks/token-service-app-token-requests-high.md`. The paths of 0152's tenant admin and runtime author pages are returned as an edit needed elsewhere.
  - Applied 0129-F12: **No fallback to the App.** states that the Token Service refuses an App write token mint for a `human` session on a VCS pool with `pushIdentity: user` and that `netagent` attaches none. GHR-8 and a `security` row cover it.
  - Applied 0129-F13: the `ghe.com` row of "Request layout" registers no uploads path until the new spike S20 measures the host `upload_url` names, and the `github.com` row states that a binding on `uploads.github.com` takes the `github` flavour and resolves to `github-deny` with no `preset` key.
  - Applied 0129-F15: "The repository check on GitHub" states that the check's `GET /repos/{owner}/{repo}` response supplies `id` and `owner.id` for `repository_ids` and `target_id`, and that a mint is refused when the latest check reads an `id` or `owner.id` other than the one an earlier mint of the session used.
  - Applied 0129-F16: GH-1 and 0129.S1 state that the `sso_required` classification lands as designed and that spike S4 revises it if a SAML-enforced organization behaves otherwise. A spike is not a step, so no `(part)` edge expresses the wait.
  - Applied 0129-F17: **Mint endpoints.** states that the kind declares the exchange, refresh, revoke, and scoped token endpoints for whatever host its block names, which the egress design's mint path carries through `lenny-egress` for every link provider endpoint.
  - Raised decision 2, extended (0129-F4): the entry adds option (c), one API child for the base owner and one git child per owner, states that option (a) breaks fork pushes under 0127's `writableRepositories`, and lists what a refusal leaves unsettled (which owner is first, when the refusal happens, the response, and a mid-session grant). The change's recommendation (a) and the review's recommendation (c) are both recorded. "Child token and the session's repositories" states that the rule is the open recommendation and that it breaks fork pushes. The other change is 0127.
  - Raised decision 9 (0129-F14): whether a `security-admin` guardrail bounds how far `narrowing.permissions` on `github-scoped` can be widened. Recommendation (a), a maximum permission set as an SO-2 guardrail row of 0142. "Residual risks" points to it. The other change is 0142.
  - Dropped credentials-git-C2: the conflict is between 0152 decision 10 and GitLab pushes in 0130. 0129 is cited only for "Pushes as the user" on GitHub, where a git child exists and the default `pushIdentity: user` works, and none of the conflict's options changes this change's text.
  - Second pass (2026-11-19): the requests of other changes' review agents. Of 4 requests, 3 were applied, 1 was already done, 0 were raised as decisions, and 0 were dropped. `changes.py check` reports no problem.
    - Applied, requested by 0127 (credentials-git-C13): **Limits.** cites the one counter of token requests per App identifier, with one alert threshold, that 0127 keeps under "Credentials", **Minting.**, and states that this add-on keeps no counter of its own and counts its child mints and refreshes on it. The metric, alert, and setting names stay in this change, because 0127 names none. GHR-12, the `TOKSVC` and `pkg/tokenservice` rows, and 0129.S2 follow, and 0129.S2 names 0127.S1 as the step that adds the counter, which it already reaches through its dependencies.
    - Applied, requested by 0151 (credentials-git-C9): **Revocation.**, GHR-10, the test row, the `pkg/tokenservice` row, and 0129.S2 cite the pending-revocation record that 0151 UC-2 defines, and this add-on sets a GitHub child's deadline at its 8-hour expiry. The C8 half, the child cache honouring the `reauth-on-401` eviction with a test, was already in **On 401.**, GHR-11, and the `integration` rows.
    - Applied, requested by 0152 (0152-F16): the alert `user_credential_rule_refusals` is renamed `UserCredentialRuleRefusals`, as 0152 names it. The C1 half of the request was already done.
    - Already done, requested by 0130 (credentials-git-C1): the request layout registration is in 0129.S1, `github-deny` stays in 0129.S3, 0129.S3 depends on 0129.S1, and 0129.S2 depends on 0129.S3, from the first pass.
