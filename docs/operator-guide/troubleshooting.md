---
layout: default
title: Troubleshooting
parent: "Operator Guide"
nav_order: 10
---

# Troubleshooting

This page covers common operational issues with diagnosis and resolution steps, circuit breaker management, orphan reconciliation, and emergency procedures.

---

## Common Issues

### Warm Pool Exhaustion

**Symptoms:**
- `WarmPoolExhausted` critical alert fires
- Session creation fails with `WARM_POOL_EXHAUSTED`
- `lenny_warmpool_idle_pods` gauge is 0 for the affected pool

**Diagnosis:**

```bash
# Check warm pool status
kubectl get sandboxwarmpools -A

# Check for warmup failures
kubectl logs -n lenny-system -l app=lenny-controller --tail=100 | grep warmup

# Check the warmup failure reason
# Query: rate(lenny_warmpool_warmup_failure_total[5m]) by (reason)
```

**Common causes and resolution:**

| Cause | Indicator | Resolution |
|---|---|---|
| Image pull errors | `reason: image_pull_error` in warmup failures | Verify image registry access, check ImagePullPolicy |
| Setup command failures | `reason: setup_command_failed` | Check runtime setup commands, increase `setupPolicy.timeoutSeconds` |
| Resource quota exceeded | `reason: resource_quota_exceeded` | Increase `agentNamespaces[].resourceQuota.pods` |
| Node pressure | `reason: node_pressure` | Add nodes, check node resource utilization |
| Insufficient minWarm | Low idle count before exhaustion | Increase `minWarm` or review scaling formula |

**Emergency scaling:**

```bash
# Override minWarm for immediate scaling
lenny-ctl admin pools set-warm-count --pool <name> --min 20
```

---

### Session Creation Latency

**Symptoms:**
- `SessionCreationLatencyBurnRate` alert fires
- P99 of `POST /v1/sessions` exceeds 500ms
- Users report slow session starts

**Diagnosis:**

Check the four latency breakpoints:

1. **Pod claim time** -- `lenny_pod_claim_queue_wait_seconds`
2. **Workspace prep** -- upload and materialization time
3. **Session start** -- runtime initialization
4. **First token** -- `lenny_session_time_to_first_token_seconds`

```bash
# Check pod claim queue
# Query: histogram_quantile(0.99, lenny_pod_claim_queue_wait_seconds)

# Check if warm pool is the bottleneck
kubectl get sandboxwarmpools -A -o wide
```

**Resolution:**

| Bottleneck | Action |
|---|---|
| Pod claim queue backed up | Increase `minWarm`, check `PodClaimQueueSaturated` alert |
| Workspace materialization slow | Reduce workspace size, check MinIO latency |
| Runtime startup slow | Profile runtime initialization, check gVisor overhead |
| Certificate provisioning | Check cert-manager health and issuer status |

---

### Gateway Subsystem Circuit Breaker Open

**Symptoms:**
- `GatewaySubsystemCircuitOpen` warning alert fires
- Specific subsystem returning 503 errors
- Other subsystems continue operating normally

**Diagnosis:**

```bash
# Check which subsystem is affected
# Query: lenny_gateway_{subsystem}_circuit_state == 2
```

**Resolution by subsystem:**

| Subsystem | Common Cause | Resolution |
|---|---|---|
| **Stream Proxy** | Reconnection storms, high session churn | Check client reconnect patterns; increase `maxConcurrent` |
| **Upload Handler** | MinIO unavailability, large upload bursts | Check MinIO health; review upload size limits |
| **MCP Fabric** | Deep delegation trees starving goroutines | Check delegation depth limits; review delegation policies |
| **LLM Proxy** | Upstream LLM provider outage | Check provider status; credential pool health |

---

### Checkpoint Failures

**Symptoms:**
- `CheckpointStale` warning alert fires
- `CheckpointStorageUnavailable` critical alert fires
- `lenny_checkpoint_storage_failure_total` incrementing

**Diagnosis:**

```bash
# Check MinIO connectivity
kubectl exec -n lenny-system deploy/lenny-gateway -- \
  wget -qO- http://minio:9000/minio/health/live

# Check checkpoint duration
# Query: histogram_quantile(0.95, lenny_checkpoint_duration_seconds)

# Check workspace size
# Query: lenny_checkpoint_size_exceeded_total
```

