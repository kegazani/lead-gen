#!/usr/bin/env bash
set -Eeuo pipefail
set +x
umask 077

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
STATE="${TMPDIR:-/var/folders/xv/pq9h5rt5717gr_vbvl9m8js80000gn/T/opencode}/local-current-request-crawl"
PG_NAME=leadgen-lccrc-pg17
RABBIT_NAME=leadgen-lccrc-rabbit
TEMPORAL_NAME=leadgen-lccrc-temporal
TEMPORAL_PG_NAME=leadgen-lccrc-temporal-pg17
APP_NAME=leadgen-lccrc-app
PROXY_NAME=leadgen-lccrc-operator-proxy
PG_VOLUME=leadgen-lccrc-pg17-data
TEMPORAL_PG_VOLUME=leadgen-lccrc-temporal-pg17-data
SECRET_VOLUME=leadgen-lccrc-console-secret
NETWORK_NAME=leadgen-lccrc-runtime
APP_IMAGE=leadgen:lccrc-current-request-crawl
NAMESPACE=local-current-request-crawl
TASK_QUEUE=local-current-request-crawl
APP_BASE=29200
PROXY_PORT=29180
PG_PORT=29186
RABBIT_PORT=29187
TEMPORAL_PORT=29188
LABEL_KEY=com.leadgen.dev-harness
LABEL_VALUE=local-compose-current-request-crawl
LABEL="$LABEL_KEY=$LABEL_VALUE"
CONSOLE_PASSWORD_FILE="${CONSOLE_PASSWORD_FILE:-$ROOT/deploy/tofu/.secrets/console-password}"
MIGRATION_ROOT=""
MIGRATION_DIGEST=""
PROXY_CONFIG_DIGEST=""

mkdir -p "$STATE/logs"
chmod 700 "$STATE" "$STATE/logs"

say() { printf '%s\n' "$*"; }
fail() { say "Блокер: $*" >&2; exit 1; }

container_exists() { docker inspect "$1" >/dev/null 2>&1; }

