package sourceingest

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// bundle.go implements Source over a customer-pushed source bundle: a gzipped tar the customer uploads
// for one revision, which the platform extracts into a scratch directory and hands to discovery.
//
// # Why a pushed bundle rather than a clone from their remote
//
// Both were on the table. The bundle wins the first round for one reason that is not about effort: a
// clone needs a credential that can read the customer's repository, held by the platform, usable at any
// time, for any revision, without the customer present. That is a standing capability. A bundle is an
// act — the customer ran a command, for one revision, and can stop. The platform's ability to read their
// code is then bounded by what they pushed instead of by a token's scope.
//
// This is not an argument that clones are wrong; it is an argument about which one to build first when
// only one of them can be reviewed carefully today. Source is the interface a GitSource slots into.
//
// # An uploaded archive is hostile input
//
// This extractor takes bytes from outside the trust boundary and writes files to the platform's disk.
// Every unpacker that has ever been written has had to learn the same list, so the list is enforced here
// rather than assumed: no absolute paths, no `..` escape, no symlinks or hardlinks, no device nodes, and
// hard caps on entry count, per-file size and total uncompressed bytes so a 2 KiB upload cannot become a
// full disk. Each rule has a test in bundle_test.go that constructs the malicious archive and asserts the
// refusal — the rules are load-bearing, so they are exercised, not documented.
//
// 🔴 The symlink rule is a REFUSAL, not a sanitization. A tempting alternative is to accept symlinks and
// resolve them inside the extraction root, which sounds equivalent and is not: discovery walks the tree
// afterwards, and a link that resolves safely at extraction time can be made to resolve elsewhere by a
// later entry in the same archive that replaces a parent directory. Refusing the entry outright removes
// the ordering question entirely. A source tree that genuinely needs a symlink to be discovered is a case
// we have never seen and would rather hear about than silently mishandle.

// Extraction limits. Deliberately generous for real repositories and still far below "fills the disk".
// They are constants rather than configuration because a per-tenant override is a way for the limit to be
// raised to whatever the incident needed, one tenant at a time, until it protects nobody.
const (
	// MaxBundleEntries caps files+directories in one bundle.
	MaxBundleEntries = 200_000
	// MaxBundleFileBytes caps a single extracted file at 64 MiB. Source files are kilobytes; anything
	// this large is a vendored binary or an attack, and neither is discoverable.
	MaxBundleFileBytes = 64 << 20
	// MaxBundleTotalBytes caps total uncompressed bytes at 2 GiB — the decompression-bomb ceiling.
	MaxBundleTotalBytes = 2 << 30
	// MaxBundlePathLength caps a single entry's path.
	MaxBundlePathLength = 4096
)

// BundleStore holds the pushed bundles. Reads return ErrNoSource when the snapshot is absent, so the
// distinction between "not opted in" and "store failure" survives all the way from SQL to the console.
type BundleStore interface {
	// Put records a bundle's bytes for a ref, replacing any bundle previously stored for the same ref.
	// Replace rather than append, for migration 0021's reason: a customer re-pushing the same revision
	// has not produced a second snapshot, and two rows for one revision makes "which tree was this graph
	// discovered from" unanswerable.
	Put(ctx context.Context, ref Ref, data []byte) error
	// Open returns the bundle bytes for a ref, or ErrNoSource if none was pushed.
	Open(ctx context.Context, ref Ref) (io.ReadCloser, error)
	// Delete removes a bundle. The customer-facing retraction: it is what makes "stop holding my source"
	// an operation rather than a support ticket.
	Delete(ctx context.Context, ref Ref) error
}

// BundleSource materializes source from pushed bundles.
type BundleSource struct {
	store BundleStore
	// scratch is the parent directory extractions happen under. Each Materialize gets a fresh child.
	scratch string
}

// NewBundleSource returns a Source over a bundle store, extracting under scratch.
func NewBundleSource(store BundleStore, scratch string) (*BundleSource, error) {
	if store == nil {
		return nil, fmt.Errorf("sourceingest: nil bundle store")
	}
	if scratch == "" {
		return nil, fmt.Errorf("sourceingest: no scratch directory")
	}
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		return nil, fmt.Errorf("sourceingest: scratch %s: %w", scratch, err)
	}
	return &BundleSource{store: store, scratch: scratch}, nil
}

