import { useState } from "react";
import { ChevronDownIcon, Trash2Icon } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@/components/ui/dialog";
import { TabsContent } from "@/components/ui/tabs";
import { useAppState } from "./app-state.tsx";
import { DraftComment, ReviewCommentCard } from "./review.tsx";
import {
  anchored,
  commentApplicability,
  type ReviewComment,
} from "./review-model.ts";

type CommentState = "open" | "resolved" | "stale";
type CommentFilter = CommentState | "all";

const COMMENT_FILTERS: readonly CommentFilter[] = [
  "open",
  "resolved",
  "stale",
  "all",
];
const DELETE_FILTERS: readonly CommentFilter[] = [
  "all",
  "open",
  "resolved",
  "stale",
];

function filterLabel(filter: CommentFilter) {
  if (filter === "open") return "Open";
  if (filter === "resolved") return "Resolved";
  if (filter === "stale") return "Stale";
  return "All";
}

export function CommentsPanel() {
  const [filter, setFilter] = useState<CommentFilter>("open");
  const [deleteSelection, setDeleteSelection] = useState<CommentFilter | null>(
    null,
  );
  const {
    source: { repository },
    draft,
    review,
    navigateComment,
  } = useAppState();
  const state = (comment: ReviewComment): CommentState =>
    commentApplicability(comment, repository) === "stale"
      ? "stale"
      : comment.status;
  const counts = {
    open: review.comments.filter((comment) => state(comment) === "open").length,
    resolved: review.comments.filter((comment) => state(comment) === "resolved")
      .length,
    stale: review.comments.filter((comment) => state(comment) === "stale")
      .length,
  };
  const activeCount =
    filter === "all" ? review.comments.length : counts[filter];
  const deleteCount =
    deleteSelection === null
      ? 0
      : deleteSelection === "all"
        ? review.comments.length
        : counts[deleteSelection];
  const currentRound = review.rounds.find((round) => round.current);
  const earlierRounds = review.rounds.filter((round) => !round.current);
  const visible = (comment: ReviewComment) =>
    filter === "all" || filter === state(comment);
  const currentComments = currentRound?.comments.filter(visible) ?? [];
  const visibleEarlierComments = earlierRounds.reduce(
    (count, round) => count + round.comments.filter(visible).length,
    0,
  );
  return (
    <TabsContent
      value="comments"
      id="comments-panel"
      aria-label="Review comments"
    >
      <div className="comment-panel-header">
        {!!review.comments.length && (
          <>
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <Button
                    type="button"
                    className="comment-filter-trigger"
                    variant="outline"
                    size="xs"
                    aria-label={`Filter comments: ${filterLabel(filter)}`}
                  />
                }
              >
                {filterLabel(filter)}
                <Badge variant="outline" className="comment-filter-count">
                  {activeCount}
                </Badge>
                <ChevronDownIcon aria-hidden="true" />
              </DropdownMenuTrigger>
              <DropdownMenuContent
                align="start"
                className="comment-filter-menu"
                aria-label="Filter comments"
              >
                <DropdownMenuRadioGroup
                  value={filter}
                  onValueChange={(value: unknown) => {
                    if (
                      value === "open" ||
                      value === "resolved" ||
                      value === "stale" ||
                      value === "all"
                    )
                      setFilter(value);
                  }}
                >
                  {COMMENT_FILTERS.map((value) => (
                    <DropdownMenuRadioItem
                      key={value}
                      value={value}
                      closeOnClick
                      className="comment-filter-option"
                    >
                      <span>{filterLabel(value)}</span>
                      <Badge variant="outline" className="comment-filter-count">
                        {value === "all"
                          ? review.comments.length
                          : counts[value]}
                      </Badge>
                    </DropdownMenuRadioItem>
                  ))}
                </DropdownMenuRadioGroup>
              </DropdownMenuContent>
            </DropdownMenu>
            <div className="bulk-delete-actions">
              <Button
                type="button"
                className="bulk-delete-primary"
                variant="destructive"
                size="xs"
                onClick={() => setDeleteSelection("all")}
              >
                <Trash2Icon aria-hidden="true" />
                Delete
              </Button>
              <DropdownMenu>
                <DropdownMenuTrigger
                  render={
                    <Button
                      type="button"
                      className="bulk-delete-menu-trigger"
                      variant="outline"
                      size="icon-xs"
                      aria-label="Delete options"
                      title="Delete options"
                    />
                  }
                >
                  <ChevronDownIcon aria-hidden="true" />
                </DropdownMenuTrigger>
                <DropdownMenuContent
                  align="start"
                  className="bulk-delete-menu"
                  aria-label="Delete comments"
                >
                  {DELETE_FILTERS.map((value) => {
                    const count =
                      value === "all" ? review.comments.length : counts[value];
                    return (
                      <DropdownMenuItem
                        key={value}
                        className="bulk-delete-option"
                        disabled={count === 0}
                        onClick={() => setDeleteSelection(value)}
                      >
                        <span>{filterLabel(value)}</span>
                        <Badge
                          variant="outline"
                          className="comment-filter-count"
                        >
                          {count}
                        </Badge>
                      </DropdownMenuItem>
                    );
                  })}
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          </>
        )}
      </div>
      {draft &&
        (!anchored(draft, repository) ? (
          <DraftComment />
        ) : (
          <Button
            type="button"
            variant="outline"
            size="xs"
            onClick={() => navigateComment(draft)}
          >
            Continue draft
          </Button>
        ))}
      {currentRound && !!currentComments.length && (
        <section className="review-round" aria-label="Current review">
          <h2>Current review</h2>
          {currentComments.map((comment) => (
            <ReviewCommentCard key={comment.id} comment={comment} sidebar />
          ))}
        </section>
      )}
      {earlierRounds.map((round, index) => {
        const open = round.comments.filter(
          (comment) => state(comment) === "open",
        ).length;
        const resolved = round.comments.filter(
          (comment) => state(comment) === "resolved",
        ).length;
        const stale = round.comments.length - open - resolved;
        const comments = round.comments.filter(visible);
        if (!comments.length) return null;
        return (
          <details className="review-round earlier-review" key={round.key}>
            <summary>
              Earlier review {earlierRounds.length - index} · {open} open ·{" "}
              {resolved} resolved
              {!!stale && ` · ${stale} stale`}
            </summary>
            {comments.map((comment) => (
              <ReviewCommentCard key={comment.id} comment={comment} sidebar />
            ))}
          </details>
        );
      })}
      {!review.comments.length && (
        <p className="comment-empty">
          Hover a code line and click + to leave feedback. Select several lines
          to comment on a range. Saved comments persist through the local server
          and can be copied for your coding agent.
        </p>
      )}
      {!!review.comments.length &&
        !currentRound &&
        !!visibleEarlierComments && (
          <p className="comment-empty">
            No comments in this review yet. Changes after copied feedback start
            a new review round; add comments from the Files tab.
          </p>
        )}
      {!!review.comments.length &&
        !currentComments.length &&
        !visibleEarlierComments && (
          <p className="comment-empty">No {filter} comments.</p>
        )}
      <Dialog
        open={deleteSelection !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteSelection(null);
        }}
      >
        <DialogContent
          className="delete-comment-dialog"
          showCloseButton={false}
        >
          <DialogTitle>
            Delete{" "}
            {deleteSelection ? filterLabel(deleteSelection).toLowerCase() : ""}{" "}
            comments?
          </DialogTitle>
          <DialogDescription>
            This permanently removes {deleteCount}{" "}
            {deleteSelection === "all" ? "" : `${deleteSelection} `}
            {deleteCount === 1 ? "comment" : "comments"}. This cannot be undone.
          </DialogDescription>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => setDeleteSelection(null)}
            >
              Cancel
            </Button>
            <Button
              type="button"
              className="delete-comment-confirm"
              variant="destructive"
              onClick={() => {
                if (deleteSelection)
                  void review.removeComments(deleteSelection);
                setDeleteSelection(null);
              }}
            >
              <Trash2Icon aria-hidden="true" />
              Delete comments
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </TabsContent>
  );
}
