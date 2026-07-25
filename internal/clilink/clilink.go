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

	dryRun := cfg.Get("dry-run") == "true"
	if dryRun {
		s.Narratef("link: dry-run — rendering the exact payload for %s; nothing is transmitted", runID)
		return s.EmitJSON("link", cli.ExitOK, LinkData{RunID: runID, DryRun: true, Endpoint: runlink.PlatformBaseURL, Payload: payload}, nil, nil)
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
	if res.AlreadyLinked {
		s.Narratef("link: %s was already linked — counted once, not again (idempotent)", runID)
	} else {
		s.Narratef("link: %s linked · view it at %s", runID, res.RunURL)
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
