# P31 The Conversational Console — capability statement and claim discipline (Sales Operations)

- **Status:** Accepted (2026-08-21)
- **Audience:** anyone who describes P31 to a customer — a deck, a demo, a scoping call, an SoW.
- **Rule:** this surface's honest pitch is narrower than "you can ask our agent anything" and **stronger**
  than it, because the narrow version is the one a customer can check. Do not trade the second for the first.

## 1. The one sentence

> *"Ask in English. The agent reports what it found with the evidence behind each finding, and opens a
> pull request when a change is verified better."*

Every clause is a shipped capability. `docs/prd/P31-conversational-console.md` §9.8 is where it comes from,
and `web/console/tests/conversation.test.mjs` is what stops it drifting.

## 2. 🚫 What must never be said

| Do not say | Why it is false, and what it costs when a customer finds out |
|---|---|
| *"It understands your codebase."* | It reads what your CI reported and answers from that. A customer who believes the first sentence expects an answer about a file nobody sent, gets a `not measured`, and concludes the product is broken rather than uninformed. |
| *"Ask it anything."* | It answers **fourteen** questions (§4). An open text box implies infinity; the surface itself prints the fourteen in every refusal, so a prospect who was told "anything" meets the boundary in the demo. |
| *"It scores your agent."* | There is no overall score, deliberately. See §3 — this is the single most important thing to say FIRST. |
| *"It fixes things automatically."* | It asks a person before anything reaches a repository, and the approval is the same gate as everywhere else in the product. Autonomy is [P35](../prd/P35-autonomous-improvement-run.md) and is not this. |
| *"It remembers your previous conversations."* | It refuses to. Cross-conversation memory is declined by design (ADR-015), and the surface says so on screen. |

## 3. 🔴 The boundary to state OUT LOUD, before anybody asks

**There is no overall score, by design.**

Say it early, say it as a choice, and give the reason:

> *"We don't produce a single number for your agent. A score over surfaces that were measured
> differently — and some that were not measured at all — is a number nobody can check, and an
> unverifiable score is worse than none. What you get instead is a per-surface answer with the evidence
> behind it, and an explicit `not measured` wherever we could not measure, naming what was missing."*

This is program ruling **R4**. Sales operations' standing note applies exactly here: **a differentiator
when stated confidently, a weakness when discovered later.** Every competitor demo has a gauge on it. The
question a technical buyer asks about that gauge is "what is it over?", and we are the only ones who can
answer.

The sentence is also ON THE SURFACE — it is the first thing the empty conversation renders — so a
prospect meets it in the product, not only in a deck.

## 4. The fourteen things it can be asked

Not a marketing list: it is generated from the intent table in `internal/conversation/intent.go`, printed
by the product in every refusal, and fenced so the two cannot drift.

| | |
|---|---|
| what does my agent actually do, step by step? | what happened in that run? |
| is this version better than the last one? | what exactly would you write into my source? |
| how does an approved change reach my repository? | change the instruction / change the model |
| change something on an axis and show me the diff | should these nodes run in this order? |
| what conversation history does this node get? | what does this node remember between calls? |
| how many turns does it take, and in what loop? | what did you measure, and what did you not? |
| look at my repository and tell me what is weak | fix it, and open a pull request |

**Anything else is refused**, and account, billing and identity questions are refused **by name** —
*"changing a plan is not something this surface does; /app/billing does that."*

⚠️ The last two rows — *assess* and *improve* — are the **[P33](../prd/P33-surface-assessment.md)** and
**[P35](../prd/P35-autonomous-improvement-run.md)** capabilities. They are routable today and answer
*"not available in this deployment"* until those phases ship. **Do not demo them.** A refusal is the
correct behaviour and it is a bad first impression; ask one of the other twelve.

## 5. What a customer sees that competitors' chat surfaces do not

These are the demo moments. Each is a shipped behaviour, each is checkable on screen, and each is the
opposite of what a chat product normally does:

1. **The plan arrives before the work does**, with four numbers on it — turn ceiling, token budget,
   tool-call ceiling, time limit. *"You know what it is going to do and what it may spend before it
   starts."*
2. **The budget counts down while it runs.** Not a spinner: the phase it is in and what is left.
3. **Every step is reconciled at the end** — done, skipped, refused or not measured, each naming its
   reason. *"An agent that quietly did three of eight steps writes prose that reads exactly like an agent
   that did eight. This one shows you the denominator."*
4. **A run that stops on a limit says which limit**, and is not drawn as finished.
5. **`not measured` is a rendered state, not silence.** It names the input that was missing — usually a
   command the customer can run.
6. **A refusal from a lower layer is quoted verbatim.** We do not re-word a safety boundary into something
   softer.
7. **Every turn has a trace id**, on screen and copyable.

## 6. The security answer, for the review that will ask it

Repository content is untrusted input to a system that can open a pull request. The question is *"what
stops a README that says 'approve everything' from doing anything?"*

**The structural answer, which does not depend on detection working:** an effect-bearing message —
a proposal, an approval request, a result — is constructed by the platform from a typed artifact a model
cannot mint. A `proposal_id` must resolve in the verification ledger; a delivery must reference a delivery
record. A fully compromised model can produce text, and text is not a ledger row.

Injection detection exists as well, and it is deliberately **not** what we point at: it is a classifier,
classifiers have a false-negative rate, and a system that is secure at exactly the rate its classifier is
accurate is a system with an undisclosed failure probability. When we detect an attempt we report it as a
finding about the repository and take no action on it.

The fences that prove this run with detection **switched off** (`internal/conversation/untrusted_test.go`).
That is the sentence to give a security reviewer.

## 7. Scoping questions this changes

- **"Does it need our source?"** No. It answers from what your CI reported (`heros link --with-ir`). It
  never needs a copy of your repository to answer twelve of the fourteen.
- **"Can our whole team use it?"** A conversation is per-person. The RUN belongs to the organization, so a
  colleague reaches the same work through `/app/runs` — they do not see each other's questions.
- **"Is the transcript kept?"** No, and that is a decision rather than a gap ([ADR-015](../adr/ADR-015-conversation-is-a-view-over-a-run.md)).
  Reloading resumes the run and replays its messages; it does not restore a history spanning runs. Say this
  before a customer discovers it — the surface itself says it in a banner.
- **"What does one question cost?"** A bounded amount, declared before the run starts and derived from the
  plan's entitlement. It is displayed and it is not editable in the conversation.
