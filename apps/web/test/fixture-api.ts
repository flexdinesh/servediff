import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import type {
  ChangedFile,
  DiffMode,
  RepositoryDiff,
  ReviewComment,
} from "@servediff/shared";
import type { Plugin } from "vite";
import { handleApi, respondWithProblem } from "../../server/src/api.ts";
import { ReviewStore } from "../../server/src/review-store.ts";
import { openPatch } from "../../server/src/stdin.ts";

function fixtureFile(repository: RepositoryDiff, path: string) {
  const file = repository.files.find((entry) => entry.path === path);
  if (!file) throw new Error(`Missing fixture file: ${path}`);
  return file;
}

function fixtureComment(
  repository: RepositoryDiff,
  file: ChangedFile,
  comment: Omit<ReviewComment, "path" | "scope" | "fingerprint" | "origin">,
): ReviewComment {
  return {
    ...comment,
    path: file.path,
    scope: repository.mode,
    fingerprint: file.fingerprint,
    origin: {
      source: repository.source,
      repository: repository.name,
      branch: repository.branch,
      head: repository.head,
      revision: repository.revision,
      file: { status: file.status, oldPath: file.oldPath },
    },
  };
}

export async function seedFixtureReview(
  store: ReviewStore,
  sessionId: string,
  repository: RepositoryDiff,
) {
  const value = fixtureFile(repository, "src/value.ts");
  const badge = fixtureFile(repository, "src/components/Badge.tsx");
  const button = fixtureFile(repository, "src/components/Button.tsx");
  const legacy = fixtureFile(repository, "docs/legacy.md");
  await store.importComments(sessionId, [
    fixtureComment(repository, value, {
      id: "fixture-open-comment",
      side: "additions",
      start: 1,
      end: 1,
      code: "+ export const value = 2;",
      body: "Should this remain backwards-compatible with value 1?",
      status: "open",
      createdAt: Date.UTC(2026, 0, 1),
    }),
    fixtureComment(repository, badge, {
      id: "fixture-resolved-comment",
      side: "additions",
      start: 4,
      end: 4,
      code: "+   return <span>{label}</span>;",
      body: "Please expose the label as accessible text.",
      status: "resolved",
      createdAt: Date.UTC(2026, 0, 2),
    }),
    {
      id: "fixture-stale-comment",
      path: button.path,
      scope: repository.mode,
      fingerprint: "earlier-button-version",
      side: "additions",
      start: 3,
      end: 3,
      code: "+ export function Button({ children }: ButtonProps) {",
      body: "Keep the button API backwards-compatible.",
      status: "open",
      createdAt: Date.UTC(2025, 11, 31),
      origin: {
        source: repository.source,
        repository: repository.name,
        branch: repository.branch,
        head: repository.head,
        revision: "earlier-fixture-review",
        file: { status: button.status, oldPath: button.oldPath },
      },
    },
  ]);
  await store.putMark(sessionId, {
    fileId: legacy.id,
    fileVersion: legacy.fingerprint,
    scope: repository.mode,
  });
}

export async function fixtureApiPlugin(): Promise<Plugin> {
  const input = await readFile(
    new URL("../../../test/fixtures/sample.diff", import.meta.url),
    "utf8",
  );
  const source = openPatch(input);
  const store = new ReviewStore(null);
  const sessionId = createHash("sha256")
    .update(`fixture\0${source.root}`)
    .digest("hex");
  const snapshots = new Map<DiffMode, Promise<RepositoryDiff>>();
  const snapshot = (mode: DiffMode, fresh = false) => {
    if (fresh) return source.snapshot(mode);
    const current = snapshots.get(mode) ?? source.snapshot(mode);
    snapshots.set(mode, current);
    return current;
  };
  await seedFixtureReview(store, sessionId, await snapshot("all"));

  return {
    name: "servediff-fixture-api",
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        const url = new URL(request.url ?? "/", "http://localhost");
        void handleApi(request, response, url, {
          source,
          sessionId,
          token: "",
          store,
          snapshot,
        })
          .then((handled) => {
            if (!handled) next();
          })
          .catch((error: unknown) => respondWithProblem(response, error));
      });
    },
  };
}
