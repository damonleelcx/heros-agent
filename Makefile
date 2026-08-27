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

.PHONY: ci go build vet fmt test schema lint db-proof pg-proof pgproof-packages verifier-proof tidy-check clean help demo-evalboard \
        deploy-lint deploy-up deploy-down deploy-down-hard \
        console-types console-types-check console-tokens console-test operator-console-test operator-ledger operator-hermes docs-facts docs-facts-check \
        build-discover discovery-ci discovery-throughput \
        discovery-parity-snapshot discovery-parity-verify \
        discovery-sandbox-proof discovery-sandbox-proof-redcheck \
        sandbox-proof sandbox-proof-redcheck \
        classifier-calibration demo-patterngraph demo-proposals demo-billing demo-billing-states \
        release-rehearse release-rehearse-redcheck readme-install packaging-proof install-smoke install-smoke-refusals \
        agent-rehearse agent-status repo-intake-hermes assessment-hermes axissplit-hermes agentgraph-hermes sourcebound-hermes \
        intent-holdout intent-holdout-strict attribution-holdout p31-fence-redcheck p33-fence-redcheck p34-fence-redcheck p35-fence-redcheck console-edge-proof assessment-holdout p35-live-four-step improve-hermes

## ci: the locally-provable gate (go + schema + console-types + discovery-ci + intent-holdout). Lint/db-proof run as their own CI jobs.
ci: go schema console-types-check docs-facts-check discovery-ci intent-holdout-strict
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

## console-tokens: the customer console's design-language fence — no colour/spacing/type/radius literal.
#
# Its own target for the same reason operator-ledger is one: `npm run build` runs it too, and a gate
# that exists only inside a composite command is a gate nobody can see pass.
console-tokens:
	cd web/console && npm run scan:tokens

## console-test: the customer console's gates — design-language fence, types, build, and its 661 tests.
#
# 🔴 The BUILD is not optional, and this target went without it long enough for the omission to matter.
# tests/support/harness.mjs runs `next start` against `.next` and fails loud when there is no production
# build, so on a clean checkout this target could not complete — and no CI job ran it, so nothing said
# so. It is also what runs scan:origins, scan:events, scan:strings, scan:markup, scan:claims, scan:docs
# and scan:bundle, none of which exist anywhere else.
#
# 🔴 Run this with NO `next dev` server running. A dev server clobbers `.next`, which makes the bundle
# scan refuse to measure and manufactures a large number of spurious sign-in failures — a known trap in
# this repository, and one that reads as a broken suite rather than as a broken environment.
#
# HEROS_RELEASE_OFFLINE=1 for the reason CI pins it: gen:release-assets builds the install page's
# checksum table from the latest published GitHub Release, so an online build makes this target's input
# change when a release is cut. A gate whose answer moves without the commit moving is not a gate.
console-test: console-tokens
	cd web/console && npm run typecheck
	cd web/console && HEROS_RELEASE_OFFLINE=1 npm run build
	cd web/console && npm test

## operator-ledger: the P26 operator-surface fence — a capability with no recorded operator story fails.
#
# Its own target as well as a step of the operator console's build, for the same reason the
# reproducible-build gate is named as its own CI step: a gate that exists only as one of many tests is
# a gate nobody can see pass, and the day it stops running nothing goes red.
operator-ledger:
	cd web/admin-console && npm run scan:ledger

## repo-intake-hermes: run P32's whole intake pipeline against a REAL repository over the REAL network.
##
## Every P32 fence is green, and green fences prove the parts. This proves the WALK a customer performs
## — connect, clone from github.com, guard, archive, store, extract, discover, read the ledger, revoke,
## and prove the tree is gone — against `nousresearch/hermes-agent`, a repository nobody here wrote.
##
## It stands up an ephemeral Postgres so the grant, the snapshot and the ledger are real rows.
##
## 🔴 The one thing NOT real is the credential: hermes-agent is public and GitHub serves it to any
## basic-auth pair, so the run uses a placeholder and says so in its own output. A private repository
## needs a grant a customer creates.
repo-intake-hermes:
	bash db/migrations/postgres/run_pg_docker.sh $(GO) run ./cmd/proof/repointake

