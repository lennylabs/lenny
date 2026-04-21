---
layout: default
title: Error Catalog
parent: Reference
nav_order: 1
---

# Error Catalog
{: .no_toc }

Complete reference for every error code returned by the Lenny platform. Each error includes its category, HTTP status, retryability, a detailed description, and the recommended client action.

For guidance on building error-handling logic into your client, see the [Error Handling Guide](../client-guide/).

<details open markdown="block">
  <summary>Table of contents</summary>
  {: .text-delta }
- TOC
{:toc}
</details>

---

## Error response envelope

All REST API endpoints return errors using the following canonical JSON envelope:

```json
{
  "error": {
    "code": "QUOTA_EXCEEDED",
    "category": "POLICY",
    "message": "Tenant t1 has exceeded its monthly session quota (limit: 500).",
    "retryable": false,
    "details": {}
  }
}
```

| Field | Type | Description |
|:------|:-----|:------------|
| `code` | string (required) | Machine-readable error code from the tables below. |
| `category` | string (required) | One of `TRANSIENT`, `PERMANENT`, `POLICY`, `UPSTREAM`. |
| `message` | string (required) | Human-readable description. |
| `retryable` | boolean (required) | Whether the client should retry the request. |
| `details` | object (optional) | Additional context; structure varies by error code. |

MCP tool errors use the same `code` and `category` fields inside the MCP error response format, so clients can apply a single error-handling strategy regardless of API surface.

---

## Error categories

| Category | Meaning | General guidance |
|:---------|:--------|:-----------------|
| `TRANSIENT` | Retryable infrastructure or timing issue (pod crash, timeout, pool exhaustion). | Retry with exponential backoff. Respect `Retry-After` header when present. |
| `PERMANENT` | Not retryable without changing the request (validation failure, invalid state, missing resource). | Fix the request payload or sequencing, then retry. |
| `POLICY` | Denied by the policy engine (quota, rate limit, scope, credential). | Check quotas, permissions, and policy configuration. Some are retryable after a cooldown. |
| `UPSTREAM` | External dependency failure (MCP tool, auth provider, LLM endpoint). | Retry if the upstream issue is transient. Check provider status pages. |

---

## PERMANENT errors

Errors that indicate a client-side issue. The request must be corrected before retrying.

