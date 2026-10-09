import { useRef, useState } from "react";
import { Trash2Icon } from "lucide-react";
import { errorDetail } from "@servediff/api";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { useDeleteContext, useSession } from "./session-context.tsx";
import { contextDiagnostics } from "./project-picker.ts";

export function DeleteContextButton() {
  const session = useSession();
  const deleteContext = useDeleteContext();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const pending = useRef(false);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const label = "Delete snapshot";

  async function confirm() {
    if (pending.current) return;
    pending.current = true;
    setBusy(true);
    setError("");
    try {
      await deleteContext(session.id);
      setOpen(false);
    } catch (error: unknown) {
      setError(errorDetail(error, "Unable to delete. Try again."));
    } finally {
      pending.current = false;
      setBusy(false);
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(value) => {
        if (pending.current) return;
        setError("");
        setOpen(value);
      }}
    >
      <DialogTrigger
        render={
          <Button
            type="button"
            variant="ghost"
            size="icon"
            aria-label={label}
            title={label}
          />
        }
      >
        <Trash2Icon className="size-(--icon-base)" aria-hidden="true" />
      </DialogTrigger>
      <DialogContent
        className="delete-dialog"
        showCloseButton={false}
        initialFocus={() => cancelRef.current}
      >
        <DialogTitle>{label}?</DialogTitle>
        <DialogDescription>
          This permanently deletes this snapshot, including comments and
          reviewed-file marks. This cannot be undone. Your Git files stay
          unchanged. New collections can appear again.
        </DialogDescription>
        <p
          className="delete-context-details"
          title={contextDiagnostics(session)}
        >
          <strong>{session.name}</strong>
          {session.branch && <span>{session.branch}</span>}
          {session.observation ? (
            <span>
              {session.observation.hostname} ·{" "}
              {new Date(session.observation.collectedAt).toLocaleString()}
            </span>
          ) : session.root ? (
            <span>{session.root}</span>
          ) : null}
        </p>
        {error && (
          <p role="alert" className="text-destructive">
            {error}
          </p>
        )}
        <DialogFooter>
          <Button
            ref={cancelRef}
            type="button"
            variant="outline"
            disabled={busy}
            onClick={() => setOpen(false)}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            className="delete-confirm"
            disabled={busy}
            aria-busy={busy}
            onClick={() => void confirm()}
          >
            <Trash2Icon aria-hidden="true" />
            {busy ? "Deleting…" : label}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
