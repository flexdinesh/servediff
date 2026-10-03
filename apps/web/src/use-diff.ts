import {
  type CodeViewItem,
  parseDiffFromFile,
  parsePatchFiles,
  setLanguageOverride,
} from "@pierre/diffs";
import { api, errorDetail } from "@servediff/api";
import {
  type ChangedFile,
  type DiffMode,
  type RepositoryDiff,
} from "@servediff/shared";
import {
  useCallback,
  useEffect,
  useEffectEvent,
  useMemo,
  useRef,
  useState,
} from "react";
import { subscribeChanges } from "./change-events.ts";
import type { CommentAnnotation } from "./review-model.ts";
import { languageOverride } from "./display-options.ts";

async function getDiff(contextId: string, mode: DiffMode, signal: AbortSignal) {
  const { data, error } = await api.GET(
    "/api/v2/contexts/{contextId}/diffs/current",
    {
      params: { path: { contextId }, query: { scope: mode } },
      signal,
    },
  );
  if (!data) throw new Error(errorDetail(error, "Unable to load changes"));
  return data;
}
function messageItem(
  file: ChangedFile,
  message: string,
): CodeViewItem<CommentAnnotation> {
  return {
    id: file.path,
    type: "file",
    file: { name: file.path, contents: `${message}\n`, lang: "text" },
  };
}
interface Preview {
  fingerprint: string;
  item: CodeViewItem<CommentAnnotation>;
  additions: number;
  deletions: number;
}
interface DiffState {
  repository: RepositoryDiff | null;
  items: Map<string, CodeViewItem<CommentAnnotation>>;
  busy: boolean;
  notice: string;
  connected: boolean;
}
const initialState: DiffState = {
  repository: null,
  items: new Map(),
  busy: false,
  notice: "",
  connected: false,
};

