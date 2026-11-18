# Coverage equivalence of the spec map and the annotation index

Measured for 0116.S6 on 2026-11-18, on `change/0116.S4-S6` before the map was deleted, over the tree of `main` at `98728a207`. A temporary test in `cmd/lenny-test` compared, for each key of `tests/spec-map.json` that lists tests, the map's entries with what `list --spec <key>` and `run --spec <key>` select from the annotation index alone (`specModel.testsFor`, without the spec-map half of the union that 0116.S1 added). The test was not committed.

## Method

- A map entry of the form `path::Name` names one test function. It counts as selected when the index selects that function for the key.
- A map entry that names a file, a directory, a `dir/...` tree, or a glob names a package or a set of files to run. It counts as selected when the index selects at least one test function under that path for the key, because the run then reaches the same package.
- A tier counts as lost when an entry of the key sits in that tier and the index selects no test of that tier for the key. The tier of an entry is the tier whose directory holds it, or the unit tier for a test beside its code.
- For each entry the index does not select, the record states why: the function is absent from the tree, the path names no test function, the test carries no annotation, or its annotation cites another section or identifier, which it names.

## Result

The index does not select at least what the map did for every key. Of the 213 keys that list tests, the index selects every entry and every tier for 24. 49 keys lose a tier, and the index selects no test for 15 keys. Every entry the index does not select falls into one of four cases, and none leaves a test unreachable:

1. **The annotation cites another section (230 function entries, and most of the 303 path entries the index does not reach).** In most cases the test cites the parent section, such as `§4.7` for a test the map listed under `4.7.8`, or `§13.2` for one listed under `13.2.1`. A query of the parent section selects it, because a section query selects the tests that cite the section or a subsection, and a query of a subsection does not reach a test that cites only its parent. The other cases cite a sibling section, a page code, or an identifier the map did not key, and a query of what the annotation cites selects them.
2. **The test carries no annotation (570 function entries).** These are unit tests under `pkg/`, such as the `pkg/checkpoint` tests the map listed under `4.4.4` and `4.4.5`, and a few in other tiers. The coverage view counts them as unannotated per tier and per package. `--changed` and `--pkg` select them through the import graph when their package or a dependency changes, and the merge gate runs them on such a change. Re-annotating them is each area's work, as `change.md` ("Scope", out of scope) and `spec-annotations.md` ("Transition") state.
3. **The entry names nothing (34 function entries and 55 path entries).** The function was renamed or deleted, or the path holds no test function, such as a k6 scenario directory under `tests/load_kind/scenarios/`. Nothing runs for these entries today, so nothing is lost.
4. **The key names no numbered heading (`6.5`, `9.5`, and `14.5`).** These keys are build-phase numbers. Their tests in `tests/load_kind/` cite TESTING.md section numbers (`13.17`, `13.21`, and `13.31`) that name no specification heading, so the coverage view lists them under unresolved citations, and the area that owns load testing fixes them when it re-annotates (the fifth item of "Validation owed"). The load_kind tier runs whole at the release gate.

The tiers a key loses are all outside the merge gate's unit, component, contract, and integration tiers in 28 of the 49 keys, and such a tier runs whole at the area or release gate. In the other 21 keys, for the merge-gate tiers (unit, component, contract, and integration), a lost tier means that `run --spec <key>` no longer runs those tests; `--changed` still selects them when the code they exercise changes. The unit tier also narrows: under the union, a key whose map entries named a unit test ran the unit tier over every package, and without the map `run --spec` scopes the unit tier to the packages of the unit tests the index selects, or runs it whole when the index selects no test.

No annotation was edited for this record. The annotations that cite a parent section are correct citations, and the three that cite TESTING.md numbers fall to the area that re-annotates them, as above.

## Counts

| Measure | Count |
| :-- | --: |
| function entries | 4009 |
| function entries: annotation cites another section | 230 |
| function entries: function absent from the tree | 34 |
| function entries: selected by the index | 3175 |
| function entries: unannotated | 570 |
| keys that lose a tier | 49 |
| keys the index covers at least as the map | 24 |
| keys the index selects no test for | 15 |
| keys with tests | 213 |
| keys without tests | 18 |
| path entries | 1640 |
| path entries: index selects a test under the path | 1282 |
| path entries: index selects no test under the path | 303 |
| path entries: path names no test function | 55 |

## Keys that lose a tier

| Key | Numbered heading | Tiers lost |
| :-- | :-- | :-- |
| `1` | yes | component, contract |
| `2` | yes | contract |
| `4.4.1` | yes | component, contract |
| `4.4.2` | yes | component, integration |
| `4.4.3` | yes | e2e_kind |
| `4.4.4` | yes | unit |
| `4.4.5` | yes | chaos, component, docs, e2e_kind, integration, unit |
| `4.7.2` | yes | unit |
| `4.7.3` | yes | unit |
| `4.7.4` | yes | contract, unit |
| `4.7.5` | yes | unit |
| `4.7.7` | yes | conformance, unit |
| `4.7.8` | yes | chaos, integration, unit |
| `4.7.11` | yes | integration |
| `6.1` | yes | conformance, integration |
| `6.4` | yes | chaos, conformance |
| `6.5` | no | load_kind |
| `9.3` | yes | load_local |
| `9.5` | no | load_kind |
| `10.1.3` | yes | chaos, component |
| `10.1.4` | yes | e2e_kind |
| `10.1.5` | yes | chaos |
| `10.1.6` | yes | e2e_kind, load_kind |
| `10.1.8` | yes | e2e_kind |
| `10.4` | yes | component, integration |
| `10.6` | yes | chaos, e2e_kind, security |
| `11.1` | yes | chaos, e2e_kind, security |
| `11.5` | yes | load_kind |
| `11.6` | yes | integration |
| `13.2.1` | yes | security |
| `13.2.3` | yes | e2e_cloud |
| `13.2.5` | yes | security |
| `13.2.6` | yes | security |
| `13.2.7` | yes | security |
| `14.5` | no | load_kind |
| `15.4.3` | yes | conformance |
| `16.3` | yes | component |
| `16.5` | yes | load_kind |
| `17.8.6` | yes | unit |
| `24.18` | yes | docs, e2e_kind |
| `24.20` | yes | docs |
| `25.2` | yes | chaos, e2e_cloud |
| `25.3` | yes | chaos, security |
| `25.10` | yes | security |
| `25.13` | yes | chaos |
| `26` | yes | conformance |
| `27.7` | yes | unit |
| `28.3` | yes | integration |
| `28.5.4` | yes | chaos |

## Keys the index selects no test for

| Key | Numbered heading | Map entries |
| :-- | :-- | --: |
| `4.4.1` | yes | 4 |
| `4.4.4` | yes | 3 |
| `4.7.2` | yes | 5 |
| `4.7.4` | yes | 3 |
| `4.7.7` | yes | 2 |
| `4.7.8` | yes | 7 |
| `6.5` | no | 2 |
| `9.5` | no | 2 |
| `10.1.6` | yes | 2 |
| `13.2.1` | yes | 1 |
| `13.2.7` | yes | 2 |
| `14.5` | no | 2 |
| `24.3` | yes | 1 |
| `24.7` | yes | 1 |
| `24.14` | yes | 1 |

## Entries the index does not select, by key

### `1`

Tiers the map names and the index does not: component, contract.

- `tests/component/auditintegrity/integrity_test.go` (5 test functions): cites §11.7
- `tests/component/migrations/migrations_test.go` (5 test functions): cites §12.2; cites §12.2, §12.3; cites §12.3
- `tests/contract/oauth_token/oauth_token_test.go` (10 test functions): cites §13.3
- `tests/contract/rest_auth/auth_test.go` (11 test functions): cites §10.2; cites §10.2, §15.1
- `tests/contract/rest_sessions/sessions_test.go` (16 test functions): cites §15.1; cites §15.1, §7.3; cites §4.2

### `2`

Tiers the map names and the index does not: contract.

- `tests/contract/adapter_jsonl/messages_test.go` (8 test functions): cites §15.4
- `tests/contract/oauth_token/oauth_token_test.go` (10 test functions): cites §13.3

### `3`

- `tests/contract/workspaceplan/sources_test.go` (13 test functions): cites §14; cites §14, §14.1; cites §14, §15.1; cites §14.1

### `4`

- `pkg/checkpoint/checkpoint_test.go::TestAllLevelsIsExhaustive`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestConsistencyForLevel`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestAllTriggersIsExhaustive`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestRetryBudgetForTriggers`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestCheckpointTimeoutIs60s`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestAllOutcomesIsExhaustive`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestAllResumeModesIsExhaustive`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestResumeModeWorkspaceLost`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestWorkspaceSizePreCheckRejectsExceed`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestFreshnessCheckRejectsStale`: unannotated
- `pkg/admission/podclaim_guard/guard_test.go::TestPatchAdmitsWhenSandboxClaimed`: no such test function in the tree
- `pkg/admission/podclaim_guard/guard_test.go::TestPatchRejectsWhenSandboxNotClaimed`: no such test function in the tree
- `pkg/controller/poolscaling/strategy/strategy_test.go::TestComputeStandardSession`: unannotated
- `pkg/controller/poolscaling/strategy/strategy_test.go::TestComputeTaskModeReducesSteady`: no such test function in the tree
- `pkg/controller/poolscaling/strategy/strategy_test.go::TestComputeConcurrentModeReducesBothTerms`: no such test function in the tree
- `pkg/controller/poolscaling/strategy/strategy_test.go::TestComputeVariantPool`: unannotated
- `pkg/controller/poolscaling/strategy/strategy_test.go::TestComputeAdjustedBasePool`: unannotated
- `pkg/controller/poolscaling/strategy/strategy_test.go::TestComputeBootstrapMode`: unannotated
- `pkg/controller/poolscaling/strategy/strategy_test.go::TestComputeRejectsVariantWeightsExceedingOne`: unannotated
- `pkg/admission/ownership/ownership_test.go::TestValidateAllowsOwnedWrites`: unannotated
- `pkg/admission/ownership/ownership_test.go::TestValidateRejectsCrossControllerWrites`: unannotated
- `pkg/admission/ownership/ownership_test.go::TestOwnerSdkWarmCircuitBreakerOverridesDefault`: unannotated
- `pkg/admission/ownership/ownership_test.go::TestAllRulesEnumerateMatrix`: unannotated
- `pkg/credential/credential_test.go::TestAllProvidersIsExhaustive`: unannotated
- `pkg/credential/credential_test.go::TestAllAssignmentStrategiesIsExhaustive`: unannotated
- `pkg/credential/credential_test.go::TestAllRotationTriggersIsExhaustive`: unannotated
- `pkg/credential/credential_test.go::TestRotationTriggerIsFaultTriggered`: unannotated
- `pkg/credential/credential_test.go::TestRotationTriggerIsCeilingApplicable`: unannotated
- `pkg/credential/credential_test.go::TestRotationTriggerCountsAgainstBudget`: unannotated
- `pkg/credential/credential_test.go::TestPoolConfigValidateHappy`: unannotated
- `pkg/credential/credential_test.go::TestPoolConfigValidateRejectsBadFields`: unannotated
- `charts/lenny/tests/etcd-encryption_test.yaml`: the file exists and declares no test function

### `4.0`

- `pkg/upgrade/upgrade_test.go::TestAdvanceEmitsUpgradeProgressedOnEveryTransition`: unannotated
- `pkg/upgrade/upgrade_test.go::TestAdvanceWithNilEmitterStillTransitions`: unannotated
- `pkg/upgrade/upgrade_test.go::TestAdvanceRollbackEmitsRolledBackPhase`: unannotated
- `pkg/controller/warmpool/poolphase_test.go::TestReconcileEmitsPoolStateChangedOnTransition`: unannotated
- `pkg/controller/warmpool/poolphase_test.go::TestReconcileNoEmitOnStablePhase`: unannotated
- `pkg/controller/warmpool/poolphase_test.go::TestReconcileEmitsExhaustedWithWarningSeverity`: unannotated
- `pkg/gateway/externalapi/admin/breakers_test.go::TestOpenBreakerEmitsStateChangedAuditPayload_spec_16_7_673`: cites §16.7
- `pkg/gateway/externalapi/admin/breakers_test.go::TestCloseBreakerEmitsStateChangedAuditPayload_spec_16_7_673`: cites §16.7
- `pkg/gateway/externalapi/admin/eventbuffer_test.go::TestEventBufferSurfacesCircuitBreakerEvent`: unannotated
- `pkg/gateway/sessionserver/opsevent_internal_test.go::TestRecordSessionCompletedEmitsSessionFailed`: unannotated
- `pkg/gateway/credentials/credentialserver/credentialserver_audit_test.go::TestRotateEmitsCredentialRotated_F491`: cites §4.9.2

### `4.1`

- `tests/contract/sdks/go_client_test.go::TestGoClientMessageBodyOmitsSlotAddress`: cites §15.4, §7.2
- `tests/contract/sdks/python_client_test.go::TestPythonClientMessageBodyOmitsSlotAddress`: cites §15.4, §7.2
- `tests/contract/sdks/typescript_client_test.go::TestTypeScriptClientMessageBodyOmitsSlotAddress`: cites §15.4, §7.2

### `4.2`

- `pkg/session/...` (10 test functions): cites §7.2; cites §7.2, §6.2; unannotated
- `tests/contract/oauth_token/oauth_token_test.go` (10 test functions): cites §13.3
- `tests/integration/idempotency_test.go` (4 test functions): cites §11.5
- `tests/integration/checkpoint_resume_test.go::TestResumeBumpsRecoveryGenerationDurablyPostgres`: no such test function in the tree
- `tests/component/migrations/prod_columns_test.go` (10 test functions): cites §11.1; cites §12.2; cites §12.2, §18.5; cites §12.8; and 6 more

### `4.3`

- `pkg/tokenexchange/...` (23 test functions): cites §13.3; unannotated
- `pkg/credential/...` (80 test functions): cites §4.9; cites §4.9, §12.8; cites §4.9, §17.6; cites §4.9, §26.6, §26.5, §26.8, §26.9; and 2 more
- `tests/contract/oauth_token/oauth_token_test.go` (10 test functions): cites §13.3
- `tests/contract/oauth_token/oauth_token_record_test.go` (2 test functions): cites §13.3
- `tests/e2e_cloud/scaffolds_test.go::TestCloudKMS`: no such test function in the tree
- `tests/e2e_cloud/scaffolds_test.go::TestCloudSecretStore`: no such test function in the tree
- `tests/integration/tenant_deletion_phase4_test.go::TestTenantDeletionPhase4ClearsArchivedTreesAndCheckpoints_spec_12_8`: cites §12.8

### `4.4`

- `pkg/checkpoint/checkpoint_test.go::TestAllLevelsIsExhaustive`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestConsistencyForLevel`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestAllTriggersIsExhaustive`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestRetryBudgetForTriggers`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestCheckpointTimeoutIs60s`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestAllOutcomesIsExhaustive`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestAllResumeModesIsExhaustive`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestResumeModeWorkspaceLost`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestWorkspaceSizePreCheckRejectsExceed`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestFreshnessCheckRejectsStale`: unannotated
- `tests/component/stores/partialmanifeststore_test.go` (1 test functions): cites §10.1, §11.2
- `tests/integration/checkpoint_minio_atomicity_test.go::TestCheckpointWritesMetadataOnlyAfterMinIOUpload`: no such test function in the tree
- `tests/integration/checkpoint_minio_atomicity_test.go::TestCheckpointDiscardedWhenMinIOUploadFails`: no such test function in the tree
- `pkg/gateway/runtime/adapterclient/client_test.go` (63 test functions): cites §11.3; cites §13.2; cites §4.7; cites §4.7, §11.2; and 15 more

### `4.4.1`

Tiers the map names and the index does not: component, contract.

- `tests/component/stores/eventstore_test.go` (3 test functions): cites §12.2.1; cites §4.4
- `tests/contract/ocsf_audit/admin_audit_envelope_test.go` (5 test functions): cites §25.9; cites §25.9, §11.7; cites §4.4, §11.7
- `tests/contract/ocsf_audit/determinism_test.go::TestOCSFChainHashRecomputableFromCanonicalTuple`: cites §4.4, §11.7
- `tests/contract/ocsf_audit/ocsf_audit_test.go` (5 test functions): cites §11.7

### `4.4.2`

Tiers the map names and the index does not: component, integration.

- `tests/component/stores/partialmanifeststore_test.go` (1 test functions): cites §10.1, §11.2
- `tests/component/stores/partialcleanup_component_test.go` (1 test functions): cites §4.4, §12.5
- `tests/integration/checkpoint_minio_atomicity_test.go::TestCheckpointFinalisesCompleteOnlyAfterEveryByteConfirmed`: cites §10.1
- `tests/integration/checkpoint_minio_atomicity_test.go::TestCheckpointLeavesPartialAndSweepsChunksOnPutFailure`: cites §4.4, §10.1.7

### `4.4.3`

Tiers the map names and the index does not: e2e_kind.

- `pkg/checkpoint/checkpoint_test.go::TestAllLevelsIsExhaustive`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestConsistencyForLevel`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestCheckpointTimeoutIs60s`: unannotated
- `tests/e2e_kind/checkpoint_isolation_profile_test.go::TestCheckpointHandshakeUnderGvisorTagsConsistent`: cites §4.4
- `tests/e2e_kind/checkpoint_isolation_profile_test.go::TestSignalCheckpointingRefusedUnderSandboxedProfiles`: cites §4.4

### `4.4.4`

Tiers the map names and the index does not: unit.

- `pkg/checkpoint/checkpoint_test.go::TestAllTriggersIsExhaustive`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestWorkspaceSizePreCheckRejectsExceed`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestFreshnessCheckRejectsStale`: unannotated

### `4.4.5`

Tiers the map names and the index does not: chaos, component, docs, e2e_kind, integration, unit.

- `pkg/checkpoint/checkpoint_test.go::TestRetryBudgetForTriggers`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestAllOutcomesIsExhaustive`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestAllResumeModesIsExhaustive`: unannotated
- `pkg/checkpoint/checkpoint_test.go::TestResumeModeWorkspaceLost`: unannotated
- `tests/component/stores/evictionfallback_test.go` (5 test functions): cites §4.4
- `tests/component/stores/evictionstatestore_test.go` (1 test functions): cites §12.2.1
- `tests/integration/eviction_fallback_outage_test.go::TestEvictionFallbackMinIOOutageWritesPostgresMinimalState`: cites §4.4
- `tests/integration/eviction_fallback_outage_test.go::TestEvictionFallbackTotalLossWhenBothStoresDown`: cites §4.4
- `tests/chaos/minio_outage_during_checkpoint_test.go` (1 test functions): cites §4.4
- `tests/chaos/node_drain_during_minio_outage_test.go` (1 test functions): cites §4.4
- `tests/e2e_kind/checkpoint_resume_test.go::TestCheckpointResumeMinIOOutageFallsBackToPostgresMinimalState`: cites §4.4
- `tests/docs/eviction_coordinator_route_consistency_test.go::TestEvictionCoordinatorRouteResolvesToSurfaces`: cites §4.6.1, §4.7, §4.4

