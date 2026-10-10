import { api, errorDetail } from "@diffx/api";
import type { ChangedFile, DiffMode, RepositoryDiff } from "@diffx/shared";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
} from "react";
import { createRequestOwner } from "./request-owner.ts";

const noReviews = new Map<string, string>();

export function useReviewedFiles(
  contextId: string,
  repository: RepositoryDiff | null,
  mode: DiffMode,
  setCollapsed: Dispatch<SetStateAction<Set<string>>>,
) {
  const diffId = repository?.id ?? "";
  const key = `reviewed:${contextId}:${diffId}`;
  const [stored, setStored] = useState({
    key: "",
    entries: new Map<string, string>(),
  });
  const entries = stored.key === key ? stored.entries : noReviews;
  const [failure, setFailure] = useState<{
    kind: "read" | "write";
    message: string;
  } | null>(null);
  const error = failure?.message ?? "";
  const [pending, setPending] = useState(new Set<string>());
  const requests = useRef<ReturnType<typeof createRequestOwner> | null>(null);
  const reload = useRef<(force?: boolean) => void>(() => {});
  const latest = useRef({ repository, entries });
  useLayoutEffect(() => {
    latest.current = { repository, entries };
  });

  useEffect(() => {
    if (!diffId) return;
    const owner = createRequestOwner(key);
    requests.current = owner;
    setPending(new Set());
    setFailure(null);
    async function load(force = false) {
      const current = latest.current.repository;
      if (!current || current.id !== diffId || current.mode !== mode) return;
      const request = owner.beginRead(force);
      if (!request) return;
      try {
        const { data, error } = await api.GET(
          "/api/v2/contexts/{contextId}/review-marks",
          {
            params: { path: { contextId }, query: { scope: mode } },
            signal: request.signal,
          },
        );
        if (!request.isCurrent()) return;
        if (!data)
          throw new Error(errorDetail(error, "Unable to load reviewed files"));
        setStored({
          key,
          entries: new Map(
            data.marks.map((mark) => [mark.fileId, mark.fileVersion]),
          ),
        });
        setFailure((current) => (current?.kind === "read" ? null : current));
      } catch (cause) {
        if (request.isCurrent())
          setFailure({
            kind: "read",
            message: errorDetail(cause, "Unable to load reviewed files"),
          });
      } finally {
        request.finish();
      }
    }
    reload.current = (force) => void load(force);
    void load();
    return () => owner.dispose();
  }, [contextId, diffId, key, mode]);

  // A new diff snapshot may invalidate marks, but keeps this scope's write owner.
  useEffect(() => {
    reload.current();
  }, [repository?.revision, key]);

  const toggleReviewed = useCallback(
    async (file: ChangedFile) => {
      const owner = requests.current;
      const currentRepository = latest.current.repository;
      if (owner?.key !== key || currentRepository?.id !== diffId) return;
      const request = owner.beginWrite(file.id);
      if (!request) return;
      const marked = latest.current.entries.get(file.id) === file.fingerprint;
      setPending((current) => new Set(current).add(file.id));
      setFailure(null);
      try {
        const result = marked
          ? await api.DELETE(
              "/api/v2/contexts/{contextId}/review-marks/{fileId}",
              {
                params: {
                  path: { contextId, fileId: file.id },
                  query: { scope: mode },
                },
                signal: request.signal,
              },
            )
          : await api.PUT(
              "/api/v2/contexts/{contextId}/review-marks/{fileId}",
              {
                params: {
                  path: { contextId, fileId: file.id },
                  query: { scope: mode },
                },
                body: {
                  fileVersion: file.fingerprint,
                  versionId: currentRepository.versionId,
                },
                signal: request.signal,
              },
            );
        if (!request.isCurrent()) return;
        if (!result.response.ok)
          throw new Error(
            errorDetail(result.error, "Unable to update reviewed file"),
          );
        setStored((current) => {
          const entries = new Map(
            current.key === key ? current.entries : noReviews,
          );
          if (marked) entries.delete(file.id);
          else entries.set(file.id, file.fingerprint);
          return { key, entries };
        });
        setCollapsed((current) => {
          const next = new Set(current);
          if (marked) next.delete(file.path);
          else next.add(file.path);
          return next;
        });
      } catch (cause) {
        if (request.isCurrent())
          setFailure({
            kind: "write",
            message: errorDetail(cause, "Unable to update reviewed file"),
          });
      } finally {
        const current = request.isCurrent();
        request.finish();
        if (current) {
          setPending((previous) => {
            const next = new Set(previous);
            next.delete(file.id);
            return next;
          });
          reload.current();
        }
      }
    },
    [contextId, diffId, key, mode, setCollapsed],
  );

  const resetReviewed = useCallback(async () => {
    const owner = requests.current;
    if (owner?.key !== key) return;
    const request = owner.beginWrite("*");
    if (!request) return;
    setPending((current) => new Set(current).add("*"));
    setFailure(null);
    try {
      await request.ready;
      if (!request.isCurrent()) return;
      const result = await api.DELETE(
        "/api/v2/contexts/{contextId}/review-marks",
        {
          params: { path: { contextId }, query: { scope: mode } },
          signal: request.signal,
        },
      );
      if (!request.isCurrent()) return;
      if (!result.response.ok)
        throw new Error(
          errorDetail(result.error, "Unable to reset reviewed files"),
        );
      setStored({ key, entries: new Map() });
      setCollapsed(new Set());
    } catch (cause) {
      if (request.isCurrent())
        setFailure({
          kind: "write",
          message: errorDetail(cause, "Unable to reset reviewed files"),
        });
    } finally {
      const current = request.isCurrent();
      request.finish();
      if (current) {
        setPending((previous) => {
          const next = new Set(previous);
          next.delete("*");
          return next;
        });
        reload.current();
      }
    }
  }, [contextId, key, mode, setCollapsed]);

  const isReviewed = useCallback(
    (file: ChangedFile) => entries.get(file.id) === file.fingerprint,
    [entries],
  );
  const retry = useCallback(() => {
    setFailure(null);
    reload.current(true);
  }, []);
  return useMemo(
    () => ({
      isReviewed,
      toggleReviewed,
      resetReviewed,
      pending,
      error,
      retry,
    }),
    [isReviewed, toggleReviewed, resetReviewed, pending, error, retry],
  );
}
