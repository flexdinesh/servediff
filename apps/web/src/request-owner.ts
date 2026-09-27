// One owner per repository/scope. Reads cannot settle across writes or disposal.
// Independent writes may overlap; a bulk write waits for earlier writes and
// blocks later writes until it settles. Cancellation never implies server rollback.
export function createRequestOwner(key: string) {
  let disposed = false;
  let generation = 0;
  let readController: AbortController | null = null;
  const writes = new Map<
    string,
    { controller: AbortController; done: Promise<void>; finish: () => void }
  >();

  function invalidateReads() {
    generation++;
    readController?.abort();
    readController = null;
  }

  function beginRead(force = false) {
    if (disposed || writes.size > 0 || (readController && !force)) return null;
    if (force) invalidateReads();
    const controller = new AbortController();
    const started = generation;
    readController = controller;
    return {
      signal: controller.signal,
      isCurrent: () =>
        !disposed && !controller.signal.aborted && started === generation,
      finish: () => {
        if (readController === controller) readController = null;
      },
    };
  }

  function beginWrite(id: string) {
    if (disposed || writes.has(id) || writes.has("*")) return null;
    const ready =
      id === "*"
        ? Promise.all([...writes.values()].map((write) => write.done))
        : Promise.resolve();
    const controller = new AbortController();
    let finish = () => {};
    const done = new Promise<void>((resolve) => {
      finish = resolve;
    });
    const write = { controller, done, finish };
    writes.set(id, write);
    invalidateReads();
    return {
      ready,
      signal: controller.signal,
      isCurrent: () => !disposed && !controller.signal.aborted,
      finish: () => {
        if (writes.get(id) !== write) return;
        writes.delete(id);
        invalidateReads();
        finish();
      },
    };
  }

  return {
    key,
    beginRead,
    beginWrite,
    invalidateReads,
    dispose: () => {
      disposed = true;
      invalidateReads();
      for (const write of writes.values()) {
        write.controller.abort();
        write.finish();
      }
      writes.clear();
    },
  };
}
