# typescript_framework fixture — declarative LangGraph.js DAG (FR5)

**0 IR nodes**, **1 framework subgraph** in the run report.

0 nodes is correct: the builder functions (`classifyFn`, …) are references, not LLM call sites, so there is
no static LLM call to emit. The declarative graph travels in the run report's `framework_subgraphs`, not on
IR nodes (doc 07 Finding A) — the same reason the Go `framework` fixture is also 0 nodes.

The subgraph must be `framework_source: "langgraph"`, `recognized: true`, with:
- **nodes**: answer, classify, escalate, route
- **edges**: `classify -> route` (**data**, from addEdge); `route -> answer` and `route -> escalate`
  (**control**, from addConditionalEdges)

🔴 The routing-map KEYS (`faq`, `esc`) are route LABELS and must NEVER become nodes or edge targets — only
the map VALUES are targets. That is the specific mis-inference this fixture guards against.

**Why TypeScript has this fixture and rust/java/kotlin do not**: `frameworkReadersByLanguage` registers
langGraphReader for typescript/javascript, because LangGraph.js is real and its builder API is the same
one, camelCased (`addNode` vs `add_node`). This fixture proves the camelCase mapping in
normalizeBuilderMethod is live and not dead code.
