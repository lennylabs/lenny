# GitLab credential add-on

Status: design for owner review, revision 1, 2026-11-06. Nothing in this document is built or staged. This document took its content from `user-git-identity-design.md` revision 27 and `user-credential-delivery-design.md` revision 18, and it reads against `user-credential-delivery-design.md` revision 19 and `user-git-identity-design.md` revision 28. Decisions 1 and 2 carry recommendations for owner review. No live GitLab write, permission check, or quick action has run. The measurements cited ran against a recording stub, and **the stub proves forwarding only**. Statements about GitLab come from the pages under "Sources". Proposals carry the label GL-n. Claims carry the tags `user-git-identity-design.md` defines.

## Scope

This document is the GitLab add-on of the user credential mechanism. It implements the VCS layer of `user-git-identity-design.md` for GitLab.com, self-managed GitLab, and GitLab Dedicated: the `gitlab` link kind and its repository check (GL-1), user-authored merge requests (GL-2, formerly UG-5), and the `gitlab-deny` preset with GitLab's quick actions (GL-3).

Out of scope: the link flow, the binding, request rules, and the add-on model, which `user-credential-delivery-design.md` owns, and repository authorization, the attach rule, the host policy, push identity, and the approval check, which `user-git-identity-design.md` owns.

## High-level design

**The kind and the check (GL-1).** The add-on registers the `gitlab` kind. Each link provider names a tenant's own GitLab application, and the kind block holds the host. The kind fixes the OAuth endpoints, the API host, and `attach` from the host, and links with `read_api` and `read_user`, which grant no write. Its release check reads the project with the user's parent token and requires Developer access for a write.

**User-authored merge requests (GL-2).** On GitLab 19.2 and later, the target is a per-session fine-grained token minted from a parent the Token Service holds, so a merge request has the user as its author and no `api` token reaches a pod. Spike S13 decides which parent works. Below 19.2, on a VCS pool with `prAuthoring: bot`, and if S13 finds no programmatic path, merge requests keep a project token bot as author.

**The `gitlab-deny` preset and quick actions (GL-3).** The preset refuses approval, merge, content, policy, and credential routes, and its body patterns refuse lines that name GitLab quick actions with review or merge power, because GitLab runs those actions from descriptions and notes.

## Terms

| Term | Meaning |
| :-- | :-- |
| Options A to F | The ways of authoring merge requests compared under "Options". E is the recommendation, and D is the project token path the egress design has today. |
| Fine-grained token | A GitLab personal access token whose permissions name projects and actions, generally available from GitLab 19.2. |
| Quick action | A `/word` command GitLab runs from a description or comment. |

## GL-1: The `gitlab` kind

### Kind block and endpoints

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

The block holds `host` alone, and it is in the token trust record. The kind derives the rest from the host:

| Step | GitLab **[documented]** |
| :-- | :-- |
| Authorize | `https://<host>/oauth/authorize` with PKCE |
| Exchange and refresh | `https://<host>/oauth/token` |
| Access token lifetime | 2 hours by default, and configurable on self-managed and Dedicated |
| User | `GET /api/v4/user` |
| Revoke | `POST /oauth/revoke` with client ID, secret, and token |
| Application registration | A user, group, or instance application |

- **Refresh.** GitLab refresh "Invalidates the existing `access_token` and `refresh_token`" **[documented]**, which is the case the generic refresh lease serves.
- **`allowedAccounts`.** The kind checks group membership with `read_api`.
- **Verified email.** The kind reads the verified primary email from `GET /api/v4/user`. A self-managed administrator can change a user's email.
- **Administrators.** The kind sets `provider_is_admin` from the user endpoint.

### The repository check on GitLab

| Host | Read | Write | Tag |
| :-- | :-- | :-- | :-- |
| GitLab | `GET /api/v4/projects/:id` returning 200 with `read_api` | Effective access level of at least Developer | **[documented]** for the scopes. The field that reports inherited and shared access is S11. |

### What authorization needs

UG-2 on GitLab uses `read_api` and `read_user`, which grant no write **[documented]**. The token stays in the Token Service. It ships regardless of the choices below, on GitLab.com and on self-managed, with the same 2-hour refresh rules.

## GL-2: User-authored merge requests

### What PR authoring has to face

GitLab describes `api` as "complete read/write access to the API, including all groups and projects", and no OAuth scope limits a token to a project **[documented]**. **A user's `api` token in a binding with `delivery: inject` gives the agent the user's full power across every project and group if `netagent`'s request rules are its only limit, and a compromised `netagent` holds that token.** On self-managed, an administrator's token holds administrator power over the instance.

