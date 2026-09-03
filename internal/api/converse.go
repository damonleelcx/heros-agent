package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/converse"
	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/memory"
	"github.com/heros-foreal/heros/internal/tenancy"
)

// converse.go is the conversational half of `/api/ask`: everything that happens AFTER the deterministic
// safety floor has had its say and declined to answer.
//
// # 🔴 The order in `decide` is the security design, not a style choice
//
//	1. Unbounded refusal  — before anything is planned
//	2. Out-of-scope redirect — before anything can be talked out of it
//	3. The agent          — may be persuaded, and can only choose from a closed set
//	4. Confirmation       — before anything spends or writes
//	5. The keyword router — whenever step 3 could not answer
//
// Steps 1 and 2 are deterministic on purpose. Connecting a repository creates a standing read grant —
// a credential used when the customer is not present — and its disclosure must be displayed before the
// grant exists. A model cannot be the only thing standing between a sentence and that.
//
// What makes step 3 safe is not phrase matching, it is the CLOSED ACTION SURFACE: the agent may say
// anything and may only do one of `intent.All()`, none of which connects a repository, changes a
// password or touches billing. So a sentence the floor does not recognise cannot be acted on however it
// is phrased — the worst case is that the agent talks about it and names the page that owns it.

// pending is a capability the agent chose that must not run until a person says so.
type pendingAction struct {
	ID         string
	Capability intent.Intent
	Axis       string
	// Why is the agent's own account of what it understood, which is what the person is confirming.
	Why string
	// Text is the sentence that produced it, carried so the effect path can still read the original
	// wording rather than a paraphrase.
	Text      string
	CreatedAt time.Time
	// Decided guards against a double-confirm racing itself: confirming is consent to run something
	// ONCE, and two clicks must not start two runs.
	Decided bool
}

// pendingActions holds them between choosing and confirming.
//
// In memory on purpose, and stated as a limitation rather than hidden: a restart loses unconfirmed
// actions, which is correct for consent — a confirmation nobody answered should not survive to be
// answered by accident later. The same argument `approvals` makes about undecided diffs.
type pendingActions struct {
	mu sync.Mutex
	by map[string]*pendingAction
}

// NewPendingActions builds an empty store.
func NewPendingActions() *pendingActions { return &pendingActions{by: map[string]*pendingAction{}} }

func (p *pendingActions) put(a *pendingAction) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.by[a.ID] = a
}

// take marks an action decided and returns it, refusing a second decision on the same one.
func (p *pendingActions) take(id string) (*pendingAction, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok := p.by[id]
	if !ok {
		return nil, fmt.Errorf("that action is no longer waiting; it may have been confirmed already, " +
			"or the server restarted, which discards unconfirmed actions")
	}
	if a.Decided {
		return nil, fmt.Errorf("that action has already been confirmed")
	}
	a.Decided = true
	return a, nil
}

