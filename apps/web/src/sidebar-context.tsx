import { createContext, useContext, type ReactNode } from "react";
import { useSidebar } from "./use-sidebar.ts";

const SidebarContext = createContext<ReturnType<typeof useSidebar> | null>(
  null,
);

export function SidebarProvider({ children }: { children: ReactNode }) {
  const sidebar = useSidebar();
  return <SidebarContext value={sidebar}>{children}</SidebarContext>;
}

export function useSidebarState() {
  const sidebar = useContext(SidebarContext);
  if (!sidebar) throw new Error("Page components require SidebarProvider");
  return sidebar;
}
