package erroreport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/errorcode"
)

// ModulePath is this module's import path. A frame whose package starts with it is platform code; every
// other frame is a dependency or the runtime, carries no source context, and says so via `in_app`.
const ModulePath = "github.com/heros-foreal/agentd"

// Level is the two-value severity an event may carry. Two rather than seven, because an inbox needs to
// order by severity and nothing downstream distinguishes "warning" from "info" for a failure that
// produced a stack.
type Level string

const (
	LevelError Level = "error"
	LevelFatal Level = "fatal"
)

// Frame is one stack frame, reduced to the five fields the allowlist permits.
//
// It is built from `runtime.Frame` by reading named fields, never by embedding it: `runtime.Frame`
// carries an `Entry` program counter and a `Func` pointer, and a future Go release may add more. A
// struct that embedded it would transmit whatever the runtime gains next.
type Frame struct {
	Function string
	Package  string
	File     string
	Line     int
	InApp    bool
}

// Event is the complete set of things that may be reported. Every field maps to exactly one allowlist
// entry, and there is no field that does not.
//
// It is a VALUE, constructed by the caller or by `FromError`, so a reviewer reading a call site can see
// what will be transmitted without following an error through five wrappers.
type Event struct {
	// Type is the exception class — a type name, never a value. Set from `%T`, which never calls a
	// String or Error method and therefore cannot interpolate content.
	Type string
	// Code is a member of the central enum. A value that is not a member is replaced by
	// errorcode.Unknown before transmission — dropped, not passed through.
	Code errorcode.Code
	// Level is `error` or `fatal`.
	Level Level
	// Frames is the stack, platform frames first as the runtime produced them.
	Frames []Frame
	// TraceID is the identity already on the span, the log line and the X-Trace-Id response header.
	TraceID string
	// Release is the build identifier.
	Release string
	// Edition is the deployment shape, from a closed set.
	Edition string
	// Surface is an id from the closed surface enum. Never a URL.
	Surface string
	// Runtime is `go <version>` or `browser <version>`.
	Runtime string
}

// FromError builds an event from an error WITHOUT reading its message.
//
// 🔴 `err.Error()` is never called, here or anywhere in this package. That is the single most important
// line in the file: the message is where the leak lives, and the only defence that survives an engineer
// in a hurry is that the code which would read it does not exist.
//
// `code` is supplied by the CALL SITE rather than inferred from the error, because inference would
// mean pattern-matching on a message — reading the exact string this boundary refuses to transmit, in
// order to decide what to transmit instead.
func FromError(err error, code errorcode.Code, skip int) Event {
	ev := Event{
		Type:   "nil",
		Code:   code,
		Level:  LevelError,
		Frames: CaptureStack(skip + 1),
	}
	if err != nil {
		// `%T` yields a TYPE name. It does not call Error() or String(), so an error whose message
		// carries a prompt contributes its type and nothing else.
		ev.Type = fmt.Sprintf("%T", err)
	}
	return ev
}

// CaptureStack reads the current stack and reduces it to allowlisted frames.
//
// Exported because the panic recovery in `internal/api` needs a stack from ITS frame, not from a
// constructor's. `skip` is the number of frames above this call to discard.
func CaptureStack(skip int) []Frame {
	pcs := make([]uintptr, 64)
	n := runtime.Callers(skip+2, pcs)
	if n == 0 {
		return nil
	}
	out := make([]Frame, 0, n)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		pkg, fn := splitFunc(f.Function)
		inApp := strings.HasPrefix(pkg, ModulePath)
		out = append(out, Frame{
			Function: fn,
			Package:  pkg,
			File:     trimFile(f.File, pkg, inApp),
			Line:     f.Line,
			InApp:    inApp,
		})
		if !more {
			break
		}
	}
	return out
}

// splitFunc splits a runtime function name into its package path and its symbol.
//
// `github.com/heros-foreal/agentd/internal/api.(*Server).handleReadyz` is a package path containing
// dots and slashes followed by a symbol containing dots, so the split is at the first dot AFTER the
// last slash — anything simpler mis-splits one of the two.
func splitFunc(name string) (pkg, fn string) {
	if name == "" {
		return "", ""
	}
	slash := strings.LastIndex(name, "/")
	dot := strings.Index(name[slash+1:], ".")
	if dot < 0 {
		return name, ""
	}
	cut := slash + 1 + dot
	return name[:cut], name[cut+1:]
}

// trimFile reduces a build-host absolute path to a module-relative one.
//
// # Why this is a boundary concern and not cosmetics
//
// `runtime.Frame.File` is the path on the machine that COMPILED the binary. On CI that is a checkout
// directory; on a developer's machine it is `/Users/<name>/…`, which is a personal identifier, and on a
// customer's build host it is their directory layout. None of that is our code and none of it belongs
// in a third party's inbox — while `internal/api/server.go` is exactly as useful for triage.
//
// A non-platform frame is reduced to its base name: a dependency's internal path tells a reader
// nothing they can act on, and it is the largest source of incidental host detail in a stack.
func trimFile(file, pkg string, inApp bool) string {
	if file == "" {
		return ""
	}
	if !inApp {
		return path.Base(file)
	}
	rel := strings.TrimPrefix(pkg, ModulePath)
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return path.Base(file)
	}
	if idx := strings.LastIndex(file, "/"+rel+"/"); idx >= 0 {
		return file[idx+1:]
	}
	// The package directory is not in the path (a test binary, a generated file). Fall back to the base
	// name rather than shipping the absolute path: a fallback that leaks is worse than one that is terse.
	return path.Join(rel, path.Base(file))
}

