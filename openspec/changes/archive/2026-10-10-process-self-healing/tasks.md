# tasks.md — process-self-healing

Порядок: супервизор роли → бюджет → трактовка `nil` → внешний liveness →
гигиена каталога → трассировка паники → гейт. Статус задачи меняется (`- [ ]` →
`- [x]`) только по выполненному критерию верификации (§4 AGENTS.md). Пока на
пути задачи стоит заглушка, задача не закрывается (§3 AGENTS.md).

## 1. Перехват паники на границе роли

- [x] 1.1 В `backend/cmd/lead-gen/supervisor.go` обернуть исполнение роли
  (`runner.Run(ctx)`) в `recover`: паника перехватывается в goroutine супервизора
  и превращается в ошибку, идущую в существующий backoff-рестарт; процесс и
  соседние роли не падают. Верификация: `go test ./backend/cmd/lead-gen/...`
  — тест `TestRoleSupervisorRecoversPanic`: паникующая роль перезапускается, а
  параллельная роль продолжает выполняться.
- [x] 1.2 Уточнить контракт `RoleRunner` в
  `backend/platform/composition/runner.go`: паника на границе исполнения роли
  обрабатывается супервизором, а не пробрасывается в процесс. Верификация:
  `go build ./...`, комментарий-контракт согласован с реализацией.

## 2. Ограниченный бюджет перезапуска роли

- [x] 2.1 В `roleSupervisor` ввести лимит попыток (`roleRestartMaxAttempts`) и
  накопленный бюджет времени (`roleRestartBudget`); по исчерпании любого из
  лимитов `run` прекращает рестарт и возвращает ошибку. Верификация:
  unit-тест `TestRoleSupervisorStopsAfterBudget` с подменённым `sleep` —
  мгновенно падающая роль после бюджета не перезапускается, `run` возвращает
  ошибку.
- [x] 2.2 В `backend/cmd/lead-gen/main.go` довести ошибку супервизора до
  завершения процесса: `startRoles`/`startSelectedServices` возвращают ошибку,
  `main` логирует её и вызывает `os.Exit(1)`, чтобы Docker
  (`restart: unless-stopped`) поднял свежий контейнер. Верификация: тест
  `TestStartSelectedServicesPropagatesBudgetError` либо `go build ./...` +
  ручная проверка, что исчерпание бюджета приводит к ненулевому коду выхода.
- [x] 2.3 Вынести лимит попыток и бюджет в флаги точки входа с безопасными
  значениями по умолчанию (не бесконечность). Верификация: `go test` точки
  входа зелёный, значение по умолчанию отлично от бесконечности.

## 3. Роль, завершившаяся при живом контексте

- [x] 3.1 Роли-циклы (`worker`, `chains`, `schedule`, `sender`) при возврате
  `nil` и живом `ctx` трактовать как сбой и перезапускать с backoff в пределах
  бюджета. Верификация: тест `TestRoleSupervisorRestartsNilOnLiveContext`.
- [x] 3.2 Серверную роль `api` при возврате `nil` и живом `ctx` эскалировать:
  запись `Error` в лог и метрика (`metrics.Incr`), без тихой остановки.
  Верификация: тест `TestRoleSupervisorEscalatesAPIExit` — счётчик метрики
  увеличился, в лог записан Level=Error.
- [x] 3.3 Обновить комментарий-контракт `RoleRunner.Run` в `runner.go`: `nil`
  допустим только при отменённом контексте. Верификация: `go build ./...`.

## 4. Внешняя liveness-проба прикладных контейнеров

- [x] 4.1 Реализовать liveness уровня процесса: отдельный http-слушатель в
  `backend/cmd/lead-gen/main.go` (не роль) и режим пробы `-healthcheck` в том же
  статическом бинарнике (distroless не даёт shell/curl) с таймаутом. Проба
  отвечает кодом 0/1 и не проверяет здоровье отдельных ролей. Верификация:
  unit-тест пробы (таймаут → ненулевой код; живой слушатель → 0).
