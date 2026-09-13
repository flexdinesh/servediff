import { isDiffMode } from "@servediff/shared";
import { Settings2Icon } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useAppState } from "./app-state.tsx";
import {
  DIFF_THEMES,
  LINE_DIFF_TYPES,
  readDiffTheme,
  readLineDiffType,
} from "./display-options.ts";

export function DiffToolbar() {
  const {
    source: { mode, piped, changeMode },
    display: {
      layout,
      setLayout,
      wrap,
      setWrap,
      diffTheme,
      setDiffTheme,
      lineDiffType,
      setLineDiffType,
      collapsed,
      setCollapsed,
    },
    navigation: { files },
  } = useAppState();
  const allCollapsed =
    files.length > 0 && files.every((file) => collapsed.has(file.path));
  return (
    <div className="toolbar">
      <ToggleGroup
        id="diff-scope"
        className="segmented modes"
        aria-label="Diff scope"
        hidden={piped}
        spacing={0}
        variant="default"
        size="sm"
        value={[mode]}
        onValueChange={(values) => {
          const value = values[0];
          if (value && isDiffMode(value)) changeMode(value);
        }}
      >
        {["all", "staged", "unstaged"].map((value) => (
          <ToggleGroupItem key={value} value={value} data-mode={value}>
            {value === "all"
              ? "All changes"
              : value === "staged"
                ? "Staged"
                : "Unstaged"}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
      <div className="toolbar-spacer" />
      <ToggleGroup
        className="segmented layout-control"
        aria-label="Diff layout"
        spacing={0}
        variant="default"
        size="sm"
        value={[layout]}
        onValueChange={(values) => {
          const value = values[0];
          if (value === "split" || value === "unified") setLayout(value);
        }}
      >
        <ToggleGroupItem value="split" data-layout="split">
          Split
        </ToggleGroupItem>
        <ToggleGroupItem value="unified" data-layout="unified">
          Unified
        </ToggleGroupItem>
      </ToggleGroup>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              type="button"
              id="view-options"
              variant="outline"
              aria-label="View options"
            />
          }
        >
          <Settings2Icon aria-hidden="true" />
          <span className="view-options-label">View options</span>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          className="view-options-menu"
          aria-label="View options"
        >
          <DropdownMenuCheckboxItem checked={wrap} onCheckedChange={setWrap}>
            Wrap lines
          </DropdownMenuCheckboxItem>
          <DropdownMenuItem
            onClick={() =>
              setCollapsed(
                allCollapsed
                  ? new Set()
                  : new Set(files.map((file) => file.path)),
              )
            }
          >
            {allCollapsed ? "Expand all files" : "Collapse all files"}
          </DropdownMenuItem>
          <div className="dropdown-menu-label">Layout</div>
          <DropdownMenuRadioGroup
            aria-label="Diff layout"
            value={layout}
            onValueChange={(value) => {
              if (value === "split" || value === "unified") setLayout(value);
            }}
          >
            <DropdownMenuRadioItem value="split">Split</DropdownMenuRadioItem>
            <DropdownMenuRadioItem value="unified">
              Unified
            </DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
          <div className="dropdown-menu-label">Inline changes</div>
          <DropdownMenuRadioGroup
            aria-label="Inline change detail"
            value={lineDiffType}
            onValueChange={(value) => setLineDiffType(readLineDiffType(value))}
          >
            {LINE_DIFF_TYPES.map((option) => (
              <DropdownMenuRadioItem key={option.value} value={option.value}>
                {option.label}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
          <div className="dropdown-menu-label">Code theme</div>
          <DropdownMenuRadioGroup
            aria-label="Code theme"
            value={diffTheme}
            onValueChange={(value) => setDiffTheme(readDiffTheme(value))}
          >
            {DIFF_THEMES.map((option) => (
              <DropdownMenuRadioItem key={option.value} value={option.value}>
                {option.label}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}
