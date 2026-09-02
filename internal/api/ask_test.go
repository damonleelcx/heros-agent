package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/tenancy"
)

// ask_test.go fences `POST /api/ask`, the endpoint this whole product is a front for.
//
// # 🔴 Why this file exists, and what it is protecting
//
// It had NO tests. Not "thin coverage" — none. The routing package had a holdout suite and the intent
// package had structural fences, but nothing asserted that a sentence arriving over HTTP produced the
// response the console renders. So the layer where routing, the safety floor, subject state and tenancy
// actually meet was the one layer nobody checked.
//
// These tests were written BEFORE the conversational rewrite, deliberately, to pin the behaviour that
// must survive it. Two of the properties below are load-bearing for reasons that have nothing to do
// with conversation quality:
//
//   - An unbounded request is refused BEFORE anything is planned. "Keep going until it is perfect" must
//     not first become a goal and then get stopped.
//   - A sentence naming another surface is REDIRECTED, not acted on. Connecting a repository creates a
//     standing read grant, and the disclosure has to be displayed before the grant exists — so an agent
//     that could act on "connect my repo" is a path around the consent screen.
//
// Both must hold no matter what any model later decides, which is why they are asserted here at the
// HTTP boundary rather than only in `internal/router`.

// askAs posts a sentence and returns the decoded response.
func (hz *harness) askAs(t *testing.T, as *http.Cookie, text string) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	rec := hz.do(t, "POST", "/api/ask", string(body), as)
	if rec.Code != http.StatusOK {
		t.Fatalf("ask %q: %d %s", text, rec.Code, rec.Body.String())
	}
	return decode(t, rec)
}

// TestAnUnboundedRequestIsRefusedBeforeAnythingIsPlanned.
//
// 🔴 The ORDER is the property, not merely the refusal. `handleAsk` checks Unbounded before it routes,
// so no goal is created, no ceiling is negotiated and nothing is spent. A version that planned first
// and refused afterwards would pass a test that only checked the response body.
func TestAnUnboundedRequestIsRefusedBeforeAnythingIsPlanned(t *testing.T) {
	hz := newHarness(t)
	member, _ := hz.user(t, tenancy.Member)

	for _, sentence := range []string{
		"keep improving until it is perfect",
		"fix everything you can find",
		"just keep going until you run out",
	} {
		got := hz.askAs(t, member, sentence)
		if got["kind"] != "refusal" {
			t.Errorf("%q returned kind %q, want refusal", sentence, got["kind"])
			continue
		}
		if got["cause"] != string(bounds.UnboundedRequested) {
			t.Errorf("%q refused with cause %q, want %q", sentence, got["cause"],
				bounds.UnboundedRequested)
		}
		if got["next_action"] == "" {
			t.Errorf("%q was refused with no next action; a refusal that does not say how to "+
				"succeed is a dead end", sentence)
		}
		// Nothing may have been planned. A goal id in the response would mean the refusal happened
		// after the run existed.
		if _, planned := got["goal_id"]; planned {
			t.Errorf("%q produced a goal before being refused", sentence)
		}
	}

	// The mirror half: a bounded version of the same request must NOT be caught by this rule. A floor
	// that refuses everything passes a one-sided test perfectly.
	got := hz.askAs(t, member, "look at my repository and tell me what is weak")
	if got["kind"] == "refusal" && got["cause"] == string(bounds.UnboundedRequested) {
		t.Error("a bounded request was refused as unbounded")
	}
}

// TestASentenceAboutAnotherSurfaceIsRedirectedRatherThanActedOn.
//
// 🔴 A SECURITY property, not a routing nicety. Connecting a repository creates a standing read grant —
// a credential used when the customer is not present — and the disclosure must be displayed before the
// grant is created. An agent that acted on "connect my repo" from a sentence would be the path around
// the consent screen that requirement exists to close. Revocation is here for the mirror reason: it is
// destructive, and its confirmation states what will be deleted.
func TestASentenceAboutAnotherSurfaceIsRedirectedRatherThanActedOn(t *testing.T) {
	hz := newHarness(t)
	member, _ := hz.user(t, tenancy.Member)

	for _, tc := range []struct{ text, surface string }{
		{"connect a repository for me", "/app/connections"},
		{"revoke that connection", "/app/connections"},
		{"change my password", "/app/account"},
		{"I want to see the invoice", "/app/billing"},
		{"invite someone to my org", "/app/settings/members"},
	} {
		got := hz.askAs(t, member, tc.text)
		if got["kind"] != "redirect" {
			t.Errorf("%q returned kind %q, want redirect — this is the consent-screen bypass",
				tc.text, got["kind"])
			continue
		}
		if got["surface"] != tc.surface {
			t.Errorf("%q redirected to %q, want %q", tc.text, got["surface"], tc.surface)
		}
		if got["does"] == "" {
			t.Errorf("%q redirected without saying what that surface does; the person now has to go "+
				"looking for it", tc.text)
		}
	}
}