### `4.5`

- `tests/e2e_cloud/scaffolds_test.go::TestCloudCSI`: no such test function in the tree

### `4.6`

- `tests/e2e_kind/runtime_publish_journey_test.go` (1 test functions): cites §26

### `4.6.1`

- `tests/component/controllers/pod_lifecycle_kinds_test.go` (3 test functions): cites KAPI; cites KAPOD, KPCLAIM, KADMIT; cites KPSET, KAPOD, POOL
- `pkg/admission/podclaim_guard/guard_test.go::TestPatchAdmitsWhenSandboxClaimed`: no such test function in the tree
- `pkg/admission/podclaim_guard/guard_test.go::TestPatchRejectsWhenSandboxNotClaimed`: no such test function in the tree
- `tests/chaos/live_session_test.go` (1 test functions): cites §12.8
- `pkg/controller/warmpool/pod_reconciler_test.go::TestPodReconcileDrainRequestIgnoredWhenAbsent_spec_4_6`: cites §4.6.3
- `pkg/controller/warmpool/pod_reconciler_test.go::TestPodReconcileDrainRequestCorruptStampIgnored_spec_4_6`: cites §4.6.3
- `pkg/gateway/podlifecycle/podclaim/slotclaimer_test.go::TestReleaseSlotRecyclingPatchesRecyclingAndArmsTimeout_spec_3_4`: cites §3.4, §3.1, §6.41, §5.2
- `pkg/gateway/sessionserver/create_test.go` (47 test functions): cites §10.1; cites §14.1; cites §15.1, §4.6, §6.2, §7.1; cites §5.2, §7.1; and 6 more
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestReleaseRecyclingPatchesClaimToRecycling_spec_6_2`: unannotated
- `tests/integration/concurrent_slot_release_test.go::TestCrossReplicaTerminalWriteRecordsTheReleaseOnTheBindingReplica_spec_7_1`: cites §7.1, §10.1.1, §5.2

### `4.6.2`

- `pkg/controller/poolscaling/strategy/strategy_test.go::TestComputeStandardSession`: unannotated
- `pkg/controller/poolscaling/strategy/strategy_test.go::TestComputeTaskModeReducesSteady`: no such test function in the tree
- `pkg/controller/poolscaling/strategy/strategy_test.go::TestComputeConcurrentModeReducesBothTerms`: no such test function in the tree
- `pkg/controller/poolscaling/strategy/strategy_test.go::TestComputeVariantPool`: unannotated
- `pkg/controller/poolscaling/strategy/strategy_test.go::TestComputeAdjustedBasePool`: unannotated
- `pkg/controller/poolscaling/strategy/strategy_test.go::TestComputeBootstrapMode`: unannotated
- `pkg/controller/poolscaling/strategy/strategy_test.go::TestComputeRejectsVariantWeightsExceedingOne`: unannotated

### `4.6.3`

- `pkg/admission/ownership/ownership_test.go::TestValidateAllowsOwnedWrites`: unannotated
- `pkg/admission/ownership/ownership_test.go::TestValidateRejectsCrossControllerWrites`: unannotated
- `pkg/admission/ownership/ownership_test.go::TestOwnerSdkWarmCircuitBreakerOverridesDefault`: unannotated
- `pkg/admission/ownership/ownership_test.go::TestAllRulesEnumerateMatrix`: unannotated
- `pkg/chart/values/cluster_role_verbs_test.go::TestClusterRolesGrantRequiredVerbs`: unannotated
- `pkg/controller/ssaretry/ssaretry_test.go::TestRetryOnConflictSSAEmptyCRDIsSilent`: cites §16.1
- `tests/chaos/config_drift_test.go` (4 test functions): cites §12.8
- `pkg/gateway/sessionserver/start_pod_test.go` (27 test functions): cites §15.1, §13.4, §7.4; cites §15.1, §4.9; cites §4.2; cites §4.6.1, §5.2; and 14 more
- `tests/integration/recycle_scrub_path_test.go::TestRecyclePathConcurrentPerReleaseSessionCountDrains_spec_5_2`: cites §5.2, §16.1, §6.2
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestReleaseRecyclingPatchesClaimToRecycling_spec_6_2`: unannotated
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestReleaseNonRecyclingDeletesClaimWritesNoAgentPodStatus`: unannotated
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestBindWritesNoAgentPodStatusThroughSetupChain`: unannotated
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestReleaseExpiredDeletesClaimWithoutAgentPodStatus`: unannotated
- `tests/docs/session_failure_outcome_doc_reconciliation_test.go::TestRetiredFailureOutcomePhrasesAbsent`: cites §7.2, §7.3, §28.5.1
- `tests/docs/session_failure_outcome_doc_reconciliation_test.go::TestFailureOutcomeEdgesStated`: cites §7.2, §7.3, §6.2, §28.5.1, §5.2

### `4.7`

- `tests/contract/adapter_session_address/session_address_wire_test.go` (4 test functions): cites §15.4; cites §4.1, §5.2; cites §5.2, §4.1
- `pkg/adapter/peercredselftest_test.go::TestPeercredSelftest_spec_4_7_passes_on_loopback`: unannotated
- `tests/load_local/coordinator_hold_termination_race_test.go` (2 test functions): cites §10.1, §10.1.4, §5.2
- `tests/component/migrations/prod_columns_test.go` (10 test functions): cites §11.1; cites §12.2; cites §12.2, §18.5; cites §12.8; and 6 more

### `4.7.1`

- `tests/contract/adapter_chunk_encoding/chunk_encoding_wire_test.go` (2 test functions): cites §10.1, §4.4
- `tests/contract/adapter_jsonl/message_before_session_start_test.go` (1 test functions): cites §28.5.3
- `tests/load_local/message_session_start_order_race_test.go` (1 test functions): cites §28.5.3
- `pkg/adapter/one_session_only_test.go` (8 test functions): cites §4.7, §5.2; cites §4.7, §6.1; cites §5.2, §6.1
- `pkg/adapter/resume_test.go` (14 test functions): cites §10.1; cites §10.1.7; cites §13.2; cites §4.7, §5.2; and 3 more
- `pkg/adapter/slot_test.go` (11 test functions): cites §4.7; cites §5.2; cites §5.2, §6.4; cites §6.1; and 1 more
- `pkg/adapter/server_test.go` (16 test functions): cites §4.7; unannotated
- `pkg/adapter/session_test.go` (14 test functions): cites §11.4; cites §28.5.3, §5.2; cites §4.7, §5.2; cites §4.7, §5.2, §11.4; and 3 more
- `pkg/adapter/checkpoint_stream_test.go` (13 test functions): cites §10.1.8, §10.1.2; cites §28.5.3, §4.4; cites §4.1, §5.2, §6.4; cites §4.4; and 6 more
- `tests/contract/checkpoint_stream/checkpoint_stream_wire_test.go` (8 test functions): cites §10.1.7, §11.2, §4.7; cites §10.1.7, §11.2, §4.7, §5.2; cites §4.1, §5.2, §6.4; cites §4.4.5, §13.2
- `tests/contract/adapter_checkpointbarrier/checkpointbarrier_wire_test.go` (1 test functions): cites §10.1, §4.7
- `tests/contract/adapter_extendcredlease/extend_credential_lease_wire_test.go` (3 test functions): cites §4.9, §4.7; cites §4.9, §6.1
- `tests/contract/gatewaycontrol_scrub/scrub_wire_test.go` (8 test functions): cites §4.7, §3.4; cites §4.7, §3.4, §5.2; cites §4.7, §5.2; cites §5.2; and 1 more
- `tests/contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go` (4 test functions): cites §4.1, §4.7, §5.2; cites §4.7, §5.2; cites §5.2, §4.7
- `tests/integration/token_service_unavailability_guard_test.go` (2 test functions): cites §4.9
- `tests/chaos/token_service_unavailability_guard_test.go` (2 test functions): cites §4.9; cites §4.9, §8.8
- `pkg/adapter/socketruntime_test.go` (14 test functions): cites §28.5.3; cites §4.7.10, §28.5.3, §5.2; cites §4.7.10, §5.2; cites §4.7.10, §5.2, §15.4.3, §28.5.3; and 5 more
- `tests/load_local/session_frame_order_race_test.go` (1 test functions): cites §28.5.3, §5.2
- `tests/integration/concurrent_workspace_test.go` (2 test functions): cites §5.2, §6.4; cites §7.1, §5.2
- `tests/load_local/coordinator_hold_termination_race_test.go` (2 test functions): cites §10.1, §10.1.4, §5.2

### `4.7.2`

Tiers the map names and the index does not: unit.

- `pkg/adapter/oplock_test.go::TestOpLockSerializesAndPromotes`: unannotated
- `pkg/adapter/oplock_test.go::TestOpLockPromotesCheckpointsInSessionIdentifierOrder`: cites §5.2, §4.7
- `pkg/adapter/oplock_test.go::TestOpLockCoalescesSameSessionAdmitsDistinctSession`: cites §5.2, §4.7
- `pkg/adapter/oplock_test.go::TestOpLockRejectsSecondQueuedInterrupt`: cites §4.7
- `pkg/adapter/oplock_test.go::TestOpLockInterruptDuringCheckpointDeliversAfterComplete`: unannotated

### `4.7.3`

Tiers the map names and the index does not: unit.

- `pkg/adapter/adapterevents_test.go` (6 test functions): cites §4.7; cites §6.1; unannotated
- `tests/contract/adapter_checkpointbarrier/checkpointbarrier_wire_test.go` (1 test functions): cites §10.1, §4.7
- `tests/contract/adapter_reportusage/reportusage_wire_test.go` (3 test functions): cites §4.7, §11.2; cites §4.7, §11.2, §10.1, §4.1
- `tests/contract/lifecycle_tokens/tokens_wire_test.go` (4 test functions): cites §4.7; cites §4.7, §11.2

### `4.7.4`

Tiers the map names and the index does not: contract, unit.

- `tests/contract/adapter_jsonl/messages_test.go` (8 test functions): cites §15.4
- `tests/contract/adapter_jsonl/mcp_nonce_wire_test.go` (2 test functions): cites §15.4.3
- `pkg/adapter/runtimeops_test.go` (17 test functions): cites §15.5; cites §28.5.3; cites §28.5.3, §4.4; cites §28.5.3, §4.7.11; and 3 more

### `4.7.5`

Tiers the map names and the index does not: unit.

- `pkg/adapter/manifest_test.go` (11 test functions): cites §4.7; cites §4.7.6; unannotated
- `pkg/adapter/warmlayout_test.go` (11 test functions): cites §6.1, §6.4; cites §6.4; unannotated

### `4.7.6`

- `pkg/adapter/manifest_fields_test.go` (2 test functions): cites §4.7

### `4.7.7`

Tiers the map names and the index does not: conformance, unit.

- `pkg/adapter/integrationlevel_test.go` (6 test functions): cites §5.1; cites §5.1, §15.4.3; cites §5.1, §4.7
- `tests/conformance/recycle_scrub_conformance_test.go` (5 test functions): cites §4.7, §4.7.10, §5.2; cites §5.2, §4.7; cites §5.2, §4.7.10, §4.7

### `4.7.8`

Tiers the map names and the index does not: chaos, integration, unit.

- `pkg/adapter/credentials_test.go` (11 test functions): cites §4.7, §28.5.3; cites §4.9, §6.1; cites §6.1; cites §6.1, §4.9; and 2 more
- `pkg/adapter/credexpiry_test.go` (17 test functions): cites §4.9; cites §6.1, §4.9; unannotated
- `pkg/adapter/rotationgate_test.go` (4 test functions): unannotated
- `tests/integration/credential_lifecycle_test.go::TestCredentialLifecycleAssignRotateRebindRevokeTerminate`: cites §4.7
- `tests/chaos/credential_rotation_ceiling_test.go::TestRotationInflightCeilingForcesRotatedFrameOnWithholdingRuntime_spec_4_7`: cites §4.7, §4.9
- `tests/chaos/credential_rotation_ceiling_test.go::TestProactiveRenewalRotationWaitsUnboundedForWithheldRequest_spec_4_7`: cites §4.7, §4.9
- `tests/chaos/credential_rotation_ceiling_test.go::TestRotationAckTimeoutFallsThroughToStandardPath_spec_4_7`: cites §4.7

### `4.7.9`

- `pkg/adapter/socketruntime_test.go` (14 test functions): cites §28.5.3; cites §4.7.10, §28.5.3, §5.2; cites §4.7.10, §5.2; cites §4.7.10, §5.2, §15.4.3, §28.5.3; and 5 more
- `pkg/adapter/socketruntime_e2e_test.go` (3 test functions): cites §4.7.10, §15.4; unannotated
- `pkg/adapter/sdkwarm_test.go` (13 test functions): cites §4.7; cites §4.7.1, §6.1, §7.1; cites §6.1; unannotated

### `4.7.10`

- `tests/security/runtime_socket_peercred_test.go::TestRuntimeSocketKeepsAcceptingAfterRefusals_spec_4_7_11`: cites §4.7.11, §28.5.3
- `tests/integration/concurrent_workspace_test.go` (2 test functions): cites §5.2, §6.4; cites §7.1, §5.2
- `pkg/adapter/embedded_test.go` (5 test functions): cites §28.5.3; unannotated
- `pkg/adapter/embedded_sdkwarm_test.go` (1 test functions): cites §6.1
- `tests/contract/embedded_gatewaycallback/callback_reachability_test.go` (4 test functions): cites §4.7, §15.3; cites §4.7, §8.6, §15.3, §17.4; cites §4.7, §8.6, §9.1, §17.4
- `pkg/adapter/sessionframes_test.go` (15 test functions): cites §28.5.3; cites §28.5.3, §4.7.1, §5.2; cites §28.5.3, §4.7.11; cites §28.5.3, §6.1; and 1 more
- `pkg/adapter/export_test.go`: the file exists and declares no test function

### `4.7.11`

Tiers the map names and the index does not: integration.

- `pkg/adapter/manifest_fields_test.go` (2 test functions): cites §4.7
- `pkg/adapter/peercredselftest_test.go::TestPeercredSelftest_spec_4_7_passes_on_loopback`: unannotated
- `pkg/adapter/credredact_test.go` (5 test functions): cites §16.4; unannotated
- `tests/security/adapter_peercred_test.go::TestAdapterPeercredSelftestPassesUnderRuncAndGvisor`: cites §4.7
- `tests/security/adapter_peercred_test.go::TestAdapterPeercredSelftestFaultCrashLoops`: cites §4.7, §13.1
- `tests/security/adapter_mcp_nonce_test.go::TestPlatformMCPRejectsNonceLessAdversary_spec_4_7`: cites §4.7, §15.4.3
- `tests/security/adapter_mcp_nonce_test.go::TestConnectorMCPRejectsNonceLessAdversary_spec_4_7`: cites §4.7, §15.4.3
- `tests/security/adapter_mcp_nonce_test.go::TestPlatformMCPNonceBearerReachesPrivilegedTool_spec_4_7`: cites §4.7, §15.4.3
- `tests/security/credential_file_contract_test.go::TestCredentialFileDeliveryContract`: cites §4.7, §13.1
- `cmd/lenny-ctl/runtimescaffold/scaffold_test.go` (11 test functions): cites §26.12; cites §6.4, §6.1; unannotated
- `tests/conformance/token_service_unavailability_guard_conformance_test.go` (1 test functions): cites §4.9, §4.7
- `tests/contract/adapter_usage_wired/wired_reportusage_test.go` (4 test functions): cites §28.5.3, §11.2; cites §4.7; cites §4.7, §11.2
- `tests/integration/concurrent_workspace_test.go` (2 test functions): cites §5.2, §6.4; cites §7.1, §5.2

### `4.8`

- `pkg/gateway/mcpfabric/delegationpolicystore/...` (32 test functions): cites §4.2; unannotated
- `tests/component/translators/openai_singleshot_lifecycle_test.go` (8 test functions): cites §15, §5.2, §6.2, §4.6.3; cites §15, §6.2, §4.6.3; cites §15, §7.2; cites §15.2.1, §11.1, §10.6, §11.4, §11.2; and 2 more

### `4.9`

- `pkg/credential/credential_test.go::TestAllProvidersIsExhaustive`: unannotated
- `pkg/credential/credential_test.go::TestAllAssignmentStrategiesIsExhaustive`: unannotated
- `pkg/credential/credential_test.go::TestAllRotationTriggersIsExhaustive`: unannotated
- `pkg/credential/credential_test.go::TestRotationTriggerIsFaultTriggered`: unannotated
- `pkg/credential/credential_test.go::TestRotationTriggerIsCeilingApplicable`: unannotated
- `pkg/credential/credential_test.go::TestRotationTriggerCountsAgainstBudget`: unannotated
- `pkg/credential/credential_test.go::TestPoolConfigValidateHappy`: unannotated
- `pkg/credential/credential_test.go::TestPoolConfigValidateRejectsBadFields`: unannotated
- `tests/integration/credential_lifecycle_test.go` (2 test functions): cites §4.7
- `tests/chaos/testbed_partition_test.go` (1 test functions): cites §12.8
- `tests/integration/concurrent_delegation_proxy_test.go::TestConcurrentSlotsDelegationAndProxyIsolation_spec_5_2`: cites §5.2
- `pkg/adapter/manifest_fields_test.go` (2 test functions): cites §4.7
- `tests/component/migrations/prod_columns_test.go` (10 test functions): cites §11.1; cites §12.2; cites §12.2, §18.5; cites §12.8; and 6 more

### `5`

- `pkg/admission/label_immutability/label_immutability_test.go::TestDecideAllowsTenantInitialAssignmentByGateway`: unannotated
- `pkg/admission/label_immutability/label_immutability_test.go::TestDecideRejectsTenantInitialAssignmentByOtherSA`: unannotated
- `pkg/admission/label_immutability/label_immutability_test.go::TestDecideAllowsTenantResetByWarmPoolController`: unannotated
- `pkg/admission/label_immutability/label_immutability_test.go::TestDecideRejectsCrossTenantRewrite`: unannotated
- `pkg/admission/label_immutability/label_immutability_test.go::TestDecideRejectsManagedLabelMutation`: unannotated

### `5.1`

- `tests/integration/checkpoint_workspace_derive_test.go` (8 test functions): cites §10.1, §4.5; cites §15.1, §10.1; cites §15.1, §7.1, §10.1; cites §15.1, §7.1, §7.4; and 2 more
- `cmd/lenny-ctl/runtimescaffold/scaffold_test.go` (11 test functions): cites §26.12; cites §6.4, §6.1; unannotated
- `tests/component/migrations/prod_columns_test.go` (10 test functions): cites §11.1; cites §12.2; cites §12.2, §18.5; cites §12.8; and 6 more

### `5.2`

- `tests/integration/checkpoint_workspace_derive_test.go` (8 test functions): cites §10.1, §4.5; cites §15.1, §10.1; cites §15.1, §7.1, §10.1; cites §15.1, §7.1, §7.4; and 2 more
- `pkg/admission/label_immutability/label_immutability_test.go::TestDecideAllowsTenantInitialAssignmentByGateway`: unannotated
- `pkg/admission/label_immutability/label_immutability_test.go::TestDecideRejectsTenantInitialAssignmentByOtherSA`: unannotated
- `pkg/admission/label_immutability/label_immutability_test.go::TestDecideAllowsTenantResetByWarmPoolController`: unannotated
- `pkg/admission/label_immutability/label_immutability_test.go::TestDecideRejectsCrossTenantRewrite`: unannotated
- `pkg/admission/label_immutability/label_immutability_test.go::TestDecideRejectsManagedLabelMutation`: unannotated
- `pkg/gateway/runtime/poolstore/poolstore_test.go::TestValidateConcurrentConfig`: no such test function in the tree
- `pkg/sandbox/state/state_test.go::TestConcurrentOccupancyCollapsesToClaimed_spec_6_2`: unannotated
- `tests/integration/concurrent_stateless_test.go`: names no test function in the tree
- `tests/load_cloud/session_mode_test.go` (4 test functions): cites §12.7
- `tests/contract/adapter_frame_resolution/session_scoped_frame_resolution_test.go::TestUnaddressedFrameResolvesOnAPodHoldingOneSlot_spec_28_5_3`: cites §28.5.3
- `tests/contract/adapter_frame_resolution/session_scoped_frame_resolution_test.go::TestUnaddressedFrameIsRejectedOnAPodHoldingTwoSlots_spec_28_5_3`: cites §28.5.3
- `tests/contract/adapter_frame_resolution/session_scoped_frame_resolution_test.go::TestInboundEnvelopeCarriesTheAddressedSession_spec_28_5_3`: cites §28.5.3
- `tests/contract/adapter_jsonl/gateway_envelope_address_test.go::TestGatewayEnvelopeCarriesTheAddressedSession_spec_28_5_3`: cites §28.5.3
- `tests/integration/delegation_child_materialization_test.go` (3 test functions): cites §8.2; cites §8.3
- `tests/component/stores/reserve_slot_under_lock_test.go::TestReserveSlotUnderLock`: cites §12.4
- `pkg/adapter/usage_test.go` (13 test functions): cites §11.2; cites §28.5.3, §11.2; cites §28.5.3, §4.7; cites §4.7; and 1 more
- `pkg/controller/agentpod/podspec/podspec_test.go` (38 test functions): cites §13.1; cites §4.6.1, §4.7.10; cites §6.4, §4.7; unannotated
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestReleaseRecyclingArmsMissingReportTimeout_spec_5_2`: unannotated
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestReleaseRecyclingFailedDoesNotArmTimeout_spec_5_2`: unannotated
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestReleaseRecyclingFailedDrainsNotRecycle_spec_5_2`: unannotated
- `pkg/gateway/runtime/slothealth/slothealth_test.go::TestForget`: unannotated
- `tests/load_local/tracing_context_release_race_test.go` (3 test functions): cites §28.5.3, §6.4, §8.3; cites §28.5.3, §8.3
- `pkg/gateway/sessionserver/slotretry_test.go::TestSlotBindErrorReason_spec_5_2`: unannotated
- `tests/component/stores/agentpodstatestore_test.go::TestAgentPodStateClaimIdle`: cites §4.6.1
- `pkg/gateway/runtime/poolstore/runtime_process_test.go::TestValidateRecyclePolicyRefusesRestartInterim`: unannotated
- `tests/load_local/scenarios/vm_restart_recycle_disposition`: names no test function in the tree
- `tests/e2e_kind/stream_failure_kind_test.go::TestStreamFailureRetiresTheExclusivePodAndRestoresOntoAnotherPod_spec_28_5_1`: cites §28.5.1, §7.3, §6.2
- `tests/docs/session_failure_outcome_doc_reconciliation_test.go::TestRetiredFailureOutcomePhrasesAbsent`: cites §7.2, §7.3, §28.5.1
- `pkg/adapter/scrub/scrub_supervised_test.go::TestRunJoinsEnumerationAndRemovalErrorsInStep0_spec_5_2`: unannotated
- `pkg/gateway/sessionserver/lifecycle_internal_test.go::TestEmitTerminalLifecycleRecordsVariantMetrics_spec_10_7_1124`: cites §10.7, §16.1

