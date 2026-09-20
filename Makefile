.PHONY: build test race vet fmt lint arch openspec check

# Корневого go.mod больше нет: workspace связан go.work, поэтому цели обходят
# модули по списку, а не через `go build ./...` из корня.
MODULES := backend/cmd/lead-gen backend/contexts/outreach-orchestration contracts/content-production contracts/engagement-observation contracts/outreach-orchestration frontend/components frontend/public backend/platform tools/archlint tools/importlint

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
