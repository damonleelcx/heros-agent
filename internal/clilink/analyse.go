package clilink

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/heros-foreal/agentd/internal/cli"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/herosagent"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// analyse.go is `heros analyse` — P30 task 7.1, the CUSTOMER-PLACED half of D6.
//
// # What it is, in one sentence
//
// It runs the PLATFORM'S OWN analysis agent, on this machine, against a locally discovered IR, spending
// THIS machine's provider credential, and submits the result through the ingest that already carries
// workflow structure.
//
// # Why it lives here and not in internal/cli
//
// `internal/cli` links no network stack, by construction and by two separate tests — and it must not,
// because that is what makes "discover/apply/eval cannot reach the network" a property of the build
// rather than a promise. `internal/herosagent` reaches a provider through `providergateway`, which links
// an HTTP client and the AWS SDK. So this is a NetCommand, like `link` and `push-source`, injected into
// the dispatcher rather than compiled into the offline surface.
//
// # 🔴 Whose credential, and where it never goes
//
// The provider key is resolved on THIS machine, by `providergateway`'s configured Secrets source — the
// process environment by default. It is never sent anywhere: the platform holds no customer provider key
// under any placement (Q1), and the definition that comes down carries a provider NAME, not a value.
// That asymmetry is the entire reason placement `customer` exists — it is the supported answer for a
// customer whose source may not leave their network, and the platform is deliberately not offered a way
// to hold their key on their behalf.

// analyseTimeout bounds the whole command. Generous compared to a link, because it contains a provider
// call; bounded at all because a CI step that hangs is worse than one that fails.
const analyseTimeout = 5 * time.Minute

// Analyse runs the platform's agent locally and submits what it produced.
func (c Commands) Analyse(cfg cli.Config, s cli.Streams) error {
	cred, ok := cli.LoadCredential()
	if !ok {
		return badConfig("analyse: not authenticated — run `heros login`. A customer-placed " +
			"analysis runs the PLATFORM's agent definition, so it has to ask the platform which one is " +
			"active before it can run anything")
	}
	irPath := cfg.Get("ir")
	if irPath == "" {
		return badConfig("analyse: -ir is required — it names the IR `heros discover -out <path>` " +
			"wrote. The analysis reads the gap in THAT graph and nothing else")
	}

	ctx, cancel := context.WithTimeout(context.Background(), analyseTimeout)
	defer cancel()
	client := c.client(cred.Token)

	// 1 · what is this tenant allowed to do, and what is it supposed to run.
	def, err := client.FetchAgentDefinition(ctx)
	if err != nil {
		return failed("analyse: reading the active agent definition", err)
	}
	if err := refuseUnrunnable(def); err != nil {
		return err
	}

	// 2 · the local graph, and the gap in it.
	ir, err := cli.LoadIR(irPath)
	if err != nil {
		return err
	}
	report := discovery.DiscoveryReport{}
	residue := herosagent.SelectResidue(ir, report, nil)

	s.Narratef("analyse: %s at %s — %d candidate pairs, %d unlabelled regions, under definition %s",
		ir.Workflow.ID, short12(ir.Workflow.Repo.CommitSHA),
		len(residue.Pairs), len(residue.UnlabelledRegions), short12(def.ConfigHash))

	// 3 · the runner. THE SAME Runner the platform uses, with the platform's floor — see
	// NewCustomerRunner for why a second implementation is the thing D6 exists to prevent.
	res, err := c.runLocally(ctx, def, ir, residue)
	if err != nil {
		return err
	}
	if res.Code == herosagent.CodeNothingToInfer {
		// A fully rule-covered repository costs nothing and produces nothing. A healthy answer, and it
		// must not read as a failure — D3's "cost proportional to the gap", visible at the terminal.
		s.Narratef("analyse: every pair and region already carries a rule-derived fact — " +
			"no provider call was made and there is nothing to submit")
		return nil
	}

	// 4 · submit, through the ingest that already exists. 🚫 No second transport.
	payload := cli.BuildWorkflowIR(ir)
	payload.AgentConfigHash = def.ConfigHash
	for _, e := range res.Edges {
		payload.Edges = append(payload.Edges, runlink.WireIREdge{
			From: e.From, To: e.To, Kind: e.Kind,
			Author: runlink.AuthorHEROS, Confidence: e.Confidence,
		})
	}
	for _, a := range res.Abstentions {
		payload.Abstentions = append(payload.Abstentions, runlink.WireAbstention{
			Subject: a.Subject, Cause: string(a.Reason), Confidence: a.Confidence,
		})
	}

	if cfg.Get("dry-run") == "true" {
		// The same courtesy `link --dry-run` extends: render the EXACT payload, without sending it. A
		// developer deciding whether to enable this reads what would cross, not a description of it —
		// and on this command that matters more than on `link`, because what crosses now includes facts
		// a model wrote about their repository.
		s.Narratef("analyse: dry-run — rendering the exact payload; nothing is transmitted")
		return s.EmitJSON("analyse", cli.ExitOK, AnalyseData{
			DryRun: true, Endpoint: runlink.PlatformBaseURL, ConfigHash: def.ConfigHash,
			InferredEdges: len(res.Edges), Abstentions: len(res.Abstentions), Payload: &payload,
		}, nil, nil)
	}

	out, err := client.SendWorkflowIR(ctx, payload)
	if err != nil {
		return failed("analyse: submitting the result", err)
	}
	s.Narratef("analyse: submitted %d inferred edge(s) and %d abstention(s) — %s",
		len(res.Edges), len(res.Abstentions), out.GraphURL)
	return nil
}

