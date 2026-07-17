# java_loop fixture — a call inside a loop is ONE static definition, variable at runtime

**1 node**: `Agent.batch` → `model.generate(item)` inside an enhanced-for, with
`invocation_semantics.type == "loop"` and `variable_at_runtime == true`.

🔴 **1 node, not N.** The IR describes STATIC definitions: a call site in a loop is one definition whose
runtime cardinality is unknown, and I2 forbids emitting any fixed runtime count for it. `variable_at_runtime`
is how that unknown is stated honestly instead of guessed.

This pins Java's `enhanced_for_statement` handling specifically — the java frontend counts
for/enhanced-for/while/do as loops, and an enhanced-for is the form real Java batch code uses.
