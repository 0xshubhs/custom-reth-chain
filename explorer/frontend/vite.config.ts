/**
 * Vite config: Meowchain block explorer (React SPA, localhost).
 *
 * Same house setup as the faucet and the user app:
 *   - file routes: @tanstack/router-plugin scans src/routes/ and writes
 *     src/routeTree.gen.ts, which types every Route.useParams()/useSearch();
 *   - Tailwind 4 through its Vite plugin;
 *   - `/api/*` goes to the explorer engine: server.proxy here in dev, nginx in
 *     production (deploy/nginx.conf and docker/web-nginx.conf);
 *   - security headers and the CSP live only in nginx.
 *
 * The live feed is an EventSource on /api/v2/stream. The dev proxy passes it
 * through unbuffered (Vite's proxy streams by default); nginx needs its own
 * unbuffered location for it, see deploy/nginx.conf.
 */
import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const SRC = path.resolve(__dirname, "src");

/**
 * Ports (the same as compose.yaml at the repo root):
 *   WEB_PORT      `vite preview`: 3000, the explorer web port
 *   WEB_DEV_PORT  `vite dev`: 3001, on its own port so a dev server never
 *                 lands on the port a production build is serving
 * Both can be overridden from the environment.
 */
const PORT = Number(process.env.WEB_PORT ?? 3000);
const DEV_PORT = Number(process.env.WEB_DEV_PORT ?? 3001);

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, __dirname, "VITE_");

  /** The explorer engine (API role), 127.0.0.1:4000 from compose.yaml. */
  const engineUrl = env.VITE_BASE_URL || "http://localhost:4000";

  return {
    envDir: __dirname,
    envPrefix: "VITE_",
    publicDir: path.resolve(__dirname, "public"),

    plugins: [
      // Must precede the react plugin: it generates the route tree that
      // main.tsx imports.
      tanstackRouter({ target: "react", autoCodeSplitting: true }),
      react(),
      tailwindcss(),
    ],

    resolve: {
      alias: { "@": SRC },
    },

    /**
     * `allowedHosts: true`: the preview server sits behind nginx, which
     * forwards the real Host (localhost). preview reuses
     * server.proxy when preview.proxy is unset, so /api works here too.
     */
    preview: {
      port: PORT,
      strictPort: true,
      host: true,
      allowedHosts: true,
    },

    server: {
      port: DEV_PORT,
      strictPort: true,
      proxy: {
        "/api": {
          target: engineUrl,
          changeOrigin: true,
        },
      },
    },

    build: {
      outDir: "dist",
      rollupOptions: {
        /**
         * viem's `ox` dependency places PURE annotations where Rollup cannot
         * attach them. Harmless and not ours; silenced so real warnings stay
         * visible.
         */
        onwarn(warning, warn) {
          if (warning.code === "INVALID_ANNOTATION" && warning.message.includes("#__PURE__")) return;
          warn(warning);
        },
        output: {
          // viem is deliberately not named here: left alone, Rollup keeps it
          // in the lazily loaded contract-tab chunk, so pages that never open
          // that tab never download it. Naming it made the entry preload it.
          manualChunks: {
            router: ["@tanstack/react-router", "@tanstack/react-query"],
          },
        },
      },
    },

    optimizeDeps: {
      entries: [path.resolve(__dirname, "index.html")],
    },
  };
});
