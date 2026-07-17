# kotlin_dedup fixture — one call site hit by BOTH sources is ONE node

**1 node**, with `detections_by_source.registry == 1` AND `detections_by_source.declared == 1`, and
**1 dedup_merge record**.

`model.generate("summarize this")` is matched twice:
- by registry row `kt.langchain4j.generate` (import-presence of `dev.langchain4j` + selector `generate`), and
- by the declared method entrypoint `dev.langchain4j.model.chat.(*ChatLanguageModel).generate`.

The merge must credit both sources rather than emitting two nodes for one static call site (§3.5). This is
the Kotlin counterpart of the Go `dedup` fixture, proving the merge is language-neutral.

Node identity is `(pkg, enclosing symbol, selector, occurrence)`, so the two sources land on the SAME
node_id and collapse. If the merge regressed, this fixture would emit 2 nodes.
