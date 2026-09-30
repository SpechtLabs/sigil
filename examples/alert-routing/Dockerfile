# alertrouter container image.
#
# The build context is the repository root, not examples/alert-routing/: the
# module replaces github.com/spechtlabs/sigil with ../../, so the build needs
# the library sources next to it. Build it from the root:
#
#   docker build -f examples/alert-routing/Dockerfile --build-arg VERSION=dev -t alertrouter .

# Keep in lockstep with the toolchain directive in examples/alert-routing/go.mod.
FROM --platform=$BUILDPLATFORM golang:1.27.1 AS builder

WORKDIR /src

# The module files first, so the download layer survives source edits. Both
# go.mod files are needed: the replace directive makes the root module part of
# the example's build list.
COPY go.mod go.sum ./
COPY examples/alert-routing/go.mod examples/alert-routing/go.sum ./examples/alert-routing/
WORKDIR /src/examples/alert-routing
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Only the library packages the example imports, and the example itself.
WORKDIR /src
COPY pkg/ ./pkg/
COPY internal/ ./internal/
COPY examples/alert-routing/ ./examples/alert-routing/

ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH

# Cross-compiled on the build platform, so a multi-arch build doesn't run the
# Go toolchain under emulation. CGO is off because the runtime image has no libc.
WORKDIR /src/examples/alert-routing
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/alertrouter ./cmd/alertrouter

# distroless/static: CA certificates, tzdata and a nonroot user, and nothing
# else, not even a shell. The platform policies and the default team directory
# are embedded in the binary; team policies come from a mounted directory (see
# --policies) or, without one, from the embedded example teams.
FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev

LABEL org.opencontainers.image.title="alertrouter"
LABEL org.opencontainers.image.source="https://github.com/spechtlabs/sigil"
LABEL org.opencontainers.image.description="alertrouter is the example alert-routing service for the Sigil policy language."
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.version="${VERSION}"

COPY --from=builder /out/alertrouter /alertrouter

USER nonroot:nonroot

EXPOSE 8080

ENTRYPOINT ["/alertrouter"]
CMD ["serve"]
