package harness

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/promptlayer"
)

// Topology describes Leader-Follower + Team+Critic (Layer 3).
type Topology struct {
	Specialists      []string `json:"specialists"`
	CriticThreshold  float64  `json:"critic_threshold"`
	MaxCriticRetries int      `json:"max_critic_retries"`
	LeaderModel      string   `json:"leader_model"`
}

func DefaultTopology() Topology {
	return Topology{
		Specialists:      []string{"researcher", "coder", "writer", "analyst"},
		CriticThreshold:  0.55,
		MaxCriticRetries: 2,
	}
}

func LoadTopology(db *sql.DB) (Topology, error) {
	var js string
	err := db.QueryRow(`SELECT topology_json FROM harness_config WHERE id = 1`).Scan(&js)
	if err == sql.ErrNoRows {
		return DefaultTopology(), nil
	}
	if err != nil {
		return Topology{}, err
	}
	var t Topology
	if err := json.Unmarshal([]byte(js), &t); err != nil {
		return DefaultTopology(), nil
	}
	if t.CriticThreshold <= 0 {
		t.CriticThreshold = 0.55
	}
	if t.MaxCriticRetries <= 0 {
		t.MaxCriticRetries = 2
	}
	if len(t.Specialists) == 0 {
		t.Specialists = DefaultTopology().Specialists
	}
	return t, nil
}

func SaveTopology(db *sql.DB, t Topology) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO harness_config (id, topology_json, updated_at) VALUES (1, ?, datetime('now'))
		ON CONFLICT(id) DO UPDATE SET topology_json = excluded.topology_json, updated_at = datetime('now')`, string(b))
	return err
}

// SeedHarness inserts default topology if missing.
func SeedHarness(db *sql.DB) error {
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM harness_config WHERE id = 1`).Scan(&n)
	if n > 0 {
		return nil
	}
	return SaveTopology(db, DefaultTopology())
}

type LLMConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

type Orchestrator struct {
	DB      *sql.DB
	DataDir string
	LLM     LLMConfig
}

type RunResult struct {
	Goal            string             `json:"goal"`
	Plan            []string           `json:"plan"` // backward-compatible alias of todo titles
	Planning        ExecutionPlan      `json:"planning"`
	DependencyGraph DependencyGraph    `json:"dependency_graph"`
	Todos           []TodoItem         `json:"todos"`
	SubResults      map[string]string  `json:"sub_results"` // backward-compatible summary map
	SubAgentReports []SubAgentReport   `json:"sub_agent_reports"`
	CriticScores    map[string]float64 `json:"critic_scores"`
	IterationNotes  []string           `json:"iteration_notes"`
	Verification    VerificationResult `json:"verification"`
	GlobalCritique  []CritiqueAttempt  `json:"global_critique"`
	AgentVisibility []AgentVisibility  `json:"agent_visibility"`
	ClaimLog        []ClaimEvent       `json:"claim_log"`
	MissingFindings []MissingFinding   `json:"missing_findings"`
	PlanTracking    []PlanSnapshot     `json:"plan_tracking"`
	Notifications   []HarnessNotice    `json:"notifications"`
	IntensityTrace  []IntensityState   `json:"intensity_trace"`
	Evaluation      []EvaluationResult `json:"evaluation"`
	EarlyStopReason string             `json:"early_stop_reason,omitempty"`
	Final           string             `json:"final"`
	Retries         int                `json:"retries"`
	CompletedTodos  int                `json:"completed_todos"`
	TotalTodos      int                `json:"total_todos"`
}

type ExecutionPlan struct {
	Summary   string     `json:"summary"`
	Strategy  string     `json:"strategy"`
	Milestone []PlanStep `json:"milestone"`
}

type PlanStep struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	DependsOn []string `json:"depends_on,omitempty"`
	Priority  int      `json:"priority,omitempty"`
}

type DependencyGraph struct {
	Nodes []DependencyNode `json:"nodes"`
	Edges []DependencyEdge `json:"edges"`
}

type DependencyNode struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Priority int    `json:"priority,omitempty"`
}

type DependencyEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type,omitempty"` // blocks|soft
}

