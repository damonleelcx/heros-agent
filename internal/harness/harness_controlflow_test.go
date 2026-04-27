package harness

import "testing"

func TestBuildExecutionPlanIncludesDependenciesAndPriorities(t *testing.T) {
	p := buildExecutionPlan("ship feature", []string{
		"Implement API handler",
		"Write integration tests",
		"Document usage",
	})
	if len(p.Milestone) != 3 {
		t.Fatalf("milestones=%d want 3", len(p.Milestone))
	}
	if p.Milestone[1].Priority == 0 {
		t.Fatalf("expected inferred priority")
	}
	if len(p.Milestone[1].DependsOn) == 0 {
		t.Fatalf("expected tests to depend on implementation")
	}
}

func TestClaimNextTodoIndexRespectsDependencies(t *testing.T) {
	todos := []TodoItem{
		{ID: "todo-01", Title: "Implement API", Status: "pending", Priority: 2},
		{ID: "todo-02", Title: "Write tests", Status: "pending", DependsOn: []string{"todo-01"}, Priority: 3},
	}
	idx, _ := claimNextTodoIndex("analyst", todos)
	if idx != 0 {
		t.Fatalf("expected first claim to be todo-01, got idx=%d", idx)
	}
	todos[0].Status = "done"
	idx2, _ := claimNextTodoIndex("analyst", todos)
	if idx2 != 1 {
		t.Fatalf("expected second claim to be todo-02, got idx=%d", idx2)
	}
}

func TestTodoClaimScoreRoleAffinity(t *testing.T) {
	td := TodoItem{ID: "todo-01", Title: "Implement service code", Status: "pending", Priority: 2}
	coder := todoClaimScore("coder", td)
	writer := todoClaimScore("writer", td)
	if coder <= writer {
		t.Fatalf("expected coder affinity score > writer score (coder=%d writer=%d)", coder, writer)
	}
}

func TestBuildSubAgentSandbox(t *testing.T) {
	td := TodoItem{
		ID:     "todo-01",
		Title:  "Implement API and tests",
		Tools:  []string{"heros_shell", "heros_read_file"},
		Skills: []string{"core-reasoning"},
	}
	sb := buildSubAgentSandbox("coder", td)
	if sb.Role != "coder" || sb.TodoID != "todo-01" {
		t.Fatalf("unexpected sandbox identity: %#v", sb)
	}
	if len(sb.AllowedTools) == 0 || len(sb.Forbidden) == 0 {
		t.Fatalf("sandbox policy should not be empty: %#v", sb)
	}
}

func TestInferMissingFromCritique(t *testing.T) {
	out := inferMissingFromCritique("todo-02", "sub_critic", "Missing tests for edge cases.\nNeed follow-up docs.")
	if len(out) == 0 {
		t.Fatal("expected missing findings")
	}
	if out[0].TodoID != "todo-02" || out[0].Source != "sub_critic" {
		t.Fatalf("unexpected finding metadata: %#v", out[0])
	}
}

func TestComputePlanSnapshot(t *testing.T) {
	s := computePlanSnapshot(2, []TodoItem{
		{Status: "pending"},
		{Status: "in_progress"},
		{Status: "done"},
		{Status: "needs_followup"},
	})
	if s.Iteration != 2 || s.Total != 4 || s.Pending != 1 || s.InProgress != 1 || s.Done != 1 || s.NeedsFollow != 1 {
		t.Fatalf("unexpected snapshot: %#v", s)
	}
}

func TestAdjustDynamicIntensity(t *testing.T) {
	base := newDynamicIntensity(0.55)
	next := adjustDynamicIntensity(base, 2, 0.4, false, 1)
	if next.Mode != "high" || next.FollowUpMax < 4 {
		t.Fatalf("expected high intensity on repeated failure: %#v", next)
	}
	low := adjustDynamicIntensity(base, 1, 0.8, true, 0)
	if low.Mode != "low" || low.FollowUpMax != 2 {
		t.Fatalf("expected low intensity on strong pass: %#v", low)
	}
}
