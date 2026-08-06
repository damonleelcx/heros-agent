// Package mailer is the one way this platform sends a message to a person.
//
// # Why a seam, and why the fallback is not a no-op
//
// Confirmation links, password-reset links and invitations all need a channel to a human. Before P28 there was
// none — grep the repository for `net/smtp` outside an import fence and there are zero hits, which is also why
// `tenancy.Invitation` has been a complete data model with no way to tell the invitee it exists.
//
// The obvious shape is "send if configured, otherwise return nil". 🔴 That is the shape this package refuses.
// A person waiting for a reset link cannot distinguish a discarded message from a broken product, and neither
// can the operator, so a silent no-op converts a configuration gap into an unfalsifiable support ticket. The
// unconfigured path here RECORDS the message — link included — logs at WARN naming the recipient and the
// purpose, and reports `Configured() == false` so `/readyz` can say so out loud. A deployment with no mail
// server still works; its operator hands the link over, and nothing pretends otherwise.
//
// # Mail failure never fails the act that produced it
//
// A sign-up whose confirmation bounces is still a sign-up. Rolling back an account because a mail server was
// down loses a customer for an outage that is ours. Callers log the send error and carry on; the person can
// ask for the message again.
//
// # 🔴 What a message may contain
//
// One link, carrying one single-use, purpose-bound, expiring token. No password, no API credential and no
// session token.
//
// ⚠️ This said "and no HTML — plain text only". That is no longer true: a message now carries BOTH a text
// part and a styled HTML part matching the console (see render.go). The sentence is corrected rather than
// deleted because the two REASONS it gave still bind, and they are what the HTML part is built to satisfy:
//
//   - "no rendering context in which a value could be interpreted" — every interpolated value is escaped,
//     and the only values interpolated are our own copy and a link we minted;
//   - "no image loads that would report when a person opened it" — there is no remote resource of ANY kind
//     in the HTML part. The wordmark is text. A hosted logo would be a read receipt on a password-reset
//     email, which is the thing that sentence was protecting against rather than HTML as such.
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
	"net/smtp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Purpose names why a message is being sent. It reaches the log and the operator record, and never the
// message body — a recipient does not need our word for what they are looking at.
type Purpose string

const (
	PurposeVerifyEmail    Purpose = "verify_email"
	PurposeResetPassword  Purpose = "reset_password"
	PurposeSignupAttempt  Purpose = "signup_attempt_on_existing_address"
	PurposeInvitation     Purpose = "invitation"
	PurposeOwnerBootstrap Purpose = "owner_bootstrap"
)

// Message is one message to one person.
//
// One recipient, no CC, no BCC, no attachments. Every one of those is a capability nothing in this product
// needs and each is a way for a bug to reach somebody it was not addressed to.
type Message struct {
	To      string
	Subject string
	// TextBody is the plain-text part, and it is NOT a downgrade — it is what a screen reader, a terminal
	// client and every client that refuses HTML actually get, so it is written to read on its own.
	TextBody string
	// HTMLBody is the styled part, matching the console's sign-in surface. Empty sends a text-only message,
	// which is what the two of them were before P28's styling and what a caller building a message by hand
	// still gets.
	//
	// 🔴 Both parts are generated from ONE declaration (see render.go). A `multipart/alternative` message
	// asserts that its parts say the same thing, and the client picks one — so hand-writing them separately
	// is how an expiry says one hour in the part nobody read and twenty-four in the part they did.
	HTMLBody string
	Purpose  Purpose
}

func (m Message) validate() error {
	if strings.TrimSpace(m.To) == "" {
		return errors.New("mailer: a message needs a recipient")
	}
	if strings.ContainsAny(m.To, "\r\n") || strings.ContainsAny(m.Subject, "\r\n") {
		// 🔴 Header injection. A newline in a recipient or a subject is how one message becomes two, with a
		// `Bcc:` the caller never wrote. The address reaching here came from a person's own sign-up form.
		return errors.New("mailer: a recipient or subject containing a line break is refused")
	}
	if strings.TrimSpace(m.Subject) == "" || strings.TrimSpace(m.TextBody) == "" {
		// The TEXT part is what is required, even when an HTML part exists: it is the fallback, and a
		// multipart message whose text half is empty renders as a blank email in every client that prefers
		// text — including the ones a security-conscious reader uses on purpose.
		return errors.New("mailer: a message needs a subject and a plain-text body")
	}
	return nil
}

// Mailer is the seam. Two implementations and no third: SMTP, and the operator-visible fallback.
type Mailer interface {
	Send(ctx context.Context, m Message) error
	// Configured reports whether this deployment can actually deliver. 🔴 It is a fact, never a guess: the
	// readiness surface reports it, and a deployment that answers `true` while dropping mail is the exact
	// dishonesty this package exists to prevent.
	Configured() bool
	// From is the sender address, for a caller that wants to name it in a message body ("if you did not
	// request this, reply to …"). Empty on the unconfigured path.
	From() string
}

