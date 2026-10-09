import type { APIRequestContext } from "@playwright/test";

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

export async function resetFixtureState(request: APIRequestContext) {
  const catalog: unknown = await (await request.get("/api/v2/contexts")).json();
  if (!record(catalog) || !Array.isArray(catalog.contexts))
    throw new Error("Missing fixture catalog");
  const context: unknown = catalog.contexts[0];
  if (!record(context) || typeof context.id !== "string")
    throw new Error("Missing fixture context");
  const base = "/api/v2/contexts/" + encodeURIComponent(context.id);
  const response = await request.get(base + "/comments");
  const body: unknown = await response.json();
  if (record(body) && Array.isArray(body.comments)) {
    for (const comment of body.comments) {
      if (record(comment) && typeof comment.id === "string")
        await request.delete(
          `${base}/comments/${encodeURIComponent(comment.id)}`,
        );
    }
  }
  for (const scope of ["all", "staged", "unstaged"])
    await request.delete(`${base}/review-marks?scope=${scope}`);
}