`glab` needs nothing unusual: REST alone for the commands captured, `Private-Token` carrying the placeholder on all 234 requests, and no `Content-Encoding` **[captured]**. With default settings it also opened one `CONNECT gitlab.com:443` **[captured]**, and the profile's `GLAB_CHECK_UPDATE=false` and `GLAB_SEND_TELEMETRY=false` stopped it **[captured]**.

### Options

| Option | `glab` unchanged | MR author | What a compromised `netagent` holds | Approve and merge stopped by |
| :-- | :-- | :-- | :-- | :-- |
| A. `api` OAuth token in a binding with `delivery: inject`, with request rules | Yes | User | The user's full API power, for up to 2 hours | Request rules alone |
| B. `write_repository` with `merge_request.create` on push | No. `glab mr create` needs the API. | Unstated by the docs (S11), and the pusher is expected | Read and write on every repository the user reaches, with no API | The token lacks API access. It relies on `merge_request.*` push options, which every other option refuses, so it would need its own allowlist entry, and then `netagent` must read `merge_request.description` for quick actions and refuse `merge_request.auto_merge`. |
| C. A or B behind a tenant opt-in, off by default | As chosen | As chosen | As chosen | As chosen |
| D. Project access token bots, as the egress design has today | Yes **[captured]** with a stub | The bot | One project's token, at that project's role | Request rules and `gitlab-deny`'s `bodyPatterns`, since a Developer-level project token can approve |
| E. A per-session fine-grained token from a parent held in the Token Service | Yes, if `glab`'s routes fit (S13) | **User** | One session's projects, with `Read` and `Create` and optionally `Update` on merge requests | **On routes, the host**, because the permission table separates `Approve` and `Merge` from `Create` and `Update` **[documented]**, then request rules. **On quick actions, `gitlab-deny`'s `bodyPatterns`**, until S13 shows whether the host applies the token's permissions to a command in a description. |
| F. A fine-grained token the user creates and registers, as a `registered` type. **Withdrawn in revision 5.** | Yes, if the same fit holds | **User** | The user's selected projects and permissions, for up to 365 days by default | The egress design refuses a stored token under `delivery: inject` on a git host a VCS pool covers, and GitLab serves API and git on one host. An exception would read cleanly in none of the three texts, and the same token under `delivery: file` is available where `allowRuntimeHeldCredentials` applies. |

Facts behind E:

- Fine-grained tokens are generally available from GitLab 19.2 on every tier and offering **[documented]**.
- `POST /user/personal_access_tokens` accepts `granular_scopes` from 19.2, with `selected_memberships` and `project_ids` **[documented]**. "You must be authenticated" is the only stated prerequisite, and the page does not say whether an OAuth token can call it or which scope it needs.
- The REST table lists `Personal Access Token`, `Create`, on that path, so a fine-grained token holding that permission can be the parent, if GitLab lets a token create one no wider than itself.
- `expires_at` has day granularity, so revocation at session end uses the egress design's durable record.
- Whether each creation notifies the user is unknown.

### Recommendation

**LFS is refused on GitLab until a live run passes.** GitLab's project tokens and stored tokens fall in the egress design's credential classes `mint-coarse` and `stored`, so the egress design refuses `info/lfs` on them, and `git push` of an LFS object fails there **[measured]**. GitLab's form ran on a stub **[measured]**, and the one live object tried returned an error rather than an action **[live]**, so no live GitLab action has been read.

User attribution was half of the request, and revision 1's answer left GitLab users with a bot author. With fine-grained tokens at 19.2, that no longer needs to be the answer.

**On GitLab 19.2 and later, the target is option E: user-authored merge requests from a per-session fine-grained token, with no `api` token in any pod.** S13 settles it, and it needs a free account and an hour rather than any Lenny code, so it runs before GL-2 starts. The token holds `selected_memberships` with the session's projects whose entries are in state `granted`, and `Read` on the project and merge requests, and `Create` and `Update` on merge requests, all as the template's `permissions` data. `Approve` and `Merge` are never in the default set.

The outcome of S13 selects the parent:

| S13 result | Parent in the Token Service | Cost |
| :-- | :-- | :-- |
| An OAuth token with a low scope can create the child | The link's OAuth token | The link needs that scope, and users relink once |
| An OAuth token needs `api` | The link's `api` OAuth token, on the tenant's opt-in. `provider_is_admin` refuses a link for an instance administrator. | `api` in the Token Service for those links, which widens Token Service compromise and never touches a pod |
| No OAuth token can, and a fine-grained parent can | A fine-grained token the user registers with the Token Service as the link's parent, holding only that create permission, and never delivered | A manual step per user, up to 365 days |
| No programmatic path | D remains, and the MR author stays the bot. A tenant that accepts the limits of `delivery: file` can use it where every VCS pool covering the host sets `allowRuntimeHeldCredentials`. | UR-4 stays unmet on GitLab |

