// Command herosdist is the release pipeline's brain (P20 section 2).
//
// # Why it exists rather than living in release.yml
//
// Every rule this tool enforces — the version comes from the tag, the matrix is complete, the manifest is
// signed, the generated package manifests carry no second copy of the version — is a rule whose failure
// mode is a bad release. YAML can express those checks; it cannot let anyone run them before pushing a
// tag. A gate that is only reachable by pushing a `v*` tag to the default branch gets debugged in
// production, one tag at a time, and each debugging attempt is a public Release.
//
// So the logic is Go, in internal/distribution, with tests; release.yml is wiring. `make release-rehearse`
// runs the whole thing locally on a fake tag.
//
//	herosdist plan     --tag v1.2.3            → KEY=VALUE lines for $GITHUB_OUTPUT
//	herosdist merge    --in artifacts --out dist → one sorted SHA256SUMS over every runner's binaries
//	herosdist attest   --tag v1.2.3 --dir dist → trust.json, the machine-readable trust posture
//	herosdist gate     --tag v1.2.3 --dir dist --markers artifacts → fail-closed release gate
//	herosdist manifests --tag v1.2.3 --dir dist --out packaging → every channel manifest, generated (D5)
//	herosdist notes    --dir dist              → release notes body with only the claims delivered
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/heros-foreal/agentd/internal/distribution"
	"github.com/heros-foreal/agentd/internal/release"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "plan":
		err = plan(os.Args[2:])
	case "merge":
		err = merge(os.Args[2:])
	case "attest":
		err = attest(os.Args[2:])
	case "gate":
		err = gate(os.Args[2:])
	case "manifests":
		err = manifests(os.Args[2:])
	case "readme":
		err = readme(os.Args[2:])
	case "notes":
		err = notes(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "herosdist: "+err.Error())
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: herosdist <plan|merge|attest|gate|manifests|readme|notes> [flags]

  plan      --tag vX.Y.Z                          resolve the one version every artifact derives from
  merge     --in <dir> --out <dir>                recompute + cross-check + sort into one SHA256SUMS
  attest    --tag vX.Y.Z --dir <dir> [--sig f]    write trust.json: what this release actually delivered
  gate      --tag vX.Y.Z --dir <dir> --markers <dir>   fail-closed release gate
  manifests --tag vX.Y.Z --dir <dir> --out <dir>  every channel manifest, generated from the signed manifest
  readme    --tag vX.Y.Z [--dir <dir>] [--write] the generated README install section (drift-gated)
  notes     --dir <dir> [--image-tags a,b]        release notes body, claiming only what was delivered`)
}

// plan resolves the tag and emits KEY=VALUE lines for $GITHUB_OUTPUT.
//
// Every downstream job reads its version from these lines, so there is exactly one parse of the tag in the
// whole pipeline. The alternative — each job doing its own `${GITHUB_REF#refs/tags/}` — is five copies of a
// rule that has to agree.
func plan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	tag := fs.String("tag", "", "the git tag being released (vX.Y.Z or vX.Y.Z-rc.N)")
	ref := fs.String("ref", "", "a git ref such as refs/tags/vX.Y.Z, used when --tag is not given")
	_ = fs.Parse(args)

	t := *tag
	if t == "" && *ref != "" {
		t = strings.TrimPrefix(*ref, "refs/tags/")
	}
	v, err := distribution.ParseTag(t)
	if err != nil {
		return err
	}
	out := []string{
		"tag=" + v.Tag,
		"version=" + v.Version,
		"channel=" + string(v.Channel),
		fmt.Sprintf("draft=%v", v.Draft()),
		fmt.Sprintf("latest=%v", v.Latest()),
		fmt.Sprintf("requires_signature=%v", v.RequiresSignature()),
		"ldflags=" + v.LDFlags(),
		"image_tags=" + strings.Join(v.ImageTags(distribution.ImageRepo), ","),
		"expected_assets=" + strings.Join(distribution.ExpectedAssets(v.Version), ","),
		"matrix_version=" + distribution.MatrixVersion(),
	}
	fmt.Println(strings.Join(out, "\n"))
	return nil
}