### `5.3`

- `pkg/admission/webhook/direct_mode_isolation_test.go` (6 test functions): cites §13.2; unannotated
- `tests/component/migrations/prod_columns_test.go` (10 test functions): cites §11.1; cites §12.2; cites §12.2, §18.5; cites §12.8; and 6 more

### `6`

- `pkg/sandbox/state/state_test.go::TestAllStatesIncludePhase1Additions`: no such test function in the tree
- `tests/load_kind/scenarios/startup_latency`: names no test function in the tree

### `6.1`

Tiers the map names and the index does not: conformance, integration.

- `pkg/controller/warmpool/...` (151 test functions): cites §12.9.8; cites §13.1; cites §13.2; cites §16.1; and 38 more
- `tests/e2e_cloud/cluster_assertions_test.go::TestGvisorIsolation`: cites §5.3, §13.1
- `tests/e2e_cloud/cluster_assertions_test.go::TestConfidentialContainersIsolation`: cites §12.6, §17.5
- `tests/e2e_kind/gvisor_isolation_test.go::TestGvisorIsolation`: cites §5.3
- `tests/integration/concurrent_delegation_proxy_test.go::TestConcurrentSlotsDelegationAndProxyIsolation_spec_5_2`: cites §5.2
- `tests/conformance/credential_path_resolution_conformance_test.go` (9 test functions): cites §28.5.3, §4.7.10, §4.7.11; cites §28.5.3, §4.7.11; cites §28.5.3, §4.7.11, §4.9
- `tests/docs/retirement_sweep_surfaces_test.go` (2 test functions): cites §6.4
- `tests/docs/rotation_ceiling_cotenant_reconciliation_test.go` (3 test functions): cites §4.7, §16.1, §16.5; cites §4.7, §16.5
- `sdks/runtime/go/runtime/runtime_test.go` (13 test functions): cites §15.5; cites §15.7, §28.5.3; cites §15.7, §28.5.3, §4.7.10; cites §28.5.3, §15.5; and 2 more
- `tests/integration/token_service_unavailability_guard_test.go` (2 test functions): cites §4.9
- `tests/chaos/token_service_unavailability_guard_test.go` (2 test functions): cites §4.9; cites §4.9, §8.8
- `pkg/adapter/manifest_fields_test.go` (2 test functions): cites §4.7
- `tests/docs/session_failure_outcome_doc_reconciliation_test.go::TestRetiredFailureOutcomePhrasesAbsent`: cites §7.2, §7.3, §28.5.1
- `tests/docs/session_failure_outcome_doc_reconciliation_test.go::TestFailureOutcomeEdgesStated`: cites §7.2, §7.3, §6.2, §28.5.1, §5.2

### `6.2`

