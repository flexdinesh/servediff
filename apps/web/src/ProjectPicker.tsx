import type { ApiContext } from "@servediff/api";
import {
  ArrowLeftIcon,
  ChevronRightIcon,
  CheckIcon,
  FolderGit2Icon,
  GitBranchIcon,
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
  contextHasChanges,
  contextUnavailableReason,
  defaultPickerFilters,
  pickerEntryAvailable,
  pickerFilterOptions,
  pickerEntries,
  observationDetail,
} from "./project-picker.ts";
import {
  PickerFilterControls,
  pickerFiltersChanged,
  pickerFilterSummary,
} from "./PickerFilters.tsx";

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
          ? `${current.name} · ${contextDetail(current)}${current.worktreeName ? ` · Worktree: ${current.worktreeName}` : ""}\n${observationDetail(current)}\n${current.root ?? "Piped diff"}`
          : "Switch repository"
      }
      onClick={onOpen}
    >
      {current && <ContextIcon context={current} />}
      <span className="context-switcher-name">
        {current?.name ?? "Switch repository"}
      </span>
      {current && current.kind !== "capture" && (
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
  const [repositoryId, setRepositoryId] = useState("");
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
      ? selectable.find(
          (entry) =>
            entry.kind === "context" && entry.context.id === selectedId,
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
    if (entry?.kind === "repository") {
      setRepositoryId(entry.context.repositoryId ?? "");
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
        <DialogTitle className="sr-only">Switch repository</DialogTitle>
        <DialogDescription className="sr-only">
          Choose a repository, then a collected snapshot. Search branches,
          worktrees, hosts, and runs. Use arrow keys to navigate and Enter to
          choose.
        </DialogDescription>
        <div className="project-picker-header">
          <div className="project-picker-search">
            {repositoryId ? (
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                onClick={back}
                aria-label="Back to repositories"
              >
                <ArrowLeftIcon aria-hidden="true" />
              </Button>
            ) : (
              <SearchIcon className="size-(--icon-base)" aria-hidden="true" />
            )}
            <Input
              ref={searchRef}
              role="combobox"
              aria-label="Search repositories"
              aria-autocomplete="list"
              aria-expanded={open}
              aria-controls={listId}
              aria-activedescendant={activeOptionId}
              placeholder={
                repositoryId
                  ? `Search ${repository?.name ?? "observations"}…`
                  : "Search repositories, branches, hosts…"
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
            <DialogClose render={<Button variant="ghost" size="icon-sm" />}>
              <XIcon aria-hidden="true" />
              <span className="sr-only">Close</span>
            </DialogClose>
          </div>
          <div id={filtersId} hidden={!filtersOpen}>
            <PickerFilterControls
              filters={filters}
              options={pickerFilterOptions(contexts)}
              onChange={(next) => {
                setFilters(next);
                setHighlightedId("");
              }}
            />
          </div>
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
        </div>
        <div
          id={listId}
          className="project-picker-results"
          role="listbox"
          aria-label={repositoryId ? "Collected snapshots" : "Repositories"}
        >
          {results.map((entry) => {
            const context = entry.context;
            const grouped = entry.kind === "repository";
            const available = pickerEntryAvailable(entry);
            return (
              <button
                type="button"
                role="option"
                tabIndex={-1}
                id={optionId(entry.id)}
                key={entry.id}
                className="project-picker-option"
                aria-selected={entry.id === active?.id}
                aria-disabled={!available}
                disabled={!available}
                title={`${context.root ?? context.name}\n${available ? observationDetail(context) : contextUnavailableReason(context)}`}
                onClick={() => choose(entry.id)}
                onMouseMove={() => {
                  if (available) setHighlightedId(entry.id);
                }}
              >
                <ContextIcon context={context} />
                <span className="project-picker-copy">
                  <span className="project-picker-label">
                    <span className="project-picker-name">
                      {grouped
                        ? context.name
                        : context.observation
                          ? context.worktreeName || context.name
                          : context.name}
                    </span>
                    <span className="project-picker-branch">
                      {!grouped && context.kind !== "capture" && (
                        <GitBranchIcon
                          className="size-(--icon-sm)"
                          aria-hidden="true"
                        />
                      )}
                      <span>
                        {grouped
                          ? `${entry.contexts.length} ${entry.contexts.length === 1 ? "snapshot" : "snapshots"}`
                          : context.kind === "capture"
                            ? "Snapshot"
                            : contextDetail(context)}
                      </span>
                    </span>
                  </span>
                  {!available && (
                    <span className="project-picker-unavailable-reason">
                      {contextUnavailableReason(context)}
                    </span>
                  )}
                  <span className="project-picker-path">
                    {!context.observation && context.worktreeName && (
                      <span className="project-picker-kind">Worktree · </span>
                    )}
                    {grouped
                      ? context.observation?.remoteUrl ||
                        "Choose a collected snapshot"
                      : observationDetail(context)}
                  </span>
                </span>
                {!grouped && (
                  <span
                    className="project-picker-changes"
                    data-has-changes={contextHasChanges(context)}
                    data-availability={context.availability}
                    title={
                      context.kind === "capture"
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
                    <span className="sr-only">
                      Current<span className="sr-only"> repository</span>
                    </span>
                  </span>
                )}
              </button>
            );
          })}
          {!results.length && (
            <div className="project-picker-empty">
              <p>
                {repositoryId ? "No snapshots found" : "No repositories found"}
              </p>
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
                : "No matching repositories"}
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
