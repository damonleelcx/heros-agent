package sandbox

// This file selects the isolate enforcer for a deployment and documents how the OS-level restrictions
// (default-deny egress, filesystem scoping) are actually delivered.
//
// # Two layers, one boundary
//
// The Go SubprocessEnforcer guarantees the controls a process can enforce on itself, portably: a
// scrubbed environment (no ambient credentials) and hard resource bounds (CPU / memory / file-size /
// wall-clock / captured-output). It reports network- and filesystem-namespace isolation as UNAVAILABLE
// because a bare subprocess cannot deny its own egress or unmap host paths without OS namespaces or a
// container — and it must not pretend otherwise, or the fail-closed gate is defeated.
//
// The remaining controls — default-deny network egress and a read-only, working-set-scoped filesystem
// — are delivered by the DEPLOYMENT layer, exactly as the Discovery worker's least-privilege runtime is
// (deploy/docker-compose.discovery.yml, proven by `make discovery-sandbox-proof`): the isolate process
// runs inside a container/namespace with `network_mode: none` and a read-only working-set mount. When a
// deployment has established those, it constructs the enforcer with WithCapabilities so the sandbox
// knows egress-deny and FS-scope are in force and admits nodes that require them. On a host WITHOUT
// that outer isolation, NewOSEnforcer reports them unavailable and every untrusted-repo-tool node fails
// closed (task 3.6) — the correct posture, not a silent downgrade to host execution.
//
// This split keeps the Go unit tests hermetic and cross-platform (they prove the fail-closed gate,
// env-scrub, resource bounds, audit, and lifecycle) while the container proof — the same kind the repo
// already ships for Discovery — proves the network/FS denial the OS provides.

// NewOSEnforcer returns the enforcer for the current host: the portable subprocess enforcer, reporting
// exactly the capabilities this process can guarantee on its own. A deployment that wraps the isolate
// in a network-denied, FS-scoped container calls NewContainedEnforcer instead.
func NewOSEnforcer() Enforcer {
	return NewSubprocessEnforcer()
}

// NewContainedEnforcer is the production enforcer for a host where the isolate already runs inside an
// outer container/namespace that denies egress (default-deny, allowlist applied at the container's
// network policy) and mounts only the read-only working set. It advertises NetworkDeny + FilesystemScope
// as in force so the sandbox admits untrusted-repo-tool nodes; the Go layer still enforces env-scrub and
// resource bounds inside that container.
//
// Use ONLY when the outer container genuinely provides those restrictions (verified by the container
// proof). Passing this on a bare host would claim containment that is not there — the one thing the
// fail-closed design exists to prevent.
func NewContainedEnforcer() Enforcer {
	return NewSubprocessEnforcer().WithCapabilities(Capabilities{
		ScrubEnv:        true,
		ResourceLimits:  hasShell(),
		NetworkDeny:     true,
		FilesystemScope: true,
	})
}
