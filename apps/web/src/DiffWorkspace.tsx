import type {
  CodeViewItem,
  DiffLineAnnotation,
  FileDiffMetadata,
  SelectedLineRange,
} from "@pierre/diffs";
import {
  CodeView,
  type CodeViewReactOptions,
  useWorkerPool,
} from "@pierre/diffs/react";
import { api, errorDetail } from "@servediff/api";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { togglePath, useAppState } from "./app-state.tsx";
import { DiffToolbar } from "./DiffToolbar.tsx";
import { themesFor } from "./display-options.ts";
import { DraftComment, ReviewCommentCard } from "./review.tsx";
import type { CommentAnnotation } from "./review-model.ts";
import { ServerMetrics } from "./ServerMetrics.tsx";

const NAVIGATION_CUE_MS = 1_000;

type CommentLineRange = {
  side: "deletions" | "additions";
  start: number;
  end: number;
};

function markCommentedLines(
  root: ParentNode,
  ranges: readonly CommentLineRange[],
) {
  for (const element of root.querySelectorAll<HTMLElement>(
    "[data-commented-line]",
  ))
    element.removeAttribute("data-commented-line");
  for (const range of ranges) {
    const column = root.querySelector<HTMLElement>(
      `[data-code][data-${range.side}]`,
    );
    if (!column) continue;
    for (const element of column.querySelectorAll<HTMLElement>(
      "[data-line], [data-column-number]",
    )) {
      const value =
        element.getAttribute("data-line") ??
        element.getAttribute("data-column-number");
      const line = Number(value);
      if (line < range.start || line > range.end) continue;
      const lineType = element.getAttribute("data-line-type");
      const tone =
        lineType === "change-addition"
          ? "addition"
          : lineType === "change-deletion"
            ? "deletion"
            : "context";
      element.setAttribute("data-commented-line", tone);
    }
  }
}

function selectionLabel(prefix: string, range: SelectedLineRange | null) {
  if (!range) return "";
  const start = Math.min(range.start, range.end);
  const end = Math.max(range.start, range.end);
  return start === end
    ? `${prefix} line ${start}`
    : `${prefix} lines ${start}–${end}`;
}

async function loadDiffFiles(
  fileDiff: FileDiffMetadata,
  repository: NonNullable<
    ReturnType<typeof useAppState>["source"]["repository"]
  >,
) {
  const file = repository.files.find((entry) => entry.path === fileDiff.name);
  if (!file) throw new Error("File is no longer in this diff");
  const { data: body, error } = await api.GET(
    "/api/v1/diffs/{diffId}/files/{fileId}/contents",
    {
      params: {
        path: { diffId: repository.revision, fileId: file.id },
        query: {
          scope: repository.mode,
          fileVersion: file.fingerprint,
        },
      },
    },
  );
  if (!body)
    throw new Error(errorDetail(error, "Unable to load full file context"));
  return {
    oldFile: {
      name: file.oldPath ?? file.path,
      contents: body.before,
    },
    newFile: { name: file.path, contents: body.after },
  };
}

// Pierre needs numeric versions. Cache them by preview identity and annotation
// contents so typing in a React comment editor doesn't rebuild the diff DOM.
function itemVersions() {
  const versions = new Map<
    string,
    {
      item: CodeViewItem<CommentAnnotation>;
      signature: string;
      version: number;
    }
  >();
  let next = 0;
  return (item: CodeViewItem<CommentAnnotation>, signature: string) => {
    const previous = versions.get(item.id);
    if (previous?.item === item && previous.signature === signature)
      return previous.version;
    const version = ++next;
    versions.set(item.id, { item, signature, version });
    return version;
  };
}

