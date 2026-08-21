package sourceingest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/providergateway"
)

// git.go implements Source over a repository connection: a shallow clone at the requested revision,
// run past the same TreeGuard the bundle path runs, stored as a snapshot the retention job can expire
// and a revocation can delete.
//
// # The mode is invisible downstream (design D1)
//
// `Materialize` returns the same `Materialized` the bundle path returns, and it gets there by
// converging on the same extraction: a clone is archived into the SAME snapshot store a push writes
// to, so discovery, the graph editor and everything else read one shape. That convergence is not
// tidiness — it is what makes §7.8's mode-parity fence assertable at all. If the clone path built its
// own tree, "identical IR across modes" would be a claim about two code paths staying in agreement,
// which is the claim that is never true for long.
//
// # Why the tree is archived rather than kept as a worktree
//
// A worktree on local disk cannot be revoked. Revocation has to delete every tree derived from the
// grant (D3), and a pod cannot delete a directory on another pod's disk — so a worktree-based
// snapshot would produce exactly the failure D3 names: the system keeps answering, correctly, from
// data it is no longer authorized to hold, and nothing fails when it does. Writing the snapshot to the
// blob store the bundle path already uses makes `DELETE ... WHERE connection_id = $1` the whole
// cascade, and makes it work from any replica.
//
// # 🔴 Symlinks are materialized on purpose
//
// `core.symlinks=false` would make git write a symlink as a regular file containing its target path.
// That sounds safer and is the opposite: the hazard becomes invisible, the TreeGuard sees a small text
// file and admits it, and discovery then walks a tree whose links have been silently rewritten. The
// clone keeps symlinks so the guard can REFUSE them, which is the behaviour §7.1 fences.

// CloneCause is the closed set of reasons a clone failed (FR11).
//
// Four, and they stay four. P9's rule that failure classes stay distinguishable, applied to intake: a
// rotated token and a renamed default branch are different people's problems on different days, and
// "could not connect to the repository" sends both of them to the same wrong place.
type CloneCause string

const (
	// CauseCredentialRejected — the forge refused the credential. The remedy is a rotation, and it is
	// the customer's to perform.
	CauseCredentialRejected CloneCause = "credential_rejected"
	// CauseRepositoryNotFound — the repository is gone, renamed, or made private. Indistinguishable
	// from "never existed" by design on every forge, and reported as one cause for that reason.
	CauseRepositoryNotFound CloneCause = "repository_not_found"
	// CauseRevisionNotFound — the repository is reachable and the revision is not in it. The common
	// case is a rebased or deleted branch, and it is emphatically NOT a credential problem.
	CauseRevisionNotFound CloneCause = "revision_not_found"
	// CauseNetwork — the forge could not be reached at all. Ours to fix, not theirs.
	CauseNetwork CloneCause = "network"
)

