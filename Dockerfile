# @note all COPY commands are restricted to list of files in .dockerignore file.

# Fail when Go or mise versions are not supplied by CI or Taskfile.
ARG GO_VERSION=INVALID
ARG MISE_VERSION=INVALID

# For build stage we use standard debian version of image.
# --platform=$BUILDPLATFORM ensures cross-compilation using `go build` instead of QEMU.
# @see https://www.docker.com/blog/faster-multi-platform-builds-dockerfile-cross-compilation-guide
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS builder

# Passed by buildx for cross-compilation.
ARG TARGETOS
ARG TARGETARCH

# Release metadata embedded in the application binary.
ARG COMMIT_HASH
ARG RELEASE_TAG

# Supplied from etc/mise.toml by CI or Taskfile.
ARG GOPLS_VERSION

# Use deterministic timestamps for reproducible builds.
# @see https://reproducible-builds.org/docs/source-date-epoch/
ARG SOURCE_DATE_EPOCH=0
ENV SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH}

# CGO is disabled by default for this build.
# Builds that require CGO must enable it and provide:
#   * the required C toolchain and dependencies in the builder stage
#   * any required runtime dependencies in the packaging stage
ENV CGO_ENABLED=0

# Use bash by default and fail on any command in a pipe that fails.
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

WORKDIR /tmp/build

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod,id=gomodcache go mod download

# Install gopls before copying application source to preserve this layer.
RUN --mount=type=cache,target=/go/pkg/mod,id=gomodcache \
    --mount=type=cache,target=/root/.cache/go-build,id=gobuildcache \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go install golang.org/x/tools/gopls@v${GOPLS_VERSION} \
    && install -D "$(find /go/bin -type f -name gopls)" /out/gopls

COPY . .

# Build.
#
# `docker buildx` provides TARGETOS and TARGETARCH for cross-compilation. These values
# must be passed to the Go toolchain explicitly as GOOS and GOARCH.
#
# -v prints package names as they are compiled.
#
# -trimpath removes file system paths from the binary, improving build reproducibility.
#
# -buildvcs=false omits automatically discovered VCS metadata, improving build reproducibility.
#
# -s -w omits the symbol table and DWARF debug information, reducing binary size
# but limiting debugger inspection.
#
# -X sets xBuildVersion and xBuildCommit in internal/buildinfo to the selected release identity.
#
RUN --mount=type=cache,target=/go/pkg/mod,id=gomodcache \
    --mount=type=cache,target=/root/.cache/go-build,id=gobuildcache \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -v -trimpath -buildvcs=false \
    -ldflags "-s -w -X github.com/go42-dev/go42x/internal/buildinfo.xBuildVersion=${RELEASE_TAG} -X github.com/go42-dev/go42x/internal/buildinfo.xBuildCommit=${COMMIT_HASH}" \
    -o /out/go42x .

# Validate binary.
RUN --mount=type=cache,target=/root/.cache/go-build,id=gobuildcache \
    readelf -h /out/go42x && du -h /out/go42x && sha256sum /out/go42x && go tool buildid /out/go42x

# ---

# Copy the target architecture's static mise binary without executing it.
FROM ghcr.io/jdx/mise:${MISE_VERSION} AS mise

# We need Go runtime in final image to run tools like gopls.
FROM golang:${GO_VERSION}-alpine3.24

# Install runtime tools and CGO dependencies for projects inspected by gopls.
# @see https://pkgs.alpinelinux.org/packages?branch=v3.24
RUN apk add --no-cache \
    bash=5.3.9-r1 \
    ca-certificates=20261020-r0 \
    curl=8.22.0-r0 \
    gcc=15.2.0-r5 \
    git=2.54.0-r0 \
    libcrypto3=3.5.9-r0 \
    libssl3=3.5.9-r0 \
    musl-dev=1.2.6-r3 \
    tini=0.19.0-r3 \
    tzdata=2026e-r0 \
    zlib=1.3.2-r1

# Match GitHub's hosted Linux runner so mounted instructions and indexes remain accessible.
RUN addgroup -g 1001 appuser && \
    adduser -u 1001 -G appuser -s /bin/sh -D appuser

# Copy stable runtime files before the application binary.
COPY --from=mise /usr/local/bin/mise /usr/local/bin/mise
COPY --from=builder /out/gopls /usr/local/bin/

# Forward signals and reap orphaned child processes.
ENTRYPOINT ["/sbin/tini", "--"]

USER 1001:1001
WORKDIR /home/appuser

# Keep the changing binary last so runtime files stay cached across builds.
COPY --from=builder /out/go42x /usr/local/bin/go42x
CMD ["/usr/local/bin/go42x"]
