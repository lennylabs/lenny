# Layer 5: live smoke runs

Layers 1 through 4 verify the **machine**: which agent runs, in what order, told
what, and what the script does with the answer. They cannot verify the
**judgement**. Whether a model given the `fix-design` brief actually triages
instead of over-thinking a typo, whether the log conventions survive twelve
parallel lenses, whether the change-class table makes a build cheaper rather
than merely different — none of that is observable from a stub.

Every claim this rework makes about token savings is a hypothesis. These two
runs are the only thing that tests it. They cost real tokens and they are not
automated; run them before treating the rework as finished, and write what you
saw into `tmp/`.

## S1 — change-proposal, `new` mode, small seeded problem

Run at low `maxSpecReviewRounds` and `maxNonSpecReviewRounds` (4 or so) against a
problem you already understand, so you can judge the output rather than only
observe it.

Read afterwards:

- **The `fix-design` outputs.** Did trivial findings actually get one-line
  treatment, or did the architect brief leak into all of them? This is the
  single most likely way the design fails, and no stub can show it. Count the
  tokens each group's design cost against its `effort` field.
- **The review log after two compactions.** Did the tag vocabulary hold? Did
  compaction promote what was marked `USEFUL` and retire what was marked
  `CORRECTS`? Read `## Standing context` as an agent would: is it something you
  would want to carry, or is it padding?
- **The per-round spend**, against the same proposal's pre-rework run if one
  exists. The grouping change is a bet that focus beats batching, and this is
  where it is settled.
- **`review.loops[].specTouched`.** Did the non-spec loop reopen the spec
  staging, and was it right to?

## S1b — the first real lazy migration

Converge an existing draft. 0076 is the natural candidate: it is `Draft` and
small. Then read the resulting directory against the original file.

- Was the spec/non-spec partition of the design sections sensible, or did the
  migrator put implementation detail in `.spec-changes.md`?
- Did `check-proposal-split.mjs` pass first time, or did it take repair passes?
- Were the inbound references retargeted to the right thing — a finding to the
  directory, a test that reads staged text to `.spec-changes.md`?

This is the one part of the design where the agent has real latitude and the
script can only check that nothing was **lost**, not that the judgement was
good.

## S2 — implement-proposal, `spec-only`, an already approved proposal

Read afterwards:

- **The lease's whole lifecycle** in `git status` and the logs. Then interrupt a
  run mid-spec-step deliberately and confirm the stale-lease path reports rather
  than silently unlocking, and that the next run refuses to start.
- **The test-scoping decisions** in the step logs: which tiers were skipped,
  with what stated reason, and whether the final gate caught anything they
  missed. One instance of the gate catching a real failure validates the design.
  Several steps with no miss at all is weak evidence the scoping is too
  permissive rather than well-tuned — check `gateMisses` either way.
- **The cost ledger** at `scratchpad/test-times/<branch>.json` after several
  steps. Are the medians plausible? Is `expensiveTierSeconds` at 300 separating
  the tiers you would want it to separate on this machine?

## What to write down

A note in `tmp/`, naming what you ran, what you read, and what you would change.
Expect to tune the `fix-design` brief and the change-class table from what these
show; that is what they are for.
