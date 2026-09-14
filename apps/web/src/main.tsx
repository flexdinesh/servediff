import { WorkerPoolContextProvider } from "@pierre/diffs/react";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App.tsx";
import { AppProvider } from "./app-state.tsx";
import { highlighterOptions, poolOptions } from "./diff-workers.ts";
import "@fontsource-variable/jetbrains-mono/wght.css";
import "./typography.css";
import "./tokens.css";
import "./style.css";
import "./review.css";

const root = document.getElementById("root");
if (!root) throw new Error("Missing React root");
createRoot(root).render(
  <StrictMode>
    <WorkerPoolContextProvider
      poolOptions={poolOptions}
      highlighterOptions={highlighterOptions}
    >
      <AppProvider>
        <App />
      </AppProvider>
    </WorkerPoolContextProvider>
  </StrictMode>,
);