## assessment-hermes: run P33's whole assessment against a REAL repository over the REAL network.
##
## Every P33 fence is green and every one has been drilled red. Green fences prove the parts. This
## proves the WALK a customer gets — clone, discover, assess nine axes, PERSIST, SELECT the findings
## back, assert nine axes and resolvable evidence, assess again and prove the report is byte-identical
## — against `nousresearch/hermes-agent`, a repository nobody here wrote.
##
## It stands up an ephemeral Postgres, because the assessment is persisted and read back: a return
## value is not evidence of a write.
##
## 🚫 Every finding it produces is STRUCTURAL. Inference is gated on a holdout run that has not
## happened and measurement needs the sandbox to execute customer code, so no provider is called and
## nothing costs money.
##
## 🔴 P34 CHANGED WHAT THIS PRINTS, and the change is the phase's end-to-end proof. `loop` and `graph`
## used to report `refused` naming P34 — the configuration layer had no such axes. They now REPORT:
## `loop` reads the discovered control loop, and `graph` names which of the frontend, the analysis or
## the language support is missing (FR18) rather than a generic unsupported state. `harness` narrowed to
## the EXECUTION ENVELOPE, which a source snapshot structurally cannot contain, so it is `not_measured`
## with that reason rather than `observed`.
##
## So against hermes-agent the tally is now `0 measured · 4 observed · 5 not_measured · 0 REFUSED`.
## Zero refusals is the correct answer, not a suppressed one: `Axis.P34Pending()` returns false for
## every axis because both configurations now exist.
assessment-hermes:
	bash db/migrations/postgres/run_pg_docker.sh $(GO) run ./cmd/proof/assessment

## axissplit-hermes: run P34's three axes against a REAL repository's own call sites.
##
## Every P34 fence is green and all fourteen have been drilled red (`make p34-fence-redcheck`). Green
## fences prove the parts, against a two-node fixture this repository wrote with hand-built io_contracts
## chosen to make the assertion clean. That is the right shape for a fence and it is not evidence about
## a customer.
##
## This proves the WALK: it discovers `nousresearch/hermes-agent`'s actual call sites and authors P34
## configurations against the node ids that come out, so every refusal it prints names a symbol somebody
## else wrote. Eight gates: the turn ceiling naming both numbers, a loop that fits, the host-service
## refusal moved left, the ambiguity refusal naming both refs, the legacy path still resolving, a fan-in
## with no merge refused at validate, the same fan-in refused at transform by name, and each axis's
## declared coverage for the repository's language.
##
## 🚫 It calls NO provider and costs nothing. Every P34 gate is a resolve-time gate by construction —
## that is the phase's central claim — so proving them needs no run, no sandbox and no credential.
##
## It needs a checkout; clone it first (a public repository needs no token):
##
##	git clone --depth 1 https://github.com/nousresearch/hermes-agent /tmp/hermes-agent
##	make axissplit-hermes
## agentgraph-hermes: run P36's multi-node agent against a REAL repository's own call sites.
##
## Every P36 fence is green and every one has been drilled red. Green fences prove the parts, against a
## definition this repository wrote over a hand-built IR chosen to make the assertion clean. That is the
## right shape for a fence and it is not evidence about a customer.
##
## This proves the WALK. It discovers `nousresearch/hermes-agent`'s actual call sites and runs a
## THREE-NODE agent over them — a triage, an analyst, and a critic behind a conditional edge — so every
## node id in the per-node attribution is a symbol somebody else wrote. Fourteen steps: the single-node
## hash held byte-identical, the fan-in refusal proved to be the CUSTOMER's own sentence from the
## customer's own function, a predicate out of scope refused by the expression path, a loop refused at
## publish, activation refused before rehearsal, the walk itself, per-node attribution on the
## repository's ids, the conditional edge taken BOTH ways, twenty byte-identical repeats, rollback as
## one act, the pin surviving two activations, and the per-node health document.
##
## 🚫 It calls NO provider and costs nothing. The models are a deterministic local stub: every claim is
## about the SHAPE of the run — which node was entered, which was routed around, what each contributed —
## and a version that called a real model would be measuring the model instead.
##
## It needs a checkout; clone it first (a public repository needs no token):
##
##	git clone --depth 1 https://github.com/nousresearch/hermes-agent /tmp/hermes-agent
##	make agentgraph-hermes
agentgraph-hermes:
	@test -d "$(HERMES)" || { echo "agentgraph-hermes: no checkout at $(HERMES)"; \
		echo "  git clone --depth 1 https://github.com/nousresearch/hermes-agent $(HERMES)"; exit 2; }
	GOWORK=off $(GO) run ./cmd/proof/agentgraph -local $(HERMES)

