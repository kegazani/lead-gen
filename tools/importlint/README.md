# importlint

Проверяет границы импортов между слоями, bounded contexts и публичным edge в
multi-module workspace `lead-gen`. Читает Go-импорты через `go/parser` (в
отличие от `archlint`, который смотрит каталог, схему, OpenSpec-спеки и
диаграммы, а не Go-код).

Грамматика путей (базовый префикс задаётся флагом `-module`, по умолчанию
`lead-gen`):

- `lead-gen/contexts/<ctx>/internal/<layer>[/<subpath>]` — внутренний слой контекста;
- `lead-gen/contexts/<ctx>[/<subpath>]` — публичная поверхность (сборка сервиса);
- `lead-gen/contracts/<owner>[/<subpath>]` — опубликованный контракт;
- `lead-gen/platform/...` — platform;
- `lead-gen/cmd/...` — точка входа (composition root);
- `lead-gen/frontend/public/...` — публичный edge.

Запрещённые рёбра:

1. `internal/domain` не импортирует свой `internal/app`/`internal/ports`/`internal/adapters`.
2. `internal/app` не импортирует свой `internal/adapters`.
3. `internal/ports` не импортирует свой `internal/adapters`.
4. Внутренние слои контекста, кроме `internal/adapters`, не импортируют внешние
   модули (SDK поставщика, драйвер хранилища): они допустимы только в `adapters`.
5. Межконтекстно контекст импортирует другой контекст только через
   `lead-gen/contracts/<owner>`. Чужой `lead-gen/contexts/<owner>/internal/...`,
   чужой корневой пакет контекста и чужое хранилище
   `.../internal/adapters/persistence/**` запрещены без исключений.
5a. HTTP-адаптер консоли (`lead-gen/backend/contexts/<ctx>/internal/adapters/http/**`)
   не импортирует `internal/domain`, `internal/app` и прочие внутренние слои чужого
   контекста: экран принадлежит контексту-владельцу агрегата и добирает чужие данные
   только через опубликованный `lead-gen/contracts/<owner>` (design.md,
   Решение 3). Свой `domain`/`app`/`ports`, чужой контракт и `platform`
   остаются допустимыми — правило касается только чужого контекста и только
   пакета `internal/adapters/http`.
6. `lead-gen/frontend/public/...` не импортирует `lead-gen/contexts/...` и
   адаптеры хранилищ (любой путь `lead-gen/.../adapters/persistence`).
7. `lead-gen/cmd/...` — composition root, границами не ограничен.

Исключение `lead-gen/contracts/<owner>` — единственный легальный
межконтекстный импорт: это контракт, который владелец публикует явно. Общий
транспорт и роли запуска лежат в `lead-gen/platform/...` (`Kind == platform`),
который контекстом не считается, поэтому его импортируют и контексты, и edge.

Запуск из корня workspace (по умолчанию `-root` поднимается вверх до каталога с
`go.work`):

```bash
go -C tools/importlint run .
```

При нарушениях — код возврата 1 и список на stderr, иначе код 0 и краткая сводка.

Unit-тесты:

```bash
go -C tools/importlint test ./...
```

Отрицательные и положительные фикстуры лежат в `testdata/`; Go игнорирует
каталог `testdata` при сборке (`go build ./...`, `go vet ./...`), а `importlint`
пропускает его при обходе реального дерева, чтобы намеренно некорректные фикстуры
не давали ложных срабатываний. Сетевой доступ и внешние Go-модули не требуются.
