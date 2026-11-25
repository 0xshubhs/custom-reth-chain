/** Route error boundary: a page that threw while rendering lands here. */
import { useEffect } from "react";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, RotateCw } from "lucide-react";

export default function ErrorScreen({ error, reset }: { error: unknown; reset: () => void }) {
  useEffect(() => {
    console.error(error);
  }, [error]);

  return (
    <div className="mx-auto flex max-w-lg flex-col items-center py-16 text-center">
      <span className="mb-5 flex h-14 w-14 items-center justify-center rounded-2xl border border-bad/30 bg-bad/10 text-bad">
        <AlertTriangle className="h-7 w-7" />
      </span>
      <h1 className="text-2xl font-bold text-bright">Something went wrong on this page</h1>
      <p className="mt-2 text-sm text-muted">
        {(error instanceof Error ? error.message : "") || "An unexpected error stopped this page from rendering."}
      </p>
      <div className="mt-6 flex flex-wrap justify-center gap-2">
        <button type="button" className="btn btn-primary" onClick={() => reset()}>
          <RotateCw className="h-4 w-4" /> Try again
        </button>
        <Link to="/" className="btn">
          Explorer home
        </Link>
      </div>
    </div>
  );
}