**Resolution:**

| Issue | Resolution |
|---|---|
| MinIO unavailable | Restore MinIO; sessions continue without new checkpoints |
| Checkpoint duration too high | Reduce `workspaceSizeLimitBytes`; use `.lennyignore` |
| Workspace size limit exceeded | Review workspace hygiene; increase limit if justified |
| Full-level quiescence timeout | Check runtime `checkpoint_ready` response time |

---

### Credential Pool Exhaustion

**Symptoms:**
- `CredentialPoolExhausted` critical alert fires
- Session creation fails with `CREDENTIAL_POOL_EXHAUSTED`
- All credentials in cooldown or revoked

**Diagnosis:**

```bash
# Check credential pool status
lenny-ctl admin credential-pools get --pool <name>

# Check credential health scores and cooldown state
# Query: lenny_credential_pool_utilization
# Query: lenny_credential_pool_health
```

**Resolution:**

1. Add more credentials to the pool
2. Review cooldown settings -- reduce cooldown duration if rate-limiting is transient
3. Check for revoked credentials that can be re-enabled
4. Review `maxLeases` per credential -- may be too low

```bash
# Add a new credential
lenny-ctl admin credential-pools add-credential \
  --pool anthropic-prod \
  --provider anthropic_direct

# Re-enable a revoked credential after rotation
lenny-ctl admin credential-pools re-enable \
  --pool anthropic-prod \
  --credential key-1 \
  --reason "Key rotated at provider"
```

---

### Token Service Unavailability

**Symptoms:**
- `TokenServiceUnavailable` critical alert fires
- New credential-requiring sessions fail
- Existing sessions continue until lease expiry

**Diagnosis:**

```bash
# Check Token Service pods
kubectl get pods -n lenny-system -l app=lenny-token-service

# Check Token Service logs
kubectl logs -n lenny-system -l app=lenny-token-service --tail=50

# Check circuit breaker state
# Query: lenny_token_service_circuit_state
```

**Resolution:**

1. Check Token Service pod health and restart if necessary
2. Verify KMS connectivity (Token Service requires KMS for decryption)
3. Check ServiceAccount permissions for KMS access
4. Verify mTLS certificates between gateway and Token Service

---

### PoolConfigDrift Alert

**Symptoms:**
- `PoolConfigDrift` warning alert fires
- Pool configuration in Postgres doesn't match CRD state
- `lenny_pool_config_reconciliation_lag_seconds` is high

**Diagnosis:**

```bash
# Check sync status
lenny-ctl admin pools sync-status --pool <name>

# Check PoolScalingController health
kubectl get pods -n lenny-system -l app=lenny-pool-scaling-controller
kubectl logs -n lenny-system -l app=lenny-pool-scaling-controller --tail=50
```

**Resolution:**

