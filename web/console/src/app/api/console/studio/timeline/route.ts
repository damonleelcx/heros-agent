import { withSession, isResponse, forward } from "@/lib/bff";

/** P10 studio — the version timeline for a prompt name (task 4.1). `?name=` is a subject, not scope. */
export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const name = new URL(request.url).searchParams.get("name") ?? "";
  return forward(context, context.paths.promptTimeline(name));
}
