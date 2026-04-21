---
layout: default
title: "Delegation"
parent: "Runtime Author Guide"
nav_order: 7
---

# Delegation

Recursive delegation is implemented by the gateway. Your runtime decides whether and how to use it. This page covers the delegation model in detail: how to spawn child tasks, manage budgets, handle results, and build reliable orchestration patterns.

---

## How Delegation Works

When your runtime needs another agent to perform a subtask, it calls `lenny/delegate_task` on the platform MCP server. The gateway handles everything: pod allocation, file delivery, credential assignment, and session lifecycle. Your runtime interacts with the child through a gateway-hosted virtual interface.

### The Delegation Flow

```
1. Your runtime calls lenny/delegate_task(target, task, lease_slice?)
2. Gateway validates against your delegation policy and lease
3. Gateway asks your runtime to export files matching the export spec
4. Gateway stores exported files (rebased to child workspace root)
5. Gateway allocates a child pod from the specified pool
6. Gateway streams rebased files into the child before it starts
7. Child starts with its own local workspace
8. Gateway creates a virtual MCP child interface for your runtime
9. Your runtime interacts with the child through this interface
```

**What you see:** Task status/result, elicitation forwarding, cancellation, and message delivery.

**What you never see:** Pod addresses, internal endpoints, or raw credentials.

---

## Target Discovery

Before delegating, discover available targets:

```json
{
  "method": "tools/call",
  "params": {
    "name": "lenny/discover_agents",
    "arguments": {
      "filter": { "labels": { "team": "platform" } }
    }
  }
}
```

Results are scoped by your session's effective delegation policy. You only see targets you are authorized to delegate to. Target IDs are opaque --- you do not know whether a target is a standalone runtime, derived runtime, or external agent.

---

## File Export

When delegating, you specify which workspace files the child should receive:

```json
{
  "task": {
    "input": [{ "type": "text", "inline": "Review this code." }],
    "workspaceFiles": {
      "export": [
        { "glob": "src/auth/**", "destPrefix": "/" },
        { "glob": "config/child-config.json", "destPrefix": "" }
      ]
    }
  }
}
```

### Rebasing Rules

The source glob's base path is stripped, and matched files are placed at the child's workspace root (or under `destPrefix`):

| Parent Workspace | Source Glob | destPrefix | Child Sees |
|-----------------|-------------|------------|------------|
| `./exports/foo.ts` | `./exports/*` | _(none)_ | `./foo.ts` |
| `./exports/lib/bar.ts` | `./exports/*` | _(none)_ | `./lib/bar.ts` |
| `./exports/foo.ts` | `./exports/*` | `input/` | `./input/foo.ts` |
| `./src/auth.ts` | `./src/*` | `project/src/` | `./project/src/auth.ts` |

The child has no visibility into your broader directory structure. You control what slice of your workspace becomes the child's world.

### Export Limits

File exports are bounded by the `fileExportLimits` in your delegation lease:

- `maxFiles`: Maximum number of files (default: 100)
- `maxTotalSize`: Maximum total size (default: 100MB)

For workflows producing large artifacts, deployers can increase these limits per delegation preset.

### Security Note

Exported files are untrusted input from the child's perspective. A compromised parent can include adversarial content. Child runtimes should treat all workspace files received via delegation as untrusted.

---

## Budget Management

### The Delegation Lease

Every delegating session carries a **delegation lease** that defines its quantitative authority:

```json
{
  "maxDepth": 3,
  "maxChildrenTotal": 10,
  "maxParallelChildren": 3,
  "maxTreeSize": 50,
  "maxTokenBudget": 500000,
  "delegationPolicyRef": "orchestrator-policy",
  "minIsolationProfile": "sandboxed",
  "perChildRetryBudget": 1,
  "perChildMaxAge": 3600,
  "fileExportLimits": { "maxFiles": 100, "maxTotalSize": "100MB" },
  "cascadeOnFailure": "cancel_all",
  "credentialPropagation": "independent"
}
```

### LeaseSlice

When calling `lenny/delegate_task`, you can optionally specify a `lease_slice` to control the child's budget:

