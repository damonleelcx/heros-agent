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
#   make release-rehearse         # run the release pipeline's spine locally on a rehearsal tag (P20 2.6/2.7)

GO      ?= go
PYTHON  ?= python3
PKG     ?= ./...
# PARITY_DIR holds the pre-change discovery output baseline (see discovery-parity-snapshot).
PARITY_DIR ?= .parity

.DEFAULT_GOAL := ci

.PHONY: ci go build vet fmt test schema lint db-proof pg-proof verifier-proof tidy-check clean help demo-evalboard \
        deploy-lint deploy-up deploy-down deploy-down-hard \
        console-types console-types-check console-test operator-console-test operator-ledger operator-hermes docs-facts docs-facts-check \
        build-discover discovery-ci discovery-throughput \
        discovery-parity-snapshot discovery-parity-verify \
        discovery-sandbox-proof discovery-sandbox-proof-redcheck \
        sandbox-proof sandbox-proof-redcheck \
        classifier-calibration demo-patterngraph demo-proposals demo-billing demo-billing-states \
        release-rehearse release-rehearse-redcheck readme-install packaging-proof install-smoke install-smoke-refusals

## ci: the locally-provable gate (go + schema + console-types + discovery-ci). Lint/db-proof run as their own CI jobs.
ci: go schema console-types-check docs-facts-check discovery-ci
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

## console-types: regenerate the console's data contract from the Go view structs (P9 ADR-007)
console-types:
	GOWORK=off $(GO) run ./cmd/consoletypes

## console-types-check: fail if a Go view type changed and the checked-in contract did not.
#
# This is the drift gate, and it is the whole reason the artifacts are generated rather than written.
# The failure mode it prevents is not a compile error — it is a BLANK CELL in production, because a
# field renamed in Go becomes `undefined` in TypeScript and renders as an em-dash that looks exactly
# like legitimately absent data.
#
# ⚠️ GOWORK=off for docs-facts-check's reason: inside a parent workspace this target dies with
# `go work use .` instead of reporting drift, and a gate that errors for an unrelated reason is a gate
# nobody reads the output of.
console-types-check:
	GOWORK=off $(GO) run ./cmd/consoletypes -check

## docs-facts: regenerate the facts P23's documentation is generated from and fenced against.
#
# The CLI command registry, the exit-code contract, the metric catalogue and the install-channel
# contract all live in Go. The console's generators and fences are Node scripts. This is the one bridge,
# for the same reason ADR-007 gives for console types: a JavaScript parser for Go source is a second
# implementation of the truth, and it drifts silently.
docs-facts:
	GOWORK=off $(GO) run ./cmd/docsfacts

## docs-facts-check: fail if a command, exit code, metric or install channel changed and the checked-in
## facts did not.
#
# The failure this prevents is P23 Decision 14's: adding a subcommand is a normal Tuesday, and
# remembering the reference is not. With this gate, forgetting is a red build; without it, the product
# accumulates commands nobody can look up.
#
# ⚠️ GOWORK=off, matching the other targets that set it. This repo ships no go.work, but a developer who
# keeps it inside a parent workspace gets `directory cmd/docsfacts is contained in a module that is not
# one of the workspace modules` — a module error, which reads as broken tooling rather than as stale
# facts. That is a second way for this gate to be silent, and it is the way it was silent locally.
docs-facts-check:
	GOWORK=off $(GO) run ./cmd/docsfacts /tmp/heros-docs-facts.json
	@diff -u web/console/src/generated/docs-facts.json /tmp/heros-docs-facts.json \
	  || { echo ""; echo "docs facts are STALE. Run 'make docs-facts' and commit the result."; exit 1; }
	@echo "docs facts are current."

## deploy-up: ONE command — build all three images from this checkout and bring up the backend, the
## customer console and the operator console on this host, then WAIT for the aggregated /readyz and
## prove auth is enforced. Generates credentials once (deploy/.env.local, 0600, git-ignored) and never
## rewrites them. Idempotent: re-run it to apply changes. Needs only Docker.
deploy-up:
	bash deploy/scripts/up.sh

## deploy-down: stop the stack, KEEPING data (named volumes and credentials survive).
deploy-down:
	docker compose --project-directory deploy \
	  --env-file deploy/.env.images.local --env-file deploy/.env.local \
	  -f deploy/docker-compose.platform.yml \
	  -f deploy/docker-compose.platform.build.yml \
	  -f deploy/docker-compose.admin-console.yml down

