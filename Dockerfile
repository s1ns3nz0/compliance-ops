# syntax=docker/dockerfile:1
#
# compliance-ops: single Go binary that serves the HTTP API and the embedded
# React UI. Three stages: build the web bundle, build the Go binary with the
# bundle embedded, then copy the static binary into a distroless image.
#
# Build:  docker build -t compliance-ops:dev .
# Run the complete non-root stack with Docker Compose. Its one-shot token-prep
# service copies the protected host token file into an app-readable named volume.

# ---- Stage 1: frontend -----------------------------------------------------
FROM node:22-alpine AS web

WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY web/ ./
# vite.config.ts writes to ../internal/webui/dist, i.e. /src/internal/webui/dist.
RUN npm run build

# ---- Stage 2: Go binary ----------------------------------------------------
FROM golang:1.26-alpine AS build

# go.mod pins a patch release; let the toolchain download a newer one if the
# image's Go is ever older than what go.mod requires.
ENV GOTOOLCHAIN=auto CGO_ENABLED=0 GOFLAGS=-mod=readonly

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Replace the tracked placeholder with the real bundle before embedding.
COPY --from=web /src/internal/webui/dist ./internal/webui/dist

RUN go build -trimpath -ldflags='-s -w' -o /out/compliance-ops ./cmd/server

# ---- Stage 3: runtime ------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

# The server writes evidence uploads to a temp file (os.CreateTemp) before
# streaming them to object storage. With a read-only root filesystem, /tmp
# must be a writable mount (tmpfs in compose, emptyDir in Kubernetes).
ENV TMPDIR=/tmp PORT=3000

COPY --from=build /out/compliance-ops /compliance-ops

EXPOSE 3000
USER nonroot:nonroot
ENTRYPOINT ["/compliance-ops"]