HERMES ?= /tmp/hermes-agent

## sourcebound-hermes: run P37's source-bound editors against a REAL repository's own call sites.
##
## Every P37 fence is green and every one has been drilled red. Green fences prove the parts, against a
## two-node fixture this repository wrote with values chosen to make the assertion clean. That is the
## right shape for a fence and it is not evidence about a customer.
##
## This proves the WALK, and the walk is the phase's whole claim: it discovers
## `nousresearch/hermes-agent`'s actual call sites, builds the structure the platform would hold, and
## reads each node's CURRENT value on each of the nine axes — so every value it prints is a reading of a
## file this repository has never seen.
##
## 🔴 What it is looking for is NOT "everything resolved". It exits non-zero when a value was SUPPLIED
## rather than read — a sentinel rendered as a model, an `observed` with nothing to show, a
## `not_measured` that names nothing. A run where everything resolved would mean the sentinel check
## never fired, which is a weaker result rather than a stronger one.
##
## 🔴 On 2026-08-27 it found that ALL 29 of hermes-agent's nodes carry `model_id = "unresolved"` —
## discovery's own sentinel. A naive `!= ""` check would have rendered a model called "unresolved" as
## the current value on every one of them. That is P37 §5.3's defect firing on 100% of a real
## repository, and it is why the check exists.
##
## 🚫 It calls NO provider and costs nothing: every read P37 adds is a resolve-time read by construction.
##
## It needs a checkout; clone it first (a public repository needs no token):
##
##	git clone --depth 1 https://github.com/nousresearch/hermes-agent $(HERMES)
##	make sourcebound-hermes
sourcebound-hermes:
	@test -d "$(HERMES)" || { echo "sourcebound-hermes: no checkout at $(HERMES)"; \
		echo "  git clone --depth 1 https://github.com/nousresearch/hermes-agent $(HERMES)"; exit 2; }
	GOWORK=off $(GO) run ./cmd/proof/sourcebound -local $(HERMES)

axissplit-hermes:
	@test -d "$(HERMES)" || { echo "axissplit-hermes: no checkout at $(HERMES)"; \
		echo "  git clone --depth 1 https://github.com/nousresearch/hermes-agent $(HERMES)"; exit 2; }
	GOWORK=off $(GO) run ./cmd/proof/axissplit -local $(HERMES)

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
# 🔴 The BUILD is not optional here either, and for the same reason: since P24 this suite starts
# `next start` to assert a real Content-Security-Policy header, so without a production build every
# signed-in assertion fails against a console that is entirely correct. The operator console's own
# `npm run build` is what runs scan:origins, scan:events, scan:tokens, scan:ledger and scan:bundle.
operator-console-test: operator-ledger
	cd web/admin-console && npm run typecheck
	cd web/admin-console && npm run build
	cd web/admin-console && npm test

## schema: JSON-schema validation gate + contract proofs (task 4.2)
schema:
	$(PYTHON) schemas/validate.py
	$(PYTHON) schemas/test_config_hash.py
	$(PYTHON) schemas/test_schema_evolution.py
	$(PYTHON) schemas/spike_io_contract.py

## intent-holdout: P31's intent router against its held-out labelled set (task 3.4/3.5).
#
# 🔴 Run this BEFORE landing any change to the router or its term table, and again after — including for
# a change that "cannot possibly alter behaviour". There is no pure-refactor exemption: the last time
# somebody was sure about that here, the golden test they were relying on had never actually run.
#
# It prints FOURTEEN ROWS and no overall accuracy. A mean over fourteen intents can sit at 93% while the
# intent that answers "what did you NOT measure" is routed correctly one time in three.
#
#	make intent-holdout            # print the table
#	make intent-holdout STRICT=1   # exit non-zero if any intent is below its floor (what CI asserts)
STRICT ?=
intent-holdout:
	GOWORK=off $(GO) run ./cmd/proof/intentrouting $(if $(STRICT),-strict,)

