# P23 — acceptance evidence

Closes **task 12.10** and records the §13 acceptance outcomes (task 13.2).

**Date:** 2026-07-31. **Against:** published release **v0.20.0**, `heros` 0.20.0, console at
commit `feat/p23-legal-and-docs`.

---

## 0. 🔴 Who performed this, stated first because it changes what it is worth

**The end-to-end below was performed by the agent that wrote the code, not by an independent reviewer on
a clean machine.**

Task 12.10 asks for a reviewer who follows the published install page and quickstart *without reading
source or asking a question*. That is a real and different check: its whole value is that the person
doing it does not already know the answers, and no amount of care makes an author into that person.

So this document records **what was actually executed** — which is a genuine end-to-end against the real
published release, and is worth having — and leaves 12.10's independent half **open**, in
[§5](#5-what-is-still-open). Reporting it as satisfied would be exactly the failure this phase exists to
prevent: a green record over a check nobody performed.

---

## 1. The install page, followed as written

The path published at `/docs/start/install` was executed against the real GitHub Release, on macOS
(arm64), in a clean directory.

**Downloaded** the binary, the manifest and the manifest's signature:

```
heros-0.20.0-darwin-arm64   16,662,610 bytes
SHA256SUMS                         618 bytes
SHA256SUMS.sig                     129 bytes
```

**Checksum, before anything reached `PATH`:**

```
$ shasum -a 256 -c SHA256SUMS --ignore-missing
heros-0.20.0-darwin-arm64: OK
```

**Signature over the manifest:**

```
$ heros verify-release --manifest SHA256SUMS --sig SHA256SUMS.sig
{
  "contract_version": "p11.cli.v1",
  "command": "verify-release",
  "ok": true,
  "exit_code": 0,
  "data": {
    "signing_key_id": "heros-release-2026c",
    "checked": ["heros-0.20.0-darwin-arm64"],
    "manifest_entries": 7
  }
}
```

**The OS-trust posture the page warned about, met exactly as described.** Gatekeeper refused the binary
on first run; the page's one-line quarantine clear worked:

```
$ xattr -d com.apple.quarantine heros-0.20.0-darwin-arm64
$ ./heros-0.20.0-darwin-arm64 version
heros 0.20.0 · contract p11.link.v1 · output p11.cli.v1 · link https://heros-agent.space
```

**Verdict: the install page is accurate.** Every filename, every checksum and the signing key id on the
page came from the release itself, and each matched what the release actually served.

---

## 2. The quickstart, followed as written

Against a repository the page's own example names —
[nousresearch/hermes-agent](https://github.com/nousresearch/hermes-agent) at `98105f31`.

```
$ heros discover --repo hermes-agent --out ir.json --report discovery.json
discover: analyzing hermes-agent (offline, no account)…
discover: 26 nodes, 0 edges → ir.json
```

```json
{
  "contract_version": "p11.cli.v1",
  "command": "discover",
  "ok": true,
  "exit_code": 0,
  "data": {
    "workflow_id": "workflow",
    "ir_path": "ir.json",
    "report_path": "discovery.json",
    "nodes": 26,
    "edges": 0,
    "source_revision": "98105f31f46d3de58a8f69a2a439cee3f7a5e389"
  }
}
```

**No config file was edited**, no account existed, and no network request was made. That is the P20
first-run contract the quickstart claims, executed.

**The sample on the quickstart page is this run**, labelled with the version that produced it (task 5.6).
It is not an illustration and it is not model-generated.

### The `edges: 0` finding

The quickstart explains this rather than hiding it: 26 call sites, no statically provable edges between
them. Reading the page after seeing the number, the explanation matched — the graph states what it
proved and keeps runtime-only edges separate.

---

## 3. Reading and printing both legal documents

Both were read end to end in a browser at 1280×900 and at 375×812, in both themes.

| Check | Outcome |
|---|---|
| Terms renders 15 sections with a scroll-spy TOC | ✅ |
| Privacy Notice renders 11 sections | ✅ |
| The current section is marked by `aria-current="location"` **and the word "Reading"** | ✅ verified in the DOM, not by eye |
| Identity strip carries version, effective date, materiality, language and the sha256 | ✅ |
| `/legal/terms/v/1.0.0` serves its own text and does **not** redirect | ✅ |
| The print stylesheet emits kind, version, effective date and full hash | ✅ asserted in `tests/legal.test.mjs` |
| Both documents readable with **no session** | ✅ |
| Both documents readable with the **platform stopped** | ✅ upstream counter did not move |

**Content hashes at the time of this record:**

| Document | Version | sha256 |
|---|---|---|
| Terms of Service | 1.0.0 | `3d83017760dd1b4c3e505a9e0995d07f61fb4e5296c11fd18b878d07afb97b30` |
| Privacy Notice | 1.0.0 | `d4083974abc2c44eefbbaa469235a605d14fa846e4e20903033b80facf1e130a` |

These are the values `/legal/manifest.json` and `/api/health` both report, and the two were asserted
equal (task 11.2).

---

## 4. The §13 acceptance checklist

| # | Check | Outcome |
|---|---|---|
| 12.1 | Availability: platform stopped, every legal and docs route 200, upstream counter still | ✅ `tests/legal.test.mjs` |
| 12.2 | Editing a document body without bumping the version fails the build; deleting an archived version fails the build | ✅ both watched red, then restored |
| 12.3 | Double-submit → one row against **real Postgres**; material → asked again; non-material → not; forced write failure → nothing rendered as accepted | ✅ 12 concurrent inserts → 1 row; failure path verified in a browser |
| 12.4 | Each fence fixture fails individually | ✅ 12 fixtures, 12 refusals, plus a pass over the real corpus |
| 12.5 | A registry subcommand with no reference entry fails; a swapped exit-code meaning fails | ✅ |
| 12.6 | An unpublished channel fails; a hand-typed checksum fails; a path reaching `PATH` before verifying fails | ✅ |
| 12.7 | shields.io, `api.github.com`, a hand-typed count, a private repository — each fails; the CSP refuses independently | ✅ 9 assertions |
| 12.8 | Every docs page reachable by navigation; every published deep link resolves | ✅ |
| 12.9 | WCAG AA in both themes; the print stylesheet emits the identity | ✅ token pairs + a new assertion that the reading surface uses no alpha-reduced body text |
| 12.10 | The human end-to-end | ⚠️ **partially** — see §5 |

**Defects found and fixed during this phase**, recorded because a phase that reports no findings usually
means nobody looked:

1. **Both client islands were dead.** The reading routes were `force-static`; a nonce cannot exist in
   build-time HTML, so the nonce + `strict-dynamic` CSP refused every script. The build was green and the
   pages rendered perfectly. Found by clicking, not by testing.
2. **`.deb` / `.rpm` claim manifest coverage they do not have.** `SHA256SUMS` for v0.20.0 lists five
   binaries and two scripts and no packages. The install page now generates an honest per-channel
   verification sentence from the manifest instead of repeating the channel contract's prose.
3. **The `.rpm` install command names an asset the release does not publish** —
   `heros-0.20.0.x86_64.rpm` against a published `heros-0.20.0-1.x86_64.rpm`. The generator withholds the
   commands and says why.
4. **A table row with an escaped pipe was refused by the Markdown parser.** The parser was right that the
   row did not line up and wrong about why; `splitRow` did not honour `\|`.
5. **The consent path was filed in `scope.ts`**, which the P22 ADR-008 fence pins byte-for-byte. The
   fence was right twice over: that module is tenant *scoping*, and this path carries no tenant.

(2) and (3) are pre-existing in P20's channel contract. P23's fences make the documentation honest about
them; `internal/distribution/channels.go` is still wrong and is **not** fixed here.

---

## 5. What is still open

Named rather than implied.

| # | Open | Why it is not closed |
|---|---|---|
| O1 | **12.10's independent reviewer.** A person who did not write this, on a machine that has never had the binary, following the install page and quickstart without reading source or asking a question. | An author cannot perform it. §1 records what *was* executed; this is the part that remains. |
| O2 | **Counsel review of Terms v1.0.0.** `docs/sales/P23-terms-reconciliation.md` establishes that every commercial sentence matches a mechanism. It does not establish enforceability. | Counsel's. |
| O3 | **A printed read of both documents on paper.** The print stylesheet is asserted to emit the identity, and the pages were read on screen. Nobody has held the printout. | Needs a printer and a person. |
| O4 | **`internal/distribution/channels.go`'s two wrong claims** (§4 items 2 and 3). | P20's file; out of P23's scope, reported rather than edited. |
| O5 | **The consent endpoints have not run against a live platform deployment.** The domain and store are proved against real Postgres; the HTTP legs are covered by unit and structural tests. | No vendor-operated deployment exists to run them on (see the data inventory §0). |

---

## 6. How to reproduce §1 and §2

```bash
curl -fsSL -o heros-0.20.0-darwin-arm64 https://github.com/damonleelcx/heros-agent/releases/download/v0.20.0/heros-0.20.0-darwin-arm64
curl -fsSL -o SHA256SUMS     https://github.com/damonleelcx/heros-agent/releases/download/v0.20.0/SHA256SUMS
curl -fsSL -o SHA256SUMS.sig https://github.com/damonleelcx/heros-agent/releases/download/v0.20.0/SHA256SUMS.sig
shasum -a 256 -c SHA256SUMS --ignore-missing
heros verify-release --manifest SHA256SUMS --sig SHA256SUMS.sig
git clone --depth 1 https://github.com/nousresearch/hermes-agent
./heros-0.20.0-darwin-arm64 discover --repo hermes-agent --out ir.json --report discovery.json
```
