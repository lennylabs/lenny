# Evidence for the user git identity design

This folder holds the test evidence behind `designs/user-git-identity-design.md`. `designs/user-credential-delivery-design.md` relies on the same runs and cites this folder, and `designs/egress-and-git-design.md` cites it for the git path under candidate T.

## Contents

| Path | Contents |
| :-- | :-- |
| `spike-gh/RESULTS.md` | The results of the capture and of the later runs against the design text, in full. |
| `spike-gh/raw/` | Summary tables, attack tables, per-case captures, and the output of each test. |
| `spike-gh/go/`, `spike-gh/gitopt/go/` | The test programs: the `netagent` prototype, the recording stub, the attack client, and the origin. |
| `spike-gh/gitopt/t/`, `spike-gh/*.sh`, `spike-gh/*.py`, `spike-gh/policy-*.json` | The drivers, the policies, and the presets as data. |
| `gh-history/out/`, `gh-history/scripts/` | The `gh` mutation history across releases, and the scripts that read it. |
| `spike-ghlive/` | The live GitHub runs of 2026-11-06 for S1, S2, S3, S5, S6, S15, and S16, against organization `lenny-test`: `RESULTS.md`, the scripts, and their output, with no token in any file. |
| `review-repro/` | The scripts a review used to reproduce `gh` and `glab` stopping under the prior `insteadOf` configuration. Their output was not kept. |

## Date and environment

The runs took place on 2026-11-03. They used `gh` 2.103.3, `glab` 1.127.0, gqlparser 2.5.33, and stock git in Linux arm64 containers, under Docker on a laptop with 12 CPUs and 8 GB, with other load running on the same laptop. Clients reached a Go `netagent` prototype on a Docker `--internal` network, and the origin was a recording stub in front of stock `git http-backend`.

**The stub proves forwarding only.** A row that reaches the stub states that the request was forwarded. It does not state that a provider would accept the spelling, grant the permission, or perform the action. No GitHub or GitLab API host received any test traffic. Traffic to a provider consisted of nine anonymous requests for the LFS read in the revision 5 run, whose output is under `spike-gh/raw/gitopt/uc5/live/`.

## How the design documents cite this folder

Citations in `designs/user-git-identity-design.md` and `designs/user-credential-delivery-design.md` resolve as follows, relative to `spike-gh/`:

| Citation | File |
| :-- | :-- |
| `raw/gitopt/...` | `raw/gitopt/...` |
| `t4/table.txt` | `raw/gitopt/t4/table.txt` |
| `deny-ghcom.txt` | `raw/gitopt/attack/deny-ghcom.txt` |
| `source-headers.txt` | `raw/gitopt/review/source-headers.txt` |

The tags **[captured]** and **[measured]** in those documents refer to `spike-gh/RESULTS.md`. Sections of `RESULTS.md` state a base path for their own citations. `attack/`, `held/`, `git/`, `lfs/`, and `t13/` are relative to `raw/gitopt/uc4/`, `raw/gitopt/uc5/`, or `raw/gitopt/uc6/`, as the section names, and `review/` and `t1/` to `t14v2/` are relative to `raw/gitopt/`.

## Changes to copied files

`RESULTS.md` files and raw output are otherwise byte-for-byte copies, and no measured value, row, or number was changed.

- Absolute paths were rewritten to paths relative to this folder. The scratchpad root became `.`, and a path into the repository became `../../..`. The rewrite touched `spike-gh/raw/cost-binary-size.txt`.
- Go sources and `go.mod` files carry an added `.txt` suffix, so a citation of `go/cmd/netagent/filter.go` refers to `go/cmd/netagent/filter.go.txt`. Without the suffix, the repository's `go build ./...`, `go vet ./...`, `gofumpt -l .`, and `goimports -l .` treat these throwaway programs as repository source.
- File modes were reset to `0644`, because these programs are records and are not meant to be run from here.

## Files added during curation

- `spike-gh/raw/netagent-client-view-reqlog.summary.txt` holds request counts derived from the left-out request log. It carries the "2,619" and "234" figures that `RESULTS.md` cites.
- `spike-gh/raw/gitopt/pass1/attack/reached-denied-action-rows.txt` holds the rows, copied verbatim, that record the `POST /api/graphqlx` finding of the superseded first pass.

## What was left out

A citation in `RESULTS.md` of one of these paths does not resolve in this repository. `RESULTS.md` states the result, and the entries below state what remains.

