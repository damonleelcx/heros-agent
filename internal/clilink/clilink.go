// Package clilink implements the two platform-facing CLI commands — `login` and `link`. It is separate
// from internal/cli so that cli (the whole offline command surface) never imports the network. The
// dispatcher injects a Commands value as cli.NetCommands; when a build omits it, the net commands are
// simply unavailable rather than a broken import.
//
// The egress rules live here as behavior: linking requires an explicit command AND an authenticated
// identity (FR9); a dry-run renders the exact payload without sending it (FR10); a failed link never
// invalidates the local run (FR19); a successful link prints the console URL (FR18). The payload is
// built by runlink.BuildPayload — construction from the allowlist — and transmitted only to
// https://heros-agent.space by the transport client.
package clilink

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/cli"
	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/runlink/transport"
)

// Commands implements cli.NetCommands. RT and Timeout are injectable so tests can serve
// https://heros-agent.space locally and bound the wait; production leaves them zero (real transport,
// default timeout).
type Commands struct {
	RT      http.RoundTripper
	Timeout time.Duration
}

func (c Commands) client(token string) *transport.Client {
	var opts []transport.Option
	if c.Timeout > 0 {
		opts = append(opts, transport.WithTimeout(c.Timeout))
	}
	if c.RT != nil {
		opts = append(opts, transport.WithRoundTripper(c.RT))
	}
	return transport.NewClient(token, opts...)
}

// Login validates a platform token against https://heros-agent.space and stores it 0600. The token is
// read from --token, $HEROS_PLATFORM_TOKEN, or stdin (task 1.6). It is never echoed.
func (c Commands) Login(cfg cli.Config, s cli.Streams) error {
	token := cfg.Get("token")
	if token == "" {
		token = os.Getenv(cli.EnvPrefix + "PLATFORM_TOKEN")
	}
	if token == "" {
		// Read from stdin non-interactively (a pipe or a heredoc), never a prompt.
		b, _ := io.ReadAll(io.LimitReader(os.Stdin, 1<<16))
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		return &cli.ExitError{Code: cli.ExitInvalidCfg,
			Msg: "login: no token — supply --token, $HEROS_PLATFORM_TOKEN, or pipe it on stdin"}
	}

	s.Narratef("login: validating token against %s…", runlink.PlatformBaseURL)
	cl := c.client(token)
	ctx := context.Background()
	identity, err := cl.Validate(ctx)
	if err != nil {
		return &cli.ExitError{Code: cli.ExitOperational, Msg: err.Error(), Err: err}
	}
	if err := cli.SaveCredential(cli.Credential{Identity: identity, Token: token, Endpoint: runlink.PlatformBaseURL}); err != nil {
		return err
	}
	s.Narratef("login: authenticated as %s (credential stored 0600)", identity)
	return s.EmitJSON("login", cli.ExitOK, map[string]any{
		"authenticated": true, "identity": identity, "endpoint": runlink.PlatformBaseURL,
	}, nil, nil)
}

// LinkData is the machine payload for `link`. The payload field carries the EXACT bytes that would be
// (or were) transmitted, so a dry-run is evidence, not a summary.
type LinkData struct {
	RunID    string          `json:"run_id"`
	DryRun   bool            `json:"dry_run"`
	Endpoint string          `json:"endpoint"`
	Payload  runlink.Payload `json:"payload"`
	Accepted bool            `json:"accepted,omitempty"`
	RunURL   string          `json:"run_url,omitempty"`
	Already  bool            `json:"already_linked,omitempty"`
	// WorkflowIR is present only when --with-ir was given. Its absence in the envelope is the machine
	// -readable form of "structure was not transmitted", which a CI job can assert on.
	WorkflowIR *WorkflowIRData `json:"workflow_ir,omitempty"`
}

// WorkflowIRData reports the OPT-IN structure upload.
type WorkflowIRData struct {
	Sent     bool   `json:"sent"`
	Path     string `json:"path"`
	Nodes    int    `json:"nodes"`
	Edges    int    `json:"edges"`
	GraphURL string `json:"graph_url,omitempty"`
	// Payload is rendered under --dry-run so the EXACT second transmission is reviewable before it is
	// ever made. Opting in to sending more is only meaningful if you can read what "more" is.
	Payload *runlink.WorkflowIRPayload `json:"payload,omitempty"`
}

