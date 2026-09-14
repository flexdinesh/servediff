import type { RepositoryDiff } from "@servediff/shared";
import {
  CheckCircle2Icon,
  ChevronDownIcon,
  CircleDotIcon,
  CopyIcon,
  PencilIcon,
  RotateCcwIcon,
  SendIcon,
  Trash2Icon,
  TriangleAlertIcon,
  XIcon,
} from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/textarea";
import { useAppState } from "./app-state.tsx";
import { commentApplicability, type ReviewComment } from "./review-model.ts";

function location(comment: ReviewComment) {
  return `${comment.path}:${comment.start}${comment.end !== comment.start ? `–${comment.end}` : ""}`;
}

// Both the diff annotations and sidebar edit the same page-level draft.
export function DraftComment() {
  const { draft, setDraft, review } = useAppState();
  return draft ? (
    <CommentEditor
      draft={draft}
      onChange={(body) =>
        setDraft((previous) => (previous ? { ...previous, body } : null))
      }
      onSave={review.submit}
      onCancel={review.cancel}
    />
  ) : null;
}

export function ReviewCommentCard({
  comment,
  sidebar = false,
}: {
  comment: ReviewComment;
  sidebar?: boolean;
}) {
  const {
    source: { repository },
    display: { layout },
    review,
    navigateComment,
  } = useAppState();
  const file = repository?.files.find((file) => file.path === comment.path);
  const oneSided =
    file?.status === "A" || file?.status === "D" || file?.status === "?";
  return (
    <CommentCard
      comment={comment}
      sidebar={sidebar}
      layout={layout}
      oneSided={oneSided}
      repository={repository}
      onNavigate={navigateComment}
      onEdit={(comment) => {
        if (review.edit(comment)) navigateComment(comment);
      }}
      onToggle={review.toggle}
      onDelete={review.remove}
      onCopy={review.copyComment}
    />
  );
}

export function CommentEditor({
  draft,
  onChange,
  onSave,
  onCancel,
}: {
  draft: ReviewComment;
  onChange: (body: string) => void;
  onSave: () => void;
  onCancel: () => void;
}) {
  const input = useRef<HTMLTextAreaElement>(null);
  const inputId = useId();
  const errorId = useId();
  const [error, setError] = useState("");
  const id = draft.id;
  useEffect(() => {
    if (!id) return;
    input.current?.focus({ preventScroll: true });
    input.current?.scrollIntoView({ block: "nearest" });
  }, [id]);
  function saveComment() {
    if (!draft.body.trim()) {
      setError("Enter a comment before saving.");
      input.current?.focus();
      return;
    }
    setError("");
    onSave();
  }
  return (
    <form
      className="comment-editor-form"
      onSubmit={(event) => {
        event.preventDefault();
        saveComment();
      }}
    >
      <Card size="sm" className="comment-editor">
        <CardHeader className="comment-editor-header">
          <CardTitle className="comment-editor-title">
            <span>
              New comment <small>· {location(draft)}</small>
            </span>
          </CardTitle>
        </CardHeader>
        <CardContent className="comment-editor-content">
          <label className="comment-editor-label" htmlFor={inputId}>
            Review comment
          </label>
          <Textarea
            id={inputId}
            ref={input}
            placeholder="Leave a review comment…"
            aria-invalid={!!error}
            aria-describedby={error ? errorId : undefined}
            rows={3}
            value={draft.body}
            onChange={(event) => {
              if (event.target.value.trim()) setError("");
              onChange(event.target.value);
            }}
            onKeyDown={(event) => {
              event.stopPropagation();
              if (event.key === "Escape") {
                event.preventDefault();
                onCancel();
              }
              if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
                event.preventDefault();
                saveComment();
              }
            }}
          />
          {!!error && (
            <p id={errorId} className="comment-editor-error" role="alert">
              {error}
            </p>
          )}
        </CardContent>
        <CardFooter className="comment-actions">
          <span>⌘ / Ctrl + Enter to save</span>
          <Button type="button" variant="ghost" size="sm" onClick={onCancel}>
            <XIcon aria-hidden="true" />
            Cancel
          </Button>
          <Button type="submit" size="sm">
            <SendIcon aria-hidden="true" />
            Save comment
          </Button>
        </CardFooter>
      </Card>
    </form>
  );
}

