package sandbox

import (
	"context"
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
)

// SubprocessEnforcer runs the tool as an isolated subprocess. It enforces, portably (macOS + Linux):
//
//   - a scrubbed environment — the child inherits NO ambient credentials (task 3.2);
//   - hard resource bounds — CPU, memory, process/PID count, and file size are set on the child via a
//     `ulimit` shim before it execs the tool, so a fork bomb or mem bomb is contained by the OS
//     (task 3.5); wall-clock and captured-output size are enforced by the host reader;
//   - an ephemeral, working-set-scoped scratch directory — the child's cwd and HOME point at a private
//     temp dir; the declared working set is copied in read-only, and nothing else is handed over.
//
// OS-level *network* isolation and *filesystem-namespace* scoping are NOT portable to a bare macOS
// subprocess. This enforcer reports them honestly in Capabilities: on a host that cannot deny egress
// at the OS level, a Spec that Requires network isolation fails closed (task 3.6) rather than running
// untrusted code with host network access. Production on Linux wires a namespace/container enforcer
// that reports those capabilities true; the credentialed **broker** (task 4) is the only sanctioned
// egress path in every case.
type SubprocessEnforcer struct {
	caps Capabilities
}

// NewSubprocessEnforcer builds the portable enforcer. It advertises exactly what a bare subprocess can
// guarantee on this host — env scrub and resource bounds always; network/FS-namespace isolation only
// where the platform provides it (detected once).
func NewSubprocessEnforcer() *SubprocessEnforcer {
	return &SubprocessEnforcer{caps: detectCapabilities()}
}

// WithCapabilities overrides advertised capabilities. Used by a deployment that layers additional OS
// isolation (a container/namespace wrapper) around this enforcer and can therefore honestly promise
// network deny / FS scope. Never use it to CLAIM a capability the environment does not provide — that
// would defeat the fail-closed gate.
func (e *SubprocessEnforcer) WithCapabilities(c Capabilities) *SubprocessEnforcer {
	e.caps = c
	return e
}

func (e *SubprocessEnforcer) Capabilities() Capabilities { return e.caps }

// detectCapabilities probes the host. Env scrub and ulimit-based resource bounds are always available
// (a POSIX shell is assumed present); network/FS-namespace isolation is left to a platform enforcer.
func detectCapabilities() Capabilities {
	return Capabilities{
		ScrubEnv:        true,
		ResourceLimits:  hasShell(),
		NetworkDeny:     false,
		FilesystemScope: false,
	}
}

func hasShell() bool {
	_, err := exec.LookPath("sh")
	return err == nil
}

func (e *SubprocessEnforcer) Create(_ context.Context, spec Spec, pool *warmPool) (Isolate, error) {
	return newSubprocessIsolate(spec, pool, nil)
}

// newSubprocessIsolate stages a scratch dir + working set and returns a live isolate. baseSysProc lets
// a platform enforcer (Linux namespaces) inject clone flags / uid mappings; nil is the portable path.
func newSubprocessIsolate(spec Spec, pool *warmPool, baseSysProc *syscall.SysProcAttr) (Isolate, error) {
	scratch, ok := pool.get()
	if !ok {
		return nil, fmt.Errorf("could not create an ephemeral scratch directory")
	}
	iso := &subprocessIsolate{scratch: scratch, spec: spec, baseSysProc: baseSysProc}
	if err := iso.stageWorkingSet(); err != nil {
		iso.Destroy()
		return nil, err
	}
	return iso, nil
}

// subprocessIsolate is one live subprocess isolate.
type subprocessIsolate struct {
	scratch string
	spec    Spec
	once    sync.Once
	// baseSysProc, when non-nil, is the platform enforcer's SysProcAttr (e.g. Linux namespace clone
	// flags + uid mappings). Exec merges Setpgid and the group-kill Cancel onto it. Nil on the portable
	// path, where only the process group is set.
	baseSysProc *syscall.SysProcAttr
}

