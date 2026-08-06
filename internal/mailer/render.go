package mailer

import (
	"fmt"
	"html"
	"strings"
)

// render.go turns one piece of structured content into BOTH renderings of a message.
//
// # 🔴 One source, two renderings, because the alternative is a lie in one of them
//
// A `multipart/alternative` message asserts that its parts say the same thing — the client picks one and
// the reader never sees the other. Writing the text and the HTML separately is how a reset mail ends up
// promising a one-hour expiry in one part and twenty-four in the other, and nobody notices because nobody
// reads both. So each message is declared once, as `content`, and both parts are generated from it.
//
// # Why the styling is inline, table-based and image-free
//
// Not nostalgia — those are the constraints of the medium. Mail clients strip `<style>` blocks, ignore
// most modern layout, and rewrite what they keep. And 🔴 there is no remote image anywhere in here: the
// wordmark is text. A logo hosted on our origin would report to us the moment somebody opened a
// password-reset email, from which IP, and that is a tracking pixel whether or not it was meant as one.
//
// The palette is the console's own, copied by value from `web/console/src/app/tokens.customer.css`:
//
//	--marketing-canvas     #080d17
//	--marketing-panel      #0a1020
//	--marketing-ink        #ffffff
//	--marketing-accent     #2ecfa8
//	--marketing-accent-ink #051a14
//
// ⚠️ Copied, not shared, and that is a real seam: a token change in the console does not reach here. The
// console's token scanner cannot see Go, so nothing enforces the agreement. It is written down because
// the honest options were "duplicate and say so" or "build a token pipeline into a backend service", and
// the second is a lot of machinery for five hex values that change approximately never.

const (
	colorCanvas    = "#080d17"
	colorPanel     = "#0a1020"
	colorInk       = "#ffffff"
	colorAccent    = "#2ecfa8"
	colorAccentInk = "#051a14"
	// colorMuted is the console's `text-marketing-ink/50` flattened against the panel. Email cannot do
	// alpha compositing reliably, so the blend is done here rather than hoped for.
	colorMuted = "#8b93a7"
	// colorBody is the primary reading colour — brighter than muted, dimmer than the headings. The console
	// gives its body copy `text-marketing-ink/50` against a panel; at email sizes that is too faint, so
	// this is the same relationship one step brighter.
	colorBody = "#c8cfdc"
	// colorHairline is `border-marketing-ink/10` flattened the same way.
	colorHairline = "#1c2536"
	// colorCalloutBg is the accent at low opacity, flattened against the panel — the console's warn banner
	// shape, in the one place an email needs to stop somebody before they act.
	colorCalloutBg = "#0d1a22"

	// fontSans and fontDisplay mirror the console's stacks with the WEBFONT REMOVED. Newsreader and Jakarta
	// are loaded by the console over the network; an email cannot rely on a webfont, and @font-face in mail
	// is stripped by most clients. What is left is each stack's own fallback — Georgia for the display
	// wordmark, the system sans for everything else — which is what the console falls back to anyway.
	//
	// 🔴 SINGLE quotes around the multi-word family names, and this is not a style preference.
	//
	// These stacks go inside `style="…"`, which is delimited by DOUBLE quotes. Written the CSS-canonical
	// way — `Georgia, "Times New Roman", serif` — the first inner double quote TERMINATES THE ATTRIBUTE, and
	// an HTML parser discards everything after it: font-size, line-height, and `color`. The rendered mail
	// was dark text on a dark card, unreadable, in every client.
	//
	// ⚠️ It shipped because the tests asserted the palette was PRESENT IN THE STRING — and it was, sitting
	// in a fragment the parser throws away. A substring assertion cannot see broken markup, which is why
	// `TestEveryStyleAttributeSurvivesParsing` now checks what a parser would keep rather than what the
	// bytes contain.
	fontSans    = `-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif`
	fontDisplay = `Georgia, 'Times New Roman', serif`
)

// content is one message, before it is rendered into either form.
type content struct {
	subject string
	purpose Purpose
	// heading is the one-line answer to "what is this". It is NOT the subject repeated: the subject is read
	// in a list, the heading is read after opening, and repeating it wastes the first line of the message.
	heading string
	// intro are the paragraphs before the action.
	intro []string
	// action is the button, when there is one. Empty label means the message has no action — which is the
	// signup-attempt notice, and it deliberately has none.
	actionLabel string
	actionURL   string
	// callout is the one thing a reader must not skim past — rendered as a bordered panel above the note,
	// the same shape the console uses for a consequence stated before the button. Empty for messages that
	// have nothing that severe to say, which is most of them.
	callout string
	// note is what appears below the action in muted text: the expiry, the "if this was not you". Rendered
	// last in both parts.
	note []string
}

// build renders both parts and returns the Message.
func (c content) build() Message {
	return Message{
		Subject:  c.subject,
		Purpose:  c.purpose,
		TextBody: c.text(),
		HTMLBody: c.html(),
	}
}

