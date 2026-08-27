// Command rehearsal runs the pinned calibration set against a live model and prints the verdict.
//
// # Why this exists as a command rather than a test
//
// The activation gate can only be reached one way in the product: an operator presses activate, and
// `AgentService.Activate` runs the rehearsal on the deployment's own credential. That is correct — the
// person who decides to activate is the one who should see the bill — but it makes the gate impossible
// to investigate. A failing rehearsal stores a report on a version row, and reproducing it means
// publishing another definition and pressing the button again.
//
// This runs the SAME pieces the deployed gate runs — DiskFixtures over discovery's real frontends,
// GatewayModel, Runner, Rehearsal.Run — against any prompt and model, and prints every abstention. It
// is how a stored report gets explained without touching a deployment.
//
// # What it found
//
// Four definitions were published against the calibration set on the hosted deployment and all four
// failed, with three fixtures at 0 correct / 0 wrong / 2 missed. That reads as an agent that finds no
// edges. Running the published prompt through this command showed every edge coming back
// `out_of_vocabulary`: the prompt described the edge `kind` as "<short relationship name>" and never
// named the closed {data, control} vocabulary `validate` checks. Six correct edges, at confidences from
// 0.74 to 0.98, all discarded. Reproduced on two unrelated models, which is what proved the defect was
// the prompt rather than the model.
//
// # 🔴 IT SPENDS
//
// One provider call per fixture, on whatever credential the environment resolves. `-dry-run` assembles
// and prints exactly what each fixture would send and makes NO call, which is the mode to use when the
// question is "what is the model being shown" rather than "what does it answer".
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/herosagent"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
)

func main() {
	log.SetFlags(0)
	promptFile := flag.String("prompt-file", "",
		"REQUIRED. The instruction to measure. To reproduce a deployment's stored report, use that "+
			"deployment's published prompt body, fetched by its content hash")
	provider := flag.String("provider", "anthropic", "the provider to call (anthropic | openai)")
	modelID := flag.String("model", "claude-opus-5", "the model id the definition binds")
	fixtureRoot := flag.String("fixture-root", ".",
		"repository root the calibration fixture directories are relative to")
	out := flag.String("out", "", "write the full report (with narratives) here as JSON")
	maxTokens := flag.Int("max-tokens", 4096,
		"max_tokens for one answer. REQUIRED by Anthropic; the gateway refuses to invent one")
	minPrecision := flag.Float64("min-precision", herosagent.DefaultMinPrecision, "per-fixture precision floor")
	minRecall := flag.Float64("min-recall", herosagent.DefaultMinRecall, "per-fixture recall floor")
	dryRun := flag.Bool("dry-run", false,
		"assemble and print what each fixture would send, make NO provider call and spend nothing")
	flag.Parse()

	if strings.TrimSpace(*promptFile) == "" {
		log.Fatalf("rehearsal: -prompt-file is required. There is no default: a rehearsal measures ONE " +
			"instruction, and silently supplying some other prompt would produce a number about a " +
			"configuration nobody chose")
	}
	body, err := os.ReadFile(*promptFile)
	if err != nil {
		log.Fatalf("rehearsal: reading %s: %v", *promptFile, err)
	}
	if strings.TrimSpace(string(body)) == "" {
		log.Fatalf("rehearsal: %s is empty. An empty instruction resolves and renders cleanly, so the "+
			"agent would run with no instruction and the only symptom would be a bad score", *promptFile)
	}
	sum := sha256.Sum256(body)
	log.Printf("prompt   %s (%d bytes, sha256 %s)", *promptFile, len(body), hex.EncodeToString(sum[:]))

	reg, err := discovery.DefaultRegistry()
	if err != nil {
		log.Fatalf("rehearsal: discovery registry: %v", err)
	}
	discover := func(repo string) (*discovery.IR, discovery.DiscoveryReport, error) {
		res, derr := discovery.Run(discovery.Options{Repo: repo, Registry: reg, WorkflowID: "calibration"})
		if derr != nil {
			return nil, discovery.DiscoveryReport{}, derr
		}
		ir := res.IR
		return &ir, res.Report, nil
	}
	fixtures := herosagent.DiskFixtures{Root: *fixtureRoot, Discover: discover}

	if *dryRun {
		// A Rehearsal with no analyser cannot be constructed, and must not be: a preview is a preview OF
		// a configured gate. The runner here is never called — Preview stops before the provider.
		reh, rerr := herosagent.NewRehearsal(fixtures, discover, unreachableAnalyser{}, *minPrecision, *minRecall)
		if rerr != nil {
			log.Fatalf("rehearsal: %v", rerr)
		}
		previews, perr := reh.Preview()
		if perr != nil {
			log.Fatalf("rehearsal: %v", perr)
		}
		log.Printf("DRY RUN  %d fixture(s), no provider call, no spend", len(previews))
		for _, p := range previews {
			var pretty json.RawMessage = p.ModelInput
			indented, _ := json.MarshalIndent(pretty, "  ", " ")
			log.Printf("\n===== %s (%s) =====\ntruth=%d edges  held_out=%d  candidate_pairs=%d\n  %s",
				p.Fixture, p.Language, p.TrueEdges, p.HeldOutEdges, p.Pairs, string(indented))
		}
		return
	}

	ctx := context.Background()
	secrets, err := providergateway.NewSecretsFromEnv(ctx)
	if err != nil {
		log.Fatalf("rehearsal: secrets source: %v", err)
	}
	// 🔴 ScopeRehearsal, never ScopeAnalysis. This sends the pinned fixtures and nothing of any
	// customer's, and the scope that carries customer source must not be widened by running this.
	endpoints, err := providergateway.BaseURLOverridesFromEnv(providergateway.ScopeRehearsal)
	if err != nil {
		log.Fatalf("rehearsal: endpoint overrides: %v", err)
	}
	for _, line := range endpoints.Describe() {
		log.Printf("endpoint %s", line)
	}

	model, err := herosagent.NewGatewayModel(
		providergateway.New(secrets, endpoints.Options()...),
		&registry.ModelEntry{
			Name: *modelID,
			Spec: registry.ModelSpec{
				Provider: *provider, ModelID: *modelID,
				Params: registry.ModelParams{MaxTokens: maxTokens},
			},
		}, string(body))
	if err != nil {
		log.Fatalf("rehearsal: gateway model: %v", err)
	}

	// In-memory ON PURPOSE: these answers measure a candidate and are not facts about any customer's
	// workflow, so they must not land in the table the console counts as real analysis.
	store := herosagent.NewMemInferenceStore()
	runner, err := herosagent.NewRunner(model, store, herosagent.DefaultConfidenceFloor,
		func() int64 { return time.Now().UnixMilli() })
	if err != nil {
		log.Fatalf("rehearsal: runner: %v", err)
	}
	reh, err := herosagent.NewRehearsal(fixtures, discover, runner, *minPrecision, *minRecall)
	if err != nil {
		log.Fatalf("rehearsal: %v", err)
	}

	const configHash = "live-rehearsal"
	log.Printf("LIVE     provider=%s model=%s floors P>=%.2f R>=%.2f — one provider call per fixture",
		*provider, *modelID, *minPrecision, *minRecall)
	start := time.Now()
	rep, err := reh.Run(ctx, herosagent.BindHash(configHash))
	elapsed := time.Since(start)
	if err != nil {
		// 🚫 NOT a score of zero. Rehearsal.Run refuses to report numbers it did not measure, and a
		// caller that printed a report here would be inventing one.
		log.Fatalf("rehearsal: the run did not complete after %s, so NOTHING was measured: %v", elapsed, err)
	}

	log.Printf("completed in %s over %d fixture(s)", elapsed, len(rep.Scores))
	for _, s := range rep.Scores {
		log.Printf("  %-22s %-11s P=%.2f R=%.2f  tp=%d fp=%d fn=%d  held_out=%d  refused=%d",
			s.Fixture, s.Language, s.Precision, s.Recall,
			s.TruePositives, s.FalsePositives, s.FalseNegatives, s.HeldOutEdges, len(s.Abstentions))
		for _, a := range s.Abstentions {
			conf := "none"
			if a.Confidence != nil {
				conf = fmt.Sprintf("%.2f", *a.Confidence)
			}
			log.Printf("      REFUSED %-34s %-30s confidence=%s", a.Subject, a.Reason, conf)
		}
	}
	log.Printf("worst P=%.2f R=%.2f   mean P=%.2f R=%.2f   passed=%v",
		rep.WorstPrecision, rep.WorstRecall, rep.MeanPrecision, rep.MeanRecall, rep.Passed)
	for _, f := range rep.Failures {
		log.Printf("FAILURE %s", f)
	}

	if *out != "" {
		if err := writeReport(ctx, *out, *provider, *modelID, elapsed, rep, store, configHash); err != nil {
			log.Fatalf("rehearsal: writing %s: %v", *out, err)
		}
		log.Printf("report   %s", *out)
	}
	if !rep.Passed {
		// A gate that did not pass exits non-zero, so a script cannot read "it ran" as "it passed".
		os.Exit(1)
	}
}