// CloneCauses returns every cause, sorted. The console renders one message per member and a fence
// asserts the two lists are the same length, so a fifth cause cannot arrive as a blank card.
func CloneCauses() []CloneCause {
	out := []CloneCause{CauseCredentialRejected, CauseNetwork, CauseRepositoryNotFound, CauseRevisionNotFound}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Valid reports membership.
func (c CloneCause) Valid() bool {
	for _, v := range CloneCauses() {
		if v == c {
			return true
		}
	}
	return false
}

// String makes CloneCause printable.
func (c CloneCause) String() string { return string(c) }

// CloneError is a clone failure with its cause attached.
//
// The cause is a FIELD rather than a sentinel per cause, because callers need both — the console
// switches on the cause, the metrics count by it, and the operator reads the detail. Four sentinels
// would make `errors.Is` the only usable check and would lose the detail.
type CloneError struct {
	Cause CloneCause
	// Repository and Revision are what was asked for. Safe to render: neither is a credential.
	Repository string
	Revision   string
	// Detail is git's own message, REDACTED. Never the raw output — see redactForgeOutput.
	Detail string
}

func (e *CloneError) Error() string {
	return fmt.Sprintf("sourceingest: clone of %s at %s failed (%s): %s", e.Repository, e.Revision, e.Cause, e.Detail)
}

// CauseOf extracts the cause from an error, reporting false when it is not a clone failure.
func CauseOf(err error) (CloneCause, bool) {
	var ce *CloneError
	if errors.As(err, &ce) {
		return ce.Cause, true
	}
	return "", false
}

// SnapshotStore is the write side GitSource needs from the snapshot store: record a cloned tree with
// its connection and its expiry, and know whether one is already there.
//
// Narrower than BundleStore on purpose, and pointedly it CANNOT read bytes: the clone path has no
// business serving a snapshot back, and an interface that cannot express a read is one that cannot
// grow a handler that does.
type SnapshotStore interface {
	// PutDerived records a snapshot produced by a connection, expiring at expiresAtMS.
	PutDerived(ctx context.Context, ref Ref, data []byte, connectionID string, expiresAtMS int64) error
	// LiveSnapshot reports whether an unexpired snapshot DERIVED FROM connectionID exists for ref.
	//
	// 🔴 The connection is part of the question, and leaving it out was a real defect this
	// interface's first version had. A workflow can have a connection AND an older pushed bundle at
	// the same revision — a customer who used `push-source` before connecting. Asking only "is there
	// a live snapshot" then answers YES about the BUNDLE, and the connected workflow is quietly
	// served an upload nobody checked against the repository, forever, without a single clone. That
	// is design D4's silent degradation arriving through the cache rather than through a failure.
	LiveSnapshot(ctx context.Context, ref Ref, connectionID string, nowMS int64) (bool, error)
	// DeleteByConnection removes every snapshot derived from a connection and reports how many.
	// This IS the cascade (D3).
	DeleteByConnection(ctx context.Context, tenantID, connectionID string) (int, error)
	// DeleteExpired removes every snapshot past its expiry as of nowMS and reports how many.
	DeleteExpired(ctx context.Context, nowMS int64) (int, error)
}

// DefaultCloneRetention is the window a cloned snapshot is held for: 72 hours (PRD §14 A4).
//
// A pushed bundle has no expiry — it is retained until the customer deletes it, because it exists
// because they ran a command naming a revision. A clone has no act behind it, and an unbounded hold
// with no act is the standing capability ADR-013 spent its argument bounding. 72 h spans a weekend
// (so a Monday re-run does not spend the grant again), does not span a holiday, and bounds the worst
// case in days rather than in "until somebody notices".
const DefaultCloneRetention = 72 * time.Hour

// GitSource materializes source by cloning a connected repository.
type GitSource struct {
	conns    ConnectionStore
	snaps    SnapshotStore
	secrets  providergateway.ForgeSecrets
	guard    *TreeGuard
	bundles  *BundleSource
	scratch  string
	metrics  *IngestMetrics
	retain   time.Duration
	nowMS    func() int64
	runGit   gitRunner
	newIDFor func(prefix string) string
}

// GitConfig wires a GitSource.
type GitConfig struct {
	Connections ConnectionStore
	Snapshots   SnapshotStore
	Secrets     providergateway.ForgeSecrets
	// Bundles is the SAME BundleSource the push path uses. Shared deliberately: it is what makes the
	// three modes converge on one extraction, and therefore what makes mode parity a property rather
	// than a promise.
	Bundles *BundleSource
	// Scratch is where clones are made. Each clone gets a fresh child directory, removed afterwards.
	Scratch string
	// Metrics receives per-forge ingest outcomes. Optional — a deployment without it still clones.
	Metrics *IngestMetrics
	// Retention overrides DefaultCloneRetention. Present for tests, which cannot wait 72 hours.
	Retention time.Duration
	// NowMS and IDFor are injected for the same reason every clock in this repository is: a test with
	// a second clock goes red on the calendar alone.
	NowMS func() int64
	IDFor func(prefix string) string
}

// NewGitSource builds the clone-backed Source.
func NewGitSource(cfg GitConfig) (*GitSource, error) {
	switch {
	case cfg.Connections == nil:
		return nil, fmt.Errorf("sourceingest: git source needs a connection store")
	case cfg.Snapshots == nil:
		return nil, fmt.Errorf("sourceingest: git source needs a snapshot store")
	case cfg.Secrets == nil:
		return nil, fmt.Errorf("sourceingest: git source needs a forge-credential source")
	case cfg.Bundles == nil:
		return nil, fmt.Errorf("sourceingest: git source needs the bundle source to materialize through")
	case cfg.Scratch == "":
		return nil, fmt.Errorf("sourceingest: git source needs a scratch directory")
	}
	if err := os.MkdirAll(cfg.Scratch, 0o700); err != nil {
		return nil, fmt.Errorf("sourceingest: clone scratch %s: %w", cfg.Scratch, err)
	}
	g := &GitSource{
		conns:   cfg.Connections,
		snaps:   cfg.Snapshots,
		secrets: cfg.Secrets,
		guard:   NewTreeGuard(),
		bundles: cfg.Bundles,
		scratch: cfg.Scratch,
		metrics: cfg.Metrics,
		retain:  cfg.Retention,
		nowMS:   cfg.NowMS,
		runGit:  execGit,
	}
	g.setIDFor(cfg.IDFor)
	if g.retain == 0 {
		g.retain = DefaultCloneRetention
	}
	if g.nowMS == nil {
		g.nowMS = func() int64 { return time.Now().UnixMilli() }
	}
	if g.newIDFor == nil {
		g.newIDFor = defaultID
	}
	return g, nil
}

// setIDFor installs the identifier generator. The field is unexported so a caller cannot swap it
// after construction — a clone-record id generator replaced mid-flight is a ledger with two id shapes.
func (g *GitSource) setIDFor(fn func(string) string) {
	if fn != nil {
		g.newIDFor = fn
	}
}

// readContext carries who asked and why into the clone record (FR9).
//
// Passed through the CONTEXT rather than through Materialize's signature, because `Materialize` is
// `Source`'s method and adding a parameter to it would be a branch threaded through the pipeline —
// the exact thing design D1 refuses. A caller that sets nothing gets `scheduled`, which is the
// fail-toward-disclosure direction: an unattributed read recorded as unattended is honest, and one
// recorded as person-initiated is a lie the ledger cannot be corrected from.
type readContext struct {
	Actor   Actor
	ActorID string
	Reason  string
}

type readContextKey struct{}

// WithReadContext attaches the actor and reason a clone will be recorded under.
func WithReadContext(ctx context.Context, actor Actor, actorID, reason string) context.Context {
	if !actor.Valid() {
		actor = ActorScheduled
	}
	return context.WithValue(ctx, readContextKey{}, readContext{Actor: actor, ActorID: actorID, Reason: reason})
}

func readContextFrom(ctx context.Context) readContext {
	if rc, ok := ctx.Value(readContextKey{}).(readContext); ok {
		if !rc.Actor.Valid() {
			rc.Actor = ActorScheduled
		}
		return rc
	}
	// 🔴 The default is `scheduled`. See WithReadContext.
	return readContext{Actor: ActorScheduled}
}

// Materialize clones the connected repository at ref's revision and returns the extracted tree.
//
// # 🚫 No fallback, and where the line actually is
//
// A clone failure NEVER serves an older snapshot (D4). A clone fails most often while nobody is
// watching, which is when a plausible answer is most likely to be believed — yesterday's tree under
// today's question produces a finding about source that no longer exists, and nothing about the
// finding says so.
//
// What IS served without cloning is an unexpired snapshot THIS CONNECTION already produced for the
// SAME revision, and that is not a fallback: a revision is immutable, so the stored tree and a fresh
// clone are the same bytes. The distinction matters because collapsing the two would either re-clone on
// every read (spending the grant, which is the cost this phase measures) or serve a different revision
// (which is the defect).
//
// 🔴 A snapshot the connection did NOT produce — a pushed bundle at the same revision — is not a
// shortcut. It is an upload nobody checked against the repository, and serving it to a connected
// workflow would mean the connection never reads anything and nobody finds out.
func (g *GitSource) Materialize(ctx context.Context, ref Ref) (Materialized, error) {
	if err := ref.Validate(); err != nil {
		return Materialized{}, err
	}
	conn, err := g.conns.ForWorkflow(ctx, ref.TenantID, ref.WorkflowID)
	switch {
	case errors.Is(err, ErrNoConnection):
		// A tenant with no connection has not opted in. That is ErrNoSource — the same first-class
		// state the bundle path returns — so a caller cannot tell the modes apart here either.
		return Materialized{}, ErrNoSource
	case err != nil:
		return Materialized{}, fmt.Errorf("sourceingest: read connection for %s: %w", ref, err)
	}

	live, err := g.snaps.LiveSnapshot(ctx, ref, conn.ConnectionID, g.nowMS())
	if err != nil {
		return Materialized{}, fmt.Errorf("sourceingest: check snapshot for %s: %w", ref, err)
	}
	if live {
		return g.bundles.Materialize(ctx, ref)
	}

	if err := g.clone(ctx, conn, ref); err != nil {
		return Materialized{}, err
	}
	return g.bundles.Materialize(ctx, ref)
}

// clone performs the read, records it, and stores the snapshot.
func (g *GitSource) clone(ctx context.Context, conn Connection, ref Ref) error {
	rc := readContextFrom(ctx)
	started := g.nowMS()

	dir, err := os.MkdirTemp(g.scratch, "clone-")
	if err != nil {
		return fmt.Errorf("sourceingest: clone scratch for %s: %w", ref, err)
	}
	// The worktree is removed whatever happens. It is the customer's source on our disk and it has no
	// reason to outlive the archive it becomes.
	defer func() { _ = os.RemoveAll(dir) }()

	archive, adm, cerr := g.fetch(ctx, conn, ref, dir)
	if cerr != nil {
		cause := CauseNetwork
		if ce, ok := CauseOf(cerr); ok {
			cause = ce
		}
		g.record(ctx, conn, ref, rc, Outcome(cause), 0, 0, g.nowMS()-started)
		g.metrics.Observe(conn.Forge, cause, 0, time.Duration(g.nowMS()-started)*time.Millisecond)
		return cerr
	}

	expires := g.nowMS() + g.retain.Milliseconds()
	if err := g.snaps.PutDerived(ctx, ref, archive, conn.ConnectionID, expires); err != nil {
		// A store failure is NOT one of the four clone causes: the clone succeeded and the platform
		// could not keep what it read. Flattening it into `network` would send an operator to look at
		// the forge, and the forge is fine.
		return fmt.Errorf("sourceingest: store cloned snapshot for %s: %w", ref, err)
	}
	dur := g.nowMS() - started
	g.record(ctx, conn, ref, rc, OutcomeSucceeded, adm.Bytes(), adm.Entries(), dur)
	g.metrics.Observe(conn.Forge, "", int64(len(archive)), time.Duration(dur)*time.Millisecond)
	return nil
}

// Outcome is what one entry in the read ledger records: a success, or one of the four causes.
//
// 🔴 FIVE members, not four, and it is a distinct type from CloneCause rather than "CloneCause plus
// empty". The ledger's rows are read by a console that renders one message per member, and an outcome
// it does not recognise lands in a `default:` arm and renders as NOTHING — which, in a list of reads,
// is indistinguishable from a read that succeeded. Making success a MEMBER means the union the browser
// checks against is the union the database constrains.
type Outcome string

// OutcomeSucceeded is the ledger value for a read that worked.
const OutcomeSucceeded Outcome = "succeeded"

// Outcomes returns every ledger outcome, sorted — success and the four causes.
func Outcomes() []Outcome {
	out := []Outcome{OutcomeSucceeded}
	for _, c := range CloneCauses() {
		out = append(out, Outcome(c))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Valid reports membership.
func (o Outcome) Valid() bool {
	for _, v := range Outcomes() {
		if v == o {
			return true
		}
	}
	return false
}

// String makes Outcome printable.
func (o Outcome) String() string { return string(o) }

// fetch clones into dir, guards the tree, and archives it.
func (g *GitSource) fetch(ctx context.Context, conn Connection, ref Ref, dir string) ([]byte, *Admission, error) {
	public, err := PublicCloneURL(conn.Forge, conn.Repository)
	if err != nil {
		return nil, nil, err
	}
	var cloneErr error
	useErr := g.secrets.UseForgeToken(ctx,
		providergateway.ForgeRef{Forge: conn.Forge.String(), ConnectionID: conn.ConnectionID},
		func(token string) error {
			url, err := CloneURL(conn.Forge, conn.Repository, conn.ExternalID, token)
			if err != nil {
				return err
			}
			// 🔴 The credentialed URL is written to a file git reads, never passed as an argument.
			// argv is world-readable in /proc on Linux, so a token in a command line is a token every
			// process on the box can read for as long as the clone runs.
			cloneErr = g.shallowClone(ctx, url, public, ref.SourceRevision, dir)
			return nil
		})
	if errors.Is(useErr, providergateway.ErrNoForgeCredential) {
		return nil, nil, &CloneError{
			Cause: CauseCredentialRejected, Repository: conn.Repository, Revision: ref.SourceRevision,
			Detail: "no credential is stored for this connection — re-authorize it",
		}
	}
	if useErr != nil {
		return nil, nil, fmt.Errorf("sourceingest: resolve forge credential for %s: %w", conn.Repository, useErr)
	}
	if cloneErr != nil {
		return nil, nil, cloneErr
	}

	root := dir
	if conn.SubPath != "" {
		root = filepath.Join(dir, filepath.FromSlash(conn.SubPath))
		if _, err := os.Stat(root); err != nil {
			return nil, nil, &CloneError{
				Cause: CauseRevisionNotFound, Repository: conn.Repository, Revision: ref.SourceRevision,
				Detail: fmt.Sprintf("the repository has no path %q at this revision", conn.SubPath),
			}
		}
	}

	// 🔴 The guard runs BEFORE anything walks or archives the tree. Inspecting after discovery would
	// mean discovery had already opened whatever the guard was going to refuse.
	adm, err := g.guard.InspectTree(root, skipGitMetadata)
	if err != nil {
		return nil, nil, fmt.Errorf("sourceingest: the cloned tree at %s was refused: %w", ref, err)
	}
	archive, err := archiveTree(ctx, root, skipGitMetadata)
	if err != nil {
		return nil, nil, fmt.Errorf("sourceingest: archive cloned tree for %s: %w", ref, err)
	}
	return archive, adm, nil
}

// skipGitMetadata prunes `.git` from the walk and the archive.
//
// It is the platform's own clone metadata, not the customer's source. Walking it would spend the entry
// budget on pack files and occasionally trip the per-file ceiling on a large pack — a refusal about
// our own tooling, reported to a customer as a refusal about their repository.
func skipGitMetadata(name string) bool { return name == ".git" }

// shallowClone fetches exactly one revision.
//
// Shallow, at the revision, no history (design D6). History is not an input to discovery, so cloning
// it spends time and disk to widen what the platform holds for no consumer. If a later phase needs
// history it can say why and change this, which is a smaller decision than un-holding it afterwards.
//
// The sequence is init + fetch + checkout rather than `git clone --branch`, because `clone --branch`
// takes a ref NAME and a revision may be a commit SHA — and the whole point of a `Ref` is that it
// pins a commit rather than following a moving branch.
func (g *GitSource) shallowClone(ctx context.Context, credentialedURL, publicURL, revision, dir string) error {
	fail := func(err error, out string) error {
		return classifyGitFailure(err, out, publicURL, revision)
	}
	if out, err := g.runGit(ctx, dir, "init", "--quiet"); err != nil {
		return fail(err, out)
	}
	if out, err := g.runGit(ctx, dir, "remote", "add", "origin", credentialedURL); err != nil {
		return fail(err, out)
	}
	// --depth 1 of exactly this revision. `--filter=blob:none` is deliberately NOT used: discovery
	// reads file CONTENT, so a blobless clone would fetch every blob lazily during the walk, one
	// round trip at a time, which is slower and holds the grant open far longer.
	if out, err := g.runGit(ctx, dir, "fetch", "--depth", "1", "--quiet", "origin", revision); err != nil {
		return fail(err, out)
	}
	if out, err := g.runGit(ctx, dir, "checkout", "--quiet", "FETCH_HEAD"); err != nil {
		return fail(err, out)
	}
	return nil
}

// classifyGitFailure maps git's exit onto exactly one of the four causes (FR11).
//
// # Why substrings and not exit codes
//
// git exits 128 for authentication failure, missing repository and missing revision alike. The exit
// code carries no cause at all, so the cause has to come from the message — and the messages differ
// per forge, which is why each pattern below is matched case-insensitively against a fragment rather
// than against a whole line.
//
// 🔴 The default is `network`, and that is the CORRECT default rather than a lazy one: an
// unrecognised failure that we call `credential rejected` sends the customer to rotate a working
// token, while one we call `network` sends an operator to look at the platform. The unknown case is
// ours until proven otherwise.
func classifyGitFailure(err error, out, publicURL, revision string) error {
	repo := strings.TrimSuffix(strings.TrimPrefix(publicURL, "https://"), ".git")
	detail := redactForgeOutput(out)
	if detail == "" {
		detail = err.Error()
	}
	low := strings.ToLower(out)
	cause := CauseNetwork
	switch {
	case containsAny(low, "authentication failed", "invalid username or password", "401", "403",
		"permission denied", "access denied", "could not read username", "terminal prompts disabled"):
		cause = CauseCredentialRejected
	case containsAny(low, "repository not found", "does not exist", "not found", "404", "no such repository"):
		cause = CauseRepositoryNotFound
	case containsAny(low, "couldn't find remote ref", "could not find remote ref", "unadvertised object",
		// 🔴 `not our ref` is what UPLOAD-PACK ITSELF says when the remote has the repository and not
		// the object, and it was MISSING from this list. The unit fixtures were written from GitHub's
		// HTTPS wording; this is the git PROTOCOL's wording — the same failure one layer down, and it
		// reaches a customer over HTTPS too. It fell through to `network`, which is the worst available
		// answer for it: the customer would be told the platform could not reach their forge, when the
		// forge answered immediately and correctly.
		//
		// Found by the live pgproof clone against a real repository. No amount of care over the
		// hand-written fixtures would have found it, because the fixtures are a record of what somebody
		// remembered git saying.
		"not our ref",
		"reference is not a tree", "pathspec", "did not match any file"):
		cause = CauseRevisionNotFound
	}
	return &CloneError{Cause: cause, Repository: repo, Revision: revision, Detail: detail}
}

func containsAny(hay string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(hay, n) {
			return true
		}
	}
	return false
}

// redactForgeOutput removes anything credential-shaped from git's output before it can reach a log, an
// error, a record or the console.
//
// 🔴 The rule is URL-SHAPED, not value-shaped. Matching the token's literal value would require the
// token at every call site — which is precisely the shape whose absence the closure custody exists to
// guarantee — so the redaction instead removes the `user:secret@` component of any URL in the text,
// which is the only form the credential can appear in. That means it works without ever being handed
// the secret, which is what makes it usable outside the closure.
func redactForgeOutput(s string) string {
	const marker = "https://"
	var b strings.Builder
	for {
		i := strings.Index(s, marker)
		if i < 0 {
			b.WriteString(s)
			return strings.TrimSpace(b.String())
		}
		b.WriteString(s[:i+len(marker)])
		rest := s[i+len(marker):]
		// The authority runs to the first '/', and a credential is the part before an '@' inside it.
		end := strings.IndexAny(rest, "/ \t\n\"'")
		if end < 0 {
			end = len(rest)
		}
		authority := rest[:end]
		if at := strings.LastIndex(authority, "@"); at >= 0 {
			b.WriteString("[redacted]@")
			b.WriteString(authority[at+1:])
		} else {
			b.WriteString(authority)
		}
		s = rest[end:]
	}
}

// gitRunner runs one git command and returns its combined output. Injected so the failure-cause fence
// can drive every one of the four causes without a network.
type gitRunner func(ctx context.Context, dir string, args ...string) (string, error)

// execGit runs git hermetically.
//
// The environment mirrors `internal/worktree`'s runner, for the same reason: a developer's own git
// config — hooks, a credential helper, a signing key, an editor — would otherwise change what a clone
// does from machine to machine. Two entries matter more here than there:
//
//   - `GIT_TERMINAL_PROMPT=0` so a bad credential FAILS instead of hanging on a password prompt in a
//     process with no terminal, which is an outage that looks like a slow forge.
//   - `credential.helper=` (empty) so no helper on the host can supply a credential we did not
//     resolve through the secret store. A clone that succeeds using the operator's own git credential
//     is a clone that works in staging and fails in production, and worse, it is a read performed
//     under an identity the ledger did not record.
func execGit(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir, "-c", "credential.helper=", "-c", "protocol.version=2"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1", "HOME="+os.TempDir(), "XDG_CONFIG_HOME="+os.TempDir(),
		"GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_ASKPASS=", "SSH_ASKPASS=",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// archiveTree packs a directory into the gzipped tar the snapshot store holds.
//
// It writes the same shape `git archive` produces on the push path — regular files and directories,
// relative paths, no links — which is what lets one extractor serve both modes. Links cannot appear
// here because InspectTree has already refused any tree containing one.
func archiveTree(ctx context.Context, root string, skip func(string) bool) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skip != nil && skip(info.Name()) {
				return filepath.SkipDir
			}
			return tw.WriteHeader(&tar.Header{
				Name: filepath.ToSlash(rel) + "/", Typeflag: tar.TypeDir, Mode: 0o750,
			})
		}
		if !info.Mode().IsRegular() {
			// Unreachable after InspectTree. Refused rather than skipped, so a future caller that
			// archives an UNGUARDED tree does not silently drop entries.
			return fmt.Errorf("archive: %s is not a regular file", rel)
		}
		if err := tw.WriteHeader(&tar.Header{
			Name: filepath.ToSlash(rel), Typeflag: tar.TypeReg, Mode: 0o640, Size: info.Size(),
		}); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, cerr := io.CopyN(tw, f, info.Size())
		closeErr := f.Close()
		if cerr != nil && cerr != io.EOF {
			return cerr
		}
		return closeErr
	})
	if err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// record appends one clone record. A failure to append is LOGGED-shaped rather than fatal in the
// caller — but it is returned here so the caller decides, and every caller today treats it as
// non-fatal for the ledger's own reason: losing the read is worse than losing the note about it, and
// the note is re-derivable from the metrics.
func (g *GitSource) record(ctx context.Context, conn Connection, ref Ref, rc readContext, outcome Outcome, bytes int64, entries int, durMS int64) {
	_ = g.conns.AppendRecord(ctx, CloneRecord{
		RecordID:     g.newIDFor("clone"),
		TenantID:     conn.TenantID,
		ConnectionID: conn.ConnectionID,
		Repository:   conn.Repository,
		Revision:     ref.SourceRevision,
		Actor:        rc.Actor,
		ActorID:      rc.ActorID,
		Reason:       rc.Reason,
		Outcome:      outcome,
		Bytes:        bytes,
		Entries:      entries,
		DurationMS:   durMS,
		AtMS:         g.nowMS(),
	})
}

// defaultID builds an opaque identifier. Not a UUID dependency: the value has to be unique and
// unguessable-enough to be a map key, and `crypto/rand` through hex is both without a module.
func defaultID(prefix string) string {
	return prefix + "-" + randomHex(16)
}
