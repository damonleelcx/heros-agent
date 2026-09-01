package mailer

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// mailer_test.go. Two of these fences exist because the bug they describe has already shipped.

// styleAttr reads style attributes the way a PARSER does, not the way the source reads.
//
// 🔴 This is the whole lesson. A double quote inside style="…" TERMINATES the attribute: the parser
// keeps what came before it and silently discards the rest. Assert on what the consumer keeps, never on
// what was emitted.
var styleAttr = regexp.MustCompile(`style="([^"]*)"`)

// brokenStyles returns a description of every style attribute that a parser would truncate.
func brokenStyles(markup string) []string {
	var bad []string
	for _, m := range styleAttr.FindAllStringSubmatch(markup, -1) {
		decl := m[1]
		if !strings.Contains(decl, "font-family") {
			continue
		}
		// A font stack that a parser kept in full ends in a generic family. One that was truncated by a
		// stray double quote stops mid-stack.
		if !strings.Contains(decl, "serif") && !strings.Contains(decl, "sans-serif") &&
			!strings.Contains(decl, "monospace") {
			bad = append(bad, "font stack truncated: "+decl)
			continue
		}
		// And the declarations that follow it must have survived. `color` is the one that matters: losing
		// it renders dark text on a dark card, in every client, while every part is present.
		if !strings.Contains(decl, "color:") {
			bad = append(bad, "declarations after the font stack were lost: "+decl)
		}
	}
	return bad
}

// TestEveryStyleAttributeSurvivesParsing.
//
// # 🔴 The bug this exists for
//
// `font-family:Georgia, "Times New Roman", serif` inside a style attribute. The inner double quote ends
// the attribute; a parser keeps `…font-family:Georgia, ` and discards font-size, line-height and colour.
// Every text cell lost its colour and the mail shipped as dark text on a dark card, unreadable in every
// client.
//
// ⚠️ The test that was supposed to catch it asked whether "#ffffff" appeared in the string. It did —
// inside a fragment no parser applies. A substring assertion cannot see broken markup.
func TestEveryStyleAttributeSurvivesParsing(t *testing.T) {
	links, err := NewLinks("https://console.example.test")
	if err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]Message{
		"invitation":   Invitation("someone@example.test", "Acme", "boss@acme.test", links.Invitation("tok"), InvitationLifetimeForTest),
		"reset":        ResetPassword("someone@example.test", "Acme", links.Reset("tok"), time.Hour),
		"verification": VerifyEmail("someone@example.test", "Acme", links.Verify("tok"), 24*time.Hour),
	} {
		for _, problem := range brokenStyles(m.HTML) {
			t.Errorf("%s mail: %s", name, problem)
		}
	}
}

// TestTheStyleFenceCatchesTheBugThatShipped.
//
// 🔴 A fence nobody has watched fail is a fence of unknown polarity. This runs the detector against the
// exact broken markup, so a future "simplification" of `brokenStyles` that makes it always pass is
// caught here rather than in a customer's inbox.
func TestTheStyleFenceCatchesTheBugThatShipped(t *testing.T) {
	broken := `<td style="padding:16px;font-family:Georgia, "Times New Roman", serif;color:#ffffff;">hi</td>`
	if len(brokenStyles(broken)) == 0 {
		t.Fatal("the detector passed the markup that shipped unreadable; it is not detecting anything")
	}
	fixed := `<td style="padding:16px;font-family:Georgia, 'Times New Roman', serif;color:#ffffff;">hi</td>`
	if bad := brokenStyles(fixed); len(bad) != 0 {
		t.Fatalf("the detector rejects correct markup: %v", bad)
	}
}

// InvitationLifetimeForTest keeps this file independent of the auth package's constants.
const InvitationLifetimeForTest = 7 * 24 * time.Hour

// TestMailCarriesNoRemoteResource.
//
// 🔴 A hosted logo in a password-reset mail is a read receipt: whoever serves it learns when the
// recipient opened it and from where. The only URL in these messages is the one the person is meant to
// follow, deliberately.
func TestMailCarriesNoRemoteResource(t *testing.T) {
	links, _ := NewLinks("https://console.example.test")
	action := links.Reset("tok")
	m := ResetPassword("someone@example.test", "Acme", action, time.Hour)
	for _, forbidden := range []string{"<img", "src=", "background-image", "url(", "@import", "<link"} {
		if strings.Contains(strings.ToLower(m.HTML), forbidden) {
			t.Errorf("the mail contains %q, which fetches something when it is opened", forbidden)
		}
	}
	// Every remaining URL must be the action link itself.
	for _, u := range regexp.MustCompile(`https?://[^\s"'<>]+`).FindAllString(m.HTML, -1) {
		if u != action {
			t.Errorf("the mail references %q, which is not the link it is asking the reader to follow", u)
		}
	}
}

