# java_dedup fixture — one call site hit by BOTH sources is ONE node

**1 node**, `detections_by_source.registry == 1` AND `detections_by_source.declared == 1`, **1 dedup_merge**.

`model.generate("summarize this")` is matched by registry row `java.langchain4j.generate` AND by the declared
method entrypoint `dev.langchain4j.model.openai.(*OpenAiChatModel).generate`. Both land on one node identity
and merge into a single node crediting both sources (§3.5).
