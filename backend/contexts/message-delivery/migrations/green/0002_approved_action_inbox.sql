-- schema: message-delivery
-- Durable inbox для утверждённых действий. Lease истекает и может быть захвачен
-- после падения worker; payload hash запрещает повтор ключа с иным содержимым.
CREATE TABLE IF NOT EXISTS "message-delivery".approved_action_inbox (
    idempotency_key text PRIMARY KEY,
    payload_hash    text NOT NULL,
    payload         jsonb NOT NULL,
    status          text NOT NULL CHECK (status IN ('pending', 'leased', 'acked')),
    lease_token     text,
    lease_until     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    acked_at        timestamptz,
    CHECK ((status = 'leased' AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
        OR (status <> 'leased' AND lease_token IS NULL AND lease_until IS NULL)),
    CHECK ((status = 'acked') = (acked_at IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS approved_action_inbox_claim_idx
    ON "message-delivery".approved_action_inbox (created_at, idempotency_key)
    WHERE status IN ('pending', 'leased');
