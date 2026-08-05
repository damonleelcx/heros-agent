import { withSession, isResponse, forward } from "@/lib/bff";
/** P10 matrix — the model catalog (rows). */
export const dynamic = "force-dynamic";
export async function GET(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  return forward(context, context.paths.studioModels());
}
