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
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/cli"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/nodeaxis"
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

// Login authenticates against https://heros-agent.space and stores a credential 0600. Nothing is echoed.
//
// # Three paths, and the order they are chosen in
//
//	--token / $HEROS_PLATFORM_TOKEN   → MACHINE. A credential naming nobody, for CI. Unchanged since P11.
//	--device                          → the browser approval flow (P27 task 13.2). Personal.
//	an address and a password         → P28. Personal, and the default a person gets.
//
// The order is explicit-beats-implicit: a flag that was given is honoured, and the password path is what
// remains. 🔴 The password path became the DEFAULT because the alternative it replaces was not one: before
// P28 the only console sign-in on the production seam was a shared string an operator read out of the
// cluster, so `heros login`'s device flow ended by sending a person to a door they had no key to.
//
// # How stdin stays unambiguous across the P11 and P28 meanings
//
// A single piped line has meant "a platform token" since P11 and still does. Two lines mean an address and
// a password; one line with an address already supplied means the password. The rule is positional, so
// nothing sniffs a value to decide what it is — see passwordlogin.go's header.
func (c Commands) Login(cfg cli.Config, s cli.Streams) error {
	token := cfg.Get("token")
	if token == "" {
		token = os.Getenv(cli.EnvPrefix + "PLATFORM_TOKEN")
	}

	// stdin is read ONCE, here, because it can only be read once. Both the token path and the password path
	// need it, and a second reader would find it empty — which is the sort of bug that only appears when
	// somebody pipes input, in CI, at the worst time.
	var piped []string
	if token == "" && !onTerminal() {
		b, _ := io.ReadAll(io.LimitReader(os.Stdin, 1<<16))
		for _, line := range strings.Split(strings.TrimRight(string(b), "\r\n"), "\n") {
			if line = strings.TrimRight(line, "\r"); strings.TrimSpace(line) != "" {
				piped = append(piped, line)
			}
		}
		emailSupplied := strings.TrimSpace(cfg.Get("email")) != "" || strings.TrimSpace(os.Getenv(EnvEmail)) != ""
		if len(piped) == 1 && !emailSupplied && cfg.Get("device") != "true" {
			// The pre-P28 meaning, preserved exactly: one line and no address is a platform token.
			token = strings.TrimSpace(piped[0])
			piped = nil
		}
	}

	if token == "" {
		if cfg.Get("device") == "true" {
			// The browser approval flow, kept as an explicit choice. It is the right one on a machine where
			// typing a password is not wanted — a shared terminal, a screen share, a recorded session.
			return c.deviceLogin(cfg, s)
		}
		// 🔴 The PERSON path. `--token` remains the machine path below: a credential with no user
		// reference, reported as machine, and NOT revoked by removing anybody (P27 task 13.4).
		//
		// This is what makes § 4.5's promise true in a terminal. A person pasting an organization key gets
		// a credential that names nobody: it survives their offboarding, attributes nothing, and "remove a
		// member and their access ends" is simply false in a shell.
		return c.passwordLogin(cfg, s, piped)
	}

	s.Narratef("login: validating token against %s…", runlink.PlatformBaseURL)
	cl := c.client(token)
	ctx := context.Background()
	// 🔴 The MACHINE path, unchanged in what it does (task 13.4): the same token, the same validation, the
	// same stored credential. What is added is what the platform now SAYS about it — the organization and
	// the credential kind — recorded so `status` can name them without a network call.
	//
	// `WhoAmI` reads the same endpoint `Validate` does. It is a second reader rather than a change to
	// `Validate`, because two callers depend on that function returning exactly an identity string.
	who, err := cl.WhoAmI(ctx)
	if err != nil {
		return &cli.ExitError{Code: cli.ExitOperational, Msg: err.Error(), Err: err}
	}
	identity := who.Identity
	kind := who.CredentialKind
	if kind == "" {
		// A platform that predates P27 says nothing about the kind. A token supplied on the command line
		// is the machine path by construction, so that is the honest default rather than an empty field.
		kind = "machine"
	}
	if err := cli.SaveCredential(cli.Credential{
		Identity: identity, Token: token, Endpoint: runlink.PlatformBaseURL,
		OrganizationName: who.OrganizationName, UserID: who.UserID, Kind: kind,
	}); err != nil {
		return err
	}
	s.Narratef("login: authenticated as %s (credential stored 0600)", identity)
	return s.EmitJSON("login", cli.ExitOK, map[string]any{
		"authenticated": true, "identity": identity, "endpoint": runlink.PlatformBaseURL,
		"organization_id": who.OrganizationID, "organization_name": who.OrganizationName,
		"credential_kind": kind,
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
	// Surfaces names every console page this transmission filled, and for each one it did not, the ONE
	// option that would (P29 §2.10). Derived from the surface registry, so a page added to the console
	// cannot be omitted from this list silently — see cli.LinkSurfaces and its fence.
	Surfaces []cli.LinkSurfaceReport `json:"surfaces,omitempty"`
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
	//
	// P29 §2.5 — the flag now has TWO forms and one meaning. `--with-ir <path>` transmits a
	// previously-written representation, exactly as before. Bare `--with-ir` discovers in place from
	// `--repo`, because the choreography was indefensible: opting in cost two commands and a file path
	// (`heros discover -out ir.json` then `heros link --with-ir ir.json`), and a developer who ran only
	// the second got a message about an artefact they had no reason to know existed.
	//
	// 🔴 What did NOT change is that it is opt-in and named. Making the structure default-on would trade
	// the egress promise — the product's most load-bearing sentence — for convenience, which is a level-1
	// concession bought with a level-3 gain.
	irSource := cfg.Get("with-ir")
	var irPayload *runlink.WorkflowIRPayload
	var irOrigin string
	if cfg.Has("with-ir") {
		ir, origin, ierr := c.resolveIR(irSource, repo, record, s)
		if ierr != nil {
			return ierr
		}
		irOrigin = origin
		// 🔴 The verdicts are computed HERE, against the real tree, by the real engine. See
		// internal/nodeaxis: the platform is forbidden from deriving one, and this is the only place the
		// source is.
		s.Narratef("link: computing per-node applicability against %s with the transform engine…", repo)
		report := nodeaxis.Compute(ir, repo)
		built := cli.BuildWorkflowIRWithVerdicts(ir, report)
		irPayload = &built
		applies, refused, reported := verdictTally(built)
		s.Narratef("link: --with-ir — %d node(s) and %d edge(s) of STRUCTURE will be transmitted as a "+
			"second payload: symbols, files, line spans, models, context policies and tool COUNTS. "+
			"No prompt text, no source, no keys.", len(built.Nodes), len(built.Edges))
		s.Narratef("link: %d node(s) carry axis verdicts computed here (%d applies, %d refused, coverage "+
			"%s). A node with no verdict is transmitted as REPORTED-BUT-UNDECIDED and the console shows "+
			"`not reported`, never `not applicable`.", reported, applies, refused, built.CoverageVersion)
	}

	dryRun := cfg.Get("dry-run") == "true"
	if dryRun {
		s.Narratef("link: dry-run — rendering the exact payload for %s; nothing is transmitted", runID)
		d := LinkData{RunID: runID, DryRun: true, Endpoint: runlink.PlatformBaseURL, Payload: payload}
		if irPayload != nil {
			d.WorkflowIR = &WorkflowIRData{Path: irOrigin, Nodes: len(irPayload.Nodes), Edges: len(irPayload.Edges), Payload: irPayload}
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
			data.WorkflowIR = &WorkflowIRData{Sent: false, Path: irOrigin, Nodes: len(irPayload.Nodes), Edges: len(irPayload.Edges)}
		} else {
			s.Narratef("link: structure transmitted — %d node(s), %d edge(s) · graph at %s", ires.Nodes, ires.Edges, ires.GraphURL)
			data.WorkflowIR = &WorkflowIRData{Sent: true, Path: irOrigin, Nodes: ires.Nodes, Edges: ires.Edges, GraphURL: ires.GraphURL}
		}
	}

	// 🔴 What the reader is told next. This block is the CLI's half of the defect this phase closes: a
	// successful link used to print a URL and stop, and the console behind that URL had fifteen pages
	// with nothing on them and no explanation. The moment of maximum context is right here.
	data.Surfaces = cli.ReportLinkSurfaces(data.WorkflowIR != nil && data.WorkflowIR.Sent, false)
	var unfilled []cli.LinkSurfaceReport
	for _, sf := range data.Surfaces {
		if !sf.Filled {
			unfilled = append(unfilled, sf)
		}
	}
	s.Narratef("link: %d of %d console surface(s) now carry this organization's data.",
		len(data.Surfaces)-len(unfilled), len(data.Surfaces))
	for _, sf := range unfilled {
		s.Narratef("link:   %-28s not filled — %s", sf.Route, sf.FillWith)
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

// resolveIR turns the `--with-ir` value into a discovered IR, by either of the two forms.
//
// It returns the ORIGIN as well, because the success output has to say which one happened: "discovered in
// place" and "read from ir.json" fail in different ways and a reader debugging an unexpected graph needs
// to know which artefact to look at.
func (c Commands) resolveIR(value, repo string, record runlink.RunRecord, s cli.Streams) (*discovery.IR, string, error) {
	if value != cli.WithIRDiscover {
		ir, err := cli.LoadIRForLink(value, record.WorkflowID, record.SourceRevision)
		return ir, value, err
	}
	// 🔴 Discovered with the run's OWN workflow id and revision, not with whatever the repo would derive.
	// `LoadIRForLink` refuses a mismatch for the path form precisely because attaching one repository's
	// shape to another's measurements produces a graph that looks right and describes something else; the
	// in-place form must not be the way around that check — so it is given the run's identity up front
	// and cannot disagree with it.
	s.Narratef("link: --with-ir with no path — discovering %s in place (offline, no account)…", repo)
	res, err := discovery.Run(discovery.Options{
		Repo:       repo,
		WorkflowID: record.WorkflowID,
		CommitSHA:  record.SourceRevision,
	})
	if err != nil {
		return nil, "", &cli.ExitError{Code: cli.ExitOperational,
			Msg: "link: --with-ir could not discover " + repo + ": " + err.Error(), Err: err}
	}
	if len(res.IR.Nodes) == 0 {
		return nil, "", &cli.ExitError{Code: cli.ExitInvalidCfg,
			Msg: "link: --with-ir discovered no call sites in " + repo + " — transmitting an empty " +
				"structure would tell the console this workflow has no nodes, which is a claim about your " +
				"code. Check --repo, or run `heros discover` to see the diagnostics."}
	}
	return &res.IR, "discovered in place", nil
}

// verdictTally counts what the structure payload is about to say, for the narration.
func verdictTally(p runlink.WorkflowIRPayload) (applies, refused, reported int) {
	for _, n := range p.Nodes {
		if len(n.AxisVerdicts) > 0 {
			reported++
		}
		for _, v := range n.AxisVerdicts {
			switch v.Status {
			case runlink.VerdictApplies:
				applies++
			case runlink.VerdictRefused:
				refused++
			}
		}
	}
	return applies, refused, reported
}

// allowlistCheck marshals the payload and asserts every key is allowlisted.
func allowlistCheck(p runlink.Payload) (offenders []string, err error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return runlink.AssertAllowlisted(b)
}

// deviceLogin runs the device authorization: ask for a code, show it, wait, store what comes back.
//
// # What it prints, and what it never prints
//
// The USER code and the URL — the two things a person needs to act. Never the device code (the CLI's own
// polling secret) and never the issued credential: a token in a terminal is a token in a scrollback
// buffer, a screen recording and a support paste.
func (c Commands) deviceLogin(cfg cli.Config, s cli.Streams) error {
	cl := c.client("")
	ctx := context.Background()

	auth, err := cl.RequestDeviceAuth(ctx, deviceLabel())
	if err != nil {
		return &cli.ExitError{Code: cli.ExitOperational, Msg: err.Error(), Err: err}
	}

	verify := auth.VerificationURI
	if strings.HasPrefix(verify, "/") {
		verify = runlink.PlatformBaseURL + verify
	}
	s.Narratef("login: open %s and enter code %s", verify, auth.UserCode)
	s.Narratef("login: waiting for approval (this terminal is described as %q)…", deviceLabel())

	res, err := cl.PollDeviceAuth(ctx, auth, nil)
	switch {
	case errors.Is(err, transport.ErrDeviceCode):
		// ONE sentence for denied, expired, already-used and unknown (task 13.7). The difference is
		// useful only to somebody guessing codes, and the user's next action is identical in all four.
		return &cli.ExitError{Code: cli.ExitOperational,
			Msg: "login: that code is no longer usable — run `heros login` again"}
	case errors.Is(err, context.DeadlineExceeded):
		return &cli.ExitError{Code: cli.ExitOperational,
			Msg: "login: nobody approved this terminal in time — run `heros login` again"}
	case err != nil:
		return &cli.ExitError{Code: cli.ExitOperational, Msg: err.Error(), Err: err}
	}

	if err := cli.SaveCredential(cli.Credential{
		Identity: res.Identity, Token: res.Token, Endpoint: runlink.PlatformBaseURL,
		OrganizationName: res.OrganizationName, Kind: "personal",
	}); err != nil {
		return err
	}
	who := res.OrganizationName
	if who == "" {
		who = res.Identity
	}
	s.Narratef("login: authenticated as %s (credential stored 0600)", who)
	// 🔴 The envelope carries no token, exactly as the --token path's does not. `credential_kind` is
	// stated rather than left to be inferred: it is what decides whether removing this person ends this
	// login, and a caller that had to guess would guess wrong for the machine path.
	return s.EmitJSON("login", cli.ExitOK, map[string]any{
		"authenticated":     true,
		"identity":          res.Identity,
		"endpoint":          runlink.PlatformBaseURL,
		"organization_id":   res.OrganizationID,
		"organization_name": res.OrganizationName,
		"credential_kind":   "personal",
	}, nil, nil)
}

// deviceLabel describes this machine for the approval screen and for the credential's label afterwards.
//
// It is a DISPLAY string and the server never compares it — so it is allowed to be missing, wrong, or
// duplicated. What it buys is that the person approving can tell which terminal they are approving, and
// that a revocation screen later names something a human recognises instead of an opaque id.
func deviceLabel() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "unknown host"
	}
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}
	if user != "" {
		host = user + "@" + host
	}
	return fmt.Sprintf("%s (%s/%s)", host, runtime.GOOS, runtime.GOARCH)
}
