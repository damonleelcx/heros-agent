// heros: one command — start agentd in-process, wait for /health, then run the terminal agent REPL.
// Config and API keys are auto-discovered; workspace shell defaults to the current working directory.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/heros-foreal/agentd/internal/cliagent"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/launch"
)

func main() {
	cfgPath := flag.String("config", "", "override path to config.json (optional; otherwise auto-discover: cwd parents, %APPDATA%/heros/, ~/.heros/, ~/.heros-agent/, or defaults)")

	apiKey := flag.String("api-key", "", "X-API-Key when agentd auth_mode=required")
	openaiBase := flag.String("openai-base", "", "override LLM API base (else OPENAI_BASE_URL, then config openai_base_url)")
	openaiKeyFlag := flag.String("openai-api-key", "", "override LLM bearer token (else OPENAI_API_KEY, then config openai_api_key)")
	model := flag.String("model", "", "override chat model (else OPENAI_MODEL / HEROS_MODEL, then config openai_model)")
	sessionID := flag.String("session", "", "episodic memory session id (default: random UUID)")
	noSessionLog := flag.Bool("no-session-log", false, "do not auto-append each turn to episodic memory")
	workdir := flag.String("workdir", "", "workspace for heros_shell (default: current working directory)")
	noStream := flag.Bool("no-stream", false, "disable SSE streaming")
	agentShell := flag.Bool("agent-shell", false, "expose heros_agent_shell on agentd host")
	noReadline := flag.Bool("no-readline", false, "simple stdin REPL")
	targetTenant := flag.String("target-tenant", "", "default target_tenant for heros_submit_proposal (admin keys)")

	flag.Parse()

	cfg, cfgSrc, err := config.LoadAuto(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}

	srv, err := launch.StartAgentd(context.Background(), cfg)
	if err != nil {
		log.Fatalf("start agentd: %v", err)
	}
	defer func() {
		shCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		if err := srv.Shutdown(shCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 45*time.Second)
	err = launch.WaitReady(waitCtx, srv.AgentdBaseURL())
	waitCancel()
	if err != nil {
		log.Fatalf("agent not ready at %s: %v", srv.AgentdBaseURL(), err)
	}

	def := config.Default()
	openaiBaseStr := strings.TrimSpace(*openaiBase)
	if openaiBaseStr == "" {
		openaiBaseStr = strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	}
	if openaiBaseStr == "" {
		openaiBaseStr = strings.TrimSpace(cfg.OpenAIBaseURL)
	}
	if openaiBaseStr == "" {
		openaiBaseStr = def.OpenAIBaseURL
	}

	modelStr := strings.TrimSpace(*model)
	if modelStr == "" {
		modelStr = strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
	}
	if modelStr == "" {
		modelStr = strings.TrimSpace(os.Getenv("HEROS_MODEL"))
	}
	if modelStr == "" {
		modelStr = strings.TrimSpace(cfg.OpenAIModel)
	}
	if modelStr == "" {
		modelStr = def.OpenAIModel
	}

	openaiKey := strings.TrimSpace(*openaiKeyFlag)
	if openaiKey == "" {
		openaiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	if openaiKey == "" {
		openaiKey = strings.TrimSpace(cfg.OpenAIAPIKey)
	}
	if openaiKey == "" {
		log.Fatal("no LLM API key: set OPENAI_API_KEY, add openai_api_key to config.json, or pass -openai-api-key")
	}

	var wd string
	if strings.TrimSpace(*workdir) != "" {
		wd, err = filepath.Abs(*workdir)
		if err != nil {
			log.Fatalf("workdir: %v", err)
		}
	} else {
		wd, err = os.Getwd()
		if err != nil {
			log.Fatalf("getwd: %v", err)
		}
	}

	sid := *sessionID
	if sid == "" {
		sid = uuid.NewString()
	}

	base := srv.AgentdBaseURL()
	cfgNote := "defaults"
	if cfgSrc != "" {
		cfgNote = cfgSrc
	}
	log.Printf("heros: config %s | agent %s | data_dir=%s | workdir=%s", cfgNote, base, cfg.DataDir, wd)
	log.Printf("heros: /exit or Ctrl+D stops the agent")

	sess := &cliagent.Session{
		Agentd: &cliagent.AgentdClient{
			BaseURL:    base,
			APIKey:     *apiKey,
			HTTPClient: cliagent.DefaultHTTPClient(),
		},
		OpenAIBase:         openaiBaseStr,
		OpenAIKey:          openaiKey,
		Model:              modelStr,
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
