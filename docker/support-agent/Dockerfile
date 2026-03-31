# Support Agent — handles spec-writer, tech-lead, verifier
# Lightweight: only needs git for browsing repos
FROM golang:1.22-alpine AS builder
RUN apk add --no-cache git
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/worker ./cmd/support-agent

FROM alpine:3.20
RUN apk add --no-cache ca-certificates git
COPY --from=builder /bin/worker /bin/worker
ENTRYPOINT ["/bin/worker"]
