---
layout: default
title: "Evaluation and Scoring"
parent: Tutorials
nav_order: 10
---

# Evaluation and Scoring

**Persona:** Client Developer + Platform Operator | **Difficulty:** Intermediate

Lenny is not an eval platform. Runtime builders bring their own eval framework (LangSmith, Braintrust, Arize, Langfuse, home-grown); Lenny's only eval surface is a basic mechanism to store and retrieve scores. This tutorial covers how runtimes integrate their chosen eval framework, how to use Lenny's optional score storage, how cross-delegation tracing supports multi-agent observability, and optionally how to connect stored scores with variant context from an experiment.

## Prerequisites

- Lenny running locally via `docker compose up`
- At least one runtime registered (e.g., `claude-worker-v1`)
- Familiarity with [Your First Session](first-session)
- curl and jq installed

---

## Step 1: Runtime-Native Evaluation (the default path)

Runtimes integrate with whichever eval framework fits their workflow — LangSmith, Braintrust, Weights & Biases, Arize, Langfuse, or a home-grown pipeline — and use that framework's own SDKs and APIs directly. Lenny does not integrate outbound with these platforms and does not prescribe which one to use.

No Lenny-specific setup is required for runtime-native eval. Runtimes score sessions using their own tooling.

### Cross-delegation tracing

When runtimes delegate to child agents, tracing identifiers need to follow the delegation chain so eval platforms can stitch traces across runtimes. This is an observability feature useful for any multi-agent delegation, not just experiments.

Runtimes register their tracing identifiers via:

- **MCP** (Standard+ level): `lenny/set_tracing_context` tool
- **JSONL** (all levels): `set_tracing_context` message

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

## Step 2: Basic Score Storage (`/eval` endpoint)

Lenny provides a **basic mechanism to store and retrieve scores** alongside session state. This is useful when you want to persist scores close to the session record without standing up another system. It is not an eval runner, a judge, or a scoring model — it is a database table with an API in front of it.

Run a session to completion, then submit scores:

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

You can submit multiple scores per session (up to `maxEvalsPerSession`, default: 50), each from a different evaluator or scoring dimension. Only sessions in `completed` or `failed` state accept score submissions. Rate limits are configurable via `evalRateLimit.perSessionPerMinute` and `evalRateLimit.perTenantPerMinute` in tenant configuration.

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

## Step 4 (Optional): Connecting Scores with Experiment Variant Context

The steps above work without any experiments. If you are also routing traffic between variants (via Lenny's basic variant assigner or an external experimentation platform), evaluation connects with variant context in two ways:

### Variant context in the adapter manifest

When a session is routed to a variant, the adapter manifest includes an `experimentContext` field:

```json
{
  "experimentContext": {
    "experimentId": "claude-v2-rollout",
    "variantId": "candidate",
    "inherited": false
  }
}
```

Runtimes can use this to tag traces with variant metadata for filtering and grouping in their eval platform. The runtime reads `experimentId` and `variantId` from the manifest and includes them as metadata on its eval traces.

Variant comparison in eval platforms (LangSmith, Braintrust, W&B) works via metadata filtering and grouping in those platforms' UIs — they don't have native A/B comparison features. For statistical rigor (significance testing, confidence intervals, winner recommendation), bring in a dedicated experimentation platform (LaunchDarkly, Statsig, Unleash) — Lenny integrates with any OpenFeature-compatible provider. See SPEC Section 10.7 "Full A/B testing with external platforms" for the three-platform integration pattern.

### Automatic attribution on the built-in endpoint

When a session is routed to a variant and you submit scores via the built-in `/eval` endpoint, the gateway automatically populates `experiment_id` and `variant_id` on the stored record. No scorer-side wiring is needed. The Results API (`GET /v1/admin/experiments/{name}/results`) provides per-variant aggregation of scores that were stored through the built-in endpoint.

### Setting up a variant pool with the basic built-in assigner

To route traffic between two variants using Lenny's basic built-in assigner (most teams will instead configure assignment in an external experimentation platform — see [OpenFeature integration](../operator-guide/openfeature-integration.md)):

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

### Querying stored results (built-in endpoint)

```bash
curl -s "http://localhost:8080/v1/admin/experiments/claude-v2-rollout/results" \
  -H "Authorization: Bearer $ADMIN_TOKEN" | jq .
```

This returns per-variant aggregation for scores stored via the built-in `/eval` endpoint only. Scores in external eval platforms should be queried from those platforms directly; statistical analysis (significance, confidence) belongs in your experimentation platform.

---

## Key Concepts

- **Lenny is not an eval platform.** Runtime builders bring whichever eval framework they prefer (LangSmith, Braintrust, Arize, Langfuse, home-grown).
- **Basic score storage** (`/eval` endpoint): a database-backed mechanism to persist scores alongside session state. Use it if it fits; replace it or ignore it otherwise.
- Eval is independent of experimentation: any session can be scored, with or without an active variant pool.
- Cross-delegation tracing: `tracingContext` propagation supports trace stitching across delegation chains in external eval platforms — works for any multi-agent delegation, not only experiments.
- Variant context delivery (optional): When a session is routed to a variant, the adapter manifest includes `experimentContext` so runtimes can tag traces with variant metadata for filtering and grouping.
- Session replay: Replaying sessions against different runtimes provides controlled comparison, with or without experiments.
- Configurable rate limits: `evalRateLimit.perSessionPerMinute` and `evalRateLimit.perTenantPerMinute` control built-in score-submission rates.

---

## Next Steps

- [Session Derive and Replay](session-derive-replay): workspace forking and replay details
- [REST API Reference](../api/rest): score storage and variant-pool endpoints
- [Configuration Reference](../reference/configuration): score rate limits and quota settings
