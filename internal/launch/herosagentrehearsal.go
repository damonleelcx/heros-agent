package launch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/herosagent"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
)

// herosagentrehearsal.go mounts D7's activation gate in the PRODUCT.
//
// # 🔴 The gate existed and nothing could run it
//
// `herosagent.NewRehearsal` was constructed in exactly one file in this repository, and that file was
// `cmd/proof/acceptance/main.go` — a proof binary nobody deploys. Everything else was real: the
// calibration set, the minimum-not-mean verdict, the per-fixture report stored on the version row,
// the fences that go red. But `Publisher.Activate` refuses any version whose rehearsal state is not
// `passed`, and no deployed code path could move a version off `pending`. So the operator console
// could publish a definition and could never activate one, and the refusal looked exactly like the
// gate working.
//
// That is the "no runner is how bugs survive" shape at capability grain: the mechanism is written,
// tested and unreachable.
//
// # What this file adds
//
// One function per activation. It resolves the version's own prompt and model through the operator
// registry, builds a runner over the provider gateway with the deployment's credential, and runs the
// pinned fixtures through `GateActivation` — which stores the per-fixture report on the version row
// whether it passes or fails, and refuses activation when any fixture is below the floor.
//
// # Why a factory rather than one Rehearsal built at boot
//
// A rehearsal measures ONE definition, and which model and prompt it uses is a property of that
// definition. A Rehearsal built at boot would have to bind a model before anybody had published a
// definition to measure — so it would either measure the wrong thing or be rebuilt anyway.

// agentRehearsalConfig is everything the gate needs that this deployment already holds.
type agentRehearsalConfig struct {
	Versions *herosagent.PGVersionStore
	Prompts  registryPrompts
	Models   registryModels
	Gateway  *providergateway.Gateway
	// Discovery is discovery's frontend registry, built once: it is the same pipeline that reads a
	// customer's repository, which is what makes a Go fixture's ground truth a MEASUREMENT.
	Discovery *discovery.Registry
	NowMS     func() int64
}

// rehearsalAnswerTokens bounds one calibration answer.
//
// 🔴 REQUIRED, not a tuning knob. Anthropic rejects a request with no `max_tokens`, and
// `providergateway` refuses to invent one rather than picking a ceiling on somebody's bill — found by
// the first live call, which is the only place it could be found. The value is generous for a JSON
// edge list and is stated here rather than buried in a struct literal.
const rehearsalAnswerTokens = 4096

// defaultCalibrationRoot is where the shipped image carries the fixture trees.
//
// The fixtures are REAL repositories — a Go tree whose typed frontend resolves a two-edge chain, a
// Python chain that is not a router, a fan-out with no merge — and their paths in `calibrationSet` are
// relative to a repository root. The image therefore carries those subtrees at the same relative
// paths under this directory, so one root resolves all nine. See deploy/Dockerfile.agentd.
const defaultCalibrationRoot = "/usr/share/heros/calibration"

// calibrationRoot answers where the fixtures are, and says so out loud when they are absent.
//
// 🚫 It does NOT fall back to "no fixtures, therefore no gate". A deployment that cannot measure must
// refuse to activate, and it does: this returns an error, `newAgentRehearsal` refuses to build a gate
// out of it, and `Publisher.Activate` then goes on refusing every `pending` version. An absent
// calibration set can only ever make activation harder.
func calibrationRoot() (string, error) {
	root := os.Getenv("HEROS_CALIBRATION_ROOT")
	if root == "" {
		root = defaultCalibrationRoot
	}
	// One fixture is enough to tell "the trees are here" from "this path is empty or wrong", and it is
	// the MEASURED one — the fixture whose ground truth is the Go frontend's own output.
	probe := filepath.Join(root, "internal", "herosagent", "testdata", "fixtures", "go_chain")
	if st, err := os.Stat(probe); err != nil || !st.IsDir() {
		return "", fmt.Errorf("the calibration fixtures are not readable at %s (looked for %s): the "+
			"rehearsal gate measures a definition against real repositories, and a deployment without "+
			"them cannot measure one — so it will keep refusing to activate any definition. Set "+
			"HEROS_CALIBRATION_ROOT, or deploy an image built with the fixture trees", root, probe)
	}
	return root, nil
}