check_container_owner() {
    local name="$1" owner
    owner="$(docker inspect --format "{{index .Config.Labels \"$LABEL_KEY\"}}" "$name" 2>/dev/null || true)"
    [[ "$owner" == "$LABEL_VALUE" ]] || fail "контейнер $name существует, но не принадлежит этому harness; не изменяю его"
}

check_volume_owner() {
    local name="$1" owner
    if docker volume inspect "$name" >/dev/null 2>&1; then
        owner="$(docker volume inspect --format "{{index .Labels \"$LABEL_KEY\"}}" "$name")"
        [[ "$owner" == "$LABEL_VALUE" ]] || fail "volume $name существует, но не принадлежит этому harness; не изменяю его"
    else
        docker volume create --label "$LABEL" "$name" >/dev/null
    fi
}

check_network_owner() {
    local owner
    if docker network inspect "$NETWORK_NAME" >/dev/null 2>&1; then
        owner="$(docker network inspect --format "{{index .Labels \"$LABEL_KEY\"}}" "$NETWORK_NAME")"
        [[ "$owner" == "$LABEL_VALUE" ]] || fail "network $NETWORK_NAME существует, но не принадлежит этому harness; не изменяю её"
    else
        docker network create --label "$LABEL" "$NETWORK_NAME" >/dev/null
    fi
}

container_is_running() {
    [[ "$(docker inspect --format '{{.State.Running}}' "$1" 2>/dev/null || true)" == true ]]
}

ensure_started_or_new() {
    local name="$1"
    shift
    if container_exists "$name"; then
        check_container_owner "$name"
        if ! container_is_running "$name"; then
            docker start "$name" >/dev/null || fail "не удалось возобновить принадлежащий harness контейнер $name"
        fi
        return
    fi
    "$@"
}

port_is_available_or_ours() {
    local port="$1" owner_container="$2" container_port="$3" mapped
    if ! lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
        return
    fi
    if container_exists "$owner_container"; then
        check_container_owner "$owner_container"
        mapped="$(docker port "$owner_container" "$container_port" 2>/dev/null || true)"
        if [[ "$mapped" == *":$port" ]]; then
            return
        fi
    fi
    fail "порт $port занят посторонним процессом; его не останавливаю"
}

ports_available() {
    port_is_available_or_ours "$PROXY_PORT" "$PROXY_NAME" 8000/tcp
    port_is_available_or_ours "$PG_PORT" "$PG_NAME" 5432/tcp
    port_is_available_or_ours "$RABBIT_PORT" "$RABBIT_NAME" 5672/tcp
    port_is_available_or_ours "$TEMPORAL_PORT" "$TEMPORAL_NAME" 7233/tcp
}

prepare_operator_secret() {
    local generation secret_dir marker
    [[ -r "$CONSOLE_PASSWORD_FILE" ]] || fail "недоступен существующий console-password file"
    generation="${LEADGEN_SECRET_GENERATION:-${SECRET_GENERATION:-}}"
    [[ -n "$generation" && "$generation" =~ ^[A-Za-z0-9_-]{1,64}$ ]] || fail "не задано допустимое generation для harness-owned secret bundle"
    secret_dir="$(dirname "$CONSOLE_PASSWORD_FILE")"
    if [[ "$(basename "$secret_dir")" == "$generation" && -r "$secret_dir/generation" ]]; then
        IFS= read -r marker <"$secret_dir/generation" || true
        if [[ "$marker" == "$generation" ]]; then
            # Уже staged read-only bundle: используем ровно его, не создавая копию.
            printf '%s\n' "$generation" >"$STATE/generation.id"
            chmod 600 "$STATE/generation.id"
            export LEADGEN_SECRET_GENERATION="$generation"
            return
        fi
    fi

    # Переводим только существующий console-password через штатный stage-secrets
    # в отдельный harness-owned named volume. Исходный каталог монтируется ro;
    # manifest/trust key/release-state не входят в allowlist bundle platform.
    local harness_generation
    if [[ -f "$STATE/generation.id" ]]; then
        IFS= read -r harness_generation <"$STATE/generation.id" || true
    else
        harness_generation="lccrc-dev-$(date -u +%Y%m%dT%H%M%SZ)-$(python3 -c 'import secrets; print(secrets.token_hex(4))')"
        printf '%s\n' "$harness_generation" >"$STATE/generation.id"
        chmod 600 "$STATE/generation.id"
    fi
    [[ "$harness_generation" =~ ^[A-Za-z0-9_-]{1,64}$ ]] || fail "локальный generation metadata повреждён; файлы и ресурсы сохранены"
    export LEADGEN_SECRET_GENERATION="$harness_generation"
}

prepare_private_configuration() {
    [[ -r "$ROOT/.env" ]] || fail "отсутствует локальный .env с provider credentials"
    [[ -r "$CONSOLE_PASSWORD_FILE" ]] || fail "недоступен существующий console-password file"

    set -a
    # Читаем локальную конфигурацию без отображения её значений; не меняем .env.
    # shellcheck disable=SC1090
    source "$ROOT/.env"
    set +a
    [[ -n "${FIRECRAWL_API_KEY:-}" && -n "${LLM_API_KEY:-}" ]] || fail "в локальном .env отсутствует Firecrawl или LLM provider key"
    [[ -n "${CONSOLE_USER:-}" ]] || fail "в локальном .env отсутствует операторский login; auth не ослабляется"

    : "${EMBEDDING_BASE_URL:=http://host.docker.internal:11435}"
    : "${EMBEDDING_MODEL:=bge-m3}"
    : "${EMBEDDING_KEY:=local}"
    export EMBEDDING_BASE_URL EMBEDDING_MODEL EMBEDDING_KEY
    prepare_operator_secret

    if [[ -d "$STATE/credentials" && ! -f "$STATE/credentials.ready" ]]; then
        private_credentials_complete "$STATE/credentials" \
            || fail "приватный credential bundle неполон; данные/ресурсы не удалены"
        : >"$STATE/credentials.ready"
        chmod 600 "$STATE/credentials.ready"
    fi
    if [[ ! -f "$STATE/credentials.ready" ]]; then
        if [[ -e "$STATE/credentials.partial" ]]; then
            no_runtime_resources_created || fail "неполная credential подготовка при существующих runtime resources; ничего не удалено"
            private_credentials_complete "$STATE/credentials.partial" || discard_own_incomplete_credentials
            if [[ -d "$STATE/credentials.partial" ]]; then
                chmod 600 "$STATE/credentials.partial"/*
                mv "$STATE/credentials.partial" "$STATE/credentials"
            fi
        fi
    fi
    if [[ ! -f "$STATE/credentials.ready" ]]; then
        mkdir "$STATE/credentials.partial"
        chmod 700 "$STATE/credentials.partial"
        local pg_password rabbit_password temporal_password
        pg_password="$(python3 -c 'import secrets; print(secrets.token_urlsafe(32))')"
        rabbit_password="$(python3 -c 'import secrets; print(secrets.token_urlsafe(32))')"
        temporal_password="$(python3 -c 'import secrets; print(secrets.token_urlsafe(32))')"
        {
            printf 'POSTGRES_USER=leadgen_lccrc\n'
            printf 'POSTGRES_PASSWORD=%s\n' "$pg_password"
            printf 'POSTGRES_DB=lead_gen\n'
        } >"$STATE/credentials.partial/postgres.env"
        {
            printf 'RABBITMQ_DEFAULT_USER=leadgen_lccrc\n'
            printf 'RABBITMQ_DEFAULT_PASS=%s\n' "$rabbit_password"
        } >"$STATE/credentials.partial/rabbitmq.env"
        {
            printf 'POSTGRES_USER=temporal_lccrc\n'
            printf 'POSTGRES_PASSWORD=%s\n' "$temporal_password"
            printf 'POSTGRES_DB=temporal\n'
        } >"$STATE/credentials.partial/temporal-postgres.env"
        {
            printf 'DB=postgres12\nDB_PORT=5432\nPOSTGRES_USER=temporal_lccrc\n'
            printf 'POSTGRES_PWD=%s\nPOSTGRES_SEEDS=%s\n' "$temporal_password" "$TEMPORAL_PG_NAME"
            printf 'DEFAULT_NAMESPACE=%s\nBIND_ON_IP=0.0.0.0\n' "$NAMESPACE"
        } >"$STATE/credentials.partial/temporal.env"
        python3 - "$STATE/credentials.partial" "$PG_PORT" "$RABBIT_PORT" <<'PY'
import pathlib, sys, urllib.parse
directory = pathlib.Path(sys.argv[1])
pg_port, rabbit_port = sys.argv[2:]
def read_env(path):
    return dict(line.split("=", 1) for line in path.read_text().splitlines())
pg = read_env(directory / "postgres.env")
rabbit = read_env(directory / "rabbitmq.env")
user = urllib.parse.quote(pg["POSTGRES_USER"], safe="")
password = urllib.parse.quote(pg["POSTGRES_PASSWORD"], safe="")
ruser = urllib.parse.quote(rabbit["RABBITMQ_DEFAULT_USER"], safe="")
rpass = urllib.parse.quote(rabbit["RABBITMQ_DEFAULT_PASS"], safe="")
(directory / "green.dsn").write_text(f"postgres://{user}:{password}@127.0.0.1:{pg_port}/lead_gen?sslmode=disable\n")
(directory / "red.dsn").write_text(f"postgres://{user}:{password}@127.0.0.1:{pg_port}/lead_gen_red?sslmode=disable\n")
PY
        # DSN файлов тестового verifier должны указывать на published loopback PG;
        # runtime app DSNs заменяются на внутренние сетевые имена ниже.
        chmod 600 "$STATE/credentials.partial"/*
        mv "$STATE/credentials.partial" "$STATE/credentials"
        : >"$STATE/credentials.ready"
        chmod 600 "$STATE/credentials.ready"
    fi

    private_credentials_complete "$STATE/credentials" \
        || fail "сохранённый private credential bundle неполон; runtime resources не очищены"

    [[ -f "$STATE/app.env" ]] && return
    local config="$STATE/app.env.tmp"
    python3 - "$STATE/credentials" "$config" <<'PY'
import os, pathlib, sys
directory, output = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
def read_env(path):
    return dict(line.split("=", 1) for line in path.read_text().splitlines())
pg = read_env(directory / "postgres.env")
rabbit = read_env(directory / "rabbitmq.env")
user, password = pg["POSTGRES_USER"], pg["POSTGRES_PASSWORD"]
ruser, rpass = rabbit["RABBITMQ_DEFAULT_USER"], rabbit["RABBITMQ_DEFAULT_PASS"]
values = {
    "LEADGEN_LEAD_GREEN_DSN": f"postgres://{user}:{password}@leadgen-lccrc-pg17:5432/lead_gen?sslmode=disable",
    "LEADGEN_LEAD_RED_DSN": f"postgres://{user}:{password}@leadgen-lccrc-pg17:5432/lead_gen_red?sslmode=disable",
    "LEADGEN_REQUEST_GREEN_DSN": f"postgres://{user}:{password}@leadgen-lccrc-pg17:5432/lead_gen?sslmode=disable",
    "LEADGEN_SUPPORT_GREEN_DSN": f"postgres://{user}:{password}@leadgen-lccrc-pg17:5432/lead_gen?sslmode=disable",
    "LEADGEN_BROKER_URL": f"amqp://{ruser}:{rpass}@leadgen-lccrc-rabbit:5672/",
    "LEADGEN_SECRET_GENERATION": os.environ["LEADGEN_SECRET_GENERATION"],
    "LEADGEN_REQUEST_EXECUTIONS_URL": "http://leadgen-lccrc-app:29201",
    "FIRECRAWL_API_KEY": os.environ["FIRECRAWL_API_KEY"],
    "LLM_API_KEY": os.environ["LLM_API_KEY"],
    "EMBEDDING_KEY": os.environ["EMBEDDING_KEY"],
    "EMBEDDING_BASE_URL": os.environ["EMBEDDING_BASE_URL"],
    "EMBEDDING_MODEL": os.environ["EMBEDDING_MODEL"],
}
for name in ("FIRECRAWL_BASE_URL", "LLM_BASE_URL", "LLM_MODEL", "CONSOLE_USER"):
    if os.environ.get(name):
        values[name] = os.environ[name]
for key, value in values.items():
    if "\n" in value or "\r" in value:
        raise SystemExit(1)
output.write_text("".join(f"{key}={value}\n" for key, value in values.items()))
PY
    chmod 600 "$config"
    mv "$config" "$STATE/app.env"
}

private_credentials_complete() {
    local directory="$1" name
    for name in postgres.env rabbitmq.env temporal-postgres.env temporal.env green.dsn red.dsn; do
        [[ -f "$directory/$name" && ! -L "$directory/$name" && -s "$directory/$name" ]] || return 1
    done
}

no_runtime_resources_created() {
    local name
    for name in "$PG_NAME" "$RABBIT_NAME" "$TEMPORAL_NAME" "$TEMPORAL_PG_NAME" "$APP_NAME" "$PROXY_NAME"; do
        if container_exists "$name"; then
            check_container_owner "$name"
            return 1
        fi
    done
    for name in "$PG_VOLUME" "$TEMPORAL_PG_VOLUME" "$SECRET_VOLUME"; do
        docker volume inspect "$name" >/dev/null 2>&1 && return 1
    done
    docker network inspect "$NETWORK_NAME" >/dev/null 2>&1 && return 1
    return 0
}

discard_own_incomplete_credentials() {
    local path entry name
    for entry in "$STATE/credentials.partial"/*; do
        [[ -e "$entry" ]] || continue
        [[ -f "$entry" && ! -L "$entry" ]] || fail "неожиданный объект в harness-owned incomplete credentials; сохраняю его"
        name="${entry##*/}"
        case "$name" in
            postgres.env|rabbitmq.env|temporal-postgres.env|temporal.env|green.dsn|red.dsn) rm -- "$entry" ;;
            *) fail "неожиданный файл в harness-owned incomplete credentials; сохраняю его" ;;
        esac
    done
    rmdir "$STATE/credentials.partial" || fail "не удалось безопасно закрыть только собственный incomplete credential directory"
}

