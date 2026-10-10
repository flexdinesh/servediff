import { isDiffMode } from "@diffx/shared";
import {
  ChevronsDownUpIcon,
  ChevronsUpDownIcon,
  Settings2Icon,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useDiffSource, useDiffCollapse, useNavigation } from "./app-state.tsx";
import { useAppearance } from "./appearance-context.tsx";
import {
  DIFF_THEMES,
  LINE_DIFF_TYPES,
  readDiffTheme,
  readLineDiffType,
} from "./display-options.ts";
import { capabilityEnabled, useSession } from "./session-context.tsx";

export function DiffToolbar() {
  const { mode, changeMode } = useDiffSource();
  const { capabilities } = useSession();
  const {
    layout,
    setLayout,
    wrap,
    setWrap,
    diffTheme,
    setDiffTheme,
    lineDiffType,
    setLineDiffType,
  } = useAppearance();
  const { collapsed, setCollapsed } = useDiffCollapse();
  const { files } = useNavigation();
  const scopes = capabilities.diff.scopes;
  const allCollapsed =
    files.length > 0 && files.every((file) => collapsed.has(file.path));
  return (
    <div className="toolbar">
      <ToggleGroup
        id="diff-scope"
        className="segmented modes"
        aria-label="Diff scope"
        hidden={!capabilityEnabled(scopes) || scopes.values.length < 2}
        spacing={0}
        variant="default"
        size="sm"
        value={[mode]}
        onValueChange={(values) => {
          const value = values[0];
          if (value && isDiffMode(value)) changeMode(value);
        }}
      >
        {scopes.values.map((value) => (
          <ToggleGroupItem
            key={value}
            value={value}
            data-mode={value}
            aria-label={value === "all" ? "All changes" : undefined}
          >
            {value === "all" ? (
              <>
                All<span className="scope-label-detail"> changes</span>
              </>
            ) : value === "staged" ? (
              "Staged"
            ) : (
              "Unstaged"
            )}
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
              variant="ghost"
              size="icon-sm"
              aria-label="View options"
              title="View options"
            />
          }
        >
          <Settings2Icon aria-hidden="true" />
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          className="view-options-menu"
          aria-label="View options"
        >
          <DropdownMenuCheckboxItem checked={wrap} onCheckedChange={setWrap}>
            Wrap lines
          </DropdownMenuCheckboxItem>
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
      <Button
        type="button"
        id="toggle-all-files"
        variant="ghost"
        size="icon-sm"
        disabled={files.length === 0}
        aria-label={allCollapsed ? "Expand all files" : "Collapse all files"}
        title={allCollapsed ? "Expand all files" : "Collapse all files"}
        onClick={() =>
          setCollapsed(
            allCollapsed ? new Set() : new Set(files.map((file) => file.path)),
          )
        }
      >
        {allCollapsed ? (
          <ChevronsUpDownIcon aria-hidden="true" />
        ) : (
          <ChevronsDownUpIcon aria-hidden="true" />
        )}
      </Button>
    </div>
  );
}
