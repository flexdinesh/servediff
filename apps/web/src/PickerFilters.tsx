import { ChevronDownIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  defaultPickerFilters,
  type PickerFilters,
  type pickerFilterOptions,
} from "./project-picker.ts";

const freshnessLabels = {
  all: "All snapshots",
  latest: "Latest snapshots",
  stale: "Stale snapshots",
};

export function pickerFilterSummary(filters: PickerFilters) {
  return [
    freshnessLabels[filters.freshness],
    filters.includeAll ? "All statuses" : "With changes",
    ...filters.hosts.map((host) => `Host: ${host}`),
    ...filters.branches.map((branch) => `Branch: ${branch}`),
    ...filters.worktrees.map((worktree) => `Worktree: ${worktree}`),
  ].join(" · ");
}

export function pickerFiltersChanged(filters: PickerFilters) {
  return (
    filters.freshness !== defaultPickerFilters.freshness ||
    filters.includeAll !== defaultPickerFilters.includeAll ||
    filters.hosts.length > 0 ||
    filters.branches.length > 0 ||
    filters.worktrees.length > 0
  );
}

function filterTrigger(label: string) {
  return (
    <Button variant="outline-muted" size="sm">
      {label}
      <ChevronDownIcon aria-hidden="true" />
    </Button>
  );
}

function MultiFilter({
  label,
  options,
  selected,
  onChange,
}: {
  label: string;
  options: string[];
  selected: string[];
  onChange: (values: string[]) => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label={label}
        render={filterTrigger(
          `${label}${selected.length ? ` (${selected.length})` : ""}`,
        )}
      />
      <DropdownMenuContent className="project-picker-filter-menu">
        {!options.length && (
          <p className="project-picker-filter-empty">
            No recorded {label.toLowerCase()}
          </p>
        )}
        {options.map((value) => (
          <DropdownMenuCheckboxItem
            key={value}
            checked={selected.includes(value)}
            closeOnClick={false}
            onCheckedChange={(checked) =>
              onChange(
                checked
                  ? [...selected, value]
                  : selected.filter((item) => item !== value),
              )
            }
          >
            {value}
          </DropdownMenuCheckboxItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function PickerFilterControls({
  filters,
  options,
  onChange,
}: {
  filters: PickerFilters;
  options: ReturnType<typeof pickerFilterOptions>;
  onChange: (filters: PickerFilters) => void;
}) {
  return (
    <div
      className="project-picker-filters"
      role="group"
      aria-label="Repository filters"
    >
      <div className="project-picker-freshness-row">
        <div className="segmented" role="group" aria-label="Snapshot freshness">
          {(
            ["all", "latest", "stale"] satisfies PickerFilters["freshness"][]
          ).map((value) => (
            <Button
              key={value}
              variant="ghost"
              size="sm"
              aria-pressed={filters.freshness === value}
              onClick={() => onChange({ ...filters, freshness: value })}
            >
              {value === "all"
                ? "All"
                : value === "latest"
                  ? "Latest"
                  : "Stale"}
            </Button>
          ))}
        </div>
        <label
          className="project-picker-all"
          title="Include unavailable snapshots and snapshots with no changes or unknown status"
        >
          <input
            type="checkbox"
            checked={filters.includeAll}
            onChange={(event) =>
              onChange({ ...filters, includeAll: event.target.checked })
            }
          />
          All
        </label>
      </div>
      <MultiFilter
        label="Host"
        options={options.hosts}
        selected={filters.hosts}
        onChange={(hosts) => onChange({ ...filters, hosts })}
      />
      <MultiFilter
        label="Branch"
        options={options.branches}
        selected={filters.branches}
        onChange={(branches) => onChange({ ...filters, branches })}
      />
      <MultiFilter
        label="Worktree"
        options={options.worktrees}
        selected={filters.worktrees}
        onChange={(worktrees) => onChange({ ...filters, worktrees })}
      />
    </div>
  );
}
