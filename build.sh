#!/usr/bin/env bash
# build.sh – Hopscotch builder script
# Usage:
#   ./build.sh binary          # local binary for the current platform
#   ./build.sh binary-all      # binaries for all platforms
#   ./build.sh container       # multiarch Docker image (local only)
#   ./build.sh publish         # build multiarch Docker image and push to registry
#   ./build.sh release         # binary-all + publish
#   ./build.sh clean           # remove dist/

set -euo pipefail

# ── Configuration ─────────────────────────────────────────────────────────────
BINARY_NAME="hopscotch"
REGISTRY="ghcr.io"
GITHUB_USER="${GITHUB_USER:-pottom}"
IMAGE_REPO="${REGISTRY}/${GITHUB_USER}/${BINARY_NAME}"
DIST_DIR="dist"

# ── Version ───────────────────────────────────────────────────────────────────
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS="-s -w \
  -X hopscotch/internal/version.Version=${VERSION} \
  -X hopscotch/internal/version.GitCommit=${GIT_COMMIT} \
  -X hopscotch/internal/version.BuildDate=${BUILD_DATE}"

# ── Docker tag generation ──────────────────────────────────────────────────────
generate_tags() {
    local version="$1"
    local tags=("${IMAGE_REPO}:latest" "${IMAGE_REPO}:${version}")

    if [[ "$version" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+) ]]; then
        tags+=("${IMAGE_REPO}:v${BASH_REMATCH[1]}")
        tags+=("${IMAGE_REPO}:v${BASH_REMATCH[1]}.${BASH_REMATCH[2]}")
    fi

    printf '%s\n' "${tags[@]}"
}

# ── Commands ──────────────────────────────────────────────────────────────────
cmd_binary() {
    echo "▶ Building binary for current platform (${VERSION})..."
    mkdir -p "${DIST_DIR}"
    go build -ldflags="${LDFLAGS}" -o "${DIST_DIR}/${BINARY_NAME}" .
    echo "✓ ${DIST_DIR}/${BINARY_NAME}"
}

cmd_binary_all() {
    echo "▶ Building binaries for all platforms (${VERSION})..."
    mkdir -p "${DIST_DIR}"

    local platforms=(
        "darwin/arm64"
        "darwin/amd64"
        "linux/amd64"
        "linux/arm64"
        "windows/amd64"
    )

    for platform in "${platforms[@]}"; do
        local GOOS="${platform%/*}"
        local GOARCH="${platform#*/}"
        local output="${DIST_DIR}/${BINARY_NAME}-${GOOS}-${GOARCH}"
        [[ "$GOOS" == "windows" ]] && output="${output}.exe"

        echo "  → ${GOOS}/${GOARCH}"
        CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" \
            go build -ldflags="${LDFLAGS}" -o "${output}" .
    done

    echo "✓ Binaries in ${DIST_DIR}/"
    ls -lh "${DIST_DIR}/"
}

cmd_container() {
    echo "▶ Building multiarch container image (${VERSION}, local only)..."

    docker buildx inspect hopscotch-builder &>/dev/null || \
        docker buildx create --name hopscotch-builder --use

    local TAGS
    TAGS=$(generate_tags "${VERSION}")
    local TAG_ARGS
    TAG_ARGS=$(echo "$TAGS" | sed 's/^/-t /' | tr '\n' ' ')

    # shellcheck disable=SC2086
    docker buildx build \
        --platform linux/amd64,linux/arm64 \
        --build-arg VERSION="${VERSION}" \
        --build-arg GIT_COMMIT="${GIT_COMMIT}" \
        --build-arg BUILD_DATE="${BUILD_DATE}" \
        ${TAG_ARGS} \
        --load \
        -f deploy/Dockerfile \
        .

    echo "✓ Container images built:"
    echo "$TAGS" | sed 's/^/  /'
}

cmd_publish() {
    echo "▶ Building and pushing multiarch container image (${VERSION})..."

    echo "${GITHUB_TOKEN:?GITHUB_TOKEN is required}" | \
        docker login "${REGISTRY}" -u "${GITHUB_USER}" --password-stdin

    docker buildx inspect hopscotch-builder &>/dev/null || \
        docker buildx create --name hopscotch-builder --use

    local TAGS
    TAGS=$(generate_tags "${VERSION}")
    local TAG_ARGS
    TAG_ARGS=$(echo "$TAGS" | sed 's/^/-t /' | tr '\n' ' ')

    # shellcheck disable=SC2086
    docker buildx build \
        --platform linux/amd64,linux/arm64 \
        --build-arg VERSION="${VERSION}" \
        --build-arg GIT_COMMIT="${GIT_COMMIT}" \
        --build-arg BUILD_DATE="${BUILD_DATE}" \
        ${TAG_ARGS} \
        --push \
        -f deploy/Dockerfile \
        .

    echo "✓ Published tags:"
    echo "$TAGS" | sed 's/^/  /'
}

cmd_release() {
    cmd_binary_all
    cmd_publish
}

cmd_clean() {
    rm -rf "${DIST_DIR}"
    echo "✓ Cleaned ${DIST_DIR}/"
}

# ── Entrypoint ────────────────────────────────────────────────────────────────
case "${1:-help}" in
    binary)      cmd_binary ;;
    binary-all)  cmd_binary_all ;;
    container)   cmd_container ;;
    publish)     cmd_publish ;;
    release)     cmd_release ;;
    clean)       cmd_clean ;;
    *)
        echo "Hopscotch Builder ${VERSION}"
        echo ""
        echo "Usage: ./build.sh <command>"
        echo ""
        echo "Commands:"
        echo "  binary       Build local binary for the current platform"
        echo "  binary-all   Build binaries for all platforms (dist/)"
        echo "  container    Build multiarch Docker image (local, no push)"
        echo "  publish      Build multiarch Docker image and push to registry"
        echo "  release      binary-all + publish"
        echo "  clean        Remove dist/ directory"
        echo ""
        echo "Environment variables for publish:"
        echo "  GITHUB_USER    GitHub username (default: baistvan)"
        echo "  GITHUB_TOKEN   GitHub Personal Access Token (write:packages scope)"
        ;;
esac