prepare_migration_snapshot() {
    local digest snapshot temporary
    digest="$(python3 - "$ROOT" <<'PY'
import hashlib, pathlib, sys
root = pathlib.Path(sys.argv[1])
directories = (
    "backend/contexts/lead-management/migrations/green",
    "backend/contexts/lead-management/migrations/red",
    "backend/contexts/request-fulfillment/migrations/green",
    "backend/contexts/platform-governance/migrations",
    "backend/contexts/integration-platform/migrations",
    "backend/platform/consoleauth/migrations/green",
)
h = hashlib.sha256()
for relative in directories:
    for path in sorted((root / relative).glob("*.sql")):
        if path.name.startswith("._"):
            continue
        h.update(path.relative_to(root).as_posix().encode() + b"\0")
        h.update(path.read_bytes())
print(h.hexdigest())
PY
    )"
    snapshot="$STATE/migrations-$digest"
    if [[ ! -d "$snapshot/backend" ]]; then
        temporary="$(mktemp -d "$STATE/.migration-snapshot.XXXXXX")"
        tar -C "$ROOT" --exclude='._*' --exclude='**/._*' --exclude='.DS_Store' --exclude='**/.DS_Store' \
            -cf - backend/contexts/lead-management/migrations/green \
            backend/contexts/lead-management/migrations/red \
            backend/contexts/request-fulfillment/migrations/green \
            backend/contexts/platform-governance/migrations \
            backend/contexts/integration-platform/migrations \
            backend/platform/consoleauth/migrations/green \
            | tar -xf - -C "$temporary" \
            || fail "не удалось подготовить безопасный snapshot миграций"
        mv "$temporary" "$snapshot"
    fi
    python3 - "$ROOT" "$snapshot" <<'PY' || fail "snapshot миграций не совпал с allowlisted source SQL"
