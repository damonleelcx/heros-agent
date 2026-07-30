package clilink

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/cli"
	"github.com/heros-foreal/agentd/internal/distribution"
	"github.com/heros-foreal/agentd/internal/release"
)

// upgrade.go is `heros upgrade` (P20 task 5.4, D7): fetch the latest release, verify it with the SAME routine
// the installer uses, and replace the binary in place only on success.
//
// # Why it lives here and not in internal/cli
//
// Because it is the only local-feeling command that must reach the network, and internal/cli — the whole
// offline command surface — does not import net/http at all. That is not a convention, it is the mechanism:
// `discover`, `apply`, `eval`, `doctor` and `init` cannot phone home because the code that runs them does not
// link a network stack. Putting upgrade here keeps that true, and it is why task 5.6's "no update-check on the
// hot path" needs no discipline to maintain — the hot path has no way to make a request.
//
// # The four refusals D7 requires, and what each one prevents
//
//	1. never execute before verifying    — the download is verified as BYTES, never run to ask its version
//	2. no-op when already current        — an upgrade that reinstalls the same version wastes a download and
//	                                       rewrites a binary someone may be running, for nothing
//	3. defer to the package manager      — overwriting a brew/dpkg-owned file corrupts that manager's state,
//	                                       and the next `brew upgrade` silently reverts the user anyway
//	4. refuse a downgrade                — an OLD release is a legitimately signed artifact, so the signature
//	                                       cannot distinguish "newer" from "the version with the bug"
//
// (4) is the one that is easy to miss: verification proves provenance, not freshness.

