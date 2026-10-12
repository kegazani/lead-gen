## Why

Целевой поток продукта рвётся на стыке «AI сгенерировал письма → оператор утвердил и отправил». Сейчас `request-fulfillment` по недобору автоматически генерирует персонализированные письма (`request-shortfall` → контракт `email-generation`) и складывает их в `request-fulfillment.email_draft`, но эти черновики **никуда не передаются**: утверждение и отправка живут в другом контексте (`outreach-orchestration` → `message-delivery`), а передачи между ними нет. Живой прогон 2026-10-05 подтвердил: письма генерируются (`drafted:2`), но чтобы их отправить, черновик приходилось создавать вручную в очереди outreach. То есть сквозной сценарий приёмки `live-e2e-contour` 3.1/3.2 не замыкается не из-за ключей, а из-за отсутствующей передачи.

Отправка при этом уже работает целиком: контракт `outreach-approval` требует у черновика непрозрачные ссылки на получателя (`RecipientRef`) и материал (`MaterialVersionRef`), мост `delivery-bridge` разрешает их у владельцев (`lead-management`, `content-production`) и `message-delivery` шлёт письмо. Не хватает ровно одного звена: превратить сгенерированные письма заявки в черновики кампании outreach.

## What Changes

- **Новая capability `outreach-handoff` (`outreach-orchestration`)** — принимающая сторона: по команде от `request-fulfillment` создаёт **одну кампанию на заявку** и по черновику на каждый переданный лид, идемпотентно по `request_id`; черновики встают в ожидание утверждения оператором, отправка остаётся за человеком. Пустой состав не создаёт кампанию; повтор команды не создаёт дублей.
- **`request-shortfall` (`request-fulfillment`) — MODIFIED**: после автоматической генерации писем при недоборе система передаёт их в `outreach-orchestration` на утверждение. В передачу входят только письма, для которых разрешимы получатель и материал; прочие честно пропускаются (не подменяются заглушкой). Отправка по-прежнему за человеком.
- **`email-generation` (`lead-management`) — MODIFIED**: ответ контракта дополнительно несёт непрозрачную ссылку на получателя (`recipient_ref`) — идентификатор контакта лида с пригодным адресом, либо пусто, если адреса нет. Персональных данных (адрес, телефон) контракт по-прежнему не несёт. Это позволяет владельцу письма приложить получателя, не заводя отдельного запроса «лид → получатель».
- **Новый контракт `outreach-handoff`** (владелец поверхности — `outreach-orchestration`, которую вызывает инициатор `request-fulfillment`; sync/http-json, команда) и **новые связи каталога**: `requests-to-outreach` (`outreach-orchestration` → `request-fulfillment`) и `content-for-requests` (`content-production` → `request-fulfillment`, контракт `content-request`). Материал письма `request-fulfillment` производит через существующий контракт `content-request` (`subject` — первая строка, `body` — тело).

**Не входит:** изменение пути утверждения/отправки outreach (он уже реализован), смена владельцев, новая отправка без человека, изменение контракта `approved-action`.

## Capabilities

### New Capabilities

- `outreach-handoff`: приём передачи писем заявки из `request-fulfillment` и идемпотентное создание кампании с черновиками в очереди утверждения.

### Modified Capabilities

- `request-shortfall`: к требованию автоматической генерации писем добавляется передача сгенерированных писем в outreach на утверждение (с условиями включаемости и честным пропуском неразрешимых).
- `email-generation`: ответ контракта дополняется непрозрачной ссылкой на получателя (`recipient_ref`); требование об отсутствии персональных данных сохраняется (ссылка непрозрачна).

## Impact

**Affected bounded contexts:** `request-fulfillment` (источник передачи, обязательный материал, новое звено workflow), `outreach-orchestration` (новая capability `outreach-handoff`, приём команды), `lead-management` (расширение ответа `email-generation` непрозрачной ссылкой получателя), `content-production` (потребитель существующего `content-request` — без дельты требований).

**Доменная модель:** каталог `architecture/domain-catalog.yaml` дополняется capability `outreach-handoff` и связями `requests-to-outreach`, `content-for-requests`; обновляются `docs/domain-model.md` и затронутые диаграммы — в том же change.

**Код и данные:** `contracts/lead-management/emailgeneration` (MINOR), новый `contracts/outreach-orchestration/outreachhandoff`, `backend/contexts/request-fulfillment` (хранение `recipient_ref`, activity передачи, сборка `content-request`), `backend/contexts/outreach-orchestration` (приём и создание кампании/черновиков), `backend/cmd/lead-gen` (проводка контрактов по топологии), миграция `email_draft` (`recipient_ref`).

**Спецификации/приёмка:** снимается блокер приёмки `live-e2e-contour` 3.1/3.2 — после реализации письма заявки попадают в очередь утверждения и отправляются оператором; задача 4.3 того же change (заглушка риска на пути утверждения) остаётся отдельной.