// refuseUnrunnable turns the platform's answer into a decision, naming what is missing.
//
// 🔴 Every branch here refuses rather than proceeding, and each says something DIFFERENT. "This tenant
// is analysed platform-side" and "HEROS is switched off for this tenant" and "this deployment has
// published no agent" send a developer to three different places, and a single "cannot analyse" would
// send them to none of them.
func refuseUnrunnable(def runlink.AgentDefinition) error {
	switch herosagent.Placement(def.Placement) {
	case herosagent.PlacementCustomer:
		// The one that proceeds.
	case herosagent.PlacementPlatform:
		return badConfig("analyse: this organization is placed `platform`, so the platform runs " +
			"the analysis under its own credential and already has the answer. Running it here would " +
			"spend a second credential to produce a result the ingest would refuse")
	case herosagent.PlacementDisabled:
		return badConfig("analyse: agent analysis is disabled for this organization, which is the " +
			"default. An operator enables it per organization; until then your graph shows exactly the " +
			"facts your language frontends established, which is what it showed before")
	default:
		return badConfig(fmt.Sprintf("analyse: the platform reported placement %q, which this "+
			"build does not know. Run `heros upgrade`", def.Placement))
	}
	if !def.Runnable() {
		// 🔴 Placement says `customer` and the definition is incomplete. Proceeding would call a provider
		// with a missing instruction, which produces a bill and an answer to a question nobody asked.
		return failed("analyse: this deployment has published no active agent definition, so "+
			"there is nothing to run. An operator publishes one and activates it after its rehearsal", nil)
	}
	return nil
}

// runLocally performs the inference. Split out so the wiring — gateway, secrets, model entry — is one
// readable block rather than a paragraph in the middle of the command.
func (c Commands) runLocally(ctx context.Context, def runlink.AgentDefinition,
	ir *discovery.IR, residue herosagent.Residue) (herosagent.Result, error) {

	// The customer's OWN secrets source. `NewSecretsFromEnv` reads $HEROS_SECRETS_SOURCE, so a customer
	// running a real secrets manager gets theirs; the default is the process environment.
	secrets, err := providergateway.NewSecretsFromEnv(ctx)
	if err != nil {
		return herosagent.Result{}, failed("analyse: resolving this machine's secrets source", err)
	}
	var opts []providergateway.Option
	if c.RT != nil {
		// Injected only for tests, exactly as the platform client's transport is — and it reaches the
		// PROVIDER here, not the platform. The two are separate round-trippers on purpose: a test that
		// served both through one would not notice a command that sent a repository's residue to the
		// platform, or a credential to us.
		opts = append(opts, providergateway.WithHTTPClient(&http.Client{Transport: c.RT, Timeout: analyseTimeout}))
	}
	gw := providergateway.New(secrets, opts...)

	entry := &registry.ModelEntry{
		// A model entry assembled from what the platform sent rather than resolved from a registry: this
		// machine has no operator registry and must not need one. The definition already resolved it —
		// see AgentDefinition.ModelID.
		Name: def.ModelID,
		Spec: registry.ModelSpec{Provider: def.Provider, ModelID: def.ModelID},
	}
	model, err := herosagent.NewGatewayModel(gw, entry, def.Prompt)
	if err != nil {
		return herosagent.Result{}, err
	}

	runner, err := herosagent.NewCustomerRunner(model, herosagent.NewMemInferenceStore(),
		def.ConfidenceFloor, func() int64 { return time.Now().UnixMilli() })
	if err != nil {
		return herosagent.Result{}, err
	}

	res, err := runner.Infer(ctx, herosagent.Input{
		WorkflowID:     ir.Workflow.ID,
		SourceRevision: ir.Workflow.Repo.CommitSHA,
		RuleIR:         ir,
		Residue:        residue,
		Budget: herosagent.Budget{
			// The PLATFORM's budget. A customer spending their own credential still gets a ceiling: an
			// unbounded run is how a repository-shaped cost arrives on a bill nobody approved, and whose
			// bill it is does not change that.
			MaxTokens: def.MaxTokens,
			MaxWall:   time.Duration(def.MaxWallSeconds) * time.Second,
		},
	}, def.ConfigHash, herosagent.PlacementCustomer)
	if err != nil {
		// 🚫 Never an empty graph. The cause travels, exactly as it does platform-side: a provider outage
		// reported as "we found nothing" is an outage rendered as a finding about the customer's workflow.
		return herosagent.Result{}, &cli.ExitError{Code: cli.ExitOperational, Err: err,
			Msg: fmt.Sprintf("analyse: the analysis failed (%s): %s", res.Code, res.Cause)}
	}
	return res, nil
}

// AnalyseData is the machine output — the same shape LinkData is, so a CI step parses one format.
type AnalyseData struct {
	DryRun        bool                       `json:"dry_run,omitempty"`
	Endpoint      string                     `json:"endpoint"`
	ConfigHash    string                     `json:"agent_config_hash"`
	InferredEdges int                        `json:"inferred_edges"`
	Abstentions   int                        `json:"abstentions"`
	GraphURL      string                     `json:"graph_url,omitempty"`
	Payload       *runlink.WorkflowIRPayload `json:"payload,omitempty"`
}

// badConfig and failed name the two exits this command can take. Spelled here because `internal/cli`
// keeps its own constructors unexported — a deliberate boundary, since an exit code is a contract with
// CI and every package minting one should have to say so.
func badConfig(msg string) error {
	return &cli.ExitError{Code: cli.ExitInvalidCfg, Msg: msg}
}

func failed(msg string, err error) error {
	if err != nil {
		msg = msg + ": " + err.Error()
	}
	return &cli.ExitError{Code: cli.ExitOperational, Msg: msg, Err: err}
}

func short12(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}
