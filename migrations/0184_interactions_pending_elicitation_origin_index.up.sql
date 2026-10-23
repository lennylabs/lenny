-- §9.2 pending-elicitation expiry index.
--
-- When a session leaves its pod, every lenny/request_elicitation elicitation
-- that session's pod raised and that is still pending expires (§9.2
-- Elicitation Chain). The raising session is the elicitation's
-- detail->>'originPod', which differs from the row's session_id when the
-- elicitation is held for a parent under §8.2. The expiry deletes the
-- tenant's pending elicitations with that origin, so this partial index is
-- keyed on (tenant_id, detail->>'originPod') and restricted to pending
-- elicitations: the delete reads only the rows it removes, however many
-- resolved interactions the tenant keeps until erasure.
--
-- spec: §9.2 (Elicitation Chain).
CREATE INDEX IF NOT EXISTS idx_interactions_pending_elicitation_origin
    ON interactions (tenant_id, (detail->>'originPod'))
    WHERE kind = 'elicitation' AND phase = 'pending';
