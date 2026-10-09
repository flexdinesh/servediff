import type { ApiRepositoryDiff } from "@servediff/api";
export type DiffMode = ApiRepositoryDiff["mode"];

export function isDiffMode(value: unknown): value is DiffMode {
  return value === "all" || value === "staged" || value === "unstaged";
}

export type {
  ApiChangedFile as ChangedFile,
  ApiRepositoryDiff as RepositoryDiff,
  ApiFilePatch as FilePatch,
} from "@servediff/api";

export * from "./review.ts";