// TestBothPartsSayTheSameThing.
//
// 🔴 multipart/alternative asserts its parts are renderings of one message and the client shows exactly
// one of them, so a difference between them is invisible to whoever wrote it. This is why both are built
// from one struct — and this asserts they still are.
func TestBothPartsSayTheSameThing(t *testing.T) {
	links, _ := NewLinks("https://console.example.test")
	link := links.Invitation("the-token")
	m := Invitation("someone@example.test", "Acme", "boss@acme.test", link, InvitationLifetimeForTest)
	for _, part := range []struct{ name, body string }{{"text", m.Text}, {"html", m.HTML}} {
		if !strings.Contains(part.body, link) {
			t.Errorf("the %s part does not contain the link", part.name)
		}
		if !strings.Contains(part.body, "7 days") {
			t.Errorf("the %s part does not state the expiry, so the two parts could disagree about it",
				part.name)
		}
		if !strings.Contains(part.body, "Acme") {
			t.Errorf("the %s part does not name the organization", part.name)
		}
	}
}

// TestTheTextPartComesFirst.
//
// 🔴 multipart/alternative is ordered least-preferred to most-preferred and clients show the LAST part
// they understand. HTML first shows plain text everywhere, which reads as the styling having failed —
// and survives review, because both parts are present and individually correct.
func TestTheTextPartComesFirst(t *testing.T) {
	body, err := Compose("support@heros-agent.space", Message{
		To: "someone@example.test", Subject: "Hello", Text: "plain", HTML: "<p>rich</p>",
	})
	if err != nil {
		t.Fatal(err)
	}
	plain := strings.Index(body, "text/plain")
	rich := strings.Index(body, "text/html")
	if plain < 0 || rich < 0 {
		t.Fatal("the message is not multipart/alternative")
	}
	if plain > rich {
		t.Error("the HTML part comes first, so clients will show the plain-text one")
	}
}

