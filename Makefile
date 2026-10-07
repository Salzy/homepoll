.PHONY: dev build docker test test-all fmt tidy migrate-create up down

IMAGE ?= homepoll

dev: ## live-reload the collector (loads .env if present)
	set -a; [ -f .env ] && . ./.env; set +a; air

build:
	go build -o ./tmp/collector ./cmd/collector

docker: ## build the container image (override with IMAGE=name:tag)
	docker build -t $(IMAGE) .

test: ## unit tests only (integration tests skip via -short)
	go test -short ./...

test-all: ## full suite: unit + DB-backed integration tests (needs `make up`)
	go test ./...

fmt: ## gofmt + wrap lines at 100 (golines)
	golines -w -m 100 .

tidy:
	go mod tidy

migrate-create: ## make NAME=add_solar migrate-create
	goose -dir internal/db/migrations create $(NAME) sql

up: ## start local dependencies (postgres + grafana)
	docker compose up -d

down:
	docker compose down
