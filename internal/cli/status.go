package cli

import (
	"github.com/heros-foreal/agentd/internal/runlink"
)

// status.go is the `status` command: it answers "why is it behaving this way" by reporting each
// effective configuration value AND where it came from (PRD FR7, task 2.6), plus the fixed facts a
// support conversation needs — tool version, contract version, and the pinned link endpoint. It
// contacts nothing; it is a pure readout of resolved state.

// StatusData is the machine payload for `status`.
type StatusData struct {
	ToolVersion     string `json:"tool_version"`
	ContractVersion string `json:"contract_version"`
	OutputContract  string `json:"output_contract_version"`
	LinkEndpoint    string `json:"link_endpoint"`
	Authenticated   bool   `json:"authenticated"`
	Identity        string `json:"identity,omitempty"`
	// Who and where, from the stored credential (task 13.6). All three are omitempty and all three are
	// non-secret: the token is never read here, never rendered, and has no field on this struct.
	OrganizationName string     `json:"organization_name,omitempty"`
	UserID           string     `json:"user_id,omitempty"`
	CredentialKind   string     `json:"credential_kind,omitempty"`
	LocalRuns        int        `json:"local_runs"`
	Config           []Resolved `json:"config"`
}

// Status reports effective configuration with provenance and the fixed contract facts. authed/identity
// describe the stored credential without ever revealing it.
func Status(cfg Config, s Streams, authed bool, identity string) error {
	// The stored credential's non-secret attributes. Read here rather than threaded through the caller
	// so `status` stays a pure readout — and read leniently: a credential written before P27 has none of
	// these and must still print a correct, if shorter, line.
	var orgName, userID, kind string
	if c, ok := LoadCredential(); ok {
		orgName, userID, kind = c.OrganizationName, c.UserID, c.Kind
	}
	repo := cfg.Get("repo")
	if repo == "" {
		repo = "."
	}
	runs, _ := OpenRunStore(repo).List()

	data := StatusData{
		ToolVersion:      ToolVersion,
		ContractVersion:  ContractVersion,
		OutputContract:   OutputContractVersion,
		LinkEndpoint:     runlink.PlatformBaseURL,
		Authenticated:    authed,
		Identity:         identity,
		LocalRuns:        len(runs),
		Config:           cfg.Effective(),
		OrganizationName: orgName,
		UserID:           userID,
		CredentialKind:   kind,
	}

	s.Narratef("status: tool %s · contract %s · link endpoint %s", data.ToolVersion, data.ContractVersion, data.LinkEndpoint)
	// The coverage table's version, so an operator sees which table this binary is refusing from without
	// running a second command — the fact that makes a CLI/console disagreement diagnosable (P13 FR49).
	s.Narratef("status: %s", coverageSummaryLine())
	if authed {
		// 🔴 Names the person and the organization when they are known, and says nothing about either
		// when they are not. A machine credential has no person, and inventing one — "service account",
		// "unknown" — would put a name on actions nobody took.
		switch {
		case userID != "" && orgName != "":
			s.Narratef("status: authenticated as %s in %s (%s credential)", userID, orgName, kind)
		case orgName != "":
			s.Narratef("status: authenticated in %s (%s credential)", orgName, kindOr(kind, "machine"))
		default:
			s.Narratef("status: authenticated as %s", identity)
		}
	} else {
		s.Narratef("status: not authenticated (run `heros login` to link runs)")
	}
	for _, r := range data.Config {
		over := ""
		if len(r.Overridden) > 0 {
			over = " (also set in: "
			for i, o := range r.Overridden {
				if i > 0 {
					over += ", "
				}
				over += string(o)
			}
			over += ")"
		}
		s.Narratef("  %-14s = %-24s [%s]%s", r.Key, r.Value, r.Source, over)
	}
	return s.EmitJSON("status", ExitOK, data, nil, nil)
}

// VersionData is the machine payload for `version`.
type VersionData struct {
	ToolVersion     string `json:"tool_version"`
	ContractVersion string `json:"contract_version"`
	OutputContract  string `json:"output_contract_version"`
	LinkEndpoint    string `json:"link_endpoint"`
}

// Version reports the CLI's own version and the contract versions it implements. It is the smallest
// possible command — no config, no I/O beyond its own output — so a pipeline can pin or gate on the CLI
// version, and a support conversation can start from an unambiguous "what am I running". A human line on
// stderr, the machine envelope on stdout.
func Version(s Streams) error {
	data := VersionData{
		ToolVersion:     ToolVersion,
		ContractVersion: ContractVersion,
		OutputContract:  OutputContractVersion,
		LinkEndpoint:    runlink.PlatformBaseURL,
	}
	s.Narratef("heros %s · contract %s · output %s · link %s",
		data.ToolVersion, data.ContractVersion, data.OutputContract, data.LinkEndpoint)
	return s.EmitJSON("version", ExitOK, data, nil, nil)
}

// kindOr names the credential kind, defaulting for a credential written before P27 recorded one.
func kindOr(kind, fallback string) string {
	if kind == "" {
		return fallback
	}
	return kind
}