// Wire returns the event as the exact map of allowlisted keys.
//
// This is the artefact the boundary test walks: every leaf key it produces must be `Permitted`, and
// across a corpus every allowlisted key must be populated by something. Nothing else in this package
// serialises an event.
func (e Event) Wire() map[string]any {
	code := e.Code
	if !errorcode.Valid(string(code)) {
		// 🔴 Dropped, not passed through. A value that is not a member of the central enum is the exact
		// shape a leaked message would take if somebody typed a literal at a call site.
		code = errorcode.Unknown
	}
	level := e.Level
	if level != LevelFatal {
		level = LevelError
	}
	surface := e.Surface
	if !ValidServerSurface(surface) {
		// Same discipline as an unrecognised error code, for the same reason: an id typed at a call
		// site is the shape a leaked path would take, and "unknown" is a truthful answer where a path
		// would be a leak wearing a field name.
		surface = "unknown"
	}

	frames := make([]any, 0, len(e.Frames))
	for _, f := range e.Frames {
		frames = append(frames, map[string]any{
			"function": f.Function,
			"package":  f.Package,
			"file":     f.File,
			"line":     f.Line,
			"in_app":   f.InApp,
		})
	}

	return map[string]any{
		"error.type": e.Type,
		"error.code": string(code),
		"level":      string(level),
		"frames":     frames,
		"trace_id":   e.TraceID,
		"release":    e.Release,
		"edition":    e.Edition,
		"surface":    surface,
		"runtime":    e.Runtime,
	}
}

// ProtocolValues is the complete set of literal values the envelope adds that did NOT come from the
// event.
//
// It exists so the boundary test can assert the strong form of the guarantee: every string in the
// transmitted bytes is either a value the allowlist produced or one of these. Without it the test could
// only check that forbidden shapes are absent, which is a denylist — and this package's entire argument
// is that a denylist is the wrong direction.
var ProtocolValues = []string{
	"event",            // the envelope item type
	"application/json", // the item's content type
	"go",               // the platform, for a Go-side event
	"javascript",       // the platform, for a browser event
}

// envelopeMeta is the two values the protocol requires that are neither allowlisted nor constant.
//
// `event_id` is REQUIRED by the ingest protocol and is derived — a hash of the event's own allowlisted
// content plus the send time — rather than randomly minted. That matters for a reason the design states
// explicitly: adding an incident system usually adds an incident identity, after which two systems hold
// half an incident each with no join key. `trace_id` remains the only correlation identity anyone uses;
// this value is a protocol artefact that resolves nothing, and deriving it rather than minting it keeps
// it from becoming a handle by habit.
type envelopeMeta struct {
	EventID string
	SentAt  time.Time
}

func (e Event) meta(sentAt time.Time) envelopeMeta {
	h := sha256.New()
	for _, part := range []string{e.TraceID, string(e.Code), e.Type, e.Release, e.Surface} {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	for _, f := range e.Frames {
		_, _ = h.Write([]byte(f.Package + "." + f.Function + ":" + strconv.Itoa(f.Line) + "\x00"))
	}
	_, _ = h.Write([]byte(sentAt.UTC().Format(time.RFC3339Nano)))
	return envelopeMeta{EventID: hex.EncodeToString(h.Sum(nil))[:32], SentAt: sentAt.UTC()}
}

// Payload renders the event into the ingest schema.
//
// Every value here is read out of `Wire()` or is a `ProtocolValues` member. The mapping from our wire
// keys to the protocol's field names is written out longhand rather than generated, so a reviewer can
// see both halves at once and check that nothing was added on the way across.
func (e Event) Payload(sentAt time.Time, platform string) ([]byte, error) {
	w := e.Wire()
	meta := e.meta(sentAt)

	frames := make([]map[string]any, 0, len(e.Frames))
	for _, f := range e.Frames {
		frames = append(frames, map[string]any{
			"function": f.Function,
			"module":   f.Package,
			"filename": f.File,
			"lineno":   f.Line,
			"in_app":   f.InApp,
		})
	}

	payload := map[string]any{
		"event_id": meta.EventID,
		"platform": platform,
		"level":    w["level"],
		"release":  w["release"],
		// Tags rather than a `contexts.trace` block. The trace context's schema requires a span id,
		// which we would have to invent — and inventing an identifier to satisfy a schema is how a
		// second correlation identity gets created by accident.
		"tags": map[string]any{
			"trace_id":   w["trace_id"],
			"error.code": w["error.code"],
			"edition":    w["edition"],
			"surface":    w["surface"],
			"runtime":    w["runtime"],
		},
		"exception": map[string]any{
			"values": []any{
				map[string]any{
					"type": w["error.type"],
					// The ONLY message-shaped field, and it is a member of a closed enum.
					"value":      w["error.code"],
					"stacktrace": map[string]any{"frames": frames},
				},
			},
		},
	}
	return json.Marshal(payload)
}

// Envelope renders the complete bytes that go on the wire, in the ingest envelope format.
//
// Three newline-separated JSON documents: an envelope header, an item header, and the payload. There is
// nothing else — no attachments, no session item, no profile, no transaction.
func (e Event) Envelope(sentAt time.Time, platform string) ([]byte, error) {
	payload, err := e.Payload(sentAt, platform)
	if err != nil {
		return nil, err
	}
	meta := e.meta(sentAt)
	header, err := json.Marshal(map[string]any{
		"event_id": meta.EventID,
		"sent_at":  meta.SentAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, err
	}
	item, err := json.Marshal(map[string]any{
		"type":         "event",
		"content_type": "application/json",
		"length":       len(payload),
	})
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.Write(header)
	b.WriteByte('\n')
	b.Write(item)
	b.WriteByte('\n')
	b.Write(payload)
	b.WriteByte('\n')
	return []byte(b.String()), nil
}
