export interface ChangeNotification {
  kind: string;
  contextId: string;
  repositoryId: string | null;
}

const listeners = new Set<(event: ChangeNotification) => void>();
let source: EventSource | null = null;

function notify(event: ChangeNotification) {
  for (const listener of listeners) listener(event);
}

// Share one connection; reconnecting refreshes selected data, never scans repositories.
export function subscribeChanges(
  listener: (event: ChangeNotification) => void,
) {
  listeners.add(listener);
  if (!source) {
    source = new EventSource("/api/v2/events");
    // Catch notifications missed during initial connection or a connection gap.
    source.onopen = () =>
      notify({ kind: "reconnect", contextId: "", repositoryId: null });
    source.onmessage = (event) => {
      try {
        const value: unknown = JSON.parse(event.data);
        if (
          typeof value !== "object" ||
          value === null ||
          !("kind" in value) ||
          typeof value.kind !== "string" ||
          !("contextId" in value) ||
          typeof value.contextId !== "string" ||
          !("repositoryId" in value) ||
          (value.repositoryId !== null &&
            typeof value.repositoryId !== "string")
        )
          return;
        notify({
          kind: value.kind,
          contextId: value.contextId,
          repositoryId: value.repositoryId,
        });
      } catch {
        /* Ignore malformed notifications; navigation always collects fresh data. */
      }
    };
  }
  return () => {
    listeners.delete(listener);
    if (!listeners.size) {
      source?.close();
      source = null;
    }
  };
}
