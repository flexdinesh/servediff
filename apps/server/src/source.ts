import type {
  ChangedFile,
  DiffMode,
  FilePatch,
  RepositoryDiff,
} from "@servediff/shared";

export interface DiffSource {
  root: string;
  kind: "local" | "stdin";
  scopes: readonly DiffMode[];
  live: boolean;
  snapshot(mode: DiffMode): Promise<RepositoryDiff>;
  patch(
    mode: DiffMode,
    file: ChangedFile,
    head: string | null,
  ): Promise<FilePatch>;
  contents(
    mode: DiffMode,
    file: ChangedFile,
    head: string | null,
  ): Promise<{ before: string; after: string }>;
}

export class RequestError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}
