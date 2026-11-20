# Steps: 0150 Tenant security settings

## Validation owed

- The erasure job meeting a 1-hour deadline, and the tenant deletion meeting 4 hours, on a tenant with production data volumes. Today's T4 values are claims with no measurement found in the tree.
- The `encryption_key_scope` count and the move from `retained` to `none` at scale, against the `artifact_store` table's indexes.
- The per-provider binding of the tenant key when switched on for a tenant with existing objects, on MinIO, S3, GCS, and Azure, including a GCS tenant moving to a per-tenant bucket.
- That every region-bearing resource the residency webhook sees carries the tenant's region today, so the universal check of I34 refuses nothing that §12.8 allows **[inference]**.
- That one SO-2 applier can activate pending versions of two record types without reordering a tenant's writes.
- Which stores other than the artifact store hold Secret or Confidential data that §12.9's store write check should refuse, such as Redis for credential leases, and whether each can receive the class at write time ("TSS-8").

## Steps

### 0150.S1 Data class renames
- status: Planned
- aliases: [TSS-A, TSS-8]
- lanes: [specification, code, documentation]
- closes: [F-12.9.23]

The class renames of "TSS-8" across the specification, with Secret for the top class, the enforcement statement per control row, and the universal region match (I34). The enforcement statement restates the access row for each kind of data as the role check of the API that serves it, and retires the row for workspace data (I31), which closes F-12.9.23. The step renames the store write check's reason `tier_store_mismatch` to `class_store_mismatch` in `pkg/blobstore`; 0150.S2 keys the guard `checkTierStoreMismatch` on the tenant key state. The Secret row's statement about §4.9 waits on O15, and the step lists F-12.9.24 under `closes` once O15 is settled.

The step has no dependencies. The steps of 0128, 0142, 0148, 0149, and 0151 that write the class names into §12.9 depend on it, as "Instructions for reviewers of other designs" in `change.md` asks.

### 0150.S2 Settings model, routes, and tenant keys
- status: Planned
- aliases: [TSS-B, TSS-1, TSS-2, TSS-3, TSS-4, TSS-5, TSS-7, TSS-9 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0150.S1, 0142.S1 (part), 0142.S4, 0141.S1 (part)]
- closes: [F-12.9.25, T-12.9.2, T-12.9.7]

The settings model, §12.10, versions, the admin routes, templates, the tenant key states and renames, the deadlines, retention, the backup bound, and the removal of the tenant and environment `workspaceTier`. The step lands writes that tighten. Until 0150.S4 lands the relaxation rule, a settings write or a template apply that would relax a setting is refused with `422 SECURITY_SETTING_UNSATISFIABLE` and `details.reason: relaxation_unavailable`; creating a tenant relaxes nothing and applies its template at once. It depends on SO-1 (0142.S1, the part that adds the `security-auditor` and `security-admin` roles) for the tenant roles that read the settings, on SO-4a's mirror (0142.S4), and on RT-2 (0141.S1, the part that adds the post-chain point) for the point at which session creation validates the active settings (I33); `pkg/` has no post-chain point today. The validation there runs on both creation routes, which closes F-12.9.25. Removing the environment override closes T-12.9.7, and the `chaos` test of the tenant key disabled at the provider drives the fail-closed write through the gateway, which closes T-12.9.2.

The tenant key states and the deadlines stay in this step rather than in steps of their own, because today's tenant key path and the tier-keyed retention and deadlines read the tenant `workspaceTier` that this step removes. The move from `retained` to `none` counts the evidence data keys of the evidence key store that [0128.S7](../0128-forensics-evidence-preservation/steps.md#steps) creates; whichever of 0150.S2 and 0128.S7 lands second adds that count.

0150.S2 can land before the placement design, because the placement settings it stores take effect only when 0150.S3 lands. Until then, the view's `effective` block reports each placement setting with `"enforced": false`, so the view does not present a stored value that placement ignores, and between this step and 0136.S4, `placement.nodeTenancy: dedicated` is stored without PM-2's conflict check. 0150.S2 does not remove `workspaceTier` from the Runtime and `PodSet`, which 0150.S3 removes. Whether the T4 webhook still reads the Runtime's field when this step lands depends on where the placement design removes the webhook, which is O17.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes or removes `GWSEAM-ProbeTenantKMS` and `GWSEAM-LastTenantKMSProbe`, through which the admin API probes a tenant's key before it accepts a workspace tier and reports the last successful probe, with the tenant key states and the removal of the tenant `workspaceTier`. The API front reaches both operations through `seam.TenantKeys` under decision 63 of 0117, so the new probe route and the key state read go through the reshaped operations rather than an import of `pkg/tenantkms`.

### 0150.S3 Placement settings as operator requirements
- status: Planned
- aliases: [TSS-C, TSS-6, TSS-9 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0150.S2, 0136.S4, 0136.S6]

The placement settings as operator requirements, the removal of the T4 text of §6.4's **T4 workspace limitation:**, and the removal of `workspaceTier` from the Runtime and `PodSet`, with the column `runtime_definitions.workspace_tier`. It lands after the placement design's steps for PM-2 (0136.S4) and PM-4 (0136.S6), and it removes the `"enforced": false` marker that 0150.S2 puts on the placement settings in the view. §5.2's T4 cross-tenant reuse prohibition and its checks leave in [0136.S1](../0136-placement/steps.md#steps), and this step adds §12.10 to its successor pointer. The T4 webhook, its node selector, and §6.4's **T4 dedicated-node requirement:** leave in the placement design's step that O17 settles. [0140.S1](../0140-runtime-revisions/steps.md#steps) lands after this step or drops its row for the field, as 0140's steps state.

### 0150.S4 Relaxation rule
- status: Planned
- aliases: [TSS-D]
- lanes: [specification, code, documentation]
- depends-on: [0150.S2, 0142.S2]

The relaxation rule of O5 (b) and TSSR-4: pending versions of the settings, the mixed write, a template apply that relaxes, the cancel route, the tenant list's `pendingRelaxation` filter, the view's `pending` field, the events `tenant.security_settings_relaxation_requested`, `tenant.security_settings_relaxed`, and `tenant.security_settings_relaxation_cancelled`, the alert `TenantSecuritySettingRelaxationRequested`, and the second record type of SO-2's applier. It removes the refusal with `relaxation_unavailable` that 0150.S2 lands. It depends on SO-2 (0142.S2) for the applier and the weakening delay. What a pending version holds, and what a later write does to it, is O14, which blocks this step.
