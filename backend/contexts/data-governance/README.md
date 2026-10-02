# Сервис data-governance — зоны данных, основание обработки и стоп-лист

Сервис контекста `data-governance` (architecture/domain-catalog.yaml). Отвечает за
классификацию зон данных, основание обработки данных тенанта и правила подавления
контактов. Собирает только свой контекст: адаптеры, порты и use cases
`internal/{domain,app,ports,adapters}` соединяются в этом пакете, бизнес-правил
здесь нет.

## Возможности (capabilities)

- `data-handling` → контракт `data-zone-policy`;
- `contact-suppression` → контракт `suppression-policy`;
- `lead-pool-client-side` → контракт `processing-authority`.

## Домен

Агрегат **`processing-governance`** (корень `processing-basis`):

- сущности: `processing-basis`, `suppression-entry`;
- value objects: `processing-purpose`, `retention-period`;
- события: `processing-authority-changed`, `contact-suppressed`;
- политики: `data-zone-policy`, `suppression-policy`.

Инварианты:

1. **I1.** Полномочие действительно только для указанной цели, категории, тенанта
   и периода — проверяется методом `ProcessingBasis.Authorizes` и конструкторами
   value objects.
2. **I2.** Активная запись стоп-листа имеет приоритет над кампанией, решением
   оператора и поставщиком — обеспечивается `ProcessingBasis.IsSuppressed` и
   чистой политикой `domain.SuppressionPolicy`.

Aggregate root инкапсулирует поля, проверяет инварианты в конструкторах и методах
и возвращает domain events; репозиторий существует только для корня, а запись
стоп-листа хранится в составе агрегата.

## Роли и раннеры

Каталог объявляет роли `api`, `worker`, `chains`; на каждую роль собран честный
раннер:

- **api** — внутренний HTTP-сервер (`GET /healthz` и ресурсы ниже). Изменяющие
  операции проверяют право актора через порт `ports.AccessDecision`.
- **worker** — фоновое истечение полномочий: тикер вызывает прикладной сценарий
  `ExpireAuthorities`, который отзывает основания с истёкшим окном действия.
- **chains** — durable-планировщик долгих состояний не реализован; роль честно
  сообщает об этом и не делает вид, что ведёт цепочки.

## HTTP-адаптер роли api

| Метод | Путь | Назначение |
|---|---|---|
| GET | `/healthz` | живость сервиса |
| POST | `/bases` | создать основание обработки |
| GET | `/bases/{basisID}?tenant_id=` | прочитать основание |
| POST | `/bases/{basisID}/authority` | изменить условия основания |
| POST | `/bases/{basisID}/revocation` | отозвать основание |
| POST | `/bases/{basisID}/suppressions` | внести контакт в стоп-лист |
| POST | `/bases/{basisID}/suppressions/{suppressionID}/lift` | снять запись |
| GET | `/zone-policy?tenant_id=&purpose=&category=` | проверить политику зон |
| GET | `/suppressions/check?tenant_id=&contact=` | проверить подавление |

## Опубликованные контракты

Контракты выражены типами в отдельном модуле `contracts/data-governance` и связаны
с реализацией владельца in-proc адаптерами в `contract.go`:

- `datazonepolicy` — `Port.Decide` (решение политики зон);
- `processingauthority` — `Port.Check` (действующее полномочие);
- `suppressionpolicy` — `Port.Check` (решение о подавлении).

Транспорт в каталоге — `async/amqp` (интеграция policy). Сетевой адаптер в объёме
среза не реализован: это точка расширения. При co-location контракт вызывается
прямым вызовом.

## Локальный запуск

```sh
go -C backend/contexts/data-governance test -count=1 ./...
go -C backend/contexts/data-governance vet ./...
```

Без внешней БД сервис поднимается на in-memory репозитории оснований: срез
работает целиком (создание, изменение, стоп-лист, проверки). Изменяющие HTTP-
операции требуют выданного права (пара актор-тенант); по умолчанию доступ закрыт.

## Заглушки среза (осознанные)

- **chains** — планировщик долгих состояний не реализован.
- **worker** — обработчики очередей брокера не зарегистрированы (публикация
  политик по amqp не собрана).
- **access** — in-memory список прав; полная семантика RBAC остаётся за
  `platform-governance` (контракт `access-decision`).
- **транспорт контрактов** — in-proc; сетевой адаптер `async/amqp` не реализован.
