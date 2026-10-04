-- schema: integration-platform
-- Миграция принадлежит схеме сервиса integration-platform (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Векторный индекс семантического recall: документ хранится строкой с
-- нормализованным вектором эмбеддинга и моментом обновления. Индекс переживает
-- рестарт процесса и разделяется ролями; поиск — точный (перебор всех строк с
-- косинусной близостью), поэтому расширение pgvector не требуется.
--
-- Вектор хранится как double precision[] (векторы нормализованы приложением,
-- поэтому косинусная близость равна скалярному произведению). Upsert по id
-- заменяет вектор, не создавая вторую строку.

CREATE TABLE IF NOT EXISTS "integration-platform".vector_document (
    id         text PRIMARY KEY,
    embedding  double precision[] NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
