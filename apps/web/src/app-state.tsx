import type { DiffMode, ReviewComment } from "@servediff/shared";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
  type Dispatch,
  type SetStateAction,
} from "react";
import {
  capabilityEnabled,
  usePinContext,
  useSession,
} from "./session-context.tsx";
import { useSidebarState } from "./sidebar-context.tsx";
import { useDiff } from "./use-diff.ts";
import { useFileNavigation } from "./use-file-navigation.ts";
import { useReview } from "./use-review.ts";
import { useReviewedFiles } from "./use-reviewed-files.ts";

export function togglePath(paths: Set<string>, path: string) {
  const next = new Set(paths);
  if (next.has(path)) next.delete(path);
  else next.add(path);
  return next;
}

interface DiffSource {
  diff: ReturnType<typeof useDiff>;
  repository: ReturnType<typeof useDiff>["repository"];
  mode: DiffMode;
  piped: boolean;
  changeMode: (mode: DiffMode) => void;
}
const SourceContext = createContext<DiffSource | null>(null);
const NavigationContext = createContext<ReturnType<
  typeof useFileNavigation
> | null>(null);
const ReviewContext = createContext<ReturnType<typeof useReview> | null>(null);
const ReviewedFilesContext = createContext<ReturnType<
  typeof useReviewedFiles
> | null>(null);
const DraftContext = createContext<{
  draft: ReviewComment | null;
  setDraft: Dispatch<SetStateAction<ReviewComment | null>>;
} | null>(null);
const CollapseContext = createContext<{
  collapsed: Set<string>;
  setCollapsed: Dispatch<SetStateAction<Set<string>>>;
} | null>(null);

function WorkspaceProvider({ children }: { children: ReactNode }) {
  const { id: contextId, capabilities } = useSession();
  const refreshEnabled = capabilityEnabled(capabilities.diff.refresh);
  const commentsEnabled = capabilityEnabled(capabilities.review.comments);
  const scopes = capabilities.diff.scopes.values;
  const [mode, setMode] = useState<DiffMode>("all");
  const [draft, setDraft] = useState<ReviewComment | null>(null);
  const pinContext = usePinContext();
  useEffect(() => {
    if (draft) pinContext();
  }, [draft, pinContext]);
  const [collapsed, setCollapsed] = useState(new Set<string>());
  const diff = useDiff(contextId, mode);
  const repository = diff.repository;
  const review = useReview(
    contextId,
    repository,
    draft,
    setDraft,
    commentsEnabled,
  );
  const reviewed = useReviewedFiles(contextId, repository, mode, setCollapsed);
  const { show: showSidebar, closeMobile } = useSidebarState();
  const changeMode = useCallback(
    (value: DiffMode) => {
      if (value === mode || !scopes.includes(value)) return;
      setMode(value);
      setCollapsed(new Set());
    },
    [mode, scopes],
  );
  const navigation = useFileNavigation({
    repository,
    mode,
    scopes,
    busy: diff.busy,
    refreshEnabled,
    refresh: diff.refresh,
    changeMode,
    setCollapsed,
    setFeedback: review.setFeedback,
    showSidebar,
    closeMobile,
  });
  const piped = repository?.source === "stdin";
  useEffect(() => {
    document.title = `servediff · ${piped ? "Piped" : "Git diff"}`;
  }, [piped]);
  const source = useMemo(
    () => ({ diff, repository, mode, piped, changeMode }),
    [diff, repository, mode, piped, changeMode],
  );
  const draftState = useMemo(() => ({ draft, setDraft }), [draft]);
  const collapse = useMemo(() => ({ collapsed, setCollapsed }), [collapsed]);
  return (
    <SourceContext value={source}>
      <NavigationContext value={navigation}>
        <ReviewContext value={review}>
          <ReviewedFilesContext value={reviewed}>
            <DraftContext value={draftState}>
              <CollapseContext value={collapse}>{children}</CollapseContext>
            </DraftContext>
          </ReviewedFilesContext>
        </ReviewContext>
      </NavigationContext>
    </SourceContext>
  );
}

// Transient workspace ownership ends when the selected context changes.
export function AppProvider({ children }: { children: ReactNode }) {
  const { id } = useSession();
  return <WorkspaceProvider key={id}>{children}</WorkspaceProvider>;
}

export function useDiffSource() {
  const source = useContext(SourceContext);
  if (!source) throw new Error("Page components require AppProvider");
  return source;
}
export function useNavigation() {
  const navigation = useContext(NavigationContext);
  if (!navigation) throw new Error("Page components require AppProvider");
  return navigation;
}
export function useReviewState() {
  const review = useContext(ReviewContext);
  if (!review) throw new Error("Page components require AppProvider");
  return review;
}
export function useReviewedFilesState() {
  const reviewed = useContext(ReviewedFilesContext);
  if (!reviewed) throw new Error("Page components require AppProvider");
  return reviewed;
}
export function useDraft() {
  const draft = useContext(DraftContext);
  if (!draft) throw new Error("Page components require AppProvider");
  return draft;
}
export function useDiffCollapse() {
  const collapse = useContext(CollapseContext);
  if (!collapse) throw new Error("Page components require AppProvider");
  return collapse;
}
