// gen-install-content.mjs writes the install page from the PUBLISHED RELEASE and the channel contract
// (tasks 6.6–6.12 · Decisions 12 and 13).
//
// # 🔴 Decision 13, in one line: the shortest path on this page is the VERIFIED path
//
// Readers copy the line that fits on one line. If the one-liner installs and a later section explains
// verification, the one-liner is what ships to production — and it has silently removed the control the
// threat model rests on. The CLI "runs inside your CI with access to your repository", so a compromised
// release is a compromise of every build it runs in.
//
// So this generator emits verification INSIDE the install path, never as an appendix, and **a documented
// path that reaches PATH before verifying is not published at all**. `scan-install.mjs` enforces that on
// the output, so the rule survives somebody editing the generator.
//
// # 🔴 A channel is documented only once it is PUBLISHED
//
// `Delivered()` in the channel contract is `Publication == published`, not "a generator exists". A
// README that lists Homebrew because the formula is generated sends a reader to run a command that
// fails, and an install command that 404s is the worst possible first sentence of a product. Pending
// channels are listed WITH THEIR BLOCKER, which is different from being hidden: a reader deciding
// whether their fleet is covered needs to know a channel is coming and why it is not here.
//
// # 🔴 The verification claim comes from the MANIFEST, not from the channel's prose
//
// This is the check that found a real defect. The channel contract says of the `.deb` and `.rpm`
// channels: "the package's sha256 is listed in the signed release manifest". For release v0.20.0 that is
// FALSE — `SHA256SUMS` lists the five binaries and the two install scripts, and no packages. So the page
// does not repeat the channel's sentence; it states what the published manifest actually covers, and
// lists anything else under a heading that says it cannot be verified.
//
// A trust claim is checked against the artifact, or it is not printed. That is the same rule as the
// checksum, applied to the sentence next to it.
//
// # What this deliberately does not check
//
//   - That the install commands WORK. They come from the channel contract, and the release pipeline's
//     own smoke job is what exercises them on real runner images. This generator transcribes.
//   - That the signature VERIFIES. It records which signature files exist; verifying is the reader's
//     `heros verify-release` on their own machine against a key compiled into their binary — the only
//     place the check means anything.

import { writeFile, mkdir } from "node:fs/promises";
import { join } from "node:path";
import process from "node:process";
import { documentedVersion } from "./lib/docs-version.mjs";

const ROOT = process.cwd();
const OUT_DIR = join(ROOT, "content", "docs", "en", "start");
const OUT = join(OUT_DIR, "install.md");

const cell = (value) => String(value).replace(/\|/g, "\\|");

function table(head, rows) {
  return [`| ${head.join(" | ")} |`, `|${head.map(() => "---").join("|")}|`, ...rows.map((r) => `| ${r.join(" | ")} |`)].join("\n");
}

/** bytes renders a size a human can compare, without pretending to precision. */
function bytes(n) {
  return `${(n / 1_000_000).toFixed(1)} MB`;
}

function osLabel(goos) {
  return { darwin: "macOS", linux: "Linux", windows: "Windows" }[goos] ?? goos;
}

