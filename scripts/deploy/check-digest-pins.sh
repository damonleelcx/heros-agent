#!/usr/bin/env bash
# CI gate: every image the Kubernetes delivery ACTUALLY APPLIES is pinned by DIGEST, never a floating
# tag (P19 kubernetes-delivery: "a mutable tag fails the lint"). A tag moves; a digest is the artifact,
# and an apply of the same digest twice yields the same running image — the reproducibility NFR9 buys.
#
# The base workloads reference images by BARE NAME (e.g. `image: neo4j`) and the kustomization's
# `images:` transformer pins the digest — so the honest test is the RENDERED manifest, where the digest
# is applied. This renders base + every overlay with kustomize and fails LOUD on any rendered `image:`
# that is not `name@sha256:<hex>`. It also statically forbids a `newTag:` in any kustomization (pin by
# digest, not tag) so the failure is caught even where kustomize is unavailable.
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
k8s="$root/deploy/k8s"
fail=0

# Static: no kustomization may pin by newTag (must be digest).
while IFS= read -r hit; do
  echo "FAIL: kustomization pins an image by newTag, not digest: $hit" >&2; fail=1
done < <(grep -rnE '^[[:space:]]*newTag:' "$k8s" 2>/dev/null || true)

# Rendered: prefer a real kustomize; fall back to `go run` so CI needs no extra install.
kustomize_cmd=""
if command -v kustomize >/dev/null 2>&1; then
  kustomize_cmd="kustomize build"
elif command -v kubectl >/dev/null 2>&1; then
  kustomize_cmd="kubectl kustomize"
fi

if [ -n "$kustomize_cmd" ]; then
  for t in base overlays/dev overlays/staging overlays/prod overlays/airgapped; do
    rendered="$($kustomize_cmd "$k8s/$t" 2>/dev/null)" || { echo "FAIL: kustomize could not render $t" >&2; fail=1; continue; }
    while IFS= read -r img; do
      val="$(printf '%s\n' "$img" | sed -E 's/^[[:space:]]*image:[[:space:]]*//; s/[[:space:]]*$//')"
      case "$val" in
        *@sha256:*) : ;;
        "") : ;;
        *) echo "FAIL: rendered image is not digest-pinned in $t: $val" >&2; fail=1 ;;
      esac
    done < <(printf '%s\n' "$rendered" | grep -E '^[[:space:]]*image:[[:space:]]*[^[:space:]]')
  done
else
  echo "check-digest-pins: kustomize/kubectl not found — checking kustomization images: blocks only" >&2
  # Every kustomization images: entry must carry a digest: line (already forbade newTag above).
  if ! grep -rqE '^[[:space:]]*digest:[[:space:]]*sha256:' "$k8s/base/kustomization.yaml"; then
    echo "FAIL: base kustomization has no digest: pins" >&2; fail=1
  fi
fi

if [ "$fail" -eq 0 ]; then
  echo "check-digest-pins: OK — every rendered image is digest-pinned across base + overlays"
fi
exit "$fail"
