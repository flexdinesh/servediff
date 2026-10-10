import { api, errorDetail } from "@diffx/api";
import { useEffect, useMemo } from "react";
import { useReviewState } from "./app-state.tsx";
import { capabilityEnabled, useSession } from "./session-context.tsx";
import { registerReviewTools, type ReviewToolsClient } from "./webmcp.ts";

export function WebMCPTools() {
  const { id: contextId, capabilities } = useSession();
  const { resolveComment } = useReviewState();
  const enabled = capabilityEnabled(capabilities.review.comments);
  const client = useMemo<ReviewToolsClient>(
    () => ({
      async getComments(includeResolved, signal) {
        const { data, error } = await api.GET(
          "/api/v2/contexts/{contextId}/review/comments",
          {
            params: { path: { contextId }, query: { includeResolved } },
            signal,
          },
        );
        if (data === undefined)
          throw new Error(errorDetail(error, "Unable to load review comments"));
        return data;
      },
      resolveComment,
    }),
    [contextId, resolveComment],
  );

  useEffect(() => {
    const modelContext =
      typeof document.modelContext?.registerTool === "function"
        ? document.modelContext
        : undefined;
    const registration = registerReviewTools(
      modelContext,
      enabled,
      client,
      () => {},
      (error) => console.warn("Unable to register WebMCP review tools", error),
    );
    return () => registration?.abort();
  }, [enabled, client]);

  return null;
}
