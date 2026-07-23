package dynamictracing

import (
	"encoding/json"
	"regexp"
)

// RedactSecrets is the default input redactor. It replaces secret/bearer/API-key shapes with a marker
// BEFORE the inputs are hashed and stored, so a secret a workflow accidentally put in a prompt can
// never reach a trace blob. It is a conservative denylist over the raw bytes — the same shapes the
// P2.5 telemetry scrubber guards — kept local so this package has no dependency on the telemetry
// scrubber's private list.
//
// It is a SECOND fence: real provider credentials come from the secrets manager and are set as request
// HEADERS by the gateway, so they are not in the request body the interceptor sees. This guards the
// case where a workflow embeds a key in its own payload.
func RedactSecrets(in []byte) []byte {
	out := in
	for _, re := range secretPatterns() {
		out = re.ReplaceAll(out, []byte(Redacted))
	}
	return out
}

// Redacted is what a matched secret is replaced with — a visible marker so a reviewer sees redaction
// happened rather than a silent deletion.
const Redacted = "[REDACTED]"

var compiledPatterns []*regexp.Regexp

func secretPatterns() []*regexp.Regexp {
	if compiledPatterns != nil {
		return compiledPatterns
	}
	compiledPatterns = []*regexp.Regexp{
		regexp.MustCompile(`sk-[A-Za-z0-9_\-]{16,}`),                                          // OpenAI-style keys
		regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{16,}`),                                      // Anthropic keys
		regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-]{16,}`),                               // bearer tokens
		regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                                                // AWS access key ids
		regexp.MustCompile(`(?i)"?(api[_-]?key|token|secret|password)"?\s*[:=]\s*"[^"]{8,}"`), // key/value secrets
	}
	return compiledPatterns
}

// encodeStack renders a redacted stack as deterministic JSON for content-hashing. Frames carry only
// function + file:line, so the blob can never hold an argument value.
func encodeStack(frames []Frame) []byte {
	b, err := json.Marshal(frames)
	if err != nil {
		return []byte("[]")
	}
	return b
}
