import type { AxisCoverageView } from "@/lib/types.generated";

/**
 * PREVIEW_COVERAGE is the engine's own coverage table, snapshotted for the browser-checkable preview.
 *
 * It is a snapshot rather than a live fetch because /preview has no session and no BFF. The ACTUAL
 * surface (/app/coverage) reads the platform, so a stale snapshot here can only make the preview look
 * wrong — never the product.
 */
export const PREVIEW_COVERAGE: AxisCoverageView = {
  "version": "cov-d7638d20",
  "languages": [
    "go",
    "java",
    "javascript",
    "kotlin",
    "python",
    "rust",
    "typescript"
  ],
  "axes": [
    "model",
    "prompt",
    "skills",
    "tools",
    "context",
    "wiring"
  ],
  "causes": [
    {
      "id": "not-expressible-at-a-call-site",
      "owner": "nobody",
      "label": "Not expressible in source — the value does not exist until run time, in any language."
    },
    {
      "id": "call-site-cannot-carry-it",
      "owner": "you",
      "label": "This call site cannot carry it — its own source has nothing to change."
    },
    {
      "id": "no-materializer-for-this-language",
      "owner": "the platform",
      "label": "Not yet applied by the platform — the missing artifact is named."
    }
  ],
  "cells": [
    {
      "axis": "context",
      "language": "go",
      "form": "full",
      "status": "materializes",
      "note": "equivalent to the un-rewritten call site; nothing is deleted"
    },
    {
      "axis": "context",
      "language": "go",
      "form": "full-history",
      "status": "materializes",
      "note": "equivalent to the un-rewritten call site; nothing is deleted"
    },
    {
      "axis": "context",
      "language": "go",
      "form": "hierarchical-summary",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by CALLING a summarizer model once per tier, host-side through HostServices, at run time; its tiers are model output, not source the author wrote"
    },
    {
      "axis": "context",
      "language": "go",
      "form": "rag-retrieval",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by RETRIEVING chunks for the live query at run time, host-side; the retrieved chunks are a function of the conversation, not of the source, so there is nothing at the call site to materialize them from"
    },
    {
      "axis": "context",
      "language": "go",
      "form": "semantic-compaction",
      "status": "materializes",
      "note": "materialized by deleting the turns the policy does not retain"
    },
    {
      "axis": "context",
      "language": "go",
      "form": "sliding-window",
      "status": "materializes",
      "note": "materialized by deleting the turns the policy does not retain"
    },
    {
      "axis": "context",
      "language": "go",
      "form": "structured-extraction",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "rewrites message CONTENT into an extracted structure, which means CONSTRUCTING new message values rather than selecting among the ones the call site wrote; a constructed message whose shape this engine guessed is the failure mode with no downstream net"
    },
    {
      "axis": "context",
      "language": "go",
      "form": "summarization",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by CALLING a summarizer model, host-side through HostServices, at run time; there is no summary in the source to select, and writing one in would freeze a model's output into the diff and claim a provider call this engine never made"
    },
    {
      "axis": "context",
      "language": "java",
      "form": "full",
      "status": "materializes",
      "note": "equivalent to the un-rewritten call site; nothing is deleted"
    },
    {
      "axis": "context",
      "language": "java",
      "form": "full-history",
      "status": "materializes",
      "note": "equivalent to the un-rewritten call site; nothing is deleted"
    },
    {
      "axis": "context",
      "language": "java",
      "form": "hierarchical-summary",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by CALLING a summarizer model once per tier, host-side through HostServices, at run time; its tiers are model output, not source the author wrote"
    },
    {
      "axis": "context",
      "language": "java",
      "form": "rag-retrieval",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by RETRIEVING chunks for the live query at run time, host-side; the retrieved chunks are a function of the conversation, not of the source, so there is nothing at the call site to materialize them from"
    },
    {
      "axis": "context",
      "language": "java",
      "form": "semantic-compaction",
      "status": "materializes",
      "note": "materialized by deleting the turns the policy does not retain"
    },
    {
      "axis": "context",
      "language": "java",
      "form": "sliding-window",
      "status": "materializes",
      "note": "materialized by deleting the turns the policy does not retain"
    },
    {
      "axis": "context",
      "language": "java",
      "form": "structured-extraction",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "rewrites message CONTENT into an extracted structure, which means CONSTRUCTING new message values rather than selecting among the ones the call site wrote; a constructed message whose shape this engine guessed is the failure mode with no downstream net"
    },
    {
      "axis": "context",
      "language": "java",
      "form": "summarization",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by CALLING a summarizer model, host-side through HostServices, at run time; there is no summary in the source to select, and writing one in would freeze a model's output into the diff and claim a provider call this engine never made"
    },
    {
      "axis": "context",
      "language": "javascript",
      "form": "full",
      "status": "materializes",
      "note": "equivalent to the un-rewritten call site; nothing is deleted"
    },
    {
      "axis": "context",
      "language": "javascript",
      "form": "full-history",
      "status": "materializes",
      "note": "equivalent to the un-rewritten call site; nothing is deleted"
    },
    {
      "axis": "context",
      "language": "javascript",
      "form": "hierarchical-summary",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by CALLING a summarizer model once per tier, host-side through HostServices, at run time; its tiers are model output, not source the author wrote"
    },
    {
      "axis": "context",
      "language": "javascript",
      "form": "rag-retrieval",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by RETRIEVING chunks for the live query at run time, host-side; the retrieved chunks are a function of the conversation, not of the source, so there is nothing at the call site to materialize them from"
    },
    {
      "axis": "context",
      "language": "javascript",
      "form": "semantic-compaction",
      "status": "materializes",
      "note": "materialized by deleting the turns the policy does not retain"
    },
    {
      "axis": "context",
      "language": "javascript",
      "form": "sliding-window",
      "status": "materializes",
      "note": "materialized by deleting the turns the policy does not retain"
    },
    {
      "axis": "context",
      "language": "javascript",
      "form": "structured-extraction",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "rewrites message CONTENT into an extracted structure, which means CONSTRUCTING new message values rather than selecting among the ones the call site wrote; a constructed message whose shape this engine guessed is the failure mode with no downstream net"
    },
    {
      "axis": "context",
      "language": "javascript",
      "form": "summarization",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by CALLING a summarizer model, host-side through HostServices, at run time; there is no summary in the source to select, and writing one in would freeze a model's output into the diff and claim a provider call this engine never made"
    },
    {
      "axis": "context",
      "language": "kotlin",
      "form": "full",
      "status": "materializes",
      "note": "equivalent to the un-rewritten call site; nothing is deleted"
    },
    {
      "axis": "context",
      "language": "kotlin",
      "form": "full-history",
      "status": "materializes",
      "note": "equivalent to the un-rewritten call site; nothing is deleted"
    },
    {
      "axis": "context",
      "language": "kotlin",
      "form": "hierarchical-summary",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by CALLING a summarizer model once per tier, host-side through HostServices, at run time; its tiers are model output, not source the author wrote"
    },
    {
      "axis": "context",
      "language": "kotlin",
      "form": "rag-retrieval",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by RETRIEVING chunks for the live query at run time, host-side; the retrieved chunks are a function of the conversation, not of the source, so there is nothing at the call site to materialize them from"
    },
    {
      "axis": "context",
      "language": "kotlin",
      "form": "semantic-compaction",
      "status": "materializes",
      "note": "materialized by deleting the turns the policy does not retain"
    },
    {
      "axis": "context",
      "language": "kotlin",
      "form": "sliding-window",
      "status": "materializes",
      "note": "materialized by deleting the turns the policy does not retain"
    },
    {
      "axis": "context",
      "language": "kotlin",
      "form": "structured-extraction",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "rewrites message CONTENT into an extracted structure, which means CONSTRUCTING new message values rather than selecting among the ones the call site wrote; a constructed message whose shape this engine guessed is the failure mode with no downstream net"
    },
    {
      "axis": "context",
      "language": "kotlin",
      "form": "summarization",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by CALLING a summarizer model, host-side through HostServices, at run time; there is no summary in the source to select, and writing one in would freeze a model's output into the diff and claim a provider call this engine never made"
    },
    {
      "axis": "context",
      "language": "python",
      "form": "full",
      "status": "materializes",
      "note": "equivalent to the un-rewritten call site; nothing is deleted"
    },
    {
      "axis": "context",
      "language": "python",
      "form": "full-history",
      "status": "materializes",
      "note": "equivalent to the un-rewritten call site; nothing is deleted"
    },
    {
      "axis": "context",
      "language": "python",
      "form": "hierarchical-summary",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by CALLING a summarizer model once per tier, host-side through HostServices, at run time; its tiers are model output, not source the author wrote"
    },
    {
      "axis": "context",
      "language": "python",
      "form": "rag-retrieval",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by RETRIEVING chunks for the live query at run time, host-side; the retrieved chunks are a function of the conversation, not of the source, so there is nothing at the call site to materialize them from"
    },
    {
      "axis": "context",
      "language": "python",
      "form": "semantic-compaction",
      "status": "materializes",
      "note": "materialized by deleting the turns the policy does not retain"
    },
    {
      "axis": "context",
      "language": "python",
      "form": "sliding-window",
      "status": "materializes",
      "note": "materialized by deleting the turns the policy does not retain"
    },
    {
      "axis": "context",
      "language": "python",
      "form": "structured-extraction",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "rewrites message CONTENT into an extracted structure, which means CONSTRUCTING new message values rather than selecting among the ones the call site wrote; a constructed message whose shape this engine guessed is the failure mode with no downstream net"
    },
    {
      "axis": "context",
      "language": "python",
      "form": "summarization",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by CALLING a summarizer model, host-side through HostServices, at run time; there is no summary in the source to select, and writing one in would freeze a model's output into the diff and claim a provider call this engine never made"
    },
    {
      "axis": "context",
      "language": "rust",
      "form": "full",
      "status": "materializes",
      "note": "equivalent to the un-rewritten call site; nothing is deleted"
    },
    {
      "axis": "context",
      "language": "rust",
      "form": "full-history",
      "status": "materializes",
      "note": "equivalent to the un-rewritten call site; nothing is deleted"
    },
    {
      "axis": "context",
      "language": "rust",
      "form": "hierarchical-summary",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by CALLING a summarizer model once per tier, host-side through HostServices, at run time; its tiers are model output, not source the author wrote"
    },
    {
      "axis": "context",
      "language": "rust",
      "form": "rag-retrieval",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by RETRIEVING chunks for the live query at run time, host-side; the retrieved chunks are a function of the conversation, not of the source, so there is nothing at the call site to materialize them from"
    },
    {
      "axis": "context",
      "language": "rust",
      "form": "semantic-compaction",
      "status": "materializes",
      "note": "materialized by deleting the turns the policy does not retain"
    },
    {
      "axis": "context",
      "language": "rust",
      "form": "sliding-window",
      "status": "materializes",
      "note": "materialized by deleting the turns the policy does not retain"
    },
    {
      "axis": "context",
      "language": "rust",
      "form": "structured-extraction",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "rewrites message CONTENT into an extracted structure, which means CONSTRUCTING new message values rather than selecting among the ones the call site wrote; a constructed message whose shape this engine guessed is the failure mode with no downstream net"
    },
    {
      "axis": "context",
      "language": "rust",
      "form": "summarization",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by CALLING a summarizer model, host-side through HostServices, at run time; there is no summary in the source to select, and writing one in would freeze a model's output into the diff and claim a provider call this engine never made"
    },
    {
      "axis": "context",
      "language": "typescript",
      "form": "full",
      "status": "materializes",
      "note": "equivalent to the un-rewritten call site; nothing is deleted"
    },
    {
      "axis": "context",
      "language": "typescript",
      "form": "full-history",
      "status": "materializes",
      "note": "equivalent to the un-rewritten call site; nothing is deleted"
    },
    {
      "axis": "context",
      "language": "typescript",
      "form": "hierarchical-summary",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by CALLING a summarizer model once per tier, host-side through HostServices, at run time; its tiers are model output, not source the author wrote"
    },
    {
      "axis": "context",
      "language": "typescript",
      "form": "rag-retrieval",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by RETRIEVING chunks for the live query at run time, host-side; the retrieved chunks are a function of the conversation, not of the source, so there is nothing at the call site to materialize them from"
    },
    {
      "axis": "context",
      "language": "typescript",
      "form": "semantic-compaction",
      "status": "materializes",
      "note": "materialized by deleting the turns the policy does not retain"
    },
    {
      "axis": "context",
      "language": "typescript",
      "form": "sliding-window",
      "status": "materializes",
      "note": "materialized by deleting the turns the policy does not retain"
    },
    {
      "axis": "context",
      "language": "typescript",
      "form": "structured-extraction",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "rewrites message CONTENT into an extracted structure, which means CONSTRUCTING new message values rather than selecting among the ones the call site wrote; a constructed message whose shape this engine guessed is the failure mode with no downstream net"
    },
    {
      "axis": "context",
      "language": "typescript",
      "form": "summarization",
      "status": "refuses",
      "cause": "not-expressible-at-a-call-site",
      "note": "assembles by CALLING a summarizer model, host-side through HostServices, at run time; there is no summary in the source to select, and writing one in would freeze a model's output into the diff and claim a provider call this engine never made"
    },
    {
      "axis": "model",
      "language": "go",
      "form": "anthropic.messages.new",
      "status": "materializes",
      "note": "bound at a field of the options value"
    },
    {
      "axis": "model",
      "language": "go",
      "form": "anthropic.messages.newstreaming",
      "status": "materializes",
      "note": "bound at a field of the options value"
    },
    {
      "axis": "model",
      "language": "go",
      "form": "bedrock.converse",
      "status": "materializes",
      "note": "bound at a field of the options value"
    },
    {
      "axis": "model",
      "language": "go",
      "form": "bedrock.conversestream",
      "status": "materializes",
      "note": "bound at a field of the options value"
    },
    {
      "axis": "model",
      "language": "go",
      "form": "bedrock.invokemodel",
      "status": "materializes",
      "note": "bound at a field of the options value"
    },
    {
      "axis": "model",
      "language": "go",
      "form": "langchaingo.generatefromsingleprompt",
      "status": "materializes",
      "note": "bound at an option constructor"
    },
    {
      "axis": "model",
      "language": "go",
      "form": "langchaingo.model.generatecontent",
      "status": "materializes",
      "note": "bound at an option constructor"
    },
    {
      "axis": "model",
      "language": "go",
      "form": "openai.chat.completions.new",
      "status": "materializes",
      "note": "bound at a field of the options value"
    },
    {
      "axis": "model",
      "language": "go",
      "form": "openai.chat.completions.newstreaming",
      "status": "materializes",
      "note": "bound at a field of the options value"
    },
    {
      "axis": "model",
      "language": "go",
      "form": "sashabaranov.createchatcompletion",
      "status": "materializes",
      "note": "bound at a field of the options value"
    },
    {
      "axis": "model",
      "language": "go",
      "form": "sashabaranov.createchatcompletionstream",
      "status": "materializes",
      "note": "bound at a field of the options value"
    },
    {
      "axis": "model",
      "language": "java",
      "form": "java.bedrock.converse",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no model at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "model",
      "language": "java",
      "form": "java.langchain4j.chat",
      "status": "materializes",
      "note": "bound at a builder-chain call"
    },
    {
      "axis": "model",
      "language": "java",
      "form": "java.langchain4j.generate",
      "status": "materializes",
      "note": "bound at a builder-chain call"
    },
    {
      "axis": "model",
      "language": "java",
      "form": "java.springai.call",
      "status": "materializes",
      "note": "bound at a builder-chain call"
    },
    {
      "axis": "model",
      "language": "javascript",
      "form": "js.anthropic.messages.create",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "model",
      "language": "javascript",
      "form": "js.langchain.invoke",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no model at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "model",
      "language": "javascript",
      "form": "js.openai.chat.completions.create",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "model",
      "language": "javascript",
      "form": "js.openai.responses.create",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "model",
      "language": "javascript",
      "form": "js.vercel.generatetext",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no model at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "model",
      "language": "javascript",
      "form": "js.vercel.streamtext",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no model at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "model",
      "language": "kotlin",
      "form": "kt.bedrock.converse",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no model at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "model",
      "language": "kotlin",
      "form": "kt.bedrock.kotlin.converse",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no model at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "model",
      "language": "kotlin",
      "form": "kt.langchain4j.chat",
      "status": "materializes",
      "note": "bound at a builder-chain call"
    },
    {
      "axis": "model",
      "language": "kotlin",
      "form": "kt.langchain4j.generate",
      "status": "materializes",
      "note": "bound at a builder-chain call"
    },
    {
      "axis": "model",
      "language": "kotlin",
      "form": "kt.springai.call",
      "status": "materializes",
      "note": "bound at a builder-chain call"
    },
    {
      "axis": "model",
      "language": "python",
      "form": "py.anthropic.messages.create",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "model",
      "language": "python",
      "form": "py.bedrock.converse",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "model",
      "language": "python",
      "form": "py.bedrock.invoke_model",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "model",
      "language": "python",
      "form": "py.crewai.kickoff",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no model at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "model",
      "language": "python",
      "form": "py.langchain.chatanthropic.invoke",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no model at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "model",
      "language": "python",
      "form": "py.langchain.chatopenai.invoke",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no model at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "model",
      "language": "python",
      "form": "py.langchain.invoke",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no model at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "model",
      "language": "python",
      "form": "py.langgraph.invoke",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no model at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "model",
      "language": "python",
      "form": "py.openai.chat.completions.create",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "model",
      "language": "rust",
      "form": "rs.anthropic.complete",
      "status": "materializes",
      "note": "bound at a field of the request value"
    },
    {
      "axis": "model",
      "language": "rust",
      "form": "rs.anthropic.messages.create",
      "status": "materializes",
      "note": "bound at a field of the request value"
    },
    {
      "axis": "model",
      "language": "rust",
      "form": "rs.async_openai.completions.create",
      "status": "materializes",
      "note": "bound at a field of the request value"
    },
    {
      "axis": "model",
      "language": "rust",
      "form": "rs.async_openai.create",
      "status": "materializes",
      "note": "bound at a field of the request value"
    },
    {
      "axis": "model",
      "language": "typescript",
      "form": "ts.anthropic.messages.create",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "model",
      "language": "typescript",
      "form": "ts.langchain.invoke",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no model at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "model",
      "language": "typescript",
      "form": "ts.openai.chat.completions.create",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "model",
      "language": "typescript",
      "form": "ts.openai.responses.create",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "model",
      "language": "typescript",
      "form": "ts.vercel.generatetext",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no model at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "model",
      "language": "typescript",
      "form": "ts.vercel.streamtext",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no model at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "prompt",
      "language": "go",
      "form": "anthropic.messages.new",
      "status": "materializes",
      "note": "bound at a field of the options value"
    },
    {
      "axis": "prompt",
      "language": "go",
      "form": "anthropic.messages.newstreaming",
      "status": "materializes",
      "note": "bound at a field of the options value"
    },
    {
      "axis": "prompt",
      "language": "go",
      "form": "bedrock.converse",
      "status": "materializes",
      "note": "bound at a field of the options value"
    },
    {
      "axis": "prompt",
      "language": "go",
      "form": "bedrock.conversestream",
      "status": "materializes",
      "note": "bound at a field of the options value"
    },
    {
      "axis": "prompt",
      "language": "go",
      "form": "bedrock.invokemodel",
      "status": "materializes",
      "note": "bound at an opaque serialized body"
    },
    {
      "axis": "prompt",
      "language": "go",
      "form": "langchaingo.generatefromsingleprompt",
      "status": "materializes",
      "note": "bound at a positional argument"
    },
    {
      "axis": "prompt",
      "language": "go",
      "form": "langchaingo.model.generatecontent",
      "status": "materializes",
      "note": "bound at a positional argument"
    },
    {
      "axis": "prompt",
      "language": "go",
      "form": "openai.chat.completions.new",
      "status": "materializes",
      "note": "bound at a field of the options value"
    },
    {
      "axis": "prompt",
      "language": "go",
      "form": "openai.chat.completions.newstreaming",
      "status": "materializes",
      "note": "bound at a field of the options value"
    },
    {
      "axis": "prompt",
      "language": "go",
      "form": "sashabaranov.createchatcompletion",
      "status": "materializes",
      "note": "bound at a field of the options value"
    },
    {
      "axis": "prompt",
      "language": "go",
      "form": "sashabaranov.createchatcompletionstream",
      "status": "materializes",
      "note": "bound at a field of the options value"
    },
    {
      "axis": "prompt",
      "language": "java",
      "form": "java.bedrock.converse",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no prompt at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "prompt",
      "language": "java",
      "form": "java.langchain4j.chat",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no prompt at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "prompt",
      "language": "java",
      "form": "java.langchain4j.generate",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no prompt at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "prompt",
      "language": "java",
      "form": "java.springai.call",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no prompt at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "prompt",
      "language": "javascript",
      "form": "js.anthropic.messages.create",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "prompt",
      "language": "javascript",
      "form": "js.langchain.invoke",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "prompt",
      "language": "javascript",
      "form": "js.openai.chat.completions.create",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "prompt",
      "language": "javascript",
      "form": "js.openai.responses.create",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no prompt at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "prompt",
      "language": "javascript",
      "form": "js.vercel.generatetext",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "prompt",
      "language": "javascript",
      "form": "js.vercel.streamtext",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "prompt",
      "language": "kotlin",
      "form": "kt.bedrock.converse",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no prompt at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "prompt",
      "language": "kotlin",
      "form": "kt.bedrock.kotlin.converse",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no prompt at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "prompt",
      "language": "kotlin",
      "form": "kt.langchain4j.chat",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no prompt at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "prompt",
      "language": "kotlin",
      "form": "kt.langchain4j.generate",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no prompt at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "prompt",
      "language": "kotlin",
      "form": "kt.springai.call",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no prompt at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "prompt",
      "language": "python",
      "form": "py.anthropic.messages.create",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "prompt",
      "language": "python",
      "form": "py.bedrock.converse",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no prompt at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "prompt",
      "language": "python",
      "form": "py.bedrock.invoke_model",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no prompt at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "prompt",
      "language": "python",
      "form": "py.crewai.kickoff",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no prompt at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "prompt",
      "language": "python",
      "form": "py.langchain.chatanthropic.invoke",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "prompt",
      "language": "python",
      "form": "py.langchain.chatopenai.invoke",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "prompt",
      "language": "python",
      "form": "py.langchain.invoke",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "prompt",
      "language": "python",
      "form": "py.langgraph.invoke",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "prompt",
      "language": "python",
      "form": "py.openai.chat.completions.create",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "prompt",
      "language": "rust",
      "form": "rs.anthropic.complete",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no prompt at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "prompt",
      "language": "rust",
      "form": "rs.anthropic.messages.create",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no prompt at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "prompt",
      "language": "rust",
      "form": "rs.async_openai.completions.create",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no prompt at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "prompt",
      "language": "rust",
      "form": "rs.async_openai.create",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no prompt at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "prompt",
      "language": "typescript",
      "form": "ts.anthropic.messages.create",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "prompt",
      "language": "typescript",
      "form": "ts.langchain.invoke",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "prompt",
      "language": "typescript",
      "form": "ts.openai.chat.completions.create",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "prompt",
      "language": "typescript",
      "form": "ts.openai.responses.create",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this row's SDK states no prompt at a locatable binding site; an arg_map entry naming where it binds would materialize it"
    },
    {
      "axis": "prompt",
      "language": "typescript",
      "form": "ts.vercel.generatetext",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "prompt",
      "language": "typescript",
      "form": "ts.vercel.streamtext",
      "status": "materializes",
      "note": "bound at a named argument"
    },
    {
      "axis": "skills",
      "language": "go",
      "form": "anthropic",
      "status": "materializes",
      "note": "anthropic-sdk-go v1 (the generation that drops the F() wrapper)"
    },
    {
      "axis": "skills",
      "language": "go",
      "form": "bedrock",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this SDK carries its tools inside an opaque serialized body (the registry row marks it opaque), so there is no tool value to construct or delete at the call site in any language"
    },
    {
      "axis": "skills",
      "language": "go",
      "form": "openai",
      "status": "materializes",
      "note": "openai-go v1 (the generation with ChatCompletionToolUnionParam)"
    },
    {
      "axis": "skills",
      "language": "java",
      "form": "anthropic",
      "status": "refuses",
      "cause": "no-materializer-for-this-language",
      "missing_artifact": "a tool-value spelling row for (java, anthropic) naming the SDK generation it targets",
      "note": "the sealed schema already supplies the SHAPE in every language; what is missing is how this SDK spells a tool list here"
    },
    {
      "axis": "skills",
      "language": "java",
      "form": "bedrock",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this SDK carries its tools inside an opaque serialized body (the registry row marks it opaque), so there is no tool value to construct or delete at the call site in any language"
    },
    {
      "axis": "skills",
      "language": "java",
      "form": "openai",
      "status": "refuses",
      "cause": "no-materializer-for-this-language",
      "missing_artifact": "a tool-value spelling row for (java, openai) naming the SDK generation it targets",
      "note": "the sealed schema already supplies the SHAPE in every language; what is missing is how this SDK spells a tool list here"
    },
    {
      "axis": "skills",
      "language": "javascript",
      "form": "anthropic",
      "status": "materializes",
      "note": "@anthropic-ai/sdk v0.2x (tools as objects with name/input_schema)"
    },
    {
      "axis": "skills",
      "language": "javascript",
      "form": "bedrock",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this SDK carries its tools inside an opaque serialized body (the registry row marks it opaque), so there is no tool value to construct or delete at the call site in any language"
    },
    {
      "axis": "skills",
      "language": "javascript",
      "form": "openai",
      "status": "materializes",
      "note": "openai-node v4 (tools as objects with type: 'function')"
    },
    {
      "axis": "skills",
      "language": "kotlin",
      "form": "anthropic",
      "status": "refuses",
      "cause": "no-materializer-for-this-language",
      "missing_artifact": "a tool-value spelling row for (kotlin, anthropic) naming the SDK generation it targets",
      "note": "the sealed schema already supplies the SHAPE in every language; what is missing is how this SDK spells a tool list here"
    },
    {
      "axis": "skills",
      "language": "kotlin",
      "form": "bedrock",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this SDK carries its tools inside an opaque serialized body (the registry row marks it opaque), so there is no tool value to construct or delete at the call site in any language"
    },
    {
      "axis": "skills",
      "language": "kotlin",
      "form": "openai",
      "status": "refuses",
      "cause": "no-materializer-for-this-language",
      "missing_artifact": "a tool-value spelling row for (kotlin, openai) naming the SDK generation it targets",
      "note": "the sealed schema already supplies the SHAPE in every language; what is missing is how this SDK spells a tool list here"
    },
    {
      "axis": "skills",
      "language": "python",
      "form": "anthropic",
      "status": "materializes",
      "note": "anthropic-sdk-python v0.3x (tools as dicts with name/input_schema)"
    },
    {
      "axis": "skills",
      "language": "python",
      "form": "bedrock",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this SDK carries its tools inside an opaque serialized body (the registry row marks it opaque), so there is no tool value to construct or delete at the call site in any language"
    },
    {
      "axis": "skills",
      "language": "python",
      "form": "openai",
      "status": "materializes",
      "note": "openai-python v1 (tools as dicts with type=function)"
    },
    {
      "axis": "skills",
      "language": "rust",
      "form": "anthropic",
      "status": "refuses",
      "cause": "no-materializer-for-this-language",
      "missing_artifact": "a tool-value spelling row for (rust, anthropic) naming the SDK generation it targets",
      "note": "the sealed schema already supplies the SHAPE in every language; what is missing is how this SDK spells a tool list here"
    },
    {
      "axis": "skills",
      "language": "rust",
      "form": "bedrock",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this SDK carries its tools inside an opaque serialized body (the registry row marks it opaque), so there is no tool value to construct or delete at the call site in any language"
    },
    {
      "axis": "skills",
      "language": "rust",
      "form": "openai",
      "status": "refuses",
      "cause": "no-materializer-for-this-language",
      "missing_artifact": "a tool-value spelling row for (rust, openai) naming the SDK generation it targets",
      "note": "the sealed schema already supplies the SHAPE in every language; what is missing is how this SDK spells a tool list here"
    },
    {
      "axis": "skills",
      "language": "typescript",
      "form": "anthropic",
      "status": "materializes",
      "note": "@anthropic-ai/sdk v0.2x (tools as objects with name/input_schema)"
    },
    {
      "axis": "skills",
      "language": "typescript",
      "form": "bedrock",
      "status": "refuses",
      "cause": "call-site-cannot-carry-it",
      "note": "this SDK carries its tools inside an opaque serialized body (the registry row marks it opaque), so there is no tool value to construct or delete at the call site in any language"
    },
    {
      "axis": "skills",
      "language": "typescript",
      "form": "openai",
      "status": "materializes",
      "note": "openai-node v4 (tools as objects with type: 'function')"
    },
    {
      "axis": "tools",
      "language": "go",
      "form": "a statically-written tool list",
      "status": "materializes",
      "note": "a tool the frontend recorded with a declaration location is deleted; one recorded without a location refuses"
    },
    {
      "axis": "tools",
      "language": "java",
      "form": "a statically-written tool list",
      "status": "materializes",
      "note": "a tool the frontend recorded with a declaration location is deleted; one recorded without a location refuses"
    },
    {
      "axis": "tools",
      "language": "javascript",
      "form": "a statically-written tool list",
      "status": "materializes",
      "note": "a tool the frontend recorded with a declaration location is deleted; one recorded without a location refuses"
    },
    {
      "axis": "tools",
      "language": "kotlin",
      "form": "a statically-written tool list",
      "status": "materializes",
      "note": "a tool the frontend recorded with a declaration location is deleted; one recorded without a location refuses"
    },
    {
      "axis": "tools",
      "language": "python",
      "form": "a statically-written tool list",
      "status": "materializes",
      "note": "a tool the frontend recorded with a declaration location is deleted; one recorded without a location refuses"
    },
    {
      "axis": "tools",
      "language": "rust",
      "form": "a statically-written tool list",
      "status": "materializes",
      "note": "a tool the frontend recorded with a declaration location is deleted; one recorded without a location refuses"
    },
    {
      "axis": "tools",
      "language": "typescript",
      "form": "a statically-written tool list",
      "status": "materializes",
      "note": "a tool the frontend recorded with a declaration location is deleted; one recorded without a location refuses"
    },
    {
      "axis": "wiring",
      "language": "go",
      "form": "an adjacent statement transposition",
      "status": "materializes",
      "note": "a single transposition of two adjacent sibling statements; every other shape refuses by shape, in every language"
    },
    {
      "axis": "wiring",
      "language": "java",
      "form": "an adjacent statement transposition",
      "status": "materializes",
      "note": "a single transposition of two adjacent sibling statements; every other shape refuses by shape, in every language"
    },
    {
      "axis": "wiring",
      "language": "javascript",
      "form": "an adjacent statement transposition",
      "status": "materializes",
      "note": "a single transposition of two adjacent sibling statements; every other shape refuses by shape, in every language"
    },
    {
      "axis": "wiring",
      "language": "kotlin",
      "form": "an adjacent statement transposition",
      "status": "materializes",
      "note": "a single transposition of two adjacent sibling statements; every other shape refuses by shape, in every language"
    },
    {
      "axis": "wiring",
      "language": "python",
      "form": "an adjacent statement transposition",
      "status": "materializes",
      "note": "a single transposition of two adjacent sibling statements; every other shape refuses by shape, in every language"
    },
    {
      "axis": "wiring",
      "language": "rust",
      "form": "an adjacent statement transposition",
      "status": "materializes",
      "note": "a single transposition of two adjacent sibling statements; every other shape refuses by shape, in every language"
    },
    {
      "axis": "wiring",
      "language": "typescript",
      "form": "an adjacent statement transposition",
      "status": "materializes",
      "note": "a single transposition of two adjacent sibling statements; every other shape refuses by shape, in every language"
    }
  ]
};