export function CommentCard({
  comment,
  sidebar = false,
  layout,
  oneSided,
  repository,
  onNavigate,
  onEdit,
  onToggle,
  onDelete,
  onCopy,
}: {
  comment: ReviewComment;
  sidebar?: boolean;
  layout: "split" | "unified";
  oneSided: boolean;
  repository: RepositoryDiff | null;
  onNavigate: (comment: ReviewComment) => void;
  onEdit: (comment: ReviewComment) => void;
  onToggle: (comment: ReviewComment) => void;
  onDelete: (comment: ReviewComment) => void;
  onCopy: (comment: ReviewComment) => void;
}) {
  const resolved = comment.status === "resolved";
  const applicability = commentApplicability(comment, repository);
  const stale = applicability === "stale";
  const [expanded, setExpanded] = useState(!resolved && !stale);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const contentId = useId();
  useEffect(
    () => setExpanded(!resolved && !stale),
    [comment.id, resolved, stale],
  );
  return (
    <article data-comment-id={comment.id}>
      <Card
        size="sm"
        className={`comment-card${resolved ? " resolved" : ""}${stale ? " stale" : ""}`}
        data-expanded={expanded}
        data-diff-layout={sidebar ? undefined : layout}
        data-one-sided={!sidebar && oneSided ? "" : undefined}
      >
        <CardHeader className="comment-card-header">
          <Button
            type="button"
            className="comment-collapse"
            variant="ghost"
            size="icon-xs"
            aria-controls={contentId}
            aria-expanded={expanded}
            aria-label={`${expanded ? "Collapse" : "Expand"} ${stale ? "stale " : resolved ? "resolved " : ""}comment at ${location(comment)}`}
            onClick={() => setExpanded((current) => !current)}
          >
            <ChevronDownIcon aria-hidden="true" />
          </Button>
          <CardTitle className="comment-card-title">
            {sidebar ? (
              <Button
                type="button"
                className="comment-location"
                variant="ghost"
                size="xs"
                title={location(comment)}
                onClick={() => onNavigate(comment)}
              >
                {location(comment)}
              </Button>
            ) : (
              <>
                <strong>Review comment</strong>
                <span>
                  · line {comment.start}
                  {comment.end !== comment.start ? `–${comment.end}` : ""}
                </span>
              </>
            )}
          </CardTitle>
          <CardAction>
            <Badge
              variant="outline"
              className="comment-state"
              title={`Lifecycle: ${comment.status}; applicability: ${applicability}`}
            >
              {stale ? (
                <TriangleAlertIcon aria-hidden="true" />
              ) : resolved ? (
                <CheckCircle2Icon aria-hidden="true" />
              ) : (
                <CircleDotIcon aria-hidden="true" />
              )}
              {stale ? "Stale" : resolved ? "Resolved" : "Open"}
            </Badge>
          </CardAction>
        </CardHeader>
        <div
          id={contentId}
          className="comment-collapse-panel"
          hidden={!expanded}
        >
          <CardContent className="comment-card-content">
            <p className="comment-body">{comment.body}</p>
            {sidebar && (
              <>
                {applicability !== "anchored" && (
                  <p className="comment-outdated">
                    {applicability === "other-scope"
                      ? `From ${comment.scope} changes`
                      : applicability === "stale"
                        ? "Stale — file changed; original code preserved"
                        : "Original code preserved"}
                  </p>
                )}
                <details>
                  <summary>Code context</summary>
                  <pre>{comment.code}</pre>
                </details>
              </>
            )}
          </CardContent>
          <CardFooter className="comment-actions">
            <Button
              type="button"
              className="copy-comment"
              variant="ghost"
              size={sidebar ? "icon-xs" : "icon-sm"}
              aria-label={`Copy comment at ${location(comment)}`}
              title="Copy comment"
              onClick={() => onCopy(comment)}
            >
              <CopyIcon aria-hidden="true" />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size={sidebar ? "xs" : "sm"}
              onClick={() => onEdit(comment)}
            >
              <PencilIcon aria-hidden="true" />
              Edit
            </Button>
            <Button
              type="button"
              variant="outline"
              size={sidebar ? "xs" : "sm"}
              onClick={() => onToggle(comment)}
            >
              {resolved ? (
                <RotateCcwIcon aria-hidden="true" />
              ) : (
                <CheckCircle2Icon aria-hidden="true" />
              )}
              {resolved ? "Reopen" : "Resolve"}
            </Button>
            <Button
              type="button"
              variant="destructive"
              size={sidebar ? "xs" : "sm"}
              onClick={() => setConfirmDelete(true)}
            >
              <Trash2Icon aria-hidden="true" />
              Delete
            </Button>
          </CardFooter>
        </div>
      </Card>
      <Dialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <DialogContent
          className="delete-comment-dialog"
          showCloseButton={false}
        >
          <DialogTitle>Delete comment?</DialogTitle>
          <DialogDescription>
            This permanently removes the comment. This cannot be undone.
          </DialogDescription>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => setConfirmDelete(false)}
            >
              Cancel
            </Button>
            <Button
              type="button"
              className="delete-comment-confirm"
              variant="destructive"
              onClick={() => {
                onDelete(comment);
                setConfirmDelete(false);
              }}
            >
              <Trash2Icon aria-hidden="true" />
              Delete comment
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </article>
  );
}

export function CopyDialog({
  text,
  onClose,
}: {
  text: string;
  onClose: () => void;
}) {
  const input = useRef<HTMLTextAreaElement>(null);
  return (
    <Dialog
      open
      onOpenChange={(open) => !open && onClose()}
      onOpenChangeComplete={(open) => {
        if (open) input.current?.select();
      }}
    >
      <DialogContent
        className="copy-dialog"
        showCloseButton={false}
        initialFocus={input}
      >
        <DialogTitle id="copy-dialog-title">Copy review comments</DialogTitle>
        <DialogDescription>
          Clipboard access is unavailable. Copy the selected text with ⌘C /
          Ctrl+C.
        </DialogDescription>
        <Textarea ref={input} value={text} readOnly aria-label="Comments XML" />
        <Button type="button" variant="outline" onClick={onClose}>
          Close
        </Button>
      </DialogContent>
    </Dialog>
  );
}
