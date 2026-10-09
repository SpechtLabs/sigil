# alertrouter container image: the TypeScript service on Node, with Sigil's
# engine as WebAssembly. No Go runs in the image; Go only builds sigil.wasm.
#
# The build context is the repository root, not examples/alert-routing/: the
# image builds sigil.wasm from the Go sources and @spechtlabs/sigil from
# bindings/typescript. Dockerfile.dockerignore next to this file keeps the
# context to those and the example. Build it from the root:
#
#   docker build -f examples/alert-routing/Dockerfile --build-arg VERSION=dev -t alertrouter .

# sigil.wasm, the engine. WebAssembly is the same on every platform, so it's
# built once, on the build platform. Keep in lockstep with the go pin in
# ../../.mise.toml, and the flags with its wasm-build task.
FROM --platform=$BUILDPLATFORM golang:1.27.2 AS wasm

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

# Bun installs, runs the scripts and builds; Next's build itself runs on
# Node, the runtime it ships for. Keep the tags in lockstep with the bun and
# node pins in ../../.mise.toml. This stage runs on the target platform, not
# the build platform: the install picks platform-specific packages (sharp,
# which Next traces into the standalone output), so it must pick the
# target's.
FROM oven/bun:1.4.2 AS bun
FROM node:24.21.0-trixie-slim AS build
COPY --from=bun /usr/local/bin/bun /usr/local/bin/bun
ENV NEXT_TELEMETRY_DISABLED=1

WORKDIR /src

# @spechtlabs/sigil first: it is a file: dependency, which the example's
# install copies into node_modules as it is, so it must be built by then.
# The lockfiles come before the sources, so the install layers survive
# source edits.
COPY bindings/typescript/package.json bindings/typescript/bun.lock ./bindings/typescript/
RUN --mount=type=cache,target=/root/.bun/install/cache \
    bun install --cwd bindings/typescript --frozen-lockfile
COPY bindings/typescript/ ./bindings/typescript/
COPY --from=wasm /out/sigil.wasm ./dist/wasm/sigil.wasm
RUN bun run --cwd bindings/typescript build

# The example's postinstall (scripts/vendor-sigil.ts) turns the symlinks bun
# leaves for a file: dependency on Linux into copies, which Next's bundler
# needs, so the script comes in with the lockfile.
COPY examples/alert-routing/package.json examples/alert-routing/bun.lock ./examples/alert-routing/
COPY examples/alert-routing/scripts/vendor-sigil.ts ./examples/alert-routing/scripts/
RUN --mount=type=cache,target=/root/.bun/install/cache \
    bun install --cwd examples/alert-routing --frozen-lockfile
COPY examples/alert-routing/ ./examples/alert-routing/
RUN bun run --cwd examples/alert-routing build

# distroless/nodejs: Node, CA certificates, tzdata and a nonroot user, and
# nothing else, not even a shell. The platform policies and the default team
# bundle and directory are compiled into the app; team policies come from a
# mounted directory (ALERTROUTER_POLICIES) or, without one, from the built-in
# example teams.
FROM gcr.io/distroless/nodejs24-debian13:nonroot

ARG VERSION=dev

LABEL org.opencontainers.image.title="alertrouter"
LABEL org.opencontainers.image.source="https://github.com/spechtlabs/sigil"
LABEL org.opencontainers.image.description="alertrouter is the example alert-routing service for the Sigil policy language, in TypeScript on its WebAssembly engine."
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.version="${VERSION}"

ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    ALERTROUTER_VERSION=${VERSION}

WORKDIR /app

# The standalone server with the node_modules Next traced for it. The build
# (scripts/assemble-standalone.ts) has already put the browser assets Next
# doesn't copy itself and the entry point and healthcheck from bin/ next to
# server.js, the same layout `bun run start` runs locally. WORKDIR matters:
# the service finds sigil.wasm under node_modules from the working directory.
COPY --from=build /src/examples/alert-routing/.next/standalone/ ./
# The profiler's native addon: node-gyp-build finds the prebuilt binary for
# the platform at run time, so Next's tracer, which follows imports, leaves
# the prebuilds out.
COPY --from=build /src/examples/alert-routing/node_modules/@datadog/pprof/ ./node_modules/@datadog/pprof/

USER nonroot:nonroot

EXPOSE 8080

# alertrouter.mjs maps ALERTROUTER_ADDR to the PORT and HOSTNAME Next's
# server reads and leaves SIGTERM to the service, which drains and flushes
# telemetry before it exits. The image's entrypoint is node.
CMD ["alertrouter.mjs"]
