import type { ChangedFile } from "@servediff/shared";

export function toggleReviewedFileState(
  reviews: Map<string, string>,
  collapsed: Set<string>,
  file: ChangedFile,
) {
  const nextReviews = new Map(reviews);
  if (nextReviews.get(file.path) === file.fingerprint) {
    nextReviews.delete(file.path);
    const nextCollapsed = new Set(collapsed);
    nextCollapsed.delete(file.path);
    return { reviews: nextReviews, collapsed: nextCollapsed };
  }
  nextReviews.set(file.path, file.fingerprint);
  const nextCollapsed = new Set(collapsed);
  nextCollapsed.add(file.path);
  return { reviews: nextReviews, collapsed: nextCollapsed };
}