| Code | HTTP | Description | Recommended client action |
|:-----|:-----|:------------|:--------------------------|
| `VALIDATION_ERROR` | 400 | Request body or query parameters failed validation. The `details.fields` array describes each invalid field with its `field` path, `message`, `rule`, and optional `params`. | Fix the identified fields and resubmit. See the [validation error format](#validation-error-format) below. |
| `WORKSPACE_PLAN_INVALID` | 400 | The `workspacePlan` object in `POST /v1/sessions` (or `POST /v1/sessions/start`) failed JSON Schema validation. Reserved for inner-plan schema failures; outer-envelope failures use field-specific codes. `details.field` identifies the offending plan path (e.g., `sources[<n>].mode`, `sources[<n>].resolvedCommitSha`) and `details.reason` carries a short sub-code (`invalid_mode_format`, `setuid_setgid_prohibited`, `sticky_on_file_prohibited`, `gateway_written_field`, `schema_validation_failed`). `details.fields` is included for multi-violation reports. | Correct the `workspacePlan` fields named in `details` and resubmit. |
| `INVALID_STATE_TRANSITION` | 409 | The requested operation is not valid for the current resource state. `details.currentState` and `details.allowedStates` are included. | Check the current session/resource state and call the appropriate endpoint for that state. See [State Machines](state-machines). |
| `RESOURCE_NOT_FOUND` | 404 | The requested resource does not exist or is not visible to the caller. | Verify the resource ID. The resource may have been deleted or may belong to a different tenant. |
| `RESOURCE_ALREADY_EXISTS` | 409 | A resource with the given identifier already exists. | Use a different identifier or update the existing resource via `PUT`. |
| `RESOURCE_HAS_DEPENDENTS` | 409 | Resource cannot be deleted because it is referenced by active dependents. `details.dependents` lists blocking references by type, name, count, and up to 20 individual resource IDs (truncated with `truncated: true` when more exist). | Remove or reassign the blocking dependents first, then retry the deletion. |
| `ETAG_MISMATCH` | 412 | The `If-Match` ETag does not match the current resource version. `details.currentEtag` contains the current value. | Re-`GET` the specific resource, merge changes against the current version, and retry with the updated ETag. |
| `ETAG_REQUIRED` | 428 | `If-Match` header is required on PUT but was not provided. | Re-`GET` the resource to obtain its current ETag, then include it in the `If-Match` header. |
| `UNAUTHORIZED` | 401 | Missing or invalid authentication credentials. | Provide valid credentials (OIDC token, API key). |
| `MCP_VERSION_UNSUPPORTED` | 400 | Client MCP version is not supported by this gateway. | Upgrade the MCP client to a supported version. The error response includes the list of supported versions. |
| `IMAGE_RESOLUTION_FAILED` | 422 | Container image reference is invalid or could not be resolved. `details.image` contains the reference; `details.reason` describes the failure (`invalid_digest`, `tag_not_found`, `registry_unreachable`). | Verify the image reference, ensure the registry is reachable, and check digest/tag validity. |
| `RESERVED_IDENTIFIER` | 422 | A field value uses a platform-reserved identifier (e.g., variant `id: "control"`). `details.field` and `details.value` identify the offending value. | Choose a different, non-reserved identifier. |
| `CONFIGURATION_CONFLICT` | 422 | The requested configuration contains mutually incompatible field values. `details.conflicts` lists each incompatibility with the conflicting fields and a description. | Resolve the field conflicts described in the error details. |
| `SEED_CONFLICT` | 409 | A bootstrap/seed upsert conflicts with an existing resource in a non-idempotent way and `--force-update` was not set. `details.resource` and `details.conflictingFields` identify the conflict. | Use `--force-update` to override, or resolve the conflicting fields. |
| `INVALID_INTERCEPTOR_PRIORITY` | 422 | External interceptor registration specifies `priority <= 100`, which is reserved for built-in security-critical interceptors. | Set `priority > 100`. |
| `INVALID_INTERCEPTOR_PHASE` | 422 | External interceptor registration includes the `PreAuth` phase, which is exclusively reserved for built-in interceptors. | Remove `PreAuth` from the phase set. |
| `INCOMPATIBLE_RUNTIME` | 400 | `POST /v1/sessions/{id}/replay` rejected because `targetRuntime` has a different `executionMode` than the source session. `details.sourceExecutionMode` and `details.targetExecutionMode` are included. | Use a target runtime with the same execution mode as the source session. |
| `REPLAY_ON_LIVE_SESSION` | 409 | `POST /v1/sessions/{id}/replay` rejected because the source session is not in a terminal state. | Wait for the source session to reach a terminal state (`completed`, `failed`, `cancelled`, `expired`). |
| `DERIVE_ON_LIVE_SESSION` | 409 | `POST /v1/sessions/{id}/derive` rejected because the source session is not in a terminal state and `allowStale: true` was not set. | Either wait for the source session to complete, or pass `allowStale: true` in the request body. |
| `TARGET_TERMINAL` | 409 | Target task or session is in a terminal state. | The target session has already ended. Start a new session or derive from the completed one. |
| `DELEGATION_CYCLE_DETECTED` | 400 | Delegation rejected because the target's resolved `(runtime_name, pool_name)` tuple appears in the caller's delegation lineage. `details.cycleRuntimeName` and `details.cyclePoolName` identify the cycle. | Choose a different delegation target to break the cycle. |
| `OUTPUTPART_TOO_LARGE` | 413 | An `OutputPart` payload exceeds the per-part size limit (50 MB). `details.partIndex`, `details.sizeBytes`, and `details.limitBytes` are included. | Split the output into smaller parts or reduce the payload size. |
| `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` | 422 | A stored `WorkspacePlan` uses a `schemaVersion` higher than this gateway understands. `details.knownVersion` and `details.encounteredVersion` are included. | Upgrade the gateway to a version that supports the newer schema. |
| `GIT_CLONE_REF_UNRESOLVABLE` | 422 | A `gitClone` source in the `WorkspacePlan` could not be materialized because the gateway failed to resolve the requested ref to a commit and the failure is not retryable without source-definition changes. `details.url`, `details.ref`, `details.sourceIndex`, and `details.reason` (`auth_failed` \| `ref_not_found`) are included. | Verify the repository URL, the requested ref, and the configured credential. Retry only after correcting the source definition. |
| `GIT_CLONE_REF_RESOLVE_TRANSIENT` | 503 | A `gitClone` source in the `WorkspacePlan` could not be resolved because `git ls-remote` failed with a transient network error (DNS, connection reset, TLS handshake, remote-host timeout). `details.url`, `details.ref`, `details.sourceIndex`, and `details.reason` (`network_error`) are included. `Retry-After` suggests a delay. | Retry the request after the `Retry-After` window. |
| `INVALID_POOL_CONFIGURATION` | 422 | Pool creation or update rejected due to an invalid configuration constraint. `details.message` describes the violated constraint. | Correct the pool configuration per the constraint described in the error. |
| `COMPLIANCE_PGAUDIT_REQUIRED` | 422 | Tenant creation/update rejected because its `complianceProfile` requires pgaudit to be enabled with a configured `sinkEndpoint`. | Configure `audit.pgaudit.enabled: true` with a valid `sinkEndpoint` before creating the regulated tenant. |
| `SESSION_NOT_EVAL_ELIGIBLE` | 422 | Eval submission rejected because the target session is in a terminal state (`cancelled` or `expired`) that is not eligible for eval storage. | Submit evals only for sessions in `completed` or `failed` state. |
| `DUPLICATE_MESSAGE_ID` | 400 | A sender-supplied message `id` is not globally unique within the tenant. `details.duplicateId` is included. | Use a globally unique message ID for each message. |
| `OUTPUTPART_INLINE_REF_CONFLICT` | 400 | An `OutputPart` has both `inline` and `ref` fields set, which are mutually exclusive. | Set exactly one: `inline` for direct embedding or `ref` for external blob reference. |
| `INVALID_DELIVERY_VALUE` | 400 | A message delivery envelope contains an unrecognized `delivery` field value. | Use `queued` or `immediate` as the delivery value. |
| `SDK_DEMOTION_NOT_SUPPORTED` | 422 | Session creation failed because the pool uses SDK-warm mode but the adapter does not implement `DemoteSDK`. | Implement `DemoteSDK` in the runtime adapter before declaring `preConnect: true`. |
| `REGION_CONSTRAINT_UNRESOLVABLE` | 422 | No storage or pool configuration can satisfy the requested `dataResidencyRegion`. `details.region` is included. | Configure storage or pools in the required region, or use a different region. |
| `KMS_REGION_UNRESOLVABLE` | 422 | No KMS key is configured for the required region. `details.region` and `details.provider` are included. | Configure a KMS key in the required region. |
| `ENV_VAR_BLOCKLISTED` | 400 | One or more requested environment variables are on the platform blocklist. `details.blocklisted` lists the offending names. | Remove the blocklisted environment variable names from the request. |
| `UPLOAD_TOKEN_EXPIRED` | 401 | The upload token's TTL has elapsed. | Create a new session to obtain a fresh upload token. |
| `UPLOAD_TOKEN_MISMATCH` | 403 | The upload token's embedded `session_id` does not match the target session. | Use the upload token that was issued for this specific session. |
| `UPLOAD_TOKEN_CONSUMED` | 410 | The upload token has already been invalidated by a successful `FinalizeWorkspace` call. | Upload tokens are single-use. No further uploads are permitted after finalization. For mid-session uploads, use the session bearer credential. |
| `ELICITATION_NOT_FOUND` | 404 | The elicitation ID does not match any pending elicitation for this session and user. | Verify the `elicitation_id` and `session_id`. The elicitation may have expired or been dismissed. |
| `ELICITATION_CONTENT_TAMPERED` | 409 | Returned only when the tenant's **effective** elicitation content integrity enforcement mode is `enforce` (see `PUT /v1/admin/tenants/{id}/elicitation-content-integrity`). An intermediate pod re-emitted an MCP `elicitation/create` wire frame for an existing `elicitation_id` with a `{message, schema}` pair diverging from the gateway-recorded original. Per the gateway-origin-binding invariant, the gateway is the authoritative source for elicitation display text; forwarding hops may not rewrite it. `details.elicitationId`, `details.originPod`, `details.tamperingPod`, and `details.enforcementMode` (`enforce`) are included. The forward is dropped before the modified text reaches the client, and `lenny_elicitation_content_tamper_detected_total{enforcement_mode="enforce"}` fires `ElicitationContentTamperDetected`. Under effective mode `detect-only` the same divergence is recorded (audit event emitted, counter incremented under the `detect-only` label, `ElicitationContentIntegrityPermissiveTamper` warning alert fires) but this error is NOT returned. Under effective mode `off` no check runs. | To present transformed text (translation, rephrasing) to a different audience, emit a new `lenny/request_elicitation` establishing a fresh `elicitation_id`; do not rewrite an existing one. Investigate the `tampering_pod` for prompt-injection compromise. |
| `ELICITATION_INTEGRITY_JUSTIFICATION_REQUIRED` | 400 | `PUT /v1/admin/tenants/{id}/elicitation-content-integrity` rejected because the request body sets `mode` to `detect-only` or `off` without a non-empty `justification` string. The justification is mandatory whenever the stored mode is weaker than `enforce` so the operator identity and reason for weakening are captured in the audit trail. | Resubmit the request with a non-empty `justification` describing why the tenant's enforcement mode is being weakened. |
| `ELICITATION_INTEGRITY_BELOW_PLATFORM_FLOOR` | 400 | `PUT /v1/admin/tenants/{id}/elicitation-content-integrity` rejected because the requested stored `mode` is strictly weaker than the platform-configured minimum-enforcement floor (`.Values.security.elicitationContentIntegrity.floor`). Ordering: `off < detect-only < enforce`. `details.storedMode` (requested) and `details.platformFloor` (deployed) are included. | Either raise the requested `mode` to at least the platform floor, or have the platform operator lower the floor via Helm (audited as `platform.elicitation_content_integrity_floor_changed`). |
| `IDEMPOTENCY_KEY_REUSED` | 422 | An idempotency key was reused with a different request body. | Each idempotency key must correspond to a single unique request. Use a new key for a different request. |
| `COMPLIANCE_SIEM_REQUIRED` | 422 | Tenant creation/update rejected because its `complianceProfile` requires a SIEM endpoint. | Configure `audit.siem.endpoint` before creating the regulated tenant. |
| `COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED` | 422 | `PUT /v1/admin/tenants/{id}` rejected because the request would lower `complianceProfile` below its current value. The ratchet is strictly one-way (`none < soc2 < fedramp < hipaa`). `details.currentProfile` and `details.requestedProfile` identify both sides. | Use `POST /v1/admin/tenants/{id}/compliance-profile/decommission` (`platform-admin`, with `acknowledgeDataRemediation: true` and a required `justification`) for legitimate wind-down flows. |
| `URL_MODE_ELICITATION_DOMAIN_REQUIRED` | 400 | Pool registration/update rejected because `urlModeElicitation.enabled: true` was set without a non-empty `domainAllowlist`. | Provide a non-empty `domainAllowlist` when enabling URL-mode elicitation. |
| `INPUT_TOO_LARGE` | 413 | Delegation rejected because `TaskSpec.input` exceeds `contentPolicy.maxInputSize`. `details.sizeBytes` and `details.limitBytes` are included. | Reduce the input size below the configured limit. |
| `EXPORT_SCAN_REQUIRES_INTERCEPTOR` | 400 | `POST`/`PUT /v1/admin/delegation-policies` rejected because `contentPolicy.scanExportedFiles: true` was set without a backing `contentPolicy.interceptorRef`. There is no platform-default scan pipeline — `scanExportedFiles` requires a named interceptor to route per-file `PreExportMaterialization` calls to. | Set `interceptorRef` to a registered `RequestInterceptor` name, or clear `scanExportedFiles` to `false`. |
| `EXPORT_FILE_SCAN_SIZE_EXCEEDED` | 413 | Delegation rejected at file-export materialization because an exported file exceeds `contentPolicy.maxExportedFileSize` under `scanExportedFiles: true`. `details.filePath`, `details.sizeBytes`, and `details.limitBytes` are included. Distinct from `fileExportLimits.maxTotalSize` / `maxFiles` violations which surface earlier in the export validation. | Reduce the file size, narrow the `fileExport` selector to exclude the oversized file, or (on the admin side) increase the policy's `maxExportedFileSize`. |
| `LLM_REQUEST_REJECTED` | 403 | LLM request rejected by `PreLLMRequest` interceptor. `details.reason` contains the rejection reason. Proxy mode only. | Review the content against the interceptor's policy. Modify the request to comply. |
| `LLM_RESPONSE_REJECTED` | 502 | LLM response rejected by `PostLLMResponse` interceptor. `details.reason` contains the rejection reason. Proxy mode only. | The LLM response was filtered by policy. Consider adjusting the prompt or interceptor configuration. |
| `CONNECTOR_REQUEST_REJECTED` | 403 | Connector tool call rejected by `PreConnectorRequest` interceptor. `details.reason` is included. | Review the connector request against the interceptor's policy. |
| `CONNECTOR_RESPONSE_REJECTED` | 502 | Connector response rejected by `PostConnectorResponse` interceptor. `details.reason` is included. | Contact the operator if this is unexpected. |
| `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` | 422 | ArtifactStore replication runtime residency preflight observed a jurisdiction-tag mismatch, missing tag, DNS rebinding outside the allowlisted CIDRs, or a failed destination tag-probe. Replication for the affected region is suspended and does not auto-resume. | Fix the jurisdiction mismatch and invoke `POST /v1/admin/artifact-replication/{region}/resume` (requires `platform-admin`). |
| `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` | 422 | Phase 3.5 of the tenant force-delete lifecycle aborted because the resolved target escrow region — derived from the tenant's `dataResidencyRegion` (or the deployment's single-region default when unset) — has no corresponding `storage.regions.<region>.legalHoldEscrow.{endpoint, bucket, kmsKeyId, escrowKekId}` entry, or that region's escrow KMS key is unreachable, or its bucket endpoint is unreachable. Fail-closed mirror of `BACKUP_REGION_UNRESOLVABLE` / `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` for the legal-hold escrow surface (CMP-054). Raises the `LegalHoldEscrowResidencyViolation` critical alert. `details.tenantId`, `details.resolvedRegion`, and `details.failureMode` are included. | Configure the missing `storage.regions.<region>.legalHoldEscrow` entry (or restore endpoint/KMS reachability) and re-invoke force-delete. |
| `PLATFORM_AUDIT_REGION_UNRESOLVABLE` | 422 | A platform-tenant audit event referencing a non-platform `target_tenant_id` (e.g., `security.audit_write_rejected`, `admin.impersonation_started`/`_ended`, `gdpr.legal_hold_overridden_tenant`, `legal_hold.escrow_region_resolved`, `legal_hold.escrowed`, `legal_hold.escrow_released`, `compliance.profile_decommissioned`) failed to commit because the target tenant's `dataResidencyRegion` has no corresponding `storage.regions.<region>.postgresEndpoint` entry or that region's platform-Postgres is unreachable. Fail-closed mirror of `BACKUP_REGION_UNRESOLVABLE` / `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` for the platform-tenant audit-write surface (CMP-058) — the *fact* of a platform-tenant event describing a regulated tenant is itself personal data describing that tenant and must reside in its jurisdiction. The originating operation halts (impersonation issuance, Phase 3.5 escrow ledger write, compliance decommission). Raises the `PlatformAuditResidencyViolation` critical alert. `details.targetTenantId`, `details.resolvedRegion`, `details.eventType`, and `details.failureMode` (`missing_entry` / `postgres_unreachable`) are included. | Configure the missing `storage.regions.<region>.postgresEndpoint` entry (or restore platform-Postgres reachability) and retry the originating operation. |
| `LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL` | 400 | Playground bearer-mint endpoint rejected the admission material because it does not match the configured `playground.authMode`: `oidc` mode rejects `Authorization: Bearer`; `apiKey` mode rejects the `lenny_playground_session` cookie. `details.configuredAuthMode` and `details.presentedMaterial` are included. | Present the correct material for the configured mode: cookie for `oidc`, bearer for `apiKey`. |
| `LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED` | 503 | `/playground/*` request rejected in `authMode=dev` because the configured `playground.devTenantId` is not yet present in Postgres (the `lenny-bootstrap` Job has not completed). Non-playground routes are unaffected. `Retry-After: 5` is set. `details.devTenantId` echoes the configured value. | Wait for the `lenny-bootstrap` Job to complete; the 503 self-heals once the tenant row commits. |
| `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` | 422 | `pods/ephemeralcontainers` subresource request rejected by the `lenny-ephemeral-container-cred-guard` ValidatingAdmissionWebhook because the proposed ephemeral container would reach through the agent pod's credential-file read boundary (four-condition enforcement per SPEC §13.1). `details.reason` carries a sub-code, one per rejected condition branch: conditions (i)–(iii) on the `securityContext` UID/GID surface — `runAsUser_equals_adapter_uid`, `runAsUser_equals_agent_uid`, `cred_readers_gid_in_supplementalGroups`, `cred_readers_gid_in_runAsGroup`, `runAsUser_absent`, `runAsGroup_absent`, `supplementalGroups_absent`; condition (iv) on the `volumeMounts` surface — `credential_volume_mounted` (the `volumeMount` `name` matches the pod-level credential tmpfs volume) or `run_lenny_path_mounted` (the `volumeMount` `mountPath` equals `/run/lenny` or begins with `/run/lenny/`). `details.targetPod` identifies the target pod. | Resubmit the ephemeral container with `securityContext.runAsUser`/`runAsGroup`/`supplementalGroups` explicitly set to values outside the adapter UID, agent UID, and the `lenny-cred-readers` GID, AND with `volumeMounts` containing no entry referencing the pod-level credential tmpfs volume by name and no entry whose `mountPath` equals `/run/lenny` or begins with `/run/lenny/` (four-condition rationale: SPEC §13.1). |

