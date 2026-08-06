// Command mailproof sends one real message through the deployment's configured relay, using the
// PRODUCTION code path.
//
// # Why this exists as a command rather than as a test
//
// `internal/mailer`'s own tests cover the operator fallback, header injection, the TLS refusal and the
// message bodies — everything that can be decided without a network. What they cannot cover is the one
// question an operator actually has after configuring SMTP: *does mail leave this deployment and arrive?*
// That needs a real relay, real credentials and a real inbox, so it is a command somebody runs, not a test
// that would either be skipped or make the suite depend on a mail server.
//
// 🔴 It constructs the mailer through `mailer.New(mailer.ConfigFromEnv(), nil)` and sends a real
// `ResetPassword` body — the same two calls the platform makes. A proof that sent a hand-written SMTP
// message would establish that the relay works and nothing about whether OUR code can reach it, which is
// the half that has actually been wrong before.
//
// # What it will not do
//
// It takes credentials from the environment only. There is no flag that accepts a password, because a
// password in argv is a password in the shell history and in every process listing — the same rule
// `heros login` follows. `make mail-proof` supplies them from the secret store without echoing them.
//
//	HEROS_SMTP_HOST/PORT/FROM/TLS/USERNAME/PASSWORD   the deployment's own configuration
//	MAILPROOF_TO                                      where to send it
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/mailer"
	"github.com/heros-foreal/agentd/internal/tenancy"
)

func main() {
	to := strings.TrimSpace(os.Getenv("MAILPROOF_TO"))
	if to == "" {
		fmt.Fprintln(os.Stderr, "mailproof: set MAILPROOF_TO to the address that should receive the proof")
		os.Exit(2)
	}

	m, err := mailer.New(mailer.ConfigFromEnv(), nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mailproof: the mail configuration was refused:", err)
		os.Exit(1)
	}
	fmt.Printf("mailproof: configured=%v from=%q\n", m.Configured(), m.From())
	if !m.Configured() {
		// 🔴 The fallback is a SUCCESSFUL send as far as the platform is concerned — it records the message
		// and returns nil. So a proof that just checked the error would pass on a deployment that delivers
		// nothing, which is the exact state this command exists to distinguish.
		fmt.Fprintln(os.Stderr, "mailproof: FAILED — the operator fallback was selected, so nothing would be "+
			"delivered. HEROS_SMTP_HOST and HEROS_SMTP_FROM did not reach this process.")
		os.Exit(1)
	}

	msg := mailer.ResetPassword("https://heros-agent.space",
		"https://heros-agent.space/reset-password?t=MAILPROOF-NOT-A-REAL-TOKEN", tenancy.ResetTokenTTL)
	msg.To = to

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.Send(ctx, msg); err != nil {
		fmt.Fprintln(os.Stderr, "mailproof: FAILED to send:", err)
		os.Exit(1)
	}
	fmt.Printf("mailproof: accepted by the relay for %s\n", to)
	// ⚠️ Said explicitly, because "the relay accepted it" is layer 3 of 4 and people stop at 3. An accepted
	// message can still bounce, be filtered, or be dropped by a policy the relay applies later.
	fmt.Println("mailproof: ⚠️ that is the RELAY accepting it, not the inbox receiving it. Check the inbox " +
		"— and if it is not there, check spam and the relay's own suppression list before changing anything.")
}
