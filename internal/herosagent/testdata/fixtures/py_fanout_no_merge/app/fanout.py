# NEAR-MISS: a fan-out with NO merge.
#
# `plan` feeds both `wing_a` and `wing_b`, and nothing joins their results — they are returned
# separately and consumed by different callers. Parallelization requires a merge; without one this is
# two independent continuations, and an agent that emits a join edge has invented the one thing that
# would make it a pattern.
import anthropic

client = anthropic.Anthropic()


def plan(task):
    return client.messages.create(model="claude-sonnet-4-5", messages=[{"role": "user", "content": task}])


def wing_a(step):
    return client.messages.create(model="claude-sonnet-4-5", messages=[{"role": "user", "content": step}])


def wing_b(step):
    return client.messages.create(model="claude-sonnet-4-5", messages=[{"role": "user", "content": step}])


def run(task, sink_a, sink_b):
    steps = plan(task)
    sink_a(wing_a(steps))
    sink_b(wing_b(steps))
