# deploygate container image.
#
# The build context is the repository root, not examples/: the examples module
# replaces github.com/spechtlabs/sigil with ../, so the build needs the library
# sources next to it. Build it from the root:
#
#   docker build -f examples/Dockerfile --build-arg VERSION=dev -t deploygate .

# Keep in lockstep with the toolchain directive in examples/go.mod.
FROM --platform=$BUILDPLATFORM golang:1.27.1 AS builder

WORKDIR /src

# The module files first, so the download layer survives source edits. Both
# go.mod files are needed: the replace directive makes the root module part of
# the examples' build list.
COPY go.mod go.sum ./
COPY examples/go.mod examples/go.sum ./examples/
WORKDIR /src/examples
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Only the library packages the examples import, and the examples themselves.
WORKDIR /src
COPY pkg/ ./pkg/
COPY internal/ ./internal/
COPY examples/ ./examples/

ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH

# Cross-compiled on the build platform, so a multi-arch build doesn't run the
# Go toolchain under emulation. CGO is off because the runtime image has no libc.
WORKDIR /src/examples
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/deploygate ./cmd/deploygate

# distroless/static: CA certificates, tzdata and a nonroot user, and nothing
# else, not even a shell. The platform policies are embedded in the binary; team
# policies come from a mounted directory (see --policies).
FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev

LABEL org.opencontainers.image.title="deploygate"
LABEL org.opencontainers.image.source="https://github.com/spechtlabs/sigil"
LABEL org.opencontainers.image.description="deploygate is the example deploy-approval service for the Sigil policy language."
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.version="${VERSION}"

COPY --from=builder /out/deploygate /deploygate

USER nonroot:nonroot

EXPOSE 8080

ENTRYPOINT ["/deploygate"]
CMD ["serve"]
