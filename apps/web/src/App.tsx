import { useAppState } from "./app-state.tsx";
import { DiffWorkspace } from "./DiffWorkspace.tsx";
import { FileExplorerNav, SidebarResizer } from "./FileExplorerNav.tsx";
import { Header } from "./Header.tsx";
import { CopyDialog } from "./review.tsx";
import { capabilityEnabled } from "./session-context.tsx";
import { WebMCPTools } from "./WebMCPTools.tsx";

export function App() {
  const { review, sidebar, capabilities } = useAppState();
  return (
    <>
      <WebMCPTools />
      <Header />
      {sidebar.mobile && sidebar.open && (
        <button
          type="button"
          className="sidebar-backdrop"
          aria-label="Close review sidebar"
          tabIndex={-1}
          onClick={sidebar.closeMobile}
        />
      )}
      <div className="workspace">
        <FileExplorerNav />
        <SidebarResizer />
        <DiffWorkspace />
      </div>
      {capabilityEnabled(capabilities.review.comments) &&
        review.copyContent !== null && (
          <CopyDialog content={review.copyContent} onClose={review.closeCopy} />
        )}
    </>
  );
}
