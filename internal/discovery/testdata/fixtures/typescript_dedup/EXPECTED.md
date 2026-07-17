# typescript_dedup fixture — one call site hit by BOTH sources is ONE node

**1 node**, `detections_by_source.registry == 1` AND `detections_by_source.declared == 1`, **1 dedup_merge**.

`openai.chat.completions.create(...)` is matched by registry row `ts.openai.chat.completions.create` AND by
the declared method entrypoint `openai.(*OpenAI).create`. Both land on one node identity and merge (§3.5).