1. Check if PoolScalingController leader election has stalled
2. Verify RBAC permissions for CRD updates
3. Check for CRD validation errors in controller logs
4. If stale CRDs, follow the [CRD upgrade procedure](upgrades.html#crd-upgrades)

---

## Circuit Breaker Management

### Operator-Managed Circuit Breakers

Operator-managed circuit breakers are Redis-backed and propagate across all gateway replicas. They provide platform-wide request rejection for infrastructure-wide outages.

```bash
# List all circuit breakers
lenny-ctl admin circuit-breakers list

# Open a circuit breaker (rejects matching admission requests platform-wide).
# --limit-tier + --scope declare what the breaker matches against (see §11.6).
# Scope is immutable across the breaker's lifecycle — to change it, close the
# breaker and open a new one under a different name.
lenny-ctl admin circuit-breakers open <name> \
  --limit-tier runtime \
  --scope runtime=runtime_python_ml \
  --reason "runtime degraded — upstream 5xx"

# operation_type values come from the closed set:
#   uploads | delegation_depth | session_creation | message_injection
lenny-ctl admin circuit-breakers open uploads-paused \
  --limit-tier operation_type \
  --scope operation_type=uploads \
  --reason "storage provider incident INC-123"

# Close a circuit breaker (body is empty; persisted scope is retained across
# open→closed→open cycles for the same name).
lenny-ctl admin circuit-breakers close <name>
```

### Per-Replica Automatic Circuit Breakers

Each gateway subsystem has its own per-replica automatic circuit breaker. These are not shared across replicas -- each replica independently discovers failures.

**Behavior:**
- Clients may see non-deterministic 503s during the convergence window
- All replicas converge to open state within seconds
- Clients should retry on 503; load balancer distributes retries

### SDK-Warm Circuit Breaker

Pools with `preConnect: true` have an SDK-warm circuit breaker that trips when SDK warm startup systematically fails:

```bash
# Override SDK-warm circuit breaker state
lenny-ctl admin pools circuit-breaker --pool <name> --state disabled

# Restore automatic control
lenny-ctl admin pools circuit-breaker --pool <name> --state auto
```

---

## Orphan Reconciliation

### Automatic Reconciliation

The gateway runs a periodic orphan reconciler every 60 seconds that:

1. Detects sessions with no responsive pod
2. Detects pods with no corresponding session
3. Transitions orphaned sessions to `failed` state
4. Cleans up orphaned `SandboxClaim` resources

Monitor via:
- `lenny_orphan_session_reconciliations_total` -- orphaned sessions cleaned up
- `lenny_orphaned_claims_total` -- orphaned claims deleted
- `lenny_orphan_tasks_active` -- active orphan tasks awaiting cleanup

### Manual Investigation

```bash
# Investigate a specific session
lenny-ctl admin sessions get <session-id>

# Force-terminate a stuck session
lenny-ctl admin sessions force-terminate <session-id>
```

### Orphan Task Alerts

| Alert | Condition | Action |
|---|---|---|
| `SandboxClaimOrphanRateHigh` | > 10 orphans in 15 min | Check gateway stability |
| `OrphanTasksPerTenantHigh` | > 80% of `maxOrphanTasksPerTenant` | Check for misbehaving orchestrator |

---

## Emergency Procedures

### Emergency Credential Revocation

When a credential is compromised:

```bash
# Revoke a single credential (terminates all active leases)
lenny-ctl admin credential-pools revoke-credential \
  --pool <pool-name> \
  --credential <credential-id> \
  --reason "Credential compromised -- incident INC-1234"

# Revoke an entire pool
lenny-ctl admin credential-pools revoke-pool \
  --pool <pool-name> \
  --reason "All keys in pool potentially compromised"
```

### Emergency Pool Drain

To immediately stop new sessions on a pool:

```bash
lenny-ctl admin pools drain --pool <pool-name>
```

This:
- Stops new session assignments
- Returns estimated drain completion time
- New requests receive `503 POOL_DRAINING` with `Retry-After`
- Existing sessions complete normally

### Force-Terminate Stuck Sessions

```bash
# Identify stuck sessions
lenny-ctl admin sessions get <session-id>

# Force terminate
lenny-ctl admin sessions force-terminate <session-id>
```

### Quota Reconciliation After Redis Recovery

After a Redis restart or extended outage:

```bash
lenny-ctl admin quota reconcile --all-tenants
```

This re-aggregates in-flight session usage from Postgres into Redis.

### Audit Partition Management

If the SIEM forwarder is stalled and audit partitions are being held:

```bash
# Check held partitions (look for AuditPartitionDropBlocked alerts)

# Force-drop a partition (acknowledges data loss)
lenny-ctl audit drop-partition <partition-name> --force --acknowledge-data-loss
```

---

## Diagnostic Commands Quick Reference

| Task | Command |
|---|---|
| Check system pods | `kubectl get pods -n lenny-system` |
| Check agent pods | `kubectl get pods -n lenny-agents` |
| Check warm pools | `kubectl get sandboxwarmpools -A` |
| Check sandbox status | `kubectl get sandboxes -A` |
| Gateway health | `kubectl exec deploy/lenny-gateway -- wget -qO- http://localhost:8080/healthz` |
| Pool status | `lenny-ctl admin pools get <name>` |
| Pool sync status | `lenny-ctl admin pools sync-status <name>` |
| Session investigation | `lenny-ctl admin sessions get <id>` |
| Credential pool health | `lenny-ctl admin credential-pools get --pool <name>` |
| Migration status | `lenny-ctl migrate status` |
| Run preflight | `lenny-ctl preflight --config values.yaml` |
| Circuit breaker list | `lenny-ctl admin circuit-breakers list` |
