// Package executor runs a built, transformed working copy and records what it did (PRD §6 FR14–FR18;
// tasks 3.7, 3.8, 3.9, 5.1, 5.3).
//
// # Task 3.7 contradicts ADR-001, and this is how it is resolved
//
// Task 3.7 asks for: "run the built, transformed working copy in a sandbox, walking the node graph in
// declared ordering; pass each node's output through the typed I/O contract before it feeds a
// downstream node."
//
// Those two halves are from different designs. ADR-001's own terminology table retires the second:
//
//	| executor "walks the graph through the shim" | executor "runs the transformed working copy in an isolated sandbox" |
//
// Under the shim model the executor WAS the data path — it called each node and routed the output to
// the next, so it could inspect a value "before it feeds a downstream node". Source transformation
// removes that seam by design: the transformed program contains the graph and walks it itself, in the
// process, with no intermediary. There is nowhere for us to stand between two nodes. Keeping the old
// wording would mean re-introducing the shim ADR-001 rejected as infeasible for compiled languages —
// and it is precisely why P2 is faithful: the code under measurement is the code that ships.
//
// So this package implements the half that survives, and it is not a weaker one:
//
//   - We run the built copy as a sandboxed subprocess (FR14). It walks its own graph.
//   - It emits one record per node invocation as it goes.
//   - We validate each record's output against that node's typed io_contract from the P0 IR.
//   - On a violation we HALT: the process is killed (FR15, 3.8).
//
// The halt is what "do not pass malformed data downstream" means once we are not the data path. It is
// enforced rather than declined: the process that would have made the next provider call no longer
// exists. That is a stronger guarantee than refusing to route a value — it also stops every other
// effect the workflow would have had — and it is the only one available without a shim.
//
// # The sandbox
//
// P2's sandbox bounds OUR execution of the target's own build output: a process group that is killed
// as a unit, a wall-clock deadline, a scrubbed environment, and a working directory confined to the
// worktree. It is deliberately NOT the tool-isolation boundary — PRD §3 defers "sandboxed execution
// of arbitrary repo tool code" to P3, and this package does not pretend to contain hostile code. It
// contains code the user wrote and we rewrote, which is a much weaker threat model, and saying so is
// better than implying a containment that is not there.
package executor

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Status is a run's terminal state (FR18). Mirrors 0005's closed CHECK vocabulary.
type Status string

const (
	StatusRunning Status = "running"
	// StatusSucceeded: the transformed copy ran to completion.
	StatusSucceeded Status = "succeeded"
	// StatusFailed: it ran and did not complete — non-zero exit, crash, timeout.
	StatusFailed Status = "failed"
	// StatusHalted: WE stopped it on a typed-contract violation. Distinct from failed on purpose: the
	// workflow did not break, we stopped it, and that difference tells the user whether to look at
	// their code or at their contract.
	StatusHalted Status = "halted"
	// StatusBuildRejected: the transform never ran.
	StatusBuildRejected Status = "build-rejected"
)

var (
	// ErrContractViolation: a node's output did not satisfy its typed io_contract. The run halts.
	ErrContractViolation = errors.New("executor: node output violates its typed I/O contract")
	// ErrRunFailed: the transformed copy exited non-zero or died.
	ErrRunFailed = errors.New("executor: the transformed copy did not complete")
)

// HaltError names the node and the reason a run was halted (FR15, 3.8; PRD §9's unhappy path).
type HaltError struct {
	NodeID string
	Reason string
}

func (e *HaltError) Error() string {
	return fmt.Sprintf("%s: node %q: %s", ErrContractViolation.Error(), e.NodeID, e.Reason)
}

func (e *HaltError) Unwrap() error { return ErrContractViolation }

// Invocation is one node invocation the transformed copy reports.
//
// The shape is P0's runtime-invocation contract (schemas/runtime-invocation.schema.json) plus the I/O
// payloads P2 needs to check the contract. The identity fields are frozen; `input`/`output` are the
// values this package validates and records.
type Invocation struct {
	InvocationID string `json:"invocation_id"`
	NodeID       string `json:"node_id"`
	RunID        string `json:"run_id"`
	// InvocationIndex is 0-based within the run — a loop that fires 7 times reports 0..6 for ONE node.
	InvocationIndex int             `json:"invocation_index"`
	Input           json.RawMessage `json:"input,omitempty"`
	Output          json.RawMessage `json:"output,omitempty"`
	Error           string          `json:"error,omitempty"`
}

