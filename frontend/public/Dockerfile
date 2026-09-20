# Образ публичного edge — отдельный от служебного образа `cmd/lead-gen`
# (design.md, Решение 4: публичный веб-контур поставляется отдельным модулем и
# отдельным образом на своём домене). Корневой Dockerfile служебного образа
# этот файл не заменяет и не изменяет.
#
# Почему состав зависимостей ограничен. Публичный edge — внешняя поверхность
# атаки, поэтому его зависимости прямо сужены: нет драйвера БД, нет клиента
# Temporal, нет брокера (tools/importlint проверяет границы импортов). Образ
# делает это ограничение физическим: сборка копирует только модули edge —
# frontend/public и общий слой frontend/components — и published-контракты, а
# служебные модули (backend, tools) в образ не попадают.
#
# Контекст сборки — КОРЕНЬ репозитория, потому что модуль edge зависит от
# published-контрактов `lead-gen/contracts/*`. Команда:
#   docker build -f frontend/public/Dockerfile -t lead-gen-public-edge:local .
#
# go.work создаётся минимальный. Копировать корневой go.work нельзя: он ссылается
# на служебные модули, которых в контексте образа нет, и сборка в workspace
# упала бы на «модуль не найден». Минимальный workspace перечисляет ровно те
# модули, что нужны edge.
#
# Сборка многоступенчатая: golang-ступень собирает статический бинарник,
# ступень выполнения — минимальный образ без оболочки и пакетного менеджера.
# Проба живости снимается снаружи по `/healthz`: в distroless нет ни оболочки,
# ни curl, поэтому HEALTHCHECK в образе не задаётся.

# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build

WORKDIR /src

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOFLAGS=-trimpath

COPY frontend/components/ ./frontend/components/
COPY frontend/public/ ./frontend/public/
COPY contracts/ ./contracts/

RUN printf 'go 1.25\n\nuse (\n\t./frontend/components\n\t./frontend/public\n\t./contracts/content-production\n\t./contracts/engagement-observation\n\t./contracts/outreach-orchestration\n)\n' > go.work

RUN cd frontend/public && go build -ldflags="-s -w" -o /out/public-edge .

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=build /out/public-edge /usr/local/bin/public-edge

USER nonroot:nonroot

# Публичный порт процесса edge. Наружу его выставляет обратный прокси на домене
# контура; сам контейнер публикует только этот порт.
EXPOSE 8080

# Значение по умолчанию — демонстрационный лендинг, чтобы образ можно было
# проверить сразу после сборки. Рабочее развёртывание передаёт свою ссылку
# флагом `-landing` или оставляет корень без страницы (пустое значение).
ENTRYPOINT ["/usr/local/bin/public-edge"]
