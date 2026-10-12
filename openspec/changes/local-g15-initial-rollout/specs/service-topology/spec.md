## ADDED Requirements

### Requirement: Межконтейнерный release ledger IPC без PID namespace предположений

Release ledger SHALL иметь единственного писателя — holder, который удерживает remote lock и сам открывает запись в release-state. Control client в отдельном container PID namespace SHALL NOT идентифицироваться сравнением peer PID с PID holder; control mutation SHALL требовать ограниченный per-attempt capability, одноразовый challenge, точную deployment/nonce/attempt/revision/action привязку, проверку holder lock и CAS. Capability SHALL поступать только краткоживущим release control clients через отдельный read-only mount; runtime и DB jobs SHALL NOT получать capability или mutation endpoint.

Read-only ledger consumers MAY использовать отдельный read endpoint без capability. Ответ SHALL быть аутентифицирован временным session public key holder и связан с свежим challenge, exact ledger identity/epoch/revision; replay или неверный scope SHALL блокироваться. Holder SHALL оставаться единственным процессом, который изменяет ledger и active release artifacts; gate/stager/client containers SHALL NOT иметь прямой RW доступ к release-state.

Capability bytes и session private key SHALL NOT попадать в argv, ENV, stdout/stderr, logs, ledger, attestation или OpenTofu state. Socket parent/endpoint SHALL иметь точный allowlist, owner/GID/mode и no-symlink проверки; UID/GID checks, lock ownership, allowed state transitions, nonce uniqueness и revision CAS SHALL сохраняться.

#### Scenario: Авторизованная mutation из отдельного container PID namespace

- **WHEN** control client в отдельном PID namespace отправляет корректно HMAC-authenticated request с новым challenge, точной owner attempt/nonce/scope и ожидаемой ledger revision, а holder держит remote lock
- **THEN** holder проверяет request и применяет только допустимый transition через CAS; runner не сравнивает несопоставимые PID namespaces

#### Scenario: Runtime с тем же UID не может мутировать ledger

- **WHEN** intake runtime с UID/GID `65532` имеет release-state и trust только read-only, но не смонтированный per-attempt capability volume, и пытается выполнить write action
- **THEN** mutation отвергается без изменения ledger revision/active state; runtime может только проверить signed read response

#### Scenario: Replay control request или read response

- **WHEN** повторно предъявлен использованный challenge, старый holder epoch, другой nonce/attempt либо устаревшая expected revision
- **THEN** holder/client отвергает request/response, ничего не записывает и не выдает stale snapshot как актуальный

#### Scenario: Holder lock утрачён или IPC path изменён

- **WHEN** holder больше не владеет удалённым flock, socket/parent заменён, или owner/GID/mode/path отличаются от pinned identity
- **THEN** control/read IPC завершается fail-closed; прямой файл ledger недоступен и stale lock не снимается автоматически

#### Scenario: Capability или signing material попадает в диагностический вывод

- **WHEN** IPC helper формирует error, attestation, ledger record или log entry
- **THEN** capability bytes, HMAC, private key и DSN/credential values не выводятся и не сохраняются; допустимы только несекретные key/attempt IDs и sanitized status