| Field | Type | Description |
|-------|------|-------------|
| `maxTokenBudget` | int | Token budget for the child tree |
| `maxChildrenTotal` | int | Max children the child may spawn |
| `maxTreeSize` | int | Contribution limit toward the tree-wide pod cap |
| `maxParallelChildren` | int | Max concurrent children for the child |
| `perChildMaxAge` | int | Max wall-clock seconds for the child |

All fields are optional. When omitted, the child receives `min(remaining_parent_budget, default_fraction)`. The default fraction is 50% of remaining budget.

### Atomic Budget Reservation

When you call `lenny/delegate_task`, the gateway atomically:

1. Reads your token budget, usage, tree size, children count, parallel children count, and tree memory.
2. Validates all limits in a single Redis Lua script.
3. Reserves the budget (decrements your available tokens, increments counters).
4. Returns the granted slice.

If any check fails, no operation is applied and the delegation is rejected. There is no TOCTOU window.

### Budget Return

When a child reaches a terminal state, unused budget is returned to your available pool:

- **Unused budget** = child's allocated budget minus child's actual consumption (including all descendants).
- Returns never exceed your original allocation.
- The return happens atomically via Redis.

### Minimum Budget Threshold

If your remaining budget is below a minimum usable threshold (default: 10,000 tokens), delegation is rejected with `BUDGET_EXHAUSTED`.

---

## Delegation Policy

### DelegationPolicy Resources

Delegation targets are controlled by named `DelegationPolicy` resources with tag-based matching:

```yaml
name: orchestrator-policy
rules:
  - target:
      matchLabels:
        team: platform
      types: [agent]
    allow: true
  - target:
      ids: [github, jira]
      types: [connector]
    allow: true
contentPolicy:
  maxInputSize: 131072
  interceptorRef: null
  scanExportedFiles: false
  maxExportedFileSize: 10485760
```

**Effective policy** is the intersection of the runtime-level policy and any derived-runtime policy --- derived policies can only restrict, never expand.

### Content Policy

The `contentPolicy` on `DelegationPolicy` provides prompt injection mitigation:

- `maxInputSize`: Hard byte-size limit on `TaskSpec.input` (default: 128KB). Delegations exceeding it are rejected with `INPUT_TOO_LARGE`.
- `interceptorRef`: Optional reference to a `RequestInterceptor` for content scanning. The interceptor can `ALLOW`, `REJECT`, or `MODIFY` content.
- `scanExportedFiles` (default `false`): When `true`, each exported file transferred from parent to child is additionally routed through `interceptorRef` at the `PreExportMaterialization` phase *before* the file is materialized into the child's workspace. Requires a non-null `interceptorRef` (otherwise the policy is rejected with `EXPORT_SCAN_REQUIRES_INTERCEPTOR`). Lets deployers inspect attacker-controlled `CLAUDE.md`, `AGENTS.md`, or any other file whose content a child runtime will treat as instructions. Runtime-time rejection surfaces as `EXPORT_FILE_SCAN_REJECTED`; interceptor unavailability under `fail-closed` surfaces as `EXPORT_FILE_SCAN_UNAVAILABLE`.
- `maxExportedFileSize` (default 10 MiB): Per-file byte ceiling when `scanExportedFiles` is `true`. Files exceeding this surface as `EXPORT_FILE_SCAN_SIZE_EXCEEDED`.

---

## Isolation Monotonicity

Children must use an isolation profile **at least as restrictive** as their parent:

```
standard (runc) < sandboxed (gVisor) < microvm (Kata)
```

A `sandboxed` parent cannot delegate to a `standard` child. The `minIsolationProfile` field in the lease enforces this.

Violations are rejected with `ISOLATION_MONOTONICITY_VIOLATED` before pod allocation.

---

## Credential Propagation

The `credentialPropagation` field controls how child sessions get LLM provider credentials:

| Mode | Behavior |
|------|----------|
| `inherit` | Child uses the same credential pool as parent |
| `independent` | Child gets its own credential lease based on tenant policy |
| `deny` | Child receives no LLM credentials |

`inherit` mode applies **per-hop** --- each node controls its own children independently.

**Fan-out guidance:** `inherit` is not suitable for high fan-out trees. All descendants sharing `inherit` draw from the same pool. Use `independent` when `maxParallelChildren > pool.maxConcurrentSessions / expected_tree_depth`.

---

## Cycle Detection

