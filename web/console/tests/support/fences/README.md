# Fence fixtures

Each directory here is a **deliberately broken content corpus** that proves one fence goes red.

The rule these exist for (P23 §4, standing rule 1): *a fence without a failing fixture is not delivered.*
A fence nobody has seen fail is a fence nobody knows is connected — and the failure mode is silent, because
a disconnected fence reports success on everything.

`tests/fences.test.mjs` runs each scan against its fixture with `HEROS_CONTENT_ROOT` pointed here, and
asserts a **non-zero exit and a message that names the actual problem**. Exit code alone is not enough: a
scan that crashed would also exit non-zero, and would prove nothing about the rule.

Nothing here is ever rendered. The fixtures are invalid on purpose.
