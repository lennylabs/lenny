# SPDX-License-Identifier: MIT
#
# Lenny test infrastructure Makefile.
#
# This Makefile is a thin convenience layer around `go build`,
# `go test`, and `lenny-test`. The harness (cmd/lenny-test) is the
# single source of truth for what runs and in what order; targets
# here exist so contributors don't have to memorize the harness
# invocation for the common cases. See TESTING.md §6 for the full
# command surface.
#
# Targets that depend on `lenny-test` first run `install` so the
# binary is on PATH at $(go env GOPATH)/bin.
#
# The contributor dev loop (`make run`) is a forward-looking target;
# it activates once cmd/lenny-dev ships in a later phase. The other
# targets work against the current Phase 0 surface.

.DEFAULT_GOAL := help
GOPATH_BIN := $(shell go env GOPATH)/bin

.PHONY: help
help: ## Print this message
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build every cmd/ binary into ./bin
	@mkdir -p bin
	@for d in $$(find cmd -mindepth 1 -name '*.go' -exec dirname {} \; | sort -u); do \
		name=$$(basename $$d); \
		echo "  build $$name"; \
		go build -o bin/$$name ./$$d || exit 1; \
	done

.PHONY: install
install: ## Install lenny-test + lenny-compliance into $(go env GOPATH)/bin
	@echo "  install lenny-test → $(GOPATH_BIN)"
	@go install ./cmd/lenny-test
	@echo "  install lenny-compliance → $(GOPATH_BIN)"
	@go install ./cmd/lenny-compliance

.PHONY: test
test: install ## Run the unit tier (go test ./...)
	@lenny-test --tier unit --output human

.PHONY: pr
pr: install ## Run the pr-fast group (changed-only, fast tiers)
	@lenny-test --group pr-fast --output human

.PHONY: pr-full
pr-full: install ## Run the full pr group (everything that runs on every push)
	@lenny-test --group pr --output human

.PHONY: validate
validate: install ## Run validate-maps and validate-diagnosis
	@lenny-test validate-maps
	@lenny-test validate-diagnosis

.PHONY: lint
lint: install ## Run every Tier 0 static linter
	@lenny-test --tier static --output human

.PHONY: coverage
coverage: install ## Report Go and spec coverage
	@lenny-test coverage --go
	@lenny-test coverage --spec

.PHONY: clean
clean: ## Remove ./bin and the test results directory
	@rm -rf bin tests/results

.PHONY: run
run: ## (Future) Native-process dev loop with SQLite + in-memory stores
	@echo "make run: cmd/lenny-dev is a later-phase deliverable per TESTING.md §17.4."
	@echo "Today: use 'make test' for the unit tier or 'make pr' for the PR group."
	@exit 1
