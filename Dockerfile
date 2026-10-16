# Служебный образ: единая точка входа `backend/cmd/lead-gen` (design.md, Решение 6).
#
# Образ один на всё: конфигурация deploy/services.yaml выбирает, какие группы
# процессов и роли поднимет конкретный контейнер. Совмещённый контур поднимает
# весь продукт в одном контейнере (`-api-port-base` даёт каждому api-сервису свой
# порт); целевой — отдельный контейнер на роль api.
#
# Репозиторий — рабочее пространство Go (go.work) из нескольких модулей: backend,
# contracts/*, frontend/components. Пакеты связываются workspace-ом.
#
# Сборка многоступенчатая: golang-ступень собирает статический бинарник,
# ступень выполнения — минимальный образ без оболочки и пакетного менеджера.

# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build

WORKDIR /src

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOFLAGS=-trimpath

# Модули служебного контура. Полный go.work в образ не копируется: он ссылается
# на frontend/public и tools/*, которых в контексте образа нет (см. .dockerignore), —
# вместо него ниже собирается минимальный workspace из модулей, нужных cmd/lead-gen
# (консоль переиспользует frontend/components).
COPY backend/ ./backend/
COPY contracts/ ./contracts/
COPY frontend/components/ ./frontend/components/

RUN printf 'go 1.27\n\nuse (\n\t./backend/cmd/lead-gen\n\t./backend/contexts/agent-orchestration\n\t./backend/contexts/analytics\n\t./backend/contexts/audience\n\t./backend/contexts/content-production\n\t./backend/contexts/data-governance\n\t./backend/contexts/engagement-observation\n\t./backend/contexts/integration-platform\n\t./backend/contexts/lead-management\n\t./backend/contexts/message-delivery\n\t./backend/contexts/outreach-orchestration\n\t./backend/contexts/platform-governance\n\t./backend/contexts/request-fulfillment\n\t./backend/platform\n\t./contracts/agent-orchestration\n\t./contracts/analytics\n\t./contracts/audience\n\t./contracts/content-production\n\t./contracts/data-governance\n\t./contracts/engagement-observation\n\t./contracts/integration-platform\n\t./contracts/lead-management\n\t./contracts/message-delivery\n\t./contracts/outreach-orchestration\n\t./contracts/platform-governance\n\t./contracts/request-fulfillment\n\t./frontend/components\n)\n' > go.work

# Go загружает только зависимости служебного бинарника; workspace содержит также
# модули вне этого пути, поэтому полный `go mod download` тратит сеть на лишнее.
RUN go build -ldflags="-s -w" -o /out/lead-gen ./backend/cmd/lead-gen
RUN mkdir -p /out/release-state && chmod 0700 /out/release-state

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=build /out/lead-gen /usr/local/bin/lead-gen
COPY --from=build --chown=65532:65532 /out/release-state /run/leadgen-release

# Канонические источники и конфигурация по умолчанию: оркестрация задаёт актуальную
# копию поверх этого пути (bind-mount на чтение) и передаёт флаги. Каталог нужен
# консоли топологии и проверкам; адреса инфраструктуры — только внутренние.
COPY deploy/services.yaml /deploy/services.yaml
COPY deploy/research/services.yaml /deploy/research/services.yaml
COPY deploy/infrastructure.yaml /deploy/infrastructure.yaml
COPY architecture/domain-catalog.yaml /architecture/domain-catalog.yaml

# Миграции схем сервисов читаются из каталога относительно рабочего каталога
# (applyMigrations в cmd/lead-gen), поэтому кладём их по тем же путям от корня.
COPY backend/contexts/agent-orchestration/migrations/ /backend/contexts/agent-orchestration/migrations/
COPY backend/contexts/analytics/migrations/ /backend/contexts/analytics/migrations/
COPY backend/contexts/audience/migrations/ /backend/contexts/audience/migrations/
COPY backend/contexts/content-production/migrations/ /backend/contexts/content-production/migrations/
COPY backend/contexts/data-governance/migrations/ /backend/contexts/data-governance/migrations/
COPY backend/contexts/engagement-observation/migrations/ /backend/contexts/engagement-observation/migrations/
COPY backend/contexts/integration-platform/migrations/ /backend/contexts/integration-platform/migrations/
COPY backend/contexts/lead-management/migrations/ /backend/contexts/lead-management/migrations/
COPY backend/contexts/message-delivery/migrations/ /backend/contexts/message-delivery/migrations/
COPY backend/contexts/outreach-orchestration/migrations/ /backend/contexts/outreach-orchestration/migrations/
COPY backend/contexts/platform-governance/migrations/ /backend/contexts/platform-governance/migrations/
COPY backend/contexts/request-fulfillment/migrations/ /backend/contexts/request-fulfillment/migrations/
COPY backend/platform/consoleauth/migrations/ /backend/platform/consoleauth/migrations/

WORKDIR /

USER nonroot:nonroot

# Внутренние HTTP-сервера ролей. Совмещённый контур публикует диапазон портов
# (см. compose.yaml); наружу публикует оркестрация.
EXPOSE 8080 8081

ENTRYPOINT ["/usr/local/bin/lead-gen"]
