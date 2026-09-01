// Package mailer sends the three mails this product depends on: an invitation, a password reset, and an
// address confirmation.
//
// # 🔴 Why partial configuration is a startup failure
//
// A previous deployment of this product ran for days with a mailer that reported itself configured and
// sent nothing: the host was set, the username and password were empty, the health endpoint was green,
// and a proof script passed on a build machine that was not where the product runs. Mail was not
// degraded, it was absent, and every signal said otherwise.
//
// So `FromEnv` refuses halves. Either every SMTP variable is present, or none is and the operator has
// said in so many words which no-mail behaviour they want. A server that will not start is a far better
// outcome than one that starts and silently drops every password reset.
package mailer

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

// ErrNotConfigured means no mail transport exists, so an operation that must send cannot proceed.
//
// 🔴 Returned rather than swallowed. Creating an invitation and failing to send it produces a person
// waiting for a mail that will never come, and an inviter who believes the job is done. The caller is
// told, and tells the customer.
var ErrNotConfigured = errors.New("mailer: no mail transport is configured")

// Message is one mail.
//
// Text and HTML are two renderings of the SAME message, sent as multipart/alternative. The client shows
// one of them, so they must say the same thing — which is why every template here produces both from one
// struct rather than being written twice.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Mailer sends mail.
type Mailer interface {
	Send(ctx context.Context, m Message) error
	// Describe says what this mailer is, for the startup banner. The operator should never have to guess
	// whether mail is real in this deployment.
	Describe() string
}

// ── configuration ────────────────────────────────────────────────────────────────────────────────

// Config is an SMTP relay.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

// FromEnv builds the mailer this deployment should use, or explains what is missing.
//
// HEROS_SMTP_HOST, _PORT, _USERNAME, _PASSWORD and HEROS_MAIL_FROM configure a relay. With none of them
// set, HEROS_MAIL_MODE decides what happens instead:
//
//	log  — write the whole mail, links included, to the server log. For development.
//	off  — refuse to send. Any operation needing mail fails with a message saying why.
//
// 🚫 There is no default mode when nothing is set. The operator states which one they mean, because the
// two differ in whether password-reset links end up in a log file, and that is not a question to answer
// by whichever branch happened to be written first.
func FromEnv() (Mailer, error) {
	host := strings.TrimSpace(os.Getenv("HEROS_SMTP_HOST"))
	user := strings.TrimSpace(os.Getenv("HEROS_SMTP_USERNAME"))
	pass := os.Getenv("HEROS_SMTP_PASSWORD")
	from := strings.TrimSpace(os.Getenv("HEROS_MAIL_FROM"))
	portRaw := strings.TrimSpace(os.Getenv("HEROS_SMTP_PORT"))

	anySet := host != "" || user != "" || pass != "" || from != "" || portRaw != ""
	if !anySet {
		switch strings.TrimSpace(os.Getenv("HEROS_MAIL_MODE")) {
		case "log":
			return LogMailer{}, nil
		case "off":
			return Unconfigured{}, nil
		default:
			return nil, fmt.Errorf("no mail transport is configured, and HEROS_MAIL_MODE does not say " +
				"what to do instead.\n" +
				"  For a real deployment set HEROS_SMTP_HOST, HEROS_SMTP_PORT, HEROS_SMTP_USERNAME, " +
				"HEROS_SMTP_PASSWORD and HEROS_MAIL_FROM.\n" +
				"  For development set HEROS_MAIL_MODE=log, which writes invitation and reset links to " +
				"this log instead of mailing them.\n" +
				"  To run with no mail at all set HEROS_MAIL_MODE=off; invitations and password resets " +
				"will be refused with an explanation")
		}
	}

	// 🔴 Every field, named individually. "SMTP is misconfigured" sends somebody to read source; naming
	// the empty variable ends the investigation.
	var missing []string
	for _, f := range []struct {
		name  string
		value string
	}{
		{"HEROS_SMTP_HOST", host},
		{"HEROS_SMTP_USERNAME", user},
		{"HEROS_SMTP_PASSWORD", pass},
		{"HEROS_MAIL_FROM", from},
	} {
		if strings.TrimSpace(f.value) == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("SMTP is half configured: %s %s not set.\n"+
			"  A relay with a missing credential accepts the connection and delivers nothing, which "+
			"looks identical to working until a customer cannot reset their password. Set the missing "+
			"variables, or unset all of them and choose HEROS_MAIL_MODE",
			strings.Join(missing, ", "), plural(len(missing)))
	}

	port := 587
	if portRaw != "" {
		n, err := strconv.Atoi(portRaw)
		if err != nil || n <= 0 || n > 65535 {
			return nil, fmt.Errorf("HEROS_SMTP_PORT is %q, which is not a port number", portRaw)
		}
		port = n
	}
	if _, err := mail.ParseAddress(from); err != nil {
		return nil, fmt.Errorf("HEROS_MAIL_FROM is %q, which is not an email address: %w", from, err)
	}
	return &SMTP{Config: Config{Host: host, Port: port, Username: user, Password: pass, From: from}}, nil
}

