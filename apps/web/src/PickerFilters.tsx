import { ChevronDownIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  defaultPickerFilters,
  type PickerFilters,
  type pickerFilterOptions,
} from "./project-picker.ts";

const availabilityLabels = {
  all: "All availability",
  available: "Available",
  unavailable: "Unavailable",
};
const changesLabels = {
  all: "All changes",
  changed: "Changed",
  unchanged: "No changes",
};

export function pickerFilterSummary(filters: PickerFilters) {
  return [
    availabilityLabels[filters.availability],
    ...(filters.changes === "all" ? [] : [changesLabels[filters.changes]]),
    ...filters.hosts.map((host) => `Host: ${host}`),
    ...filters.branches.map((branch) => `Branch: ${branch}`),
    ...filters.worktrees.map((worktree) => `Worktree: ${worktree}`),
  ].join(" · ");
}

export function pickerFiltersChanged(filters: PickerFilters) {
  return (
    filters.availability !== defaultPickerFilters.availability ||
    filters.changes !== defaultPickerFilters.changes ||
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
      <DropdownMenu>
        <DropdownMenuTrigger
          aria-label="Availability"
          render={filterTrigger(availabilityLabels[filters.availability])}
        />
        <DropdownMenuContent>
          <DropdownMenuRadioGroup
            value={filters.availability}
            onValueChange={(value) => {
              if (
                value === "all" ||
                value === "available" ||
                value === "unavailable"
              )
                onChange({ ...filters, availability: value });
            }}
          >
            <DropdownMenuRadioItem closeOnClick value="all">
              All
            </DropdownMenuRadioItem>
            <DropdownMenuRadioItem closeOnClick value="available">
              Available
            </DropdownMenuRadioItem>
            <DropdownMenuRadioItem closeOnClick value="unavailable">
              Unavailable
            </DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      <DropdownMenu>
        <DropdownMenuTrigger
          aria-label="Changes"
          render={filterTrigger(changesLabels[filters.changes])}
        />
        <DropdownMenuContent>
          <DropdownMenuRadioGroup
            value={filters.changes}
            onValueChange={(value) => {
              if (
                value === "all" ||
                value === "changed" ||
                value === "unchanged"
              )
                onChange({ ...filters, changes: value });
            }}
          >
            <DropdownMenuRadioItem closeOnClick value="all">
              All
            </DropdownMenuRadioItem>
            <DropdownMenuRadioItem closeOnClick value="changed">
              Changed
            </DropdownMenuRadioItem>
            <DropdownMenuRadioItem closeOnClick value="unchanged">
              No changes
            </DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>
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