type TodoItem struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Status    string   `json:"status"` // pending|in_progress|done|needs_followup
	Assignee  string   `json:"assignee,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
	Priority  int      `json:"priority,omitempty"`
	Tools     []string `json:"tools,omitempty"`
	Skills    []string `json:"skills,omitempty"`
	Attempt   int      `json:"attempt"`
	Feedback  string   `json:"feedback,omitempty"`
	ParentID  string   `json:"parent_id,omitempty"`
	CreatedBy string   `json:"created_by,omitempty"` // leader|critic
}

type SubAgentReport struct {
	TodoID     string          `json:"todo_id"`
	Role       string          `json:"role"`
	Task       string          `json:"task"`
	Attempt    int             `json:"attempt"`
	Output     string          `json:"output"`
	Critique   string          `json:"critique"`
	Score      float64         `json:"score"`
	Status     string          `json:"status"` // accepted|needs_followup
	ToolsUsed  []string        `json:"tools_used"`
	SkillsUsed []string        `json:"skills_used"`
	MemoryUsed []string        `json:"memory_used"`
	Sandbox    SubAgentSandbox `json:"sandbox"`
}

type CritiqueAttempt struct {
	Attempt   int     `json:"attempt"`
	Score     float64 `json:"score"`
	Rationale string  `json:"rationale"`
}

type VerificationResult struct {
	Summary string   `json:"summary"`
	Checks  []string `json:"checks"`
	Passed  bool     `json:"passed"`
}

type AgentVisibility struct {
	Role       string   `json:"role"`
	ToolsUsed  []string `json:"tools_used"`
	SkillsUsed []string `json:"skills_used"`
	MemoryUsed []string `json:"memory_used"`
}

type ClaimEvent struct {
	Iteration int    `json:"iteration"`
	Role      string `json:"role"`
	TodoID    string `json:"todo_id"`
	Action    string `json:"action"` // claimed|skipped|forced
	Reason    string `json:"reason,omitempty"`
}

type SubAgentSandbox struct {
	Role            string   `json:"role"`
	TodoID          string   `json:"todo_id"`
	Scope           string   `json:"scope"`
	AllowedTools    []string `json:"allowed_tools"`
	AllowedSkills   []string `json:"allowed_skills"`
	Forbidden       []string `json:"forbidden"`
	RequireEvidence bool     `json:"require_evidence"`
}

type MissingFinding struct {
	TodoID   string `json:"todo_id,omitempty"`
	Source   string `json:"source"` // specialist_error|sub_critic|global_critic|verification
	Detail   string `json:"detail"`
	Severity string `json:"severity"` // low|medium|high
}

type PlanSnapshot struct {
	Iteration   int `json:"iteration"`
	Total       int `json:"total"`
	Pending     int `json:"pending"`
	InProgress  int `json:"in_progress"`
	Done        int `json:"done"`
	NeedsFollow int `json:"needs_followup"`
}

type HarnessNotice struct {
	Iteration int    `json:"iteration"`
	Level     string `json:"level"` // info|warn
	Title     string `json:"title"`
	Detail    string `json:"detail"`
}

type IntensityState struct {
	Iteration           int     `json:"iteration"`
	Mode                string  `json:"mode"` // low|normal|high
	Score               float64 `json:"score"`
	Threshold           float64 `json:"threshold"`
	FollowUpMax         int     `json:"follow_up_max"`
	MissingBoostApplied bool    `json:"missing_boost_applied"`
}

type EvaluationResult struct {
	Iteration  int                 `json:"iteration"`
	Metrics    EvaluationMetrics   `json:"metrics"`
	Decision   EarlyStopDecision   `json:"decision"`
	Successful bool                `json:"successful"`
}

type EvaluationMetrics struct {
	SuccessRate float64 `json:"success_rate"`
	Efficiency  float64 `json:"efficiency"`
	Cost        float64 `json:"cost"`
	Robustness  float64 `json:"robustness"`
	Safety      float64 `json:"safety"`
	Consistency float64 `json:"consistency"`
}

type EarlyStopDecision struct {
	Stop   bool   `json:"stop"`
	Reason string `json:"reason,omitempty"`
}

// Harness pipeline sections (1–5) for progress displays.
const (
	SectionPlanning  = 1 // planning, todo, goal
	SectionSubagents = 2 // sub-agents execute work
	SectionFeedback  = 3 // feedback, critic, score
	SectionRetry     = 4 // retry, redo
	SectionRepeat    = 5 // repeat until done
)

const (
	SectionLabelPlanning  = "planning"
	SectionLabelSubagents = "subagents"
	SectionLabelFeedback  = "feedback"
	SectionLabelRetry     = "retry"
	SectionLabelRepeat    = "repeat"
)

func progressSection(ev ProgressEvent, section int, label string, step, stepsTotal int) ProgressEvent {
	ev.Section = section
	ev.SectionLabel = label
	if step > 0 && stepsTotal > 0 {
		ev.SectionStep = step
		ev.SectionStepsTotal = stepsTotal
	}
	return ev
}

// ProgressEvent is a structured lifecycle event emitted while harness execution is in-flight.
type ProgressEvent struct {
	Phase             string   `json:"phase"`
	Stage             string   `json:"stage"`
	Detail            string   `json:"detail,omitempty"`
	Index             int      `json:"index,omitempty"`
	Total             int      `json:"total,omitempty"`
	Role              string   `json:"role,omitempty"`
	TodoID            string   `json:"todo_id,omitempty"`
	Status            string   `json:"status,omitempty"`
	Tools             []string `json:"tools,omitempty"`
	Skills            []string `json:"skills,omitempty"`
	Memory            []string `json:"memory,omitempty"`
	Attempt           int      `json:"attempt,omitempty"`
	Score             float64  `json:"score,omitempty"`
	Threshold         float64  `json:"threshold,omitempty"`
	Section           int      `json:"section,omitempty"`
	SectionLabel      string   `json:"section_label,omitempty"`
	SectionStep       int      `json:"section_step,omitempty"`
	SectionStepsTotal int      `json:"section_steps_total,omitempty"`
	StageIndex        int      `json:"stage_index,omitempty"`
	StageTotal        int      `json:"stage_total,omitempty"`
	IterationSummary  string   `json:"iteration_summary,omitempty"`
	Offset            int64    `json:"offset,omitempty"`
}

const (
	HarnessStagePlanning = 1
	HarnessStageAssign   = 2
	HarnessStageFeedback = 3
	HarnessStageRetry    = 4
	HarnessStageRepeat   = 5
	HarnessStageSummary  = 6
)

type KafkaStageMessage struct {
	RunID     string `json:"run_id"`
	Iteration int    `json:"iteration"`
	Stage     int    `json:"stage"`
	Phase     string `json:"phase"`
	Status    string `json:"status"`
	Summary   string `json:"summary,omitempty"`
	Offset    int64  `json:"offset"`
}

type KafkaStageBus interface {
	Publish(KafkaStageMessage) error
}

type InMemoryKafkaStageBus struct {
	mu      sync.Mutex
	offset  int64
	records []KafkaStageMessage
}

func (b *InMemoryKafkaStageBus) Publish(m KafkaStageMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.offset++
	m.Offset = b.offset
	b.records = append(b.records, m)
	return nil
}

type stageController struct {
	bus       KafkaStageBus
	runID     string
	iteration int
	stage     int
	offset    int64
}

func newStageController() *stageController {
	return &stageController{
		bus:   &InMemoryKafkaStageBus{},
		runID: fmt.Sprintf("run-%d", timeNowUnixNano()),
	}
}

func (c *stageController) transition(iteration, stage int, phase, status, summary string) (int64, error) {
	if iteration <= 0 {
		return 0, fmt.Errorf("invalid iteration %d", iteration)
	}
	if stage < HarnessStagePlanning || stage > HarnessStageSummary {
		return 0, fmt.Errorf("invalid stage %d", stage)
	}
	if c.iteration == 0 {
		if stage != HarnessStagePlanning {
			return 0, fmt.Errorf("first stage must be planning")
		}
	} else {
		if iteration < c.iteration {
			return 0, fmt.Errorf("iteration moved backwards")
		}
		if iteration == c.iteration {
			if !validStageTransition(c.stage, stage) {
				return 0, fmt.Errorf("invalid stage transition %d->%d", c.stage, stage)
			}
		} else {
			// New iteration can start only after repeat stage.
			if c.stage != HarnessStageRepeat || stage != HarnessStageAssign {
				return 0, fmt.Errorf("invalid iteration transition %d(stage=%d)->%d(stage=%d)", c.iteration, c.stage, iteration, stage)
			}
		}
	}
	c.iteration = iteration
	c.stage = stage
	msg := KafkaStageMessage{
		RunID:     c.runID,
		Iteration: iteration,
		Stage:     stage,
		Phase:     strings.TrimSpace(phase),
		Status:    strings.TrimSpace(status),
		Summary:   strings.TrimSpace(summary),
	}
	if err := c.bus.Publish(msg); err != nil {
		return 0, err
	}
	c.offset++
	return c.offset, nil
}

func validStageTransition(prev, next int) bool {
	switch prev {
	case HarnessStagePlanning:
		return next == HarnessStageAssign
	case HarnessStageAssign:
		return next == HarnessStageFeedback
	case HarnessStageFeedback:
		return next == HarnessStageSummary
	case HarnessStageSummary:
		return next == HarnessStageRetry
	case HarnessStageRetry:
		return next == HarnessStageRepeat
	case HarnessStageRepeat:
		return next == HarnessStageAssign
	default:
		return false
	}
}

func timeNowUnixNano() int64 {
	return time.Now().UnixNano()
}

// Run executes leader decomposition, parallel specialist stubs, critic loop.
func (o *Orchestrator) Run(ctx context.Context, goal string) (*RunResult, error) {
	return o.RunWithProgress(ctx, goal, nil)
}

// RunWithProgress executes leader decomposition, specialist passes, and critic/retry loop with optional progress callbacks.
func (o *Orchestrator) RunWithProgress(ctx context.Context, goal string, onProgress func(ProgressEvent)) (*RunResult, error) {
	stageCtl := newStageController()
	emit := func(ev ProgressEvent) {
		if onProgress != nil {
			onProgress(ev)
		}
	}
	emitStage := func(iteration, stage int, ev ProgressEvent, status, summary string) error {
		off, err := stageCtl.transition(iteration, stage, ev.Phase, status, summary)
		if err != nil {
			return err
		}
		ev.StageIndex = stage
		ev.StageTotal = HarnessStageSummary
		ev.Offset = off
		emit(ev)
		return nil
	}
	topo, err := LoadTopology(o.DB)
	if err != nil {
		return nil, err
	}
	maxPasses := topo.MaxCriticRetries + 1
	if maxPasses < 1 {
		maxPasses = 1
	}
	if maxPasses < 8 {
		maxPasses = 8
	}
	sys, _, err := promptlayer.LoadActiveSystemPrompt(o.DB, o.DataDir)
	if err != nil {
		sys = ""
	}

	g := strings.TrimSpace(goal)
	if g == "" {
		g = "(empty goal)"
	}
	if err := emitStage(1, HarnessStagePlanning, progressSection(ProgressEvent{Phase: "harness", Stage: "start", Detail: g}, SectionPlanning, SectionLabelPlanning, 1, 3), "start", "planning goal and todo decomposition"); err != nil {
		return nil, err
	}

	// Research-first: run a web-search grounding pass before todo generation.
	emit(progressSection(ProgressEvent{Phase: "research", Stage: "start", Detail: "web search before generating todos"}, SectionPlanning, SectionLabelPlanning, 2, 3))
	researchBrief, rerr := o.preTodoWebResearch(ctx, sys, goal)
	if rerr != nil {
		researchBrief = ""
	}
	emit(progressSection(ProgressEvent{Phase: "research", Stage: "end", Detail: "web research grounding completed"}, SectionPlanning, SectionLabelPlanning, 2, 3))

	// Leader: define explicit todo list tied to the goal and web-grounded context.
	emit(progressSection(ProgressEvent{Phase: "leader", Stage: "start", Detail: "defining goal-aligned todo list"}, SectionPlanning, SectionLabelPlanning, 2, 3))
	planGoal := goal
	if strings.TrimSpace(researchBrief) != "" {
		planGoal = goal + "\n\nWeb research briefing:\n" + researchBrief
	}
	plan, err := o.leaderPlan(ctx, sys, topo, planGoal)
	if err != nil {
		plan = []string{goal}
	}
	emit(progressSection(ProgressEvent{Phase: "leader", Stage: "end", Total: len(plan), Detail: "todo list ready"}, SectionPlanning, SectionLabelPlanning, 2, 3))
	planning := buildExecutionPlan(goal, plan)
	depGraph := planningToDependencyGraph(planning)

	todos := make([]TodoItem, 0, len(planning.Milestone))
	for i, step := range planning.Milestone {
		todos = append(todos, TodoItem{
			ID:        step.ID,
			Title:     step.Title,
			Status:    "pending",
			Attempt:   1,
			CreatedBy: "leader",
			DependsOn: append([]string{}, step.DependsOn...),
			Priority:  step.Priority,
			Tools:     inferToolsForTodo(step.Title),
			Skills:    inferSkillsForTodo(step.Title),
		})
		emit(progressSection(ProgressEvent{
			Phase:  "todo",
			Stage:  "created",
			Index:  i + 1,
			Total:  len(planning.Milestone),
			TodoID: step.ID,
			Detail: fmt.Sprintf("%s | deps=%s tools=%s skills=%s", step.Title, strings.Join(step.DependsOn, ","), strings.Join(inferToolsForTodo(step.Title), ","), strings.Join(inferSkillsForTodo(step.Title), ",")),
			Status: "pending",
		}, SectionPlanning, SectionLabelPlanning, 3, 3))
	}

	res := &RunResult{
		Goal:            goal,
		Plan:            plan,
		Planning:        planning,
		DependencyGraph: depGraph,
		Todos:           todos,
		SubResults:      map[string]string{},
		CriticScores:    map[string]float64{},
		SubAgentReports: []SubAgentReport{},
		ClaimLog:        []ClaimEvent{},
		MissingFindings: []MissingFinding{},
		PlanTracking:    []PlanSnapshot{},
		Notifications:   []HarnessNotice{},
		IntensityTrace:  []IntensityState{},
		Evaluation:      []EvaluationResult{},
	}

	retries := 0
	latestByTodo := map[string]string{}
	dyn := newDynamicIntensity(topo.CriticThreshold)
	for {
		iteration := retries + 1
		snap := computePlanSnapshot(iteration, res.Todos)
		res.PlanTracking = append(res.PlanTracking, snap)
		emit(progressSection(ProgressEvent{
			Phase:  "plan",
			Stage:  "tracking",
			Detail: fmt.Sprintf("plan tracking: total=%d done=%d pending=%d in_progress=%d needs_followup=%d", snap.Total, snap.Done, snap.Pending, snap.InProgress, snap.NeedsFollow),
			Index:  snap.Done,
			Total:  snap.Total,
		}, SectionPlanning, SectionLabelPlanning, 3, 3))
		note := HarnessNotice{
			Iteration: iteration,
			Level:     "info",
			Title:     "Background update",
			Detail:    fmt.Sprintf("Pass %d/%d in progress (%d done of %d todos)", iteration, maxPasses, snap.Done, snap.Total),
		}
		res.Notifications = append(res.Notifications, note)
		emit(progressSection(ProgressEvent{
			Phase:  "notify",
			Stage:  "background",
			Detail: note.Detail,
			Index:  iteration,
			Total:  maxPasses,
		}, SectionRepeat, SectionLabelRepeat, iteration, maxPasses))
		if err := emitStage(iteration, HarnessStageAssign, progressSection(ProgressEvent{
			Phase:   "todo",
			Stage:   "iteration_start",
			Attempt: retries + 1,
			Detail:  "assigning todos to sub-agents",
			Index:   retries + 1,
			Total:   maxPasses,
		}, SectionSubagents, SectionLabelSubagents, 1, 1), "start", "assigning todos to sub-agents"); err != nil {
			return nil, err
		}
		emit(progressSection(ProgressEvent{
			Phase:   "todo",
			Stage:   "iteration_start",
			Attempt: retries + 1,
			Detail:  "distributing todo items to sub-agents",
			Index:   retries + 1,
			Total:   maxPasses,
		}, SectionRepeat, SectionLabelRepeat, retries+1, maxPasses))
		totalActive := countOpenTodos(res.Todos)
		doneThisPass := 0
		for doneThisPass < totalActive {
			progressMade := false
			for _, spec := range topo.Specialists {
				claimIdx, claimReason := claimNextTodoIndex(spec, res.Todos)
				if claimIdx < 0 {
					res.ClaimLog = append(res.ClaimLog, ClaimEvent{Iteration: iteration, Role: spec, Action: "skipped", Reason: "no claimable todo"})
					continue
				}
				td := &res.Todos[claimIdx]
				res.ClaimLog = append(res.ClaimLog, ClaimEvent{Iteration: iteration, Role: spec, TodoID: td.ID, Action: "claimed", Reason: claimReason})
				progressMade = true
				td.Assignee = spec
				td.Attempt = retries + 1
				td.Status = "in_progress"
				vis := agentVisibilityFor(spec)
				emit(progressSection(ProgressEvent{
					Phase:  "todo",
					Stage:  "claimed",
					Role:   spec,
					TodoID: td.ID,
					Detail: claimReason,
					Status: td.Status,
				}, SectionSubagents, SectionLabelSubagents, 0, 0))
				emit(progressSection(ProgressEvent{
					Phase:  "specialist",
					Stage:  "start",
					Index:  doneThisPass + 1,
					Total:  totalActive,
					Role:   spec,
					TodoID: td.ID,
					Detail: td.Title,
					Status: td.Status,
					Tools:  vis.ToolsUsed,
					Skills: vis.SkillsUsed,
					Memory: vis.MemoryUsed,
				}, SectionSubagents, SectionLabelSubagents, 0, 0))

				sbx := buildSubAgentSandbox(spec, *td)
				out, err := o.specialist(ctx, sys, sbx, td.Title)
				if err != nil {
					out = fmt.Sprintf("(specialist %s error: %v)", spec, err)
					res.MissingFindings = append(res.MissingFindings, MissingFinding{
						TodoID:   td.ID,
						Source:   "specialist_error",
						Detail:   truncate(err.Error(), 220),
						Severity: "high",
					})
				}
				emit(progressSection(ProgressEvent{
					Phase:  "specialist",
					Stage:  "end",
					Index:  doneThisPass + 1,
					Total:  totalActive,
					Role:   spec,
					TodoID: td.ID,
					Status: "output_ready",
					Tools:  vis.ToolsUsed,
					Skills: vis.SkillsUsed,
					Memory: vis.MemoryUsed,
					Detail: "sub-agent output ready",
				}, SectionSubagents, SectionLabelSubagents, 0, 0))

				emit(progressSection(ProgressEvent{
					Phase:  "feedback",
					Stage:  "start",
					Index:  doneThisPass + 1,
					Total:  totalActive,
					Role:   spec,
					TodoID: td.ID,
					Detail: "critic scoring sub-agent output",
					Tools:  vis.ToolsUsed,
					Skills: vis.SkillsUsed,
					Memory: vis.MemoryUsed,
				}, SectionFeedback, SectionLabelFeedback, 0, 0))
				score, fb, err := o.criticSubAgent(ctx, sys, goal, td.Title, out)
				if err != nil {
					score, fb = 0.5, err.Error()
				}
				status := "accepted"
				td.Status = "done"
				td.Feedback = ""
				if score < dyn.Threshold {
					status = "needs_followup"
					td.Status = "needs_followup"
					td.Feedback = fb
					res.MissingFindings = append(res.MissingFindings, inferMissingFromCritique(td.ID, "sub_critic", fb)...)
				}
				latestByTodo[td.ID] = out
				res.SubResults[fmt.Sprintf("%s:%s", spec, td.ID)] = out
				res.SubAgentReports = append(res.SubAgentReports, SubAgentReport{
					TodoID:     td.ID,
					Role:       spec,
					Task:       td.Title,
					Attempt:    retries + 1,
					Output:     out,
					Critique:   fb,
					Score:      score,
					Status:     status,
					ToolsUsed:  vis.ToolsUsed,
					SkillsUsed: vis.SkillsUsed,
					MemoryUsed: vis.MemoryUsed,
					Sandbox:    sbx,
				})
				emit(progressSection(ProgressEvent{
					Phase:     "feedback",
					Stage:     "end",
					Index:     doneThisPass + 1,
					Total:     totalActive,
					Role:      spec,
					TodoID:    td.ID,
					Status:    td.Status,
					Score:     score,
					Threshold: dyn.Threshold,
					Tools:     vis.ToolsUsed,
					Skills:    vis.SkillsUsed,
					Memory:    vis.MemoryUsed,
					Detail:    fb,
				}, SectionFeedback, SectionLabelFeedback, 0, 0))
				doneThisPass++
				if doneThisPass >= totalActive {
					break
				}
			}
			if !progressMade {
				for i := range res.Todos {
					if res.Todos[i].Status == "pending" || res.Todos[i].Status == "needs_followup" {
						res.ClaimLog = append(res.ClaimLog, ClaimEvent{
							Iteration: iteration,
							Role:      topo.Specialists[0],
							TodoID:    res.Todos[i].ID,
							Action:    "forced",
							Reason:    "dependency deadlock fallback",
						})
						res.Todos[i].DependsOn = nil
						break
					}
				}
			}
		}
		emit(progressSection(ProgressEvent{
			Phase:   "todo",
			Stage:   "iteration_end",
			Attempt: retries + 1,
			Detail:  "sub-agents finished assigned todos",
			Index:   retries + 1,
			Total:   maxPasses,
		}, SectionRepeat, SectionLabelRepeat, retries+1, maxPasses))
		if err := emitStage(iteration, HarnessStageFeedback, progressSection(ProgressEvent{
			Phase:   "feedback",
			Stage:   "iteration_scored",
			Attempt: iteration,
			Detail:  "feedback/critic scoring complete",
			Index:   iteration,
			Total:   maxPasses,
		}, SectionFeedback, SectionLabelFeedback, 1, 1), "ok", "feedback and critic scoring complete"); err != nil {
			return nil, err
		}

		merged := mergeTodoResults(res.Todos, latestByTodo)
		emit(progressSection(ProgressEvent{
			Phase:   "critic",
			Stage:   "start",
			Attempt: retries + 1,
			Detail:  "scoring merged draft",
		}, SectionFeedback, SectionLabelFeedback, 1, 2))
		score, rationale, err := o.critic(ctx, sys, topo, goal, merged)
		if err != nil {
			score = 0.6
			rationale = err.Error()
		}
		res.CriticScores[fmt.Sprintf("attempt_%d", retries)] = score
		res.GlobalCritique = append(res.GlobalCritique, CritiqueAttempt{Attempt: retries + 1, Score: score, Rationale: rationale})
		if score < dyn.Threshold {
			res.MissingFindings = append(res.MissingFindings, inferMissingFromCritique("", "global_critic", rationale)...)
		}
		emit(progressSection(ProgressEvent{
			Phase:     "critic",
			Stage:     "end",
			Attempt:   retries + 1,
			Score:     score,
			Threshold: dyn.Threshold,
			Detail:    truncate(strings.TrimSpace(rationale), 240),
		}, SectionFeedback, SectionLabelFeedback, 1, 2))
		emit(progressSection(ProgressEvent{
			Phase:   "verify",
			Stage:   "start",
			Attempt: retries + 1,
			Detail:  "test and preview",
		}, SectionFeedback, SectionLabelFeedback, 2, 2))
		ver, verr := o.testAndPreview(ctx, sys, goal, merged)
		if verr != nil {
			ver = VerificationResult{Summary: verr.Error(), Checks: []string{"preview unavailable"}, Passed: false}
		}
		res.Verification = ver
		if !ver.Passed {
			res.MissingFindings = append(res.MissingFindings, inferMissingFromVerification(ver)...)
		}
		if ver.Passed {
			emit(progressSection(ProgressEvent{Phase: "verify", Stage: "end", Attempt: retries + 1, Status: "passed", Detail: ver.Summary}, SectionFeedback, SectionLabelFeedback, 2, 2))
		} else {
			emit(progressSection(ProgressEvent{Phase: "verify", Stage: "end", Attempt: retries + 1, Status: "failed", Detail: ver.Summary}, SectionFeedback, SectionLabelFeedback, 2, 2))
		}
		verifyStatus := "failed"
		if ver.Passed {
			verifyStatus = "passed"
		}
		dyn = adjustDynamicIntensity(dyn, iteration, score, ver.Passed, len(res.MissingFindings))
		res.IntensityTrace = append(res.IntensityTrace, IntensityState{
			Iteration:           iteration,
			Mode:                dyn.Mode,
			Score:               score,
			Threshold:           dyn.Threshold,
			FollowUpMax:         dyn.FollowUpMax,
			MissingBoostApplied: dyn.MissingBoostApplied,
		})
		iterSummary := fmt.Sprintf("iteration %d summary: score=%.2f threshold=%.2f verify=%s open_todos=%d intensity=%s", iteration, score, dyn.Threshold, verifyStatus, countOpenTodos(res.Todos), dyn.Mode)
		eval := evaluateIteration(iteration, score, dyn.Threshold, ver, res.Todos, res.SubAgentReports, res.MissingFindings, retries+1)
		res.Evaluation = append(res.Evaluation, eval)
		if eval.Decision.Stop {
			res.EarlyStopReason = eval.Decision.Reason
			res.Final = merged + "\n\n[early stop] " + eval.Decision.Reason
			res.Retries = retries
			res.CompletedTodos, res.TotalTodos = summarizeTodos(res.Todos)
			res.AgentVisibility = aggregateVisibility(res.SubAgentReports)
			return res, nil
		}
		res.IterationNotes = append(res.IterationNotes, iterSummary)
		if err := emitStage(iteration, HarnessStageSummary, progressSection(ProgressEvent{
			Phase:            "summary",
			Stage:            "iteration_end",
			Attempt:          iteration,
			Detail:           iterSummary,
			IterationSummary: iterSummary,
			Index:            iteration,
			Total:            maxPasses,
		}, SectionRepeat, SectionLabelRepeat, iteration, maxPasses), "ok", iterSummary); err != nil {
			return nil, err
		}

		if score >= dyn.Threshold && ver.Passed {
			res.Final = merged + "\n\n[critic score=" + fmt.Sprintf("%.2f", score) + "] " + rationale
			res.Retries = retries
			res.CompletedTodos, res.TotalTodos = summarizeTodos(res.Todos)
			res.AgentVisibility = aggregateVisibility(res.SubAgentReports)
			emit(progressSection(ProgressEvent{
				Phase:     "harness",
				Stage:     "end",
				Attempt:   retries + 1,
				Score:     score,
				Threshold: dyn.Threshold,
				Detail:    "done",
				Index:     retries + 1,
				Total:     maxPasses,
			}, SectionRepeat, SectionLabelRepeat, retries+1, maxPasses))
			return res, nil
		}
		if retries >= maxPasses-1 {
			return nil, fmt.Errorf("harness did not reach successful results after %d iterations", maxPasses)
		}
		if err := emitStage(iteration, HarnessStageRetry, progressSection(ProgressEvent{
			Phase:     "critic",
			Stage:     "retry",
			Attempt:   retries + 1,
			Score:     score,
			Threshold: dyn.Threshold,
			Detail:    "retry/redo due to score or verification failure",
		}, SectionRetry, SectionLabelRetry, 1, 2), "retry", "retry and redo triggered"); err != nil {
			return nil, err
		}
		emit(progressSection(ProgressEvent{
			Phase:     "critic",
			Stage:     "retry",
			Attempt:   retries + 1,
			Score:     score,
			Threshold: dyn.Threshold,
			Detail:    "below threshold; redo with follow-up todos",
		}, SectionRetry, SectionLabelRetry, 1, 2))
		emit(progressSection(ProgressEvent{
			Phase:   "refine",
			Stage:   "start",
			Attempt: retries + 1,
			Detail:  "creating follow-up todos from critique gaps",
		}, SectionRetry, SectionLabelRetry, 2, 2))
		newTodos, err := o.followUpTodos(ctx, sys, goal, rationale, res.Todos, retries+2, res.MissingFindings, dyn.FollowUpMax)
		if err != nil || len(newTodos) == 0 {
			newTodos = []TodoItem{{
				ID:        fmt.Sprintf("todo-followup-%02d", retries+1),
				Title:     "Address critique gaps: " + truncate(rationale, 180),
				Status:    "pending",
				Attempt:   retries + 2,
				CreatedBy: "critic",
				Priority:  inferPriorityForTodo(rationale),
			}}
		}
		for j, td := range newTodos {
			res.Plan = append(res.Plan, td.Title)
			res.Todos = append(res.Todos, td)
			res.Planning.Milestone = append(res.Planning.Milestone, PlanStep{
				ID:        td.ID,
				Title:     td.Title,
				DependsOn: append([]string{}, td.DependsOn...),
				Priority:  td.Priority,
			})
			res.DependencyGraph.Nodes = append(res.DependencyGraph.Nodes, DependencyNode{
				ID:       td.ID,
				Title:    td.Title,
				Priority: td.Priority,
			})
			for _, dep := range td.DependsOn {
				res.DependencyGraph.Edges = append(res.DependencyGraph.Edges, DependencyEdge{
					From: dep,
					To:   td.ID,
					Type: "blocks",
				})
			}
			res.IterationNotes = append(res.IterationNotes, fmt.Sprintf("attempt %d added todo %s: %s", retries+1, td.ID, td.Title))
			emit(progressSection(ProgressEvent{
				Phase:   "todo",
				Stage:   "created",
				Attempt: retries + 2,
				TodoID:  td.ID,
				Detail:  td.Title,
				Status:  td.Status,
				Index:   j + 1,
				Total:   len(newTodos),
			}, SectionRetry, SectionLabelRetry, 2, 2))
		}
		emit(progressSection(ProgressEvent{Phase: "refine", Stage: "end", Attempt: retries + 1}, SectionRetry, SectionLabelRetry, 2, 2))
		if err := emitStage(iteration, HarnessStageRepeat, progressSection(ProgressEvent{
			Phase:   "todo",
			Stage:   "repeat",
			Attempt: iteration,
			Detail:  "repeat loop with new/follow-up todos",
			Index:   iteration,
			Total:   maxPasses,
		}, SectionRepeat, SectionLabelRepeat, iteration, maxPasses), "repeat", "repeat until success"); err != nil {
			return nil, err
		}
		retries++
	}
}

func mergeTodoResults(todos []TodoItem, latestByTodo map[string]string) string {
	var b strings.Builder
	for _, td := range todos {
		v := strings.TrimSpace(latestByTodo[td.ID])
		if v == "" {
			continue
		}
		b.WriteString("### ")
		b.WriteString(td.ID)
		b.WriteString(" [")
		b.WriteString(td.Assignee)
		b.WriteString("] ")
		b.WriteString(td.Title)
		b.WriteString("\n")
		b.WriteString(v)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

func inferToolsForTodo(title string) []string {
	t := strings.ToLower(strings.TrimSpace(title))
	tools := []string{"heros_shell"}
	if strings.Contains(t, "file") || strings.Contains(t, "edit") || strings.Contains(t, "write") || strings.Contains(t, "create") {
		tools = append(tools, "heros_read_file", "heros_write_file")
	}
	if strings.Contains(t, "test") || strings.Contains(t, "build") {
		tools = append(tools, "heros_shell")
	}
	if strings.Contains(t, "memory") || strings.Contains(t, "recall") {
		tools = append(tools, "heros_memory_search")
	}
	return dedupeStrings(tools)
}

func inferSkillsForTodo(title string) []string {
	t := strings.ToLower(strings.TrimSpace(title))
	var skills []string
	if strings.Contains(t, "test") || strings.Contains(t, "failing") {
		skills = append(skills, "test-driven-development")
	}
	if strings.Contains(t, "plan") || strings.Contains(t, "architecture") {
		skills = append(skills, "writing-plans")
	}
	if len(skills) == 0 {
		skills = append(skills, "core-reasoning")
	}
	return dedupeStrings(skills)
}

func buildExecutionPlan(goal string, plan []string) ExecutionPlan {
	steps := make([]PlanStep, 0, len(plan))
	for i, title := range plan {
		id := fmt.Sprintf("todo-%02d", i+1)
		steps = append(steps, PlanStep{
			ID:       id,
			Title:    strings.TrimSpace(title),
			Priority: inferPriorityForTodo(title),
		})
	}
	edges := inferDependencyEdges(steps)
	depByTo := map[string][]string{}
	for _, e := range edges {
		if e.Type == "blocks" {
			depByTo[e.To] = append(depByTo[e.To], e.From)
		}
	}
	for i := range steps {
		steps[i].DependsOn = dedupeStrings(depByTo[steps[i].ID])
	}
	return ExecutionPlan{
		Summary:   truncate(strings.TrimSpace(goal), 240),
		Strategy:  "dependency-aware decomposition with autonomous claiming",
		Milestone: steps,
	}
}

func planningToDependencyGraph(p ExecutionPlan) DependencyGraph {
	nodes := make([]DependencyNode, 0, len(p.Milestone))
	for _, s := range p.Milestone {
		nodes = append(nodes, DependencyNode{
			ID:       s.ID,
			Title:    s.Title,
			Priority: s.Priority,
		})
	}
	return DependencyGraph{
		Nodes: nodes,
		Edges: inferDependencyEdges(p.Milestone),
	}
}

func inferDependencyEdges(steps []PlanStep) []DependencyEdge {
	edges := []DependencyEdge{}
	for i := range steps {
		if i > 0 {
			edges = append(edges, DependencyEdge{
				From: steps[i-1].ID,
				To:   steps[i].ID,
				Type: "soft",
			})
		}
	}
	for i := range steps {
		ti := strings.ToLower(steps[i].Title)
		for j := range steps {
			if i == j {
				continue
			}
			tj := strings.ToLower(steps[j].Title)
			if dependsByKeyword(ti, tj) {
				edges = append(edges, DependencyEdge{
					From: steps[j].ID,
					To:   steps[i].ID,
					Type: "blocks",
				})
			}
		}
	}
	return dedupeEdges(edges)
}

func dependsByKeyword(target, candidateDep string) bool {
	if strings.Contains(target, "test") && (strings.Contains(candidateDep, "build") || strings.Contains(candidateDep, "implement") || strings.Contains(candidateDep, "code")) {
		return true
	}
	if strings.Contains(target, "deploy") && (strings.Contains(candidateDep, "build") || strings.Contains(candidateDep, "package")) {
		return true
	}
	if strings.Contains(target, "document") && (strings.Contains(candidateDep, "implement") || strings.Contains(candidateDep, "finalize")) {
		return true
	}
	return false
}

func dedupeEdges(in []DependencyEdge) []DependencyEdge {
	seen := map[string]bool{}
	out := make([]DependencyEdge, 0, len(in))
	for _, e := range in {
		if strings.TrimSpace(e.From) == "" || strings.TrimSpace(e.To) == "" || e.From == e.To {
			continue
		}
		k := e.From + "->" + e.To + ":" + e.Type
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, e)
	}
	return out
}

func inferPriorityForTodo(title string) int {
	t := strings.ToLower(strings.TrimSpace(title))
	switch {
	case strings.Contains(t, "blocker"), strings.Contains(t, "critical"), strings.Contains(t, "urgent"):
		return 1
	case strings.Contains(t, "test"), strings.Contains(t, "verify"), strings.Contains(t, "qa"):
		return 3
	default:
		return 2
	}
}

func claimNextTodoIndex(role string, todos []TodoItem) (int, string) {
	bestIdx := -1
	bestScore := -9999
	bestReason := ""
	for i := range todos {
		td := todos[i]
		if td.Status != "pending" && td.Status != "needs_followup" {
			continue
		}
		if !todoDependenciesSatisfied(td, todos) {
			continue
		}
		score := todoClaimScore(role, td)
		if score > bestScore {
			bestIdx = i
			bestScore = score
			bestReason = fmt.Sprintf("eligible with role-score=%d priority=%d", score, td.Priority)
		}
	}
	return bestIdx, bestReason
}

func todoDependenciesSatisfied(td TodoItem, todos []TodoItem) bool {
	if len(td.DependsOn) == 0 {
		return true
	}
	byID := map[string]TodoItem{}
	for _, t := range todos {
		byID[t.ID] = t
	}
	for _, dep := range td.DependsOn {
		d, ok := byID[dep]
		if !ok || d.Status != "done" {
			return false
		}
	}
	return true
}

func todoClaimScore(role string, td TodoItem) int {
	r := strings.ToLower(strings.TrimSpace(role))
	t := strings.ToLower(strings.TrimSpace(td.Title))
	score := 100 - (td.Priority * 10)
	switch {
	case strings.Contains(r, "coder") && (strings.Contains(t, "implement") || strings.Contains(t, "code") || strings.Contains(t, "build")):
		score += 25
	case strings.Contains(r, "research") && (strings.Contains(t, "research") || strings.Contains(t, "analy")):
		score += 25
	case strings.Contains(r, "writer") && (strings.Contains(t, "document") || strings.Contains(t, "write")):
		score += 25
	case strings.Contains(r, "analyst") && (strings.Contains(t, "test") || strings.Contains(t, "verify") || strings.Contains(t, "measure")):
		score += 25
	}
	if td.Status == "needs_followup" {
		score += 15
	}
	return score
}

func buildSubAgentSandbox(role string, td TodoItem) SubAgentSandbox {
	return SubAgentSandbox{
		Role:            strings.TrimSpace(role),
		TodoID:          strings.TrimSpace(td.ID),
		Scope:           truncate(strings.TrimSpace(td.Title), 180),
		AllowedTools:    dedupeStrings(append([]string{}, td.Tools...)),
		AllowedSkills:   dedupeStrings(append([]string{}, td.Skills...)),
		Forbidden:       []string{"destructive_actions_without_review", "out_of_scope_changes", "secret_exfiltration"},
		RequireEvidence: true,
	}
}

func inferMissingFromCritique(todoID, source, critique string) []MissingFinding {
	lines := splitSignalLines(critique)
	out := make([]MissingFinding, 0, len(lines))
	for _, l := range lines {
		if !missingSignalLine(l) {
			continue
		}
		out = append(out, MissingFinding{
			TodoID:   strings.TrimSpace(todoID),
			Source:   strings.TrimSpace(source),
			Detail:   truncate(strings.TrimSpace(l), 200),
			Severity: "medium",
		})
	}
	if len(out) == 0 && strings.TrimSpace(critique) != "" {
		out = append(out, MissingFinding{
			TodoID:   strings.TrimSpace(todoID),
			Source:   strings.TrimSpace(source),
			Detail:   truncate(strings.TrimSpace(critique), 200),
			Severity: "medium",
		})
	}
	return out
}

func inferMissingFromVerification(v VerificationResult) []MissingFinding {
	if v.Passed {
		return nil
	}
	out := []MissingFinding{}
	for _, c := range v.Checks {
		if missingSignalLine(c) {
			out = append(out, MissingFinding{Source: "verification", Detail: truncate(strings.TrimSpace(c), 200), Severity: "high"})
		}
	}
	if len(out) == 0 && strings.TrimSpace(v.Summary) != "" {
		out = append(out, MissingFinding{Source: "verification", Detail: truncate(strings.TrimSpace(v.Summary), 200), Severity: "high"})
	}
	return out
}

func splitSignalLines(s string) []string {
	raw := strings.Split(strings.ReplaceAll(strings.TrimSpace(s), "\r", ""), "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		l = strings.TrimSpace(strings.TrimLeft(l, "-*0123456789. "))
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func missingSignalLine(s string) bool {
	l := strings.ToLower(strings.TrimSpace(s))
	if l == "" {
		return false
	}
	for _, k := range []string{"missing", "not covered", "gap", "lack", "needs", "todo", "follow-up", "incomplete", "failed"} {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

type dynamicIntensity struct {
	Mode                string
	Threshold           float64
	FollowUpMax         int
	MissingBoostApplied bool
}

func newDynamicIntensity(baseThreshold float64) dynamicIntensity {
	if baseThreshold <= 0 {
		baseThreshold = 0.55
	}
	return dynamicIntensity{
		Mode:        "normal",
		Threshold:   baseThreshold,
		FollowUpMax: 3,
	}
}

func adjustDynamicIntensity(cur dynamicIntensity, iteration int, score float64, verifyPassed bool, missingCount int) dynamicIntensity {
	next := cur
	next.MissingBoostApplied = false
	if (!verifyPassed || score < cur.Threshold) && iteration >= 2 {
		next.Mode = "high"
		next.Threshold = clampFloat(cur.Threshold-0.03, 0.45, 0.80)
		next.FollowUpMax = 5
	}
	if verifyPassed && score >= cur.Threshold+0.1 {
		next.Mode = "low"
		next.Threshold = clampFloat(cur.Threshold+0.02, 0.45, 0.85)
		next.FollowUpMax = 2
	}
	if missingCount >= 5 {
		next.Mode = "high"
		next.FollowUpMax = 5
		next.MissingBoostApplied = true
	}
	return next
}

func computePlanSnapshot(iteration int, todos []TodoItem) PlanSnapshot {
	s := PlanSnapshot{Iteration: iteration, Total: len(todos)}
	for _, td := range todos {
		switch strings.TrimSpace(td.Status) {
		case "pending":
			s.Pending++
		case "in_progress":
			s.InProgress++
		case "needs_followup":
			s.NeedsFollow++
		case "done":
			s.Done++
		default:
			s.Pending++
		}
	}
	return s
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func evaluateIteration(iteration int, score float64, threshold float64, ver VerificationResult, todos []TodoItem, reports []SubAgentReport, missing []MissingFinding, passNumber int) EvaluationResult {
	m := EvaluationMetrics{
		SuccessRate: calcSuccessRate(todos),
		Efficiency:  clampFloat(1.0/float64(maxInt(passNumber, 1)), 0, 1),
		Cost:        clampFloat(float64(len(reports))/float64(maxInt(len(todos), 1)), 0, 1),
		Robustness:  clampFloat(1.0-float64(len(missing))/float64(maxInt(len(todos), 1)), 0, 1),
		Safety:      1.0,
		Consistency: calcConsistency(reports),
	}
	if !ver.Passed {
		m.Safety = 0.5
	}
	for _, mf := range missing {
		if mf.Severity == "high" {
			m.Safety = clampFloat(m.Safety-0.2, 0, 1)
		}
	}
	// Lower cost is better; normalize to quality-like score.
	costQuality := clampFloat(1.0-m.Cost, 0, 1)
	passed := score >= threshold && ver.Passed
	if passed {
		return EvaluationResult{
			Iteration: iteration,
			Metrics:   m,
			Decision:  EarlyStopDecision{Stop: true, Reason: "quality gates met"},
			Successful: true,
		}
	}
	if m.Safety < 0.3 {
		return EvaluationResult{
			Iteration: iteration,
			Metrics:   m,
			Decision:  EarlyStopDecision{Stop: true, Reason: "early stop: safety risk exceeded"},
		}
	}
	if iteration >= 3 && m.SuccessRate < 0.35 && m.Consistency < 0.35 {
		return EvaluationResult{
			Iteration: iteration,
			Metrics:   m,
			Decision:  EarlyStopDecision{Stop: true, Reason: "early stop: low success and consistency after repeated attempts"},
		}
	}
	_ = costQuality
	return EvaluationResult{Iteration: iteration, Metrics: m}
}

func calcSuccessRate(todos []TodoItem) float64 {
	if len(todos) == 0 {
		return 0
	}
	done := 0
	for _, td := range todos {
		if td.Status == "done" {
			done++
		}
	}
	return clampFloat(float64(done)/float64(len(todos)), 0, 1)
}

func calcConsistency(reports []SubAgentReport) float64 {
	if len(reports) == 0 {
		return 1
	}
	sum := 0.0
	for _, r := range reports {
		sum += clampFloat(r.Score, 0, 1)
	}
	mean := sum / float64(len(reports))
	variance := 0.0
	for _, r := range reports {
		d := clampFloat(r.Score, 0, 1) - mean
		variance += d * d
	}
	variance /= float64(len(reports))
	return clampFloat(1.0-variance, 0, 1)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (o *Orchestrator) preTodoWebResearch(ctx context.Context, sys, goal string) (string, error) {
	sb := SubAgentSandbox{
		Role:            "researcher",
		TodoID:          "todo-research-00",
		Scope:           "web research grounding before planning",
		AllowedTools:    []string{"heros_web_search", "heros_memory_search"},
		AllowedSkills:   []string{"research"},
		Forbidden:       []string{"shell", "write_file"},
		RequireEvidence: true,
	}
	prompt := "Search the web first and return a short planning brief: key facts, latest constraints, risks, and links/sources relevant to this goal.\nGoal: " + strings.TrimSpace(goal)
	return o.specialist(ctx, sys, sb, prompt)
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func (o *Orchestrator) leaderPlan(ctx context.Context, sys string, topo Topology, goal string) ([]string, error) {
	model := topo.LeaderModel
	if model == "" {
		model = o.LLM.Model
	}
	prompt := `Decompose the user goal into 2-5 concrete sub-tasks, one per line, no numbering. Goal: ` + goal
	raw, err := o.chat(ctx, model, sys, prompt)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return []string{goal}, nil
	}
	return lines, nil
}

func (o *Orchestrator) specialist(ctx context.Context, sys string, sandbox SubAgentSandbox, step string) (string, error) {
	prompt := fmt.Sprintf(
		"You are the %s specialist running in a sandbox.\nScope: %s\nAllowed tools: %s\nAllowed skills: %s\nForbidden: %s\nRequire evidence: %t\nExecute this sub-task briefly and practically, and call out what's missing if blocked:\n%s",
		sandbox.Role,
		sandbox.Scope,
		strings.Join(sandbox.AllowedTools, ", "),
		strings.Join(sandbox.AllowedSkills, ", "),
		strings.Join(sandbox.Forbidden, ", "),
		sandbox.RequireEvidence,
		step,
	)
	return o.chat(ctx, o.LLM.Model, sys, prompt)
}

func (o *Orchestrator) criticSubAgent(ctx context.Context, sys, goal, task, output string) (float64, string, error) {
	prompt := fmt.Sprintf(`Critique this sub-agent task output against the goal and task. Reply JSON only:
{"score":0.0,"feedback":"..."}
Goal: %s
Task: %s
Output:
%s`, goal, task, output)
	raw, err := o.chat(ctx, o.LLM.Model, sys, prompt)
	if err != nil {
		return 0, "", err
	}
	var out struct {
		Score    float64 `json:"score"`
		Feedback string  `json:"feedback"`
	}
	_ = json.Unmarshal([]byte(extractJSON(raw)), &out)
	if out.Score == 0 && !strings.Contains(raw, "score") {
		return 0.5, truncate(raw, 240), nil
	}
	return out.Score, strings.TrimSpace(out.Feedback), nil
}

func (o *Orchestrator) critic(ctx context.Context, sys string, topo Topology, goal, draft string) (float64, string, error) {
	prompt := fmt.Sprintf(`Score 0.0-1.0 how well the draft satisfies the goal. Reply JSON only: {"score":0.0,"rationale":"..."}
