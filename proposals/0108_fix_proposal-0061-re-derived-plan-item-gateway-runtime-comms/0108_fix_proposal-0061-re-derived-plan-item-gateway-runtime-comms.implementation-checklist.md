# Implementation checklist — 0108_fix_proposal-0061-re-derived-plan-item-gateway-runtime-comms

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1, SPEC-2. Narrow the §15 single-shot paragraph and add **Open Responses conversation sessions.**
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-3. Split the §29.3 adapter off-holder row and edit the lead-in clause.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-4. Add the §15 link to the §28.5.4 `CH-HOLDERFORWARD` card's **Messages.** bullet.
      Tiers 0, 11. Depends on: S1
- [ ] **S4 · code** — CODE-1. Transcript `Append` returns the last assigned Seq, with every caller updated.
      Tiers 0, 1, 2, 7a. Depends on: S1
- [ ] **S5 · code** — CODE-2. Per-response records and the requested runtime ref on the session request-envelope bundle.
      Tiers 0, 1, 2. Depends on: S1
- [ ] **S6 · code** — CODE-3. `ForwardKindResponsesTurn`, `ForwardResponsesTurn`, and the listener route.
      Tiers 0, 1, 4. Depends on: S2, S3
- [ ] **S7 · code** — CODE-4. Response-id codec, Seq-bounded rehydrate, required record, and GET.
      Tiers 0, 1, 3, 9. Depends on: S1, S4, S5
- [ ] **S8 · code** — CODE-5. `handleCreate` resolve, forward, warm, and cold routing with the forwarder adapter.
      Tiers 0, 1, 2, 3, 4, 7a, 9. Depends on: S1, S2, S6, S7
- [ ] **S9 · docs** — DOCS-1. Open Responses API page.
      Tiers 0, 11. Depends on: S1
- [ ] **S10 · test** — TEST-1. The cross-tier cases in the Testing section that S4 through S8 did not already land.
      Tiers 0, 1, 2, 3, 4, 7a, 8, 9. Depends on: S8
