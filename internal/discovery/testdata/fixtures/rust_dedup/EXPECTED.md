# rust_dedup fixture — one call site hit by BOTH sources is ONE node

**1 node**, `detections_by_source.registry == 1` AND `detections_by_source.declared == 1`, **1 dedup_merge**.

`client.chat().create(req)` is matched by registry row `rs.async_openai.create` (crate import-presence +
selector `chat.create`) AND by the declared method entrypoint `async_openai.(*Client).create`. Both land on
one node identity and merge into a single node crediting both sources (§3.5).
