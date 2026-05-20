-- §25.5 webhook subscription registry for lenny-ops. The webhook
-- delivery worker (pkg/ops/opsservice/webhookloop) reads the active
-- subscriptions on every reconcile through a small SubscriptionSource
-- adapter, so a Store change is visible to the worker without a
-- process restart.
--
-- The table is platform-scoped (the lenny-ops control plane is not
-- multi-tenanted at the §25 boundary), so no tenant column or RLS
-- policy applies here. The id column is the application-allocated
-- subscription id; types is a JSONB-encoded sorted string array so
-- the Service.Create normalization round-trips through the column.

CREATE TABLE ops_event_subscriptions (
    id           TEXT        NOT NULL PRIMARY KEY,
    callback_url TEXT        NOT NULL,
    types        JSONB       NOT NULL DEFAULT '[]'::jsonb,
    secret       TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ops_event_subscriptions_created_at_idx
    ON ops_event_subscriptions (created_at);
