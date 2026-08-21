package conversation

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// adversarial.go carries the ATTACK CORPUS (task 3.6) and the detector that reports what it finds.
//
// # Why the corpus is embedded rather than read from testdata by each fence
//
// Because §6's fences live in three packages and the boundary they defend is one boundary. A corpus
// each fence loaded from its own relative path is a corpus that exists in three versions within a
// quarter, and the version that goes stale is the one nobody is running.
//
// # 🔴 The fixtures are UNSANITIZED, and that is the requirement
//
// design.md D7 says it plainly: *"A test whose fixture was already sanitized by a helper proves nothing,
// and is the shape this fence will take if nobody is watching."* So the files under `testdata/adversarial`
// contain live injection strings, a real-looking metadata-service URL, a shell pipeline, and model output
// shaped exactly like a `proposal` with a well-formed identifier that exists in no ledger.
//
// Nothing in this package cleans them. `Detect` REPORTS on them; it never rewrites them, because a
// rewriting step is a step somebody would later rely on.
//
// # 🚫 What this detector is NOT
//
// It is not the defence. NFR-S2 is the defence — an effect-bearing message requires an artifact a model
// cannot mint, checked in `Emitter.resolveArtifact` — and it holds whether or not anything below fires.
// This is defence in depth and a REPORTING mechanism: NFR-S5's rule is that a detected instruction
// attempt becomes a `finding` about the repository, because silently ignoring it wastes the one signal
// that something is wrong.
//
// The distinction is load-bearing in the test suite: §6.3 and §6.14 run with detection DELIBERATELY
// DISABLED and assert that no effect is produced anyway. A fence that only passes with the classifier on
// is testing the classifier.

//go:embed testdata/adversarial
var adversarialFS embed.FS

// AdversarialFixture is one file of untrusted repository content.
type AdversarialFixture struct {
	// Name is the path as it would appear in a repository.
	Name string
	// Content is the file, VERBATIM. 🚫 Never trimmed, escaped or normalised on the way out.
	Content string
}

