/**
 * Root route: the site layout every page renders inside (header with search,
 * footer, toasts) plus the providers (react-query, network config).
 */
import { createRootRoute, HeadContent, Outlet } from "@tanstack/react-router";
import { QueryClientProvider } from "@tanstack/react-query";
import { Toaster } from "sonner";

import { Header } from "@/components/Header";
import { Footer } from "@/components/Footer";
import NotFound from "@/components/NotFound";
import { queryClient } from "@/lib/api";
import { ChainProvider } from "@/lib/chain";
import { seo, absolute } from "@/lib/seo";
import { NETWORK_NAME, SITE_NAME, SITE_URL } from "@/lib/site";
import { useTheme } from "@/lib/theme";
import { useSlashToSearch } from "@/components/SearchBar";

import "@fontsource-variable/inter";
import "@/styles/fonts.css";
// app.css pulls globals.css in through a CSS @import and adds the @source
// Tailwind needs. Importing globals.css directly would emit no utilities.
import "@/styles/app.css";

export const Route = createRootRoute({
  head: () => {
    const base = seo({
      title: `${NETWORK_NAME} Explorer: blocks, transactions, tokens and contracts`,
      description:
        `Block explorer for the ${NETWORK_NAME} chain: search blocks, transactions, addresses and tokens, read verified contract source, and verify your own contracts.`,
      canonical: SITE_URL,
      image: absolute(SITE_URL, "/opengraph-image.png"),
      imageAlt: `${NETWORK_NAME} Explorer`,
      siteName: SITE_NAME,
    });
    return {
      meta: base.meta,
      links: [...base.links, { rel: "manifest", href: "/manifest.webmanifest" }],
    };
  },
  component: RootLayout,
  notFoundComponent: NotFound,
});

function RootLayout() {
  const { isDark } = useTheme();
  useSlashToSearch();
  return (
    <QueryClientProvider client={queryClient}>
      <ChainProvider>
        <HeadContent />
        <a
          href="#main"
          className="sr-only focus:not-sr-only focus:fixed focus:left-4 focus:top-4 focus:z-50 focus:rounded-lg focus:bg-gold focus:px-3 focus:py-2 focus:text-on-gold"
        >
          Skip to content
        </a>
        <div className="flex min-h-screen flex-col">
          <Header />
          <main id="main" className="mx-auto w-full min-w-0 max-w-[1400px] flex-1 px-4 py-5 sm:px-6 sm:py-6">
            <Outlet />
          </main>
          <Footer />
        </div>
        <Toaster
          theme={isDark ? "dark" : "light"}
          position="bottom-right"
          closeButton
          toastOptions={{
            style: {
              background: "var(--card)",
              border: "1px solid var(--border)",
              color: "var(--text-primary)",
            },
          }}
        />
      </ChainProvider>
    </QueryClientProvider>
  );
}
