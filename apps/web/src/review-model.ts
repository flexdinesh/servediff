import type { ReviewComment } from "@diffx/shared";

export {
  anchored,
  commentApplicability,
  commentContext,
  formatComments,
  lineContext,
  parseComments,
  reviewRounds,
} from "@diffx/shared";
export type {
  CommentApplicability,
  ReviewComment,
  ReviewOrigin,
  ReviewRound,
} from "@diffx/shared";

export type CommentAnnotation =
  | { kind: "saved"; comment: ReviewComment }
  | { kind: "draft" };

export function createCommentId(
  bytes = crypto.getRandomValues(new Uint8Array(16)),
) {
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join(
    "",
  );
}
