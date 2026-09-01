#!/usr/bin/env bash
# Render the manifests at a given image digest and apply them on the node.
#
# There is no checkout of this repository on the node, so the manifests are rendered here and shipped
# over SSM. gzip+base64 keeps the whole set inside an SSM parameter.
set -euo pipefail

DIGEST=${1:?usage: apply.sh sha256:…}
[[ "$DIGEST" == sha256:* ]] || { echo "digest must start with sha256: — got $DIGEST" >&2; exit 2; }

HERE=$(cd "$(dirname "$0")" && pwd)
IMAGE="373468206837.dkr.ecr.us-east-1.amazonaws.com/heros-eval@${DIGEST}"

RENDER=$(mktemp)
trap 'rm -f "$RENDER"' EXIT
for f in "$HERE"/k8s/*.yaml; do
  printf -- '---\n'
  # The image placeholder is the ONE value that differs between deploys.
  sed "s#373468206837.dkr.ecr.us-east-1.amazonaws.com/heros-eval:REPLACED_BY_DEPLOY#${IMAGE}#" "$f"
done > "$RENDER"

grep -q "REPLACED_BY_DEPLOY" "$RENDER" && { echo "the image placeholder was not substituted" >&2; exit 1; }
grep -q "$IMAGE" "$RENDER" || { echo "the rendered manifests do not name the digest" >&2; exit 1; }

SUM=$(shasum -a 256 "$RENDER" | cut -d' ' -f1)
PAYLOAD=$(gzip -9 < "$RENDER" | base64 | tr -d '\n')
echo "rendered $(wc -l < "$RENDER") lines, sha256 ${SUM}, ${#PAYLOAD} bytes on the wire"

"$HERE/ssm.sh" "$(cat <<REMOTE
set -euo pipefail
mkdir -p /opt/heros-eval
echo '${PAYLOAD}' | base64 -d | gunzip > /opt/heros-eval/eval.yaml
# 🔴 Verify the file on the node is the file that was rendered here before applying it. Truncation in
# transit produces valid YAML describing less than was intended, and kubectl applies it happily.
got=\$(sha256sum /opt/heros-eval/eval.yaml | cut -d' ' -f1)
[ "\$got" = "${SUM}" ] || { echo "checksum mismatch: \$got != ${SUM}" >&2; exit 1; }
echo "--- diff against the live cluster"
k3s kubectl diff -f /opt/heros-eval/eval.yaml || true
echo "--- apply"
k3s kubectl apply -f /opt/heros-eval/eval.yaml
k3s kubectl -n heros-eval rollout status deploy/heros-eval --timeout=180s
REMOTE
)"