---

## TRANSIENT errors

Errors caused by temporary infrastructure conditions. Clients should retry with exponential backoff.

| Code | HTTP | Description | Recommended client action |
|:-----|:-----|:------------|:--------------------------|
| `RUNTIME_UNAVAILABLE` | 503 | No healthy pods available for the requested runtime. | Retry with exponential backoff. The warm pool may be replenishing. |
| `POD_CRASH` | 502 | The session pod terminated unexpectedly. | If `retryable: true`, the platform will attempt automatic recovery. Otherwise, check session status and consider starting a new session. |
| `TIMEOUT` | 504 | Operation timed out. | Retry with backoff. If persistent, check the health of dependent services. |
| `INTERNAL_ERROR` | 500 | Unexpected server error. | Retry once. If persistent, report the issue with the `trace_id` from the response. |
| `WARM_POOL_EXHAUSTED` | 503 | No idle pods are available in the warm pool after exhausting both the API-server claim path and the Postgres fallback. | Retry with exponential backoff. The pool controller will provision replacement pods. |
| `INTERCEPTOR_TIMEOUT` | 503 | An external interceptor did not respond within its configured timeout. `details.interceptor_ref`, `details.phase`, and `details.timeout_ms` are included. | Retry. Returned only when `failPolicy: fail-closed`; suppressed when `fail-open`. |
| `EXPORT_FILE_SCAN_UNAVAILABLE` | 503 | Delegation rejected because a `PreExportMaterialization` interceptor call timed out or returned a gRPC error under a `fail-closed` `failPolicy`. `details.filePath`, `details.interceptorRef`, and `details.reason` (one of `timeout`, `grpc_error`, `unreachable`) are included. Under `fail-open` policy the same conditions admit the file and emit a `delegation.export_scan_failed_open` audit event instead. | Retry with exponential backoff. If persistent, investigate the interceptor's health. |
| `POOL_DRAINING` | 503 | Session creation rejected because the target pool is in `draining` state. `Retry-After` header indicates estimated drain completion. `details.pool` and `details.estimatedDrainSeconds` are included. | Wait for the `Retry-After` duration and retry. The pool may be undergoing a rolling upgrade. |
| `CREDENTIAL_RENEWAL_FAILED` | 503 | All credential renewal retries were exhausted. The session is entering the credential fallback flow. `details.provider` identifies the affected provider. | The platform handles fallback automatically. Monitor the session for completion or further errors. |
| `BUDGET_STATE_UNRECOVERABLE` | 503 | A delegation tree's budget state could not be reconstructed after Redis recovery. The root session is moved to `awaiting_client_action`. | Check session state. The client may need to resume or start a new session. |
| `REQUEST_INPUT_TIMEOUT` | 504 | A `lenny/request_input` call blocked longer than `maxRequestInputWaitSeconds`. `details.requestId` and `details.timeoutSeconds` are included. | Respond to input requests more quickly, or increase the timeout configuration. |
| `DERIVE_SNAPSHOT_UNAVAILABLE` | 503 | The referenced workspace snapshot object was not found in object storage. `details.snapshotRef` is included. | Wait and retry, or derive from a different source session state. |
| `REGION_UNAVAILABLE` | 503 | The storage region required by the session's data residency constraint is temporarily unavailable. `details.region` is included. | Retry when the region recovers. |
| `TARGET_NOT_READY` | 409 | Inter-session message rejected because the target session is in a pre-running state (`created`, `ready`, `starting`, `finalizing`). | Retry after the target session transitions to `running`. |
| `DEADLOCK_TIMEOUT` | 504 | A delegated subtree deadlock was not resolved within `maxDeadlockWaitSeconds`. The deepest blocked tasks have been failed. | The root task may retry after the deadlock is broken. |

