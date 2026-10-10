import type { CodeViewHandle } from "@pierre/diffs/react";
import type { DiffMode, RepositoryDiff, ReviewComment } from "@diffx/shared";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
} from "react";
import { ancestorPaths, filesInTreeOrder } from "./file-tree.ts";
import { anchored, type CommentAnnotation } from "./review-model.ts";

interface NavigationOptions {
  repository: RepositoryDiff | null;
  mode: DiffMode;
  scopes: readonly DiffMode[];
  busy: boolean;
  refreshEnabled: boolean;
  refresh: () => void;
  changeMode: (mode: DiffMode) => void;
  setCollapsed: Dispatch<SetStateAction<Set<string>>>;
  setFeedback: (message: string) => void;
  showSidebar: () => void;
  closeMobile: () => void;
}

export function useFileNavigation({
  repository,
  mode,
  scopes,
  busy,
  refreshEnabled,
  refresh,
  changeMode,
  setCollapsed,
  setFeedback,
  showSidebar,
  closeMobile,
}: NavigationOptions) {
  const [tab, setTab] = useState<"files" | "comments">("files");
  const [filter, setFilter] = useState("");
  const [selected, setSelected] = useState("");
  const [navigationTarget, setNavigationTarget] = useState({
    path: "",
    sequence: 0,
  });
  const [commentNavigationTarget, setCommentNavigationTarget] = useState<{
    path: string;
    side: ReviewComment["side"];
    start: number;
    end: number;
    sequence: number;
  } | null>(null);
  const [closed, setClosed] = useState(new Set<string>());
  const [filteredClosed, setFilteredClosed] = useState(new Set<string>());
  const [pendingComment, setPendingComment] = useState<ReviewComment | null>(
    null,
  );
  const viewer = useRef<CodeViewHandle<CommentAnnotation, undefined>>(null);
  const search = useRef<HTMLInputElement>(null);
  const files = useMemo(
    () =>
      filesInTreeOrder(
        repository?.files.filter((file) =>
          file.path.toLowerCase().includes(filter.toLowerCase()),
        ) ?? [],
      ),
    [repository, filter],
  );
  const activePath = files.some((file) => file.path === selected)
    ? selected
    : (files[0]?.path ?? "");
  const revealFile = useCallback(
    (path: string) => {
      setSelected(path);
      const expandParents = (previous: Set<string>) => {
        const parents = ancestorPaths(path);
        if (!parents.some((parent) => previous.has(parent))) return previous;
        const next = new Set(previous);
        for (const parent of parents) next.delete(parent);
        return next;
      };
      setClosed(expandParents);
      setFilteredClosed(expandParents);
      setCollapsed((previous) => {
        if (!previous.has(path)) return previous;
        const next = new Set(previous);
        next.delete(path);
        return next;
      });
      closeMobile();
      requestAnimationFrame(() => {
        document
          .querySelector<HTMLElement>(
            `#file-tree .file-row[data-path="${CSS.escape(path)}"]`,
          )
          ?.scrollIntoView({ block: "nearest" });
      });
    },
    [closeMobile, setCollapsed],
  );
  const selectFile = useCallback(
    (path: string) => {
      revealFile(path);
      setNavigationTarget((previous) => ({
        path,
        sequence: previous.sequence + 1,
      }));
      requestAnimationFrame(() => {
        viewer.current?.scrollTo({
          type: "item",
          id: path,
          align: "start",
          behavior: "instant",
        });
      });
    },
    [revealFile],
  );
  const navigateComment = useCallback(
    (comment: ReviewComment) => {
      if (comment.scope !== mode && scopes.includes(comment.scope)) {
        setPendingComment(comment);
        changeMode(comment.scope);
        return;
      }
      if (!anchored(comment, repository)) {
        setTab("comments");
        showSidebar();
        setFeedback(
          comment.target === "file"
            ? "This file comment belongs to an earlier diff. Open it in the review sidebar."
            : "This comment belongs to an earlier diff. Its original code context is preserved below.",
        );
        return;
      }
      setFilter("");
      setPendingComment(comment);
    },
    [mode, scopes, changeMode, repository, showSidebar, setFeedback],
  );
  // Navigate only after the requested scope and its parsed previews are installed.
  useEffect(() => {
    if (!pendingComment || busy || repository?.mode !== pendingComment.scope)
      return;
    if (anchored(pendingComment, repository)) {
      if (filter) {
        setFilter("");
        return;
      }
      revealFile(pendingComment.path);
      const frame = requestAnimationFrame(() => {
        if (pendingComment.target === "file") {
          viewer.current?.scrollTo({
            type: "item",
            id: pendingComment.path,
            align: "start",
            behavior: "instant",
          });
          setNavigationTarget((previous) => ({
            path: pendingComment.path,
            sequence: previous.sequence + 1,
          }));
          setCommentNavigationTarget(null);
        } else {
          viewer.current?.scrollTo({
            type: "range",
            id: pendingComment.path,
            range: {
              start: pendingComment.start,
              end: pendingComment.end,
              side: pendingComment.side,
            },
            align: "center",
            behavior: "instant",
          });
          setCommentNavigationTarget((previous) => ({
            path: pendingComment.path,
            side: pendingComment.side,
            start: pendingComment.start,
            end: pendingComment.end,
            sequence: (previous?.sequence ?? 0) + 1,
          }));
        }
        setPendingComment(null);
      });
      return () => cancelAnimationFrame(frame);
    }
    setTab("comments");
    setPendingComment(null);
  }, [pendingComment, busy, repository, filter, revealFile]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (
        event
          .composedPath()
          .some(
            (target) =>
              target instanceof HTMLInputElement ||
              target instanceof HTMLTextAreaElement ||
              (target instanceof HTMLElement && target.isContentEditable),
          ) ||
        event.metaKey ||
        event.ctrlKey ||
        !event.altKey
      )
        return;
      if (event.code === "Slash") {
        event.preventDefault();
        showSidebar();
        setTab("files");
        requestAnimationFrame(() => search.current?.focus());
      }
      if (event.code === "KeyR") {
        event.preventDefault();
        if (refreshEnabled) refresh();
      }
      if (event.code === "KeyJ" || event.code === "KeyK") {
        event.preventDefault();
        const index = Math.max(
          0,
          files.findIndex((file) => file.path === activePath),
        );
        const file =
          files[
            Math.max(
              0,
              Math.min(
                files.length - 1,
                index + (event.code === "KeyJ" ? 1 : -1),
              ),
            )
          ];
        if (file) selectFile(file.path);
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [files, activePath, showSidebar, refresh, refreshEnabled, selectFile]);

  return useMemo(
    () => ({
      tab,
      setTab,
      filter,
      setFilter,
      files,
      activePath,
      navigationTarget,
      commentNavigationTarget,
      closed,
      setClosed,
      filteredClosed,
      setFilteredClosed,
      selectFile,
      search,
      viewer,
      navigateComment,
    }),
    [
      tab,
      filter,
      files,
      activePath,
      navigationTarget,
      commentNavigationTarget,
      closed,
      filteredClosed,
      selectFile,
      navigateComment,
    ],
  );
}
