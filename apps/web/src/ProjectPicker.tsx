import {
  api,
  errorDetail,
  type ApiRepository,
  type ApiContext,
} from "@servediff/api";
import {
  ArrowLeftIcon,
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
      variant="ghost"
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
  repositories,
  onDiscovered,
  selectedId,
  select,
  open,
  onOpenChange,
  triggerRef,
}: {
  contexts: ApiContext[];
  repositories: ApiRepository[];
  onDiscovered: (items: ApiContext[]) => void;
  selectedId: string;
  select: (id: string) => void;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  triggerRef: RefObject<HTMLButtonElement | null>;
}) {
  const [query, setQuery] = useState("");
  const [highlightedId, setHighlightedId] = useState("");
  const [repository, setRepository] = useState<ApiRepository | null>(null);
  const [worktrees, setWorktrees] = useState<ApiContext[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const searchRef = useRef<HTMLInputElement>(null);
  const listId = useId();

  useEffect(() => {
    if (!open || !repository) return;
    const controller = new AbortController();
    setLoading(true);
    setError("");
    void api
      .GET("/api/v2/repositories/{repositoryId}/worktrees", {
        params: { path: { repositoryId: repository.id } },
        signal: controller.signal,
      })
      .then(({ data, error }) => {
        if (controller.signal.aborted) return;
        if (!data)
          throw new Error(errorDetail(error, "Unable to load worktrees"));
        setWorktrees(data.worktrees);
        onDiscovered(data.worktrees);
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(errorDetail(error, "Unable to load worktrees"));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [repository, open, attempt, onDiscovered]);

  type Item =
    | {
        kind: "repository";
        repository: ApiRepository;
        id: string;
        name: string;
        detail: string;
        path: string;
      }
    | {
        kind: "context";
        context: ApiContext;
        id: string;
        name: string;
        detail: string;
        path: string;
      };
  const contextItems = (entries: ApiContext[]): Item[] =>
    pickerResults(entries, query).map((context) => ({
      kind: "context",
      context,
      id: context.id,
      name: repository
        ? (context.worktreeName ?? "Main worktree")
        : context.name,
      detail: contextDetail(context),
      path: context.root ?? context.submittedFrom ?? "Piped diff",
    }));
  const words = query.toLowerCase().trim().split(/\s+/).filter(Boolean);
  const results: Item[] = repository
    ? contextItems(worktrees)
    : [
        ...repositories
          .filter((item) =>
            words.every((word) =>
              (item.name + " " + item.root).toLowerCase().includes(word),
            ),
          )
          .map((item): Item => ({
            kind: "repository",
            repository: item,
            id: item.id,
            name: item.name,
            detail: "Repository",
            path: item.root,
          })),
        ...contextItems(contexts.filter((item) => item.kind === "capture")),
      ];
  const active =
    results.find((item) => item.id === highlightedId) ??
    results.find((item) => item.id === selectedId) ??
    results[0];
  const optionId = (id: string) => listId + "-" + id;
  const reset = () => {
    setQuery("");
    setHighlightedId("");
    setRepository(null);
    setWorktrees([]);
    setError("");
    setLoading(false);
  };
  const changeOpen = (next: boolean) => {
    if (!next) reset();
    onOpenChange(next);
  };
  const choose = (item: Item) => {
    if (item.kind === "repository") {
      setRepository(item.repository);
      setQuery("");
      setHighlightedId("");
      setWorktrees([]);
      setLoading(true);
      setError("");
      searchRef.current?.focus();
    } else {
      changeOpen(false);
      select(item.context.id);
    }
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
      onOpenChange(true);
      searchRef.current?.focus();
    };
    document.addEventListener("keydown", shortcut);
    return () => document.removeEventListener("keydown", shortcut);
  }, [onOpenChange, open]);
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
        <DialogTitle className="sr-only">
          {repository ? "Select worktree" : "Switch repository"}
        </DialogTitle>
        <DialogDescription className="sr-only">
          Select a repository, then its worktree. Use arrow keys to navigate and
          Enter to select.
        </DialogDescription>
        {repository && (
          <div className="project-picker-step">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => {
                reset();
                searchRef.current?.focus();
              }}
              aria-label="Back to repositories"
            >
              <ArrowLeftIcon aria-hidden="true" /> Repositories
            </Button>
            <span title={worktrees[0]?.name ?? repository.name}>
              {worktrees[0]?.name ?? repository.name}
            </span>
          </div>
        )}
        <div className="project-picker-search">
          <SearchIcon className="size-(--icon-base)" aria-hidden="true" />
          <Input
            ref={searchRef}
            role="combobox"
            aria-label={repository ? "Search worktrees" : "Search repositories"}
            aria-autocomplete="list"
            aria-expanded={open}
            aria-controls={listId}
            aria-activedescendant={activeOptionId}
            placeholder={
              repository ? "Search worktrees…" : "Search repositories…"
            }
            value={query}
            onChange={(event) => {
              setQuery(event.target.value);
              setHighlightedId("");
            }}
            onKeyDown={(event) => {
              if (event.nativeEvent.isComposing) return;
              const index = results.findIndex((item) => item.id === active?.id);
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
                choose(active);
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
          aria-label={repository ? "Worktrees" : "Repositories"}
          aria-busy={loading}
        >
          {loading ? (
            <div className="project-picker-empty" role="status">
              <p>Loading worktrees…</p>
              <span>Checking {repository?.name}</span>
              <div className="project-picker-loading" aria-hidden="true">
                <span />
                <span />
                <span />
              </div>
            </div>
          ) : error ? (
            <div className="project-picker-empty" role="alert">
              <p>Cannot load worktrees</p>
              <span>{error}</span>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => setAttempt((current) => current + 1)}
              >
                Retry
              </Button>
            </div>
          ) : (
            results.map((item) => (
              <button
                type="button"
                role="option"
                tabIndex={-1}
                id={optionId(item.id)}
                key={item.id}
                className="project-picker-option"
                aria-selected={item.id === active?.id}
                title={item.path}
                onClick={() => choose(item)}
                onMouseMove={() => setHighlightedId(item.id)}
              >
                {item.kind === "repository" ? (
                  <FolderGit2Icon
                    className="size-(--icon-base)"
                    aria-hidden="true"
                  />
                ) : (
                  <ContextIcon context={item.context} />
                )}
                <span className="project-picker-copy">
                  <span className="project-picker-label">
                    <span className="project-picker-name">{item.name}</span>
                    <span className="project-picker-branch">
                      {item.kind === "context" &&
                        item.context.kind === "worktree" && (
                          <GitBranchIcon
                            className="size-(--icon-sm)"
                            aria-hidden="true"
                          />
                        )}
                      <span>{item.detail}</span>
                    </span>
                  </span>
                  <span className="project-picker-path">{item.path}</span>
                  {item.kind === "context" &&
                    item.context.availability === "unavailable" && (
                      <span className="project-picker-unavailable">
                        Unavailable
                      </span>
                    )}
                </span>
                {item.id === selectedId && (
                  <span className="project-picker-current">
                    <CheckIcon
                      className="size-(--icon-base)"
                      aria-hidden="true"
                    />
                    <span className="sr-only">Current worktree</span>
                  </span>
                )}
              </button>
            ))
          )}
          {!loading && !error && !results.length && (
            <div className="project-picker-empty">
              <p>
                {repository ? "No worktrees found" : "No repositories found"}
              </p>
              <span>
                {query
                  ? "Try another name or path."
                  : repository
                    ? "Create a worktree, then reopen this repository."
                    : "Run servediff in a repository to register it."}
              </span>
            </div>
          )}
        </div>
        <div className="project-picker-footer">
          <p className="project-picker-status" role="status">
            {loading
              ? "Loading worktrees"
              : results.length +
                " " +
                (repository ? "worktrees" : "repositories and captures")}
          </p>
          <div className="project-picker-shortcuts">
            <span>
              <kbd>↑ ↓</kbd> Navigate
            </span>
            <span>
              <kbd>↵</kbd> Select
            </span>
            <span>
              <kbd>esc</kbd> Close
            </span>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