| Left out | Reason | What remains |
| :-- | :-- | :-- |
| `bin/`, `dl/`, `gitopt/bin/`, `run/`, and `gitopt/run/` | Binaries, downloaded releases, npm packages, JGit jars, per-run CA material, and transient state. | The sources and the versions below. |
| `src/` and `gh-history/src/` | Third-party source and schema downloads, read as data. | The versions below. |
| `raw/gitopt/logs/` (252 MB) and `raw/netagent-client-view-reqlog.jsonl` (4.2 MB) | Bulk logs. | The tables, `uc5/hosts.txt`, the token audits, and the summary file above. |
| `raw/gitopt/earlier/` and `earlier2/` | Passes that a failed stop spoiled. | Nothing, as `RESULTS.md` discards them. |
| `raw/gitopt/pass1/` other than `summary.txt` | The first pass, superseded by `raw/gitopt/attack/`. | `summary.txt` and the extracted rows. |
| `raw/attack-ghes.first-run.txt` and `raw/gitopt/t14/` | Superseded by a later run. | `raw/attack-ghes.txt` and `raw/gitopt/t14v2/`. |
| `raw/gitopt/uc4/attack/old-*` and `new-*`, including `old-ghcom.contaminated.txt`, and `raw/gitopt/uc4/t*/` step and output files | Superseded by the revision 5 and revision 6 runs, which supply the numbers the documents state. | `uc4/rows1.table`, `uc4/attack/diff-strict-final.txt`, and each `.table` file. |
| `raw/gitopt/review/attack/`, `raw/gitopt/uc5/attack/old-ghes-unboundother.txt`, and `raw/gitopt/uc6/attack/old-ghe.txt` and `old-ghcom.txt` | Repeated attack tables. | `raw/gitopt/uc5/attack/`, `raw/gitopt/uc6/attack/old-ghes.txt`, `new-ghes.txt`, and `diff-uc5-uc6.txt`. |
| `raw/canon-ghes/`, `raw/filtered-*/`, and `raw/gh-attack-*/` | Per-case output with a summary beside it. | Each `*.summary.txt`. `raw/capture-ghes/`, `raw/capture-ghcom/`, and `raw/capture-glab/` are kept whole. |
| `*.steps` and `*.join` under `raw/gitopt/` | Per-step logs that each `.table` file summarizes. | The `.table` and `.out` files. |
| `patch*.py`, `gitopt/splice.py`, and `gitopt/section.txt` | Edits to the harness and to text, which the kept source reflects. | The final source. |
| `go.sum` | Module checksums. | Versions in `go.mod.txt`. |
| Design edit scripts and review reports | The design documents' own review sections are the record. | Those sections. |

`work/` was also left out. It held the throwaway client repositories that the drivers recreate.

## Third-party inputs

These were downloaded for the runs, read as data, and not copied.

| Input | Source | Version |
| :-- | :-- | :-- |
| `gh` source and release binaries | [cli/cli](https://github.com/cli/cli) | Tag v2.103.3, and the releases 2.72.0, 2.73.0, 2.86.0, 2.87.0, 2.93.0, 2.94.0, and 2.103.3 |
| `glab` | [gitlab-org/cli](https://gitlab.com/gitlab-org/cli) | 1.127.0 |
| GitHub REST description | [github/rest-api-description](https://github.com/github/rest-api-description) | `info.version` 1.1.4 |
| GitHub GraphQL schema | [`schema.docs.graphql`](https://docs.github.com/en/graphql/overview/public-schema) and the history in [octokit/graphql-schema](https://github.com/octokit/graphql-schema) | Read on 2026-11-03 |
| GitHub permission data | Fine-grained permission data files in [github/docs](https://github.com/github/docs) | Read on 2026-11-03 |
| GitLab quick actions | [quick actions page](https://docs.gitlab.com/user/project/quick_actions/) | Read on 2026-11-03 |
| Parsers | gqlparser, graphql-go, graphql-js, graphql-ruby, and `graphql-c_parser` | 2.5.33, as in `go.mod.txt`, 17.0.2, 2.6.14, and 1.1.4 |
| JGit | [org.eclipse.jgit](https://www.eclipse.org/jgit/) | 7.3.0.202506031305-r |

## Credentials

No private key, certificate, or kubeconfig is in this folder. The per-pod CA and the leaf certificates were generated at run time, and the private keys were excluded. The token values that remain, such as `lenny-injected`, `GH-REAL-55aa01`, and `USER-FILE-TOKEN`, are placeholders that the harness wrote. A signed query string in `raw/gitopt/uc5/live/` was redacted when the run recorded it.
