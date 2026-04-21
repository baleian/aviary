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

# Prod target: bake a full production build. NEXT_PUBLIC_* are frozen into the
# client bundle at build time; INTERNAL_API_URL is baked into
# routes-manifest.json by next.config.ts's rewrites(), so it must also be
# supplied at build time.
FROM base AS runner
ARG NEXT_PUBLIC_API_URL=http://localhost:8000
ARG NEXT_PUBLIC_WS_URL=ws://localhost:8000
ARG NEXT_PUBLIC_OIDC_ISSUER=http://localhost:8080/realms/aviary
ARG NEXT_PUBLIC_OIDC_CLIENT_ID=aviary-web
ARG INTERNAL_API_URL=http://api:8000
ENV NEXT_PUBLIC_API_URL=$NEXT_PUBLIC_API_URL \
    NEXT_PUBLIC_WS_URL=$NEXT_PUBLIC_WS_URL \
    NEXT_PUBLIC_OIDC_ISSUER=$NEXT_PUBLIC_OIDC_ISSUER \
    NEXT_PUBLIC_OIDC_CLIENT_ID=$NEXT_PUBLIC_OIDC_CLIENT_ID \
    INTERNAL_API_URL=$INTERNAL_API_URL \
    NODE_ENV=production
RUN npm run build
CMD ["npm", "run", "start"]
