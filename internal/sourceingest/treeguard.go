package sourceingest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// treeguard.go is the hostile-input refusal set, factored out of bundle.go so that BOTH source
// implementations run it (P32 design D2).
//
// # Why this is a separate step and not a second copy of the rules
//
// The defences exist because a *tree* can be malicious, not because an *upload* can. Git delivers
// whatever the repository contains, and a repository can hold a symlink to `/etc/shadow` as easily as
// a tar can. The reasoning that would weaken this — "it came from GitHub, so it is a real repository" —
// confuses the transport's trustworthiness with the payload's.
//
// The alternative — write the same nine refusals again inside the clone path — was rejected for the
// reason every duplicated security rule is eventually rejected: the copies do not drift together. The
// bundle path's rules have been amended twice already (the pax-header skip, the checked Close), and a
// second copy would have received neither amendment and would have looked correct the whole time.
//
// # Why a per-traversal Admission rather than methods on TreeGuard
//
// Three of the ceilings are CUMULATIVE — entry count and total bytes are properties of the traversal,
// not of an entry — so the counters have to live somewhere. Putting them on TreeGuard would make one
// guard usable by exactly one traversal at a time, and two concurrent ingests would consume each
// other's budget. Admission is the traversal; TreeGuard is the policy, and it is safe to share.
//
// # 🔴 What was deliberately NOT changed while moving this
//
// The ORDER of the checks, the exact refusal wording the characterization suite asserts, and the pax
// skip's position AFTER the entry-count and path-length checks. `bundle_test.go` is the fence for this
// refactor: it constructs each malicious archive by hand and asserts each refusal, and it must pass
// unchanged before and after. A refactor of security-load-bearing code that also "improves" the
// messages is a refactor whose fence cannot tell the two changes apart.
//
// The two limit messages that used to say "bundle" now say "source tree", because they are now reached
// from a clone as well and telling a customer their *bundle* is too large when they connected a
// repository is a message that sends them to the wrong remedy. No test asserted those two strings; the
// nine that are asserted are byte-identical.

// Extraction limits. Deliberately generous for real repositories and still far below "fills the disk".
// They are constants rather than configuration because a per-tenant override is a way for the limit to
// be raised to whatever the incident needed, one tenant at a time, until it protects nobody.
//
// The `Bundle` names are kept — they are referenced by `bundle_test.go` and by the API layer — even
// though the limits now govern the clone path too. Renaming them would be a churn commit across the
// characterization suite this refactor is fenced by, which is the one commit that must stay legible.
const (
	// MaxBundleEntries caps files+directories in one source tree.
	MaxBundleEntries = 200_000
	// MaxBundleFileBytes caps a single extracted file at 64 MiB. Source files are kilobytes; anything
	// this large is a vendored binary or an attack, and neither is discoverable.
	MaxBundleFileBytes = 64 << 20
	// MaxBundleTotalBytes caps total uncompressed bytes at 2 GiB — the decompression-bomb ceiling.
	MaxBundleTotalBytes = 2 << 30
	// MaxBundlePathLength caps a single entry's path.
	MaxBundlePathLength = 4096
)

// ErrSkipEntry reports an entry that is METADATA rather than a filesystem entry: it is neither accepted
// nor refused, and the caller writes nothing for it.
//
// 🔴 It is a distinct outcome rather than "accepted with an empty target" because those two are exactly
// what the pax-header defect confused. `git archive` writes `pax_global_header` at the head of every
// archive of a repository that has a commit; classifying it as a filesystem entry made `push-source`
// fail on EVERY repository with `entry pax_global_header has unsupported type "g"`. A caller that
// receives ErrSkipEntry cannot accidentally create a file for it, because there is no target to create.
var ErrSkipEntry = errors.New("sourceingest: entry is archive metadata, not a filesystem entry")

// EntryKind is the closed vocabulary of what a tree entry can be.
//
// Closed on purpose: the refusal for "anything else" is the default arm, and an open vocabulary is how
// a device node eventually arrives classified as something benign.
type EntryKind uint8

