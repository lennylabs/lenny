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

.PHONY: images
images: ## Build container images for the deployable platform binaries
	@for b in lenny-adapter lenny-controller lenny-gateway lenny-webhook lenny-token-service lenny-preflight; do \
		echo "  image $$b"; \
		docker build --build-arg BINARY=$$b -t ghcr.io/lennylabs/$$b:dev . || exit 1; \
	done

.PHONY: generate
generate: ## Regenerate DeepCopy + CRD manifests + bundled alerting rules
	@echo "  controller-gen object → pkg/apis DeepCopy"
	@$(GOPATH_BIN)/controller-gen object:headerFile=hack/boilerplate.go.txt paths=./pkg/apis/lenny/v1/...
	@echo "  controller-gen crd → charts/lenny/crds"
	@$(GOPATH_BIN)/controller-gen crd paths=./pkg/apis/lenny/v1/... output:crd:dir=charts/lenny/crds
	@echo "  gen-alerting-rules → charts/lenny/files/alerting-rules.yaml"
	@go run ./cmd/gen-alerting-rules

.PHONY: generate-proto
generate-proto: ## Regenerate the gRPC bindings from schemas/*.proto
	@echo "  buf generate → pkg/proto"
	@cd schemas && PATH="$(GOPATH_BIN):$$PATH" buf generate
	@find pkg/proto -name '*.go' | while read -r f; do \
		head -1 "$$f" | grep -q SPDX-License-Identifier || \
		{ printf '// SPDX-License-Identifier: MIT\n\n' | cat - "$$f" > "$$f.tmp" && mv "$$f.tmp" "$$f"; }; \
	done
	@echo "  goimports → pkg/proto"
	@PATH="$(GOPATH_BIN):$$PATH" goimports -w -local github.com/lennylabs/lenny ./pkg/proto

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
run: ## Native-process dev loop: gateway + in-memory stores + echo runtime
	@mkdir -p bin
	@echo "  build echo runtime"
	@go build -o bin/echo ./cmd/runtimes/echo
	@echo "  build lenny-gateway"
	@go build -o bin/lenny-gateway ./cmd/lenny-gateway
	@echo "  starting gateway on :8080 (dev mode, echo runtime, in-memory stores)"
	@echo "  POST http://localhost:8080/v1/sessions/start to drive a session;"
	@echo "  GET  http://localhost:8080/healthz for liveness; Ctrl-C to stop."
	@./bin/lenny-gateway --addr :8080 --dev-mode --runtime-bin ./bin/echo
