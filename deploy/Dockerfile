# syntax=docker/dockerfile:1

FROM node:22-bookworm AS frontend
WORKDIR /src/frontend

COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.25-bookworm AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=frontend /src/frontend/dist ./frontend/dist

RUN rm -rf internal/webui/dist \
    && mkdir -p internal/webui/dist \
    && cp -R frontend/dist/. internal/webui/dist/

ARG IPM_VERSION=2.1.2
ARG IPM_COMMIT=unknown
ARG IPM_BUILT_AT
RUN if [ -z "$IPM_BUILT_AT" ]; then IPM_BUILT_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"; fi \
    && CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X icloud-privacy-mail-v2/internal/buildinfo.Version=${IPM_VERSION} -X icloud-privacy-mail-v2/internal/buildinfo.Commit=${IPM_COMMIT} -X icloud-privacy-mail-v2/internal/buildinfo.BuiltAt=${IPM_BUILT_AT}" \
      -o /out/ipm-server .

FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates wget \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system --gid 10001 ipm \
    && useradd --system --uid 10001 --gid 10001 --home-dir /app --shell /usr/sbin/nologin ipm

WORKDIR /app
COPY --from=builder /out/ipm-server /app/bin/ipm-server
COPY deploy/config.container.example.json /app/config.container.example.json
RUN mkdir -p /app/data \
    && chown -R ipm:ipm /app

USER ipm
EXPOSE 8788
ENTRYPOINT ["/app/bin/ipm-server", "-config", "/app/config.json"]
