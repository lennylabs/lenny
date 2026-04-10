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
| `IDEMPOTENCY_KEY_REUSED` | 422 | An idempotency key was reused with a different request body. | Each idempotency key must correspond to a single unique request. Use a new key for a different request. |
| `COMPLIANCE_SIEM_REQUIRED` | 422 | Tenant creation/update rejected because its `complianceProfile` requires a SIEM endpoint. | Configure `audit.siem.endpoint` before creating the regulated tenant. |
| `URL_MODE_ELICITATION_DOMAIN_REQUIRED` | 400 | Pool registration/update rejected because `urlModeElicitation.enabled: true` was set without a non-empty `domainAllowlist`. | Provide a non-empty `domainAllowlist` when enabling URL-mode elicitation. |
| `INPUT_TOO_LARGE` | 413 | Delegation rejected because `TaskSpec.input` exceeds `contentPolicy.maxInputSize`. `details.sizeBytes` and `details.limitBytes` are included. | Reduce the input size below the configured limit. |

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
| `ISOLATION_MONOTONICITY_VIOLATED` | 403 | Delegation rejected because the target pool's isolation profile is less restrictive than the calling session's `minIsolationProfile`. `details.parentIsolation` and `details.targetIsolation` are included. | Delegate to a pool with an equal or more restrictive isolation profile. |
| `CREDENTIAL_PROVIDER_MISMATCH` | 422 | Cross-environment delegation with `credentialPropagation: inherit` rejected because provider sets have no intersection. | Use `credentialPropagation: independent` for cross-environment delegations with different providers. |
| `CIRCUIT_BREAKER_OPEN` | 503 | Session creation or delegation rejected because an operator-declared circuit breaker is active. `details.circuit_name`, `details.reason`, and `details.opened_at` are included. | Wait for the operator to close the circuit breaker. Do not retry automatically. |
| `EVAL_QUOTA_EXCEEDED` | 429 | The per-session `EvalResult` storage cap has been reached. `details.sessionId` and `details.limit` are included. | Reduce eval submission frequency or request a higher `maxEvalsPerSession` from the operator. |
| `STORAGE_QUOTA_EXCEEDED` | 429 | Tenant artifact storage quota would be exceeded. `details.currentBytes` and `details.limitBytes` are included. | Clean up old session artifacts or request a storage quota increase. |
| `ERASURE_IN_PROGRESS` | 403 | Session creation rejected because the target `user_id` has a pending GDPR erasure job. `details.userId` and `details.jobId` are included. | Wait for the erasure job to complete before creating sessions for this user. |
| `LLM_REQUEST_REJECTED` | 403 | LLM request rejected by `PreLLMRequest` interceptor. `details.reason` contains the rejection reason. Proxy mode only. | Review the content against the interceptor's policy. Modify the request to comply. |
| `LLM_RESPONSE_REJECTED` | 502 | LLM response rejected by `PostLLMResponse` interceptor. `details.reason` contains the rejection reason. Proxy mode only. | The LLM response was filtered by policy. Consider adjusting the prompt or interceptor configuration. |
| `CONNECTOR_REQUEST_REJECTED` | 403 | Connector tool call rejected by `PreConnectorRequest` interceptor. `details.reason` is included. | Review the connector request against the interceptor's policy. |
| `CONNECTOR_RESPONSE_REJECTED` | 502 | Connector response rejected by `PostConnectorResponse` interceptor. `details.reason` is included. | Contact the operator if this is unexpected. |
| `CONTENT_POLICY_WEAKENING` | 403 | Delegation rejected because the child lease removes a content policy interceptor that the parent had. | Retain the parent's `contentPolicy.interceptorRef` in the child lease. |
| `CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION` | 403 | Delegation rejected because the child lease names a different `contentPolicy.interceptorRef` than the parent without retaining the parent's reference. | Use the same interceptor reference as the parent, or configure chaining. |
| `DOMAIN_NOT_ALLOWLISTED` | 403 | URL-mode elicitation dropped because the URL's host does not match the pool's `urlModeElicitation.domainAllowlist`. `details.host` and `details.allowlist` are included. | Add the domain to the pool's allowlist, or use an allowed domain. |
| `BUDGET_EXHAUSTED` | 429 | Delegation or lease extension rejected because the remaining token budget, tree-size budget, or tree-memory budget is insufficient. `details.limitType` indicates which resource is exhausted. | Request a budget extension from the parent, or reduce resource consumption. |
| `CROSS_TENANT_MESSAGE_DENIED` | 403 | Inter-session message rejected because sender and target belong to different tenants. | Cross-tenant messaging is unconditionally prohibited. Ensure both sessions belong to the same tenant. |
| `DERIVE_LOCK_CONTENTION` | 429 | Too many concurrent derive operations are in progress for this session. | Retry with exponential backoff. |
| `REGION_CONSTRAINT_VIOLATED` | 403 | The resolved storage region does not satisfy the session's `dataResidencyRegion` constraint. `details.requiredRegion` and `details.resolvedRegion` are included. | Ensure the deployment has storage configured in the required region. |
| `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` | 400 | An external interceptor returned `MODIFY` with changes to immutable fields. `details.interceptor_ref`, `details.phase`, and `details.violated_fields` are included. | Fix the interceptor to not modify immutable fields (`user_id`, `tenant_id`). |
| `LEASE_SPIFFE_MISMATCH` | 403 | A pod presented a SPIFFE identity that does not match the credential lease's expected identity. The lease is invalidated. | This indicates a security violation. Investigate the pod identity mismatch. |
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
