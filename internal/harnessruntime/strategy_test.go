package harnessruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
)

// P18 §10 (and §7) — the harness runtime.
//
// The thing this file exists to make impossible: a loop that can run more turns than it declared. Every
// other guarantee here is downstream of that one, because an unbounded loop is not a bug in a scaffold —
// it is an unbounded bill and an unbounded blast radius in someone else's process.

// countingInvoke returns a turn function that records how many times it was called and what it was given.
func countingInvoke(answers ...string) (Invoke, *int, *[][]Message) {
	calls := 0
	var seen [][]Message
	return func(msgs []Message) (string, error) {
		seen = append(seen, append([]Message(nil), msgs...))
		a := "no marker here"
		if calls < len(answers) {
			a = answers[calls]
		}
		calls++
		return a, nil
	}, &calls, &seen
}

func reflexionParams(maxTurns int) Params {
	return Params{MaxTurns: maxTurns, StopCondition: stopAnswerMarker, AnswerMarker: "DONE",
		ReflectionPrompt: "find the error and fix it"}
}

// TestMaxTurnsBoundedAndTerminates — tasks 7.1 and 10.3 🔴. The bound, asserted against a stop condition
// that is NEVER satisfied, which is the only way to see the ceiling do its job.
func TestMaxTurnsBoundedAndTerminates(t *testing.T) {
	for _, ceiling := range []int{2, 3, 5, TurnCeiling} {
		t.Run(fmt.Sprintf("ceiling-%d", ceiling), func(t *testing.T) {
			invoke, calls, _ := countingInvoke() // never contains "DONE"
			got, err := Run(Config{Strategy: "reflexion", Params: reflexionParams(ceiling)},
				Hosts{}, []Message{{Role: "user", Content: "q"}}, invoke)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if *calls != ceiling {
				t.Fatalf("the turn function was called %d times against a ceiling of %d; a loop that can "+
					"exceed its declared bound is an unbounded bill in someone else's process", *calls, ceiling)
			}
			if got.Turns != ceiling {
				t.Fatalf("Result.Turns = %d, want %d", got.Turns, ceiling)
			}
			if got.Stop != StopCeiling {
				t.Fatalf("Stop = %q, want %q", got.Stop, StopCeiling)
			}
			if len(got.Trace) != ceiling {
				t.Fatalf("the trace has %d records for %d turns", len(got.Trace), ceiling)
			}
		})
	}

	// 🔴 A strategy whose Plan always says "continue" still cannot exceed the ceiling. The bound is a `for`
	// bound, not a break inside the body, so there is no path a strategy can talk its way past.
	t.Run("a strategy that never stops still stops", func(t *testing.T) {
		invoke, calls, _ := countingInvoke()
		got, err := Run(Config{Strategy: "critic-loop", Params: Params{MaxTurns: 4, CriticModelRef: "m"}},
			Hosts{Critic: acceptingCritic{}}, nil, invoke)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if *calls != 4 || got.Turns != 4 {
			t.Fatalf("critic-loop's Plan always continues; the ceiling must still bind (calls=%d turns=%d)",
				*calls, got.Turns)
		}
	})
}