// Materialize extracts the pushed bundle for ref into a fresh directory.
//
// The returned Release removes the whole extraction. A caller that forgets it leaves a customer's source
// on disk indefinitely; the orchestrator defers it and TestMaterializeReleaseRemovesTree asserts it.
func (b *BundleSource) Materialize(ctx context.Context, ref Ref) (Materialized, error) {
	if err := ref.Validate(); err != nil {
		return Materialized{}, err
	}
	rc, err := b.store.Open(ctx, ref)
	if err != nil {
		return Materialized{}, err // ErrNoSource passes through unwrapped by the store's contract
	}
	defer func() { _ = rc.Close() }()

	dir, err := os.MkdirTemp(b.scratch, "src-")
	if err != nil {
		return Materialized{}, fmt.Errorf("sourceingest: scratch dir for %s: %w", ref, err)
	}
	release := func() { _ = os.RemoveAll(dir) }

	if err := extractTarGz(ctx, rc, dir); err != nil {
		release()
		return Materialized{}, fmt.Errorf("sourceingest: extracting %s: %w", ref, err)
	}
	return Materialized{Dir: dir, Release: release}, nil
}

// extractTarGz unpacks a gzipped tar into dest, enforcing every rule in this file's header comment.
func extractTarGz(ctx context.Context, r io.Reader, dest string) error {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer func() { _ = zr.Close() }()

	// The extraction root, resolved. Compared against every destination path so that a rule can be
	// stated in terms of the real filesystem rather than in terms of string prefixes.
	root, err := filepath.Abs(dest)
	if err != nil {
		return fmt.Errorf("resolve dest: %w", err)
	}

	tr := tar.NewReader(zr)
	var entries int
	var total int64
	for {
		// An upload is attacker-controlled work; a cancelled request must stop doing it.
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}

		entries++
		if entries > MaxBundleEntries {
			return fmt.Errorf("bundle has more than %d entries", MaxBundleEntries)
		}
		if len(hdr.Name) > MaxBundlePathLength {
			return fmt.Errorf("entry path exceeds %d bytes", MaxBundlePathLength)
		}

		target, err := safeJoin(root, hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return fmt.Errorf("mkdir %s: %w", hdr.Name, err)
			}

		case tar.TypeReg:
			if hdr.Size > MaxBundleFileBytes {
				return fmt.Errorf("entry %s is %d bytes, over the %d limit", hdr.Name, hdr.Size, MaxBundleFileBytes)
			}
			if total+hdr.Size > MaxBundleTotalBytes {
				return fmt.Errorf("bundle exceeds %d uncompressed bytes", MaxBundleTotalBytes)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return fmt.Errorf("mkdir for %s: %w", hdr.Name, err)
			}
			n, err := writeFile(target, tr, hdr.Size)
			if err != nil {
				return fmt.Errorf("write %s: %w", hdr.Name, err)
			}
			total += n

		case tar.TypeSymlink, tar.TypeLink:
			// Refused outright — see this file's header comment for why resolving them is not equivalent.
			return fmt.Errorf("entry %s is a link (%q); bundles may not contain links", hdr.Name, hdr.Linkname)

		default:
			// Char devices, block devices, FIFOs, sockets. Nothing discoverable is any of these, and an
			// unpacker that skips unknown types quietly is how one of them eventually gets written.
			return fmt.Errorf("entry %s has unsupported type %q", hdr.Name, string(rune(hdr.Typeflag)))
		}
	}
}

// safeJoin resolves name under root and refuses anything that would land outside it.
//
// Both classic escapes are covered: an absolute path ("/etc/passwd") and a relative climb ("../../x").
// The check is on the CLEANED, joined result rather than on the input string — "a/../../b" contains no
// leading ".." and still escapes, so inspecting the raw name is not enough.
func safeJoin(root, name string) (string, error) {
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
	target := filepath.Join(root, filepath.Clean(name))
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("entry %s escapes the extraction root", name)
	}
	return target, nil
}

// writeFile writes exactly size bytes from r to path.
//
// io.CopyN with an exact count, not io.Copy: the tar header's Size is what the caps were checked
// against, so copying "until EOF" would let a body longer than its header claims write past a limit that
// was already approved. The short-read check catches the opposite lie.
func writeFile(path string, r io.Reader, size int64) (int64, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return 0, err
	}
	n, err := io.CopyN(f, r, size)
	if err != nil && err != io.EOF {
		_ = f.Close()
		return n, err
	}
	if n != size {
		_ = f.Close()
		return n, fmt.Errorf("short read: header declared %d bytes, got %d", size, n)
	}
	// 🔴 Close is CHECKED, not deferred-and-ignored. On a buffered or network-backed filesystem the
	// write errors surface here and nowhere else, so a discarded Close error means this function
	// reports "exactly size bytes landed" about a file that is short or absent — and discovery then
	// analyses a truncated tree as though it were the customer's workflow. errcheck flagged this as a
	// style issue; it is a correctness one.
	if err := f.Close(); err != nil {
		return n, fmt.Errorf("close after writing %d bytes: %w", n, err)
	}
	return n, nil
}
