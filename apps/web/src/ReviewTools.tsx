import { ChevronDownIcon, CopyIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useAppState } from "./app-state.tsx";
import { save, saved } from "./preferences.ts";
import { commentApplicability } from "./review-model.ts";

export function ReviewTools() {
  const {
    source: { repository },
    review,
    reviewed: { isReviewed, resetReviewed },
  } = useAppState();
  const [toolsCollapsed, setToolsCollapsed] = useState(
    () => saved("review-tools-collapsed") === "true",
  );
  useEffect(() => {
    save("review-tools-collapsed", String(toolsCollapsed));
  }, [toolsCollapsed]);
  const allFiles = repository?.files ?? [];
  const additions = allFiles.reduce((sum, file) => sum + file.additions, 0);
  const deletions = allFiles.reduce((sum, file) => sum + file.deletions, 0);
  const unresolved = review.comments.filter(
    (comment) =>
      comment.status === "open" &&
      commentApplicability(comment, repository) !== "stale",
  ).length;
  const reviewedCount = allFiles.filter(isReviewed).length;
  return (
    <section className="review-tools" aria-label="Review tools">
      <Button
        type="button"
        className="review-tools-toggle"
        variant="ghost"
        aria-controls="review-tools-content"
        aria-expanded={!toolsCollapsed}
        aria-label={`${toolsCollapsed ? "Show" : "Hide"} review tools`}
        onClick={() => setToolsCollapsed((previous) => !previous)}
      >
        Review tools
        <svg viewBox="0 0 16 16" aria-hidden="true">
          <path d={toolsCollapsed ? "m4 10 4-4 4 4" : "m4 6 4 4 4-4"} />
        </svg>
      </Button>
      <div id="review-tools-content" hidden={toolsCollapsed}>
        {(!!review.comments.length || !!review.feedback) && (
          <div className="copy-comments-bar">
            {!!review.comments.length && (
              <div className="copy-comments-actions">
                <Button
                  type="button"
                  id="copy-review"
                  size="sm"
                  disabled={!unresolved}
                  onClick={() => {
                    void review.copy(false);
                  }}
                >
                  <CopyIcon aria-hidden="true" />
                  Copy review
                </Button>
                <DropdownMenu>
                  <DropdownMenuTrigger
                    render={
                      <Button
                        type="button"
                        className="copy-comments-menu-trigger"
                        variant="outline"
                        size="icon-sm"
                        aria-label="Copy options"
                        title="Copy options"
                      />
                    }
                  >
                    <ChevronDownIcon aria-hidden="true" />
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    <DropdownMenuItem
                      className="copy-comments-menu-item"
                      disabled={!unresolved}
                      onClick={() => {
                        void review.copy(false);
                      }}
                    >
                      Open
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      className="copy-comments-menu-item"
                      onClick={() => {
                        void review.copy(true);
                      }}
                    >
                      All
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              </div>
            )}
            <p id="comment-feedback" role="status" aria-live="polite">
              {review.feedback}
            </p>
          </div>
        )}
        <div className="sidebar-bottom">
          <div className="summary-label">CHANGE SUMMARY</div>
          <div className="summary-row">
            <span>Files changed</span>
            <strong id="summary-files">{allFiles.length}</strong>
          </div>
          <div className="summary-row">
            <span>Additions</span>
            <strong id="additions" className="positive">
              +{additions.toLocaleString()}
            </strong>
          </div>
          <div className="summary-row">
            <span>Deletions</span>
            <strong id="deletions" className="negative">
              −{deletions.toLocaleString()}
            </strong>
          </div>
          <div
            id="change-bar"
            className="change-bar"
            style={{ opacity: additions + deletions ? 1 : 0.15 }}
          >
            <span
              style={{
                width: `${additions + deletions ? (additions / (additions + deletions)) * 100 : 0}%`,
              }}
            />
          </div>
          <div className="review-progress">
            <span id="review-count">
              {reviewedCount} of {allFiles.length} reviewed
            </span>
            <Button
              type="button"
              id="reset-reviewed"
              variant="outline"
              size="xs"
              disabled={reviewedCount === 0}
              title="Clear all viewed files"
              onClick={resetReviewed}
            >
              Reset viewed
            </Button>
          </div>
        </div>
      </div>
    </section>
  );
}