// Upgrade replaces this binary with the latest verified release.
func (c Commands) Upgrade(cfg cli.Config, s cli.Streams) error {
	exe := cli.ExecutablePath()
	if exe == "" {
		return &cli.ExitError{Code: cli.ExitOperational,
			Msg: "upgrade: cannot determine this binary's own path, so it cannot be replaced safely. " +
				"Re-run the installer instead — it writes atomically."}
	}

	// Refusal 3, BEFORE any network use. A brew-installed user gets their answer without a byte downloaded:
	// asking the network first and then telling them to run `brew upgrade` would be a download nobody wanted.
	if channel, command, owned := cli.UpgradeAdvice(exe); owned {
		s.Narratef("heros upgrade: this binary is managed by %s (%s).", channel, exe)
		s.Narratef("               Replacing it here would corrupt that manager's state, and its next upgrade")
		s.Narratef("               would silently revert you. Use its own command:")
		s.Narratef("")
		s.Narratef("    %s", command)
		return s.EmitJSON("upgrade", cli.ExitOK, UpgradeData{
			Current: cli.ToolVersion, Action: "defer-to-package-manager",
			Channel: channel, Command: command, Path: exe,
		}, nil, nil)
	}

	target, known := distribution.TargetFor(runtime.GOOS, runtime.GOARCH)
	if !known || target.Support != distribution.SupportShipped {
		msg := "upgrade: " + runtime.GOOS + "/" + runtime.GOARCH + " has no published build"
		if known {
			msg += " — " + target.Limit + ". Instead: " + target.Answer
		}
		return &cli.ExitError{Code: cli.ExitOperational, Msg: msg}
	}

	current, currentErr := distribution.ParseTag("v" + cli.ToolVersion)

	latest, err := c.latestVersion(cfg)
	if err != nil {
		return err
	}
	s.Narratef("heros upgrade: installed %s · latest %s", cli.ToolVersion, latest.Version)

	// Refusal 2. A dev build has no comparable version, so it is upgraded rather than compared — comparing a
	// hand-built binary against a release would refuse every developer's upgrade.
	if currentErr == nil {
		switch current.Compare(latest) {
		case 0:
			s.Narratef("               Already current. Nothing downloaded, nothing changed.")
			return s.EmitJSON("upgrade", cli.ExitOK, UpgradeData{
				Current: cli.ToolVersion, Latest: latest.Version, Action: "no-op-already-current", Path: exe,
			}, nil, nil)
		case 1:
			// Refusal 4.
			return &cli.ExitError{Code: cli.ExitOperational, Msg: fmt.Sprintf(
				"upgrade: the latest published release (%s) is OLDER than the installed version (%s). "+
					"Refusing: an old release is a legitimately signed artifact, so verification cannot tell an "+
					"upgrade from someone serving you the version with the bug. If a downgrade is what you want, "+
					"install it explicitly with HEROS_VERSION=%s and the install script.",
				latest.Version, cli.ToolVersion, latest.Version)}
		}
	} else {
		s.Narratef("               (this build's version %q is not a release version, so no comparison is made)", cli.ToolVersion)
	}

	// Download the asset and the manifest + signature. Nothing is executed: the binary is verified as bytes.
	assetName := distribution.AssetName(latest.Version, runtime.GOOS, runtime.GOARCH)
	base := releaseDownloadBase(cfg, latest.Tag)
	s.Narratef("               downloading %s", assetName)

	assetBytes, err := c.fetch(base + "/" + assetName)
	if err != nil {
		return &cli.ExitError{Code: cli.ExitOperational,
			Msg: "upgrade: cannot download " + assetName + " — " + err.Error(), Err: err}
	}
	manifest, err := c.fetch(base + "/SHA256SUMS")
	if err != nil {
		return &cli.ExitError{Code: cli.ExitOperational,
			Msg: "upgrade: cannot download the checksum manifest. Without it there is nothing to verify " +
				"against, and this command does not install unverified bytes.", Err: err}
	}
	sig, err := c.fetch(base + "/SHA256SUMS.sig")
	if err != nil {
		return &cli.ExitError{Code: cli.ExitOperational,
			Msg: "upgrade: release " + latest.Tag + " has no signature over its checksum manifest. " +
				"Checksums prove the download is intact, not who produced it — refusing.", Err: err}
	}

	// Refusal 1: THE shared routine, the same one `heros verify-release` runs and the same one the installer
	// invokes when a previous heros is present. One implementation, so it cannot drift into weakness.
	out, verr := release.VerifyBundle(release.Bundle{
		Manifest: manifest, SignatureHex: string(sig),
		Assets: map[string][]byte{assetName: assetBytes},
	})
	if verr != nil {
		return &cli.ExitError{Code: cli.ExitOperational,
			Msg: "upgrade: VERIFICATION FAILED — the downloaded release was not installed. " + verr.Error(),
			Err: verr}
	}
	s.Narratef("               ✅ %s", out.Describe())

	if err := replaceBinary(exe, assetBytes); err != nil {
		return err
	}
	s.Narratef("               replaced %s", exe)

	notice := distribution.Attestation{Version: latest.Version}.FirstRunNotice(runtime.GOOS, exe)
	if notice != "" {
		s.Narratef("")
		s.Narratef("%s", notice)
	}
	return s.EmitJSON("upgrade", cli.ExitOK, UpgradeData{
		Current: cli.ToolVersion, Latest: latest.Version, Action: "replaced",
		Path: exe, SigningKeyID: out.SigningKeyID,
	}, nil, nil)
}

// UpgradeData is the machine payload for `heros upgrade`.
type UpgradeData struct {
	Current string `json:"current"`
	Latest  string `json:"latest,omitempty"`
	// Action is one of replaced | no-op-already-current | defer-to-package-manager. A machine consumer
	// branches on this rather than on the absence of a field.
	Action       string `json:"action"`
	Channel      string `json:"channel,omitempty"`
	Command      string `json:"command,omitempty"`
	Path         string `json:"path"`
	SigningKeyID string `json:"signing_key_id,omitempty"`
}

// releaseDownloadBase is where assets come from. Overridable for the same reason install.sh's endpoint is: a
// tamper red-check needs a fixture, and a verification step never shown to reject is treated as absent. The
// trust root is compiled in, so redirecting the download changes where the bytes come from, not which key must
// have signed them.
func releaseDownloadBase(cfg cli.Config, tag string) string {
	if v := os.Getenv("HEROS_RELEASE_BASE_URL"); v != "" {
		return strings.TrimRight(v, "/") + "/download/" + tag
	}
	return "https://github.com/damonleelcx/heros-agent/releases/download/" + tag
}

func (c Commands) httpClient() *http.Client {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	if c.RT != nil {
		client.Transport = c.RT
	}
	return client
}

