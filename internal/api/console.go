package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/memory"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/tenancy"
)

// console.go serves the three surfaces the console's shell needs and the conversation itself does not:
// the list of threads in the left rail, the person's own profile, and stopping a run from the right
// rail.
//
// # 🔴 Why these are here rather than folded into server.go
//
// server.go is the ASK path — the floor, the agent, the router, the run. Everything in this file is
// chrome around that path: it reads what already exists and writes one person's own settings. Keeping
// them apart means a change to the shell cannot reach into the path that decides whether something
// spends money or writes to a repository.

// ── the threads in the left rail ─────────────────────────────────────────────────────────────────

type conversationBrief struct {
	ID string `json:"id"`
	// Title is the opening sentence, truncated by the store. Empty when a thread holds no user turn;
	// the console renders that as "untitled" rather than as a blank row.
	Title  string `json:"title"`
	Turns  int    `json:"turns"`
	LastAt string `json:"last_at"`
}

type conversationsResp struct {
	Conversations []conversationBrief `json:"conversations"`
	// Current is the thread a client with no opinion should open — the same answer
	// `GET /api/conversation` gives when asked without an id. Sent alongside the list so the rail can
	// mark the active row without a second request, and so the two endpoints cannot disagree about
	// which thread is current.
	Current string `json:"current,omitempty"`
}

// handleConversations lists this organization's threads for the session rail.
//
// 🔴 There is no "create a conversation" endpoint and there deliberately is not one. A thread exists
// exactly when somebody has said something in it — the console mints an id locally and it materialises
// on the first turn. An endpoint that created empty threads would let a person accumulate rows in the
// rail that open onto nothing, and would need a cleanup path for the ones they never used.
func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	tenant, err := tenancy.MustTenant(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	// An empty list, not an error: a deployment with no memory store still has a console, and the rail
	// should render as "no conversations yet" rather than as broken.
	if s.Episodes == nil {
		writeJSON(w, http.StatusOK, conversationsResp{Conversations: []conversationBrief{}})
		return
	}
	scoped := s.Episodes.For(tenant)
	list, err := scoped.Conversations(tenant)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp := conversationsResp{Conversations: make([]conversationBrief, 0, len(list))}
	for _, c := range list {
		resp.Conversations = append(resp.Conversations, conversationBrief{
			ID: c.ID, Title: c.Title, Turns: c.Turns, LastAt: c.LastAt.Format(time.RFC3339),
		})
	}
	if latest, ok, err := scoped.LatestConversation(tenant); err == nil && ok {
		resp.Current = latest
	}
	writeJSON(w, http.StatusOK, resp)
}

// ── the person's own profile ─────────────────────────────────────────────────────────────────────

// profileFields is the closed set of things a person may tell the agent about themselves.
//
// # 🔴 A table, and a CLOSED one, rather than an open key/value bag
//
// These values are read back into the model's system prompt. An open bag would mean the set of things
// that can end up in that prompt is whatever any client chose to POST, which is not a set anybody can
// review. Enumerating them here means the answer to "what can influence a reply?" is this list, and
// adding to it is a visible change.
//
// # 🔴 Why the preferences table and not a new one
//
// `preferences` already means "a standing instruction from a person, never inferred" — which is exactly
// what these are — and it already refuses a write the system tries to author. A `user_profiles` table
// would be a second home for the same concept, and a new table is a one-way door once it ships.
var profileFields = []struct {
	// Field is the JSON name the console uses.
	Field string
	// Key is the suffix under which it is stored. Prefixed per person at write time.
	Key string
	// Limit bounds what one field may contribute to the prompt.
	Limit int
}{
	{Field: "display_name", Key: "display_name", Limit: 120},
	{Field: "role", Key: "role", Limit: 200},
	{Field: "instructions", Key: "instructions", Limit: 2000},
	{Field: "reply_language", Key: "reply_language", Limit: 60},
}

// profileKey namespaces a profile field to one person inside one organization.
//
// 🔴 The email is in the KEY, not only in `authored_by`. `preferences` is unique on (tenant, key), so a
// key of plain "display_name" would give a whole organization one shared profile whose last writer
// wins — colleagues would silently overwrite each other's standing instructions, and the agent would
// address one person by another's name.
func profileKey(email, field string) string { return "user:" + email + ":" + field }

// ProfileFor reads one person's profile out of the preference rows.
//
// Exported because the ask path needs it too: a profile that the console can edit but the agent never
// reads is a settings page that does nothing, which is worse than not having one.
func ProfileFor(prefs []memory.Preference, email string) map[string]string {
	out := map[string]string{}
	for _, f := range profileFields {
		key := profileKey(email, f.Key)
		for _, p := range prefs {
			if p.Key == key {
				out[f.Field] = p.Value
				break
			}
		}
	}
	return out
}

