# Spike: `gh` and `glab` through a filtering `netagent` (2026-11-03)

This page records what was measured when `gh` and `glab` ran through a terminating `netagent` with an operation allowlist. The upstream was a local recording stub on a Docker `--internal` network. No request reached github.com's API, and no credentials or accounts were used.

Versions: `gh` 2.103.3 (2026-10-26), found through `cli.github.com/packages` and the `releases/latest` redirect. `glab` 1.127.0. gqlparser 2.5.33. Docker on arm64, 12 CPUs, 8 GB. Another batch ran load on the laptop throughout.

Tags: **[captured]** means seen at the stub or at `netagent`. **[source-read]** means read from cli/cli at tag v2.103.3 (`src/gh/`). **[docs-read]** means read from published documentation and not exercised. **[stub cannot show]** means only the real service can answer.

Raw output is under `raw/`. Programs are under `go/cmd/` (`netagent` with `filter.go`, `stub`, `attack`, `corpus`), and the policies are `policy-*.json`.

## Summary

- `gh pr create` and `gh pr review` use GraphQL [captured, source-read]. `pr merge` also uses GraphQL. A REST-only allowlist does not cover `gh`.
- `gh pr create` worked unchanged through the filter with every listed flag on Alpine; see "Platform gaps" under "Git without a loopback rewrite" for images that send `Time-Zone`. For 10 of 10 variants on each host flavour, the stub received the same requests with and without the filter, with the real token and no placeholder.
- Under the final policy the attack table has 522 rows per flavour and no bypass. The table is not the whole result: the section "Bypasses found" lists what got through during the spike, including one item that stays open.
- A filter check costs 11 µs on a typical `pr create` and 88 µs on the largest document `gh` sent. A 256 KiB document built to maximize tokens costs 36 ms and 40 MB of transient allocation.
- `glab` used REST alone for the listed commands. The allowlist method covers its routes, but GitLab quick actions move approval and merge into the MR description, where a route check does not see them.

## Part 1: Capture

### Wire facts [captured]

- `gh` spoke HTTP/2 to `netagent` on all 2,619 of its requests in the client-side log (`raw/netagent-client-view-reqlog.jsonl`), with `Authorization: token lenny-injected` and `Accept-Encoding: gzip` on each. That log spans every run in this spike and also holds `git`, `glab`, and attack requests.
- No request carried `Content-Encoding`, in 176 GHES and 138 github.com requests. Bodies are plain JSON. `netagent` rewrites `Accept-Encoding` to `gzip, deflate`.
- The stub received `token GH-REAL-55aa01`, and no stub record in `raw/capture-ghes/` holds the placeholder.
- Headers `gh` sends: `Accept`, `Accept-Encoding`, `Authorization`, `Content-Type`, `Content-Length`, `User-Agent`, `X-Github-Api-Version: 2022-11-28`, `Graphql-Features: merge_queue`, and `X-Gh-Cache-Ttl`. The last is `gh`'s cache hint, and it reaches the origin. A header allowlist has to list it.
- The review event travels only in variables: `{"input":{"pullRequestId":"...","body":"","event":"APPROVE"}}`. The document is the same for approve, comment, and request changes.
- `gh` caches feature detection on disk. The driver cleared the cache before each case, so a later run of a real session sends fewer requests than the table shows.

### Routes

An Enterprise host uses `/api/v3` and `/api/graphql`. The github.com flavour was an extra pass with `GH_TOKEN`, using `api.github.com` as a network alias for the stub on the internal network (`raw/no-route.txt` shows no route off it). It uses `/` and `/graphql`. The flavours differ because `gh` selects login-based mutations on github.com [source-read: `internal/featuredetection`]. An allowlist written from an Enterprise capture alone breaks `--reviewer` and `--assignee` on github.com.

| Command | REST | GraphQL query fields | GraphQL mutation fields |
| :-- | :-- | :-- | :-- |
| `pr create`, `--draft` | GHES: `GET /meta` | `repository`; GHES: `__type` | `createPullRequest` |
| `--reviewer bob,acme/core` | same | adds, GHES: `viewer`, `organization` | GHES: `requestReviews`. github.com: `requestReviewsByLogin` |
| `--label` | same | same as basic | `updatePullRequest` (`labelIds`) |
| `--assignee` | same | same as basic | GHES: `updatePullRequest`. github.com: `replaceActorsForAssignable` |
| `--milestone` | same | same as basic | `updatePullRequest` (`milestoneId`) |
| `--project` | same | adds `organization`, `viewer` | `addProjectV2ItemById` |
| `--head alice:feature` | same | same as basic | `createPullRequest` with `headRefName: "alice:feature"` |
| `pr view`, `pr view --json` | GHES: `GET /meta` | `repository` | none |
| `pr list` | none | `repository` | none |
| `pr list --search` | GHES: `GET /meta` | `__type`, `search` | none |
| `pr status` | none | `__type`, `viewer` (GHES), `repository`, `search` | none |
| `pr checks` | none | `repository`, `__type`, `node` | none |
| `pr diff` | `GET /repos/{o}/{r}/pulls/{n}` with a diff `Accept` | `repository` | none |
| `pr comment` | none | `repository` | `addComment` |
| `pr edit --title` | GHES: `GET /meta` | `__type` (GHES), `repository` | `updatePullRequest` (`title`) |
| `pr edit` with metadata | GHES: `POST /repos/{o}/{r}/pulls/{n}/requested_reviewers` | `repository`, `organization`, `viewer` | GHES: `addLabelsToLabelable`, `updatePullRequest`, `addProjectV2ItemById`. github.com adds `requestReviewsByLogin`, `replaceActorsForAssignable` |
| `pr ready` | none | `repository` | `markPullRequestReadyForReview` |
| `pr close` | none | `repository` | `closePullRequest` |
| `pr review` (approve, comment, changes) | none | `repository` | `addPullRequestReview` |
| `pr merge` (`--merge`, `--rebase`, `--squash`, `--admin`) | none | `__type`, `repository` | `mergePullRequest` |
| `pr merge --delete-branch` | `DELETE /repos/{o}/{r}/git/refs/heads%2Ffeature` | same | `mergePullRequest` |
| `pr merge --auto` on a blocked PR | none | same | `enablePullRequestAutoMerge` |
| `pr merge` on a merge-queue PR | none | same | `enablePullRequestAutoMerge` |
| `pr merge --disable-auto` | none | same | `disablePullRequestAutoMerge` |
| `repo view` | `GET /repos/{o}/{r}/readme` | `repository` | none |
| `issue view` | GHES: `GET /meta` | `__type` (GHES), `repository` | none |
| `auth status` | `GET /` (`/api/v3/` on GHES) | `viewer` | none |
| `gh api` REST | as written | none | none |
| `gh api graphql` | none | as written | as written |

Per-case detail is in `raw/capture-ghes.table.txt` and `raw/capture-ghcom.table.txt`. Each case's full requests are under `raw/capture-ghes/` and `raw/capture-ghcom/`.

Source-read facts the stub could not provoke:

- The automatic fork in `pr create` is offered only at an interactive prompt (`create.go`, "Create a fork of"). It calls `POST /repos/{o}/{r}/forks` (`api/queries_repo.go`, `ForkRepo`). Without a terminal, `gh` stops with "you must first push the current branch to a remote, or use the --head flag".
- `pr review` calls `addPullRequestReview` for all three events (`api/queries_pr_review.go`, `AddReview`).
- The login-based mutations apply to github.com and ghe.com, and GHES uses the node-ID ones. ghe.com was not captured.

`gh pr merge --delete-branch` also ran `git`, which requested `GET /acme/widgets.git/info/refs` on the same host [captured]. On GHES the API and git share a host name, so a filter on that binding refuses git unless git goes elsewhere. No git request was run under the filter, so this is an inference from the policy text.

## Part 2: Filter

`go/cmd/netagent/filter.go` runs in the request path after the authority is derived, on terminated and plain-upgrade requests alike. A filtered connection refuses any other authority, and a request naming a filtered host is checked wherever it arrives.

- **REST:** exact method, exact path template, listed query keys, and JSON object bodies. Path literals are case-sensitive and owner and repo names are free. Any `%`, `//`, dot segment, `;`, `#`, or backslash fails. The filter also refuses if the forwarded target would differ from the checked text.
- **GraphQL:** POST to one path with no URL query, one `Content-Type: application/json`, no `Content-Encoding`, and at most 256 KiB. The body is one JSON object with valid UTF-8, no duplicate top-level keys, and only `query`, `variables`, and `operationName`. gqlparser parses the document. Every operation is checked whichever one `operationName` selects, using field names with fragments expanded and aliases ignored. Subscriptions fail. Input keys of `updatePullRequest` are limited to `pullRequestId`, `assigneeIds`, `labelIds`, `projectIds`, and `milestoneId`.
- **Headers:** an allowlist. Override headers, `Upgrade`, and trailers fail, as does a `Connection` option other than `keep-alive` or `close`.
- **Final policy additions:** `lexStrict` and `maxDepth: 32`, both added because of findings below. The deepest `gh` document had 14 levels (`raw/gh-doc-depth.txt`).

The policy allows `createPullRequest`, `updatePullRequest` (limited input), `requestReviews`, `requestReviewsByLogin`, `replaceActorsForAssignable`, and `addProjectV2ItemById`. The query fields are `repository`, `viewer`, `organization`, `node`, `search`, and `__type`.

### Attack table (`raw/attack-ghes.txt`, `raw/attack-ghcom.txt`, `raw/attack-groups.txt`)

Most cases ran on HTTP/1.1 and HTTP/2, and each row is one protocol. The results are the same for both flavours.

| Expected | Rows | Result |
| :-- | :-- | :-- |
| Allowed (positive controls) | 52 | All reached the stub with the real token |
| Refused | 467 | 449 by the filter, 11 with a 400 from `netagent` or its HTTP libraries, 7 with no HTTP status. The stub received nothing for any of them |
| Opaque route, no credential | 3 | 2 reached the stub without a credential, 1 did not connect |

The refused rows cover each item in the request:

- REST routes for reviews, merges, branch updates, and refs.
- Aliases, including an alias named `createPullRequest`.
- Several operations with `operationName` on either one, batches, fragments (nested, inline, cyclic, and unused), and event-carrying variables.
- Comments and odd whitespace, duplicate and case-variant `query` keys, and `query` as a URL parameter on GET and POST.
- Form, `application/graphql`, gzip, zstd, and deflate bodies, and bodies over the bound by length and by chunking.
- Malformed documents and subscriptions.
- `//`, encoded slashes, `..`, trailing slash, case changes, method overrides, HEAD, and OPTIONS.
- Absolute-form requests, differing `Host` and `:authority`, another bound host in one connection, reuse after an allowed request, pipelining, and HTTP/1.1 framing conflicts.

Six of the 7 rows without a status are HTTP/2 protocol errors from the server library. The seventh is the bare-LF defect below.

Real `gh` commands (`raw/gh-attack-ghes.summary.txt`, `raw/gh-attack-ghcom.summary.txt`): 24 of 25 exited non-zero with the refusal text, and the 25th is the allowed control. `pr review`, `pr merge` with each mode, `gh api` with each REST route, and `gh api graphql` with each mutation all failed. No write reached the stub in either flavour (`raw/filter-compare.txt`).

### Bypasses found

1. **Parser differential on a bare CR, closed.** Before `lexStrict`, this document passed the filter and reached the stub with the real token on both protocols (`raw/attack-ghes.v1-no-lexcheck.txt`): `# x\rquery{viewer(a:"""\nmutation{addPullRequestReview(...){clientMutationId}} #"""){login}}`. gqlparser, graphql-go, graphql-js 17.0.2, and graphql-ruby's C parser (`graphql-c_parser` 1.1.4) read one query. graphql-ruby 2.6.14's default lexer ends a comment only at LF and reads one mutation, `addPullRequestReview` (`raw/corpus-table.txt`). Which parser GitHub runs in production is unknown [stub cannot show]. `lexStrict` refuses a bare CR, other control characters, and U+0085, U+2028, U+2029, and U+FEFF. Forwarding the filter's own printing of the document also closes it at the parser level (`raw/corpus-printed-compare.txt`), and `gh` kept the same exit codes and operations that way (`raw/canon-ghes.summary.txt`, `raw/canon-compare.txt`).
2. **Duplicate keys under `variables`, open.** `{"i":{"pullRequestId":"P","state":"CLOSED"},"i":{"pullRequestId":"P"}}` passed the `updatePullRequest` input check and reached the stub with the real token (`raw/nested-duplicate-variables.txt`). The filter reads the last value. A first-wins origin would close the PR. Ruby's standard JSON reads the last value (`raw/ruby-json-duplicate.txt`), and GitHub's reader is unknown. The effect is limited to fields of a listed mutation, so it does not reach approval or merge. The duplicate check needs to apply at every depth. Canonical forwarding as written does not close this, because it copies `variables` verbatim.
3. **GitLab routes, closed for the tested spellings.** See Part 4.

Probes that looked like bypasses and are not: the first run flagged U+2028 after a comment and a `\"""` block string (`raw/attack-ghes.first-run.txt`). All five readers agree that these are one query. The expectation in the table was wrong, and it was corrected. Under the final policy U+2028 is refused anyway.