- `tests/integration/start_starting_state_integration_test.go::TestStartLaunchFailureInsideTheBinderFailsTheSession_spec_7_2`: cites §7.2, §15.1
- `pkg/sandbox/state/state_test.go::TestAllStatesIncludePhase1Additions`: no such test function in the tree
- `pkg/gateway/runtime/slothealth/slothealth_test.go::TestForgetClearsPersistentLeaks_spec_6_2`: unannotated
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestBindRejectsUnderperformingRuntime_spec_5_1`: cites §5.1
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestReleaseRecyclingPatchesClaimToRecycling_spec_6_2`: unannotated
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestReleaseRecyclingFailedDoesNotArmTimeout_spec_5_2`: unannotated
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestReleaseRecyclingFailedDrainsNotRecycle_spec_5_2`: unannotated
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestPrepareReclaimReturnsSlotBindRefusalWithoutDrain_spec_7_1`: cites §7.1, §5.2
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestLaunchReclaimReturnsAlreadyStartedWithoutDrain_spec_7_1`: cites §7.1, §5.2
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestConcurrentPrepareLoserRefusalLeavesWinnerPodUndrained_spec_7_1`: cites §7.1, §5.2
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestPrepareMintsOneBindAttemptPerAttempt_spec_4_7_1`: cites §4.7.1, §7.1, §5.2
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestResumeMintsItsOwnBindAttempt_spec_4_7_1`: cites §4.7.1, §7.1, §5.2
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestLaunchSendsNoBindAttempt_spec_4_7_1`: cites §4.7.1, §7.1, §5.2
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestShutdownAdapterSendsUnconditionalTeardown_spec_4_7_1`: cites §4.7.1, §7.1, §5.2
- `pkg/gateway/sessionserver/upload_to_session_test.go` (8 test functions): cites §4.7.1, §7.4, §7.1; cites §7.4; cites §7.4, §5.2, §15.1; unannotated
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestPrepareCredentialReleaseIsScopedToTheAttempt_spec_7_1`: cites §7.1, §5.2
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestResumeFailureCompensatesWithItsOwnToken_spec_7_1`: cites §7.1, §7.3, §5.2
- `pkg/gateway/podlifecycle/podsession/binder_test.go::TestResumeCompensationOutcomeReachesTheReclaimHook_spec_7_1`: cites §7.1, §5.2
- `pkg/gateway/sessionserver/slotretry_test.go::TestSlotRetryReleasesWithTheReclaimDisposition_spec_7_1`: cites §7.1, §5.2
- `tests/docs/session_failure_outcome_doc_reconciliation_test.go::TestRetiredFailureOutcomePhrasesAbsent`: cites §7.2, §7.3, §28.5.1
- `tests/load_local/suspension_interleaving_test.go::TestHeldDeliveryAndReleaseRequiredPairs_spec_7_2`: cites §7.2, §7.3

### `6.3`

- `tests/load_kind/scenarios/startup_latency`: names no test function in the tree

### `6.4`

Tiers the map names and the index does not: chaos, conformance.

- `pkg/adapter/workspace/materialize_test.go` (19 test functions): cites §14; cites §7.1; cites §7.1, §5.1, §14.1; unannotated
- `tests/contract/adapter_session_address/session_address_wire_test.go` (4 test functions): cites §15.4; cites §4.1, §5.2; cites §5.2, §4.1
- `tests/conformance/concurrent_slot_conformance_test.go::TestConcurrentSessionDispatchConformance`: cites §5.2
- `tests/load_local/racestart_testsupport_test.go`: the file exists and declares no test function
- `tests/contract/adapter_frame_resolution/session_scoped_frame_resolution_test.go::TestInboundEnvelopeCarriesTheAddressedSession_spec_28_5_3`: cites §28.5.3
- `tests/chaos/token_service_unavailability_guard_test.go` (2 test functions): cites §4.9; cites §4.9, §8.8
- `tests/component/migrations/prod_columns_test.go` (10 test functions): cites §11.1; cites §12.2; cites §12.2, §18.5; cites §12.8; and 6 more
- `pkg/adapter/slotlayout/slotlayout_test.go` (10 test functions): cites §6.1; unannotated

### `6.5`

No numbered heading carries this number.

Tiers the map names and the index does not: load_kind.

- `tests/load_kind/streaming_test.go::TestStreamingThroughput`: cites §13.17
- `tests/load_kind/scenarios/streaming_throughput`: names no test function in the tree

### `7`

- `pkg/checkpoint/...` (20 test functions): cites §10.1.7; unannotated
- `tests/component/auditintegrity/integrity_test.go` (5 test functions): cites §11.7

### `7.1`

- `pkg/session/...` (10 test functions): cites §7.2; cites §7.2, §6.2; unannotated
- `tests/e2e_kind/checkpoint_resume_test.go::TestCheckpointThenResumeRestoresWorkspace`: cites §4.4
- `pkg/adapter/resume_test.go` (14 test functions): cites §10.1; cites §10.1.7; cites §13.2; cites §4.7, §5.2; and 3 more

### `7.2`

- `tests/docs/resume_override_api_doc_reconciliation_test.go` (6 test functions): cites §15.1; cites §15.1, §11.1; cites §15.1, §7.3; cites §15.2, §11.1; and 1 more
- `pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go::TestReportResumeFailureSetupCommand_spec_7_3`: cites §7.3, §15.1, §6.2
- `tests/e2e_kind/ha_streaming_replica_test.go::TestHAStreamingReconnectAcrossGatewayReplicas`: cites §10.1
- `sdks/runtime/go/runtime/runtime_test.go` (13 test functions): cites §15.5; cites §15.7, §28.5.3; cites §15.7, §28.5.3, §4.7.10; cites §28.5.3, §15.5; and 2 more
- `pkg/adapter/manifest_test.go` (11 test functions): cites §4.7; cites §4.7.6; unannotated
- `pkg/gateway/session/executor/podstream_internal_test.go::TestEvictStreamCancelsTheStreamAndIsSilent_spec_28_5_1`: cites §28.5.1
- `pkg/gateway/session/executor/podstream_internal_test.go::TestReleaseEndOfStreamIsSilent_spec_28_5_1`: cites §28.5.1
- `pkg/gateway/session/executor/podstream_internal_test.go::TestStreamEndEvictsAndNextSendReattaches_spec_28_5_1`: cites §28.5.1
- `pkg/gateway/session/executor/podstream_internal_test.go::TestSendWaitingOnAReplyReturnsWhenTheStreamEnds_spec_28_5_1`: cites §28.5.1
- `pkg/gateway/session/executor/podstream_internal_test.go::TestStaleReaderLeavesTheReplacementCached_spec_28_5_1`: cites §28.5.1
- `pkg/gateway/session/executor/podstream_internal_test.go::TestFailedOpenReachesEveryWaiterAndIsRetried_spec_28_5_1`: cites §28.5.1
- `pkg/gateway/session/executor/podstream_internal_test.go::TestOpenWithoutAdapterFails_spec_28_5_1`: cites §28.5.1
- `pkg/gateway/session/executor/podstream_internal_test.go::TestSendWaitingForTheTokenHonorsItsContext_spec_28_5_1`: cites §28.5.1
- `pkg/gateway/session/executor/podstream_internal_test.go::TestFailedWriteReleasesTheTurn_spec_28_5_1`: cites §28.5.1
- `pkg/gateway/session/executor/podstream_internal_test.go::TestFailedWriteWaitsForTheStreamEndObserver_spec_28_5_1`: cites §28.5.1
- `pkg/gateway/session/executor/podstream_internal_test.go::TestTurnOnAnEndedStreamFails_spec_28_5_1`: cites §28.5.1
- `pkg/gateway/session/executor/podstream_internal_test.go::TestWaitForOpenHonorsTheCallerContext_spec_28_5_1`: cites §28.5.1
- `pkg/gateway/session/executor/podstream_internal_test.go::TestAwaitReplyWithoutAReplyReturnsTheCause_spec_28_5_1`: cites §28.5.1
- `tests/docs/build_gaps_suspend_resume_records_currency_test.go::TestSuspendResumeFiledFindingsAreFiledOnce_spec_7_2`: unannotated
- `tests/docs/build_gaps_suspend_resume_records_currency_test.go::TestFiledSuspendResumeFindingsMatchTheTree_spec_7_2`: unannotated
- `tests/docs/build_gaps_suspend_resume_records_currency_test.go::TestBlockedSkipsNameOpenFindings_spec_7_2`: unannotated
- `tests/docs/build_gaps_suspend_resume_records_currency_test.go::TestSuspendResumeRecordProbes_spec_7_2`: unannotated
- `tests/docs/build_gaps_suspend_resume_records_currency_test.go::TestOffHolderInboxFindingCrossReferencesTheForwardFinding_spec_29_3`: unannotated
- `tests/integration/holder_forward_reread_test.go::TestForwardedRequestWhoseRowLeftTheForwardStatesIsRefusedAtANonHolder_spec_29_3`: cites §29.3, §28.5.4
- `tests/integration/holder_forward_routes_test.go::TestOffHolderMCPToolCallsForwardAsToolCalls_spec_29_3`: cites §29.3, §2, §3, §4, §6
- `pkg/gateway/mcpfabric/mcptools/messaging_handler_test.go::TestForwardedSendSkipsTheSenderLimitsOnTheCoordinator_spec_29_3`: cites §29.3, §8.3

### `7.3`

- `pkg/checkpoint/...` (20 test functions): cites §10.1.7; unannotated
- `tests/e2e_kind/ha_streaming_replica_test.go::TestHAStreamingReconnectAcrossGatewayReplicas`: cites §10.1
- `pkg/gateway/checkpoint/checkpointer/checkpointer_test.go` (38 test functions): cites §10.1.7; cites §10.1.7, §7.1; cites §12.5; cites §12.5, §4.4; and 5 more
- `pkg/gateway/runtime/adapterclient/client_test.go` (63 test functions): cites §11.3; cites §13.2; cites §4.7; cites §4.7, §11.2; and 15 more
- `tests/component/migrations/prod_columns_test.go` (10 test functions): cites §11.1; cites §12.2; cites §12.2, §18.5; cites §12.8; and 6 more
- `tests/e2e_kind/stream_failure_kind_test.go::TestStreamFailureConcurrentPoolDrainsAtThreshold_spec_5_2`: cites §5.2, §28.5.1
- `tests/docs/build_gaps_stream_failure_records_currency_test.go::TestClosedStreamFindingsCiteLandedCodeAndTests_spec_7_3`: unannotated
- `tests/docs/build_gaps_stream_failure_records_currency_test.go::TestRemediationPlanRecordsTheStreamFailureProposal_spec_28_5_1`: unannotated
- `tests/docs/build_gaps_stream_failure_records_currency_test.go::TestStreamFailureRecordProbes_spec_28_5_1`: unannotated
- `tests/docs/build_gaps_suspend_resume_records_currency_test.go::TestClosedSuspendResumeFindingsCiteLandedCodeAndTests_spec_7_3`: unannotated
- `tests/docs/build_gaps_suspend_resume_records_currency_test.go::TestRemediationPlanRecordsTheSuspendResumeProposal_spec_7_3`: unannotated

### `7.4`

- `pkg/gateway/podlifecycle/podsession/binder_archive_test.go::TestBindUploadArchiveAbortRecordsMetric_MaliciousArchives`: unannotated
- `pkg/adapter/staging_test.go` (20 test functions): cites §14; cites §14.1; cites §5.1; cites §5.2; and 3 more

### `7.5`

- `pkg/adapter/staging_test.go` (20 test functions): cites §14; cites §14.1; cites §5.1; cites §5.2; and 3 more
- `pkg/gateway/runtime/adapterclient/client_test.go` (63 test functions): cites §11.3; cites §13.2; cites §4.7; cites §4.7, §11.2; and 15 more

### `8`

- `pkg/session/state/...` (10 test functions): cites §7.2; cites §7.2, §6.2; unannotated

### `8.2`

- `pkg/delegation/cycle/cycle_test.go::TestAllModesIsExhaustive`: unannotated
- `pkg/delegation/cycle/cycle_test.go::TestIdentityEqualsRequiresBothFields`: unannotated
- `pkg/delegation/cycle/cycle_test.go::TestDecideNonCyclicHopAdmits`: unannotated
- `pkg/delegation/cycle/cycle_test.go::TestDecidePermissiveAdmitsSelfRecursion`: unannotated
- `pkg/delegation/cycle/cycle_test.go::TestDecideEnforceAdmitsWhenAllLayersTrue`: unannotated
- `pkg/delegation/cycle/cycle_test.go::TestDecideEnforceRejectsAndAttributesBlocker`: unannotated
- `pkg/delegation/cycle/cycle_test.go::TestDecideWarnAdmitsAndRecordsBlocker`: unannotated
- `pkg/delegation/cycle/cycle_test.go::TestDecidePoolDifferentiatedIsNotCycle`: unannotated
- `pkg/delegation/cycle/cycle_test.go::TestToErrorMapsRejectionToTypedError`: unannotated
- `pkg/delegation/lease/lease_test.go::TestValidateChildSliceWithinBudget`: unannotated
- `pkg/delegation/lease/lease_test.go::TestValidateChildSliceRejectsTokenBudgetOverrun`: unannotated
- `pkg/delegation/lease/lease_test.go::TestValidateChildSliceRejectsParallelOverrun`: unannotated
- `pkg/delegation/lease/lease_test.go::TestResolveMaxDepthApplies82BisPrecedence`: unannotated
- `pkg/delegation/lease/lease_test.go::TestEnforcePolicyCeilingCapsResolved`: unannotated
- `pkg/delegation/lease/lease_test.go::TestCheckDepthRejectsAtCeiling`: unannotated
- `tests/integration/concurrent_delegation_proxy_test.go::TestConcurrentSlotsDelegationAndProxyIsolation_spec_5_2`: cites §5.2

### `8.3`

- `pkg/delegation/lease/lease_test.go::TestValidateChildSliceRejectsParallelOverrun`: unannotated
- `pkg/delegation/lease/lease_test.go::TestValidateChildSliceRejectsTokenBudgetOverrun`: unannotated
- `pkg/delegation/lease/lease_test.go::TestValidateChildSliceWithinBudget`: unannotated
- `tests/integration/interceptor_weakening_cooldown_test.go::TestInterceptorWeakeningCooldownComposed_spec_4_8_1058`: cites §4.8
- `tests/component/migrations/prod_columns_test.go` (10 test functions): cites §11.1; cites §12.2; cites §12.2, §18.5; cites §12.8; and 6 more
- `tests/integration/holder_forward_routes_test.go::TestOffHolderMCPToolCallsForwardAsToolCalls_spec_29_3`: cites §29.3, §2, §3, §4, §6

### `8.4`

- `pkg/gateway/mcpfabric/delegationpolicystore/...` (32 test functions): cites §4.2; unannotated

### `8.5`

- `pkg/gateway/mcpfabric/delegation/...` (144 test functions): cites §11.4, §12.8; cites §12.4; cites §12.8; cites §13.5; and 21 more

### `8.6`

- `pkg/delegation/lease/...` (18 test functions): cites §4.2; cites §8.3; cites §8.4; unannotated

### `8.8`

- `pkg/session/state/state_test.go::TestMCPProtocolStateSessionMapping_spec_8_8_871`: unannotated
- `pkg/session/state/state_test.go::TestMCPProtocolStateUnknownStateReturnsEmpty_spec_8_8_871`: unannotated
- `tests/component/migrations/prod_columns_test.go` (10 test functions): cites §11.1; cites §12.2; cites §12.2, §18.5; cites §12.8; and 6 more
- `tests/docs/session_failure_outcome_doc_reconciliation_test.go::TestRetiredFailureOutcomePhrasesAbsent`: cites §7.2, §7.3, §28.5.1
- `tests/docs/session_failure_outcome_doc_reconciliation_test.go::TestFailureOutcomeEdgesStated`: cites §7.2, §7.3, §6.2, §28.5.1, §5.2

### `8.9`

- `pkg/gateway/mcpfabric/mcptools/mcptools_test.go` (41 test functions): cites §15.2.1, §10.2; cites §15.4, §15.2.1; cites §15.4, §8.5; cites §7.2; and 2 more
- `pkg/delegation/recovery/...` (17 test functions): cites §8.10; unannotated

### `9.1`

- `pkg/gateway/mcpfabric/mcp/...` (100 test functions): cites §10.2, §15.2.1; cites §11.5; cites §15; cites §15.2; and 21 more
- `pkg/gateway/mcpfabric/mcptools/discover_agents_filtering_test.go` (12 test functions): cites §10.6; unannotated

### `9.2`

- `pkg/elicitation/elicitation_test.go::TestAllModesIsExhaustive`: unannotated
- `pkg/elicitation/elicitation_test.go::TestModeOrderingStrict`: unannotated
- `pkg/elicitation/elicitation_test.go::TestResolveEffectivePicksStricter`: unannotated
- `pkg/elicitation/elicitation_test.go::TestDepthPolicyAllowAll`: unannotated
- `pkg/elicitation/elicitation_test.go::TestDepthPolicyBlockAll`: unannotated
- `pkg/elicitation/elicitation_test.go::TestDepthPolicySuppressAtDepth`: unannotated
- `pkg/elicitation/elicitation_test.go::TestContentDigestIsStable`: unannotated
- `pkg/elicitation/elicitation_test.go::TestContentDigestCanonicalKeyOrder`: unannotated
- `pkg/elicitation/elicitation_test.go::TestVerifyContentRejectsTampered`: unannotated
- `pkg/elicitation/elicitation_test.go::TestProvenanceValidateHappyPath`: unannotated
- `pkg/elicitation/elicitation_test.go::TestProvenanceValidateRejectsConnectorWithoutID`: unannotated

### `9.3`

Tiers the map names and the index does not: load_local.

- `tests/load_local/podmcp_once_per_pod_start_race_test.go` (1 test functions): cites §4.7, §15.4.3, §28.5.3

### `9.4`

- `pkg/gateway/mcpfabric/mcptools/memorytools_test.go` (5 test functions): unannotated
- `tests/component/stores/semanticcache_test.go` (1 test functions): cites §12.2.1, §4.9, §12.4

### `9.5`

No numbered heading carries this number.

Tiers the map names and the index does not: load_kind.

- `tests/load_kind/delegation_test.go::TestDelegationFanout`: cites §13.21
- `tests/load_kind/scenarios/delegation_fanout_mcp`: names no test function in the tree

### `10`

- `pkg/auth/auth_test.go::TestAllTokenTypesIsExhaustive`: unannotated
- `pkg/auth/auth_test.go::TestAllRolesIsExhaustive`: unannotated
- `pkg/auth/auth_test.go::TestRoleIsTenantScoped`: unannotated
- `pkg/auth/auth_test.go::TestValidateTenantIDAcceptsCanonicalValues`: unannotated
- `pkg/auth/auth_test.go::TestValidateTenantIDRejectsBadValues`: unannotated
- `pkg/auth/auth_test.go::TestExtractTenantSingleTenantReturnsDefault`: unannotated
- `pkg/auth/auth_test.go::TestExtractTenantMultiTenantClaimMissing`: unannotated
- `pkg/auth/auth_test.go::TestExtractTenantMultiTenantUnregistered`: unannotated
- `pkg/auth/auth_test.go::TestExtractTenantMultiTenantRegistered`: unannotated
- `pkg/auth/auth_test.go::TestExtractTenantMultiTenantRegistryError`: unannotated
- `pkg/mtls/spiffe/spiffe_test.go::TestParseAgent`: unannotated
- `pkg/mtls/spiffe/spiffe_test.go::TestParseInterceptor`: unannotated
- `pkg/mtls/spiffe/spiffe_test.go::TestParseRejectsMalformed`: unannotated
- `pkg/mtls/spiffe/spiffe_test.go::TestValidateAgentRejectsMismatches`: unannotated
- `pkg/mtls/spiffe/spiffe_test.go::TestValidateInterceptorMatchesAllowlistedNamespaces`: unannotated
- `pkg/mtls/spiffe/spiffe_test.go::TestValidateInterceptorRejectsForeignNamespace`: unannotated
- `pkg/mtls/denylist/denylist_test.go::TestAddAndContains`: unannotated
- `pkg/mtls/denylist/denylist_test.go::TestContainsExpiresEntries`: unannotated
- `pkg/mtls/denylist/denylist_test.go::TestSweepEvictsAllExpired`: unannotated
- `pkg/mtls/denylist/denylist_test.go::TestConcurrentAddContainsSafe`: unannotated
- `pkg/runtime/upgrade/state/state_test.go::TestIsValidCanonicalTransitions`: unannotated
- `pkg/runtime/upgrade/state/state_test.go::TestIsValidIllegalTransitionsRejected`: unannotated
- `pkg/runtime/upgrade/state/state_test.go::TestRecordHappyPath`: unannotated
- `pkg/runtime/upgrade/state/state_test.go::TestRecordPauseAndResumePreservesPrior`: unannotated
- `pkg/runtime/upgrade/state/state_test.go::TestRecordPauseRejectsTerminal`: unannotated
- `pkg/runtime/upgrade/state/state_test.go::TestRecordDoublePauseIsError`: unannotated
- `pkg/runtime/upgrade/state/state_test.go::TestNewRecordAtRehydratesPausedRecord`: unannotated
- `pkg/environment/environment_test.go::TestAllRolesIsExhaustive`: unannotated
- `pkg/environment/environment_test.go::TestRoleAtLeastOrder`: unannotated
- `pkg/environment/environment_test.go::TestAllLabelOperatorsIsExhaustive`: unannotated
- `pkg/environment/environment_test.go::TestRequirementValidateRejectsBadShape`: unannotated
- `pkg/environment/environment_test.go::TestSelectorMatchLabelsExactMatch`: unannotated
- `pkg/environment/environment_test.go::TestSelectorMatchExpressionsIn`: unannotated
- `pkg/environment/environment_test.go::TestSelectorMatchExpressionsNotIn`: unannotated
- `pkg/environment/environment_test.go::TestSelectorMatchExpressionsExists`: unannotated
- `pkg/environment/environment_test.go::TestSelectorMatchExpressionsDoesNotExist`: unannotated
- `pkg/environment/environment_test.go::TestSelectorTypesFilter`: unannotated
- `pkg/environment/environment_test.go::TestSelectorExcludeOverridesMatchLabels`: unannotated
- `pkg/environment/environment_test.go::TestSelectorIncludeOverridesSelector`: unannotated
- `pkg/environment/environment_test.go::TestSelectorExcludeBeatsInclude`: unannotated
- `pkg/environment/environment_test.go::TestFilterReturnsAdmittedSubset`: unannotated
- `pkg/experiment/experiment_test.go::TestAllStatusesIsExhaustive`: unannotated
- `pkg/experiment/experiment_test.go::TestStatusIsRoutable`: unannotated
- `pkg/experiment/experiment_test.go::TestAllTargetingModesIsExhaustive`: unannotated
- `pkg/experiment/experiment_test.go::TestAllStickyValuesIsExhaustive`: unannotated
- `pkg/experiment/experiment_test.go::TestAllPropagationsIsExhaustive`: unannotated
- `pkg/experiment/experiment_test.go::TestDefinitionValidateHappyPath`: unannotated
- `pkg/experiment/experiment_test.go::TestDefinitionRejectsReservedControlID`: unannotated
- `pkg/experiment/experiment_test.go::TestDefinitionRejectsBadWeights`: unannotated
- `pkg/experiment/experiment_test.go::TestDefinitionRejectsSumWeightsAtOrAboveOne`: unannotated
- `pkg/experiment/experiment_test.go::TestAssignVariantEmptyKeyReturnsControl`: unannotated
- `pkg/experiment/experiment_test.go::TestAssignVariantIsDeterministic`: unannotated
- `pkg/experiment/experiment_test.go::TestAssignVariantApproximatesWeightDistribution`: unannotated
- `pkg/experiment/experiment_test.go::TestAssignVariantHonoursDefinitionOrder`: unannotated
- `pkg/experiment/experiment_test.go::TestAssignVariantExperimentIDPartOfHashKey`: unannotated

### `10.1`

- `tests/e2e_kind/gateway_replica_continuity_test.go::TestSessionContinuesAfterCoordinatingGatewayReplicaLoss`: cites §4.1
- `tests/integration/checkpoint_grant_remint_test.go` (1 test functions): cites §4.4
- `tests/component/migrations/prod_columns_test.go` (10 test functions): cites §11.1; cites §12.2; cites §12.2, §18.5; cites §12.8; and 6 more

### `10.1.1`

- `pkg/gateway/sessionserver/holder_forward_test.go::TestForwardToHolderNotDeliveredWithoutReachableHolder_spec_29_3`: cites §29.3, §11.5
- `tests/component/coordination/...` (6 test functions): cites §10.1; cites §10.1, §10.1.2, §4.2; cites §25.4
- `tests/component/stores/coordleasestore_test.go` (2 test functions): cites §10.1.8; cites §12.1
- `tests/e2e_kind/gateway_replica_continuity_test.go::TestSessionContinuesAfterCoordinatingGatewayReplicaLoss`: cites §4.1
- `tests/chaos/coordination_crash_takeover_test.go::TestCoordinatorFailoverCrashTakeover_spec_10_1`: cites §10.1, §4.6.1
- `tests/docs/build_gaps_stream_failure_records_currency_test.go::TestFiledStreamFindingsMatchTheTree_spec_10_1_1`: unannotated
- `charts/lenny/tests/holder-forward_test.yaml`: the file exists and declares no test function
- `pkg/gateway/sessionserver/holder_forward_decode_internal_test.go::TestDecodeToolForwardResponseRejectsUndecodableResponses_spec_29_3`: cites §29.3
- `pkg/gateway/sessionserver/holder_forward_decode_internal_test.go::TestStreamBodyReportsWriteAndReadFailures_spec_29_3`: cites §29.3
- `pkg/gateway/sessionserver/holder_forward_decode_internal_test.go::TestForwardRequestBuildFailuresAreReported_spec_29_3`: cites §29.3

### `10.1.2`

- `tests/integration/coordination_fence_split_brain_test.go::TestCoordinationSplitBrainFenceAcrossTwoReplicas_spec_10_1`: cites §10.1, §4.2, §4.6.1
- `tests/load_local/coordination_colocation_race_test.go::TestColocationInvariantUnderConcurrentHandoff_spec_10_1`: cites §10.1, §4.6.1, §4.7
- `tests/chaos/coordination_crash_takeover_test.go::TestCoordinatorFailoverCrashTakeover_spec_10_1`: cites §10.1, §4.6.1
- `tests/chaos/lifecycle_fault_injection_test.go::TestIdleSuspensionFaultInjection_spec_6_2`: cites §6.2, §7.2

### `10.1.3`

Tiers the map names and the index does not: chaos, component.

- `tests/component/coordination/...` (6 test functions): cites §10.1; cites §10.1, §10.1.2, §4.2; cites §25.4
- `tests/chaos/coordination_crash_takeover_test.go::TestCoordinatorFailoverCrashTakeover_spec_10_1`: cites §10.1, §4.6.1

### `10.1.4`

Tiers the map names and the index does not: e2e_kind.

- `tests/e2e_kind/gateway_replica_continuity_test.go::TestSessionContinuesAfterCoordinatingGatewayReplicaLoss`: cites §4.1
- `tests/chaos/coordination_crash_takeover_test.go::TestCoordinatorFailoverCrashTakeover_spec_10_1`: cites §10.1, §4.6.1
- `tests/docs/concurrent_slot_lifecycle_doc_reconciliation_test.go::TestLeakedSlotCountingLifetimeAgrees_F5231`: cites §5.2, §6.2, §10.1
- `tests/security/adapter_hold_termination_surface_test.go::TestSharedPlatformMCPRefusesAfterCoordinatorLostTermination_spec_10_1`: cites §10.1, §9.1, §15.4.3, §13.1

### `10.1.5`

Tiers the map names and the index does not: chaos.

- `tests/integration/coordination_fence_split_brain_test.go::TestCoordinationSplitBrainFenceAcrossTwoReplicas_spec_10_1`: cites §10.1, §4.2, §4.6.1
- `tests/load_local/coordination_colocation_race_test.go::TestColocationInvariantUnderConcurrentHandoff_spec_10_1`: cites §10.1, §4.6.1, §4.7
- `tests/chaos/coordination_crash_takeover_test.go::TestCoordinatorFailoverCrashTakeover_spec_10_1`: cites §10.1, §4.6.1
- `tests/load_local/suspension_interleaving_test.go::TestIdleSuspensionRequiredPairs_spec_6_2`: cites §6.2, §7.2, §29.3
- `tests/component/slotrelease/revoke_double_teardown_test.go` (3 test functions): cites §10.1, §5.2, §6.2; cites §5.2, §6.2

### `10.1.6`

Tiers the map names and the index does not: e2e_kind, load_kind.

- `tests/load_kind/scenarios/gateway_capacity_calibration`: names no test function in the tree
- `tests/e2e_kind/ha_streaming_replica_test.go::TestHAStreamingReconnectAcrossGatewayReplicas`: cites §10.1

### `10.1.7`

- `tests/load_local/prestop_no_double_checkpoint_test.go` (1 test functions): cites §10.1, §4.4
- `tests/docs/checkpoint_pipeline_consistency_test.go::TestCheckpointManifestColumnSetMatchesMigration0178`: cites §10.1
- `tests/docs/checkpoint_pipeline_consistency_test.go::TestManifestReasonAndPartialCounterDomainsAgree`: cites §10.1, §16.1
- `tests/component/stores/partialmanifeststore_test.go` (1 test functions): cites §10.1, §11.2

### `10.1.8`

Tiers the map names and the index does not: e2e_kind.

- `tests/contract/adapter_checkpointbarrier/...` (1 test functions): cites §10.1, §4.7
- `tests/e2e_kind/ha_streaming_replica_test.go::TestHAStreamingReconnectAcrossGatewayReplicas`: cites §10.1
- `pkg/gateway/sessionserver/resume_chunk_selection_internal_test.go` (7 test functions): cites §10.1; cites §10.1.7; cites §16.1

### `10.2`

- `tests/integration/checkpoint_chunk_helpers_test.go`: the file exists and declares no test function
- `pkg/auth/auth_test.go::TestAllTokenTypesIsExhaustive`: unannotated
- `pkg/auth/auth_test.go::TestAllRolesIsExhaustive`: unannotated
- `pkg/auth/auth_test.go::TestRoleIsTenantScoped`: unannotated
- `pkg/auth/auth_test.go::TestValidateTenantIDAcceptsCanonicalValues`: unannotated
- `pkg/auth/auth_test.go::TestValidateTenantIDRejectsBadValues`: unannotated
- `pkg/auth/auth_test.go::TestExtractTenantSingleTenantReturnsDefault`: unannotated
- `pkg/auth/auth_test.go::TestExtractTenantMultiTenantClaimMissing`: unannotated
- `pkg/auth/auth_test.go::TestExtractTenantMultiTenantUnregistered`: unannotated
- `pkg/auth/auth_test.go::TestExtractTenantMultiTenantRegistered`: unannotated
- `pkg/auth/auth_test.go::TestExtractTenantMultiTenantRegistryError`: unannotated
- `tests/component/migrations/prod_columns_test.go` (10 test functions): cites §11.1; cites §12.2; cites §12.2, §18.5; cites §12.8; and 6 more
- `cmd/lenny-gateway/authposture_test.go::TestDevPostureHonoursTenantHeader`: cites §17.4, §18
- `pkg/gateway/middleware/auth/public_path_test.go::TestDevHeadersHonouredUnderAllowDevHeaders`: cites §17.4, §18
- `pkg/gateway/sessionserver/request_identity_internal_test.go::TestHandlerServesTenantlessRoutesWithoutPrincipal`: cites §5.1
- `pkg/gateway/sessionserver/request_identity_internal_test.go::TestHandlerDevModeKeepsHeaderTenant`: cites §17.4, §18
- `tests/integration/gateway_require_auth_test.go::TestDevGatewayHonoursTenantHeader`: cites §17.4, §18
- `tests/integration/tenant_deletion_phase4_test.go::TestTenantDeletionPhase4ClearsArchivedTreesAndCheckpoints_spec_12_8`: cites §12.8
- `charts/lenny/tests/token-service-deployment_test.yaml`: the file exists and declares no test function
- `charts/lenny/tests/gateway-deployment_test.yaml`: the file exists and declares no test function
- `charts/lenny/tests/kms-guard_test.yaml`: the file exists and declares no test function
- `charts/lenny/tests/kms-master-key-secret_test.yaml`: the file exists and declares no test function
- `charts/lenny/tests/ops-deployment_test.yaml`: the file exists and declares no test function
- `tests/docs/build_gaps_pod_no_jwt_records_currency_test.go::TestPodNoJWTProposalLabelProbe_spec_10_2`: unannotated

### `10.3`

- `pkg/mtls/spiffe/spiffe_test.go::TestParseAgent`: unannotated
- `pkg/mtls/spiffe/spiffe_test.go::TestParseInterceptor`: unannotated
- `pkg/mtls/spiffe/spiffe_test.go::TestParseRejectsMalformed`: unannotated
- `pkg/mtls/spiffe/spiffe_test.go::TestValidateAgentRejectsMismatches`: unannotated
- `pkg/mtls/spiffe/spiffe_test.go::TestValidateInterceptorMatchesAllowlistedNamespaces`: unannotated
- `pkg/mtls/spiffe/spiffe_test.go::TestValidateInterceptorRejectsForeignNamespace`: unannotated
- `pkg/mtls/denylist/denylist_test.go::TestAddAndContains`: unannotated
- `pkg/mtls/denylist/denylist_test.go::TestContainsExpiresEntries`: unannotated
- `pkg/mtls/denylist/denylist_test.go::TestSweepEvictsAllExpired`: unannotated
- `pkg/mtls/denylist/denylist_test.go::TestConcurrentAddContainsSafe`: unannotated
- `charts/lenny/tests/holder-forward_test.yaml`: the file exists and declares no test function

### `10.4`

Tiers the map names and the index does not: component, integration.

- `pkg/events/...` (23 test functions): cites §25.3; cites §25.3, §12.6; cites §25.5; unannotated
- `pkg/gateway/eventbuffer/...` (33 test functions): unannotated
- `tests/component/coordination/sweep_test.go` (1 test functions): cites §10.1
- `tests/integration/streaming_reconnect_test.go::TestStreamingReconnectResumesExactlyFromCursor`: cites §7.2

### `10.5`

- `pkg/runtime/upgrade/state/state_test.go::TestIsValidCanonicalTransitions`: unannotated
- `pkg/runtime/upgrade/state/state_test.go::TestIsValidIllegalTransitionsRejected`: unannotated
- `pkg/runtime/upgrade/state/state_test.go::TestRecordHappyPath`: unannotated
- `pkg/runtime/upgrade/state/state_test.go::TestRecordPauseAndResumePreservesPrior`: unannotated
- `pkg/runtime/upgrade/state/state_test.go::TestRecordPauseRejectsTerminal`: unannotated
- `pkg/runtime/upgrade/state/state_test.go::TestRecordDoublePauseIsError`: unannotated
- `pkg/runtime/upgrade/state/state_test.go::TestNewRecordAtRehydratesPausedRecord`: unannotated

### `10.6`

Tiers the map names and the index does not: chaos, e2e_kind, security.

- `pkg/environment/environment_test.go::TestAllRolesIsExhaustive`: unannotated
- `pkg/environment/environment_test.go::TestRoleAtLeastOrder`: unannotated
- `pkg/environment/environment_test.go::TestAllLabelOperatorsIsExhaustive`: unannotated
- `pkg/environment/environment_test.go::TestRequirementValidateRejectsBadShape`: unannotated
- `pkg/environment/environment_test.go::TestSelectorMatchLabelsExactMatch`: unannotated
- `pkg/environment/environment_test.go::TestSelectorMatchExpressionsIn`: unannotated
- `pkg/environment/environment_test.go::TestSelectorMatchExpressionsNotIn`: unannotated
- `pkg/environment/environment_test.go::TestSelectorMatchExpressionsExists`: unannotated
- `pkg/environment/environment_test.go::TestSelectorMatchExpressionsDoesNotExist`: unannotated
- `pkg/environment/environment_test.go::TestSelectorTypesFilter`: unannotated
- `pkg/environment/environment_test.go::TestSelectorExcludeOverridesMatchLabels`: unannotated
- `pkg/environment/environment_test.go::TestSelectorIncludeOverridesSelector`: unannotated
- `pkg/environment/environment_test.go::TestSelectorExcludeBeatsInclude`: unannotated
- `pkg/environment/environment_test.go::TestFilterReturnsAdmittedSubset`: unannotated
- `tests/testinfra/sessiondriver/sessiondriver_test.go` (2 test functions): cites §15.1
- `tests/e2e_kind/gateway_probe_test.go`: the file exists and declares no test function
- `tests/chaos/live_session_test.go` (1 test functions): cites §12.8
- `tests/security/live_session_test.go` (2 test functions): cites §12.9.1; cites §12.9.7
- `tests/e2e_kind/execution_modes_test.go` (4 test functions): cites §5.2; cites §5.2, §4.7.10; cites §6.4
- `tests/e2e_kind/scaffolds_test.go` (3 test functions): cites §13.19; cites §13.27; cites §13.32, §15.1
- `pkg/gateway/sessionserver/create_test.go` (47 test functions): cites §10.1; cites §14.1; cites §15.1, §4.6, §6.2, §7.1; cites §5.2, §7.1; and 6 more

### `10.7`

- `pkg/experiment/experiment_test.go::TestAllStatusesIsExhaustive`: unannotated
- `pkg/experiment/experiment_test.go::TestStatusIsRoutable`: unannotated
- `pkg/experiment/experiment_test.go::TestAllTargetingModesIsExhaustive`: unannotated
- `pkg/experiment/experiment_test.go::TestAllStickyValuesIsExhaustive`: unannotated
- `pkg/experiment/experiment_test.go::TestAllPropagationsIsExhaustive`: unannotated
- `pkg/experiment/experiment_test.go::TestDefinitionValidateHappyPath`: unannotated
- `pkg/experiment/experiment_test.go::TestDefinitionRejectsReservedControlID`: unannotated
- `pkg/experiment/experiment_test.go::TestDefinitionRejectsBadWeights`: unannotated
- `pkg/experiment/experiment_test.go::TestDefinitionRejectsSumWeightsAtOrAboveOne`: unannotated
- `pkg/experiment/experiment_test.go::TestAssignVariantEmptyKeyReturnsControl`: unannotated
- `pkg/experiment/experiment_test.go::TestAssignVariantIsDeterministic`: unannotated
- `pkg/experiment/experiment_test.go::TestAssignVariantApproximatesWeightDistribution`: unannotated
- `pkg/experiment/experiment_test.go::TestAssignVariantHonoursDefinitionOrder`: unannotated
- `pkg/experiment/experiment_test.go::TestAssignVariantExperimentIDPartOfHashKey`: unannotated
- `pkg/gateway/sessionserver/eval_test.go::TestEvalRateLimitFixedWindowBoundary_spec_10_7`: unannotated

### `11`

- `pkg/quota/quota_test.go::TestAllResetPeriodsIsExhaustive`: unannotated
- `pkg/quota/quota_test.go::TestCheckOK`: unannotated
- `pkg/quota/quota_test.go::TestCheckSoftWarning`: unannotated
- `pkg/quota/quota_test.go::TestCheckHardExceeded`: unannotated
- `pkg/quota/quota_test.go::TestHierarchyValidateAcceptsNested`: unannotated
- `pkg/quota/quota_test.go::TestHierarchyValidateRejectsInverted`: unannotated
- `pkg/quota/quota_test.go::TestHierarchicalCheckPicksMostRestrictive`: unannotated
- `pkg/quota/quota_test.go::TestFailOpenCeilingFormula`: unannotated
- `pkg/quota/quota_test.go::TestPerUserFailOpenCeiling`: unannotated
- `pkg/quota/quota_test.go::TestMaxOvershootFormula`: unannotated
- `pkg/quota/quota_test.go::TestReconcileMaxImplementsMaxRule`: unannotated
- `pkg/idempotency/idempotency_test.go::TestMaxKeyLengthMatchesSpec`: unannotated
- `pkg/idempotency/idempotency_test.go::TestTTLMatchesSpec`: unannotated
- `pkg/idempotency/idempotency_test.go::TestKeyValidateAcceptsCanonical`: unannotated
- `pkg/idempotency/idempotency_test.go::TestKeyValidateRejectsOverLengthValue`: unannotated
- `pkg/idempotency/idempotency_test.go::TestHashBodyIsStable`: unannotated
- `pkg/idempotency/idempotency_test.go::TestDetectReuseStoreNewWhenNoPrior`: unannotated
- `pkg/idempotency/idempotency_test.go::TestDetectReuseReplayWhenMatch`: unannotated
- `pkg/idempotency/idempotency_test.go::TestDetectReuseRejectsKeyReusedWithDifferentBody`: unannotated
- `pkg/idempotency/idempotency_test.go::TestDetectReuseTreatsExpiredAsNoPrior`: unannotated
- `pkg/idempotency/idempotency_test.go::TestRecordIsExpired`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestAllStatesIsExhaustive`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestAllTiersIsExhaustive`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestAllOperationTypesIsExhaustive`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestValidateAcceptsWellFormedBreakers`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestValidateRejectsMismatchedScope`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestMatchClosedBreakerNeverRejects`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestMatchRuntimeTier`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestMatchPoolTier`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestMatchConnectorTier`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestMatchOperationTypeTier`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestFirstMatchReturnsFirstOpenMatch`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestScopeMatches`: unannotated
- `pkg/audit/audit_test.go::TestAllChainIntegritiesIsExhaustive`: unannotated
- `pkg/audit/audit_test.go::TestChainIntegrityIsAlarming`: unannotated
- `pkg/audit/audit_test.go::TestAllComplianceProfilesIsExhaustive`: unannotated
- `pkg/audit/audit_test.go::TestComplianceProfileRequiresSIEM`: unannotated
- `pkg/audit/audit_test.go::TestAllRetentionPresetsIsExhaustive`: unannotated
- `pkg/audit/audit_test.go::TestPresetDaysMatchesSpec`: unannotated
- `pkg/audit/audit_test.go::TestPresetCompatibilityMatrix`: unannotated
- `pkg/audit/audit_test.go::TestValidatePairingHappyPath`: unannotated
- `pkg/audit/audit_test.go::TestValidatePairingMismatches`: unannotated
- `pkg/audit/audit_test.go::TestAllOCSFStatesIsExhaustive`: unannotated
- `pkg/audit/audit_test.go::TestOCSFStateIsTerminal`: unannotated

### `11.1`

Tiers the map names and the index does not: chaos, e2e_kind, security.

- `tests/e2e_kind/gateway_probe_test.go`: the file exists and declares no test function
- `tests/testinfra/sessiondriver/sessiondriver_test.go` (2 test functions): cites §15.1
- `tests/chaos/live_session_test.go` (1 test functions): cites §12.8
- `tests/security/live_session_test.go` (2 test functions): cites §12.9.1; cites §12.9.7
- `pkg/gateway/sessionserver/create_test.go` (47 test functions): cites §10.1; cites §14.1; cites §15.1, §4.6, §6.2, §7.1; cites §5.2, §7.1; and 6 more

### `11.2`

- `pkg/quota/quota_test.go::TestAllResetPeriodsIsExhaustive`: unannotated
- `pkg/quota/quota_test.go::TestCheckOK`: unannotated
- `pkg/quota/quota_test.go::TestCheckSoftWarning`: unannotated
- `pkg/quota/quota_test.go::TestCheckHardExceeded`: unannotated
- `pkg/quota/quota_test.go::TestHierarchyValidateAcceptsNested`: unannotated
- `pkg/quota/quota_test.go::TestHierarchyValidateRejectsInverted`: unannotated
- `pkg/quota/quota_test.go::TestHierarchicalCheckPicksMostRestrictive`: unannotated
- `pkg/quota/quota_test.go::TestFailOpenCeilingFormula`: unannotated
- `pkg/quota/quota_test.go::TestPerUserFailOpenCeiling`: unannotated
- `pkg/quota/quota_test.go::TestMaxOvershootFormula`: unannotated
- `pkg/quota/quota_test.go::TestReconcileMaxImplementsMaxRule`: unannotated
- `tests/e2e_cloud/scaffolds_test.go::TestCloudBillingExport`: no such test function in the tree
- `tests/component/migrations/prod_columns_test.go` (10 test functions): cites §11.1; cites §12.2; cites §12.2, §18.5; cites §12.8; and 6 more

### `11.3`

- `pkg/gateway/session/inputwait/...` (18 test functions): cites §16.1, §8.8; cites §7.2; cites §7.2, §28.3; unannotated
- `pkg/session/state/...` (10 test functions): cites §7.2; cites §7.2, §6.2; unannotated

### `11.4`

- `tests/component/slotrelease/revoke_double_teardown_test.go` (3 test functions): cites §10.1, §5.2, §6.2; cites §5.2, §6.2
- `tests/docs/tenant_deletion_credentials_doc_reconciliation_test.go` (7 test functions): cites §10.2; cites §10.2, §12.8; cites §12.8; cites §12.8, §10.2; and 2 more

### `11.5`

Tiers the map names and the index does not: load_kind.

- `pkg/idempotency/idempotency_test.go::TestMaxKeyLengthMatchesSpec`: unannotated
- `pkg/idempotency/idempotency_test.go::TestTTLMatchesSpec`: unannotated
- `pkg/idempotency/idempotency_test.go::TestKeyValidateAcceptsCanonical`: unannotated
- `pkg/idempotency/idempotency_test.go::TestKeyValidateRejectsOverLengthValue`: unannotated
- `pkg/idempotency/idempotency_test.go::TestHashBodyIsStable`: unannotated
- `pkg/idempotency/idempotency_test.go::TestDetectReuseStoreNewWhenNoPrior`: unannotated
- `pkg/idempotency/idempotency_test.go::TestDetectReuseReplayWhenMatch`: unannotated
- `pkg/idempotency/idempotency_test.go::TestDetectReuseRejectsKeyReusedWithDifferentBody`: unannotated
- `pkg/idempotency/idempotency_test.go::TestDetectReuseTreatsExpiredAsNoPrior`: unannotated
- `pkg/idempotency/idempotency_test.go::TestRecordIsExpired`: unannotated
- `tests/load_kind/credential_lifecycle_test.go::TestCredentialLifecycle`: cites §13.24
- `tests/load_kind/scenarios/credential_lifecycle`: names no test function in the tree

### `11.6`

Tiers the map names and the index does not: integration.

- `pkg/circuitbreaker/circuitbreaker_test.go::TestAllStatesIsExhaustive`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestAllTiersIsExhaustive`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestAllOperationTypesIsExhaustive`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestValidateAcceptsWellFormedBreakers`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestValidateRejectsMismatchedScope`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestMatchClosedBreakerNeverRejects`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestMatchRuntimeTier`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestMatchPoolTier`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestMatchConnectorTier`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestMatchOperationTypeTier`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestFirstMatchReturnsFirstOpenMatch`: unannotated
- `pkg/circuitbreaker/circuitbreaker_test.go::TestScopeMatches`: unannotated
- `tests/integration/admission_gate_order_test.go` (1 test functions): cites §4.8

### `11.7`

- `pkg/audit/audit_test.go::TestAllChainIntegritiesIsExhaustive`: unannotated
- `pkg/audit/audit_test.go::TestChainIntegrityIsAlarming`: unannotated
- `pkg/audit/audit_test.go::TestAllComplianceProfilesIsExhaustive`: unannotated
- `pkg/audit/audit_test.go::TestComplianceProfileRequiresSIEM`: unannotated
- `pkg/audit/audit_test.go::TestAllRetentionPresetsIsExhaustive`: unannotated
- `pkg/audit/audit_test.go::TestPresetDaysMatchesSpec`: unannotated
- `pkg/audit/audit_test.go::TestPresetCompatibilityMatrix`: unannotated
- `pkg/audit/audit_test.go::TestValidatePairingHappyPath`: unannotated
- `pkg/audit/audit_test.go::TestValidatePairingMismatches`: unannotated
- `pkg/audit/audit_test.go::TestAllOCSFStatesIsExhaustive`: unannotated
- `pkg/audit/audit_test.go::TestOCSFStateIsTerminal`: unannotated
- `tests/component/stores/eventstore_test.go` (3 test functions): cites §12.2.1; cites §4.4
- `tests/component/migrations/prod_columns_test.go` (10 test functions): cites §11.1; cites §12.2; cites §12.2, §18.5; cites §12.8; and 6 more
- `tests/component/issuedtokencascade/source_recheck_test.go::TestIssuanceReReadsSourceRevocationUnderLock_spec_13_3`: cites §13.3

### `12.2`

- `tests/component/stores/erasure_interface_test.go` (1 test functions): unannotated

### `12.3`

- `tests/chaos/lifecycle_fault_injection_test.go::TestLifecycleFaultInjection_spec_10_1_2`: cites §10.1.2, §7.3, §7.2
- `tests/component/migrations/prod_columns_test.go` (10 test functions): cites §11.1; cites §12.2; cites §12.2, §18.5; cites §12.8; and 6 more

### `12.4`

- `pkg/redisconn/redisconn_test.go` (7 test functions): unannotated
- `tests/docs/remediation_plan_redis_condition_audit_test.go` (3 test functions): unannotated
- `tests/chaos/redis_ha_failover_kind_test.go::TestRedisHAFailoverKind`: cites §17.3
- `pkg/gateway/quota/quotacheckpoint/podusage_reconcile_test.go` (7 test functions): cites §11.2; cites §11.2, §4.7; cites §24.6

### `12.5`

- `tests/e2e_cloud/scaffolds_test.go::TestMultiAZMinIO`: no such test function in the tree
- `tests/component/stores/partialmanifeststore_test.go` (1 test functions): cites §10.1, §11.2
- `tests/component/migrations/prod_columns_test.go` (10 test functions): cites §11.1; cites §12.2; cites §12.2, §18.5; cites §12.8; and 6 more

### `12.6`

- `pkg/gateway/storage/eventbus/eventbus_test.go` (6 test functions): cites §12.3.7
- `pkg/events/auditbearing_test.go::TestOperationalEventEmptyExtensionsRoundTrip`: cites §25.3
- `tests/component/stores/eventstore_test.go` (3 test functions): cites §12.2.1; cites §4.4
- `tests/contract/cloudevents/published_schema_test.go` (1 test functions): cites §12.3.7
- `tests/component/migrations/prod_columns_test.go` (10 test functions): cites §11.1; cites §12.2; cites §12.2, §18.5; cites §12.8; and 6 more

### `12.8`

- `tests/chaos/chaos_helpers_test.go`: the file exists and declares no test function
- `tests/load_local/open_responses_conversation_invariant_test.go::TestOpenResponsesInvariantCheckerComparesRESTDeliveries_spec_15`: cites §15, §15.1

### `12.9`

- `tests/security/pentest/...` (6 test functions): cites §18.33
- `tests/security/store_helpers_test.go`: the file exists and declares no test function
- `tests/security/ops_rbac_test.go::TestOpsTenantAdminDriftCrossTenantForbidden_spec_12_9_1`: unannotated

### `13`

- `pkg/podsecurity/podsecurity_test.go::TestValidateAcceptsWellFormedAgentPod`: unannotated
- `pkg/podsecurity/podsecurity_test.go::TestValidateRejectsHostSharing`: unannotated
- `pkg/podsecurity/podsecurity_test.go::TestValidateRejectsMissingFSGroup`: unannotated
- `pkg/podsecurity/podsecurity_test.go::TestValidateRejectsWrongFSGroup`: unannotated
- `pkg/podsecurity/podsecurity_test.go::TestValidateRejectsRoot`: unannotated
- `pkg/podsecurity/podsecurity_test.go::TestValidateRejectsAllowPrivilegeEscalation`: unannotated
- `pkg/podsecurity/podsecurity_test.go::TestValidateRejectsPrivilegedContainer`: unannotated
- `pkg/podsecurity/podsecurity_test.go::TestValidateRejectsMutableRootFS`: unannotated
- `pkg/podsecurity/podsecurity_test.go::TestValidateRequiresDropAll`: unannotated
- `pkg/podsecurity/podsecurity_test.go::TestValidateRejectsAddedCapabilities`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRotationHappyPath`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRejectsCrossTenantCaller`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRejectsCrossTenantIssued`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRejectsScopeBroadening`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRejectsAudienceBroadening`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRejectsCallerTypeElevation`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRejectsExpiredSubject`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateChildMintingProducesA2ADelegation`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRejectsExpiredActor`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRejectsTypMutationOnRotation`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateExpRespectsPerDialectCap`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateExpTruncatesToWholeSeconds`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRejectsExpInPast`: unannotated
- `pkg/upload/upload_test.go::TestNormativeCeilingsMatchSpec`: unannotated
- `pkg/upload/upload_test.go::TestForbiddenEntryKinds`: unannotated
- `pkg/upload/upload_test.go::TestValidateEntryAcceptsRegularInWorkspace`: unannotated
- `pkg/upload/upload_test.go::TestValidateEntryRejectsForbiddenKinds`: unannotated
- `pkg/upload/upload_test.go::TestValidateEntryRejectsAbsolutePath`: cites §7.4, §15.1
- `pkg/upload/upload_test.go::TestValidateEntryRejectsTraversal`: cites §7.4, §15.1
- `pkg/upload/upload_test.go::TestValidateEntryRejectsOverLengthPath`: unannotated
- `pkg/upload/upload_test.go::TestValidateEntryRejectsExcessiveDepth`: unannotated
- `pkg/upload/upload_test.go::TestValidateEntryRejectsOversizedEntry`: unannotated
- `pkg/upload/upload_test.go::TestValidateEntryRejectsSymlinkByDefault`: unannotated
- `pkg/upload/upload_test.go::TestValidateEntrySymlinkOptInWorkspaceLocalTarget`: unannotated
- `pkg/upload/upload_test.go::TestValidateEntrySymlinkRejectsEscape`: unannotated
- `pkg/upload/upload_test.go::TestValidateArchiveRejectsOversizedAggregate`: unannotated
- `pkg/upload/upload_test.go::TestValidateArchiveRejectsTooManyEntries`: unannotated
- `pkg/upload/upload_test.go::TestValidateArchiveRejectsExtremeRatio`: unannotated
- `tests/e2e_kind/gateway_probe_test.go`: the file exists and declares no test function
- `tests/e2e_kind/pod_lifecycle_test.go` (1 test functions): cites §4.7, §5.3, §6.2, §4.6.1
- `tests/security/gateway_probe_test.go`: the file exists and declares no test function
- `tests/e2e_cloud/scaffolds_test.go::TestCloudOIDC`: no such test function in the tree

