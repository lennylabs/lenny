-- §4.7 / §5.1 — the runtime registry mirror of the nonce-only-mode
-- activation field the RuntimeReconciler writes from Runtime.spec.
--
-- require_so_peercred records the §4.7 nonce-only activation decision a
-- runtime's CRD carries: true (the default) requires the SO_PEERCRED
-- adapter-agent boundary self-test to pass, and false activates
-- nonce-only mode on a deploymentModel: sidecar runtime after confirmed
-- gVisor SO_PEERCRED divergence (Phase 3.5). The RuntimeReconciler
-- mirrors Runtime.spec.requireSoPeercred into this column through the
-- CRD-to-store apply path; the gateway pool admission gate reads it to
-- reject an unacknowledged pool that references a nonce-only runtime
-- (§5.3 acknowledgeNonceOnlyAuth). The field is settable only through
-- Runtime CRD registration, so a runtime registered only through the
-- admin API runs with the column at its true default. Stored as a plain
-- BOOLEAN column alongside workspace_tier and egress_profile rather than
-- in a JSONB blob so the admission lookup is a single-column read.
ALTER TABLE runtime_definitions
    ADD COLUMN require_so_peercred BOOLEAN NOT NULL DEFAULT true;
