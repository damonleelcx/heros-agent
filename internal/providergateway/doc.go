// Package providergateway is the unified LLM provider gateway for the Workflow Evaluation &
// Configuration System — the LiteLLM-style abstraction of Phase P2 (PRD §6 FR12/FR13, tasks 4.1–4.5;
// see openspec/changes/p2-config-runtime and docs/prd/P2-config-runtime.md).
//
// It is the seam that makes a provider swap a one-argument change: the Runtime resolves a Variant
// Spec's model_ref to a registry model entry and calls Gateway.Complete, which normalizes the request
// and the response so nothing above this package knows which provider answered. ADR-001 does not
// touch it — the config-application mechanism changed from runtime shim to source transformation, but
// providers are still abstracted at execution time.
//
// # What is here
//
//   - Gateway.Complete — a normalized Request/Response across OpenAI (chat/completions), Anthropic
//     (Messages), and Bedrock (Converse). One Message/Tool/Usage/StopReason vocabulary; each
//     provider's wire shape is confined to an adapter (adapters.go).
//   - Per-call timeout (60 s default, per-model override) bounding ALL attempts together, plus
//     bounded exponential backoff with full jitter, on transient failures only (gateway.go).
//   - Credentials from a Secrets source, injected at call time and scrubbed out of every error
//     (secrets.go). A credential is never part of a model entry, and so never part of a config_hash.
//
// It began as the OpenAI-compatible chat client salvaged from the retired interactive agent
// (internal/cliagent/openai.go and stream.go). ChatCompletion and ChatCompletionStream are what
// remains of it: a lower-level transport with no retries, timeouts, or secret handling. New code
// should call Gateway; those two are kept only because ChatCompletionStream is the sole streaming
// path and Gateway does not stream yet.
//
// The old agent's hardcoded tool catalog (OpenAITools/ToolOptions) was intentionally left behind —
// tool/skill schemas now come from the Skill Registry (internal/registry) and reach a provider
// through Request.Tools.
//
// # Still open
//
//   - TODO(P2.5): per-request instrumentation hooks (latency/TTFT/tokens/cost) feeding the
//     OpenTelemetry substrate. Response.Usage and Response.Attempts exist for this — the normalized
//     Usage fields follow the GenAI semantic conventions so instrumentation is a read, not a
//     translation.
//   - Request.IdempotencyKey is carried and sent on every attempt, retries included. Deriving it is
//     the Runtime's job — call through executor.CallProvider, which stamps it from
//     {run_id, node_id, attempt_group} so a retry cannot become a second charge. Calling Complete
//     directly with no key gets retries with NO de-duplication, which is a real way to be billed
//     twice; internal/executor's TestCallProvider_ForcedRetryProducesExactlyOneCharge is the proof
//     that the wired path does not.
//   - TODO: streaming through Gateway. Complete is unary; ChatCompletionStream remains
//     OpenAI-specific and unnormalized, so a streaming node cannot yet swap providers.
package providergateway
