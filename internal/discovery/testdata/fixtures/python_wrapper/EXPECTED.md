# python_wrapper fixture — user-declared entrypoint (FR2)

**1 node WITH llm-eval.yaml, 0 nodes WITHOUT it.** `myco.llm.complete` matches no registry row, so the
in-house wrapper is invisible until declared.

**prompt resolves inline to "summarize the ticket"** via the `{ name: prompt }` locator + the keyword-argument
call `complete(prompt="…")` — the one form the syntactic floor can resolve (10.5).

This case previously existed ONLY as an in-test fixture inside frontend_python_test.go
(TestPythonWrapperDeclared), so the CLI path never exercised it: a library test passing does not prove the
`discover` CLI emits schema-valid IR for it. It is now a committed fixture and therefore covered by
scripts/discovery_ci.py like every other one.
