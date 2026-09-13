import type { FileDiffMetadata, SelectedLineRange } from "@pierre/diffs";
import { api, errorDetail } from "@servediff/api";
import type {
  ChangedFile,
  RepositoryDiff,
  ReviewComment,
} from "@servediff/shared";
import {
  type Dispatch,
  type SetStateAction,
  useEffect,
  useRef,
  useState,
} from "react";
import { removeSaved, saved } from "./preferences.ts";
import {
  commentContext,
  createCommentId,
  parseComments,
  reviewRounds,
} from "./review-model.ts";

export function useReview(
  repository: RepositoryDiff | null,
  draft: ReviewComment | null,
  setDraft: Dispatch<SetStateAction<ReviewComment | null>>,
) {
  const root = repository?.root ?? "";
  const [stored, setStored] = useState<{
    key: string;
    comments: ReviewComment[];
  }>({ key: "", comments: [] });
  const key = root ? `servediff:comments:${root}` : stored.key;
  const previousRoot = useRef("");
  const comments = stored.key === key ? stored.comments : [];
  const rounds = reviewRounds(comments, repository);
  const currentComments = rounds.find((round) => round.current)?.comments ?? [];
  const [feedback, setFeedback] = useState("");
  const [copyText, setCopyText] = useState<string | null>(null);

  useEffect(() => {
    if (!root) return;
    if (previousRoot.current && previousRoot.current !== root) setDraft(null);
    previousRoot.current = root;
    let disposed = false;
    let controller = new AbortController();
    async function load(importLegacy: boolean) {
      controller.abort();
      const requestController = new AbortController();
      controller = requestController;
      try {
        const legacy = importLegacy ? parseComments(saved(key)) : [];
        const result = legacy.length
          ? await api.POST("/api/v1/comments/import", {
              body: { comments: legacy },
              signal: requestController.signal,
            })
          : await api.GET("/api/v1/comments", {
              signal: requestController.signal,
            });
        if (disposed || requestController.signal.aborted) return;
        if (!result.data) {
          setFeedback(errorDetail(result.error, "Unable to load comments"));
          return;
        }
        if (legacy.length) removeSaved(key);
        setStored({ key, comments: result.data.comments });
      } catch (error) {
        if (disposed || requestController.signal.aborted) return;
        setFeedback(errorDetail(error, "Unable to load comments"));
      }
    }
    void load(true);
    const timer = setInterval(() => {
      if (!document.hidden) void load(false);
    }, 3_000);
    return () => {
      disposed = true;
      controller.abort();
      clearInterval(timer);
    };
  }, [key, root, setDraft]);

  function replace(comment: ReviewComment) {
    setStored((current) => ({
      key,
      comments: current.comments.some((entry) => entry.id === comment.id)
        ? current.comments.map((entry) =>
            entry.id === comment.id ? comment : entry,
          )
        : [...current.comments, comment],
    }));
  }

  function begin(
    file: ChangedFile,
    diff: FileDiffMetadata,
    range: SelectedLineRange,
  ) {
    if (draft) {
      setFeedback("Finish or cancel your current draft first.");
      return;
    }
    if (!repository) return;
    const context = commentContext(diff, range);
    if (!context) {
      setFeedback("Select up to 200 visible lines on one side to comment.");
      return;
    }
    setDraft({
      id: createCommentId(),
      path: file.path,
      scope: repository.mode,
      fingerprint: file.fingerprint,
      ...context,
      body: "",
      status: "open",
      createdAt: Date.now(),
      origin: {
        source: repository.source,
        repository: repository.name,
        branch: repository.branch,
        head: repository.head,
        revision: repository.revision,
        file: { status: file.status, oldPath: file.oldPath },
      },
    });
    setFeedback("Draft open — automatic refresh paused.");
  }

  async function submit() {
    if (!draft?.body.trim() || !repository) return;
    setFeedback("Saving comment…");
    try {
      const existing = comments.some((comment) => comment.id === draft.id);
      const file = repository.files.find(
        (entry) =>
          entry.path === draft.path && entry.fingerprint === draft.fingerprint,
      );
      const result = existing
        ? await api.PATCH("/api/v1/comments/{commentId}", {
            params: { path: { commentId: draft.id } },
            body: { body: draft.body.trim() },
          })
        : file
          ? await api.POST("/api/v1/comments", {
              body: {
                diffId: repository.revision,
                fileId: file.id,
                scope: draft.scope,
                fileVersion: draft.fingerprint,
                side: draft.side,
                start: draft.start,
                end: draft.end,
                body: draft.body.trim(),
              },
            })
          : null;
      if (!result) {
        setFeedback("Unable to save comment: file changed");
        return;
      }
      if (!result.data) {
        setFeedback(errorDetail(result.error, "Unable to save comment"));
        return;
      }
      replace(result.data);
      setDraft(null);
      setFeedback("Comment saved.");
    } catch (error) {
      setFeedback(errorDetail(error, "Unable to save comment"));
    }
  }

  function cancel() {
    setDraft(null);
    setFeedback("Draft cancelled.");
  }

  function edit(comment: ReviewComment) {
    if (draft) {
      setFeedback("Finish or cancel your current draft first.");
      return false;
    }
    setDraft({ ...comment });
    setFeedback("Draft open — automatic refresh paused.");
    return true;
  }

  async function toggle(comment: ReviewComment) {
    try {
      const status = comment.status === "open" ? "resolved" : "open";
      const { data, error } = await api.PATCH("/api/v1/comments/{commentId}", {
        params: { path: { commentId: comment.id } },
        body: { status },
      });
      if (!data) {
        setFeedback(errorDetail(error, "Unable to update comment"));
        return;
      }
      replace(data);
    } catch (error) {
      setFeedback(errorDetail(error, "Unable to update comment"));
    }
  }

  async function remove(comment: ReviewComment) {
    try {
      const { response, error } = await api.DELETE(
        "/api/v1/comments/{commentId}",
        { params: { path: { commentId: comment.id } } },
      );
      if (!response.ok) {
        setFeedback(errorDetail(error, "Unable to delete comment"));
        return;
      }
      if (draft?.id === comment.id) setDraft(null);
      setStored((current) => ({
        key,
        comments: current.comments.filter((entry) => entry.id !== comment.id),
      }));
    } catch (error) {
      setFeedback(errorDetail(error, "Unable to delete comment"));
    }
  }

  async function copy(includeResolved: boolean, currentOnly = false) {
    try {
      const { data, error } = await api.GET("/api/v1/comments/export", {
        params: {
          query: {
            includeResolved,
            ...(currentOnly && repository
              ? { revision: repository.revision, scope: repository.mode }
              : {}),
          },
        },
        parseAs: "text",
      });
      if (data === undefined) {
        setFeedback(errorDetail(error, "Unable to export comments"));
        return;
      }
      if (!data) return;
      const copiedCount = currentOnly
        ? currentComments.filter(
            (comment) => includeResolved || comment.status === "open",
          ).length
        : comments.filter(
            (comment) => includeResolved || comment.status === "open",
          ).length;
      try {
        await navigator.clipboard.writeText(data);
        setFeedback(
          `Copied ${copiedCount} ${copiedCount === 1 ? "comment" : "comments"} as XML.`,
        );
      } catch {
        setCopyText(data);
      }
    } catch (error) {
      setFeedback(errorDetail(error, "Unable to export comments"));
    }
  }

  return {
    comments,
    rounds,
    currentComments,
    feedback,
    setFeedback,
    begin,
    submit,
    cancel,
    edit,
    toggle,
    remove,
    copy: (includeResolved: boolean) => copy(includeResolved),
    copyCurrent: (includeResolved: boolean) => copy(includeResolved, true),
    copyText,
    closeCopy: () => setCopyText(null),
  };
}
