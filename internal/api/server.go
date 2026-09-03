package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/heros-foreal/heros/internal/auth"
	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/converse"
	"github.com/heros-foreal/heros/internal/discovery"
	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/intake"
	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/mailer"
	"github.com/heros-foreal/heros/internal/memory"
	"github.com/heros-foreal/heros/internal/planner"
	"github.com/heros-foreal/heros/internal/provider"
	"github.com/heros-foreal/heros/internal/ratelimit"
	"github.com/heros-foreal/heros/internal/router"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/task"
	"github.com/heros-foreal/heros/internal/tenancy"
	"github.com/heros-foreal/heros/internal/toolcontract"
	"github.com/heros-foreal/heros/internal/tools"
	"log"
)

// Server is the console's HTTP surface.
type Server struct {
	// Root hands out tenant-scoped stores. 🔴 The server never holds an unscoped Store, so no handler can
	// construct a query for a tenant other than the caller's — not because it is careful, but because it
	// has nothing to be careless with.
	Root     store.Root
	Auth     *auth.Store
	Planners *planner.Registry
	// SupervisorFor builds the supervisor for one organization, against that organization's slice of
	// the store. Supplied by the caller rather than constructed here, because building one needs a
	// worker, which needs the tool registry and the approval policy — all of which are the daemon's to
	// assemble. See supervisors.go for why there is one per organization rather than one per server.
	SupervisorFor func(tenant string) *Supervisor
	// sups caches them. Never read directly — use supFor, which builds on first use.
	sups     supervisorSet
	Resolver *intake.Resolver
	Router   router.Router
	Ceilings bounds.Ceilings
	// DefaultTenant is used when a login omits one, for single-organization deployments.
	DefaultTenant string

	// Mail sends invitations, password resets and address confirmations.
	//
	// 🔴 An interface rather than an SMTP client, so a deployment with no relay is a DIFFERENT
	// implementation that refuses loudly rather than a client that silently drops. See internal/mailer:
	// a mailer that reports itself healthy and delivers nothing has already cost this product days.
	Mail mailer.Mailer
	// Links builds the URLs that go in that mail, from a configured origin.
	//
	// 🔴 Never from the request's Host header. Whoever asks for a password reset chooses that request's
	// headers, and would choose to have the link point at themselves.
	Links mailer.Links
	// ForgotLimit caps how often a password reset can be ASKED FOR, per address.
	//
	// 🔴 Named for the endpoint, not for the feature. It was `ResetLimit`, which stopped being a name the
	// moment a second limit appeared in the same flow: this one bounds requesting a link and is keyed on
	// an address, RedeemLimit bounds using one and is keyed on a token. Two fields that could both
	// reasonably be called "the reset limit" is how the wrong one gets used.
	//
	// 🔴 Per address rather than per caller, because what is being protected is somebody's INBOX. An
	// unauthenticated endpoint that sends mail to any address on request is a way to flood a person the
	// attacker dislikes, and the victim is the address, not whoever made the request.
	ForgotLimit *ratelimit.Limiter
	// LoginLimit caps password guesses, per ACCOUNT — tenant AND address.
	//
	// 🔴 A different key from ForgotLimit, because a different thing is being protected. A reset floods an
	// inbox, and an inbox is one mailbox however many organizations write to it, so that limit is keyed on
	// the address alone. A login guesses at an ACCOUNT, and the same address in two organizations is two
	// accounts with two passwords — keyed on the address alone, guessing at one customer's user would
	// spend the budget of a different customer's user, which is one tenant degrading another.
	LoginLimit *ratelimit.Limiter
	// SignupLimit caps how often an organization can be created for one address. Its ceiling and the
	// gap it does not close are documented beside the handler, in signup.go.
	SignupLimit *ratelimit.Limiter
	// AcceptLimit caps how hard one invitation can be redeemed, keyed on the TOKEN's hash.
	//
	// 🔴 It bounds hammering of a single valid invitation. It cannot bound a flood of INVENTED tokens,
	// because each invented token is a fresh key — which is why the store checks the token before it
	// hashes anything, rather than relying on this. A limit and a cheap rejection close different halves,
	// and only one of them was ever going to close that half.
	AcceptLimit *ratelimit.Limiter
	// ResendLimit caps how often confirmation mail is SENT, keyed on the address — like ForgotLimit, and
	// for the same reason: an inbox is what fills up.
	//
	// 🔴 Named for sending, not for the feature. It was `VerifyLimit`, which had the same problem
	// `ResetLimit` had: with a second limit in the same flow, one bounds sending a link and the other
	// bounds using one, and a field either could be named after is a field the wrong one gets used for.
	ResendLimit *ratelimit.Limiter
	// ConfirmLimit caps how hard one confirmation link can be used, keyed on the TOKEN's hash.
	//
	// 🚫 The weakest of the four token limits, and worth saying so rather than dressing it up. This path
	// runs no argon2id — a request costs one transaction and an indexed lookup — so what it bounds is
	// connection-pool churn against a single link, not anything expensive. It exists mostly so that every
	// token-redemption endpoint behaves the same way and nobody has to remember which one is the
	// exception. Like the others, it cannot bound a flood of invented tokens: each is a fresh key.
	ConfirmLimit *ratelimit.Limiter
	// RedeemLimit caps how hard one password-reset link can be used, keyed on the TOKEN's hash.
	//
	// 🔴 The counterpart to AcceptLimit, and with the same blind spot: it bounds hammering of one live
	// link and cannot bound a flood of invented tokens, because each invented token is a fresh key. What
	// closes that is the store checking the token before it hashes anything.
	RedeemLimit *ratelimit.Limiter

	// ToolRegistry, Provider and Model let the server rebind the assessment tool to the LOADED corpus.
	//
	// 🔴 The tool needs the repository the conversation is about, and that is chosen after the process
	// starts. A registry built once at boot holds a tool bound to no source — so the source is injected
	// when a subject is loaded, through Registry.Replace, which re-runs the same refusals rather than
	// mutating the map from outside.
	ToolRegistry *toolcontract.Registry
	Provider     provider.Provider
	Model        string
	// Approvals holds Tier-C changes between proposing and deciding.
	Approvals *approvals
	// Converse is the agent that reads a sentence and decides what it means.
	//
	// 🔴 OPTIONAL, and the whole design depends on it staying optional. When it is nil — no provider
	// configured, a deployment that has not been given a key — the console falls back to the
	// deterministic keyword router and keeps working. A conversational surface that cannot start
	// without its provider is one that goes down when the provider does.
	Converse *converse.Agent
	// Pending holds capabilities the agent chose that spend money or write, until a person confirms.
	Pending *pendingActions
	// Episodes is the episodic record, read by run history and by the timeline.
	//
	// 🔴 A Root, not a Store: a handler is handed a view bound to the caller's tenant and never holds
	// the unscoped store, so it cannot ask for another customer's history.
	Episodes memory.Root

	mu sync.RWMutex
	// subjects is the loaded repository PER TENANT.
	//
	// 🔴 It was a single field, shared by every caller. Loading a repository replaced it globally, so one
	// customer's question was answered about whichever repository another customer had opened most
	// recently — with real file:line references, confidently, about code they have never seen. That is a
	// cross-tenant data leak wearing the shape of a cache.
	subjects map[string]*subjectState
}