// TestAHeaderCannotBeInjectedThroughAnAddressOrSubject.
//
// 🔴 A newline in a recipient does not corrupt the mail, it EXTENDS it:
// "victim@example.com\nBcc: attacker@evil" silently copies every invitation somewhere else.
func TestAHeaderCannotBeInjectedThroughAnAddressOrSubject(t *testing.T) {
	for name, m := range map[string]Message{
		"newline in recipient": {To: "victim@example.test\nBcc: attacker@evil.test", Subject: "hi"},
		"return in recipient":  {To: "victim@example.test\rBcc: attacker@evil.test", Subject: "hi"},
		"newline in subject":   {To: "victim@example.test", Subject: "hi\nBcc: attacker@evil.test"},
		"not an address":       {To: "not-an-address", Subject: "hi"},
	} {
		if _, err := Compose("support@heros-agent.space", m); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := Compose("support@heros-agent.space",
		Message{To: "Someone <someone@example.test>", Subject: "hi", Text: "x", HTML: "x"}); err != nil {
		t.Errorf("a perfectly ordinary address was refused: %v", err)
	}
}

// TestALinkOriginIsConfiguredAndNotGuessed.
//
// # 🔴 The attack this closes
//
// The natural implementation of a reset link reads the request's Host header. "I forgot my password" is
// a request an ATTACKER can make, and they choose its headers: `Host: evil.example` produces a real
// token for the victim's real account, in a mail from the real product, pointing at the attacker's
// server. There is no field on Links that a request could reach, and this asserts the constructor
// refuses everything except a bare origin.
func TestALinkOriginIsConfiguredAndNotGuessed(t *testing.T) {
	for name, base := range map[string]string{
		"empty":        "",
		"no scheme":    "console.example.test",
		"wrong scheme": "javascript:alert(1)",
		"with a path":  "https://console.example.test/app",
		"with a query": "https://console.example.test?next=/x",
		"no host":      "https:///",
	} {
		if _, err := NewLinks(base); err == nil {
			t.Errorf("%s (%q) was accepted as a public origin", name, base)
		}
	}
	l, err := NewLinks("https://console.example.test/")
	if err != nil {
		t.Fatalf("a trailing slash was refused: %v", err)
	}
	if got := l.Invitation("tok en&x"); !strings.HasPrefix(got, "https://console.example.test/?invite=") {
		t.Errorf("unexpected link shape: %s", got)
	}
	// And the token is escaped rather than concatenated, or a token containing an ampersand would split
	// into two parameters and arrive truncated.
	if strings.Contains(l.Invitation("a&b=c"), "a&b=c") {
		t.Error("the token is not URL-encoded")
	}
}

// TestHalfConfiguredSmtpRefusesToStart.
//
// # 🔴 The failure this prevents
//
// This product has run for days with a mailer that reported itself configured and sent nothing: host
// set, username and password empty, health endpoint green, and a proof script passing on a machine that
// was not where the product runs. Mail was not degraded, it was absent, and every signal said otherwise.
func TestHalfConfiguredSmtpRefusesToStart(t *testing.T) {
	clear := func(t *testing.T) {
		for _, k := range []string{"HEROS_SMTP_HOST", "HEROS_SMTP_PORT", "HEROS_SMTP_USERNAME",
			"HEROS_SMTP_PASSWORD", "HEROS_MAIL_FROM", "HEROS_MAIL_MODE"} {
			t.Setenv(k, "")
		}
	}

	t.Run("a missing password is not a working relay", func(t *testing.T) {
		clear(t)
		t.Setenv("HEROS_SMTP_HOST", "mail.example.test")
		t.Setenv("HEROS_SMTP_USERNAME", "support@heros-agent.space")
		t.Setenv("HEROS_MAIL_FROM", "support@heros-agent.space")
		_, err := FromEnv()
		if err == nil {
			t.Fatal("a relay with no password was accepted; it would deliver nothing and say nothing")
		}
		if !strings.Contains(err.Error(), "HEROS_SMTP_PASSWORD") {
			t.Errorf("the error does not name the variable that is missing: %v", err)
		}
	})

	t.Run("nothing set is not silently no-mail", func(t *testing.T) {
		clear(t)
		if _, err := FromEnv(); err == nil {
			t.Fatal("an unconfigured deployment started without saying what it wants instead")
		}
	})

	t.Run("the two no-mail modes are explicit", func(t *testing.T) {
		clear(t)
		t.Setenv("HEROS_MAIL_MODE", "off")
		m, err := FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := m.(Unconfigured); !ok {
			t.Fatalf("HEROS_MAIL_MODE=off built a %T", m)
		}
		if err := m.Send(t.Context(), Message{To: "a@b.test"}); err == nil {
			t.Error("the no-mail mailer accepted a message; a discarded mail is worse than a refused one")
		}
		t.Setenv("HEROS_MAIL_MODE", "log")
		if m, err = FromEnv(); err != nil {
			t.Fatal(err)
		}
		if _, ok := m.(LogMailer); !ok {
			t.Fatalf("HEROS_MAIL_MODE=log built a %T", m)
		}
	})

	t.Run("a complete configuration works", func(t *testing.T) {
		clear(t)
		t.Setenv("HEROS_SMTP_HOST", "mail.example.test")
		t.Setenv("HEROS_SMTP_USERNAME", "support@heros-agent.space")
		t.Setenv("HEROS_SMTP_PASSWORD", "secret")
		t.Setenv("HEROS_MAIL_FROM", "support@heros-agent.space")
		m, err := FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		s, ok := m.(*SMTP)
		if !ok {
			t.Fatalf("built a %T", m)
		}
		if s.Port != 587 {
			t.Errorf("default port is %d, not 587", s.Port)
		}
		// 🔴 The description must never carry the password. It is printed at startup, into a log.
		if strings.Contains(s.Describe(), "secret") {
			t.Error("the startup banner prints the mail password")
		}
	})

	t.Run("a from address that is not an address is refused", func(t *testing.T) {
		clear(t)
		t.Setenv("HEROS_SMTP_HOST", "mail.example.test")
		t.Setenv("HEROS_SMTP_USERNAME", "u")
		t.Setenv("HEROS_SMTP_PASSWORD", "p")
		t.Setenv("HEROS_MAIL_FROM", "not an address")
		if _, err := FromEnv(); err == nil {
			t.Fatal("a malformed From was accepted; every send would be rejected by the relay")
		}
	})
}

// TestALineOfDotsCannotTruncateTheMessage.
//
// 🔴 A lone "." at the start of a line ends the SMTP DATA command. An organization name or a link that
// produced one would silently truncate the mail at that point.
func TestALineOfDotsCannotTruncateTheMessage(t *testing.T) {
	body, err := Compose("support@heros-agent.space", Message{
		To: "someone@example.test", Subject: "hi", Text: "line one\n.\nline three", HTML: "<p>x</p>",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(body, "\r\n") {
		if line == "." {
			t.Fatal("the encoded message contains a bare dot on its own line, which ends DATA early")
		}
	}
}
