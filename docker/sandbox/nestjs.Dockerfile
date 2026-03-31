# Sandbox image for NestJS coding agent
FROM node:20-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
    git curl jq \
    && rm -rf /var/lib/apt/lists/*

RUN npm install -g \
    @nestjs/cli \
    typescript \
    ts-node \
    prisma \
    eslint \
    prettier \
    npm-check-updates

RUN mkdir -p /workspace
WORKDIR /workspace
