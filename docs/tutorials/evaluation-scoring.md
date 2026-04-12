---
layout: default
title: "Evaluation Scoring"
parent: Tutorials
nav_order: 10
---

# Evaluation Scoring

**Persona:** Client Developer + Platform Operator | **Difficulty:** Intermediate

Lenny provides built-in evaluation hooks for scoring session quality and comparing runtime versions. In this tutorial you will submit eval scores for a completed session, set up an experiment with two runtime variants, run sessions under the experiment, and query results to compare variants.

## Prerequisites

- Lenny running locally via `docker compose up`
- Two runtime versions registered (e.g., `claude-worker-v1` and `claude-worker-v2`)
- Familiarity with [Your First Session](first-session)
- curl and jq installed

---

## Step 1: Run a Session and Submit an Eval Score

First, run a session to completion using the standard lifecycle (create, finalize, start, send a message, terminate). See [Your First Session](first-session) for details.

Once the session is in a terminal state (`completed` or `failed`), submit an eval score:

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

You can submit multiple evals per session (up to `maxEvalsPerSession`, default: 50), each from a different evaluator or scoring dimension.

**Important:** Only sessions in `completed` or `failed` state accept eval submissions. Sessions in `cancelled` or `expired` state return `SESSION_NOT_EVAL_ELIGIBLE`.

---

## Step 2: Set Up an Experiment

Experiments enable A/B testing of runtime versions. Create an experiment with two variants using the Admin API:

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

Key fields:
- **`variants`**: Each variant specifies a runtime, pool, and traffic weight.
- **`bucketingKey`**: Deterministic assignment key (`user_id` ensures the same user always gets the same variant).
- **`sticky`**: Once a user is assigned to a variant, they stay there for the experiment's lifetime.

---

## Step 3: Run Sessions Under the Experiment

Create sessions that reference the experiment. The gateway assigns each session to a variant based on the bucketing key:

```bash
curl -s -X POST http://localhost:8080/v1/sessions \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "runtime": "claude-worker-v1",
    "labels": {
      "experiment": "claude-v2-rollout",
      "task": "refactoring"
    }
  }' | jq .
```

The gateway overrides the runtime selection based on the experiment's variant assignment. The response includes the actual runtime used:

```json
{
  "session_id": "sess_02...",
  "runtime": "claude-worker-v2",
  "metadata": {
    "experiment": "claude-v2-rollout",
    "variant": "candidate"
  }
}
```

Run the session through its lifecycle, then submit eval scores as in Step 1. Repeat for multiple sessions to build up a statistically meaningful sample.

---

## Step 4: Query Results to Compare Variants

Use the Results API to compare variant performance:

```bash
curl -s "http://localhost:8080/v1/admin/experiments/claude-v2-rollout/results" \
  -H "Authorization: Bearer $ADMIN_TOKEN" | jq .
```

Expected response:

```json
{
  "experiment": "claude-v2-rollout",
  "variants": [
    {
      "id": "baseline",
      "runtime": "claude-worker-v1",
      "sessionCount": 25,
      "avgScores": {
        "accuracy": 0.87,
        "helpfulness": 0.82,
        "code_quality": 0.84
      }
    },
    {
      "id": "candidate",
      "runtime": "claude-worker-v2",
      "sessionCount": 25,
      "avgScores": {
        "accuracy": 0.93,
        "helpfulness": 0.89,
        "code_quality": 0.91
      }
    }
  ],
  "status": "active"
}
```

---

## Step 5: Use Session Replay for Controlled Comparison

For a more controlled comparison, replay completed sessions against the candidate runtime:

```bash
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/replay" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "targetRuntime": "claude-worker-v2",
    "replayMode": "prompt_history",
    "evalRef": "claude-v2-rollout"
  }' | jq .
```

This creates a new session that replays the original session's prompt history against `claude-worker-v2`. The `evalRef` links the replayed session to the experiment for aggregated comparison.

---

## Key Concepts

- **Multi-dimensional scoring**: Eval scores support arbitrary dimensions (accuracy, helpfulness, etc.).
- **Deterministic bucketing**: Experiments use consistent hashing on the bucketing key for reproducible assignment.
- **Sticky assignment**: Users stay in their assigned variant for the experiment lifetime.
- **Session replay**: Replaying sessions against different runtimes provides controlled pairwise comparison.
- **Quota protection**: `maxEvalsPerSession` prevents unbounded eval storage (returns `EVAL_QUOTA_EXCEEDED`).

---

## Next Steps

- [Session Derive and Replay](session-derive-replay) -- workspace forking and replay details
- [REST API Reference](../api/rest) -- eval and experiment endpoints
- [Configuration Reference](../reference/configuration) -- `eval.maxEvalsPerSession` setting
