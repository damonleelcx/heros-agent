# herosd — the durable-agent console, as a container.
#
# 🔴 NOT distroless, and not scratch. `internal/intake` resolves a repository by shelling out to
# `git` (intake.go: exec.Command("git", ...)), so a runtime image without git produces a deployment
# that starts cleanly, serves the console, signs people in, and then fails on the first question
# anybody asks it. The binary being static does not make the product self-contained.
#
# ca-certificates is equally load-bearing: the DeepSeek client and `git clone` both speak TLS to the
# public internet, and without a trust store both fail with an error about certificates that reads
# like a network problem.

# ---- build -------------------------------------------------------------------------------------
FROM golang:1.25-bookworm AS build

# 🔴 proxy.golang.org is unreachable from the machine this image is built on. The ARG exists so the
# build can be pointed at a mirror without editing this file; it defaults to the real proxy so a
# build anywhere else needs no argument.
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

# 🔴 GOWORK=off. This checkout normally sits under a parent directory holding a go.work that lists
# OTHER modules and captures this one. It is not copied into the image, but setting this here means
# the image builds the same way whether or not a future context includes one — the Makefile sets it
# for the same reason.
ENV GOWORK=off CGO_ENABLED=0

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Trimpath so the binary does not carry this machine's directory layout.
RUN go build -trimpath -ldflags="-s -w" -o /out/herosd ./cmd/herosd

# ---- runtime -----------------------------------------------------------------------------------
FROM debian:bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates \
 && rm -rf /var/lib/apt/lists/*

# 🔴 A named non-root user with a real home directory. herosd calls os.UserCacheDir() to decide
# where cloned repositories live, which is $HOME/.cache — on a user with no home that resolves to a
# path the process cannot write, and the failure appears at repository-load time rather than at
# startup. HOME is set explicitly below rather than inherited, so the answer does not depend on how
# the container was entered.
RUN useradd --create-home --uid 10001 --shell /usr/sbin/nologin heros

COPY --from=build /out/herosd /usr/local/bin/herosd
# The console is a single static page served from -web; it ships beside the binary.
COPY --chown=heros:heros web/static /srv/heros/web

# 🔴 The working directory must be WRITABLE. tools.PublishEvalSet and tools.OpenPullRequest are
# constructed with Root: "." — they write beneath the process's working directory. Pointing it at a
# volume rather than at / means a read-only root filesystem is still possible.
ENV HOME=/var/lib/heros
WORKDIR /var/lib/heros
RUN chown heros:heros /var/lib/heros

USER heros
EXPOSE 8080

# 0.0.0.0, not the 127.0.0.1 default: a container that listens on loopback is unreachable from its
# own Service, and the symptom is a readiness probe that never passes with nothing in the log.
ENTRYPOINT ["/usr/local/bin/herosd", "-addr", "0.0.0.0:8080", "-web", "/srv/heros/web"]
