import { CheckIcon, CopyIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";

export function CopyPathButton({
  value,
  label,
}: {
  value: string;
  label: string;
}) {
  const [status, setStatus] = useState<"idle" | "copying" | "copied" | "error">(
    "idle",
  );
  const feedback =
    status === "copied" ? "Copied" : status === "error" ? "Copy failed" : "";

  useEffect(() => {
    if (status !== "copied" && status !== "error") return;
    const timeout = window.setTimeout(() => setStatus("idle"), 2_000);
    return () => window.clearTimeout(timeout);
  }, [status]);

  async function copy() {
    setStatus("copying");
    try {
      await navigator.clipboard.writeText(value);
      setStatus("copied");
    } catch {
      setStatus("error");
    }
  }

  return (
    <span className="copy-path" data-status={status}>
      <Button
        type="button"
        variant="ghost"
        size="icon-xs"
        aria-label={`${label}: ${value}`}
        title={feedback || label}
        aria-busy={status === "copying"}
        disabled={status === "copying"}
        onClick={copy}
      >
        {status === "copied" ? (
          <CheckIcon aria-hidden="true" />
        ) : (
          <CopyIcon aria-hidden="true" />
        )}
      </Button>
      <span className="copy-path-feedback" role="status">
        {feedback}
      </span>
    </span>
  );
}
