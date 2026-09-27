import { useReviewState, useReviewedFilesState } from "./app-state.tsx";
import { Button } from "@/components/ui/button";

export function WorkspaceFeedback() {
  const { feedback } = useReviewState();
  const { error, retry } = useReviewedFilesState();
  return (
    <div className="workspace-feedback" hidden={!feedback && !error}>
      <p id="comment-feedback" role="status" aria-live="polite">
        {feedback}
      </p>
      {error && (
        <div className="reviewed-error" role="alert">
          <p>{error}</p>
          <Button type="button" variant="outline" size="sm" onClick={retry}>
            Retry reviewed files
          </Button>
        </div>
      )}
    </div>
  );
}
