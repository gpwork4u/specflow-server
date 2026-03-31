# Sandbox image for Frontend coding agent
FROM node:20-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
    git curl jq \
    && rm -rf /var/lib/apt/lists/*

RUN corepack enable && corepack prepare pnpm@latest --activate

RUN npm install -g \
    vite \
    create-vite \
    typescript \
    eslint \
    prettier \
    tailwindcss \
    postcss \
    autoprefixer

RUN mkdir -p /workspace
WORKDIR /workspace
