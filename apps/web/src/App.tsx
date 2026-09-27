import { useReviewState } from "./app-state.tsx";
import { DiffWorkspace } from "./DiffWorkspace.tsx";
import { FileExplorerNav, SidebarResizer } from "./FileExplorerNav.tsx";
import { Header } from "./Header.tsx";
import { CopyDialog } from "./review.tsx";
import { capabilityEnabled, useSession } from "./session-context.tsx";
import { useSidebarState } from "./sidebar-context.tsx";
import { WebMCPTools } from "./WebMCPTools.tsx";

export function App() {
  return (
    <>
      <WebMCPTools />
      <Header />
      <SidebarBackdrop />
      <div className="workspace">
        <FileExplorerNav />
        <SidebarResizer />
        <DiffWorkspace />
      </div>
      <ReviewCopyDialog />
    </>
  );
}

function SidebarBackdrop() {
  const sidebar = useSidebarState();
  return sidebar.mobile && sidebar.open ? (
    <button
      type="button"
      className="sidebar-backdrop"
      aria-label="Close review sidebar"
      tabIndex={-1}
      onClick={sidebar.closeMobile}
    />
  ) : null;
}

function ReviewCopyDialog() {
  const review = useReviewState();
  const { capabilities } = useSession();
  return capabilityEnabled(capabilities.review.comments) &&
    review.copyContent !== null ? (
    <CopyDialog content={review.copyContent} onClose={review.closeCopy} />
  ) : null;
}