## deploy-down-hard: stop the stack and DELETE every named volume — Postgres, the ledger, the object
## store, everything. Not an upgrade path and not a rollback: it is the "start over" button, and the
## data it removes is not recoverable from anything this target leaves behind.
deploy-down-hard:
	docker compose --project-directory deploy \
	  --env-file deploy/.env.images.local --env-file deploy/.env.local \
	  -f deploy/docker-compose.platform.yml \
	  -f deploy/docker-compose.platform.build.yml \
	  -f deploy/docker-compose.admin-console.yml down -v

## deploy-lint: P19 deploy gates — digest-pinned images, image-set parity across substrates, ENV-CONTRACT
## parity across substrates, no committed plaintext Secret. Each fails LOUD and names the offender; run
## before every deploy PR. Decision 2 asked for image AND env parity; only images were gated until the
## two substrates had already drifted apart on ADMIN_CONSOLE_HEALTH_URL and seven identity variables.
deploy-lint:
	bash scripts/deploy/check-digest-pins.sh
	bash scripts/deploy/check-image-parity.sh
	bash scripts/deploy/check-env-parity.sh
	bash scripts/deploy/check-no-plaintext-secrets.sh
	bash scripts/deploy/check-mail-relay-pinned.sh
	@echo "== make deploy-lint: PASS =="

## console-test: the customer console's own suite (needs npm; see web/console/README.md)
console-test:
	cd web/console && npm run typecheck && npm test

## operator-ledger: the P26 operator-surface fence — a capability with no recorded operator story fails.
#
# Its own target as well as a step of the operator console's build, for the same reason the
# reproducible-build gate is named as its own CI step: a gate that exists only as one of many tests is
# a gate nobody can see pass, and the day it stops running nothing goes red.
operator-ledger:
	cd web/admin-console && npm run scan:ledger

## operator-hermes: run P26's operator surfaces against a REAL repository (nousresearch/hermes-agent).
#
# The read models are pointed at a real discovered IR and a real checkout, and the run exits non-zero on
# any honesty violation — a refusal with no cause, a permanent boundary that names an artefact, an
# observed-merge claim nobody observed, an inferred deployment version, a key fingerprint long enough to
# be a blob. IR and checkout are overridable:
#
#	make operator-hermes IR=/tmp/ir.json REPO=/tmp/hermes-agent
IR ?= /private/tmp/p23run/ir.json
REPO ?= /private/tmp/p23run/hermes-agent
operator-hermes:
	GOWORK=off $(GO) run ./cmd/proof/operatorsurfaces -ir $(IR) -repo $(REPO)

## operator-console-test: the OPERATOR console's suite (needs npm; see web/admin-console/).
#
# 🔴 Run this with NO `next dev` server running. A dev server clobbers `.next`, which makes the bundle
# scan refuse to measure and manufactures a large number of spurious sign-in failures — a known trap in
# this repository, and one that reads as a broken suite rather than as a broken environment.
operator-console-test: operator-ledger
	cd web/admin-console && npm run typecheck && npm test

## schema: JSON-schema validation gate + contract proofs (task 4.2)
schema:
	$(PYTHON) schemas/validate.py
	$(PYTHON) schemas/test_config_hash.py
	$(PYTHON) schemas/test_schema_evolution.py
	$(PYTHON) schemas/spike_io_contract.py

## classifier-calibration: print the P3.5 pattern-classifier calibration table (per-detector TP/FP/FN,
##                  recall/precision and confidence bands over the hand-labeled fixture set) and the
##                  M4 exit checklist, verdict by verdict. Already covered by `make test`; this target
##                  exists so the NUMBERS are readable rather than merely green — a detector's
##                  confidence is a claim, and a claim wants evidence a human can look at.
classifier-calibration:
	$(GO) test -count=1 -v -run 'TestCalibrationAgainstHandLabeledFixtures|TestM4ExitChecklist|TestDumpShowsEveryStage' ./internal/patternclassifier/

## demo-patterngraph: serve the P3.5 pattern-classified graph view against REAL classifier output, so the
##                 rule-vs-llm distinction and the "not yet classified" empty state can be checked in a
##                 browser. Asserting on markup proves only that the string I wrote is the string I
##                 wrote; three states have to be seen to be verified.
demo-patterngraph:
	$(GO) run ./cmd/demo/patterngraph

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

