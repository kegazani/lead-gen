-- schema: consoleauth
-- Миграция принадлежит схеме сервиса consoleauth (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Общий стор сессий оператора: одна таблица на все консольные сервисы
-- (lead-management, request-fulfillment). Сессия, выданная входом в одну
-- консоль, сохраняется здесь и принимается другой консолью, подключённой к той
-- же базе, — оператору не нужно входить повторно. Отзыв (Logout) удаляет
-- запись и действует для всех консолей сразу.
--
-- Права роли: в dev роль lead_gen владеет базой и создаёт схему сама. В prod
-- схема и таблица создаются от имени владельца схемы, а роли консольного
-- сервиса нужны права на схему consoleauth (USAGE) и на таблицу
-- console_sessions (SELECT/INSERT/UPDATE/DELETE) — выдаются контуром
-- развёртывания, а не этой миграцией.

CREATE SCHEMA IF NOT EXISTS consoleauth;

CREATE TABLE IF NOT EXISTS consoleauth.console_sessions (
    id          text        PRIMARY KEY,
    username    text        NOT NULL,
    csrf        text        NOT NULL,
    last_active timestamptz NOT NULL
);
