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
  useCallback,
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

interface CopyPayload {
  label: string;
  text: string;
  title: string;
}

interface CopyContent extends CopyPayload {
  clipboard: "copied" | "manual";
}

interface CopyOptions {
  fileOnly?: boolean;
  showDialogOnSuccess?: boolean;
  label?: string;
  title?: string;
}

function fileBlock(content: string) {
  const start = content.indexOf("<file ");
  const close = "</file>";
  const end = content.indexOf(close, start);
  if (start < 0 || end < 0) return "";
  return content.slice(start, end + close.length).replace(/^ {4}/gm, "");
}

export function useReview(
  repository: RepositoryDiff | null,
  draft: ReviewComment | null,
  setDraft: Dispatch<SetStateAction<ReviewComment | null>>,
  enabled: boolean,
) {
  const root = repository?.root ?? "";
  const [stored, setStored] = useState<{
    key: string;
    comments: ReviewComment[];
  }>({ key: "", comments: [] });
  const key = root ? `servediff:comments:${root}` : stored.key;
  const previousRoot = useRef("");
  const comments = enabled && stored.key === key ? stored.comments : [];
  const rounds = reviewRounds(comments, repository);
  const currentComments = rounds.find((round) => round.current)?.comments ?? [];
  const [feedback, setFeedback] = useState("");
  const [copyContent, setCopyContent] = useState<CopyContent | null>(null);

  useEffect(() => {
    if (!enabled || !root) return;
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
  }, [enabled, key, root, setDraft]);

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

  const markResolved = useCallback(
    (commentId: string) => {
      setStored((current) => ({
        key,
        comments: current.comments.map((comment) =>
          comment.id === commentId
            ? { ...comment, status: "resolved" }
            : comment,
        ),
      }));
    },
    [key],
  );

  function begin(
    file: ChangedFile,
    diff: FileDiffMetadata,
    range: SelectedLineRange,
  ) {
    if (!enabled) return;
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

  async function removeComments(status: "all" | "open" | "resolved" | "stale") {
    try {
      const { data, error } = await api.DELETE("/api/v1/comments", {
        params: { query: { status } },
      });
      if (!data) {
        setFeedback(errorDetail(error, "Unable to delete comments"));
        return;
      }
      if (
        draft &&
        comments.some((comment) => comment.id === draft.id) &&
        !data.comments.some((comment) => comment.id === draft.id)
      )
        setDraft(null);
      setStored({ key, comments: data.comments });
      setFeedback(
        `Deleted ${data.deleted} ${data.deleted === 1 ? "comment" : "comments"}.`,
      );
    } catch (error) {
      setFeedback(errorDetail(error, "Unable to delete comments"));
    }
  }

  async function writeClipboard(
    content: CopyPayload,
    successMessage: string,
    showDialogOnSuccess = false,
  ) {
    try {
      await navigator.clipboard.writeText(content.text);
      setFeedback(successMessage);
      if (showDialogOnSuccess)
        setCopyContent({ ...content, clipboard: "copied" });
    } catch {
      setCopyContent({ ...content, clipboard: "manual" });
    }
  }

  async function copy(
    includeResolved: boolean,
    commentId?: string,
    options: CopyOptions = {},
  ) {
    try {
      const { data, error } = await api.GET("/api/v1/comments/export", {
        params: {
          query: {
            includeResolved,
            ...(commentId ? { commentId } : {}),
          },
        },
        parseAs: "text",
      });
      if (data === undefined) {
        setFeedback(errorDetail(error, "Unable to export comments"));
        return;
      }
      if (!data) return;
      const text = options.fileOnly ? fileBlock(data) : data;
      if (!text) {
        setFeedback("Unable to export comment");
        return;
      }
      await writeClipboard(
        {
          label: options.label ?? "Comments XML",
          text,
          title: options.title ?? "Copy review comments",
        },
        commentId
          ? "Copied comment as XML."
          : `Copied ${includeResolved ? "all" : "unresolved"} comments as XML.`,
        options.showDialogOnSuccess,
      );
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
    markResolved,
    remove,
    removeComments,
    copy: (includeResolved: boolean) => copy(includeResolved),
    copyComment: (comment: ReviewComment) => copy(true, comment.id),
    copySidebarComment: (comment: ReviewComment) =>
      copy(true, comment.id, {
        fileOnly: true,
        showDialogOnSuccess: true,
        label: "Comment XML",
        title: "Copy review comment",
      }),
    copyWebMCPInstruction: (instruction: string) =>
      writeClipboard(
        {
          label: "WebMCP instruction",
          text: instruction,
          title: "Copy WebMCP instruction",
        },
        "Copied WebMCP instruction.",
      ),
    copyContent,
    closeCopy: () => setCopyContent(null),
  };
}
