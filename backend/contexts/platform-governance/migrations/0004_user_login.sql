-- schema: platform-governance
-- Миграция принадлежит схеме сервиса platform-governance. Добавляет уникальный
-- индекс на логин пользователя (email учётной записи) среди непустых значений:
-- один email — одна учётная запись на всех тенантах. Частичный индекс не
-- затрагивает пустые значения (логин в домене всегда непуст, но у тенантов,
-- созданных администратором напрямую до введения логина, он может быть пустым).

CREATE UNIQUE INDEX IF NOT EXISTS tenant_user_login_unique_idx
    ON "platform-governance".tenant_user (login)
    WHERE login <> '';
