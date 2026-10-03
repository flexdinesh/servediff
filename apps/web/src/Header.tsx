import {
  MonitorIcon,
  MoonIcon,
  PanelLeftCloseIcon,
  PanelLeftOpenIcon,
  RefreshCwIcon,
  SunIcon,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Separator } from "@/components/ui/separator";
import { useDiffSource } from "./app-state.tsx";
import { useAppearance } from "./appearance-context.tsx";
import { useSidebarState } from "./sidebar-context.tsx";
import {
  ContextSwitcher,
  capabilityEnabled,
  useSession,
} from "./session-context.tsx";
import { readThemePreference, type ThemePreference } from "./theme.ts";

function themeLabel(theme: ThemePreference) {
  if (theme === "light") return "Light";
  if (theme === "dark") return "Dark";
  return "System";
}

export function Header() {
  const { repository, piped, diff } = useDiffSource();
  const { capabilities } = useSession();
  const { themePreference, setThemePreference } = useAppearance();
  const sidebar = useSidebarState();
  const refreshEnabled = capabilityEnabled(capabilities.diff.refresh);
  return (
    <header className="topbar">
      <Button
        type="button"
        id="sidebar-toggle"
        variant="ghost"
        size="icon"
        aria-controls="sidebar"
        aria-expanded={sidebar.expanded}
        aria-label={`${sidebar.expanded ? "Hide" : "Show"} file sidebar`}
        title={`${sidebar.expanded ? "Hide" : "Show"} file sidebar`}
        onClick={sidebar.toggle}
      >
        {sidebar.expanded ? (
          <PanelLeftCloseIcon className="size-(--icon-lg)" />
        ) : (
          <PanelLeftOpenIcon className="size-(--icon-lg)" />
        )}
      </Button>
      <a className="brand" href="/" aria-label="servediff home">
        servediff
      </a>
      <Separator className="header-divider" orientation="vertical" />
      <ContextSwitcher />
      <div className="header-heading">
        <div className="header-title">
          <h1 id="changes-title">{piped ? "Piped diff" : "Local changes"}</h1>
        </div>
        <p
          id="repo-path"
          title={piped ? "Re-run your command to update" : repository?.root}
        >
          {piped
            ? "From stdin · Git unchanged"
            : (repository?.root ?? "Reading your repository…")}
        </p>
      </div>
      <Button
        type="button"
        id="refresh"
        variant="outline"
        aria-label="Refresh changes"
        title="Refresh changes (Alt+R)"
        hidden={!refreshEnabled}
        aria-busy={diff.busy}
        onClick={diff.refresh}
      >
        <RefreshCwIcon className="size-(--icon-base)" aria-hidden="true" />
        <span className="refresh-label">Refresh</span>
      </Button>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              type="button"
              id="theme"
              variant="ghost"
              size="icon"
              aria-label={`Theme: ${themeLabel(themePreference)}`}
              title={`Theme: ${themeLabel(themePreference)}`}
            />
          }
        >
          {themePreference === "system" ? (
            <MonitorIcon className="size-(--icon-lg)" aria-hidden="true" />
          ) : themePreference === "dark" ? (
            <MoonIcon className="size-(--icon-lg)" aria-hidden="true" />
          ) : (
            <SunIcon className="size-(--icon-lg)" aria-hidden="true" />
          )}
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" aria-label="Theme">
          <DropdownMenuRadioGroup
            value={themePreference}
            onValueChange={(value: unknown) =>
              setThemePreference(readThemePreference(value))
            }
          >
            <DropdownMenuRadioItem value="light" closeOnClick>
              <SunIcon aria-hidden="true" /> Light
            </DropdownMenuRadioItem>
            <DropdownMenuRadioItem value="dark" closeOnClick>
              <MoonIcon aria-hidden="true" /> Dark
            </DropdownMenuRadioItem>
            <DropdownMenuRadioItem value="system" closeOnClick>
              <MonitorIcon aria-hidden="true" /> System
            </DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>
    </header>
  );
}
