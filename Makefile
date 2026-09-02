# heros — local CI entrypoints.
#
# 🔴 GOWORK=off on every target. This checkout sits under a parent directory that contains a go.work
# listing OTHER modules, which captures this one and makes `go test ./...` fail with a workspace error
# that names nothing about this repository. Setting it here rather than documenting it means nobody has
# to remember, and a fresh clone behaves the same as a developer's machine.
export GOWORK := off

# The conformance suite runs against BOTH store implementations. The Postgres leg SKIPS without this
# variable, and a skip is not a pass — TestZZPostgresLegActuallyRan fails if the DSN is set but no
# Postgres subtest ran, so a stopped container cannot masquerade as a green build.
HEROS_TEST_DATABASE_URL ?= postgres://heros:heros@localhost:55700/heros?sslmode=disable
export HEROS_TEST_DATABASE_URL

PG_CONTAINER := heros-dev-pg

.PHONY: help test race vet fmt fmt-check cover check clean pg-up pg-down pg-psql

help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n",$$1,$$2}'

test: ## Run all tests
	go test ./...

race: ## Run all tests under the race detector (lease correctness depends on this)
	go test -race ./...

vet: ## go vet
	go vet ./...

fmt: ## Format
	gofmt -w .

fmt-check: ## Fail if anything is unformatted
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "unformatted:"; echo "$$out"; exit 1; fi

cover: ## Coverage summary
	go test -coverprofile=/tmp/heros.cover ./... && go tool cover -func=/tmp/heros.cover | tail -1

check: fmt-check vet race ## Everything CI runs

pg-up: ## Start the local Postgres the conformance suite needs
	@docker start $(PG_CONTAINER) 2>/dev/null || \
	  docker run -d --name $(PG_CONTAINER) -e POSTGRES_PASSWORD=heros -e POSTGRES_USER=heros \
	    -e POSTGRES_DB=heros -p 55700:5432 postgres:17
	@until docker exec $(PG_CONTAINER) pg_isready -U heros >/dev/null 2>&1; do sleep 1; done
	@echo "postgres ready on :55700"

pg-down: ## Stop the local Postgres
	@docker stop $(PG_CONTAINER) >/dev/null 2>&1 || true

pg-psql: ## Open a shell on the local Postgres
	@docker exec -it $(PG_CONTAINER) psql -U heros -d heros

clean:
	rm -f /tmp/heros.cover

# ---- deployment ---------------------------------------------------------------------------------
# eval.heros-agent.space, on the k3s node i-05f4712279b04fac5. See deploy/README.md.

INSTANCE   := i-05f4712279b04fac5
# The repository name is stated once: `deploy` and `deploy-push` both ask ECR what digest a tag
# resolved to, and two copies of the name is one rename away from a deploy that reads the digest of a
# different repository and applies it.
ECR_REPO   := heros-eval
ECR        := 373468206837.dkr.ecr.us-east-1.amazonaws.com/$(ECR_REPO)
# 🔴 Defaulted from the HOST's Go configuration, not left empty.
#
# The build runs `go mod download` inside the container, which reaches the module proxy on its own —
# it does not inherit the developer's shell proxy. On a machine that needs a mirror (this repository
# has been built on one where proxy.golang.org is unreachable), the empty default fails after a
# two-minute timeout with `dial tcp …: i/o timeout`, which reads as a network blip rather than as a
# missing flag, and the fix has to be rediscovered every time.
#
# Whatever the developer's own `go` is configured to use already works for them, so it is the right
# default. On a normal machine this expands to `https://proxy.golang.org,direct` and passing it
# explicitly changes nothing. Empty when Go is not installed, which restores the previous behaviour.
#
# NOT exported: this only feeds the --build-arg below. Exporting it would change what `go test` on
# this machine resolves against, which is not this variable's business.
GOPROXY    ?= $(shell go env GOPROXY 2>/dev/null)
# 🔴 The node is aarch64. An image built for the builder's own architecture on a non-arm machine
# lands in ECR, pulls successfully, and then CrashLoopBackOffs with `exec format error`.
PLATFORM   := linux/arm64
# Tagged by commit so a tag answers "what is running" — dirty when the tree has uncommitted changes,
# because a tag naming a commit that does not describe the build is worse than no tag.
TAG        := $(shell git rev-parse --short HEAD)$(shell git diff --quiet HEAD -- . ':!deploy' || echo -dirty)

.PHONY: deploy deploy-build deploy-push deploy-apply deploy-status

# deploy is the whole path: build, push, and apply THE DIGEST THAT ACTUALLY LANDED IN ECR.
#
# ⚠️ It was named in .PHONY above and documented in deploy/README.md, and it did not exist. Anybody
# following the runbook got `No rule to make target 'deploy'` and had to reconstruct the sequence,
# which is how a hand-typed DIGEST from an earlier push ends up being applied.
#
# 🔴 The digest is read back from the REGISTRY, never taken from the local build. That is the rule
# `deploy-apply` states, and it is preserved here rather than worked around: what a local build called
# itself and what is addressable in ECR are two different facts, and only the second one is what the
# cluster will pull. Reading it back also proves the push actually landed.
#
# 🔴 The VALUE is checked to be a digest, rather than the exit status being trusted. Asked for a tag
# that is not there, `aws ecr describe-images` was observed to write ImageNotFoundException to stderr
# and nothing to stdout; other CLI versions print the string `None`. Neither reliably fails the
# assignment, so an unguarded version hands `apply.sh` an empty string or the word "None" and the
# failure surfaces as a confusing manifest error instead of "the push did not land". Checking the
# shape catches both without depending on which version is installed.
deploy: deploy-push ## Build, push, and apply — the whole path in one command
	@digest=$$(aws ecr describe-images --repository-name $(ECR_REPO) \
	    --image-ids imageTag=$(TAG) --query 'imageDetails[0].imageDigest' --output text); \
	  case "$$digest" in \
	    sha256:*) ;; \
	    *) echo "ECR has no digest for tag $(TAG) (got '$$digest') — did the push succeed?" >&2; exit 1;; \
	  esac; \
	  echo "applying $$digest"; \
	  bash deploy/apply.sh "$$digest"

deploy-build: ## Build the container for the deployment's architecture
	docker build --platform $(PLATFORM) $(if $(GOPROXY),--build-arg GOPROXY=$(GOPROXY),) \
	  -f deploy/Dockerfile -t $(ECR):$(TAG) .

deploy-push: deploy-build ## Push to ECR and print the digest
	aws ecr get-login-password --region us-east-1 | \
	  docker login --username AWS --password-stdin $(firstword $(subst /, ,$(ECR)))
	docker push $(ECR):$(TAG)
	@echo "digest: $$(aws ecr describe-images --repository-name $(ECR_REPO) \
	  --image-ids imageTag=$(TAG) --query 'imageDetails[0].imageDigest' --output text)"

deploy-apply: ## Apply the manifests at the pushed digest (DIGEST=sha256:…)
	@test -n "$(DIGEST)" || { echo "DIGEST=sha256:… is required — read it back from ECR after the push, not from the local build"; exit 2; }
	@bash deploy/apply.sh "$(DIGEST)"

deploy-status: ## What is actually running
	@bash deploy/ssm.sh 'k3s kubectl -n heros-eval get pods,ingress,certificate -o wide'