async function main() {
  const { version, facts, release } = await documentedVersion();
  const out = [];

  const delivered = facts.channels.filter((c) => c.delivered);
  const pending = facts.channels.filter((c) => !c.delivered);
  const hasRelease = Boolean(release?.release);

  out.push("---");
  out.push("title: Install the CLI");
  out.push("tier: quickstart");
  out.push(
    "summary: Get the heros binary onto macOS, Linux or Windows — with the checksum and the signature checked before it reaches your PATH.",
  );
  out.push(`platform_version: ${version}`);
  out.push(
    "boundary: This page gets the binary onto your machine and proves it is ours. It does not create an account, and it does not send anything anywhere — the CLI runs offline with no account.",
  );
  out.push("generated: true");
  out.push("order: 1");
  out.push("---");
  out.push("");
  out.push(
    "This page is **generated** from the published release and the install-channel contract on every build. No filename, version or checksum on it was typed by a person.",
  );
  out.push("");

  if (!hasRelease) {
    // Decision 12's absent case. It does not invent a plausible filename.
    out.push("## Packaged installs are not available yet");
    out.push("");
    out.push(
      "No release has been published for this repository, so there is nothing to download and nothing to verify. Rather than print an install command that would 404, this page says so.",
    );
    out.push("");
    out.push("Build from source instead:");
    out.push("");
    out.push("```bash");
    out.push("go build -o heros ./cmd/heros");
    out.push("./heros version");
    out.push("```");
    out.push("");
    await mkdir(OUT_DIR, { recursive: true });
    await writeFile(OUT, `${out.join("\n")}\n`, "utf8");
    console.log("install page generated: no release published, rendered the not-yet-available statement.");
    return;
  }

  // ── The verified path, first and shortest ──────────────────────────────────
  out.push("## The one command");
  out.push("");
  out.push(
    `Version **${version}**. The install script downloads the binary, checks its SHA-256 against the release manifest, checks the manifest's signature against a key pinned inside the script, and only then puts anything on your \`PATH\`. **Any failure is a hard stop** — there is no "continue anyway".`,
  );
  out.push("");

  const curl = delivered.find((c) => c.id === "curl-sh");
  const pwsh = delivered.find((c) => c.id === "powershell");
  if (curl && pwsh) {
    out.push(":::tabs");
    out.push('```bash label="macOS and Linux"');
    out.push(curl.install.replaceAll("{{version}}", version));
    out.push("```");
    out.push('```powershell label="Windows"');
    out.push(pwsh.install.replaceAll("{{version}}", version));
    out.push("```");
    out.push(":::");
    out.push("");
  }

  out.push(
    "There is deliberately **no shorter unverified variant** on this page. A one-liner that installs first and verifies later is the line everyone copies, and it removes the only control that makes running our binary inside your CI defensible.",
  );
  out.push("");
  out.push("Confirm it landed:");
  out.push("");
  out.push("```bash");
  out.push("heros version");
  out.push("```");
  out.push("");

  // ── OS trust posture, from the attestation ────────────────────────────────
  out.push("## What your operating system will say");
  out.push("");
  const trust = release.trust;
  if (!trust) {
    out.push(
      `Release ${release.release} publishes no trust attestation, so this page cannot state a code-signing posture for it. It does not guess: an unstated posture is not the same as an unsigned binary.`,
    );
    out.push("");
  } else {
    for (const key of ["macos", "windows"]) {
      const posture = trust[key];
      if (!posture) continue;
      const name = osLabel(posture.GOOS);
      out.push(`### ${name}`);
      out.push("");
      if (posture.CodeSigned) {
        out.push(
          `The ${name} binaries **are code-signed**${posture.Notarized ? " and notarized" : ""}${posture.Publisher ? ` by ${cell(posture.Publisher)}` : ""}. Your machine should accept them without a prompt.`,
        );
        out.push("");
      } else {
        out.push(
          `The ${name} binaries are **not code-signed and not notarized**. This is a deliberate posture, not an oversight, and you will meet it as a dialog — so here it is before you do.`,
        );
        out.push("");
        if (key === "macos") {
          out.push(
            "Gatekeeper will refuse to run the binary the first time, with a message about an unidentified developer. Clearing the quarantine attribute is one command:",
          );
          out.push("");
          out.push("```bash");
          out.push("xattr -d com.apple.quarantine $(command -v heros)");
          out.push("```");
          out.push("");
          out.push(
            "**What accepting this means:** macOS is telling you it cannot confirm who built this binary, and you are telling it to proceed anyway. That is a real statement, and the honest reason to be comfortable with it is not the dialog — it is that you verified the SHA-256 against a signed manifest, which is a stronger check than an Apple certificate proving a paid developer account exists.",
          );
          out.push("");
        } else {
          out.push(
            "SmartScreen will show a blue \"Windows protected your PC\" panel the first time. **More info → Run anyway** proceeds.",
          );
          out.push("");
          out.push(
            "**What accepting this means:** Windows cannot attribute the binary to a certificate holder. As on macOS, the check that actually establishes provenance here is the signed manifest the install script verified before the file reached your `PATH`.",
          );
          out.push("");
        }
      }
    }
    if (trust.signed_manifest) {
      out.push(
        `In both cases the release **manifest is signed** with key \`${cell(trust.signing_key_id)}\`, and that signature is what the install script and \`heros verify-release\` check. OS code signing and manifest signing answer different questions; this release answers the second one.`,
      );
      out.push("");
    }
  }

  // ── The asset table ────────────────────────────────────────────────────────
  out.push("## Verify a download yourself");
  out.push("");
  out.push(
    `Every file below is listed in \`${release.manifest}\`, the manifest the release signs. These checksums are read from that file at build time — they are not transcribed.`,
  );
  out.push("");
  out.push(
    table(
      ["File", "For", "Size", "SHA-256"],
      release.assets.map((a) => [
        `\`${cell(a.name)}\``,
        a.target ? cell(a.target) : "install script",
        bytes(a.size_bytes),
        `\`${a.sha256}\``,
      ]),
    ),
  );
  out.push("");
  out.push("Check one by hand:");
  out.push("");
  out.push("```bash");
  out.push(`curl -fsSLO https://github.com/${"$"}{OWNER}/${"$"}{REPO}/releases/download/${release.release}/${release.manifest}`);
  out.push(`shasum -a 256 -c ${release.manifest} --ignore-missing`);
  out.push("```");
  out.push("");
  if (release.signatures?.length) {
    out.push(
      `The manifest's own signature ships as ${release.signatures.map((s) => `\`${s}\``).join(" and ")}. \`heros verify-release\` checks the checksums **and then** the signature, against a key compiled into the binary — so verification needs no network and no account:`,
    );
    out.push("");
    out.push("```bash");
    out.push(`heros verify-release --manifest ${release.manifest} --sig ${release.signatures[0]}`);
    out.push("```");
    out.push("");
  }

  if (release.unverifiable_assets?.length) {
    // The honesty section the manifest comparison produced. It exists because the channel contract's
    // prose and the published manifest disagreed, and the manifest is the artifact.
    out.push("### Files this release publishes but does not cover");
    out.push("");
    out.push(
      `These are attached to release ${release.release} but are **not listed in \`${release.manifest}\`**, so the signed manifest does not cover them and neither this page nor \`heros verify-release\` can establish that they are ours:`,
    );
    out.push("");
    out.push(
      table(
        ["File", "For"],
        release.unverifiable_assets.map((a) => [`\`${cell(a.name)}\``, a.target ? cell(a.target) : "—"]),
      ),
    );
    out.push("");
    out.push(
      "They are listed rather than hidden, and they are kept out of the table above rather than mixed into it: a download with no checksum in the signed manifest sitting next to ones that have them would imply a verification it cannot offer. **If you need a verified artifact, use one from the table above.**",
    );
    out.push("");
  }

  // ── Per-channel: pin, upgrade, uninstall ──────────────────────────────────
  out.push("## Channels, pinning, upgrading and removing");
  out.push("");
  out.push(
    "An install you cannot pin is an install you cannot reproduce, so every channel states how to install an exact version. Upgrade and uninstall are given in **each channel's own idiom** — where a package manager owns the file, `heros upgrade` defers to it and prints that manager's command rather than overwriting a file the manager is tracking.",
  );
  out.push("");

  /*
   * 🔴 A channel's commands are checked against the PUBLISHED ASSETS before they are printed.
   *
   * This caught a real defect on its first run. The `.rpm` channel's install command names
   * `heros-{{version}}.x86_64.rpm`; release v0.20.0 publishes `heros-0.20.0-1.x86_64.rpm` — an RPM
   * release number the template does not have. Printing the channel's command verbatim would have
   * published a URL that 404s, which is the exact failure Decision 12 exists to prevent.
   *
   * So a channel whose commands name a file the release does not have gets a refusal in place of its
   * commands, naming the missing filename. That is worse-looking and better: a reader learns the channel
   * is not usable for this release instead of learning that our install instructions do not work.
   */
  function unresolvedAssets(channel, publishedNames) {
    const named = new Set();
    for (const command of [channel.install, channel.pin, channel.upgrade, channel.uninstall]) {
      for (const match of String(command).replaceAll("{{version}}", version).matchAll(/\bheros[-_][0-9][A-Za-z0-9._-]*/g)) {
        named.add(match[0]);
      }
    }
    return [...named].filter((name) => !publishedNames.has(name));
  }

  /** namesChannelAsset reports whether a published filename belongs to this channel's package family. */
  function namesChannelAsset(channel, filename) {
    if (channel.id === "deb") return filename.endsWith(".deb");
    if (channel.id === "rpm") return filename.endsWith(".rpm");
    return false;
  }

  const publishedNames = new Set([
    ...release.assets.map((a) => a.name),
    ...(release.unverifiable_assets ?? []).map((a) => a.name),
  ]);

  for (const channel of delivered) {
    out.push(`### ${cell(channel.label)}`);
    out.push("");
    out.push(
      `${channel.goos.map(osLabel).join(", ")}. ${channel.manager_owned ? "A package manager owns the installed file." : "Installed directly; nothing else owns the file."}`,
    );
    out.push("");

    const unresolved = unresolvedAssets(channel, publishedNames);
    if (unresolved.length > 0) {
      out.push(
        `**Not usable for release ${release.release}.** This channel's commands name ` +
          `${unresolved.map((n) => `\`${cell(n)}\``).join(", ")}, and the release does not publish ` +
          `${unresolved.length === 1 ? "that file" : "those files"}. The commands are withheld rather than ` +
          `printed: an install command that 404s is worse than an absent one, and this page will print them ` +
          `again as soon as the filenames agree.`,
      );
      out.push("");
      continue;
    }

    out.push("```bash");
    out.push(`# install`);
    out.push(channel.install.replaceAll("{{version}}", version));
    out.push(`# pin an exact version`);
    out.push(channel.pin.replaceAll("{{version}}", version));
    out.push(`# upgrade`);
    out.push(channel.upgrade.replaceAll("{{version}}", version));
    out.push(`# remove`);
    out.push(channel.uninstall.replaceAll("{{version}}", version));
    out.push("```");
    out.push("");
    /*
     * 🔴 The verification sentence is CHECKED against the signed manifest before it is printed.
     *
     * The second real defect this generator found. The channel contract says of `.deb` and `.rpm`:
     * "the package's sha256 is listed in the signed release manifest". For release v0.20.0 it is not —
     * SHA256SUMS lists the five binaries and the two install scripts and no packages at all. Repeating
     * the contract's sentence would publish a verification claim the artifact does not support, which is
     * the same failure as a hand-typed checksum wearing a different sentence.
     *
     * So a channel whose assets are published but NOT in the signed manifest gets a generated sentence
     * naming that gap, instead of the contract's prose.
     */
    const unsigned = (release.unverifiable_assets ?? []).filter((asset) =>
      channel.goos.some(() => true) && namesChannelAsset(channel, asset.name),
    );
    if (unsigned.length > 0 && /signed release manifest|manifest is signed/i.test(channel.verification)) {
      out.push(
        `**How this channel establishes the bytes are ours:** it does not, for release ${release.release}. ` +
          `${unsigned.map((a) => `\`${cell(a.name)}\``).join(", ")} ${unsigned.length === 1 ? "is" : "are"} ` +
          `published by this release but ${unsigned.length === 1 ? "is" : "are"} **not listed in ` +
          `\`${release.manifest}\`**, so the signed manifest does not cover ` +
          `${unsigned.length === 1 ? "it" : "them"} and neither this page nor \`heros verify-release\` can ` +
          `establish provenance. Use a binary from the verified table above if that matters to you.`,
      );
    } else {
      out.push(`**How this channel establishes the bytes are ours:** ${cell(channel.verification)}.`);
    }
    out.push("");
  }

  out.push("### What removal leaves behind");
  out.push("");
  out.push(
    "Uninstalling removes the binary. It does **not** remove a `.heros.json` in a repository you configured, or an `llm-eval.yaml` you wrote — those are your files, in your repositories, and a package manager deleting them would be a surprise nobody wants. If you signed in, a stored platform token remains in your user configuration directory until you delete it.",
  );
  out.push("");

  /*
   * A pending channel's BLOCKER sometimes quotes its own install command — "until then
   * `brew install heros-foreal/tap/heros` would fail". That sentence is correct and useful, and the
   * command inside it is still a copyable command for a channel that does not work. `scan-docs-claims`
   * caught exactly that on the first run of this generator.
   *
   * So the explanation is kept and the command is neutralised: the reader learns why the channel is not
   * available without being handed a line that 404s.
   */
  function neutralise(channel, text) {
    const commands = [channel.install, channel.upgrade, channel.uninstall, channel.pin]
      .flatMap((command) => String(command).split(/\s*;\s*/))
      .map((command) => command.trim())
      .filter((command) => command.length > 5)
      .sort((a, b) => b.length - a.length);
    let out = String(text);
    for (const command of commands) {
      out = out.split(command).join("that channel's install command");
    }
    return out;
  }

  if (pending.length > 0) {
    out.push("## Channels that are not available yet");
    out.push("");
    out.push(
      "Each of these has its manifest generated and attached to every release. None of them is installable today, and each says exactly what is missing — a channel listed as unavailable with no reason is indistinguishable from one nobody thought about.",
    );
    out.push("");
    out.push(
      table(
        ["Channel", "For", "What is missing"],
        pending.map((c) => [cell(c.label), c.goos.map(osLabel).join(", "), cell(neutralise(c, c.blocker))]),
      ),
    );
    out.push("");
    out.push("Their commands are deliberately not printed here. A command that does not work yet is worse than an absence.");
    out.push("");
  }

  // ── Air-gapped ─────────────────────────────────────────────────────────────
  out.push("## Installing on a disconnected machine");
  out.push("");
  out.push(
    "Verification happens **on the disconnected machine**, not on the one that had the network. No step below needs the internet or an account.",
  );
  out.push("");
  out.push("On a connected machine, fetch four things: the binary for the target platform, the manifest, its signature, and the `heros` binary you already trust.");
  out.push("");
  out.push("```bash");
  out.push(`curl -fsSLO https://github.com/${"$"}{OWNER}/${"$"}{REPO}/releases/download/${release.release}/heros-${version}-linux-amd64`);
  out.push(`curl -fsSLO https://github.com/${"$"}{OWNER}/${"$"}{REPO}/releases/download/${release.release}/${release.manifest}`);
  if (release.signatures?.length) {
    out.push(`curl -fsSLO https://github.com/${"$"}{OWNER}/${"$"}{REPO}/releases/download/${release.release}/${release.signatures[0]}`);
  }
  out.push("```");
  out.push("");
  out.push("Transfer all of them. Then, on the disconnected machine — before the binary goes anywhere near `PATH`:");
  out.push("");
  out.push("```bash");
  out.push(`shasum -a 256 -c ${release.manifest} --ignore-missing`);
  if (release.signatures?.length) {
    out.push(`heros verify-release --manifest ${release.manifest} --sig ${release.signatures[0]}`);
  }
  out.push(`install -m 0755 heros-${version}-linux-amd64 /usr/local/bin/heros`);
  out.push("```");
  out.push("");
  out.push(
    "The release key is **compiled into the `heros` binary**, which is what makes the signature check work with no network and no keyserver. That is also why the ordering matters: you verify with a binary you already trust, then install the new one.",
  );
  out.push("");

  // ── The ending: name the quickstart's first command ───────────────────────
  out.push("## Next: your first discovery graph");
  out.push("");
  out.push(
    "You have the binary and you have proved it is ours. There is **no config file to edit** between here and a result — the next command runs against a repository you already have:",
  );
  out.push("");
  out.push("```bash");
  out.push("heros discover --repo . --out ir.json --report discovery.json");
  out.push("```");
  out.push("");
  out.push("The [quickstart](/docs/start/quickstart) walks through what it produces and how to read it.");
  out.push("");

  await mkdir(OUT_DIR, { recursive: true });
  await writeFile(OUT, `${out.join("\n")}\n`, "utf8");
  console.log(
    `install page generated from ${release.release}: ${delivered.length} delivered channel(s), ` +
      `${pending.length} pending, ${release.assets.length} verifiable asset(s), ` +
      `${release.unverifiable_assets?.length ?? 0} published but not covered by the signed manifest.`,
  );
}

main().catch((error) => {
  console.error("install content generation FAILED:", error.message);
  process.exit(1);
});
