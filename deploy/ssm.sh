#!/usr/bin/env bash
# Run a shell script on the k3s node over SSM. The instance has no SSH key pair.
#
# 🔴 The script is base64-framed. Passing a multi-line script through `--parameters "commands=[…]"`
# collapses the newlines, so it arrives as one line and fails on what used to be line 1 with an error
# that describes the concatenation rather than the script.
set -euo pipefail

INSTANCE=${INSTANCE:-i-05f4712279b04fac5}
if [[ $# -ge 1 ]]; then SCRIPT_BODY="$1"; else SCRIPT_BODY=$(cat); fi

B64=$(printf '%s\n' "export KUBECONFIG=/etc/rancher/k3s/k3s.yaml" "$SCRIPT_BODY" | base64 | tr -d '\n')
CMD=$(aws ssm send-command --instance-ids "$INSTANCE" --document-name AWS-RunShellScript \
  --parameters "commands=[\"echo $B64 | base64 -d | bash\"]" \
  --query 'Command.CommandId' --output text)

STATUS=Pending
for _ in $(seq 1 180); do
  STATUS=$(aws ssm get-command-invocation --command-id "$CMD" --instance-id "$INSTANCE" \
    --query 'Status' --output text 2>/dev/null || echo Pending)
  case "$STATUS" in Success|Failed|Cancelled|TimedOut) break ;; esac
  sleep 2
done

aws ssm get-command-invocation --command-id "$CMD" --instance-id "$INSTANCE" \
  --query 'StandardOutputContent' --output text
ERR=$(aws ssm get-command-invocation --command-id "$CMD" --instance-id "$INSTANCE" \
  --query 'StandardErrorContent' --output text)
if [[ -n "$ERR" && "$ERR" != "None" ]]; then echo "--- stderr:" >&2; echo "$ERR" >&2; fi

# 🔴 Exit non-zero when the remote command failed. Without this the caller sees the output of a
# failed command and a zero status, which under `set -e` in a deploy script means the deploy
# continues past a step that did not happen.
[[ "$STATUS" == Success ]] || { echo "--- remote status: $STATUS" >&2; exit 1; }