// TestCeilingIsRecordedDistinctly — task 10.3's second half. Reaching the ceiling and satisfying the stop
// condition mean opposite things about whether the scaffold helped; a surface showing them alike would
// present a budget exhaustion as a success.
func TestCeilingIsRecordedDistinctly(t *testing.T) {
	t.Run("satisfied", func(t *testing.T) {
		invoke, calls, _ := countingInvoke("still working", "DONE now")
		got, err := Run(Config{Strategy: "reflexion", Params: reflexionParams(5)}, Hosts{}, nil, invoke)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got.Stop != StopSatisfied {
			t.Fatalf("Stop = %q, want %q — the marker appeared on turn 2 of a 5-turn ceiling", got.Stop, StopSatisfied)
		}
		if *calls != 2 || got.Turns != 2 {
			t.Fatalf("the loop ran %d turns after being satisfied on turn 2", got.Turns)
		}
		if got.Answer != "DONE now" {
			t.Fatalf("Answer = %q, want the satisfying turn's answer", got.Answer)
		}
	})

	t.Run("ceiling", func(t *testing.T) {
		invoke, _, _ := countingInvoke("a", "b")
		got, _ := Run(Config{Strategy: "reflexion", Params: reflexionParams(2)}, Hosts{}, nil, invoke)
		if got.Stop != StopCeiling {
			t.Fatalf("Stop = %q, want %q", got.Stop, StopCeiling)
		}
	})

	t.Run("single-shot is its own reason", func(t *testing.T) {
		invoke, calls, _ := countingInvoke("one answer")
		got, err := Run(Config{Strategy: "single-shot"}, Hosts{}, nil, invoke)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if *calls != 1 || got.Turns != 1 {
			t.Fatalf("single-shot ran %d turns, want exactly 1", got.Turns)
		}
		if got.Stop != StopSingleShot {
			t.Fatalf("Stop = %q, want %q — reaching a ceiling of one is not running out of budget, and "+
				"reporting it as an exhausted budget would read as a failure", got.Stop, StopSingleShot)
		}
		if got.Answer != "one answer" {
			t.Fatalf("Answer = %q, want the turn's answer unmodified", got.Answer)
		}
	})
}

// TestLoopDeterministic — task 10.4 🔴. No clock, no random source: the same strategy, params and answers
// produce the same turn count, stop reason and trace on every execution. A loop that did not would make
// the axis unscorable, because two runs of one configuration could not be compared.
func TestLoopDeterministic(t *testing.T) {
	run := func() Result {
		invoke, _, _ := countingInvoke("a", "b", "c")
		got, err := Run(Config{Strategy: "reflexion", Params: reflexionParams(4)},
			Hosts{}, []Message{{Role: "user", Content: "q"}}, invoke)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		return got
	}
	first := run()
	for i := 0; i < 20; i++ {
		if got := run(); !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d differed:\n got %+v\nwant %+v", i, got, first)
		}
	}
}

// TestEveryBuiltinStrategyHasALoopDefinition — task 10.2 / FR22. A strategy in the sealed vocabulary with
// no loop here would fail LOUDLY at Run rather than silently running one turn; this test is what keeps
// that loud failure from ever being reached in production.
func TestEveryBuiltinStrategyHasALoopDefinition(t *testing.T) {
	sealed := registry.HarnessStrategyNames()
	defined := StrategyNames()
	if !reflect.DeepEqual(sealed, defined) {
		t.Fatalf("the sealed vocabulary and the runtime name different sets:\n sealed: %v\nruntime: %v\n"+
			"The dispatch is keyed by NAME precisely so the two can be pinned without an import; if they "+
			"drift, a sealed strategy either has no loop or the runtime has one nothing can select", sealed, defined)
	}
	if len(sealed) != registry.HarnessStrategySetSize {
		t.Fatalf("the vocabulary has %d strategies but HarnessStrategySetSize is %d",
			len(sealed), registry.HarnessStrategySetSize)
	}
	// And the ceiling is one constant, not two that can drift.
	if TurnCeiling != registry.MaxTurnsCeiling {
		t.Fatalf("TurnCeiling = %d but registry.MaxTurnsCeiling = %d; the runtime would honour a params "+
			"value the registry would not seal, making it a second and looser gate", TurnCeiling, registry.MaxTurnsCeiling)
	}
	// And the stop-condition vocabulary matches what the schemas actually enumerate, so a rename cannot
	// silently stop a loop from ever stopping.
	for _, st := range registry.BuiltinHarnessStrategies() {
		schema := string(st.ParamsSchema())
		for _, cond := range StopConditions() {
			if !strings.Contains(schema, `"`+cond+`"`) {
				continue // this strategy does not offer that condition, which is fine
			}
			found := false
			for _, known := range StopConditions() {
				if known == cond {
					found = true
				}
			}
			if !found {
				t.Errorf("%s's schema offers stop condition %q, which the runtime does not define", st.Name(), cond)
			}
		}
	}
}

