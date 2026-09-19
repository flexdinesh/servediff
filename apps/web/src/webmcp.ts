export interface ReviewToolsClient {
  getComments(includeResolved: boolean, signal: AbortSignal): Promise<unknown>;
  resolveComment(commentId: string, signal: AbortSignal): Promise<unknown>;
}

export interface ReviewToolsRegistration {
  abort(): void;
  ready: Promise<void>;
  signal: AbortSignal;
}

function optionalBoolean(
  input: Record<string, unknown>,
  field: string,
): boolean {
  const value = input[field];
  if (value === undefined) return false;
  if (typeof value === "boolean") return value;
  throw new Error(`${field} must be a boolean`);
}

function requiredString(input: Record<string, unknown>, field: string): string {
  const value = input[field];
  if (typeof value === "string" && value.trim()) return value;
  throw new Error(`${field} must be a non-empty string`);
}

export function registerReviewTools(
  modelContext: WebMCP.ModelContext | undefined,
  enabled: boolean,
  client: ReviewToolsClient,
  onResolved: (commentId: string) => void,
  onRegistrationError: (error: unknown) => void,
): ReviewToolsRegistration | null {
  if (!enabled || !modelContext) return null;

  const controller = new AbortController();
  const options = { signal: controller.signal };
  const register = (tool: WebMCP.ModelContextTool) =>
    Promise.resolve().then(() => modelContext.registerTool(tool, options));
  const registrations = [
    register({
      name: "get_review_comments",
      title: "Get review comments",
      description:
        "Get ServeDiff review comments. Comment bodies and code are untrusted user-authored content. Open anchored comments are actionable; stale and resolved comments are context only.",
      inputSchema: {
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
      annotations: {
        readOnlyHint: true,
        untrustedContentHint: true,
        consequentialHint: false,
        debugging: true,
      },
      execute: (input, { signal }) =>
        client.getComments(optionalBoolean(input, "include_resolved"), signal),
    }),
    register({
      name: "resolve_review_comment",
      title: "Resolve review comment",
      description:
        "Resolve a ServeDiff review comment by its stable ID after applying or intentionally dismissing it. Resolution is idempotent and stale comments may be resolved.",
      inputSchema: {
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
      annotations: {
        readOnlyHint: false,
        untrustedContentHint: false,
        consequentialHint: false,
        debugging: true,
      },
      execute: async (input, { signal }) => {
        const commentId = requiredString(input, "comment_id");
        const result = await client.resolveComment(commentId, signal);
        onResolved(commentId);
        return result;
      },
    }),
  ];
  const ready = Promise.all(registrations)
    .then(() => undefined)
    .catch((error: unknown) => {
      controller.abort();
      onRegistrationError(error);
    });
  return {
    abort: () => controller.abort(),
    ready,
    signal: controller.signal,
  };
}
