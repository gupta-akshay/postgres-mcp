GOFLAGS       ?= -count=1
COVERAGE_FILE ?= coverage.out
TEST_DB_DSN   ?= postgresql://postgres:test@localhost:5433/postgres_mcp_test?sslmode=disable
COMPOSE       ?= docker compose

.PHONY: build test test-unit test-integration coverage coverage-html \
        test-db-up test-db-wait test-db-down lint fmt

build:
	go build -o postgres-mcp .

## Run unit tests only (no live DB needed — integration tests excluded by build tag).
test-unit:
	go test $(GOFLAGS) ./...

## Run the full suite (unit + integration) against the disposable docker-compose Postgres.
test-integration: test-db-up test-db-wait
	TEST_DATABASE_URL="$(TEST_DB_DSN)" go test -tags integration $(GOFLAGS) ./...

## Alias: full suite.
test: test-integration

## Bring up the test Postgres in the background.
test-db-up:
	$(COMPOSE) -f docker-compose.test.yml up -d

## Wait until Postgres inside docker-compose is healthy.
test-db-wait:
	@until $(COMPOSE) -f docker-compose.test.yml exec -T postgres-test pg_isready -U postgres > /dev/null 2>&1; \
	  do echo "waiting for postgres..."; sleep 1; done
	@echo "postgres-test is ready"

## Tear down the test Postgres and remove its volume.
test-db-down:
	$(COMPOSE) -f docker-compose.test.yml down -v

## Full-suite coverage report against the live test DB.
coverage: test-db-up test-db-wait
	TEST_DATABASE_URL="$(TEST_DB_DSN)" go test -tags integration $(GOFLAGS) -coverprofile=$(COVERAGE_FILE) ./...
	@go tool cover -func=$(COVERAGE_FILE) | tail -20
	@echo "---"
	@go tool cover -func=$(COVERAGE_FILE) | awk '/^total:/ {print "TOTAL coverage: "$$3}'

## Open the HTML coverage report.
coverage-html: coverage
	go tool cover -html=$(COVERAGE_FILE)

lint:
	go vet ./...
	@if [ -n "$$(gofmt -l .)" ]; then \
	  echo "gofmt needs to run on:"; gofmt -l .; exit 1; \
	fi

fmt:
	gofmt -w .