### `13.1`

- `pkg/podsecurity/podsecurity_test.go::TestValidateAcceptsWellFormedAgentPod`: unannotated
- `pkg/podsecurity/podsecurity_test.go::TestValidateRejectsHostSharing`: unannotated
- `pkg/podsecurity/podsecurity_test.go::TestValidateRejectsMissingFSGroup`: unannotated
- `pkg/podsecurity/podsecurity_test.go::TestValidateRejectsWrongFSGroup`: unannotated
- `pkg/podsecurity/podsecurity_test.go::TestValidateRejectsRoot`: unannotated
- `pkg/podsecurity/podsecurity_test.go::TestValidateRejectsAllowPrivilegeEscalation`: unannotated
- `pkg/podsecurity/podsecurity_test.go::TestValidateRejectsPrivilegedContainer`: unannotated
- `pkg/podsecurity/podsecurity_test.go::TestValidateRejectsMutableRootFS`: unannotated
- `pkg/podsecurity/podsecurity_test.go::TestValidateRequiresDropAll`: unannotated
- `pkg/podsecurity/podsecurity_test.go::TestValidateRejectsAddedCapabilities`: unannotated
- `tests/security/admission_label_immutability_test.go` (1 test functions): cites §12.9.3
- `tests/docs/credential_path_literal_sweep_test.go` (3 test functions): cites §6.1, §4.7

