import { api, errorDetail } from "@servediff/api";
import { useEffect } from "react";
import { useAppState } from "./app-state.tsx";
import { capabilityEnabled } from "./session-context.tsx";
import { registerReviewTools, type ReviewToolsClient } from "./webmcp.ts";

const reviewToolsClient: ReviewToolsClient = {
  async getComments(includeResolved, signal) {
    const { data, error } = await api.GET("/api/v1/review/comments", {
      params: { query: { includeResolved } },
      signal,
    });
    if (data === undefined)
      throw new Error(errorDetail(error, "Unable to load review comments"));
    return data;
  },

  async resolveComment(commentId, signal) {
    const { data, error } = await api.POST(
      "/api/v1/review/comments/{commentId}/resolve",
      {
        params: { path: { commentId } },
        signal,
      },
    );
    if (data === undefined)
      throw new Error(errorDetail(error, "Unable to resolve review comment"));
    return data;
  },
};

export function WebMCPTools() {
  const { capabilities, review } = useAppState();
  const enabled = capabilityEnabled(capabilities.review.comments);
  const markResolved = review.markResolved;

  useEffect(() => {
    const modelContext =
      typeof document.modelContext?.registerTool === "function"
        ? document.modelContext
        : undefined;
    const registration = registerReviewTools(
      modelContext,
      enabled,
      reviewToolsClient,
      markResolved,
      (error) => console.warn("Unable to register WebMCP review tools", error),
    );
    return () => registration?.abort();
  }, [enabled, markResolved]);

  return null;
}
