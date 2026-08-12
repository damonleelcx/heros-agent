# NEAR-MISS: a linear chain that is NOT a router.
#
# `extract` feeds `summarize` feeds `draft`. Every call passes its OUTPUT to the next, which is a
# chain. Nothing here CHOOSES between alternatives, so nothing here routes — and an agent that reads
# "three calls, one after another" as a router has found a pattern that is not present.
import anthropic

client = anthropic.Anthropic()


def extract(doc):
    return client.messages.create(model="claude-sonnet-4-5", messages=[{"role": "user", "content": doc}])


def summarize(facts):
    return client.messages.create(model="claude-sonnet-4-5", messages=[{"role": "user", "content": facts}])


def draft(summary):
    return client.messages.create(model="claude-sonnet-4-5", messages=[{"role": "user", "content": summary}])


def run(doc):
    facts = extract(doc)
    summary = summarize(facts)
    return draft(summary)