// text is the plain-text rendering — the part that is also the fallback, and the part a screen reader or a
// terminal mail client gets. It is written to be readable on its own rather than as a degraded copy.
func (c content) text() string {
	var b strings.Builder
	b.WriteString(c.heading)
	b.WriteString("\n\n")
	for _, p := range c.intro {
		b.WriteString(p)
		b.WriteString("\n\n")
	}
	if c.actionURL != "" {
		b.WriteString(c.actionURL)
		b.WriteString("\n\n")
	}
	if c.callout != "" {
		b.WriteString(c.callout)
		b.WriteString("\n\n")
	}
	for _, n := range c.note {
		b.WriteString(n)
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// html is the styled rendering, matching the console's sign-in surface.
func (c content) html() string {
	var b strings.Builder

	// 🔴 `color-scheme` and `supported-color-schemes` tell a client this message is ALREADY dark. Without
	// them, Gmail and Outlook in dark mode "helpfully" invert the palette — and inverting a composition
	// built on a dark canvas with a bright accent produces a light card with an unreadable button, which
	// is worse than either theme.
	b.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width,initial-scale=1">`)
	b.WriteString(`<meta name="color-scheme" content="dark"><meta name="supported-color-schemes" content="dark">`)
	b.WriteString(`</head>`)
	fmt.Fprintf(&b, `<body style="margin:0;padding:0;background-color:%s;">`, colorCanvas)

	// The outer table is the background. A `<body>` background alone is dropped by several clients, which
	// is how a dark-designed email arrives as dark text on white. `bgcolor` as well as the style, because
	// Outlook's Word engine reads the attribute and ignores the property.
	fmt.Fprintf(&b, `<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" `+
		`bgcolor="%s" style="background-color:%s;"><tr><td align="center" style="padding:40px 12px;">`,
		colorCanvas, colorCanvas)

	// The card. 600px is the width every client renders without horizontal scroll.
	fmt.Fprintf(&b, `<table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" `+
		`bgcolor="%s" style="width:100%%;max-width:600px;background-color:%s;border:1px solid %s;`+
		`border-radius:16px;"><tr><td style="padding:40px;">`, colorPanel, colorPanel, colorHairline)

	// Wordmark — the console's `font-display uppercase tracking-[0.15em] font-light`, in the fallback serif.
	// Text, never an image: see the file header.
	fmt.Fprintf(&b, `<div style="font-family:%s;font-size:22px;font-weight:400;letter-spacing:0.18em;`+
		`color:%s;">HEROS</div>`, fontDisplay, colorInk)

	fmt.Fprintf(&b, `<div style="margin-top:28px;font-family:%s;font-size:24px;line-height:1.3;color:%s;">`+
		`%s</div>`, fontDisplay, colorInk, html.EscapeString(c.heading))

	for _, para := range c.intro {
		fmt.Fprintf(&b, `<div style="margin-top:16px;font-family:%s;font-size:15px;line-height:1.65;`+
			`color:%s;">%s</div>`, fontSans, colorBody, html.EscapeString(para))
	}

	if c.actionURL != "" && c.actionLabel != "" {
		// A "bulletproof" button: a table cell with a background colour and a link filling it. `<button>`
		// does nothing in mail, and a styled `<a>` alone loses its background in Outlook.
		fmt.Fprintf(&b, `<table role="presentation" cellpadding="0" cellspacing="0" border="0" `+
			`style="margin-top:32px;"><tr><td align="center" bgcolor="%s" `+
			`style="background-color:%s;border-radius:10px;">`+
			`<a href="%s" style="display:inline-block;padding:15px 30px;font-family:%s;font-size:15px;`+
			`font-weight:600;color:%s;text-decoration:none;">%s</a></td></tr></table>`,
			colorAccent, colorAccent, html.EscapeString(c.actionURL), fontSans, colorAccentInk,
			html.EscapeString(c.actionLabel))

		// 🔴 The raw URL, always, below the button. A client that strips the button, a forwarded message, a
		// reader who wants to see where a link goes before following it — all three need the address in
		// text, and the third is the one that matters on a security email.
		fmt.Fprintf(&b, `<div style="margin-top:20px;font-family:%s;font-size:12px;line-height:1.6;`+
			`color:%s;">Or paste this into your browser</div>`, fontSans, colorMuted)
		// The wrapper carries a colour of its own even though it holds only the anchor. Some clients strip
		// link colours; without this the fallback is the client's default, which on a dark card can be
		// unreadable. It also keeps `TestEveryStyleAttributeSurvivesParsing`'s rule uniform — a font with no
		// colour is the exact shape of the bug that shipped, and an exception is how a fence starts rotting.
		fmt.Fprintf(&b, `<div style="margin-top:6px;font-family:%s;font-size:12px;line-height:1.6;`+
			`color:%s;word-break:break-all;"><a href="%s" style="color:%s;text-decoration:underline;">`+
			`%s</a></div>`,
			fontSans, colorAccent, html.EscapeString(c.actionURL), colorAccent, html.EscapeString(c.actionURL))
	}

	if c.callout != "" {
		// The console's warn-banner shape: a tinted panel with an accent rule down its left edge, so the one
		// consequence a reader must not skim past does not read as another paragraph of small print.
		fmt.Fprintf(&b, `<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" `+
			`style="margin-top:32px;"><tr><td bgcolor="%s" style="background-color:%s;`+
			`border-left:3px solid %s;border-radius:8px;padding:16px 18px;font-family:%s;font-size:13px;`+
			`line-height:1.65;color:%s;">%s</td></tr></table>`,
			colorCalloutBg, colorCalloutBg, colorAccent, fontSans, colorBody, html.EscapeString(c.callout))
	}

	for _, n := range c.note {
		fmt.Fprintf(&b, `<div style="margin-top:18px;font-family:%s;font-size:13px;line-height:1.65;`+
			`color:%s;">%s</div>`, fontSans, colorMuted, html.EscapeString(n))
	}

	// Footer rule and the sender, matching the console's hairline.
	fmt.Fprintf(&b, `<div style="margin-top:36px;border-top:1px solid %s;padding-top:18px;font-family:%s;`+
		`font-size:11px;line-height:1.6;color:%s;">Sent by Heros. This message was triggered by an action `+
		`on your account — we do not send marketing to this address.</div>`,
		colorHairline, fontSans, colorMuted)

	b.WriteString(`</td></tr></table></td></tr></table></body></html>`)
	return b.String()
}
