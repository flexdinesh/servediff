import type { ApiContext } from "@diffx/api";
import {
  ArrowLeftIcon,
  ChevronRightIcon,
  CheckIcon,
  FolderGit2Icon,
  GitBranchIcon,
  GitForkIcon,
  SearchIcon,
  SquareTerminalIcon,
  SlidersHorizontalIcon,
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
import {
  contextChangeLabel,
  contextDetail,
  contextDiagnostics,
  contextIsLinkedWorktree,
  contextCheckoutLabel,
  contextHasChanges,
  contextIsPiped,
  contextTimestampLabel,
  contextUnavailableReason,
  defaultPickerFilters,
  pickerEntryAvailable,
  pickerFilterOptions,
  pickerEntries,
  pipedGroupId,
  observationDetail,
} from "./project-picker.ts";
import {
  PickerFilterControls,
  pickerFiltersChanged,
  pickerFilterSummary,
} from "./PickerFilters.tsx";

function ContextIcon({ context }: { context: ApiContext }) {
  const Icon = contextIsPiped(context) ? SquareTerminalIcon : FolderGit2Icon;
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
      aria-label="Switch review"
      aria-haspopup="dialog"
      aria-expanded={open}
      title={current ? contextDiagnostics(current) : "Switch review"}
      onClick={onOpen}
    >
      {current && <ContextIcon context={current} />}
      <span className="context-switcher-name">
        {current
          ? contextIsPiped(current)
            ? "Piped"
            : current.name
          : "Switch review"}
      </span>
      {current && contextIsPiped(current) && (
        <span className="context-switcher-branch">
          · {contextTimestampLabel(current)}
        </span>
      )}
      {current && !contextIsPiped(current) && (
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
      {current && contextIsLinkedWorktree(current) && (
        <span
          className="context-switcher-worktree"
          role="img"
          aria-label={contextCheckoutLabel(current)}
          title={contextCheckoutLabel(current)}
        >
          <GitForkIcon className="size-(--icon-sm)" aria-hidden="true" />
          <span className="context-switcher-worktree-name" aria-hidden="true">
            Worktree:{" "}
            {current.worktreeName || current.observation?.worktreeName}
          </span>
        </span>
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
  const [repositoryId, setRepositoryId] = useState("");
  const piped = repositoryId === pipedGroupId;
  const [highlightedId, setHighlightedId] = useState("");
  const [filters, setFilters] = useState(defaultPickerFilters);
  const [filtersOpen, setFiltersOpen] = useState(false);
  const searchRef = useRef<HTMLInputElement>(null);
  const listId = useId();
  const filtersId = useId();
  const results = pickerEntries(contexts, query, repositoryId, filters);
  const selectable = results.filter(pickerEntryAvailable);
  const repository = contexts.find(
    (context) => context.repositoryId === repositoryId,
  );
  const active =
    selectable.find((context) => context.id === highlightedId) ??
    (!query.trim()
      ? selectable.find((entry) =>
          entry.kind === "context"
            ? entry.context.id === selectedId
            : entry.contexts.some((context) => context.id === selectedId),
        )
      : undefined) ??
    selectable[0];
  const optionId = (id: string) => `${listId}-${id}`;
  const changeOpen = (next: boolean) => {
    if (!next) {
      setRepositoryId("");
      setQuery("");
      setHighlightedId("");
    }
    onOpenChange(next);
  };
  const choose = (id: string) => {
    const entry = results.find((result) => result.id === id);
    if (!entry || !pickerEntryAvailable(entry)) return;
    if (entry.kind !== "context") {
      setRepositoryId(
        entry.kind === "piped"
          ? pipedGroupId
          : (entry.context.repositoryId ?? ""),
      );
      if (entry.kind === "piped") setQuery("");
      setHighlightedId("");
      searchRef.current?.focus();
      return;
    }
    changeOpen(false);
    select(id);
  };
  const back = () => {
    setRepositoryId("");
    setHighlightedId("");
    searchRef.current?.focus();
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
        <DialogTitle className="sr-only">Switch review</DialogTitle>
        <DialogDescription className="sr-only">
          Choose a repository or Piped, then a collected snapshot. Search
          branches, worktrees, hosts, and runs. Use arrow keys to navigate and
          Enter to choose.
        </DialogDescription>
        <div className="project-picker-header">
          <div className="project-picker-search">
            {repositoryId ? (
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                onClick={back}
                aria-label="Back to reviews"
              >
                <ArrowLeftIcon aria-hidden="true" />
              </Button>
            ) : (
              <SearchIcon className="size-(--icon-base)" aria-hidden="true" />
            )}
            <Input
              ref={searchRef}
              role="combobox"
              aria-label={piped ? "Search Piped" : "Search reviews"}
              aria-autocomplete="list"
              aria-expanded={open}
              aria-controls={listId}
              aria-activedescendant={activeOptionId}
              placeholder={
                piped
                  ? "Search Piped…"
                  : repositoryId
                    ? `Search ${repository?.name ?? "observations"}…`
                    : "Search repositories or Piped…"
              }
              value={query}
              onChange={(event) => {
                setQuery(event.target.value);
                setHighlightedId("");
              }}
              onKeyDown={(event) => {
                if (event.nativeEvent.isComposing) return;
                const index = selectable.findIndex(
                  (context) => context.id === active?.id,
                );
                if (event.key === "ArrowDown" || event.key === "ArrowUp") {
                  event.preventDefault();
                  const next =
                    selectable[
                      Math.max(
                        0,
                        Math.min(
                          selectable.length - 1,
                          index + (event.key === "ArrowDown" ? 1 : -1),
                        ),
                      )
                    ];
                  if (next) setHighlightedId(next.id);
                } else if (
                  event.key === "ArrowLeft" &&
                  repositoryId &&
                  !query
                ) {
                  event.preventDefault();
                  back();
                } else if (event.key === "Enter" && active) {
                  event.preventDefault();
                  choose(active.id);
                }
              }}
            />
            {!piped && (
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label="Filters"
                title="Filter repositories"
                aria-expanded={filtersOpen}
                aria-controls={filtersId}
                onClick={() => setFiltersOpen((value) => !value)}
              >
                <SlidersHorizontalIcon aria-hidden="true" />
              </Button>
            )}
            <DialogClose render={<Button variant="ghost" size="icon-sm" />}>
              <XIcon aria-hidden="true" />
              <span className="sr-only">Close</span>
            </DialogClose>
          </div>
          <div id={filtersId} hidden={piped || !filtersOpen}>
            <PickerFilterControls
              filters={filters}
              options={pickerFilterOptions(contexts)}
              onChange={(next) => {
                setFilters(next);
                setHighlightedId("");
              }}
            />
          </div>
          {!piped && (
            <div className="project-picker-filter-summary">
              <span title={pickerFilterSummary(filters)}>
                {pickerFilterSummary(filters)}
              </span>
              {pickerFiltersChanged(filters) && (
                <Button
                  variant="ghost"
                  size="xs"
                  onClick={() => {
                    setFilters(defaultPickerFilters);
                    setHighlightedId("");
                  }}
                >
                  Clear filters
                </Button>
              )}
            </div>
          )}
          {piped && (
            <div className="project-picker-filter-summary">
              Piped · Newest first
            </div>
          )}
        </div>
        <div
          id={listId}
          className="project-picker-results"
          role="listbox"
          aria-label={
            piped ? "Piped" : repositoryId ? "Collected snapshots" : "Reviews"
          }
        >
          {results.map((entry) => {
            const context = entry.context;
            const grouped = entry.kind !== "context";
            const pipedContext = contextIsPiped(context);
            const available = pickerEntryAvailable(entry);
            return (
              <button
                type="button"
                role="option"
                tabIndex={-1}
                id={optionId(entry.id)}
                key={entry.id}
                className="project-picker-option"
                data-piped-group={entry.kind === "piped" || undefined}
                data-piped-context={(!grouped && pipedContext) || undefined}
                aria-selected={entry.id === active?.id}
                aria-disabled={!available}
                disabled={!available}
                title={`${contextDiagnostics(context)}${available ? "" : `\n${contextUnavailableReason()}`}`}
                onClick={() => choose(entry.id)}
                onMouseMove={() => {
                  if (available) setHighlightedId(entry.id);
                }}
              >
                <ContextIcon context={context} />
                <span className="project-picker-copy">
                  <span className="project-picker-label">
                    <span className="project-picker-name">
                      {entry.kind === "piped"
                        ? "Piped"
                        : pipedContext
                          ? contextTimestampLabel(context)
                          : grouped
                            ? context.name
                            : context.observation
                              ? context.worktreeName || context.name
                              : context.name}
                    </span>
                    {(!pipedContext || grouped) && (
                      <span className="project-picker-branch">
                        {!grouped && !pipedContext && (
                          <GitBranchIcon
                            className="size-(--icon-sm)"
                            aria-hidden="true"
                          />
                        )}
                        <span>
                          {grouped
                            ? `${entry.contexts.length} ${entry.contexts.length === 1 ? "snapshot" : "snapshots"}`
                            : contextDetail(context)}
                        </span>
                      </span>
                    )}
                  </span>
                  {!available && (
                    <span className="project-picker-unavailable-reason">
                      {contextUnavailableReason()}
                    </span>
                  )}
                  {(!pipedContext || grouped) && (
                    <span className="project-picker-path">
                      {!grouped && contextIsLinkedWorktree(context) && (
                        <span className="project-picker-kind">Worktree · </span>
                      )}
                      {entry.kind === "piped"
                        ? "Choose a snapshot"
                        : grouped
                          ? context.observation?.remoteUrl ||
                            "Choose a collected snapshot"
                          : observationDetail(context)}
                    </span>
                  )}
                </span>
                {!grouped && !pipedContext && context.stale && (
                  <span className="project-picker-stale">Stale</span>
                )}
                {!grouped && (
                  <span
                    className="project-picker-changes"
                    data-has-changes={contextHasChanges(context)}
                    data-availability={context.availability}
                    title={
                      pipedContext
                        ? "Changed files in this snapshot"
                        : "Changed files across staged, unstaged, and untracked changes"
                    }
                  >
                    {contextChangeLabel(context)}
                  </span>
                )}
                {grouped && (
                  <ChevronRightIcon
                    className="size-(--icon-base)"
                    aria-hidden="true"
                  />
                )}
                {!grouped && context.id === selectedId && (
                  <span className="project-picker-current">
                    <CheckIcon
                      className="size-(--icon-base)"
                      aria-hidden="true"
                    />
                    <span className="sr-only">Current review</span>
                  </span>
                )}
              </button>
            );
          })}
          {!results.length && (
            <div className="project-picker-empty">
              <p>{repositoryId ? "No snapshots found" : "No reviews found"}</p>
              <span>Try another search or clear filters.</span>
            </div>
          )}
        </div>
        <div className="project-picker-footer">
          <p className="project-picker-status" role="status">
            {results.length
              ? `${results.length} ${results.length === 1 ? "result" : "results"}`
              : repositoryId
                ? "No matching snapshots"
                : "No matching reviews"}
          </p>
          <div className="project-picker-shortcuts">
            <span>
              <kbd>↑ ↓</kbd> Navigate
            </span>
            <span>
              <kbd>Enter</kbd> {repositoryId ? "Review" : "Choose"}
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
