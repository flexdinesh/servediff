import type { ApiContext } from "@servediff/api";

export function contextDetail(context: ApiContext) {
  return context.kind === "capture"
    ? "Piped diff"
    : (context.branch ?? "Branch unknown");
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

export function pickerGroups(contexts: ApiContext[], query: string) {
  const words = query.toLowerCase().trim().split(/\s+/).filter(Boolean);
  const groups = new Map<
    string,
    {
      id: string;
      name: string;
      contexts: ApiContext[];
      score: number;
      recent: number;
    }
  >();
  const scores = new Map<string, number>();
  for (const context of contexts) {
    const values = [
      context.name,
      context.branch,
      context.worktreeName,
      context.root,
    ]
      .filter((value) => typeof value === "string")
      .map((value) => value.toLowerCase());
    const matches = words.map((word) =>
      Math.max(...values.map((value) => matchScore(value, word))),
    );
    if (matches.some((score) => score === 0)) continue;
    const score = matches.reduce((sum, value) => sum + value, 0);
    scores.set(context.id, score);
    const id =
      context.kind === "capture"
        ? "captures"
        : (context.repositoryId ?? context.id);
    const group = groups.get(id) ?? {
      id,
      name: context.kind === "capture" ? "Piped diffs" : context.name,
      contexts: [],
      score: 0,
      recent: 0,
    };
    group.contexts.push(context);
    group.score = Math.max(group.score, score);
    group.recent = Math.max(group.recent, context.lastSubmittedAt);
    groups.set(id, group);
  }
  return [...groups.values()]
    .sort(
      (left, right) =>
        right.score - left.score ||
        Number(left.id === "captures") - Number(right.id === "captures") ||
        right.recent - left.recent ||
        left.id.localeCompare(right.id),
    )
    .map((group) => ({
      ...group,
      contexts: group.contexts.sort(
        (left, right) =>
          (scores.get(right.id) ?? 0) - (scores.get(left.id) ?? 0) ||
          Number(left.worktreeName !== null) -
            Number(right.worktreeName !== null) ||
          contextDetail(left).localeCompare(contextDetail(right)) ||
          left.id.localeCompare(right.id),
      ),
    }));
}
