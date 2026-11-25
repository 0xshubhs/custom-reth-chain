/**
 * SPA entry: builds the router from the file-route tree
 * (src/routeTree.gen.ts, written by @tanstack/router-plugin) and mounts React.
 */
import { createRoot } from "react-dom/client";
import { createRouter, RouterProvider } from "@tanstack/react-router";

import { routeTree } from "./routeTree.gen";
import { initThemeMeta } from "./lib/theme";
import ErrorScreen from "./components/ErrorScreen";
import NotFound from "./components/NotFound";

const router = createRouter({
  routeTree,
  defaultPreload: false,
  scrollRestoration: true,
  defaultErrorComponent: ErrorScreen,
  defaultNotFoundComponent: NotFound,
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

initThemeMeta();

const container = document.getElementById("root");
if (!container) throw new Error("#root not found in index.html");

createRoot(container).render(<RouterProvider router={router} />);
