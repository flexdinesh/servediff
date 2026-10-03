import type { ApiContext } from "@servediff/api";
import {
  CheckIcon,
  FolderGit2Icon,
  GitBranchIcon,
  SearchIcon,
  SquareTerminalIcon,
  XIcon,
} from "lucide-react";
import { useEffect, useId, useRef, useState, type RefObject } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { contextDetail, pickerResults } from "./project-picker.ts";

function ContextIcon({ context }: { context: ApiContext }) {
  const Icon = context.kind === "capture" ? SquareTerminalIcon : FolderGit2Icon;
  return <Icon className="size-(--icon-base)" aria-hidden="true" />;
}

export function ProjectPickerTrigger({
  current,
  open,
  onOpen,
  triggerRef,
}: {
  current: ApiContext | undefined;
  open: boolean;
  onOpen: () => void;
  triggerRef: RefObject<HTMLButtonElement | null>;
}) {
  return (
    <Button
      ref={triggerRef}
      type="button"
      variant="outline"
      className="context-switcher"
      aria-label="Switch repository"
      aria-haspopup="dialog"
      aria-expanded={open}
      title={
        current
          ? `${current.name} · ${contextDetail(current)}${current.worktreeName ? ` · Worktree: ${current.worktreeName}` : ""}\n${current.root ?? "Piped diff"}`
          : "Switch repository"
      }
      onClick={onOpen}
    >
      {current && <ContextIcon context={current} />}
      <span className="context-switcher-name">
        {current?.name ?? "Switch repository"}
      </span>
      {current?.kind === "worktree" && (
        <>
          <span className="context-switcher-separator" aria-hidden="true">
            /
          </span>
          <GitBranchIcon className="size-(--icon-sm)" aria-hidden="true" />
          <span className="context-switcher-branch">
            {contextDetail(current)}
          </span>
        </>
      )}
      <SearchIcon
        className="context-switcher-search size-(--icon-base)"
        aria-hidden="true"
      />
      <kbd className="context-switcher-shortcut">
        {navigator.platform.includes("Mac") ? "⌘K" : "Ctrl K"}
      </kbd>
    </Button>
  );
}

