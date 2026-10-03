---
version: 1
slug: "apps-web-src-diffworkspace-tsx"
primary_target: "apps/web/src/DiffWorkspace.tsx"
related_targets: []
---

# Quiet frames diff workspace

Mode: Operate. Developers review local changes, navigate files, and leave feedback.
Scope: right diff canvas only; retain shell, toolbar, syntax themes, and review behavior.
Chosen direction: user-selected Quiet frames from the approved interactive preview.
Constraints: Pierre owns virtualization, sticky headers, wrapping, selection, and annotations.

## Direction contract

THESIS: Each file reads as a separate review object on a faint dotted canvas.

OWN-WORLD: Existing neutral/violet system; light gray or charcoal canvas, opaque code surfaces, thin neutral inset frame, small corners, no elevation shadow.

STORY: Navigate a file, read its diff, mark reviewed, and leave anchored feedback with existing controls.

FIRST VIEWPORT: Unchanged top bar, file sidebar, and display toolbar; remaining width shows file diffs with 16px gutters and measured 16px gaps. Dots stay behind the code surfaces.

FORM: User-selected Quiet frames; seed key user-selected/quiet-frames. Frame changes add no item height. Browser font scaling updates renderer metrics and spacing together.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

Unresolved decisions: none.
