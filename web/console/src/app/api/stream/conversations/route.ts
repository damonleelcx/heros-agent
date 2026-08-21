import { withSession, isResponse } from "@/lib/bff";
import { openPlatformStream } from "@/lib/platformApi";

/**
 * The SSE proxy for a conversation (P31 FR5/FR6).
 *
 * # Why this is a proxy and not a re-implementation, restated because SSE punishes the difference
 *
 * The upstream body is handed to the client body directly, so each chunk the platform flushes is a
 * chunk the browser receives. Nothing here reads an event, parses one, buffers, or waits for a second.
 * **An SSE proxy that coalesces is worse than no SSE at all**: the client believes it is streaming while
 * the screen updates in bursts, nothing errors, and the failure is indistinguishable from slowness at
 * the application layer — which is design.md D3's stated hazard and the reason the edge configuration is
 * asserted rather than assumed.
 *
 * # The acknowledgement cursor passes through, and nothing else does
 *
 * `after` is the id of the last message the client processed. It is the ONLY thing the browser may tell
 * the server about the state of the world (FR21). Phase, remaining budget and completed steps arrive in
 * the stream's own `state` frame, read from the run — so a client that lied about `after` gets a
 * different slice of the transcript and an identical answer about everything else.
 *
 * # `X-Accel-Buffering: no`
 *
 * A request, not a guarantee. `deploy/k8s/overlays/prod/ingress.yaml` carries the matching annotation
 * and `make console-edge-proof` asserts incremental arrival against the running edge, because a header
 * is a request and an assertion is a fact.
 */
export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;

  const url = new URL(request.url);
  const conversationId = url.searchParams.get("conversation_id") ?? "";
  if (!conversationId) {
    return Response.json(
      { error: "a stream names the conversation it is for", kind: "not-found" },
      { status: 400, headers: { "cache-control": "no-store" } },
    );
  }
  // A malformed cursor becomes 0 — replay everything — rather than an error. Replaying more than
  // necessary costs a repaint; refusing the stream costs the user their run.
  const parsed = Number.parseInt(url.searchParams.get("after") ?? "0", 10);
  const after = Number.isFinite(parsed) && parsed > 0 ? parsed : 0;

  const upstream = await openPlatformStream(context.paths.conversationStream(conversationId, after), {
    tenantId: context.session.tenantId,
    userId: context.session.userId,
    // When the browser closes the EventSource, the upstream stream closes with it. Without this, a
    // person who opens and closes a conversation twenty times leaves twenty upstream streams alive.
    //
    // 🔴 This closes the STREAM, not the RUN. FR7: a closed tab does not cancel a run — the turn is on a
    // detached context platform-side and its messages remain retrievable on reconnect.
    signal: request.signal,
  });

  if (!upstream.ok) {
    const status = upstream.kind === "transport" ? 502 : upstream.status;
    return Response.json(
      { error: upstream.error, kind: upstream.kind },
      { status, headers: { "cache-control": "no-store" } },
    );
  }
  if (!upstream.response.body) {
    // A 200 with no body is not a stream. Saying so beats handing the client an EventSource that will
    // never emit and never fail — which is the one failure mode a person cannot distinguish from a
    // question that is simply taking a while.
    return Response.json(
      { error: "the platform returned an empty stream", kind: "transport" },
      { status: 502, headers: { "cache-control": "no-store" } },
    );
  }

  return new Response(upstream.response.body, {
    status: 200,
    headers: {
      "content-type": "text/event-stream; charset=utf-8",
      "cache-control": "no-store, no-transform",
      connection: "keep-alive",
      "x-accel-buffering": "no",
    },
  });
}