import hashlib, pathlib, sys
source, snapshot = map(pathlib.Path, sys.argv[1:])
directories = (
    "backend/contexts/lead-management/migrations/green",
    "backend/contexts/lead-management/migrations/red",
    "backend/contexts/request-fulfillment/migrations/green",
    "backend/contexts/platform-governance/migrations",
    "backend/contexts/integration-platform/migrations",
    "backend/platform/consoleauth/migrations/green",
)
def inventory(root):
    result = {}
    for directory in directories:
        for path in (root / directory).glob("*.sql"):
            if path.name.startswith("._"):
                continue
            result[path.relative_to(root).as_posix()] = hashlib.sha256(path.read_bytes()).digest()
    return result
if inventory(source) != inventory(snapshot):
    raise SystemExit(1)
PY
    MIGRATION_ROOT="$snapshot/backend"
    MIGRATION_DIGEST="$digest"
}

probe_real_providers() {
    python3 - <<'PY' || fail "Firecrawl, LLM или embeddings provider readiness не подтверждена; crawl не запускался"
import json, os, urllib.request
def get_json(url, key):
    request = urllib.request.Request(url, headers={"Authorization": "Bearer " + key})
    with urllib.request.urlopen(request, timeout=25) as response:
        return json.loads(response.read(1024 * 1024))
try:
    firecrawl = os.environ.get("FIRECRAWL_BASE_URL", "").rstrip("/") or "https://api.firecrawl.dev"
    usage = get_json(firecrawl + "/v2/team/credit-usage", os.environ["FIRECRAWL_API_KEY"])
    if not usage.get("success") or usage.get("data", {}).get("remainingCredits") is None:
        raise ValueError()
    llm = os.environ.get("LLM_BASE_URL", "").rstrip("/") or "https://api.deepseek.com"
    models = get_json(llm + "/models", os.environ["LLM_API_KEY"])
    if not any(item.get("id") for item in models.get("data", [])):
        raise ValueError()
    embedding_base = os.environ["EMBEDDING_BASE_URL"].rstrip("/")
    if embedding_base.startswith("http://host.docker.internal:"):
        embedding_base = embedding_base.replace("http://host.docker.internal:", "http://127.0.0.1:", 1)
    request = urllib.request.Request(
        embedding_base + "/v1/embeddings",
        data=json.dumps({"model": os.environ["EMBEDDING_MODEL"], "input": "readiness check"}).encode(),
        method="POST",
        headers={"Authorization": "Bearer " + os.environ["EMBEDDING_KEY"], "Content-Type": "application/json"},
    )
    with urllib.request.urlopen(request, timeout=25) as response:
        embeddings = json.loads(response.read(1024 * 1024))
    if not embeddings.get("data") or not embeddings["data"][0].get("embedding"):
        raise ValueError()
except Exception:
    raise SystemExit(1)
PY
}

