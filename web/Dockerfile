# syntax=docker/dockerfile:1

# --- build stage ---------------------------------------------------------------
# Debian-based (glibc) so esbuild's native binary installs cleanly.
FROM node:22-slim AS build
WORKDIR /app

RUN corepack enable

COPY package.json pnpm-lock.yaml* pnpm-workspace.yaml* package-lock.json* ./
RUN if [ -f pnpm-lock.yaml ]; then pnpm install --frozen-lockfile; \
    else npm install; fi

COPY . .
RUN npm run build

# --- runtime stage -------------------------------------------------------------
FROM nginx:1.27-alpine

ENV CHRONOS_API_UPSTREAM=http://server:8080

COPY --from=build /app/dist /usr/share/nginx/html
COPY nginx.conf.template /etc/nginx/templates/default.conf.template

EXPOSE 80