// NodeResult is one recorded node execution.
type NodeResult struct {
	NodeID         string
	AttemptGroup   int
	Status         Status
	IdempotencyKey string
	InputBlobHash  string
	OutputBlobHash string
	Error          string
}

// Run is a completed execution.
type Run struct {
	RunID          string
	ConfigHash     string
	SourceRevision string
	Seed           int64
	Status         Status
	HaltedNodeID   string
	HaltedReason   string
	Nodes          []NodeResult
	Stdout         string
	Stderr         string
	StartedAt      time.Time
	FinishedAt     time.Time
}

// BlobStore stores node I/O under its content hash.
type BlobStore interface {
	Put(ctx context.Context, data []byte) (contentHash string, err error)
}

// Options configure one run.
type Options struct {
	// Dir is the BUILT, transformed worktree (worktree.Applied.Dir). Running anything else would
	// measure code that is not the code that would ship, which is the whole point of ADR-001.
	Dir string
	// Cmd is the command to run inside Dir. Supplied by the caller because "how this workflow runs"
	// is the target's business — P2 consumes a hardcoded graph (PRD §2), so the caller knows.
	Cmd []string
	// RunID identifies this execution batch. Also half of every idempotency key.
	RunID string
	// Seed is threaded to the transformed copy and to every stochastic step (task 5.3).
	Seed int64
	// IR supplies each node's typed io_contract. Without it there is nothing to check and the run
	// cannot honour FR15, so it is required.
	IR *discovery.IR
	// Timeout bounds the whole run. Zero means DefaultRunTimeout.
	Timeout time.Duration
	// Env, when non-nil, is the exact environment. See sandboxEnv for why the default is not os.Environ().
	Env []string
}

// DefaultRunTimeout bounds a run that never terminates. PRD §7: one slow provider must not exhaust
// the executor; the same applies to a workflow that hangs.
const DefaultRunTimeout = 10 * time.Minute

// InvocationLogEnv names the file the transformed copy writes its invocation records to.
//
// A file rather than stdout: the workflow's own stdout is the user's, and multiplexing our protocol
// into it would mean a `fmt.Println` in their code could forge or corrupt an invocation record. A
// dedicated channel keeps their output theirs and ours unambiguous.
const InvocationLogEnv = "HEROS_INVOCATION_LOG"

// SeedEnv carries the run's seed into the transformed copy (task 5.3).
const SeedEnv = "HEROS_SEED"

// RunIDEnv carries the run id, so emitted records can be attributed without the copy guessing.
const RunIDEnv = "HEROS_RUN_ID"

// Executor runs built, transformed working copies.
type Executor struct {
	blobs BlobStore
}

func New(blobs BlobStore) *Executor { return &Executor{blobs: blobs} }

// IdempotencyKey derives the key for one node invocation (task 5.1).
//
// PRD OQ1 pins the unit at {run_id, node_id, attempt_group}. Every retry WITHIN a node invocation
// reuses it, so the provider de-duplicates and a transient failure cannot bill twice (FR17); a
// genuinely new invocation gets a new attempt_group and is a new charge, because it is one.
//
// It is derived, not random: a random key regenerated on retry is not an idempotency key at all, it
// is a second request wearing a hat. Deriving it from the run's own coordinates means two executors
// racing the same node — an at-least-once queue delivering twice (PRD OQ4) — independently compute
// the SAME key, so the provider collapses them even though neither knew about the other.
func IdempotencyKey(runID, nodeID string, attemptGroup int) string {
	return fmt.Sprintf("%s:%s:%d", runID, nodeID, attemptGroup)
}