### Other defects found in the prototype

- **Out-of-memory kill.** Canonical forwarding with gqlparser's printer was killed at a 256 MiB limit by an 80 KB document nested 40,000 levels (`raw/attack-ghes.canon-first-run-oom.txt`). The process exit stops egress for the whole pod. `maxDepth` closed it, and the table then passed with the printer on (`raw/attack-ghes.canon.txt`).
- **No head timeout.** A request with bare LF line endings gets no answer. `peekHead` waits for CRLFCRLF until the client gives up, 20 s here. Nothing reached the stub, so this fails closed, but the prototype holds the connection with no deadline.
- **No token limit.** gqlparser offers `ParseQueryWithTokenLimit`, which is not wired in. The largest `gh` document has about 608 tokens by a regex count (`raw/gh-doc-tokens.txt`).

### Legitimate `gh` commands the filter breaks

The matrix is `raw/filtered-ghes.summary.txt` and `raw/filtered-ghcom.summary.txt`: 25 of 42 cases exit 0 and 17 fail.

Working: every `pr create` variant, `pr view`, `pr list`, `pr status`, `pr checks`, `pr diff`, `repo view`, `issue view`, `auth status`, `gh api` REST on listed routes, and `gh api graphql` queries.

Refused by the policy's choice: `pr comment`, `pr ready`, `pr close`, `pr edit`, `pr review`, `pr merge`, and any `gh api graphql` mutation not listed. Those commands need `addComment`, `markPullRequestReadyForReview`, and `closePullRequest` on the list; a policy with them listed was not run.

Refused as a side effect:

- **`pr edit` partial effects.** `pr edit` with metadata exits 1 after some allowed writes reached the stub: `updatePullRequest` and `addProjectV2ItemById` on GHES (`raw/filter-compare.txt`), plus `requestReviewsByLogin` and `replaceActorsForAssignable` on github.com.
- **The 256 KiB bound.** `gh` JSON-encodes with HTML escaping, so `<` costs 6 bytes (`raw/bigbody-strict.txt`):

| PR body | Bytes on the wire | Result |
| :-- | :-- | :-- |
| 65,536 `x` | 65,888 | passed |
| 65,536 `"`, `é`, or newline | 131,424 | passed |
| 43,000 `<` | 258,352 | passed |
| 44,000 `<` | 264,352 | 413 |
| 65,536 `<` | 393,568 | 413 |
| 65,536 emoji | 262,496 | 413 |

The figure 65,536 is GitHub's stated body limit as recalled; it was not verified. An ordinary PR body stays well under the bound.

### What the allowlist does not bind

These hold under the final policy and need a separate decision in the design:

- The filter checks operations and not targets. `createPullRequest` takes an opaque `repositoryId`, and `updatePullRequest`, `requestReviews`, and `addProjectV2ItemById` take node IDs, so they apply to anything the user's token reaches.
- `repository`, `node`, and `search` read anything the user can read.
- A PR body can carry a command to a repository's bot, and the filter does not read it.
- Git is outside this filter. Updating a ref needs Contents: write per the docs table below, and merging requires the same permission. A `git push` was not examined.

### Permissions [docs-read; stub cannot show]

From `github/docs` fine-grained permission data (`raw/github-docs-permissions.txt`), none of it exercised:

| Route | Documented permission |
| :-- | :-- |
| `POST /pulls` | Pull requests: write |
| `POST /pulls/{n}/reviews`, `.../events` | Pull requests: write |
| `PATCH /pulls/{n}`, `PUT .../update-branch` | Pull requests: write |
| `PUT /pulls/{n}/merge`, `POST /merges` | Contents: write |
| `PATCH /git/refs/{ref}` | Contents: write |

As documented, `pull_requests: write` permits approval, so the token alone does not prevent it and the filter has to. As documented, merging fails without `contents: write`. Neither was tested. The GraphQL mutations' permission checks, and whether GitHub honors batches, GET queries, form bodies, or method overrides, are also unknown from this spike.

## Part 3: Cost

Filter check alone, `go test -bench`, 1 CPU, final policy (`raw/cost-bench.txt`):

| Document | Body bytes | Time per check | Allocated per check |
| :-- | :-- | :-- | :-- |
| `pr create` (`createPullRequest`) | 353 | 11 µs | 6.8 KB |
| `pr status` | 1,833 | 55 µs | 43 KB |
| Largest `gh` sent (`pr view --json`, all fields) | 2,388 | 88 µs | 78 KB |
| `pr create` with a 65,536-character body | 65,784 | 606 µs | 559 KB |
| 258 KB of escaped characters | 258,185 | 5.5 ms | 1.7 MB |
| 256 KB comment | 262,144 | 3.0 ms | 2.5 MB |
| 256 KB of tokens | 260,023 | 36 ms | 40 MB |

Canonical printing adds 1.9 µs on `pr create`, 44 µs on the largest `gh` document, and 14 ms on the token document.

End to end, paired on one `netagent` limited to 1 CPU, at 5 pairs per second for 60 pairs. The same body alternates between a filtered and an unfiltered host on the same stub (`raw/cost-e2e.txt`):

| Document | Unfiltered p50 | Filtered p50 | Paired difference p50 | Average time inside the check |
| :-- | :-- | :-- | :-- | :-- |
| `createPullRequest`, 209 B | 0.61 ms | 0.63 ms | -0.07 ms (below noise) | 62 µs |
| Largest `gh`, 2,388 B | 0.85 ms | 1.00 ms | 0.06 ms | 180 µs |
| 65,784 B | 1.45 ms | 2.24 ms | 0.94 ms | 1.05 ms |
| 258,185 B | 3.72 ms | 7.97 ms | 4.2 ms | 4.6 ms |
| 260,023 B of tokens | 68 ms | 86 ms | 17 ms | 27 ms |

Time inside the check includes reading the request body from the client, which the benchmark does not do. That read accounts for most of the gap between the two tables on small documents.

Memory, `memory.current` and `memory.peak` in MiB, cumulative across the run in the order shown: 3.0 and 10.9 idle after start, 13.4 and 15.5 after `pr create`, 16.0 and 17.9 after the largest `gh` document, 16.6 and 18.4 after 65 KB, 16.7 and 18.9 after 258 KB, and 68.3 and 74.9 after the token document. These rows have no filter-off baseline, because both hosts share the process. The binary grew from 10.87 MB to 11.41 MB (`raw/cost-binary-size.txt`), compared with spike C's build.

`gh pr create` wall time, 8 runs in each block with an empty cache, ordered on, off, on, off: medians 82, 78, 73, and 63 ms (`raw/cost-gh-wall.txt`). The difference is inside the run-to-run spread of 58 to 130 ms.

## Part 4: `glab`

`glab` used REST alone for `mr create`, `mr view`, `mr list`, `mr approve`, and `mr merge` [captured, `raw/capture-glab.table.txt`]. GraphQL appears only through `glab api graphql`. This covers the listed commands and says nothing about other `glab` commands.

| Command | REST |
| :-- | :-- |
| `mr create` | `GET /api/v4/projects/acme%2Fwidgets?license=true&with_custom_attributes=true`, `POST /api/v4/projects/acme%2Fwidgets/merge_requests` |
| `mr create` with metadata | adds `GET .../milestones?title=...` and `GET /api/v4/users?username=...`; a draft is a `Draft: ` title prefix |
| `mr view` | `GET .../merge_requests/{n}`; `--comments` adds `GET .../discussions` |
| `mr list` | `GET .../merge_requests?page=1&per_page=30&state=opened` |
| `mr approve` | `GET .../merge_requests/{n}`, `POST .../merge_requests/{n}/approve` |
| `mr merge` | `GET .../merge_requests/{n}`, `PUT .../merge_requests/{n}/merge` |
| `glab api` | as written; GraphQL at `POST /api/graphql` |

The token travels in `Private-Token`. `glab` sent `lenny-injected` on all 234 of its requests in the client-side log, and the stub received the real value. All requests were HTTP/2 with no `Content-Encoding`. With default settings `glab` also opened one `CONNECT gitlab.com:443`, which the internal network refused (`raw/opaque-destinations.txt`); with `GLAB_CHECK_UPDATE=false` and `GLAB_SEND_TELEMETRY=false` it opened none. The purpose of that connection was not determined.

The allowlist method covers the routes with two changes:

- **Encoded project paths.** `{project}` must accept `%2F`, because `acme%2Fwidgets` is the legitimate form. The "refuse any percent" rule therefore cannot apply to that segment. The first pass let `acme%2Fwidgets%2Fmerge_requests%2F7%2Fapprove/merge_requests` and `acme%2F..%2Fwidgets` reach the stub (`raw/glab-raw.first-run.txt`). The filter now refuses dot segments and route words inside the project path (`raw/glab-raw.txt`). How GitLab routes such a path is unknown [stub cannot show].
- **Quick actions in the description.** GitLab's documentation lists `/approve` and `/merge` and states that quick actions run when a description or comment is saved [docs-read, `raw/gitlab-quick-actions-grep.txt`]. So `glab mr create --description /merge` carries a merge request inside an allowed route. A route-only policy sent it to the stub with the real token (`raw/filtered-glab-routes.summary.txt`). Whether GitLab would execute it for this user is unknown [stub cannot show].

The final GitLab policy decodes JSON strings and refuses any line starting with `/word`. It refused `/merge`, a following-line `/approve`, the `/merge` spelling, and a CR-separated `/merge` (the last only after the pattern was widened). It also refuses an ordinary description line such as `/usr/bin/env`, and it is a denylist over free text, so it is weaker evidence than the route check. Refusing every slash-leading line, as tested, breaks legitimate descriptions. Under this policy `mr approve` and `mr merge` fail with nothing written at the stub, and `glab api` with each route and each mutation is refused (`raw/filtered-glab-strict.summary.txt`). A `merge_when_pipeline_succeeds` field on create still passes, because the filter has no body-key allowlist.

## Harness defects

- A stray `cat > /tmp/ignore` in my own command waited on stdin, so the first `main.go` patch never ran. It created an empty file outside the scratchpad, which I removed.
- The first attack client used a new HPACK decoder per response, which misread later HTTP/2 statuses as 0 on reused connections. It produced one false failure. Fixed, with the first run kept as `raw/attack-ghes.first-run.txt`.
- The first verdict logic counted a connection refused as a refusal. The out-of-memory run's totals showed 485 passes, many of them false, and that run was discarded. Connection failures are now `INVALID`, and no retained final run has one.
- `raw/canon-compare.txt` first reported 6 differences. They were a pairing artefact of concurrent requests and went to 0 after sorting both sides.
- The stub answers `__type` as if every field exists, which selects `gh`'s newest code paths. An older GHES can select different documents, so the allowlist needs testing against the versions in use.
- The stub's two parsers are gqlparser and graphql-go, so it reports `query:viewer` for the CR document. The differential appears only in the separate parser table, and no origin executed anything.
- The stub executes nothing and enforces nothing, so a 200 at the stub says only that the request was forwarded.
- `pr create` from a fork was captured with `--head alice:feature`. The prompt-driven automatic fork was not run.
- The load batch ran concurrently, so small latency differences are not resolved. `netagent` and the stub each had a 1 CPU limit.
- Downloads beyond the `gh` and `glab` packages, all inside containers: Go modules, Alpine packages, the npm `graphql` package, the `graphql` and `graphql-c_parser` gems, `github/docs` data files, and GitLab's quick actions page.
- The github.com pass is an addition to the request. It used stub aliases on the internal network and made no external call.
- `docker build` layer cache for `gh-client` and `gh-ruby` was not pruned, because a prune cannot be limited to this spike's entries.

## Cleanup

`down.sh` reported 0 `gh-` containers, networks, volumes, and images (`raw/cleanup.txt`). The `od-` containers, `podium-postgres`, and `podium-minio` were still running afterwards. The repository was not edited.

## Git without a loopback rewrite: candidates L, T, and P, and the deny preset

This section replaces the earlier incomplete section. All results use raw output under `raw/gitopt/`, and programs and scripts under `gitopt/`. The origin ran stock `git http-backend` behind a recording stub, so git behaviour is real and GitHub and GitLab API behaviour is not. **The stub proves forwarding only.** A row that reaches the stub says nothing about whether a real host would accept the spelling, grant the permission, or perform the action.

Clients had a route to `netagent` only. `netagent` reached the origin through a CONNECT stand-in that required the pod credential. Every command carried a timeout inside its container, with a watchdog on the docker client.

The candidates are:

- **L:** the design's `insteadOf` to `http://127.0.0.1:3129/https/...`, as a control.
- **T:** `http.proxy` at `netagent`, termination of VCS hosts, and `http.sslCAInfo` at a bundle of the per-pod CA and public roots. A stand-in root played the public roots.
- **P:** `url."http://".insteadOf = https://` with `http.proxy` at the loopback listener. `netagent` reads absolute-form plain HTTP and uses TLS upstream.

### Hang diagnosis

