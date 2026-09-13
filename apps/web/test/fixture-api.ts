import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import type { DiffMode, RepositoryDiff } from "@servediff/shared";
import type { Plugin } from "vite";
import { handleApi, respondWithProblem } from "../../server/src/api.ts";
import { ReviewStore } from "../../server/src/review-store.ts";
import { openPatch } from "../../server/src/stdin.ts";

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