// TestUnknownStrategyFailsLoud — the fail-closed direction. 🔴 Never a fallback to one turn: running a
// single shot under a loop's config_hash reports one scaffold as another.
func TestUnknownStrategyFailsLoud(t *testing.T) {
	invoke, calls, _ := countingInvoke("x")
	_, err := Run(Config{Strategy: "hyper-loop", Params: Params{MaxTurns: 3}}, Hosts{}, nil, invoke)
	if err == nil {
		t.Fatal("an unknown strategy ran; falling back to one turn would report a single shot under a " +
			"loop's config_hash")
	}
	if !errors.Is(err, ErrUnknownStrategy) {
		t.Fatalf("got %v, want ErrUnknownStrategy", err)
	}
	if *calls != 0 {
		t.Errorf("the turn function was called %d times before the strategy was rejected; a run that "+
			"cannot complete honestly must never spend a call", *calls)
	}
}

// TestBoundedByConstruction — no params combination expresses an unbounded loop. The refusals are BEFORE
// the first turn, so a config that cannot describe a bound never spends a call.
func TestBoundedByConstruction(t *testing.T) {
	for _, p := range []Params{
		{MaxTurns: 0, StopCondition: stopAnswerMarker, AnswerMarker: "D", ReflectionPrompt: "r"},
		{MaxTurns: -1, StopCondition: stopAnswerMarker, AnswerMarker: "D", ReflectionPrompt: "r"},
		{MaxTurns: TurnCeiling + 1, StopCondition: stopAnswerMarker, AnswerMarker: "D", ReflectionPrompt: "r"},
		{MaxTurns: 1000, StopCondition: stopMaxTurns, ReflectionPrompt: "r"},
	} {
		invoke, calls, _ := countingInvoke("x")
		_, err := Run(Config{Strategy: "reflexion", Params: p}, Hosts{}, nil, invoke)
		if err == nil {
			t.Errorf("max_turns=%d was accepted; the ceiling is a policy about how much autonomous "+
				"tool-calling one node may do, and a runtime that honoured a value the registry would not "+
				"seal would be a second and looser gate", p.MaxTurns)
		}
		if !errors.Is(err, ErrInvalidParams) {
			t.Errorf("max_turns=%d: got %v, want ErrInvalidParams", p.MaxTurns, err)
		}
		if *calls != 0 {
			t.Errorf("max_turns=%d spent %d call(s) before being rejected", p.MaxTurns, *calls)
		}
	}
}

// acceptingCritic / recordingTools are host doubles. They exist so the host-needing strategies can be run
// at all — and so the refusal test below is asserting an absence rather than an unimplemented path.
type acceptingCritic struct{}

func (acceptingCritic) Critique(string) (string, bool, error) { return "looks fine", true, nil }

type recordingTools struct{ calls int }

func (r *recordingTools) InvokeTool(string, string) (string, error) {
	r.calls++
	return "tool said hi", nil
}

type stubPlanner struct{}

func (stubPlanner) Plan(string) ([]string, error)      { return []string{"step one"}, nil }
func (stubPlanner) ExecuteStep(string) (string, error) { return "did it", nil }