const (
	// EntryOther is anything the caller could not classify — device nodes, FIFOs, sockets. Refused.
	// It is the ZERO VALUE deliberately: an Entry built without setting Kind is refused rather than
	// admitted, so a forgotten field fails closed.
	EntryOther EntryKind = iota
	// EntryDir is a directory.
	EntryDir
	// EntryFile is a regular file.
	EntryFile
	// EntryLink is a symlink or hardlink. Refused — see TreeGuard's note on why resolving is not
	// equivalent to refusing.
	EntryLink
	// EntryMetadata is archive metadata carrying no filesystem entry (tar pax headers). Skipped.
	EntryMetadata
)

// Entry is one candidate entry, built from a tar header or from a directory walk.
//
// The two producers fill it from different sources and that is the whole point: the REFUSALS do not
// know which one they are looking at, so neither producer can be given a weaker set.
type Entry struct {
	// Path is the entry's path relative to the tree root, as the producer found it. NOT cleaned or
	// normalised — the guard's escape check has to see what was actually written.
	Path string
	// Kind classifies the entry.
	Kind EntryKind
	// Size is the declared size of a regular file, in bytes. Read from the header on the bundle path,
	// so the per-file ceiling refuses BEFORE the bytes are copied.
	Size int64
	// LinkTarget is a link's target, carried only so the refusal can name it. Never followed.
	LinkTarget string
	// RawKind is the producer's own name for an unclassifiable type, carried into the refusal so an
	// operator sees what actually arrived rather than "other".
	RawKind string
}

// TreeGuard is the refusal policy: no absolute paths, no `..` escape, no symlinks or hardlinks, no
// device nodes, and the entry, per-file and total-bytes ceilings.
//
// 🔴 The symlink rule is a REFUSAL, not a sanitization. A tempting alternative is to accept symlinks
// and resolve them inside the root, which sounds equivalent and is not: discovery walks the tree
// afterwards, and a link that resolves safely at admission time can be made to resolve elsewhere by a
// later entry that replaces a parent directory. Refusing the entry outright removes the ordering
// question entirely. A source tree that genuinely needs a symlink to be discovered is a case we have
// never seen and would rather hear about than silently mishandle.
type TreeGuard struct {
	maxEntries    int
	maxFileBytes  int64
	maxTotalBytes int64
	maxPathLength int
}

// NewTreeGuard returns the guard with the shipped ceilings. Both implementations construct it this way;
// there is no constructor that takes limits, because a limit a caller can supply is a limit an incident
// can raise.
func NewTreeGuard() *TreeGuard {
	return &TreeGuard{
		maxEntries:    MaxBundleEntries,
		maxFileBytes:  MaxBundleFileBytes,
		maxTotalBytes: MaxBundleTotalBytes,
		maxPathLength: MaxBundlePathLength,
	}
}

// Admission is one guarded traversal. It holds the cumulative counters, so two concurrent ingests do
// not consume each other's budget.
type Admission struct {
	g *TreeGuard
	// root is the resolved extraction root. Every destination is compared against it, so the escape
	// rule is stated in terms of the real filesystem rather than in terms of string prefixes.
	root    string
	entries int
	total   int64
}

// Admit begins a guarded traversal rooted at root.
func (g *TreeGuard) Admit(root string) (*Admission, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve dest: %w", err)
	}
	return &Admission{g: g, root: abs}, nil
}

// Root reports the resolved root this admission is scoped to.
func (a *Admission) Root() string { return a.root }

// Entries and Bytes report what the traversal has admitted so far. Read by the metrics the ingest
// surface publishes, so "how large was that tree" is a number rather than an impression.
func (a *Admission) Entries() int { return a.entries }

// Bytes reports the total admitted regular-file bytes.
func (a *Admission) Bytes() int64 { return a.total }

