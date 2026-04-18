---
layout: default
title: "Evaluation and Scoring"
parent: Tutorials
nav_order: 10
---

# Evaluation and Scoring

**Persona:** Client Developer + Platform Operator | **Difficulty:** Intermediate

This tutorial covers how to set up evaluation using runtime-native eval platforms and Lenny's built-in `/eval` endpoint, how to use cross-delegation tracing for multi-agent observability, and optionally how to connect evaluation with A/B experimentation.

## Prerequisites

- Lenny running locally via `docker compose up`
- At least one runtime registered (e.g., `claude-worker-v1`)
- Familiarity with [Your First Session](first-session)
- curl and jq installed

---

## Step 1: Runtime-Native Evaluation

Most production runtimes integrate with dedicated eval platforms (LangSmith, Braintrust, Weights & Biases, etc.) for scoring, observability, and prompt iteration. Lenny does not integrate outbound with these platforms; runtimes use their own SDKs and APIs directly.

No Lenny-specific setup is required for runtime-native eval. Runtimes score sessions using their own tooling.

### Cross-delegation tracing

When runtimes delegate to child agents, tracing identifiers need to follow the delegation chain so eval platforms can stitch traces across runtimes. This is an observability feature useful for any multi-agent delegation, not just experiments.

Runtimes register their tracing identifiers via:

- **MCP** (Standard+ tier): `lenny/set_tracing_context` tool
- **JSONL** (all tiers): `set_tracing_context` message

```bash
# Example: runtime registers its LangSmith run ID
# (this happens inside the runtime, not via curl)
# MCP tool call:
{
  "tool": "lenny/set_tracing_context",
  "arguments": {
    "context": {
      "langsmith_run_id": "run_abc123",
      "langsmith_trace_id": "trace_xyz789"
    }
  }
}
```

The gateway automatically attaches the parent's `tracingContext` to child delegation leases. Child runtimes see these identifiers in their adapter manifest and can use them to link their traces to the parent's trace tree.

---

## Step 2: Built-in Eval Endpoint

For deployers without dedicated eval tooling, Lenny provides a score ingestion endpoint. Run a session to completion, then submit scores:

```bash
SESSION_ID="sess_01..."

curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/eval" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "scores": {
      "accuracy": 0.92,
      "helpfulness": 0.85,
      "code_quality": 0.88
    },
    "evaluator": "llm-judge-gpt4",
    "metadata": {
      "prompt_category": "refactoring",
      "ground_truth_available": true
    }
  }' | jq .
```

Expected response:

```json
{
  "evalId": "eval_01...",
  "sessionId": "sess_01...",
  "scores": {
    "accuracy": 0.92,
    "helpfulness": 0.85,
    "code_quality": 0.88
  },
  "evaluator": "llm-judge-gpt4",
  "createdAt": "2026-04-12T10:15:00Z"
}
```

You can submit multiple evals per session (up to `maxEvalsPerSession`, default: 50), each from a different evaluator or scoring dimension. Only sessions in `completed` or `failed` state accept eval submissions. Rate limits are configurable via `evalRateLimit.perSessionPerMinute` and `evalRateLimit.perTenantPerMinute` in tenant configuration.

---

## Step 3: Session Replay for Regression Testing

Session replay supports controlled comparison across runtime versions without A/B experiments:

```bash
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/replay" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "targetRuntime": "claude-worker-v2",
    "replayMode": "prompt_history"
  }' | jq .
```

This creates a new session that replays the original session's prompt history against `claude-worker-v2`. You can then score both the original and replayed sessions (via runtime-native eval or the built-in endpoint) and compare results.

---

## Step 4 (Optional): Connecting Evaluation with Experimentation

The steps above work without any A/B experiments. If you are also running experiments, evaluation connects with experimentation in two ways:

### Experiment context in the adapter manifest

When a session is enrolled in an experiment, the adapter manifest includes an `experimentContext` field:

```json
{
  "experimentContext": {
    "experimentId": "claude-v2-rollout",
    "variantId": "candidate",
    "inherited": false
  }
}
```

Runtimes can use this to tag traces with variant metadata for filtering and grouping in their eval platform. This is how variant-level comparison works: the runtime reads `experimentId` and `variantId` from the manifest and includes them as metadata on its eval traces.

Note that eval platforms (LangSmith, Braintrust, W&B) do not have native A/B comparison features; variant comparison works via metadata filtering and grouping in those platforms' UIs. For statistical rigor (significance testing, confidence intervals, winner recommendation), use a dedicated experimentation platform (LaunchDarkly, Statsig). See the SPEC Section 10.7 "Full A/B testing with external platforms" for the three-platform integration pattern.

### Automatic attribution on the built-in endpoint

When a session is enrolled in an experiment and you submit scores via the built-in `/eval` endpoint, the gateway automatically populates `experiment_id` and `variant_id` on the eval result. No scorer-side wiring is needed. The Results API (`GET /v1/admin/experiments/{name}/results`) provides per-variant aggregation.

### Setting up an experiment

To create an experiment with two variants:

```bash
curl -s -X POST http://localhost:8080/v1/admin/experiments \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "claude-v2-rollout",
    "description": "Compare claude-worker-v1 vs v2 on refactoring tasks",
    "variants": [
      {
        "id": "baseline",
        "runtime": "claude-worker-v1",
        "pool": "default-pool",
        "weight": 50
      },
      {
        "id": "candidate",
        "runtime": "claude-worker-v2",
        "pool": "default-pool",
        "weight": 50
      }
    ],
    "bucketingKey": "user_id",
    "sticky": true
  }' | jq .
```

### Querying experiment results (built-in endpoint)

```bash
curl -s "http://localhost:8080/v1/admin/experiments/claude-v2-rollout/results" \
  -H "Authorization: Bearer $ADMIN_TOKEN" | jq .
```

This returns per-variant score aggregation for scores submitted via the built-in `/eval` endpoint only. Scores in runtime-native eval platforms should be queried from those platforms directly.

---

## Key Concepts

- Eval is independent of experimentation: any session can be scored, with or without an active experiment.
- Two evaluation paths: runtime-native eval platforms (LangSmith, Braintrust, etc.) and Lenny's `/eval` endpoint.
- Cross-delegation tracing: `tracingContext` propagation supports trace stitching across delegation chains, for any multi-agent delegation, not only experiments.
- Experiment context delivery (optional): When experiments are active, the adapter manifest includes `experimentContext` so runtimes can tag traces with variant metadata for filtering and grouping.
- Session replay: Replaying sessions against different runtimes provides controlled comparison, with or without experiments.
- Configurable rate limits: `evalRateLimit.perSessionPerMinute` and `evalRateLimit.perTenantPerMinute` control built-in eval submission rates.

---

## Next Steps

- [Session Derive and Replay](session-derive-replay): workspace forking and replay details
- [REST API Reference](../api/rest): eval and experiment endpoints
- [Configuration Reference](../reference/configuration): eval rate limits and quota settings
