package toolindex

import (
	"strings"

	"github.com/heros-foreal/agentd/internal/config"
)

// SyncPolicy controls YAML ↔ tool_registry behavior (see config.ToolRegistrySync).
type SyncPolicy struct {
	// DiskToDB: "all" (default) = merge disk metadata into registry for every scanned tool.
	// "approved_only" = update existing rows from disk only when approved=1; new tools from disk still INSERT with approved=0.
	DiskToDB string
	// Conflict: when applying disk → DB for an existing row (and DiskToDB allows writes):
	// "yaml" = disk fields replace description/risk_tier/script_path (defaults filled as today).
	// "db" = never UPDATE existing rows from disk (only INSERT missing rows).
	// "yaml_nonblank" = per-field: use disk value only if non-empty, else keep DB.
	Conflict string
	// PushToDisk: "all" = registry-to-disk writes every row; "approved_only" = only approved=1.
	PushToDisk string
}

// DefaultSyncPolicy matches historical behavior before policy knobs existed.
func DefaultSyncPolicy() SyncPolicy {
	return SyncPolicy{
		DiskToDB:   "all",
		Conflict:   "yaml",
		PushToDisk: "all",
	}
}

// SyncPolicyFromConfig maps JSON config into a normalized policy.
func SyncPolicyFromConfig(c config.Config) SyncPolicy {
	p := DefaultSyncPolicy()
	s := c.ToolRegistrySync
	if strings.TrimSpace(s.DiskToDB) != "" {
		p.DiskToDB = strings.TrimSpace(s.DiskToDB)
	}
	if strings.TrimSpace(s.Conflict) != "" {
		p.Conflict = strings.TrimSpace(s.Conflict)
	}
	if strings.TrimSpace(s.PushToDisk) != "" {
		p.PushToDisk = strings.TrimSpace(s.PushToDisk)
	}
	return p.Normalize()
}

// Normalize coerces unknown values to safe defaults.
func (p SyncPolicy) Normalize() SyncPolicy {
	switch strings.ToLower(strings.TrimSpace(p.DiskToDB)) {
	case "approved_only":
		p.DiskToDB = "approved_only"
	default:
		p.DiskToDB = "all"
	}
	switch strings.ToLower(strings.TrimSpace(p.Conflict)) {
	case "db", "preserve_db":
		p.Conflict = "db"
	case "yaml_nonblank", "merge":
		p.Conflict = "yaml_nonblank"
	default:
		p.Conflict = "yaml"
	}
	switch strings.ToLower(strings.TrimSpace(p.PushToDisk)) {
	case "approved_only":
		p.PushToDisk = "approved_only"
	default:
		p.PushToDisk = "all"
	}
	return p
}

func (p SyncPolicy) diskToDBApprovedOnly() bool { return p.DiskToDB == "approved_only" }
func (p SyncPolicy) conflictDB() bool            { return p.Conflict == "db" }
func (p SyncPolicy) conflictYAMLNonBlank() bool { return p.Conflict == "yaml_nonblank" }
func (p SyncPolicy) pushApprovedOnly() bool     { return p.PushToDisk == "approved_only" }
