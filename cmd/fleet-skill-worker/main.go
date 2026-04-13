// fleet-skill-worker: subscribe to heros.fleet.proposals.approved (NATS), apply prompt_engineering
// skill diffs to a local agent data_dir (skills/), optional git pull, then optionally POST agentd /api/catalog/reindex.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/approval"
	"github.com/heros-foreal/agentd/internal/fleetworker"
	"github.com/nats-io/nats.go"
)

type appliedState struct {
	IDs map[string]int64 `json:"ids"` // proposal id -> unix seconds
}

func loadState(path string) (appliedState, error) {
	var s appliedState
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return appliedState{IDs: map[string]int64{}}, nil
		}
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	if s.IDs == nil {
		s.IDs = map[string]int64{}
	}
	return s, nil
}

func saveState(path string, s appliedState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func main() {
	natsURL := flag.String("nats", os.Getenv("NATS_URL"), "NATS server URL (required)")
	subject := flag.String("subject", "heros.fleet.proposals.approved", "subscription subject")
	queue := flag.String("queue", "fleet-skill-workers", "optional queue group for competing consumers (empty = no queue)")
	dataDir := flag.String("data-dir", "", "agent data directory (same as agentd data_dir; contains skills/) — required")
	statePath := flag.String("state-file", defaultStatePath(), "JSON file of already-applied proposal ids")
	applySystem := flag.Bool("apply-system-prompt", false, "also write system/prompt.md when diff has ### SYSTEM_PROMPT")
	gitPullDir := flag.String("git-pull-dir", "", "if set, run: git -C <dir> pull --ff-only after a successful apply")
	reindexURL := flag.String("agentd-reindex-url", "", "if set, POST this URL (e.g. http://127.0.0.1:8787/api/catalog/reindex) after apply")
	apiKey := flag.String("api-key", "", "optional X-API-Key for agentd reindex")
	flag.Parse()

	if strings.TrimSpace(*natsURL) == "" || strings.TrimSpace(*dataDir) == "" {
		flag.Usage()
		os.Exit(2)
	}

	st, err := loadState(*statePath)
	if err != nil {
		log.Fatalf("state: %v", err)
	}

	nc, err := nats.Connect(*natsURL, nats.Name("fleet-skill-worker"), nats.Timeout(15*time.Second), nats.RetryOnFailedConnect(true), nats.MaxReconnects(-1))
	if err != nil {
		log.Fatalf("nats: %v", err)
	}
	defer nc.Close()
	log.Printf("fleet-skill-worker: connected, data_dir=%s subject=%s", *dataDir, *subject)

	handler := func(msg *nats.Msg) {
		var p approval.Proposal
		if err := json.Unmarshal(msg.Data, &p); err != nil {
			log.Printf("skip: bad json: %v", err)
			return
		}
		if p.Status != approval.StatusApproved {
			log.Printf("skip %s: status=%s (want approved)", p.ID, p.Status)
			return
		}
		if approval.Layer(p.Layer) != approval.LayerPrompt {
			return
		}
		if p.ID != "" {
			if _, ok := st.IDs[p.ID]; ok {
				log.Printf("skip %s: already applied", p.ID)
				return
			}
		}

		paths, err := fleetworker.ApplyPromptLayerDiff(*dataDir, p.TenantID, p.DiffText, fleetworker.Options{ApplySystemPrompt: *applySystem})
		if err != nil {
			log.Printf("apply %s: %v", p.ID, err)
			return
		}
		log.Printf("applied %s: %v", p.ID, paths)

		if p.ID != "" {
			st.IDs[p.ID] = time.Now().Unix()
			if err := saveState(*statePath, st); err != nil {
				log.Printf("state save: %v", err)
			}
		}

		if d := strings.TrimSpace(*gitPullDir); d != "" {
			cmd := exec.Command("git", "-C", d, "pull", "--ff-only")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				log.Printf("git pull: %v", err)
			}
		}

		u := strings.TrimSpace(*reindexURL)
		if u != "" {
			req, err := http.NewRequest(http.MethodPost, u, nil)
			if err != nil {
				log.Printf("reindex req: %v", err)
			} else {
				if k := strings.TrimSpace(*apiKey); k != "" {
					req.Header.Set("X-API-Key", k)
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					log.Printf("reindex: %v", err)
				} else {
					_ = resp.Body.Close()
					if resp.StatusCode < 200 || resp.StatusCode >= 300 {
						log.Printf("reindex: %s", resp.Status)
					}
				}
			}
		}
	}

	var sub *nats.Subscription
	if q := strings.TrimSpace(*queue); q != "" {
		sub, err = nc.QueueSubscribe(*subject, q, handler)
	} else {
		sub, err = nc.Subscribe(*subject, handler)
	}
	if err != nil {
		log.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	log.Printf("fleet-skill-worker: listening (Ctrl+C to stop)")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
}

func defaultStatePath() string {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(".heros-fleet-worker", "applied.json")
	}
	return filepath.Join(cfg, "heros-fleet-worker", "applied.json")
}

func init() {
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		_, _ = fmt.Fprintf(out, "Usage: %s -nats nats://127.0.0.1:4222 -data-dir C:\\\\path\\\\to\\\\.heros-agent\n\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
		_, _ = fmt.Fprintf(out, "\nSee docs/FLEET-SKILL-WORKER.md for storage patterns (NATS vs Git vs S3).\n")
	}
}
