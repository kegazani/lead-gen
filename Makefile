.PHONY: build test race vet fmt lint arch openspec check test-postgres

# Корневого go.mod больше нет: workspace связан go.work, поэтому цели обходят
# модули по списку, а не через `go build ./...` из корня.
MODULES := backend/cmd/lead-gen backend/contexts/analytics backend/contexts/audience backend/contexts/billing backend/contexts/community-intelligence backend/contexts/content-production backend/contexts/data-governance backend/contexts/engagement-observation backend/contexts/integration-platform backend/contexts/lead-management backend/contexts/message-delivery backend/contexts/outreach-orchestration backend/contexts/platform-governance backend/contexts/publishing backend/contexts/request-fulfillment backend/contexts/sales-workflow contracts/audience contracts/billing contracts/community-intelligence contracts/content-production contracts/data-governance contracts/engagement-observation contracts/integration-platform contracts/lead-management contracts/message-delivery contracts/outreach-orchestration contracts/publishing contracts/request-fulfillment contracts/sales-workflow frontend/components frontend/public backend/platform tools/archlint tools/importlint

# `go build ./...` для модуля с единственным main-пакетом пишет бинарник в
# каталог модуля. Цель собирает без вывода (-o /dev/null) и проверяет
# компилируемость — как прежний `go build ./...` из корня.
build:
	@for m in $(MODULES); do \
		echo "==> $$m"; \
		( cd $$m && go build -o /dev/null ./... ) || exit 1; \
	done

test:
	@for m in $(MODULES); do \
		echo "==> $$m"; \
		( cd $$m && go test ./... ) || exit 1; \
	done

race:
	@for m in $(MODULES); do \
		echo "==> $$m"; \
		( cd $$m && go test -race ./... ) || exit 1; \
	done

vet:
	@for m in $(MODULES); do \
		echo "==> $$m"; \
		( cd $$m && go vet ./... ) || exit 1; \
	done

fmt:
	@for m in $(MODULES); do \
		gofmt -l $$m || exit 1; \
	done

lint: fmt vet
	go -C tools/importlint run .

arch:
	go -C tools/archlint run .

openspec:
	openspec validate --all --strict

check: build test vet lint arch openspec

# Порт и образ одноразовой базы для контрактных suite PostgreSQL-адаптера.
PG_TEST_PORT ?= 5433
PG_TEST_IMAGE ?= postgres:16-alpine

# Прогон контрактных suite PostgreSQL-адаптера против одноразовой базы.
# Требует Docker; база поднимается, дожидается готовности и удаляется после
# прогона. Без этой цели PostgreSQL-тесты адаптера пропускаются, поэтому
# `make check` базы не требует.
test-postgres:
	@docker rm -f leadgen-pg-test >/dev/null 2>&1 || true
	@docker run -d --rm --name leadgen-pg-test \
		-e POSTGRES_PASSWORD=postgres -e POSTGRES_USER=postgres -e POSTGRES_DB=leadgen \
		-p $(PG_TEST_PORT):5432 $(PG_TEST_IMAGE) >/dev/null
	@for i in $$(seq 1 30); do \
		docker exec leadgen-pg-test pg_isready -U postgres -d leadgen >/dev/null 2>&1 && break; \
		sleep 1; \
	done
	@DSN='postgres://postgres:postgres@localhost:$(PG_TEST_PORT)/leadgen?sslmode=disable'; \
	LEADGEN_POSTGRES_TEST_DSN=$$DSN go -C backend/contexts/outreach-orchestration test ./internal/adapters/persistence/postgres/...; \
	status=$$?; \
	LEADGEN_POSTGRES_TEST_DSN=$$DSN go -C backend/contexts/lead-management test ./internal/adapters/persistence/postgres/...; \
	status=$$((status + $$?)); \
	LEADGEN_POSTGRES_TEST_DSN=$$DSN go -C backend/contexts/request-fulfillment test ./internal/adapters/persistence/postgres/...; \
	status=$$((status + $$?)); \
	docker rm -f leadgen-pg-test >/dev/null 2>&1; exit $$status
