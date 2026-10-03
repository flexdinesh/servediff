import { WorkerPoolContextProvider } from "@pierre/diffs/react";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App.tsx";
import { AppearanceProvider } from "./appearance-context.tsx";
import { SidebarProvider } from "./sidebar-context.tsx";
import { AppProvider } from "./app-state.tsx";
import { highlighterOptions, poolOptions } from "./diff-workers.ts";
import { SessionProvider } from "./session-context.tsx";
import "@fontsource-variable/jetbrains-mono/wght.css";
import "./typography.css";
import "./tokens.css";
import "./style.css";
import "./review.css";

const root = document.getElementById("root");
if (!root) throw new Error("Missing React root");
createRoot(root).render(
  <StrictMode>
    <SidebarProvider>
      <AppearanceProvider>
        <SessionProvider>
          <WorkerPoolContextProvider
            poolOptions={poolOptions}
            highlighterOptions={highlighterOptions}
          >
            <AppProvider>
              <App />
            </AppProvider>
          </WorkerPoolContextProvider>
        </SessionProvider>
      </AppearanceProvider>
    </SidebarProvider>
  </StrictMode>,
);