// NewServer builds a server with everything that has to exist before the first request.
//
// 🔴 Approvals is created HERE and not by the caller. It was assembled in main alongside a dozen other
// fields, and any server built without that one line — a test, a future entry point — answered
// `POST /api/decide` by dereferencing nil. The route was reachable, authenticated, authorised, and then
// panicked. A constructor that returns a half-built object makes correct assembly a thing each caller
// has to remember, which is the same argument as every other one in this package: not because they are
// careless, but because remembering is not a property a codebase keeps.
func NewServer() *Server {
	return &Server{
		subjects:     map[string]*subjectState{},
		Approvals:    NewApprovals(),
		Pending:      NewPendingActions(),
		ForgotLimit:  ratelimit.New(ForgotBurst, ForgotRefill, ForgotKeyCeiling),
		LoginLimit:   ratelimit.New(LoginBurst, LoginRefill, LoginKeyCeiling),
		SignupLimit:  ratelimit.New(SignupBurst, SignupRefill, SignupKeyCeiling),
		AcceptLimit:  ratelimit.New(AcceptBurst, AcceptRefill, AcceptKeyCeiling),
		ResendLimit:  ratelimit.New(ResendBurst, ResendRefill, ResendKeyCeiling),
		RedeemLimit:  ratelimit.New(RedeemBurst, RedeemRefill, RedeemKeyCeiling),
		ConfirmLimit: ratelimit.New(ConfirmBurst, ConfirmRefill, ConfirmKeyCeiling),
	}
}

// The password-reset ceiling.
//
// Three back to back covers the real case — somebody asks, the mail lands in spam, they ask again — and
// then one every twenty minutes, which is far more than anyone needs and far less than a flood. The
// numbers are here rather than inline so that a deployment arguing about them has one place to look.
const (
	ForgotBurst  = 3
	ForgotRefill = 20 * time.Minute
	// ForgotKeyCeiling bounds memory: the limiter is keyed on an address the CALLER supplies, so an
	// unbounded map would be a memory-exhaustion vector reachable by anybody who can send a POST. Fully
	// refilled buckets are swept before this is consulted, so reaching it means fifty thousand distinct
	// addresses inside twenty minutes — a flood, not a busy afternoon.
	ForgotKeyCeiling = 50_000
)

// The login ceiling.
//
// # 🔴 Why these are loose where the reset numbers are tight
//
// A wrong password is something people do — three times, then they check caps lock, then they get it.
// The limit has to sit far above that and still far below what makes online guessing worth attempting.
// Ten back to back and one a minute afterwards is roughly 1,500 guesses a day against one account; the
// same account with no limit is bounded only by how fast the server can run argon2id, which is nearer a
// million. Against the twelve-character minimum, 1,500 a day is not an attack, it is a hobby.
//
// 🔴 And the ceiling is only ever charged for FAILURES — see handleLogin. Ten is therefore not "ten
// sign-ins an hour", which would break anybody with several devices; it is ten WRONG ones.
const (
	LoginBurst  = 10
	LoginRefill = time.Minute
	// LoginKeyCeiling bounds memory: the key contains a tenant and an address, both supplied by the
	// caller, so both are attacker-chosen.
	LoginKeyCeiling = 50_000
)

// loginKey identifies one account for the purpose of counting guesses.
//
// 🔴 The address is folded to auth.EmailKey, the form the database compares by — keyed on what was
// typed, the ceiling would be "ten wrong passwords per capitalisation". The NUL separator cannot appear
// in either half, so no pair of (tenant, address) values can collide with another pair by concatenation.
// Redeeming an invitation, and asking for another confirmation mail.
//
// Accepting is something a person does once. Five back to back covers a flaky connection and a double
// click; one a minute afterwards is far more than anybody needs to redeem a link they already hold.
//
// Confirmation mail matches the reset numbers, because it is the same inbox being protected. 🔴 They are
// SEPARATE buckets, so the total this deployment will send to one address is the sum of the two — six an
// hour. Sharing one bucket would model the inbox more exactly and would also mean somebody who asked for
// three password resets could not then confirm their address, which is two unrelated journeys tangled
// together for a ceiling nobody was near.
const (
	AcceptBurst      = 5
	AcceptRefill     = time.Minute
	AcceptKeyCeiling = 50_000

	ResendBurst      = 3
	ResendRefill     = 20 * time.Minute
	ResendKeyCeiling = 50_000

	// Redeeming a password-reset link. The same numbers as accepting an invitation, because it is the
	// same act: somebody holding a one-time token, using it once, with room for a flaky connection.
	RedeemBurst      = 5
	RedeemRefill     = time.Minute
	RedeemKeyCeiling = 50_000

	// Following a confirmation link. Same numbers again: one person, one token, used once, with room for
	// a mail client that follows links and a person who clicks twice.
	ConfirmBurst      = 5
	ConfirmRefill     = time.Minute
	ConfirmKeyCeiling = 50_000
)