func merge(args []string) error {
	fs := flag.NewFlagSet("merge", flag.ExitOnError)
	in := fs.String("in", "artifacts", "directory of per-runner artifact bundles")
	out := fs.String("out", "dist", "directory to write the merged binaries + SHA256SUMS into")
	include := fs.String("include", "", "comma-separated repo files to cover with the signed manifest (task 3.7)")
	_ = fs.Parse(args)

	arts, err := distribution.CollectRunnerArtifacts(*in)
	if err != nil {
		return err
	}
	// Repo-sourced files — the install scripts — join the manifest so a user who piped `curl … | sh` can
	// verify what they ran against the same signature as the binaries.
	repo := map[string]string{}
	for _, p := range strings.Split(*include, ",") {
		if p = strings.TrimSpace(p); p != "" {
			repo[filepath.Base(p)] = p
		}
	}
	arts = append(arts, distribution.RepoArtifacts(repo)...)
	m, err := distribution.Merge(arts)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	// Move the bytes we actually hashed into the publish directory. Copying the file we verified — rather
	// than re-resolving the name from the source tree — is what makes the manifest describe the artifact
	// that gets uploaded.
	for _, a := range arts {
		data, err := os.ReadFile(a.Path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(*out, a.Name), data, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(*out, "SHA256SUMS"), []byte(m.Text), 0o644); err != nil {
		return err
	}
	fmt.Printf("herosdist merge: %d artifacts, cross-checked against their per-runner claims\n", len(m.Assets))
	for _, n := range m.Assets {
		fmt.Printf("  %s  %s\n", m.Sums[n][:16]+"…", n)
	}
	return nil
}

// attest records what the release DELIVERED, as opposed to what the posture intends (D3).
//
// The OS-signing flags are set by the workflow steps that actually ran the signing, from their own success
// — never from a decision constant. That is the whole reason this file cannot just print the posture.
func attest(args []string) error {
	fs := flag.NewFlagSet("attest", flag.ExitOnError)
	tag := fs.String("tag", "", "the git tag being released")
	dir := fs.String("dir", "dist", "the merged release directory")
	sig := fs.String("sig", "", "detached signature over SHA256SUMS (default <dir>/SHA256SUMS.sig if present)")
	macSigned := fs.Bool("macos-signed", false, "the macOS binaries were Developer-ID code-signed by this run")
	macNotarized := fs.Bool("macos-notarized", false, "Apple's notary service accepted the macOS artifacts in this run")
	macStapled := fs.Bool("macos-stapled", false, "the notarization ticket is attached to the artifact (only possible for .app/.dmg/.pkg, never a bare executable)")
	macPublisher := fs.String("macos-publisher", "", "the identity the macOS signature names")
	macIdentity := fs.String("macos-team-id", "", "the Apple Team ID that signed")
	winSigned := fs.Bool("windows-signed", false, "the Windows binary was Authenticode-signed by this run")
	winPublisher := fs.String("windows-publisher", "", "the publisher declared on the Windows package")
	winIdentity := fs.String("windows-thumbprint", "", "the Authenticode certificate thumbprint")
	out := fs.String("out", "", "path to write trust.json (default <dir>/trust.json)")
	_ = fs.Parse(args)

	v, err := version(*tag)
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(*dir, "SHA256SUMS")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("attest: no merged manifest at %s: %w", manifestPath, err)
	}
	sigPath := *sig
	if sigPath == "" {
		sigPath = manifestPath + ".sig"
	}

	a := distribution.Attestation{
		Version: v.Version,
		MacOS: distribution.OSTrust{
			GOOS: "darwin", CodeSigned: *macSigned, Notarized: *macNotarized, Stapled: *macStapled,
			Publisher: *macPublisher, Identity: *macIdentity,
		},
		Windows: distribution.OSTrust{
			GOOS: "windows", CodeSigned: *winSigned,
			Publisher: *winPublisher, Identity: *winIdentity,
		},
	}
	for name := range distribution.ParseManifest(string(manifest)) {
		a.Assets = append(a.Assets, name)
	}
	sortStrings(a.Assets)

	// The signature is VERIFIED here, not just noted as present. `SignedManifest: true` because a .sig
	// file exists would be a claim about the filesystem; the point of the field is to be a claim about
	// the bytes, and the same verifier every installer uses is the one that decides it.
	if raw, err := os.ReadFile(sigPath); err == nil {
		keyID, verr := release.VerifyTrusted(manifest, string(raw))
		if verr != nil {
			return fmt.Errorf("attest: a signature is present but does NOT verify against the published "+
				"trust root: %w — refusing to record this release as signed", verr)
		}
		a.SignedManifest = true
		a.SigningKeyID = keyID
	} else if v.RequiresSignature() {
		fmt.Fprintf(os.Stderr, "herosdist attest: no signature at %s — recording this release as UNSIGNED. "+
			"The gate will refuse to publish it on channel %s.\n", sigPath, v.Channel)
	}

	path := *out
	if path == "" {
		path = filepath.Join(*dir, "trust.json")
	}
	blob, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(blob, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Print(a.Describe())
	return nil
}

func gate(args []string) error {
	fs := flag.NewFlagSet("gate", flag.ExitOnError)
	tag := fs.String("tag", "", "the git tag being released")
	dir := fs.String("dir", "dist", "the merged release directory")
	markers := fs.String("markers", "", "directory of per-runner bundles holding REPRODUCIBLE-<goos>-<goarch> markers")
	repoRoot := fs.String("repo-root", ".", "repository root to audit for hand-written packaging versions (D5)")
	reproducible := fs.Bool("reproducible", false, "assert reproducibility directly (local rehearsal only; --markers is the CI path)")
	_ = fs.Parse(args)

	v, err := version(*tag)
	if err != nil {
		return err
	}
	a, err := readAttestation(*dir)
	if err != nil {
		return err
	}
	// Marker files beat a flag: a boolean can be produced by a step that never ran, while a file can only
	// exist because the reproducibility test passed on that runner.
	repro := distribution.Repro{Verified: *reproducible}
	if *markers != "" {
		ok, missing := distribution.ReproducibleMarkers(*markers)
		repro = distribution.Repro{Verified: ok, Missing: missing}
	}
	fails := distribution.Gate(v, a, repro)

	// D5: no packaging manifest in the repository may carry a resolved version. A committed formula drifts
	// to last release's URL and checksum, and the user who runs `brew install` gets the old binary while
	// `heros version` disagrees with the Release page.
	handwritten, err := distribution.AuditNoHandWrittenVersions(*repoRoot)
	if err != nil {
		return fmt.Errorf("gate: cannot audit %s for hand-written versions: %w", *repoRoot, err)
	}
	fails = append(fails, distribution.GateManifests(handwritten)...)

	// Task 4.3: the documents may state a trust property only if this release delivered it. Both the generated
	// release notes and the hand-written README are audited — the generated one because a generator can still be
	// changed to overclaim, the hand-written one because that is where a copy-paste from last release lands.
	for _, doc := range []string{"README.md", filepath.Join(*dir, "RELEASE_NOTES.md")} {
		blob, readErr := os.ReadFile(doc)
		if readErr != nil {
			continue // a document that does not exist makes no claims
		}
		fails = append(fails, distribution.GateClaims(distribution.AuditClaims(doc, string(blob), a))...)
	}
	fmt.Print(distribution.GateReport(v, fails))
	if len(fails) > 0 {
		return fmt.Errorf("%d release gate(s) failed — nothing published", len(fails))
	}
	return nil
}

// manifests generates every package-manager manifest from the release (D5, tasks 3.3–3.6).
//
// The checksums come from the SIGNED manifest, not from hashing the files here — that is what links each
// channel back to the release key. `brew` checks a sha256 it finds in the formula; the formula's authority is
// that its sha256 was copied from the document the release key signed.
func manifests(args []string) error {
	fs := flag.NewFlagSet("manifests", flag.ExitOnError)
	tag := fs.String("tag", "", "the git tag being released")
	dir := fs.String("dir", "dist", "the merged release directory holding SHA256SUMS and trust.json")
	out := fs.String("out", "dist/packaging", "directory to write the generated manifests into")
	only := fs.String("only", "", "comma-separated channel ids to generate (default: every channel, requiring every shipped target)")
	_ = fs.Parse(args)

	v, err := version(*tag)
	if err != nil {
		return err
	}
	blob, err := os.ReadFile(filepath.Join(*dir, "SHA256SUMS"))
	if err != nil {
		return fmt.Errorf("manifests: no signed manifest at %s/SHA256SUMS: %w", *dir, err)
	}
	a, err := readAttestation(*dir)
	if err != nil {
		return err
	}
	var scope []string
	for _, id := range strings.Split(*only, ",") {
		if id = strings.TrimSpace(id); id != "" {
			scope = append(scope, id)
		}
	}
	files, err := distribution.Generate(v, distribution.ParseManifest(string(blob)), a, scope...)
	if err != nil {
		return err
	}
	for _, f := range files {
		path := filepath.Join(*out, f.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(f.Content), os.FileMode(f.Mode)); err != nil {
			return err
		}
		state := "published by the pipeline"
		if c, ok := distribution.ChannelByID(f.Channel); ok && !c.Delivered() {
			state = "generated; " + string(c.Publication)
		}
		fmt.Printf("  %-58s %s\n", f.Path, state)
	}
	fmt.Printf("herosdist manifests: %d files from the signed manifest of %s\n", len(files), v.Tag)
	return nil
}

// readme renders the generated README install section, and with --write splices it into README.md between the
// markers.
//
// It is generated-and-checked rather than generated-into-a-separate-file because a README is read by people who
// will not run a generator. The section lives in the file a reader opens; TestReadmeInstallSectionMatchesContract
// fails when it drifts from the channel contract, which is what makes "claim only delivered channels"
// enforceable rather than aspirational.
func readme(args []string) error {
	fs := flag.NewFlagSet("readme", flag.ExitOnError)
	tag := fs.String("tag", "", "the release tag the section documents")
	dir := fs.String("dir", "", "a merged release directory, to read install.sh's checksum from its SIGNED manifest")
	write := fs.Bool("write", false, "splice the section into README.md between the markers")
	_ = fs.Parse(args)

	// With no --tag, the section is generated for the release the DOCS describe. That default is what makes
	// `make readme-install` runnable by anyone at any time, rather than only during a release.
	t := *tag
	if t == "" {
		t = distribution.DocumentedRelease
	}
	v, err := distribution.ParseTag(t)
	if err != nil {
		return err
	}
	// The install-script checksum comes from the signed manifest, never from hashing the file here: the point
	// of publishing it is that a reader can check what they piped against what the release key signed.
	installSum := ""
	if *dir != "" {
		blob, err := os.ReadFile(filepath.Join(*dir, "SHA256SUMS"))
		if err != nil {
			return fmt.Errorf("readme: cannot read the signed manifest in %s: %w", *dir, err)
		}
		installSum = distribution.ParseManifest(string(blob))["install.sh"]
		if installSum == "" {
			return fmt.Errorf("readme: %s/SHA256SUMS does not cover install.sh — merge it with "+
				"`--include scripts/install.sh` so the pinned URL can be checksum-referenced (task 3.7)", *dir)
		}
	}
	section := distribution.ReadmeInstallSection(v, installSum)
	if !*write {
		fmt.Println(section)
		return nil
	}
	current, err := os.ReadFile("README.md")
	if err != nil {
		return err
	}
	updated, err := distribution.SpliceReadme(string(current), section)
	if err != nil {
		return err
	}
	return os.WriteFile("README.md", []byte(updated), 0o644)
}

// notes renders the release notes body from the attestation — never from the ratified posture, so the notes
// cannot claim a trust property the artifacts do not carry (task 4.3).
func notes(args []string) error {
	fs := flag.NewFlagSet("notes", flag.ExitOnError)
	dir := fs.String("dir", "dist", "the merged release directory holding trust.json")
	imageTags := fs.String("image-tags", "", "comma-separated container tags that were actually pushed")
	_ = fs.Parse(args)

	a, err := readAttestation(*dir)
	if err != nil {
		return err
	}
	var tags []string
	for _, t := range strings.Split(*imageTags, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	fmt.Print(distribution.ReleaseNotes(a, tags))
	return nil
}

func version(tag string) (distribution.Version, error) {
	if tag == "" {
		if ref := os.Getenv("GITHUB_REF"); strings.HasPrefix(ref, "refs/tags/") {
			tag = strings.TrimPrefix(ref, "refs/tags/")
		}
	}
	return distribution.ParseTag(tag)
}

func readAttestation(dir string) (distribution.Attestation, error) {
	var a distribution.Attestation
	blob, err := os.ReadFile(filepath.Join(dir, "trust.json"))
	if err != nil {
		return a, fmt.Errorf("gate: no trust.json in %s — run `herosdist attest` first: the gate checks what "+
			"the release delivered, and with no record of that there is nothing to check: %w", dir, err)
	}
	return a, json.Unmarshal(blob, &a)
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