## sandbox-proof: prove the P3 isolate's least-privilege RUNTIME (sandbox spec §3; tasks 3.2–3.5).
##                          Asserts deploy/docker-compose.sandbox.yml — the concrete
##                          sandbox.NewContainedEnforcer posture — actually enforces: default-deny
##                          network egress (metadata endpoint unreachable), read-only working-set FS,
##                          no ambient provider creds, and cgroup resource bounds — statically (the
##                          field is in the shipped spec) and dynamically (the probe it forbids fails).
##                          Complements internal/sandbox's hermetic Go proofs, which report OS-level
##                          egress-deny + FS-scope as UNAVAILABLE on a bare host (fail-closed). Docker only.
sandbox-proof:
	$(PYTHON) scripts/sandbox_proof.py

## sandbox-proof-redcheck: prove the sandbox proof can FAIL. Weakens the compose spec one guarantee at
##                          a time (drop network_mode:none / /work to :rw / forward OPENAI_API_KEY /
##                          drop pids_limit) and asserts the proof goes red for that specific claim.
sandbox-proof-redcheck:
	$(PYTHON) scripts/sandbox_redcheck.py

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

## pg-proof: live-Postgres proofs. internal/pgmigrate goes FIRST and is the one that applies the whole
##           EMBEDDED SET, in order, exactly as a booting deployment does — every other proof here
##           hand-lists the few migrations its own tables need, which is why nothing applied anything
##           past ~0009 and why a `CREATE TABLE` on an already-existing table reached a green build.
##           Also: live-Postgres proof of the four registries — schema guards + store (P2 1.1/1.6/1.7).
##           Boots an ephemeral Postgres in Docker, so it needs only Docker (no local PG install).
##           These tests are behind the `pgproof` build tag, so `make go` does not compile them; with
##           no database they FAIL rather than skip.
pg-proof:
	bash db/migrations/postgres/run_pg_docker.sh $(GO) test -tags pgproof -count=1 ./internal/pgmigrate/ ./internal/launch/ ./internal/billing/ ./internal/proposalstore/ ./internal/registry/ ./internal/variantspec/ ./internal/worktree/ ./internal/executor/ ./internal/runqueue/ ./internal/submit/ ./internal/e2e/ ./internal/telemetry/ ./internal/evalrun/ ./internal/metering/ ./internal/legal/ ./internal/api/ ./internal/tenancy/ ./internal/signup/ ./internal/herosagent/

## demo-evalboard: stand up the P4 eval board against a live fan-out with a stubbed provider.
##                Everything between the queue and the pixel is the shipped path: the eval set comes
##                from the gap-filling loop, the runs go through the bounded worker pool, the
##                evaluators score real telemetry spans, and the board is the computed view over the
##                score cache. Use -extra-variants to exercise the board's virtualization at scale.
demo-evalboard:
	$(GO) run ./cmd/demo/evalboard

## demo-proposals: serve the P5.5 ranked-recommendation + verification surface (task 7.7). Every verdict on
##           screen comes out of internal/verification.Verify (significance + regression + nothing-
##           unverified); the ONLY stub is the EvalRunner (provider), so the five outcomes (known-good,
##           noise, overfit, cost-regression, cluster-regression), the held-out labelling, the trend
##           view, and the Assisted PR-open gate can be SEEN. Pass -level advisory to check the default.
demo-proposals:
	$(GO) run ./cmd/demo/proposals

## demo-billing: serve the P7 billing/usage surface against the REAL 7a+7b stack — SUM derived from
##           real P2.5 cost events, plans from a config store on disk (never git), entitlements from the
##           real gate, and gainshare computed from a P5.5 verified-delta ledger that deliberately holds
##           an ESTIMATE and an UN-MERGED proposal alongside two verified, merged deltas. The ONLY stub
##           is the billing provider (Stripe-style, test mode). Asserting on a payload proves the
##           payload; the paywall, the dunning banner and the "considered and not billed" group have to
##           be SEEN. Flags drive each first-class state:
##             make demo-billing P7FLAGS="-plan team -over-limit -payment-failed -drift -no-consent"
demo-billing:
	$(GO) run ./cmd/demo/billing $(P7FLAGS)