// TestARedirectionBeatsAKeywordScore.
//
// "Change my password" contains "change", which scores for the authoring intents. The redirection has
// to win regardless — and it must not fire on a word that merely CONTAINS a topic: "remember" contains
// "member", and that exact substring bug once sent a question about node memory to the members page.
func TestARedirectionBeatsAKeywordScore(t *testing.T) {
	hz := newHarness(t)
	member, _ := hz.user(t, tenancy.Member)

	if got := hz.askAs(t, member, "change my password"); got["kind"] != "redirect" {
		t.Errorf("a password sentence returned %q; a redirection must beat every keyword score",
			got["kind"])
	}
	if got := hz.askAs(t, member, "what does this node remember between calls"); got["kind"] == "redirect" {
		t.Errorf("a question about memory was redirected to %q — 'member' matched inside 'remember'",
			got["surface"])
	}
}

// TestAnUnroutableSentenceOffersTheWholeFiniteList.
//
// ⚠️ This pins TODAY's behaviour, which the conversational rewrite deliberately CHANGES: "hello" will
// get a reply rather than a list. It is written down anyway, because the property that must survive is
// not the abstention — it is that an unroutable sentence never silently becomes a run. When this test
// is updated, the replacement must still assert that.
func TestAnUnroutableSentenceOffersTheWholeFiniteList(t *testing.T) {
	hz := newHarness(t)
	member, _ := hz.user(t, tenancy.Member)

	got := hz.askAs(t, member, "hello")
	if got["kind"] != "abstain" {
		t.Fatalf("%q returned kind %q, want abstain", "hello", got["kind"])
	}
	list, ok := got["can_do"].([]any)
	if !ok {
		t.Fatalf("an abstention carried no list of what this surface does: %v", got)
	}
	// 🔴 The WHOLE list, generated from the intent table. A hand-written sample beside a table of
	// nineteen intents is two lists that will disagree, and the copy is always the one that is wrong.
	if len(list) != len(intent.CanDo()) {
		t.Errorf("the abstention offered %d capabilities, but the closed set has %d — the list a user "+
			"reads and the table the code dispatches on have drifted", len(list), len(intent.CanDo()))
	}
	if _, planned := got["goal_id"]; planned {
		t.Error("an unroutable sentence produced a goal")
	}
}

// TestARoutedSentenceWithNoRepositoryAsksForOne.
//
// The refusal a person actually hits first, and the one whose next action matters most: they have typed
// nothing wrong, they simply have not said which repository.
func TestARoutedSentenceWithNoRepositoryAsksForOne(t *testing.T) {
	hz := newHarness(t)
	member, _ := hz.user(t, tenancy.Member)

	got := hz.askAs(t, member, "what does this node remember between calls")
	if got["kind"] != "refusal" {
		t.Fatalf("with no repository loaded, a routed sentence returned kind %q, want refusal", got["kind"])
	}
	if got["cause"] != string(bounds.NoSubject) {
		t.Errorf("refused with cause %q, want %q", got["cause"], bounds.NoSubject)
	}
	if got["next_action"] == "" {
		t.Error("the person was told no repository is loaded but not how to load one")
	}
	// 🔴 The intent is still reported. A refusal that drops what it understood makes "it did not
	// understand me" and "it understood me but cannot act yet" look identical.
	if got["intent"] != string(intent.Memory) {
		t.Errorf("the refusal reported intent %q, want %q", got["intent"], intent.Memory)
	}
}

