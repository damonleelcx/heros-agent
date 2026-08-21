package launch

import (
	"log/slog"
	"time"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/conversation"
	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/eventname"
)

// conversationwiring.go assembles P31's conversational surface from whatever this deployment mounted.
//
// # Why it is mounted UNCONDITIONALLY, unlike every other capability in capabilities.go
//
// Every other mount here is gated on a durable store, and the pattern is right for a read model: a
// deployment with no platform database has no proposals to show, so the surface answers 503 and says so.
//
// The conversational console is different in one way that decides it: **it is the surface that explains
// the others**. On a deployment with nothing reported yet, the honest answer to "what does my agent
// actually do?" is *"nothing has been reported for this workflow — run `heros link --with-ir`"*, and
// that answer is more valuable than any other screen in the product at that moment. Gating the whole
// surface on the presence of data would hide the one thing that tells a new customer what to do next.
//
// So it mounts always, and the READER is what degrades: `platformSurfaceReader.Mounted` reports which
// intents this deployment can answer at all (that is the `not_mounted` failure class), and every
// intent it can answer produces one of the four finding states rather than an absence.
//
// # 🔴 What is NOT degraded, ever
//
// The artifact resolvers. A deployment that cannot resolve a `proposal_id` gets a NIL resolver, and the
// emitter refuses to emit a `proposal` at all rather than skipping the check — `ErrNoResolver`. The
// alternative would make a missing store into a silently disabled security control, which is the
// failure shape this repository has learned to be most afraid of: everything green, nothing enforced.

// mountConversations wires the conversational console. It never fails the boot: a mount that could stop
// a deployment starting would make a chat surface a dependency of the platform, which inverts what it is.
func mountConversations(h *api.Server, gate *entitlement.Gate, pins api.ConversationPinSource) Capability {
	store := conversation.NewStore(time.Now)
	reader := h.ConversationSurfaceReader()

	mount := &api.ConversationMount{
		Store:     store,
		Approvals: h.ConversationApprovalGate(),
		Workflows: h.ConversationWorkflows(),
		Resolvers: h.ConversationResolvers(),
		Log:       slog.Default(),
		Now:       time.Now,
		Observe:   conversationObserver(),
	}
	mount.Runner = &conversation.Runner{
		Store:   store,
		Router:  conversation.NewRouter(),
		Reader:  reader,
		Pins:    h.ConversationPins(pins),
		Budgets: h.ConversationBudgets(gate),
		Now:     time.Now,
		Observe: mount.Observe,
	}
	h.MountConversations(mount)

	detail := "p31_conversational_console (ask in English on /app/ask; typed messages stream back over " +
		"SSE, every finding carries its evidence, and an approval routes to the existing gate"
	if pins == nil {
		// 🔴 STATED, not hidden. A deployment with no inference store answers every repeated question by
		// re-reading its surfaces, which is correct and cheap — but FR11's determinism guarantee has
		// nothing to replay from, and an operator reading "served" would otherwise assume it does.
		detail += "; no inference store, so nothing is pinned and every question is answered fresh"
	}
	return Capability{Name: detail + ")", Served: true}
}

// conversationObserver writes an event from the CENTRAL ENUM to the process log.
//
// 🔴 The name is an `eventname.Name`, so a call site cannot invent one — that is the whole point of the
// enum, and the reason this indirection exists rather than a `slog.Info("console.conversation...")` at
// four call sites. A template literal is what an engineer reaches for when they want a name to vary, and
// a varying name is a free-text field on the far side of a boundary.
func conversationObserver() func(eventname.Name, map[string]any) {
	return func(name eventname.Name, attrs map[string]any) {
		if !name.Valid() {
			// An invented name is DROPPED rather than emitted. Emitting it would defeat the enum; a
			// silent drop with a WARN beside it is the honest compromise, because an event nobody
			// declared is an event no dashboard can already be reading.
			slog.Default().Warn("an undeclared event name was suppressed",
				"event", eventname.ConversationRefused.String())
			return
		}
		args := make([]any, 0, len(attrs)*2+2)
		args = append(args, "event", name.String())
		for k, v := range attrs {
			args = append(args, k, v)
		}
		slog.Default().Info("conversation", args...)
	}
}
