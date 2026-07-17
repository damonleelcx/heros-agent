# python_dedup fixture — one call site hit by BOTH sources is ONE node

**1 node**, `detections_by_source.registry == 1` AND `detections_by_source.declared == 1`, **1 dedup_merge**.

`client.chat.completions.create(...)` is matched by registry row `py.openai.chat.completions.create` AND by
the declared method entrypoint `openai.(*OpenAI).create` (method entrypoints match on import-presence of the
defining package + method name, since a receiver type is unresolvable without type info — documented in
doc.go). Both land on the same node identity and merge into one node crediting both sources (§3.5).
