-- The §5.1 taskPolicy block on a runtime.
--
-- task_policy carries the runtime's §5.1 taskPolicy: the §5.2 task-mode
-- pod-reuse and workspace-cleanup policy (best-effort-scrub
-- acknowledgment, cross-tenant reuse, cleanup commands, pod retirement
-- limits, per-task retry budget). SQL NULL means the runtime declares
-- no task policy.

ALTER TABLE runtime_definitions
    ADD COLUMN task_policy JSONB;
