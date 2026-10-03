# Служебный образ: единая точка входа `backend/cmd/lead-gen` (design.md, Решение 6).
#
# Образ один на всё: конфигурация deploy/services.yaml выбирает, какие группы
# процессов и роли поднимет конкретный контейнер. Образ публичного edge — отдельный
# и здесь не собирается.
#
# Репозиторий — рабочее пространство Go (go.work) из нескольких модулей: backend,
# contracts/*, frontend/*, tools/*. Пакеты связываются workspace-ом.
#
# Сборка многоступенчатая: golang-ступень собирает статический бинарник,
# ступень выполнения — минимальный образ без оболочки и пакетного менеджера.

# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build

WORKDIR /src

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOFLAGS=-trimpath

# Модули служебного контура. Полный go.work в образ не копируется: он ссылается
# на frontend/public и tools/*, которых в контексте образа нет (см. .dockerignore), —
# вместо него ниже собирается минимальный workspace из модулей, нужных
# cmd/lead-gen (консоль lead-management переиспользует frontend/components).
# Вместе с каталогами приходят go.sum модулей с внешними зависимостями (pgx/v5).
COPY backend/ ./backend/
COPY contracts/ ./contracts/
COPY frontend/components/ ./frontend/components/

RUN printf 'go 1.25\n\nuse (\n\t./backend/cmd/lead-gen\n\t./backend/contexts/analytics\n\t./backend/contexts/audience\n\t./backend/contexts/billing\n\t./backend/contexts/community-intelligence\n\t./backend/contexts/content-production\n\t./backend/contexts/data-governance\n\t./backend/contexts/engagement-observation\n\t./backend/contexts/integration-platform\n\t./backend/contexts/lead-management\n\t./backend/contexts/message-delivery\n\t./backend/contexts/outreach-orchestration\n\t./backend/contexts/platform-governance\n\t./backend/contexts/publishing\n\t./backend/contexts/request-fulfillment\n\t./backend/contexts/sales-workflow\n\t./backend/platform\n\t./contracts/audience\n\t./contracts/billing\n\t./contracts/community-intelligence\n\t./contracts/content-production\n\t./contracts/data-governance\n\t./contracts/engagement-observation\n\t./contracts/integration-platform\n\t./contracts/lead-management\n\t./contracts/message-delivery\n\t./contracts/outreach-orchestration\n\t./contracts/publishing\n\t./contracts/request-fulfillment\n\t./contracts/sales-workflow\n\t./frontend/components\n)\n' > go.work

# Внешние зависимости появились (github.com/jackc/pgx/v5 и транзитивные):
# скачиваем до сборки, чтобы слой кэшировался. Требует сети.
RUN go mod download

RUN go build -ldflags="-s -w" -o /out/lead-gen ./backend/cmd/lead-gen

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=build /out/lead-gen /usr/local/bin/lead-gen

# Конфигурация по умолчанию: образ самодостаточен. Оркестрация (OpenTofu или
# docker compose) задаёт актуальную копию deploy/services.yaml поверх этого пути
# (bind-mount только на чтение) и передаёт `-config`.
COPY deploy/services.yaml /etc/lead-gen/services.yaml

# Миграции схем сервисов читаются из каталога относительно рабочего каталога
# (applyMigrations в cmd/lead-gen), поэтому кладём их по тому же пути от корня.
# Рабочий каталог ступени выполнения — /.
COPY backend/contexts/lead-management/migrations/ /backend/contexts/lead-management/migrations/
COPY backend/contexts/request-fulfillment/migrations/ /backend/contexts/request-fulfillment/migrations/

WORKDIR /

USER nonroot:nonroot

# Внутренние HTTP-сервера ролей: api и ingest. Наружу публикует оркестрация.
EXPOSE 8080 8081

ENTRYPOINT ["/usr/local/bin/lead-gen"]
