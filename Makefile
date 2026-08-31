# heros — local CI entrypoints.
#
# 🔴 GOWORK=off on every target. This checkout sits under a parent directory that contains a go.work
# listing OTHER modules, which captures this one and makes `go test ./...` fail with a workspace error
# that names nothing about this repository. Setting it here rather than documenting it means nobody has
# to remember, and a fresh clone behaves the same as a developer's machine.
export GOWORK := off

.PHONY: help test race vet fmt fmt-check cover check clean

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

clean:
	rm -f /tmp/heros.cover
