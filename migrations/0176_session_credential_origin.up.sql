-- §8.3 line 472 / line 488 — credential_origin_session_id identifies the
-- session whose credential pool a `credentialPropagation: inherit` hop
-- draws from. The §8.3 multi-hop rule requires an `inherit` hop to forward
-- the same origin pool, traced through contiguous `inherit` hops back to
-- where `independent` was last used or the root, and requires the
-- cross-environment provider-compatibility check to compare that origin
-- pool's providers against the immediate target runtime's supportedProviders
-- at each boundary. root_session_id plus parent_session_id cannot locate the
-- origin across an `independent` break without per-hop mode history, so the
-- resolved origin is persisted once, at child-row creation, and read in O(1)
-- at each subsequent hop. The delegation Service stamps it: an `inherit`
-- child copies the parent's origin (or the parent's own id when the parent
-- has none); an `independent`, `deny`, root, or top-level session uses its
-- own id. NULL (the read path collapses it to the row's own id) marks a
-- self-origin, matching the parent_session_id NULLIF/COALESCE convention.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS credential_origin_session_id UUID NULL;
