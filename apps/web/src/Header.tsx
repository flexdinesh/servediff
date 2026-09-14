import {
  GitBranchIcon,
  MonitorIcon,
  MoonIcon,
  PanelLeftCloseIcon,
  PanelLeftOpenIcon,
  RefreshCwIcon,
  SquareTerminalIcon,
  SunIcon,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Separator } from "@/components/ui/separator";
import { useAppState } from "./app-state.tsx";
import { readThemePreference, type ThemePreference } from "./theme.ts";

function themeLabel(theme: ThemePreference) {
  if (theme === "light") return "Light";
  if (theme === "dark") return "Dark";
  return "System";
}

export function Header() {
  const {
    source: { repository, piped, diff },
    display: { themePreference, setThemePreference },
    sidebar,
  } = useAppState();
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
      <div className="header-heading">
        <div className="header-title">
          <h1 id="changes-title">
            {piped ? "Piped diff" : (repository?.name ?? "Local changes")}
          </h1>
          {!piped && <span id="repo-name">Local changes</span>}
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
      <Badge variant="secondary" className="branch-badge">
        {piped ? (
          <SquareTerminalIcon className="size-(--icon-base)!" />
        ) : (
          <GitBranchIcon className="size-(--icon-base)!" />
        )}
        <span id="branch">
          {piped ? "Fixed snapshot" : (repository?.branch ?? "—")}
        </span>
      </Badge>
      <Button
        type="button"
        id="refresh"
        variant="outline"
        aria-label="Refresh changes"
        title="Refresh changes (Alt+R)"
        hidden={piped}
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
