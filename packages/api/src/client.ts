import createClient from "openapi-fetch";
import type { components, paths } from "./schema.d.ts";

const TOKEN_KEY = "servediff:api-token";

function token() {
  if (typeof window === "undefined") return null;
  try {
    const fragmentToken = new URLSearchParams(location.hash.slice(1)).get(
      "token",
    );
    if (fragmentToken) {
      sessionStorage.setItem(TOKEN_KEY, fragmentToken);
      history.replaceState(null, "", `${location.pathname}${location.search}`);
      return fragmentToken;
    }
    return sessionStorage.getItem(TOKEN_KEY);
  } catch {
    return null;
  }
}

export function createApiClient({
  baseUrl,
  token,
}: {
  baseUrl?: string;
  token?: string;
} = {}) {
  return createClient<paths>({
    ...(baseUrl === undefined ? {} : { baseUrl }),
    ...(token === undefined
      ? {}
      : { headers: { Authorization: `Bearer ${token}` } }),
  });
}

export const api = createApiClient();

api.use({
  onRequest({ request }) {
    const value = token();
    if (value) request.headers.set("Authorization", `Bearer ${value}`);
    return request;
  },
});

export type ApiSession = components["schemas"]["Session"];
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
