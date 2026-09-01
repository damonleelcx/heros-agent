package mailer

import (
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"
)

// render.go turns a link into the mail that carries it.
//
// # 🔴 Why one struct produces both parts
//
// multipart/alternative asserts that its parts say the same thing, and the client shows exactly one of
// them. Writing the text and the HTML separately is how an expiry ends up reading "1 hour" in the part
// nobody opened and "24 hours" in the one they did — and no test catches it, because both parts are
// individually correct. Every template below fills one `content` and renders it twice.

// ── links ────────────────────────────────────────────────────────────────────────────────────────

// Links builds the URLs that go in mail.
//
// # 🔴 Why the base URL is configuration and never the request's Host header
//
// The natural implementation reads `r.Host` and builds `https://` + that. It works in every test and in
// production, and it is a complete account takeover: "I forgot my password" is a request an ATTACKER can
// make, and they choose its headers. `Host: evil.example` produces a real reset token, for the victim's
// real account, in a mail from the real product, pointing at the attacker's server — and the victim did
// nothing but click a link in a mail they were half expecting.
//
// So the origin comes from HEROS_PUBLIC_URL, which an operator sets once and a request cannot influence.
type Links struct{ base *url.URL }

// NewLinks validates the public origin of this deployment.
func NewLinks(base string) (Links, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return Links{}, fmt.Errorf("HEROS_PUBLIC_URL is not set. Mail contains links back to this " +
			"console, and the address cannot be taken from the incoming request: whoever asks for a " +
			"password reset chooses that request's headers, and would choose to have the link point at " +
			"themselves. Set it to the origin customers reach, e.g. https://console.example.com")
	}
	u, err := url.Parse(base)
	if err != nil {
		return Links{}, fmt.Errorf("HEROS_PUBLIC_URL is not a URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Links{}, fmt.Errorf("HEROS_PUBLIC_URL is %q; it needs an http:// or https:// scheme", base)
	}
	if u.Host == "" {
		return Links{}, fmt.Errorf("HEROS_PUBLIC_URL is %q; it needs a host", base)
	}
	// A path, query or fragment on the origin would be silently dropped or duplicated when a link is
	// built. Refusing is clearer than guessing which the operator meant.
	if strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" {
		return Links{}, fmt.Errorf("HEROS_PUBLIC_URL is %q; it should be just the origin, with no path "+
			"or query — for example https://console.example.com", base)
	}
	// Normalised to a single slash so every link reads `https://host/?token=…`. Without it `url.URL`
	// renders `https://host?token=…`, which is legal, unusual enough that some mail clients mangle it
	// when they linkify text, and different from what an operator pasted — three small ways for a link
	// to arrive broken, none of which would be traced back to here.
	u.Path, u.RawQuery, u.Fragment = "/", "", ""
	return Links{base: u}, nil
}

// Origin renders the configured origin, for copy that names it.
func (l Links) Origin() string {
	if l.base == nil {
		return ""
	}
	return strings.TrimRight(l.base.String(), "/")
}

// 🔴 Tokens travel in the QUERY, and the console strips them from the address bar as soon as it has read
// them. A token in the path would be recorded by every proxy log verbatim; in the query it still is, so
// this is a mitigation and not a cure — the real defence is that all three tokens are single-use and
// short-lived.
func (l Links) with(param, token string) string {
	u := *l.base
	q := url.Values{}
	q.Set(param, token)
	u.RawQuery = q.Encode()
	return u.String()
}

// Invitation is where somebody accepts an invitation and chooses a password.
func (l Links) Invitation(token string) string { return l.with("invite", token) }

// Reset is where somebody chooses a new password.
func (l Links) Reset(token string) string { return l.with("reset", token) }

// Verify is where somebody confirms their address.
func (l Links) Verify(token string) string { return l.with("verify", token) }

// ── templates ────────────────────────────────────────────────────────────────────────────────────

// content is one message, before it becomes two renderings of itself.
type content struct {
	Subject string
	// Heading is the one line that says what happened.
	Heading string
	// Body is the paragraphs, in order.
	Body []string
	// Action is the button, and the URL printed in full underneath it — because a button in the HTML
	// part is a link the plain-text reader cannot see, and a URL nobody can read is a URL nobody can
	// check before clicking.
	ActionLabel string
	ActionURL   string
	// Footer is the "if this was not you" line. Every one of these mails needs one.
	Footer string
}

