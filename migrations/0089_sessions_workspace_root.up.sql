-- §7.3 line 408 step (d) — Recreate same absolute `cwd` path. The
-- adapter reports its WorkspaceRoot on the §15.5 NegotiateVersion
-- handshake; the gateway captures it on the first Bind and persists it
-- on the session row so a subsequent Resume can pass it back to the
-- replacement pod's adapter as `expected_workspace_root`. The adapter
-- asserts equality against its own WorkspaceRoot before extracting any
-- checkpoint bytes — the §7.3 contractual guard against runtime
-- template drift between the original and replacement pods. F-7.3.15.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS workspace_root TEXT NOT NULL DEFAULT '';
