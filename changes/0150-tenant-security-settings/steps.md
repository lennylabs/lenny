# Steps: 0150 Tenant security settings

## Validation owed

- The erasure job meeting a 1-hour deadline, and the tenant deletion meeting 4 hours, on a tenant with production data volumes. Today's T4 values are claims with no measurement found in the tree.
- The `encryption_key_scope` count and the move from `retained` to `none` at scale, against the `artifact_store` table's indexes.
- The per-provider binding of the tenant key when switched on for a tenant with existing objects, on MinIO, S3, GCS, and Azure, including a GCS tenant moving to a per-tenant bucket.
- That every region-bearing resource the residency webhook sees carries the tenant's region today, so the universal check of I34 refuses nothing that §12.8 allows **[inference]**.
- That one SO-2 applier can activate pending versions of two record types without reordering a tenant's writes.
- Which stores other than the artifact store hold Secret or Confidential data that §12.9's store write check should refuse, such as Redis for credential leases and the memory store's pluggable backends, and whether each can receive the class at write time ("TSS-8").

## Steps

### 0150.S1 Data class renames
- status: Planned
- aliases: [TSS-A, TSS-8]
- lanes: [specification, code, documentation]

The class renames of "TSS-8" across the specification, with Secret for the top class, the enforcement statement per control row, and the universal region match (I34).

### 0150.S2 Settings model, routes, and tenant keys
- status: Planned
- aliases: [TSS-B, TSS-1, TSS-2, TSS-3, TSS-4, TSS-5, TSS-7, TSS-9 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0150.S1, 0142.S2, 0142.S4]

The settings model, §12.10, versions and the relaxation rule, the admin routes, templates, the tenant key states and renames, the deadlines, retention, and the removal of the tenant and environment `workspaceTier`. It depends on SO-2's proposal (0142.S2) for the applier and the weakening delay, and on SO-4a's mirror (0142.S4).

0150.S2 can land before the placement design, because the placement settings it stores take effect only when 0150.S3 lands. Until then the T4 webhook keeps reading the Runtime's field, so 0150.S2 must not remove that field.

### 0150.S3 Placement settings as operator requirements
- status: Planned
- aliases: [TSS-C, TSS-6, TSS-9 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0150.S2, 0136.S4, 0136.S6]

The placement settings as operator requirements, the removal of §6.4 and §5.2's T4 rules, and the removal of `workspaceTier` from the Runtime and `PodSet`. It lands after the placement design's steps for PM-2 (0136.S4) and PM-4 (0136.S6), together with the placement design's removal of the T4 webhook.