---

## POLICY errors

Errors caused by policy enforcement. Typically require configuration changes, quota adjustments, or permission grants.

| Code | HTTP | Description | Recommended client action |
|:-----|:-----|:------------|:--------------------------|
| `FORBIDDEN` | 403 | Authenticated but not authorized for this operation. | Verify that the caller's role grants access to the requested operation. |
| `PERMISSION_DENIED` | 403 | The authenticated identity lacks the required permission for this specific resource or operation. Distinguished from `FORBIDDEN` in that this is evaluated at the resource level (delegation scope, policy rule). | Check resource-level permissions and policy configuration. |
| `QUOTA_EXCEEDED` | 429 | Tenant or user quota exceeded. | Wait for the quota window to reset or request a quota increase from the platform operator. |
| `RATE_LIMITED` | 429 | Request rate limit exceeded. | Wait for the `Retry-After` duration before retrying. |
| `CREDENTIAL_POOL_EXHAUSTED` | 503 | No available credentials in the assigned pool. All credentials may be exhausted, in cooldown, or revoked. | Retry after a cooldown period. Contact the platform operator if persistent -- more credentials may need to be added to the pool. |
| `CREDENTIAL_REVOKED` | 403 | The credential backing the active session lease has been explicitly revoked. Active sessions using this credential are terminated immediately. | The session is terminated. Start a new session -- a different credential will be assigned. |
| `INJECTION_REJECTED` | 403 | Message injection rejected because the runtime has `injection.supported: false`. | This runtime does not accept injected messages. Use a different runtime or modify the runtime configuration. |
| `SCOPE_DENIED` | 403 | Inter-session message rejected because the sender's effective `messagingScope` does not permit messaging the target session. | Ensure the sender and target are within the allowed messaging scope (`direct` or `siblings`). |
| `ISOLATION_MONOTONICITY_VIOLATED` | 422 | Request rejected because the target pool's `sessionIsolationLevel.isolationProfile` is weaker than the source (parent or derive/replay source) session's. Applies to `delegate_task`, `POST /v1/sessions/{id}/derive`, and `POST /v1/sessions/{id}/replay` with `replayMode: workspace_derive`. `details.sourceIsolationProfile`, `details.targetIsolationProfile`, and `details.targetPool` are included. Overridable by `platform-admin` callers via `allowIsolationDowngrade: true` (derive/replay only). | Delegate/derive to a pool with an equal or more restrictive isolation profile, or hold `platform-admin` to override with `allowIsolationDowngrade`. |
| `CREDENTIAL_PROVIDER_MISMATCH` | 422 | Cross-environment delegation with `credentialPropagation: inherit` rejected because provider sets have no intersection. | Use `credentialPropagation: independent` for cross-environment delegations with different providers. |
| `CIRCUIT_BREAKER_OPEN` | 503 | Session creation or delegation rejected because an operator-declared circuit breaker is active. `details.circuit_name`, `details.reason`, and `details.opened_at` are included. | Wait for the operator to close the circuit breaker. Do not retry automatically. |
| `INVALID_BREAKER_SCOPE` | 422 | `POST /v1/admin/circuit-breakers/{name}/open` rejected because the body's `limit_tier` / `scope` pair is absent, outside its closed vocabulary, mismatched (key doesn't match selected tier), references an `operation_type` outside the closed set (`uploads` \| `delegation_depth` \| `session_creation` \| `message_injection`), or attempts to change the persisted scope of an existing breaker (scope is immutable). `details.field` and `details.reason` are included. | Supply a well-formed `limit_tier` and matching `scope` object in the request body. To change scope on an existing breaker, close it and open a new breaker under a distinct `{name}`. |
| `EVAL_QUOTA_EXCEEDED` | 429 | The per-session `EvalResult` storage cap has been reached. `details.sessionId` and `details.limit` are included. | Reduce eval submission frequency or request a higher `maxEvalsPerSession` from the operator. |
| `STORAGE_QUOTA_EXCEEDED` | 429 | Tenant artifact storage quota would be exceeded. `details.currentBytes` and `details.limitBytes` are included. | Clean up old session artifacts or request a storage quota increase. |
| `ERASURE_IN_PROGRESS` | 403 | Session creation rejected because the target `user_id` has a pending GDPR erasure job. `details.userId` and `details.jobId` are included. | Wait for the erasure job to complete before creating sessions for this user. |
| `CONTENT_POLICY_WEAKENING` | 422 | Delegation rejected because the child lease's `contentPolicy` weakens the parent's. Raised when the child sets `contentPolicy.interceptorRef: null` over a non-null parent, transitions `contentPolicy.scanExportedFiles` from `true` to `false`, or sets `contentPolicy.maxExportedFileSize` larger than the parent's. `details.field` identifies the weakened axis; `details.parentValue` and `details.childValue` are included. | Retain (or tighten) each `contentPolicy` axis in the child lease — never remove an interceptor, never clear `scanExportedFiles`, and never raise `maxExportedFileSize` above the parent's value. |
| `CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION` | 422 | Delegation rejected because the child lease names a different `contentPolicy.interceptorRef` than the parent without retaining the parent's reference. | Use the same interceptor reference as the parent, or configure chaining. |
| `EXPORT_FILE_SCAN_REJECTED` | 422 | Delegation rejected because the `contentPolicy.interceptorRef` returned `REJECT` on at least one exported file at the `PreExportMaterialization` phase. `details.filePath` identifies the first rejected file; `details.interceptorRef` names the interceptor; `details.reason` is the interceptor-provided human-readable reason (may be empty). No partial materialization — the whole delegation is rolled back atomically. | Review the rejected file against the interceptor's policy. Either remove the file from the export set or amend its content. |
| `DELEGATION_POLICY_WEAKENING` | 422 | Delegation rejected because the child lease's `maxDelegationPolicy` would expand the effective delegation authority beyond the parent's. A child's `maxDelegationPolicy` must be at least as restrictive as the parent's. `details.parentPolicy` and `details.childPolicy` are included. | Narrow the child's `maxDelegationPolicy` to be no broader than the parent's. |
| `TREE_VISIBILITY_WEAKENING` | 422 | Delegation rejected because the child lease's `treeVisibility` would widen the visibility boundary beyond the parent's (`full → parent-and-self → self-only` is strict). `details.parentTreeVisibility` and `details.childTreeVisibility` are included. | Narrow the child's `treeVisibility` to be no broader than the parent's. |
| `VARIANT_ISOLATION_UNAVAILABLE` | 422 | Session creation rejected because the `ExperimentRouter`'s isolation monotonicity check found the active experiment's variant pool's isolation profile weaker than the session's `minIsolationProfile`; the router fails closed rather than falling through to the control bucket. `details.experimentId`, `details.variantId`, `details.sessionMinIsolation`, and `details.variantPoolIsolation` are included. | Relax the session's `minIsolationProfile` or ask the operator to re-provision the variant pool at a compatible isolation profile. |
| `ERASURE_BLOCKED_BY_LEGAL_HOLD` | 409 | `POST /v1/admin/users/{user_id}/erase` rejected by the DeleteByUser legal-hold preflight. One or more active legal holds are scoped to the target user's data. `details.userId`, `details.holdCount`, and `details.holds` are included. | Release the blocking holds or re-invoke erasure with `{"acknowledgeHoldOverride": true, "justification": "<text>"}` (requires `platform-admin`). |
| `TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD` | 409 | Tenant deletion rejected by the Phase 3.5 legal-hold segregation gate. One or more active legal holds are scoped to the tenant's data. `details.tenantId`, `details.holdCount`, and `details.holds` are included. | Release the blocking holds or re-invoke force-delete with `{"acknowledgeHoldOverride": true, "justification": "<text>"}` (requires `platform-admin`). |
| `DELEGATION_PARENT_REVOKED` | 403 | `lenny/delegate_task` rejected because the parent session's token was rotated or revoked between the call and the internal child-token exchange. No child token is issued and no child pod is allocated. `details.parentSessionId` and `details.revocationReason` (`token_rotated`, `recursive_revocation`) are included. | Re-authenticate or accept that the parent session has been terminated. |
| `DOMAIN_NOT_ALLOWLISTED` | 403 | URL-mode elicitation dropped because the URL's host does not match the pool's `urlModeElicitation.domainAllowlist`. `details.host` and `details.allowlist` are included. | Add the domain to the pool's allowlist, or use an allowed domain. |
| `BUDGET_EXHAUSTED` | 429 | Delegation or lease extension rejected because the remaining token budget, tree-size budget, or tree-memory budget is insufficient. `details.limitType` indicates which resource is exhausted. | Request a budget extension from the parent, or reduce resource consumption. |
| `CROSS_TENANT_MESSAGE_DENIED` | 403 | Inter-session message rejected because sender and target belong to different tenants. | Cross-tenant messaging is unconditionally prohibited. Ensure both sessions belong to the same tenant. |
| `DERIVE_LOCK_CONTENTION` | 429 | Too many concurrent derive operations are in progress for this session. | Retry with exponential backoff. |
| `REGION_CONSTRAINT_VIOLATED` | 403 | The resolved storage region does not satisfy the session's `dataResidencyRegion` constraint. `details.requiredRegion` and `details.resolvedRegion` are included. | Ensure the deployment has storage configured in the required region. |
| `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` | 400 | An external interceptor returned `MODIFY` with changes to immutable fields. `details.interceptor_ref`, `details.phase`, and `details.violated_fields` are included. | Fix the interceptor to not modify immutable fields (`user_id`, `tenant_id`). |
| `LEASE_SPIFFE_MISMATCH` | 403 | A pod presented a cryptographic identity that does not match the one recorded on its credential lease. The lease is invalidated. | This indicates a security violation. Investigate the pod identity mismatch. |
| `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED` | 400 | Pool configuration rejected because `cacheScope: tenant` is prohibited under the active compliance profile. | Use `cacheScope: per-user` (the default) for regulated pools. |
| `USER_CREDENTIAL_NOT_FOUND` | 404 | No pre-registered credential found for user and provider. | Register a credential via `POST /v1/credentials` or configure pool fallback. |

