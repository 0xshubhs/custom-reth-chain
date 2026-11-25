/**
 * Types for the generated site files (robots.txt, the web manifest, the
 * sitemap).
 *
 * These used to come from Next's `MetadataRoute` namespace. They are plain
 * data shapes (nothing about them was framework-specific), so they are
 * declared here against the actual specs instead:
 *   - robots.txt   https://www.rfc-editor.org/rfc/rfc9309.html
 *   - manifest     https://www.w3.org/TR/appmanifest/
 *   - sitemap      https://www.sitemaps.org/protocol.html
 *
 * scripts/generate-static-meta.mjs imports the modules that use these and
 * serialises whatever they return into dist/ at build time.
 */

export interface RobotsRule {
  userAgent?: string | string[];
  allow?: string | string[];
  disallow?: string | string[];
  crawlDelay?: number;
}

export interface RobotsFile {
  rules: RobotsRule | RobotsRule[];
  sitemap?: string | string[];
  host?: string;
}

export interface ManifestIcon {
  src: string;
  sizes?: string;
  type?: string;
  purpose?: "any" | "maskable" | "monochrome";
}

export interface WebManifest {
  name: string;
  short_name?: string;
  description?: string;
  start_url?: string;
  scope?: string;
  display?: "fullscreen" | "standalone" | "minimal-ui" | "browser";
  orientation?: "any" | "natural" | "portrait" | "landscape";
  background_color?: string;
  theme_color?: string;
  categories?: string[];
  icons?: ManifestIcon[];
}

export interface SitemapEntry {
  url: string;
  lastModified?: string | Date;
  changeFrequency?: "always" | "hourly" | "daily" | "weekly" | "monthly" | "yearly" | "never";
  priority?: number;
}

export type Sitemap = SitemapEntry[];
