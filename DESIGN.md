---
name: diffx
description: Focused, compact, calm code review workspace
colors:
  diff-canvas: "#ffffff"
  diff-canvas-dot: "#e5e7ec"
  diff-canvas-dark: "#111216"
  diff-canvas-dot-dark: "#282a31"
---

# Design system

## Design direction

diffx is a focused review workspace around [Pierre diffs](https://diffs.com).
The code is the primary content. Navigation, display controls, comments, and
review progress help people read and act on it.

Keep the interface **focused, compact, calm, precise, readable, and familiar**.
Retain neutral surfaces, violet selection, system UI type, and monospace code.
Polish this direction rather than inventing a new one.

Avoid dashboard-like card grids, oversized headings, decorative gradients,
floating toolbars, pill-shaped everything, decorative shadows, and colored
chrome competing with additions, deletions, syntax, or selected lines.

## Design principles

- Consistency over novelty. A different location does not justify a new style.
- Hierarchy before decoration. Code first, review actions second, metadata last.
- Spacing communicates grouping: close within a task, farther between tasks.
- Use semantic colors, not a convenient hex value or a syntax-theme color.
- Reuse shared primitives before adding component-specific variants.
- Keep density deliberate: compact controls, readable prose, generous hit areas.
- Responsive layouts preserve task order and access, not identical geometry.
- Respect Pierre's rendering contract. Visual changes must not break selection,
  annotations, sticky headers, context expansion, or virtual scrolling.

## Sources of truth

- `apps/web/src/tokens.css`: semantic colors, spacing, geometry, elevation,
  responsive gutters, and light/dark equivalents.
- `apps/web/src/typography.css`: fonts, text/icon sizes, weights, line heights.
- `apps/web/src/style.css`: Tailwind entry point, shadcn theme bridge, shell,
  navigation, and viewer boundary.
- `apps/web/src/components/ui`: repository-owned shadcn/Base UI primitives and
  shared variants.
- `apps/web/src/review.css`: review/sidebar composition and touch adaptations.
- `apps/web/src/DiffWorkspace.tsx`: Pierre options, React slots, measured metrics.
- `apps/web/src/display-options.ts`: supported Pierre syntax-theme pairs.

Keep these responsibilities clear. Fix the original shared rule instead of
adding another late override. Existing short names such as `--bg`, `--fg`, and
`--panel` remain canonical semantic roles; do not create a second design-token
system for Tailwind or shadcn.

## UI stack and ownership

- React and Vite own application composition and delivery.
- Tailwind CSS v4 supplies utility styling through its Vite integration and the
  single global entry stylesheet.
- shadcn/ui uses Base UI for interactive primitives. Generated components are
  source code owned by this repository, not an opaque dependency.
- Add or update primitives through `apps/web/components.json`, then customize
  the shared component source once to match this design system.
- Prefer a shared shadcn primitive and its variants for standard controls. Use
  authored CSS for shell/responsive composition, dynamic tree depth, resizing,
  measured geometry, status graphics, and Pierre integration.

The global stylesheet maps shadcn/Tailwind theme roles to existing tokens:

| Tailwind/shadcn role     | Canonical token                   |
| ------------------------ | --------------------------------- |
| Background / foreground  | `--bg` / `--fg`                   |
| Card / secondary surface | `--panel`                         |
| Popover                  | `--surface-raised`                |
| Primary / focus ring     | `--accent`                        |
| Primary foreground       | `--on-accent`                     |
| Border / input           | `--border`                        |
| Destructive              | `--error`                         |
| Muted foreground         | `--muted`                         |
| Radius / typography      | Existing geometry and type tokens |

Dark mode continues to use `[data-theme="dark"]`; do not add independent
theme state. Tailwind Preflight and utility use must not override Pierre's
measured elements or replace the application tokens with framework defaults.

## Color

| Role                          | Token               | Rule                                                               |
| ----------------------------- | ------------------- | ------------------------------------------------------------------ |
| Application / primary surface | `--bg`              | Inputs, header, primary content                                    |
| Diff canvas                   | `--diff-canvas`     | Quiet neutral surround behind opaque file diffs                    |
| Diff canvas dots              | `--diff-canvas-dot` | Faint dotted texture visible only between file diffs               |
| Secondary surface             | `--panel`           | Sidebar, grouped controls, inline review annotations               |
| Elevated surface              | `--surface-raised`  | Dialogs and future actual overlays                                 |
| Primary text                  | `--fg`              | File names, headings, review content                               |
| Secondary text                | `--text-secondary`  | Supporting prose and available secondary actions                   |
| Muted text                    | `--muted`           | Paths, counts, captions; still readable, never disabled by default |
| Separator                     | `--border-muted`    | Pane dividers and low-emphasis grouping                            |
| Control boundary              | `--border-control`  | Inputs and controls requiring stronger boundaries                  |
| Emphasized boundary           | `--border-strong`   | Rare boundaries requiring emphasis beyond focus or error styling   |
| Neutral hover                 | `--hover`           | Available controls under the pointer, compact count surfaces       |
| Accent                        | `--accent`          | Selection, focus, navigation links, primary commit action          |
| Accent interaction            | `--accent-hover`    | Hover/active on filled primary actions                             |
| Selected surface              | `--accent-bg`       | Selected file or pressed toggle                                    |
| On accent                     | `--on-accent`       | Text on filled accent; never assume white in both themes           |
| Success                       | `--success`         | Added files/counts, reviewed and resolved state                    |
| Warning                       | `--warning`         | Modified files, recoverable notices                                |
| Review anchor                 | `--review-anchor`   | Subtle tint on lines covered by a visible review comment           |
| Destructive / error           | `--error`           | Deleted files/counts, conflicts, delete actions, errors            |

- Use one foreground hierarchy across both themes. Dark mode maps the same roles;
  it is not a separate visual identity.
- Accent must explain an action or state. Do not outline every comment in violet.
  Open comments use a neutral edge; resolved comments add success plus text.
- `--file-code`, `--file-data`, `--file-markup`, and `--file-config` preserve small
  file-kind hints. They are a bounded categorical palette, not extra UI accents.
- Status always includes text, a sign, an icon, or a shape. Preserve staging's
  empty/half/full dot distinction; its half-fill is information, not decoration.
- CPU and RAM in the status bar describe the Diffx server process, not
  the browser or whole machine. Keep that scope explicit in the tooltip.
- Pierre owns syntax colors, changed-line fills, word highlights, line selection,
  and code-theme surfaces. App status tokens govern the surrounding UI only.
- Quiet frames use the light canvas/dot tokens or their dark equivalents from
  the frontmatter. Keep dots behind opaque code surfaces; they do not tint code.
  Light mode uses the white primary surface and separator-colored dots; dark mode
  uses a deeper charcoal canvas and softened dots. File headers retain `--panel`.

## Typography

Use `--font-sans` for UI and review prose; `--font-mono` for code, paths, counts,
and captured context. Keep the root at `100%` to respect browser font preferences.

| Style               | Size token (default px) | Weight / leading      | Use                                             |
| ------------------- | ----------------------- | --------------------- | ----------------------------------------------- |
| Page/overlay title  | `--text-xl` (20)        | semibold / UI         | Empty states and dialogs                        |
| Workspace heading   | `--text-md` (14)        | semibold / UI         | Compact header title, future section headings   |
| Review body         | `--text-md` (14)        | normal / copy         | Comments, explanations, editor text             |
| UI body / label     | `--text-base` (14)      | normal or medium / UI | Buttons, tabs, file navigation                  |
| Small body / action | `--text-sm` (13)        | normal or medium / UI | Toolbar actions, select labels, comment actions |
| Metadata / caption  | `--text-xs` (12)        | normal / UI           | Paths, counts, footer, state labels             |
| Code                | `--text-code` (13)      | normal / code         | Pierre rows; measured independently from UI     |

- Weights: `--weight-normal`, `--weight-medium`, `--weight-semibold` (400/500/600).
  Do not introduce incidental 550/650 weights.
- `--leading-ui` is 1.4; `--leading-copy` is 1.5. Use copy leading for prose,
  textareas, and preserved comment context.
- `--tracking-heading` gives display text a subtle negative tracking;
  `--tracking-label` is reserved for short uppercase section labels.
- Keep headings sentence case. Use semantic heading levels independently of size.
  Do not use uppercase as the default label style.
- Metadata is not body copy. Do not shrink functional text below `--text-xs`.
- `--text-input-touch` (16) is a functional mobile-input exception to prevent
  browser auto-zoom, not another heading/body style.
- Icons use `--icon-sm` (14), `--icon-base` (16), or `--icon-lg` (20).
  Brand/empty symbols are illustrations. SVG monogram text uses viewBox units.
- Code stays at `--text-base` with `--leading-code` (22px rows by default).
  Structured text uses bundled JetBrains Mono through `--font-mono`; tree text
  uses `--leading-tree`, with hit height separate from text leading.
- New type styles require a new semantic role, not a request for “slightly bigger.”

## Spacing

Use the rem-based scale: `--space-0-5`, `--space-1`, `--space-1-5`, `--space-2`,
`--space-3`, `--space-4`, `--space-6`, `--space-8`, `--space-12`, `--space-16`
→ 2, 4, 6, 8, 12, 16, 24, 32, 48, 64px by default. Keep 2/6px for compact
component internals; layout remains aligned to the 4px rhythm.

| Relationship                           | Rule                                      |
| -------------------------------------- | ----------------------------------------- |
| Icon / label, tightly related metadata | 4 or 8                                    |
| Related actions                        | 8; wrap whole actions when space runs out |
| Label to input; heading to prose       | 8                                         |
| Comment card/editor padding            | 12 in both sidebar and inline locations   |
| Sidebar section inset                  | 16                                        |
| Diff canvas gutters / file gaps        | 12; measured from `--space-3`             |
| Form groups / distinct sections        | 16 or 24                                  |
| Dialog padding                         | 24                                        |
| Large conceptual separation            | 32 or 48, rarely needed in review chrome  |

Use `--page-gutter` for notices and footer: 24 desktop, 16 below
1012px, 12 below 768px. Header, toolbar, and file frames share `--diff-gutter`
(12px). Their trailing actions account for the viewport's native scrollbar inset.
Use explicit margins instead of relying on browser-default paragraph spacing.

Custom values are acceptable only for documented optical or renderer geometry:
1px separators, 2px focus/segmented insets, small status marks, the 3px comment
edge, SVG coordinates, and the overlapping resize handle. Do not promote every
such detail into a token. New layout spacing must use the scale.

## Layout

- Use a full-width application shell, not a centered marketing container.
  The diff receives all width remaining after navigation.
- Header: `--topbar-height` (40 desktop, 44 narrow or coarse pointer). Keep
  desktop controls at 32px, header icons at 16px, the divider at 16px, and group
  spacing at 8px so the header recedes into the editor chrome. Toolbar and sidebar tabs: `--toolbar-height`
  (44) as the desktop baseline. The narrow toolbar matches the 44px page header;
  its icon buttons match the header's 44px touch targets and 16px icons. Scope
  controls fill the row without adding border/padding height; abbreviate the
  visible “All changes” label to “All”, retaining its full accessible name.
- Header and display-toolbar icon actions share borderless ghost buttons,
  16px icons, and 1.7px strokes, including menu and dialog triggers. Hover and
  expanded states use the same neutral surface feedback.
- Header uses `--panel`, a quiet brand, and the review trigger as its primary
  context. Keep the secondary scope heading and muted monospace path inline;
  hide the path at 768–1011px and both below 768px. Group ghost Refresh/theme
  actions at the trailing edge; share `--hover` feedback with the trigger.
- Status bar: `--statusbar-height` (28), with shortcuts shed before server metrics.
- The diff display toolbar uses `--panel`, matching the header and status bar.
- Desktop sidebar: `--sidebar-width` defaults to 330px. `use-sidebar.ts` owns
  the user's pixel width, bounded to 200–520px and available viewport space.
  Do not add competing CSS defaults or override a saved width at tablet sizes.
- Sidebar interiors share a 16px inset. Tree rows use 8px base padding plus
  16px per nesting level; file and folder names align to the same icon column.
- The viewport, file list, and comments list own their scrolling. Flex children
  need `min-width: 0` / `min-height: 0` so content can shrink without page overflow.
  Keep a 12rem minimum basis for sidebar content; the outer sidebar can scroll
  on short viewports or enlarged text so navigation/export/footer stay reachable.
- `--reading-width` (680px default) bounds dialogs and explanatory text, never code.
- Use flex layouts for action groups; use grids only for genuine aligned data.
  Column gaps use the spacing scale. Do not turn every grouping into a card.
- Keep overlays anchored to their owning region. Sidebar/resizer layering uses
  `--layer-sidebar` / `--layer-resizer`; modal dialogs use the native top layer.
  The resize handle overlaps the divider fully and adds no layout width, so the
  canvas's shared 12px gutter starts at the sidebar edge.

### Pierre boundary

- Prefer `CodeView` options and `renderHeaderPrefix`, `renderHeaderMetadata`, and
  `renderAnnotation` slots. Reuse the same comment components in slots and sidebar.
- Pass display preferences through the existing options and worker-pool theme
  integration. Preserve the user's split/unified, wrapping, and code-theme choice.
- Set supported `--diffs-font-*` / `--diffs-line-height` properties on `#viewer`.
  Keep the sizing probe, `ResizeObserver`, and `itemMetrics` synchronized.
- Pierre's header height is `2 * --space-4 + --space-1` (36px by default).
  Measure it as file spacing × 3 so browser font scaling updates CSS and
  `itemMetrics` together. Keep header-slot controls compact; do not apply global
  touch sizing to these slots.
- Do not add outer margins, padding, or borders to virtualized `diffs-container`
  elements. Quiet frames use `--space-3` horizontal gutters on `CodeView` and
  Pierre-measured top/bottom padding and file gaps (12px by default), read from
  the sizing probe. Frame each file with `--radius-md`, `overflow: clip`, and a
  1px `--border` outline at the edge, matching shell separators in both themes.
  Remove bottom padding from code and diff surfaces. Native horizontal scrollbar
  tracks use the final row's addition/deletion color; other endings use the base
  code surface. Set `itemMetrics.paddingBottom` to the measured native scrollbar
  height for unwrapped code, or 0 when wrapped. The frame adds no layout geometry;
  keep `itemMetrics` synchronized so virtual scrolling remains correct.
- Keep `unsafeCSS` small and justified. Do not reconstruct syntax styling or
  broadly target internal shadow-DOM elements to make the library look like chrome.
- Each file header shows a Reviewed toggle and, when comments are enabled, an
  icon titled "Leave review comment on file". File comments use explicit
  `target: "file"`, appear in Pierre's lineNumber 0 annotation slot, and never
  select code lines. Navigation targets the file header; export omits line context.
- Changing code/header geometry requires validating navigation, sticky headers,
  annotations, expansion, wrapping, and browser font scaling together.

## Borders, radii, and shadows

- Use the shared `outline-muted` Button variant (`--border-muted`) for compact
  file-level diff actions and reset reviewed. Segmented layout controls and the grouped
  file-filter field share this token.
  Text, icons, hover and focus identify these actions; avoid per-button colors.
- Use 1px `--border-muted` for separators and `--border-control` for interactive
  boundaries requiring 3:1 contrast. Reserve `--border-strong` for exceptional
  emphasis; focus and errors use their semantic tokens. Do not stack a card
  border inside another bordered panel unless it
  represents an independent review object.
- `--radius-sm` (3) is for rows, badges, and compact controls;
  `--radius-md` (6) for standard controls; `--radius-lg` (12) for sidebar comments
  and dialogs. Inline review annotations remain square. Circles are reserved for
  dots and small status marks.
- `--shadow-overlay` is the single elevation treatment for drawers/dialogs.
  Use `--backdrop` for modal separation. Whitespace and surface changes do the
  grouping work in the rest of the app.
- Quiet frames use small corners (`--radius-md`, 6px by default), a neutral
  outline, and measured gaps without an elevation shadow. The faint canvas dots
  use a 20px pitch and remain visible only outside file surfaces.

## Components

### Buttons and toggles

- Reuse the shared shadcn `Button`, `Toggle`, and `ToggleGroup` primitives.
  Primary means commit the current task; destructive signals a destructive
  action. Add shared semantic variants instead of page-specific size variants.
- Standard control height is `--control-height` (32). Compact header-slot
  controls use `--control-compact` (24); segmented groups share the 32px outer size.
- Outlined buttons use regular borders, medium UI text, 12px horizontal padding,
  and `--radius-md`. Quiet buttons use secondary text and no resting border.
- Standard buttons and button-like header statuses use the 32px control height.
  Compact sizing is reserved for subordinate icon and metadata controls. Shared
  variants own hover, active, border, and background styling.
- Filled accent is reserved for the primary action, such as saving a comment.
  Refresh, copy/export, and layout preferences are secondary actions.
- Icon buttons have accessible names and centered icons; size the hit box
  independently from the icon. Do not replace labels with unexplained symbols.

### Forms

- Reuse the shared shadcn `Input`, `Select`, and `Textarea` primitives. They use
  `--bg` or `--panel`, `--border`, and `--radius-md`. Textareas share the
  review body style and 8px/12px padding.
- Label every control. Placeholders are hints, not labels. Keep label-to-control
  spacing at 8px, including the heading above a comment editor.
- Search shows its focus ring around the entire grouped field. Selects and
  textareas retain the shared visible focus ring.
- Keep Base UI values validated at application boundaries. New validation must
  pair `--error` with explanatory text and `aria-invalid` / `aria-describedby`.

### Cards, navigation, and status

- Review comments and editors use the shared shadcn `Card` composition. Inline
  cards use a complete subtle border, `--radius-md`, generous outer spacing, and
  no shadow; sidebar cards use `--radius-lg`. Separate card regions with a raised
  body surface, neutral header, and neutral action row; reserve accent tint for
  active editors and selections. Keep status at the
  header's trailing edge and use compact labeled icons for state and actions.
- Comment actions and each file's viewed control use standard button sizing.
  Comment status badges match the same control height.
- Resolved comments collapse to their header by default. A labeled chevron
  expands the body and actions; reopening restores the expanded open state.
- Comment views default to open comments and offer Open, Resolved, and All filters.
  Export uses one primary `Copy review` action plus a menu for broader scopes.
- Keep `Save comment` enabled when idle; empty submission focuses the labeled
  editor and shows an inline error. Disable it while saving. Deleting a saved
  comment requires explicit confirmation.
- Tint every old/new diff row covered by a visible comment, including its
  gutter and multi-line range. Use a stronger green/red tint for addition and
  deletion rows; reserve `--review-anchor` yellow for unchanged context. Mix
  each tint with Pierre's existing background instead of replacing it.
- Navigation uses a quiet background, aligned text/icons, and a clear current
  item. Reviewed files retain readable text and a check; do not fade the entire row.
- Navigating from the file list briefly pulses the destination file header with
  the accent for about one second. Reduced-motion mode uses static feedback that
  remains visible long enough to identify the destination without animation.
- Sidebar mode buttons use an accent underline. Segmented choices and pressed
  toggles use accent text on `--accent-bg`. Expose state with native/ARIA semantics.
- Badges use compact text, `--radius-sm`, and neutral surfaces. Status color is
  supplemental; retain “Open”, “Resolved”, Git letters, counts, and staging shapes.
- Future menus or tables inherit these text, row, surface, and selection rules.
  Do not invent a separate menu/table palette or rounded container style.

### Overlays

- Project navigation uses a compact borderless ghost action after the brand
  divider, with a semibold repository name, quieter branch, and repository
  icon. Give the repository name priority when long context labels truncate.
  Piped reviews use a terminal icon, “Piped”, and collection time; omit repository,
  branch, worktree, and directory identity.
  Use a search icon and shortcut hint, never a dropdown chevron. Click and
  Cmd/Ctrl+K open the same searchable “Switch review” dialog. Position it at
  `--topbar-height` plus `--space-6` on desktop (64px default), `--space-4` on
  mobile (16px default). Bound picker width to 35rem and viewport gutters. The
  picker owns focus and stays available during context errors.
- Search leads the picker; keep “Switch review” as its screen-reader title.
  Label global search “Search reviews” and Piped history search “Search Piped”.
  Use one integrated field (36px input) with close at its right edge and the
  focus ring around its entire boundary.
  Use a flat, scrollable result list with changed checkouts first, then recency
  within each group; search preserves that order, using match strength to break
  recency ties. Compact rows (about 48px) put repository name and branch on
  one line, with a muted monospace path below; truncate long text to single lines.
  Mark linked paths with “Worktree”. Group retained imports under a separate
  “Piped” entry after repositories, with a quiet separator and snapshot count.
  Its history shows collection timestamps newest first, including legacy captures;
  changed-file counts remain secondary. Show “Piped · Newest first” below search
  and omit repository filters from this history. Repository freshness, host,
  branch, worktree, and status filters never hide Piped imports.
  Distinguish the current review with a check and accessible “Current review”
  text; retain explicit “Unavailable” feedback. Keep the footer to a short result
  count and keyboard hints.
  Show a compact trailing changed-file count across staged, unstaged, and
  untracked changes, independent of the current scope. Empty checkouts show
  “No changes”; unknown metadata shows “Status unknown”. Repository results default to
  Latest snapshots with changes. Put All / Latest / Stale in a compact segmented
  control below search, with a right-aligned All checkbox that includes unavailable,
  empty, and unknown-status snapshots. Keep freshness independent of that checkbox.
  Mark stale snapshot rows with an explicit trailing “Stale” indicator. Preserve
  the selected review when ingestion refreshes the catalog.
  Keep names readable and truncate paths before status, including on mobile.

- Use the controlled shadcn `Dialog` built on Base UI. General dialogs retain a
  visible accessible title; the review picker uses the screen-reader title
  above. Keep focus inside while open, support Escape, and restore focus on close.
- Use `--surface-raised`, `--radius-lg`, `--shadow-overlay`, and `--backdrop`.
  Shadows communicate actual elevation only; no resting button/card shadows.
- Bound width by `--reading-width` and viewport gutters; bound height by the
  dynamic viewport and allow internal scrolling. Long XML must remain selectable.
- Navigation below 1012px is a modal drawer with backdrop, focus containment,
  explicit close, inert background, Escape support, and focus restoration.

## Interaction states

| State    | Expectation                                                                         |
| -------- | ----------------------------------------------------------------------------------- |
| Hover    | Neutral surface feedback on available actions; preserve selected styling            |
| Focus    | `--focus-ring` with `--focus-offset`; never rely on hover or color alone            |
| Active   | Immediate surface feedback; filled primary uses `--accent-hover`                    |
| Selected | Persistent accent surface/underline plus `aria-current` or `aria-pressed`           |
| Disabled | Native `disabled`, subdued opacity, no hover, no pointer cursor                     |
| Loading  | `aria-busy`, status text, stable size, progress cursor, reduced-motion-safe spinner |
| Error    | `--error` plus a readable explanation and recovery action where applicable          |

Do not add motion for decoration. New animation must honor
`prefers-reduced-motion`; feedback must still work without animation.

## Responsive design

- Use 1012px for drawer navigation/tighter gutters and 768px for compact mobile
  chrome. Synchronize drawer behavior with `use-sidebar.ts`.
- Keep scope and desktop layout choice visible. View options and collapse/expand
  all are adjacent icon buttons with action titles. Put wrap, inline detail,
  code theme, and narrow layout choice in View options.
- Mobile retains the review trigger's repository/branch or Piped/time context; truncate long
  labels and hide its shortcut hint. Hide the secondary heading/path and refresh
  text, preserving accessible labels and theme/sidebar controls.
- Collapse navigation to the toggle-controlled modal drawer; its width is
  `min(320px, 88vw)`. Keep file selection, comments, and export reachable there.
  Its tab strip matches `--topbar-height`, including the tablet header height.
  File/folder rows, filter, summary toggle, and review progress retain desktop
  control heights and spacing; do not apply the general mobile enlargement here.
- Hide redundant summary/shortcut detail before removing essential actions.
  Keep the Files changed total and inline added/deleted/modified/renamed counts
  visible in the drawer. Use file icons, semantic status colors, hover labels,
  and accessible names; wrap the group when the sidebar is too narrow.
- At mobile widths or coarse pointers, app-owned controls target 44px hit heights;
  icon controls also reach 44px width. Text-input sizing uses `--text-input-touch`.
  The compact View options and collapse/expand icons match the theme control's
  44px size within the narrow 44px toolbar, matching the header touch targets.
  Drawer navigation and summary controls retain desktop density, per the same brief.
- Pierre code rows and header slots retain measured geometry. Larger library
  gutter/header targets need a coordinated renderer-metric change, not a CSS override.
- Preserve the desktop split/unified and wrap preferences. Default narrow screens
  to unified without overwriting the desktop preference; allow a session override.
- Test narrow and short viewports, long paths/comments, expanded navigation,
  wrapped controls, and enlarged browser fonts. Shrinking text is not a fix.

## Accessibility

- Meet WCAG AA: at least 4.5:1 for normal text and 3:1 for meaningful control
  boundaries, focus indicators, and large text. Check actual adjacent surfaces
  in both themes, including selected and elevated surfaces.
- Use real buttons, inputs, selects, summaries, and dialogs. Modifier-based
  keyboard shortcuts avoid single-character activation conflicts. Preserve tree
  navigation, sidebar resizing, and Pierre's interaction model.
- Every icon-only action needs a name. Every form field needs a label. Connect
  dialog titles and validation messages programmatically.
- Focus must remain visible inside scrollable panes and on slotted review controls.
  Never remove an outline without providing an equally visible replacement.
- Aim for 44px touch targets; compact desktop actions must meet at least 24px.
  Audit Pierre-owned targets separately when changing library interaction geometry.
- Do not communicate reviewed, selected, staged, loading, or error state using
  color alone. Announce asynchronous feedback with the existing live regions.
- Respect root font preferences. Keep code metrics measured rather than assuming
  that rem always equals 16px; keep prose readable and long content wrappable.

## Rules for future UI work

> Do not introduce a new color, font size, spacing value, radius, shadow, or
> component variant unless the existing system cannot express the required design meaning.

Before adding a value or visual pattern:

1. Does a token already express this role?
2. Does an existing component solve this?
3. Is the difference meaningful or incidental?
4. Does this compete with Pierre's code or duplicate its styling?
5. Does it preserve selection, annotations, and measured scrolling?
6. Does it remain coherent on mobile, with long content, and in both themes?
7. Are keyboard focus, contrast, labels, and hit areas correct?
8. Does this follow `DESIGN.md`? If a new role is necessary, update it here.

## Enforcement

`mise run check:design` scans CSS, TypeScript and TSX under `apps/web/src`,
including new nested files, static utilities, inline style objects, SVG colors
and Pierre's `unsafeCSS`. Local `mise run check` and pre-push run the guard
and its contracts. It rejects literal palettes outside `tokens.css` definitions,
independent typography, literal radii/elevation, and arbitrary padding/margin/gap
lengths. Existing ordinary Tailwind numeric spacing utilities remain allowed;
their relationship to the spacing scale needs review. Compose authored values
from the canonical tokens. Zero, automatic spacing
and 1px optical insets remain valid; dimensions, breakpoints and dynamic measured
geometry require review rather than a blanket literal-value ban.

The guard has exact exceptions for status circles, SVG monogram units, icon line
boxes, compact comment counts, the overlapping sidebar resizer, and Pierre's
line-surface mixes/inset strokes. Existing generated tabs' 3px inset and active
shadow, and the dialog title's unit line-height, are bounded legacy exceptions;
do not copy them into new controls. Changes to exceptions need a concrete role,
rationale and contract test in the same PR. Do not exempt entire components.

Static checks cannot prove responsive behavior, accessibility, visual hierarchy
or correctness of styles assembled at runtime. Use existing browser tests and
review affected themes, narrow layouts and keyboard interaction. Changes to
Pierre geometry also require selection, annotation, expansion, wrapping and
scrolling checks. Record this evidence in the PR template.

## Audit baseline and consolidation

This audit records the starting points, not permission to reuse legacy values.

| Area             | Existing pattern / inconsistency                                                            | Consolidation                                                                                |
| ---------------- | ------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| Surfaces         | White / `#f8f9fb`; dark `#151619` / `#1b1c20`                                               | Retain neutral shell; add a semantic elevated surface                                        |
| Text             | `#24252a` / `#e1e2e7`; most supporting text shared `#787c86` / `#91949e`                    | Distinguish secondary/muted; strengthen muted contrast                                       |
| Borders/hover    | `#e5e7ec` / `#303137`; `#eeeef4` / `#282930`                                                | Retain separators; distinguish control boundaries                                            |
| Accent/brand     | Violet `#6260df` / `#aba7ff`, pale violet fills; former yellow logo                         | Preserve violet roles; remove the nonessential logo                                          |
| Status           | Green `#24844c` / `#70cc95`, red `#cf4b51` / `#ed8a8e`, shared gold `#b58a29`               | Semantic success/error/warning with theme-equivalent contrast                                |
| File kinds       | Four inline light/dark blue, gold, orange, purple pairs                                     | Bounded semantic file-kind tokens                                                            |
| Type             | System sans/mono; 9, 10, 11, 12, 13, 14, 15, 17, 18px; 400/550/600/650/700 weights          | Five UI sizes; three weights; separate functional touch-input size                           |
| Leading/tracking | Browser defaults, 18/13 tree, 22/13 code, 1.5/1.6/1.7 prose; −0.6px and 1.1px tracking      | Explicit UI/copy leading, semantic tracking; preserve measured code leading                  |
| Spacing          | Repeated 3/5/6/7/9/10/11/14/15/17/18/20/22px padding, margins, gaps mixed with 4/8/12/16/24 | 4px rhythm; documented optical exceptions only                                               |
| Radii            | 3, 4, 5, 6, 7, 8, 10px plus circles                                                         | 3/6/12px roles; circles only for dots/checks                                                 |
| Shadows          | Tiny button and segmented shadows; sidebar shadow; dialog backdrop                          | One overlay shadow; non-geometric file outlines without elevation                            |
| Geometry         | Header 58px, toolbar 40px; 24/28/30px icon controls and content-sized buttons               | 56/44px shell rhythm, 32px controls, 24px Pierre slots, 44px touch targets                   |
| Width/gutters    | Competing 260/220/280px sidebar rules; 10/12/14/22/24px gutters; 680px dialog               | One user-owned sidebar width; shared shell gutters/reading width; measured 12px diff spacing |
| Review           | Inline 12×14px padding versus sidebar/mobile 10px; 85px editor, 300px XML area              | Shared 12px comment inset, readable editor, viewport-bounded overlay                         |
| States           | Search removed input focus; textarea/select lacked shared focus; reviewed rows faded to 55% | Group/field focus, readable reviewed state, explicit destructive action                      |
| Responsive       | 1000/760px breakpoints; toolbar wrapped only below 760px                                    | 1012px drawer, 768px compact chrome, unified narrow default                                  |

### Decisions needing product judgment

- Validate compact density and the retained file-kind colors with real review
  sessions before adding a density preference or changing the brand direction.
- Larger Pierre gutter/header touch targets require a renderer-aware accessibility
  pass. Do not compromise scroll geometry to claim touch-target compliance.
- Keep all existing code-theme options; judge their syntax contrast separately
  from the shell when adding or upgrading a Pierre theme.