wait_for() {
    local label="$1" command="$2" attempt
    for attempt in $(seq 1 60); do
        if bash -c "$command" >/dev/null 2>&1; then return; fi
        sleep 2
    done
    fail "$label не готов; owned resources сохранены для status/resume, секретные данные не выводились"
}

run_postgres() {
    docker run -d --name "$PG_NAME" --label "$LABEL" --restart unless-stopped \
        --network "$NETWORK_NAME" --env-file "$STATE/credentials/postgres.env" \
        -p "127.0.0.1:$PG_PORT:5432" -v "$PG_VOLUME:/var/lib/postgresql/data" \
        postgres:17.6-alpine >"$STATE/logs/pg-container-id"
}

run_rabbit() {
    docker run -d --name "$RABBIT_NAME" --label "$LABEL" --restart unless-stopped \
        --network "$NETWORK_NAME" --env-file "$STATE/credentials/rabbitmq.env" \
        -p "127.0.0.1:$RABBIT_PORT:5672" rabbitmq:4.1-management-alpine \
        >"$STATE/logs/rabbit-container-id"
}

run_temporal_pg() {
    docker run -d --name "$TEMPORAL_PG_NAME" --label "$LABEL" --restart unless-stopped \
        --network "$NETWORK_NAME" --env-file "$STATE/credentials/temporal-postgres.env" \
        -v "$TEMPORAL_PG_VOLUME:/var/lib/postgresql/data" postgres:17.6-alpine \
        >"$STATE/logs/temporal-pg-container-id"
}

run_temporal() {
    docker run -d --name "$TEMPORAL_NAME" --label "$LABEL" --restart unless-stopped \
        --network "$NETWORK_NAME" --env-file "$STATE/credentials/temporal.env" \
        -p "127.0.0.1:$TEMPORAL_PORT:7233" temporalio/auto-setup:1.26.2 \
        >"$STATE/logs/temporal-container-id"
}