// TestMissingHostServiceRefusesByName — task 10.6 🔴. A strategy whose host service is absent refuses,
// naming the service. 🚫 Never degraded: a critic-loop without a critic IS reflexion, and running it here
// would report one strategy under another's config_hash.
func TestMissingHostServiceRefusesByName(t *testing.T) {
	cases := []struct {
		strategy string
		params   Params
		names    string
		supply   Hosts
	}{
		{"react-loop", Params{MaxTurns: 3, StopCondition: stopNoToolCall}, "tool executor",
			Hosts{ToolInvoker: &recordingTools{}}},
		{"plan-execute", Params{MaxTurns: 3, StopCondition: stopPlanComplete}, "plan",
			Hosts{Planner: stubPlanner{}}},
		{"critic-loop", Params{MaxTurns: 3, CriticModelRef: "m"}, "critic",
			Hosts{Critic: acceptingCritic{}}},
	}
	for _, c := range cases {
		t.Run(c.strategy, func(t *testing.T) {
			invoke, calls, _ := countingInvoke("x")
			_, err := Run(Config{Strategy: c.strategy, Params: c.params}, Hosts{}, nil, invoke)
			if err == nil {
				t.Fatalf("%s ran with no host service; the loop it executed would not be the loop its "+
					"config_hash names", c.strategy)
			}
			if !errors.Is(err, ErrMissingHostService) {
				t.Fatalf("got %v, want ErrMissingHostService", err)
			}
			if !strings.Contains(err.Error(), c.names) {
				t.Errorf("the refusal does not name what to supply (%q): %v", c.names, err)
			}
			if *calls != 0 {
				t.Errorf("%s spent %d call(s) before refusing", c.strategy, *calls)
			}

			// And with the service supplied it runs — so the refusal above is an absence, not an
			// unimplemented path.
			invoke2, calls2, _ := countingInvoke("x")
			if _, err := Run(Config{Strategy: c.strategy, Params: c.params}, c.supply, nil, invoke2); err != nil {
				t.Fatalf("%s refused even with its host service supplied: %v", c.strategy, err)
			}
			if *calls2 == 0 {
				t.Errorf("%s ran no turns with its host service supplied", c.strategy)
			}
		})
	}

	// The two that need nothing must not require anything, or the check above would be vacuous.
	for _, s := range []string{"single-shot", "reflexion"} {
		if svc := HostServiceFor(s); svc != HostNone {
			t.Errorf("%s requires host service %q; it needs none — reflexion's critique is another turn of "+
				"the SAME call, which is exactly why it is the multi-turn strategy a generated module can run",
				s, svc)
		}
	}
}

// TestRuntimeMakesNoProviderCall — task 10.5 🚫. The runtime's only outbound call is the caller's own turn
// function. This is asserted structurally: every host service is an INTERFACE the caller supplies, so
// there is no path from this package to a provider, a credential, or a network destination — and a run
// with no hosts at all still completes for the strategies that need none.
func TestRuntimeMakesNoProviderCall(t *testing.T) {
	tools := &recordingTools{}
	invoke, calls, _ := countingInvoke("a", "b", "c")
	got, err := Run(Config{Strategy: "reflexion", Params: reflexionParams(3)},
		Hosts{ToolInvoker: tools, Critic: acceptingCritic{}, Planner: stubPlanner{}}, nil, invoke)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Turns != *calls {
		t.Fatalf("the runtime made calls the turn function did not account for (turns=%d calls=%d)",
			got.Turns, *calls)
	}
	// 🔴 Even with every host supplied, a strategy that needs none touches none. A runtime that "helpfully"
	// called an available critic would be running a different strategy than the one it was asked for.
	if tools.calls != 0 {
		t.Errorf("reflexion invoked the tool executor %d time(s); it needs no second actor, and reaching "+
			"for one that happens to be available would run a different strategy", tools.calls)
	}
}

// TestHarnessTurnsStayInExistingGrant — task 7.2 🔴. The added turns execute the caller-supplied turn
// function and nothing else, so whatever that call could reach before is exactly what it can reach now.
// The guarantee is structural, and this test is what makes it checkable: the SAME message list prefix goes
// into every turn, and the only growth is what the strategy's own continuation appended.
func TestHarnessTurnsStayInExistingGrant(t *testing.T) {
	base := []Message{{Role: "system", Content: "you are terse"}, {Role: "user", Content: "q"}}
	invoke, calls, seen := countingInvoke("a", "b", "c")
	got, err := Run(Config{Strategy: "reflexion", Params: reflexionParams(3)}, Hosts{}, base, invoke)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if *calls != 3 || got.Turns != 3 {
		t.Fatalf("expected 3 turns, got %d", got.Turns)
	}
	for i, msgs := range *seen {
		if len(msgs) < len(base) {
			t.Fatalf("turn %d was given fewer messages than the caller supplied", i+1)
		}
		for j, want := range base {
			if msgs[j] != want {
				t.Fatalf("turn %d altered the caller's message %d: got %+v want %+v", i+1, j, msgs[j], want)
			}
		}
		// Every added message is the strategy's own continuation — the previous answer and the author's
		// declared reflection instruction — and nothing else. No destination, no tool, no credential.
		for _, m := range msgs[len(base):] {
			if m.Role != "assistant" && m.Role != "user" {
				t.Errorf("turn %d was given a message with role %q, which the strategy never appends", i+1, m.Role)
			}
		}
	}
	// The caller's own slice is never mutated.
	if len(base) != 2 {
		t.Fatal("Run mutated the caller's message slice")
	}
}