# intent-holdout-strict is what `make ci` runs: the same evaluation, failing the build on any intent
# below its floor. Separate from `intent-holdout` so a person can look at the table without being told
# off, which is the difference between a tool and a gate.
intent-holdout-strict:
	GOWORK=off $(GO) run ./cmd/proof/intentrouting -strict

## console-edge-proof: assert a conversation stream arrives INCREMENTALLY through a running edge (task 5.2).
#
# 🔴 The manifests REQUEST that each hop not buffer; this is the assertion that one listened. A reverse
# proxy that buffers turns streaming into batching, the stream still completes, nothing errors, and the
# failure is indistinguishable from slowness at the application layer — there is no status code for it.
#
#	make console-edge-proof CONSOLE_URL=https://… CONSOLE_COOKIE='heros_session=…' CONVERSATION_ID=conv_…
#
# It REFUSES rather than skipping when those are unset: a check that passes having measured nothing is
# worse than no check.
console-edge-proof:
	cd web/console && npm run edge-proof

## p31-fence-redcheck: prove P31's server-side refusals can go RED (tasks 6.1, 6.12, 6.13, 6.14, 6.16).
#
# 🔴 Three tasks in P31 §6 say "Mutate the check; the test must fail" in those words. This is that,
# mechanised: each refusal is removed from the real source in turn, the test that claims to catch it is
# run, and the drill fails if that test still passes.
#
# It refuses to run on a dirty working tree — it rewrites source and restores from memory, and it cannot
# tell work in progress from a mutation a previous crash failed to clean up.
p31-fence-redcheck:
	$(PYTHON) scripts/p31_fence_redcheck.py

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
##
## 🔴 THE PACKAGE LIST IS `PGPROOF_PKGS`, AND IT IS THE ONLY COPY. It used to be written twice — here
##    and in .github/workflows/ci.yml — and both had drifted: `internal/reportstore` and
##    `internal/deliveryrecord` were in NEITHER, so their proofs had never run anywhere. CI now reads
##    this same variable through `pgproof-packages`, and `internal/deploy/pgproof_gate_test.go` asserts
##    every package carrying a `pgproof` build tag appears in it or is excepted WITH A REASON.
pg-proof:
	bash db/migrations/postgres/run_pg_docker.sh $(GO) test -tags pgproof -count=1 $(PGPROOF_PKGS)

## pgproof-packages: print PGPROOF_PKGS, one per line — what CI runs.
##
## 🔴 CI reads THIS rather than keeping its own list. The comment that used to stand in ci.yml said the
## quiet part out loud — "This list is maintained BY HAND and does not read the pg-proof make target,
## so a new pgproof package must be added here as well or its migration ships untested in CI" — and
## that is exactly what went wrong. Two hand-maintained copies of one fact drift; the only question is
## when.
pgproof-packages:
	@printf '%s\n' $(PGPROOF_PKGS)

## PGPROOF_PKGS: every package with live-Postgres proofs. `internal/pgmigrate` FIRST, because it is the
## one that applies the whole EMBEDDED set in order, exactly as a booting deployment does, while every
## other proof hand-lists the few migrations its own tables need.
##
## 🔴 Written out rather than discovered by grep, deliberately. Auto-discovery would silently pull a
## NEW and RED proof into the gate, and "adding a red package to the gate would break the gate for
## everybody, which is how a gate gets bypassed" — the reasoning `pgproof_gate_test.go` already
## records. Adding a package here stays a decision somebody makes; the fence is what makes forgetting
## impossible.
PGPROOF_PKGS = ./internal/pgmigrate/ ./internal/adminops/ ./internal/api/ \
	./internal/assessment/ ./internal/billing/ ./internal/deliveryrecord/ \
	./internal/deliveryroute/ ./internal/e2e/ ./internal/evalrun/ ./internal/executor/ \
	./internal/herosagent/ ./internal/launch/ ./internal/legal/ ./internal/metering/ \
	./internal/pgtest/ ./internal/proposalstore/ ./internal/registry/ ./internal/reportstore/ \
	./internal/runqueue/ ./internal/signup/ ./internal/sourceingest/ ./internal/submit/ \
	./internal/telemetry/ ./internal/tenancy/ ./internal/variantspec/ ./internal/worktree/

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

