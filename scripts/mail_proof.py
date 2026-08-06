#!/usr/bin/env python3
"""Run cmd/mailproof with the deployment's real SMTP credentials, without ever printing them.

Why a script rather than a shell one-liner in the Makefile: the credentials have to travel from the secret
store into the child process's ENVIRONMENT and nowhere else. A shell version would put them on a command
line (visible in `ps`) or into an exported variable that outlives the call. Here they exist only in the
dict handed to `subprocess.run`, and only the child's own stdout is printed.
"""
import json
import os
import subprocess
import sys

REGION = "us-east-1"
SECRET = "heros/platform"
# The relay's non-secret half, kept in ONE place with the manifest that configures the cluster. If these
# two ever disagree, this proof passes against a relay the deployment does not use — which is worse than
# no proof, so the runbook says to change both together.
RELAY = {
    "HEROS_SMTP_HOST": "email-smtp.us-east-1.amazonaws.com",
    "HEROS_SMTP_PORT": "587",
    "HEROS_SMTP_TLS": "starttls",
    "HEROS_SMTP_FROM": "support@heros-agent.space",
}


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: mail_proof.py <recipient>", file=sys.stderr)
        return 2
    try:
        raw = subprocess.run(
            ["aws", "secretsmanager", "get-secret-value", "--secret-id", SECRET,
             "--region", REGION, "--query", "SecretString", "--output", "text"],
            capture_output=True, text=True, check=True).stdout
    except subprocess.CalledProcessError as err:
        # The stderr is printed because an auth failure here is the common case and its message is the
        # actionable part. It carries no secret: the call failed before returning one.
        print("mail-proof: could not read", SECRET, "-", err.stderr.strip(), file=sys.stderr)
        return 1

    doc = json.loads(raw)
    missing = [k for k in ("smtp-username", "smtp-password") if not doc.get(k)]
    if missing:
        print("mail-proof: the secret store has no", ", ".join(missing),
              "- the relay credentials were never written", file=sys.stderr)
        return 1

    env = dict(os.environ)
    env.update(RELAY)
    env["HEROS_SMTP_USERNAME"] = doc["smtp-username"]
    env["HEROS_SMTP_PASSWORD"] = doc["smtp-password"]
    env["MAILPROOF_TO"] = sys.argv[1]
    env["GOWORK"] = "off"

    return subprocess.run(["go", "run", "./cmd/mailproof"], env=env).returncode


if __name__ == "__main__":
    raise SystemExit(main())
