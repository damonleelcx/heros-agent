FROM debian:12
# A genuinely clean machine: no Go, no compiler, no heros. curl and openssh-client are the BASELINE a real
# Debian/Ubuntu host has — openssh-client is what provides `ssh-keygen -Y verify`, the installer's verifier.
# Nothing else is added, so anything the installer needs and does not find is a real finding.
RUN apt-get update -qq \
 && apt-get install -y -qq --no-install-recommends curl openssh-client ca-certificates \
 && rm -rf /var/lib/apt/lists/*
