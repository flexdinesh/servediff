import { readdirSync, readFileSync } from "node:fs";
import { join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "@babel/parser";

export interface DesignSource {
  path: string;
  text: string;
}

export interface DesignViolation {
  path: string;
  line: number;
  property: string;
  value: string;
}

const colors = new Set(
  "aliceblue antiquewhite aqua aquamarine azure beige bisque black blanchedalmond blue blueviolet brown burlywood cadetblue chartreuse chocolate coral cornflowerblue cornsilk crimson cyan darkblue darkcyan darkgoldenrod darkgray darkgreen darkgrey darkkhaki darkmagenta darkolivegreen darkorange darkorchid darkred darksalmon darkseagreen darkslateblue darkslategray darkslategrey darkturquoise darkviolet deeppink deepskyblue dimgray dimgrey dodgerblue firebrick floralwhite forestgreen fuchsia gainsboro ghostwhite gold goldenrod gray green greenyellow grey honeydew hotpink indianred indigo ivory khaki lavender lavenderblush lawngreen lemonchiffon lightblue lightcoral lightcyan lightgoldenrodyellow lightgray lightgreen lightgrey lightpink lightsalmon lightseagreen lightskyblue lightslategray lightslategrey lightsteelblue lightyellow lime limegreen linen magenta maroon mediumaquamarine mediumblue mediumorchid mediumpurple mediumseagreen mediumslateblue mediumspringgreen mediumturquoise mediumvioletred midnightblue mintcream mistyrose moccasin navajowhite navy oldlace olive olivedrab orange orangered orchid palegoldenrod palegreen paleturquoise palevioletred papayawhip peachpuff peru pink plum powderblue purple rebeccapurple red rosybrown royalblue saddlebrown salmon sandybrown seagreen seashell sienna silver skyblue slateblue slategray slategrey snow springgreen steelblue tan teal thistle tomato turquoise violet wheat white whitesmoke yellow yellowgreen".split(
    " ",
  ),
);
const typeProperty =
  /^(?:(?:font(?:-family|-size|-weight)?|line-height|letter-spacing)$|--(?:font|text|weight|leading|tracking|icon)-)/;
const spacingProperty =
  /^(?:(?:padding|margin)(?:-(?:top|right|bottom|left|inline|block)(?:-start|-end)?)?|(?:row-|column-)?gap)$/;
const radiusProperty = /^border(?:-[\w-]+)?-radius$/;
const colorProperty =
  /(?:color|background|border|outline|shadow|fill|stroke)|^--/;

// Exact optical/renderer allowances, not file-wide exclusions. DESIGN.md owns
// the reasons; token rules still apply to every other declaration in these files.
const exceptions = new Set([
  "style.css|.live-dot|border-radius|50%",
  "review.css|.file-reviewed-check|border-radius|50%",
  "review.css|.staging-dot|border-radius|50%",
  "review.css|.file-row .file-icon|line-height|1",
  "review.css|.monogram-icon text|font|700 9px ui-monospace, monospace",
  "review.css|.comment-filter-count|font-size|calc(var(--text-xs) - 1px)",
  "review.css|.comment-filter-count|line-height|1",
  "review.css|#sidebar-resizer|margin|0 -2px 0 -3px",
  "style.css|#viewer|--diff-gutter-divider-width|2px",
]);

function normalize(value: string): string {
  return value
    .replace(/\s+/g, " ")
    .trim()
    .replace(/\s*!important$/i, "")
    .replace(/\(\s+/g, "(")
    .replace(/\s+\)/g, ")");
}

function withoutVariables(value: string): string {
  // Keep fallbacks visible: var(--role, red) must still fail the palette rule.
  return value.replace(/var\(\s*--[\w-]+\s*(?:,\s*([^()]*))?\)/gi, "$1");
}

function invalidValue(
  path: string,
  selector: string,
  property: string,
  raw: string,
): boolean {
  const value = normalize(raw);
  if (exceptions.has(`${path}|${selector}|${property}|${value}`)) return false;
  const paletteOwner = path === "tokens.css" && property.startsWith("--");
  const typeOwner = path === "typography.css";
  const remainder = withoutVariables(value).replace(
    /url\([^)]*\)|"[^"\\]*(?:\\.[^"\\]*)*"|'[^'\\]*(?:\\.[^'\\]*)*'/gi,
    "",
  );
  const literalColor =
    /#[\da-f]{3,8}\b|\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color)\(/i.test(
      remainder,
    );
  const namedColor =
    colorProperty.test(property) &&
    (remainder.toLowerCase().match(/[a-z]+/g) ?? []).some((word) =>
      colors.has(word),
    );
  // Pierre mixes black into its own addition/deletion backgrounds for comments.
  const pierreMix =
    path === "DiffWorkspace.tsx" &&
    property === "--diffs-line-bg" &&
    /^color-mix\(in srgb, var\(--diffs-computed-diff-line-bg\) 76%, color-mix\(in srgb, var\(--diffs-(?:addition|deletion)-base\) 72%, black\)\)$/.test(
      value,
    );
  if (!paletteOwner && (literalColor || (namedColor && !pierreMix)))
    return true;
  if (
    property.startsWith("--") &&
    !paletteOwner &&
    !typeOwner &&
    /(?:\d*\.)?\d+(?:px|rem|em|%|vh|vw|dvh|dvw)/.test(remainder)
  ) {
    const dimensions =
      remainder.match(/(?:\d*\.)?\d+(?:px|rem|em|%|vh|vw|dvh|dvw)/g) ?? [];
    if (
      dimensions.some(
        (dimension) => !/^0(?:px|rem|em|%|vh|vw|dvh|dvw)$/.test(dimension),
      ) &&
      !property.startsWith("--diffs-line-bg")
    )
      return true;
  }
  if (
    typeProperty.test(property) &&
    !typeOwner &&
    !(paletteOwner && literalColor)
  ) {
    if (/^(?:inherit|initial|unset|revert|revert-layer)$/.test(value))
      return false;
    if (!value.includes("var(")) return true;
    if (/\b\d/.test(remainder)) return true;
    if (/[a-z]/i.test(remainder.replace(/\b(?:calc|min|max|clamp)\b/g, "")))
      return true;
  }
  if (
    radiusProperty.test(property) &&
    !/^(?:0(?:px|rem|em|%)?|none|inherit|initial|unset)$/.test(value)
  ) {
    if (
      !value.includes("var(") ||
      /(?:\d*\.)?\d+(?:px|rem|em|%)/.test(remainder)
    )
      return true;
  }
  if (
    /^(?:box|text)-shadow$/.test(property) &&
    !/^(?:none|inherit|initial|unset|var\(--[\w-]+\))$/.test(value)
  ) {
    // These inset strokes carry header separators/navigation feedback, no elevation.
    if (
      !(
        path === "DiffWorkspace.tsx" &&
        property === "box-shadow" &&
        /^(?:inset 0 -1px var\(--border\)|inset 3px 0 var\(--accent\)(?:, inset 0 -1px var\(--border\))?|inset 0 0 transparent(?:, inset 0 -1px var\(--border\))?)$/.test(
          value,
        )
      )
    )
      return true;
  }
  if (spacingProperty.test(property) && !paletteOwner) {
    // Zero, auto, calc multipliers and 1px optical insets are allowed; layout
    // lengths use tokens. The overlapping resizer has an exact allowance above.
    const lengths =
      remainder.match(/-?(?:\d*\.)?\d+(?:px|rem|em|%|vh|vw|dvh|dvw)/g) ?? [];
    if (
      lengths.some(
        (length) => !/^-?(?:0(?:px|rem|em|%|vh|vw|dvh|dvw)|1px)$/.test(length),
      )
    )
      return true;
    if (/^[-+]?\d+(?:\.\d+)?$/.test(value) && Number(value) !== 0) return true;
  }
  return false;
}

// Lex declarations rather than grep lines: comments, strings, nested rules,
// multiline functions and semicolons inside URLs do not split declarations.
function withoutComments(text: string): string {
  let output = "";
  let quote = "";
  for (let index = 0; index < text.length; index++) {
    const character = text[index] ?? "";
    if (quote) {
      output += character;
      if (character === "\\") output += text[++index] ?? "";
      else if (character === quote) quote = "";
    } else if (character === '"' || character === "'") {
      quote = character;
      output += character;
    } else if (character === "/" && text[index + 1] === "*") {
      const end = text.indexOf("*/", index + 2);
      const finish = end < 0 ? text.length : end + 2;
      output += text.slice(index, finish).replace(/[^\n]/g, " ");
      index = finish - 1;
    } else output += character;
  }
  return output;
}

function cssDeclarations(
  text: string,
  visit: (
    selector: string,
    property: string,
    value: string,
    offset: number,
  ) => void,
): void {
  const clean = withoutComments(text);
  const selectors: string[] = [];
  let start = 0;
  let parentheses = 0;
  let quote = "";
  function declaration(end: number) {
    const segment = clean.slice(start, end);
    const match = /^\s*([\w-]+)\s*:\s*([\s\S]*)$/.exec(segment);
    if (match?.[1] && match[2] !== undefined) {
      visit(
        selectors.at(-1) ?? "",
        match[1].toLowerCase(),
        match[2],
        start + segment.search(/\S/),
      );
    }
    const apply = /^\s*@apply\s+([\s\S]+)$/.exec(segment);
    if (apply?.[1])
      visit(
        selectors.at(-1) ?? "",
        "@apply",
        apply[1],
        start + segment.search(/\S/),
      );
  }
  for (let index = 0; index < clean.length; index++) {
    const character = clean[index];
    if (quote) {
      if (character === "\\") index++;
      else if (character === quote) quote = "";
      continue;
    }
    if (character === '"' || character === "'") {
      quote = character;
      continue;
    }
    if (character === "(") parentheses++;
    if (character === ")") parentheses--;
    if (parentheses !== 0) continue;
    if (character === "{") {
      selectors.push(normalize(clean.slice(start, index)));
      start = index + 1;
    } else if (character === ";") {
      declaration(index);
      start = index + 1;
    } else if (character === "}") {
      declaration(index);
      selectors.pop();
      start = index + 1;
    }
  }
}

function utilityTokens(text: string): string[] {
  return text.split(/\s+/);
}

function baseUtility(token: string): string {
  let depth = 0;
  let lastVariant = -1;
  for (let index = 0; index < token.length; index++) {
    if (token[index] === "[" || token[index] === "(") depth++;
    if (token[index] === "]" || token[index] === ")") depth--;
    if (token[index] === ":" && depth === 0) lastVariant = index;
  }
  return token.slice(lastVariant + 1).replace(/^!|!$/g, "");
}

function utilityInvalid(path: string, original: string, context = ""): boolean {
  const token = baseUtility(original);
  const property = /^\[([\w-]+):(.+)\]$/.exec(token);
  if (property?.[1] && property[2])
    return invalidValue(
      path,
      "",
      property[1],
      property[2].replaceAll("_", " "),
    );
  const arbitrary =
    /^(rounded(?:-[trblse]{1,2})?|font|text|leading|tracking|shadow|bg|border|outline|ring|fill|stroke|p[xytrblse]?|m[xytrblse]?|gap(?:-[xy])?)-\[(.+)\]$/.exec(
      token,
    );
  if (arbitrary?.[1] && arbitrary[2]) {
    const prefix = arbitrary[1];
    const value = arbitrary[2]
      .replace(/^(?:length|color):/, "")
      .replaceAll("_", " ");
    if (
      path === "components/ui/tabs.tsx" &&
      token === "p-[3px]" &&
      context ===
        "group/tabs-list inline-flex w-fit items-center justify-center rounded-[var(--radius-control)] p-[3px] text-muted-foreground group-data-horizontal/tabs:h-[var(--control-height)] group-data-vertical/tabs:h-fit group-data-vertical/tabs:flex-col data-[variant=line]:rounded-none"
    )
      return false;
    const cssProperty = prefix.startsWith("rounded")
      ? "border-radius"
      : prefix === "font"
        ? "font"
        : prefix === "leading"
          ? "line-height"
          : prefix === "tracking"
            ? "letter-spacing"
            : prefix === "shadow"
              ? "box-shadow"
              : /^(?:p|m|gap)/.test(prefix)
                ? "padding"
                : prefix === "text" &&
                    (arbitrary[2].startsWith("length:") ||
                      /\d(?:px|rem|em)/.test(value))
                  ? "font-size"
                  : "color";
    return invalidValue(path, "", cssProperty, value);
  }
  if (
    /^(?:bg|text|border|outline|ring|fill|stroke|shadow)-(?:black|white|(?:slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-\d+)(?:\/.*)?$/.test(
      token,
    )
  )
    return true;
  if (
    /^text-(?:xs|sm|base|lg|xl|\d+xl)$|^font-(?:thin|extralight|light|normal|medium|semibold|bold|extrabold|black|sans|serif|mono)$|^leading-(?:none|tight|snug|normal|relaxed|loose|\d+)$|^tracking-(?:tighter|tight|normal|wide|wider|widest)$|^rounded(?:-[trblse]{1,2})?(?:-(?:xs|sm|md|lg|xl|\d+xl|full))?$|^(?:text-)?shadow(?:-(?:xs|sm|md|lg|xl|\d+xl))?$/.test(
      token,
    )
  ) {
    return (
      !(
        path === "components/ui/tabs.tsx" &&
        original ===
          "group-data-[variant=default]/tabs-list:data-active:shadow-sm"
      ) &&
      !(
        path === "components/ui/dialog.tsx" &&
        token === "leading-none" &&
        context ===
          "font-heading text-[length:var(--text-base)] leading-none font-[var(--weight-medium)]"
      )
    );
  }
  return false;
}

export function checkDesign(
  sources: readonly DesignSource[],
): DesignViolation[] {
  const violations: DesignViolation[] = [];
  for (const source of sources) {
    const add = (property: string, value: string, offset: number) =>
      violations.push({
        path: source.path,
        line: source.text.slice(0, offset).split("\n").length,
        property,
        value: normalize(value),
      });
    const css = (text: string, offset: number) =>
      cssDeclarations(text, (selector, property, value, at) => {
        if (property === "@apply") {
          for (const token of utilityTokens(value))
            if (utilityInvalid(source.path, token))
              add("utility", token, offset + at);
        } else if (invalidValue(source.path, selector, property, value))
          add(property, value, offset + at);
      });
    if (source.path.endsWith(".css")) {
      css(source.text, 0);
      continue;
    }
    const ast = parse(source.text, {
      sourceType: "module",
      plugins: ["typescript", "jsx"],
    });
    function record(node: unknown): node is Record<string, unknown> {
      return typeof node === "object" && node !== null;
    }
    function literal(node: unknown): string | undefined {
      if (!record(node)) return undefined;
      if (node.type === "StringLiteral" && typeof node.value === "string")
        return node.value;
      if (node.type === "NumericLiteral" && typeof node.value === "number")
        return String(node.value);
      if (node.type === "TemplateLiteral" && Array.isArray(node.quasis)) {
        const parts: string[] = [];
        for (const quasi of node.quasis) {
          if (
            !record(quasi) ||
            !record(quasi.value) ||
            typeof quasi.value.cooked !== "string"
          )
            return undefined;
          parts.push(quasi.value.cooked);
        }
        return parts.join("0");
      }
      return undefined;
    }
    function interpolated(node: unknown): boolean {
      return (
        record(node) &&
        node.type === "TemplateLiteral" &&
        Array.isArray(node.expressions) &&
        node.expressions.length > 0
      );
    }
    const styleNames = new Set<string>();
    const classNames = new Set<string>();
    const declarations = new Map<string, unknown>();
    function collectIdentifiers(node: unknown, names: Set<string>) {
      if (Array.isArray(node)) {
        for (const item of node) collectIdentifiers(item, names);
        return;
      }
      if (!record(node)) return;
      if (node.type === "Identifier" && typeof node.name === "string")
        names.add(node.name);
      for (const [key, value] of Object.entries(node))
        if (key !== "loc" && key !== "comments")
          collectIdentifiers(value, names);
    }
    function utilityCall(node: Record<string, unknown>): boolean {
      return (
        node.type === "CallExpression" &&
        record(node.callee) &&
        typeof node.callee.name === "string" &&
        /^(?:cn|cva|clsx|classnames)$/.test(node.callee.name)
      );
    }
    function collect(node: unknown) {
      if (Array.isArray(node)) {
        for (const item of node) collect(item);
        return;
      }
      if (!record(node)) return;
      if (
        node.type === "JSXAttribute" &&
        record(node.name) &&
        node.name.name === "style"
      )
        collectIdentifiers(node.value, styleNames);
      if (
        node.type === "JSXAttribute" &&
        record(node.name) &&
        node.name.name === "className"
      )
        collectIdentifiers(node.value, classNames);
      if (utilityCall(node)) collectIdentifiers(node.arguments, classNames);
      if (
        node.type === "VariableDeclarator" &&
        record(node.id) &&
        typeof node.id.name === "string"
      ) {
        declarations.set(node.id.name, node.init);
        if (/style$/i.test(node.id.name)) styleNames.add(node.id.name);
        if (/class(?:es|names)?$/i.test(node.id.name))
          classNames.add(node.id.name);
        const annotation = record(node.id.typeAnnotation)
          ? node.id.typeAnnotation.typeAnnotation
          : undefined;
        if (
          record(annotation) &&
          record(annotation.typeName) &&
          (annotation.typeName.name === "CSSProperties" ||
            (record(annotation.typeName.right) &&
              annotation.typeName.right.name === "CSSProperties"))
        )
          styleNames.add(node.id.name);
      }
      for (const [key, value] of Object.entries(node))
        if (key !== "loc" && key !== "comments") collect(value);
    }
    collect(ast);
    // Resolve referenced object aliases/spreads, without evaluating JS behavior.
    for (const name of styleNames)
      collectIdentifiers(declarations.get(name), styleNames);
    for (const name of classNames)
      collectIdentifiers(declarations.get(name), classNames);
    function walk(node: unknown, styleContext = false, utilityContext = false) {
      if (Array.isArray(node)) {
        for (const item of node) walk(item, styleContext, utilityContext);
        return;
      }
      if (!record(node)) return;
      const offset = typeof node.start === "number" ? node.start : 0;
      if (
        (node.type === "JSXAttribute" &&
          record(node.name) &&
          node.name.name === "style") ||
        (node.type === "VariableDeclarator" &&
          record(node.id) &&
          typeof node.id.name === "string" &&
          styleNames.has(node.id.name))
      )
        styleContext = true;
      if (
        (node.type === "JSXAttribute" &&
          record(node.name) &&
          node.name.name === "className") ||
        utilityCall(node) ||
        (node.type === "VariableDeclarator" &&
          record(node.id) &&
          typeof node.id.name === "string" &&
          classNames.has(node.id.name))
      )
        utilityContext = true;
      if (
        node.type === "JSXAttribute" &&
        record(node.name) &&
        typeof node.name.name === "string" &&
        /^(?:fill|stroke|color)$/.test(node.name.name)
      ) {
        const attribute =
          record(node.value) && node.value.type === "JSXExpressionContainer"
            ? node.value.expression
            : node.value;
        const value = literal(attribute);
        if (
          value === undefined ||
          interpolated(attribute) ||
          invalidValue(source.path, "", node.name.name, value)
        )
          add(node.name.name, value ?? "<dynamic>", offset);
      }
      if (node.type === "ObjectProperty") {
        const name =
          record(node.key) && typeof node.key.name === "string"
            ? node.key.name
            : literal(node.key);
        const value = literal(node.value);
        if (name === "unsafeCSS") {
          const expressions =
            record(node.value) && Array.isArray(node.value.expressions)
              ? node.value.expressions
              : [];
          if (
            value === undefined ||
            expressions.some(
              (expression: unknown) =>
                source.path !== "DiffWorkspace.tsx" ||
                !record(expression) ||
                expression.type !== "Identifier" ||
                expression.name !== "NAVIGATION_CUE_MS",
            )
          )
            add("unsafeCSS", "<dynamic>", offset);
          else
            css(
              value,
              record(node.value) && typeof node.value.start === "number"
                ? node.value.start
                : offset,
            );
        } else if (name && styleContext) {
          const property = name.replace(
            /[A-Z]/g,
            (letter) => `-${letter.toLowerCase()}`,
          );
          if (
            typeProperty.test(property) ||
            radiusProperty.test(property) ||
            spacingProperty.test(property) ||
            /(?:^|-)color$|^background(?:-image)?$|^(?:border|outline)(?:-(?:top|right|bottom|left|inline|block)(?:-(?:start|end))?)?$|^(?:box|text)-shadow$|^(?:fill|stroke)$/.test(
              property,
            )
          ) {
            if (
              value === undefined ||
              (interpolated(node.value) &&
                !(
                  spacingProperty.test(property) &&
                  value.startsWith("calc(") &&
                  value.includes("var(--space-")
                )) ||
              invalidValue(source.path, "", property, value)
            )
              add(property, value ?? "<dynamic>", offset);
          }
        }
      }
      if (
        utilityContext &&
        (node.type === "StringLiteral" || node.type === "TemplateLiteral")
      ) {
        const text = literal(node) ?? "";
        for (const token of utilityTokens(text))
          if (utilityInvalid(source.path, token, text))
            add("utility", token, offset);
      }
      for (const [key, value] of Object.entries(node))
        if (key !== "loc" && key !== "comments")
          walk(value, styleContext, utilityContext);
    }
    walk(ast);
  }
  return violations;
}

export function readDesignSources(root: string): DesignSource[] {
  const sources: DesignSource[] = [];
  function walk(directory: string) {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const path = join(directory, entry.name);
      if (entry.isDirectory()) walk(path);
      else if (/\.(css|tsx?)$/.test(path))
        sources.push({
          path: relative(root, path).split(sep).join("/"),
          text: readFileSync(path, "utf8"),
        });
    }
  }
  walk(root);
  return sources;
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const root = fileURLToPath(new URL("../apps/web/src/", import.meta.url));
  const violations = checkDesign(readDesignSources(root));
  for (const violation of violations)
    console.error(
      `${violation.path}:${violation.line}: ${violation.property}: ${violation.value} — use DESIGN.md tokens`,
    );
  if (violations.length) process.exitCode = 1;
}
