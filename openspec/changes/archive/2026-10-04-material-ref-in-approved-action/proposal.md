## Why

В реальном продуктовом пути `approve → outbox → relay` (`outreach-orchestration`)
исходящее письмо уходит с пустыми темой и телом: отображение `relay.MapActionApproved`
не заполняет `MaterialVersionRef` в контракте `approved-action`, потому что доменное
событие `action-approved` не несёт ссылку на материал утверждённого черновика. Материал
при этом разрешим (`content-production`, контракт `content-version`), и ветка материала
в мосте доставки рабочая — недостижима она только на основном пути. Сквозной E2E волны 3
зафиксировал это как честную находку. Пока ссылка не проставляется, «утверждено →
отправлено» отправляет пустое письмо — то есть продуктовая функция не работает.

## What Changes

- Доменное событие `action-approved` (агрегат `campaign`, контекст
  `outreach-orchestration`) начинает несть **ссылку на версию материала** утверждённого
  черновика: при положительном решении событие заполняет материал из состава черновика
  (`DraftSpec.Material`), к точной версии которого относится решение.
- Отображение `relay.MapActionApproved` заполняет `approvedaction.ApprovedAction.MaterialVersionRef`
  из события, поэтому на пути `approve → outbox → relay` материал письма становится
  разрешимым (`asset_id@version_id`).
- Проводимая форма события `action-approved` в outbox (PostgreSQL-адаптер) расширяется
  необязательным полем материала — аддитивно, совместимо со старыми записями.
- Сквозной in-process E2E основного пути (`backend/cmd/lead-gen/e2e_approved_to_sent_test.go`)
  перестаёт утверждать пустую тему: основной путь даёт непустые тему и тело из материала.
- Выравнивается стейл-ожидание топологического теста `backend/platform/deployment`
  (число связей) с каноническим каталогом — без этого гейт `make check` красный (не связано
  с поведением, только приведение теста к источнику истины).
- **Не входит:** изменение состава контракта `approved-action` (поле
  `MaterialVersionRef` уже существует и опционально — версия схемы не меняется),
  сетевой транспорт контракта, изменение владения материалом (`content-production`).

## Capabilities

### New Capabilities

- `outreach-approval`: утверждённое действие, публикуемое в `approved-action`, несёт
  ссылку на версию материала утверждённого черновика; без неё материал письма на
  продуктовом пути не разрешается.

### Modified Capabilities

Нет. Состав контракта `approved-action` не меняется (поле уже объявлено), требования
`delivery-bridge` и `outreach-channel-email` не меняются — мост и отправка уже умеют
разрешать заданную ссылку.

## Impact

**Affected bounded contexts:** `outreach-orchestration` (владелец события `action-approved`,
отображения relay и outbox-кодека). Контракт `approved-action` принадлежит тому же
контексту и по составу полей не меняется. `message-delivery`/`content-production`
затрагиваются только как потребители уже существующей ссылки — их требования и код не
меняются.

**Доменная модель.** Capability `outreach-approval` уже объявлена каталогом за контекстом
`outreach-orchestration`; владение, агрегаты, связи и policy не меняются. Событие
`action-approved` дополняется полем-ссылкой на материал — это уточнение состава события,
а не новый агрегат/связь. `architecture/domain-catalog.yaml` не меняется; при наличии
описания полей события в `docs/domain-model.md` оно уточняется в этом же change.

**Код.** `backend/contexts/outreach-orchestration/internal/domain/events.go` (состав
`ActionApproved`), `internal/domain/campaign.go` (заполнение при утверждении),
`internal/adapters/outbox/relay/relay.go` (отображение), `internal/adapters/persistence/postgres/events.go`
(проводимая форма outbox), `backend/cmd/lead-gen/e2e_approved_to_sent_test.go` (приёмка
основного пути).
