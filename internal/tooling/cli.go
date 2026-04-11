package tooling

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/heros-foreal/agentd/internal/agentlayout"
	"github.com/heros-foreal/agentd/internal/toolindex"
)

type CLIOutcome string

const (
	OutcomeExecuted       CLIOutcome = "executed"
	OutcomeBlocked        CLIOutcome = "blocked_pending_approval"
	OutcomeLogged         CLIOutcome = "logged_notify"
	OutcomeRejectedPolicy CLIOutcome = "rejected_policy"
)

// ExecWithPolicy runs shell command after tiered gates. High-risk never runs unless allowHighRisk true (still records audit).
func ExecWithPolicy(ctx context.Context, db *sql.DB, cmd string, allowHighRisk bool) (tier RiskTier, outcome CLIOutcome, output string, err error) {
	tier = ClassifyCLI(cmd)
	switch tier {
	case RiskLow:
		out, e := runShell(ctx, cmd)
		audit(db, cmd, string(tier), string(OutcomeExecuted), e)
		return tier, OutcomeExecuted, out, e
	case RiskMedium:
		out, e := runShell(ctx, cmd)
		audit(db, cmd, string(tier), string(OutcomeLogged), e)
		return tier, OutcomeLogged, out, e
	case RiskHigh:
		if !allowHighRisk {
			audit(db, cmd, string(tier), string(OutcomeBlocked), nil)
			return tier, OutcomeBlocked, "", fmt.Errorf("high-risk command blocked pending human approval")
		}
		out, e := runShell(ctx, cmd)
		audit(db, cmd, string(tier), string(OutcomeExecuted), e)
		return tier, OutcomeExecuted, out, e
	default:
		return tier, OutcomeRejectedPolicy, "", fmt.Errorf("unknown tier")
	}
}

func runShell(ctx context.Context, cmd string) (string, error) {
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.CommandContext(ctx, "cmd", "/C", cmd)
	} else {
		c = exec.CommandContext(ctx, "sh", "-c", cmd)
	}
	b, err := c.CombinedOutput()
	return strings.TrimSpace(string(b)), err
}

func audit(db *sql.DB, cmd, tier, outcome string, execErr error) {
	detail := ""
	if execErr != nil {
		detail = execErr.Error()
	}
	if detail == "" {
		detail = "-"
	}
	_, _ = db.Exec(`INSERT INTO cli_audit (cmd, risk_tier, outcome, detail) VALUES (?, ?, ?, ?)`,
		cmd, tier, outcome, detail)
}

// RegisterToolProposal inserts a pending tool row (requires human approval to set approved=1).
func RegisterToolProposal(db *sql.DB, tenantID, name, description string, tier RiskTier, scriptPath, proposalID string) error {
	ts := agentlayout.SanitizeTenantScope(tenantID)
	var sp any = strings.TrimSpace(scriptPath)
	if sp == "" {
		sp = nil
	}
	_, err := db.Exec(`INSERT INTO tool_registry (tenant_id, name, description, risk_tier, script_path, approved, proposal_id) VALUES (?, ?, ?, ?, ?, 0, ?)
		ON CONFLICT(tenant_id, name) DO UPDATE SET description=excluded.description, risk_tier=excluded.risk_tier, script_path=excluded.script_path, approved=0, proposal_id=excluded.proposal_id`,
		ts, name, description, string(tier), sp, proposalID)
	return err
}

func ApproveTool(db *sql.DB, tenantID, name string) error {
	ts := agentlayout.SanitizeTenantScope(tenantID)
	res, err := db.Exec(`UPDATE tool_registry SET approved = 1 WHERE tenant_id = ? AND name = ?`, ts, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("tool not found")
	}
	return nil
}

// ApplyToolMutation parses JSON: {"register":{"name","description","risk_tier","script_path","skills":[]}}
func ApplyToolMutation(db *sql.DB, dataDir string, p toolindex.SyncPolicy, tenantID, proposalID, diff string) error {
	type reg struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		RiskTier    string   `json:"risk_tier"`
		ScriptPath  string   `json:"script_path"`
		Skills      []string `json:"skills"`
	}
	var payload struct {
		Register *reg `json:"register"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(diff)), &payload); err != nil {
		return err
	}
	if payload.Register != nil {
		ts := agentlayout.SanitizeTenantScope(tenantID)
		if err := RegisterToolProposal(db, ts, payload.Register.Name, payload.Register.Description, RiskTier(payload.Register.RiskTier), payload.Register.ScriptPath, proposalID); err != nil {
			return err
		}
		if err := ApproveTool(db, ts, payload.Register.Name); err != nil {
			return err
		}
		if strings.TrimSpace(dataDir) != "" {
			if err := toolindex.PersistFromRegistry(db, dataDir, ts, payload.Register.Name, payload.Register.Skills); err != nil {
				return err
			}
			if err := toolindex.Rebuild(db, dataDir, p.Normalize()); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("tooling diff: expected JSON with register{...}")
}