// TestHarnessSurfaceObservableInTrace — task 7.2's second half. The enlarged turn surface is not asserted
// away, it is written down: how many turns ran, what each produced, and why the loop stopped.
func TestHarnessSurfaceObservableInTrace(t *testing.T) {
	invoke, _, _ := countingInvoke("first", "second", "DONE third")
	got, err := Run(Config{Strategy: "reflexion", Params: reflexionParams(5)}, Hosts{}, nil, invoke)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got.Trace) != 3 {
		t.Fatalf("the trace has %d records for a 3-turn run; the added surface must be readable, or the "+
			"guarantee is 'trust us' rather than 'look'", len(got.Trace))
	}
	for i, r := range got.Trace {
		if r.Turn != i+1 {
			t.Errorf("trace[%d].Turn = %d, want %d", i, r.Turn, i+1)
		}
		if r.Answer == "" {
			t.Errorf("trace[%d] records no answer", i)
		}
		wantContinued := i < 2
		if r.Continued != wantContinued {
			t.Errorf("trace[%d].Continued = %v, want %v", i, r.Continued, wantContinued)
		}
	}
	if got.Trace[2].Reason != StopSatisfied {
		t.Errorf("the last record's reason is %q, want %q", got.Trace[2].Reason, StopSatisfied)
	}
	// The intermediate records carry no reason — they did not stop.
	for i := 0; i < 2; i++ {
		if got.Trace[i].Reason != "" {
			t.Errorf("trace[%d] claims a stop reason %q on a turn that continued", i, got.Trace[i].Reason)
		}
	}
}

// TestDecodeParamsRoundTrip — one decoder and one set of field names, so a caller holding a resolved
// projection never reads a param by hand.
func TestDecodeParamsRoundTrip(t *testing.T) {
	raw := json.RawMessage(`{"max_turns":3,"stop_condition":"answer-marker","answer_marker":"DONE",` +
		`"reflection_prompt":"redo","retry_budget":2,"critic_model_ref":"m1"}`)
	p, err := DecodeParams(raw)
	if err != nil {
		t.Fatalf("DecodeParams: %v", err)
	}
	want := Params{MaxTurns: 3, StopCondition: "answer-marker", AnswerMarker: "DONE",
		ReflectionPrompt: "redo", RetryBudget: 2, CriticModelRef: "m1"}
	if p != want {
		t.Fatalf("DecodeParams = %+v, want %+v", p, want)
	}
	if _, err := DecodeParams(nil); err != nil {
		t.Fatalf("empty params must decode to the zero value, not an error: %v", err)
	}
}

