import createClient from "openapi-fetch";
import type { components, paths } from "./schema.d.ts";

export function createApiClient({ baseUrl }: { baseUrl?: string } = {}) {
  return createClient<paths>({
    ...(baseUrl === undefined ? {} : { baseUrl }),
  });
}

export const api = createApiClient();

export type ApiSession = components["schemas"]["Session"];
export type ApiServerMetrics = components["schemas"]["ServerMetrics"];
export type ApiRepositoryDiff = components["schemas"]["RepositoryDiff"];
export type ApiChangedFile = components["schemas"]["ChangedFile"];
export type ApiFilePatch = components["schemas"]["FilePatch"];
export type ApiFileContents = components["schemas"]["FileContents"];
export type ApiReviewComment = components["schemas"]["ReviewComment"];
export type ApiReviewMark = components["schemas"]["ReviewMark"];

export function errorDetail(error: unknown, fallback: string) {
  if (error instanceof Error) return error.message;
  return typeof error === "object" &&
    error !== null &&
    "detail" in error &&
    typeof error.detail === "string"
    ? error.detail
    : fallback;
}