Goal: %s
Draft:
%s`, goal, draft)
	raw, err := o.chat(ctx, o.LLM.Model, sys, prompt)
	if err != nil {
		return 0, "", err
	}
	var out struct {
		Score     float64 `json:"score"`
		Rationale string  `json:"rationale"`
	}
	_ = json.Unmarshal([]byte(extractJSON(raw)), &out)
	if out.Score == 0 && !strings.Contains(raw, "score") {
		return 0.5, raw, nil
	}
	return out.Score, out.Rationale, nil
}

func (o *Orchestrator) followUpTodos(ctx context.Context, sys, goal, critique string, existing []TodoItem, attempt int, missing []MissingFinding, maxFollowUps int) ([]TodoItem, error) {
	if maxFollowUps <= 0 {
		maxFollowUps = 3
	}
	existingTitles := make([]string, 0, len(existing))
	lastDoneID := ""
	for _, td := range existing {
		existingTitles = append(existingTitles, td.Title)
		if td.Status == "done" {
			lastDoneID = td.ID
		}
	}
	prompt := fmt.Sprintf(`Generate 1-3 additional todo items needed to meet the goal based on critique.
Rules: one line per todo, concise, non-empty, no numbering.
Goal: %s
Critique: %s
Existing todos:
%s`, goal, critique, strings.Join(existingTitles, "\n"))
	raw, err := o.chat(ctx, o.LLM.Model, sys, prompt)
	if err != nil {
		return nil, err
	}
	var out []TodoItem
	seen := map[string]bool{}
	for i, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*0123456789. "))
		if line == "" || seen[strings.ToLower(line)] {
			continue
		}
		seen[strings.ToLower(line)] = true
		out = append(out, TodoItem{
			ID:        fmt.Sprintf("todo-%02d-a%d", i+1, attempt),
			Title:     line,
			Status:    "pending",
			Attempt:   attempt,
			CreatedBy: "critic",
			Priority:  inferPriorityForTodo(line),
			DependsOn: nil,
		})
		if lastDoneID != "" {
			out[len(out)-1].DependsOn = []string{lastDoneID}
		}
		if len(out) >= maxFollowUps {
			break
		}
	}
	for _, m := range missing {
		detail := strings.TrimSpace(m.Detail)
		if detail == "" {
			continue
		}
		out = append(out, TodoItem{
			ID:        fmt.Sprintf("todo-missing-%02d-a%d", len(out)+1, attempt),
			Title:     "Address missing: " + truncate(detail, 140),
			Status:    "pending",
			Attempt:   attempt,
			CreatedBy: "critic",
			Priority:  1,
			DependsOn: nil,
		})
		if lastDoneID != "" {
			out[len(out)-1].DependsOn = []string{lastDoneID}
		}
		if len(out) >= maxFollowUps {
			break
		}
	}
	return out, nil
}

func (o *Orchestrator) testAndPreview(ctx context.Context, sys, goal, draft string) (VerificationResult, error) {
	prompt := fmt.Sprintf(`Act as the main agent verifier. Evaluate testability and preview quality for the draft against the goal.