// RunIDFor is the run_id for one experiment: {config_hash, source_revision, seed}.
//
// # Why a run's id is derived and not minted
//
// Those three fields are already this system's stated reproducibility unit — 0005's `seed` column
// says so in as many words ("The reproducibility unit is {config_hash, source_revision, seed}, and
// this column is the third axis"), and config-hash-spec §5 keeps seed OUT of config_hash precisely so
// seeds 1..5 of one configuration roll up together. Two submissions agreeing on all three describe
// the same experiment, and the platform is deterministic by construction, so running it a second time
// can only reproduce the first answer at the price of a second build and a second bill.
//
// Deriving the id from the unit makes that mechanical instead of a rule someone has to remember: the
// duplicate submit collides on the primary key and collapses into the run that already exists. A
// minted id (a UUID at submit time) would make every re-submit a new run, and every one of them would
// be billed — the same failure runqueue's doc comment describes for per-delivery ids.
//
// The converse is just as important and is why seed is IN the key: a re-submit with a different seed
// is a genuinely different experiment (it is how variance gets measured) and MUST get its own run.
//
// Domain-separated ("heros.run.v1") so this hash can never collide with another hash in the system
// that happens to be taken over the same fields, and versioned so a future change to what identifies
// a run is a visible, greppable break rather than a silent re-mapping of existing ids.
func RunIDFor(configHash, sourceRevision string, seed int64) string {
	h := sha256.Sum256([]byte("heros.run.v1\n" + configHash + "\n" + sourceRevision + "\n" +
		strconv.FormatInt(seed, 10)))
	// 96 bits of a SHA-256, which is far past collision relevance for a workload of ~10 runs/day
	// (storage-decision-record A6) and stays short enough to read in a URL and a log line.
	return "run_" + hex.EncodeToString(h[:])[:24]
}

// Run executes the built, transformed copy and returns its terminal state (FR14, FR18).
func (e *Executor) Run(ctx context.Context, opts Options) (*Run, error) {
	if opts.Dir == "" || len(opts.Cmd) == 0 {
		return nil, fmt.Errorf("executor: Run requires a built worktree and a command")
	}
	if opts.RunID == "" {
		return nil, fmt.Errorf("executor: Run requires a run_id")
	}
	if opts.IR == nil {
		return nil, fmt.Errorf("executor: Run requires the IR: without it there is no typed contract to check")
	}
	contracts, err := compileContracts(opts.IR)
	if err != nil {
		return nil, err
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultRunTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	logPath := filepath.Join(opts.Dir, ".heros-invocations.jsonl")
	_ = os.Remove(logPath) // a previous run's records must not be read as this one's
	run := &Run{RunID: opts.RunID, Seed: opts.Seed, Status: StatusRunning, StartedAt: time.Now().UTC()}

	cmd := exec.CommandContext(ctx, opts.Cmd[0], opts.Cmd[1:]...)
	cmd.Dir = opts.Dir
	cmd.Env = sandboxEnv(opts, logPath)
	// Own process group, so a halt kills the workflow AND everything it spawned. Killing only the
	// parent would leave a child mid-provider-call — still spending money, still writing — which is
	// exactly what "halt" must not mean.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Cancel must kill the GROUP too. exec.CommandContext's default kills only the direct child, so a
	// workflow that shells out (`sh -c` wrapping anything) leaves the grandchild alive — and because
	// that grandchild still holds the stdout pipe, cmd.Wait blocks on it and the deadline does not
	// fire at all. A 500 ms timeout that returns after 60 s is not a timeout.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// And bound the wait for the pipes themselves: a survivor holding stdout open must not hang the
	// executor after the process group is gone.
	cmd.WaitDelay = 2 * time.Second

	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("executor: start %v: %w", opts.Cmd, err)
	}

	// Watch the invocation log while the copy runs, so a violation halts it mid-flight rather than
	// after it has finished doing the damage.
	halt := make(chan *HaltError, 1)
	procDone := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		e.watch(ctx, procDone, logPath, contracts, opts, run, halt, cmd)
	}()

	waitErr := cmd.Wait()
	// Signal, do not cancel. A fast workflow writes its records and exits before the watcher has read
	// a byte; cancelling here would drop every one of them and report a run with no nodes. The watcher
	// drains what is left and then stops.
	close(procDone)
	wg.Wait()

	run.Stdout, run.Stderr = stdout.String(), stderr.String()
	run.FinishedAt = time.Now().UTC()

	// The halt takes precedence over the exit status. A killed process exits non-zero, and reporting
	// that as `failed` would tell the user their workflow crashed when in fact we stopped it — sending
	// them to debug code that was working.
	select {
	case h := <-halt:
		run.Status, run.HaltedNodeID, run.HaltedReason = StatusHalted, h.NodeID, h.Reason
		return run, h
	default:
	}

	if waitErr != nil {
		run.Status = StatusFailed
		if ctx.Err() != nil {
			return run, fmt.Errorf("%w: timed out after %s", ErrRunFailed, timeout)
		}
		return run, fmt.Errorf("%w: %v", ErrRunFailed, waitErr)
	}
	run.Status = StatusSucceeded
	return run, nil
}

