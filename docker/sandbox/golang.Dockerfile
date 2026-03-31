# Sandbox image for Golang coding agent
# This is the EPHEMERAL container that gets created per-task
FROM golang:1.22-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
    git curl make gcc jq \
    && rm -rf /var/lib/apt/lists/*

RUN go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest && \
    go install golang.org/x/tools/cmd/goimports@latest && \
    go install github.com/go-delve/delve/cmd/dlv@latest && \
    go install gotest.tools/gotestsum@latest

RUN mkdir -p /workspace
WORKDIR /workspace