The gateway prevents delegation cycles by checking the full runtime lineage. If the target's `(runtime_name, pool_name)` tuple appears anywhere in the caller's lineage, the delegation is rejected with `DELEGATION_CYCLE_DETECTED` by default.

Pool-differentiated cycles (e.g., `A/pool1 -> B -> A/pool2`) are intentionally **not** detected, because deploying the same runtime in different pools is a legitimate pattern. `maxDepth` bounds any such chain.

### Opting into self-recursion

Some agent patterns (planner-recurses-into-self refinement loops, deliberate self-evaluation) require a runtime to delegate to itself. The platform supports this through a three-layer AND gate; admission requires every layer to opt in:

| Layer | Setting | Default | Owner |
|---|---|---|---|
| Platform | Helm `gateway.allowSelfRecursion: yes\|no` | `yes` | Platform operator |
| Runtime | `Runtime.spec.allowSelfRecursion: true\|false` | `false` | Runtime author |
| Policy | `DelegationPolicy.allowSelfRecursion: true\|false` | `false` | Tenant operator |

The gate runs only under Helm `gateway.cycleDetection.mode: enforce` (the default). Under `mode: warn` the hop is admitted regardless of layer state and `delegation.cycle_warning` is audited; under `mode: permissive` no check runs at all (intended for development clusters).

When the gate rejects, the error's `details.blockedBy` field names the first layer (declared order: `platform` → `runtime` → `policy`) whose value evaluated `false`, so you don't have to read three configs to know which knob to flip. `details.effectiveSettings` carries the full resolved tuple.

`maxDepth` is enforced in every mode and can never be disabled. The Helm fallback `gateway.delegation.defaultMaxDepth` (default `10`) applies when no narrower value is set on the lease, preset, runtime default, or policy.

### Inheritance of `allowSelfRecursion` across hops

`DelegationPolicy.allowSelfRecursion` is monotonic across delegation hops: a child lease may narrow `true → false` (tighter posture is always allowed), but cannot widen `false → true`. Attempts to widen are rejected with `DELEGATION_POLICY_WEAKENING` and `details.field: "allowSelfRecursion"`.

---

## Awaiting Results

### Basic Pattern

```go
// Delegate two tasks
handle1 := delegateTask("code-reviewer", task1)
handle2 := delegateTask("test-runner", task2)

// Wait for both to complete
results := awaitChildren([handle1.sessionId, handle2.sessionId], mode="all")

for _, result := range results {
    if result.State == "completed" {
        // Process result.Output
    } else {
        // Handle failure
    }
}
```

### Handling input_required

Children may need input from the parent during execution:

```go
// Open a streaming await
stream := awaitChildren([childId], mode="all")

for event := range stream {
    switch event.State {
    case "input_required":
        // Child is blocked, needs our input
        answer := processQuestion(event.Parts)
        sendMessage(target=event.ChildId, inReplyTo=event.RequestId, parts=answer)

    case "completed":
        // Child finished, process result
        processResult(event.Output)

    case "failed":
        // Child failed, handle error
        handleFailure(event.Error)
    }
}
```

### TaskResult Schema

```json
{
  "schemaVersion": 1,
  "taskId": "child_abc123",
  "state": "completed",
  "output": {
    "parts": [{ "type": "text", "inline": "Review complete. No issues found." }]
  },
  "usage": {
    "inputTokens": 15000,
    "outputTokens": 8000,
    "wallClockSeconds": 120,
    "podMinutes": 2.1
  },
  "treeUsage": {
    "inputTokens": 45000,
    "outputTokens": 22000,
    "wallClockSeconds": 450,
    "totalTasks": 4
  },
  "error": null
}
```

`treeUsage` includes the sum of this task's usage plus all descendants. It is only available after all descendants have settled.

---

## Cascade Policies

The `cascadeOnFailure` policy governs what happens to children when the parent reaches any terminal state (including normal completion):

| Policy | Behavior |
|--------|----------|
| `cancel_all` (default) | Cancel all descendants immediately |
| `await_completion` | Let running children finish (up to `cascadeTimeoutSeconds`), then collect results |
| `detach` | Children become orphaned; results are stored but no parent collects them |