// TLSMode is how the SMTP connection is secured.
type TLSMode string

const (
	// TLSStartTLS connects in the clear and upgrades. Port 587's convention.
	TLSStartTLS TLSMode = "starttls"
	// TLSImplicit connects inside TLS from the first byte. Port 465's convention.
	TLSImplicit TLSMode = "implicit"
	// TLSNone is refused unless the host is a loopback address — see New.
	TLSNone TLSMode = "none"
)

// Config is the deployment's mail configuration.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	TLS      TLSMode
	Timeout  time.Duration
}

// Configured reports whether enough is present to attempt delivery. Host and sender are the two that cannot
// be defaulted; a username is optional because a relay inside a customer's own network often has none.
func (c Config) Configured() bool {
	return strings.TrimSpace(c.Host) != "" && strings.TrimSpace(c.From) != ""
}

// New returns the mailer this configuration describes: SMTP when it is complete, the operator fallback when
// it is not.
//
// 🔴 It REFUSES `TLSNone` to a non-loopback host rather than honouring it. A reset link travelling in clear
// text across a network is the credential this whole phase exists to protect, and "the operator asked for it"
// is not a reason — the operator asking for it is the mistake. Loopback is allowed because that is a local
// test relay, where there is no network to be on.
func New(cfg Config, l *log.Logger) (Mailer, error) {
	if !cfg.Configured() {
		return NewOperatorMailer(l), nil
	}
	if cfg.TLS == "" {
		cfg.TLS = TLSStartTLS
	}
	switch cfg.TLS {
	case TLSStartTLS, TLSImplicit:
	case TLSNone:
		if !isLoopback(cfg.Host) {
			return nil, fmt.Errorf("mailer: TLS mode %q is refused for host %q — a reset link in clear text is "+
				"the credential this protects; use starttls or implicit", cfg.TLS, cfg.Host)
		}
	default:
		return nil, fmt.Errorf("mailer: unknown TLS mode %q", cfg.TLS)
	}
	if cfg.Port == 0 {
		switch cfg.TLS {
		case TLSImplicit:
			cfg.Port = 465
		default:
			cfg.Port = 587
		}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	return &SMTPMailer{cfg: cfg, log: logger(l)}, nil
}

func isLoopback(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func logger(l *log.Logger) *log.Logger {
	if l != nil {
		return l
	}
	return log.Default()
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// SMTP
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

// SMTPMailer delivers over SMTP.
//
// # The connection is constructed explicitly, never defaulted
//
// This repository has no egress-lane registry, so "declare the lane" is honoured the way it can be here: the
// dialer, the timeout, the TLS configuration and the server name are all written down at the one place the
// connection is made. There is no package-level client, no `http.DefaultTransport`, and no path by which this
// reaches a host other than the configured one.
type SMTPMailer struct {
	cfg Config
	log *log.Logger
}

func (s *SMTPMailer) Configured() bool { return true }
func (s *SMTPMailer) From() string     { return s.cfg.From }

func (s *SMTPMailer) Send(ctx context.Context, m Message) error {
	if err := m.validate(); err != nil {
		return err
	}
	addr := net.JoinHostPort(s.cfg.Host, fmt.Sprint(s.cfg.Port))
	dialer := &net.Dialer{Timeout: s.cfg.Timeout}
	tlsCfg := &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}

	var conn net.Conn
	var err error
	if s.cfg.TLS == TLSImplicit {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("mailer: cannot reach %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(s.cfg.Timeout))
	}

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("mailer: SMTP handshake with %s failed: %w", addr, err)
	}
	defer func() { _ = c.Quit() }()

	if s.cfg.TLS == TLSStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			// 🔴 Refuse rather than continue in the clear. A server that will not upgrade is a server this
			// message must not be handed to, and "we tried" is not a property anybody can audit later.
			return fmt.Errorf("mailer: %s does not offer STARTTLS and this message will not be sent in clear text", addr)
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("mailer: STARTTLS with %s failed: %w", addr, err)
		}
	}
	if s.cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return fmt.Errorf("mailer: authentication to %s failed: %w", addr, err)
		}
	}
	if err := c.Mail(s.cfg.From); err != nil {
		return fmt.Errorf("mailer: sender refused by %s: %w", addr, err)
	}
	if err := c.Rcpt(m.To); err != nil {
		return fmt.Errorf("mailer: recipient refused by %s: %w", addr, err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mailer: %s refused the message body: %w", addr, err)
	}
	if _, err := w.Write([]byte(render(s.cfg.From, m))); err != nil {
		return fmt.Errorf("mailer: writing to %s failed: %w", addr, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mailer: %s rejected the message: %w", addr, err)
	}
	return nil
}

// render builds the RFC 5322 message: text-only, or `multipart/alternative` when an HTML part exists.
//
// 🔴 The TEXT part comes FIRST. In `multipart/alternative` the parts are ordered least-preferred to
// most-preferred, so a client shows the LAST one it understands. Text first, HTML second means an
// HTML-capable client shows the styled version and everything else falls back cleanly — reversing them
// makes every rich client show plain text, which looks like the styling silently failed.
func render(from string, m Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", m.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", m.Subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	// 🔴 Tells well-behaved clients and gateways not to fetch anything on the recipient's behalf. It stays
	// true with the HTML part in place, because that part references NO remote resource — the wordmark is
	// text, and a hosted logo would report the moment somebody opened a password-reset email.
	b.WriteString("Auto-Submitted: auto-generated\r\n")

	if strings.TrimSpace(m.HTMLBody) == "" {
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		b.WriteString(crlf(m.TextBody))
		b.WriteString("\r\n")
		return b.String()
	}

	// A fixed boundary would be a bug the day a body contains it. This one is drawn from the same source
	// every other unguessable value here comes from.
	boundary := newBoundary()
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", boundary)
	// A line for clients that render neither part. Almost nothing reaches it; it costs one line.
	b.WriteString("This is a message in MIME format.\r\n")

	fmt.Fprintf(&b, "\r\n--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	b.WriteString(crlf(m.TextBody))

	fmt.Fprintf(&b, "\r\n--%s\r\n", boundary)
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
	b.WriteString(crlf(m.HTMLBody))

	fmt.Fprintf(&b, "\r\n--%s--\r\n", boundary)
	return b.String()
}

// crlf normalises line endings for the wire.
func crlf(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

// newBoundary mints a MIME boundary that cannot collide with a body.
func newBoundary() string {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// A CSPRNG that cannot be read is not something to paper over with a constant: a predictable
		// boundary that appears in a body splits the message. Refusing is louder, and the caller turns a
		// send failure into a WARN rather than a corrupted mail.
		panic("mailer: cannot mint a MIME boundary: " + err.Error())
	}
	return "heros-" + base64.RawURLEncoding.EncodeToString(raw[:])
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// The operator fallback
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

// Record is one message a deployment could not send.
type Record struct {
	At      time.Time
	To      string
	Subject string
	Purpose Purpose
	// Body is kept IN FULL, link included. That is the whole point: the operator is the delivery mechanism
	// on this path, and a record that omitted the link would be a log line pretending to be a mailbox.
	Body string
}

// OperatorMailer is what a deployment with no SMTP configuration uses.
//
// 🔴 It is not a discard. Every message is recorded and logged at WARN, and `Configured()` answers false so
// the readiness surface reports the deployment as degraded rather than healthy. See the package doc.
type OperatorMailer struct {
	mu      sync.Mutex
	records []Record
	log     *log.Logger
	// max bounds the record so a deployment left unconfigured for a year does not grow without limit. The
	// OLDEST are dropped and the drop is logged — a silent eviction here would be the same defect one layer
	// down.
	max int
	now func() time.Time
}

// NewOperatorMailer builds the fallback.
func NewOperatorMailer(l *log.Logger) *OperatorMailer {
	return &OperatorMailer{log: logger(l), max: 200, now: func() time.Time { return time.Now().UTC() }}
}

func (o *OperatorMailer) Configured() bool { return false }
func (o *OperatorMailer) From() string     { return "" }

func (o *OperatorMailer) Send(_ context.Context, m Message) error {
	if err := m.validate(); err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	rec := Record{At: o.now(), To: m.To, Subject: m.Subject, Purpose: m.Purpose, Body: m.TextBody}
	o.records = append(o.records, rec)
	if len(o.records) > o.max {
		dropped := len(o.records) - o.max
		o.log.Printf("WARN mail: dropping %d undelivered message(s) from the operator record — it holds the "+
			"most recent %d. Configure SMTP, or these links cannot be handed over at all.", dropped, o.max)
		o.records = append([]Record(nil), o.records[dropped:]...)
	}
	// 🔴 WARN, naming the recipient and the purpose — and NOT the body. The link is in the record, which is
	// an authenticated operator surface; putting it in the log would copy a live credential into whatever
	// aggregator ships these lines, under a different retention policy and a wider audience.
	o.log.Printf("WARN mail: not configured — a %s message to %s was NOT sent. It is held on the operator "+
		"surface; hand the link over or configure SMTP.", m.Purpose, m.To)
	return nil
}

// Undelivered returns the held messages, newest first.
func (o *OperatorMailer) Undelivered() []Record {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := append([]Record(nil), o.records...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}
