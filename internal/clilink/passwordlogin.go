package clilink

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/heros-foreal/agentd/internal/cli"
	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/runlink/transport"
)

// passwordlogin.go is `heros login` with an email address and a password (P28).
//
// # 🔴 There is no `--password` flag, and there will not be one
//
// A password in `argv` is a password in the shell's history file, in every `ps` on the machine, and in the
// process listing any other user on a shared box can read. The three ways to supply one are all channels
// that do not persist: an environment variable, standard input, and a prompt that does not echo.
//
// # The non-interactive contract is kept, not eroded
//
// The P11 contract is that every command completes without REQUIRING a terminal. This command still does:
// with `--email` and `$HEROS_PASSWORD` set it never prompts. What it adds is that when a value is missing
// AND a terminal is attached, it asks — and when a value is missing and there is NO terminal, it refuses
// with a message naming all three ways to supply it. 🚫 It never blocks on a prompt nobody can see, which
// is the specific behaviour that would turn a CI job into a timeout with no output.
//
// # How stdin stays unambiguous
//
// `heros login` has read a piped token from stdin since P11, and that must keep working. The rule is
// positional and deterministic rather than a guess about content:
//
//	two lines on stdin        → address, then password
//	one line + an address set → the password
//	one line, no address      → a platform token (the pre-P28 behaviour, unchanged)
//
// Nothing sniffs the value to decide what it is, so a password that happens to look like a token, or an
// address that happens to contain a colon, cannot change the meaning of the input.

// EnvEmail and EnvPassword are the non-interactive channels.
const (
	EnvEmail    = cli.EnvPrefix + "EMAIL"
	EnvPassword = cli.EnvPrefix + "PASSWORD"
)

// noTerminal is the refusal a CI runner gets. It names every non-interactive way in, because "no terminal"
// on its own is a diagnosis with no next action — and this is the one command a person is most likely to
// first run from a script.
//
// ⚠️ The stdin example uses an angle-bracketed placeholder rather than the bare word, and the brackets are
// load-bearing in two directions. A reader could take the bare word as the literal string to type; and a
// secret scanner reads an address followed by a credential-shaped literal as a credential PAIR and opens a
// finding on this file — GitGuardian did, on PR #73. It was a false positive, and that is the problem: a
// scanner that cries wolf on placeholder text is one whose next real finding gets waved through.
//
// 🔴 Which is also why this note describes the shape instead of quoting it. The first version of this
// comment reproduced the offending literal in order to explain it, so the commit that FIXED the finding
// contained the finding. Documenting a pattern by pasting it is how a scanner exemption becomes permanent.
const noTerminal = `login: no terminal to prompt on, and no credentials supplied.

Supply them one of these ways:
  heros login --email you@example.com          and set ` + EnvPassword + `
  printf 'you@example.com\n<your-password>\n' | heros login   (on stdin)
  heros login --token <machine-credential>     (a machine credential, for CI)
  heros login --device                          (approve from a browser instead)`