run_app() {
    local generation
    IFS= read -r generation <"$STATE/generation.id"
    docker run -d --name "$APP_NAME" --label "$LABEL" --label "com.leadgen.migration-source=$MIGRATION_DIGEST" --restart unless-stopped \
        --network "$NETWORK_NAME" --user 65532:65532 --read-only \
        --security-opt no-new-privileges --cap-drop ALL \
        --env-file "$STATE/app.env" \
        -v "$ROOT/deploy/services.yaml:/deploy/services.yaml:ro" \
        -v "$SECRET_VOLUME:/run/secrets:ro" \
        --mount "type=bind,source=$MIGRATION_ROOT/contexts/lead-management/migrations/green,target=/backend/contexts/lead-management/migrations/green,readonly" \
        --mount "type=bind,source=$MIGRATION_ROOT/contexts/lead-management/migrations/red,target=/backend/contexts/lead-management/migrations/red,readonly" \
        --mount "type=bind,source=$MIGRATION_ROOT/contexts/request-fulfillment/migrations/green,target=/backend/contexts/request-fulfillment/migrations/green,readonly" \
        --mount "type=bind,source=$MIGRATION_ROOT/contexts/platform-governance/migrations,target=/backend/contexts/platform-governance/migrations,readonly" \
        --mount "type=bind,source=$MIGRATION_ROOT/contexts/integration-platform/migrations,target=/backend/contexts/integration-platform/migrations,readonly" \
        --mount "type=bind,source=$MIGRATION_ROOT/platform/consoleauth/migrations/green,target=/backend/platform/consoleauth/migrations/green,readonly" \
        "$APP_IMAGE" \
        -config /deploy/services.yaml \
        -service lead-management,request-fulfillment,platform-governance \
        -api-port-base "$APP_BASE" -api-bind-host 0.0.0.0 \
        -addr 0.0.0.0:29210 -liveness-addr 0.0.0.0:29211 -metrics-addr "" \
        -console-password-file "/run/secrets/$generation/console-password" \
        -temporal-address "$TEMPORAL_NAME:7233" \
        -temporal-namespace "$NAMESPACE" -temporal-task-queue "$TASK_QUEUE" \
        -workflow-search-stub-leads 0 -firecrawl-max-credits 10 \
        -lead-max-pages 3 -tenant lccrc-demo -actor admin \
        >"$STATE/logs/app-container-id"
}

run_proxy() {
    docker run -d --name "$PROXY_NAME" --label "$LABEL" \
        --label "com.leadgen.proxy-config=$PROXY_CONFIG_DIGEST" --restart unless-stopped \
        --network "$NETWORK_NAME" -p "127.0.0.1:$PROXY_PORT:8000" \
        -v "$ROOT/deploy/dev:/etc/nginx/conf.d:ro" \
        nginx:1.27-alpine >"$STATE/logs/proxy-container-id"
}

ensure_proxy_container() {
    if container_exists "$PROXY_NAME"; then
        check_container_owner "$PROXY_NAME"
        local config_digest
        config_digest="$(docker inspect --format '{{index .Config.Labels "com.leadgen.proxy-config"}}' "$PROXY_NAME")"
        if [[ "$config_digest" != "$PROXY_CONFIG_DIGEST" ]]; then
            # Пересоздаётся только собственный stateless proxy; app/DB/secret volumes
            # и созданные пользователем данные остаются нетронутыми.
            docker stop "$PROXY_NAME" >/dev/null 2>&1 || true
            docker rm "$PROXY_NAME" >/dev/null
            run_proxy
        elif ! container_is_running "$PROXY_NAME"; then
            docker start "$PROXY_NAME" >/dev/null || fail "не удалось возобновить operator proxy"
        fi
    else
        run_proxy
    fi
}

reload_proxy_only() {
    container_exists "$PROXY_NAME" || fail "owned operator proxy отсутствует; используйте resume"
    docker network inspect "$NETWORK_NAME" >/dev/null 2>&1 || fail "owned harness network отсутствует; используйте resume"
    PROXY_CONFIG_DIGEST="$(shasum -a 256 "$ROOT/deploy/dev/current-request-crawl-nginx.conf" | awk '{print $1}')"
    check_network_owner
    ensure_proxy_container
    docker exec "$PROXY_NAME" nginx -t >"$STATE/logs/proxy-config-test.log" 2>&1 \
        || fail "operator proxy config не прошёл nginx -t; app и demo data не изменялись"
    docker exec "$PROXY_NAME" nginx -s reload >"$STATE/logs/proxy-reload.log" 2>&1 \
        || fail "operator proxy config reload не прошёл; app и demo data не изменялись"
    wait_http_proxy
}

ensure_application_image() {
    tar -C "$ROOT" --exclude='*.env' --exclude='*.env.*' --exclude='.secrets' --exclude='**/.secrets/**' \
        --exclude='._*' --exclude='**/._*' --exclude='.DS_Store' --exclude='**/.DS_Store' \
        -cf - Dockerfile backend contracts frontend/components deploy/services.yaml \
        deploy/infrastructure.yaml architecture/domain-catalog.yaml \
        | docker build --no-cache --label "$LABEL" --tag "$APP_IMAGE" - \
            >"$STATE/logs/image-build.log" 2>&1 \
        || fail "сборка образа из allowlisted source paths не прошла; закрытый журнал: $STATE/logs/image-build.log"
}

