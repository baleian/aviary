FROM node:22-slim AS base
WORKDIR /app
ARG NPM_CONFIG_REGISTRY
COPY package.json package-lock.json /app/
RUN npm ci
COPY . /app/
EXPOSE 3000

# Dev target: bind-mounted source + next dev server (see docker-compose.override.yml).
FROM base AS dev
CMD ["npm", "run", "dev"]

# Prod target: a full production build. URLs come from window.location at
# runtime, so the same image works behind any edge proxy.
FROM base AS runner
ENV NODE_ENV=production
RUN npm run build
CMD ["npm", "run", "start"]
