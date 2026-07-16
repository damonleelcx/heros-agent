# Single source of truth for build/test/lint — CI calls THESE targets, so "green locally" == "green in
# CI" (DevOps rule: deliver "anyone can run it", never "works on my machine"). See .github/workflows/ci.yml.
#
# Quick start:
#   make ci        # everything a PR must pass, minus external installs (go + schema)
#   make go        # build, vet, gofmt-check, test
#   make schema    # JSON-schema + contract proofs (needs: pip install jsonschema)
#   make lint      # golangci-lint (needs it installed; CI installs it)
#   make db-proof  # live-Postgres constraint + slice proofs (needs postgres bin + psycopg[binary])

GO      ?= go
PYTHON  ?= python3
PKG     ?= ./...

.DEFAULT_GOAL := ci

.PHONY: ci go build vet fmt test schema lint db-proof tidy-check clean help \
        build-discover discovery-ci discovery-throughput

## ci: the locally-provable gate (go + schema + discovery-ci). Lint/db-proof run as their own CI jobs.
ci: go schema discovery-ci
	@echo "== make ci: PASS =="

## go: build, vet, gofmt-check, test
go: build vet fmt test

build:
	$(GO) build $(PKG)

vet:
	$(GO) vet $(PKG)

# gofmt-check: fail loud if any file is unformatted (never auto-fix in CI — report and block).
fmt:
	@unformatted="$$(gofmt -l . 2>/dev/null)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt: the following files are not formatted:"; echo "$$unformatted"; \
		echo "run: gofmt -w ."; exit 1; \
	else echo "gofmt: clean"; fi

test:
	$(GO) test -count=1 $(PKG)

## schema: JSON-schema validation gate + contract proofs (task 4.2)
schema:
	$(PYTHON) schemas/validate.py
	$(PYTHON) schemas/test_config_hash.py
	$(PYTHON) schemas/test_schema_evolution.py
	$(PYTHON) schemas/spike_io_contract.py

## build-discover: build the Discovery CLI to bin/discover
build-discover:
	$(GO) build -o bin/discover ./cmd/discover

## discovery-ci: run the discover CLI on every fixture; validate emitted IR against the schema +
##               node-count regression + golden-IR drift (tasks 8.1, 8.2). Needs: pip install jsonschema
discovery-ci: build-discover
	$(PYTHON) scripts/discovery_ci.py

## discovery-throughput: soft throughput signal (≤60s / ~200k LOC) — informs, never blocks (task 8.3)
discovery-throughput: build-discover
	$(PYTHON) scripts/discovery_throughput.py

## lint: golangci-lint (installed by CI; locally install from https://golangci-lint.run)
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed — see https://golangci-lint.run/welcome/install/"; exit 1; \
	fi

## db-proof: apply the Postgres migration and prove constraints + slices fire (tasks 2.1/3.1)
db-proof:
	bash db/migrations/postgres/run_pg_proof.sh prove_constraints.py
	bash db/migrations/postgres/run_pg_proof.sh prove_slices.py

## tidy-check: assert go.mod/go.sum are tidy (no drift)
tidy-check:
	$(GO) mod tidy
	@git diff --exit-code go.mod go.sum || (echo "go.mod/go.sum not tidy; run 'go mod tidy'"; exit 1)

clean:
	$(GO) clean
	rm -rf .gocache

help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
