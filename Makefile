.DEFAULT_GOAL := help

GO       ?= go
GOBIN    := $(shell $(GO) env GOPATH)/bin
PKG      := github.com/AymanYouss/chronos-engine
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -X $(PKG)/internal/version.Version=$(VERSION)

export PATH := $(GOBIN):$(PATH)

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: tools
tools: ## Install code-gen and lint tooling
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.6
	$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1

.PHONY: proto
proto: ## Generate gRPC/protobuf code from api/proto
	buf generate

.PHONY: build
build: ## Build server and worker binaries into ./bin
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/chronos-server ./cmd/chronos-server
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/chronos-worker ./cmd/chronos-worker

.PHONY: test
test: ## Run unit + integration tests
	$(GO) test -race -count=1 ./...

.PHONY: cover
cover: ## Run tests with coverage report
	$(GO) test -race -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -func=coverage.out | tail -1

.PHONY: lint
lint: ## Run go vet and formatting checks
	$(GO) vet ./...
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './api/gen/*'))" || \
		(echo "gofmt needed on:" && gofmt -l $$(find . -name '*.go' -not -path './api/gen/*') && exit 1)

.PHONY: tidy
tidy: ## Tidy go modules
	$(GO) mod tidy

.PHONY: up
up: ## Bring up the full stack via docker-compose
	docker compose up --build

.PHONY: down
down: ## Tear down the docker-compose stack
	docker compose down -v

.PHONY: demo
demo: ## Run the crash-and-resume demonstration
	./scripts/demo-crash-resume.sh

.PHONY: web
web: ## Run the inspector UI in dev mode
	cd web && pnpm install && pnpm dev