The earlier run stopped at `pygit2.clone_repository(..., proxy=True)` while the per-pod CA excluded the target host. The process sat in a poll for 8 h 44 min with `netagent` showing no active connection (`raw/gitopt/t6/hang-diagnosis.txt`). The rerun killed it at 45 s and printed no result. A libgit2 client therefore hangs on this certificate failure, where git, `gh`, and go-git return an error. The cause inside libgit2 was not diagnosed. The harness had no timeout on that path, so the run stalled there.

### Part A results

**Git** (`raw/gitopt/t1/`): T, P, and L each passed on OpenSSL (git 2.49.1) and GnuTLS (git 2.47.3): clone, fetch, shallow, partial, push, 64 MiB default and chunked pushes, LFS with a separate storage host, and submodules on bound and unbound hosts. Each refused a push to `main` and a clone outside the set. An in-scope redirect was followed and an out-of-scope redirect got 403. With the per-pod CA alone, T failed LFS and the unbound-host submodule on both builds.

Credential prompts: a normal fetch made 0 askpass calls on every candidate. After a 401 on a covered host git asked twice, and the origin saw only the mint on every candidate. After a 401 on an unbound host, T delivered the agent's answer to the origin, and P and L dropped it.

**`gh` and `glab`** (`raw/gitopt/t2/`, `t2c/`): on T and P, on Alpine and Debian, on GHES and github.com flavours, `pr create`, `view`, `list`, `status`, `checks`, `checkout`, `repo view`, `repo clone`, `repo set-default`, and `glab mr create`, `view`, and `list` all exited 0, with the placeholder replaced at the stub. Branch inference and fork handling worked: a branch pushed to a `fork` remote produced a head of `alice:<branch>`, `gh repo clone` of a fork added `upstream`, and an interactive `pr create` under a pty pushed through the `origin` remote as git renders it. Two commands failed on both: `gh browse` (a `HEAD` that the allowlist in this run refused) and a non-interactive `pr create` on an unpushed branch, which is `gh`'s own abort.

On L, every `gh` command that reads remotes stopped with "none of the git remotes". `glab mr create` and `mr list` failed the same way, and `glab mr view` and `repo view` exited 0.

P against T: the outputs differ only where git prints a URL. `git remote -v` prints `http://host/o/r`, git messages name `http://`, and `gh`'s host and repo are right. `gh` and `glab` printed and stored no `http://` URL: PR URLs, `remote.origin.url`, and `branch.cross-branch.remote` stayed `https://`, and API traffic stayed HTTPS. `gh` ignores the scheme because `ghrepo.FromURL` reads the hostname and path [source-read, `raw/gitopt/source-gh-remote-handling.txt`].

`test.sh` cases (`raw/gitopt/t9/`): under L, the rewrite cases fail with "none of the git remotes" on Alpine and Debian. With only the config replaced, T and P reach the network call on every `gh` case, and the `glab` rewrite case passes the remote check.

Platform gaps (`raw/gitopt/t10/`): the old allowlist refused every `gh` GraphQL request on any image with `/etc/localtime`, because `gh` then sends `Time-Zone`. The earlier Part 2 claim held on Alpine only. With the header added, 10 images and `TZ` and `LANG` variants gave 0 refusals. `gh pr create` needs git in the image even with `-R`, and 6 of the 10 have none. macOS, Windows, and amd64 were not run because the harness is Linux arm64 containers. The .NET SDK image was not local and was not pulled.

**Remote forms** (`raw/gitopt/t3/`), for a remote on a repository, a clone the agent makes, and a remote added later:

| Config | `git remote -v` for `git@host:o/r` | git on a bound host | `gh` or `glab` | Arbitrary host |
| :-- | :-- | :-- | :-- | :-- |
| L | `http://127.0.0.1:3129/scp/...` | works | "none of the git remotes" | git prints the clear `ERR`; `gh` fails |
| T or P, no SSH rule | unchanged | fails with an SSH error | works | `gh` works; git fails with an SSH error |
| T or P, loopback rule alone | `http://127.0.0.1:3129/scp/...` | **fails**: `http.proxy` catches the loopback URL | "none of the git remotes" | as bound host |
| T or P, loopback rule and `[http "http://127.0.0.1:3129/"] proxy =` | as above | works | "none of the git remotes" | git prints the clear `ERR` |
| T or P, per-host rule from the binding | `https://host/o/r` or `http://host/o/r` | works, on all three uses | works | rule absent: as "no SSH rule" |
| P, `url."http://".insteadOf = git@` | `http://host:o/r` | fails: URL rejected | "no git remotes found" | same |

What is lost on an arbitrary host under a per-host rule: the rendered config cannot name it, so an SSH remote gets an SSH failure with no `ERR` message and no `sshForms` mapping. `alice@host:o/r` matches no rule on any candidate.

**Evasion** (`raw/gitopt/t4/table.txt`, `t4b/`), with an agent UID that owns its config and environment:

| Attempt | T | P | L |
| :-- | :-- | :-- | :-- |
| `http.proxy` unset | no route | no route | still loopback, refused |
| `http.proxy` at the egress proxy, no credential | 407 | 405 | 405 |
| `http.sslVerify=false`, `GIT_SSL_NO_VERIFY` | enforcement holds | holds | holds |
| `http.extraHeader` token | replaced by the mint | replaced by the mint | replaced by the mint |
| URL token | replaced by the mint | replaced by the mint | request fails, no token at the origin |
| Differing `Host` | refused | refused | refused |
| Outside the set, read-only push, ref outside patterns, deletion, push cert, gzip, path spellings | refused | refused | refused |
| `curl` smart HTTP over HTTPS through the proxy | same enforcement | refused | refused |
| `curl` smart HTTP over plain HTTP | same enforcement on 3128 | same enforcement on 3128 and 3129 | same enforcement on 3129, refused on 3128 |
| Rewrite removed, host with an API binding | enforced | refused | refused |
| Rewrite removed, VCS host with no API binding | enforced | **opaque** | **opaque** |
| `.git/config` from a hook, `.gitmodules`, `.lfsconfig` | no token follows | no token follows | no token follows |
| Agent's own token to an unbound host | **reaches the host** | dropped on the rewrite path | dropped on the rewrite path |

The opaque row applies to P and L. With the rewrite removed, git reached github.com through a tunnel with no enforcement: public reads succeeded, private reads got 401, and an agent's own token reached the origin. `raw/gitopt/t11/P-file.out` shows a push to `main` with a held token accepted by the origin on both hosts. Three CONNECT rules were measured for such a host (`t4b/`): opaque lets all of that through; refuse stops it and also breaks release downloads and web pages; terminate refuses smart HTTP and keeps downloads working, but it needs the per-pod CA for that host.

Same enforcement on every path: yes for T on VCS-covered hosts. For P and L, only while the rewrite stands or the host is terminated. Prototype bugs found here and fixed: a ref of `refs/heads/lenny/../main` passed the pattern check, and a case-variant `Operation` key passed the LFS check.

Tokens at the wrong host: none. The findings in `raw/gitopt/token-audit.txt` (35) and `review/token-audit.txt` (20) are 50 placeholders on unbound hosts and 5 mints on `acme.ghe.com`. That host is bound in the review run and absent from the audit's host list, so those 5 are a limit of the audit script. A reflected token was masked.

**A token the runtime holds itself** (`raw/gitopt/t11/`):

| Case | T | P |
| :-- | :-- | :-- |
| `git push` by helper, header, URL, or `gh auth git-credential` | token dropped, mint used, patterns hold | same on the rewrite path |
| Same, rewrite removed | same as above | **token reaches the host; push to `main` accepted** on any host not terminated |
| `gh`, no binding on the API host | token passes; no filter, so `pr merge` reaches the stub | same |
| `gh`, a filter-only binding | token passes; `pr merge` refused | same; on github.com git stays opaque |

Consequence for the owner's decision: on a terminated VCS host the runtime's token never reaches the host for git, so the push acts as the minted identity. A preset applies only where a binding names it, so a `file` token needs a filter-only binding on its API host to get the preset. Whether Lenny should send the held token instead of a mint is a design choice these runs did not test.

**Clients** (`raw/gitopt/t5/`, `t6/`):

| Client | T | P | L |
| :-- | :-- | :-- | :-- |
| go-git, defaults | clones | refused, because it reads no `insteadOf` | same as P |
| JGit, JVM proxy and truststore | clones | "git-upload-pack not permitted": the URL stays `https://` | same as P |
| libgit2 default | no proxy, DNS failure | DNS failure | clones |
| libgit2 `proxy=True` or explicit | clones | DNS failure | 502 |
| git in the golang:1.26, node:20, maven, and gradle images | clones | clones | clones |

**CA, warm pods, and routes** (`t6/`): with the host outside the constraints, OpenSSL git, GnuTLS git, `gh`, and go-git refused; JGit accepted, so the JVM does not enforce constraints on this anchor; libgit2 hung. After a re-issue the next run of each client passed with no restart. An arbitrary host under T is an opaque tunnel, so the constraints do not apply and it needs public roots. Before a binding, T with the per-pod CA alone failed everything, and T with the bundle reached public repositories only as an opaque tunnel. P and L served public fetches through `netagent` and refused a push. A warm fill and a mirror route worked on all three once the host was in `netagent`'s data, and T also needs that host in the CA. Read after push was not implemented or run.

**Cost** (`t7/`), two rounds of three 64 MiB clones, ten `ls-remote`, and one 64 MiB push:

| | T | P | L |
| :-- | :-- | :-- | :-- |
| `netagent` CPU per block | 689 to 714 ms | 515 to 517 ms | 520 to 541 ms |
| 10 `ls-remote` | 115 to 116 ms | 87 to 90 ms | 87 to 101 ms |
| 64 MiB clone | 472 to 521 ms | 468 to 499 ms | 468 to 491 ms |
| 64 MiB push | 2.53 to 2.63 s | 2.54 to 2.61 s | 2.51 to 2.57 s |
| `memory.peak` | 28.7 to 29.1 MB | 27.9 MB | 27.9 to 30.4 MB |

An opaque tunnel used 54 to 55 ms of CPU and 20.8 to 22.5 MB. Those blocks ran no push and cloned a different repository in about 1,620 ms, so they compare only as an order of magnitude.

**What each loses against the local endpoint:**

- **T:** repository and route enforcement on any host `netagent` does not terminate. That covers path checks, refusal of a push to an uncovered host, the mirror route for unbound public hosts, and the SSH `ERR` message. It needs the per-pod CA on every git client, public roots in git's trust, and the host in the CA before a warm fill.
- **P:** nothing on the rewrite path, but the rewrite is advisory. Holding the limit needs termination or refusal on VCS hosts, which brings back the CA, and go-git, JGit, and libgit2 fail. Git prints `http://` for an `https://` remote, so an agent reads a URL that differs from the configured one.
- **L:** `gh` and `glab` do not work.

**Text that would change in `egress-and-git-design.md`:**

- **T:** "Local git endpoint": the opening sentence on `127.0.0.1:3129`, **Redirects** (no loopback form), **LFS** (derives from the unrewritten remote), and the whole-section framing that git goes to a loopback endpoint. "Write scope" step 2 ("plain HTTP to the loopback endpoint"), the adapter clone bullet (`http://127.0.0.1:3129/...`), and **What the limit covers**, which today refuses smart HTTP on the egress endpoint. "Git configuration": the opening claim that the config is identical for every host if per-host SSH rules are rendered, the config block, **Paths**, **SSH remotes**, **Overrides**, and **`pushurl`**. "Fixed endpoints" loses 3129. The trust section must reconcile `GIT_SSL_CAINFO`, which overrides `http.sslCAInfo`.
- **P:** "Local git endpoint": the opening sentence becomes a proxy listener reading absolute-form, and **Redirects** rewrites to `http://host`. "Write scope" step 2 and the adapter clone bullet change. "Git configuration": the config block, **Paths**, **SSH remotes**, **Remotes the rewrite misses**, and **Overrides**, which must then state termination or refusal of VCS hosts. **What the limit covers** needs absolute-form served on the git listener.
- **L:** none, and `gh` stays broken.

**Alternatives that keep the loopback endpoint** (`t8/`), none of which works unchanged:

- `GH_REPO`: only `pr create --head` and `pr list` work. `pr view`, `status`, and `checkout` fail.
- `gh repo set-default` and `gh-resolved`: fail, because the remote is still unrecognised.
- A wrapper with no git config for `gh`: `pr view` works, and `pr create` and `checkout` fail on git.
- `pushInsteadOf` alone: `gh` works; fetch gets 403 and push went through the proxy and failed.
- A loopback fetch URL with the real URL on the push line: `gh` works through its push-URL fallback and fetch works; push gets 403.
- A hostname with a port (`http://host:3129/o/r`): `gh` parses it, and a short loopback form fails. Git against that form was not run.

**Recommendation for Part A: T, with the bundle, for VCS-covered hosts.** T is the only candidate where `gh`, `glab`, go-git, JGit, and proxied libgit2 all work, and its enforcement on those hosts holds with the config removed and for a token the runtime holds. P gives `gh` and `glab` the same results, and it holds that enforcement only with a second CONNECT rule that needs the CA anyway. T costs about 35% more `netagent` CPU on git, about 2.7 ms per `ls-remote`, the public roots in git's trust, CA coverage of each VCS host before a warm fill, the design edits above, and a libgit2 hang on a constraint mismatch. T also loses enforcement on unbound hosts: the agent's credential reaches them and a push is not refused. A hybrid of T on covered hosts and the plain path for unbound hosts was not measured.

