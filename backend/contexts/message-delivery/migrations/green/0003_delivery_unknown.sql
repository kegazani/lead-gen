-- schema: message-delivery
-- Закрепляет закрытый набор состояний после добавления durable unknown outcome.
-- Ограничения добавляются NOT VALID, чтобы миграция не блокировалась на старых
-- строках, и сразу валидируются после проверки совместимости существующих данных.

ALTER TABLE "message-delivery".message
    ADD CONSTRAINT message_status_known
    CHECK (status IN ('pending', 'sending', 'delivered', 'failed', 'unknown')) NOT VALID;

ALTER TABLE "message-delivery".message
    VALIDATE CONSTRAINT message_status_known;

ALTER TABLE "message-delivery".message
    ADD CONSTRAINT message_last_outcome_known
    CHECK (last_outcome IN ('', 'delivered', 'hard-bounce', 'soft-bounce', 'temporary', 'suppressed', 'unknown')) NOT VALID;

ALTER TABLE "message-delivery".message
    VALIDATE CONSTRAINT message_last_outcome_known;
