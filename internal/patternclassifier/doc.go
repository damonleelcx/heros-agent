// Package patternclassifier labels each SUBGRAPH of a Workflow IR with the agentic pattern(s) it
// implements, each carrying a confidence — P3.5.
//
// It is a DISPATCHER, not decoration: the label selects which metric-set (and later which failure
// taxonomy, eval cases, improvement operators) is in scope for that region. The consuming seam is
// MetricSetFor(pattern) → MetricSet; P4's metric-set selection keys off it. That is why the output
// is a set of per-subgraph labels and never one label for the whole workflow: a workflow with a
// router in one region and a RAG pipeline in another needs two DIFFERENT metric-sets.
//
// # Detection design (信号源 / baseline / 判别力证据)
//
//	信号源 (signal source):
//	  IR topology + node metadata ONLY — typed data/control edges (P0), tools_skills bound to the
//	  P2 Skill Registry, context_assembly.policy, invocation_semantics, model tier. All of it is
//	  produced by P1 Discovery from a pinned commit, so the signal is fully controlled: the same
//	  commit yields the same IR yields the same labels. No runtime traces are consumed (P5).
//
//	baseline (what is compared against):
//	  The eight structural signatures in PRD §8.2, each expressed as a topology predicate, and the
//	  hand-labeled fixture set in testdata/ that pins the expected label for each signature and for
//	  each of its near-misses. Detector confidence is calibrated against THAT fixture set
//	  (calibration_test.go), the same way an LLM judge would be — not asserted from intuition.
//
//	判别力证据 (discriminative power evidence):
//	  calibration_test.go reports per-detector agreement (TP/FP/FN) over the hand-labeled fixtures
//	  and fails if any detector fires on a near-miss. The near-misses are the evidence that the
//	  signatures DISCRIMINATE rather than merely match: a linear chain is not Routing, an
//	  empty-tools_skills node is not Tool Use, a fan-out with no merge is not Parallelization.
//	  Because every detector is a pure function of the IR, discriminative power is exact (an
//	  assertion, not a distribution) — there is no sampling noise to model.
//
// # Honesty boundary
//
// Structure can prove a loop EXISTS; it cannot prove the loop ITERATES. Patterns whose definition is
// behavioral (Reflection iterating > 1, Planning consuming a task list, voting, memory read/write,
// a human-approval pause) are therefore emitted as structural CANDIDATES with a capped confidence
// (BehavioralCandidateCap) and are never asserted as confirmed. Confirmation lands in P5 when
// dynamic traces exist.
//
// # Layering
//
// Rules first, LLM for the fuzzy residue, never unverified — the same discipline as the diagnosis
// engine. Deterministic detectors are the primary layer; the constrained LLM classifier runs ONLY on
// subgraphs no detector covers confidently, selects from the closed 20-pattern taxonomy, must return
// a confidence, and never overrides a rule label. A fully rule-covered IR makes ZERO LLM calls.
package patternclassifier
