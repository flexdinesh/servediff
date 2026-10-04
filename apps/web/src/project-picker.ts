import type { ApiContext } from "@servediff/api";

export type PickerFilters = {
  availability: "all" | "available" | "unavailable";
  changes: "all" | "changed" | "unchanged";
  hosts: string[];
  branches: string[];
  worktrees: string[];
};

export const defaultPickerFilters: PickerFilters = {
  availability: "available",
  changes: "all",
  hosts: [],
  branches: [],
  worktrees: [],
};

export function pickerFilterValues(context: ApiContext) {
  return {
    hosts: context.observation?.hostname ?? "",
    branches: context.branch ?? context.observation?.branch ?? "",
    worktrees: context.worktreeName ?? context.observation?.worktreeName ?? "",
  };
}

export function pickerFilterOptions(contexts: ApiContext[]) {
  const values = contexts.map(pickerFilterValues);
  const options = (key: "hosts" | "branches" | "worktrees") =>
    [...new Set(values.map((value) => value[key]).filter(Boolean))].sort(
      (left, right) => left.localeCompare(right),
    );
  return {
    hosts: options("hosts"),
    branches: options("branches"),
    worktrees: options("worktrees"),
  };
}

function matchesFilters(context: ApiContext, filters: PickerFilters) {
  if (
    filters.availability !== "all" &&
    context.availability !== filters.availability
  )
    return false;
  if (
    filters.changes !== "all" &&
    (context.availability === "unavailable" ||
      context.changedFileCount === null ||
      (filters.changes === "changed"
        ? context.changedFileCount === 0
        : context.changedFileCount > 0))
  )
    return false;
  const values = pickerFilterValues(context);
  return (
    (!filters.hosts.length || filters.hosts.includes(values.hosts)) &&
    (!filters.branches.length || filters.branches.includes(values.branches)) &&
    (!filters.worktrees.length || filters.worktrees.includes(values.worktrees))
  );
}

export function contextDetail(context: ApiContext) {
  return context.kind === "capture"
    ? "Piped diff"
    : (context.branch ?? "Branch unknown");
}

export function contextHasChanges(context: ApiContext) {
  return (
    context.availability !== "unavailable" &&
    context.changedFileCount !== null &&
    context.changedFileCount > 0
  );
}

export function contextChangeLabel(context: ApiContext) {
  if (context.availability === "unavailable") return "Unavailable";
  if (context.changedFileCount === null) return "Status unknown";
  if (context.changedFileCount === 0) return "No changes";
  return `${context.changedFileCount} changed ${context.changedFileCount === 1 ? "file" : "files"}`;
}

function matchScore(value: string, word: string): number {
  if (value === word) return 4;
  if (value.startsWith(word)) return 3;
  if (value.includes(word)) return 2;
  let position = 0;
  for (const letter of value) {
    if (letter === word[position]) position++;
  }
  return position === word.length ? 1 : 0;
}

function pickerRecency(context: ApiContext) {
  return context.kind === "observation"
    ? context.lastSubmittedAt
    : context.lastChangedAt;
}

export function pickerResults(
  contexts: ApiContext[],
  query: string,
  filters: PickerFilters = defaultPickerFilters,
) {
  const words = query.toLowerCase().trim().split(/\s+/).filter(Boolean);
  return contexts
    .filter((context) => matchesFilters(context, filters))
    .map((context) => {
      const values = [
        context.name,
        context.branch,
        context.worktreeName,
        context.root,
        context.observation?.repositoryName,
        context.observation?.remoteUrl,
        context.observation?.hostname,
        context.observation?.sourceId,
        context.observation?.runId,
        context.observation?.agent,
        context.observation?.trigger,
      ]
        .filter((value) => typeof value === "string")
        .map((value) => value.toLowerCase());
      const matches = words.map((word) =>
        Math.max(...values.map((value) => matchScore(value, word))),
      );
      return {
        context,
        matches,
        score: matches.reduce((sum, value) => sum + value, 0),
      };
    })
    .filter(({ matches }) => matches.every((score) => score > 0))
    .sort(
      (left, right) =>
        Number(contextHasChanges(right.context)) -
          Number(contextHasChanges(left.context)) ||
        pickerRecency(right.context) - pickerRecency(left.context) ||
        right.score - left.score ||
        left.context.name.localeCompare(right.context.name) ||
        contextDetail(left.context).localeCompare(
          contextDetail(right.context),
        ) ||
        left.context.id.localeCompare(right.context.id),
    )
    .map(({ context }) => context);
}

export function observationDetail(context: ApiContext) {
  const observation = context.observation;
  if (!observation)
    return context.root ?? context.submittedFrom ?? "Piped diff";
  const collected = new Date(observation.collectedAt).toLocaleString(
    undefined,
    {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    },
  );
  return [
    observation.hostname || observation.sourceId,
    observation.runId || observation.sourceId,
    collected,
  ]
    .filter(Boolean)
    .join(" · ");
}

export type PickerEntry =
  | {
      id: string;
      kind: "repository";
      context: ApiContext;
      contexts: ApiContext[];
    }
  | { id: string; kind: "context"; context: ApiContext };

export function pickerEntryAvailable(entry: PickerEntry) {
  return entry.kind === "repository"
    ? entry.contexts.some((context) => context.availability === "available")
    : entry.context.availability === "available";
}

export function contextUnavailableReason(context: ApiContext) {
  return context.kind === "worktree"
    ? "No collected snapshot for this checkout"
    : "Snapshot unavailable";
}

export function pickerEntries(
  contexts: ApiContext[],
  query: string,
  repositoryId: string,
  filters: PickerFilters = defaultPickerFilters,
) {
  const results = pickerResults(contexts, query, filters);
  if (repositoryId)
    return results
      .filter((context) => context.repositoryId === repositoryId)
      .map((context): PickerEntry => ({
        id: context.id,
        kind: "context",
        context,
      }));
  const entries: PickerEntry[] = [];
  const repositories = new Map<string, PickerEntry>();
  for (const context of results) {
    if (context.kind !== "observation" || !context.repositoryId) {
      entries.push({ id: context.id, kind: "context", context });
      continue;
    }
    const existing = repositories.get(context.repositoryId);
    if (existing?.kind === "repository") {
      existing.contexts.push(context);
    } else {
      const entry: PickerEntry = {
        id: `repository-${context.repositoryId}`,
        kind: "repository",
        context,
        contexts: [context],
      };
      repositories.set(context.repositoryId, entry);
      entries.push(entry);
    }
  }
  return entries;
}
