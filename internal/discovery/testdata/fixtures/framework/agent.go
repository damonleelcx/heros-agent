package framework

import "github.com/example/langgraphgo/graph"

func build() {
	g := graph.NewStateGraph()
	g.AddNode("classify", nil)
	g.AddNode("answer", nil)
	g.AddNode("escalate", nil)
	g.AddEdge("classify", "route")
	g.AddConditionalEdges("route", nil, map[string]string{"faq": "answer", "esc": "escalate"})
	g.SetEntryPoint("classify")
}
