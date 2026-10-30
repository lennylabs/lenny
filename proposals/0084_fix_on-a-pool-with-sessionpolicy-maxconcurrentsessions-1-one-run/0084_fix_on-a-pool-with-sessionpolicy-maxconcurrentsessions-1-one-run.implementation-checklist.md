## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands the §15.4.3, §15.4.4, §15.4.6, and §15.7 edits in spec/15.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the §4.7.5, §4.7.6, and §4.7.11 edits in spec/04.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands the `CH-MCP-PLATFORM`, `CH-MCP-CONNECTOR`, §28.6, and §28.8 edits in spec/28.
      Tiers 0, 11. Depends on: S1
- [ ] **S4 · spec** — SPEC-4. Lands the `CH-RUNTIMEOPS` `llm_request_completed` row in spec/28 and the §11.2 sentence in spec/11.
      Tiers 0, 11. Depends on: —
- [ ] **S5 · spec** — SPEC-5. Lands the §29.2 and §29.10 edits in spec/29.
      Tiers 0, 11. Depends on: S1, S2
- [ ] **S6 · spec** — SPEC-6. Lands the §5.2, §9.3, and §13.1 edits.
      Tiers 0, 11. Depends on: S1
- [ ] **S7 · schema** — SCHEMA-1. Lands the optional `sessionId` in the runtime-ops schema and example, and the proto comment.
      Tiers 0, 3. Depends on: S4
- [ ] **S8 · code** — CODE-1. Lands the `_lennySessionId` extraction and `CallerSession` in pkg/adapter/mcp.
      Tiers 0, 1, 3. Depends on: S1
- [ ] **S9 · code** — CODE-2. Lands `runtimeSession` and the addressed token sink.
      Tiers 0, 1, 3, 7a. Depends on: S1, S4, S7
- [ ] **S10 · code** — CODE-4. Lands the pod-surface rules and the ensure-and-publish helper.
      Tiers 0, 1, 7a. Depends on: S1, S2
- [ ] **S11 · code** — CODE-3. Routes the platform and connector providers through `runtimeSession`; lands after S10 because lifting the refusal makes co-tenants depend on the surface S10 repairs.
      Tiers 0, 1, 4, 9. Depends on: S1, S3, S6, S8, S9, S10
- [ ] **S12 · code** — CODE-5. Lands the admission message and its carriers.
      Tiers 0, 1, 9. Depends on: S6
- [ ] **S13 · code** — CODE-6. Lands the refused-connector tolerance in the three runtime SDKs.
      Tiers 0, 1, 3. Depends on: S1, S2
- [ ] **S14 · code** — CODE-7. Lands the refused-connector case in the compliance reachability check; lands after S13 so the reference runtimes pass it.
      Tiers 0, 3, 10. Depends on: S1, S13
- [ ] **S15 · test** — TEST-1. Lands the tier-0 and tier-3 wire-contract cases.
      Tiers 0, 3. Depends on: S7, S8, S9
- [ ] **S16 · test** — TEST-2. Lands the tier-1 adapter and Go SDK cases and the claim-map row.
      Tiers 0, 1. Depends on: S8, S9, S10, S11, S13
- [ ] **S17 · test** — TEST-3. Lands the tier-4 principal-resolution test.
      Tiers 0, 4. Depends on: S11
- [ ] **S18 · test** — TEST-4. Lands the tier-7a race and contention cases.
      Tiers 0, 7a. Depends on: S9, S10, S11
- [ ] **S19 · test** — TEST-5. Lands the tier-9 attribution-boundary cases.
      Tiers 0, 9. Depends on: S11, S12
- [ ] **S20 · docs** — DOCS-1. Lands the documentation pages and the tier-11 nonce gate edits.
      Tiers 0, 11. Depends on: S1, S2, S3, S4, S5, S6, S11, S13