### Part B: default-allow deny preset

The prototype's rules are data (`gitopt/t/policy-deny.json`, `policy-r-*.json`). GraphQL that fails strict parsing is refused, the printed document is forwarded, and the forwarded-header allowlist is gone. Only the binding's credential headers are stripped.

**1. Attack rows** (`raw/gitopt/attack/`): the results are the same on both flavours, and on `ghe.com` in `review/attack/`.

| Rows | Refused | Reached, not a denied action | Reached with credential, denied action |
| :-- | :-- | :-- | :-- |
| Original 522 | 340 | 181, plus 1 denied action with no credential through an opaque tunnel | 0 |
| Deny-specific 708 | 642 | 66 | 0 |

The first pass let `POST /api/graphqlx` carrying `addPullRequestReview` reach the stub, because the stub treats any path containing `graphql` as GraphQL. The filter now refuses such paths (`raw/gitopt/pass1/`). Of the 66, 48 are preset neighbours (see item 4 of the review rows), 16 are expected passes, and 2 are a block-string differential where the stub saw the printed query. Two of the 642 are one false positive on two protocols: `PUT .../contents/pulls/7/merge` matches the `...` suffix pattern. With the fixed matcher the totals become 992 refused (including the false positive) and 237 reached, not a denied action, on GHES, github.com, and `ghe.com`. The 1 denied action with no credential is unchanged on every matcher. Every reaching row is listed in the raw files. On HTTP/1.1 under the decoding matcher, the groups are `deny-neighbour` 24, `rest-path` 13, `rest-route` 12, `rest-override` 12, `rest-method` 12, `control` 12, `deny-allow` 8, `gql-field` 7, `gql-evasion` 5, `transport` 4, `reuse` 4, `h1-framing` 3, `gql-malformed` 3, `bound` 2, `gql-differential` 1, `deny-differential` 1, `chunked` 1, and `framed` 1. HTTP/2 repeats most of them. This matcher decodes paths, so the review's concern did not reproduce for `%` spellings. It did reproduce for format suffixes, as below.

