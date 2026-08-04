package launch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/heros-foreal/agentd/internal/adminlaunch"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/pgmigrate"
	"github.com/heros-foreal/agentd/internal/providergateway"
)

// adminbootstrap.go is the deployment-time entry point for creating the FIRST operator.
//
// It lives beside `StartAgentd` rather than in `cmd/agentd` because it needs the same three things the
// boot path resolves — the secrets source, the platform database, and the schema — and a second copy of
// that resolution in a command is a second place for a deployment to be configured differently from the
// process it is configuring. `adminlaunch.Bootstrap` holds the actual logic and knows nothing about
// environment variables or terminals.
//
// It runs as its own process, does its work, and exits. It does not need agentd to be running, and it
// deliberately does not talk to a running agentd: the directory it writes is in Postgres, which the
// running process reads at ITS next start. An operator bootstrapped against a live agentd is visible
// after that agentd restarts — stated in the output, because "I ran the command and still cannot sign
// in" is otherwise the obvious next question.

// AdminBootstrapOptions are the command's inputs.
type AdminBootstrapOptions struct {
	Subject string
	AdminID string
	Role    string
}

// RunAdminBootstrap creates or reconciles the first operator and writes a human-readable report to out.
//
// It returns an error for a genuine failure. Pass 1 — the seed is not provisioned yet — is NOT a
// failure of this function's contract but IS a non-zero exit for the caller, because the operator has
// work to do before the bootstrap is complete; `errors.Is(err, adminlaunch.ErrSeedNotProvisioned)`
// distinguishes them.
func RunAdminBootstrap(ctx context.Context, opts AdminBootstrapOptions, out io.Writer) error {
	secrets, err := providergateway.NewSecretsFromEnv(ctx)
	if err != nil {
		return fmt.Errorf("secrets source: %w", err)
	}
	fmt.Fprintf(out, "secrets source: %s (%s)\n", secrets.Describe().Kind, secrets.Describe().Detail)

	db, err := openPlatformDB(ctx)
	if err != nil {
		return err
	}
	if db == nil {
		return fmt.Errorf("the operator bootstrap needs the platform database, but %s is unset — the admin "+
			"directory is not something this command can write anywhere else", EnvPlatformDSN)
	}
	defer func() { _ = db.Close() }()

	// Applied here as well as at boot, and idempotently, so the bootstrap can run BEFORE the first agentd
	// start on a fresh install. `admin_factor` arrived in migration 0035; without this the command would
	// fail on a missing relation on exactly the deployment it exists to set up.
	if _, err := pgmigrate.Apply(ctx, db); err != nil {
		return fmt.Errorf("platform schema: %w", err)
	}

	res, err := adminlaunch.Bootstrap(ctx, secrets, db, adminlaunch.BootstrapRequest{
		Subject: strings.TrimSpace(opts.Subject),
		AdminID: strings.TrimSpace(opts.AdminID),
		Role:    adminrbac.Role(strings.TrimSpace(opts.Role)),
	})
	if err != nil {
		if res != nil && errors.Is(err, adminlaunch.ErrSeedNotProvisioned) {
			writeSeedInstructions(out, res)
			return err
		}
		return err
	}

	fmt.Fprintf(out, "\n── Operator bootstrapped ─────────────────────────────────────────────\n")
	fmt.Fprintf(out, "  admin id      %s\n", res.AdminID)
	fmt.Fprintf(out, "  role          %s\n", res.Role)
	fmt.Fprintf(out, "  second factor TOTP, seed held in the secrets manager as %s\n", res.SecretName)
	if res.AlreadyEnrolled {
		fmt.Fprintf(out, "  (the factor was already enrolled; the principal and role grant were reconciled)\n")
	}
	fmt.Fprintf(out, "\n  🔴 Restart agentd before signing in. The directory is read at start-up, so a\n")
	fmt.Fprintf(out, "     process that was already running does not yet know this operator exists.\n")
	fmt.Fprintf(out, "──────────────────────────────────────────────────────────────────────\n")
	return nil
}

// writeSeedInstructions is pass 1's output: everything the operator needs, and nothing written to the
// directory yet.
//
// 🔴 It prints a CREDENTIAL. That is the one moment the seed exists outside the secrets manager, which
// is why it goes to the caller's writer and never to `log` — the process log is shipped, indexed and
// retained, and a seed in it is a second factor anybody with log access holds.
func writeSeedInstructions(out io.Writer, res *adminlaunch.BootstrapResult) {
	fmt.Fprintf(out, "\n── Operator NOT yet created — provision the second factor first ──────\n")
	fmt.Fprintf(out, "\n  Nothing has been written to the admin directory. This command is safe to\n")
	fmt.Fprintf(out, "  re-run, and does the real work once the seed below is readable.\n")
	fmt.Fprintf(out, "\n  1. Add this to an authenticator app on the operator's own phone:\n\n")
	fmt.Fprintf(out, "     %s\n", res.OTPAuthURI)
	fmt.Fprintf(out, "\n     (or type the seed by hand: %s)\n", res.Seed)
	fmt.Fprintf(out, "\n  2. Store the SAME seed in the secrets manager under the logical name\n")
	fmt.Fprintf(out, "     %s. With HEROS_SECRETS_SOURCE=aws-secrets-manager\n", res.SecretName)
	fmt.Fprintf(out, "     and a HEROS_SECRETS_AWS_PREFIX of <prefix>, that is the secret id\n")
	fmt.Fprintf(out, "     <prefix>%s, holding the JSON object below — NOT the bare\n", res.SecretName)
	fmt.Fprintf(out, "     seed string, which this source rejects as malformed:\n\n")
	fmt.Fprintf(out, "       {\"api_key\": \"%s\"}\n", res.Seed)
	fmt.Fprintf(out, "\n  3. Run this command again. It will read the seed back through the same seam\n")
	fmt.Fprintf(out, "     a sign-in uses — proving it is actually resolvable — and only then create\n")
	fmt.Fprintf(out, "     the principal, the role grant and the factor.\n")
	fmt.Fprintf(out, "\n  🔴 The seed above is a credential and this is the only time it is shown. It is\n")
	fmt.Fprintf(out, "     not in the process log. Do not paste it into a ticket or a chat.\n")
	fmt.Fprintf(out, "──────────────────────────────────────────────────────────────────────\n")
}