export function ProjectPicker({
  contexts,
  selectedId,
  select,
  open,
  onOpenChange,
  triggerRef,
}: {
  contexts: ApiContext[];
  selectedId: string;
  select: (id: string) => void;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  triggerRef: RefObject<HTMLButtonElement | null>;
}) {
  const [query, setQuery] = useState("");
  const [highlightedId, setHighlightedId] = useState("");
  const searchRef = useRef<HTMLInputElement>(null);
  const listId = useId();
  const results = pickerResults(contexts, query);
  const active =
    results.find((context) => context.id === highlightedId) ??
    (!query.trim()
      ? results.find((context) => context.id === selectedId)
      : undefined) ??
    results[0];
  const optionId = (id: string) => `${listId}-${id}`;
  const changeOpen = (next: boolean) => {
    if (!next) {
      setQuery("");
      setHighlightedId("");
    }
    onOpenChange(next);
  };
  const choose = (id: string) => {
    changeOpen(false);
    select(id);
  };

  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      if (
        event.defaultPrevented ||
        event.isComposing ||
        event.altKey ||
        !(event.metaKey || event.ctrlKey) ||
        event.key.toLowerCase() !== "k"
      )
        return;
      if (
        !open &&
        document.querySelector('[role="dialog"], [role="alertdialog"]')
      )
        return;
      event.preventDefault();
      if (contexts.length) onOpenChange(true);
      if (open) searchRef.current?.focus();
    };
    document.addEventListener("keydown", shortcut);
    return () => document.removeEventListener("keydown", shortcut);
  }, [contexts.length, onOpenChange, open]);

  const activeOptionId = active ? optionId(active.id) : undefined;
  useEffect(() => {
    if (open && activeOptionId)
      document
        .getElementById(activeOptionId)
        ?.scrollIntoView({ block: "nearest" });
  }, [open, activeOptionId]);

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogContent
        className="project-picker"
        initialFocus={searchRef}
        finalFocus={triggerRef}
        showCloseButton={false}
      >
        <DialogTitle className="sr-only">Switch repository</DialogTitle>
        <DialogDescription className="sr-only">
          Search repositories, branches, worktrees, and paths. Use arrow keys to
          navigate and Enter to switch.
        </DialogDescription>
        <div className="project-picker-search">
          <SearchIcon className="size-(--icon-base)" aria-hidden="true" />
          <Input
            ref={searchRef}
            role="combobox"
            aria-label="Search repositories"
            aria-autocomplete="list"
            aria-expanded={open}
            aria-controls={listId}
            aria-activedescendant={activeOptionId}
            placeholder="Search repositories…"
            value={query}
            onChange={(event) => {
              setQuery(event.target.value);
              setHighlightedId("");
            }}
            onKeyDown={(event) => {
              if (event.nativeEvent.isComposing) return;
              const index = results.findIndex(
                (context) => context.id === active?.id,
              );
              if (event.key === "ArrowDown" || event.key === "ArrowUp") {
                event.preventDefault();
                const next =
                  results[
                    Math.max(
                      0,
                      Math.min(
                        results.length - 1,
                        index + (event.key === "ArrowDown" ? 1 : -1),
                      ),
                    )
                  ];
                if (next) setHighlightedId(next.id);
              } else if (event.key === "Enter" && active) {
                event.preventDefault();
                choose(active.id);
              }
            }}
          />
          <DialogClose render={<Button variant="ghost" size="icon-sm" />}>
            <XIcon aria-hidden="true" />
            <span className="sr-only">Close</span>
          </DialogClose>
        </div>
        <div
          id={listId}
          className="project-picker-results"
          role="listbox"
          aria-label="Repositories"
        >
          {results.map((context) => (
            <button
              type="button"
              role="option"
              tabIndex={-1}
              id={optionId(context.id)}
              key={context.id}
              className="project-picker-option"
              aria-selected={context.id === active?.id}
              title={context.root ?? context.name}
              onClick={() => choose(context.id)}
              onMouseMove={() => setHighlightedId(context.id)}
            >
              <ContextIcon context={context} />
              <span className="project-picker-copy">
                <span className="project-picker-label">
                  <span className="project-picker-name">{context.name}</span>
                  <span className="project-picker-branch">
                    {context.kind !== "capture" && (
                      <GitBranchIcon
                        className="size-(--icon-sm)"
                        aria-hidden="true"
                      />
                    )}
                    <span>
                      {context.kind === "capture"
                        ? "Snapshot"
                        : contextDetail(context)}
                    </span>
                  </span>
                </span>
                <span className="project-picker-path">
                  {context.worktreeName && (
                    <span className="project-picker-kind">Worktree · </span>
                  )}
                  {context.root ?? context.submittedFrom ?? "Piped diff"}
                </span>
                {context.availability === "unavailable" && (
                  <span className="project-picker-unavailable">
                    Unavailable
                  </span>
                )}
              </span>
              {context.id === selectedId && (
                <span className="project-picker-current">
                  <CheckIcon
                    className="size-(--icon-base)"
                    aria-hidden="true"
                  />
                  <span className="sr-only">
                    Current<span className="sr-only"> repository</span>
                  </span>
                </span>
              )}
            </button>
          ))}
          {!results.length && (
            <div className="project-picker-empty">
              <p>No repositories found</p>
              <span>Try another repository, branch, or folder.</span>
            </div>
          )}
        </div>
        <div className="project-picker-footer">
          <p className="project-picker-status" role="status">
            {results.length
              ? `${results.length} ${results.length === 1 ? "result" : "results"}`
              : "No matching repositories"}
          </p>
          <div className="project-picker-shortcuts">
            <span>
              <kbd>↑ ↓</kbd> Navigate
            </span>
            <span>
              <kbd>Enter</kbd> Switch
            </span>
            <span>
              <kbd>Esc</kbd> Close
            </span>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
