# platform/workflow

Platform-primitive рантайма durable-исполнения на Temporal. Пакет **не** является
bounded context: он не владеет доменной моделью, не объявляется в
`architecture/domain-catalog.yaml` и не знает контекстов. Поведение (шаги
фулфилмента, паузы, ветвления) задаёт owning context.

## Что внутри

| Файл | Назначение |
|---|---|
| `config.go` | параметры подключения (адрес, namespace, ключ, задача) |
| `client.go` | клиент Temporal: запуск, описание, query; метрики запусков |
| `worker.go` | host воркера: регистрация и запуск очереди задач |
| `registry.go` | реестр workflow/activities сервиса |
| `payload.go` | правило payload-гигиены и проверка `CheckIdentifiersOnly` |

## Payload-гигиена (правило и проверка на ревью)

В аргументы и результаты workflow/activities допускаются **только идентификаторы,
коды и корреляционные ссылки**. Сырые PII и секреты в payload не попадают: они
живут в owning context и передаются по `id`. Правило согласовано с `data-governance`
(красные данные не поднимаются) и `integration-platform` (секреты не в событиях).

Запрещены:

- поля, чьё имя указывает на PII или секрет (`email`, `phone`, `name`, `address`,
  `passport`, `ssn`, `birth`, `token`, `secret`, `password`, `api_key`, `iban`,
  `card`, `credential`);
- непрозрачные типы в payload: `map`, `any`, `[]byte`, `func`, `chan` — они могут
  спрятать что угодно;
- свободный пользовательский текст. Если текст нужен, он несёт короткий
  контролируемый код, а не содержимое оффера или письма.

Разрешены: именованные идентификаторы (`RequestID`, `TenantID`, …), числа, булевы
значения, `time.Time`, срезы и вложенные структуры из тех же допустимых типов.

### Проверка

Проверка — детерминированная и машинная (`CheckIdentifiersOnly`), а не «на глаз»:

```sh
go -C backend/platform test ./workflow/...                                   # правило
go -C backend/contexts/request-fulfillment test ./internal/adapters/workflow/...  # payload контекста
```

Первый прогон проверяет сам чекер на корректных и запрещённых типах; второй —
что **все** payload-типы контекста `request-fulfillment` проходят правило
(`TestPayloadsCarryIdentifiersOnly`). Новый payload-тип обязан быть добавлен в
этот тест — это и есть ревью-чек при PR.

### Чек-лист ревью

- [ ] новый/изменённый payload-тип добавлен в `TestPayloadsCarryIdentifiersOnly`;
- [ ] в payload нет полей с PII/секретами и непрозрачных типов;
- [ ] содержание оффера, письма или контакта не поднимается в payload — только `id`;
- [ ] изменение структуры workflow-графа оформлено версионированием
      (`workflow.GetVersion`), чтобы бегущие исполнения не ломались.