// Entry applies every refusal to one entry and returns where it may land.
//
// Returns ErrSkipEntry for archive metadata. The ORDER below is the order bundle.go enforced before the
// extraction: count, path length, metadata skip, path safety, then type and size. It is preserved
// exactly — a metadata entry with a 5 KiB name is still refused for its path length, which is what
// putting the skip earlier would have quietly changed.
func (a *Admission) Entry(e Entry) (string, error) {
	a.entries++
	if a.entries > a.g.maxEntries {
		return "", fmt.Errorf("source tree has more than %d entries", a.g.maxEntries)
	}
	if len(e.Path) > a.g.maxPathLength {
		return "", fmt.Errorf("entry path exceeds %d bytes", a.g.maxPathLength)
	}
	if e.Kind == EntryMetadata {
		return "", ErrSkipEntry
	}

	target, err := a.safeJoin(e.Path)
	if err != nil {
		return "", err
	}

	switch e.Kind {
	case EntryDir:
		return target, nil
	case EntryFile:
		if e.Size > a.g.maxFileBytes {
			return "", fmt.Errorf("entry %s is %d bytes, over the %d limit", e.Path, e.Size, a.g.maxFileBytes)
		}
		if a.total+e.Size > a.g.maxTotalBytes {
			return "", fmt.Errorf("source tree exceeds %d uncompressed bytes", a.g.maxTotalBytes)
		}
		a.total += e.Size
		return target, nil
	case EntryLink:
		// Refused outright — see TreeGuard's note on why resolving is not equivalent.
		return "", fmt.Errorf("entry %s is a link (%q); source trees may not contain links", e.Path, e.LinkTarget)
	default:
		// Char devices, block devices, FIFOs, sockets. Nothing discoverable is any of these, and a
		// traversal that skips unknown types quietly is how one of them eventually gets written.
		return "", fmt.Errorf("entry %s has unsupported type %q", e.Path, e.RawKind)
	}
}

// safeJoin resolves name under the root and refuses anything that would land outside it.
//
// Both classic escapes are covered: an absolute path ("/etc/passwd") and a relative climb ("../../x").
// The check is on the CLEANED, joined result rather than on the input string — "a/../../b" contains no
// leading ".." and still escapes, so inspecting the raw name is not enough.
func (a *Admission) safeJoin(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("entry has an empty path")
	}
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("entry %s is an absolute path", name)
	}
	// Windows-style volume/backslash paths, which filepath.IsAbs does not catch on unix hosts.
	if strings.Contains(name, `\`) || strings.Contains(name, ":") {
		return "", fmt.Errorf("entry %s contains a volume or backslash separator", name)
	}
	target := filepath.Join(a.root, filepath.Clean(name))
	if target != a.root && !strings.HasPrefix(target, a.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("entry %s escapes the extraction root", name)
	}
	return target, nil
}

// InspectTree runs the guard over a tree that ALREADY EXISTS on disk — the clone path's shape.
//
// # Why the clone path inspects rather than extracts
//
// `git clone` writes the worktree itself; there is no entry-by-entry hook to refuse at. So the tree is
// materialized into a scratch directory the platform owns, then inspected, and a refusal aborts the
// ingest before discovery walks it. That ordering is stated because the alternative reads as equivalent
// and is not: inspecting AFTER discovery would mean discovery had already opened whatever the guard
// was going to refuse.
//
// # 🔴 Lstat, never Stat
//
// A symlink is found by NOT following it. `filepath.WalkDir` hands back a `DirEntry` whose `Type()`
// comes from lstat, which is the property that makes the symlink refusal reachable here at all — a
// walk that resolved links would see the target's type and admit a link to `/etc/shadow` as a regular
// file.
//
// skip reports directory names to prune without walking into them. `.git` is the only one the clone
// path passes: it is the platform's own clone metadata, it is not the customer's source, and walking
// it would spend the entry budget on pack files and occasionally trip the per-file ceiling on a large
// pack — a refusal about our own tooling reported as a refusal about their repository.
func (g *TreeGuard) InspectTree(root string, skip func(name string) bool) (*Admission, error) {
	adm, err := g.Admit(root)
	if err != nil {
		return nil, err
	}
	walkErr := filepath.WalkDir(adm.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == adm.root {
			return nil // the root itself is not an entry
		}
		rel, relErr := filepath.Rel(adm.root, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() && skip != nil && skip(d.Name()) {
			return filepath.SkipDir
		}
		e := Entry{Path: filepath.ToSlash(rel)}
		mode := d.Type()
		switch {
		case mode&fs.ModeSymlink != 0:
			e.Kind = EntryLink
			if tgt, lerr := os.Readlink(path); lerr == nil {
				e.LinkTarget = tgt
			}
		case d.IsDir():
			e.Kind = EntryDir
		case mode.IsRegular():
			e.Kind = EntryFile
			info, ierr := d.Info()
			if ierr != nil {
				return ierr
			}
			e.Size = info.Size()
		default:
			e.Kind = EntryOther
			e.RawKind = mode.String()
		}
		if _, gerr := adm.Entry(e); gerr != nil {
			if errors.Is(gerr, ErrSkipEntry) {
				return nil
			}
			return gerr
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return adm, nil
}