ensure_app_container() {
    local force_retry="${1:-false}"
    if container_exists "$APP_NAME"; then
        check_container_owner "$APP_NAME"
        local running_image desired_image running_migrations
        running_image="$(docker inspect --format '{{.Image}}' "$APP_NAME")"
        desired_image="$(docker image inspect --format '{{.Id}}' "$APP_IMAGE")"
        running_migrations="$(docker inspect --format '{{index .Config.Labels "com.leadgen.migration-source"}}' "$APP_NAME")"
        if [[ "$force_retry" == true || "$running_image" != "$desired_image" || "$running_migrations" != "$MIGRATION_DIGEST" ]]; then
            # Только stateless app container harness-owned удаляется для обновления
            # бинарника. DB/Temporal/secret volumes остаются на месте.
            docker stop "$APP_NAME" >/dev/null 2>&1 || true
            docker rm "$APP_NAME" >/dev/null
            run_app
        elif ! container_is_running "$APP_NAME"; then
            docker start "$APP_NAME" >/dev/null || fail "не удалось возобновить app container"
        fi
    else
        run_app
    fi
}

stage_and_verify_console_secret() {
    local generation
    IFS= read -r generation <"$STATE/generation.id"
    check_volume_owner "$SECRET_VOLUME"
    if docker run --rm --label "$LABEL" --network none --user 65532:65532 --read-only \
        --security-opt no-new-privileges --cap-drop ALL \
        -v "$SECRET_VOLUME:/run/secrets:ro" "$APP_IMAGE" \
        -db-job verify-secrets -target platform -volume-dir /run/secrets -generation "$generation" \
        >"$STATE/logs/secret-verify.log" 2>&1; then
        return
    fi
    docker run --rm --label "$LABEL" --network none --user 0:0 --read-only \
        --security-opt no-new-privileges --cap-drop ALL \
        --cap-add CHOWN --cap-add FOWNER --cap-add DAC_OVERRIDE \
        --mount "type=bind,source=$ROOT/deploy/tofu/.secrets,target=/run/secret-source,readonly" \
        -v "$SECRET_VOLUME:/stage" "$APP_IMAGE" \
        -db-job stage-secrets -target platform -source-dir /run/secret-source \
        -volume-dir /stage -generation "$generation" \
        >"$STATE/logs/secret-stage.log" 2>&1 \
        || fail "штатный stage-secrets не завершился; source сохранён read-only, volume и журнал оставлены для resume"
    docker run --rm --label "$LABEL" --network none --user 65532:65532 --read-only \
        --security-opt no-new-privileges --cap-drop ALL \
        -v "$SECRET_VOLUME:/run/secrets:ro" "$APP_IMAGE" \
        -db-job verify-secrets -target platform -volume-dir /run/secrets -generation "$generation" \
        >"$STATE/logs/secret-verify.log" 2>&1 \
        || fail "staged bundle не прошёл проверку UID/mode/generation; не использую его в приложении"
}

wait_api_readiness() {
    local label="$1" port="$2"
    wait_for "$label" "docker exec $PROXY_NAME wget -q -O /dev/null http://$APP_NAME:$port/readyz"
}

