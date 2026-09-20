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

# go.sum не копируется: go.mod-модулей без внешних зависимостей. При их появлении
# добавить `COPY go.sum ./` и `RUN go mod download` перед копированием исходников.
#
# go.work в образ не копируется: он ссылается на модули, которых в контексте образа
# нет (frontend/*, tools/*), и сборка упала бы на «модуль не найден». Вместо него
# образ создаёт минимальный workspace из модулей служебного контура и его
# зависимостей. Внешних зависимостей у модулей нет — сборка идёт без сети.
COPY backend/ ./backend/
COPY contracts/ ./contracts/

RUN printf 'go 1.25\n\nuse (\n\t./backend/cmd/lead-gen\n\t./backend/contexts/outreach-orchestration\n\t./backend/platform\n\t./contracts/outreach-orchestration\n)\n' > go.work

RUN go build -ldflags="-s -w" -o /out/lead-gen ./backend/cmd/lead-gen

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=build /out/lead-gen /usr/local/bin/lead-gen

# Конфигурация по умолчанию: образ самодостаточен. OpenTofu монтирует актуальную
# копию deploy/services.yaml поверх этого пути (только на чтение).
COPY deploy/services.yaml /etc/lead-gen/services.yaml

USER nonroot:nonroot

# Внутренние HTTP-сервера ролей: api и ingest. Наружу не публикуются.
EXPOSE 8080 8081

ENTRYPOINT ["/usr/local/bin/lead-gen"]
