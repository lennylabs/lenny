## Implementation checklist

- [x] **S1 · spec** — SPEC-1. Lands the §13.1 **Container identity.** paragraph.
      Tiers 0, 11. Depends on: —
- [x] **S2 · code** — CODE-3. Lands the builder `runAsGroup` and the egress-capture UID and file-mode change.
      Tiers 0, 1. Depends on: S1
- [x] **S3 · code** — CODE-1, CODE-2. Lands the validator identity clause and the webhook wiring together, because the clause fails the webhook package's tests until CODE-2 updates them.
      Tiers 0, 1, 3. Depends on: S1, S2
- [x] **S4 · test** — TEST-1. Lands the Kind and security-tier cases and the agent-namespace fixture sweep.
      Tiers 0, 5, 8, 9 (tier 6 needs operator-provisioned cloud resources). Depends on: S1, S2, S3
- [x] **S5 · docs** — DOCS-1. Lands the operator-guide policy item.
      Tiers 11. Depends on: S1
