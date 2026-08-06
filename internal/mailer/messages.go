package mailer

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// messages.go holds the four message bodies and the environment that configures delivery.
//
// The copy lives HERE, beside the sender, rather than at each call site — the same single-sourcing rule the
// console applies to `content/identity.ts`. Copy that is written at the call site gets re-written at the next
// call site until one product has four ways of saying the same thing, and the one a customer reads is whichever
// path they happened to take.
//
// # What every body does and does not say
//
// Each says what happened, what the link does, how long it lasts, and what to do if it was not them. None
// names an internal mechanism, a version, or a hostname other than the console's own — a message forwarded to
// somebody's IT department should disclose nothing about our architecture.

// VerifyEmail is the confirmation sent at sign-up and on resend.
func VerifyEmail(consoleURL, link string, ttl time.Duration) Message {
	return content{
		subject:     "Confirm your email address",
		purpose:     PurposeVerifyEmail,
		heading:     "Confirm your email address",
		intro:       []string{"One click finishes setting up your account."},
		actionLabel: "Confirm this address",
		actionURL:   link,
		note: []string{
			"This link works once and expires in " + humanDuration(ttl) + ".",
			"Until you confirm, you can use everything except inviting people and moving to a paid plan — " +
				"both of those spend something on your behalf, so we ask you to confirm first.",
			"If you did not create an account at " + consoleURL + ", you can ignore this message. Nothing " +
				"was created in your name that this link does not activate.",
		},
	}.build()
}

// ResetPassword is the forgotten-password link.
func ResetPassword(consoleURL, link string, ttl time.Duration) Message {
	return content{
		subject:     "Reset your password",
		purpose:     PurposeResetPassword,
		heading:     "Reset your password",
		intro:       []string{"Somebody asked to reset the password for this address at " + consoleURL + "."},
		actionLabel: "Choose a new password",
		actionURL:   link,
		// 🔴 The consequence, in the callout rather than in the small print, because for many readers it is
		// the reason they are here — a device they no longer control is signed in somewhere.
		callout: "When you set a new password, every session and every personal access credential you hold " +
			"ends immediately, including any you no longer have access to. Machine credentials your " +
			"organization uses for automation are not affected, and the confirmation screen lists them by " +
			"name so you know what is still running.",
		note: []string{
			"This link works once and expires in " + humanDuration(ttl) + ".",
			"If this was not you, no action is needed: your password has not changed, and this link expires " +
				"on its own. If you get these repeatedly, tell whoever administers your organization.",
		},
	}.build()
}

// SignupAttempt goes to an address that already has an account when somebody tries to sign up with it again.
//
// 🔴 This message is why the sign-up response can stay neutral. Registration must not answer "does this
// address have an account here", so the screen says the same thing either way — and the information goes to
// the one party entitled to it, which is whoever holds the address.
func SignupAttempt(consoleURL, resetURL string) Message {
	return content{
		subject: "Someone tried to sign up with your email address",
		purpose: PurposeSignupAttempt,
		heading: "Someone tried to sign up with your address",
		intro: []string{
			"Somebody just tried to create an account at " + consoleURL + " using this address, which " +
				"already has one.",
			"Nothing changed. No second account was created and no existing account was modified.",
		},
		actionLabel: "Reset your password",
		actionURL:   resetURL,
		note: []string{
			"If it was you, sign in instead — or use the link above if you have forgotten your password.",
			"If it was not you, no action is needed.",
		},
	}.build()
}

// OwnerBootstrap is sent at boot on a deployment that declares a bootstrap owner and has no password for them.
//
// It is the ordinary reset path rather than a printed temporary password, deliberately: a temporary password
// is a secret that lives in a log until somebody changes it, and a single-use token is worthless the moment
// it is spent.
func OwnerBootstrap(consoleURL, link string, ttl time.Duration) Message {
	return content{
		subject:     "Set your password for " + consoleURL,
		purpose:     PurposeOwnerBootstrap,
		heading:     "Set your password",
		intro:       []string{"This install named you as its first owner."},
		actionLabel: "Set a password",
		actionURL:   link,
		note: []string{
			"This link works once and expires in " + humanDuration(ttl) + ". If it expires before you use " +
				"it, restarting the service issues another one.",
		},
	}.build()
}

func humanDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "24 hours"
		}
		return fmt.Sprintf("%d days", days)
	case d >= time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", h)
	default:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
}

// ConfigFromEnv reads the deployment's mail configuration.
//
//	HEROS_SMTP_HOST      the relay. Its ABSENCE is what selects the operator fallback.
//	HEROS_SMTP_PORT      defaults to 587 (STARTTLS) or 465 (implicit TLS)
//	HEROS_SMTP_USERNAME  optional — an internal relay often has none
//	HEROS_SMTP_PASSWORD  optional
//	HEROS_SMTP_FROM      the sender address. Required with the host: a relay that accepts anything still
//	                     needs a From, and defaulting it would put a made-up address on customer mail.
//	HEROS_SMTP_TLS       starttls (default) | implicit | none (loopback only)
//
// 🔴 Absence is not an error. A deployment that configures nothing gets the operator fallback and is told so
// on the readiness surface — see the package doc for why that is louder than a boot failure and honester than
// a silent discard.
func ConfigFromEnv() Config {
	c := Config{
		Host:     strings.TrimSpace(os.Getenv("HEROS_SMTP_HOST")),
		Username: os.Getenv("HEROS_SMTP_USERNAME"),
		Password: os.Getenv("HEROS_SMTP_PASSWORD"),
		From:     strings.TrimSpace(os.Getenv("HEROS_SMTP_FROM")),
		TLS:      TLSMode(strings.ToLower(strings.TrimSpace(os.Getenv("HEROS_SMTP_TLS")))),
	}
	if v := strings.TrimSpace(os.Getenv("HEROS_SMTP_PORT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Port = n
		}
		// A malformed port falls through to the default rather than failing the boot. The default is a
		// working value; refusing to start over a typo in an optional field would take a deployment down for
		// a setting it could have had wrong for a month with no consequence.
	}
	return c
}
