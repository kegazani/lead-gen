# Контекст `integration-platform`

Go-модуль `lead-gen/backend/contexts/integration-platform` — реализация bounded
context `integration-platform` из `architecture/domain-catalog.yaml`. Каталог
назван так же, как контекст и сервис каталога; имя Go-пакета —
`integrationplatform` (идентификатор пакета не может содержать дефис).

## Соответствие контексту

| Каталог | Слой | Содержимое |
|---|---|---|
| корень модуля | сборка сервиса | `ServiceID = "integration-platform"`, `New(Config)`, раннеры ролей, конструкторы адаптеров опубликованных контрактов |
| `internal/domain` | домен | агрегаты `adapter-registration` и `integration-invocation`, VO `manifest-version` и `correlation-ref`, domain events, политики `adapter-selection` и `error-normalization` |
| `internal/app` | приложение | use cases `RegisterAdapter`, `SelectAdapter`, `InvokeIntegration` |
| `internal/ports` | порты | `ProviderPort`, репозитории регистраций и вызовов, реэкспорт контрактов в `contracts/` |
| `internal/ports/contracttest` | контрактные тесты | общий contract suite портов |
| `internal/adapters` | адаптеры | in-memory репозитории, тестовый адаптер провайдера, адаптеры `firecrawl`/`openai`/`forum`/`smtp` опубликованных контрактов |

Публикуемые контракты контекста (`provider-port`, `adapter-capability`,
`integration-error`) — отдельный модуль `lead-gen/contracts/integration-platform`.
Потребители импортируют контракт и не видят SDK поставщика.

## Границы

`domain` не импортирует `app`, `ports`, `adapters`, другие контексты и
`backend/platform`. `app` видит свой `domain`, свои `ports` и опубликованные
контракты. `ports` видит контракты и свой `domain`. SDK поставщиков и драйверы
хранилищ допустимы только в `adapters`. Границы проверяет
`go -C tools/importlint run .`.

## Объём среза

В срезе реализованы доменные агрегаты, use cases, порты и адаптеры. Хранилища —
только in-memory (драйвер PostgreSQL отсутствует); связи с поставщиками
проверяются на тестовом адаптере. PostgreSQL-адаптеры прогонятся тем же contract
suite, когда появится драйвер.

## Проверка

```sh
go test ./backend/contexts/integration-platform/...
go -C tools/importlint run .
go -C tools/archlint run .
```
