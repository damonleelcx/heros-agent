# Single source of truth for build/test/lint — CI calls THESE targets, so "green locally" == "green in
# CI" (DevOps rule: deliver "anyone can run it", never "works on my machine"). See .github/workflows/ci.yml.
#
# Quick start:
#   make ci        # everything a PR must pass, minus external installs (go + schema)
#   make go        # build, vet, gofmt-check, test
#   make schema    # JSON-schema + contract proofs (needs: pip install jsonschema)
#   make lint      # golangci-lint (needs it installed; CI installs it)
#   make db-proof  # live-Postgres constraint + slice proofs (needs postgres bin + psycopg[binary])
#   make pg-proof  # live-Postgres registry proofs (needs only Docker)
#   make verifier-proof  # live-toolchain proofs of the 7 language gates (needs those toolchains)
#   make discovery-sandbox-proof  # least-privilege worker runtime proof (needs only Docker)

GO      ?= go
PYTHON  ?= python3
PKG     ?= ./...
# PARITY_DIR holds the pre-change discovery output baseline (see discovery-parity-snapshot).
PARITY_DIR ?= .parity

.DEFAULT_GOAL := ci

.PHONY: ci go build vet fmt test schema lint db-proof pg-proof verifier-proof tidy-check clean help \
        build-discover discovery-ci discovery-throughput \
        discovery-parity-snapshot discovery-parity-verify \
        discovery-sandbox-proof discovery-sandbox-proof-redcheck

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

## discovery-parity-snapshot: record a baseline of the discover CLI's output over EVERY fixture
##                          (SHA-256 of BOTH ir.json and report.json) into DIR (default .parity).
##                          Run this BEFORE touching the parsing path.
discovery-parity-snapshot: build-discover
	$(PYTHON) scripts/discovery_parity.py snapshot $(PARITY_DIR)

## discovery-parity-verify: re-run every fixture and prove the output is byte-identical to the
##                          baseline. This is the import-parser-research-validation gate: a parsing
##                          change is "equivalent" only when it is PROVEN so, never when it is
##                          claimed ("没有纯重构例外"). It hashes the REPORT as well as the IR on
##                          purpose — framework subgraphs, dedup merges, ambiguity flags and file
##                          diagnostics never reach the IR, so an IR-only diff is blind to exactly
##                          what a frontend change is most likely to move.
discovery-parity-verify: build-discover
	$(PYTHON) scripts/discovery_parity.py verify $(PARITY_DIR)

## discovery-throughput: soft throughput signal (≤60s / ~200k LOC) — informs, never blocks (task 8.3)
discovery-throughput: build-discover
	$(PYTHON) scripts/discovery_throughput.py

## discovery-sandbox-proof: prove the Discovery worker's least-privilege RUNTIME (task 7.2 / NFR7).
##                          Asserts deploy/docker-compose.discovery.yml actually enforces: read-only
##                          repo mount, no network egress, no ambient provider creds — statically (the
##                          field is in the shipped spec) and dynamically (the probe it forbids fails).
##                          Complements internal/discovery/noexec_test.go, which proves only the code
##                          half: `discover` parses untrusted source with tree-sitter C via cgo, which
##                          no Go import guard can constrain. Needs only Docker.
discovery-sandbox-proof:
	$(PYTHON) scripts/discovery_sandbox_proof.py

## discovery-sandbox-proof-redcheck: prove the above proof can FAIL. Weakens the compose spec one
##                          guarantee at a time (drop network_mode:none / repo mount to :rw / forward
##                          OPENAI_API_KEY) and asserts the proof goes red for that specific claim.
##                          A fence that cannot go red is decoration. Needs only Docker.
discovery-sandbox-proof-redcheck:
	$(PYTHON) scripts/discovery_sandbox_redcheck.py

## verifier-proof: prove the seven language gates against REAL toolchains (ADR-003 decision 2).
##                 For each language: a genuine rewrite PASSES at the strength the ADR says that
##                 language earns, and a broken one FAILS (围栏必须能红). These are behind the
##                 `verifiers` build tag so `make go` does not compile them, and — like pg-proof —
##                 with a toolchain absent they FAIL rather than skip: a skipped safety proof is one
##                 that quietly stopped guarding.
##
##                 The gate-strength tests that need NO toolchain (the automation rule, the dispatch
##                 table, and every missing-toolchain assertion, which need an ABSENCE) run in
##                 `make go` — that is where the most safety-critical assertions belong.
##
##                 Needs on PATH: go, python3 + mypy, node, cargo, javac (a REAL JDK — the macOS
##                 /usr/bin/javac shim is detected and rejected), kotlinc, tsc.
##                   macOS:  brew install openjdk kotlin && pip install mypy && npm i -g typescript
##                   Debian: apt-get install default-jdk && pip install mypy && npm i -g typescript
verifier-proof:
	$(GO) test -tags verifiers -count=1 ./internal/worktree/

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

## pg-proof: live-Postgres proof of the four registries — schema guards + store (P2 tasks 1.1/1.6/1.7).
##           Boots an ephemeral Postgres in Docker, so it needs only Docker (no local PG install).
##           These tests are behind the `pgproof` build tag, so `make go` does not compile them; with
##           no database they FAIL rather than skip.
pg-proof:
	bash db/migrations/postgres/run_pg_docker.sh $(GO) test -tags pgproof -count=1 ./internal/registry/ ./internal/variantspec/ ./internal/worktree/ ./internal/executor/ ./internal/runqueue/ ./internal/submit/ ./internal/e2e/

## tidy-check: assert go.mod/go.sum are tidy (no drift)
tidy-check:
	$(GO) mod tidy
	@git diff --exit-code go.mod go.sum || (echo "go.mod/go.sum not tidy; run 'go mod tidy'"; exit 1)

clean:
	$(GO) clean
	rm -rf .gocache

help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
