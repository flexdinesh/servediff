import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { checkDesign, readDesignSources } from "../../tools/check-design.ts";

function check(text: string, path = "new.css") {
  return checkDesign([{ path, text }]);
}

test("semantic tokens compose colors, type, geometry and spacing", () => {
  assert.deepEqual(
    check(`.control {
    color: var(--fg); background: color-mix(in srgb, var(--accent) 10%, transparent);
    font: var(--weight-medium) var(--text-sm) var(--font-sans);
    border: 1px solid currentColor; border-radius: var(--radius-control) 0;
    box-shadow: var(--shadow-overlay); padding: 0 var(--space-3); margin-left: auto;
  }`),
    [],
  );
});

test("literal palette cannot escape through custom properties, fallbacks or shorthand", () => {
  for (const declaration of [
    "color: red",
    "background: #abc",
    "border: 1px solid rebeccapurple",
    "--new-color: hsl(1 2% 3%)",
    "color: var(--unknown, red)",
    "--alias: blue",
    "background: color-mix(in srgb, var(--bg), white)",
  ]) {
    assert.equal(
      check(`.new { ${declaration} !important }`).length,
      1,
      declaration,
    );
  }
});

test("canonical source ownership does not exempt unrelated files", () => {
  assert.deepEqual(check(":root { --fg: #fff }", "tokens.css"), []);
  assert.equal(check(".new { color: #fff }", "tokens.css").length, 1);
  assert.equal(check(":root { --fg: #fff }", "nested/tokens.css").length, 1);
  assert.deepEqual(
    check(":root { --text-xs: .75rem; font-size: 100% }", "typography.css"),
    [],
  );
  assert.equal(check(":root { --text-xs: .75rem }", "tokens.css").length, 1);
});

test("CSS parser handles nesting, comments, strings and multiline values", () => {
  const result = check(`/* color: red; */
@media (width > 100px) {
  .new { content: "color: red; }";
    background-image: url("data:image/svg+xml;unused");
    color: rgb(
      1 2 3
    );
  }
}`);
  assert.equal(result.length, 1);
  assert.equal(result[0]?.line, 5);
  assert.equal(result[0]?.property, "color");
  assert.deepEqual(
    check(`/* @apply text-xl; */ .new { content: "/* @apply text-xl; */" }`),
    [],
  );
});

test("type, radius, shadows and layout lengths require tokens", () => {
  for (const declaration of [
    "font-size: 15px",
    "font-family: Arial",
    "font-family: var(--font-sans), Arial",
    "font-weight: 550",
    "line-height: 1.7",
    "letter-spacing: .2em",
    "border-radius: 50%",
    "border-radius: calc(var(--radius-sm) + .5rem)",
    "--local-radius: .5rem",
    "--space-custom: 13px",
    "--shadow-custom: 0 2px var(--border)",
    "box-shadow: 0 2px 8px var(--border)",
    "padding: 10px",
    "gap: calc(var(--space-2) + 2px)",
  ]) {
    assert.equal(check(`.new { ${declaration} }`).length, 1, declaration);
  }
});

test("optical exceptions remain exact in selector, value and source", () => {
  assert.deepEqual(
    check(".staging-dot { border-radius: 50% }", "review.css"),
    [],
  );
  assert.equal(check(".new { border-radius: 50% }", "review.css").length, 1);
  assert.equal(
    check(".staging-dot { border-radius: 25% }", "review.css").length,
    1,
  );
  assert.equal(
    check(".staging-dot { border-radius: 50% }", "new.css").length,
    1,
  );
  assert.deepEqual(check(".new { padding: 1px var(--space-2) }"), []);
});

