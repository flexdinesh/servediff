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

export function pickerResults(contexts: ApiContext[], query: string) {
  const words = query.toLowerCase().trim().split(/\s+/).filter(Boolean);
  return contexts
    .map((context) => {
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
      return {
        context,
        matches,
        score: matches.reduce((sum, value) => sum + value, 0),
      };
    })
    .filter(({ matches }) => matches.every((score) => score > 0))
    .sort(
      (left, right) =>
        right.score - left.score ||
        right.context.lastSubmittedAt - left.context.lastSubmittedAt ||
        left.context.name.localeCompare(right.context.name) ||
        contextDetail(left.context).localeCompare(
          contextDetail(right.context),
        ) ||
        left.context.id.localeCompare(right.context.id),
    )
    .map(({ context }) => context);
}
