import type { Page } from "@playwright/test";

// Exercise the real subscriptions without relying on a timed poll or live service.
export async function installChangeEvents(page: Page) {
  await page.addInitScript(() => {
    class TestEventSource {
      onopen: ((event: Event) => void) | null = null;
      onmessage: ((event: MessageEvent<string>) => void) | null = null;
      private receive = (event: Event) => {
        if (event instanceof CustomEvent)
          this.onmessage?.(
            new MessageEvent("message", { data: JSON.stringify(event.detail) }),
          );
      };
      constructor() {
        window.addEventListener("servediff-test-change", this.receive);
      }
      close() {
        window.removeEventListener("servediff-test-change", this.receive);
      }
    }
    Object.defineProperty(window, "EventSource", { value: TestEventSource });
  });
}

export async function notifyChange(
  page: Page,
  kind: string,
  contextId: string,
) {
  await page.evaluate(
    ({ kind, contextId }) => {
      window.dispatchEvent(
        new CustomEvent("servediff-test-change", {
          detail: { kind, contextId, repositoryId: null },
        }),
      );
    },
    { kind, contextId },
  );
}
