import { Popover } from "@base-ui/react/popover";
import { InfoIcon, XIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useDiffSource } from "./app-state.tsx";
import { diffComparison } from "./diff-comparison.ts";
import { useSession } from "./session-context.tsx";

export function DiffComparison() {
  const session = useSession();
  const { mode, repository } = useDiffSource();
  const comparison = diffComparison(session, mode, repository?.head);
  return (
    <Popover.Root key={mode}>
      <Popover.Trigger
        id="scope-description"
        aria-label={`Diff comparison: ${comparison.summary}`}
        render={
          <Button
            variant="ghost"
            size="xs"
            className="diff-comparison-trigger"
          />
        }
      >
        <span className="diff-comparison-summary">{comparison.summary}</span>
        <span className="diff-comparison-mobile-label">Comparison</span>
        <InfoIcon aria-hidden="true" />
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Positioner
          side="top"
          align="start"
          sideOffset={8}
          collisionPadding={16}
          className="z-50"
        >
          <Popover.Popup className="diff-comparison-popup">
            <div className="diff-comparison-heading">
              <Popover.Title>Diff comparison</Popover.Title>
              <Popover.Close
                render={
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    aria-label="Close diff comparison"
                  />
                }
              >
                <XIcon aria-hidden="true" />
              </Popover.Close>
            </div>
            <Popover.Description>{comparison.description}</Popover.Description>
            {comparison.details.length > 0 && (
              <dl>
                {comparison.details.map(({ label, value }) => (
                  <div key={label}>
                    <dt>{label}</dt>
                    <dd>{value}</dd>
                  </div>
                ))}
              </dl>
            )}
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}
