import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import test from "node:test";

const execFileAsync = promisify(execFile);
const generatorPath = join(
  process.cwd(),
  "tools",
  "generate-homebrew-formula.ts",
);

interface FormulaWorkspace {
  checksumsPath: string;
  outputPath: string;
}

test("generates a formula for every Homebrew target", async (t) => {
  const workspace = await createWorkspace(t.after.bind(t));
  await writeChecksums(workspace.checksumsPath, completeChecksums());
  await runGenerator(workspace);

  const formula = await readFile(workspace.outputPath, "utf8");
  assert.match(formula, /class Servediff < Formula/);
  assert.match(formula, /license "MIT"/);
  assert.doesNotMatch(formula, /^\s*version\s/m);
  for (const target of [
    "darwin_amd64",
    "darwin_arm64",
    "linux_amd64",
    "linux_arm64",
  ]) {
    assert.match(
      formula,
      new RegExp(`servediff_0\\.1\\.0_${target}\\.tar\\.gz`),
    );
  }
  assert.match(formula, /bin\.install "servediff"/);
  assert.match(formula, /assert_match "servediff #\{version\}"/);
});

test("rejects malformed checksums", async (t) => {
  const workspace = await createWorkspace(t.after.bind(t));
  await writeChecksums(workspace.checksumsPath, [
    "not-a-checksum servediff.tar.gz",
  ]);
  await assert.rejects(runGenerator(workspace), /invalid checksum line/);
});

test("requires every Homebrew target checksum", async (t) => {
  const workspace = await createWorkspace(t.after.bind(t));
  await writeChecksums(
    workspace.checksumsPath,
    completeChecksums().filter((line) => !line.includes("linux_arm64")),
  );
  await assert.rejects(
    runGenerator(workspace),
    /missing checksum.*linux_arm64/,
  );
});

test("generates valid Ruby", async (t) => {
  try {
    await execFileAsync("ruby", ["-v"]);
  } catch {
    t.skip("ruby unavailable");
    return;
  }
  const workspace = await createWorkspace(t.after.bind(t));
  await writeChecksums(workspace.checksumsPath, completeChecksums());
  await runGenerator(workspace);
  const { stdout } = await execFileAsync("ruby", ["-c", workspace.outputPath]);
  assert.match(stdout, /Syntax OK/);
});

async function createWorkspace(
  registerCleanup: (cleanup: () => Promise<void>) => void,
): Promise<FormulaWorkspace> {
  const directory = await mkdtemp(join(tmpdir(), "servediff-formula-"));
  registerCleanup(() => rm(directory, { recursive: true, force: true }));
  return {
    checksumsPath: join(directory, "checksums.txt"),
    outputPath: join(directory, "servediff.rb"),
  };
}

function completeChecksums(): string[] {
  return [
    `${"a".repeat(64)}  servediff_0.1.0_darwin_amd64.tar.gz`,
    `${"b".repeat(64)}  servediff_0.1.0_darwin_arm64.tar.gz`,
    `${"c".repeat(64)}  servediff_0.1.0_linux_amd64.tar.gz`,
    `${"d".repeat(64)}  servediff_0.1.0_linux_arm64.tar.gz`,
  ];
}

async function writeChecksums(
  path: string,
  checksums: string[],
): Promise<void> {
  await writeFile(path, `${checksums.join("\n")}\n`);
}

async function runGenerator({
  checksumsPath,
  outputPath,
}: FormulaWorkspace): Promise<void> {
  await execFileAsync("node", [
    generatorPath,
    "--version",
    "0.1.0",
    "--tag",
    "v0.1.0",
    "--checksums",
    checksumsPath,
    "--output",
    outputPath,
  ]);
}