test("inline styles and named style objects cannot bypass rules", () => {
  for (const text of [
    `const View = () => <div style={{ color: "red" }} />`,
    `const rowStyle = { fontSize: 15 };`,
    `const View = () => <div style={{ fontWeight: customWeight }} />`,
    `const rowStyle = { "border-radius": "12px" };`,
    `const s = {color: "red"}; const View = () => <div style={s} />;`,
    `const base = {color: "red"}; const s = {...base}; const View = () => <div style={s} />;`,
    `const s: React.CSSProperties = {color: "red"};`,
    `const View = () => <svg fill="#abc" />;`,
    'const View = () => <div style={{borderColor: "red"}} />;',
    'const View = () => <div style={{outline: "1px solid red"}} />;',
    "const View = () => <div style={{color: `${palette}`}} />;",
    "const View = () => <div style={{fontSize: `var(--${role})`}} />;",
    "const View = () => <div style={{padding: `${spacing}`}} />;",
    "const View = () => <svg fill={`${palette}`} />;",
  ])
    assert.equal(check(text, "new.tsx").length, 1, text);
  assert.deepEqual(
    check(
      `const rowStyle = { color: "var(--fg)", paddingLeft: \`calc(var(--space-1) + \${depth} * var(--space-3))\` };`,
      "new.tsx",
    ),
    [],
  );
  assert.deepEqual(
    check(`const itemMetrics = { lineHeight: measuredHeight };`, "new.tsx"),
    [],
  );
  assert.deepEqual(check(".control { border-radius: 0px }"), []);
});

test("unsafeCSS is checked as CSS and dynamic injection is rejected", () => {
  assert.equal(
    check("const options = { unsafeCSS: `.new { color: #fff }` };", "new.ts")
      .length,
    1,
  );
  assert.equal(
    check("const options = { unsafeCSS: injectedRules };", "new.ts").length,
    1,
  );
  assert.equal(
    check(
      "const options = { unsafeCSS: `.new { ${injectedRules} }` };",
      "new.ts",
    ).length,
    1,
  );
  assert.deepEqual(
    check(
      "const options = { unsafeCSS: `.new { color: var(--fg) }` };",
      "new.ts",
    ),
    [],
  );
});

test("utility styling covers variants, arbitrary properties and escaped JS strings", () => {
  for (const token of [
    "hover:bg-red-500",
    "text-white",
    "text-sm",
    "font-bold",
    "rounded-md",
    "shadow-lg",
    "text-[15px]",
    "bg-[#fff]",
    "[color:red]",
    "focus:[font-size:15px]",
    "[&_svg]:text-[length:12px]",
    "gap-[15px]",
  ]) {
    assert.equal(
      check(`const classes = ${JSON.stringify(token)};`, "new.tsx").length,
      1,
      token,
    );
  }
  assert.equal(
    check(`const classes = "bg-\\u0072ed-500";`, "new.tsx").length,
    1,
  );
  assert.equal(check(`.new { @apply hover:bg-red-500; }`).length, 1);
  assert.deepEqual(
    check(
      `const classes = "hover:bg-[var(--hover)] text-[length:var(--text-xs)] rounded-[var(--radius-sm)] gap-[var(--space-2)]";`,
      "new.tsx",
    ),
    [],
  );
});

test("utility guards distinguish styling from displayed data and follow aliases", () => {
  assert.deepEqual(
    check(
      'const example = "text-sm font-bold"; const View = () => <code>{example}</code>;',
      "new.tsx",
    ),
    [],
  );
  for (const text of [
    'const s = "rounded-md"; const View = () => <div className={s} />;',
    'const base = "text-sm"; const s = base; const View = () => <div className={s} />;',
    'const base = "shadow-lg"; const View = () => <div className={cn(base)} />;',
    'const variants = cva("text-sm", {variants: {size: {large: "text-xl"}}});',
  ])
    assert.ok(check(text, "new.tsx").length > 0, text);
});

test("new nested frontend files enter the check automatically", () => {
  const directory = mkdtempSync(join(tmpdir(), "servediff-design-"));
  try {
    mkdirSync(join(directory, "new"));
    writeFileSync(join(directory, "new", "control.css"), ".new { color: red }");
    writeFileSync(
      join(directory, "new", "Control.tsx"),
      'const classes = "text-xl";',
    );
    writeFileSync(join(directory, "README.md"), "color: red");
    const sources = readDesignSources(directory);
    assert.equal(sources.length, 2);
    assert.equal(checkDesign(sources).length, 2);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("current frontend satisfies the guard", () => {
  const root = fileURLToPath(new URL("../../apps/web/src/", import.meta.url));
  assert.deepEqual(checkDesign(readDesignSources(root)), []);
});
