# syntax=docker/dockerfile:1

# --- Stage 1: build the embedded web UI ---
FROM node:20-alpine AS web
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm ci
COPY frontend/ ./
# Vite writes to ../internal/web/static (see frontend/vite.config.*), so create
# the parent and build; the assets land in /src/internal/web/static.
RUN mkdir -p /src/internal/web && npm run build

# --- Stage 2: build the Go binary with the embedded UI ---
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Overwrite the checked-in static assets with the freshly built ones.
COPY --from=web /src/internal/web/static/ ./internal/web/static/
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/codex-bridge .

# --- Stage 3: minimal runtime ---
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /opt/codex-bridge
COPY --from=build /out/codex-bridge /usr/local/bin/codex-bridge
# Mount your config at /opt/codex-bridge/configs and data at /opt/codex-bridge/data.
ENV APP_ENV=prod \
    APP_HOST=0.0.0.0 \
    APP_PORT=8088
EXPOSE 8088
# Override the mode with `docker run ... bridge` to run a Bridge instead.
ENTRYPOINT ["/usr/local/bin/codex-bridge"]
CMD ["hub"]
