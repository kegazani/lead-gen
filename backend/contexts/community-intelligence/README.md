# community-intelligence

Bounded context `community-intelligence` (architecture/domain-catalog.yaml):
чтение разрешённых Telegram-сообществ и формирование обезличенной выжимки
содержания. Capability — `telegram-community-digest`; классификация —
supporting; сервис и контекст совпадают по идентификатору.

Это рабочий вертикальный срез, а не полная реализация спеки
`openspec/changes/telegram-community-digest`: домен, use case, контракты и роли
собраны и покрыты тестами, а реальный MTProto-источник намеренно оставлен за
портом (см. «Что осталось заглушкой»).

## Домен

- Агрегат `community-digest` (корень `community-digest`) — обезличенная выжимка
  разрешённого сообщества за период.
- Value objects: `community-source` (источник чтения), `digest-period` (границы
  периода), тема `topic` (частота, обезличенные цитаты, ссылки на сообщения).
- Событие: `community-digest-created`.
- Политики: `authorized-community-read` (читаются только разрешённые сообщества;
  бот-источник отвергается), `digest-anonymization` (ниже порога по размеру
  сообщества цитаты не включаются; из цитат вырезаются ссылки, упоминания,
  адреса и телефоны).
- Инварианты: выжимка строится только по разрешённому источнику; без сообщений
  выжимка не создаётся (пустая выжимка с нулями не выдаётся); агрегат структурно
  не содержит авторов и состава участников — обезличивание обеспечено типами,
  а не дисциплиной вызывающего.

Сырое содержание авторов остаётся в красной зоне: порт чтения возвращает
`ReadMessage` с автором, но прикладной слой отделяет автора до входа в домен.

## Use cases

- `command/ReadCommunity` — читает разрешённое сообщество за период, строит
  обезличенную выжимку, сохраняет её и фиксирует `community-digest-created`.
- `query/GetDigest`, `query/ListDigests` — выдача обезличенных представлений;
  выжимка чужого тенанта не раскрывается.

## Порты и адаптеры

Порты: `CommunityReader` (источник под Telegram), `CommunityRegistry` и
`CommunityTracker` (реестр и список отслеживания), `DigestRepository`, `Outbox`,
`CommandQueue`, `Clock`, `IDGenerator`.

Адаптеры в объёме среза — без сети:

- `adapters/telegram.Unavailable` — честно отвечает `ErrSessionRequired`
  (авторизованная сессия не подключена); используется по умолчанию.
- `adapters/telegram.Deterministic` — заданное содержимое, воспроизводимый
  результат; развилка проекта и тестов без MTProto.
- `adapters/persistence/inmemory` — хранилище выжимок и реестр разрешённых
  сообществ (снимки + `Rehydrate`).
- `adapters/queue/inmemory`, `adapters/outbox/inmemory`, `adapters/idgen` —
  служебные адаптеры.

## Контракты

- `contracts/community-intelligence/community-digest` — чтение обезличенной
  выжимки; потребитель — `publishing`.
- `contracts/community-intelligence/community-read-command` — команда чтения
  разрешённого сообщества.

In-proc адаптеры контрактов — публичные фабрики `NewCommunityDigest` и
`NewCommunityReadCommand` в `contract.go` (владелец реализует интерфейс, точка
сборки не видит внутренних пакетов).

## Роли

- `schedule` — регулярный сбор: тикер обходит отслеживаемые сообщества и кладёт
  команды чтения в очередь; первый проход выполняется сразу при старте.
- `worker` — разбирает очередь команд чтения и строит выжимки.

Каталог объявляет только эти роли, поэтому `/healthz` и роль `api` в срез не
добавлены.

## Проверка

```bash
gofmt -l backend/contexts/community-intelligence contracts/community-intelligence
go -C backend/contexts/community-intelligence test -count=1 ./...
go -C backend/contexts/community-intelligence vet ./...
go -C contracts/community-intelligence test -count=1 ./...
go -C tools/importlint run .
go -C tools/archlint run .
```

## Что осталось заглушкой

- Реальный MTProto под пользовательским аккаунтом (сессии, пул аккаунтов,
  ротация, хранение сессий, ограничение частоты, паузы платформы). Порт
  `CommunityReader` и ошибки `ErrSessionRequired`, `ErrRateLimited`,
  `ErrReadPaused`, `ErrCommunityUnavailable` под них готовы; реализация —
  provider-agnostic зависимость этапа `migrate-platform-to-go`.
- Состав участников в красную зону: порт сообщает о доступности состава
  (`ParticipantsAvailable`), но хранилище красной зоны не реализовано.
- Прослеживаемость удалённых сообщений и динамика темы между периодами
  (spec, задачи 6.3 и 7.2).
- Публикация событий в брокер: outbox in-memory, транспорт не подключён.
- Тематизация содержания: темы группируются по совпадающей формулировке
  (нормализованный текст), а не по семантике.
