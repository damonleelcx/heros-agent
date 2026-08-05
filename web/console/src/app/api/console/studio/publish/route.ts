import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * P10 studio — publish a new prompt version (task 4.3).
 *
 * The action is "Save as new version", never "Save": publishing is immutable and content-addressed, so
 * an edit creates a new version and never mutates one. The body is customer content — forwarded as
 * received, never logged or inspected. Tenant scope is the session's, applied by the platform.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body: unknown = await request.json().catch(() => null);
  return forward(context, context.paths.publishPrompt(), { method: "POST", body });
}