// AdversarialCorpus returns the attack fixtures, sorted by name so a test's output is stable.
func AdversarialCorpus() ([]AdversarialFixture, error) {
	entries, err := fs.ReadDir(adversarialFS, "testdata/adversarial")
	if err != nil {
		return nil, err
	}
	out := make([]AdversarialFixture, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := adversarialFS.ReadFile(path.Join("testdata/adversarial", e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, AdversarialFixture{Name: e.Name(), Content: string(b)})
	}
	if len(out) == 0 {
		// An empty corpus would make every fence over it pass vacuously.
		return nil, fmt.Errorf("conversation: the adversarial corpus is empty")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// AttemptClass is what kind of instruction attempt was found. A closed set, because a `finding` about an
// injection has to say WHAT was attempted for a reader to judge it, and "suspicious content" is not that.
type AttemptClass string

const (
	// AttemptInstructionOverride — text addressed to the agent trying to replace its instructions.
	AttemptInstructionOverride AttemptClass = "instruction_override"
	// AttemptForgedApproval — text claiming an approval was already given, or instructing one.
	AttemptForgedApproval AttemptClass = "forged_approval"
	// AttemptEgress — a URL, endpoint or callback the content wants followed. Includes the cloud
	// metadata service, which is the highest-value one and the easiest to miss in a config file.
	AttemptEgress AttemptClass = "egress_request"
	// AttemptCommand — a shell command the content wants executed.
	AttemptCommand AttemptClass = "command_execution"
	// AttemptForgedArtifact — output shaped like an effect-bearing message, carrying a well-formed
	// identifier. 🔴 Detecting this changes NOTHING about whether an effect happens: the artifact still
	// has to resolve. It is reported so a person learns their repository contains one.
	AttemptForgedArtifact AttemptClass = "forged_artifact"
)

// Attempt is one detection.
type Attempt struct {
	Class AttemptClass
	// File is where it was found.
	File string
	// Line is 1-based.
	Line int
	// Excerpt is the matching line, TRUNCATED and with control characters removed.
	//
	// 🔴 Truncated because this string is rendered and logged, and an attacker chooses it. An
	// unbounded excerpt is an attacker-controlled paragraph in an operator's terminal; the excerpt
	// exists to let a person FIND the line in their own editor, not to reproduce it.
	Excerpt string
}

// excerptLimit bounds what a detection may quote back.
const excerptLimit = 120

// signals is the detection table: a lowercase substring and the class it indicates.
//
// # Why a table of substrings and not a model
//
// Because this is defence in DEPTH, and depth is worth having only if it is cheap, deterministic and
// impossible to talk out of. A model asked "is this an injection?" is a second thing an injection can
// address. A substring table cannot be persuaded.
//
// 🔴 Its false-negative rate is high and that is ACCEPTED, in writing, because the structural defence
// does not depend on it. What this must not do is produce false CONFIDENCE — so nothing anywhere reads
// "no attempts detected" as "this content is safe".
var signals = []struct {
	needle string
	class  AttemptClass
}{
	{"ignore all previous instructions", AttemptInstructionOverride},
	{"ignore prior instructions", AttemptInstructionOverride},
	{"ignore previous instructions", AttemptInstructionOverride},
	{"supersedes your system prompt", AttemptInstructionOverride},
	{"you are now in maintenance mode", AttemptInstructionOverride},
	{"agent directive", AttemptInstructionOverride},
	{"assistant_instructions", AttemptInstructionOverride},
	{"system:", AttemptInstructionOverride},
	{"assistant:", AttemptInstructionOverride},

	{"approve all", AttemptForgedApproval},
	{"already granted approval", AttemptForgedApproval},
	{"without review", AttemptForgedApproval},
	{"do not ask again", AttemptForgedApproval},
	{"approvable=true", AttemptForgedApproval},
	{"authorised to approve", AttemptForgedApproval},
	{"authorized to approve", AttemptForgedApproval},

	// 169.254.169.254 is the cloud metadata service. It is listed FIRST among the egress signals
	// because it is the one that turns "the agent followed a URL" into "the agent read an instance
	// credential", and because it looks like an ordinary address in a config file.
	{"169.254.169.254", AttemptEgress},
	{"metadata.google.internal", AttemptEgress},
	{"webhook:", AttemptEgress},
	{"callback", AttemptEgress},
	{"http://", AttemptEgress},
	{"https://", AttemptEgress},

	{"curl ", AttemptCommand},
	{"| sh", AttemptCommand},
	{"|sh", AttemptCommand},
	{"rm -rf", AttemptCommand},
	{"wget ", AttemptCommand},
	{"on_complete:", AttemptCommand},

	{"\"kind\": \"proposal\"", AttemptForgedArtifact},
	{"\"kind\": \"result\"", AttemptForgedArtifact},
	{"\"proposal_id\"", AttemptForgedArtifact},
	{"\"verdict_ref\"", AttemptForgedArtifact},
}

// Detect reports every instruction attempt in one file of repository content.
//
// 🔴 It takes CONTENT and returns REPORTS. It cannot modify anything, it cannot decide anything, and it
// has no path to an effect — which is what keeps NFR-S5 ("report it") from quietly becoming "act on it".
func Detect(file, content string) []Attempt {
	var out []Attempt
	for i, line := range strings.Split(content, "\n") {
		lower := strings.ToLower(line)
		seen := map[AttemptClass]bool{}
		for _, sig := range signals {
			if !strings.Contains(lower, sig.needle) || seen[sig.class] {
				continue
			}
			seen[sig.class] = true
			out = append(out, Attempt{
				Class: sig.class, File: file, Line: i + 1, Excerpt: excerpt(line),
			})
		}
	}
	return out
}

// excerpt truncates and strips control characters.
//
// Control characters are removed because a terminal reading this is a terminal an attacker can drive:
// an ANSI escape in an excerpt can rewrite the lines above it, which turns a security report into a
// place to hide a security report.
func excerpt(line string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(line) {
		if r < 0x20 || r == 0x7f {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
		if b.Len() >= excerptLimit {
			return b.String() + "…"
		}
	}
	return b.String()
}

// FindingsFor turns detections into the findings NFR-S5 requires, one per (file, class).
//
// 🔴 Grouped by class rather than one per line. A README with forty URLs would otherwise produce forty
// messages and bury the one that matters, and a conversation that floods is a conversation people learn
// to scroll past — which is the same outcome as not reporting at all, reached more expensively.
func FindingsFor(attempts []Attempt, surfaceHref string) []FindingPayload {
	type key struct {
		file  string
		class AttemptClass
	}
	grouped := map[key][]Attempt{}
	var order []key
	for _, a := range attempts {
		k := key{a.File, a.Class}
		if _, seen := grouped[k]; !seen {
			order = append(order, k)
		}
		grouped[k] = append(grouped[k], a)
	}

	out := make([]FindingPayload, 0, len(order))
	for _, k := range order {
		group := grouped[k]
		lines := make([]string, 0, len(group))
		for _, a := range group {
			lines = append(lines, fmt.Sprintf("%d", a.Line))
		}
		out = append(out, FindingPayload{
			Surface:     "source",
			SurfaceHref: surfaceHref,
			Claim: fmt.Sprintf("%s contains text addressed to an automated reader (%s) at line %s. "+
				"It was not acted on.", k.file, k.class, strings.Join(lines, ", ")),
			// 🔴 The evidence reference is the FILE AND LINES, which is what a person opens. A finding
			// about injected text with no location is a claim the reader cannot check — and this is the
			// one finding where "go and look yourself" is the only acceptable resolution, because
			// nothing here should be trusted to summarise the content.
			EvidenceRef: fmt.Sprintf("source:%s#L%s", k.file, strings.Join(lines, ",L")),
			State:       FindingMeasured,
		})
	}
	return out
}
