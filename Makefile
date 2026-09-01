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
ECR        := 373468206837.dkr.ecr.us-east-1.amazonaws.com/heros-eval
# 🔴 The node is aarch64. An image built for the builder's own architecture on a non-arm machine
# lands in ECR, pulls successfully, and then CrashLoopBackOffs with `exec format error`.
PLATFORM   := linux/arm64
# Tagged by commit so a tag answers "what is running" — dirty when the tree has uncommitted changes,
# because a tag naming a commit that does not describe the build is worse than no tag.
TAG        := $(shell git rev-parse --short HEAD)$(shell git diff --quiet HEAD -- . ':!deploy' || echo -dirty)

.PHONY: deploy deploy-build deploy-push deploy-apply deploy-status

deploy-build: ## Build the container for the deployment's architecture
	docker build --platform $(PLATFORM) $(if $(GOPROXY),--build-arg GOPROXY=$(GOPROXY),) \
	  -f deploy/Dockerfile -t $(ECR):$(TAG) .

deploy-push: deploy-build ## Push to ECR and print the digest
	aws ecr get-login-password --region us-east-1 | \
	  docker login --username AWS --password-stdin $(firstword $(subst /, ,$(ECR)))
	docker push $(ECR):$(TAG)
	@echo "digest: $$(aws ecr describe-images --repository-name heros-eval \
	  --image-ids imageTag=$(TAG) --query 'imageDetails[0].imageDigest' --output text)"

deploy-apply: ## Apply the manifests at the pushed digest (DIGEST=sha256:…)
	@test -n "$(DIGEST)" || { echo "DIGEST=sha256:… is required — read it back from ECR after the push, not from the local build"; exit 2; }
	@bash deploy/apply.sh "$(DIGEST)"

deploy-status: ## What is actually running
	@bash deploy/ssm.sh 'k3s kubectl -n heros-eval get pods,ingress,certificate -o wide'