## demo-billing-states: print the exact command for each first-class billing state, so the browser check
##           (task 9.7) is a repeatable procedure rather than a thing somebody once did. Every state
##           below has to be SEEN: asserting on a payload proves the payload, and the paywall, the
##           dunning banner and the "considered and not billed" group are the reason the surface exists.
demo-billing-states:
	@echo "Drive each state in a browser at http://127.0.0.1:8097/p7/billing:"
	@echo "  1. plan-select + usage/SUM + invoice + verified gainshare evidence:"
	@echo "       make demo-billing P7FLAGS='-plan enterprise'"
	@echo "  2. over-limit with upgrade path (+ dunning + reconciliation drift):"
	@echo "       make demo-billing P7FLAGS='-plan team -over-limit -payment-failed -drift'"
	@echo "  3. gainshare consent absent (the control is absent, not broken):"
	@echo "       make demo-billing P7FLAGS=\"-plan team -no-consent\""
	@echo "  4. consent grant/revoke round trip: click 'Revoke consent' / 'Give consent' on -plan enterprise"
	@echo "  5. rollout DARK — reads work, nothing is charged:"
	@echo "       make demo-billing P7FLAGS='-dark'   then GET /readyz"
	@echo ""
	@echo "The no-hardcoded-money rule is machine-checked, not eyeballed: internal/api TestClientHardcodesNoMoney."

## tidy-check: assert go.mod/go.sum are tidy (no drift)
tidy-check:
	$(GO) mod tidy
	@git diff --exit-code go.mod go.sum || (echo "go.mod/go.sum not tidy; run 'go mod tidy'"; exit 1)

clean:
	$(GO) clean
	rm -rf .gocache

help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'

## release-rehearse: run the P20 release spine locally against a rehearsal tag — plan, native build, version
## stamp, reproducibility gate, merge with cross-check, sign, attest, fail-closed gate, notes. Needs no
## secret and publishes nothing. This is what makes the release gates debuggable without pushing a tag.
release-rehearse:
	GOWORK=off bash scripts/release-rehearse.sh $(TAG)

## release-rehearse-redcheck: guards the guard — runs the rehearsal with FOUR of the five targets absent and
## requires the completeness gate to refuse. A gate never shown to reject is treated as absent.
release-rehearse-redcheck:
	@echo "== release gate red-check: an incomplete matrix must be REFUSED =="
	@if GOWORK=off bash scripts/release-rehearse.sh $(or $(TAG),v0.20.0-rc.1) --real-only 2>&1 \
	    | tee /dev/stderr | grep -q "release gate  *: ⛔ refused"; then \
	  echo "== red-check PASS: the gate refused an incomplete matrix =="; \
	else \
	  echo "== red-check FAIL: the gate did NOT refuse an incomplete matrix =="; exit 1; \
	fi

## readme-install: regenerate the README's install section from the channel contract (D5/task 8.1). The section
## is generated-and-checked rather than hand-written, so a channel that becomes unavailable cannot keep sending
## users to a command that fails. TestReadmeInstallSectionMatchesContract is the gate.
readme-install:
	GOWORK=off $(GO) run ./cmd/herosdist readme $(if $(TAG),--tag $(TAG),) $(if $(DIST),--dir $(DIST),) --write
	@echo "== README install section regenerated =="

## packaging-proof: hand every generated package manifest to the tool that reads it — ruby for the formula, a
## JSON/YAML parse for scoop and winget, a real nfpm .deb/.rpm build, and a container image that is BUILT AND
## RUN. Content tests cannot tell you whether the consuming tool accepts the file.
packaging-proof:
	GOWORK=off bash scripts/packaging_proof.sh $(or $(DIST),dist)

## install-smoke-refusals: the four tamper/refusal cases only. Needs NO signing key, because every one of them
## must be refused regardless of which key signed the fixture — the installers pin their own. This is the mode
## CI runs on a pull request.
install-smoke-refusals:
	GOWORK=off $(PYTHON) scripts/install_smoke.py --host --refusals-only

## install-smoke: fresh-machine install matrix + tamper red-check for scripts/install.sh — on this host and in a
## genuinely clean debian:12 container with a real natively-built linux binary. Needs
## HEROS_RELEASE_PRIVATE_KEY, because the point is that the key PINNED in the installer verifies; it fails
## rather than skips without it.
install-smoke:
	GOWORK=off $(PYTHON) scripts/install_smoke.py

