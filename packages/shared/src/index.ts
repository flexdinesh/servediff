export type DiffMode = "all" | "staged" | "unstaged";

export function isDiffMode(value: unknown): value is DiffMode {
  return value === "all" || value === "staged" || value === "unstaged";
}

export interface ChangedFile {
  id: string;
  path: string;
  oldPath: string | null;
  status: string;
  indexStatus: string;
  worktreeStatus: string;
  additions: number;
  deletions: number;
  binary: boolean;
  fingerprint: string;
  recreated?: boolean;
}

export interface RepositoryDiff {
  id: string;
  versionId: string;
  locationId: string | null;
  repositoryId: string | null;
  source: "local" | "stdin";
  root: string;
  name: string;
  branch: string;
  head: string | null;
  mode: DiffMode;
  files: ChangedFile[];
  revision: string;
}

export interface FilePatch {
  patch: string;
  message: string | null;
  contents?: { before: string; after: string };
}

export * from "./review.ts";