// Link transmits a run's allowlisted payload to the platform, or (with --dry-run) renders it without
// sending. It requires an authenticated identity unless dry-run (which sends nothing).
func (c Commands) Link(cfg cli.Config, s cli.Streams) error {
	runID, err := cfg.Require("run")
	if err != nil {
		return err
	}
	repo := cfg.Get("repo")
	if repo == "" {
		repo = "."
	}
	record, err := cli.OpenRunStore(repo).Get(runID)
	if err != nil {
		return err
	}
	payload := runlink.BuildPayload(record)

	// Defense in depth: whatever we are about to render/send must contain only allowlisted keys. This is
	// the same assertion the egress test makes, run at the boundary itself so a construction bug is
	// caught before transmission, not only in CI.
	if offenders, aerr := allowlistCheck(payload); aerr != nil {
		return &cli.ExitError{Code: cli.ExitOperational, Msg: "link: payload self-check failed: " + aerr.Error()}
	} else if len(offenders) > 0 {
		return &cli.ExitError{Code: cli.ExitOperational,
			Msg: "link: REFUSING to transmit — payload carries non-allowlisted keys: " + strings.Join(offenders, ", ")}
	}

	// --with-ir is the OPT-IN second payload. It is resolved before any transmission so a mismatched or
	// unreadable IR fails the command outright rather than after the run has already been linked —
	// half a transmission is the one outcome neither the developer nor the platform can reason about.
	irPath := cfg.Get("with-ir")
	var irPayload *runlink.WorkflowIRPayload
	if irPath != "" {
		ir, ierr := cli.LoadIRForLink(irPath, record.WorkflowID, record.SourceRevision)
		if ierr != nil {
			return ierr
		}
		built := cli.BuildWorkflowIR(ir)
		irPayload = &built
		s.Narratef("link: --with-ir — %d node(s) and %d edge(s) of STRUCTURE will be transmitted as a "+
			"second payload: symbols, files, line spans, models, context policies and tool COUNTS. "+
			"No prompt text, no source, no keys.", len(built.Nodes), len(built.Edges))
	}

	dryRun := cfg.Get("dry-run") == "true"
	if dryRun {
		s.Narratef("link: dry-run — rendering the exact payload for %s; nothing is transmitted", runID)
		d := LinkData{RunID: runID, DryRun: true, Endpoint: runlink.PlatformBaseURL, Payload: payload}
		if irPayload != nil {
			d.WorkflowIR = &WorkflowIRData{Path: irPath, Nodes: len(irPayload.Nodes), Edges: len(irPayload.Edges), Payload: irPayload}
		}
		return s.EmitJSON("link", cli.ExitOK, d, nil, nil)
	}

	// Real link requires an authenticated identity (FR9).
	cred, ok := cli.LoadCredential()
	if !ok {
		return &cli.ExitError{Code: cli.ExitOperational,
			Msg: "link: authentication required — run `heros login` first (nothing was transmitted)"}
	}

	s.Narratef("link: transmitting %s to %s as %s…", runID, runlink.PlatformBaseURL, cred.Identity)
	cl := c.client(cred.Token)
	res, err := cl.Link(context.Background(), payload)
	if err != nil {
		// FR19: a link failure does not invalidate the local result. The run record on disk is untouched;
		// the failure is reported as a TRANSMISSION failure, distinct from a run failure.
		s.Narratef("link: transmission FAILED (the local run %s remains valid): %v", runID, err)
		return &cli.ExitError{Code: cli.ExitOperational, Msg: fmt.Sprintf("link failed for %s (local result intact): %v", runID, err), Err: err}
	}

	data := LinkData{RunID: runID, Endpoint: runlink.PlatformBaseURL, Payload: payload, Accepted: res.Accepted, RunURL: res.RunURL, Already: res.AlreadyLinked}

	// The structure goes SECOND, and only after the run itself landed. If it fails, the run is still
	// linked and stays linked: the optional half of an opt-in must never be able to undo the half the
	// developer did not opt into.
	if irPayload != nil {
		ires, ierr := cl.SendWorkflowIR(context.Background(), *irPayload)
		if ierr != nil {
			s.Narratef("link: the run linked, but the structure did NOT (%v). Nothing about the run is "+
				"affected; re-run with --with-ir to try the structure again.", ierr)
			data.WorkflowIR = &WorkflowIRData{Sent: false, Path: irPath, Nodes: len(irPayload.Nodes), Edges: len(irPayload.Edges)}
		} else {
			s.Narratef("link: structure transmitted — %d node(s), %d edge(s) · graph at %s", ires.Nodes, ires.Edges, ires.GraphURL)
			data.WorkflowIR = &WorkflowIRData{Sent: true, Path: irPath, Nodes: ires.Nodes, Edges: ires.Edges, GraphURL: ires.GraphURL}
		}
	}

	if res.AlreadyLinked {
		s.Narratef("link: %s was already linked — counted once, not again (idempotent)", runID)
	} else {
		s.Narratef("link: %s linked · view it at %s", runID, res.RunURL)
		// 🔴 Naming the credential is part of the success message, not a nicety.
		//
		// This line printed a dashboard URL and stopped. The console then asked for a "tenant
		// credential" — and on a deployment using the older `configured` seam that is a DIFFERENT
		// secret, which no command here mints, prints or derives. So the final line of a successful
		// link pointed at a door the person who ran it had no key to, and nothing said so. A console
		// on the `platform` seam takes the token this command just authenticated with; saying which
		// one it is costs a sentence and is the difference between a link and a dead end.
		s.Narratef("link: sign in there with the same token `heros login` stored — no second credential.")
	}
	return s.EmitJSON("link", cli.ExitOK, data, nil, nil)
}

// allowlistCheck marshals the payload and asserts every key is allowlisted.
func allowlistCheck(p runlink.Payload) (offenders []string, err error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return runlink.AssertAllowlisted(b)
}