- [x] 4.2 Добавить `healthcheck` в общий якорь `x-app` в `compose.yaml`:
  команда-проба бинарника, `interval`/`timeout`/`retries`/`start_period`; не
  использовать `/readyz`. Верификация: `docker compose config` без ошибок,
  `healthcheck` виден у всех прикладных сервисов, наследующих `x-app`.
- [x] 4.3 Добавить параметры `healthcheck` в `deploy/tofu/modules/app/main.tf`
  (`docker_container.service`) и переменные модуля в
  `deploy/tofu/modules/app/variables.tf`. Верификация: `tofu fmt -check` и
  `tofu validate` модуля зелёные.
- [x] 4.4 Обновить `deploy/README.md` (liveness vs `/readyz`, поведение при
  зависании). Верификация: документация согласована с `compose.yaml`.

## 5. Гигиена каталога `deploy/services.yaml`

- [x] 5.1 У `analytics` объявлена роль `schedule`, но не зарегистрирована в
  `RoleRunner` (`backend/contexts/analytics/service.go`). Выбрать одно: убрать
  роль из каталога либо реализовать и зарегистрировать её. Верификация: старт
  соответствующей группы не логирует «роль не зарегистрирована»; каталог и
  регистрация совпадают. Обновить комментарий в `deploy/services.yaml`.

## 6. Трассировка паники в лог

- [x] 6.1 Логировать панику роли с трассировкой стека (`runtime/debug.Stack()`)
  и полями `process`/`service`/`role`. Верификация: тест `TestRoleSupervisorLogsPanicStack`
  — запись лога содержит трассировку стека паники, а не только сообщение.

## 7. Финальный гейт

- [x] 7.1 Прогнать `make check` (build / test / vet / importlint / archlint /
  openspec). Верификация: команда завершается кодом 0.
- [x] 7.2 `openspec validate process-self-healing --strict` и
  `go -C tools/archlint run .`. Верификация: обе команды завершаются кодом 0;
  на пути задач нет заглушек (§3 AGENTS.md).

## 8. Исправления замечаний ревью (двойное ревью + тесты)

- [x] 8.1 **БЛОКЕР** — кумулятивный бюджет перезапуска. `attempt`/`budgetStart`
  накапливались за всю жизнь процесса и не сбрасывались после успешного
  восстановления роли. Введён порог `roleRestartHealthyThreshold`: роль,
  проработавшая дольше порога до сбоя, «прощает» прежние сбои — бюджет считается
  за эпизод сбоя, а не за жизнь. Тест
  `TestRoleSupervisorScatteredFailuresDoNotExhaustBudget` (разрозненные сбои с
  длинной успешной работой между ними не исчерпывают бюджет).
- [x] 8.2 **БЛОКЕР** — каталог. У `analytics` в
  `architecture/domain-catalog.yaml` снята роль `schedule` (`["api", "worker"]`);
  `docs/domain-model.md` обновлён (`campaign-reporting`, `client-dashboard`,
  `lead-quality-insights`). Каталог и регистрация `RoleRunner` совпадают.
- [x] 8.3 Семантика лимита. Убрана принудительная подстановка `<=0` на дефолт в
  `newRoleSupervisor`: неположительное значение честно отключает лимит (только
  для тестов), ветки `> 0` в `restartExhausted` больше не мёртвый код;
  комментарии приведены в соответствие с design (лимиты конечны в точке входа).
- [x] 8.4 Удалён неиспользуемый `isCycleRole` (`supervisor.go`).
- [x] 8.5 `spec.md`/`design.md`/`proposal.md` дополнены серверной ролью `ingest`
  наравне с `api` (эскалируется, не рестартует мгновенно).
