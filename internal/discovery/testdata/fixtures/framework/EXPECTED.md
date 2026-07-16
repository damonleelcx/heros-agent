# framework-DAG fixture — expected

- report.framework_subgraphs[0].framework_source = "go-graph-builder", recognized=true.
- nodes: classify, answer, escalate, route.
- edges: classify->route (data); route->answer, route->escalate (control). Route labels (faq/esc)
  are NOT targets.
