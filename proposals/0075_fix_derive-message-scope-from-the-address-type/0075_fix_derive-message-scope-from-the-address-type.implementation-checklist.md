## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. The derivation rule, its stream-envelope clause, and the constraint the
      derivation rests on replace the whole `#### Request Message Scope` block of
      `spec/04_system-components.md`, which is the introducing paragraph at `:151`, the table at
      `:153-186`, and the grounding paragraph at `:188`, leaving the `ShutdownRequest` paragraph at `:190`
      standing.
      Tiers 0, 11. Tier 0 is red from the end of this step until S2 replaces the gate, because deleting the
      table leaves `parseMessageScopeTable` with no rows and the retiring gate then reports a missing row
      for every declared request message. Depends on: —
- [ ] **S2 · test** — TEST-1. The tier-0 table-reconciliation gate is replaced by the rule gate over the
      addressing convention, the shared proto parse carries each field's type and its `oneof` membership,
      its other caller moves with that signature, and `tests/spec-map.json` re-registers the replacement
      gate's cases under sections 4.1 and 28.5.3 inside this step rather than as a step of its own, because
      tier 0 is red between retiring the old function names and registering the new ones.
      Tiers 0. Depends on: S1
- [ ] **S3 · test** — TEST-2. The tier-3 session-address suite covers `CoordinatorFenceRequest` and its
      false coverage clause is removed.
      Tiers 3. Depends on: S1
