import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath } from "node:url";
import { defineConfig, type ProxyOptions } from "vite";
import { startFixtureServer } from "./test/fixture-server.ts";

export default defineConfig(async ({ command }) => {
  const configuredApiUrl = process.env.SERVEDIFF_API_URL;
  const fixture =
    command === "serve" && !configuredApiUrl
      ? await startFixtureServer()
      : undefined;
  const apiUrl = configuredApiUrl ?? fixture?.target;
  const proxy: ProxyOptions | undefined = apiUrl
    ? {
        target: apiUrl,
        changeOrigin: true,
        configure(server) {
          server.on("proxyReq", (request) => request.removeHeader("Origin"));
        },
      }
    : undefined;
  return {
    server: {
      host: true,
      ...(proxy
        ? {
            proxy: {
              "/api": proxy,
              "/openapi.yaml": proxy,
            },
          }
        : {}),
    },
    resolve: {
      alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
    },
    plugins: [react(), tailwindcss(), ...(fixture ? [fixture.plugin] : [])],
  };
});