// Keep Pierre's annotation versions and event callbacks local to its renderer.
export function DiffWorkspace() {
  const {
    source: { diff, repository, piped, mode },
    display: {
      theme,
      diffTheme,
      lineDiffType,
      layout,
      wrap,
      collapsed,
      setCollapsed,
    },
    navigation: {
      files,
      filter,
      setFilter,
      navigationTarget,
      commentNavigationTarget,
    },
    reviewed: { isReviewed, toggleReviewed },
    draft,
    review,
    navigateComment,
    viewer,
  } = useAppState();
  const lineMetric = useRef<HTMLSpanElement>(null);
  const [lineHeight, setLineHeight] = useState<number>();
  const [selectionFeedback, setSelectionFeedback] = useState("");
  const workerPool = useWorkerPool();
  useEffect(() => {
    if (!workerPool) return;
    void workerPool
      .setRenderOptions({ theme: themesFor(diffTheme), lineDiffType })
      .catch((error: unknown) => console.error(error));
  }, [workerPool, diffTheme, lineDiffType]);
  // Virtual scroll offsets must use the same row height as our rem-based CSS.
  // Observe a sizing probe so browser font preferences also stay in sync.
  useLayoutEffect(() => {
    const element = lineMetric.current;
    if (!element) return;
    const measure = () => setLineHeight(element.getBoundingClientRect().height);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  const versionFor = useMemo(itemVersions, []);
  const allFiles = repository?.files ?? [];
  const items = useMemo(
    () =>
      files.flatMap((file): CodeViewItem<CommentAnnotation>[] => {
        const item = diff.items.get(file.path);
        if (!item) return [];
        const annotations: DiffLineAnnotation<CommentAnnotation>[] =
          review.comments
            .filter(
              (comment) =>
                comment.path === file.path &&
                comment.scope === mode &&
                comment.fingerprint === file.fingerprint &&
                comment.id !== draft?.id,
            )
            .map((comment) => ({
              side: comment.side,
              lineNumber: comment.end,
              metadata: { kind: "saved", comment },
            }));
        if (
          draft &&
          draft.path === file.path &&
          draft.scope === mode &&
          draft.fingerprint === file.fingerprint
        )
          annotations.push({
            side: draft.side,
            lineNumber: draft.end,
            metadata: { kind: "draft" },
          });
        return [
          {
            ...item,
            ...(item.type === "diff" ? { annotations } : {}),
            collapsed: collapsed.has(file.path),
            version: versionFor(
              item,
              `${collapsed.has(file.path)}:${JSON.stringify(annotations)}`,
            ),
          },
        ];
      }),
    [files, diff.items, review.comments, draft, mode, collapsed, versionFor],
  );

  // Keep renderer options stable while event callbacks see current React state.
  const actions = useRef({
    repository,
    begin: review.begin,
    draft,
    navigateComment,
  });
  useLayoutEffect(() => {
    actions.current = {
      repository,
      begin: review.begin,
      draft,
      navigateComment,
    };
  });
  useLayoutEffect(() => {
    if (
      draft &&
      draft.scope === mode &&
      files.some(
        (file) =>
          file.path === draft.path && file.fingerprint === draft.fingerprint,
      )
    ) {
      viewer.current?.setSelectedLines({
        id: draft.path,
        range: { start: draft.start, end: draft.end, side: draft.side },
      });
    } else viewer.current?.clearSelectedLines();
  }, [draft, files, mode, viewer]);
  useEffect(() => {
    if (!navigationTarget.path) return;
    let frame = 0;
    let timeout = 0;
    let target: HTMLElement | undefined;
    const deadline = performance.now() + NAVIGATION_CUE_MS;
    const highlight = () => {
      const item = viewer.current
        ?.getInstance()
        ?.getRenderedItems()
        .find((rendered) => rendered.id === navigationTarget.path);
      target =
        item?.element.shadowRoot?.querySelector<HTMLElement>(
          "[data-diffs-header]",
        ) ?? undefined;
      if (!target) {
        if (performance.now() < deadline)
          frame = requestAnimationFrame(highlight);
        return;
      }
      target.setAttribute("data-navigation-target", "");
      timeout = window.setTimeout(() => {
        target?.removeAttribute("data-navigation-target");
      }, NAVIGATION_CUE_MS);
    };
    frame = requestAnimationFrame(highlight);
    return () => {
      cancelAnimationFrame(frame);
      clearTimeout(timeout);
      target?.removeAttribute("data-navigation-target");
    };
  }, [navigationTarget, viewer]);
  useEffect(() => {
    if (!commentNavigationTarget) return;
    let frame = 0;
    let timeout = 0;
    let targets: HTMLElement[] = [];
    const deadline = performance.now() + NAVIGATION_CUE_MS;
    const highlight = () => {
      const item = viewer.current
        ?.getInstance()
        ?.getRenderedItems()
        .find((rendered) => rendered.id === commentNavigationTarget.path);
      const column = item?.element.shadowRoot?.querySelector<HTMLElement>(
        `[data-code][data-${commentNavigationTarget.side}]`,
      );
      targets = Array.from(
        column?.querySelectorAll<HTMLElement>("[data-line]") ?? [],
      ).filter((element) => {
        const line = Number(element.getAttribute("data-line"));
        return (
          line >= commentNavigationTarget.start &&
          line <= commentNavigationTarget.end
        );
      });
      if (targets.length === 0) {
        if (performance.now() < deadline)
          frame = requestAnimationFrame(highlight);
        return;
      }
      for (const target of targets)
        target.setAttribute("data-comment-navigation-target", "");
      timeout = window.setTimeout(() => {
        for (const target of targets)
          target.removeAttribute("data-comment-navigation-target");
      }, NAVIGATION_CUE_MS);
    };
    frame = requestAnimationFrame(highlight);
    return () => {
      cancelAnimationFrame(frame);
      clearTimeout(timeout);
      for (const target of targets)
        target.removeAttribute("data-comment-navigation-target");
    };
  }, [commentNavigationTarget, viewer]);
  const options = useMemo<CodeViewReactOptions<CommentAnnotation, undefined>>(
    () => ({
      theme: themesFor(diffTheme),
      themeType: theme,
      diffStyle: layout,
      lineDiffType,
      overflow: wrap ? "wrap" : "scroll",
      diffIndicators: "bars",
      hunkSeparators: piped ? "metadata" : "line-info-basic",
      expansionLineCount: 20,
      collapsedContextThreshold: 3,
      ...(piped
        ? {}
        : {
            loadDiffFiles(fileDiff) {
              const current = actions.current.repository;
              if (!current) throw new Error("Repository is unavailable");
              return loadDiffFiles(fileDiff, current);
            },
          }),
      unsafeCSS: `
        [data-diffs-header] {
          cursor: pointer;
          background: var(--diff-file-header);
          box-shadow: inset 0 -1px var(--border);
        }
        [data-change-icon="change"] { color: var(--warning); }
        [data-diffs-header][data-navigation-target] {
          animation: navigation-target ${NAVIGATION_CUE_MS}ms ease-out;
        }
        @keyframes navigation-target {
          0%, 35% {
            background: var(--accent-bg);
            box-shadow:
              inset 3px 0 var(--accent),
              inset 0 -1px var(--border);
          }
          100% {
            background: var(--diff-file-header);
            box-shadow:
              inset 0 0 transparent,
              inset 0 -1px var(--border);
          }
        }
        @media (prefers-reduced-motion: reduce) {
          [data-diffs-header][data-navigation-target] {
            animation: none;
            background: var(--accent-bg);
            box-shadow:
              inset 3px 0 var(--accent),
              inset 0 -1px var(--border);
          }
        }
        [data-selected-line][data-hovered] {
          --diffs-computed-hovered-line-bg: var(--diffs-computed-selected-line-bg);
        }
        [data-commented-line="context"] {
          --diffs-line-bg: color-mix(
            in srgb,
            var(--diffs-computed-diff-line-bg) 84%,
            var(--review-anchor)
          );
        }
        [data-commented-line="addition"] {
          --diffs-line-bg: color-mix(
            in srgb,
            var(--diffs-computed-diff-line-bg) 76%,
            color-mix(in srgb, var(--diffs-addition-base) 72%, black)
          );
        }
        [data-commented-line="deletion"] {
          --diffs-line-bg: color-mix(
            in srgb,
            var(--diffs-computed-diff-line-bg) 76%,
            color-mix(in srgb, var(--diffs-deletion-base) 72%, black)
          );
        }
        [data-comment-navigation-target] {
          animation: comment-navigation-target ${NAVIGATION_CUE_MS}ms ease-out;
        }
        @keyframes comment-navigation-target {
          0%, 40% {
            box-shadow: inset 3px 0 var(--accent);
            filter: saturate(1.25) brightness(0.92);
          }
          100% {
            box-shadow: inset 0 0 transparent;
            filter: none;
          }
        }
        @media (prefers-reduced-motion: reduce) {
          [data-comment-navigation-target] {
            animation: none;
            box-shadow: inset 3px 0 var(--accent);
            filter: saturate(1.25) brightness(0.92);
          }
        }
      `,
      stickyHeaders: true,
      ...(lineHeight === undefined
        ? {}
        : {
            // Pierre's default header adds 12px padding above and below the row.
            itemMetrics: { lineHeight, diffHeaderHeight: lineHeight + 24 },
          }),
      enableGutterUtility: true,
      lineHoverHighlight: "both",
      layout: { paddingTop: 0, paddingBottom: 24, gap: 8 },
      onPostRender(node, _instance, phase, context) {
        if (phase === "unmount" || context.item.type !== "diff") return;
        const currentDraft = actions.current.draft;
        const ranges = (context.item.annotations ?? []).flatMap(
          (annotation): CommentLineRange[] => {
            if (annotation.metadata.kind === "saved")
              return [annotation.metadata.comment];
            return currentDraft && currentDraft.path === context.item.id
              ? [currentDraft]
              : [];
          },
        );
        markCommentedLines(node.shadowRoot ?? node, ranges);
      },
      onGutterUtilityClick(range, context) {
        if (context.item.type !== "diff") return;
        const current = actions.current;
        if (current.draft) {
          current.navigateComment(current.draft);
          return;
        }
        const file = current.repository?.files.find(
          (file) => file.path === context.item.id,
        );
        if (file) current.begin(file, context.item.fileDiff, range);
      },
      onLineSelectionStart(range) {
        setSelectionFeedback(selectionLabel("Selecting", range));
      },
      onLineSelectionChange(range) {
        setSelectionFeedback(selectionLabel("Selecting", range));
      },
      onLineSelectionEnd(range) {
        setSelectionFeedback(selectionLabel("Selected", range));
      },
      onLineEnter(_event, context) {
        requestAnimationFrame(() =>
          context.element?.shadowRoot
            ?.querySelector("[data-utility-button]")
            ?.setAttribute("aria-label", "Add review comment"),
        );
      },
    }),
    [theme, diffTheme, lineDiffType, layout, wrap, lineHeight, piped],
  );

  const scopeDescription = piped
    ? "Command output · fixed snapshot"
    : mode === "staged"
      ? "HEAD → index"
      : mode === "unstaged"
        ? "Index → working tree, including untracked files"
        : "HEAD → working tree, including untracked files";
  const emptyTitle = !repository
    ? diff.notice
      ? "Cannot load changes"
      : "Loading changes"
    : filter
      ? "No matching files"
      : piped
        ? "No file changes in this input"
        : mode === "staged"
          ? "Nothing staged"
          : "Working tree is clean";
  const emptyDescription = !repository
    ? diff.notice
      ? "Check that the server is running, then refresh."
      : "Your working tree, in focus."
    : filter
      ? "Try a different filename or clear the filter."
      : piped
        ? "Run a command that emits a Git patch, then pipe it into servediff."
        : mode === "staged"
          ? "Stage changes with Git to review them here."
          : "Changes will appear here as you edit. Ignored files stay hidden.";
  return (
    <main>
      <DiffToolbar />
      <div id="notice" role="status" hidden={!diff.notice}>
        {diff.notice}
      </div>
      <p className="visually-hidden" role="status" aria-live="polite">
        {selectionFeedback}
      </p>
      <div className="review-surface">
        <section
          id="viewer"
          aria-label="Code differences"
          onClickCapture={(event) => {
            const path = event.nativeEvent.composedPath();
            if (
              path.some(
                (target) =>
                  target instanceof HTMLElement &&
                  target.matches(
                    "button, a, input, select, textarea, [role=button]",
                  ),
              ) ||
              !path.some(
                (target) =>
                  target instanceof HTMLElement &&
                  target.hasAttribute("data-diffs-header"),
              )
            )
              return;
            const item = viewer.current
              ?.getInstance()
              ?.getRenderedItems()
              .find((rendered) => path.includes(rendered.element));
            if (item) setCollapsed((previous) => togglePath(previous, item.id));
          }}
        >
          <span
            ref={lineMetric}
            className="diff-line-metric"
            aria-hidden="true"
          />
          <CodeView
            ref={viewer}
            items={items}
            options={options}
            style={{ height: "100%", width: "100%", overflow: "auto" }}
            renderHeaderPrefix={(item) => (
              <Button
                type="button"
                className="diff-collapse"
                variant="ghost"
                size="icon-xs"
                aria-label={`${item.collapsed ? "Expand" : "Collapse"} ${item.id}`}
                aria-expanded={!item.collapsed}
                onClick={() =>
                  setCollapsed((previous) => togglePath(previous, item.id))
                }
              >
                <svg viewBox="0 0 16 16" aria-hidden="true">
                  <path d={item.collapsed ? "m6 4 4 4-4 4" : "m4 6 4 4 4-4"} />
                </svg>
              </Button>
            )}
            renderHeaderMetadata={(item) => {
              const file = allFiles.find((file) => file.path === item.id);
              return file ? (
                <Button
                  type="button"
                  className="review-button"
                  variant="outline"
                  aria-label={`Mark ${file.path} ${isReviewed(file) ? "unreviewed" : "reviewed"}`}
                  aria-pressed={isReviewed(file)}
                  onClick={() => toggleReviewed(file)}
                >
                  <svg
                    className="review-checkbox"
                    viewBox="0 0 16 16"
                    aria-hidden="true"
                  >
                    <rect x="2" y="2" width="12" height="12" rx="2" />
                    {isReviewed(file) && <path d="m4.5 8 2.5 2.5 4.5-5" />}
                  </svg>
                  viewed
                </Button>
              ) : null;
            }}
            renderAnnotation={(annotation) =>
              annotation.metadata.kind === "draft" ? (
                <DraftComment />
              ) : (
                <ReviewCommentCard comment={annotation.metadata.comment} />
              )
            }
          />
        </section>
        <div id="empty" role="status" hidden={files.length > 0}>
          <div className="empty-symbol" aria-hidden="true">
            {!repository && diff.notice ? "!" : "±"}
          </div>
          <h2>{emptyTitle}</h2>
          <p>{emptyDescription}</p>
          {!!filter && (
            <Button
              type="button"
              variant="outline"
              onClick={() => setFilter("")}
            >
              Clear filter
            </Button>
          )}
        </div>
      </div>
      <footer className="main-footer">
        <span id="scope-description">{scopeDescription}</span>
        <span className="footer-details">
          <span className="footer-shortcuts">
            <kbd>Alt+J</kbd> <kbd>Alt+K</kbd> files <kbd>Alt+/</kbd> filter
            {!piped && (
              <>
                {" "}
                <kbd>Alt+R</kbd> refresh
              </>
            )}
          </span>
          <ServerMetrics />
        </span>
      </footer>
    </main>
  );
}
