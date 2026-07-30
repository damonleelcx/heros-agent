package distribution

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// merge.go is the release merge step (P20 task 2.2) and the fail-closed gate (task 2.5).
//
// # Why the merge RECOMPUTES the checksums instead of concatenating the per-runner manifests
//
// Five native runners (D1) each produce a binary and a per-runner `SHA256SUMS`, and those files travel
// through the CI artifact store to get here. Concatenating the five claims would produce a manifest that
// is internally consistent and says nothing: it would attest to what each runner *claimed* about bytes
// that a later step actually re-uploaded. If an artifact were corrupted or swapped in transit, the merged
// manifest would faithfully record the old checksum, the signature would cover it, and every installer
// downstream would then reject the published binary — a fail-closed outcome, but one discovered by users
// rather than by the pipeline.
//
// So the merge hashes the bytes it actually holds, AND cross-checks each against the per-runner claim. The
// two must agree. A disagreement is a hard stop naming the file, because exactly one of two things has
// happened and both are serious: the artifact changed after it was built, or a runner reported a checksum
// it did not compute.
//
// # Why sorted, and why that matters more than it looks
//
// The manifest is the signed document. Two runs that produce identical binaries must produce byte-identical
// manifests, or the signature differs, the "did this re-run reproduce?" check (task 2.6) is meaningless,
// and a customer diffing two releases sees noise. Sorting by name is what makes the document a function of
// its contents rather than of the order five parallel jobs happened to finish in.

// Artifact is one built file on its way into the release.
type Artifact struct {
	// Name is the published asset name, which must be exactly AssetName(version, goos, goarch).
	Name string
	// Path is where the bytes are right now.
	Path string
	// ClaimedSum is the checksum the building runner recorded in its own SHA256SUMS, if it was collected.
	// Empty means no claim travelled with the file, which is itself reported rather than assumed benign.
	ClaimedSum string
	// Source says where the bytes came from, which decides whether a travelling checksum is required.
	Source Source
}

// Source is an artifact's provenance.
type Source string

const (
	// SourceRunner is a file a matrix job built and uploaded through the CI artifact store. Its checksum
	// MUST have travelled with it, because the store is the one hop where an artifact can change without
	// anyone noticing.
	SourceRunner Source = "runner"
	// SourceRepo is a file taken straight from the git checkout the merge job itself made — the install
	// scripts. There is no transit hop to cross-check against: the provenance IS the checkout, which the
	// pipeline verified when it cloned. Requiring a claim here would mean inventing one and then comparing
	// the file to itself, which proves nothing and reads as if it did.
	SourceRepo Source = "repo"
)

// Manifest is the merged, sorted, signable checksum document.
type Manifest struct {
	// Text is the exact bytes that get signed and published as SHA256SUMS.
	Text string
	// Assets is the sorted asset names it covers.
	Assets []string
	// Sums maps asset name to its recomputed hex sha256.
	Sums map[string]string
}