**2. `gh` command families** (`raw/gitopt/review/t12/`, final matcher): 103 commands on each of Alpine and Debian, GHES and github.com, gave the same counts: **23 denied actions refused, 0 denied actions missed, 0 other actions refused**, 69 writes forwarded, and 11 with no write at the stub. Of those 11, 4 are reads and 7 are stub limits (6 `project` commands and `discussion create`, which fail on the stub's answers with no filter refusal). The 8 project mutations sent by name through `gh api` were all forwarded (`review/t19/`). `gh browse` sends `HEAD`. `gh release upload` goes to `uploads.github.com`, which had no binding, so it sent the placeholder and needs a binding. The first pass with the decoding matcher ran 100 commands with 0 over-refusals and 0 misses. Coverage is of the commands listed in `gitopt/t/t12.sh` rather than every flag.

**3. Version drift** (`raw/gitopt/t14v2/`), 12 rows per version and mode, on GHES and github.com, on the Alpine client:

| `gh` | Deny: refused other than merge | Allowlist: refused other than merge |
| :-- | :-- | :-- |
| 2.72.0, 2.73.0 | 0 | 8, including `pr create --reviewer --assignee` (query field `user`) |
| 2.86.0, 2.87.0, 2.93.0, 2.94.0, 2.103.3 | 0 | 6 (`pr edit`, `pr comment`) |

Under deny, merge was refused on all 14 merge rows across versions and flavours. Two rows on github.com, 2.86.0 and 2.87.0 `pr edit` with metadata, exit 1 with "'alice' not found" and no filter refusal, which is a stub limit. The allowlist here includes the `Time-Zone` fix. The fixed matcher ran only on 2.72.0 and 2.103.3, with 8 writes forwarded and merge refused on each (`review/t14/`).

**4. Parse cost and stability** (`raw/gitopt/b4/`): the 159 documents in the corpus are all idempotent under printing, and a graphql-js comparison of each original and printed form found 0 differences. In 6, definitions are reordered (fragments after operations), so the bytes change. A second printing changes nothing. Every `gh` command above ran with printing on.

| Body | Allowlist | Deny with printing |
| :-- | :-- | :-- |
| `pr create`, 353 B | 9.3 µs | 11.6 µs, 8.9 KB |
| `status` | 53 µs | 68 µs, 56 KB |
| The benchmark's `largest` case, 2,388 B | 81.5 µs | 121 µs, 108 KB |
| 65 KB PR body | 699 µs | 805 µs, 635 KB |
| 258 KB of `<` at the bound | 5.8 ms | 6.3 ms, 1.9 MB |
| 14 KB of tokens | 1.6 ms, 2.0 MB | 2.15 ms, 2.35 MB |
| 256 KB of tokens | 33 ms, 40 MB | refused at 15,000 tokens in 5.9 ms, 6 MB |
| REST GET, no body | 0.87 µs | 1.05 µs |

Paired end-to-end latency at 5 pairs per second, 60 pairs, deny against an unfiltered host: the p50 difference is 0.50 ms at 209 B, 1.48 ms at 2,388 B, and 2.45 ms at 65,784 B, and the p90 difference is 2.4 ms, 3.1 ms, and 6.9 ms, so the numbers are noisy. `netagent` `memory.peak` reached 41.7 MB. An earlier finding stands: 65,536 `<` characters still print to more than 256 KiB.

**GitLab preset** (`raw/gitopt/t13/`, `review/t13/`): The denied routes were refused in every spelling tried, `glab mr approve` and `merge` were refused, and `glab mr revoke` was refused as unapprove. `mr create`, `view`, `list`, `note`, `update`, `close`, and `issue create` passed. `review/t13/strict.table` labels 18 rows `OVER` because its expected list does not know them: they are `mr revoke` and 17 text-rule rows. Two of the 17 are the false positives below. The text rule refused `/merge` and `/approve` in JSON, escaped, CR-separated, query, form, note, and GraphQL forms. It also refused prose in a line starting `/merge the two lists` and a `/merge` inside a code block, and it left `/usr/bin` and `/assign_reviewer` alone. A commit comment route has no rule, so its text passed. Whether GitLab would execute any of these is unknown.

**Recommendation for Part B:** use the deny preset with the fixed matcher. Over the versions run it refused no `gh` write other than the denied ones, where the allowlist refused 6 to 8 of 12 rows. Its costs are 24% to 49% more filter CPU on the `gh` documents benchmarked, a printed body that differs from the client's in 6 of 159 documents, and the coverage gaps below. The twin routes in item 4 of the review rows need a decision, because the extended preset also refuses `gh repo sync`, `gh api` contents writes, and `gh api` branch protection writes.

### Review rows

Matchers compared (`raw/gitopt/review/compare.txt`): `literal` is a control, `decode` is the matcher above, `strict` is the fixed matcher, and `strictext` is `strict` with the extended preset. The stub proves forwarding only for all of these rows.

**1. Path spellings**, where R means it reached the stub and `-` means refused:

| Row | `literal` | `decode` | `strict` |
| :-- | :-- | :-- | :-- |
| `.../r%65views`, `.../m%65rge`, `appr%6Fve`, `/graphq%6C`, trailing slash, `%2F`, double-encoded | R | - | - |
| `.../reviews.json`, `.../merge.json`, `.../git/refs.json`, GitLab `approve.json`, `unapprove.json`, `merge.json`, and `rebase.json` | R | **R** | - |
| Undeclared escape on an allowed route | R | R | - |

The fixed matcher refuses any `%` outside a declared encoded segment, a backslash, a `;`, and dot segments, drops empty segments and trailing slashes, strips format suffixes for matching, and forwards the canonical path. Legitimate traffic still passed: 0 other refusals in the 103 commands, `glab` with `acme%2Fwidgets`, nested groups, and lower-case `%2f`, and labels, contents, branches, and tags with escapes.

Evidence on host decoding exists for GitLab's framework only. GitLab's Gemfile pins `grape ~> 3.2` and `rack ~> 2.2.9` (`review/gitlab-gemfile-grep.txt`), and a Grape 3.3.5 app built to match its routes routed `appr%6Fve`, `%61pprove`, a trailing slash, `//`, and `approve.json` to the approve handler, and returned 404 for `.xml`, upper case, and `;` (`review/routers-grape.txt`). That is the framework rather than GitLab's whole application. The app has a `graphql` route only under its version prefix, so its `/api/graphql` rows carry no information. A generic Rails 8.1.4 router routed a trailing slash, `//`, and `.json`, and returned 404 for `%65` (`review/routers-rails.txt`). GitHub's stack is unknown, and nothing here says how it reads these.

**2. Inline strings against body patterns**: a raw-bytes pattern (`literal`) missed all of the newline, CR, unicode-escape, block-string, list, variable, fragment, and alias rows. Walking the parsed document (`decode`) caught those, and it missed a variable default, a directive argument, and a nested field argument. The fixed walk (`strict`) caught those too, and the benign text row passed on every matcher. These rows ran on the GitLab preset. The GitHub preset has no text rule, so they all reach the stub there by design.

**3. Coverage**: GHES on `/api/graphql` and `/api/v3/...` and `api.acme.ghe.com` on `/graphql` and `/repos/...` refused denied mutations and routes, and `gh` with `GH_HOST=acme.ghe.com` created a PR and was refused on review and merge (`review/t17.out`). Unmatched paths: `/graphql/foo` and `/api/foo` reached the stub under `decode`, and `strict` refused them on each flavour tried. `/graphql2`, `/graphqlx`, `/api/graphqlx`, and `/api/v3/graphql` were refused under both.

**4. REST twins**: from GitHub's OpenAPI description (`rest-api-description`, `info.version` 1.1.4) and `schema.docs.graphql`, `raw/gitopt/review/routes.txt` lists 72 write routes under `/repos` and 48 mutations by a name search, with their source. 94 of those 120 reach the stub under the owner's preset. Those with a review, merge, ref, protection, or ruleset effect include:

- **Review:** `PUT` and `DELETE .../reviews/{id}` and `PUT .../dismissals`.
- **Merge:** `PUT .../pulls/{n}/merge-async` and `POST .../merge-upstream`.
- **Ref:** `PUT` and `DELETE .../contents/{path}`, `POST .../git/commits`, `trees`, and `tags`, and `POST .../branches/{b}/rename`.
- **Protection:** every `.../branches/{b}/protection*` write, environments, and `POST .../actions/runs/{id}/pending_deployments`.
- **Ruleset:** `POST`, `PUT`, and `DELETE .../rulesets`.
- **Mutations:** create, update, and delete for `BranchProtectionRule` and `RepositoryRuleset`, and `approveDeployments` and `rejectDeployments`.

The extended preset (76 REST rules and 26 mutations, against 11 and 18) refused 95 of the 120. The 25 that still reach are the request-review routes and mutations outside the listed effects, such as `resolveReviewThread`, `closePullRequest`, and environment and deployment mutations. It refused 3 more `gh` rows: `repo sync`, `gh api` contents, and `gh api` branch protection. The classification is by route name and manual reading, so it is a starting list rather than a proof of completeness. GitLab has no equivalent list here.

**5. Override headers**:

| Header | Preset host | Generic bound host, no preset |
| :-- | :-- | :-- |
| `X-Original-URL`, `X-Rewrite-URL` | refused | forwarded |
| `X-HTTP-Method-Override` | forwarded, unless the effective method hits a denied route | forwarded |
| GitLab `Sudo` | forwarded; stripped only under `strictext` | forwarded |
| Client `Authorization` beside the binding on GitLab | stripped | not measured |

Neither `gh` nor `glab` sent any of them in any captured request. Neither binary contains those header names; `Sudo` matches only Go's runtime `Sudog` (`review/source-headers.txt`, `t10/headers.txt`). A grep of the `glab` Go module did not run, because the module needs a newer toolchain than the image had. The binary check stands in for it.

### Not run

- Any GitHub or GitLab host, permission check, or quick action.
- `gh` on amd64, macOS, or Windows, the .NET SDK image, and version drift on the Debian client.
- A `glab` fork flow, read after push, and git against `ghe.com` beyond the stub.
- A T and P hybrid, and a GitLab route inventory.

### Harness defects

- No timeouts in the first runs, which caused the 8-hour stall.
- A failed stop left false rows in an earlier pass, whose files are under `raw/gitopt/earlier/` and `earlier2/`.
- `$RC` was overwritten by the step helper, which invalidated the first `curl` POST rows. `rm` of a live log lost the egress entries of one pass. Both were fixed and rerun.
- The route-push rows on P and L reused a branch name, so the push ended non-fast-forward after reaching the upstream.
- The Grape graphql rows are not meaningful, because that app mounted the route under its version.

### Cleanup

0 `gh-` containers, networks, volumes, and images remain (`raw/gitopt/cleanup.txt`). `podium-postgres` and `podium-minio` were not touched and are still running. Public base images remain, because their names lack the prefix. Docker build cache for the `gh-` images was not pruned, because a prune cannot be limited to them.

## UC-4 follow-up: the final preset, the bounds, and a runtime-held token

This section tests `user-credential-delivery-design.md` revision 3, sections UC-4 and "Interactions with VCS pools". It leaves the sections above as they were written, and it changes numbers only where it says so. Raw output is under `raw/gitopt/uc4/`. The evaluator is `gitopt/go/cmd/netagent/uc4.go`, and the presets are data in `gitopt/t/uc4-pol.py` and `policy-uc4.json`. **The stub proves forwarding only.** A row that reaches the stub says nothing about whether a host would accept the spelling, grant the permission, or perform the action.

The rows ran under candidate T. Every command had a `timeout` inside its container and a watchdog on the docker client.

### Readings the text left open

The prototype had to choose in these places, and a different choice changes the rows named:

- A REST rule in the family table applies to any method other than `GET`, `HEAD`, and `OPTIONS`. A trailing `**` matches zero or more segments, so `{repo}/git/refs/**` catches `POST .../git/refs`.
- `gitlab-deny` rules are rooted at `projects/*` below `/api/v4/`. The approval rule routes, and the `refuseDecoded` list beyond `merge_requests`, are the prototype's choices.
- The quick action pattern is `(?i)^\s*/(approve|lgtm|unapprove|merge|rebase|submit_review|target_branch|run_pipeline)\b`. The text says "with aliases" and names none. `lgtm` comes from the text's example, and the alias list is unverified.
- Body patterns and `denyBodyKeys` apply to GraphQL mutations only, because the text says a query is not checked further.
- Selection depth follows fragment spreads, and a cycle is refused. Value depth counts the leaf, so 31 nested lists and a scalar pass. Root fields are bounded per mutation, as `user-git-identity-design.md` states.
- Concurrent checks count inspected requests only. Past 4, a request gets 429 with `Retry-After`.
- A form write is read even when no body rule applies, because `_method` in a form cannot be refused otherwise. The "Buffering" paragraph does not list this case.

### 1. The presets as written

`raw/gitopt/uc4/rows1.table` holds 2,415 rows, each with what the text predicts. 2,403 matched. The 12 others are harness errors: 6 are a bare `?` that curl removes (a raw client confirms the refusal, `t23.txt`), 3 are the value depth reading above, and 3 are an off-by-one in the harness's JSON depth builder (`t23.txt` shows 32 passes and 33 is refused).

- **The false positive.** `PUT .../contents/pulls/7/merge` is still refused under the extended preset, now by `{repo}/contents/**`. That is the content family applying to a file write. With the content family removed, the same request reaches the stub and `PUT .../pulls/7/merge` is still refused (`t24.txt`). The merge rule no longer matches it. `POST .../issues/pulls/7/merge` and `PUT .../issues/7/merge` also reach the stub, and the suffix matcher refused them.
- **`graphql-tools`.** `GET` and `POST` on `/repos/acme/graphql-tools/...` reach the stub with the credential on GHES, github.com, and `ghe.com`, and so do `/repos/acme/graphql`, `/repos/graphql/graphql-js`, `/users/graphql`, and `/search/graphql`. `PUT /repos/acme/graphql-tools/pulls/7/merge` is refused. `/graphql-tools`, `/graphql2`, `/graphql/foo`, `/api/foo`, `/api/graphqlx`, and `/api/v3/graphql` are refused on each flavour. The stub answers 400 for a repository it does not know, so `gh api repos/acme/graphql-tools` exits 1 after forwarding.
- **`/repositories/{id}`.** Every REST rule below `{repo}` refused under both roots: 30 concrete routes, 2 roots, 4 write methods, and 3 flavours. `GET` on each was forwarded.
- **Mutations.** All 29, by name and by alias, were refused on each flavour, including `createLinkedBranch`, `updateEnvironment`, and `deleteEnvironment`.
- **OpenAPI list.** 96 of the 120 are refused and 24 reach on each flavour (`routes.table`). The run before had 95 and 25. Two environment mutations moved to refused, and `POST .../issues/{n}/suggestions/{id}/approve`, which the suffix matcher caught, now reaches. `createEnvironment`, `pinEnvironment`, and `reorderEnvironment` reach, while `{repo}/environments/**` refuses the REST twin.
- **Credential family.** `POST /user/keys` and `POST {repo}/keys` are refused under both roots. `DELETE /user/keys/1` and `DELETE {repo}/keys/1` reach, because the text names no tail. `gh gpg-key add` and `gh ssh-key delete` reach. On GitLab, `POST /api/v4/deploy_keys` and `DELETE /api/v4/user/keys/1` reach.
- **Paths that reach as written.** `PUT .../merge.foo` and `.../merge.json.json` reach, because only the listed suffixes are stripped, once. `PUT .../branches/a/b/protection`, `.../branches/main%2Fprotection`, and `.../labels/x%2Fpulls%2F7%2Fmerge` reach, because `github-deny` has no `refuseDecoded`. On GHES, `PUT /repos/acme/widgets/pulls/7/merge` with no prefix reaches, because rules apply below the prefix. On GitLab, the unencoded `POST /api/v4/projects/acme/widgets/merge_requests/7/approve` reaches. `OPTIONS` on a denied path is forwarded.

Router rows for those spellings (`routers-grape.txt`, `routers-rails.txt`). The Grape app uses GitLab's pins, and the Rails app is generic. Neither says what GitHub runs.

| Path | Grape 3.3.5 | Rails router |
| :-- | :-- | :-- |
| `merge.foo` | 404 | **200, to the handler** |
| `merge.JSON` | 404 | 200, to the handler (netagent refuses it) |
| `merge.json.json` | 404 | 404 |
| Unencoded `projects/acme/widgets/...` | 404 | not run |
| `branches/main%2Fprotection` | not run | 404 |
| `branches/a%2Fb/protection` | not run | 200 (netagent refuses it) |
| `branches/a/b/protection` | not run | 404 |

### 2. Envelope, overrides, and re-encoding

All rows matched the text on HTTP/1.1 and HTTP/2, on each flavour.

- **Envelope.** A top-level array, string, number, or null, a non-string or absent `query`, and `variables` as a string, array, number, or boolean are refused. `variables` as an object, null, or absent passes.
- **JSON.** A duplicate key at the top, under `variables`, deep in a list, and through `query` is refused. So are `Query`, `Variables`, `operationname`, invalid UTF-8, a lone high or low surrogate, and data after the object. A paired surrogate passes, and so does an `extensions` key.
- **Re-encoding.** `{ "a" : 1e2 , "b" : "A\/x" , "big" : 1234...7890 }` reaches the stub as `{"a":1e2,"b":"A/x",...}` with number text preserved. A duplicate key under `variables`, which passed the earlier prototype when bytes were forwarded verbatim, is now refused. On GitLab a REST JSON write is re-encoded too, and a GitHub REST write streams unchanged.
- **Overrides, on preset hosts and a generic rule set.** `X-HTTP-Method-Override`, `X-HTTP-Method`, `X-Method-Override`, `X-Original-URL`, `X-Rewrite-URL`, `X-Forwarded-Path`, `X-Forwarded-Prefix`, `_method` in a query, and `_method` in a form are refused on an allowed read and an allowed write, including an empty value. `Upgrade`, `HTTP2-Settings`, `Sec-WebSocket-Key`, and `CONNECT` inside the tunnel are refused on the preset hosts tried. A binding with no rules forwards the override headers and `%` paths.
- **What still passes.** `X-Forwarded-Uri`, `X-Envoy-Original-Path`, and `X-Original-Method` are on no list and are forwarded, where the earlier prototype refused the first two. `_method` inside a multipart body streams to the stub on a generic rule set.
- **Header stripping.** `Cookie`, `X-Api-Key`, `Job-Token`, `Deploy-Token`, and `Proxy-Authorization` do not reach the stub, and the binding's credential replaces `Authorization` or `Private-Token`. `gitlab-deny` strips `Sudo`, and `github-deny` forwards it.
- **A row that changes meaning.** `POST /graphql/` and `//graphql` now canonicalize to the GraphQL path and are checked and forwarded as `/graphql`. They were refused by spelling before.

### 3. Bounds

The corpus (`corpus-bench.txt`) holds 166 documents: the 159 from before and 7 strings added from this run, as the filter printed them. **All 166 pass the design's bounds, with no refusal by a bound, a parse, one operation per document, or the mutation root rule.** 5 are refused by a denied name.

| Measure | Largest seen | Bound |
| :-- | :-- | :-- |
| Tokens, by the parser's lexer | 698 | 4,096 |
| Selection depth, with spreads followed | 14 | 32 |
| Mutation root fields | 1 | 16 |
| Value depth | 2 | 32 |
| Document bytes | 7,180 | 65,536 |

The design's figure of about 608 tokens came from a regex count. The boundary rows on GHES, github.com, and GitLab:

| Bound | Passes | Refused |
| :-- | :-- | :-- |
| Tokens | 4,096 | 4,097, 400 |
| Selection depth | 32 | 33, 403 |
| Fragment chain | 15 plus 15 levels | 20 plus 20 levels, and a cycle, 403 |
| JSON depth | 32 | 33 and 40,000, 403 |
| Mutation root fields | 16 | 17, 403 |
| Query root fields | 200 (no bound) | not applicable |
| Body | 524,288 | 524,289, also chunked, 413 |
| Document | 65,536 | 65,537, 413 |

Other limits, on github.com only:

- **Body read.** A body sent at 2 KB/s got 408, with 32.0 s total including connection setup.
- **Concurrent checks.** With 6 slow bodies at once, 4 got 200 and 2 got 429.
- **Check budget.** With the budget set to 1 µs, a request got 503. At 100 ms the budget was never reached: the worst case benchmarked below is 10.5 ms.
- **Head read, 10 s.** A partial head got 400 at 10.0 s. A bare LF head also waited 10.0 s, where `user-git-identity-design.md` says it is refused at once. The prototype does not implement "at once".

`gh` bodies pass where the 256 KiB bound refused them: 65,536 `<`, 65,536 emoji, and 500,000 bytes.

Check cost at 2 CPUs, 200 iterations, so this is noisier than the earlier table:

| Body | Time | Allocated |
| :-- | :-- | :-- |
| `pr create`, 353 B | 19.6 µs (11.6 before) | 11 KB |
| `status` | 68 µs (68) | 60 KB |
| `largest`, 2,388 B | 120 µs (121) | 106 KB |
| 65 KB PR body | 600 µs (805) | 584 KB |
| 258 KB of `<` | 2.7 ms (6.3) | 1.7 MB |
| 4,096 tokens | 1.09 ms | 1.4 MB |
| Depth 32 | 36 µs | 22 KB |
| 64 KiB document, mostly a comment | 695 µs | 476 KB |
| 512 KiB body | 3.65 ms | 5.2 MB |
| 40,000 JSON nodes in variables | 10.5 ms | 7.5 MB |
| REST `GET` | 2.1 µs (1.05) | 1.1 KB |

`netagent` `memory.peak` under these bounds was not measured in this run.

### 4. Reruns, and what changed

**Attack rows** (`attack/`), the same 1,230 rows on each of GHES, github.com, and `ghe.com`, with the same results on each:

| Matcher | Refused | Reached, not denied | Denied action, with credential | Denied action, no credential |
| :-- | :-- | :-- | :-- | :-- |
| Earlier `decode` | 982 | 247 | 0 | 1 |
| Earlier `strict` | 992 | 237 | 0 | 1 |
| **Final** | **1,063** | **162** | **4** | **1** |

The final row was measured on GHES with `other.example.test` unbound. In the runs with that host bound to a generic rule set, on all three flavours, the row with no credential is refused, giving 1,064, 162, 4, and 0. The extended oracle adds 220 rows for the added mutations, giving 1,450 rows: 1,284 refused, 162 reached, 4, and 0. All 220 added rows are refused.

**The 4 are 2 rows on 2 protocols, and both are new since `strict`:**

- `query { ...F } fragment F on Mutation { addPullRequestReview(...) }` reaches the stub as a query, because "a query is not checked further". `query{mergePullRequest(...)}` does too. Whether a host validates this away is unknown here. None of the 166 corpus documents contains `on Mutation`, so a refusal of that fragment in a query costs `gh` and `glab` nothing.
- `POST /repos/acme/widgets/graphql` with a denied mutation reaches the stub, because anchoring stopped checking the last segment. The stub treats any path holding `graphql` as GraphQL. GitHub's OpenAPI description has no path with `graphql` in it, and that is the only evidence here on whether such a route exists.

Two other rows reach that `strict` refused: an unused fragment on `Mutation`, and an `extensions` key. 41 row labels reach under `strict` and are refused under final, mostly the extended families and the override refusal (`attack/diff-strict-final.txt`). 39 rows the tool expected to pass are refused, and those are by the text: the neighbours now in a family, 2 controls that hold two operations or a root fragment, 2 document bound rows, and a subscription.

The 162 by group on GHES, both protocols together: `deny-neighbour` 20, `control` 20, `rest-method` 16, `gql-evasion` 14, `rest-route` 14, `gql-field` 14, `deny-allow` 14, `rest-path` 12, `reuse` 9, `rest-override` 9, `transport` 6, `h1-framing` 4, `h2` 3, and the rest 7.

**The 103 command matrix** (`t12/`), identical on Alpine and Debian:

| Preset | Forwarded | Denied refused | Other refused | Missed | No write |
| :-- | :-- | :-- | :-- | :-- | :-- |
| Earlier `strict` | 69 | 23 | 0 | 0 | 11 |
| Earlier extended | 66 | 23 | 3 | 0 | 11 |
| **Final, GHES** | **63** | **23** | **6** | **0** | **11** |
| **Final, github.com** | **64** | **23** | **5** | **0** | **11** |

**Every command the final preset refuses:**

- The 23 expected: `gh pr review` (3 forms), `gh pr merge` (7 forms), `gh pr update-branch`, `gh pr revert`, and `gh api` review, merge, `update-branch`, `merges`, ref writes, and the three GraphQL mutations.
- In the matrix beyond those: `gh issue develop`, `gh repo sync`, `gh repo deploy-key add`, `gh api -X PUT .../contents/...`, and `gh api -X DELETE .../branches/main/protection`.
- On GHES only, **`gh release upload`**, because `/api/uploads/...` falls in the API family with no flavour path owning it. It also carries a query string and a non-JSON body, so a flavour path alone would not admit it. On github.com it goes to `uploads.github.com`, which had no binding.
- Outside the matrix (`t12/x-*`, 22 commands, identical on Alpine, Debian, and both flavours): `gh ssh-key add`, **`gh secret set --env`, `gh secret delete --env`, `gh variable set --env`, and `gh variable delete --env`**, and `gh api` with 17 root fields, a query string on a write, a form body, or `repositories/123/pulls/7/merge`.
- `writeForm` refused no `gh` or `glab` command in the matrix. It refused only the `gh api` rows written to test it.
- `gh repo edit --enable-auto-merge` and `gh secret set --org` reach.

**Version drift** (`t14/`), all 7 versions on Alpine and Debian, on both flavours, 12 rows each: 8 writes forwarded, merge refused on both flavours, and 0 others refused, at every version. The only non-zero exit other than merge is `pr edit` with metadata on 2.86.0 and 2.87.0 on github.com, with no filter refusal, which is the stub limit recorded above. This extends the earlier run from 2 versions on Alpine with no change.

**GitLab** (`t13/`, 67 rows): 8 forwarded, 37 denied refused, 20 refused beyond the table's expected list, and 2 with no write. The run before had 10 and 18. The 2 that moved are the commit comment, because a body pattern now applies to every write, and `/target_branch release`, because the text names it. `glab mr approve`, `revoke`, `merge`, and `rebase`, and `glab mr create` and `note` holding `/merge`, are refused. `glab mr create`, `view`, `list`, `note`, `update`, `close`, and `issue create` pass. Of 272 extra rows in `rows1.table`, these are new results:

- `reset_approvals`, `cancel_merge_when_pipeline_succeeds`, approval rules, commits, files, branches, `protected_branches`, `deploy_keys`, and token rotation are refused, by project path and project number.
- `merge_when_pipeline_succeeds` and `auto_merge` are refused at any depth, through an escaped key, in upper case, and in a GraphQL variable.
- `acme%2Fwidgets%2Fmerge_requests%2F7%2Fapprove` is refused on its route word. That row was marked [design] in the text.
- The false positives remain: `/merge the two lists` and `/merge` in a code block. ` /merge` and `​/merge` pass, because `\s` in RE2 is ASCII. `see /merge` and `/merge_requests/1` pass.

**Tokens** (`token-audit.txt`): 6 findings, all placeholders on github.com and `uploads.github.com`. No real token and no mint reached a wrong host.

### 5. A runtime-held token under T

`gitForward` now reads a per-host session record (`t/session-*.json`), and `t21.sh` ran 27 rows per state (`t21/`). The origin's log gives the credential it saw.

| Session state | Header token equal to current | Helper or URL token | No credential | A different token |
| :-- | :-- | :-- | :-- | :-- |
| No session record | mint | mint | mint | mint |
| **All conditions hold, absent credential challenged** | **held, no mint** | **held, after a 401** | **401, git fails** | mint |
| All conditions hold, absent credential minted | **held, no mint** | **mint: git never sends it** | mint | mint |
| Token rotated, runtime has the old one | mint | mint, after a 401 | 401, git fails | mint |
| Binding suspended | mint | mint | mint | mint |
| Flag off | mint | mint | mint | mint |
| No release to the session | mint | mint | mint | mint |
| Two pools, flag on one | mint | mint | mint | mint |
| Two pools, flag on both | held | held, after a 401 | 401, git fails | mint |

The results that matter to the text:

- **Held token, no mint.** The origin saw only `USER-FILE-TOKEN` on `info/refs`, `git-upload-pack`, `git-receive-pack`, and LFS batch, on GHES and github.com. Helper, `http.extraHeader` as `Bearer` or `token`, URL token, and `gh auth git-credential` all work, and LFS and an in-scope redirect work. No token went to any other host: the LFS storage host got none, and the held token reached only `ghes.example.test` and `github.com`.
- **A session without it stays on the mint.** Each of the states listed as mint in every column sends a mint whatever the client presents.
- **A different token.** It is dropped, the push runs as the mint, and git reports success. After a rotation a runtime with an old token keeps pushing, as the mint, with nothing telling it. `git.operation` records `mint-credential-differs`.
- **Ref patterns apply to a held token.** A push to `main`, a mixed push, and a raw `curl` receive-pack to `main` are refused. So are a deletion, a push option, a push outside the set, a push to a read-only repository, and a read outside the set, on GHES and github.com. An origin that would accept the push does not see it.
- **Push options.** The prototype had no refusal before this run. It now refuses the `push-options` capability on both the held and mint paths.
- **Reflection.** A reflected held token is masked in the body and in a header.
- **`gh`.** A file token on the API host passes unchanged, and `pr merge` reaches the stub, as the text says for a host with no rule.

**The text is silent on a request with no credential, and that decides whether the rule works.** Git sends a helper or URL credential only after a 401. If `netagent` mints for an absent credential, the helper never runs, so only `http.extraHeader` delivers the held token. If `netagent` answers 401, every client flow delivers it, but a client with no helper cannot clone at all. That includes any clone the adapter makes without one. The text needs to choose. Another option, not measured, is to attach the held value `netagent` already has.

### Findings for the design text

1. State what a request with no credential gets under `allowRuntimeHeldCredentials`.
2. `gh release upload` on GHES is refused by the family rule, `writeForm`, or both. The flavour needs an uploads path with its own form, or the design states the loss.
3. `{repo}/environments/**` refuses `gh secret set --env` and `gh variable set --env` and their deletes. State this as accepted or narrow the rule.
4. `query` documents can name a denied field or spread a `Mutation` fragment. Refusing a `Mutation` fragment in a query costs no corpus document.
5. `.../merge.foo` reaches, and a generic Rails router sends it to the handler. A rule that matches the stem of the last segment before any dot would catch it. GitHub's behaviour is unknown.
6. `POST .../repos/a/b/graphql` reaches. This is the cost of anchoring. Its effect depends on a route the OpenAPI description does not list.
7. The credential family names no `DELETE` tails, and no GPG or signing keys.
8. `createEnvironment`, `pinEnvironment`, and `reorderEnvironment` reach, where the REST twin is refused.
9. The "Buffering" paragraph needs to admit a form write, or `_method` in a form passes. `_method` in a multipart body passes on a generic rule set.
10. `X-Forwarded-Uri`, `X-Envoy-Original-Path`, and `X-Original-Method` are absent from the shipped lists.

### Not run

- Any GitHub or GitLab host, permission check, or quick action.
- `gh` on amd64, macOS, or Windows.
- A second pod's `cacheUntil` timing on rotation or suspension. Each state is a fresh `netagent` start.
- A pin exception, a guardrail, posture, and `required` pool write refusals, which are control plane behaviour with no prototype.
- `netagent` `memory.peak` under the new bounds.
- The `Grape` rows for branch spellings, and the `Rails` row for the unencoded GitLab path, as the table states.

### Harness defects

- A tool limit moved one foreground command to the background at 180 s. It was stopped, and its `attack` process kept running inside the container for up to 80 s. It overlapped the next github.com pass, whose file is kept as `attack/old-ghcom.contaminated.txt`. It showed 4 extra rows reaching. The rerun with no other process matches the other flavours, and later commands stayed under 180 s.
- Two extended oracle passes hit their own 80 s timeout and were rerun with a longer one.
- `t20.py` had the 3 row errors stated under item 1.
- The earlier logs moved to `raw/gitopt/logs/pre-uc4/`, and this run's are `logs/*-uc4.jsonl`.

### Cleanup

0 `gh-` containers, networks, volumes, and images remain (`raw/gitopt/cleanup.txt`). `podium-postgres` and `podium-minio` are running and were not touched. Public base images and Docker build cache remain.

## Revision 5 run: attach on every git request, the preset as written, the git path rules, and LFS

This section tests `user-credential-delivery-design.md` revision 5 and `egress-and-git-design.md` revision 48. It leaves the sections above as written, and it states each number that changes. Raw output is under `raw/gitopt/uc5/`. The presets are data in `gitopt/t/uc5-pol.py`, and the changes are in `gitopt/go/cmd/netagent/{uc4.go,git.go,main.go}`, `go/cmd/tokensvc`, and `go/cmd/egress`. **The stub proves forwarding only.** A row that reaches the stub says nothing about whether a host would accept the spelling, grant the permission, or perform the action.

Every command had a `timeout` inside its container and a watchdog on the docker client, and every tool call stayed under 180 s.

### 1. Attach on every git request

`netagent` now attaches the current value for a qualifying session, in the mint's form, whatever the client sent. The state file is re-read when it changes, as a binding change would reach a pod. The origin's log gives the credential below, on clone and on push, on GHES. `OLD` is what the runtime holds, and `OTHER` is a different token.

| Session state | No credential | Helper (`OLD`) | URL token (`OLD`) | Header token (`OLD`) | Different token (`OTHER`) |
| :-- | :-- | :-- | :-- | :-- | :-- |
| No record | mint | mint | mint | mint | mint |
| **All conditions hold** | **current** | **current** | **current** | **current** | **current** |
| Rotated, runtime holds the old value | **rotated** | **rotated** | **rotated** | **rotated** | **rotated** |
| Suspended | mint | mint | mint | mint | mint |
| Flag off | mint | mint | mint | mint | mint |
| No release | mint | mint | mint | mint | mint |
| Two pools, flag on one | mint | mint | mint | mint | mint |
| Two pools, flag on both | current | current | current | current | current |

The confirmations asked for:

- **A client with no helper can clone.** It exited 0 with the current value at the origin. Under the comparison form this row failed on a 401 or used the mint, depending on the reading.
- **Rotation takes effect with no action by the runtime** (`held/live.table`). With the helper still holding `OLD`, a push carried `USER-FILE-CURRENT`. After the state file changed, the next push carried `USER-FILE-ROTATED`. After suspension the next push carried the mint, and after reinstatement it carried the current value.
- **A different token never reaches the host.** `OTHER-TOKEN`, `USER-FILE-OLD-HELD`, and `AGENT-OWN` appear 0 times in the origin's log for this run (`hosts.txt`).
- **The enforcement holds under the attached value**, on GHES and github.com, with no change from the mint path. A push to `main`, a mixed push, a deletion, a push option, a push outside the set, a push to a read-only repository, and a read outside the set are all refused.
- **Other hosts get none.** In `hosts.txt` the held values appear only on `ghes.example.test` and `github.com`. An LFS push and a smudge through a separate store host passed, with the store seeing no credential. An in-scope redirect carried the value on the same host. An out-of-scope redirect got 403, and the value had reached only the first host.
- **Reflection.** A reflected value is masked in a body and in a header.

**The Token Service, modelled** (`go/cmd/tokensvc`, `held/ts.txt`):

| Case | Result |
| :-- | :-- |
| Clone and push through `netagent`, qualifying session | 0 Token Service requests |
| Direct `POST /mint`, qualifying session and host | 403 |
| Direct `POST /mint`, a host the session does not qualify on | 200 |
| An image without the attach rule (`-held-rule=false`) | It asks, gets 403, and git gets 403, so nothing pushes as the App |
| An image older than the release's `handling` (`-handling 1`) | It refuses before asking, with 0 Token Service requests |
| No record | 5 mints for a clone and a push |
| Mirror route, qualifying session | Fetch carries `MIRROR-SA` at the mirror, and the push advert carries the current value at the host |

The mirror row's push ended non-fast-forward, because the stub's mirror store is a separate repository. The stand-in reads the same state file as `netagent`, so it does not test the two disagreeing.

**A row the text may want to look at.** A session that stops qualifying falls to the mint. After suspension, a push succeeded as the App, with a credential the runtime never asked for. The text rules this out for a rotated value and a different token, and it does not state it for a suspended binding or a cleared flag.

### 2. The preset as written

`rows2.table` holds 3,045 rows, each with what the text predicts, and 3,033 matched. The 12 others are the same harness errors as the follow-up: 6 of a bare `?` that curl removes, and 6 of the depth rows' builder. A raw client refused the bare `?` in the follow-up, and this run did not repeat it.

**The 4 denied actions with a credential are now 0** on GHES, github.com, and `ghe.com`.

| Text | Result |
| :-- | :-- |
| First-dot stem | `merge.foo`, `merge.json.foo`, `merge.json.json`, `merge.JSON`, `reviews.x`, and GitLab `approve.foo` and `approve.json.json` are refused. `POST .../issues.json`, `.../issues/7/comments.foo`, and `GET .../labels.json` are forwarded with the suffix. A dot in a repository name is not a final segment, so `POST /repos/acme/widgets.js/issues` passes and `PUT .../widgets.js/pulls/7/merge` is refused. |
| `query` documents | A fragment on `Mutation` defined or spread, an inline `... on Mutation`, and a denied field at the root, nested, or in a fragment on another type are refused. A fragment on `Query` passes. |
| Final `graphql` | `POST .../widgets/graphql`, `.../graphql.json`, `.../graphql.foo`, `.../GraphQL`, `POST /repos/acme/graphql`, and the OpenAPI cluster route are refused. `PATCH /repos/acme/graphql`, `PUT .../widgets/graphql`, `GET .../widgets/graphql`, and `POST /repos/acme/graphql/issues` pass. |
| Environments | `PUT` and `DELETE .../environments/prod`, and writes under `deployment-branch-policies` and `deployment_protection_rules`, are refused. `POST` and `PATCH .../environments/prod`, and writes under `secrets/S` and `variables`, pass. **`gh secret set --env`, `gh variable set --env`, and their deletes pass.** |
| `{repo}/branches/**` | `branches/a/b/protection`, `branches/main%2Fprotection`, `branches/main/rename`, and `POST .../branches` are refused. `GET` on each passes. |
| Credential tails | Writes to `/user/keys/1`, `/user/gpg_keys`, `/user/gpg_keys/1`, `/user/ssh_signing_keys`, `/user/ssh_signing_keys/1`, and `{repo}/keys/1` are refused, and `GET` on each passes. On GitLab `POST /deploy_keys`, `DELETE /user/keys/1`, and `POST .../access_tokens` and `.../deploy_tokens` are refused. `DELETE .../access_tokens/1` and `.../deploy_tokens/1` pass, because the text names no tail on those two. |
| Added headers | `X-Forwarded-Uri`, `X-Envoy-Original-Path`, and `X-Original-Method` are refused on preset hosts and a generic rule set. |
| Non-root writes | On GHES, a write to `/repos/.../merge`, `/repos/.../issues`, `/x`, and a relative root is refused, and `GET /x` passes. GitLab is the same. |
| `/api/*` under a root prefix | Refused, including `/api/uploads/...`. |
| Uploads path | **`gh release upload` on GHES passes**, with its query string and binary body. On GHES, `/api/uploads/x` and `/api/uploads/repos/acme/widgets/releases/1` are refused, and `_method` and override headers are refused on the uploads path. |
| GitLab mutations | `commitCreate`, `branchCreate`, and `branchDelete` are refused by name and alias, and `query{mergeRequestAccept}` is refused. |

Path, GraphQL path, and family matching are case-insensitive as the text says, so `POST /GraphQL` on a root flavour is checked as GraphQL and forwarded as sent. The unencoded GitLab `POST /api/v4/projects/acme/widgets/merge_requests/7/approve` still reaches the stub as the text states, and Grape answered 404 in the follow-up.

**OpenAPI list:** 94 refused and 26 reach on each flavour, as the text predicted. The 5 environment secret and variable routes reach, and `createEnvironment`, `pinEnvironment`, and `reorderEnvironment` moved to refused. `POST .../issues/{n}/suggestions/{id}/approve` still reaches.

**Corpus:** the 166 documents are unchanged. The `query` rule refused none of them, and 5 are refused by a denied mutation name, as before.

### 3. Reruns, and what changed

**Attack rows** (`attack/`), identical on GHES, github.com, and `ghe.com`:

| Run | Refused | Reached, not denied | Denied action, with credential | Denied action, no credential |
| :-- | :-- | :-- | :-- | :-- |
| Follow-up, 1,230 rows, generic host unbound (GHES) | 1,063 | 162 | 4 | 1 |
| **Revision 5, 1,230 rows, generic host unbound (GHES)** | **1,069** | **160** | **0** | **1** |
| Follow-up, 1,230 rows, generic host bound | 1,064 | 162 | 4 | 0 |
| **Revision 5, 1,230 rows, generic host bound** | **1,070** | **160** | **0** | **0** |
| Follow-up, extended oracle, 1,450 rows | 1,284 | 162 | 4 | 0 |
| **Revision 5, extended oracle, 1,510 rows** | **1,350** | **160** | **0** | **0** |

The 3 labels that moved to refused, on 2 protocols, are the `query` spreading a `Mutation` fragment, an unused fragment on `Mutation`, and `POST /repos/acme/widgets/graphql`. No row moved the other way (`attack/diff-uc4-uc5.txt`). The 1 with no credential is the same opaque tunnel row as before. The extended oracle's 60 added rows, for the 3 added mutations, are all refused.

**The 103 command matrix** (`t12/`), identical on Alpine and Debian:

| Run | Forwarded | Denied refused | Other refused | Missed | No write |
| :-- | :-- | :-- | :-- | :-- | :-- |
| Follow-up, GHES | 63 | 23 | 6 | 0 | 11 |
| Follow-up, github.com | 64 | 23 | 5 | 0 | 11 |
| **Revision 5, GHES and github.com** | **63** | **23** | **6** | **0** | **11** |

**Every command refused:**

- The 23 expected: `gh pr review` (3 forms), `gh pr merge` (7 forms), `gh pr update-branch`, `gh pr revert`, and `gh api` review, review events, merge, `update-branch`, `merges`, ref writes, and the 3 GraphQL mutations.
- In the matrix beyond those: `gh issue develop`, `gh repo sync`, `gh repo deploy-key add`, **`gh repo deploy-key delete` (new)**, `gh api -X PUT .../contents/...`, and `gh api -X DELETE .../branches/main/protection`.
- **`gh release upload` on GHES no longer refused.** On github.com it goes to `uploads.github.com` with no binding, as before.
- Outside the matrix (`t12/x-*`, 23 commands, identical on both images and flavours): `gh ssh-key add`, **`gh ssh-key delete` (new)**, **`gh gpg-key add` (new)**, and `gh api` with 17 root fields, a query string on a write, a form body, and `repositories/123/pulls/7/merge`.
- **No longer refused outside the matrix:** `gh secret set --env`, `gh secret delete --env`, `gh variable set --env`, and `gh variable delete --env`. `gh variable set --env` sends `POST /api/v3/repositories/1/environments/prod/variables`, which the rule admits under both roots.
- **`gh gpg-key delete` did not confirm:** the stub has no key to list, so `gh` exits 1 before any write. The raw `DELETE /user/gpg_keys/1` is refused.

**Version drift** (`t14/`): no change. All 7 versions on Alpine and Debian, on both flavours, gave 8 forwarded, merge refused, and 0 others refused. The only non-zero exit other than merge is the same `pr edit` metadata row on 2.86.0 and 2.87.0 on github.com, with no filter refusal.

**GitLab** (`t13/`): no change, at 8 forwarded, 37 denied refused, 20 beyond the table's list, and 2 with no write. The 312 rows in `rows2.table` matched apart from the depth rows above.

**Tokens** (`token-audit.txt`): 13 findings, all placeholders. No value reached a wrong host.

### 4. EG's git path rules and LFS

`git/rows.table` holds 139 rows, and all matched. "Refused" means nothing reached the stub.

- **Classification before any filter, and the strict form.** On a covered host, a bound host with no pool, a bound host with rules, and an API host, these are refused with a canonical path error: a trailing slash, `.json`, `//`, `%2d`, `%252d`, upper and mixed case, `info%2Frefs`, `;`, dot segments, encoded dot segments, `INFO/LFS`, a bare `.git/`, and a marker deep in a path. An HTTP/2 spelling and an absolute-form spelling are too.
- **Single `service=`.** No query, two services, a repeat, another value, `Service`, an extra key, an encoded value, and an empty value are refused. `git-upload-pack` and `git-receive-pack` pass.
- **`git-receive-pack`.** A gzip or deflate body is refused with 415, before its commands are read. `push-cert`, a push option, a wrong type, a query, and `GET` are refused. A plain body on an allowed ref is forwarded.
- **Overrides.** All 13 headers on the list are refused. `X-Evil`, `Cookie`, `Private-Token`, and `X-Api-Key` do not reach the stub, and `Git-Protocol` does.
- **Smart HTTP on a bound host with no pool.** A fetch carries no credential: a public repository got 200 with no `Authorization` at the origin, even with one from the client, and a private one got 401. A push, its advert, and an LFS upload are refused. The binding's secret never attaches to git, and a non-git request on the same host carries it.
- **CONNECT gate** (`git/gate.txt`). Before the first generation, CONNECT got 503 for covered, bound, and opaque hosts. After it, all three connected. After a `docker restart` of `netagent`, CONNECT got 503 again until a generation arrived.
- **Terminated marker.** With `github.com` absent from `netagent`'s data and the egress stand-in holding it as covered, CONNECT got 403 with no marker. An opaque host still connected.

**Collateral of classification.** A path holding a marker on an API host is a git request, so it is refused. `GET .../contents/.git/config`, `GET .../contents/docs/info/refs`, `GET .../contents/git-upload-pack.md`, and `PUT .../contents/.git/config` are refused on API hosts. No command in the matrix sent such a path.

**LFS in GitLab's form** (`git/lfs.txt`, `git/lfs2.txt`). The stub returns `<repo>.git/gitlab-lfs/objects/<oid>` on the same host with `Authorization`, and it answers 401 on a transfer without that header.

- **End to end with git-lfs 3.6.0.** A push and a smudged clone passed and the bytes matched. The stub saw the action's value on `PUT` and `GET`. `GIT_CURL_VERBOSE` and `GIT_TRACE` output held the value 0 times, and the batch response at the client held no `Authorization`. A reflected value was masked.
- **Matching.** The value attached only on the same method, host, path, and repository. No value attached for: another object, `PUT` where only `GET` was held, the same object on another repository, an action past its `expires_in` (1 s, waited 2.2 s), or an action whose `href` names another repository. A client's own `Authorization` was replaced where a value was held, and dropped where none was.
- **Path form.** `POST` on a transfer path, an upper-case object ID, no `.git`, a trailing slash, a repository outside the set, and a host that declares no such path are refused.
- **Provider level.** With no live run recorded, `info/lfs` and the transfer path got 403 on a `mint-coarse` host, and `git push` of an LFS object failed.

Three results for the text:

- **"That transfer fails" is not what the client did.** With `Authorization` removed from an action on another host, the store answered 401 and git-lfs did not exit before the step limit. With `authenticated: true` it sent **28,896 `PUT`s in 8 s, and 108,621 in 30 s**. With `authenticated` false it sent 1 `PUT` and 6 batch requests in 8 s. The client and stub were on one machine, so the rate is an upper bound, and a host's store may not answer 401. A tight retry against a provider's store is a cost the text does not state, and a timeout is what ended it here. The value did not reach the store.
- **Only `Authorization` is stripped.** A `Private-Token` in an action's headers reached the client as data (`ACTION-PRIVATE-TOKEN`). The text names `Authorization`, and an action can carry a credential in another header.
- **`_links`.** A lower-case `authorization` under `_links` was stripped and held. git-lfs 3.6.0 ignored `_links` and retried with a backoff, so that row proves the strip at the wire alone.

### 5. Live read

Traffic to providers, all anonymous and from a container on the default bridge, is 9 requests: 5 `GET`s to find pointers and 4 `POST`s to the batch endpoint. No credential was sent. Output is in `live/`, with only signed query strings redacted.

| Request | Target | Result |
| :-- | :-- | :-- |
| `GET` 1 | `raw.githubusercontent.com`, `Unity-Technologies/ml-agents`, `3DBall.onnx` | A binary file rather than a pointer |
| `GET` 2 | `raw.githubusercontent.com`, `mozilla/DeepSpeech` v0.6.1, `lm.binary` | A pointer |
| `POST` 1 and 3 | `github.com/mozilla/DeepSpeech.git/info/lfs/objects/batch` | 200, object error 410, "Object does not exist on the server" |
| `GET` 3 | `gitlab.com` API, `gitlab-org/gitlab-test`, `lfs_object.iso` | A pointer |
| `POST` 2 | `gitlab.com/gitlab-org/gitlab-test.git/info/lfs/objects/batch` | 200, object error 404, "Object does not exist on the server or you don't have permissions to access it" |
| `GET` 4 | `gitlab.com` API, `gitlab-org/gitlab-design` tree | 3 candidate files, not used |
| `GET` 5 | `raw.githubusercontent.com`, `onnx/models`, `mnist-12.onnx` | A pointer |
| **`POST` 4** | **`github.com/onnx/models.git/info/lfs/objects/batch`** | **200, a download action** |

`POST` 1's body was lost to a filter in the harness's pipeline, so `POST` 3 repeated it. The budget then went to GitHub, because the coordinator named GitHub's behaviour as the one held from memory.

**GitHub, measured:** `POST` 4 returned this, with `Content-Type: application/json; charset=utf-8`.

```json
{"objects":[{"oid":"5c688690...1bdd","size":26143,"actions":{"download":{
  "href":"https://github-cloud.githubusercontent.com/alambic/media/158171734/5c/68/5c688690...1bdd?<signed query redacted>",
  "expires_at":"2026-11-03T22:10:24Z","expires_in":3600}}}]}
```

The action host is `github-cloud.githubusercontent.com`, a different host from `github.com`. **The action carries no `header` key**, and the object has no `authenticated` key. This is one anonymous download of one object. An upload, or a request with a token, may differ, and neither ran.

**GitLab, not obtained:** the one object tried returned an error and no action, so no live GitLab action was read. GitLab's form in item 4 therefore rests on the text's source reading, and `info/lfs` stays owed on a live GitLab run.

### Not run

- Any live write, permission check, or quick action, and any live LFS upload or transfer.
- A live GitLab action, and `gh release upload` against a GHES host.
- `upload_url` on `ghe.com`, which the text marks as owed.
- A store's behaviour on a missing header. The 401 is the stub's.
- `gh` on amd64, macOS, or Windows.
- A decision service. The marker and the generation gate ran against stand-ins.
- Read after push.
- The `netagent` head deadline and bare LF rows, and the time and concurrency bounds, which were not repeated from the follow-up because revision 5 did not change them.

### Harness defects

- The stub's release `url` was `/x` and its asset `url` was `/a`, so `gh release delete` and `delete-asset` left the REST prefix on GHES and were refused. That was a stub error, and the stub now returns `/api/v3/repos/...`. Both pass after the correction.
- Restarting the stub gave it a new CA. One matrix pass and one version pass ran against the old trust, failed on TLS, and were discarded and rerun after a fresh `netagent`.
- A zsh glob stopped the first CONNECT gate pass before it ran.
- The stub's log holds the retry rows above, so `logs/origin-uc5.jsonl` is large.

### Cleanup

0 `gh-` containers, networks, volumes, and images remain (`raw/gitopt/cleanup.txt`). `podium-postgres` and `podium-minio` are running and were not touched. Public base images and Docker build cache remain.

## Revision 6 run: what was still marked as design

This section tests `user-credential-delivery-design.md` revision 6, `user-git-identity-design.md` revision 9, and `egress-and-git-design.md` revision 49. It leaves the sections above as written. Raw output is under `raw/gitopt/uc6/`, and the preset deltas are in `gitopt/t/uc6-pol.py`. **The stub proves forwarding only.** No traffic went to any provider in this run. Every command had a `timeout` inside its container.

### 1. git-lfs 3.6.0 against a per-object error

`netagent` now replaces an undeliverable object's `actions` with `error`, as the text says. An undeliverable action is one with a credential on a host `netagent` does not terminate. Requests are counted at `netagent` (`lfs/sweep.txt`).

**422 stops the client, on push and on clone, with no retry:**

| Operation | Git exit | Time | Batch requests | Transfers | What the client prints |
| :-- | :-- | :-- | :-- | :-- | :-- |
| Push | 1 | under 0.1 s | 1, plus 1 locks request | 0 | `[<oid>] the action names lfs-store.example.test, which netagent cannot deliver a credential to: [422] ...`, then `error: failed to push some refs` |
| Clone | 128 | 0.1 s | 2 | 0 | `error: external filter 'git-lfs filter-process' failed`, `fatal: o.bin: smudge filter lfs failed`, and `warning: Clone succeeded, but checkout failed.` |

The earlier run sent up to 28,896 `PUT`s in 8 s on this path. Codes 404 and 500 gave the same exits and counts, and `authenticated` false gave the same result. **A clone's terminal output does not name the host.** The push output does. A session reading only the clone output sees a filter failure, and the message is left to `git lfs logs last`, which was not read here.

**The refusal after 3 consecutive transfer failures** (`lfs/solo*`), on a terminated transfer path. It refuses with 403, and a new batch response resets the count, as the text says.

| Stub's answer to the transfer | Operation | Git exit | Time | Batch | Transfers at the stub | Refused by `netagent` |
| :-- | :-- | :-- | :-- | :-- | :-- | :-- |
| 401 | Push | 1 | 35.9 s | 9 | 27 | 9 |
| 401 | Clone | 128 | 71.9 s | 18 | 54 | 18 |
| 500 | Push | 1 | 35.9 s | 9 | 9 | 0 |
| 500 | Clone | 128 | 71.9 s | 18 | 18 | 0 |

- **The rule stops the tight retry on a 401**, at 3 transfers per batch. The push message is `LFS: refused by the egress operation allowlist: this transfer failed 3 consecutive times; a new batch response is needed`.
- **The client exits on its own retry limit**, 9 batches per object run. A clone costs twice a push because its filter runs the sequence twice. Neither exit comes from the refusal itself, since git-lfs treats the 403 as retryable and asks for a new batch.
- **On a 500 the rule never fires.** git-lfs sends one transfer per batch, and each new batch resets the count. The client stops on its limit with `Fatal error: Server error: <url>`.
- **A variant where a new batch does not reset** sent 3 transfers in total and did not exit within a 30 s step limit, with the same batch cadence.
- **A terminated path with no rule at all did not run**, so this run has no direct comparison for the 27. The 28,896 figure is from a host `netagent` does not terminate.

### 2. The 401 states

A session qualifies when it names the binding, whatever the release or suspension state. With no current value nothing attaches. Each cell is the credential at the origin and its status, the same for all 5 client forms on clone and on push, and for `gh auth git-credential` on push (`held/state-table.txt`).

| Session state | Every client form | Token Service requests | A push succeeded |
| :-- | :-- | :-- | :-- |
| **Suspended** | **none, 401** | **0** | **0** |
| **Flag cleared** | **none, 401** | **0** | **0** |
| **No release** | **none, 401** | **0** | **0** |
| **Two pools, flag on one** | **none, 401** | **0** | **0** |
| All conditions hold | current, 200 | 0 | yes |
| Rotated | rotated, 200 | 0 | yes |
| Two pools, flag on both | current, 200 | 0 | yes |
| No record | mint, 200 | 53 | yes |
| Released but not named (the delegated child) | mint, 200 | 53 | yes |

**None of the 401 states pushes as the App:** the 4 state tables hold 0 mint rows. With no helper, a header, or a different token, git prints `could not read Username ... terminal prompts disabled`. With a helper it prints `Authentication failed`. Path enforcement in those states is unchanged: a push outside the set and a read outside the set are refused with no request to the origin. A push to `main` stops at the advert's 401, so the ref check is not reached there. On a mirror route in the suspended state, a read carried `MIRROR-SA` at the mirror, and a push got 401 at the host.

The first pass of this item got 502 on every row, because the stand-in's route file did not yet hold the digests. It is not in the table.

### Items 3 to 6

`rows.table` holds 403 rows with a prediction from the text, on HTTP/1.1 and HTTP/2, and all 403 matched.

**3. The stem's direction.**

- `POST /graphql.json`, `.foo`, `.json.foo`, and `.JSON`, on github.com, GHES (`/api/graphql...`), and GitLab, are refused with a denied mutation and with an allowed query. The exact path checks the document.
- `/api/uploads/.../assets.json`, `.foo`, and `.bin.foo` are refused, and the exact path passes.
- On a generic rule set with `/graphql` listed, `POST /graphql.json` with a denied mutation is refused by the final segment rule. `PUT /graphql.json` with the same body is forwarded, because that rule is `POST` only and the path is not a listed one. The document is not read on that request. This follows the text, and it is the one suffixed row with a denied name that reaches the stub.
- Under `default: deny` with an allow rule on `/rest/api/*/project`, the exact path passes, and `.json`, `.foo`, and `.json.foo` are refused on `GET` and `POST`.
- A deny on `/files/report.tar` refuses `report.tar`, `report.tar.gz`, `report.tar.gz.bak`, and `REPORT.TAR.GZ`. `report.txt`, `report`, `report.ta`, `report.tarx`, and `x.report.tar` pass.

**4. Carried mutation names.** Both presets carry the union, 36 names.

- `github-deny` on a GitLab host refuses `mergeRequestAccept`, `commitCreate`, `branchCreate`, and `branchDelete`, by name and alias, and the GitHub names tried.
- `gitlab-deny` on GHES refuses the same 13 names tried, and a `query` naming any of them.
- A different mutation and a query pass on each.
- REST fails closed on the prefix: `github-deny` refuses every `/api/v4/...` tried, including `GET`, and `gitlab-deny` refuses `/api/v3/...` and writes to `/repos/...`, and `GET /api/v3/...`.

The 13 are a sample of the union rather than all 36.

**5. The routing override list on every terminated host.** Refused on a host with no rules, a generic rule set, GHES, `api.github.com`, GitLab, and `github.com` with no binding. The rows cover all 6 headers, a lower-case spelling, an empty value, `GET` and `POST`, both protocols, and absolute-form on the no-rules host. `X-HTTP-Method-Override` on a host with no rules is forwarded, because it is not on the routing list. An opaque host forwards the header, since `netagent` sees only a CONNECT there.

**6. GitLab token routes.** With `/**`, `POST`, `PUT`, and `DELETE` on `access_tokens/1` and `deploy_tokens/1`, `.../1/rotate`, `.../1.json`, and `access_tokens.foo` are refused, by project path and project number. `GET` on the collection and on `/1` passes.

### 7. A `restricted` pool's formula hosts

The host is data: a host in `netagent`'s set with no pool, and in the egress stand-in's covered list. The formula itself did not run (`items78.txt`).

| Row on `assets.example.test` | Formula host covered | Host not covered, for comparison |
| :-- | :-- | :-- |
| Fetch of a public repository, client sends a token | passes, no credential at the origin | token at the origin |
| Fetch of a private repository, client sends a token | 401, no credential at the origin | **token at the origin, 200** |
| Push by header or URL token | **refused with 403, recorded in `git.operation`** | **token at the origin, push accepted** |
| Receive-pack advert | refused | 200 |
| Any other request | forwarded, nothing attached | same |

With the egress stand-in holding the host as covered and `netagent` lacking it, CONNECT got 403.

### 8. Route assertion

`netagent` sends `X-Lenny-Route-Assert: <list> <digest>` on CONNECT, from a session record, and it now keeps separate upstream pools for git and everything else. The stand-in compares against a route file it re-reads on each CONNECT.

- **The list is named.** 3 git and 3 API requests on one host made 2 CONNECTs: `git gitdigest1` and `egress egressdigest1`.
- **No shared upstream connection.** The stub saw one connection for git and one for the API, with none shared.
- **A forged client header** on the CONNECT does not reach the stand-in. It saw `netagent`'s own values.
- **A route edit on the git list, with no pooled connection.** Every git request got 502, from a 403 at the stand-in, and API requests passed. Only the edited list stopped.
- **A route edit with idle pooled connections: all 3 git requests got 200 on the old connection, with 0 new CONNECTs.** The assertion is compared at CONNECT, so a pooled connection carries the value past the edit. The text says each client CONNECT gets its own decision and a deny closes the pool. The prototype has no per-client-CONNECT decision, so that rule did not run, and the closure of this window depends on it.

The Token Service's own assertion did not run.

### Reruns

**No number changes from the revision 5 section.**

| Run | Refused | Reached, not denied | Denied action, with credential | No credential |
| :-- | :-- | :-- | :-- | :-- |
| Attack, 1,230 rows, on each of GHES, github.com, and `ghe.com`, generic host bound | 1,070 | 160 | 0 | 0 |
| Attack, extended oracle, 1,510 rows, GHES only | 1,350 | 160 | 0 | 0 |

The unbound-host row and the extended oracle on the other two flavours were not repeated. The reached rows on GHES are the same as in the revision 5 run (`attack/diff-uc5-uc6.txt` is empty).

| Run | Forwarded | Denied refused | Other refused | Missed | No write |
| :-- | :-- | :-- | :-- | :-- | :-- |
| Matrix, 103 commands, Alpine and Debian, both flavours | 63 | 23 | 6 | 0 | 11 |
| Outside the matrix, 23 commands, same | 12 | 0 | 7 | 0 | 4 |
| GitLab, 67 rows | 8 | 37 | 20 | 0 | 2 |

The refused commands are the same list as in the revision 5 section. Version drift was not rerun, since it was not asked for. Audit: 6 findings, all placeholders on github.com and `uploads.github.com`.

### Not run

- Any provider, live write, permission check, or LFS transfer.
- The `gitAccess` formula computed from a pool, and a warm pod's case. Item 7 ran on data.
- A decision service. The marker, the assertion, and the generation gate ran on stand-ins, and the per-client-CONNECT decision did not run.
- The Token Service's assertion.
- `git lfs logs last`.
- A terminated transfer path with no 3 failure rule.
- The 3,045 rows of the revision 5 section, because some predictions there changed under this revision. The 403 rows above replace the ones that changed.
- `gh` on amd64, macOS, or Windows.

### Cleanup

0 `gh-` containers, networks, volumes, and images remain (`raw/gitopt/cleanup.txt`). `podium-postgres` and `podium-minio` are running and were not touched. Public base images and Docker build cache remain.