// loginKey identifies the account a sign-in attempt is guessing at.
//
// # 🔴 The ADDRESS ALONE, since migration 0008 — and the organization must not be mixed back in
//
// It used to be organization + address, because the same address could name different accounts in
// different organizations, and keying on the address alone would have let guessing at one customer's
// user spend a different customer's user's budget.
//
// An address now identifies exactly one account across the whole deployment, so that reason is gone —
// and including the organization actively creates a BYPASS. The organization arrives in the request
// body and is optional: a caller who omits it gets one bucket, a caller who invents a value gets
// another, and the same account can therefore be guessed at without limit by varying a field the
// attacker controls and the account owner never sees.
//
// The rule: this key may only be built from values that identify the account, never from values the
// caller supplies freely.
func loginKey(email string) string { return auth.EmailKey(email) }

// subjectFor returns the repository loaded by this tenant, if any.
func (s *Server) subjectFor(tenant string) *subjectState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.subjects[tenant]
}

func (s *Server) setSubject(tenant string, sub *subjectState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subjects == nil {
		s.subjects = map[string]*subjectState{}
	}
	s.subjects[tenant] = sub
}

// subjectState is the currently-loaded repository. One at a time, deliberately: a conversation is about
// one repository, and "which repo did that answer describe?" is not a question a person should have to ask.
type subjectState struct {
	Source intake.Source
	Corpus discovery.Corpus
	// Index holds the call sites, computed ONCE. Asking a Corpus for nine axes rescans it nine times —
	// 26 seconds on a 2,541-file repository, for an answer identical every time.
	Index *discovery.Index
}

// Routes returns the API mux WITHOUT authentication. Use Handler for anything served to a network.
//
// 🔴 Every route is registered from `apiRoutes` and each one's capability wrapper is applied HERE, at
// registration. A handler cannot be reached except through the wrapper its row declared, so "the check
// was forgotten" is not a state this mux can be in — the row either names a capability or is visibly
// blank in a table a reviewer reads top to bottom.
func (s *Server) Routes() *http.ServeMux {
	m := http.NewServeMux()
	for _, r := range apiRoutes {
		h := r.Handler(s)
		if !r.Public && r.Needs != "" {
			h = requireCapability(r.Needs, h)
		}
		m.Handle(r.Method+" "+r.Path, h)
	}
	return m
}

