from langgraph.graph import StateGraph

def build():
    g = StateGraph(dict)
    g.add_node("classify", classify_fn)
    g.add_node("answer", answer_fn)
    g.add_node("escalate", escalate_fn)
    g.add_edge("classify", "route")
    g.add_conditional_edges("route", pick, {"faq": "answer", "esc": "escalate"})
    g.set_entry_point("classify")
