import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import { fixtureApiPlugin } from "./test/fixture-api.ts";

export default defineConfig(async ({ command }) => {
  const apiUrl = process.env.SERVEDIFF_API_URL;
  return {
    server: {
      host: true,
      ...(apiUrl
        ? {
            proxy: {
              "/api": { target: apiUrl, changeOrigin: true },
              "/openapi.yaml": { target: apiUrl, changeOrigin: true },
            },
          }
        : {}),
    },
    resolve: {
      alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
    },
    plugins: [
      react(),
      tailwindcss(),
      ...(command === "serve" && !apiUrl ? [await fixtureApiPlugin()] : []),
    ],
  };
});
