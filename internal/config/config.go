package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Config drives the local daemon; collective URL is optional for federated sync.
type Config struct {
	ListenAddr       string `json:"listen_addr"`
	DataDir          string `json:"data_dir"`
	OpenAIBaseURL    string `json:"openai_base_url"`    // e.g. https://api.openai.com/v1
	OpenAIAPIKey     string `json:"openai_api_key"`     // optional; harness degrades without it
	OpenAIModel      string `json:"openai_model"`       // default chat model
	CollectiveURL    string `json:"collective_url"`     // optional peer for sync proposals
	NodeID           string `json:"node_id"`            // stable device id
	AllowHighRiskCLI bool   `json:"allow_high_risk_cli"` // if false, high-risk never runs without pending approval

	// Enterprise infra (leave empty to use SQLite-only semantic + local graph tables).
	QdrantURL        string `json:"qdrant_url"`         // e.g. http://127.0.0.1:6333
	QdrantAPIKey     string `json:"qdrant_api_key"`
	QdrantCollection string `json:"qdrant_collection"` // default heros_memory
	EmbeddingModel   string `json:"embedding_model"`   // e.g. text-embedding-3-small when using OpenAI embeddings
	EmbeddingDims    int    `json:"embedding_dims"`    // 0=auto (1536 with OpenAI path, else 128 naive)

	Neo4jURI      string `json:"neo4j_uri"`       // neo4j://127.0.0.1:7687
	Neo4jUser     string `json:"neo4j_user"`
	Neo4jPassword string `json:"neo4j_password"`
	Neo4jDatabase string `json:"neo4j_database"` // default neo4j

	NatsURL string `json:"nats_url"` // nats://127.0.0.1:4222

	// JetStream: durable stream over heros.> (server must start nats with -js).
	JetStreamEnabled     bool   `json:"jetstream_enabled"`
	JetStreamStreamName  string `json:"jetstream_stream_name"`  // default HEROS
	JetStreamMaxAgeHours int    `json:"jetstream_max_age_hours"` // default 168 (7d)

	// Auth: "off" | "required". When required, /api/* needs X-API-Key or Bearer + tenant credentials.
	AuthMode           string              `json:"auth_mode"`
	TenantCredentials  []TenantCredential  `json:"tenant_credentials"`

	// Observability
	MetricsEnabled bool `json:"metrics_enabled"` // GET /metrics Prometheus text
	InboxSigningKey string `json:"inbox_signing_key"` // optional HMAC secret for inbox payload verification

	// ToolRegistrySync rules for tools/*/tool.yaml ↔ SQLite tool_registry (see toolindex.SyncPolicy).
	ToolRegistrySync ToolRegistrySync `json:"tool_registry_sync"`

	// KnowledgeVaults indexes Obsidian-style Markdown trees into semantic_chunks (+ optional Qdrant).
	// Paths are usually absolute and may live outside data_dir; tenant_id scopes rows and retrieval.
	KnowledgeVaults []KnowledgeVault `json:"knowledge_vaults"`

	// HerosDesktop: optional Fyne heros-desktop UI (appearance/accent). Ignored by agentd; heros-desktop reads/writes. See heros_desktop.go.
	HerosDesktop HerosDesktopPrefs `json:"heros_desktop,omitempty"`
}

// KnowledgeVault configures one vault root on disk (read-only indexing + optional note append).
type KnowledgeVault struct {
	Path               string   `json:"path"`
	TenantID           string   `json:"tenant_id"`
	IncludeGlobs       []string `json:"include_globs"`
	ExcludeGlobs       []string `json:"exclude_globs"`
	PollSeconds        int      `json:"poll_seconds"` // 0 = no periodic reindex (still startup + POST /api/memory/vault/reindex)
	FollowSymlinks     bool     `json:"follow_symlinks"`
	AgentNotesSubdir   string   `json:"agent_notes_subdir"`    // relative to vault root; default Agent/heros-notes
	VaultAppendEnabled bool     `json:"vault_append_enabled"`  // mirror role=note episodic writes into the vault
	AgentNotesMode     string   `json:"agent_notes_mode"`      // daily | session (default daily)
}

// ToolRegistrySync configures disk/registry merge behavior.
// disk_to_db: "all" | "approved_only" — restrict YAML→registry updates for existing rows to approved tools only.
// conflict: "yaml" | "db" | "yaml_nonblank" — when updating an existing registry row from disk.
// push_to_disk: "all" | "approved_only" — POST /api/catalog/tools/registry-to-disk scope.
//
// Env overrides (non-empty wins over JSON; applied in Load for every process using config.Load):
//   HEROS_TOOL_REGISTRY_SYNC_DISK_TO_DB
//   HEROS_TOOL_REGISTRY_SYNC_CONFLICT
//   HEROS_TOOL_REGISTRY_SYNC_PUSH_TO_DISK
type ToolRegistrySync struct {
	DiskToDB   string `json:"disk_to_db"`   // default all
	Conflict   string `json:"conflict"`     // default yaml
	PushToDisk string `json:"push_to_disk"` // default all
}

// TenantCredential maps a static API key to a tenant (MVP; replace with IdP in production).
type TenantCredential struct {
	TenantID string `json:"tenant_id"`
	APIKey   string `json:"api_key"`
	Role     string `json:"role"` // admin | member
	KeyID    string `json:"key_id"` // optional label for audit
}

func Default() Config {
	home, _ := os.UserHomeDir()
	data := filepath.Join(home, ".heros-agent")
	return Config{
		ListenAddr:       "127.0.0.1:8787",
		DataDir:          data,
		OpenAIBaseURL:    "https://api.openai.com/v1",
		OpenAIModel:      "gpt-4o-mini",
		NodeID:           "local-node",
		QdrantCollection: "heros_memory",
	}
}

func Load(path string) (Config, error) {
	c := Default()
	if strings.TrimSpace(path) != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				applyToolRegistrySyncEnv(&c.ToolRegistrySync)
				return c, nil
			}
			return c, err
		}
		if err := json.Unmarshal(b, &c); err != nil {
			return c, err
		}
	}
	if c.DataDir == "" {
		c.DataDir = Default().DataDir
	}
	if c.ListenAddr == "" {
		c.ListenAddr = Default().ListenAddr
	}
	if c.QdrantCollection == "" {
		c.QdrantCollection = Default().QdrantCollection
	}
	if c.Neo4jDatabase == "" {
		c.Neo4jDatabase = "neo4j"
	}
	if c.JetStreamStreamName == "" {
		c.JetStreamStreamName = "HEROS"
	}
	if c.JetStreamMaxAgeHours <= 0 {
		c.JetStreamMaxAgeHours = 168
	}
	if c.AuthMode == "" {
		c.AuthMode = "off"
	}
	applyToolRegistrySyncEnv(&c.ToolRegistrySync)
	return c, nil
}

func applyToolRegistrySyncEnv(s *ToolRegistrySync) {
	if s == nil {
		return
	}
	if v := strings.TrimSpace(os.Getenv("HEROS_TOOL_REGISTRY_SYNC_DISK_TO_DB")); v != "" {
		s.DiskToDB = v
	}
	if v := strings.TrimSpace(os.Getenv("HEROS_TOOL_REGISTRY_SYNC_CONFLICT")); v != "" {
		s.Conflict = v
	}
	if v := strings.TrimSpace(os.Getenv("HEROS_TOOL_REGISTRY_SYNC_PUSH_TO_DISK")); v != "" {
		s.PushToDisk = v
	}
}