// Invitation is the mail sent to somebody who has been asked to join an organization.
func Invitation(to, org, invitedBy, link string, expires time.Duration) Message {
	invitedByLine := fmt.Sprintf("%s has invited you to join %s on Heros.", invitedBy, org)
	if strings.TrimSpace(invitedBy) == "" {
		invitedByLine = fmt.Sprintf("You have been invited to join %s on Heros.", org)
	}
	return render(to, content{
		Subject: fmt.Sprintf("Join %s on Heros", org),
		Heading: "You have been invited",
		Body: []string{
			invitedByLine,
			"Heros reads a codebase, proposes changes to it, and asks a person before writing any of " +
				"them. Following this link lets you choose a password and sign in.",
			fmt.Sprintf("The invitation expires in %s.", humanDuration(expires)),
		},
		ActionLabel: "Accept the invitation",
		ActionURL:   link,
		Footer: "If you were not expecting this, you can ignore it — nothing happens until somebody " +
			"follows the link, and the invitation expires on its own.",
	})
}

// ResetPassword is the mail sent when somebody asks to reset a password.
func ResetPassword(to, org, link string, expires time.Duration) Message {
	return render(to, content{
		Subject: "Reset your Heros password",
		Heading: "Choose a new password",
		Body: []string{
			fmt.Sprintf("Somebody asked to reset the password for this address in %s.", org),
			fmt.Sprintf("The link works once and expires in %s. Choosing a new password also signs you "+
				"out everywhere else, so a session somebody else is holding ends immediately.",
				humanDuration(expires)),
		},
		ActionLabel: "Choose a new password",
		ActionURL:   link,
		// 🔴 Says plainly that inaction is safe. A reset mail that reads as urgent trains people to click
		// the one that was not from us.
		Footer: "If this was not you, ignore this message. Your password has not changed, and it will " +
			"not change unless somebody follows this link.",
	})
}

// VerifyEmail is the mail that proves an address is real.
func VerifyEmail(to, org, link string, expires time.Duration) Message {
	return render(to, content{
		Subject: "Confirm your email address",
		Heading: "Confirm this address",
		Body: []string{
			fmt.Sprintf("This address was added to %s on Heros.", org),
			fmt.Sprintf("Confirming it is how we know a password reset would actually reach you. The "+
				"link expires in %s. You can keep using Heros either way.", humanDuration(expires)),
		},
		ActionLabel: "Confirm this address",
		ActionURL:   link,
		Footer: "If you do not recognise this, ignore it. An unconfirmed address is not used for " +
			"anything except being confirmed.",
	})
}

// render produces the text and HTML parts from one content.
func render(to string, c content) Message {
	return Message{To: to, Subject: c.Subject, Text: renderText(c), HTML: renderHTML(c)}
}

func renderText(c content) string {
	var b strings.Builder
	b.WriteString("HEROS\n\n")
	b.WriteString(c.Heading)
	b.WriteString("\n\n")
	for _, p := range c.Body {
		b.WriteString(wrap(p, 72))
		b.WriteString("\n\n")
	}
	b.WriteString(c.ActionLabel)
	b.WriteString(":\n")
	b.WriteString(c.ActionURL)
	b.WriteString("\n\n--\n")
	b.WriteString(wrap(c.Footer, 72))
	b.WriteString("\n")
	return b.String()
}

// Palette copied BY VALUE from web/static/index.html.
//
// ⚠️ A real duplication seam: the console's tokens live in CSS, and nothing here can read them, so a
// palette change there leaves this mail on the old colours. Named constants rather than literals
// scattered through the markup, so the seam is one place to fix rather than thirty.
const (
	mailCanvas    = "#080d17"
	mailCard      = "#0e1422"
	mailInk       = "#dde2f0"
	mailInkMuted  = "#8b95b5"
	mailBorder    = "#ffffff1a"
	mailAccent    = "#2ecfa8"
	mailAccentInk = "#051a14"
	// 🔴 Single quotes inside every font stack. A double quote inside a style="…" attribute TERMINATES
	// the attribute: a parser keeps whatever came before it and silently discards the rest — including
	// `color`. This shipped once, as dark text on a dark card in every client, while a test asserting
	// the colour "appeared in the string" passed the whole time, because it did appear, inside a
	// fragment no parser applies. See TestEveryStyleAttributeSurvivesParsing.
	mailDisplay = "Georgia, 'Times New Roman', serif"
	mailBody    = "system-ui, -apple-system, 'Segoe UI', Helvetica, Arial, sans-serif"
)

