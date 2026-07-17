# kotlin_wrapper fixture — user-declared entrypoint (FR2)

**1 node WITH llm-eval.yaml, 0 nodes WITHOUT it.** `com.myco.llm.complete` is an in-house wrapper: it
matches no registry row, so it is invisible until the user declares it. Same mechanism as the Go `wrapper`
fixture, proving the declared-entrypoint path is language-neutral and reaches Kotlin.

**prompt resolves inline to "summarize the ticket".** The declaration uses a `{ name: prompt }` locator and
the call uses Kotlin's NAMED-argument form `complete(prompt = "…")`. A named argument bound to a string
literal is the one thing the syntactic floor CAN resolve (10.5) — so this fixture also pins Kotlin's named
-argument extraction.

⚠️ A `{ index: 0 }` locator would NOT resolve here, unlike the Go `wrapper` fixture which uses one. That is
the honest floor, not a bug: `extractSyntacticFloor` resolves `LocParamName` only, because a positional
argument cannot be tied to a parameter name without type/signature resolution, which tree-sitter frontends
deliberately do not do. Go resolves it because go/ast gives it real signatures.

This fixture is also the guard on Kotlin's import convention: `import com.myco.llm.complete` binds `complete`
to PACKAGE `com.myco.llm` (Python's convention), not to the full dotted path (Java's). Bind it the Java way
and the declaration stops matching and this fixture drops to 0 nodes.
