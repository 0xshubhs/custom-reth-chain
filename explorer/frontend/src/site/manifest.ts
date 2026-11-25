/** Web app manifest (written to dist/manifest.webmanifest at build time). */
import type { WebManifest } from "./types";
import { NETWORK_NAME } from "../lib/site";

export default function manifest(): WebManifest {
  return {
    name: `${NETWORK_NAME} Explorer`,
    short_name: `${NETWORK_NAME} Explorer`,
    description: `Block explorer for the ${NETWORK_NAME} chain: blocks, transactions, tokens and verified contracts.`,
    start_url: "/",
    scope: "/",
    display: "standalone",
    orientation: "any",
    background_color: "#0a0a0a",
    theme_color: "#0a0a0a",
    categories: ["developer", "utilities", "finance"],
    icons: [
      { src: "/favicon-32.png", sizes: "32x32", type: "image/png" },
      { src: "/apple-icon.png", sizes: "180x180", type: "image/png" },
      { src: "/icon-192.png", sizes: "192x192", type: "image/png" },
      { src: "/icon-512.png", sizes: "512x512", type: "image/png" },
    ],
  };
}
