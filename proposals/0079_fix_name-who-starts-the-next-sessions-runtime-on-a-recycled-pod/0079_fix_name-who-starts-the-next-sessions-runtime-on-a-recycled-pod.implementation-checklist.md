# Implementation checklist: Name who starts the next session's runtime on a recycled pod

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1, SPEC-2. §4.7.9 step 7 split by deployment model; the §4.7.10 runtime-process-lifetime paragraph and trade-off row.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-3. The §5.2 scrub-reach paragraph and the step 1 qualifier.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-4, SPEC-5. The §5.2 recycling narrowing, the `no_successor_runtime` retirement trigger, and the `mode_factor` consequence.
      Tiers 0, 11. Depends on: S2
- [ ] **S4 · spec** — SPEC-6, SPEC-7, SPEC-8. The §6.1 recycle row and §6.2 recycle edges; the §15.4 sentence; the §16.1 retirement-counter row.
      Tiers 0, 11. Depends on: S3
- [ ] **S5 · code** — CODE-1. The `lenny.dev/runtime-recreatable` label constant and the Sandbox reconciler stamp.
      Tiers 0, 1, 2. Depends on: S4
- [ ] **S6 · code** — CODE-2. `podscrub.Inputs.RuntimeRecreatable`, `ReasonNoSuccessorRuntime`, and the `Decide` branch.
      Tiers 0, 1. Depends on: S4
- [ ] **S7 · code** — CODE-3. The recycle policy resolver reads the label and carries it into `Decide`.
      Tiers 0, 1, 2, 4. Depends on: S5, S6
- [ ] **S8 · code** — CODE-4. The scrub, socket-runtime, and runtime-SDK doc-comment corrections.
      Tiers 0, 1. Depends on: S4
- [ ] **S9 · test** — TEST-1, TEST-2, TEST-3. The tier-1 `Decide` table, the tier-1 label parse, and the tier-2 reconciler cases.
      Tiers 0, 1, 2. Depends on: S5, S6, S7
- [ ] **S10 · test** — TEST-4. The tier-10 conformance case on a real `SocketRuntimeProcess`, with the successor-`Start` property.
      Tiers 0, 10. Depends on: S8
- [ ] **S11 · test** — FIXTURE-1, TEST-5, TEST-6, TEST-7. The embedded recycling pool fixture, the tier-5 split, and the skip-register deletion.
      Tiers 0, 5. Depends on: S7
- [ ] **S12 · test** — TEST-8. The tier-7a concurrent occupancy-zero retire-once case.
      Tiers 0, 7a. Depends on: S7
- [ ] **S13 · docs** — DOC-1, DOC-2, DOC-3, DOC-4. The runtime-author guide, the execution-modes reference, the glossary and level pages, and the operator narratives.
      Tiers 0, 11. Depends on: S4
