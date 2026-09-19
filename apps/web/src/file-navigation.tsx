import type { ChangedFile } from "@servediff/shared";
import { MessageSquareIcon } from "lucide-react";
import type { KeyboardEvent } from "react";
import { Button } from "@/components/ui/button";
import {
  fileKind,
  gitDecoration,
  monograms,
  shapes,
} from "./file-decoration.ts";
import {
  ancestorPaths,
  buildFileTree,
  type FileTreeNode,
} from "./file-tree.ts";

function FolderIcon({ open }: { open: boolean }) {
  return (
    <svg
      viewBox="0 0 20 20"
      className="folder-icon"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M2 16V5a1 1 0 0 1 1-1h4l2 2h7a1 1 0 0 1 1 1v2" />
      <path d={open ? "M2 16l3-7h13l-3 7Z" : "M2 8h15v8H2Z"} />
    </svg>
  );
}
function FileIcon({ path, reviewed }: { path: string; reviewed: boolean }) {
  const kind = fileKind(path);
  const monogram = monograms[kind];
  return (
    <span className="file-icon" data-kind={kind} aria-hidden="true">
      <svg
        viewBox="0 0 20 20"
        className={`file-type-icon${monogram ? " monogram-icon" : ""}`}
        aria-hidden="true"
        focusable="false"
      >
        {monogram ? (
          <>
            <path d="M2 2h16v16H2Z" />
            <text x="10" y="13.5">
              {monogram}
            </text>
          </>
        ) : (
          <path d={shapes[kind] ?? shapes.file} />
        )}
      </svg>
      {reviewed && <span className="file-reviewed-check">✓</span>}
    </span>
  );
}
function CommentIcon() {
  return <MessageSquareIcon aria-hidden="true" focusable="false" />;
}
function StatusBadge({
  file,
  stagingMetadata,
}: {
  file: ChangedFile;
  stagingMetadata: boolean;
}) {
  const decoration = gitDecoration(file);
  return (
    <span
      className="file-status"
      data-status={file.status}
      data-state={decoration.state}
      title={decoration.details}
      aria-hidden="true"
    >
      <span>{decoration.code}</span>
      {stagingMetadata &&
        ["staged", "unstaged", "both"].includes(decoration.state) && (
          <span
            className={`staging-dot ${decoration.state}`}
            aria-hidden="true"
          />
        )}
    </span>
  );
}

interface Props {
  files: ChangedFile[];
  selected: string;
  closed: Set<string>;
  filtering: boolean;
  isReviewed: (file: ChangedFile) => boolean;
  commentCounts: Map<string, number>;
  stagingMetadata: boolean;
  onToggle: (path: string) => void;
  onSelect: (path: string) => void;
}

export function FileNavigation({
  files,
  selected,
  closed,
  filtering,
  isReviewed,
  commentCounts,
  stagingMetadata,
  onToggle,
  onSelect,
}: Props) {
  function onKeyDown(event: KeyboardEvent<HTMLButtonElement>) {
    const focused = event.currentTarget;
    const buttons = [
      ...(focused
        .closest("nav")
        ?.querySelectorAll<HTMLButtonElement>("button") ?? []),
    ];
    const index = buttons.indexOf(focused);
    if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
      event.preventDefault();
      const next =
        event.key === "Home"
          ? 0
          : event.key === "End"
            ? buttons.length - 1
            : index + (event.key === "ArrowDown" ? 1 : -1);
      buttons[Math.max(0, Math.min(buttons.length - 1, next))]?.focus();
    }
    if (
      event.key === "ArrowRight" &&
      focused.getAttribute("aria-expanded") === "false"
    ) {
      event.preventDefault();
      focused.click();
    }
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      if (focused.getAttribute("aria-expanded") === "true") focused.click();
      else {
        const parent = ancestorPaths(focused.dataset.treePath ?? "").at(-1);
        buttons.find((button) => button.dataset.treePath === parent)?.focus();
      }
    }
  }
  function renderNodes(nodes: FileTreeNode[], depth: number) {
    return (
      <ul className="tree-list">
        {nodes.map((node) => {
          const style = {
            paddingLeft: `calc(var(--space-1) + ${depth} * var(--space-3))`,
          };
          if (node.kind === "folder") {
            const open = !closed.has(node.path);
            return (
              <li key={node.path}>
                <Button
                  type="button"
                  className="tree-folder"
                  variant="ghost"
                  size="sm"
                  style={style}
                  data-tree-path={node.path}
                  tabIndex={-1}
                  title={node.path}
                  aria-expanded={open}
                  aria-label={`${open ? "Collapse" : "Expand"} folder ${node.path}`}
                  onClick={() => onToggle(node.path)}
                  onKeyDown={onKeyDown}
                >
                  <span className="tree-chevron" aria-hidden="true">
                    <svg viewBox="0 0 16 16">
                      <path d={open ? "m4 6 4 4 4-4" : "m6 4 4 4-4 4"} />
                    </svg>
                  </span>
                  <FolderIcon open={open} />
                  <span className="file-name">{node.name}</span>
                </Button>
                {open && renderNodes(node.children, depth + 1)}
              </li>
            );
          }
          const file = node.file;
          const viewed = isReviewed(file);
          const decoration = gitDecoration(file);
          const count = commentCounts.get(file.path) ?? 0;
          return (
            <li key={node.path}>
              <Button
                type="button"
                className={`file-row${viewed ? " reviewed" : ""}`}
                variant="ghost"
                size="sm"
                style={style}
                data-path={file.path}
                data-tree-path={file.path}
                tabIndex={selected === file.path ? 0 : -1}
                aria-current={selected === file.path}
                aria-label={`${file.path}, ${decoration.label}${viewed ? ", reviewed" : ""}${count ? `, ${count} review ${count === 1 ? "comment" : "comments"}` : ""}`}
                title={`${file.oldPath ? `${file.oldPath} → ${file.path}` : file.path}\n${decoration.details}`}
                onClick={() => onSelect(file.path)}
                onKeyDown={onKeyDown}
              >
                <FileIcon path={file.path} reviewed={viewed} />
                <span className="file-name">{node.name}</span>
                {count > 0 && (
                  <span
                    className="file-comment-count"
                    title={`${count} review ${count === 1 ? "comment" : "comments"}`}
                  >
                    <CommentIcon /> {count}
                  </span>
                )}
                <StatusBadge file={file} stagingMetadata={stagingMetadata} />
              </Button>
            </li>
          );
        })}
      </ul>
    );
  }
  return (
    <nav id="file-tree" aria-label="Changed file navigation">
      {files.length ? (
        renderNodes(buildFileTree(files), 0)
      ) : (
        <p className="tree-folder">
          {filtering ? "No matching files" : "No changed files"}
        </p>
      )}
    </nav>
  );
}