// CollectRunnerArtifacts walks a directory of per-runner artifact bundles — the shape
// actions/download-artifact produces: one subdirectory per matrix job, each holding the binary it built
// and that job's SHA256SUMS — and returns the artifacts with their travelling claims attached.
//
// It is deliberately shape-tolerant about the directory layout (one level or nested) and completely
// intolerant about content: a binary with no name it recognises, or two runners producing the same asset
// name, is an error rather than a last-writer-wins merge.
func CollectRunnerArtifacts(root string) ([]Artifact, error) {
	claims := map[string]string{} // asset name → checksum claimed by the runner that built it
	claimedBy := map[string]string{}
	paths := map[string]string{}   // asset name → path
	foundBy := map[string]string{} // asset name → which bundle it came from

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("collect: %w", err)
	}
	walk := func(dir, bundle string) error {
		files, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			p := filepath.Join(dir, f.Name())
			if f.Name() == "SHA256SUMS" {
				b, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				for name, sum := range parseManifest(string(b)) {
					if prev, ok := claims[name]; ok && prev != sum {
						return fmt.Errorf("collect: runners %q and %q claim different checksums for %s — "+
							"two jobs built the same asset", claimedBy[name], bundle, name)
					}
					claims[name] = sum
					claimedBy[name] = bundle
				}
				continue
			}
			if !strings.HasPrefix(f.Name(), "heros-") {
				continue // packaging inputs and logs are collected by their own steps
			}
			if prev, ok := foundBy[f.Name()]; ok {
				return fmt.Errorf("collect: %s was produced by both %q and %q — the matrix has an "+
					"overlapping row, and merging would silently keep one", f.Name(), prev, bundle)
			}
			paths[f.Name()] = p
			foundBy[f.Name()] = bundle
		}
		return nil
	}

	if err := walk(root, "."); err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			if err := walk(filepath.Join(root, e.Name()), e.Name()); err != nil {
				return nil, err
			}
		}
	}

	var out []Artifact
	for name, p := range paths {
		out = append(out, Artifact{Name: name, Path: p, ClaimedSum: claims[name], Source: SourceRunner})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// parseManifest reads "sha256  name" lines into a map. Malformed lines are skipped here rather than
// failing, because this is reading a CLAIM whose only authority is to be compared against a recomputation
// — the recomputation is what the manifest ends up asserting.
func parseManifest(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 2 && len(fields[0]) == 64 {
			// 🔴 A leading `*` is the coreutils BINARY-MODE marker, not part of the filename: the format is
			// "<hash><space><' '|'*'><name>", and `sha256sum -c` accepts both modes. Git-bash on Windows
			// defaults to binary mode, so the windows/amd64 runner writes "<hash> *heros-...exe" while every
			// POSIX runner writes "<hash>  heros-...". Keeping the `*` files the claim under a name no
			// artifact has, and the merge then reports the checksum as MISSING — which reads as "the build
			// job did not upload its SHA256SUMS" when the job did exactly that.
			out[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
		}
	}
	return out
}

// ParseManifest exposes the "sha256  name" reader to the installer-side verifier and to tests. The
// installer reads a published manifest it did not write, so it needs the same parse the merge used — a
// second implementation is how one side ends up case-sensitive about hex and the other not.
func ParseManifest(text string) map[string]string { return parseManifest(text) }

// Merge recomputes every artifact's checksum, cross-checks it against the claim that travelled with it,
// and returns the one sorted manifest the release signs.
func Merge(arts []Artifact) (Manifest, error) {
	if len(arts) == 0 {
		return Manifest{}, errors.New("merge: no artifacts — refusing to sign an empty manifest, which would " +
			"verify perfectly and attest to nothing")
	}
	sums := map[string]string{}
	var names []string
	for _, a := range arts {
		data, err := os.ReadFile(a.Path)
		if err != nil {
			return Manifest{}, fmt.Errorf("merge: cannot read %s: %w", a.Name, err)
		}
		if len(data) == 0 {
			return Manifest{}, fmt.Errorf("merge: %s is empty — a zero-byte binary hashes and signs just "+
				"fine, and fails on the user's machine", a.Name)
		}
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		if a.ClaimedSum != "" && a.ClaimedSum != got {
			return Manifest{}, fmt.Errorf("merge: %s CHANGED after it was built — the building runner "+
				"recorded %s, the bytes here hash to %s. Either the artifact was modified in transit or a "+
				"runner reported a checksum it did not compute; both are release-stopping", a.Name, a.ClaimedSum, got)
		}
		if a.ClaimedSum == "" && a.Source != SourceRepo {
			return Manifest{}, fmt.Errorf("merge: %s arrived with no per-runner checksum to cross-check "+
				"against — the build job did not upload its SHA256SUMS, so the merge cannot tell a "+
				"transit corruption from a rebuild", a.Name)
		}
		sums[a.Name] = got
		names = append(names, a.Name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s  %s\n", sums[n], n)
	}
	return Manifest{Text: b.String(), Assets: names, Sums: sums}, nil
}

// ReproducibleMarkers reports whether every shipped target left a reproducibility marker in the collected
// artifacts, and names the ones that did not.
//
// # Why a file per target rather than a boolean
//
// A boolean threaded through workflow outputs can be produced by a step that never ran: `if:` conditions,
// a skipped job, and a typo'd output name all yield an empty string, and an empty string compared against
// "false" is easy to get backwards. A marker file can only exist because a step wrote it, and the step that
// writes it runs immediately after the reproducibility test passes on that runner.
//
// Reproducibility is a PER-PLATFORM property — the same source, the same flags, the same C toolchain. A
// single marker from the Linux runner says nothing about the macOS artifact, so a complete set is required.
func ReproducibleMarkers(root string) (bool, []string) {
	present := map[string]bool{}
	_ = filepathWalk(root, func(name string) {
		if strings.HasPrefix(name, "REPRODUCIBLE-") {
			present[strings.TrimPrefix(name, "REPRODUCIBLE-")] = true
		}
	})
	var missing []string
	for _, t := range Shipped() {
		if !present[t.GOOS+"-"+t.GOARCH] {
			missing = append(missing, t.Key())
		}
	}
	sort.Strings(missing)
	return len(missing) == 0, missing
}

// filepathWalk visits every regular file's base name under root, one level deep and one level nested —
// the two shapes CI artifact downloads produce. It ignores read errors on individual directories because a
// missing marker is already the failure this feeds; an unreadable directory cannot make one appear.
func filepathWalk(root string, visit func(name string)) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			visit(e.Name())
			continue
		}
		sub, err := os.ReadDir(filepath.Join(root, e.Name()))
		if err != nil {
			continue
		}
		for _, f := range sub {
			if !f.IsDir() {
				visit(f.Name())
			}
		}
	}
	return nil
}

