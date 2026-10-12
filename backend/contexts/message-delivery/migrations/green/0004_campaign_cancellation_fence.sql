-- Tombstone действует до approved-action и сохраняется независимо от inbox rows.
CREATE TABLE IF NOT EXISTS "message-delivery".campaign_cancellation (
    tenant_id   text NOT NULL,
    campaign_id text NOT NULL,
    cancelled_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, campaign_id)
);

ALTER TABLE "message-delivery".approved_action_inbox
    DROP CONSTRAINT IF EXISTS approved_action_inbox_status_check;
ALTER TABLE "message-delivery".approved_action_inbox
    ADD CONSTRAINT approved_action_inbox_status_check
    CHECK (status IN ('pending', 'leased', 'acked', 'cancelled'));

-- cancelled item больше не имеет lease; acked_at остаётся связан только с ACK.