// latestVersion asks the Releases API which tag is latest.
//
// This is the ONLY place `heros` learns about a newer version, and it happens only when a user typed
// `upgrade` (task 5.6): no ordinary command reaches it, there is no background check, and nothing is recorded
// or transmitted about the request. There is no telemetry in this binary — the request carries a version in
// its User-Agent so a rate-limited API can be diagnosed, and nothing else.
func (c Commands) latestVersion(cfg cli.Config) (distribution.Version, error) {
	api := os.Getenv("HEROS_RELEASE_API_URL")
	if api == "" {
		api = "https://api.github.com/repos/damonleelcx/heros-agent/releases"
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(api, "/")+"/latest", nil)
	if err != nil {
		return distribution.Version{}, &cli.ExitError{Code: cli.ExitOperational, Msg: "upgrade: " + err.Error()}
	}
	req.Header.Set("User-Agent", "heros/"+cli.ToolVersion)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return distribution.Version{}, &cli.ExitError{Code: cli.ExitOperational,
			Msg: "upgrade: cannot reach the release index — " + err.Error() +
				". Every other command works offline; only this one needs the network.", Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return distribution.Version{}, &cli.ExitError{Code: cli.ExitOperational,
			Msg: fmt.Sprintf("upgrade: the release index answered %d", resp.StatusCode)}
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return distribution.Version{}, &cli.ExitError{Code: cli.ExitOperational,
			Msg: "upgrade: the release index returned something this build cannot read", Err: err}
	}
	v, err := distribution.ParseTag(payload.TagName)
	if err != nil {
		return distribution.Version{}, &cli.ExitError{Code: cli.ExitOperational,
			Msg: "upgrade: the release index named a tag this build cannot parse: " + err.Error(), Err: err}
	}
	return v, nil
}

func (c Commands) fetch(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "heros/"+cli.ToolVersion)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %d", url, resp.StatusCode)
	}
	// A cap, because a verifier that has already been handed the bytes cannot protect memory: an endpoint
	// serving an endless stream would exhaust it before verification ever ran. 512 MiB is far above any
	// plausible release asset and far below a problem.
	return io.ReadAll(io.LimitReader(resp.Body, 512<<20))
}

// replaceBinary writes the new bytes into place atomically.
//
// # Why write-then-rename, and why the old file is moved aside on Windows
//
// A partially written binary is worse than an old one: it is on PATH, it is executable, and it fails in a way
// that looks like a corrupted install. So the new bytes land in a temporary file in the SAME directory (a
// rename across filesystems is not atomic) and are renamed over the target in one step.
//
// Windows refuses to replace a running executable, so the old file is moved aside first — a rename succeeds
// even while the file is open. The moved-aside copy is left for the OS to clean up rather than deleted, because
// deleting a file a running process has mapped fails, and failing at that point would leave no binary at all.
func replaceBinary(exe string, data []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".heros-upgrade-*")
	if err != nil {
		return &cli.ExitError{Code: cli.ExitOperational, Msg: "upgrade: cannot write into " + dir +
			" — the verified download was NOT installed. Re-run with write access, or use the install script.", Err: err}
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once the rename has succeeded
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close() // best-effort: the write error below is the one worth reporting
		return &cli.ExitError{Code: cli.ExitOperational, Msg: "upgrade: cannot write the new binary", Err: err}
	}
	if err := tmp.Close(); err != nil {
		return &cli.ExitError{Code: cli.ExitOperational, Msg: "upgrade: cannot close the new binary", Err: err}
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return &cli.ExitError{Code: cli.ExitOperational, Msg: "upgrade: cannot make the new binary executable", Err: err}
	}
	if runtime.GOOS == "windows" {
		_ = os.Remove(exe + ".old")
		if err := os.Rename(exe, exe+".old"); err != nil {
			return &cli.ExitError{Code: cli.ExitOperational,
				Msg: "upgrade: cannot move the running heros.exe aside — close any process using it and retry", Err: err}
		}
	}
	if err := os.Rename(tmpName, exe); err != nil {
		return &cli.ExitError{Code: cli.ExitOperational,
			Msg: "upgrade: cannot replace " + exe + " — the verified download was NOT installed", Err: err}
	}
	return nil
}