// RepoArtifacts wraps files taken from the checkout so they join the signed manifest.
//
// # Why the install scripts belong in the signed manifest
//
// The documented install is `curl … | sh`, and a user who wants to audit what they piped has nowhere to check
// it against unless the script is covered by the same signature as the binaries. With it in SHA256SUMS, the
// README can publish a tag-pinned URL and a checksum that a reviewer verifies with the two documented
// commands — the same two they already run for the binary (task 3.7).
func RepoArtifacts(paths map[string]string) []Artifact {
	var out []Artifact
	for name, path := range paths {
		out = append(out, Artifact{Name: name, Path: path, Source: SourceRepo})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// GateFailure is one reason a release must not publish. They are collected rather than returned one at a
// time so an operator sees every problem in one run — fixing three failures across three re-runs of a
// 20-minute matrix is how people start reaching for the manual override.
type GateFailure struct {
	// Gate is the stable name of the rule, so a log line can be searched for.
	Gate string
	// Detail says what specifically was wrong, with the values.
	Detail string
}

func (g GateFailure) Error() string { return g.Gate + ": " + g.Detail }

// Gate is the release's fail-closed check (task 2.5), run after the merge and before anything publishes.
//
// It refuses on:
//   - an INCOMPLETE matrix — any shipped target missing from the manifest;
//   - an UNSIGNED manifest on any channel but dev;
//   - a REPRODUCIBILITY regression — the caller ran the reproducible-build test and reports the result;
//   - a version that is not exactly the tag's version, anywhere it appears.
//
// It takes the reproducibility result as a parameter rather than running the test itself so that the
// pipeline's gate and the local rehearsal check the same predicate: the test is run by `go test`, in one
// place, and its outcome is data here.

// Repro is the reproducibility evidence the gate was given. Missing names the targets whose marker was
// absent, so the refusal says WHICH platform is unproven rather than that "reproducibility failed" — on a
// five-runner matrix those are very different next actions.
type Repro struct {
	Verified bool
	Missing  []string
}

func Gate(v Version, a Attestation, repro Repro) []GateFailure {
	var fails []GateFailure

	if a.Version != v.Version {
		fails = append(fails, GateFailure{"version-single-source", fmt.Sprintf(
			"the attestation says %q but the tag says %q — a second copy of the version has drifted from the tag",
			a.Version, v.Version)})
	}
	if ok, missing := a.Complete(); !ok {
		fails = append(fails, GateFailure{"matrix-complete", fmt.Sprintf(
			"the release is missing %d of %d shipped targets: %v. A release that omits a target is worse "+
				"than a failed one: every other channel keeps working and only that platform 404s",
			len(missing), len(Shipped()), missing)})
	}
	if v.RequiresSignature() && !a.Verified() {
		fails = append(fails, GateFailure{"manifest-signed", fmt.Sprintf(
			"channel %q requires a signed manifest and this one is not signed (signed=%v key=%q). Every "+
				"installer verifies the signature before placing a binary on PATH, so publishing this would "+
				"ship a release no channel can install", v.Channel, a.SignedManifest, a.SigningKeyID)})
	}
	if !repro.Verified {
		detail := "TestReproducibleBuild did not pass. Reproducibility is what lets a third party rebuild " +
			"and confirm the bytes; a release that cannot be reproduced cannot be independently verified"
		if len(repro.Missing) > 0 {
			detail = fmt.Sprintf("no reproducibility evidence for %v — reproducibility is a per-platform "+
				"property, so a marker from another runner says nothing about these targets", repro.Missing)
		}
		fails = append(fails, GateFailure{"reproducible-build", detail})
	}
	return fails
}

// GateReport renders the gate result for a CI log: every failure, each naming its rule, or a single line
// stating what passed. It never renders "0 failures" as silence — a gate nobody can see run is a gate an
// operator will assume was skipped.
func GateReport(v Version, fails []GateFailure) string {
	var b strings.Builder
	fmt.Fprintf(&b, "release gate — %s (channel %s)\n", v.Tag, v.Channel)
	if len(fails) == 0 {
		fmt.Fprintf(&b, "  ✅ matrix complete (%d targets) · manifest signed · reproducible build\n", len(Shipped()))
		return b.String()
	}
	for _, f := range fails {
		fmt.Fprintf(&b, "  ⛔ %s — %s\n", f.Gate, f.Detail)
	}
	fmt.Fprintf(&b, "\n%d gate(s) failed. Nothing is published. This is fail-closed by design: fix the cause "+
		"and re-run the same tag — the pipeline is idempotent.\n", len(fails))
	return b.String()
}
