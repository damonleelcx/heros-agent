import { withSession, isResponse } from "@/lib/bff";
import { openPlatformStream } from "@/lib/platformApi";

/**
 * The SSE proxy for the live run monitor (FR7).
 *
 * # The three properties that make this a proxy rather than a re-implementation
 *
 * 1. **Flush semantics are preserved.** The upstream body is handed to the client body directly, so
 *    each chunk the platform flushes is a chunk the browser receives. Nothing here reads an event,
 *    parses it, or waits for a second one. An SSE proxy that coalesces is worse than no SSE at all:
 *    the client believes it is streaming while the screen updates in bursts, and the polling fallback
 *    never engages because messages ARE arriving.
 * 2. **Upstream close closes the client stream.** Piping the body through gives this for free — when
 *    the platform ends the stream because the run reached a terminal state, the browser's
 *    `EventSource` sees a closed connection rather than a hang.
 * 3. **A failure leaves the fallback available.** If the upstream refuses or is unreachable, this
 *    route returns an ordinary JSON error and holds no state. The client can then fall back to
 *    polling `…/monitor`, which is a different route with a different transport, so nothing about
 *    this one's failure prevents it.
 *
 * # Why `X-Accel-Buffering: no`
 *
 * A reverse proxy that buffers is precisely the condition the polling fallback exists to survive, and
 * this header is the standard request not to. It is belt and braces: if an intermediary buffers
 * anyway, the fallback engages, which is the design (Decision 4) rather than a workaround for it.
 */
export const dynamic = "force-dynamic";

export async function GET(request: Request, { params }: { params: Promise<{ runId: string }> }) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const { runId } = await params;

  const upstream = await openPlatformStream(context.paths.monitorStream(runId), {
    tenantId: context.session.tenantId,
    // When the browser closes the EventSource, the upstream stream is closed too. Without this, a user
    // who opens and closes a run monitor twenty times leaves twenty upstream streams alive.
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
    // A 200 with no body is not a stream. Saying so is better than handing the client an EventSource
    // that will never emit and never fail.
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
