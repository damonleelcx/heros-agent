package sourceingest

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
// Every unpacker that has ever been written has had to learn the same list — no absolute paths, no
// `..` escape, no symlinks or hardlinks, no device nodes, and hard caps on entry count, per-file size
// and total uncompressed bytes so a 2 KiB upload cannot become a full disk.
//
// 🔴 That list NO LONGER LIVES HERE. It is `TreeGuard` in treeguard.go, and the clone path runs the
// same one — because the defences exist because a *tree* can be malicious, not because an *upload*
// can (P32 design D2). What remains in this file is tar-specific: gzip framing, the pax-header
// classification, and the exact-count copy. Every rule has a test in bundle_test.go that constructs the
// malicious archive and asserts the refusal, and that suite is the characterization fence the
// extraction was moved under — it passes unchanged before and after.

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

// extractTarGz unpacks a gzipped tar into dest, running every entry past the shared TreeGuard.
//
// What is left here after the guard was factored out is exactly the tar-specific half: gzip framing,
// the typeflag→EntryKind classification, and the exact-count copy. The refusals themselves are in
// treeguard.go and the clone path runs the same ones.
func extractTarGz(ctx context.Context, r io.Reader, dest string) error {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer func() { _ = zr.Close() }()

	adm, err := NewTreeGuard().Admit(dest)
	if err != nil {
		return err
	}

	tr := tar.NewReader(zr)
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

		target, err := adm.Entry(tarEntryOf(hdr))
		if errors.Is(err, ErrSkipEntry) {
			continue
		}
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return fmt.Errorf("mkdir %s: %w", hdr.Name, err)
			}

		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return fmt.Errorf("mkdir for %s: %w", hdr.Name, err)
			}
			if _, err := writeFile(target, tr, hdr.Size); err != nil {
				return fmt.Errorf("write %s: %w", hdr.Name, err)
			}

		default:
			// Unreachable: adm.Entry refuses or skips every other typeflag. It is a `default` rather
			// than the absent arm of an exhaustive switch because "unreachable" is a claim about the
			// guard, and a guard that is later relaxed must not silently start writing here.
			return fmt.Errorf("entry %s has unsupported type %q", hdr.Name, string(rune(hdr.Typeflag)))
		}
	}
}

// tarEntryOf classifies one tar header into the guard's vocabulary.
//
// 🔴 A pax header is METADATA, not a filesystem entry. `git archive` writes `pax_global_header`
// (typeflag 'g') at the head of every archive of a repository that has a commit — so without this,
// `push-source` failed on EVERY repository, with `entry pax_global_header has unsupported type "g"`:
// the default arm classifying a comment as a device node. Classifying it as EntryMetadata writes
// nothing and relaxes no rule; the extended-header form ('x') carries per-entry metadata Go's tar
// reader has already folded into the NEXT header by the time it is returned, so it is likewise nothing
// to unpack.
func tarEntryOf(hdr *tar.Header) Entry {
	e := Entry{Path: hdr.Name, RawKind: string(rune(hdr.Typeflag))}
	switch hdr.Typeflag {
	case tar.TypeXGlobalHeader, tar.TypeXHeader:
		e.Kind = EntryMetadata
	case tar.TypeDir:
		e.Kind = EntryDir
	case tar.TypeReg:
		e.Kind = EntryFile
		e.Size = hdr.Size
	case tar.TypeSymlink, tar.TypeLink:
		e.Kind = EntryLink
		e.LinkTarget = hdr.Linkname
	default:
		// Char devices, block devices, FIFOs, sockets — EntryOther is the zero value, so this arm is
		// the explicit statement of what the zero value means rather than a silent fallthrough.
		e.Kind = EntryOther
	}
	return e
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
