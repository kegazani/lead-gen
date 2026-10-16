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
- HTTP-тесты request-fulfillment, включая последние тексты фаз UI, повторно
  прошли после сохранения (1,020 с). Полный контейнерный сценарий после них
  отдельно не повторялся. Полная сборка Dockerfile этим smoke
  не подтверждена: использовался собранный binary в cached runtime image.
- Последний реальный контактный прогон повторно сверен read-only по PostgreSQL
  и сохранённым HTTP/Chromium artifacts: миссия
  `research-289ca8e4d3de6ec2993fc91493fef661`, revision160,
  completed/finished, outcome `partial`, причина
  `firecrawl-exhausted-no-alternative`. Обнаружено 27, документов 12,
  сохранено восемь компаний; accepted/contacts/email-ready — ноль.
  Владелец заявки `main-ui-1792084501175793186` — failed, version5.
  Grant20: spent20, reserved0, unknown0; 16 завершённых acquisition attempts.
  В assertions также нет email/phone: отсутствие контактов подтверждено
  в хранилище, а не только в UI. Сохранённый Chromium отчёт содержит
  восемь строк, четыре экрана, без JS exceptions и переполнения.
  Readiness на момент повторной проверки HTTP200/up, обе worker-пробы up.
  Контактная цель не принята; новых платных вызовов не было.
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

## Сохранение и повторные адресные проверки

Реализация сохранена и отправлена коммитом
`f7d4faa7ba0ec039215fd35b0ee4c89b0a3553ff` (182 файла). До дальнейшей
правки этого отчёта рабочая копия была чистой; remote branch указывала на тот
же SHA. Проверка staged-файлов не нашла provider tokens/private keys.

После push повторно прошли `openspec validate intelligent-lead-research --strict`,
`go -C tools/archlint run .` и HTTP package tests request-fulfillment.
Это адресные проверки; прежний отказ общего `make check` ими не снимается.
Сохранённый actual-provider прогон выполнен предыдущим собранным binary;
новый платный end-to-end именно commit `f7d4faa` не запускался. Учёт credits
основан на тарифе адаптера, отдельно с provider billing не сверялся.
