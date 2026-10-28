# syntax=docker/dockerfile:1

# One image for both the Home Assistant add-on and plain Docker, built for
# amd64 and arm64. Both build stages run on the build machine and
# cross-compile, so multi-arch builds need no emulation.

FROM --platform=$BUILDPLATFORM node:26.12.1-alpine3.24 AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.24 AS server
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/homedrive ./cmd/homedrive

FROM alpine:3.24.3
ARG VERSION=dev
RUN apk add --no-cache ca-certificates tzdata
COPY --from=server /out/homedrive /usr/local/bin/homedrive
COPY --from=web /src/web/dist /app/web

# No USER here: Home Assistant mounts /data and /share root-owned, so the
# add-on runs as root inside its confined container. Plain Docker should run
# it as a normal user (see docker-compose.example.yml).
ENV HOMEDRIVE_WEB_DIR=/app/web
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/usr/local/bin/homedrive", "healthcheck"]

LABEL org.opencontainers.image.title="Homedrive" \
      org.opencontainers.image.description="Self-hosted online drive for household documents" \
      org.opencontainers.image.source="https://github.com/BertVanBaelen/HomeDrive" \
      org.opencontainers.image.version="${VERSION}" \
      io.hass.type="addon" \
      io.hass.version="${VERSION}"

ENTRYPOINT ["/usr/local/bin/homedrive"]
