import type { FileDiffMetadata, SelectedLineRange } from "@pierre/diffs";
import { api, errorDetail } from "@diffx/api";
import type { ChangedFile, RepositoryDiff, ReviewComment } from "@diffx/shared";
import {
  type Dispatch,
  type SetStateAction,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { createRequestOwner } from "./request-owner.ts";
import {
  commentContext,
  createCommentId,
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

const noComments: ReviewComment[] = [];

function fileBlock(content: string) {
  const start = content.indexOf("<file ");
  const close = "</file>";
  const end = content.indexOf(close, start);
  if (start < 0 || end < 0) return "";
  return content.slice(start, end + close.length).replace(/^ {4}/gm, "");
}

export function useReview(
  contextId: string,
  repository: RepositoryDiff | null,
  draft: ReviewComment | null,
  setDraft: Dispatch<SetStateAction<ReviewComment | null>>,
  enabled: boolean,
) {
  const [stored, setStored] = useState<{
    key: string;
    comments: ReviewComment[];
  }>({ key: "", comments: [] });
  const key = contextId ? `diffx:comments:${contextId}` : stored.key;
  const previousKey = useRef("");
  const requests = useRef<ReturnType<typeof createRequestOwner> | null>(null);
  const refreshComments = useRef<() => void>(() => {});
  const comments = enabled && stored.key === key ? stored.comments : noComments;
  const rounds = useMemo(
    () => reviewRounds(comments, repository),
    [comments, repository],
  );
  const currentComments =
    rounds.find((round) => round.current)?.comments ?? noComments;
  const [feedback, setFeedback] = useState("");
  const [copyContent, setCopyContent] = useState<CopyContent | null>(null);
  const [pending, setPending] = useState(new Set<string>());
  const [saveError, setSaveError] = useState<{
    draftId: string;
    message: string;
  } | null>(null);
  const latest = useRef({ repository, draft, comments });
  useLayoutEffect(() => {
    latest.current = { repository, draft, comments };
  });

  useEffect(() => {
    if (!enabled || !key) return;
    if (previousKey.current && previousKey.current !== key) setDraft(null);
    previousKey.current = key;
    const owner = createRequestOwner(key);
    requests.current = owner;
    setStored((current) =>
      current.key === key ? current : { key, comments: [] },
    );
    setPending(new Set());
    setSaveError(null);
    async function load(force = false) {
      const request = owner.beginRead(force);
      if (!request) return;
      try {
        const result = await api.GET("/api/v2/contexts/{contextId}/comments", {
          params: { path: { contextId } },
          signal: request.signal,
        });
        if (!request.isCurrent()) return;
        if (!result.data) {
          setFeedback(errorDetail(result.error, "Unable to load comments"));
          return;
        }
        setStored({ key, comments: result.data.comments });
      } catch (error) {
        if (!request.isCurrent()) return;
        setFeedback(errorDetail(error, "Unable to load comments"));
      } finally {
        request.finish();
      }
    }
    refreshComments.current = () => void load();
    void load(true);
    const timer = setInterval(() => {
      if (!document.hidden) void load();
    }, 3_000);
    return () => {
      owner.dispose();
      clearInterval(timer);
    };
  }, [contextId, enabled, key, setDraft]);

  const startMutation = useCallback(
    (id: string) => {
      const owner = requests.current;
      if (!enabled || owner?.key !== key) return null;
      const request = owner.beginWrite(id);
      if (!request) return null;
      setPending((current) => new Set(current).add(id));
      return request;
    },
    [enabled, key],
  );

  const finishMutation = useCallback(
    (
      id: string,
      request: NonNullable<
        ReturnType<ReturnType<typeof createRequestOwner>["beginWrite"]>
      >,
    ) => {
      const current = request.isCurrent();
      request.finish();
      if (!current) return;
      setPending((previous) => {
        const next = new Set(previous);
        next.delete(id);
        return next;
      });
      refreshComments.current();
    },
    [],
  );

  const replace = useCallback(
    (comment: ReviewComment) => {
      setStored((current) => ({
        key,
        comments:
          current.key !== key
            ? [comment]
            : current.comments.some((entry) => entry.id === comment.id)
              ? current.comments.map((entry) =>
                  entry.id === comment.id ? comment : entry,
                )
              : [...current.comments, comment],
      }));
    },
    [key],
  );

  const markResolved = useCallback(
    (commentId: string) => {
      const owner = requests.current;
      if (owner?.key !== key) return;
      owner.invalidateReads();
      setStored((current) =>
        current.key !== key
          ? current
          : {
              ...current,
              comments: current.comments.map((comment) =>
                comment.id === commentId
                  ? { ...comment, status: "resolved" }
                  : comment,
              ),
            },
      );
    },
    [key],
  );

  const begin = useCallback(
    (file: ChangedFile, diff?: FileDiffMetadata, range?: SelectedLineRange) => {
      const { repository, draft } = latest.current;
      if (!enabled) return;
      if (draft) {
        setFeedback("Finish or cancel your current draft first.");
        return;
      }
      if (!repository) return;
      const context =
        diff && range
          ? commentContext(diff, range)
          : ({
              target: "file",
              side: "additions",
              start: 0,
              end: 0,
              code: "",
            } satisfies Pick<
              ReviewComment,
              "target" | "side" | "start" | "end" | "code"
            >);
      if (!context) {
        setFeedback("Select up to 200 visible lines on one side to comment.");
        return;
      }
      setDraft({
        id: createCommentId(),
        diffId: repository.id,
        versionId: repository.versionId,
        path: file.path,
        scope: repository.mode,
        fingerprint: file.fingerprint,
        ...context,
        body: "",
        status: "open",
        createdAt: Date.now(),
        origin: {
          diffId: repository.id,
          versionId: repository.versionId,
          source: repository.source,
          repository: repository.name,
          branch: repository.branch,
          head: repository.head,
          revision: repository.revision,
          file: { status: file.status, oldPath: file.oldPath },
        },
      });
      setSaveError(null);
      setFeedback("Draft open.");
    },
    [enabled, setDraft],
  );

  const submit = useCallback(async () => {
    const { repository, draft, comments } = latest.current;
    if (!draft?.body.trim() || !repository) return;
    const request = startMutation(draft.id);
    if (!request) return;
    setSaveError(null);
    setFeedback("Saving comment…");
    try {
      const existing = comments.some((comment) => comment.id === draft.id);
      const file = repository.files.find(
        (entry) =>
          entry.path === draft.path && entry.fingerprint === draft.fingerprint,
      );
      const result = existing
        ? await api.PATCH("/api/v2/contexts/{contextId}/comments/{commentId}", {
            params: { path: { contextId, commentId: draft.id } },
            body: { body: draft.body.trim() },
            signal: request.signal,
          })
        : file
          ? await api.POST("/api/v2/contexts/{contextId}/comments", {
              params: { path: { contextId } },
              body: {
                diffId: repository.id,
                versionId: repository.versionId,
                fileId: file.id,
                scope: draft.scope,
                fileVersion: draft.fingerprint,
                ...(draft.target ? { target: draft.target } : {}),
                side: draft.side,
                start: draft.start,
                end: draft.end,
                body: draft.body.trim(),
              },
              signal: request.signal,
            })
          : null;
      if (!request.isCurrent()) return;
      if (!result) throw new Error("Unable to save comment: file changed");
      if (!result.data)
        throw new Error(errorDetail(result.error, "Unable to save comment"));
      const savedComment = result.data;
      replace(savedComment);
      setDraft((current) => {
        if (current?.id !== draft.id) return current;
        // A newly created comment receives its stable ID from the server.
        // Retain newer text under that ID so its next save updates, not creates.
        return current === draft
          ? null
          : { ...savedComment, body: current.body };
      });
      setFeedback("Comment saved.");
    } catch (error) {
      if (!request.isCurrent()) return;
      const message = errorDetail(error, "Unable to save comment");
      setSaveError({ draftId: draft.id, message });
      setFeedback(message);
    } finally {
      finishMutation(draft.id, request);
    }
  }, [contextId, startMutation, finishMutation, replace, setDraft]);

  const cancel = useCallback(() => {
    setDraft(null);
    setSaveError(null);
    setFeedback("Draft cancelled.");
  }, [setDraft]);

  const edit = useCallback(
    (comment: ReviewComment) => {
      const { draft } = latest.current;
      if (draft) {
        setFeedback("Finish or cancel your current draft first.");
        return false;
      }
      setDraft({ ...comment });
      setSaveError(null);
      setFeedback("Draft open.");
      return true;
    },
    [setDraft],
  );

  const toggle = useCallback(
    async (comment: ReviewComment) => {
      const request = startMutation(comment.id);
      if (!request) return;
      try {
        const status = comment.status === "open" ? "resolved" : "open";
        const { data, error } = await api.PATCH(
          "/api/v2/contexts/{contextId}/comments/{commentId}",
          {
            params: { path: { contextId, commentId: comment.id } },
            body: { status },
            signal: request.signal,
          },
        );
        if (!request.isCurrent()) return;
        if (!data) {
          setFeedback(errorDetail(error, "Unable to update comment"));
          return;
        }
        replace(data);
      } catch (error) {
        if (!request.isCurrent()) return;
        setFeedback(errorDetail(error, "Unable to update comment"));
      } finally {
        finishMutation(comment.id, request);
      }
    },
    [contextId, startMutation, finishMutation, replace],
  );

  const remove = useCallback(
    async (comment: ReviewComment) => {
      const request = startMutation(comment.id);
      if (!request) return;
      try {
        const { response, error } = await api.DELETE(
          "/api/v2/contexts/{contextId}/comments/{commentId}",
          {
            params: { path: { contextId, commentId: comment.id } },
            signal: request.signal,
          },
        );
        if (!request.isCurrent()) return;
        if (!response.ok) {
          setFeedback(errorDetail(error, "Unable to delete comment"));
          return;
        }
        setDraft((current) => (current?.id === comment.id ? null : current));
        setStored((current) => ({
          key,
          comments: current.comments.filter((entry) => entry.id !== comment.id),
        }));
      } catch (error) {
        if (!request.isCurrent()) return;
        setFeedback(errorDetail(error, "Unable to delete comment"));
      } finally {
        finishMutation(comment.id, request);
      }
    },
    [contextId, key, startMutation, finishMutation, setDraft],
  );

  const removeComments = useCallback(
    async (status: "all" | "open" | "resolved" | "stale") => {
      const request = startMutation("*");
      if (!request) return;
      try {
        await request.ready;
        if (!request.isCurrent()) return;
        const previousComments = latest.current.comments;
        const { data, error } = await api.DELETE(
          "/api/v2/contexts/{contextId}/comments",
          {
            params: { path: { contextId }, query: { status } },
            signal: request.signal,
          },
        );
        if (!request.isCurrent()) return;
        if (!data) {
          setFeedback(errorDetail(error, "Unable to delete comments"));
          return;
        }
        setDraft((current) =>
          current &&
          previousComments.some((comment) => comment.id === current.id) &&
          !data.comments.some((comment) => comment.id === current.id)
            ? null
            : current,
        );
        setStored({ key, comments: data.comments });
        setFeedback(
          `Deleted ${data.deleted} ${data.deleted === 1 ? "comment" : "comments"}.`,
        );
      } catch (error) {
        if (!request.isCurrent()) return;
        setFeedback(errorDetail(error, "Unable to delete comments"));
      } finally {
        finishMutation("*", request);
      }
    },
    [contextId, key, startMutation, finishMutation, setDraft],
  );

  const resolveComment = useCallback(
    async (commentId: string, signal: AbortSignal) => {
      const request = startMutation(commentId);
      if (!request) throw new Error("Comment update already pending");
      try {
        const { data, error } = await api.POST(
          "/api/v2/contexts/{contextId}/review/comments/{commentId}/resolve",
          {
            params: { path: { contextId, commentId } },
            signal: AbortSignal.any([signal, request.signal]),
          },
        );
        if (!data)
          throw new Error(
            errorDetail(error, "Unable to resolve review comment"),
          );
        signal.throwIfAborted();
        if (!request.isCurrent()) throw new Error("Comment update cancelled");
        markResolved(commentId);
        return data;
      } finally {
        finishMutation(commentId, request);
      }
    },
    [contextId, startMutation, finishMutation, markResolved],
  );

  const writeClipboard = useCallback(
    async (
      content: CopyPayload,
      successMessage: string,
      showDialogOnSuccess = false,
    ) => {
      try {
        await navigator.clipboard.writeText(content.text);
        setFeedback(successMessage);
        if (showDialogOnSuccess)
          setCopyContent({ ...content, clipboard: "copied" });
      } catch {
        setCopyContent({ ...content, clipboard: "manual" });
      }
    },
    [],
  );

  const copy = useCallback(
    async (
      includeResolved: boolean,
      commentId?: string,
      options: CopyOptions = {},
    ) => {
      try {
        const { data, error } = await api.GET(
          "/api/v2/contexts/{contextId}/comments/export",
          {
            params: {
              path: { contextId },
              query: {
                includeResolved,
                ...(commentId ? { commentId } : {}),
              },
            },
            parseAs: "text",
          },
        );
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
    },
    [contextId, writeClipboard],
  );

  return useMemo(
    () => ({
      comments,
      rounds,
      currentComments,
      feedback,
      setFeedback,
      pending,
      saveError,
      begin,
      submit,
      cancel,
      edit,
      toggle,
      markResolved,
      resolveComment,
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
    }),
    [
      comments,
      rounds,
      currentComments,
      feedback,
      pending,
      saveError,
      begin,
      submit,
      cancel,
      edit,
      toggle,
      markResolved,
      resolveComment,
      remove,
      removeComments,
      copy,
      writeClipboard,
      copyContent,
    ],
  );
}