// unreachableAnalyser satisfies the Rehearsal's analyser dependency for -dry-run.
//
// 🚫 It PANICS rather than returning an empty Result. Preview stops before the provider by
// construction, so a call here would mean the dry run had started asking a model — and a silent empty
// answer would turn that into a report of nines zeros rather than a loud bug.
type unreachableAnalyser struct{}

func (unreachableAnalyser) Infer(context.Context, herosagent.Input, herosagent.AssessmentBinding, herosagent.Placement) (
	herosagent.Result, error) {
	panic("rehearsal: -dry-run reached the analyser; Preview must stop before the provider")
}

// reportDoc is what -out writes: the verdict plus the narratives, which live on the inference store
// rather than on the report and are the most useful thing when comparing two runs.
type reportDoc struct {
	Provider   string                     `json:"provider"`
	Model      string                     `json:"model"`
	ElapsedMS  int64                      `json:"elapsed_ms"`
	Report     herosagent.RehearsalReport `json:"report"`
	Narratives map[string]string          `json:"narratives"`
}

func writeReport(ctx context.Context, path, provider, modelID string, elapsed time.Duration,
	rep herosagent.RehearsalReport, store *herosagent.MemInferenceStore, configHash string) error {

	doc := reportDoc{Provider: provider, Model: modelID, ElapsedMS: elapsed.Milliseconds(),
		Report: rep, Narratives: map[string]string{}}
	for _, s := range rep.Scores {
		st, ok, err := store.Get(ctx, s.Fixture, "fixture", configHash)
		if err != nil || !ok {
			continue
		}
		if n := strings.TrimSpace(st.Narrative); n != "" {
			doc.Narratives[s.Fixture] = n
		}
	}
	blob, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, blob, 0o600)
}
