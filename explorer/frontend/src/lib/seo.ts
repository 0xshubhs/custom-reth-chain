/**
 * Builds the `head` payload TanStack Router expects from a flat description of
 * a page. The router merges head() per route, later routes winning on the same
 * name/property, so the root sets the defaults and a page overrides what
 * differs. Same helper as the faucet's.
 */
import { TITLE_SUFFIX } from "./site";

export type MetaTag = { title: string } | { name: string; content: string } | { property: string; content: string };

export interface LinkTag {
  rel: string;
  href: string;
  [attr: string]: string;
}

export interface SeoInput {
  title?: string;
  description?: string;
  canonical?: string;
  image?: string;
  imageAlt?: string;
  noindex?: boolean;
  siteName?: string;
}

export function seo({ title, description, canonical, image, imageAlt, noindex, siteName }: SeoInput) {
  const meta: MetaTag[] = [];
  const links: LinkTag[] = [];
  if (title) {
    meta.push({ title });
    meta.push({ property: "og:title", content: title });
    meta.push({ name: "twitter:title", content: title });
  }
  if (description) {
    meta.push({ name: "description", content: description });
    meta.push({ property: "og:description", content: description });
    meta.push({ name: "twitter:description", content: description });
  }
  meta.push({ name: "robots", content: noindex ? "noindex, nofollow" : "index, follow" });
  meta.push({ property: "og:type", content: "website" });
  if (siteName) meta.push({ property: "og:site_name", content: siteName });
  if (canonical) {
    meta.push({ property: "og:url", content: canonical });
    links.push({ rel: "canonical", href: canonical });
  }
  if (image) {
    meta.push({ property: "og:image", content: image });
    meta.push({ name: "twitter:image", content: image });
    meta.push({ name: "twitter:card", content: "summary_large_image" });
    if (imageAlt) meta.push({ property: "og:image:alt", content: imageAlt });
  }
  return { meta, links };
}

/** Resolves a root-relative path against the site origin. */
export function absolute(origin: string, pathOrUrl: string): string {
  return /^https?:\/\//.test(pathOrUrl) ? pathOrUrl : `${origin}${pathOrUrl}`;
}

/** A page title with the site suffix, for a route's head(). */
export function pageHead(title: string, description?: string) {
  return { meta: seo({ title: `${title}${TITLE_SUFFIX}`, description }).meta };
}