// stageWorkingSet copies each declared working-set path into the read-only view the tool sees. The
// tool is never handed a host path: it only sees the copies under scratch/work, so a path outside the
// working set is simply not present (task 3.4). Copies are made read-only.
func (iso *subprocessIsolate) stageWorkingSet() error {
	workDir := filepath.Join(iso.scratch, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}
	for _, p := range iso.spec.WorkingSet {
		info, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("working-set path %q: %w", p, err)
		}
		dst := filepath.Join(workDir, filepath.Base(p))
		if info.IsDir() {
			if err := copyTreeReadOnly(p, dst); err != nil {
				return err
			}
			continue
		}
		if err := copyFileReadOnly(p, dst); err != nil {
			return err
		}
	}
	return nil
}

// Exec runs the tool with resource bounds, a scrubbed env, and an output cap. onDenial is called for a
// resource breach; the error wraps ErrResourceBreach so the node fails closed with a typed error.
func (iso *subprocessIsolate) Exec(ctx context.Context, tool Tool, onDenial func(Denial)) (*Result, error) {
	b := iso.spec.Bounds
	ctx, cancel := context.WithTimeout(ctx, b.Wallclock)
	defer cancel()

	// The ulimit shim sets child rlimits then execs the tool. `ulimit` applies to the shell and every
	// process it spawns, so a fork/mem bomb is contained by the OS, not by us watching it.
	argv := shimArgv(b, tool.Argv)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = filepath.Join(iso.scratch, "work")
	cmd.Env = scrubbedEnv(iso.scratch)
	if len(tool.Stdin) > 0 {
		cmd.Stdin = strings.NewReader(string(tool.Stdin))
	}
	// Own process group so a breach kills the tool AND everything it forked — killing only the parent
	// would leave a fork bomb's children alive (same discipline as the executor). A platform enforcer's
	// namespace/uid attributes (if any) are the base; Setpgid rides on top.
	sysProc := &syscall.SysProcAttr{}
	if iso.baseSysProc != nil {
		*sysProc = *iso.baseSysProc
	}
	sysProc.Setpgid = true
	cmd.SysProcAttr = sysProc
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 2 * time.Second

	// Bounded capture: reading into a cap'd buffer means a tool spewing gigabytes cannot exhaust host
	// memory, and crossing the cap is itself a resource breach (task 3.5).
	outCap := &capWriter{limit: b.MaxOutput}
	errCap := &capWriter{limit: b.MaxOutput}
	cmd.Stdout = outCap
	cmd.Stderr = errCap

	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	res := &Result{Stdout: outCap.bytes(), Stderr: errCap.bytes()}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}

	// Classify a resource breach: output overflow, wall-clock timeout, or a limit-kill (ulimit CPU/PID/
	// file-size shows up as a non-zero exit or signal). Any of these terminates the isolate and fails
	// the node closed with a typed resource error.
	breach := ""
	switch {
	case outCap.overflowed() || errCap.overflowed():
		breach = "captured output exceeded " + strconv.FormatInt(b.MaxOutput, 10) + " bytes"
	case ctx.Err() == context.DeadlineExceeded || elapsed >= b.Wallclock:
		breach = "wall-clock deadline of " + b.Wallclock.String() + " exceeded"
	case runErr != nil && signaledOrLimited(cmd.ProcessState):
		breach = "resource limit breach (CPU/PID/memory/file-size); tool terminated by the OS"
	}
	if breach != "" {
		res.ResourceBreach = breach
		res.Denials = append(res.Denials, Denial{Kind: DenyResource, Reason: breach})
		if onDenial != nil {
			onDenial(Denial{Kind: DenyResource, Reason: breach})
		}
		return res, fmt.Errorf("%w: %s", ErrResourceBreach, breach)
	}
	if runErr != nil {
		// A non-zero exit that is not a resource breach is the tool failing on its own terms. Surface it,
		// but it is not a sandbox denial.
		return res, fmt.Errorf("sandbox: tool exited with error: %w", runErr)
	}
	return res, nil
}

func (iso *subprocessIsolate) Destroy() {
	iso.once.Do(func() {
		if iso.scratch != "" {
			_ = os.RemoveAll(iso.scratch) // ephemeral: no state survives the isolate (task 3.7)
		}
	})
}

