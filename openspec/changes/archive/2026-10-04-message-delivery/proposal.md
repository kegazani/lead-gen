## Why

Контекст `message-delivery` собран как вертикальный срез на in-memory адаптерах: реальная отправка не подключена, стоп-лист пуст, состояние живёт в памяти процесса (`docs/implementation-matrix.md` §0, пункты A1 и B). Из-за этого уже реализованный контур согласования исходящих действий не замыкается: утверждённое письмо не уходит получателю, а исход доставки теряется при рестарте и не виден другим ролям.

## What Changes

- **Реальная отправка почты.** За `ports.Mailer` подключается адаптер, исполняющий отправку через реального почтового провайдера (SMTP-адаптер владельца `integration-platform`, контракт `mail`). На продуктовом пути `mailer.Unavailable` заменяется; недоступность провайдера остаётся нормализованным временным исходом, а не выдуманным результатом.
- **Интеграция стоп-листа.** За `ports.SuppressionList` подключается адаптер поверх контракта `suppression-policy` (владелец `data-governance`) вместо пустого in-memory списка. Проверка выполняется до вызова провайдера.
- **Персистентность.** Порты контекста (`message`, `sender-reputation`, outbox, unit-of-work) получают PostgreSQL-адаптеры и миграции. Состояние переживает рестарт и разделяется между ролями; схема и роль СУБД — по правилам `service-per-context`.
- **Роль `schedule`.** Реализуется тикер повторных окон доставки вместо каркаса, который честно сообщает о нереализованности.
- **Сборка в `cmd/lead-gen`.** Добавляются параметры DSN зелёной зоны, провайдера отправки и стоп-листа; выбор in-memory допускается только как явный dev-режим.

## Capabilities

### New Capabilities

- `outreach-channel-email`: доставка утверждённых почтовых действий — реальная отправка за портом провайдера, проверка стоп-листа, идемпотентность по утверждённому действию, персистентность состояния и публикация исхода.
- `domain-deliverability`: репутация отправляющего домена — учёт только нормализованных ответов провайдера, вычисляемые ограничения (`paused`, `throttled`), персистентность и снимок политики для потребителей.

### Modified Capabilities

Нет: требования существующих спек не меняются.

## Impact

- **Affected bounded contexts:** `message-delivery` (владелец, все изменения), `integration-platform` (контракт `mail`/SMTP-адаптер), `data-governance` (контракт `suppression-policy`), `engagement-observation` и `analytics` (потребители `delivery-event` — форма контракта не меняется).
- **Доменная модель:** границы и capability не меняются — `outreach-channel-email` и `domain-deliverability` уже объявлены за `message-delivery` в `architecture/domain-catalog.yaml`; `archlint` падать не должен.
- **Код:** `backend/contexts/message-delivery/**` (адаптеры provider/suppression, `persistence/postgres`, миграции, роль `schedule`), `backend/cmd/lead-gen/{runners.go,main.go}`, возможный мост `approved-action → delivery-command`, `deploy/services.yaml` (группа `delivery`).
- **Данные и зависимости:** новый источник персистентности — PostgreSQL (зелёная зона); внешние зависимости — SMTP `integration-platform`; новых внешних сервисов не вводится.
- **Вне объёма (открытые разрывы, отдельные change'и):** мост `approved-action → delivery-command` (требует разрешения `RecipientRef` в адрес и `MaterialVersionRef` в контент — межконтекстный контракт разрешения) и сборка тела письма из `content-production`.
