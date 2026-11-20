# Steps: 0130 GitLab credential add-on

## Steps

### 0130.S1 The gitlab kind
- status: Planned
- aliases: [GL-1]
- lanes: [specification, code, documentation]
- depends-on: [0152.S1, 0152.S2, 0151.S7]

The design is in [GL-1](change.md#gl-1-the-gitlab-kind), which covers the kind, its request layout, and the repository check. The step registers the `gitlab` request layout (flavour, `apiBase`, `restPrefix`, GraphQL paths, uploads, and `attach`) with the kind, and 0130.S3 registers its default preset. The dependencies cover the VCS kind rules, the release check, and the kind registry with its request layout point. The spike S11 under ["Validation owed"](#validation-owed) runs before the step starts, because it settles the field the write check reads, and [open decision 3](decisions.md#open-decisions) settles the `scopes` values a write admits.

### 0130.S2 User-authored merge requests
- status: Planned
- aliases: [GL-2]
- lanes: [specification, code, documentation]
- depends-on: [0130.S1, 0130.S3, 0152.S3]

The design is in [GL-2](change.md#gl-2-user-authored-merge-requests). The dependencies cover the token path, the request layout that renders `{{apiBase}}`, and the `gitlab-deny` preset, which GL-2 keeps on whatever token is in use, so a user-authored token never runs on a GitLab API host without it. The spikes S13 and S17 under ["Validation owed"](#validation-owed) have to pass, and S13 runs before the step starts. [Open decisions](decisions.md#open-decisions) 1, 3, 4, 5, and 6 govern the step.

### 0130.S3 The gitlab-deny preset
- status: Planned
- aliases: [GL-3]
- lanes: [specification, code, documentation]
- depends-on: [0130.S1, 0151.S4, 0152.S3]

The design is in [GL-3](change.md#gl-3-the-gitlab-deny-preset). The dependencies cover the `gitlab` flavour that 0130.S1 registers, the rule evaluator, the preset format, and the default preset on a VCS pool's API host. The step registers `gitlab-deny` as the flavour's default preset, adds `Sudo` to the GitLab host's per-host credential list where the egress design's host data holds it, and adds the scheduled CI job that diffs GitLab's quick action page, fine-grained permission tables, and GraphQL schema. S17 passes before the step lands, and [open decisions](decisions.md#open-decisions) 2, 7, and 8 govern it.

## Validation owed

No live GitLab write, permission check, or quick action has run.

| Spike | Question | Decides | State |
| :-- | :-- | :-- | :-- |
| S10 | What does `glab` send? | Preset tests | **Done** for `mr create`, `view`, `list`, `note`, `update`, `close`, `revoke`, `approve`, `merge`, `rebase`, and `issue create` **[measured]**. 67 rows gave 8 forwarded, 37 denied refused, 20 refused beyond the expected list, and 2 with no write. Owed: `mr reopen`, a fork flow, and whether `glab` names a branch holding `/` with `%2F`, which `repository/branches/{encoded}` admits. |
| S11 | GitLab: which field reports effective access including shared groups, and who authors a merge request from `merge_request.create`? | The check, and option B's record. 0130.S1 waits on it. | Open, live |
| S12 | GitLab: how does the committer rule ("Prevent approvals by users who add commits") match a commit's author, and on which tiers are external status checks, GitLab's way for an outside service to pass or fail a merge request, offered? | Commit authorship and [UG-4](../0152-user-git-identity/change.md#ug-4-independent-approval-check) on GitLab | Open, live |
| S13 | Can an OAuth token call `POST /user/personal_access_tokens` with `granular_scopes`, and with which scope? Can a fine-grained token create one? Does each creation notify the user? Does `glab mr create` run under `Read` and `Create` alone, and `mr note` and `mr update` under which additions? **Under a `Create`-only token, does a description holding `/approve` or `/merge` execute?** Which scope does `GET /api/v4/version` need? Does `DELETE /personal_access_tokens/self` revoke the derived token, and can the parent find and revoke a token of the user's by name? On GitLab.com Free, can a project access token be created? | The GitLab recommendation and GL-2, including "Fine-grained token per session", and open decisions 1, 3, 4, and 6 | Open, live. Needs a free account and no Lenny code. |
| S17 | On a live GitLab: do descriptions and notes created through the API run quick actions, under which permissions, with what case and whitespace rules, inside fences, and after a bare CR? How is an encoded project path routed? Does the GraphQL schema hold a mutation that approves or unapproves a merge request? Which routes create a pipeline, and which write project settings, members, hooks, and CI variables? | `gitlab-deny`'s patterns, its Review family, and open decisions 7 and 8 | Open, live |
| V5 | S13 and S17 above, for `gitlab-fine-grained` and `gitlab-deny` | Those two data files | Owed, live |