func plural(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// ── SMTP ─────────────────────────────────────────────────────────────────────────────────────────

// SMTP sends through a relay.
type SMTP struct {
	Config
	// Timeout bounds the whole conversation. Zero means 30 seconds. A relay that accepts a connection and
	// then stops talking would otherwise hold an HTTP handler open until the customer gives up.
	Timeout time.Duration
}

func (s *SMTP) Describe() string {
	return fmt.Sprintf("smtp %s:%d as %s, from %s", s.Host, s.Port, s.Username, s.From)
}

// Send delivers one message.
//
// # 🔴 Why the connection must be encrypted before the password is offered
//
// `smtp.PlainAuth` refuses to hand credentials to an unencrypted connection, which is the right default
// and is also the whole protection — so this code does not work around it. On 465 the socket is TLS from
// the first byte; on every other port STARTTLS is required, and a relay that does not offer it is an
// error rather than a fallback to cleartext. A fallback would mean the relay decides whether this
// product's mail credentials cross the network in the clear.
func (s *SMTP) Send(ctx context.Context, m Message) error {
	if err := validateHeaders(m); err != nil {
		return err
	}
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	var conn net.Conn
	var err error
	d := &net.Dialer{}
	if s.Port == 465 {
		conn, err = (&tls.Dialer{NetDialer: d, Config: &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}}).
			DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("mailer: connecting to %s: %w", addr, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return fmt.Errorf("mailer: smtp handshake with %s: %w", addr, err)
	}
	defer func() { _ = c.Quit() }()

	if s.Port != 465 {
		ok, _ := c.Extension("STARTTLS")
		if !ok {
			return fmt.Errorf("mailer: %s does not offer STARTTLS. Refusing to continue: the next step "+
				"sends this product's mail password, and an unencrypted relay would put it on the wire "+
				"in clear text", addr)
		}
		if err := c.StartTLS(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("mailer: starting TLS with %s: %w", addr, err)
		}
	}
	if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
		// 🔴 The error is wrapped without the password anywhere near it, and net/smtp does not include it.
		return fmt.Errorf("mailer: authenticating to %s as %s: %w", addr, s.Username, err)
	}
	if err := c.Mail(s.From); err != nil {
		return fmt.Errorf("mailer: MAIL FROM %s: %w", s.From, err)
	}
	if err := c.Rcpt(m.To); err != nil {
		return fmt.Errorf("mailer: RCPT TO %s: %w", m.To, err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mailer: DATA: %w", err)
	}
	body, err := Compose(s.From, m)
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(body)); err != nil {
		return fmt.Errorf("mailer: writing message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mailer: closing message: %w", err)
	}
	return nil
}

// ── the no-relay mailers ─────────────────────────────────────────────────────────────────────────

// LogMailer writes mail to the server log instead of sending it. For development.
//
// 🔴 It prints the link. That is the point — without it a developer with no relay cannot complete an
// invitation — and it is also why this is never the default: anybody who can read the log can take over
// any account. Choosing it takes HEROS_MAIL_MODE=log, and the daemon says so loudly at startup.
type LogMailer struct{}

func (LogMailer) Describe() string {
	return "log — mail is written to this log, links included. Development only."
}

