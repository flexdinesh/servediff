import { createHash, randomBytes } from "node:crypto";
import { createReadStream } from "node:fs";
import { readFile, stat } from "node:fs/promises";
import { createServer } from "node:http";
import { networkInterfaces } from "node:os";
import { extname, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import type { DiffMode, RepositoryDiff } from "@servediff/shared";
import { handleApi, respondWithProblem } from "./api.ts";
import { openRepository } from "./git.ts";
import { defaultReviewPath, ReviewStore } from "./review-store.ts";
import { RequestError } from "./source.ts";
import { openPatch } from "./stdin.ts";

const webRoot = fileURLToPath(new URL("../../web/", import.meta.url));
const mime: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".svg": "image/svg+xml",
  ".woff2": "font/woff2",
  ".wasm": "application/wasm",
  ".json": "application/json",
};

function localNetworkAddress() {
  for (const addresses of Object.values(networkInterfaces())) {
    for (const address of addresses ?? []) {
      if (address.family === "IPv4" && !address.internal)
        return address.address;
    }
  }
  return null;
}

export async function startServer(options: {
  directory: string;
  port: number;
  dev?: boolean;
  input?: string;
  token?: string;
  store?: ReviewStore;
}) {
  const source =
    options.input === undefined
      ? await openRepository(options.directory)
      : openPatch(options.input);
  const token = options.token ?? randomBytes(24).toString("hex");
  const sessionId = createHash("sha256")
    .update(`${source.kind}\0${source.root}`)
    .digest("hex");
  const store = options.store ?? new ReviewStore(defaultReviewPath(sessionId));
  const dist = resolve(webRoot, "dist");
  if (!options.dev) {
    await stat(resolve(dist, "index.html")).catch(() => {
      throw new Error(
        "Web assets missing. Run pnpm build in the servediff checkout, or start with servediff . --dev.",
      );
    });
  }
  const vite = options.dev
    ? await import("vite").then(({ createServer }) =>
        createServer({
          root: webRoot,
          server: { middlewareMode: true, hmr: false },
          appType: "custom",
        }),
      )
    : null;
  const snapshots = new Map<
    DiffMode,
    { time: number; result: Promise<RepositoryDiff> }
  >();
  const networkAddress = localNetworkAddress();
  function snapshot(mode: DiffMode, fresh = false) {
    const cached = snapshots.get(mode);
    if (!fresh && cached && Date.now() - cached.time < 500)
      return cached.result;
    const result = source.snapshot(mode);
    snapshots.set(mode, { time: Date.now(), result });
    result.catch(() => snapshots.delete(mode));
    return result;
  }

  const server = createServer(async (request, response) => {
    try {
      // Reject unadvertised hosts before exposing local code.
      const host = request.headers.host;
      const address = server.address();
      const port =
        address && typeof address === "object" ? address.port : options.port;
      const allowedHosts = ["127.0.0.1", "localhost", "0.0.0.0"];
      if (networkAddress) allowedHosts.push(networkAddress);
      if (!allowedHosts.some((address) => host === `${address}:${port}`))
        throw new RequestError(403, "Invalid host");
      if (request.headers.origin && request.headers.origin !== `http://${host}`)
        throw new RequestError(403, "Cross-origin access denied");
      if (request.headers["sec-fetch-site"] === "cross-site")
        throw new RequestError(403, "Cross-site access denied");
      response.setHeader("X-Content-Type-Options", "nosniff");
      response.setHeader("Referrer-Policy", "no-referrer");
      response.setHeader("Cache-Control", "no-store");
      const url = new URL(request.url ?? "/", `http://${host}`);
      if (
        await handleApi(request, response, url, {
          source,
          sessionId,
          token,
          store,
          snapshot,
        })
      )
        return;
      if (request.method !== "GET" && request.method !== "HEAD")
        throw new RequestError(405, "Method not allowed");
      if (vite) {
        if (url.pathname === "/") {
          const html = await vite.transformIndexHtml(
            "/",
            await readFile(resolve(webRoot, "index.html"), "utf8"),
          );
          response.setHeader("Content-Type", mime[".html"] ?? "text/html");
          response.end(request.method === "HEAD" ? undefined : html);
        } else {
          vite.middlewares(request, response, () => {
            response.writeHead(404).end("Not found");
          });
        }
        return;
      }
      const path = resolve(
        dist,
        `.${decodeURIComponent(url.pathname === "/" ? "/index.html" : url.pathname)}`,
      );
      if (!path.startsWith(`${dist}${sep}`))
        throw new RequestError(404, "Not found");
      const file = await stat(path).catch(() => null);
      if (!file?.isFile()) throw new RequestError(404, "Not found");
      response.setHeader(
        "Content-Type",
        mime[extname(path)] ?? "application/octet-stream",
      );
      if (request.method === "HEAD") response.end();
      else
        createReadStream(path)
          .on("error", () => response.destroy())
          .pipe(response);
    } catch (error) {
      if (response.headersSent) {
        response.destroy();
        return;
      }
      await respondWithProblem(response, error);
    }
  });
  try {
    await new Promise<void>((resolve, reject) => {
      server.once("error", reject);
      server.listen(options.port, "0.0.0.0", resolve);
    });
  } catch (error) {
    await vite?.close();
    throw error;
  }
  const address = server.address();
  if (!address || typeof address === "string")
    throw new Error("Unable to bind server");
  return {
    root: source.root,
    token,
    sessionId,
    url: `http://127.0.0.1:${address.port}`,
    addresses: {
      localhost: `http://localhost:${address.port}#token=${token}`,
      all: `http://0.0.0.0:${address.port}#token=${token}`,
      network: networkAddress
        ? `http://${networkAddress}:${address.port}#token=${token}`
        : null,
    },
    async close() {
      await vite?.close();
      server.closeAllConnections();
      await new Promise<void>((resolve, reject) =>
        server.close((error) => (error ? reject(error) : resolve())),
      );
    },
  };
}