// passwordLogin signs in with an address and a password and stores a PERSONAL credential.
func (c Commands) passwordLogin(cfg cli.Config, s cli.Streams, piped []string) error {
	email, plaintext, err := resolveCredentials(cfg, piped)
	if err != nil {
		return err
	}

	s.Narratef("login: authenticating %s against %s…", email, runlink.PlatformBaseURL)
	res, err := c.client("").PasswordSignIn(context.Background(), email, plaintext,
		deviceLabel(), strings.TrimSpace(cfg.Get("org")))
	switch {
	case errors.Is(err, transport.ErrBadCredentials):
		// The platform's ONE refusal for an unknown address and a wrong password, repeated here rather
		// than elaborated: guessing which half was wrong is exactly what the server declines to disclose.
		return &cli.ExitError{Code: cli.ExitOperational,
			Msg: "login: that email and password did not match — check them, or reset your password at " +
				runlink.PlatformBaseURL + "/forgot-password"}
	case errors.Is(err, transport.ErrLocked):
		// The server's sentence, verbatim: it is the only party that knows how long the lock has left.
		return &cli.ExitError{Code: cli.ExitOperational, Msg: "login: " + err.Error()}
	case err != nil:
		return &cli.ExitError{Code: cli.ExitOperational, Msg: err.Error(), Err: err}
	}

	kind := res.CredentialKind
	if kind == "" {
		// A password sign-in always names a person, so `personal` is the honest default rather than an
		// empty field. It is stated because it decides whether removing this person ends this login.
		kind = "personal"
	}
	if err := cli.SaveCredential(cli.Credential{
		Identity: res.Identity, Token: res.Token, Endpoint: runlink.PlatformBaseURL,
		OrganizationName: res.OrganizationName, UserID: res.UserID, Kind: kind,
	}); err != nil {
		return err
	}

	who := res.OrganizationName
	if who == "" {
		who = res.Identity
	}
	s.Narratef("login: authenticated as %s (%s · credential stored 0600)", res.Identity, who)
	if !res.EmailVerified {
		// ⚠️ Said once, here, because it is the thing that will confuse them later: the CLI works, and then
		// an invitation or an upgrade is refused, and nothing connects the two.
		s.Narratef("login: ⚠️ %s is not confirmed yet. Everything works except inviting people and moving "+
			"to a paid plan; confirm the link we emailed you to lift that.", res.Email)
	}
	// 🔴 No token in the envelope, exactly as the other two paths do not carry one.
	return s.EmitJSON("login", cli.ExitOK, map[string]any{
		"authenticated":     true,
		"identity":          res.Identity,
		"endpoint":          runlink.PlatformBaseURL,
		"organization_id":   res.OrganizationID,
		"organization_name": res.OrganizationName,
		"credential_kind":   kind,
		"email_verified":    res.EmailVerified,
	}, nil, nil)
}

// resolveCredentials applies the precedence: flag, environment, piped stdin, prompt.
func resolveCredentials(cfg cli.Config, piped []string) (email, plaintext string, err error) {
	email = strings.TrimSpace(cfg.Get("email"))
	if email == "" {
		email = strings.TrimSpace(os.Getenv(EnvEmail))
	}
	plaintext = os.Getenv(EnvPassword)

	// Piped input fills whatever is still missing, positionally. See the header for why this is positional
	// rather than content-sniffed.
	switch {
	case len(piped) >= 2:
		if email == "" {
			email = strings.TrimSpace(piped[0])
		}
		if plaintext == "" {
			plaintext = piped[1]
		}
	case len(piped) == 1 && email != "" && plaintext == "":
		plaintext = piped[0]
	}

	if email == "" || plaintext == "" {
		if !onTerminal() {
			return "", "", &cli.ExitError{Code: cli.ExitInvalidCfg, Msg: noTerminal}
		}
		if email == "" {
			if email, err = promptLine("Email: "); err != nil {
				return "", "", &cli.ExitError{Code: cli.ExitOperational, Msg: "login: " + err.Error(), Err: err}
			}
		}
		if plaintext == "" {
			if plaintext, err = promptHidden("Password: "); err != nil {
				return "", "", &cli.ExitError{Code: cli.ExitOperational, Msg: "login: " + err.Error(), Err: err}
			}
		}
	}
	if email == "" || plaintext == "" {
		return "", "", &cli.ExitError{Code: cli.ExitInvalidCfg, Msg: noTerminal}
	}
	return email, plaintext, nil
}

// onTerminal reports whether there is a person at the other end of stdin.
//
// It checks STDIN rather than stdout, because that is where a prompt's answer has to come from: a command
// whose output is piped to a file still has a person at the keyboard, and one whose input is piped does not
// — even though its output is on a terminal.
func onTerminal() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// promptLine reads one visible line. The prompt goes to STDERR, which is where all human narration goes —
// stdout carries the machine envelope, and a prompt written there would corrupt it for anything parsing.
func promptLine(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// promptHidden reads a password without echoing it.
//
// 🔴 `term.ReadPassword` puts the terminal into raw mode and restores it, including on the interrupt path.
// The alternative people reach for — shelling out to `stty -echo` — leaves the terminal with echo OFF when
// the process is killed between the two calls, so the user's next command is invisible and they conclude
// their shell is broken. It is also the reason this is the most platform-dependent line in the change, and
// why the Windows leg of integration is called out in the PRD rather than assumed.
func promptHidden(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
