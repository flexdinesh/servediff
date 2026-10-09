import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { Ajv2020 } from "ajv/dist/2020.js";
import { parse } from "yaml";
import {
  validComment,
  commentApplicability,
  formatComments,
} from "../../packages/shared/src/review.ts";
import type { ApiRepositoryDiff } from "../../packages/api/src/client.ts";

const schema: unknown = parse(
  readFileSync(
    new URL("../../packages/api/openapi.yaml", import.meta.url),
    "utf8",
  ),
);
assert.ok(typeof schema === "object" && schema !== null);
const ajv = new Ajv2020({ strict: false, validateFormats: false });
ajv.addSchema(schema, "api");
const repository = ajv.compile<ApiRepositoryDiff>({
  $ref: "api#/components/schemas/RepositoryDiff",
});
const cases: unknown = JSON.parse(
  readFileSync(new URL("./comments.json", import.meta.url), "utf8"),
);
assert.ok(Array.isArray(cases));
function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}
for (const value of cases) {
  const entry: unknown = value;
  assert.ok(record(entry));
  test(String(entry.name), () => {
    const comment: unknown = entry.comment;
    assert.equal(validComment(comment), entry.valid);
    // Invalid selections still have an applicability; Go verifies that separately.
    if (!validComment(comment)) return;
    const snapshot: unknown = entry.repository;
    assert.ok(snapshot === null || repository(snapshot));
    assert.equal(commentApplicability(comment, snapshot), entry.applicability);
    if (entry.export)
      assert.equal(
        formatComments([comment], true, snapshot ? [snapshot] : []),
        entry.export,
      );
  });
}
