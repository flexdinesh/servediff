import assert from "node:assert/strict";
import test from "node:test";
import { registerReviewTools, type ReviewToolsClient } from "../src/webmcp.ts";

class ModelContextFake extends EventTarget implements WebMCP.ModelContext {
  ontoolchange: ((this: WebMCP.ModelContext, event: Event) => unknown) | null =
    null;
  tools: WebMCP.ModelContextTool[] = [];
  signals: AbortSignal[] = [];
  registrationError: unknown;

  async registerTool(
    tool: WebMCP.ModelContextTool,
    options?: WebMCP.ModelContextRegisterToolOptions,
  ) {
    this.tools.push(tool);
    if (options?.signal) this.signals.push(options.signal);
    if (this.registrationError !== undefined) throw this.registrationError;
  }

  async getTools() {
    return [];
  }
}

function client(): ReviewToolsClient & {
  getCalls: { includeResolved: boolean; signal: AbortSignal }[];
  resolveCalls: { commentId: string; signal: AbortSignal }[];
} {
  const getCalls: { includeResolved: boolean; signal: AbortSignal }[] = [];
  const resolveCalls: { commentId: string; signal: AbortSignal }[] = [];
  return {
    getCalls,
    resolveCalls,
    async getComments(includeResolved, signal) {
      getCalls.push({ includeResolved, signal });
      return { comments: [] };
    },
    async resolveComment(commentId, signal) {
      resolveCalls.push({ commentId, signal });
      return { comment_id: commentId, status: "resolved" };
    },
  };
}

test("does not register tools without browser support or comment capability", () => {
  const api = client();
  const errors: unknown[] = [];
  assert.equal(
    registerReviewTools(
      undefined,
      true,
      api,
      () => undefined,
      (error) => errors.push(error),
    ),
    null,
  );
  assert.equal(
    registerReviewTools(
      new ModelContextFake(),
      false,
      api,
      () => undefined,
      (error) => errors.push(error),
    ),
    null,
  );
  assert.deepEqual(errors, []);
});

test("registers current WebMCP review tools and unregisters both", async () => {
  const modelContext = new ModelContextFake();
  const registration = registerReviewTools(
    modelContext,
    true,
    client(),
    () => undefined,
    () => undefined,
  );
  assert.ok(registration);
  await registration.ready;
  assert.deepEqual(
    modelContext.tools.map((tool) => tool.name),
    ["get_review_comments", "resolve_review_comment"],
  );
  assert.deepEqual(modelContext.tools[0]?.annotations, {
    readOnlyHint: true,
    untrustedContentHint: true,
    consequentialHint: false,
    debugging: true,
  });
  assert.deepEqual(modelContext.tools[1]?.annotations, {
    readOnlyHint: false,
    untrustedContentHint: false,
    consequentialHint: false,
    debugging: true,
  });
  assert.deepEqual(
    modelContext.tools.map((tool) => tool.inputSchema),
    [
      {
        type: "object",
        properties: {
          include_resolved: {
            type: "boolean",
            description: "Include resolved comments. Defaults to false.",
            default: false,
          },
        },
        additionalProperties: false,
      },
      {
        type: "object",
        properties: {
          comment_id: {
            type: "string",
            minLength: 1,
            description: "Stable ServeDiff comment ID.",
          },
        },
        required: ["comment_id"],
        additionalProperties: false,
      },
    ],
  );
  assert.equal(modelContext.signals.length, 2);
  registration.abort();
  assert.equal(registration.signal.aborted, true);
  assert.equal(
    modelContext.signals.every((signal) => signal.aborted),
    true,
  );
});

test("getter validates input and forwards cancellation", async () => {
  const modelContext = new ModelContextFake();
  const api = client();
  const registration = registerReviewTools(
    modelContext,
    true,
    api,
    () => undefined,
    () => undefined,
  );
  assert.ok(registration);
  await registration.ready;
  const getter = modelContext.tools.find(
    (tool) => tool.name === "get_review_comments",
  );
  assert.ok(getter);
  const execution = new AbortController();
  assert.deepEqual(await getter.execute({}, { signal: execution.signal }), {
    comments: [],
  });
  await getter.execute(
    { include_resolved: true },
    { signal: execution.signal },
  );
  assert.deepEqual(
    api.getCalls.map(({ includeResolved }) => includeResolved),
    [false, true],
  );
  assert.equal(
    api.getCalls.every(({ signal }) => signal === execution.signal),
    true,
  );
  assert.throws(
    () =>
      getter.execute({ include_resolved: "yes" }, { signal: execution.signal }),
    /include_resolved must be a boolean/,
  );
});

test("resolver updates local status only after server success", async () => {
  const modelContext = new ModelContextFake();
  const api = client();
  const resolved: string[] = [];
  const registration = registerReviewTools(
    modelContext,
    true,
    api,
    (commentId) => resolved.push(commentId),
    () => undefined,
  );
  assert.ok(registration);
  await registration.ready;
  const resolver = modelContext.tools.find(
    (tool) => tool.name === "resolve_review_comment",
  );
  assert.ok(resolver);
  const execution = new AbortController();
  assert.deepEqual(
    await resolver.execute(
      { comment_id: "comment-1" },
      { signal: execution.signal },
    ),
    { comment_id: "comment-1", status: "resolved" },
  );
  assert.deepEqual(resolved, ["comment-1"]);
  assert.deepEqual(
    api.resolveCalls.map(({ commentId }) => commentId),
    ["comment-1"],
  );
  assert.equal(api.resolveCalls[0]?.signal, execution.signal);
  api.resolveComment = async () => {
    throw new Error("server rejected resolution");
  };
  await assert.rejects(
    async () =>
      await resolver.execute(
        { comment_id: "comment-2" },
        { signal: execution.signal },
      ),
    /server rejected resolution/,
  );
  assert.deepEqual(resolved, ["comment-1"]);
  await assert.rejects(
    async () =>
      await resolver.execute({ comment_id: "" }, { signal: execution.signal }),
    /comment_id must be a non-empty string/,
  );
});

test("registration failure aborts partial registration without escaping", async () => {
  const modelContext = new ModelContextFake();
  modelContext.registrationError = new Error("unavailable");
  const errors: unknown[] = [];
  const registration = registerReviewTools(
    modelContext,
    true,
    client(),
    () => undefined,
    (error) => errors.push(error),
  );
  assert.ok(registration);
  await registration.ready;
  assert.equal(registration.signal.aborted, true);
  assert.equal(errors.length, 1);
  assert.equal(errors[0] instanceof Error, true);
});
