-- The §10.6 environment a session was created in.
--
-- environment records the §10.6 Environment the session belongs to,
-- set at session creation and immutable thereafter. An empty value
-- means the session is not scoped to an environment. The §10.6
-- cross-environment delegation checks resolve against this value.

ALTER TABLE sessions
    ADD COLUMN environment TEXT NOT NULL DEFAULT '';
