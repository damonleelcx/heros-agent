package sourceingest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// bundle_test.go builds the hostile archives by hand and asserts each one is refused.
//
// These are not "does the happy path work" tests with a security flavour. Each constructs the specific
// artifact an attacker would upload — an entry that climbs out of the root, a symlink aimed at /etc, a
// body longer than its own header — and fails if extraction accepts it. The limits in bundle.go are
// load-bearing, and a limit nothing exercises is a comment.

// memBundleStore is an in-memory BundleStore for tests.
type memBundleStore struct{ data map[string][]byte }

func newMemBundleStore() *memBundleStore { return &memBundleStore{data: map[string][]byte{}} }

func (m *memBundleStore) key(r Ref) string {
	return r.TenantID + "\x00" + r.WorkflowID + "\x00" + r.SourceRevision
}

func (m *memBundleStore) Put(_ context.Context, ref Ref, data []byte) error {
	m.data[m.key(ref)] = append([]byte(nil), data...)
	return nil
}

func (m *memBundleStore) Open(_ context.Context, ref Ref) (io.ReadCloser, error) {
	b, ok := m.data[m.key(ref)]
	if !ok {
		return nil, ErrNoSource
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *memBundleStore) Delete(_ context.Context, ref Ref) error {
	delete(m.data, m.key(ref))
	return nil
}

// tarEntry is one entry to write into a test archive.
type tarEntry struct {
	name     string
	body     string
	typeflag byte
	linkname string
}

// makeTarGz builds a gzipped tar from entries, writing headers verbatim so a test can express an
// archive that a well-behaved tar writer would never produce.
func makeTarGz(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		flag := e.typeflag
		if flag == 0 {
			flag = tar.TypeReg
		}
		size := int64(len(e.body))
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     0o640,
			Size:     size,
			Typeflag: flag,
			Linkname: e.linkname,
		}
		if flag == tar.TypeDir {
			hdr.Size = 0
			hdr.Mode = 0o750
		}
		if flag == tar.TypeSymlink || flag == tar.TypeLink {
			hdr.Size = 0
		}
		// A pax global header carries records, not a body. `git archive` writes one comment record holding
		// the commit id; Go's writer encodes the same bytes from PAXRecords and refuses a hand-set body,
		// so the entry is expressed the way the format defines it rather than the way the struct allows.
		if flag == tar.TypeXGlobalHeader {
			hdr.Size = 0
			hdr.Mode = 0
			hdr.Format = tar.FormatPAX
			hdr.PAXRecords = map[string]string{"comment": e.body}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %s: %v", e.name, err)
		}
		if flag == tar.TypeReg && e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("write body %s: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// rawTarGz assembles tar headers BY HAND so a test can declare a size the body does not honour.
//
// archive/tar refuses to write such a header ("missed writing N bytes"), which is correct of it and
// makes it useless here: the archives worth testing are the ones a well-behaved writer cannot produce.
// A real attacker writes the bytes directly, so the test does too.
func rawTarGz(t *testing.T, name string, declaredSize int64, body string) []byte {
	t.Helper()
	var hdr [512]byte
	copy(hdr[0:100], name)
	copy(hdr[100:108], "0000640\x00")           // mode
	copy(hdr[108:116], "0000000\x00")           // uid
	copy(hdr[116:124], "0000000\x00")           // gid
	copy(hdr[124:136], octal(declaredSize, 11)) // size
	copy(hdr[136:148], octal(0, 11))            // mtime
	hdr[156] = tar.TypeReg                      // typeflag
	copy(hdr[257:263], "ustar\x00")             // magic
	copy(hdr[263:265], "00")                    // version

	// Checksum is computed with the checksum field itself read as spaces.
	for i := 148; i < 156; i++ {
		hdr[i] = ' '
	}
	var sum int64
	for _, b := range hdr {
		sum += int64(b)
	}
	copy(hdr[148:154], octal(sum, 6))
	hdr[154] = 0
	hdr[155] = ' '

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	// Checked, not ignored: a short write here would produce a MALFORMED archive, and the extractor
	// would then refuse it for the wrong reason — the test would still pass, while proving nothing
	// about the rule it names.
	write := func(b []byte) {
		if _, err := zw.Write(b); err != nil {
			t.Fatalf("write archive: %v", err)
		}
	}
	write(hdr[:])
	write([]byte(body))
	// Pad the body to a 512-byte boundary, then the two empty blocks that end an archive.
	if pad := (512 - len(body)%512) % 512; pad > 0 {
		write(make([]byte, pad))
	}
	write(make([]byte, 1024))
	if err := zw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// octal renders n as a NUL-terminated octal field of the given digit width, as tar requires.
func octal(n int64, digits int) string {
	s := strconv.FormatInt(n, 8)
	for len(s) < digits {
		s = "0" + s
	}
	return s + "\x00"
}

// materializeBundle pushes an archive and materializes it, returning the extraction dir and error.
func materializeBundle(t *testing.T, archive []byte) (Materialized, error) {
	t.Helper()
	store := newMemBundleStore()
	ref := Ref{TenantID: "t1", WorkflowID: "wf1", SourceRevision: "rev1"}
	if err := store.Put(context.Background(), ref, archive); err != nil {
		t.Fatalf("put: %v", err)
	}
	src, err := NewBundleSource(store, t.TempDir())
	if err != nil {
		t.Fatalf("new source: %v", err)
	}
	return src.Materialize(context.Background(), ref)
}

func TestMaterializeExtractsAWellFormedBundle(t *testing.T) {
	archive := makeTarGz(t,
		tarEntry{name: "pkg", typeflag: tar.TypeDir},
		tarEntry{name: "pkg/main.go", body: "package main\n"},
		tarEntry{name: "go.mod", body: "module example.com/x\n"},
	)
	m, err := materializeBundle(t, archive)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	defer m.Release()

	got, err := os.ReadFile(filepath.Join(m.Dir, "pkg", "main.go"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(got) != "package main\n" {
		t.Errorf("extracted body = %q, want %q", got, "package main\n")
	}
}

func TestMaterializeReleaseRemovesTree(t *testing.T) {
	archive := makeTarGz(t, tarEntry{name: "a.go", body: "package a\n"})
	m, err := materializeBundle(t, archive)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	m.Release()
	if _, err := os.Stat(m.Dir); !os.IsNotExist(err) {
		t.Fatalf("extraction dir still present after Release: stat err = %v", err)
	}
	// Release is documented safe to call twice; a caller with both a defer and an explicit call is
	// normal, and a panic on the second call would only ever be found in production.
	m.Release()
}

func TestMaterializeMissingBundleIsErrNoSource(t *testing.T) {
	src, err := NewBundleSource(newMemBundleStore(), t.TempDir())
	if err != nil {
		t.Fatalf("new source: %v", err)
	}
	_, err = src.Materialize(context.Background(), Ref{TenantID: "t", WorkflowID: "w", SourceRevision: "r"})
	if !errors.Is(err, ErrNoSource) {
		t.Fatalf("err = %v, want ErrNoSource — a customer who has not pushed source must be "+
			"distinguishable from a broken platform", err)
	}
}

func TestMaterializeRejectsPartialRef(t *testing.T) {
	src, err := NewBundleSource(newMemBundleStore(), t.TempDir())
	if err != nil {
		t.Fatalf("new source: %v", err)
	}
	for _, ref := range []Ref{
		{WorkflowID: "w", SourceRevision: "r"},
		{TenantID: "t", SourceRevision: "r"},
		{TenantID: "t", WorkflowID: "w"},
	} {
		if _, err := src.Materialize(context.Background(), ref); err == nil {
			t.Errorf("Materialize(%+v) succeeded; a partial ref must be refused before it reaches a "+
				"tenant-keyed store", ref)
		}
	}
}

// --- the hostile archives ------------------------------------------------------------------------

func TestExtractionRefusesHostileEntries(t *testing.T) {
	cases := []struct {
		name    string
		entries []tarEntry
		want    string
	}{
		{
			name:    "relative climb out of the root",
			entries: []tarEntry{{name: "../escaped.go", body: "package x\n"}},
			want:    "escapes the extraction root",
		},
		{
			name: "climb hidden mid-path, with no leading dotdot",
			// The case that defeats a raw-string prefix check: nothing here starts with "..".
			entries: []tarEntry{{name: "a/b/../../../escaped.go", body: "package x\n"}},
			want:    "escapes the extraction root",
		},
		{
			name:    "absolute path",
			entries: []tarEntry{{name: "/etc/passwd", body: "root\n"}},
			want:    "absolute path",
		},
		{
			name:    "symlink",
			entries: []tarEntry{{name: "link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"}},
			want:    "may not contain links",
		},
		{
			name:    "hardlink",
			entries: []tarEntry{{name: "link", typeflag: tar.TypeLink, linkname: "/etc/passwd"}},
			want:    "may not contain links",
		},
		{
			name:    "fifo",
			entries: []tarEntry{{name: "pipe", typeflag: tar.TypeFifo}},
			want:    "unsupported type",
		},
		{
			name:    "char device",
			entries: []tarEntry{{name: "dev", typeflag: tar.TypeChar}},
			want:    "unsupported type",
		},
		{
			name:    "empty path",
			entries: []tarEntry{{name: "", body: "x"}},
			want:    "empty path",
		},
		{
			name:    "backslash separator",
			entries: []tarEntry{{name: `a\b.go`, body: "package x\n"}},
			want:    "backslash separator",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := materializeBundle(t, makeTarGz(t, tc.entries...))
			if err == nil {
				m.Release()
				t.Fatalf("extraction ACCEPTED a hostile archive (%s)", tc.name)
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestEscapedFileIsNotWritten checks the outcome rather than the message: a traversal that is reported
// as an error but has already written the file is not a refusal.
func TestEscapedFileIsNotWritten(t *testing.T) {
	scratch := t.TempDir()
	// A canary next to the extraction root — where "../escaped.go" would land.
	canary := filepath.Join(scratch, "escaped.go")

	store := newMemBundleStore()
	ref := Ref{TenantID: "t1", WorkflowID: "wf1", SourceRevision: "rev1"}
	if err := store.Put(context.Background(), ref, makeTarGz(t,
		tarEntry{name: "ok.go", body: "package a\n"},
		tarEntry{name: "../escaped.go", body: "package evil\n"},
	)); err != nil {
		t.Fatalf("put: %v", err)
	}
	src, err := NewBundleSource(store, scratch)
	if err != nil {
		t.Fatalf("new source: %v", err)
	}
	if m, err := src.Materialize(context.Background(), ref); err == nil {
		m.Release()
		t.Fatal("traversal accepted")
	}
	if _, err := os.Stat(canary); !os.IsNotExist(err) {
		t.Fatalf("the escaping entry was WRITTEN to %s despite the error: stat err = %v", canary, err)
	}
}

// TestFailedExtractionLeavesNothingBehind: a refused bundle must not leave a half-extracted tree of
// customer source on the platform's disk. The entries before the hostile one were already written when
// the refusal happened, so this asserts the cleanup, not the ordering.
func TestFailedExtractionLeavesNothingBehind(t *testing.T) {
	scratch := t.TempDir()
	store := newMemBundleStore()
	ref := Ref{TenantID: "t1", WorkflowID: "wf1", SourceRevision: "rev1"}
	if err := store.Put(context.Background(), ref, makeTarGz(t,
		tarEntry{name: "first.go", body: "package a\n"},
		tarEntry{name: "link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
	)); err != nil {
		t.Fatalf("put: %v", err)
	}
	src, err := NewBundleSource(store, scratch)
	if err != nil {
		t.Fatalf("new source: %v", err)
	}
	if m, err := src.Materialize(context.Background(), ref); err == nil {
		m.Release()
		t.Fatal("hostile bundle accepted")
	}
	remaining, err := os.ReadDir(scratch)
	if err != nil {
		t.Fatalf("read scratch: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("scratch still holds %d extraction(s) after a refused bundle: %v", len(remaining), remaining)
	}
}

// TestBodyShorterThanDeclaredSizeIsRefused covers the lie writeFile's exact-count copy exists to catch:
// a header that claims a size the body does not deliver. Accepting it would leave a truncated file the
// extractor believes is complete, and discovery would then analyse a workflow that does not exist.
func TestBodyShorterThanDeclaredSizeIsRefused(t *testing.T) {
	m, err := materializeBundle(t, rawTarGz(t, "a.go", 1<<20, "short"))
	if err == nil {
		m.Release()
		t.Fatal("an entry whose body is shorter than its declared size was accepted")
	}
}

func TestOversizeEntryIsRefused(t *testing.T) {
	// Declared size over the per-file cap. The body is one byte — the header is what the check reads,
	// which is the point: the refusal happens before 64 MiB is ever copied.
	m, err := materializeBundle(t, rawTarGz(t, "big.bin", MaxBundleFileBytes+1, "x"))
	if err == nil {
		m.Release()
		t.Fatal("an entry over the per-file cap was accepted")
	}
	if !strings.Contains(err.Error(), "over the") {
		t.Errorf("err = %v, want it to name the size limit", err)
	}
}

func TestNonGzipInputIsRefused(t *testing.T) {
	m, err := materializeBundle(t, []byte("this is not a gzip stream"))
	if err == nil {
		m.Release()
		t.Fatal("non-gzip input accepted")
	}
	if !strings.Contains(err.Error(), "gzip") {
		t.Errorf("err = %v, want a gzip error", err)
	}
}

// TestAPaxGlobalHeaderIsSkippedRatherThanRefused pins the shape `git archive` actually writes.
//
// 🔴 This is a regression fence for a defect that made `push-source` fail on EVERY repository with a
// commit, not on some unusual one. `git archive` emits `pax_global_header` (typeflag 'g') as the first
// entry of the archive, carrying the commit id as a comment. Extraction classified it through the
// default branch — the one written for char devices and FIFOs — and answered
// `entry pax_global_header has unsupported type "g"`, which reached the customer as a 500 from
// `POST /api/v1/workflow-source-discovery`: an opaque failure about a device node, for a valid archive.
//
// The entry is asserted to leave NOTHING behind, because "skipped" and "written somewhere harmless" are
// different outcomes and only one of them is safe. The refusals around it are asserted in the same test
// so that a future relaxation of 'g' cannot quietly widen into 'x'-adjacent link handling: the symlink
// still has to be refused with the archive containing a pax header, or this fence would be evidence
// that the skip is narrow when it is not.
func TestAPaxGlobalHeaderIsSkippedRatherThanRefused(t *testing.T) {
	archive := makeTarGz(t,
		tarEntry{name: "pax_global_header", body: "bb612cbe4c1dbfa18b44a653c4338e9466a1a8ce",
			typeflag: tar.TypeXGlobalHeader},
		tarEntry{name: "src", typeflag: tar.TypeDir},
		tarEntry{name: "src/app.ts", body: "export const x = 1\n"},
	)
	m, err := materializeBundle(t, archive)
	if err != nil {
		t.Fatalf("an archive with a pax global header was refused: %v", err)
	}
	defer m.Release()

	got, err := os.ReadFile(filepath.Join(m.Dir, "src", "app.ts"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(got) != "export const x = 1\n" {
		t.Errorf("extracted body = %q", got)
	}
	if _, err := os.Lstat(filepath.Join(m.Dir, "pax_global_header")); !os.IsNotExist(err) {
		t.Errorf("the pax header was WRITTEN to the tree (lstat err = %v); it must be skipped, not unpacked", err)
	}

	// The skip must not have widened. A link in the same archive is still refused by name.
	linked := makeTarGz(t,
		tarEntry{name: "pax_global_header", body: "deadbeef", typeflag: tar.TypeXGlobalHeader},
		tarEntry{name: "etc", typeflag: tar.TypeSymlink, linkname: "/etc"},
	)
	m2, err := materializeBundle(t, linked)
	if err == nil {
		m2.Release()
		t.Fatal("a symlink was accepted when the archive also carried a pax header")
	}
	if !strings.Contains(err.Error(), "is a link") {
		t.Errorf("err = %v, want the link refusal", err)
	}
}
