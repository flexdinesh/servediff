# Design system

## Design direction

servediff is a focused review workspace around [Pierre diffs](https://diffs.com).
The code is the primary content. Navigation, display controls, comments, and
review progress help people read and act on it.

Keep the interface **focused, compact, calm, precise, readable, and familiar**.
Retain neutral surfaces, violet selection, the yellow brand mark, system UI
type, and monospace code. Polish this direction rather than inventing a new one.

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

| Role                          | Token              | Rule                                                               |
| ----------------------------- | ------------------ | ------------------------------------------------------------------ |
| Application / primary surface | `--bg`             | Canvas surround, inputs, header, primary content                   |
| Secondary surface             | `--panel`          | Sidebar, grouped controls, inline review annotations               |
| Elevated surface              | `--surface-raised` | Dialogs and future actual overlays                                 |
| Primary text                  | `--fg`             | File names, headings, review content                               |
| Secondary text                | `--text-secondary` | Supporting prose and available secondary actions                   |
| Muted text                    | `--muted`          | Paths, counts, captions; still readable, never disabled by default |
| Separator                     | `--border-muted`   | Pane dividers and low-emphasis grouping                            |
| Control boundary              | `--border-control` | Inputs, outlined buttons, and resting interactive boundaries       |
| Emphasized boundary           | `--border-strong`  | Rare boundaries requiring emphasis beyond focus or error styling   |
| Neutral hover                 | `--hover`          | Available controls under the pointer, compact count surfaces       |
| Accent                        | `--accent`         | Selection, focus, navigation links, primary commit action          |
| Accent interaction            | `--accent-hover`   | Hover/active on filled primary actions                             |
| Selected surface              | `--accent-bg`      | Selected file or pressed toggle                                    |
| On accent                     | `--on-accent`      | Text on filled accent; never assume white in both themes           |
| Success                       | `--success`        | Added files/counts, reviewed and resolved state                    |
| Warning                       | `--warning`        | Modified files, recoverable notices                                |
| Review anchor                 | `--review-anchor`  | Subtle tint on lines covered by a visible review comment           |
| Destructive / error           | `--error`          | Deleted files/counts, conflicts, delete actions, errors            |

- Use one foreground hierarchy across both themes. Dark mode maps the same roles;
  it is not a separate visual identity.
- Accent must explain an action or state. Do not outline every comment in violet.
  Open comments use a neutral edge; resolved comments add success plus text.
- Reserve `--brand-bg` / `--on-brand` for the existing yellow logo, not buttons.
- `--file-code`, `--file-data`, `--file-markup`, and `--file-config` preserve small
  file-kind hints. They are a bounded categorical palette, not extra UI accents.
- Status always includes text, a sign, an icon, or a shape. Preserve staging's
  empty/half/full dot distinction; its half-fill is information, not decoration.
- Pierre owns syntax colors, changed-line fills, word highlights, line selection,
  and code-theme surfaces. App status tokens govern the surrounding UI only.

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
  Tree text uses `--leading-tree`; hit height is separate from text leading.
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
| Form groups / distinct sections        | 16 or 24                                  |
| Dialog padding                         | 24                                        |
| Large conceptual separation            | 32 or 48, rarely needed in review chrome  |

Use `--page-gutter` for header, toolbar, notices, and footer: 24 desktop, 16 below
1012px, 12 below 768px. Do not independently center those regions.
Use explicit margins instead of relying on browser-default paragraph spacing.

Custom values are acceptable only for documented optical or renderer geometry:
1px separators, 2px focus/segmented insets, small status marks, the 3px comment
edge, SVG coordinates, and the overlapping resize handle. Do not promote every
such detail into a token. New layout spacing must use the scale.

## Layout

- Use a full-width application shell, not a centered marketing container.
  The diff receives all width remaining after navigation.
- Header: `--topbar-height` (56 desktop, 48 narrow). Toolbar and sidebar tabs: `--toolbar-height`
  (44) as the desktop baseline; allow toolbar height to grow when controls wrap.
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

### Pierre boundary

- Prefer `CodeView` options and `renderHeaderPrefix`, `renderHeaderMetadata`, and
  `renderAnnotation` slots. Reuse the same comment components in slots and sidebar.
- Pass display preferences through the existing options and worker-pool theme
  integration. Preserve the user's split/unified, wrapping, and code-theme choice.
- Set supported `--diffs-font-*` / `--diffs-line-height` properties on `#viewer`.
  Keep the sizing probe, `ResizeObserver`, and `itemMetrics` synchronized.
- Pierre's header uses the measured row height plus 24px native padding. Keep
  header-slot controls compact; do not apply global touch sizing to these slots.
- Do not add outer margins, padding, or borders to virtualized `diffs-container`
  elements. Separate files with a Pierre-measured 8px gap and a subtle full-width
  divider; keep `itemMetrics` synchronized so virtual scrolling remains correct.
- Keep `unsafeCSS` small and justified. Do not reconstruct syntax styling or
  broadly target internal shadow-DOM elements to make the library look like chrome.
- Changing code/header geometry requires validating navigation, sticky headers,
  annotations, expansion, wrapping, and browser font scaling together.

## Borders, radii, and shadows

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
- Pierre's file divider and measured gap are separation, not elevation.

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
- Keep `Save comment` enabled; empty submission focuses the labeled editor and
  shows an inline error. Deleting a saved comment requires explicit confirmation.
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

- Use the controlled shadcn `Dialog` built on Base UI, with a visible accessible
  title. Keep focus inside while open, support Escape, and restore focus on close.
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
- Keep scope and desktop layout choice visible. Put wrap, collapse-all, inline
  detail, code theme, and narrow layout choice in View options.
- Mobile hides secondary repository/branch chrome and the refresh text, not its
  accessible label. Keep the current file task and theme/sidebar controls available.
- Collapse navigation to the toggle-controlled modal drawer; its width is
  `min(320px, 88vw)`. Keep file selection, comments, and export reachable there.
- Hide redundant summary/shortcut detail before removing essential actions.
- At mobile widths or coarse pointers, app-owned controls target 44px hit heights;
  icon controls also reach 44px width. Text-input sizing uses `--text-input-touch`.
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

## Audit baseline and consolidation

This audit records the starting points, not permission to reuse legacy values.

| Area             | Existing pattern / inconsistency                                                            | Consolidation                                                                  |
| ---------------- | ------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| Surfaces         | White / `#f8f9fb`; dark `#151619` / `#1b1c20`                                               | Retain neutral shell; add a semantic elevated surface                          |
| Text             | `#24252a` / `#e1e2e7`; most supporting text shared `#787c86` / `#91949e`                    | Distinguish secondary/muted; strengthen muted contrast                         |
| Borders/hover    | `#e5e7ec` / `#303137`; `#eeeef4` / `#282930`                                                | Retain separators; distinguish control boundaries                              |
| Accent/brand     | Violet `#6260df` / `#aba7ff`, pale violet fills; yellow `#facc15` logo                      | Preserve roles; add hover and on-accent counterparts                           |
| Status           | Green `#24844c` / `#70cc95`, red `#cf4b51` / `#ed8a8e`, shared gold `#b58a29`               | Semantic success/error/warning with theme-equivalent contrast                  |
| File kinds       | Four inline light/dark blue, gold, orange, purple pairs                                     | Bounded semantic file-kind tokens                                              |
| Type             | System sans/mono; 9, 10, 11, 12, 13, 14, 15, 17, 18px; 400/550/600/650/700 weights          | Five UI sizes; three weights; separate functional touch-input size             |
| Leading/tracking | Browser defaults, 18/13 tree, 22/13 code, 1.5/1.6/1.7 prose; −0.6px and 1.1px tracking      | Explicit UI/copy leading, semantic tracking; preserve measured code leading    |
| Spacing          | Repeated 3/5/6/7/9/10/11/14/15/17/18/20/22px padding, margins, gaps mixed with 4/8/12/16/24 | 4px rhythm; documented optical exceptions only                                 |
| Radii            | 3, 4, 5, 6, 7, 8, 10px plus circles                                                         | 3/6/12px roles; circles only for dots/checks                                   |
| Shadows          | Tiny button and segmented shadows; sidebar shadow; dialog backdrop                          | One overlay shadow; retain non-geometric inset diff divider                    |
| Geometry         | Header 58px, toolbar 40px; 24/28/30px icon controls and content-sized buttons               | 56/44px shell rhythm, 32px controls, 24px Pierre slots, 44px touch targets     |
| Width/gutters    | Competing 260/220/280px sidebar rules; 10/12/14/22/24px gutters; 680px dialog               | One user-owned sidebar width; shared responsive gutter and reading-width roles |
| Review           | Inline 12×14px padding versus sidebar/mobile 10px; 85px editor, 300px XML area              | Shared 12px comment inset, readable editor, viewport-bounded overlay           |
| States           | Search removed input focus; textarea/select lacked shared focus; reviewed rows faded to 55% | Group/field focus, readable reviewed state, explicit destructive action        |
| Responsive       | 1000/760px breakpoints; toolbar wrapped only below 760px                                    | 1012px drawer, 768px compact chrome, unified narrow default                    |

### Decisions needing product judgment

- Validate compact density and the retained file-kind colors with real review
  sessions before adding a density preference or changing the brand direction.
- Larger Pierre gutter/header touch targets require a renderer-aware accessibility
  pass. Do not compromise scroll geometry to claim touch-target compliance.
- Keep all existing code-theme options; judge their syntax contrast separately
  from the shell when adding or upgrading a Pierre theme.