## p33-fence-redcheck: prove P33's honesty checks can go RED (tasks 7.1, 7.2, 7.6, 7.7, 7.9).
#
# 🔴 Task 7.1 says "Mutate the extractor to return a default; the test must fail" in those words. This
# is that, mechanised: each rule is broken in the real source in turn, the test that claims to catch it
# is run, and the drill fails if that test still passes. Every mutation must COMPILE first — a mutation
# that does not build also exits non-zero, and accepting that would report a fence as proven when it
# was never run.
p33-fence-redcheck:
	$(PYTHON) scripts/p33_fence_redcheck.py

## p34-fence-redcheck: P34 §9 — prove the twelve QA fences can actually FAIL.
##
## 🔴 §9 is titled "fences that can go red", and the title is the requirement. Every rule this phase adds
## is a few lines; weakening one leaves the whole suite green except the test that names it. The drill
## breaks each rule, asserts the package STILL COMPILES (a mutation that does not build exits non-zero
## for a reason that has nothing to do with the fence), and asserts the test goes red.
##
## 9.10 is inverted and runs separately: its claim is that a missing Kind case fails to BUILD, so there
## a successful compile is the failure.
p34-fence-redcheck:
	$(PYTHON) scripts/p34_fence_redcheck.py

## ## attribution-holdout: P34 §6.4 — attribution under OVERLAPPING spans, measured on a holdout.
##
## 🔴 PRD §9.5 requires this proved BEFORE concurrency ships, with no pure-refactor exemption. It prints
## three lines: the sequential baseline, the same cases with the spans overlapping and ordered by START
## TIME (the defect — the answer flips on a nanosecond of scheduling), and the same cases ordered by the
## SPEC'''s DECLARED order (the fix, which is replay-consistent). A run whose middle line stopped flipping
## would mean the comparison no longer demonstrates anything, and the test says so.
attribution-holdout:
	GOWORK=off $(GO) test -count=1 -v -run "TestAttributionDoesNotDegradeUnderOverlappingSpans|TestBothNodesDivergingIsWhereOrderActuallyDecides|TestTheOverlapHoldoutCanActuallyFail" ./internal/attribution/

assessment-holdout: score P33's inference against the holdout set (§3.4, §3.5).
##
## 🔴 IT NEEDS A REAL PROVIDER. Without one there is nothing to measure: the suite that runs in `make
## go` uses a SCRIPTED analyst and therefore measures the harness — that abstention counts as a
## success, that precision is per axis, that a wrong answer is caught — and nothing whatever about a
## model. Set HEROS_HOLDOUT_MODEL to a registered model entry and provide the deployment's secrets
## source, then run this. Until somebody does, the per-axis precision and abstention rate of the
## actual inference are UNMEASURED, and the phase's tasks say so rather than a green suite implying
## otherwise.
assessment-holdout:
	GOWORK=off $(GO) test -count=1 -v -tags holdout -run TestHoldoutAgainstARealProvider ./internal/assessment/

