package main

import (
	"testing"

	"github.com/heros-foreal/heros/internal/memory"
	"github.com/heros-foreal/heros/internal/planner"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/toolcontract"
)

// TestTheDaemonsWorkerIsFullyWired.
//
// 🔴 Regression fence for a bug that was invisible in every other test. The daemon assembled its worker
// inline and never set `Reviser`, so improvement runs assessed and stopped — and reported SUCCESS,
// because a scoped goal's completion criterion is satisfied by the assessment alone. The end-to-end test
// passed the whole time, because it wires its own worker: the tested path and the shipped path were
// different objects assembled by different code.
//
// This asserts on the object the daemon actually runs.
func TestTheDaemonsWorkerIsFullyWired(t *testing.T) {
	plans, err := planner.Default()
	if err != nil {
		t.Fatalf("planners: %v", err)
	}
	w := buildWorker(store.NewMemory(), toolcontract.NewRegistry(), memory.NewMem(), plans)

	if w.Store == nil {
		t.Error("no store: the worker cannot claim anything")
	}
	if w.Tools == nil {
		t.Error("no tool registry: every task would fail with `no tool registered`")
	}
	if w.Policy == nil {
		t.Error("no approval policy: effect-bearing tasks would run ungated")
	}
	if w.Clock == nil {
		t.Error("no clock: lease expiry cannot be evaluated")
	}
	if w.Lease <= 0 {
		t.Error("no lease duration: a claim would expire immediately and every task would be reclaimed")
	}
	if w.Episodes == nil {
		t.Error("no episode store: run history would be permanently empty")
	}
	// 🔴 The one that was missing.
	if w.Reviser == nil {
		t.Fatal("no reviser: an improvement run assesses, stops, and reports SUCCESS for doing so — " +
			"the plan never grows into proposals and nothing goes red")
	}
}
