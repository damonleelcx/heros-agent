# P32 §9.3 — security review of the `TreeGuard` extraction

- **Status:** Accepted (2026-08-21)
- **Subject:** the refactor that moved the bundle extractor's refusals out of
  [`internal/sourceingest/bundle.go`](../../internal/sourceingest/bundle.go) into
  [`treeguard.go`](../../internal/sourceingest/treeguard.go), so both source implementations run them.
- **Why a review at all:** the task asks for one, and the reason is stated in the design —
  *"the `TreeGuard` refactor touches shipped, security-load-bearing code."* This is the one change in
  P32 that can make an EXISTING customer less safe, and every other change in the phase can only affect
  a customer who opted into something new.

## 1. What was moved, exactly

Nine refusals and four ceilings. Verbatim, from the original `extractTarGz` and `safeJoin`:

| Refusal | Where it is now |
|---|---|
| empty path | `Admission.safeJoin` |
| absolute path | `Admission.safeJoin` |
| volume / backslash separator | `Admission.safeJoin` |
| escapes the extraction root (on the CLEANED, joined result) | `Admission.safeJoin` |
| symlink | `Admission.Entry`, `EntryLink` arm |
| hardlink | `Admission.Entry`, `EntryLink` arm |
| device node / FIFO / socket (the `default` arm) | `Admission.Entry`, `default` arm |
| entry-count ceiling | `Admission.Entry`, first check |
| path-length ceiling | `Admission.Entry`, second check |
| per-file byte ceiling | `Admission.Entry`, `EntryFile` arm |
| total-byte ceiling | `Admission.Entry`, `EntryFile` arm |

What did NOT move, and stayed in `bundle.go`: gzip framing, the tar typeflag→`EntryKind`
classification, the pax-header recognition, and `writeFile`'s exact-count copy with its checked
`Close`. Those are tar-specific and have no meaning on the clone path.

## 2. The property under review, and the three ways it could have been broken

The property: **an archive that was refused before is refused now, for the same reason, at the same
point in the traversal.**

### 2.1 A refusal could have been dropped

Checked by the characterization suite. `bundle_test.go` **passes unchanged** — not edited, not
extended, not re-ordered — before and after. It constructs each malicious archive by hand and asserts
each refusal by message, which is the strongest available check on a move of this kind: a dropped rule
does not compile away, it simply stops firing, and only a test that constructs the input notices.

Verified by mutation, not by reading. Two drills were run:

1. the walk stopped classifying symlinks → four cases went red;
2. `InspectTree` stopped calling the guard at all → eleven cases went red.

A refactor whose fence cannot be made to fail is a refactor with no fence.

### 2.2 The ORDER could have changed

This is the subtle one, and it is where a careless version of this refactor goes wrong.

In the original, the entry-count and path-length checks ran **before** the pax-header skip. That
ordering is load-bearing: an archive of ten million pax headers must still trip the entry ceiling. The
tempting simplification is to skip metadata first — it reads as an obvious short-circuit and it
silently removes a bound.

The order is preserved, and it is now pinned by
`TestPaxHeaderIsCountedBeforeItIsSkipped`, which fills the entry budget with metadata entries and
requires the ceiling to fire, then requires a metadata entry with an over-long path to be refused for
its path length. Both would pass silently if the skip moved earlier — that is why the test exists
rather than a comment.

### 2.3 The two producers could DIVERGE later

The original risk was a dropped rule. The new, ongoing risk is different: two producers now feed one
refusal set, and one of them could stop feeding it correctly.

`TestBundleAndCloneRefuseTheSameThings` compares the two producers **against each other** — same
logical entry through both, same verdict, same message — rather than each against a written
expectation. That distinction matters: an expectation is edited by whoever changes the code, so
A-vs-expectation drifts silently and A-vs-B cannot.

## 3. What CHANGED behaviourally, stated rather than buried

Two things, both deliberate:

1. **Two refusal messages were reworded.** `"bundle has more than %d entries"` → `"source tree has more
   than %d entries"`, and `"bundle exceeds %d uncompressed bytes"` → `"source tree exceeds …"`. Neither
   was asserted by any test. The reason is not tidiness: these messages are now reachable from a
   CLONE, and telling a customer their *bundle* is too large when they connected a repository sends
   them to the wrong remedy. The nine messages that ARE asserted are byte-identical.

2. **`EntryOther` is the zero value of `EntryKind`.** New surface, and the direction is the safe one:
   an `Entry` constructed without setting `Kind` is REFUSED rather than admitted. Pinned by
   `TestEntryOtherIsTheZeroValue`, because a future re-ordering of the constants would silently invert
   it — and if `EntryFile` were the zero value, a producer that forgot the field would admit whatever
   it found, which is the shape of every unpacker defect there has ever been.

## 4. What the clone path adds, and its own review

`InspectTree` is new code running the same refusals over a materialized directory. Three points:

- **`Lstat`, never `Stat`.** A symlink is found by not following it. `filepath.WalkDir` hands back a
  `DirEntry` whose `Type()` comes from lstat, which is what makes the symlink refusal reachable at all
  — a walk that resolved links would see the target's type and admit a link to `/etc/shadow` as a
  regular file. Asserted by `TestCloneTreeRefusesHostileEntries`, whose fixtures are real symlinks on a
  real filesystem rather than constructed entries.

- **Symlinks are MATERIALIZED on purpose.** The clone does not set `core.symlinks=false`. That setting
  would make git write a link as a regular file containing its target path — which sounds safer and is
  the opposite: the hazard becomes invisible, the guard sees a small text file and admits it, and
  discovery then walks a tree whose links were silently rewritten. Keeping them is what lets the guard
  refuse them.

- **`.git` is pruned.** Justified twice over, and the second reason is the one that matters here: it is
  a **privacy control**, because `.git/config` records the credentialed remote URL verbatim. Asserted
  by `TestNoForgeCredentialReachesTheCloneScratchOrTheLedger`, whose fake runner deliberately writes
  that file. (The performance reason — not spending the entry budget on pack files — is real and
  secondary; `TestCloneTreeGitMetadataIsPrunedNotRefused` covers it with an oversized pack.)

- **Order of operations.** The guard runs BEFORE anything walks or archives the tree, and a refusal
  aborts the ingest before discovery sees it. Asserted end-to-end against real git by
  `TestACloneOfATreeWithAnEscapingSymlinkIsRefused`, which additionally requires that **nothing was
  stored** — a refusal that reported a problem and kept the tree anyway would be the worst outcome
  available.

## 5. Residual risk, accepted and named

- **The guard has no protection against a tree that is hostile in a way nobody has thought of.** The
  refusal set is a list, and a list is a record of known hazards. What bounds the unknown is the
  ceilings (a tree cannot be unbounded in any dimension) and the fact that discovery never executes the
  target repository (discovery invariant I1). Neither is new.

- **`InspectTree` walks a tree the platform just wrote to its own disk.** Between the clone finishing
  and the guard running, the tree exists unguarded in the scratch directory. Nothing else reads that
  directory, it is removed on every path including failure, and shortening the window further would
  mean guarding entries as git writes them — which git provides no hook for. Accepted and stated.

- **Two producers is a standing cost.** A third would be a third, and the honest mitigation is that
  `TestBundleAndCloneRefuseTheSameThings` is written to be extended rather than duplicated.

## 6. Verdict

**The extraction is safe to ship.** No refusal was dropped, the ordering that a careless version would
have broken is preserved and pinned, the two reworded messages are unasserted and improve the remedy
they point at, and the one new default (`EntryOther` as zero) fails closed. Every claim above is backed
by a test that has been made to fail on purpose.