// TestAskRefusesAnUnreadableBody keeps the malformed-input path honest: a bad body is a 400 with a
// reason, not a panic and not a silent 200 that looks like an abstention.
func TestAskRefusesAnUnreadableBody(t *testing.T) {
	hz := newHarness(t)
	member, _ := hz.user(t, tenancy.Member)

	rec := hz.do(t, "POST", "/api/ask", `{"text":`, member)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a truncated body returned %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// ── the transcript ───────────────────────────────────────────────────────────────────────────────

// askIn posts a sentence as part of a named conversation.
func (hz *harness) askIn(t *testing.T, as *http.Cookie, conv, text string) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]string{"text": text, "conversation_id": conv})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	rec := hz.do(t, "POST", "/api/ask", string(body), as)
	if rec.Code != http.StatusOK {
		t.Fatalf("ask %q: %d %s", text, rec.Code, rec.Body.String())
	}
	return decode(t, rec)
}

func (hz *harness) replay(t *testing.T, as *http.Cookie, query string) map[string]any {
	t.Helper()
	rec := hz.do(t, "GET", "/api/conversation"+query, "", as)
	if rec.Code != http.StatusOK {
		t.Fatalf("replay: %d %s", rec.Code, rec.Body.String())
	}
	return decode(t, rec)
}

// TestAConversationSurvivesARefresh is the P1 closure property.
//
// # What was broken, in the product rather than in the code
//
// `/api/ask` took `{text}` and nothing else. Nothing anywhere held the previous turn, so the console
// looked like a conversation and was structurally a run of unrelated single-shot requests. A refresh
// replayed durable GOALS and openly told the person that free answers were not written down.
//
// The agent cannot become conversational on top of that: "and what about tools?" has nothing to attach
// to. This asserts the floor that makes the rest possible — what was said is still there afterwards,
// in order, attributed, on a fresh request that shares nothing with the one that wrote it.
func TestAConversationSurvivesARefresh(t *testing.T) {
	hz := newHarness(t)
	member, _ := hz.user(t, tenancy.Member)
	conv := "c-" + randSuffix()

	hz.askIn(t, member, conv, "hello")
	hz.askIn(t, member, conv, "what does this node remember between calls")

	got := hz.replay(t, member, "?conversation_id="+conv)
	if got["conversation_id"] != conv {
		t.Fatalf("replay returned thread %q, want %q", got["conversation_id"], conv)
	}
	turns, _ := got["turns"].([]any)
	if len(turns) != 4 {
		t.Fatalf("%d turns after two exchanges, want 4 (two said, two answered): %v", len(turns), turns)
	}

	want := []struct{ role, body string }{
		{"user", "hello"},
		{"agent", ""}, // an abstention today; the body is asserted below to be non-empty
		{"user", "what does this node remember between calls"},
		{"agent", ""},
	}
	for i, w := range want {
		turn, _ := turns[i].(map[string]any)
		if turn["role"] != w.role {
			t.Errorf("turn %d was spoken by %q, want %q — the transcript is out of order",
				i, turn["role"], w.role)
		}
		if w.body != "" && turn["body"] != w.body {
			t.Errorf("turn %d reads %q, want %q", i, turn["body"], w.body)
		}
		// 🔴 No turn may be blank. An empty agent turn replays as a silent bubble AND gives the agent an
		// empty turn to reason from, so "what did you just tell me?" would be answered from nothing.
		if turn["body"] == "" {
			t.Errorf("turn %d has an empty body (role %q, kind %q)", i, turn["role"], turn["kind"])
		}
		if seq, ok := turn["seq"].(float64); !ok || int(seq) != i+1 {
			t.Errorf("turn %d carries seq %v, want %d", i, turn["seq"], i+1)
		}
	}
}

// TestAReplayRecordsHowEachAnswerWasReached.
//
// The reply path degrades: when the model is unavailable the deterministic router answers instead, and
// the two are indistinguishable once rendered. The floor — an unbounded refusal, a redirect — is a third
// case again, and it is the one that must never depend on a model at all. If the transcript cannot tell
// them apart, neither can anybody debugging it.
func TestAReplayRecordsHowEachAnswerWasReached(t *testing.T) {
	hz := newHarness(t)
	member, _ := hz.user(t, tenancy.Member)
	conv := "c-" + randSuffix()

	hz.askIn(t, member, conv, "keep going until it is perfect")
	hz.askIn(t, member, conv, "change my password")

	turns, _ := hz.replay(t, member, "?conversation_id="+conv)["turns"].([]any)
	if len(turns) != 4 {
		t.Fatalf("%d turns, want 4", len(turns))
	}
	for _, i := range []int{1, 3} {
		turn, _ := turns[i].(map[string]any)
		if turn["decided"] != "floor" {
			t.Errorf("turn %d was decided by %q, want %q — the safety floor answered this one, and a "+
				"transcript that cannot say so cannot explain why", i, turn["decided"], "floor")
		}
	}
	if kind, _ := turns[1].(map[string]any)["kind"]; kind != "refusal" {
		t.Errorf("the unbounded turn replays as kind %q, want refusal", kind)
	}
	if kind, _ := turns[3].(map[string]any)["kind"]; kind != "redirect" {
		t.Errorf("the redirected turn replays as kind %q, want redirect", kind)
	}
}

