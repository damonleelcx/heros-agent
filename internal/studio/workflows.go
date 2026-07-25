package studio

import (
	"sort"
	"sync"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// workflows.go holds the workflow IRs the matrix draws its COLUMNS from (task M3.2). It is a read
// surface over IRs loaded at startup — no new table. A node's matrix column carries a node-scoped
// prompt name, so the prompt a user authors for a node is that node's prompt across every model row
// (PRD FR35, node-scoped prompt).

// NodeSummary is one matrix column: an agent node with the identity a column header needs and the
// node-scoped prompt name the cells author.
type NodeSummary struct {
	NodeID string `json:"node_id"`
	Symbol string `json:"symbol"`
	File   string `json:"file"`
	// PromptName is the node-scoped prompt name (`node/<node_id>`). The publish route tenant-scopes it;
	// the matrix uses it verbatim so every cell in a column shares one prompt.
	PromptName string `json:"prompt_name"`
	// DiscoveredModel is what the IR observed at the call site, shown for reference (never a default a
	// bind silently adopts).
	DiscoveredModel string `json:"discovered_model"`
}

// PromptNameForNode is the single definition of a node's prompt name, so the frontend, the publish
// route, and the bind store cannot disagree about it.
func PromptNameForNode(nodeID string) string { return "node/" + nodeID }

// WorkflowCatalog holds loaded IRs by workflow id.
type WorkflowCatalog struct {
	mu  sync.RWMutex
	irs map[string]*discovery.IR
}

// NewWorkflowCatalog returns an empty catalog.
func NewWorkflowCatalog() *WorkflowCatalog {
	return &WorkflowCatalog{irs: map[string]*discovery.IR{}}
}

// Load registers a workflow's IR under a workflow id.
func (c *WorkflowCatalog) Load(workflowID string, ir *discovery.IR) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.irs[workflowID] = ir
}

// Workflows returns the loaded workflow ids, sorted.
func (c *WorkflowCatalog) Workflows() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.irs))
	for id := range c.irs {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Nodes returns a workflow's node columns, ordered by node id. The bool is false when the workflow is
// not loaded — distinct from a workflow that loaded with zero nodes.
func (c *WorkflowCatalog) Nodes(workflowID string) ([]NodeSummary, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ir, ok := c.irs[workflowID]
	if !ok {
		return nil, false
	}
	out := make([]NodeSummary, 0, len(ir.Nodes))
	for _, n := range ir.Nodes {
		out = append(out, NodeSummary{
			NodeID:          n.NodeID,
			Symbol:          n.CallSite.Symbol,
			File:            n.CallSite.File,
			PromptName:      PromptNameForNode(n.NodeID),
			DiscoveredModel: n.Model.Provider + "/" + n.Model.ModelID,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out, true
}
