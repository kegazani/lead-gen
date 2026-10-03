## 1. Переоценка компаний задания (capability `lead-relevance`)

- [x] 1.1 Нормировать переоценку уже найденных компаний: критерии заново из запроса, без повторного обхода, привязка к тому же заданию, распределение вердиктов, пропуск без карточки, явный отказ без зависимостей. Основание — `backend/contexts/lead-management/internal/app/reassess.go` (`ReassessJob`, `ReassessReport`, `reassessMaterial`); проверка — `internal/app/reassess_test.go`.
- [x] 1.2 Нормировать консольное действие переоценки: доступность только на терминальном задании с оценками, возврат на экран задания, видимый отказ и CSRF. Основание — `internal/adapters/http/server.go` (`handleJob` → `CanReassess`, `handleReassessJob`, маршрут `POST /jobs/{id}/reassess`), `internal/adapters/http/views.go` (`CanReassess`), `templates/job.html`; проверка — `internal/adapters/http/reassess_http_test.go` и golden `testdata/job.golden`.
- [x] 1.3 Снять прежнюю не-цель «Не вводить ручную переоценку» из `openspec/changes/archive/2026-09-26-verified-lead-relevance` явным решением в proposal/design; сохранить не-цель «не переносить ручную оценку в правила будущих запусков».

## 2. Экран «Лиды» и выгрузка (capability `lead-cards-console`)

- [x] 2.1 Нормировать экран «Лиды» и выгрузку: только компании с email; автономный HTML-документ и CSV с тем же набором для передачи вне консоли. Основание — `internal/adapters/http/server.go` (`handleLeads`, `handleLeadsExport`, `handleLeadsExportCSV`), `templates/leads.html`, `templates/leads-export.html`, `internal/adapters/http/views.go` (`leadRows`); проверка — `TestLeadsPageShowsOnlyLeadsWithEmail`, `TestLeadsExportStandaloneHTML`, `TestLeadsExportCSV`.
- [x] 2.2 Зафиксировать политику CSP автономной выгрузки и BOM CSV. Основание — ослабленная CSP в `handleLeadsExport`, BOM в `handleLeadsExportCSV`; правило `internal/adapters/http/views_rules_test.go` об исключении `leads-export.html` из запрета inline-скриптов.

## 3. Документация и проверки

- [x] 3.1 Уточнить описания покрытия в матрице `docs/domain-model.md` (`lead-relevance`, `lead-cards-console`), не добавляя агрегатов, сущностей, событий и контрактов; `architecture/domain-catalog.yaml` и владение не меняются.
- [x] 3.2 Прогнать `openspec validate --all --strict`. Проверка: 0 ошибок.
- [x] 3.3 Прогнать `go -C tools/archlint run .`. Проверка: archlint зелёный — новых capability нет, покрытие «каждая capability каталога объявлена в OpenSpec» сохраняется.
- [x] 3.4 Синхронизировать дельты в `openspec/specs/` и заархивировать change в `openspec/changes/archive/2026-10-03-document-reassess-and-leads-export/`.