// converseOrFallback lets the agent decide, and falls back to the keyword router when it cannot.
//
// 🔴 The bool is "the agent answered", not "the agent succeeded". Every failure — no provider, a rate
// limit, a timeout, JSON that will not parse, a capability it invented — returns false, and the caller
// carries on with the behaviour the console had before this package existed. The worst outcome of a
// provider having a bad afternoon is a blunter console, never a broken one.
func (s *Server) converseOrFallback(tenant, asker string, req askReq, sub *subjectState, onText func(string)) (askResp, bool) {
	if s.Converse == nil {
		return askResp{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := s.Converse.InterpretStream(ctx, s.history(tenant, req.ConversationID), req.Text,
		s.factsFor(tenant, asker, sub), onText)
	if err != nil {
		// 🔴 WARN with the error CLASS, not just the text. A rate limit and a prompt the model will
		// never satisfy both degrade identically for the customer and need completely different
		// responses from whoever is on call — and a degraded console that logs nothing looks exactly
		// like a console that was always this blunt.
		log.Printf("WARN heros.converse.degraded tenant=%q conversation=%q spent_micro_cents=%d "+
			"calls=%d err=%v", tenant, req.ConversationID, out.CostMicroCents, out.Calls, err)
		return askResp{}, false
	}

	if out.Talked() {
		kind := "say"
		text := out.Say
		if out.Ask != "" {
			// Recorded as its own shape although the console prints both as prose. They are different
			// acts — one ends a turn, the other is waiting for an answer — and a transcript that cannot
			// tell them apart cannot later be evaluated for how often the agent stalls.
			kind, text = "ask", out.Ask
		}
		return askResp{Kind: kind, Text: text, CostMicroCents: out.CostMicroCents}, true
	}

	spec, _ := intent.Lookup(out.Capability)

	// A capability needs something to act on. This is checked AFTER the agent has spoken rather than
	// before it, which is the change that lets a person say "hi" — and get an answer — without having
	// loaded a repository first.
	if sub == nil {
		ref := bounds.Refusal{Cause: bounds.NoSubject}
		return askResp{
			Kind: "refusal", Intent: out.Capability.String(), Cause: string(ref.Cause),
			Text: out.Why, NextAction: ref.NextAction(), CostMicroCents: out.CostMicroCents,
		}, true
	}

	if converse.NeedsConfirmation(out.Capability) {
		a := &pendingAction{
			ID: fmt.Sprintf("act-%d", time.Now().UnixNano()), Capability: out.Capability,
			Axis: out.Axis, Why: out.Why, Text: req.Text, CreatedAt: time.Now().UTC(),
		}
		s.Pending.put(a)
		return askResp{
			Kind: "confirm", Intent: out.Capability.String(), Tier: string(spec.Tier),
			ActionID: a.ID, Scope: out.Axis, Text: out.Why,
			Spends: spec.Tier == intent.TierGoal, Writes: out.Capability.EffectBearing(),
			CeilingCents: s.Ceilings.MaxCostCents, CostMicroCents: out.CostMicroCents,
		}, true
	}

	// A read: nothing is spent by running it, so it runs.
	resp, err := s.answerQuery(tenant, spec, sub)
	if err != nil {
		log.Printf("WARN heros.converse.query_failed tenant=%q capability=%q err=%v",
			tenant, out.Capability, err)
		return askResp{}, false
	}
	resp.CostMicroCents = out.CostMicroCents
	return resp, true
}

// history reads the recent transcript, treating a failure as an empty conversation.
//
// 🔴 A read failure degrades to "no history" rather than taking the turn down. The transcript is an
// enhancement to the reply, never a precondition for it — the same rule `record` follows on the way in.
// The cost is that the agent forgets, which is visible; the alternative is that it refuses, which reads
// as broken.
func (s *Server) history(tenant, conversationID string) []memory.Turn {
	if s.Episodes == nil || conversationID == "" {
		return nil
	}
	turns, err := s.Episodes.For(tenant).Turns(tenant, conversationID)
	if err != nil {
		log.Printf("WARN heros.converse.history_unreadable tenant=%q conversation=%q err=%v",
			tenant, conversationID, err)
		return nil
	}
	return turns
}

// factsFor is everything the agent is allowed to assert without looking anything up.
//
// 🔴 Assembled from the discovery index rather than passed as prose. This is the only channel through
// which the agent learns anything about the customer's code, so it is the only place a hallucinated
// fact could enter — and a struct makes the set of things it can know enumerable, which a free-text
// blob somebody appends to does not.
// factsFor assembles everything the agent is allowed to assert without looking it up.
//
// 🔴 A method now, and it reads the store. It used to be a pure function over the loaded subject; the
// person's own profile is the second input, and a profile the console can edit but the agent never
// reads would be a settings panel that changes nothing — the exact failure the product is built to
// find in other people's agents.
//
// 🔴 A failed profile read is NOT an error. It degrades to "we know nothing about this person", which
// is where the product was before profiles existed. Failing the whole turn because a preference row
// could not be read would let a settings feature take down the conversation, and the conversation is
// the product. The WARN is what keeps that from being silent.
func (s *Server) factsFor(tenant, asker string, sub *subjectState) converse.Facts {
	f := s.subjectFacts(sub)
	if s.Episodes == nil || asker == "" {
		return f
	}
	prefs, err := s.Episodes.For(tenant).Preferences(tenant)
	if err != nil {
		log.Printf("WARN heros.profile.unreadable tenant=%q asker=%q err=%v", tenant, asker, err)
		return f
	}
	p := ProfileFor(prefs, asker)
	f.Person = converse.Person{
		DisplayName: p["display_name"], Role: p["role"],
		Instructions: p["instructions"], ReplyLanguage: p["reply_language"],
	}
	return f
}

func (s *Server) subjectFacts(sub *subjectState) converse.Facts {
	if sub == nil {
		return converse.Facts{SubjectLoaded: false}
	}
	isAgent, why := sub.Index.LooksLikeAnAgent()
	f := converse.Facts{
		SubjectLoaded: true, Reference: sub.Source.Describe(),
		Revision: shortRef(sub.Source.Revision), IsAgent: isAgent, Why: why,
	}
	for _, axis := range intent.Axes() {
		if sub.Index.ForAxis(axis).Found {
			f.AxesFound = append(f.AxesFound, axis)
		} else {
			// Absence is a FINDING, not an error — the difference between "your agent has no memory
			// strategy" and "I could not read the file" is most of what an assessment is worth.
			f.AxesMissing = append(f.AxesMissing, axis)
		}
	}
	return f
}

// ── confirming ───────────────────────────────────────────────────────────────────────────────────

type confirmReq struct {
	ActionID string `json:"action_id"`
	// Approve false discards it. 🔴 Present rather than implied by calling the endpoint at all: "I
	// decided no" and "I never answered" are different, and only one of them should stop the console
	// re-offering the action later.
	Approve bool `json:"approve"`
}

// handleConfirm runs a capability the person has now agreed to.
//
// # 🔴 Why the capability is re-read from the pending record rather than from the request
//
// The request carries an id and a yes. Everything about WHAT will run — the capability, the axis, the
// original sentence — comes from the record the server wrote when it asked. A version that took the
// capability from the request body would let a caller confirm one thing and run another, which is the
// entire gate defeated by the message that passes through it.
func (s *Server) handleConfirm(w http.ResponseWriter, r *http.Request) {
	tenant, err := tenancy.MustTenant(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	var req confirmReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	a, err := s.Pending.take(req.ActionID)
	if err != nil {
		writeJSON(w, http.StatusOK, askResp{
			Kind: "refusal", Cause: "not_pending", NextAction: err.Error()})
		return
	}
	if !req.Approve {
		writeJSON(w, http.StatusOK, askResp{
			Kind: "say", Text: "Left it alone. Nothing ran and nothing was spent."})
		return
	}

	sub := s.subjectOrRestore(tenant)
	if sub == nil {
		ref := bounds.Refusal{Cause: bounds.NoSubject}
		writeJSON(w, http.StatusOK, askResp{
			Kind: "refusal", Intent: a.Capability.String(), Cause: string(ref.Cause),
			NextAction: ref.NextAction()})
		return
	}
	spec, _ := intent.Lookup(a.Capability)

	var resp askResp
	switch spec.Tier {
	case intent.TierGoal:
		resp, err = s.startGoal(tenant, spec, sub, a.Axis)
	case intent.TierEffect:
		resp, err = s.handleEffect(spec, sub, a.Axis, a.Text)
	default:
		// Unreachable by construction — a read never becomes pending — but stated rather than assumed,
		// because "unreachable" is a claim about today's tier table and this would otherwise run the
		// wrong branch silently if that changed.
		resp, err = s.answerQuery(tenant, spec, sub)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ── streaming the reply ──────────────────────────────────────────────────────────────────────────

// handleAskStream is POST /api/ask/stream: the same turn as /api/ask, delivered as it is written.
//
// # 🔴 Why this is a second route and not a change to /api/ask
//
// /api/ask is a published contract — the README documents it as the whole pipeline, the route table
// pins its capability, and every test posts to it. Changing its response shape to serve a display
// improvement would put an answer somebody needs behind a transport somebody's proxy might buffer. So
// the streaming route is additive, the console prefers it, and falls back to /api/ask if it fails at
// any point. The worst outcome of a streaming bug is the console people had yesterday.
//
// # 🔴 The events, and which one is authoritative
//
//	delta  {"text":"…"}  prose as it arrives. DECORATION. May be incomplete, may be superseded.
//	final  {…askResp…}   the real answer. The console MUST render this and discard what it drew from
//	                     deltas, because the loop may call the model more than once and text from a
//	                     call that did not decide was never part of the reply.
//	error  {"error":"…"} the turn failed; nothing was recorded.
//
// # 🔴 Why the turn is recorded here too
//
// Because it is the same turn. Recording only on the JSON route would mean a console that streams keeps
// no transcript, and "and what about tools?" would stop working for exactly the people using the newer
// path — a regression nobody would connect to streaming.
func (s *Server) handleAskStream(w http.ResponseWriter, r *http.Request) {
	p, err := tenancy.From(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	var req askReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		// 🔴 Refused rather than silently answering in one lump. A caller that asked for a stream and
		// got a single frame at the end has no way to tell that from a very slow model, and would sit
		// showing an empty bubble believing it was working.
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// 🔴 Nginx and friends buffer text/event-stream by default, which turns a stream back into one lump
	// at the end — the exact failure this route exists to remove, and invisible in local testing.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	send := func(event string, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
	}

	// 🔴 Deltas are serialised onto the request goroutine through a channel rather than written from
	// the provider's callback directly. The callback runs while the HTTP response body is being read,
	// and an http.ResponseWriter is not safe for concurrent use — writing from both would be a data
	// race that shows up as corrupted frames under load and never on one developer's machine.
	type deltaMsg struct {
		Text string `json:"text"`
	}
	deltas := make(chan string, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for piece := range deltas {
			send("delta", deltaMsg{Text: piece})
		}
	}()

	resp, decided, derr := s.decide(p.Tenant, p.Subject, req, func(piece string) {
		// Non-blocking: a console that has stopped reading must slow the turn down, never wedge it.
		select {
		case deltas <- piece:
		default:
		}
	})
	close(deltas)
	<-done

	if derr != nil {
		send("error", map[string]string{"error": derr.Error()})
		return
	}
	s.record(p.Tenant, req, resp, decided)
	send("final", resp)
}
