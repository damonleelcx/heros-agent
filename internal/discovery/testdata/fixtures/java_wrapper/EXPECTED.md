# java_wrapper fixture — user-declared entrypoint (FR2)

**1 node WITH llm-eval.yaml, 0 nodes WITHOUT it.** `com.myco.llm.LlmService#complete` matches no registry
row, so the in-house wrapper is invisible until declared — proving the mechanism reaches Java.

A METHOD entrypoint is the right shape for Java: Java has no top-level functions, so an in-house LLM
wrapper is always a method on a service type. The method form matches on import-presence of the defining
package + method name, because the receiver's type is unresolvable without type info (documented in doc.go).

**prompt is unresolved + flagged**: `complete("…")` is positional, and the floor resolves `LocParamName`
locators only. Java has no named arguments at the call site, so — unlike Kotlin, which has them — no
declaration can resolve a Java prompt at the syntactic floor. That is a real, documented language
difference, not a missing feature.
