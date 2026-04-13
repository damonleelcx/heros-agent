package cliagent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FormatPendingProposalsForUser renders pending proposals as a numbered list so the user can
// reply with a number or paste an id — used by /pending and heros_list_pending_proposals.
func FormatPendingProposalsForUser(list []map[string]any) string {
	if len(list) == 0 {
		return "(no pending proposals)\n"
	}
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "Pending proposals (%d) — reply with a number (e.g. \"approve 2\") or the id:\n\n", len(list))
	for i, p := range list {
		id := stringField(p, "id")
		title := stringField(p, "title")
		layer := stringField(p, "layer")
		_, _ = fmt.Fprintf(&b, "%d. %s\n   layer=%s  id=%s\n\n", i+1, title, layer, id)
	}
	b.WriteString("Tip: type /help for slash commands, or ask the agent to approve a number.\n")
	return b.String()
}

// FormatPendingProposalsToolResult is the tool return: human list + raw JSON for the model.
func FormatPendingProposalsToolResult(list []map[string]any) string {
	human := FormatPendingProposalsForUser(list)
	raw, _ := json.MarshalIndent(list, "", "  ")
	return human + "\n--- json ---\n" + string(raw)
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key]; ok && v != nil {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}