Reply JSON only:
{"summary":"...","checks":["...","..."],"passed":true}
Goal: %s
Draft:
%s`, goal, draft)
	raw, err := o.chat(ctx, o.LLM.Model, sys, prompt)
	if err != nil {
		return VerificationResult{}, err
	}
	var out VerificationResult
	_ = json.Unmarshal([]byte(extractJSON(raw)), &out)
	if strings.TrimSpace(out.Summary) == "" {
		out.Summary = truncate(strings.TrimSpace(raw), 220)
	}
	return out, nil
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i >= 0 {
		s = s[i:]
	}
	if j := strings.LastIndex(s, "}"); j >= 0 {
		s = s[:j+1]
	}
	return s
}

func (o *Orchestrator) chat(ctx context.Context, model, system, user string) (string, error) {
	if o.LLM.APIKey == "" {
		return heuristicReply(user), nil
	}
	if model == "" {
		model = "gpt-4o-mini"
	}
	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.LLM.BaseURL, "/")+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.LLM.APIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("llm %s: %s", resp.Status, string(rb))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rb, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("no choices")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

func heuristicReply(user string) string {
	if strings.Contains(strings.ToLower(user), "sub-task") || strings.Contains(strings.ToLower(user), "decompose") {
		return "1. Clarify requirements\n2. Draft minimal solution\n3. Verify against constraints"
	}
	return "(offline mode) Summarized: " + truncate(user, 200) + "\nAdd OPENAI_API_KEY for full LLM harness."
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func countOpenTodos(todos []TodoItem) int {
	n := 0
	for _, td := range todos {
		if td.Status != "done" {
			n++
		}
	}
	return n
}

func summarizeTodos(todos []TodoItem) (done, total int) {
	for _, td := range todos {
		total++
		if td.Status == "done" {
			done++
		}
	}
	return done, total
}

func agentVisibilityFor(role string) AgentVisibility {
	return AgentVisibility{
		Role:       role,
		ToolsUsed:  []string{"chat_completions"},
		SkillsUsed: []string{role},
		MemoryUsed: []string{"active_system_prompt", "goal", "todo_item"},
	}
}

func aggregateVisibility(reports []SubAgentReport) []AgentVisibility {
	byRole := map[string]AgentVisibility{}
	for _, r := range reports {
		v, ok := byRole[r.Role]
		if !ok {
			v = AgentVisibility{Role: r.Role}
		}
		v.ToolsUsed = unionStrings(v.ToolsUsed, r.ToolsUsed)
		v.SkillsUsed = unionStrings(v.SkillsUsed, r.SkillsUsed)
		v.MemoryUsed = unionStrings(v.MemoryUsed, r.MemoryUsed)
		byRole[r.Role] = v
	}
	out := make([]AgentVisibility, 0, len(byRole))
	for _, v := range byRole {
		out = append(out, v)
	}
	return out
}

func unionStrings(a, b []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, s := range b {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// ApplyTopologyMutation merges JSON diff into harness config (after human approval).
func ApplyTopologyMutation(db *sql.DB, diff string) (rollback string, err error) {
	cur, err := LoadTopology(db)
	if err != nil {
		return "", err
	}
	prev, _ := json.Marshal(cur)
	var patch Topology
	if err := json.Unmarshal([]byte(diff), &patch); err != nil {
		return "", err
	}
	if len(patch.Specialists) > 0 {
		cur.Specialists = patch.Specialists
	}
	if patch.CriticThreshold > 0 {
		cur.CriticThreshold = patch.CriticThreshold
	}
	if patch.MaxCriticRetries > 0 {
		cur.MaxCriticRetries = patch.MaxCriticRetries
	}
	if patch.LeaderModel != "" {
		cur.LeaderModel = patch.LeaderModel
	}
	if err := SaveTopology(db, cur); err != nil {
		return "", err
	}
	return string(prev), nil
}

// ProposeTopologyDiff returns human-readable diff for queue (self-evolution proposal).
func ProposeTopologyDiff(current, proposed Topology) string {
	a, _ := json.MarshalIndent(current, "", "  ")
	b, _ := json.MarshalIndent(proposed, "", "  ")
	return fmt.Sprintf("--- topology\n+++ topology\n@@\n-%s\n+%s\n", string(a), string(b))
}
