package clilink

import (
	"context"
	"os"
	"strings"

	"github.com/heros-foreal/agentd/internal/cli"
	"github.com/heros-foreal/agentd/internal/sourceingest"
)

// pair.go implements `heros pair` — the local agent's half of Mode 3 (P32 §4, design D5).
//
// # Why a command and not a browser file picker
//
// A browser affordance that reads a folder and posts it is Mode 1 wearing Mode 3's clothes: the control
// says "select a local repo" and the customer would reasonably believe nothing left their machine. A
// UI whose data-handling outcome differs from what its label implies is a consent failure, not a
// shortcut. So the browser never touches the tree. It shows a code; a person types it into a terminal
// that is ALREADY on the machine holding the repository; the reading happens there.
//
// # 🚫 What this command transmits, exhaustively
//
// The code, this machine's name, and the resolved commit id. Nothing else. Not the repository path —
// that is the customer's own filesystem layout, it tells the platform nothing it needs, and a field to
// put it in is how it ends up transmitted. `--repo` is used ONLY to resolve the revision, locally.
//
// # What pairing does and does not enable
//
// It enables the console to say which machine reads this workflow. It authorizes nothing: the agent
// already holds the person's credential from `heros login`, and no read anywhere is gated on a
// pairing. Un-pairing does not stop this machine reading its own disk — nothing could, and a command
// that implied otherwise would be lying about where the boundary is.

// PairData is the machine payload for `pair`.
type PairData struct {
	PairingID  string `json:"pairing_id"`
	WorkflowID string `json:"workflow_id"`
	State      string `json:"state"`
	// Machine is what this agent called itself, echoed so a script can confirm what the console will
	// display rather than having to guess how the hostname was derived.
	Machine string `json:"machine_name"`
	// Revision is the commit this machine reported. A revision id, never the code at it.
	Revision string `json:"source_revision"`
}

// Pair claims a pairing code the console issued.
func (c Commands) Pair(cfg cli.Config, s cli.Streams) error {
	ctx := context.Background()

	code := sourceingest.NormalizePairingCode(cfg.Get("code"))
	if code == "" {
		return &cli.ExitError{Code: cli.ExitInvalidCfg,
			Msg: "pair: --code is required — start a pairing in the console (Workflows → read a local " +
				"repository) and type the code it shows"}
	}

	repo := cfg.Get("repo")
	if repo == "" {
		repo = "."
	}
	// The revision is resolved LOCALLY, from the repository the person is standing in. It is the one
	// fact about their tree that crosses, and it is a commit id.
	revision, err := cli.ResolveRevision(ctx, repo, cfg.Get("commit"))
	if err != nil {
		return &cli.ExitError{Code: cli.ExitInvalidCfg, Msg: err.Error(), Err: err}
	}

	machine := machineName(cfg.Get("machine"))

	cred, ok := cli.LoadCredential()
	if !ok {
		return &cli.ExitError{Code: cli.ExitOperational,
			Msg: "pair: authentication required — run `heros login` first (nothing was transmitted)"}
	}

	res, err := c.client(cred.Token).ClaimPairing(ctx, code, machine, revision)
	if err != nil {
		return &cli.ExitError{Code: cli.ExitOperational, Msg: err.Error(), Err: err}
	}

	s.Narratef("pair: this machine (%s) now reads %s in place. Nothing from the repository was transmitted — "+
		"only this machine's name and the commit id %s.", res.MachineName, res.WorkflowID, short(res.Revision))
	s.Narratef("pair: run `heros link --with-ir` here to send the workflow's STRUCTURE " +
		"(symbols, files, line spans, models, tool counts) — never prompt text, source or a diff.")

	return s.EmitJSON("pair", cli.ExitOK, PairData{
		PairingID: res.PairingID, WorkflowID: res.WorkflowID, State: res.State,
		Machine: res.MachineName, Revision: res.Revision,
	}, nil, nil)
}

// machineName resolves what this agent calls itself.
//
// 🔴 The hostname, and it is DISCLOSED in the narration rather than sent silently. A machine name is
// mild but it is not nothing — it is often a person's name and their employer — so the command says
// what it sent, and `--machine` lets somebody who does not want their hostname on a screen supply
// something else. There is no "anonymous" default: the console's whole reason for holding this is to
// say WHICH machine, and an empty answer would make the surface useless while looking like it worked.
func machineName(override string) string {
	if v := strings.TrimSpace(override); v != "" {
		return v
	}
	h, err := os.Hostname()
	if err != nil || strings.TrimSpace(h) == "" {
		// A machine that cannot name itself still pairs. `unnamed machine` is honest and is visibly
		// not a hostname, which is better than a plausible-looking placeholder.
		return "unnamed machine"
	}
	return h
}

// short renders the leading characters of a revision for narration.
func short(rev string) string {
	if len(rev) <= 12 {
		return rev
	}
	return rev[:12]
}
