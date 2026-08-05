import { withSession, isResponse, forward } from "@/lib/bff";

/** P10 studio — diff two prompt versions (task 4.2). Both ids are subjects the platform scopes. */
export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const params = new URL(request.url).searchParams;
  return forward(context, context.paths.promptDiff(params.get("a") ?? "", params.get("b") ?? ""));
}