---

## UPSTREAM errors

Errors caused by external dependencies outside Lenny's control.

| Code | HTTP | Description | Recommended client action |
|:-----|:-----|:------------|:--------------------------|
| `UPSTREAM_ERROR` | 502 | An external dependency (MCP tool, auth provider) returned an error. | Retry if transient. Check the upstream service status. |

---

## WARNING codes (non-HTTP)

Codes emitted as annotations on responses rather than HTTP error responses.

| Code | Description |
|:-----|:------------|
| `UNREGISTERED_PART_TYPE` | An `OutputPart` carries an unprefixed `type` not present in the platform-defined registry. The part is passed through with a custom-type-to-`text` fallback and a warning annotation. Third-party types should use the `x-<vendor>/` namespace prefix. |

---

## Validation error format

When `code` is `VALIDATION_ERROR`, the `details` field contains a `fields` array:

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "category": "PERMANENT",
    "message": "Request validation failed.",
    "retryable": false,
    "details": {
      "fields": [
        {
          "field": "runtime",
          "message": "must not be empty",
          "rule": "required"
        },
        {
          "field": "workspace.maxSizeMB",
          "message": "must be between 1 and 10240",
          "rule": "range",
          "params": { "min": 1, "max": 10240 }
        }
      ]
    }
  }
}
```

Each entry contains:

| Field | Type | Description |
|:------|:-----|:------------|
| `field` | string | JSON path to the invalid field. |
| `message` | string | Human-readable description of the failure. |
| `rule` | string | Validation rule that failed (`required`, `range`, `pattern`, `enum`, `cursor_expired`). |
| `params` | object (optional) | Rule-specific parameters (e.g., `min`/`max` for range). |

---

## Example responses by category

### TRANSIENT example

```json
{
  "error": {
    "code": "WARM_POOL_EXHAUSTED",
    "category": "TRANSIENT",
    "message": "No idle pods available in pool claude-worker-standard after exhausting all claim paths.",
    "retryable": true,
    "details": {}
  }
}
```

**Headers:** `Retry-After: 5`

### PERMANENT example

```json
{
  "error": {
    "code": "INVALID_STATE_TRANSITION",
    "category": "PERMANENT",
    "message": "Cannot start a session that is already running.",
    "retryable": false,
    "details": {
      "currentState": "running",
      "allowedStates": ["created", "ready"]
    }
  }
}
```

### POLICY example

```json
{
  "error": {
    "code": "QUOTA_EXCEEDED",
    "category": "POLICY",
    "message": "Tenant t1 has exceeded its monthly session quota (limit: 500).",
    "retryable": false,
    "details": {}
  }
}
```

### UPSTREAM example

```json
{
  "error": {
    "code": "UPSTREAM_ERROR",
    "category": "UPSTREAM",
    "message": "External MCP tool 'github-mcp' returned HTTP 500.",
    "retryable": true,
    "details": {
      "upstream": "github-mcp",
      "upstreamStatus": 500
    }
  }
}
```

---

## Rate-limit headers

All REST API responses include rate-limit headers:

| Header | Description |
|:-------|:------------|
| `X-RateLimit-Limit` | Maximum requests permitted in the current window. |
| `X-RateLimit-Remaining` | Requests remaining in the current window. |
| `X-RateLimit-Reset` | UTC epoch seconds when the current window resets. |
| `Retry-After` | Seconds to wait before retrying (present on `429` and `503` responses). |
