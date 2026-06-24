# --- Build stage ---
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

ARG TARGETPLATFORM
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
    -ldflags="-s -w \
      -X hopscotch/internal/version.Version=${VERSION} \
      -X hopscotch/internal/version.GitCommit=${GIT_COMMIT} \
      -X hopscotch/internal/version.BuildDate=${BUILD_DATE}" \
    -o /hopscotch .

# --- Final stage ---
FROM alpine:3.21

# WITH_VPN=true installs openconnect for VPN-managed deployments.
# The VPN-enabled image also requires --cap-add NET_ADMIN and
# --device /dev/net/tun at runtime (see docker-compose.yml).
ARG WITH_VPN=false

RUN apk add --no-cache ca-certificates tzdata && \
    if [ "$WITH_VPN" = "true" ]; then \
      apk add --no-cache openconnect; \
    fi && \
    addgroup -g 1000 hopscotch && \
    adduser -u 1000 -G hopscotch -s /bin/sh -D hopscotch

# Mount points for config and SSH keys
RUN mkdir -p /etc/hopscotch/keys && \
    chown -R hopscotch:hopscotch /etc/hopscotch

COPY --from=builder /hopscotch /usr/local/bin/hopscotch

# Signals the state manager to use ephemeral /tmp storage
ENV HOPSCOTCH_CONTAINER=true

USER hopscotch

# SOCKS5 proxy port
EXPOSE 8080
# Admin HTTP port
EXPOSE 9090

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost:9090/health || exit 1

ENTRYPOINT ["hopscotch", "start", "--foreground"]
CMD ["--config", "/etc/hopscotch/config.yaml"]
