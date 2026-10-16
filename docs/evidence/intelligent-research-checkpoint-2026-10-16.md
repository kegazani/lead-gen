# Сохранение реализации — 2026-10-16

Change: `intelligent-lead-research`; ветка: `concept/intelligent-lead-research`.
Сохранение промежуточной реализации, а не приёмка всей программы.

## Состав

Опубликованные research-контракты; PostgreSQL память, обратимые identity решения,
планировщик и очередь миссий; Firecrawl ledger и SearXNG adapter; LLM extraction;
режим исследования человеческой заявки, прогресс и досье; отдельная runtime
composition и Docker Compose overlay. Новые миграции, тесты, каталог,
диаграммы и инструкции входят в тот же checkpoint.

## Проверки и незавершённая работа

- Предыдущий полный `make check` прошёл build/test/vet/importlint/archlint,
  затем завершился ошибкой `openspec validate --all --strict`: 45 passed,
  30 failed. На исходном `364c858` воспроизведены те же 30 отказов
  из-за длинных требований. Общий гейт не зелёный.
- Адресные PostgreSQL/race, HTTP/JS и Chromium проверки описаны в
  [runtime evidence](intelligent-research-runtime-2026-10-15.md).
  Это сохранённые результаты предыдущего запуска, а не повтор всех suites
  после восстановления сессии. Полный повтор перед этим commit не выполнялся.
- Последние изменения текстов фаз UI ещё не проверены отдельным повтором
  полного контейнерного сценария. Полная сборка Dockerfile этим smoke
  не подтверждена: использовался собранный binary в cached runtime image.
- Последний реальный контактный прогон требует итоговой сверки persistent
  результата. Сохранённый handoff сообщает partial: восемь компаний,
  ноль контактов и зачтённых результатов, 20 учтённых Firecrawl credits.
  Контактная цель не принята; повторных платных вызовов для сохранения нет.
- SearXNG полезная внешняя выдача заблокирована proxy CONNECT 403.
  Самостоятельный crawler/browser/OCR, MarketStudy, формы, inbound/outbound,
  полный индекс и связь нового досье со старым каталогом/письмами не завершены.
- Все 94 task остаются открытыми: [подробный аудит покрытия](intelligent-research-task-coverage-2026-10-15.md).
  Наличие работающих отдельных сценариев не закрывает целую задачу.

## Исключения из Git

Provider env, console password, staged secret bundles, сырые HTTP/LLM ответы,
локальные SQL/runtime logs и прочие private artifacts остаются вне checkout
или в ignored `.runtime/`. Включены только публичные исходники, конфигурация
с подстановками переменных, тестовые fixtures и очищенные evidence.