### `13.2`

- `tests/component/migrations/migrations_test.go` (5 test functions): cites §12.2; cites §12.2, §12.3; cites §12.3
- `tests/integration/checkpoint_grant_remint_test.go` (1 test functions): cites §4.4
- `tests/component/migrations/prod_columns_test.go` (10 test functions): cites §11.1; cites §12.2; cites §12.2, §18.5; cites §12.8; and 6 more
- `charts/lenny/tests/ops-network-policies_test.yaml`: the file exists and declares no test function

### `13.2.1`

Tiers the map names and the index does not: security.

- `tests/security/network_policy_test.go` (5 test functions): cites §13.2; cites §13.2, §17.2; cites §13.2.4, §28.5.4, §10.3; cites §29.3, §13.2.4, §28.5.4

### `13.2.2`

- `tests/security/network_policy_test.go` (5 test functions): cites §13.2; cites §13.2, §17.2; cites §13.2.4, §28.5.4, §10.3; cites §29.3, §13.2.4, §28.5.4
- `tests/security/agent_egress_test.go` (1 test functions): cites §13.2

### `13.2.3`

Tiers the map names and the index does not: e2e_cloud.

- `tests/security/ops_agent_pod_boundary_test.go::TestAgentPodReachesObjectStoreButNoOtherLennySystemPort_spec_13_2`: cites §13.2
- `tests/security/ops_agent_pod_boundary_test.go::TestAgentPodTrustsObjectStoreCA_spec_13_2`: cites §13.2
- `tests/e2e_cloud/agent_objectstore_egress_test.go::TestAgentPodObjectStoreEgressIsPortScoped_spec_13_2`: cites §13.2

### `13.2.4`

- `tests/e2e_kind/ingress_test.go` (1 test functions): cites §13.2
- `charts/lenny/tests/system-network-policies_test.yaml`: the file exists and declares no test function
- `charts/lenny/tests/kms-egress_test.yaml`: the file exists and declares no test function
- `charts/lenny/tests/ops-network-policies_test.yaml`: the file exists and declares no test function
- `charts/lenny/tests/holder-forward_test.yaml`: the file exists and declares no test function

### `13.2.5`

Tiers the map names and the index does not: security.

- `tests/security/agent_egress_test.go` (1 test functions): cites §13.2
- `tests/security/network_policy_test.go` (5 test functions): cites §13.2; cites §13.2, §17.2; cites §13.2.4, §28.5.4, §10.3; cites §29.3, §13.2.4, §28.5.4

### `13.2.6`

Tiers the map names and the index does not: security.

- `tests/security/agent_egress_test.go` (1 test functions): cites §13.2

### `13.2.7`

Tiers the map names and the index does not: security.

- `tests/security/agent_egress_test.go` (1 test functions): cites §13.2
- `tests/security/network_policy_test.go` (5 test functions): cites §13.2; cites §13.2, §17.2; cites §13.2.4, §28.5.4, §10.3; cites §29.3, §13.2.4, §28.5.4

### `13.3`

- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRotationHappyPath`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRejectsCrossTenantCaller`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRejectsCrossTenantIssued`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRejectsScopeBroadening`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRejectsAudienceBroadening`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRejectsCallerTypeElevation`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRejectsExpiredSubject`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateChildMintingProducesA2ADelegation`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRejectsExpiredActor`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRejectsTypMutationOnRotation`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateExpRespectsPerDialectCap`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateExpTruncatesToWholeSeconds`: unannotated
- `pkg/tokenexchange/tokenexchange_test.go::TestValidateRejectsExpInPast`: unannotated

### `13.4`

- `pkg/upload/upload_test.go::TestNormativeCeilingsMatchSpec`: unannotated
- `pkg/upload/upload_test.go::TestForbiddenEntryKinds`: unannotated
- `pkg/upload/upload_test.go::TestValidateEntryAcceptsRegularInWorkspace`: unannotated
- `pkg/upload/upload_test.go::TestValidateEntryRejectsForbiddenKinds`: unannotated
- `pkg/upload/upload_test.go::TestValidateEntryRejectsAbsolutePath`: cites §7.4, §15.1
- `pkg/upload/upload_test.go::TestValidateEntryRejectsTraversal`: cites §7.4, §15.1
- `pkg/upload/upload_test.go::TestValidateEntryRejectsOverLengthPath`: unannotated
- `pkg/upload/upload_test.go::TestValidateEntryRejectsExcessiveDepth`: unannotated
- `pkg/upload/upload_test.go::TestValidateEntryRejectsOversizedEntry`: unannotated
- `pkg/upload/upload_test.go::TestValidateEntryRejectsSymlinkByDefault`: unannotated
- `pkg/upload/upload_test.go::TestValidateEntrySymlinkOptInWorkspaceLocalTarget`: unannotated
- `pkg/upload/upload_test.go::TestValidateEntrySymlinkRejectsEscape`: unannotated
- `pkg/upload/upload_test.go::TestValidateArchiveRejectsOversizedAggregate`: unannotated
- `pkg/upload/upload_test.go::TestValidateArchiveRejectsTooManyEntries`: unannotated
- `pkg/upload/upload_test.go::TestValidateArchiveRejectsExtremeRatio`: unannotated
- `pkg/gateway/podlifecycle/podsession/binder_archive_test.go::TestBindUploadArchiveAbortRecordsMetric`: unannotated
- `pkg/gateway/podlifecycle/podsession/binder_archive_test.go::TestBindUploadArchiveAbortRecordsMetric_MaliciousArchives`: unannotated
- `pkg/adapter/staging_test.go` (20 test functions): cites §14; cites §14.1; cites §5.1; cites §5.2; and 3 more

### `13.5`

- `pkg/gateway/mcpfabric/delegationpolicystore/...` (32 test functions): cites §4.2; unannotated
- `pkg/delegation/lease/...` (18 test functions): cites §4.2; cites §8.3; cites §8.4; unannotated
- `pkg/gateway/policy/interceptor/...` (102 test functions): cites §11.7; cites §11.7, §16.1; cites §15.1, §16.1; cites §16.1; and 6 more

### `14.5`

No numbered heading carries this number.

Tiers the map names and the index does not: load_kind.

- `tests/load_kind/post_hardening_test.go::TestPostHardeningSLO`: cites §13.31
- `tests/load_kind/scenarios/post_hardening_slo`: names no test function in the tree

### `15`

- `pkg/api/v1/session/session_test.go::TestAllStatesCountMatchesSpec`: unannotated
- `pkg/api/v1/session/session_test.go::TestIsTerminalCoversFourStates`: unannotated
- `pkg/api/v1/session/session_test.go::TestFailureClassEnumIsExhaustive`: unannotated
- `pkg/api/v1/session/session_test.go::TestValidateFinalizeAdmitsCreatedOnly`: unannotated
- `pkg/api/v1/session/session_test.go::TestValidateInterruptAdmitsRunningOnly`: no such test function in the tree
- `pkg/api/v1/session/session_test.go::TestValidateTerminateAdmitsEveryNonTerminal`: unannotated
- `pkg/api/v1/session/session_test.go::TestValidateUploadMidSessionGated`: unannotated
- `pkg/api/v1/session/session_test.go::TestValidateDeriveAllowStaleGated`: unannotated
- `pkg/api/v1/session/session_test.go::TestPreconditionErrorEnvelope`: unannotated
- `pkg/gateway/session/sessionstore/memstore/memstore_test.go::TestCreateAndGet`: unannotated
- `pkg/gateway/session/sessionstore/memstore/memstore_test.go::TestGetRejectsCrossTenant`: unannotated
- `pkg/gateway/session/sessionstore/memstore/memstore_test.go::TestListExcludesForeignTenant`: unannotated
- `tests/integration/session_lifecycle_test.go::TestCrossTenantLookupRejectedAgainstRealGateway`: cites §4.2
- `cmd/lenny-compliance/full.go::checkRuntimeOpsHandshake`: no such test function in the tree
- `cmd/lenny-compliance/full.go::checkCheckpointQuiesce`: no such test function in the tree
- `cmd/lenny-compliance/full.go::checkInterruptAck`: no such test function in the tree
- `cmd/lenny-compliance/full.go::checkCredentialRotation`: no such test function in the tree
- `cmd/lenny-compliance/full.go::checkDeadlineSignal`: no such test function in the tree
- `tests/contract/sdks/harness/...` (2 test functions): cites §14.13
- `tests/integration/delegation_child_materialization_test.go` (3 test functions): cites §8.2; cites §8.3

### `15.1`

- `pkg/api/v1/session/session_test.go::TestAllStatesCountMatchesSpec`: unannotated
- `pkg/api/v1/session/session_test.go::TestIsTerminalCoversFourStates`: unannotated
- `pkg/api/v1/session/session_test.go::TestFailureClassEnumIsExhaustive`: unannotated
- `pkg/api/v1/session/session_test.go::TestValidateFinalizeAdmitsCreatedOnly`: unannotated
- `pkg/api/v1/session/session_test.go::TestValidateInterruptAdmitsRunningOnly`: no such test function in the tree
- `pkg/api/v1/session/session_test.go::TestValidateTerminateAdmitsEveryNonTerminal`: unannotated
- `pkg/api/v1/session/session_test.go::TestValidateUploadMidSessionGated`: unannotated
- `pkg/api/v1/session/session_test.go::TestValidateDeriveAllowStaleGated`: unannotated
- `pkg/api/v1/session/session_test.go::TestPreconditionErrorEnvelope`: unannotated
- `pkg/gateway/session/sessionstore/memstore/memstore_test.go::TestCreateAndGet`: unannotated
- `pkg/gateway/session/sessionstore/memstore/memstore_test.go::TestGetRejectsCrossTenant`: unannotated
- `pkg/gateway/session/sessionstore/memstore/memstore_test.go::TestListExcludesForeignTenant`: unannotated
- `tests/integration/session_lifecycle_test.go::TestCrossTenantLookupRejectedAgainstRealGateway`: cites §4.2
- `tests/component/translators/openai_chat_translator_test.go` (2 test functions): cites §4.9
- `tests/component/translators/openai_responses_translator_test.go` (1 test functions): cites §4.9
- `tests/component/translators/openai_singleshot_lifecycle_test.go` (8 test functions): cites §15, §5.2, §6.2, §4.6.3; cites §15, §6.2, §4.6.3; cites §15, §7.2; cites §15.2.1, §11.1, §10.6, §11.4, §11.2; and 2 more
- `tests/security/write_before_issue_failclosed_test.go` (3 test functions): cites §13.3, §11.7, §10.2; cites §13.3, §16.7, §11.7, §10.2
- `tests/e2e_kind/gateway_probe_test.go`: the file exists and declares no test function
- `tests/security/audit_integrity_test.go` (2 test functions): cites §12.9.10; cites §12.9.10, §11.7, §25.9
- `tests/component/migrations/prod_columns_test.go` (10 test functions): cites §11.1; cites §12.2; cites §12.2, §18.5; cites §12.8; and 6 more
- `pkg/gateway/externalapi/admin/signing_unavailable_test.go::TestRotateTokenSigningErrorReturns503RetryAfter_spec_10_2`: cites §10.2, §17.6

### `15.2`

- `tests/integration/holder_forward_routes_test.go::TestOffHolderMCPToolCallsForwardAsToolCalls_spec_29_3`: cites §29.3, §2, §3, §4, §6
- `pkg/gateway/mcpfabric/mcptools/holder_forward_sites_test.go::TestWiredCancelSessionForwardRefusalsStayOutOfTheIdempotencyCache_spec_11_5`: cites §11.5, §29.3

### `15.2.1`

- `tests/contract/ops_endpoints/event_subscription_mcp_parity_test.go::TestEventSubscriptionRESTMCPParityContract`: cites §15.1
- `tests/contract/multiprotocol_journey/journey_test.go` (1 test functions): cites §15
- `tests/integration/openai_cross_tenant_test.go` (1 test functions): cites §15.1, §15
- `pkg/gateway/sessionserver/create_test.go` (47 test functions): cites §10.1; cites §14.1; cites §15.1, §4.6, §6.2, §7.1; cites §5.2, §7.1; and 6 more

### `15.4`

