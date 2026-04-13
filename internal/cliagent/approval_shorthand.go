package cliagent

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

var (
	approvalNumberRE = regexp.MustCompile(`(?i)^(approve|reject)\s+(\d+)\s*$`)
	approveAllRE     = regexp.MustCompile(`(?i)^approve\s+all(\s+(proposals|pending))?\s*$`)
)

// TryBulkApproveCommand handles "approve all" / "approve all proposals" locally (no LLM).
func (s *Session) TryBulkApproveCommand(ctx context.Context, line string, out, errOut io.Writer) bool {
	if !approveAllRE.MatchString(strings.TrimSpace(line)) {
		return false
	}
	list, err := s.Agentd.ListPendingProposals(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "pending: %v\n", err)
		return true
	}
	if len(list) == 0 {
		_, _ = fmt.Fprintln(out, "(no pending proposals)")
		return true
	}
	_, _ = fmt.Fprintf(out, "approving %d proposal(s)…\n", len(list))
	var okN, failN int
	for i, p := range list {
		id := stringField(p, "id")
		title := stringField(p, "title")
		if id == "" {
			_, _ = fmt.Fprintf(errOut, "[%d] skip: missing id (%s)\n", i+1, title)
			failN++
			continue
		}
		res, err := s.Agentd.ApproveProposalJSON(ctx, id)
		if err != nil {
			_, _ = fmt.Fprintf(errOut, "[%d/%d] FAIL id=%s title=%q\n  %v\n", i+1, len(list), id, title, err)
			failN++
			continue
		}
		okN++
		_, _ = fmt.Fprintf(out, "[%d/%d] OK id=%s status=%v %q\n", i+1, len(list), id, res["status"], title)
	}
	_, _ = fmt.Fprintf(out, "\ndone: %d ok, %d failed (tooling proposals need valid JSON diff; see self-evolution-via-proposals / server logs)\n", okN, failN)
	return true
}

// TryApprovalNumberCommand handles lines like "approve 2" or "reject 1" without involving the LLM.
func (s *Session) TryApprovalNumberCommand(ctx context.Context, line string, out, errOut io.Writer) bool {
	line = strings.TrimSpace(line)
	sm := approvalNumberRE.FindStringSubmatch(line)
	if sm == nil {
		return false
	}
	verb := strings.ToLower(sm[1])
	n, err := strconv.Atoi(sm[2])
	if err != nil || n < 1 {
		return false
	}
	list, err := s.Agentd.ListPendingProposals(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "pending: %v\n", err)
		return true
	}
	if len(list) == 0 {
		_, _ = fmt.Fprintln(out, "(no pending proposals)")
		return true
	}
	if n > len(list) {
		_, _ = fmt.Fprintf(errOut, "invalid number %d (only %d pending; try /pending)\n", n, len(list))
		return true
	}
	id := stringField(list[n-1], "id")
	if id == "" {
		_, _ = fmt.Fprintln(errOut, "entry has no id")
		return true
	}
	if verb == "approve" {
		res, err := s.Agentd.ApproveProposalJSON(ctx, id)
		if err != nil {
			_, _ = fmt.Fprintf(errOut, "approve: %v\n", err)
			return true
		}
		_, _ = fmt.Fprintf(out, "approved #%d id=%s status=%v\n", n, id, res["status"])
		_, _ = fmt.Fprintln(out, "(skills/tools on disk updated; /refresh or ask the agent to reload catalog)")
		return true
	}
	res, err := s.Agentd.RejectProposalJSON(ctx, id)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "reject: %v\n", err)
		return true
	}
	_, _ = fmt.Fprintf(out, "rejected #%d id=%s status=%v\n", n, id, res["status"])
	return true
}