On Free, GitLab's approval settings are absent **[documented]**, so E gives attribution there and the preset gives the rest.

**Option D remains for GitLab below 19.2 and for a VCS pool with `prAuthoring: bot`.** There the MR author is the bot, and two-person review rests on the commit author and a paid setting, as "Residual risks" states, or on UG-4.

**Option A is not this configuration's recommendation, and a VCS pool's `prAuthoring: user` never selects it.** Its residual is the user's whole account, and E reaches the same `glab` with a token whose power GitLab limits. A tenant can still define an `oauth2Link` type with `api` scope under `user-credential-delivery-design.md`, whose documentation says "no template means full reach". Revision 3 said option A never ships. Under the generic mechanism, Lenny can no longer refuse that by provider without tying the mechanism to GitLab. Option B stays unused, since it breaks `glab` and option E has a smaller reach.

Whichever token is in use, `gitlab-deny` stays on by default and the push option refusal applies, because S13 has not shown that a command in a description is limited by the token rather than by the user's own permissions. Commit author and committer follow `user-git-identity-design.md`, "Commit authorship", and git itself stays on the project token until S13 shows a fine-grained `Git` permission works per session.

Under `security-oversight-design.md` decision 25, these tokens are refused on the API host of a repository rule for now.

## GL-3: The `gitlab-deny` preset

### Request layout

| Provider and host | Flavour | API host | `apiBase` | `restPrefix` | `graphqlPaths` | Uploads | `attach` |
| :-- | :-- | :-- | :-- | :-- | :-- | :-- | :-- |
| `gitlab`, any host | `gitlab` | The host | `https://<host>/api/v4` | `/api/v4/` | `/api/graphql` | None | `Private-Token: {{secret}}` |

A named `gitlab-deny` is `gitlab` on any host.

### Preset body

```yaml
- name: gitlab-deny
  version: 3
  flavours: [gitlab]
```

| Property | `gitlab-deny` |
| :-- | :-- |
| `writeForm` | No query string, because GitLab's page shows `POST .../notes?body=note` **[documented]**, and JSON alone |
| `encodedSegments` | `projects` and `groups` values with `refuseDecoded: [merge_requests, ..]`, and `repository/branches`, `files`, `tags`, `commits`, and `tree`, `labels`, and `wikis` values |
| `refuseFamilies` | Yes |
| `denyBodyKeys` | `merge_when_pipeline_succeeds`, `auto_merge` |
| `undecodableBody` | Refused |

| Family | `gitlab-deny`, not run on any GitLab host |
| :-- | :-- |
| Review | `projects/*/merge_requests/*/approve`, `unapprove`, `reset_approvals`, and approval rule routes **[measured]** |
| Merge | `projects/*/merge_requests/*/merge`, `rebase`, `cancel_merge_when_pipeline_succeeds`, mutation `mergeRequestAccept`, and `denyBodyKeys`, at any depth and in a GraphQL variable **[measured]** |
| Content and ref | `projects/*/repository/commits`, `repository/files/**`, `repository/branches` writes, and mutations `commitCreate`, `branchCreate`, and `branchDelete` **[measured]** |
| Policy | `projects/*/protected_branches/**` writes **[measured]** |
| Credential | `/personal_access_tokens/self/rotate`, `/user/keys/**`, `/deploy_keys/**`, `projects/*/deploy_keys/**`, `projects/*/access_tokens/**`, and `projects/*/deploy_tokens/**` **[measured]**. |
| Text | `(?i)^\s*/(approve\|lgtm\|unapprove\|merge\|rebase\|submit_review\|target_branch\|run_pipeline)\b`, per line. It refused these in JSON, escaped, CR-separated, query, form, note, and GraphQL forms **[measured]**, also refused prose in a line starting `/merge the two lists` and a `/merge` inside a code block, and passed `/merge` after a non-ASCII space, because RE2's `\s` is ASCII, and `see /merge` and `/merge_requests/1`. The alias list is unverified, and whether GitLab executes a command through a given token is **[pending]** S17. |

