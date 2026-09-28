## Why

Юридическая рамка продукта выражена в спеках (`data-handling`, `lead-pool-client-side`), а её механики не существует: сервис `data-governance` поднимается заглушкой, основание обработки нигде не регистрируется, стоп-лист нигде не исполняется. Стоп-лист — единственное правило, которое обязано остановить необратимое действие даже после решения оператора: каталог фиксирует инвариант «активная запись стоп-листа имеет приоритет над кампанией, решением оператора и поставщиком», но под ним нет ни требований, ни кода. ЭТАП 2 (`message-delivery`) обязан делать preflight-проверку подавления непосредственно перед доставкой — к этому моменту правило уже должно существовать и исполняться, а не проектироваться.

## What Changes

- **Вводится первый вертикальный срез** контекста `data-governance` по образцу `outreach-approval-slice`: слои `domain`/`app`/`ports`/`adapters`, тесты инвариантов, контрактные suite, in-memory адаптеры; PostgreSQL-адаптер — следующим шагом.
- **Реализуется агрегат `processing-governance`** (AR `processing-basis`; E `processing-basis`, `suppression-entry`; VO `processing-purpose`, `retention-period`) строго по каталогу: регистрация основания обработки (цель, категория, тенант, период), отзыв основания и истечение срока, проверка полномочия; внесение и снятие записи стоп-листа (отписка, жалоба, жёсткий отказ, `abuse`), проверка подавления. Инварианты каталога защищаются доменом: полномочие действительно только для указанных цели, категории, тенанта и периода; активная запись стоп-листа имеет приоритет над кампанией, решением оператора и поставщиком. Политики: `data-zone-policy`, `suppression-policy`.
- **Публикуются контракты владельца** в новом модуле `contracts/data-governance`: `processing-authority`, `suppression-policy` (policy, `async`/`amqp`; при co-location — in-process адаптер), `data-zone-policy`. События `processing-authority-changed` и `contact-suppressed` пишутся через transactional outbox.
- **Добавляется capability `contact-suppression`** (каталог и спека) — требования стоп-листа, которые сейчас существуют только как инвариант каталога и строки `docs/system-map.md`, а не как проверяемые сценарии.
- **Создаётся Go-модуль `backend/contexts/data-governance`** (context-first); в `backend/cmd/lead-gen` заглушка сервиса заменяется сборкой ролей `api`, `worker`, `chains`.
- **Вне среза:** физический кластер красной зоны (хранение `client-relational` объявлено каталогом; срез работает с in-memory и синтетическими данными), сетевые адаптеры, дедупликация и скоринг пула (принадлежат `lead-management`/`audience`), выбор формы размещения пула режима A (открытый вопрос рамки, `full-enterprise-core-scope/design.md`).

## Capabilities

### New Capabilities

- `contact-suppression`: стоп-лист как авторитетное правило подавления — виды записей, приоритет над кампанией, решением оператора и поставщиком, проверка непосредственно перед необратимым действием, снятие записи и аудит изменения.

### Modified Capabilities

Нет. Требования `data-handling` и `lead-pool-client-side` реализуются как есть, их тексты не правятся.

**Обоснование новой capability.** Политика `suppression-policy` и контракт `suppression-policy` объявлены каталогом, но требований под ними нет ни в одной спеке (проверено по `openspec/`). `contact-suppression` закрывает этот пробел как `ADDED`, не трогая существующие спеки: `MODIFIED` неприменим — capability `lead-pool-client-side`, чьим выходом матрица доменной модели называет `suppression-policy`, ещё не архивирована (`archive-order-and-rule-deltas`).

## Impact

- **Affected bounded contexts:** первичный — `data-governance`; потребители policy-контрактов — `lead-management` и `audience` (`data-zone-policy`), `outreach-orchestration` (`processing-authority`), `message-delivery` (`suppression-policy`); preflight-проверка подавления в `message-delivery` — потребитель ЭТАПА 2, его код этим изменением не меняется.
- **Каталог и модель:** `architecture/domain-catalog.yaml` — `contact-suppression` добавляется в `capabilities` контекста `data-governance`; `docs/domain-model.md` — capability matrix: новая строка `contact-suppression` → `suppression-policy`, у строки `lead-pool-client-side` остаётся `processing-authority`; `docs/system-map.md` — кросс-таблица §5. Состав агрегатов, событий и связей не меняется, диаграммы без изменений.
- **Код:** новые модули `backend/contexts/data-governance` и `contracts/data-governance`; регистрация в `go.work`; замена заглушки сборкой сервиса в `backend/cmd/lead-gen`.
- **Спеки:** новая дельта `specs/contact-suppression/spec.md` (ADDED) в этом изменении.
- **Ограничения:** только синтетические данные; драйвер PostgreSQL и внешние зависимости не добавляются; юридический гейт (`full-enterprise-core-scope`, задача 1.1) этим срезом не заменяется — механика готова, но заключение юриста остаётся обязательным до ЭТАПА 1.