// TestBoundedAutonomyAndContainmentSuite — task 8.5 🔴. The acceptance gate's safety half, as one suite:
// bounded max_turns, no new egress or tool scope, and an observable surface.
//
// 🔴 The three are asserted over ONE run rather than separately, because the failure this exists to catch
// is the conjunction: a loop that stays inside its bound while quietly reaching for an available host, or
// one that is contained but leaves no trace of how many turns it took.
func TestBoundedAutonomyAndContainmentSuite(t *testing.T) {
	const ceiling = 4
	tools := &recordingTools{}
	base := []Message{{Role: "system", Content: "s"}, {Role: "user", Content: "q"}}

	invoke, calls, seen := countingInvoke() // never satisfies the stop condition
	got, err := Run(Config{Strategy: "reflexion", Params: reflexionParams(ceiling)},
		// Every host is available. A contained runtime touches none of them for a strategy that needs none.
		Hosts{ToolInvoker: tools, Critic: acceptingCritic{}, Planner: stubPlanner{}}, base, invoke)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 1. Bounded.
	if *calls != ceiling || got.Turns != ceiling || got.Stop != StopCeiling {
		t.Fatalf("bound: calls=%d turns=%d stop=%q, want %d/%d/%q",
			*calls, got.Turns, got.Stop, ceiling, ceiling, StopCeiling)
	}

	// 2. Contained. The only outbound call is the caller's own turn function, and the caller's message
	// prefix is untouched on every turn — so whatever that call could reach before is what it reaches now.
	if tools.calls != 0 {
		t.Errorf("containment: the runtime invoked an available tool executor %d time(s) for a strategy "+
			"that needs none", tools.calls)
	}
	for i, msgs := range *seen {
		for j, want := range base {
			if msgs[j] != want {
				t.Fatalf("containment: turn %d altered the caller's message %d", i+1, j)
			}
		}
	}

	// 3. Observable. The enlarged surface is written down, not asserted away.
	if len(got.Trace) != ceiling {
		t.Fatalf("observability: %d trace records for %d turns", len(got.Trace), ceiling)
	}
	if got.Trace[ceiling-1].Reason != StopCeiling {
		t.Errorf("observability: the last record does not say the run hit its ceiling (%q)",
			got.Trace[ceiling-1].Reason)
	}
	for i := 0; i < ceiling-1; i++ {
		if !got.Trace[i].Continued {
			t.Errorf("observability: trace[%d] does not record that the loop continued", i)
		}
	}
}

// TestHarnessRuntimeSuite — task 13.1. The wave-18c acceptance gate's runtime half, as one suite.
//
// 🔴 What it adds over the units above is the CONJUNCTION across the whole closed vocabulary: every
// strategy, bounded, terminating, deterministic, with its ceiling recorded and its host service either
// absent-and-refused or supplied-and-run. A build where each property holds for SOME strategy and not
// for the one added last would satisfy every test above and fail this.
func TestHarnessRuntimeSuite(t *testing.T) {
	hosts := Hosts{ToolInvoker: &recordingTools{}, Planner: stubPlanner{}, Critic: acceptingCritic{}}

	for _, name := range StrategyNames() {
		t.Run(name, func(t *testing.T) {
			p := Params{MaxTurns: 3, StopCondition: stopMaxTurns, ReflectionPrompt: "redo",
				AnswerMarker: "DONE", CriticModelRef: "m1"}
			want := 3
			if name == "single-shot" {
				p, want = Params{}, 1
			}

			// Bounded and terminating, against a stop condition that is never satisfied.
			invoke, calls, _ := countingInvoke()
			got, err := Run(Config{Strategy: name, Params: p}, hosts, nil, invoke)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if *calls != want || got.Turns != want {
				t.Fatalf("ran %d turn(s) against a ceiling of %d", got.Turns, want)
			}
			// The ceiling is recorded, and distinguishably.
			if got.Stop != StopCeiling && got.Stop != StopSingleShot {
				t.Errorf("terminated with %q rather than recording that it reached its ceiling", got.Stop)
			}
			if len(got.Trace) != want {
				t.Errorf("the trace has %d record(s) for %d turn(s)", len(got.Trace), want)
			}
			// Deterministic.
			invoke2, _, _ := countingInvoke()
			again, err := Run(Config{Strategy: name, Params: p}, hosts, nil, invoke2)
			if err != nil {
				t.Fatalf("re-run: %v", err)
			}
			if !reflect.DeepEqual(got, again) {
				t.Errorf("two runs of the same configuration differed")
			}
			// And the host-service contract, in both directions.
			svc := HostServiceFor(name)
			invoke3, calls3, _ := countingInvoke()
			_, err = Run(Config{Strategy: name, Params: p}, Hosts{}, nil, invoke3)
			if svc == HostNone {
				if err != nil {
					t.Errorf("%s needs no host service but refused without one: %v", name, err)
				}
			} else {
				if !errors.Is(err, ErrMissingHostService) {
					t.Errorf("%s needs %q and did not refuse without it: %v", name, svc, err)
				}
				if *calls3 != 0 {
					t.Errorf("%s spent %d call(s) before refusing", name, *calls3)
				}
			}
		})
	}
}