// Handler is the API with authentication applied.
//
// 🔴 The ONLY thing that should be served. `Routes` exists so a test can exercise a handler directly;
// mounting it on a listener would serve every endpoint unauthenticated, which is why this wrapper is a
// separate, obviously-named method rather than an option on the other one.
func (s *Server) Handler(static http.Handler) http.Handler {
	mux := s.Routes()
	if static != nil {
		mux.Handle("/", static)
	}
	return s.authenticate(mux)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// ── subject ──────────────────────────────────────────────────────────────────────────────────────

type subjectReq struct {
	Ref string `json:"ref"`
}

type subjectResp struct {
	Reference string        `json:"reference"`
	Describe  string        `json:"describe"`
	Kind      string        `json:"kind"`
	Revision  string        `json:"revision"`
	Dirty     bool          `json:"dirty"`
	Files     int           `json:"files"`
	TestFiles int           `json:"test_files"`
	Truncated bool          `json:"truncated"`
	IsAgent   bool          `json:"is_agent"`
	Why       string        `json:"why"`
	Axes      []axisSummary `json:"axes"`
}

type axisSummary struct {
	Axis  string `json:"axis"`
	Found bool   `json:"found"`
	Spans int    `json:"spans"`
	Files int    `json:"files"`
	First string `json:"first,omitempty"`
	Why   string `json:"why,omitempty"`
	Note  string `json:"note,omitempty"`
}

func (s *Server) handleSubject(w http.ResponseWriter, r *http.Request) {
	tenant, err := tenancy.MustTenant(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	var req subjectReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	src, ix, err := s.loadSubject(tenant, req.Ref)
	if err != nil {
		// 🔴 The intake error text is returned VERBATIM. Every one of them names a next action — run git
		// init, check the repository is public, give a path or a link — and replacing them with a generic
		// "could not load repository" would throw away the only part the person can act on.
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	isAgent, why := ix.LooksLikeAnAgent()

	writeJSON(w, http.StatusOK, s.describeSubject(src, ix, isAgent, why))
}

// loadSubject resolves a reference, indexes it, binds the tools to it and remembers it.
//
// # 🔴 Extracted so a RESTART can replay it
//
// This used to be the body of handleSubject, which meant loading a repository was something only an
// inbound HTTP request could do. The loaded state lived in a Go map and nowhere else, so every deploy
// emptied it for every organization — and nothing said so: the header went blank and the next question
// was refused for having no subject, exactly as though nobody had ever loaded one.
//
// With the work in a function, restoreSubject can run the same path from the reference stored by
// migration 0009. One code path, so a restored subject is bound identically to a freshly loaded one —
// two paths would eventually differ, and the difference would be a tool still pointing at the previous
// repository.
func (s *Server) loadSubject(tenant, ref string) (intake.Source, *discovery.Index, error) {
	src, err := s.Resolver.Resolve(ref)
	if err != nil {
		return intake.Source{}, nil, err
	}
	corpus, err := discovery.Walk(src.Root, discovery.Limits{})
	if err != nil {
		return intake.Source{}, nil, err
	}
	ix := discovery.NewIndex(corpus)
	s.setSubject(tenant, &subjectState{Source: src, Corpus: corpus, Index: ix})

	// Rebind every source-bound tool to this repository. Without this they would act on whatever was
	// loaded last, which is the worst kind of wrong: confident, well-formed, and about someone else's code.
	if s.ToolRegistry != nil && s.Provider != nil {
		_ = s.ToolRegistry.Replace(tools.AssessAxis{
			Provider: s.Provider, Model: s.Model, Source: ix,
		}, nil)
		_ = s.ToolRegistry.Replace(tools.GenerateCases{
			Provider: s.Provider, Model: s.Model, Source: ix,
		}, nil)
		_ = s.ToolRegistry.Replace(tools.PublishEvalSet{Root: src.Root},
			tools.NewPublishVerifier(src.Root))
		_ = s.ToolRegistry.Replace(tools.ProposeChange{
			Provider: s.Provider, Model: s.Model, Source: ix, Root: src.Root,
		}, nil)
		_ = s.ToolRegistry.Replace(tools.VerifyProposal{
			Provider: s.Provider, Model: s.Model, Root: src.Root,
		}, nil)
		_ = s.ToolRegistry.Replace(tools.OpenPullRequest{Root: src.Root},
			tools.NewDeliveryVerifier(src.Root))
	}

	// 🔴 Best effort, and NOT fatal. Failing to write the note would otherwise turn a healthy load into
	// an error, which is a strictly worse outcome than forgetting it across the next restart.
	if s.Auth != nil {
		if err := s.Auth.RememberSubject(context.Background(), tenant, ref, src.Revision); err != nil {
			log.Printf("WARN api.subject.remember_failed tenant=%s: %v", tenant, err)
		}
	}
	return src, ix, nil
}

// restoreSubject rebuilds the in-memory subject from the reference stored for this organization.
//
// Returns nil when there is nothing remembered, which is the ordinary state for an organization that
// has not loaded a repository yet.
func (s *Server) restoreSubject(tenant string) *subjectState {
	if s.Auth == nil || s.Resolver == nil {
		return nil
	}
	ref, _, err := s.Auth.RememberedSubject(context.Background(), tenant)
	if err != nil || ref == "" {
		if err != nil {
			log.Printf("WARN api.subject.recall_failed tenant=%s: %v", tenant, err)
		}
		return nil
	}
	if _, _, err := s.loadSubject(tenant, ref); err != nil {
		// The repository may have moved, gone private, or been deleted since. That is a real answer to
		// give the person, not a reason to fail the request they actually made.
		log.Printf("WARN api.subject.restore_failed tenant=%s ref=%q: %v", tenant, ref, err)
		return nil
	}
	log.Printf("api.subject.restored tenant=%s ref=%q", tenant, ref)
	return s.subjectFor(tenant)
}

// subjectOrRestore returns this organization's loaded repository, rebuilding it after a restart.
func (s *Server) subjectOrRestore(tenant string) *subjectState {
	if sub := s.subjectFor(tenant); sub != nil {
		return sub
	}
	return s.restoreSubject(tenant)
}

func (s *Server) describeSubject(src intake.Source, ix *discovery.Index, isAgent bool, why string) subjectResp {
	resp := subjectResp{
		Reference: src.Reference, Describe: src.Describe(), Kind: string(src.Kind),
		Revision: src.Revision, Dirty: src.Dirty, Files: len(ix.Corpus.Files),
		TestFiles: ix.Corpus.Skipped["test-file"], Truncated: ix.Corpus.Truncated,
		IsAgent: isAgent, Why: why,
	}
	for _, axis := range intent.Axes() {
		ev := ix.ForAxis(axis)
		sum := axisSummary{Axis: axis, Found: ev.Found, Spans: len(ev.Spans), Note: ev.Note}
		files := map[string]bool{}
		for _, sp := range ev.Spans {
			files[sp.Path] = true
		}
		sum.Files = len(files)
		if len(ev.Spans) > 0 {
			sum.First, sum.Why = ev.Spans[0].Ref(), ev.Spans[0].Why
		}
		resp.Axes = append(resp.Axes, sum)
	}
	return resp
}

func (s *Server) handleGetSubject(w http.ResponseWriter, r *http.Request) {
	tenant, err := tenancy.MustTenant(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	sub := s.subjectOrRestore(tenant)
	if sub == nil {
		writeJSON(w, http.StatusOK, map[string]any{"loaded": false})
		return
	}
	isAgent, why := sub.Index.LooksLikeAnAgent()
	writeJSON(w, http.StatusOK, s.describeSubject(sub.Source, sub.Index, isAgent, why))
}

// ── ask ──────────────────────────────────────────────────────────────────────────────────────────

type askReq struct {
	Text string `json:"text"`
	// ConversationID threads one exchange to the next.
	//
	// 🔴 Chosen by the CLIENT, and scoped to the tenant on the way into the store. A server-minted id
	// would need a round trip before the first sentence could be sent, and the browser already has to
	// hold one anyway to survive a refresh. It is not a capability: naming another organization's
	// conversation id yields an empty thread of your own, which `TestATenantCannotReachAnotherTenants
	// Conversation` proves on a real database.
	//
	// Empty is allowed and means "do not record this exchange" — an older client, or a caller that has
	// no conversation. The reply is identical either way; only the memory of it differs.
	ConversationID string `json:"conversation_id"`
}

// askResp is what the console renders. Exactly one of the shapes below is populated, and `kind` says
// which — a response that could be two things at once is one the client has to guess about.
type askResp struct {
	Kind string `json:"kind"` // answer | goal | refusal | redirect | abstain

	Intent string `json:"intent,omitempty"`
	Tier   string `json:"tier,omitempty"`

	// answer
	Text     string       `json:"text,omitempty"`
	Episodes []episodeOut `json:"episodes,omitempty"`
	Axis     *axisSummary `json:"axis,omitempty"`
	Spans    []spanOut    `json:"spans,omitempty"`

	// goal
	// Scope is the axis the run was narrowed to, empty when it covers all nine. Sent so the console can
	// SHOW what it understood — a run that silently narrowed is as confusing as one that silently widened.
	Scope        string   `json:"scope,omitempty"`
	GoalID       string   `json:"goal_id,omitempty"`
	Tasks        []string `json:"tasks,omitempty"`
	CeilingCents int64    `json:"ceiling_cents,omitempty"`

	// proposal — a Tier-C change waiting for a person
	ChangeID       string `json:"change_id,omitempty"`
	Path           string `json:"path,omitempty"`
	Ref            string `json:"ref,omitempty"`
	Diff           string `json:"diff,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`

	// refusal
	Cause      string `json:"cause,omitempty"`
	NextAction string `json:"next_action,omitempty"`

	// redirect
	Surface string `json:"surface,omitempty"`
	Does    string `json:"does,omitempty"`

	// abstain
	CanDo []string `json:"can_do,omitempty"`

	// confirm — a capability the agent chose that spends money or writes, waiting for a person
	ActionID string `json:"action_id,omitempty"`
	// Spends and Writes say WHICH of the two consequences applies, so the card can name the one that
	// matters rather than warning about both. 🔴 Separate booleans, not one "dangerous" flag: "this will
	// cost money" and "this will change your repository" need different sentences and a person weighs
	// them differently.
	Spends bool `json:"spends,omitempty"`
	Writes bool `json:"writes,omitempty"`

	// CostMicroCents is what answering this turn cost.
	//
	// 🔴 Reported on every reply, because it used to be true that answering cost nothing and both this
	// code and the console said so out loud. It is not true any more, and a surface that quietly started
	// charging for something it had described as free would be the worst version of this change.
	CostMicroCents int64 `json:"cost_micro_cents,omitempty"`
}

type episodeOut struct {
	Seq     int64  `json:"seq"`
	Kind    string `json:"kind"`
	TaskID  string `json:"task_id,omitempty"`
	Summary string `json:"summary"`
	Detail  string `json:"detail,omitempty"`
	At      string `json:"at"`
}

type spanOut struct {
	Ref  string `json:"ref"`
	Why  string `json:"why"`
	Text string `json:"text"`
}

func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	// 🔴 The principal, not just the tenant, because the agent is now told who it is talking to. The
	// identity comes from the authenticated session and NEVER from the request body: a client-supplied
	// address would let anybody load a colleague's standing instructions into their own prompt just by
	// naming them.
	p, err := tenancy.From(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	tenant := p.Tenant
	var req askReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}

	resp, decided, err := s.decide(tenant, p.Subject, req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.record(tenant, req, resp, decided)
	writeJSON(w, http.StatusOK, resp)
}

// decide turns one sentence into one reply, WITHOUT writing anything to the network.
//
// # 🔴 Why the decision is separated from the writing
//
// Every reply has to be appended to the transcript before it is sent, and there is exactly one correct
// place for that: after the reply exists and before it leaves. When each branch wrote its own response,
// recording would have had to be copied into eight of them — and the branch somebody forgot would drop
// turns silently, producing a conversation with holes that nothing reports.
//
// The second return says HOW the reply was reached, which the transcript records. See memory.Decider:
// a reply produced by the deterministic floor, by a model, or by the fallback after a model failure are
// three different things that look identical once rendered.
func (s *Server) decide(tenant, asker string, req askReq) (askResp, memory.Decider, error) {
	// 🔴 Unbounded is checked BEFORE routing. The refusal has to happen before anything is planned, which
	// is the entire point of refusing — "keep going until it is perfect" must not first become a goal.
	if router.Unbounded(req.Text) {
		ref := bounds.Refusal{Cause: bounds.UnboundedRequested, Detail: req.Text}
		return askResp{
			Kind: "refusal", Cause: string(ref.Cause), Text: req.Text, NextAction: ref.NextAction(),
		}, memory.DecidedByFloor, nil
	}

	out := s.Router.Route(req.Text)
	switch {
	case out.Redirect != nil:
		// 🔴 Also the floor, and also before any model. Connecting a repository creates a standing read
		// grant whose disclosure must be displayed before the grant exists, so this decision may never
		// depend on something that can be talked out of it.
		return askResp{
			Kind: "redirect", Surface: out.Redirect.Surface, Does: out.Redirect.Does,
			Text: out.Redirect.Topic,
		}, memory.DecidedByFloor, nil
	}
	// 🔴 An abstention is NOT returned here. The whole defect being fixed is that "hi" scored zero and
	// was answered with the catalogue — so a sentence the keyword vocabulary cannot score is precisely
	// the one the agent must be given. The abstention is still reachable, further down, when the agent
	// cannot answer either.

	sub := s.subjectOrRestore(tenant)

	// ── the agent ────────────────────────────────────────────────────────────────────────────────
	//
	// 🔴 Consulted AFTER the floor and BEFORE the keyword router. It may be persuaded, which is why
	// nothing above this line depends on it; and it may fail, which is why nothing below this line
	// depends on it either.
	if resp, answered := s.converseOrFallback(tenant, asker, req, sub); answered {
		return resp, memory.DecidedByModel, nil
	}

	// ── the keyword router ───────────────────────────────────────────────────────────────────────
	//
	// Reached when no agent is configured, or when the one that is could not answer. This is exactly
	// the console's behaviour before the agent existed, which is the point: degrading returns the
	// product to a working state rather than to an error page.
	if out.Abstained() {
		return askResp{Kind: "abstain", CanDo: intent.CanDo()}, memory.DecidedByFallback, nil
	}
	spec, _ := intent.Lookup(out.Intent)

	if sub == nil {
		ref := bounds.Refusal{Cause: bounds.NoSubject}
		return askResp{
			Kind: "refusal", Intent: out.Intent.String(), Cause: string(ref.Cause),
			NextAction: ref.NextAction(),
		}, memory.DecidedByFallback, nil
	}

	switch spec.Tier {
	case intent.TierQuery:
		resp, err := s.answerQuery(tenant, spec, sub)
		return resp, memory.DecidedByFallback, err
	case intent.TierGoal:
		resp, err := s.startGoal(tenant, spec, sub, out.Axis)
		return resp, memory.DecidedByFallback, err
	default:
		resp, err := s.handleEffect(spec, sub, out.Axis, req.Text)
		return resp, memory.DecidedByFallback, err
	}
}

// record appends the exchange to the transcript, so the next sentence has something to refer to.
//
// # 🔴 Why a failure here is logged and swallowed rather than returned
//
// The transcript is an ENHANCEMENT to the reply, never a precondition for it. A person who asked a
// question and got a correct answer must not see an error because the record of that answer could not
// be written — that would take a working main path down for the sake of a side one. The cost is stated
// rather than hidden: a dropped turn means the next question has less context, and the WARN is what
// makes that visible instead of mysterious.
func (s *Server) record(tenant string, req askReq, resp askResp, decided memory.Decider) {
	if s.Episodes == nil || req.ConversationID == "" {
		return
	}
	store := s.Episodes.For(tenant)
	now := time.Now().UTC()
	if _, err := store.AppendTurn(memory.Turn{
		ConversationID: req.ConversationID, Role: memory.TurnUser, Body: req.Text, At: now,
	}); err != nil {
		log.Printf("WARN heros.ask.turn_not_recorded role=user conversation=%q tenant=%q err=%v",
			req.ConversationID, tenant, err)
		return
	}
	// 🔴 CostMicroCents is copied HERE and it was once forgotten, which cost nothing at the time and
	// everything afterwards. The column, the struct field, the INSERT and the whole plumbing from
	// converse.Outcome through askResp all existed and were correct; this literal simply did not name
	// the field, so it defaulted to zero and every turn ever recorded — on both providers — said the
	// conversation was free. The in-turn ceiling still worked, because that reads the live
	// converse.Outcome; it is only the DURABLE record that was empty. So the failure was invisible
	// exactly where you would look for it: spend was enforced, and unauditable.
	//
	// A zero here is now a real claim that a turn cost nothing, which is true only of the deterministic
	// floor and the keyword fallback. Anything decided by the model must carry a figure.
	// Fenced by TestARecordedTurnCarriesWhatItCost.
	if _, err := store.AppendTurn(memory.Turn{
		ConversationID: req.ConversationID, Role: memory.TurnAgent, Body: transcriptBody(resp),
		Kind: resp.Kind, Capability: resp.Intent, Decided: decided,
		CostMicroCents: resp.CostMicroCents, At: now,
	}); err != nil {
		log.Printf("WARN heros.ask.turn_not_recorded role=agent conversation=%q tenant=%q err=%v",
			req.ConversationID, tenant, err)
	}
}

// answerQuery serves a Tier-B intent from what discovery already read.
//
// ⚠️ This used to say "No model call, no cost", and that was true when a keyword table did the routing.
// It is only half true now: producing the ANSWER still reads the index and costs nothing, but
// UNDERSTANDING the question is a model call. The distinction is worth keeping — it is the difference
// between a question and a run — but the old sentence is not, and neither is the one the console used
// to print. See §the cost sentence below.
//
// 🔴 Returns its answer rather than writing it. Every reply now has to be recorded in the transcript
// before it is sent, and a function that writes to the ResponseWriter itself gives the caller nothing
// to record — the recording would have to be duplicated into each branch, which is how one branch ends
// up missing it. One writer, one recorder, at the top of handleAsk.
//
// A non-nil error means the request failed rather than being answered: the caller renders a 500.
func (s *Server) answerQuery(tenant string, spec intent.Spec, sub *subjectState) (askResp, error) {
	resp := askResp{Kind: "answer", Intent: spec.Intent.String(), Tier: string(spec.Tier)}

	if spec.Intent == intent.RunHistory {
		return s.answerRunHistory(tenant, resp)
	}
	if spec.Axis == "" {
		// Queries about the platform's own record rather than an axis, other than run history. Honest
		// placeholder: the record exists, the rendering does not.
		resp.Text = fmt.Sprintf("I can answer %q from what I have stored, but that view is not built yet. "+
			"Ask me about one of the nine axes and I will show you the code.", spec.Question)
		return resp, nil
	}

	ev := sub.Index.ForAxis(spec.Axis)
	sum := axisSummary{Axis: spec.Axis, Found: ev.Found, Spans: len(ev.Spans), Note: ev.Note}
	files := map[string]bool{}
	for _, sp := range ev.Spans {
		files[sp.Path] = true
		if len(resp.Spans) < 6 {
			resp.Spans = append(resp.Spans, spanOut{Ref: sp.Ref(), Why: sp.Why, Text: sp.Text})
		}
	}
	sum.Files = len(files)
	if len(ev.Spans) > 0 {
		sum.First, sum.Why = ev.Spans[0].Ref(), ev.Spans[0].Why
	}
	resp.Axis = &sum

	if !ev.Found {
		resp.Text = ev.Note
	} else {
		// 🔴 The cost sentence, corrected rather than deleted.
		//
		// It used to end "nothing ran just now, and it cost nothing", which was true and is not any
		// more: understanding the question is a model call. What is still true — and is the thing worth
		// telling somebody — is that no RUN started, so no ceiling was drawn against and nothing will
		// keep spending after this reply. Leaving the old wording in place would have been the worst
		// version of this change: a surface that quietly began charging for something it described as
		// free, in a product whose entire pitch is telling people true things.
		resp.Text = fmt.Sprintf("Found %d span(s) across %d file(s) governing %s in %s. "+
			"This is read from what I already parsed, so no run started — only reading your question "+
			"cost anything.",
			len(ev.Spans), len(files), spec.Axis, shortRef(sub.Source.Reference))
	}
	return resp, nil
}

// answerRunHistory reads the most recent run's episodes.
//
// 🔴 A query over what a durable run WROTE DOWN, not a re-derivation. That is the whole payoff of
// persisting everything: "what happened in that run?" is a SELECT, and it costs nothing.
func (s *Server) answerRunHistory(tenant string, resp askResp) (askResp, error) {
	if s.Episodes == nil {
		resp.Text = "No run history is being recorded on this deployment."
		return resp, nil
	}
	last, ok, err := s.Root.For(tenant).LatestGoal(tenant)
	if err != nil {
		return askResp{}, err
	}
	if !ok {
		resp.Text = "Nothing has run yet. Ask me to look at your repository and I will start something."
		return resp, nil
	}
	eps, err := s.Episodes.For(tenant).Episodes(string(last.ID))
	if err != nil {
		return askResp{}, err
	}
	if len(eps) == 0 {
		resp.Text = fmt.Sprintf("The last run (%s, %s) recorded no episodes.", last.Intent, last.State)
		return resp, nil
	}
	for _, e := range eps {
		resp.Episodes = append(resp.Episodes, episodeOut{
			Seq: e.Seq, Kind: string(e.Kind), TaskID: e.TaskID,
			Summary: e.Summary, Detail: e.Detail, At: e.At.Format(time.RFC3339),
		})
	}
	resp.Text = fmt.Sprintf("The last run was %s (%s): %d steps, %s spent.",
		last.Intent, last.State, len(eps), provider.FormatCents(last.Spend.CostMicroCents))
	return resp, nil
}

// criteriaFor returns what it means for THIS goal to be finished.
//
// # 🔴 Why this is per-intent rather than one default
//
// It was one default — "six of nine axes assessed" — applied to every Tier-A goal. An eval-set run has
// no axes, so it could never satisfy it: three generators succeeded, the quality gate passed, the
// artefact was written to the customer's repository, and the goal reported FAILED. Every visible sign
// said the work was done and the record said otherwise.
//
// A completion criterion has to describe the goal it belongs to. An objective borrowed from a different
// intent is not a weaker measure, it is a measure of something else.
func criteriaFor(i intent.Intent, axis string) []goal.Criterion {
	switch i {
	case intent.EvalSet:
		// The published artefact is the product. The gate already enforces its own floors on case count
		// and generator diversity, so the goal only has to see it reach publication.
		return []goal.Criterion{{Kind: goal.EvalCasesGenerated, Threshold: 1}}
	case intent.Compare:
		return []goal.Criterion{{Kind: goal.ComparisonDrawn, Threshold: 1}}
	case intent.Improve:
		// 🔴 A delivered change, not an assessment. Scoring improve on axes assessed let a run that
		// proposed a change, failed to verify it, and delivered nothing report SUCCESS — the same
		// "terminal is not an achievement" mistake in a third place.
		return []goal.Criterion{{Kind: goal.ChangesDelivered, Threshold: 1}}
	default:
		// assess. A narrowed run needs its one axis; a whole-repository run needs most of them, because
		// a report over two axes is not the report that was asked for.
		if axis != "" {
			return []goal.Criterion{{Kind: goal.AxesAssessed, Threshold: 1}}
		}
		return []goal.Criterion{{Kind: goal.AxesAssessed, Threshold: 6}}
	}
}

// startGoal admits a durable goal, plans it, and starts driving it.
func (s *Server) startGoal(tenant string, spec intent.Spec, sub *subjectState, axis string) (askResp, error) {
	now := time.Now().UTC()
	g := &goal.Goal{
		ID: goal.ID(fmt.Sprintf("g-%d", now.UnixNano())), Tenant: tenant,
		Intent: spec.Intent, State: goal.Draft, Objective: spec.Question,
		Subject: goal.Subject{
			RepoURL:  firstNonEmpty(sub.Source.RemoteURL, sub.Source.Root),
			Revision: sub.Source.Revision,
		},
		Ceilings:  s.Ceilings,
		CreatedAt: now, UpdatedAt: now,
	}
	// 🔴 The scope the person named is CARRIED, not discarded. "How do I improve the prompt?" names one
	// axis; planning nine spends nine times what was asked for, and the run reads as incoherent because
	// it is answering a question nobody put.
	//
	// The completion threshold moves with the scope for the same reason: a one-axis goal that requires
	// six assessed axes can never complete, so it would run to exhaustion and report a stall.
	if axis != "" {
		g.Axes = []string{axis}
		g.Objective = fmt.Sprintf("%s — scoped to the %s axis", spec.Question, axis)
	}
	g.Criteria = criteriaFor(spec.Intent, axis)
	if err := g.Admit(now); err != nil {
		var ref bounds.Refusal
		if asRefusal(err, &ref) {
			return askResp{
				Kind: "refusal", Intent: spec.Intent.String(), Cause: string(ref.Cause),
				Text: ref.Detail, NextAction: ref.NextAction(),
			}, nil
		}
		return askResp{
			Kind: "refusal", Intent: spec.Intent.String(), Cause: "not_admitted",
			NextAction: err.Error()}, nil
	}
	scoped := s.Root.For(tenant)
	if err := scoped.CreateGoal(g); err != nil {
		return askResp{}, err
	}
	d, err := s.Planners.Build(g, now)
	if err != nil {
		return askResp{
			Kind: "refusal", Intent: spec.Intent.String(), Cause: "could_not_plan",
			NextAction: err.Error()}, nil
	}
	if err := scoped.SaveDAG(d); err != nil {
		return askResp{}, err
	}

	ids := sortedTaskIDs(d)
	// 🔴 Driven with context.Background(), NOT the request's context. The request ends when the browser
	// has its goal id; the run outlives it by design. Tying a durable goal's lifetime to one HTTP request
	// is how a refresh cancels an hour of work.
	if sup := s.supFor(tenant); sup != nil {
		sup.Start(context.Background(), g.ID)
	} else {
		// The goal and its DAG are already written, so this is recoverable rather than lost — but
		// nothing will pick it up until somebody notices, which is exactly what this line is for.
		log.Printf("WARN heros.goal.not_driven goal=%q tenant=%q reason=%q",
			g.ID, tenant, "this deployment has no supervisor factory")
	}

	return askResp{
		Kind: "goal", Intent: spec.Intent.String(), Tier: string(spec.Tier),
		GoalID: string(g.ID), Tasks: ids, CeilingCents: g.Ceilings.MaxCostCents,
		Text: g.Objective, Scope: axis,
	}, nil
}

// handleDecideRequest decodes an approval decision.
func (s *Server) handleDecideRequest(w http.ResponseWriter, r *http.Request) {
	tenant, err := tenancy.MustTenant(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	var req decideReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	s.handleDecide(w, tenant, req)
}

// ── events ───────────────────────────────────────────────────────────────────────────────────────

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	// 🔴 The stream is scoped: a goal id alone must not be enough to watch somebody else's run, which
	// would leak its findings, its spend and its file paths in real time.
	tenant, err := tenancy.MustTenant(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	id := goal.ID(r.PathValue("id"))
	if _, err := s.Root.For(tenant).LoadGoal(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such run"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	sup := s.supFor(tenant)
	ch := sup.Subscribe(id)
	defer sup.Unsubscribe(id, ch)

	// A heartbeat keeps intermediaries from closing an idle stream, and lets the browser tell "the run is
	// quiet" from "the connection died" — which look identical without it.
	beat := time.NewTicker(15 * time.Second)
	defer beat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-beat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		case e, open := <-ch:
			if !open {
				return
			}
			b, err := json.Marshal(e)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
			if e.Terminal {
				return
			}
		}
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────────────────────────

// sortedTaskIDs returns the plan's task ids in a stable order, so the console renders the same ledger
// on every load rather than a set in map order.
func sortedTaskIDs(d *task.DAG) []string {
	out := make([]string, 0, len(d.Tasks))
	for id := range d.Tasks {
		out = append(out, string(id))
	}
	sort.Strings(out)
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func shortRef(s string) string {
	if len(s) > 48 {
		return "…" + s[len(s)-47:]
	}
	return s
}

func asRefusal(err error, out *bounds.Refusal) bool {
	r, ok := err.(bounds.Refusal)
	if ok {
		*out = r
	}
	return ok
}

// ── conversation ─────────────────────────────────────────────────────────────────────────────────

// conversationResp is a thread, replayed.
type conversationResp struct {
	// ConversationID is the thread these turns belong to. Echoed back because the client may not have
	// named one: asking with no id resumes the most recent thread, and the client needs to know which
	// one it got so its next question joins the same conversation rather than forking a new one.
	ConversationID string    `json:"conversation_id"`
	Turns          []turnOut `json:"turns"`
}

type turnOut struct {
	Seq  int64  `json:"seq"`
	Role string `json:"role"`
	Body string `json:"body"`
	// Kind is the response shape this turn was rendered as, so a replay draws the cards the person
	// originally saw rather than flattening everything into prose.
	Kind       string `json:"kind,omitempty"`
	Capability string `json:"capability,omitempty"`
	// Decided says how the reply was reached. Surfaced to the client rather than kept internal: a turn
	// answered by the keyword fallback while the model was unavailable is a DIFFERENT thing from one
	// the agent reasoned about, and a person re-reading the thread deserves to be able to tell.
	Decided string `json:"decided,omitempty"`
	At      string `json:"at"`
}

// handleConversation replays one thread.
//
// 🔴 What comes back is only what was WRITTEN DOWN. The cards a run produced — task lists, diffs, live
// progress — are rebuilt from the durable goal record by /api/history, not from here. Two sources
// because they have two lifetimes: a sentence is final the moment it is said, and a run keeps changing
// after the sentence that started it.
func (s *Server) handleConversation(w http.ResponseWriter, r *http.Request) {
	tenant, err := tenancy.MustTenant(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	if s.Episodes == nil {
		writeJSON(w, http.StatusOK, conversationResp{})
		return
	}
	store := s.Episodes.For(tenant)

	id := r.URL.Query().Get("conversation_id")
	if id == "" {
		latest, ok, err := store.LatestConversation(tenant)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if !ok {
			// No thread yet is not an error. A browser opening the console for the first time asks this
			// question and must get an empty conversation, not a 404 it has to special-case.
			writeJSON(w, http.StatusOK, conversationResp{})
			return
		}
		id = latest
	}

	turns, err := store.Turns(tenant, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp := conversationResp{ConversationID: id, Turns: []turnOut{}}
	for _, t := range turns {
		resp.Turns = append(resp.Turns, turnOut{
			Seq: t.Seq, Role: string(t.Role), Body: t.Body, Kind: t.Kind,
			Capability: t.Capability, Decided: string(t.Decided), At: t.At.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// transcriptBody is what an agent turn READS AS when the thread is replayed or re-read by the agent.
//
// # 🔴 Why this exists rather than storing resp.Text directly
//
// Several reply shapes carry no prose at all. An abstention's content is its `can_do` list; a redirect's
// is a surface and what that surface does. Storing `resp.Text` for those writes an EMPTY body — which
// replays as a blank bubble, and, worse, gives the agent an empty turn to reason from next time. "What
// did you just tell me?" would be answered from nothing.
//
// 🚫 Deliberately NOT the whole response serialised. A diff, a span list and a task DAG all live
// somewhere durable already; copying them here would duplicate data that can then go stale against its
// source, and the transcript would quietly become a second, worse copy of the run record.
func transcriptBody(resp askResp) string {
	// 🔴 Shape FIRST, `Text` only as the fallback — not the other way round.
	//
	// `Text` does not mean the same thing in every shape. On an answer it is the reply; on an unbounded
	// refusal it is the ECHO of what the person typed, which the console renders as "You asked: …". A
	// version of this function that preferred Text wrote the person's own sentence into the transcript
	// as the agent's reply, so the thread replayed as the agent repeating them back. Caught by driving
	// the real endpoint, not by any unit test — which is why there is now one below.
	//
	// See workflow/CI/bugfix/20260901-heros-console-was-never-a-conversation.md
	// Fenced by TestARefusalDoesNotReplayAsTheAgentRepeatingYou.
	switch resp.Kind {
	case "refusal":
		if resp.NextAction != "" {
			return fmt.Sprintf("I refused (%s). What to do instead: %s", resp.Cause, resp.NextAction)
		}
		return fmt.Sprintf("I refused (%s).", resp.Cause)
	case "redirect":
		return fmt.Sprintf("That is done at %s, which handles %s.", resp.Surface, resp.Does)
	}
	if resp.Text != "" {
		return resp.Text
	}
	switch resp.Kind {
	case "confirm":
		return fmt.Sprintf("I asked whether to go ahead: %s", resp.Text)
	case "abstain":
		return "I could not route that, and offered the whole list of what I do."
	case "proposal":
		return fmt.Sprintf("I proposed a change to %s, waiting for approval.", resp.Path)
	case "goal":
		return fmt.Sprintf("I started a %s run.", resp.Intent)
	default:
		// 🔴 Named rather than blank. A shape added later that nobody taught this function about should
		// read as an unrendered shape in the transcript, not as silence — silence is indistinguishable
		// from the agent having said nothing at all.
		return fmt.Sprintf("(%s)", resp.Kind)
	}
}
