# Sandbox image for QA agent — includes browsers for E2E testing
FROM mcr.microsoft.com/playwright:v1.48.0-noble

RUN apt-get update && apt-get install -y --no-install-recommends \
    git curl jq \
    && rm -rf /var/lib/apt/lists/*

RUN npm install -g \
    jest \
    @jest/globals \
    ts-jest \
    typescript \
    supertest \
    @playwright/test \
    k6

RUN mkdir -p /workspace
WORKDIR /workspace