## agent-rehearse: run the pinned calibration set against a live model and print the verdict.
##
## 🔴 The gate is otherwise only reachable by pressing activate in the operator console, which measures
## on the deployment's own credential and stores the result on a version row. That makes a FAILING
## rehearsal impossible to investigate without publishing another definition. This runs the same
## pieces — the same fixtures, the same assembler, the same validator — against any prompt and model,
## and prints every abstention, which is what says whether a fixture's zero was the model answering
## nothing or the validator refusing everything it answered.
##
## ⚠️ IT SPENDS: one provider call per fixture. Use DRY_RUN=1 to print exactly what each fixture would
## send and call nothing.
##
##   make agent-rehearse PROMPT=prompt.txt DRY_RUN=1        # no provider call, no spend
##   make agent-rehearse PROMPT=prompt.txt                  # 9 live calls
##   make agent-rehearse PROMPT=prompt.txt MODEL=claude-opus-5 PROVIDER=anthropic OUT=/tmp/r.json
agent-rehearse:
	@test -n "$(PROMPT)" || { echo "usage: make agent-rehearse PROMPT=<prompt-file> [DRY_RUN=1] [MODEL=…] [PROVIDER=…] [OUT=…]"; exit 2; }
	@GOWORK=off go run ./cmd/proof/rehearsal \
		-prompt-file "$(PROMPT)" \
		$(if $(DRY_RUN),-dry-run,) \
		$(if $(MODEL),-model "$(MODEL)",) \
		$(if $(PROVIDER),-provider "$(PROVIDER)",) \
		$(if $(OUT),-out "$(OUT)",)

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

## p35-live-four-step: P35 7.14 / A13 — approve → approval row → delivery record → THE PULL REQUEST.
##
## 🔴 §9.3 in one line: a 200 is not evidence of a write. Every other P35 fence proves the platform
## CALLED something correctly; none of them proves a pull request exists. A delivery that returns 200
## has not necessarily produced one, and a pull request existing is not a delivery record — the two can
## each be true with the other false. Only step 4 talks to a forge.
##
## ⚠️ IT OPENS A REAL PULL REQUEST on the repository you name, and leaves it open. It never merges one.
##
##   HEROS_LIVE_FORGE_TOKEN=...  a token with pull_requests:write and contents:write
##   HEROS_LIVE_FORGE_REPO=owner/repo
##   HEROS_LIVE_FORGE_BASE=main  (optional)
p35-live-four-step:
	GOWORK=off $(GO) test -count=1 -v -tags live -run TestLiveFourStep ./internal/improvementrun/

## p35-fence-redcheck: P35 §7 — prove the gate fences can actually FAIL.
##
## 🔴 §9.7 is titled "green is worth having only if green can be red", and the title is the requirement.
## Every gate P35 must not bypass is a few lines at a call site; deleting one leaves the whole suite
## green except the fence that names it. The drill breaks each gate, asserts the package STILL COMPILES
## (a mutation that does not build exits non-zero for a reason unrelated to the fence), and asserts the
## named fence goes red.
p35-fence-redcheck:
	$(PYTHON) scripts/p35_fence_redcheck.py

## improve-hermes: run P35's improvement run against a REAL repository's own call sites.
##
## Every P35 fence is green and all twenty gates have been drilled red (`make p35-fence-redcheck`).
## Green fences prove the parts, against fixtures this repository wrote, with node ids and deltas chosen
## to make the assertion clean. That is the right shape for a fence and it is NOT evidence about a
## customer's repository.
##
## This proves the WALK. It discovers `nousresearch/hermes-agent`'s actual call sites, reads the source
## revision out of the checkout, builds a bounded plan against the ids that come out, drives the SHIPPED
## `optimizer.Controller` under that plan's bounds, and prints every refusal naming a symbol somebody
## else wrote.
##
## 🔴 WHAT IS NOT REAL, printed by the command itself in its first four lines: the VERDICTS. Verification
## runs the customer's eval harness on the customer's machine against the customer's eval set, and this
## command has none of the three. Every delta it shows is labelled DECLARED.
##
## 🚫 IT OPENS NO PULL REQUEST. hermes-agent is somebody else's repository. `make p35-live-four-step` is
## the one that talks to a forge, against a repository whoever runs it chose.
##
## 🚫 It calls NO provider and costs nothing.
##
## It needs a checkout; clone it first (a public repository needs no token):
##
##	git clone --depth 1 https://github.com/nousresearch/hermes-agent $(HERMES)
##	make improve-hermes
improve-hermes:
	@test -d "$(HERMES)" || { echo "improve-hermes: no checkout at $(HERMES)"; \
		echo "  git clone --depth 1 https://github.com/nousresearch/hermes-agent $(HERMES)"; exit 2; }
	GOWORK=off $(GO) run ./cmd/proof/improvehermes -local $(HERMES)