- `tests/contract/adapter_jsonl/set_tracing_context_test.go::TestSetTracingContextRejectsNonStringSessionID`: cites §28.5.3
- `pkg/adapter/slotframe_test.go::TestDemuxSessionOutput`: cites §28.5.3
- `pkg/adapter/slotframe_test.go::TestDemuxSessionOutputCountsAnUnboundSlot_spec_28_5_3`: cites §28.5.3
- `pkg/adapter/tracingcontext_addressing_test.go::TestSetTracingContextUnaddressedOnAMultiSlotPodIsRejected_spec_28_5_3`: cites §28.5.3
- `tests/conformance/concurrent_slot_conformance_test.go::TestUnaddressedSessionScopedFrameIsRefused_spec_28_5_3`: cites §28.5.3
- `tests/contract/adapter_frame_resolution/session_scoped_frame_resolution_test.go::TestUnaddressedFrameResolvesOnAPodHoldingOneSlot_spec_28_5_3`: cites §28.5.3
- `tests/contract/adapter_frame_resolution/session_scoped_frame_resolution_test.go::TestUnaddressedFrameIsRejectedOnAPodHoldingTwoSlots_spec_28_5_3`: cites §28.5.3
- `tests/contract/adapter_frame_resolution/session_scoped_frame_resolution_test.go::TestInboundEnvelopeCarriesTheAddressedSession_spec_28_5_3`: cites §28.5.3
- `tests/contract/adapter_jsonl/gateway_envelope_address_test.go::TestGatewayEnvelopeCarriesTheAddressedSession_spec_28_5_3`: cites §28.5.3
- `tests/contract/adapter_jsonl/subprocess_envelope_address_test.go::TestSubprocessEnvelopeCarriesTheAddressedSession_spec_28_5_3`: cites §28.5.3
- `tests/security/tracing_context_session_isolation_test.go::TestUnaddressedTracingFrameWritesNoCoTenantSession_spec_28_5_3`: cites §28.5.3, §8.3, §13.1
- `pkg/gateway/session/executor/pod_test.go::TestPodExecutorSendAddressesTheAttachStreamBySession`: cites §7.2, §5.2, §28.5.3
- `pkg/gateway/sessionserver/messages_component_test.go::TestMessagesAddressesTheStreamBySessionOnAPoolOfOne`: cites §7.2, §5.2, §28.5.3
- `tests/security/tracing_context_session_isolation_test.go` (1 test functions): cites §28.5.3, §8.3, §13.1
- `tests/conformance/scaffolds_test.go::TestFidelityMatrix`: cites §12.10
- `tests/conformance/concurrent_slot_conformance_test.go::TestConcurrentSessionDispatchConformance`: cites §5.2

### `15.4.3`

Tiers the map names and the index does not: conformance.

- `tests/conformance/scaffold_battery_test.go::TestScaffoldedBinaryMinimalPassesTheBasicBattery_spec_24_18`: cites §15.4.4, §24.18
- `tests/docs/session_failure_outcome_doc_reconciliation_test.go::TestRetiredFailureOutcomePhrasesAbsent`: cites §7.2, §7.3, §28.5.1
- `tests/docs/session_failure_outcome_doc_reconciliation_test.go::TestFailureOutcomeEdgesStated`: cites §7.2, §7.3, §6.2, §28.5.1, §5.2

### `15.4.6`

- `cmd/lenny-compliance/full.go::checkRuntimeOpsHandshake`: no such test function in the tree
- `cmd/lenny-compliance/full.go::checkCheckpointQuiesce`: no such test function in the tree
- `cmd/lenny-compliance/full.go::checkInterruptAck`: no such test function in the tree
- `cmd/lenny-compliance/full.go::checkCredentialRotation`: no such test function in the tree
- `cmd/lenny-compliance/full.go::checkDeadlineSignal`: no such test function in the tree

### `15.6`

- `tests/contract/sdks/harness/...` (2 test functions): cites §14.13

### `16`

- `pkg/observability/metrics/metrics_test.go::TestValidateAcceptsCanonicalNames`: unannotated
- `pkg/observability/metrics/metrics_test.go::TestValidateRejectsForbiddenLabels`: unannotated
- `pkg/observability/metrics/metrics_test.go::TestValidateAccumulatesMultipleViolations`: unannotated
- `pkg/observability/metrics/metrics_test.go::TestForbiddenLabelsCatalog`: unannotated
- `pkg/observability/correlation/correlation_test.go::TestFromHTTPHeaderRoundTrip`: unannotated
- `pkg/observability/correlation/correlation_test.go::TestFromGRPCMetadataLowerCaseKeys`: unannotated
- `pkg/observability/tracing/tracing_test.go::TestStartProjectsCorrelationFieldsAsAttributes`: unannotated
- `pkg/observability/tracing/tracing_test.go::TestRecordErrorAttachesCategoryAttribute`: unannotated
- `pkg/observability/tracing/tracing_test.go::TestSpanNamesCatalogIsExhaustive`: unannotated
- `tests/component/observability/integration_test.go::TestObservabilityPrimitivesWireTogether`: cites §13.4
- `pkg/observability/logging/logging_test.go::TestHandlerEmitsRequiredSlogKeys`: unannotated
- `pkg/observability/logging/logging_test.go::TestHandlerProjectsCorrelationFieldsFromContext`: unannotated
- `pkg/observability/logging/logging_test.go::TestHandlerSkipsEmptyCorrelationFields`: unannotated
- `pkg/alerting/rules/rules_test.go::TestRuleValidateAcceptsWellFormed`: unannotated
- `pkg/alerting/rules/rules_test.go::TestRuleValidateRejectsBadPromQL`: unannotated
- `pkg/alerting/rules/rules_test.go::TestRuleValidateCriticalRequiresRunbook`: unannotated
- `pkg/alerting/rules/rules_test.go::TestCatalogValidates`: unannotated
- `pkg/alerting/rules/rules_test.go::TestCatalogCoversCanonicalAlerts`: unannotated
- `pkg/alerting/rules/rules_test.go::TestCatalogIsCompleteAgainstSpec165`: unannotated
- `pkg/alerting/rules/rules_test.go::TestCatalogHasNoUnspecifiedAlerts`: unannotated
- `pkg/alerting/rules/rules_test.go::TestPoolSecurityDegradedRule`: unannotated
- `pkg/alerting/rules/rules_test.go::TestBurnRateRulesAreDualWindow`: unannotated
- `pkg/alerting/rules/render_test.go::TestRenderPrometheusRuleRendersFullCatalog`: unannotated
- `pkg/observability/metrics/catalog_test.go::TestMetricCatalogIsCompleteAgainstSpec161`: unannotated
- `pkg/observability/metrics/catalog_test.go::TestMetricCatalogHasNoUnspecifiedMetrics`: unannotated
- `pkg/events/catalog_test.go::TestGatewayCatalogIsCompleteAgainstSpec166`: unannotated
- `pkg/events/catalog_test.go::TestOpsCatalogIsCompleteAgainstSpec166`: unannotated
- `pkg/observability/audit/catalog_test.go::TestCatalogIsCompleteAgainstSpec167`: unannotated
- `pkg/observability/audit/catalog_test.go::TestCatalogHasNoUnspecifiedEvents`: unannotated
- `tests/component/observability/catalog_crosscheck_test.go::TestGatewayEvaluatorTracksCatalogAndRenderedChart`: cites §25.13

### `16.1`

- `pkg/gateway/sessionserver/start_pod_test.go` (27 test functions): cites §15.1, §13.4, §7.4; cites §15.1, §4.9; cites §4.2; cites §4.6.1, §5.2; and 14 more
- `pkg/observability/metrics/metrics_test.go::TestValidateAcceptsCanonicalNames`: unannotated
- `pkg/observability/metrics/metrics_test.go::TestValidateRejectsForbiddenLabels`: unannotated
- `pkg/observability/metrics/metrics_test.go::TestValidateAccumulatesMultipleViolations`: unannotated
- `pkg/observability/metrics/metrics_test.go::TestForbiddenLabelsCatalog`: unannotated
- `pkg/observability/metrics/catalog_test.go::TestMetricCatalogEntriesAreWellFormed`: unannotated
- `pkg/observability/metrics/catalog_test.go::TestMetricCatalogIsCompleteAgainstSpec161`: unannotated
- `pkg/observability/metrics/catalog_test.go::TestMetricCatalogHasNoUnspecifiedMetrics`: unannotated
- `pkg/observability/metrics/catalog_test.go::TestLookupMetric`: unannotated
- `tests/e2e_cloud/scaffolds_test.go::TestCloudObservability`: no such test function in the tree
- `pkg/controller/warmpool/pod_retirement_meter_test.go::TestIncControllerUptimeRetirementCountsByLabels_spec_16_1`: unannotated
- `pkg/controller/warmpool/pod_retirement_meter_test.go::TestIncControllerUptimeRetirementSeparatesRuntimeClass_spec_16_1`: unannotated
- `pkg/controller/ssaretry/ssaretry_test.go::TestRetryOnConflictSSAFourConflictsThenSucceedEmitsNothing`: cites §4.6.3
- `pkg/auth/jwt/breakersigner_test.go::TestBreakerSignerHalfOpenRefusesOnlyWhileProbeInFlight_spec_10_2`: cites §10.2
- `pkg/auth/jwt/breakersigner_test.go::TestBreakerSignerConcurrentCallsReportOneOutcomeEach_spec_10_2`: cites §10.2
- `pkg/gateway/sessionserver/derive_failure_audit_test.go::TestDeriveFailurePersistsAuditRow_spec_7_1_derive_rule_2`: cites §7.1, §15.1
- `tests/docs/build_gaps_delegation_tree_records_currency_test.go::TestDelegationTreeFiledDefectsAreConfirmed_spec_8_10`: cites §8.10, §4.2, §4.5

### `16.1.1`

- `pkg/observability/metrics/metrics_test.go::TestValidateRejectsForbiddenLabels`: unannotated
- `pkg/observability/metrics/metrics_test.go::TestForbiddenLabelsCatalog`: unannotated
- `pkg/gateway/sessionserver/expiry_metric_internal_test.go` (2 test functions): unannotated

### `16.3`

Tiers the map names and the index does not: component.

- `pkg/observability/correlation/correlation_test.go::TestFromHTTPHeaderRoundTrip`: unannotated
- `pkg/observability/correlation/correlation_test.go::TestFromGRPCMetadataLowerCaseKeys`: unannotated
- `pkg/observability/tracing/tracing_test.go::TestStartProjectsCorrelationFieldsAsAttributes`: unannotated
- `pkg/observability/tracing/tracing_test.go::TestRecordErrorAttachesCategoryAttribute`: unannotated
- `pkg/observability/tracing/tracing_test.go::TestSpanNamesCatalogIsExhaustive`: unannotated
- `tests/component/observability/integration_test.go::TestObservabilityPrimitivesWireTogether`: cites §13.4
- `pkg/adapter/tracing_external_test.go` (5 test functions): unannotated
- `pkg/adapter/tracing_internal_test.go` (4 test functions): unannotated

### `16.4`

- `pkg/observability/logging/logging_test.go::TestHandlerEmitsRequiredSlogKeys`: unannotated
- `pkg/observability/logging/logging_test.go::TestHandlerProjectsCorrelationFieldsFromContext`: unannotated
- `pkg/observability/logging/logging_test.go::TestHandlerSkipsEmptyCorrelationFields`: unannotated
- `tests/component/observability/integration_test.go::TestObservabilityPrimitivesWireTogether`: cites §13.4

### `16.5`

Tiers the map names and the index does not: load_kind.

- `pkg/alerting/rules/rules_test.go::TestRuleValidateAcceptsWellFormed`: unannotated
- `pkg/alerting/rules/rules_test.go::TestRuleValidateRejectsBadPromQL`: unannotated
- `pkg/alerting/rules/rules_test.go::TestRuleValidateCriticalRequiresRunbook`: unannotated
- `pkg/alerting/rules/rules_test.go::TestCatalogValidates`: unannotated
- `pkg/alerting/rules/rules_test.go::TestCatalogCoversCanonicalAlerts`: unannotated
- `pkg/alerting/rules/rules_test.go::TestCatalogIsCompleteAgainstSpec165`: unannotated
- `pkg/alerting/rules/rules_test.go::TestCatalogHasNoUnspecifiedAlerts`: unannotated
- `pkg/alerting/rules/rules_test.go::TestPoolSecurityDegradedRule`: unannotated
- `pkg/alerting/rules/rules_test.go::TestBurnRateRulesAreDualWindow`: unannotated
- `pkg/alerting/rules/render_test.go::TestRenderPrometheusRuleRendersFullCatalog`: unannotated
- `tests/component/observability/catalog_crosscheck_test.go::TestGatewayEvaluatorTracksCatalogAndRenderedChart`: cites §25.13
- `tests/load_kind/experiment_test.go::TestExperimentLoad`: cites §13.34
- `tests/load_kind/scenarios/experiment_load`: names no test function in the tree
- `pkg/tokenservice/promemit/promemit_test.go::TestEmitterRegistersAndServesCatalog`: cites §16.1
- `charts/lenny/tests/gateway-deployment_test.yaml`: the file exists and declares no test function

### `16.6`

- `pkg/events/...` (23 test functions): cites §25.3; cites §25.3, §12.6; cites §25.5; unannotated
- `pkg/events/catalog_test.go::TestGatewayCatalogIsCompleteAgainstSpec166`: unannotated
- `pkg/events/catalog_test.go::TestOpsCatalogIsCompleteAgainstSpec166`: unannotated

### `16.7`

- `pkg/observability/audit/catalog_test.go::TestCatalogIsCompleteAgainstSpec167`: unannotated
- `pkg/observability/audit/catalog_test.go::TestCatalogHasNoUnspecifiedEvents`: unannotated
- `pkg/observability/audit/catalog_test.go::TestCatalogHasNoDuplicatesOrEmpties`: unannotated
- `pkg/observability/audit/catalog_test.go::TestIsKnownEventType`: unannotated
- `pkg/ops/configservice/configservice_test.go::TestApply_ConfirmEmitsConfigChanged_spec_16_7`: unannotated
- `pkg/ops/configservice/configservice_test.go::TestApply_DryRunNoConfigChanged_spec_16_7`: unannotated
- `pkg/ops/configservice/configservice_test.go::TestApply_ValidationFailureNoConfigChanged_spec_16_7`: unannotated
- `pkg/ops/configservice/configservice_test.go::TestApply_GatewayErrorNoConfigChanged_spec_16_7`: unannotated
- `pkg/ops/configservice/configservice_test.go::TestApply_IdempotentConfirmEmitsConfigChanged_spec_16_7`: unannotated

### `16.8`

- `pkg/observability/metrics/...` (21 test functions): cites §11.2, §16.1, §16.1.1; cites §16.1, §16.5; cites §16.5; cites §16.5, §16.1; and 1 more

### `16.10`

- `pkg/alerting/rules/openslo_test.go::TestRenderOpenSLOObjectivesMatchCatalog`: unannotated
- `pkg/alerting/rules/slo_test.go::TestSLODefinitionsBackEveryBurnRateAlert`: unannotated
- `charts/lenny/tests/openslo-configmap_test.yaml`: the file exists and declares no test function

### `17`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `17.2`

- `pkg/preflight/phasestamp_test.go` (6 test functions): unannotated
- `pkg/preflight/run_test.go` (19 test functions): cites §12.9; cites §13.1; cites §17.6, §17.9.1; cites §17.9.2, §17.9; and 1 more
- `pkg/preflight/hostsharing_test.go` (7 test functions): unannotated
- `pkg/chart/values/helm_unittest_test.go` (2 test functions): cites §12.1
- `pkg/controller/agentpod/podspec/podspec_test.go` (38 test functions): cites §13.1; cites §4.6.1, §4.7.10; cites §6.4, §4.7; unannotated

### `17.4`

- `tests/contract/adapter_jsonl/subprocess_session_start_test.go` (1 test functions): cites §28.5.3
- `tests/docs/time_to_hello_world_test.go` (3 test functions): cites §22.7
- `charts/lenny/tests/kms-master-key-secret_test.yaml`: the file exists and declares no test function
- `charts/lenny/tests/gateway-deployment_test.yaml`: the file exists and declares no test function
- `charts/lenny/tests/token-service-deployment_test.yaml`: the file exists and declares no test function
- `charts/lenny/tests/ops-deployment_test.yaml`: the file exists and declares no test function

### `17.5`

- `tests/e2e_cloud/aws_resources_test.go::TestCloudKMS`: cites §4.3
- `tests/e2e_cloud/aws_resources_test.go::TestCloudCSI`: cites §4.5
- `tests/e2e_cloud/cloud_portability_test.go::TestCloudKMSGCP`: cites §12.6
- `tests/e2e_cloud/cloud_portability_test.go::TestCloudCSIGCS`: cites §12.6
- `tests/e2e_cloud/cloud_portability_test.go::TestCloudKMSAzure`: cites §12.6
- `tests/e2e_cloud/cloud_portability_test.go::TestCloudCSIAzureBlob`: cites §12.6

### `17.6`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `17.8.2`

- `charts/lenny/tests/kms-guard_test.yaml`: the file exists and declares no test function
- `charts/lenny/tests/gateway-deployment_test.yaml`: the file exists and declares no test function

### `17.8.6`

Tiers the map names and the index does not: unit.

- `pkg/common/registry/resolver_test.go` (8 test functions): cites §18.1

### `17.9`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `18`

- `tests/docs/docs_test.go::TestTESTINGmdContractDirectoryReferencesResolve`: cites §13.10, §13.15

### `24`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more
- `tests/contract/oauth_token/oauth_token_test.go` (10 test functions): cites §13.3

### `24.0`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `24.1`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `24.2`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `24.3`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `24.4`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `24.5`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `24.6`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `24.7`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `24.8`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `24.9`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `24.10`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `24.11`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `24.12`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `24.13`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `24.14`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `24.15`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `24.16`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more
- `pkg/ctlcli/session_ops_test.go::TestMCPManagementFallsBackToGatewayWhenOpsURLAbsent`: unannotated

### `24.17`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `24.18`

Tiers the map names and the index does not: docs, e2e_kind.

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more
- `tests/docs/time_to_hello_world_test.go` (3 test functions): cites §22.7
- `tests/e2e_kind/runtime_publish_journey_test.go::TestRuntimePublishJourney`: cites §26

### `24.19`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more

### `24.20`

Tiers the map names and the index does not: docs.

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more
- `tests/docs/time_to_hello_world_test.go` (3 test functions): cites §22.7

### `25`

- `tests/contract/ops_endpoints/helpers_test.go`: the file exists and declares no test function

### `25.1`

- `tests/integration/ops_endpoints_test.go::TestOpsRemediationIdempotencyReplayAgainstPostgresE2E`: cites §25.4

### `25.2`

Tiers the map names and the index does not: chaos, e2e_cloud.

- `tests/static/structure/spec_map_operability_packages_test.go::TestSpecMapPackagesIncludeEveryTestReferencedPackage`: unannotated
- `tests/static/structure/spec_map_operability_packages_test.go::TestSpecMapShippedOperabilitySectionsCarryNoBlockedUntilPhase`: unannotated
- `tests/static/structure/spec_map_operability_packages_test.go::TestSpecMapExceptionsDoesNotDeferShippedOperabilitySections`: unannotated
- `tests/e2e_cloud/ops_surface_test.go::TestCloudOpsSurfaceHealthy`: cites §25.1
- `tests/chaos/store_failure_test.go` (4 test functions): cites §12.8

### `25.3`

Tiers the map names and the index does not: chaos, security.

- `tests/chaos/store_failure_test.go` (4 test functions): cites §12.8
- `tests/static/structure/change_graph_gateway_operability_selection_test.go::TestChangeGraphGatewayOperabilityPackagesSelectUnitTier`: cites §25.2
- `tests/security/ops_event_buffer_fanout_authz_test.go::TestOpsGatewayBufferFanOutRequiresPlatformAdmin_spec_25_4_25_5`: cites §25.4, §25.5