// Own notifications and cancellation in one effect; drafts defer reloads and scope changes
// abort stale requests. Cached previews retain their identity between refreshes.
export function useDiff(
  contextId: string,
  mode: DiffMode,
  composing: boolean,
  refreshEnabled: boolean,
) {
  const [state, setState] = useState<DiffState>(initialState);
  const cache = useRef(new Map<string, Preview>());
  const refreshRef = useRef<(force: boolean) => void>(() => {});
  const pendingChange = useRef(false);
  const paused = useEffectEvent(() => composing);
  const refresh = useCallback(() => refreshRef.current(true), []);
  useEffect(() => {
    let disposed = false;
    let busy = false;
    let retry = false;
    let current: RepositoryDiff | null = null;
    let controller = new AbortController();
    cache.current.clear();
    setState(initialState);
    async function load(force: boolean) {
      if (disposed || (!force && (busy || paused() || !refreshEnabled))) return;
      controller.abort();
      controller = new AbortController();
      const signal = controller.signal;
      busy = true;
      setState((previous) => ({ ...previous, busy: true }));
      try {
        const data: RepositoryDiff = await getDiff(contextId, mode, signal);
        if (signal.aborted || disposed) return;
        if (!force && !retry && current?.revision === data.revision) {
          setState((previous) => ({
            ...previous,
            connected: true,
            notice: "",
          }));
          return;
        }
        current = data;
        const paths = new Set(data.files.map((file) => file.path));
        for (const path of cache.current.keys())
          if (!paths.has(path)) cache.current.delete(path);
        const items = new Map<string, CodeViewItem<CommentAnnotation>>();
        const pending: ChangedFile[] = [];
        for (const file of data.files) {
          const cached = cache.current.get(file.path);
          if (cached?.fingerprint === file.fingerprint) {
            items.set(file.path, cached.item);
            file.additions = cached.additions;
            file.deletions = cached.deletions;
          } else {
            items.set(file.path, messageItem(file, "Loading diff…"));
            pending.push(file);
          }
        }
        setState({
          repository: {
            ...data,
            files: data.files.map((file) => ({ ...file })),
          },
          items: new Map(items),
          busy: true,
          notice: "",
          connected: true,
        });
        let next = 0;
        let failed = 0;
        let loadedBytes = 0;
        async function loadNext() {
          while (next < pending.length && !signal.aborted) {
            const file = pending[next++];
            if (!file) continue;
            try {
              const { data: body, error } = await api.GET(
                "/api/v2/contexts/{contextId}/diffs/{diffId}/files/{fileId}/patch",
                {
                  params: {
                    path: { contextId, diffId: data.id, fileId: file.id },
                    query: {
                      scope: mode,
                      versionId: data.versionId,
                      fileVersion: file.fingerprint,
                    },
                  },
                  signal,
                },
              );
              if (!body)
                throw new Error(errorDetail(error, "Unable to load diff"));
              if (signal.aborted || disposed) return;
              const contents = body.contents ?? null;
              loadedBytes +=
                body.patch.length +
                (contents?.before.length ?? 0) +
                (contents?.after.length ?? 0);
              let item: CodeViewItem<CommentAnnotation>;
              if (loadedBytes > 16 * 1024 * 1024) {
                item = messageItem(
                  file,
                  "Preview budget exceeded (16 MiB per refresh). Narrow the diff scope to load more.",
                );
              } else {
                const parsed =
                  contents && body.message === null
                    ? parseDiffFromFile(
                        { name: file.path, contents: contents.before },
                        { name: file.path, contents: contents.after },
                      )
                    : body.patch
                      ? parsePatchFiles(
                          body.patch,
                          file.fingerprint,
                          true,
                        ).flatMap((patch) => patch.files)[0]
                      : undefined;
                if (parsed && parsed.hunks.length > 0) {
                  const firstLine =
                    parsed.hunks[0]?.additionStart === 1
                      ? parsed.additionLines[0]
                      : parsed.hunks[0]?.deletionStart === 1
                        ? parsed.deletionLines[0]
                        : undefined;
                  const language = languageOverride(file.path, firstLine);
                  const displayDiff = language
                    ? setLanguageOverride(parsed, language)
                    : parsed;
                  // Reuse worker highlighting across navigation and scope reloads.
                  displayDiff.cacheKey = JSON.stringify([
                    contextId,
                    data.id,
                    mode,
                    file.path,
                    file.fingerprint,
                  ]);
                  displayDiff.name = file.path;
                  if (file.oldPath) displayDiff.prevName = file.oldPath;
                  file.additions = displayDiff.hunks.reduce(
                    (sum, hunk) => sum + hunk.additionLines,
                    0,
                  );
                  file.deletions = displayDiff.hunks.reduce(
                    (sum, hunk) => sum + hunk.deletionLines,
                    0,
                  );
                  item = {
                    id: file.path,
                    type: "diff",
                    fileDiff: displayDiff,
                  };
                } else
                  item = messageItem(
                    file,
                    body.message ??
                      (body.patch.includes("Binary files")
                        ? "Binary file changed. No text preview available."
                        : file.oldPath
                          ? `Renamed from ${file.oldPath}. No text changes.`
                          : "No text changes (empty file or file metadata changed)."),
                  );
              }
              items.set(file.path, item);
              cache.current.set(file.path, {
                fingerprint: file.fingerprint,
                item,
                additions: file.additions,
                deletions: file.deletions,
              });
            } catch (error) {
              if (signal.aborted || disposed) return;
              failed++;
              items.set(
                file.path,
                messageItem(
                  file,
                  error instanceof Error
                    ? error.message
                    : "Unable to load diff",
                ),
              );
            }
          }
        }
        await Promise.all(Array.from({ length: 4 }, loadNext));
        if (signal.aborted || disposed) return;
        retry = failed > 0;
        setState({
          repository: data,
          items,
          busy: false,
          connected: true,
          notice: failed
            ? `${failed} file previews could not load. Refresh to retry.`
            : "",
        });
      } catch (error) {
        if (signal.aborted || disposed) return;
        setState((previous) => ({
          ...previous,
          connected: false,
          notice:
            error instanceof Error
              ? error.message
              : "Unable to connect to the server",
        }));
      } finally {
        if (!signal.aborted && !disposed) {
          busy = false;
          setState((previous) => ({ ...previous, busy: false }));
          if (pendingChange.current && !paused() && !document.hidden) {
            pendingChange.current = false;
            void load(false);
          }
        }
      }
    }
    refreshRef.current = (force) => {
      if (!refreshEnabled) return;
      pendingChange.current = false;
      void load(force);
    };
    void load(true);

    pendingChange.current = false;
    const changed = () => {
      pendingChange.current = true;
      if (paused() || document.hidden || busy) {
        setState((previous) => ({
          ...previous,
          notice:
            "Changes available. Finish your draft or refresh to load them.",
        }));
        return;
      }
      pendingChange.current = false;
      void load(false);
    };
    const unsubscribe = subscribeChanges((event) => {
      if (
        refreshEnabled &&
        (event.kind === "reconnect" ||
          (event.kind === "change" && event.contextId === contextId))
      )
        changed();
    });
    const visible = () => {
      if (pendingChange.current && !document.hidden) changed();
    };
    document.addEventListener("visibilitychange", visible);
    return () => {
      disposed = true;
      controller.abort();
      unsubscribe();
      document.removeEventListener("visibilitychange", visible);
    };
  }, [contextId, mode, refreshEnabled]);
  useEffect(() => {
    if (!composing && pendingChange.current) refresh();
  }, [composing, refresh]);
  return useMemo(() => ({ ...state, refresh }), [state, refresh]);
}
