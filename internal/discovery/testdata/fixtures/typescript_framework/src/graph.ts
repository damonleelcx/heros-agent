import { StateGraph } from "@langchain/langgraph";

const g = new StateGraph({ channels: {} });

g.addNode("classify", classifyFn);
g.addNode("answer", answerFn);
g.addNode("escalate", escalateFn);
g.addEdge("classify", "route");
g.addConditionalEdges("route", routeFn, { faq: "answer", esc: "escalate" });
g.setEntryPoint("classify");
