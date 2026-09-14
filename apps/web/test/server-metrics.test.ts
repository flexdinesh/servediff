import assert from "node:assert/strict";
import test from "node:test";
import { formatBytes } from "../src/server-metrics.ts";

test("formats process memory without hiding precision", () => {
  assert.equal(formatBytes(512), "512 B");
  assert.equal(formatBytes(1_536), "1.5 KB");
  assert.equal(formatBytes(52_428_800), "50.0 MB");
});
