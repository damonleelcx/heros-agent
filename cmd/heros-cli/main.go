// heros-cli: terminal agent (Codex-style) backed by folder skills, tools, memory, and graph on agentd.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/heros-foreal/agentd/internal/cliagent"
)

func main() {
	agentdURL := flag.String("agentd-url", "http://127.0.0.1:8787", "running agentd base URL")
	apiKey := flag.String("api-key", "", "X-API-Key when agentd auth_mode=required")
	openaiBase := flag.String("openai-base", "https://api.openai.com/v1", "OpenAI-compatible API base URL")
	openaiKeyFlag := flag.String("openai-api-key", "", "bearer token (default: env OPENAI_API_KEY)")
	model := flag.String("model", "gpt-4o-mini", "chat model")
	sessionID := flag.String("session", "", "episodic memory session id (default: random UUID)")
	noSessionLog := flag.Bool("no-session-log", false, "do not auto-append each turn to episodic memory (model can still use heros_memory_save)")
	workdir := flag.String("workdir", ".", "workspace root for local heros_shell (absolute path recommended)")
	noStream := flag.Bool("no-stream", false, "disable SSE streaming (use if your OpenAI-compatible server mishandles stream+tools)")
	agentShell := flag.Bool("agent-shell", false, "expose heros_agent_shell (server-side shell on agentd; off by default)")
	noReadline := flag.Bool("no-readline", false, "simple stdin REPL instead of line editing")
	targetTenant := flag.String("target-tenant", "", "default target_tenant for heros_submit_proposal (admin API keys)")
	flag.Parse()

	openaiKey := *openaiKeyFlag
	if openaiKey == "" {
		openaiKey = os.Getenv("OPENAI_API_KEY")
	}
	if openaiKey == "" {
		log.Fatal("need -openai-api-key or OPENAI_API_KEY for the LLM (CLI talks to OpenAI directly; agentd holds skills/memory/tools)")
	}

	sid := *sessionID
	if sid == "" {
		sid = uuid.NewString()
	}

	wd, err := filepath.Abs(*workdir)
	if err != nil {
		log.Fatalf("workdir: %v", err)
	}

	sess := &cliagent.Session{
		Agentd: &cliagent.AgentdClient{
			BaseURL:    *agentdURL,
			APIKey:     *apiKey,
			HTTPClient: cliagent.DefaultHTTPClient(),
		},
		OpenAIBase:         *openaiBase,
		OpenAIKey:          openaiKey,
		Model:              *model,
		SessionID:          sid,
		WorkDir:            wd,
		AgentShell:         *agentShell,
		Stream:             !*noStream,
		UseReadline:        !*noReadline,
		TargetTenant:       *targetTenant,
		LogTurnsToEpisodic: !*noSessionLog,
	}

	ctx := context.Background()
	if err := sess.StdioREPL(ctx); err != nil {
		log.Fatal(err)
	}
}
