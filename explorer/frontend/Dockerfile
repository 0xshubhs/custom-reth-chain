# syntax=docker/dockerfile:1.7
# Explorer website: the built single-page app served by unprivileged nginx,
# which also proxies /api/ to the explorer API. Build context: explorer/frontend.
#
# VITE_* values are baked into the bundle at build time.

FROM oven/bun:1.3.10-alpine AS build
WORKDIR /src
COPY package.json bun.lock ./
ENV BUN_INSTALL_CACHE_DIR=/bun-cache
RUN --mount=type=cache,target=/bun-cache,sharing=locked bun install --frozen-lockfile
COPY . .
ARG VITE_SITE_URL=
ARG VITE_CHAIN_ID=
ARG VITE_CHAIN_RPC=
ARG VITE_NETWORK_NAME=
ARG VITE_NATIVE_SYMBOL=
RUN printf '%s\n' \
      "VITE_SITE_URL=${VITE_SITE_URL}" \
      "VITE_CHAIN_ID=${VITE_CHAIN_ID}" \
      "VITE_CHAIN_RPC=${VITE_CHAIN_RPC}" \
      "VITE_NETWORK_NAME=${VITE_NETWORK_NAME}" \
      "VITE_NATIVE_SYMBOL=${VITE_NATIVE_SYMBOL}" \
    > .env.production.local
ENV NODE_ENV=production NODE_OPTIONS=--max-old-space-size=4096
# `bun run build` = typecheck + vite build + static meta + CSP hashing; a type
# error fails the image.
RUN bun run build && test -f dist/index.html
# Source maps stay out of the served files.
RUN find dist -name '*.map' -type f -delete
# The page's Content-Security-Policy comes from deploy/nginx.conf's `set $csp`
# lines (the reference host config), joined into one header.
ARG CSP_MODE=enforce
RUN csp="$(grep -oE 'set \$csp "[^"]*"' deploy/nginx.conf \
          | sed -E 's/^set \$csp "//; s/^\$csp//; s/"$//' | tr -d '\n' \
          | sed -E 's/; *frame-ancestors [^;]*//')" \
 && test -n "$csp" \
 && fa="frame-ancestors 'none'" \
 && if [ "$CSP_MODE" = report ]; then \
      printf 'add_header Content-Security-Policy-Report-Only "%s; report-uri /csp-report" always;\n' "$csp"; \
      printf 'add_header Content-Security-Policy "%s" always;\n' "$fa"; \
    else \
      printf 'add_header Content-Security-Policy "%s; %s; report-uri /csp-report" always;\n' "$csp" "$fa"; \
    fi > /src/csp.conf

FROM nginxinc/nginx-unprivileged:1.27-alpine
USER root
RUN rm -f /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html
COPY deploy/container.conf /etc/nginx/conf.d/app.conf
COPY --from=build /src/csp.conf /etc/nginx/csp.conf
# Where /api/ goes: the explorer API service.
ARG API_UPSTREAM=explorer-api:8080
RUN sed -i "s|__ENGINE_UPSTREAM__|${API_UPSTREAM}|g" /etc/nginx/conf.d/app.conf \
 && grep -q "${API_UPSTREAM}" /etc/nginx/conf.d/app.conf \
 && chown -R 101:101 /usr/share/nginx/html
USER 101
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null 2>&1 || exit 1
CMD ["nginx", "-g", "daemon off;"]