GitLab has no route inventory like GitHub's. Of 67 `glab` and API rows, 8 were forwarded, 37 denied refused, 20 refused beyond the table's expected list, and 2 had no write **[measured]**. Its other evidence is Grape 3.3.5, an app built to match GitLab's routes under GitLab's pinned `grape ~> 3.2`, which routed `appr%6Fve`, a trailing slash, `//`, and `approve.json` to the approve handler **[measured]**. That is the framework rather than GitLab's whole application. A generic Rails 8.1.4 router routed a trailing slash, `//`, and `.json`, and answered 404 for `%65` **[measured]**. GitHub's stack is unknown.

### Quick actions

**GitLab.** GitLab itself runs quick actions, whatever the tenant has installed: "Run automatically when you save descriptions or comments" **[documented]**. `glab mr create --description /merge` carries a merge inside an allowed route, and a route-only policy sent it to the stub with the injected token **[captured]**. Whether GitLab would execute it for that token is **[stub cannot show]**, and it is S17.

The prototype refused every line starting with `/word`, which caught `/merge`, a following-line `/approve`, and a CR-separated `/merge`, and also refused an ordinary `/usr/bin/env` line **[captured]**. That breaks legitimate descriptions. `gitlab-deny` carries a narrower `bodyPatterns` set: lines split on LF, CR, VT, FF, U+0085, U+2028, and U+2029, and refused when, after ASCII whitespace, they name `approve`, `lgtm`, `unapprove`, `merge`, `rebase`, `submit_review`, `target_branch`, or `run_pipeline`, compared case-insensitively and regardless of code fences **[documented for the names, apart from `lgtm`]**. `/usr/bin/env` passes. A write with a query string is refused, because GitLab's page shows `POST .../notes?body=note`, and quick actions run on notes **[documented]**. The pattern with those eight names ran **[measured]**: it refused `/merge` and `/approve` in JSON, escaped, CR-separated, query, form, note, and GraphQL forms, also refused prose in a line starting `/merge the two lists` and a `/merge` inside a code block, and left `/usr/bin` and `/assign_reviewer` alone. `/target_branch` and a commit comment are refused too, because the pattern applies to every write, and ` /merge` after a non-ASCII space passes, because RE2's `\s` is ASCII. Whether GitLab would execute any of these is unknown. The alias list is unverified.

This list is a deny list, so a future GitLab command with merge power fails open until the preset changes. S17 tests each spelling against a live instance. On the bot path, a tenant lowers the risk further with a project token whose role cannot merge the target branch.

## Configuration by scope

| Setting | Scope | Who sets it | Default |
| :-- | :-- | :-- | :-- |
| `gitlab.host` on a `gitlab` link provider | Link provider | `tenant-admin` | None. A write must set it. |
| `scopes` on a `gitlab` link provider | Link provider | `tenant-admin` | `read_api` and `read_user` |

## Worked examples

### Erin on a self-managed instance

Tenant `initech` runs `gitlab.initech.internal` at 19.4, Premium, and S13 has passed with an OAuth parent. Erin is a developer at `initech`, and the VCS pool for `gitlab.initech.internal` sets `prAuthoring: user`.

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
- On GitLab 18.x or on a VCS pool with `prAuthoring: bot`, the same `glab` commands run under a project token, and the MR author is the bot. There the preset alone stops approval and merge, and Erin can approve the bot's MR unless "Prevent approvals by users who add commits" excludes her through a commit author the agent can edit.

## Dependencies

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| GL-1 (the kind and the check) | UG-1, UG-2, and UC-7 | The VCS kind rules, the release check, and the kind registry. |
| GL-2 (user-authored merge requests) | GL-1, UG-3, and S13 and S17 passing. S13 runs before GL-2 starts. | The token path and the preset's behaviour on a live host. |
| GL-3 (`gitlab-deny`) | UC-4 and UG-3 | The rule evaluator, the preset format, and the default preset on a VCS pool's API host. |

## Tests

### Tier 1: unit

- `gitlab-deny`: each named operation refused on each spelling, and an unnamed operation passing.
- `gitlab-deny`'s `bodyPatterns`: each command, alias, case, whitespace prefix, line separator, and fence, with `/usr/bin/env` admitted, and a write with a query string refused. `glab`'s `%2F` project paths pass.

### Tier 3: contract

- `gitlab-deny` replayed against recorded `glab` traffic per pinned version, with `%2F` project paths passing.

### Tier 6: end to end on cloud

Against a test GitLab instance: a link, the repository check, a per-session fine-grained token once S13 passes, `glab mr create`, and refusal of approval and merge by the preset.

## Validation owed

No live GitLab write, permission check, or quick action has run.

