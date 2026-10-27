# Implementation checklist — 0105_fix_the-per-state-session-deadlines-and-the-watchdog-sweeps

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands Edits (a) and (b) in §6.2.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands Edits (a) to (j) in §7.2, §7.3, §6.2, and §15.1 in one commit.
      Tiers 0, 11. Depends on: —
- [ ] **S3 · spec** — SPEC-3. Lands the §8.2 `LeaseSlice` row.
      Tiers 0, 11. Depends on: —
- [ ] **S4 · spec** — SPEC-4. Lands the §10.1.1 **Primary:** sentences.
      Tiers 0, 11. Depends on: —
- [ ] **S5 · code** — CODE-1. Lands the migration, the `StateEnteredAt` field, and the store stamp.
      Tiers 0, 1, 2. Depends on: S1
- [ ] **S6 · code** — CODE-2. Lands the watchdog anchors, locked-row re-checks, and unconditional reasons.
      Tiers 0, 1, 7a. Depends on: S1, S4, S5
- [ ] **S7 · code** — CODE-3. Lands the `perChildMaxAge` leg and the doc comments.
      Tiers 0, 1. Depends on: S2, S3, S6
- [ ] **S8 · code** — CODE-5. Lands the orphan-cleanup anchor and stamp.
      Tiers 0, 1. Depends on: S2, S4
- [ ] **S9 · code** — CODE-4. Lands continuation and off-pass hook dispatch in the watchdog, orphan cleanup, and the orphan-session reconciler.
      Tiers 0, 1, 7a, 8. Depends on: S4, S6, S7, S8
- [ ] **S10 · code** — CODE-6. Lands the `/start` write through `starting`.
      Tiers 0, 1, 4, 7a, 8. Depends on: S2
- [ ] **S11 · code** — CODE-7. Lands the `input_required` writes, the `status_change` emission, the adoption revert, and the gate decisions.
      Tiers 0, 1, 4, 7a. Depends on: S1, S2
- [ ] **S12 · code** — CODE-8. Lands the leader-gated orphan-session reconciler and the lag-series clear.
      Tiers 0, 1, 8. Depends on: S9
- [ ] **S13 · test** — TEST-1. Lands the tests not already landed with S5 to S12 and the `tests/spec-map.json` entries.
      Tiers 0, 1, 2, 4, 7a, 8. Depends on: S5, S6, S7, S8, S9, S10, S11, S12
- [ ] **S14 · docs** — DOC-1. Lands the `STARTING_FAILED` row of the error catalog page.
      Tiers 0, 11. Depends on: S2
- [ ] **S15 · docs** — REC-1. Lands the BUILD-GAPS notes and entries and the P3 plan entry.
      Tiers 0, 11. Depends on: S13
