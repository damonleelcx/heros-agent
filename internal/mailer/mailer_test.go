package mailer

import (
	"bytes"
	"context"
	"log"
	"regexp"
	"strings"
	"testing"
	"time"
)

// 🔴 The fence this package exists for: the unconfigured path must never return success without recording the
// message. The failure it prevents is silent and total — a person waits for a reset that was discarded, and
// nothing anywhere says so.
func TestUnconfiguredNeverDiscards(t *testing.T) {
	var logs bytes.Buffer
	m, err := New(Config{}, log.New(&logs, "", 0))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m.Configured() {
		t.Fatal("a deployment with no SMTP configuration reported itself as able to send")
	}
	msg := ResetPassword("https://console.example", "https://console.example/reset-password?t=SECRET-TOKEN", time.Hour)
	msg.To = "priya@example.com"
	if err := m.Send(context.Background(), msg); err != nil {
		t.Fatalf("the fallback must accept the message: %v", err)
	}

	op, ok := m.(*OperatorMailer)
	if !ok {
		t.Fatalf("an unconfigured deployment got %T, not the operator fallback", m)
	}
	held := op.Undelivered()
	if len(held) != 1 {
		t.Fatalf("the message was not recorded: %d held", len(held))
	}
	if !strings.Contains(held[0].Body, "SECRET-TOKEN") {
		t.Fatal("the held record does not contain the link — the operator is the delivery mechanism on this " +
			"path, so a record without the link is a log line pretending to be a mailbox")
	}
	if held[0].To != "priya@example.com" || held[0].Purpose != PurposeResetPassword {
		t.Fatalf("the record does not identify the message: %+v", held[0])
	}

	out := logs.String()
	if !strings.Contains(out, "WARN") {
		t.Fatalf("an undelivered message did not log at WARN:\n%s", out)
	}
	if !strings.Contains(out, "priya@example.com") || !strings.Contains(out, string(PurposeResetPassword)) {
		t.Fatalf("the warning names neither the recipient nor the purpose:\n%s", out)
	}
	// 🔴 And the log must NOT carry the link: these lines ship to an aggregator with a different retention
	// policy and a wider audience than the operator surface.
	if strings.Contains(out, "SECRET-TOKEN") {
		t.Fatal("the WARN line contains the link token — that copies a live credential into the log pipeline")
	}
}