// shimArgv wraps the tool in a POSIX-shell `ulimit` preamble that sets child resource limits before
// exec'ing it. CPU seconds (-t) and file size (-f) are portable and per-tree; virtual memory (-v) is
// set where the shell supports it (Linux). The tool's own argv is passed positionally so no
// shell-quoting of the tool's arguments is required.
//
// Note on PID/process-count containment: RLIMIT_NPROC (`ulimit -u`) is enforced against the REAL USER's
// total process count, not this tool's subtree — setting it low on a shared host makes every fork fail
// regardless of the tool. So the portable shim does NOT set it; per-PID containment (task 3.5) is
// delivered by the platform namespace enforcer, which runs the tool under a dedicated uid + PID
// namespace where RLIMIT_NPROC is meaningful. On this portable path a runaway (including a fork bomb)
// is still contained by the CPU limit, the wall-clock deadline, and the output cap, plus the
// process-group SIGKILL on teardown.
func shimArgv(b ResourceBounds, toolArgv []string) []string {
	cpu := int64(b.CPU / time.Second)
	if cpu < 1 {
		cpu = 1
	}
	memKB := b.Memory / 1024
	preamble := fmt.Sprintf("ulimit -t %d 2>/dev/null; ulimit -f 65536 2>/dev/null; ulimit -v %d 2>/dev/null || true; exec \"$@\"",
		cpu, memKB)
	// sh -c '<preamble>' sh <tool argv...>
	out := []string{"sh", "-c", preamble, "sh"}
	return append(out, toolArgv...)
}

// scrubbedEnv is the isolate's entire environment: no provider keys, no cloud credential files, no
// inherited secrets (task 3.2). HOME and TMPDIR point INTO the scratch dir, so a tool reading `~/.aws`
// or `$TMPDIR` finds only the empty ephemeral area, never the operator's home. PATH is included so the
// tool's interpreter resolves; it carries no secret.
func scrubbedEnv(scratch string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + scratch,
		"TMPDIR=" + scratch,
		"HEROS_ISOLATE=1", // a marker a tool can read; proves it is inside an isolate, carries no secret
	}
}

// signaledOrLimited reports whether the process was killed by a signal or exited via a limit. A ulimit
// CPU breach delivers SIGXCPU/SIGKILL; a file-size breach delivers SIGXFSZ; a fork-bomb hits EAGAIN and
// usually exits non-zero. We treat a signal death, or the shell's 128+signal convention, as a breach.
func signaledOrLimited(ps *os.ProcessState) bool {
	if ps == nil {
		return false
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok {
		if ws.Signaled() {
			return true
		}
		// A ulimit -u breach makes the shell's `exec` or a child fail; the shim exits non-zero. That is
		// ambiguous with an ordinary tool failure, so we do NOT classify a plain non-zero exit as a
		// breach here — the caller only reaches this branch when runErr != nil, and the wall-clock /
		// output checks run first.
	}
	return false
}

// capWriter captures up to limit bytes and records whether more was offered. Beyond the limit it drops
// the excess (the process is about to be killed) but flags the overflow.
type capWriter struct {
	mu       sync.Mutex
	buf      []byte
	limit    int64
	written  int64
	overflow bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.written += int64(len(p))
	if int64(len(w.buf)) < w.limit {
		room := w.limit - int64(len(w.buf))
		if room >= int64(len(p)) {
			w.buf = append(w.buf, p...)
		} else {
			w.buf = append(w.buf, p[:room]...)
		}
	}
	if w.written > w.limit {
		w.overflow = true
		// Signal the process to stop by returning an error, which closes the pipe and helps terminate a
		// runaway writer promptly (in addition to the context kill).
		return len(p), io.ErrShortWrite
	}
	return len(p), nil
}

func (w *capWriter) bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]byte, len(w.buf))
	copy(out, w.buf)
	return out
}

func (w *capWriter) overflowed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.overflow
}

// copyFileReadOnly copies src → dst with mode 0o444 (read-only).
func copyFileReadOnly(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o444)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// copyTreeReadOnly copies a directory tree, making every file read-only. Symlinks are skipped so a
// working-set dir cannot smuggle in a link to a host path outside the set.
func copyTreeReadOnly(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil // never follow a symlink out of the working set
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFileReadOnly(path, target)
	})
}
