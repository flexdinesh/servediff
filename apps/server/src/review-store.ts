import { homedir } from "node:os";
import { dirname, join } from "node:path";
import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import {
  validComment,
  type DiffMode,
  type ReviewComment,
  type ReviewMark,
} from "@servediff/shared";

interface SessionReview {
  comments: ReviewComment[];
  marks: ReviewMark[];
}

interface ReviewData {
  version: 1;
  sessions: Record<string, SessionReview>;
}

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function reviewMark(value: unknown): value is ReviewMark {
  return (
    record(value) &&
    typeof value.fileId === "string" &&
    typeof value.fileVersion === "string" &&
    (value.scope === "all" ||
      value.scope === "staged" ||
      value.scope === "unstaged")
  );
}

function parseData(raw: string): ReviewData {
  try {
    const value: unknown = JSON.parse(raw);
    if (!record(value) || value.version !== 1 || !record(value.sessions))
      throw new Error("Unsupported review data");
    const sessions: Record<string, SessionReview> = {};
    for (const [key, session] of Object.entries(value.sessions)) {
      if (!record(session)) continue;
      sessions[key] = {
        comments: Array.isArray(session.comments)
          ? session.comments.filter(validComment)
          : [],
        marks: Array.isArray(session.marks)
          ? session.marks.filter(reviewMark)
          : [],
      };
    }
    return { version: 1, sessions };
  } catch {
    throw new Error("Invalid servediff review data");
  }
}

function missingFile(error: unknown) {
  return record(error) && error.code === "ENOENT";
}

export function defaultReviewPath(sessionId: string) {
  const state =
    process.env.XDG_STATE_HOME ?? join(homedir(), ".local", "state");
  return join(state, "servediff", "reviews", `${sessionId}.json`);
}

export class ReviewStore {
  readonly path: string | null;
  private data: Promise<ReviewData>;
  private pending: Promise<void> = Promise.resolve();

  constructor(path: string | null) {
    this.path = path;
    this.data = path
      ? readFile(path, "utf8").then(parseData, (error: unknown) => {
          if (missingFile(error)) return { version: 1, sessions: {} };
          throw error;
        })
      : Promise.resolve({ version: 1, sessions: {} });
  }

  private async session(id: string) {
    const data = await this.data;
    const current = data.sessions[id] ?? { comments: [], marks: [] };
    data.sessions[id] = current;
    return current;
  }

  private async persist() {
    if (!this.path) return;
    const data = await this.data;
    await mkdir(dirname(this.path), { recursive: true, mode: 0o700 });
    const temporary = `${this.path}.${process.pid}.tmp`;
    await writeFile(temporary, JSON.stringify(data), {
      encoding: "utf8",
      mode: 0o600,
    });
    await rename(temporary, this.path);
  }

  private async mutate(change: () => Promise<void>) {
    this.pending = this.pending.then(async () => {
      await change();
      await this.persist();
    });
    await this.pending;
  }

  async comments(sessionId: string) {
    await this.pending;
    return [...(await this.session(sessionId)).comments];
  }

  async putComment(sessionId: string, comment: ReviewComment) {
    await this.mutate(async () => {
      const session = await this.session(sessionId);
      const index = session.comments.findIndex(
        (entry) => entry.id === comment.id,
      );
      if (index < 0) session.comments.push(comment);
      else session.comments[index] = comment;
    });
  }

  async importComments(sessionId: string, comments: readonly ReviewComment[]) {
    await this.mutate(async () => {
      const session = await this.session(sessionId);
      const ids = new Set(session.comments.map((comment) => comment.id));
      for (const comment of comments) {
        if (ids.has(comment.id)) continue;
        session.comments.push(comment);
        ids.add(comment.id);
      }
    });
    return this.comments(sessionId);
  }

  async deleteComment(sessionId: string, id: string) {
    let deleted = false;
    await this.mutate(async () => {
      const session = await this.session(sessionId);
      const next = session.comments.filter((comment) => comment.id !== id);
      deleted = next.length !== session.comments.length;
      session.comments = next;
    });
    return deleted;
  }

  async deleteComments(sessionId: string, ids: ReadonlySet<string>) {
    let deleted = 0;
    await this.mutate(async () => {
      const session = await this.session(sessionId);
      const next = session.comments.filter((comment) => !ids.has(comment.id));
      deleted = session.comments.length - next.length;
      session.comments = next;
    });
    return deleted;
  }

  async marks(sessionId: string, scope: DiffMode) {
    await this.pending;
    return (await this.session(sessionId)).marks.filter(
      (mark) => mark.scope === scope,
    );
  }

  async putMark(sessionId: string, mark: ReviewMark) {
    await this.mutate(async () => {
      const session = await this.session(sessionId);
      session.marks = session.marks.filter(
        (entry) => entry.scope !== mark.scope || entry.fileId !== mark.fileId,
      );
      session.marks.push(mark);
    });
  }

  async deleteMark(sessionId: string, scope: DiffMode, fileId: string) {
    await this.mutate(async () => {
      const session = await this.session(sessionId);
      session.marks = session.marks.filter(
        (mark) => mark.scope !== scope || mark.fileId !== fileId,
      );
    });
  }

  async clearMarks(sessionId: string, scope: DiffMode) {
    await this.mutate(async () => {
      const session = await this.session(sessionId);
      session.marks = session.marks.filter((mark) => mark.scope !== scope);
    });
  }
}