// The record is bounded, and the eviction is loud. A silent eviction here would be the same defect one layer
// down: messages disappearing with nothing saying so.
func TestOperatorRecordEvictsLoudly(t *testing.T) {
	var logs bytes.Buffer
	op := NewOperatorMailer(log.New(&logs, "", 0))
	op.max = 3
	for i := 0; i < 5; i++ {
		if err := op.Send(context.Background(), Message{
			To: "a@example.com", Subject: "s", TextBody: "b", Purpose: PurposeVerifyEmail,
		}); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	if got := len(op.Undelivered()); got != 3 {
		t.Fatalf("the record holds %d, want the bound of 3", got)
	}
	if !strings.Contains(logs.String(), "dropping") {
		t.Fatal("messages were evicted from the operator record with no warning")
	}
}

// 🔴 Clear-text delivery to a real host is refused at construction, not at send. "The operator asked for it"
// is not a reason — the operator asking for it is the mistake, and a reset link in the clear is exactly the
// credential this phase exists to protect.
func TestClearTextIsRefusedForRealHosts(t *testing.T) {
	if _, err := New(Config{Host: "smtp.example.com", From: "a@example.com", TLS: TLSNone}, nil); err == nil {
		t.Fatal("clear-text SMTP to a remote host was accepted")
	}
	// Loopback is a local relay: there is no network to be on.
	if _, err := New(Config{Host: "127.0.0.1", From: "a@example.com", TLS: TLSNone, Port: 1025}, nil); err != nil {
		t.Fatalf("clear-text to loopback should be allowed for a local relay: %v", err)
	}
	if _, err := New(Config{Host: "smtp.example.com", From: "a@example.com", TLS: "sorta"}, nil); err == nil {
		t.Fatal("an unknown TLS mode was accepted")
	}
}

// Header injection: a newline in a recipient or subject turns one message into two, with headers the caller
// never wrote. The address reaching here came from a person's own sign-up form.
func TestHeaderInjectionIsRefused(t *testing.T) {
	op := NewOperatorMailer(log.New(&bytes.Buffer{}, "", 0))
	for _, bad := range []Message{
		{To: "a@example.com\r\nBcc: attacker@example.net", Subject: "s", TextBody: "b"},
		{To: "a@example.com", Subject: "s\nBcc: attacker@example.net", TextBody: "b"},
		{To: "", Subject: "s", TextBody: "b"},
		{To: "a@example.com", Subject: "", TextBody: "b"},
	} {
		if err := op.Send(context.Background(), bad); err == nil {
			t.Errorf("a malformed message was accepted: %+v", bad)
		}
	}
}

// A configured deployment reports itself configured, and picks the conventional port for its TLS mode.
func TestConfiguredSelectsSMTP(t *testing.T) {
	m, err := New(Config{Host: "smtp.example.com", From: "noreply@example.com"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !m.Configured() {
		t.Fatal("a configured deployment reported itself unable to send")
	}
	s := m.(*SMTPMailer)
	if s.cfg.Port != 587 || s.cfg.TLS != TLSStartTLS {
		t.Fatalf("defaults are %d/%s, want 587/starttls", s.cfg.Port, s.cfg.TLS)
	}
	if m.From() != "noreply@example.com" {
		t.Fatalf("From() = %q", m.From())
	}
	implicit, err := New(Config{Host: "smtp.example.com", From: "a@example.com", TLS: TLSImplicit}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if implicit.(*SMTPMailer).cfg.Port != 465 {
		t.Fatalf("implicit TLS defaulted to port %d, want 465", implicit.(*SMTPMailer).cfg.Port)
	}
}

// A message body carries a link and nothing else that could be presented. This is the wire-level statement of
// "one link, one purpose, no other secret".
func TestBodiesCarryOnlyTheLink(t *testing.T) {
	for _, m := range []Message{
		VerifyEmail("https://console.example", "https://console.example/verify-email?t=T", 24*time.Hour),
		ResetPassword("https://console.example", "https://console.example/reset-password?t=T", time.Hour),
		SignupAttempt("https://console.example", "https://console.example/forgot-password"),
		OwnerBootstrap("https://console.example", "https://console.example/reset-password?t=T", 24*time.Hour),
	} {
		if strings.Count(m.TextBody, "http") != strings.Count(m.TextBody, "https://console.example") {
			t.Errorf("%q links somewhere other than the console:\n%s", m.Subject, m.TextBody)
		}
		// Banned SHAPES, not words. The reset body legitimately explains that sessions end — what must never
		// appear is something presentable: a minted credential (`heros_…`), a literal password, or a bearer
		// token. An earlier version of this test banned the word "session" and failed on that sentence, which
		// would have deleted the most important line in the message to make a test pass.
		for _, banned := range []string{"heros_", "bearer ", "authorization:", "x-api-key"} {
			if strings.Contains(strings.ToLower(m.TextBody), banned) {
				t.Errorf("%q body contains something presentable: %q", m.Subject, banned)
			}
		}
		if strings.TrimSpace(m.Subject) == "" || strings.TrimSpace(m.TextBody) == "" {
			t.Errorf("empty message: %+v", m)
		}
	}
}

// The rendered RFC 5322 message is plain text and nothing else.
func TestRenderIsPlainText(t *testing.T) {
	got := render("noreply@example.com", Message{To: "a@example.com", Subject: "Hi", TextBody: "line one\nline two"})
	for _, want := range []string{
		"From: noreply@example.com\r\n",
		"To: a@example.com\r\n",
		"Subject: Hi\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n",
		"line one\r\nline two",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered message is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(strings.ToLower(got), "text/html") {
		t.Error("the rendered message offers an HTML part")
	}
}

// ── the styled rendering (P28) ───────────────────────────────────────────────────────────────────────

// 🔴 The two parts must SAY the same thing. `multipart/alternative` asserts exactly that, and the client
// picks one — so a divergence is invisible to whoever wrote it and definitive for whoever reads it.
func TestBothPartsCarryTheSameLinkAndExpiry(t *testing.T) {
	for _, m := range []Message{
		VerifyEmail("https://console.example", "https://console.example/verify-email?t=TOKEN", 24*time.Hour),
		ResetPassword("https://console.example", "https://console.example/reset-password?t=TOKEN", time.Hour),
		OwnerBootstrap("https://console.example", "https://console.example/reset-password?t=TOKEN", 24*time.Hour),
	} {
		if m.HTMLBody == "" {
			t.Fatalf("%q has no HTML part", m.Subject)
		}
		if !strings.Contains(m.TextBody, "?t=TOKEN") || !strings.Contains(m.HTMLBody, "?t=TOKEN") {
			t.Errorf("%q: the link is not in both parts", m.Subject)
		}
		// The expiry is the value most likely to drift, because it is the one written twice in prose.
		for _, ttl := range []string{"1 hour", "24 hours"} {
			if strings.Contains(m.TextBody, ttl) != strings.Contains(m.HTMLBody, ttl) {
				t.Errorf("%q: %q appears in one part and not the other", m.Subject, ttl)
			}
		}
	}
}

// 🔴 No remote resource of any kind. A hosted logo is a read receipt on a password-reset email.
func TestTheStyledPartLoadsNothingRemote(t *testing.T) {
	m := ResetPassword("https://console.example", "https://console.example/reset-password?t=T", time.Hour)
	for _, banned := range []string{"<img", "background-image", "url(", "@import", "<script", "<link"} {
		if strings.Contains(strings.ToLower(m.HTMLBody), banned) {
			t.Errorf("the HTML part contains %q — it must reference no remote resource and run nothing", banned)
		}
	}
	// Every link in the body is the one we minted.
	for _, href := range regexpAll(m.HTMLBody, `href="([^"]+)"`) {
		if !strings.HasPrefix(href, "https://console.example") {
			t.Errorf("the HTML part links to %q, which is not this deployment's console", href)
		}
	}
}

// The palette is the console's, by value. A drift here is a mail that looks like a different product.
func TestTheStyledPartUsesTheConsolePalette(t *testing.T) {
	m := VerifyEmail("https://console.example", "https://console.example/verify-email?t=T", 24*time.Hour)
	for _, token := range []string{"#080d17", "#0a1020", "#2ecfa8", "#051a14"} {
		if !strings.Contains(m.HTMLBody, token) {
			t.Errorf("the HTML part does not use the console token %s", token)
		}
	}
	if !strings.Contains(m.HTMLBody, "HEROS") {
		t.Error("the HTML part carries no wordmark")
	}
	// Dark mode: without these a client inverts the palette and produces an unreadable button.
	for _, meta := range []string{`name="color-scheme"`, `name="supported-color-schemes"`} {
		if !strings.Contains(m.HTMLBody, meta) {
			t.Errorf("the HTML part does not declare %s, so a dark-mode client will invert it", meta)
		}
	}
}

// 🔴 Ordering: text first, HTML second. `multipart/alternative` is least- to most-preferred, so reversing
// them makes every rich client show plain text — which reads as the styling having silently failed.
func TestRenderIsMultipartWithTextFirst(t *testing.T) {
	m := ResetPassword("https://console.example", "https://console.example/reset-password?t=T", time.Hour)
	m.To = "a@example.com"
	got := render("noreply@example.com", m)

	if !strings.Contains(got, "Content-Type: multipart/alternative; boundary=") {
		t.Fatalf("not a multipart message:\n%s", got[:400])
	}
	textAt := strings.Index(got, "Content-Type: text/plain")
	htmlAt := strings.Index(got, "Content-Type: text/html")
	if textAt < 0 || htmlAt < 0 {
		t.Fatal("a part is missing")
	}
	if textAt > htmlAt {
		t.Error("the HTML part precedes the text part, so a client that prefers the LAST understood part " +
			"shows plain text")
	}
	if !strings.Contains(got, "Auto-Submitted: auto-generated") {
		t.Error("the message does not declare itself auto-generated")
	}
	// The boundary must not occur inside a body, or the message splits where it should not.
	boundary := got[strings.Index(got, "boundary=\"")+len("boundary=\"") : strings.Index(got, "\"\r\n\r\n")]
	if strings.Contains(m.TextBody, boundary) || strings.Contains(m.HTMLBody, boundary) {
		t.Error("the boundary occurs inside a body")
	}
}

// A message with no HTML part stays a plain-text message — the shape a caller building one by hand gets.
func TestRenderStaysPlainWhenThereIsNoHTML(t *testing.T) {
	got := render("noreply@example.com", Message{To: "a@example.com", Subject: "Hi", TextBody: "one\ntwo"})
	if strings.Contains(got, "multipart") || strings.Contains(got, "text/html") {
		t.Errorf("a text-only message was rendered as multipart:\n%s", got)
	}
	if !strings.Contains(got, "Content-Type: text/plain; charset=utf-8\r\n") {
		t.Errorf("the plain-text content type is missing:\n%s", got)
	}
}

func regexpAll(s, pattern string) []string {
	re := regexp.MustCompile(pattern)
	var out []string
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// 🔴 TestEveryStyleAttributeSurvivesParsing is the fence for the bug that shipped an unreadable email.
//
// The font stacks were written the CSS-canonical way — `Georgia, "Times New Roman", serif` — inside
// `style="…"`. The first inner double quote TERMINATES the attribute, so an HTML parser kept
// `padding:…;font-family:Georgia, ` and discarded the rest: font-size, line-height and `color`. Every text
// cell lost its colour, and the mail arrived as dark text on a dark card.
//
// ⚠️ The existing palette test passed throughout, because it asked whether `#ffffff` appeared in the string.
// It did — in a fragment no parser would ever apply. **A substring assertion cannot see broken markup.**
//
// This reads each style attribute the way a parser does (up to the first closing quote) and requires it to
// be complete: anything declaring a font must still carry the end of its stack AND a colour.
func TestEveryStyleAttributeSurvivesParsing(t *testing.T) {
	messages := []Message{
		VerifyEmail("https://console.example", "https://console.example/verify-email?t=T", 24*time.Hour),
		ResetPassword("https://console.example", "https://console.example/reset-password?t=T", time.Hour),
		SignupAttempt("https://console.example", "https://console.example/forgot-password"),
		OwnerBootstrap("https://console.example", "https://console.example/reset-password?t=T", 24*time.Hour),
	}
	// Exactly what a parser sees: everything between `style="` and the NEXT double quote.
	attr := regexp.MustCompile(`style="([^"]*)"`)

	for _, m := range messages {
		found := attr.FindAllStringSubmatch(m.HTMLBody, -1)
		if len(found) < 5 {
			t.Fatalf("%q: only %d style attribute(s) parsed — the scan is not reading the markup",
				m.Subject, len(found))
		}
		for _, match := range found {
			decls := match[1]
			if !strings.Contains(decls, "font-family") {
				continue
			}
			// A truncated stack ends mid-list; a complete one always reaches its generic family.
			if !strings.Contains(decls, "serif") {
				t.Errorf("%q: a style attribute declares a font and never reaches its generic family, so it "+
					"was CUT SHORT by a quote inside the stack:\n  %s", m.Subject, decls)
				continue
			}
			// Every cell that sets a font in these templates also sets a colour. If the colour is gone, the
			// attribute was truncated between the two — which is precisely how the text went invisible.
			if !strings.Contains(decls, "color:") {
				t.Errorf("%q: a style attribute sets a font and no colour, which on a dark card renders as "+
					"invisible text:\n  %s", m.Subject, decls)
			}
		}
	}
}

// The detector must be able to fail, or it is decoration. This runs it against the exact markup that
// shipped, rather than trusting that the fixed version passing means anything.
func TestTheStyleFenceCatchesTheBugThatShipped(t *testing.T) {
	broken := `<td style="padding:32px;font-family:Georgia, "Times New Roman", serif;color:#ffffff;">HEROS</td>`
	attr := regexp.MustCompile(`style="([^"]*)"`)
	match := attr.FindStringSubmatch(broken)
	if match == nil {
		t.Fatal("the scan found no style attribute in the broken fixture")
	}
	if strings.Contains(match[1], "serif") || strings.Contains(match[1], "color:") {
		t.Fatalf("the fence would not have caught the shipped bug: a parser keeps %q, which the check "+
			"treats as complete", match[1])
	}
	// And the palette check that DID pass on this markup, demonstrating why it was not enough.
	if !strings.Contains(broken, "#ffffff") {
		t.Fatal("fixture is wrong")
	}
}