func (LogMailer) Send(_ context.Context, m Message) error {
	log.Printf("MAIL (not sent — HEROS_MAIL_MODE=log)\n  to:      %s\n  subject: %s\n%s\n"+
		"  ^ anybody who can read this log can use any link above.", m.To, m.Subject, indent(m.Text))
	return nil
}

// Unconfigured refuses to send, and says so.
//
// The alternative — accepting and discarding — is what makes a broken deployment look like a working
// one. An invitation nobody receives should fail where the person clicking "invite" can see it.
type Unconfigured struct{}

func (Unconfigured) Describe() string {
	return "off — mail is not configured; invitations and password resets will be refused"
}

func (Unconfigured) Send(context.Context, Message) error { return ErrNotConfigured }

func indent(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		b.WriteString("  | ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// ── composing ────────────────────────────────────────────────────────────────────────────────────

// validateHeaders refuses anything that could add a header.
//
// # 🔴 Why an address is parsed rather than trimmed
//
// A newline in a recipient or a subject does not corrupt the mail, it EXTENDS it: `victim@example.com\nBcc:
// attacker@evil` is a valid-looking address that silently copies every invitation to somebody else.
// Everything that reaches a header line is checked for CR and LF, and the recipient is parsed as an
// address rather than merely inspected, so a form that mostly looks like an address is still refused.
func validateHeaders(m Message) error {
	if _, err := mail.ParseAddress(m.To); err != nil {
		return fmt.Errorf("mailer: %q is not a deliverable address: %w", m.To, err)
	}
	for _, f := range []struct{ name, value string }{{"recipient", m.To}, {"subject", m.Subject}} {
		if strings.ContainsAny(f.value, "\r\n") {
			return fmt.Errorf("mailer: refusing to send: the %s contains a line break, which would add "+
				"headers to the message", f.name)
		}
	}
	return nil
}

// Compose renders the wire form of a message.
//
// # 🔴 Why the text part comes first
//
// multipart/alternative is ordered least-preferred to most-preferred, and clients show the LAST part
// they understand. Putting HTML first shows plain text to everybody, which reads as the styling having
// failed rather than as a deliberate choice — and the bug survives review because both parts are
// present and correct.
func Compose(from string, m Message) (string, error) {
	if err := validateHeaders(m); err != nil {
		return "", err
	}
	boundary, err := randomBoundary()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", m.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", encodeSubject(m.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	// 🚫 No List-Unsubscribe, no tracking pixel, no remote anything. These are transactional mails about
	// somebody's account; a hosted image in a password-reset mail is a read receipt telling whoever
	// controls that host when the victim opened it.
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	b.WriteString(quotedPrintable(m.Text))
	b.WriteString("\r\n")

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	b.WriteString(quotedPrintable(m.HTML))
	b.WriteString("\r\n")

	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.String(), nil
}

// encodeSubject uses RFC 2047 when the subject is not plain ASCII, so an organization name with an
// accent in it does not arrive as mojibake.
func encodeSubject(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return "=?utf-8?B?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
		}
	}
	return s
}

// quotedPrintable encodes a body so no line exceeds the 998-octet limit and no bare CR or LF survives.
//
// 🔴 A line beginning with a lone "." would end the DATA command early, truncating the message at that
// point — the encoder escapes it, along with everything else outside printable ASCII.
func quotedPrintable(s string) string {
	var b strings.Builder
	lineLen := 0
	writeRaw := func(chunk string) {
		if lineLen+len(chunk) > 74 {
			b.WriteString("=\r\n")
			lineLen = 0
		}
		b.WriteString(chunk)
		lineLen += len(chunk)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\r':
			continue
		case c == '\n':
			b.WriteString("\r\n")
			lineLen = 0
		case c == '=' || c < 32 || c > 126:
			writeRaw(fmt.Sprintf("=%02X", c))
		case c == '.' && lineLen == 0:
			// Escaped so the SMTP dot-stuffing rule can never see a lone "." on its own line.
			writeRaw("=2E")
		default:
			writeRaw(string(c))
		}
	}
	return b.String()
}

func randomBoundary() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("mailer: no entropy available: %w", err)
	}
	return "heros-" + base64.RawURLEncoding.EncodeToString(b), nil
}
