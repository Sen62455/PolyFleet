# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src
RUN corepack enable && corepack prepare pnpm@11.16.0 --activate
COPY web ./web
COPY internal/webui ./internal/webui
RUN pnpm --dir web install --frozen-lockfile && pnpm --dir web build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG TARGETOS
ARG TARGETARCH
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -tags webui -trimpath \
    -ldflags="-s -w -X github.com/Sen62455/PolyFleet/internal/buildinfo.Version=${VERSION} -X github.com/Sen62455/PolyFleet/internal/buildinfo.Commit=${COMMIT} -X github.com/Sen62455/PolyFleet/internal/buildinfo.Date=${BUILD_DATE}" \
    -o /out/hyfleet-server ./cmd/server

FROM alpine:3.23
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.title="PolyFleet" \
      org.opencontainers.image.description="Lightweight multi-protocol proxy fleet control plane" \
      org.opencontainers.image.source="https://github.com/Sen62455/PolyFleet" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.licenses="Apache-2.0"
RUN apk add --no-cache ca-certificates && \
    addgroup -S -g 10001 hyfleet && \
    adduser -S -D -H -u 10001 -G hyfleet hyfleet && \
    mkdir /data && chown hyfleet:hyfleet /data && chmod 0700 /data
COPY --from=build /out/hyfleet-server /usr/local/bin/hyfleet-server
COPY docker/server.yaml /etc/hyfleet/server.yaml
USER 10001:10001
ENV HYFLEET_LISTEN=0.0.0.0:8080 \
    HYFLEET_DATABASE_PATH=/data/server.db \
    HYFLEET_MASTER_KEY_FILE=/data/master.key
VOLUME ["/data"]
EXPOSE 8080
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=4 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/hyfleet-server"]
CMD ["-config", "/etc/hyfleet/server.yaml"]