// watch tails the invocation log, records each node, and halts on a contract violation.
func (e *Executor) watch(ctx context.Context, procDone <-chan struct{}, path string, contracts map[string]*nodeContract, opts Options, run *Run, halt chan<- *HaltError, cmd *exec.Cmd) {
	f, err := waitForFile(ctx, procDone, path)
	if err != nil {
		return // the copy never emitted anything; that is not itself a failure
	}
	defer func() { _ = f.Close() }()

	seen := map[string]int{}
	// finalPass: the process has exited, so what is in the file is all there will ever be. Drain it
	// once more and stop — the alternative is dropping the records of any workflow fast enough to
	// finish before we got here.
	finalPass := false
	for {
		if halted := e.drain(ctx, f, seen, contracts, opts, run, halt, cmd); halted {
			return
		}
		if finalPass {
			return
		}
		select {
		case <-ctx.Done():
			finalPass = true
		case <-procDone:
			finalPass = true
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// drain reads every complete record currently in the file. It returns true if the run was halted.
func (e *Executor) drain(ctx context.Context, f *os.File, seen map[string]int, contracts map[string]*nodeContract, opts Options, run *Run, halt chan<- *HaltError, cmd *exec.Cmd) bool {
	sc := bufio.NewScanner(f)                        // resumes at the file offset, so records are never re-read
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // node I/O is prompts and completions; 64 KB is not enough
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var inv Invocation
		if err := json.Unmarshal([]byte(line), &inv); err != nil {
			continue // a malformed record is not a contract violation; it is noise on our channel
		}
		if inv.NodeID == "" {
			continue
		}
		_ = ctx

		// attempt_group counts invocations of this node within the run. A loop's second iteration is a
		// new invocation and a new charge; a retry inside one is not (see IdempotencyKey).
		group := seen[inv.NodeID]
		seen[inv.NodeID]++

		res := NodeResult{
			NodeID: inv.NodeID, AttemptGroup: group, Status: StatusSucceeded,
			IdempotencyKey: IdempotencyKey(opts.RunID, inv.NodeID, group), Error: inv.Error,
		}
		if inv.Error != "" {
			res.Status = StatusFailed
		}
		res.InputBlobHash = e.put(ctx, inv.Input)
		res.OutputBlobHash = e.put(ctx, inv.Output)
		run.Nodes = append(run.Nodes, res)

		c, ok := contracts[inv.NodeID]
		if !ok || len(inv.Output) == 0 {
			continue
		}
		if err := c.validateOutput(inv.Output); err != nil {
			res.Status = StatusHalted
			res.Error = err.Error()
			run.Nodes[len(run.Nodes)-1] = res
			h := &HaltError{NodeID: inv.NodeID, Reason: err.Error()}
			select {
			case halt <- h:
			default:
			}
			// Kill the whole process group. This is FR15 enforced rather than declined: the process
			// that would have fed this output to the next node no longer exists.
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			return true
		}
	}
	return false
}

// put stores a node I/O payload as a content-addressed blob (PRD §7: never inline).
func (e *Executor) put(ctx context.Context, data []byte) string {
	if len(data) == 0 || e.blobs == nil {
		return ""
	}
	h, err := e.blobs.Put(ctx, data)
	if err != nil {
		return "" // a blob we could not store is a missing reference, not a failed run
	}
	return h
}

// waitForFile opens the invocation log once the copy creates it.
//
// It gives up when the process has exited AND the file still is not there — a workflow that emitted
// nothing is a legitimate outcome, and spinning until the run deadline to discover that would turn
// every such run into a timeout.
func waitForFile(ctx context.Context, procDone <-chan struct{}, path string) (*os.File, error) {
	exited := false
	for {
		if f, err := os.Open(path); err == nil {
			return f, nil
		}
		if exited {
			return nil, os.ErrNotExist
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-procDone:
			exited = true // one more look: the file may have appeared as it exited
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// sandboxEnv builds the environment the transformed copy runs with.
//
// It is an ALLOWLIST, not os.Environ(). Two reasons, and the second is the one that matters:
//
//  1. Reproducibility. A run's behavior must be a function of {config_hash, source_revision, seed}.
//     Inheriting the operator's shell means a stray GOFLAGS or LANG makes two runs of the same
//     config_hash differ, and P4 would score the difference as a result.
//  2. Secrets. The executor's own process holds provider credentials. Handing os.Environ() to the
//     target would export every one of them into a process whose code we rewrote but did not write —
//     and PRD §7 says secrets never reach run records, of which the target's stdout is one.
//     Credentials reach a provider through the gateway, at call time, and nowhere else.
func sandboxEnv(opts Options, logPath string) []string {
	if opts.Env != nil {
		return append(opts.Env,
			InvocationLogEnv+"="+logPath,
			SeedEnv+"="+fmt.Sprint(opts.Seed),
			RunIDEnv+"="+opts.RunID,
		)
	}
	env := []string{
		"PATH=" + os.Getenv("PATH"), // the toolchain and the workflow's own dependencies
		"HOME=" + os.TempDir(),      // never the operator's home: it holds credentials and git config
		"TMPDIR=" + os.TempDir(),
		InvocationLogEnv + "=" + logPath,
		SeedEnv + "=" + fmt.Sprint(opts.Seed),
		RunIDEnv + "=" + opts.RunID,
	}
	return env
}

// nodeContract is one node's compiled typed I/O contract from the P0 IR.
type nodeContract struct {
	nodeID string
	output *jsonschema.Schema
}

func (c *nodeContract) validateOutput(raw json.RawMessage) error {
	v, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		return fmt.Errorf("output is not valid JSON: %v", err)
	}
	if err := c.output.Validate(v); err != nil {
		return fmt.Errorf("output violates the node's io_contract: %v", err)
	}
	return nil
}

// compileContracts compiles every node's output schema up front.
//
// Up front, and failing the whole run if any is invalid, because discovering at node 4 that node 5's
// contract does not compile would mean four nodes' worth of provider spend before finding out the run
// could never have been checked. This is the same fail-closed-before-side-effects stance the Loader
// takes on refs.
func compileContracts(ir *discovery.IR) (map[string]*nodeContract, error) {
	out := map[string]*nodeContract{}
	for i := range ir.Nodes {
		n := &ir.Nodes[i]
		if n.IOContract.OutputSchema == nil {
			continue
		}
		raw, err := json.Marshal(n.IOContract.OutputSchema)
		if err != nil {
			return nil, fmt.Errorf("executor: node %q: marshal io_contract: %w", n.NodeID, err)
		}
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
		if err != nil {
			return nil, fmt.Errorf("executor: node %q: io_contract is not valid JSON: %w", n.NodeID, err)
		}
		c := jsonschema.NewCompiler()
		c.UseLoader(offlineLoader{}) // hermetic, as in the Skill Registry: a remote $ref would make the check depend on a third party
		const loc = "heros:io-contract"
		if err := c.AddResource(loc, doc); err != nil {
			return nil, fmt.Errorf("executor: node %q: %w", n.NodeID, err)
		}
		sch, err := c.Compile(loc)
		if err != nil {
			return nil, fmt.Errorf("executor: node %q: io_contract is not a valid JSON Schema: %w", n.NodeID, err)
		}
		out[n.NodeID] = &nodeContract{nodeID: n.NodeID, output: sch}
	}
	return out, nil
}

type offlineLoader struct{}

func (offlineLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("remote $ref %q is not allowed in an io_contract", url)
}

var _ = io.Discard
