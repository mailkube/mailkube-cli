# Development entry points.
#
# Each target is a thin wrapper over the exact command CI runs, so the two cannot drift and
# nobody has to reverse-engineer the workflow YAML to reproduce a failure locally.

GOFUMPT_VERSION    ?= v0.7.0
GOLANGCI_VERSION   ?= v2.1.6

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the mailkube binary into ./dist
	@go build -trimpath -o dist/mailkube ./cmd/mailkube

.PHONY: test
test: ## Run the tests with the race detector and the coverage gate
	@go test ./... -race -covermode=atomic -coverpkg=./... -coverprofile=coverage.out
	@./scripts/check-coverage.sh coverage.out

.PHONY: lint
lint: ## Run vet, gofumpt and golangci-lint exactly as CI does
	@go vet ./...
	@go run mvdan.cc/gofumpt@$(GOFUMPT_VERSION) -l .
	@go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION) run

.PHONY: golden
golden: ## Regenerate every golden file — then READ THE DIFF before committing
	@go test ./... -update
	@echo ""
	@echo "Golden files regenerated. Review 'git diff' before committing:"
	@echo "a golden committed unread turns a behaviour change into a silent one."

.PHONY: docs
docs: ## Run the governance gate (every .rules/*.md indexed in AGENTS.md)
	@./scripts/check-rule-index.sh

.PHONY: surface
surface: ## Run the surface-parity gate (error catalogue and feature mappings)
	@./scripts/check-surface.sh

.PHONY: dry
dry: ## Run the duplication gate
	@npx --yes jscpd@4 --config .jscpd.json .

.PHONY: check
check: lint test docs surface ## Everything a pull request must pass