// TestReplayWithNoIdResumesTheMostRecentThread — what a reconnecting browser does before it knows
// which conversation it was in. Getting this wrong is invisible: the person gets an empty console and
// starts a second thread beside the one they were having.
func TestReplayWithNoIdResumesTheMostRecentThread(t *testing.T) {
	hz := newHarness(t)
	member, _ := hz.user(t, tenancy.Member)

	older, newer := "c-"+randSuffix(), "c-"+randSuffix()
	hz.askIn(t, member, older, "hello")
	time.Sleep(2 * time.Millisecond) // the store orders by timestamp; make the two separable
	hz.askIn(t, member, newer, "what tools does this node call")

	got := hz.replay(t, member, "")
	if got["conversation_id"] != newer {
		t.Errorf("an unaddressed replay resumed %q, want the most recent thread %q",
			got["conversation_id"], newer)
	}
}

// TestAskWithNoConversationIdStillAnswers.
//
// 🔴 The transcript is an ENHANCEMENT, never a precondition. An older client that does not send an id,
// or any caller that has no conversation, must get exactly the same reply — only the memory of it
// differs. Making the answer depend on the record would take the main path down for the sake of a
// side one.
func TestAskWithNoConversationIdStillAnswers(t *testing.T) {
	hz := newHarness(t)
	member, _ := hz.user(t, tenancy.Member)

	got := hz.askAs(t, member, "change my password")
	if got["kind"] != "redirect" {
		t.Fatalf("without a conversation id the reply was %q, want redirect", got["kind"])
	}
	// And nothing was recorded, so no thread was invented under a default id.
	if replayed := hz.replay(t, member, ""); replayed["conversation_id"] != "" {
		t.Errorf("an unrecorded exchange created thread %q; an empty id must mean 'do not record', "+
			"not 'record under a shared default'", replayed["conversation_id"])
	}
}

// TestARefusalDoesNotReplayAsTheAgentRepeatingYou.
//
// # The bug this fences
//
// `askResp.Text` does not mean the same thing in every shape. On an answer it is the reply; on an
// unbounded refusal it is the ECHO of what the person typed, which the console renders as
// "You asked: …". The first version of `transcriptBody` preferred `Text` over the shape, so the
// transcript recorded the person's own sentence as the agent's reply — and the thread replayed as the
// agent repeating them back at themselves.
//
// 🔴 Found by driving the real endpoint and reading the rows back, not by any unit test. A shape-blind
// mapping from response to transcript is exactly the kind of thing that type-checks, passes, and is
// only visible once somebody re-reads a conversation.
func TestARefusalDoesNotReplayAsTheAgentRepeatingYou(t *testing.T) {
	hz := newHarness(t)
	member, _ := hz.user(t, tenancy.Member)
	conv := "c-" + randSuffix()

	const said = "keep going until it is perfect"
	hz.askIn(t, member, conv, said)

	turns, _ := hz.replay(t, member, "?conversation_id="+conv)["turns"].([]any)
	if len(turns) != 2 {
		t.Fatalf("%d turns, want 2", len(turns))
	}
	reply, _ := turns[1].(map[string]any)
	body, _ := reply["body"].(string)
	if body == said {
		t.Fatalf("the agent's turn replays as the person's own sentence (%q) — Text on a refusal is the "+
			"echo, not the reply", body)
	}
	// It has to say it refused, and why, or the transcript cannot explain itself later.
	if !strings.Contains(body, string(bounds.UnboundedRequested)) {
		t.Errorf("the refusal replays as %q, which does not name the cause", body)
	}
	if !strings.Contains(body, "limit") {
		t.Errorf("the refusal replays as %q, which does not carry the next action", body)
	}
}