| Spike | Question | Decides | State |
| :-- | :-- | :-- | :-- |
| S10 | What does `glab` send? | Preset tests | **Done** for `mr create`, `view`, `list`, `note`, `update`, `close`, `revoke`, `approve`, `merge`, `rebase`, and `issue create` **[measured]**. 67 rows gave 8 forwarded, 37 denied refused, 20 refused beyond the expected list, and 2 with no write. Owed: `mr reopen`, a fork flow, and whether `glab` names a branch holding `/` with `%2F`, which `repository/branches/{encoded}` admits. |
| S11 | GitLab: which field reports effective access including shared groups, and who authors a merge request from `merge_request.create`? | The check, and option B's record | Open, live |
| S12 | GitLab: how does the committer rule match a commit's author, and on which tiers are external status checks offered? | Commit authorship and UG-4 on GitLab | Open, live |
| S13 | Can an OAuth token call `POST /user/personal_access_tokens` with `granular_scopes`, and with which scope? Can a fine-grained token create one? Does each creation notify the user? Does `glab mr create` run under `Read` and `Create` alone, and `mr note` and `mr update` under which additions? **Under a `Create`-only token, does a description holding `/approve` or `/merge` execute?** | The GitLab recommendation and GL-2 | Open, live. Needs a free account and no Lenny code. |
| S17 | On a live GitLab: do descriptions and notes created through the API run quick actions, under which permissions, with what case and whitespace rules, inside fences, and after a bare CR? How is an encoded project path routed? | `gitlab-deny`'s patterns | Open, live |
| V5 | S13 and S17 of `user-git-identity-design.md`, for `gitlab-fine-grained` and `gitlab-deny` | Those two data files | Owed, live |

## Residual risks

- **Compromised `netagent` on GitLab.** It holds one session's fine-grained token under GitLab option E, or one project's token under option D. `api` tokens are absent from pods, so a user's power over other projects is out of reach. 
- **GitLab under option D.** Below 19.2 or on a VCS pool with `prAuthoring: bot`, the MR author is a bot, and two-person review rests on an agent-editable commit author and a paid setting, so an injected agent defeats it, and Free has no such setting. UG-4 covers this where a tenant runs it.
- **GitLab quick actions.** `gitlab-deny`'s patterns are a deny list over free text. A command they do not name, or a spelling GitLab accepts and they miss, runs under whatever permissions GitLab applies (S13, S17).

## Decisions

| # | Decision | Recommendation | Cost of the recommendation | Alternative |
| :-- | :-- | :-- | :-- | :-- |
| 1 | GitLab (formerly `user-git-identity-design.md` decision 26) | Option E of "Options" on 19.2 and later, per-session fine-grained tokens, with S13 selecting the parent. Option D below 19.2, on a VCS pool with `prAuthoring: bot`, and if S13 finds no programmatic path. Option F is withdrawn. Authorization ships regardless. A VCS pool's `prAuthoring` never selects option A. | GL-2 waits on S13, and depending on it the Token Service holds `api` parents, or users take a manual step. Each session mints and revokes a token, and S13 decides whether each one notifies the user. Older instances keep the bot author. A tenant can still build option A itself under the generic mechanism. | Option D for everyone, which gives up user attribution, half of the request. Option B, which breaks `glab`. Option F, which three texts refuse. |
| 2 | Quick actions (formerly `user-git-identity-design.md` decision 31) | `gitlab-deny`'s `bodyPatterns` refuse lines naming commands that reach review or merge state, whatever the context. A tenant's rules can tighten them. | It is a deny list that fails open on a new command, and a literal `/merge` line in a code block is refused. | The prototype's refusal of every `/word` line, which rejects `/usr/bin/env` **[captured]**. Or no check, relying on the token, which S13 has not shown to bind quick actions. |

## Sources

Read on 2026-11-02 and 2026-11-03. These pages were read and their behaviour was not tested.

- GitLab: [OAuth 2.0 API](https://docs.gitlab.com/api/oauth2/), [OAuth provider and scopes](https://docs.gitlab.com/integration/oauth_provider/), [fine-grained tokens post](https://about.gitlab.com/blog/fine-grained-pats/), [fine-grained tokens](https://docs.gitlab.com/auth/tokens/fine_grained_access_tokens/), [fine-grained REST permissions](https://docs.gitlab.com/auth/tokens/fine_grained_access_tokens_rest/), [user tokens API](https://docs.gitlab.com/api/user_tokens/), [push options](https://docs.gitlab.com/topics/git/commit/#push-options-for-merge-requests), [approval settings](https://docs.gitlab.com/user/project/merge_requests/approvals/settings/), [notes API](https://docs.gitlab.com/api/notes/), and [quick actions](https://docs.gitlab.com/user/project/quick_actions/).