## mail-proof: send ONE real message through the deployment's configured relay, using the production code
## path (mailer.New + a real ResetPassword body). This is the layer `go test` cannot reach: the mailer's own
## tests cover the fallback, header injection, the clear-text refusal and the bodies — none of which answers
## "does mail leave this deployment and arrive?".
##
## Credentials come from the secret store and are passed as ENVIRONMENT, never on a command line: a password
## in argv is in the shell history and in every process listing. Nothing here echoes them.
##
##   make mail-proof TO=you@example.com
##
## ⚠️ Amazon SES is in the SANDBOX until production access is granted, and a sandbox account only delivers to
## addresses that are separately verified. So a green run here proves the relay path and NOT that a stranger
## signing up would receive anything — check `aws sesv2 get-account --query ProductionAccessEnabled` before
## reading this as production-ready.
mail-proof:
	@test -n "$(TO)" || { echo "usage: make mail-proof TO=you@example.com"; exit 2; }
	@GOWORK=off $(PYTHON) scripts/mail_proof.py "$(TO)"

## agent-status: what the analysis agent is actually doing on a deployment, read from /readyz.
##
## 🔴 It reads the LIVE signal rather than a config file, which is the whole of task 9.1: `heros_agent`
## on /readyz is resolved by doing what an inference does — reading the active definition, RESOLVING the
## credential through the same secrets source the runner calls, and comparing the real meter against the
## real ceiling. A green line here means an inference would work, not that somebody set a variable.
##
##   make agent-status                                  # the local deployment
##   make agent-status READYZ=https://heros-agent.space/readyz
agent-status:
	@GOWORK=off $(PYTHON) scripts/agent_status.py "$(or $(READYZ),http://127.0.0.1:4321/readyz)"

## agent-rollout: whether this fleet's CURRENT SHAPE permits the next rollout stage (task 9.4).
##
## 🚫 "No stage verified by hand" is the task's own words, and this is what replaces the hand. It counts
## the placement table, reads the active definition's rehearsal state and checks the fleet ceiling —
## then prints the one precondition that fails, or that the step is permitted. It CHANGES NOTHING: an
## operator still sets each placement deliberately, with a reason, because automating enablement would
## put "read a customer's source under a platform credential" behind a scheduler.
##
##   make agent-rollout                       # what stage this fleet is at
##   make agent-rollout WANT=partner          # may it advance to `partner`?
agent-rollout:
	@GOWORK=off $(GO) run ./cmd/agentrollout -want "$(WANT)"

## agent-drills: the P30 mutation drills — defeat each fence, confirm it goes RED, restore.
##
## 🔴 `-count=1` on every run, because a mutation followed by a same-second test run reads a CACHED
## PASS and reports a real fence as dead. That has happened on this repository before.
agent-drills:
	@GOWORK=off $(PYTHON) scripts/agent_drills.py

## agent-acceptance: P30 task 10.13 — the LIVE acceptance, four layers.
##
## 🔴 A 200 is none of the four. Setting the placement is layer 1 and is deliberately EXPLICIT: it
## defaults to `disabled`, and an acceptance that inherits a default stops proving anything the day the
## default changes. Layer 2 spends REAL TOKENS against a live provider.
##
## 🚫 A layer that cannot run prints NOT RUN and the command exits non-zero. There is no "skipped" that
## reads as green — a partial acceptance reported as an acceptance is how a capability ships having
## never once worked end to end.
##
##   make agent-acceptance TENANT=acme WORKFLOW=openclaw/openclaw \
##     API=https://heros-agent.space CONSOLE=https://heros-agent.space
agent-acceptance:
	@test -n "$(TENANT)" || { echo "usage: make agent-acceptance TENANT=<id> WORKFLOW=<id>"; exit 2; }
	@test -n "$(WORKFLOW)" || { echo "usage: make agent-acceptance TENANT=<id> WORKFLOW=<id>"; exit 2; }
	@GOWORK=off $(PYTHON) scripts/agent_acceptance.py \
		--tenant "$(TENANT)" --workflow "$(WORKFLOW)" \
		$(if $(API),--api "$(API)",) $(if $(CONSOLE),--console "$(CONSOLE)",)
