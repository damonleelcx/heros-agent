#!/usr/bin/env bash
# CI gate: Docker Compose and the Kubernetes base reference the SAME digest-pinned image set (P19
# Decision 2: "both substrates reference the same digests"; "a CI check fails if the two substrates
# diverge"). This makes "it works on compose but not the cluster" always a TOPOLOGY question, never an
# IMAGE question.
#
# Source of truth: deploy/images.env (compose reads its ${..._IMAGE} values); the k8s base pins the
# same name@digest in kustomization.yaml `images:`. This extracts `name digest` pairs from both, sorts
# them, and fails LOUD on any difference. Written without bash-4 associative arrays so it runs on the
# macOS system bash too.
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
imagesenv="$root/deploy/images.env"
kustom="$root/deploy/k8s/base/kustomization.yaml"

# "name digest" from images.env: VALUE is name@sha256:...; strip VAR=, any trailing comment and
# whitespace, keep only @sha256 refs, and split name@digest into "name digest".
env_pairs="$(
  grep -vE '^[[:space:]]*(#|$)' "$imagesenv" \
    | sed -E 's/^[^=]*=//; s/[[:space:]]*#.*$//; s/[[:space:]]//g' \
    | grep '@sha256:' \
    | sed -E 's/@/ /' \
    | sort
)"

# "name digest" from the kustomization images: block (name:/newName:/digest: entries, in order).
#
# 🔴 `newName:` OVERRIDES `name:`, and reading only `name:` was a blind spot rather than a simplification.
# In a kustomize `images:` entry, `name` is the SELECTOR — the string matched against the image written in
# the manifests — and `newName` is what actually gets applied. A base that retargets every platform image
# to a different registry (which is exactly what deploying from ECR instead of GHCR looks like) therefore
# ran the whole platform from a registry this check never inspected, and reported OK while doing it. The
# gate exists to make "compose and the cluster reference the same image" true; that has to mean the
# EFFECTIVE reference, not the pre-rewrite one.
k8s_pairs="$(
  awk '
    /- name:/  { sub(/.*- name:[[:space:]]*/,"");  sub(/[[:space:]]*$/,""); n=$0 }
    /newName:/ { sub(/.*newName:[[:space:]]*/,""); sub(/[[:space:]]*$/,""); n=$0 }
    /digest:/  { sub(/.*digest:[[:space:]]*/,"");  sub(/[[:space:]]*$/,""); if (n!="") { print n" "$0; n="" } }
  ' "$kustom" | sort
)"

if [ "$env_pairs" = "$k8s_pairs" ]; then
  echo "check-image-parity: OK — compose and k8s reference the same $(printf '%s\n' "$env_pairs" | grep -c .) digest-pinned images"
  exit 0
fi

echo "FAIL: the two substrates reference a different image set / digests." >&2
echo "--- only in images.env (compose) ---" >&2
comm -23 <(printf '%s\n' "$env_pairs") <(printf '%s\n' "$k8s_pairs") >&2 || true
echo "--- only in k8s base ---" >&2
comm -13 <(printf '%s\n' "$env_pairs") <(printf '%s\n' "$k8s_pairs") >&2 || true
exit 1