### `25.4`

- `tests/docs/build_gaps_ops_lock_hash_tag_currency_test.go` (3 test functions): unannotated
- `pkg/ops/opsservice/selfchecks_test.go::TestSelfHealthCheckNamesAreSnakeCase_spec_25_4_2494`: unannotated
- `pkg/ops/probe/probe_test.go` (5 test functions): unannotated
- `pkg/ops/conventions/conventions_test.go` (12 test functions): cites §16.3, §25.2; unannotated
- `pkg/ops/opsserver/locks_test.go::TestExtendSplitBrainConflictCarriesDetails`: unannotated
- `tests/integration/ops_escalations_e2e_test.go::TestOpsServiceEscalationStoreBackedE2E`: cites §25.2
- `tests/integration/lenny_ctl_ops_test.go::TestLennyCtlOperabilityCommandsE2E`: cites §25.14
- `pkg/ops/opsserver/operations_restore_test.go::TestOperationsInventoryListsRunningRestore`: unannotated
- `pkg/ops/opsserver/operations_restore_test.go::TestOperationsInventoryListsFailedRestore`: unannotated
- `pkg/ops/opsserver/operations_backup_test.go::TestOperationsInventoryListsRunningBackup`: unannotated
- `pkg/ops/opsserver/operations_backup_test.go::TestOperationsInventoryListsVerifyingBackup`: unannotated
- `charts/lenny/tests/ops-rbac_test.yaml`: the file exists and declares no test function
- `charts/lenny/tests/ops-deployment_test.yaml`: the file exists and declares no test function
- `charts/lenny/tests/ops-network-policies_test.yaml`: the file exists and declares no test function
- `tests/integration/jwt_keyset_cross_process_test.go::TestSigningKeyRotationUnderLocal_spec_10_2`: cites §10.2

### `25.5`

- `pkg/ops/opsservice/subscriptioncache_test.go::TestSubscriptionCacheRefreshUpdates`: unannotated
- `pkg/ops/opsservice/subscriptioncache_test.go::TestSubscriptionCacheSurvivesListError`: unannotated
- `pkg/ops/opsservice/webhookloop_emit_test.go::TestWebhookWorkerDoesNotRedeliverDeliveryFailureToFailingSubscription`: unannotated
- `tests/contract/ops_endpoints/event_subscription_mcp_parity_test.go::TestEventSubscriptionRESTMCPParityContract`: cites §15.1
- `tests/integration/escalation_webhook_under_outage_test.go::TestEscalationWebhookReachesReceiverWhilePostgresDown`: cites §25.4
- `tests/static/structure/spec_map_operability_packages_test.go::TestSpecMapEventStreamNotesDoNotOverclaimRedisSupport`: unannotated
- `tests/chaos/event_stream_recovery_test.go::TestOpsEventStreamRepeatsTheDegradationEnvelopeWhileRedisIsDown`: no such test function in the tree
- `tests/component/eventstream/readerscope_testsupport_test.go`: the file exists and declares no test function
- `tests/contract/cloudevents/readerscope_testsupport_test.go`: the file exists and declares no test function
- `tests/integration/readerscope_testsupport_test.go`: the file exists and declares no test function
- `tests/load_local/readerscope_testsupport_test.go`: the file exists and declares no test function
- `tests/chaos/readerscope_testsupport_test.go`: the file exists and declares no test function
- `tests/security/readerscope_testsupport_test.go`: the file exists and declares no test function

### `25.6`

- `pkg/ops/diagnostics/cause_test.go` (7 test functions): unannotated
- `pkg/ops/diagnostics/bottleneck_test.go` (1 test functions): unannotated
- `pkg/ops/diagnostics/chain_test.go` (2 test functions): unannotated
- `pkg/ops/diagnostics/prodsource/readers_test.go` (13 test functions): unannotated
- `pkg/ops/opsserver/opsserver_test.go` (16 test functions): cites §25.4; unannotated
- `pkg/ops/opsserver/diagnostics_audit_test.go` (4 test functions): unannotated
- `pkg/ops/auditrate/auditrate_test.go` (10 test functions): unannotated
- `tests/component/opsaudit/diagnostics_rate_limit_test.go` (1 test functions): cites §25.9

### `25.7`

- `pkg/ops/runbooks/index_test.go` (4 test functions): unannotated
- `pkg/ops/runbooks/steps_test.go` (2 test functions): unannotated
- `pkg/ops/mcp/server_test.go::TestToolsListReturnsTheInventory`: unannotated

### `25.8`

- `pkg/ops/upgradeservice/pgstore/pgstore_test.go::TestUpgradeStateRoundTrip_spec_25_8`: unannotated
- `pkg/ops/upgradeservice/pgstore/pgstore_test.go::TestUpgradeStateTerminalReplaces_spec_25_8`: unannotated
- `pkg/ops/upgradeservice/pgstore/pgstore_test.go::TestCheckCacheRoundTrip_spec_25_8`: unannotated
- `pkg/ops/configservice/configservice_test.go::TestDiff_InSync_spec_25_8`: unannotated
- `pkg/ops/configservice/configservice_test.go::TestDiff_Changes_spec_25_8`: unannotated
- `pkg/ops/configservice/configservice_test.go::TestDiff_GatewayUnavailable_spec_25_8`: unannotated
- `pkg/ops/configservice/configservice_test.go::TestApply_ValidationFailed_spec_25_8`: unannotated
- `pkg/ops/configservice/configservice_test.go::TestApply_DryRun_spec_25_8`: unannotated
- `pkg/ops/configservice/configservice_test.go::TestApply_Confirm_spec_25_8`: unannotated
- `pkg/ops/configservice/configservice_test.go::TestApply_ConfirmRestartRequired_spec_25_8`: unannotated
- `pkg/ops/configservice/configservice_test.go::TestApply_GatewayApplyError_spec_25_8`: unannotated
- `pkg/ops/configservice/configservice_test.go::TestApply_LowerMinimumWarning_spec_25_8`: unannotated
- `pkg/ops/configservice/configservice_test.go::TestNew_NilGatewayPanics_spec_25_8`: unannotated
- `tests/integration/drift_target_snapshot_promote_test.go::TestUpgradePromotesTargetSnapshotToLiveDurable`: cites §25.10
- `charts/lenny/tests/registry-digest-webhook_test.yaml`: the file exists and declares no test function
- `pkg/ops/opsinventory/upgrade_progress_test.go::TestUpgradeInventoryActiveUsesFixedPhaseDurations`: unannotated
- `pkg/ops/opsinventory/upgrade_progress_test.go::TestUpgradeInventoryActiveUsesHistoricalP50WithBaselines`: unannotated
- `pkg/ops/opsinventory/upgrade_progress_test.go::TestUpgradeInventoryPausedReportsNoEta`: unannotated

### `25.9`

- `tests/docs/diagnostics_audit_wiring_comment_test.go::TestDiagnosticsAuditWiringCommentsNotStale`: cites §11.7

### `25.10`

Tiers the map names and the index does not: security.

- `pkg/drift/drift_test.go` (10 test functions): unannotated
- `tests/static/structure/change_graph_drift_selection_test.go::TestChangeGraphDriftEntryCoversConfigServiceConsumer`: cites §25.8
- `tests/security/ops_rbac_test.go::TestOpsTenantAdminDriftCrossTenantForbidden_spec_12_9_1`: unannotated

### `25.11`

- `pkg/ops/backup/region_test.go` (6 test functions): unannotated
- `pkg/ops/backup/pgstore/pgstore_test.go` (6 test functions): unannotated
- `pkg/ops/backup/runner/audit_test.go` (5 test functions): cites §16.7; unannotated
- `pkg/ops/backup/runner/audit_verify_internal_test.go` (3 test functions): unannotated
- `pkg/ops/backup/runner/exec_test.go` (7 test functions): unannotated
- `pkg/ops/backup/runner/opsevents_test.go` (3 test functions): unannotated
- `pkg/ops/backup/runner/runner_test.go` (9 test functions): unannotated
- `pkg/gateway/externalapi/admin/artifact_replication_test.go` (7 test functions): unannotated
- `pkg/blobstore/replication/replication_test.go` (15 test functions): unannotated
- `charts/lenny/tests/restore-test-cronjob_test.yaml`: the file exists and declares no test function
- `charts/lenny/tests/backup-job_test.yaml`: the file exists and declares no test function

### `25.12`

- `tests/contract/ops_endpoints/event_subscription_mcp_parity_test.go::TestEventSubscriptionRESTMCPParityContract`: cites §15.1
- `pkg/ops/mcp/server_test.go::TestToolsListIntersectsCapabilityFilterWithScopeClaim`: unannotated
- `pkg/ops/mcp/server_test.go::TestToolsCallDryRunMapsToMetaFlag`: unannotated

### `25.13`

Tiers the map names and the index does not: chaos.

- `charts/lenny/tests/tier-presets_test.yaml`: the file exists and declares no test function
- `tests/chaos/store_failure_test.go` (4 test functions): cites §12.8

### `25.14`

- `cmd/lenny-ctl/...` (26 test functions): cites §15.4.6; cites §15.4.6, §28.5.3; cites §26.12; cites §5.1, §5.2; and 2 more
- `tests/integration/lenny_ctl_ops_test.go::TestLennyCtlMCPManagementOpsRoutingE2E`: cites §24.16

### `25.15`

- `tests/integration/escalation_webhook_under_outage_test.go::TestEscalationWebhookReachesReceiverWhilePostgresDown`: cites §25.4

### `26`

Tiers the map names and the index does not: conformance.

- `tests/conformance/reference_battery_test.go::TestImageDeploymentConformance`: cites §12.10

### `26.5`

- `pkg/adapter/manifest_fields_test.go` (2 test functions): cites §4.7

### `26.6`

- `pkg/adapter/manifest_fields_test.go` (2 test functions): cites §4.7

### `26.8`

- `pkg/adapter/manifest_fields_test.go` (2 test functions): cites §4.7

### `26.9`

- `pkg/adapter/manifest_fields_test.go` (2 test functions): cites §4.7

### `27`

- `pkg/gateway/mcpfabric/playground/sessionrecord_test.go` (6 test functions): unannotated

### `27.2`

- `pkg/gateway/mcpfabric/playground/playground_test.go::TestConfigValidateRejectsBadAuthMode`: unannotated
- `pkg/gateway/mcpfabric/playground/playground_test.go::TestConfigValidateDevTenantRules`: unannotated

### `27.3`

- `pkg/gateway/mcpfabric/playground/playground_test.go::TestDevModeMintStampsOriginClaim`: unannotated
- `pkg/gateway/mcpfabric/playground/playground_test.go::TestDevModeReadyGateRejectsUnseededTenant`: unannotated
- `pkg/gateway/mcpfabric/playground/playground_test.go::TestAPIKeyModeRejectsNonUserBearer`: unannotated
- `pkg/gateway/mcpfabric/playground/playground_test.go::TestAPIKeyModeScopeIsNarrowedToIntersection`: unannotated
- `pkg/gateway/mcpfabric/playground/playground_test.go::TestOIDCModeRejectsBearerOnTokenEndpoint`: unannotated
- `pkg/gateway/mcpfabric/playground/playground_test.go::TestOIDCLoginRedirectsAndSetsStateCookie`: unannotated

### `27.6`

- `pkg/gateway/mcpfabric/playground/playground_test.go::TestEffectiveIdleAndDurationCaps`: unannotated
- `pkg/gateway/mcpfabric/playground/sessionrecord_test.go::TestMemorySessionStorePerTenantIsolation`: unannotated
- `pkg/gateway/mcpfabric/playground/sessionrecord_test.go::TestMemorySessionStoreRevocationKeyIsolation`: unannotated
- `pkg/gateway/mcpfabric/playground/sessionrecord_test.go::TestRevokeSessionDeletesRecordAndMarksBearers`: unannotated
- `pkg/gateway/mcpfabric/playground/sessionrecord_test.go::TestLogoutRevokesSessionBearer`: unannotated
- `pkg/gateway/mcpfabric/playground/sessionrecord_test.go::TestRevokedBearerCheckSurvivesAcrossHandlers`: unannotated

### `27.7`

Tiers the map names and the index does not: unit.

- `pkg/gateway/mcpfabric/playground/playground_test.go::TestSecurityHeadersAndCSP`: unannotated
- `pkg/gateway/mcpfabric/playground/playground_test.go::TestStaticAssetCacheHeaders`: unannotated
- `pkg/gateway/mcpfabric/playground/playground_test.go::TestConfigJSONCarriesDevBanner`: unannotated

### `27.8`

- `pkg/observability/metrics/catalog_test.go::TestMetricCatalogIsCompleteAgainstSpec161`: unannotated

### `27.9`

- `pkg/gateway/mcpfabric/playground/playground_test.go::TestSecurityHeadersAndCSP`: unannotated
- `pkg/gateway/mcpfabric/playground/sessionrecord_test.go::TestMemorySessionStorePerTenantIsolation`: unannotated
- `pkg/gateway/mcpfabric/playground/sessionrecord_test.go::TestRevokeSessionDeletesRecordAndMarksBearers`: unannotated

### `28`

- `tests/contract/adapter_generation_fence/generation_fence_wire_test.go` (4 test functions): cites §10.1, §4.7; cites §10.1, §4.7, §10.1.8

### `28.2`

- `tests/docs/successor_pointer_test.go` (3 test functions): cites §28.1

### `28.3`

Tiers the map names and the index does not: integration.

- `tests/integration/credential_lifecycle_test.go::TestRuntimeOpsManifestKeyResolvesTheOperationsSocket`: cites §4.7

### `28.5.3`

- `pkg/adapter/manifest_test.go` (11 test functions): cites §4.7; cites §4.7.6; unannotated
- `pkg/adapter/embedded_sdkwarm_test.go` (1 test functions): cites §6.1
- `pkg/adapter/manifest_fields_test.go` (2 test functions): cites §4.7
- `pkg/outputtype/outputtype_test.go` (3 test functions): unannotated
- `tests/contract/adapter_jsonl/messages_test.go` (8 test functions): cites §15.4
- `tests/conformance/scaffold_battery_test.go::TestScaffoldedBinaryMinimalPassesTheBasicBattery_spec_24_18`: cites §15.4.4, §24.18
- `pkg/adapter/holdstate_test.go` (22 test functions): cites §10.1; cites §10.1, §10.1.4, §4.7; cites §10.1, §4.7; cites §10.1, §4.7, §5.2; and 8 more
- `tests/integration/concurrent_workspace_test.go` (2 test functions): cites §5.2, §6.4; cites §7.1, §5.2
- `tests/docs/adapter_metric_catalog_test.go` (1 test functions): cites §16.1, §4.7
- `tests/docs/session_failure_outcome_doc_reconciliation_test.go::TestRetiredFailureOutcomePhrasesAbsent`: cites §7.2, §7.3, §28.5.1
- `tests/docs/session_failure_outcome_doc_reconciliation_test.go::TestFailureOutcomeEdgesStated`: cites §7.2, §7.3, §6.2, §28.5.1, §5.2

### `28.5.4`

Tiers the map names and the index does not: chaos.

- `pkg/gateway/sessionserver/holder_forward_test.go::TestForwardFailuresAfterTheConnectionAreUnknown_spec_29_3`: cites §29.3, §11.5
- `pkg/gateway/sessionserver/holder_forward_test.go::TestForwardOnAReusedKeepAliveConnectionThatFailsOnWriteIsUnknown_spec_29_3`: cites §29.3
- `pkg/gateway/sessionserver/holder_forward_test.go::TestForwardRunsUnderTheCallersDeadlineWithNoBoundOfItsOwn_spec_29_3`: cites §29.3
- `pkg/gateway/sessionserver/holder_forward_listener_test.go::TestHolderForwardListenerValidatesTheGatewayPeer_spec_10_3`: cites §10.3
- `tests/integration/holder_forward_routes_test.go::TestStaleForwardIsRefusedByTheFormerHolder_spec_29_3`: cites §29.3, §28.6
- `tests/chaos/holder_forward_chaos_test.go::TestCoordinatorCrashAfterReadingAForwardIsRefusedAndNotHeld_spec_29_3`: cites §29.3, §7.2
- `tests/chaos/holder_forward_chaos_test.go::TestForwardResolvesTheCoordinatorThroughThePostgresLeaseFallback_spec_10_1_1`: cites §10.1.1
- `pkg/gateway/sessionserver/holder_forward_test.go::TestForwardedResponsesTurnAtANonHolderIsNotDeliveredInAnyState_spec_29_3`: cites §29.3, §11.5, §15
- `pkg/gateway/sessionserver/holder_forward_test.go::TestForwardedResponsesTurnAtTheHolderRunsLocally_spec_29_3`: cites §29.3, §15
- `pkg/gateway/sessionserver/holder_forward_test.go::TestUnforwardedResponsesTurn_spec_29_3`: cites §29.3, §6, §11.5, §15
- `pkg/gateway/sessionserver/holder_forward_test.go::TestForwardedRequestAppliesRuleFiveAheadOfTheStateGate_spec_29_3`: cites §29.3, §5, §10.1.1
- `pkg/gateway/sessionserver/holder_forward_test.go::TestResponsesTurnForwardStatesAreRunningOnly_spec_29_3`: cites §29.3, §15
- `pkg/gateway/sessionserver/holder_forward_sites_test.go::TestForwardedCreateAndStartKeepsTheAdmissionRateLimit_spec_29_3`: cites §29.3, §11.1, §15
- `cmd/lenny-gateway/holder_forward_wiring_test.go::TestHolderForwardInnerRoutesOnlyTheResponsesCreate_spec_29_3`: cites §29.3, §15
- `tests/integration/holder_forward_responses_test.go::TestOffHolderResponsesTurnIsServedOnTheHoldersPod_spec_29_3`: cites §29.3, §2, §15
- `tests/integration/holder_forward_responses_test.go::TestUndeliveredResponsesTurnFailsClosedInTheNativeEnvelope_spec_29_3`: cites §29.3, §15
- `pkg/gateway/environment/translator/open_responses_conversation_test.go::TestOpenResponsesNonLocalForwardWritesNothing_spec_29_3`: cites §29.3, §15
- `pkg/gateway/environment/translator/open_responses_conversation_test.go::TestOpenResponsesForwardedRequestAlwaysGoesToTheForwarder_spec_29_3`: cites §29.3, §15
- `pkg/gateway/environment/translator/open_responses_conversation_test.go::TestOpenResponsesHandlerRequiresForwarderAndTerminal_spec_15`: cites §15, §29.3
- `tests/contract/rest_openai_responses/held_session_contract_test.go::TestRefusedWarmTurnIsNative409WithCurrentState_spec_29_3`: cites §15, §29.3
- `cmd/lenny-gateway/credentialsurface_singleshot_test.go::TestSessionHolderForwarderAdaptsTheSessionServer_spec_29_3`: cites §29.3, §15
- `tests/chaos/open_responses_chaos_test.go::TestCoordinatorCrashAfterReadingAForwardedResponsesTurnFailsClosed_spec_29_3`: cites §29.3, §15
