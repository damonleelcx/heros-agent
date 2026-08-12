#!/usr/bin/env python3
"""agent_status.py — what the analysis agent is actually doing, read from a deployment's /readyz.

P30 task 9.6. A repeatable command rather than a shell line somebody retypes during an incident, and
the reason it exists at all is task 9.1: `heros_agent` on /readyz is resolved by DOING what an
inference does — reading the active definition, resolving the credential through the same secrets
source the runner calls, and comparing the real meter against the real ceiling.

🔴 So a green line here means an inference would work. It is not a report that somebody set a variable,
which is what every readiness signal built from configuration reports, and which this product has
already shipped once: `components.postgres: ready` on a process that had never opened a Postgres
connection.

🚫 It prints no credential and no secret. /readyz carries none — `CredentialSource` names the KIND of
source, never a value — and this script neither asks for more nor stores what it reads.
"""

from __future__ import annotations

import json
import sys
import urllib.request

# The five states, and what each one should send a reader to do. Spelled here rather than fetched,
# because a script that renders whatever word the server sent would print an unrecognised state as if
# it understood it — the same failure `SentenceForState` refuses with its missing fallback.
ACTIONS = {
    "ready": "Nothing. An inference would run: a definition is active and the credential resolves.",
    "disabled": (
        "Nothing is wrong. No organization is placed for analysis, which is the DEFAULT (Q2). "
        "Enable one from the operator console when you mean to."
    ),
    "no_active_definition": (
        "Publish and activate a definition. Organizations are enabled and there is nothing to run "
        "for them — a configuration half-done rather than a fault."
    ),
    "credential_unresolved": (
        "🔴 FIX THE CREDENTIAL. The active definition's provider reference does not resolve from this "
        "deployment's secrets source. Inference fails closed — zero provider calls, no substitution — "
        "and every surface falls back to rule-derived facts, so customers see a correct graph with "
        "nothing inferred rather than an error."
    ),
    "capped": (
        "Raise the ceiling or wait for the window to roll. The deployment is HEALTHY and declining to "
        "spend; this is a cap working."
    ),
}


def main(url: str) -> int:
    try:
        with urllib.request.urlopen(url, timeout=10) as resp:
            body = json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as err:
        # 🔴 A 503 is the NORMAL answer from a degraded deployment and its body is the useful part.
        # Treating it as a transport failure would throw away the one document that says what is wrong.
        try:
            body = json.loads(err.read().decode("utf-8"))
        except Exception:
            print(f"agent-status: {url} answered {err.code} with no readable body", file=sys.stderr)
            return 1
    except Exception as err:  # noqa: BLE001 - any transport failure reads the same to a caller
        print(f"agent-status: could not reach {url}: {err}", file=sys.stderr)
        return 1

    agent = body.get("heros_agent")
    if agent is None:
        # 🚫 NOT reported as "disabled". A deployment that does not mount the agent at all and one whose
        # tenants are all disabled are different facts, and the second is a setting somebody chose.
        print(
            "agent-status: this deployment reports no `heros_agent` entry, so it does not run the "
            "analysis agent at all. That is distinct from every organization being disabled."
        )
        return 0

    state = agent.get("state", "")
    print(f"state            {state}")
    print(f"detail           {agent.get('detail', '')}")
    if agent.get("config_hash"):
        print(f"definition       {agent['config_hash']}")
    print(f"enabled tenants  {agent.get('enabled_tenants', 0)}")
    print(f"caps enforced    {agent.get('caps_enforced', False)}")
    if agent.get("credential_source"):
        print(f"credential from  {agent['credential_source']}")

    action = ACTIONS.get(state)
    if action is None:
        # An unrecognised state is reported AS unrecognised. Rendering a plausible action for it is how
        # a sixth state ships looking like one of the five.
        print(
            f"\n🔴 `{state}` is not a state this script knows. It came from a newer platform build; "
            "read the detail above and update scripts/agent_status.py."
        )
        return 1
    print(f"\nwhat to do       {action}")

    # 🔴 The exit code distinguishes a FAULT from a setting. `disabled` and `capped` exit 0 because the
    # deployment is behaving correctly, and a CI step that failed on them would fail on the default
    # configuration every deployment ships with.
    return 1 if state == "credential_unresolved" else 0


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("usage: agent_status.py <readyz-url>", file=sys.stderr)
        raise SystemExit(2)
    raise SystemExit(main(sys.argv[1]))
