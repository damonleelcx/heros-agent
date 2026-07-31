# Contributing to the documentation and the legal surface

Closes **task 13.3**: *anything a fence cannot check becomes a named review responsibility, not an
implied guarantee.*

Twelve build-time fences check this corpus. This page exists because of what they do **not** check —
and because a reviewer who believes the fences cover more than they do stops doing the half only a
person can.

---

## 1. What the machine checks, so you do not have to

You cannot merge a change that breaks any of these. Each ships with a fixture proving it goes red.

| Fence | Refuses |
|---|---|
| `scan-content` | raw HTML, an inline handler, an external script/font/stylesheet, or a third-party origin in public-surface markup |
| `scan-secrets` | credential-shaped content — provider keys, PEM blocks, bearer tokens, JWTs |
| `scan-docs-claims` | a capability that is not `shipped: true` with an owning phase; an install command for a channel the pipeline does not publish |
| `scan-cli` | **both directions** — a `heros …` invocation naming a command or flag the registry lacks, *and* a registry command with no reference entry; plus an exit code whose documented meaning disagrees with `internal/cli/exit.go` |
| `scan-api` | any documented HTTP endpoint, while the API artifact is absent. It refuses rather than passing vacuously |
| `scan-metric` | a metric whose name, unit or computation disagrees with the harness, or that cites no computation site |
| `scan-install` | a hand-typed checksum, filename or version; a path that reaches `PATH` before verifying; a trust claim the pipeline did not earn |
| `scan-links` | an internal link or anchor that does not resolve; a removed slug with no redirect; an unlisted external origin; the reserved `/docs/v/` segment |
| `scan-legal` | a missing front-matter field; an ambiguous `material`; a deleted archived version; **text edited under an unchanged version number** |
| `scan-repo-link` | a repository link that is private or does not exist |
| `scan-claims`, `scan-tokens`, `scan-strings`, `scan-markup`, `scan-bundle` | the console's pre-existing rules, unchanged |

Run them: `npm run scan:docs` in `web/console`.

---

## 2. 🔴 What no fence checks — and is therefore yours

**This is the section that matters.** Every item below has passed a green build while being wrong.

### 2.1 Tone, emphasis, and what a page omits

A fence can check that a claim is listed in the capability manifest. It cannot check whether the
paragraph around it **oversells**, whether a caveat is buried three screens below the promise, or
whether the most important limitation was simply not written.

Omission is the one that gets past everybody, because there is nothing on the page to react to. When
you review a documentation change, ask: *what would a reader do wrong after reading only this?*

### 2.2 Whether a generated page's SOURCE is right

`scan-cli` proves the reference matches the registry. **Neither proves the registry is correct.** A
command summary that describes what the command was supposed to do, an example that runs but does not
demonstrate the thing, a flag listed under a command that ignores it — all of these generate cleanly.

If you change `internal/cli/registry.go`, you are writing documentation. Read the generated page.

### 2.3 Whether a metric's stated computation matches the code at the cited site

`scan-metric` proves the page and the catalogue agree on name, unit and computation, and that the
citation points somewhere real. It **cannot read the function**.

The citation exists so this check is cheap for a human. Open the file.

### 2.4 Whether a sample output is still what the product produces

Samples are labelled with the version that produced them, and that label is the whole guarantee. A
sample from 0.20.0 sitting under a page documenting 0.24.0 is honest and stale at the same time.

**A model-generated example may never be presented as a real run.** If you did not run it, mark it
illustrative.

### 2.5 Whether the legal text is correct

`scan-legal` proves the front matter is complete, the materiality is declared, no archived version
was deleted, and no text moved under an unchanged version number. It has no opinion about whether a
limitation-of-liability clause is enforceable.

**Legal text changes go to counsel.** Engineering owns the structure, the front matter and the
commercial facts — see [`docs/sales/P23-terms-reconciliation.md`](sales/P23-terms-reconciliation.md),
which must be re-run for every version that changes a commercial sentence.

### 2.6 Whether `material: true` was declared correctly

**No machine can judge materiality**, and this one does not try. It forces the declaration to exist
and be attributable.

Get it wrong in one direction and every customer is interrupted for a typo fix. Get it wrong in the
other and a rights-changing amendment ships silently. The reviewer's question is simple: *does this
change what a customer is agreeing to?*

### 2.7 Whether an anchor is worth citing for years

`scan-links` proves anchors resolve and that a removed one has a redirect. It cannot tell you that
`#step-3` is a bad name for something a CLI error message will point at for the next three years.

Anchors are a published contract. Name headings as if they will outlive the page.

---

## 3. How to change something

### A documentation page

1. Edit under `web/console/content/docs/en/**`. Markdown only — no HTML, no components.
2. Front matter is required: `title`, `tier`, `summary`, `platform_version`, `boundary`. `claims` if
   the page describes a capability.
3. `npm run build`. Every fence runs.
4. **State in the pull request which fences ran.** A content change that passed because a fence was
   not wired in is a content change nobody checked.

### A generated page

Do not. `reference/cli.md`, `reference/schemas.md`, `reference/metrics.md`, `reference/http-api.md`
and `start/install.md` carry `generated: true` and are overwritten on every build.

Change the source — the CLI registry, the metric catalogue, `schemas/`, the release — then
`make docs-facts` and rebuild.

### A legal document

1. **Never edit a published version in place.** Add a new file with a new version.
2. Decide `material` deliberately (§2.6).
3. The old version stays where it is, forever. Deleting it orphans every consent record pointing at
   it, and there is no recovery.
4. Get counsel's review before the version is effective.

### An anchor

Renaming a heading changes an anchor. If the old one was published, add an entry to
`web/console/content/redirects.json` **in the same change** — the fence compares against the last
commit and will otherwise refuse.

---

## 4. The two standing rules for every pull request in this area

1. **A fence without a failing fixture is not delivered.** If you add a fence, add a fixture under
   `web/console/tests/support/fences/` that proves it goes red, in the same change.
2. **Every content change states which fences ran.** In the pull request body, not in your head.

---

## 5. If a fence is wrong

It happens — three of these fences flagged correct content on their first run, and each time the
fence was fixed rather than the content.

**Do not add an allowlist entry to make a red build green.** Work out which of three cases it is:

- **The fence is right and the content is wrong.** Fix the content.
- **The fence is right and the sentence is a disclosure.** A page saying "this release does *not*
  publish that file" must not be punished for naming it. Both `scan-install` and `scan-legal` carry
  explicit disclosure guards for exactly this — extend the guard, do not delete the check.
- **The fence is wrong.** Fix the fence, and add a fixture for the case it got wrong.

A fence whose easiest fix is deleting an honest sentence is worse than no fence.