// handleGetProfile answers with the signed-in person's own profile.
//
// 🔴 Reads only THEIR rows, and takes the identity from the authenticated principal rather than from a
// query parameter. A `?email=` would make one colleague's standing instructions readable by another,
// which is a privacy leak dressed as a convenience.
func (s *Server) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	p, err := tenancy.From(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	if s.Episodes == nil {
		writeJSON(w, http.StatusOK, map[string]any{"profile": map[string]string{}})
		return
	}
	prefs, err := s.Episodes.For(p.Tenant).Preferences(p.Tenant)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": ProfileFor(prefs, p.Subject)})
}

// handleSetProfile writes the fields the request actually carried.
//
// 🔴 A PARTIAL update, keyed on presence rather than on emptiness. The console saves one panel at a
// time; a whole-object PUT would mean a panel that does not render "instructions" silently erases it.
// A field present and empty IS a deletion — that is how somebody clears a standing instruction — so the
// two cases have to be distinguishable, which is why this decodes into pointers.
func (s *Server) handleSetProfile(w http.ResponseWriter, r *http.Request) {
	p, err := tenancy.From(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	if s.Episodes == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "This deployment has no memory store, so a profile cannot be saved. " +
				"An operator needs to configure one and restart."})
		return
	}
	var req map[string]*string
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	scoped := s.Episodes.For(p.Tenant)
	for _, f := range profileFields {
		v, present := req[f.Field]
		if !present || v == nil {
			continue
		}
		value := strings.TrimSpace(*v)
		if len(value) > f.Limit {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("%q is longer than the %d characters this field allows. "+
					"It is read into every reply, so it is kept short on purpose.", f.Field, f.Limit)})
			return
		}
		// 🔴 AuthoredBy is the principal's own address, which is what makes this write legal:
		// ValidatePreference refuses anything the system appears to have authored, so the same call
		// made by a worker would be rejected rather than quietly accepted.
		if err := scoped.SetPreference(memory.Preference{
			Tenant: p.Tenant, Key: profileKey(p.Subject, f.Key), Value: value,
			AuthoredBy: p.Subject, At: time.Now().UTC(),
		}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	prefs, err := scoped.Preferences(p.Tenant)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Answers with the profile as STORED, not with what was sent. The console renders from the reply, so
	// a value that was trimmed or refused shows the person what actually landed rather than what they
	// typed — the difference being the whole point of reading it back.
	writeJSON(w, http.StatusOK, map[string]any{"profile": ProfileFor(prefs, p.Subject)})
}

// ── stopping a run ───────────────────────────────────────────────────────────────────────────────

// handleCancelGoal stops a run at a person's request.
//
// 🔴 Guarded by RunGoals, the same capability as starting one, and deliberately NOT by ApproveChange.
// Stopping work is the mirror of starting it: somebody who may launch a run must be able to stop it,
// and somebody who may only read must not be able to halt a colleague's work. ApproveChange is reserved
// for the route that writes to the customer's repository.
func (s *Server) handleCancelGoal(w http.ResponseWriter, r *http.Request) {
	tenant, err := tenancy.MustTenant(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	id := goal.ID(r.PathValue("id"))
	scoped := s.Root.For(tenant)

	// Ownership first, and answered as "not found" — the same disguise every other goal-id route wears,
	// so that a stranger cannot learn which ids are real by watching a 403 turn into a 404.
	if _, err := scoped.LoadGoal(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such run"})
		return
	}

	took, err := scoped.Cancel(id, time.Now().UTC())
	if err != nil {
		// 🔴 Already-terminal is a 409, not a 500. Two people watching the same rail will both press
		// Cancel, and the second one is not an error in the system — it is a race the product should
		// explain, with the run's actual state in the sentence.
		if errors.Is(err, goal.ErrIllegalState) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		if errors.Is(err, store.ErrGoalNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such run"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// 🔴 The reply says plainly whether something is still running. store.Cancel deliberately leaves a
	// leased task alone — see its comment — so a person who pressed Cancel and then watched a task keep
	// going would reasonably conclude the button did nothing. Saying it up front is the difference
	// between a designed delay and an apparent bug.
	g, _ := scoped.LoadGoal(id)
	stillRunning := false
	if d, derr := scoped.LoadDAG(id); derr == nil && d != nil {
		for _, t := range d.Tasks {
			if !t.State.Terminal() {
				stillRunning = true
				break
			}
		}
	}
	resp := map[string]any{
		"id": string(id), "state": string(g.State), "tasks_cancelled": took,
		"still_running": stillRunning,
	}
	if stillRunning {
		resp["status"] = "The run is cancelled. One task was already under way and will finish first — " +
			"nothing new will be started after it."
	} else {
		resp["status"] = "The run is cancelled and nothing is still running."
	}
	writeJSON(w, http.StatusOK, resp)
}