**Important:** Despite the name, `cascadeOnFailure` applies on **all** terminal transitions, not only failure. A parent completing normally after `await_children(mode="any")` will apply the cascade policy to remaining siblings.

To allow children to outlive a completed parent, use `cascadeOnFailure: detach`.

---

## Delegation Presets

Deployers define named presets to simplify configuration:

```yaml
delegationPresets:
  simple:
    maxDepth: 1
    maxChildrenTotal: 3
    maxParallelChildren: 1
    maxTokenBudget: 100000
  standard:
    maxDepth: 3
    maxChildrenTotal: 10
    maxParallelChildren: 3
    maxTokenBudget: 500000
  orchestrator:
    maxDepth: 5
    maxChildrenTotal: 50
    maxParallelChildren: 10
    maxTokenBudget: 2000000
```

Clients reference presets by name: `"delegationLease": "standard"`. Presets can be partially overridden: `"delegationLease": {"preset": "standard", "maxDepth": 2}`.

---

## Lease Extension

When your runtime's token budget is exhausted, the adapter automatically requests an extension from the gateway. This happens transparently --- your runtime sees a slightly slow LLM response, not a failure.

Extension is handled via the adapter-to-gateway gRPC channel, not the platform MCP server. Your runtime never calls it directly.

**Approval modes:**

| Mode | Behavior |
|------|----------|
| `auto` | Gateway grants automatically up to the effective max |
| `elicitation` (default) | User is prompted to approve the extension |

---

## Tree Recovery

If your pod fails while children are running:

1. Children continue running independently.
2. The gateway recovers the tree bottom-up (leaves first).
3. When your session resumes on a new pod:
   - Virtual child interfaces are re-injected.
   - You receive a `children_reattached` event listing current child states.
   - You can continue awaiting, canceling, or interacting with children.
4. Re-issuing `lenny/await_children` replays already-settled results first, then enters live-wait for running children.

### Deadlock Detection

The gateway detects subtree deadlocks where every task is blocked (all in `input_required` or `await_children` waiting on `input_required` children). When detected:

1. The root task receives a `deadlock_detected` event.
2. The event includes blocked `requestId` values and a `willTimeoutAt` timestamp.
3. Break the deadlock by responding to a pending `request_input` or canceling blocked children.
4. If unresolved within `maxDeadlockWaitSeconds` (default: 120), the deepest blocked tasks fail with `DEADLOCK_TIMEOUT`.

---

## Concurrent Delegation Patterns

### Fan-Out / Fan-In

Spawn multiple children, wait for all results:

```go
var handles []string
for _, file := range filesToReview {
    h := delegateTask("reviewer", TaskSpec{
        Input: []OutputPart{{Type: "text", Inline: "Review: " + file}},
        WorkspaceFiles: ExportSpec{Glob: file},
    })
    handles = append(handles, h.SessionId)
}

results := awaitChildren(handles, mode="all")
// Aggregate results from all reviewers
```

### First-Past-the-Post

Spawn multiple children, take the first result, cancel the rest:

```go
result := awaitChildren(handles, mode="any")
// Cancel remaining children
for _, h := range handles {
    if h != result.TaskId {
        cancelChild(h)
    }
}
```

### Pipeline

Chain tasks sequentially, passing output from one to the next:

```go
result1 := delegateAndWait("analyzer", analyzerTask)
result2 := delegateAndWait("fixer", TaskSpec{
    Input: result1.Output.Parts,
})
result3 := delegateAndWait("tester", TaskSpec{
    Input: result2.Output.Parts,
})
```

---

## Error Handling

### Child Failure

When a child fails, the gateway injects a `child_failed` event into your session stream:

```json
{
  "type": "child_failed",
  "child_task_id": "task_xyz",
  "classification": "transient",
  "error": {
    "code": "RUNTIME_CRASH",
    "message": "Agent process exited with code 137"
  },
  "retriesExhausted": true
}
```

Your runtime can:
- Re-spawn a replacement child.
- Continue with partial results.
- Propagate the failure upward.

### Budget Exhaustion

If a child exhausts its token budget, the adapter requests an extension (see Lease Extension above). If the extension is denied or the ceiling is reached, the child fails with `BUDGET_EXHAUSTED`.

### Timeout

Children that exceed `perChildMaxAge` are terminated and transition to `expired`.
