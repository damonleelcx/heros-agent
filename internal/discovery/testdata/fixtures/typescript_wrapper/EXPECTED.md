# typescript_wrapper fixture — user-declared entrypoint (FR2)

**1 node WITH llm-eval.yaml, 0 nodes WITHOUT it.** `@myco/llm`'s `complete` matches no registry row.

**prompt resolves inline to "summarize the ticket"**: TS/JS SDKs pass an options OBJECT
(`complete({ prompt: "…" })`), and the frontend lifts object keys with string-literal values into the
same keyword map the floor resolves `{ name: prompt }` against — so an options-object property behaves
exactly like a Python keyword argument here.

The declared symbol is `@myco/llm.complete`: parseDeclaredSymbol splits on the LAST dot, so the scoped npm
package name `@myco/llm` survives as the import path.
