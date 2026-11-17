# Digest: 0113 Records and workflow

Written by hand on 2026-11-16, because the digest generator arrives with 0115. Branch `change/0113-records-and-workflow`, cut from `design/v1-cleanup`.

## Steps

| Step | Commit | Result |
| :-- | :-- | :-- |
| Framing | `afcb09aa3`, `ca835e453` | `change.md`, `review-history.md`, and `changes/programs/v1.md`, with the owner's answers to the four open decisions and the program's reading guidance. |
| 0113.S1 | `dd94a0859` | `changes/README.md` and `issues/README.md` from the spike's drafts, and the skeletons of 0114 and 0115. |
| 0113.S2 | `2101789b5` | 36 designs moved to `changes/0118-*` to `changes/0153-*` by [`evidence/move_designs.py`](evidence/move_designs.py): status `Draft`, review history split into `review-history.md`, plan sections renamed "Steps", evidence moved, and links and prose paths rewritten. The spike moved to `changes/0115-spec-structure/evidence/`, and `designs/evidence/README.md` to [`evidence/designs-evidence-README.md`](evidence/designs-evidence-README.md). Present-tense sentences naming `designs/` in 0119, 0125, 0137, and 0138 now name `changes/`. |
| 0113.S3 | `ee169292d` | `scripts/specshift/scope/scope.go` and the citation document gate exclude `changes/`, `changes/BOARD.md` is written, `AGENTS.md` points at the records and drops the stale beads instructions, and `docs/about/contributing.md` names `changes/`. |

## Requirements

None. This change edits no specification text.

## Documentation

`docs/about/contributing.md`, read by contributors: one sentence names `changes/` as where accepted changes live.

## Checks

- Links: 486 broken links before the move and 486 after, with none added. The remaining ones are links in the spike's proposed tree to pages it never wrote, regular expressions inside code that the checker reads as links, and four links that were broken in the designs before the move.
- `go test ./scripts/specshift/...`: passes.
- Tier 0: 3 failures, down from 8. `TestResidualGateCertifiesTheTree` and the citation document gate now pass. `TestDeepCopyInSyncWithGoTypes` and the two runtime supervisor binary checks fail on this host as before.
- Tier 11: 1 failure, `TestEmbeddedManifestsMatchDevProfileRender_spec_17_4`, a drift between the committed embedded manifests and this host's `helm template` render. This change touches neither.

## Defaults applied without asking

- The `review-history.md` of each moved design ends with a "Move into changes" section.
- Dated statements of what was read under `designs/`, and the historical git path in 0147, keep their words.
- The status line of each moved design keeps the design's revision, date, and summary, and a "Programs" line follows it.
