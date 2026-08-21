# Design — P37: Source-Bound Editors

## Context

Six axis surfaces were built to explain an engine, because when they were built there was no reader's
node to bind them to. P32 supplies the node. This change is what those pages become once the input they
were waiting for exists — and, more delicately, it is a large deletion of text from pages where *some* of
the text is the only thing standing between a reader and a change that cannot land.

The design's job is to make the cut mechanical, so that it is argued once per sentence in a pull request
rather than re-litigated per page under a deadline, and to make the protected text survive a rewrite that
touches every one of these surfaces at once.

## D1 — The subject is resolved in the shell, not on each page

**Decision.** One `(workflow, node)` subject, resolved once, persisted across the axis surfaces, always
named on screen, always changeable.

**Why.** `lib/projection.ts` already argues this for workflows: *"Putting a workflow picker on each of
them would make seven surfaces ask a question the reader did not come there with."* The same argument
applies one level down. The reader who arrives at `/app/memory` from `/app/context` has already chosen.

**Rejected.** *A picker per page.* Less plumbing, and it asks the common reader — one workflow, one node
— a question they do not have.

**Consequence, and it is the risk.** A silently defaulted subject is a reader editing the wrong node. So
the resolution is never silent: the name is on screen even when there was only one candidate. Being told
which one was picked is not the same as being defaulted into it.

## D2 — The moving rule is mechanical, not editorial

**Decision.** A static text block belongs on the reading surface when its content does not vary with the
reader's data, and on the working surface when it does. No third category.

**Why.** The editorial version of this rule — "is this paragraph useful?" — has a different answer for
every reviewer and every deadline. It is also the rule that was in force while these pages grew to 31,628
words, with everyone involved believing they were being concise. A mechanical test can be applied by
someone who did not write the paragraph, which is the only kind of test that holds.

**Consequence.** The rule protects as much as it cuts. A refusal's cause text varies with the reader's
data. `not_measured`'s named missing input varies. The boundary above a control varies with the axis and
the node. All three stay, by the same rule that removes the eight paragraphs above them.

## D3 — A word budget, and its blind spot stated in the same breath

**Decision.** At most one lede per working route within a fixed word budget, asserted by a fence.

**Why.** A budget nobody measures is a preference, and preferences lose to deadlines.

**What the fence cannot see, stated here so nobody trusts it past its limits.** A word count is gameable:
the same content survives as three shorter blocks, as a tooltip, as an accordion, or as a modal. So the
fence is paired with a prohibition on those destinations (FR11) and with review. A fence whose weakness
is undocumented is a fence that will be cited as proof of something it never checked.

## D4 — Nothing is deleted; text moves, and each move has a named destination

**Decision.** Every paragraph leaving a working surface lands on a reading-surface section, and the pull
request enumerates the destinations.

**Why.** The frontend rule against losing a feature in a redesign applies to explanations, because an
explanation that is gone is a capability nobody can find. Recoverable-from-git is not recoverable in
practice: nobody greps for a paragraph they do not know existed.

**Consequence.** The reading surface grows substantially. That is the intended shape — P23 built it to
hold documents, it holds no session, makes no fetch, and scrolls as a document by stated exemption.

**Sequencing consequence.** Destinations ship **before** the text moves. A link to a section that does
not exist yet is a 404 in production, and it is the specific 404 nobody reports because it looks like a
docs problem.

## D5 — The coverage transcription is replaced before its fence is removed

**Decision.** `/app/context` reads coverage per node from the engine at request time. Only then is
`TestContextCoverageTableMatchesEngine` removed.

**Why.** Removing a fence is normally the wrong answer. It is right here for exactly one reason: the
fence guards a hand-transcribed table against the engine, and after the live read there is no
transcription to drift. Removing a fence whose subject still exists is a different act with the same
diff, which is why the pull request must say which one it is doing.

## D6 — `not_connected` is a fourth state, not an empty page

**Decision.** An axis surface with no connected repository renders `not_connected`, names the missing
input, and links to the connection flow. It arrives as a 200 carrying that word.

**Why.** `loadProjection` already keeps three transport treatments deliberately distinct, and its comment
states the reason: *"a 404 would be indistinguishable from a transport failure and would send the reader
to look for a broken deployment when the truth is that they have not opted in."* Not-connected is the
customer's own boundary and gets the same treatment.

**Consequence.** The disconnected reader is the first-time reader, and the right destination for them is
the reading surface. `not_connected` links there as well as to the connection flow — which is what makes
moving the explanation an improvement for that reader rather than a loss.

## D7 — The editor kit is extracted, not designed

**Decision.** The kit is lifted from `/app/memory`'s existing authoring panel — picker, params, preflight,
`unverified` stamp — generalised over the axis vocabulary and bound to the reader's node.

**Why.** That panel already encodes three decisions worth keeping and easy to lose in a rewrite: the
boundary is stated *above* the choice; the control is **live, not disabled**, because a greyed-out control
says nothing about why; and a refusal is never rendered as success. Rewriting from scratch re-decides all
three, and the second one is the one a fresh implementation gets wrong.

This is also the third occurrence of the pattern (context, memory, harness), which is the point at which
the repository's own rule says to abstract rather than to keep copying.

## What this design deliberately does not do

- It does not change any engine behaviour, any materializer boundary, or any refusal's text.
- It does not add a table. The subject selection is per-person UI state; the IR and the registries already
  answer every question the surfaces ask.
- It does not touch the operator console. That is the same complaint about a different console, with
  different vocabularies and a different blast radius, and it is [P38](../p38-agent-contract/).

## Risks this design accepts

- **Six surfaces rewritten in one phase.** Mitigated by landing them one at a time, worst-ratio first,
  behind the existing route structure — not by doing less.
- **The reading surface can become a dumping ground.** Moved text is edited into documents with a table
  of contents, not appended in the order it was cut. This is real work and it is in the task list rather
  than assumed.
- **A shorter page can imply a larger promise.** A surface that used to spend four paragraphs on a
  boundary and now spends none has promised more by saying less. FR15 keeps the boundary above the
  control, and it is treated as a customer-facing commitment rather than a layout preference.