- [x] 8.6 Зафиксировано известное ограничение: паника в под-горутинах роли
  (`runAPI` → goroutine `Shutdown`, `runWorker` → `sink.Run` и т.п.) вне
  перехвата `recover` — задокументировано в design.md `## Risks / Trade-offs` и
  уточнено в spec (гарантия — для прямой паники в теле роли).
- [x] 8.7 Liveness-слушатель: дефолт сменён на `127.0.0.1:8091` (loopback) в
  `main.go` и `compose.yaml`; healthcheck уже ходит на 127.0.0.1.
- [x] 8.8 Ошибка `ListenAndServe` liveness-слушателя фатальна: синхронная
  привязка порта, ошибка bind доводится до `os.Exit(1)` в `main`.
- [x] 8.9 Убран дубль логирования паники: паника логируется одним сообщением
  (поле `stack` в общем пути перезапуска), а не отдельно в `runOnce` и повторно
  при рестарте.

## 9. Исправление решения судьи: автовосстановление — самозавершение (Вариант C)

- [x] 9.1 Убрано ложное утверждение «зависший процесс перезапускается
  оркестратором по `restart: unless-stopped`»: `unless-stopped` реагирует только
  на выход процесса, а не на `unhealthy`. Поправлены `spec.md`/`design.md`/
  `proposal.md`/`deploy/README.md` и комментарий `deploy/tofu/modules/app/main.tf`.
- [x] 9.2 Реализован процессный сторож самозавершения (`backend/cmd/lead-gen/self_healing.go`):
  самопроверка собственного `/livez` по `-liveness-addr` с таймаутом; при N
  подряд неудач (`-self-heal-threshold`, N > 1) — graceful-останов и ненулевой
  выход, свежий контейнер поднимает `restart: unless-stopped`. Флаги
  `-self-heal-interval`/`-self-heal-timeout`/`-self-heal-threshold`/
  `-self-heal-start-period`; `0` отключает сторожа. Метрика
  `process_self_terminated_total`; причина логируется. Верификация: тесты
  `TestLivenessWatchdog*` + `go test ./backend/cmd/lead-gen/... -race`.
- [x] 9.3 Зафиксирован остаточный зазор: полная заморозка runtime (сторож и
  liveness-слушатель не выполняются) детектится, но не перезапускается — честно
  отмечен как нерешённый в `spec.md`/`design.md`/`deploy/README.md`.

## 10. Финальная полировка по итогам ревью

- [x] 10.1 **Несоответствие graceful-останов ↔ `os.Exit(1)`.** Ветка
  `selfTerminate` в `main.go` выполняет корректный останов: `cancel()` + ожидание
  `wg.Wait()` не дольше `-shutdown-timeout` (путь вынесен в `drainRoles`, общий со
  штатным остановом по сигналу), затем `os.Exit(1)`. `spec.md`/`design.md`/
  `deploy/README.md` уточнены: graceful-останов означает ограниченный по времени
  drain ролей. Верификация: `go build`, `go test ./backend/cmd/lead-gen/... -race`,
  `make check`.
- [x] 10.2 **Footgun `-self-heal-timeout <= 0`.** В `startSelfHealing` таймаут
  клампится к дефолту при неположительном значении (а не отключает сторожа и не
  даёт мгновенно отменённый контекст каждой пробе). Задокументировано в
  `design.md`/`deploy/README.md`/`spec.md`, обновлён help флага. Тест
  `TestStartSelfHealingClampsNonPositiveTimeout`. Верификация: тест зелёный.
- [x] 10.3 Покрытие `startPeriod` тестом
  `TestLivenessWatchdogStartPeriodDelaysFirstProbe`: первая самопроверка — только
  после стартового периода. Верификация: тест зелёный.
- [x] 10.4 Задокументировано, что `-self-heal-threshold` по умолчанию 3 (N > 1), а
  значение 1 допустимо, но не рекомендуется (ослабляет защиту от единичного сбоя).
  Верификация: формулировки в `design.md`/`deploy/README.md`/`spec.md` согласованы.