start_harness() {
    local force_retry="${1:-false}"
    prepare_private_configuration
    probe_real_providers
    ports_available
    PROXY_CONFIG_DIGEST="$(shasum -a 256 "$ROOT/deploy/dev/current-request-crawl-nginx.conf" | awk '{print $1}')"
    prepare_migration_snapshot
    check_network_owner
    check_volume_owner "$PG_VOLUME"
    check_volume_owner "$TEMPORAL_PG_VOLUME"

    ensure_application_image
    stage_and_verify_console_secret

    ensure_started_or_new "$PG_NAME" run_postgres
    wait_for pg "docker exec $PG_NAME pg_isready -U leadgen_lccrc -d lead_gen"
    if [[ "$(docker exec "$PG_NAME" psql -U leadgen_lccrc -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='lead_gen_red'")" != *1* ]]; then
        docker exec --user postgres "$PG_NAME" createdb -U leadgen_lccrc lead_gen_red \
            >"$STATE/logs/create-red-db.log" 2>&1 || fail "не удалось создать только собственную lead_gen_red database"
    fi
    ensure_started_or_new "$RABBIT_NAME" run_rabbit
    wait_for rabbit "docker exec $RABBIT_NAME rabbitmq-diagnostics -q ping"
    ensure_started_or_new "$TEMPORAL_PG_NAME" run_temporal_pg
    wait_for temporal-pg "docker exec $TEMPORAL_PG_NAME pg_isready -U temporal_lccrc -d temporal"
    ensure_started_or_new "$TEMPORAL_NAME" run_temporal
    wait_for temporal "docker inspect --format '{{.State.Running}}' $TEMPORAL_NAME | python3 -c 'import sys; sys.exit(0 if sys.stdin.read().strip()==\"true\" else 1)'"

    ensure_app_container "$force_retry"
    ensure_proxy_container
    docker exec "$PROXY_NAME" nginx -t >"$STATE/logs/proxy-config-test.log" 2>&1 \
        || fail "operator proxy config не прошёл nginx -t; owned ресурсы сохранены"
    docker exec "$PROXY_NAME" nginx -s reload >"$STATE/logs/proxy-reload.log" 2>&1 \
        || fail "operator proxy не перезагрузил config; owned ресурсы сохранены"
    wait_api_readiness lead-management "$APP_BASE"
    wait_api_readiness request-fulfillment "$((APP_BASE + 1))"
    wait_api_readiness platform-governance "$((APP_BASE + 2))"
    wait_http_proxy
    bash "$ROOT/deploy/dev/test-current-request-crawl.sh" live \
        >"$STATE/logs/proxy-live-regression.log" 2>&1 \
        || fail "published proxy allowlist/session regression не пройдена; owned resources сохранены"

    LEADGEN_DEV_VERIFY_GREEN_DSN_FILE="$STATE/credentials/green.dsn" \
    LEADGEN_DEV_VERIFY_RED_DSN_FILE="$STATE/credentials/red.dsn" \
    LEADGEN_DEV_VERIFY_MIGRATION_ROOT="$MIGRATION_ROOT" \
        go -C "$ROOT/backend/cmd/lead-gen" test -v -count=1 -run '^TestDevHarnessVerifySchemas$' \
        >"$STATE/logs/schema-verify.log" 2>&1 \
        || fail "read-only migrate.Verify не пройден; UI acceptance не начинать; закрытый журнал: $STATE/logs/schema-verify.log"

    local binary_hash image_id
    docker cp "$APP_NAME:/usr/local/bin/lead-gen" "$STATE/runtime-lead-gen" \
        >"$STATE/logs/binary-copy.log" 2>&1 || fail "не удалось снять runtime binary identity; приложение оставлено запущенным"
    chmod 600 "$STATE/runtime-lead-gen"
    binary_hash="$(shasum -a 256 "$STATE/runtime-lead-gen" | awk '{print $1}')"
    image_id="$(docker image inspect --format '{{.Id}}' "$APP_IMAGE")"
    printf '%s\n' "$image_id" >"$STATE/app-image.id"
    chmod 600 "$STATE/app-image.id"
    say "Изолированный harness готов: http://127.0.0.1:$PROXY_PORT/"
    say "UI registration/login и operator shell проходят через существующие серверные auth/CSRF маршруты."
    say "Готовые API: lead-management :$APP_BASE, request-fulfillment :$((APP_BASE + 1)), platform-governance :$((APP_BASE + 2)) внутри только harness network."
    say "Зависимости: PG17 :$PG_PORT, RabbitMQ :$RABBIT_PORT, Temporal :$TEMPORAL_PORT; namespace/task queue $NAMESPACE."
    say "Read-only migrate.Verify: 6 schemas/zones; application image ID $image_id."
    say "Runtime binary SHA-256: $binary_hash"
    say "Crawl и записи заявки не выполнялись. Для остановки только принадлежащих harness контейнеров: $0 stop; данные volumes сохраняются."
}

wait_http_proxy() {
    local attempt code
    for attempt in $(seq 1 60); do
        code="$(curl --silent --output /dev/null --write-out '%{http_code}' --max-time 2 "http://127.0.0.1:$PROXY_PORT/register" 2>/dev/null || true)"
        if [[ "$code" == 200 || "$code" == 302 ]]; then return; fi
        sleep 2
    done
    fail "operator proxy не готов; принадлежащие harness ресурсы сохранены"
}

wait_http() { wait_http_proxy; }

status() {
    say "Принадлежащие harness контейнеры:"
    docker ps -a --filter "label=$LABEL" --format '{{.Names}} {{.Status}} {{.Ports}}'
    say "Принадлежащие harness volumes/network остаются на месте (автоудаления нет)."
    [[ -f "$STATE/generation.id" ]] && say "Secret generation id: $(<"$STATE/generation.id")"
    [[ -f "$STATE/app-image.id" ]] && say "Application image ID: $(<"$STATE/app-image.id")"
    say "UI URL: http://127.0.0.1:$PROXY_PORT/"
    say "Temporal namespace/task queue: $NAMESPACE / $TASK_QUEUE"
}

stop_harness() {
    local name owner
    for name in "$APP_NAME" "$PROXY_NAME" "$TEMPORAL_NAME" "$TEMPORAL_PG_NAME" "$RABBIT_NAME" "$PG_NAME"; do
        if container_exists "$name"; then
            check_container_owner "$name"
            docker stop "$name" >/dev/null || fail "не удалось остановить принадлежащий harness контейнер $name"
            say "Остановлен принадлежащий harness контейнер: $name"
        fi
    done
    say "Volumes, сеть, images и demo data сохранены; ничего не удалено."
}

case "${1:-}" in
    start|resume)
        start_harness false
        ;;
    retry)
        start_harness true
        ;;
    proxy-reload)
        reload_proxy_only
        ;;
    status)
        status
        ;;
    stop)
        stop_harness
        ;;
    *)
        say "Использование: $0 {start|resume|retry|proxy-reload|status|stop}"
        exit 2
        ;;
esac
