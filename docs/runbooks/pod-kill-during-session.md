---
layout: default
title: "pod-kill-during-session"
parent: "Runbooks"
triggers:
  - alert: AgentPodKilledDuringActiveSession
    severity: warning
components:
  - sandbox
  - gateway
symptoms:
  - "an agent pod terminates mid-session (SIGKILL, OOM, node-drain)"
  - "the session reports a transport-level failure"
  - "the gateway retries onto a replacement pod and the session resumes"
tags:
  - chaos
  - lifecycle
  - sessions
requires:
  - admin-api
  - cluster-access
related:
  - gateway-to-pod-partition
  - node-drain-during-minio-outage
---

# pod-kill-during-session

An active agent pod was killed mid-session. The §4.6 controller detects the disappearance, the §4.4 checkpoint pipeline writes a final snapshot when one is in flight, and the §7.1 resume path attempts to restore the session onto a fresh pod from the most recent checkpoint.

## Trigger

The `AgentPodKilledDuringActiveSession` alert fires when:

- an agent pod whose Sandbox is in a non-terminal §6.2 phase transitions to `Failed` or is removed,
- the gateway records a transport-level failure on the affected session, and
- the §4.6 reconciler observes the kill within the alert's evaluation window.

## Diagnosis

1. Identify the affected sandbox and session.

       kubectl get sandbox -n lenny-agents <sandbox-name>
       kubectl logs -n lenny-system deployment/lenny-gateway --tail=200 | grep <session-id>

2. Determine the kill reason.

       kubectl get events -n lenny-agents --field-selector involvedObject.name=<pod-name> --sort-by=.lastTimestamp

   Look for `OOMKilled`, `Evicted`, `Killing` (preStop), or a node-drain notice.

3. Confirm whether a §4.4 checkpoint completed before the kill.

       lenny-ctl admin session get <session-id> --json | jq '.lastCheckpointAt'

## Remediation

1. If the kill is OOM, raise the pool's per-pod memory limit and roll a new SandboxTemplate revision.
2. If the kill is node-drain, follow [node-drain-during-minio-outage.md](node-drain-during-minio-outage.md) for the drain-time procedure.
3. If the kill is a flapping liveness probe, see the §4.7 adapter health-probe tuning in `charts/lenny/values.yaml` under `agent.livenessProbe`.

## Verification

- The replacement pod reaches `Ready` and the session transitions to `running` again.
- `lenny_session_resume_attempts_total{pool=<pool>, outcome="success"}` advances by one for the affected pool. The spec-named counter (§16.1) covers every retry/resume attempt and yields the resume success rate as `outcome="success"` over the total.

## Escalation

Page the on-call platform engineer if more than one pod in the same pool is killed within an hour — that pattern points at a node-level fault rather than a per-session issue.
