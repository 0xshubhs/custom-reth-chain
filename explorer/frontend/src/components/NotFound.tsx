/** 404: no route matches. Rendered inside the site layout, header search included. */
import { Link } from "@tanstack/react-router";
import { Compass } from "lucide-react";

import { SearchBar } from "./SearchBar";

export default function NotFound() {
  return (
    <div className="mx-auto flex max-w-xl flex-col items-center py-14 text-center sm:py-20">
      <span className="mb-5 flex h-14 w-14 items-center justify-center rounded-2xl border border-line bg-raised text-gold">
        <Compass className="h-7 w-7" />
      </span>
      <p className="font-mono text-sm font-semibold text-link">404</p>
      <h1 className="mt-1 text-2xl font-bold text-bright">This page does not exist</h1>
      <p className="mt-2 text-sm text-muted">
        The link may be mistyped, or it points to something this explorer does not have a page for. Search for a block,
        transaction, address or token instead.
      </p>
      <div className="mt-6 w-full">
        <SearchBar size="lg" />
      </div>
      <Link to="/" className="btn mt-6">
        Back to the explorer home
      </Link>
    </div>
  );
}
