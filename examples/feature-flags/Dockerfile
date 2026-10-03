# featuregate container image: the Rust OpenFeature (OFREP) service with
# Sigil's engine as WebAssembly. No Go runs in the image; Go only builds
# sigil.wasm, which the spechtlabs-sigil crate embeds.
#
# The build context is the repository root, not examples/feature-flags/: the
# image builds sigil.wasm from the Go sources and compiles the example against
# bindings/rust. Dockerfile.dockerignore next to this file keeps the context
# to those and the example. Build it from the root:
#
#   docker build -f examples/feature-flags/Dockerfile --build-arg VERSION=dev -t featuregate .

# sigil.wasm, the engine. WebAssembly is the same on every platform, so it's
# built once, on the build platform. Keep in lockstep with the go pin in
# ../../.mise.toml, and the flags with its wasm-build task.
FROM --platform=$BUILDPLATFORM golang:1.27.1 AS wasm

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY cmd/sigil-wasm/ ./cmd/sigil-wasm/
COPY internal/ ./internal/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=wasip1 GOARCH=wasm \
    go build -buildmode=c-shared -trimpath -ldflags='-s -w' -o /out/sigil.wasm ./cmd/sigil-wasm

# The service. Keep the tag in lockstep with the rust pin in ../../.mise.toml.
# trixie matches the runtime image's glibc, which the binary links against.
# This stage runs on the target platform, so the binary is native to it.
FROM rust:1.99.0-trixie AS build

WORKDIR /src

# The binding's build.rs embeds dist/wasm/sigil.wasm, where the repository's
# own `mise run wasm-build` puts it.
COPY --from=wasm /out/sigil.wasm ./dist/wasm/sigil.wasm
COPY bindings/rust/ ./bindings/rust/

COPY examples/feature-flags/ ./examples/feature-flags/
WORKDIR /src/examples/feature-flags
# The registry and target directory are cache mounts, so an edit recompiles
# only what changed; the binary is installed out of the mount before it ends.
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/usr/local/cargo/git \
    --mount=type=cache,target=/src/examples/feature-flags/target \
    cargo build --locked --release --bin featuregate && \
    install -D target/release/featuregate /out/featuregate

# distroless/cc: glibc, libgcc, CA certificates, tzdata and a nonroot user,
# and nothing else, not even a shell. The platform guardrail policy is
# compiled into the binary; flag policies come from a mounted directory
# (FEATUREGATE_POLICIES) or, without one, from the sample flags built into the binary.
FROM gcr.io/distroless/cc-debian13:nonroot

ARG VERSION=dev

LABEL org.opencontainers.image.title="featuregate"
LABEL org.opencontainers.image.source="https://github.com/spechtlabs/sigil"
LABEL org.opencontainers.image.description="featuregate is the example feature-flag service for the Sigil policy language: an OpenFeature (OFREP) server in Rust on its WebAssembly engine."
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.version="${VERSION}"

ENV FEATUREGATE_VERSION=${VERSION}

COPY --from=build /out/featuregate /usr/local/bin/featuregate

USER nonroot:nonroot

EXPOSE 8080

# The image has no HTTP client: `featuregate healthcheck` probes the server's
# own /readyz, for orchestrators that run it as an exec probe.
ENTRYPOINT ["/usr/local/bin/featuregate"]
CMD ["serve"]