func renderHTML(c content) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8">`)
	// 🔴 Declares the palette is intentional. Without it, dark-mode clients "helpfully" invert these
	// colours and turn the accent button into unreadable mud.
	b.WriteString(`<meta name="color-scheme" content="dark">`)
	b.WriteString(`<meta name="supported-color-schemes" content="dark">`)
	b.WriteString(`<title>` + html.EscapeString(c.Subject) + `</title></head>`)
	fmt.Fprintf(&b, `<body style="margin:0;padding:0;background:%s;">`, mailCanvas)
	fmt.Fprintf(&b, `<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" `+
		`style="background:%s;padding:32px 16px;"><tr><td align="center">`, mailCanvas)
	fmt.Fprintf(&b, `<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" `+
		`style="max-width:520px;background:%s;border:1px solid %s;border-radius:8px;">`, mailCard, mailBorder)

	// 🚫 The wordmark is TEXT, not an image. A hosted logo in a password-reset mail is a read receipt:
	// whoever serves it learns when the recipient opened it, and from where.
	fmt.Fprintf(&b, `<tr><td style="padding:28px 32px 0 32px;font-family:%s;font-size:12px;`+
		`letter-spacing:0.22em;color:%s;">HEROS</td></tr>`, mailBody, mailInkMuted)

	fmt.Fprintf(&b, `<tr><td style="padding:16px 32px 0 32px;font-family:%s;font-size:24px;`+
		`line-height:1.3;color:%s;">%s</td></tr>`, mailDisplay, mailInk, html.EscapeString(c.Heading))

	for _, p := range c.Body {
		fmt.Fprintf(&b, `<tr><td style="padding:16px 32px 0 32px;font-family:%s;font-size:15px;`+
			`line-height:1.6;color:%s;">%s</td></tr>`, mailBody, mailInkMuted, html.EscapeString(p))
	}

	fmt.Fprintf(&b, `<tr><td style="padding:28px 32px 0 32px;"><a href="%s" `+
		`style="display:inline-block;background:%s;color:%s;font-family:%s;font-size:15px;`+
		`font-weight:600;text-decoration:none;padding:12px 22px;border-radius:6px;">%s</a></td></tr>`,
		html.EscapeString(c.ActionURL), mailAccent, mailAccentInk, mailBody,
		html.EscapeString(c.ActionLabel))

	// The URL in full, under the button. Somebody who will not click a button should still be able to
	// read where it goes.
	fmt.Fprintf(&b, `<tr><td style="padding:16px 32px 0 32px;font-family:%s;font-size:12px;`+
		`line-height:1.6;color:%s;word-break:break-all;">%s</td></tr>`,
		mailBody, mailInkMuted, html.EscapeString(c.ActionURL))

	fmt.Fprintf(&b, `<tr><td style="padding:24px 32px 28px 32px;font-family:%s;font-size:12px;`+
		`line-height:1.6;color:%s;border-top:1px solid %s;margin-top:16px;">%s</td></tr>`,
		mailBody, mailInkMuted, mailBorder, html.EscapeString(c.Footer))

	b.WriteString(`</table></td></tr></table></body></html>`)
	return b.String()
}

// wrap breaks a paragraph at a column, so the plain-text part is readable in a terminal client.
func wrap(s string, width int) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > width {
			b.WriteString(line)
			b.WriteString("\n")
			line = w
			continue
		}
		line += " " + w
	}
	b.WriteString(line)
	return b.String()
}

// humanDuration renders an expiry the way a person would say it, because "3600000000000" and even "1h0m0s"
// are not sentences.
func humanDuration(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	case d >= 24*time.Hour:
		return "24 hours"
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	case d >= time.Hour:
		return "1 hour"
	default:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
}