// newAgentRehearsal builds the activation gate, or explains why this deployment has none.
func newAgentRehearsal(cfg agentRehearsalConfig) (adminops.RehearseFunc, error) {
	switch {
	case cfg.Versions == nil:
		return nil, errors.New("the rehearsal gate needs the durable version store: it records the " +
			"verdict on the row the console reads")
	case cfg.Gateway == nil:
		return nil, errors.New("the rehearsal gate needs the provider gateway — it makes one real " +
			"model call per fixture, which is what makes the number a measurement")
	case cfg.Discovery == nil:
		return nil, errors.New("the rehearsal gate needs discovery's frontend registry: a fixture's IR " +
			"must come from the SAME pipeline that reads a customer's repository")
	}
	root, err := calibrationRoot()
	if err != nil {
		return nil, err
	}
	if cfg.NowMS == nil {
		cfg.NowMS = nowMS
	}

	discover := func(repo string) (*discovery.IR, discovery.DiscoveryReport, error) {
		out, derr := discovery.Run(discovery.Options{
			Repo: repo, Registry: cfg.Discovery, WorkflowID: "calibration",
		})
		if derr != nil {
			return nil, discovery.DiscoveryReport{}, derr
		}
		ir := out.IR
		return &ir, out.Report, nil
	}

	return func(ctx context.Context, configHash string) error {
		v, ok, verr := cfg.Versions.Get(ctx, configHash)
		if verr != nil {
			return verr
		}
		if !ok {
			return fmt.Errorf("herosagent: nothing is published under %s, so there is nothing to "+
				"rehearse", configHash)
		}

		// 🔴 The definition's OWN prompt and model, not a pair chosen here. A gate that measured a
		// different configuration from the one it is about to activate would be measuring nothing —
		// the whole point of a content-addressed `config_hash` is that the thing measured and the
		// thing served are the same thing.
		prompt, ok, perr := cfg.Prompts.Render(ctx, v.Definition.PromptRef)
		if perr != nil {
			return perr
		}
		if !ok {
			return fmt.Errorf("herosagent: the definition's prompt %q is not published, so this "+
				"definition cannot be measured or served", v.Definition.PromptRef)
		}
		resolved, ok, merr := cfg.Models.Resolve(ctx, v.ModelRef)
		if merr != nil {
			return merr
		}
		provider, modelID := resolved.Provider, resolved.ModelID
		if !ok {
			return fmt.Errorf("herosagent: the definition's model %q is not in the operator registry. "+
				"Register it (a provider, a model id and a price reference) and rehearse again",
				v.ModelRef)
		}

		// ⚠️ The rehearsal keeps its OWN ceiling and does not adopt `resolved.Params`, which is now
		// available. Deliberate, and narrow: adopting them would change what the calibration set
		// measures — temperature and seed would start applying — and a gate whose verdict moves because
		// an unrelated field was plumbed is worse than one that measures a fixed configuration. It does
		// mean a rehearsal does not measure the exact parameters a customer-placed run uses; that gap is
		// real and is a question about what the gate should assert, not something to close in passing.
		maxTokens := rehearsalAnswerTokens
		model, gerr := herosagent.NewGatewayModel(cfg.Gateway, &registry.ModelEntry{
			Name: modelID,
			Spec: registry.ModelSpec{
				Provider: provider, ModelID: modelID,
				Params: registry.ModelParams{MaxTokens: &maxTokens},
			},
		}, prompt)
		if gerr != nil {
			return gerr
		}

		// The inference store is in-memory ON PURPOSE. A rehearsal's answers are measurements of a
		// candidate, not facts about any customer's workflow, and writing them to `heros_inference`
		// would put nine fixture graphs in the table the console counts as real analysis.
		runner, rerr := herosagent.NewRunner(model, herosagent.NewMemInferenceStore(),
			herosagent.DefaultConfidenceFloor, cfg.NowMS)
		if rerr != nil {
			return rerr
		}

		reh, herr := herosagent.NewRehearsal(
			herosagent.DiskFixtures{Root: root, Discover: discover}, discover, runner,
			herosagent.DefaultMinPrecision, herosagent.DefaultMinRecall)
		if herr != nil {
			return herr
		}
		_, gaerr := reh.GateActivation(ctx, cfg.Versions, configHash)
		return gaerr
	}, nil
}

// nowMS is the clock the rehearsal stamps with.
func nowMS() int64 { return time.Now().UnixMilli() }
