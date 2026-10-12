# Приёмка входа в консоль платформы

Сквозной тест `TestPlatformConsoleAuthLivePostgres` в
`backend/cmd/lead-gen/platform_console_auth_acceptance_test.go` запускает
production `buildPlatformGovernance`, HTTP-обработчики платформы и реальные
PostgreSQL-адаптеры. `httptest.NewServer` слушает TCP loopback. Build применяет
миграции `platform-governance` и `consoleauth` в созданной специально для теста
child database. Тест создаёт pending-заявки настоящим публичным `POST /register`;
внешние providers, Compose и пользовательские БД не используются.

Тест по умолчанию пропускается. Для выполнения обязательны явное значение
`LEADGEN_PLATFORM_AUTH_ACCEPTANCE=1` и отдельный DSN в
`LEADGEN_PLATFORM_AUTH_ACCEPTANCE_DSN`. DSN обязан подключать к базе
`leadgen_platform_auth_acceptance_admin` в выделенном disposable PostgreSQL 17
кластере. Эта база заранее помечается комментарием
`lead-gen-platform-auth-acceptance-admin-v1`; роль DSN должна иметь `CREATEDB`.
Не направляйте DSN на рабочую, пользовательскую или Compose-базу. Тест проверяет
версию, marker и права перед созданием дочерней базы. Child database получает
уникальное имя и собственный ownership marker. Cleanup регистрируется сразу после
успешного `CREATE DATABASE` и сохраняет доказательство создания именно этим
fixture: точное имя, OID базы и OID владельца. Для удаления все эти значения
должны совпасть; дополнительно должен совпасть marker либо, если `COMMENT` не
удалось установить, in-memory доказательство успешного `CREATE` и проверки
идентичности. После ошибки `COMMENT` тест перепроверяет marker и повторяет его
установку, когда identity всё ещё подтверждена. Чужой marker, изменённый owner/OID
или невозможность проверить состояние запрещают `DROP`.

Перед удалением fixture закрывает runtime pool registry (включая production
pool), затем child pool и проверяет отсутствие соединений к child database через
`pg_stat_activity`. Cleanup защищён mutex; после успешного удаления повторный
вызов ничего не делает. Если `DROP` или другая проверка завершается ошибкой,
parent connection сохраняется для повторной попытки. При невозможности безопасно
удалить база остаётся и тест сообщает её уникальное имя и ожидаемый marker для
ручной проверки/очистки. Это исключение намеренное: предпочтительно явное
сообщение об оставленной disposable базе, а не риск удаления чужой базы; fixture
никогда не удаляет объект без полного доказательства своего создания. Секреты и
значения учётных данных не выводятся.

Пример подготовки **только выделенного тестового кластера** после создания
отдельной базы администратором:

```sql
COMMENT ON DATABASE leadgen_platform_auth_acceptance_admin
  IS 'lead-gen-platform-auth-acceptance-admin-v1';
```

Установите DSN в окружение менеджера секретов/локальной shell-сессии, не
сохраняя его в репозитории или командной строке, затем выполните точную команду:

```sh
LEADGEN_PLATFORM_AUTH_ACCEPTANCE=1 go -C backend/cmd/lead-gen test -run '^TestPlatformConsoleAuthLivePostgres$' -count=1
```

`LEADGEN_PLATFORM_AUTH_ACCEPTANCE_DSN` должен быть уже предоставлен окружением.
Если включён opt-in, а DSN отсутствует, тест завершается ошибкой, а не skip.
Не включайте `-v` для публикации чувствительных runtime-данных; тест сам
проверяет owner UID и `0400` временного внешнего password file, загружает пароль
через production `resolveConsolePassword` и проверяет, что пароль, session
cookies, CSRF, данные форм и synthetic credentials не попали в серверный лог.
