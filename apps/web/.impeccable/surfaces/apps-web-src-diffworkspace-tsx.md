---
version: 1
slug: "apps-web-src-diffworkspace-tsx"
primary_target: "apps/web/src/DiffWorkspace.tsx"
related_targets: []
---

# Quiet frames diff workspace

Mode: Operate. Developers review local changes, navigate files, and leave feedback.
Scope: right diff canvas and toolbar surface; retain shell geometry, syntax themes, and review behavior.
Chosen direction: user-selected Quiet frames from the approved interactive preview.
Constraints: Pierre owns virtualization, sticky headers, wrapping, selection, and annotations.

## Direction contract

THESIS: Each file reads as a separate review object on a faint dotted canvas.

OWN-WORLD: Existing neutral/violet system; white or deep charcoal canvas with faint dots, panel-colored toolbar and file headers, opaque code surfaces, thin neutral outline frame, small corners, no elevation shadow.

STORY: Navigate a file, read its diff, toggle Reviewed, and leave line or whole-file feedback. File-comment and toolbar icon actions carry descriptive hover titles; collapse/expand all lives next to View options. Compact controls share muted border tokens.

FIRST VIEWPORT: Retained top bar, file sidebar, and display controls; toolbar surface matches the editor chrome. Remaining width shows file diffs with 12px gutters and measured 12px gaps. Dots stay behind the code surfaces.

FORM: User-selected Quiet frames; seed key user-selected/quiet-frames. Frame changes add no item height. Browser font scaling updates renderer metrics and spacing together.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

Unresolved decisions: none.
