// Package providergateway is the unified LLM provider gateway for the
// Workflow Evaluation & Configuration System.
//
// It began as the OpenAI-compatible chat client salvaged from the retired
// interactive agent (internal/cliagent/openai.go and stream.go) and is the
// seed of the LiteLLM-style abstraction described in Phase P2
// (see openspec/changes/p2-config-runtime and docs/prd/P2-config-runtime.md):
// the Runtime loader resolves a Variant Spec's model_ref to a provider + model
// and calls through this gateway so provider swaps are transparent to the
// executor.
//
// Only the transport layer was carried over — the old agent's hardcoded tool
// catalog (OpenAITools/ToolOptions) was intentionally left behind; tool/skill
// schemas now come from the Skill Registry.
//
// TODO(P2): add per-request instrumentation hooks (latency/TTFT/tokens/cost)
// feeding the OpenTelemetry substrate (P2.5), retries with backoff, request
// timeouts, and additional provider adapters (Anthropic Messages, Bedrock
// Converse) behind this same interface.
package providergateway
