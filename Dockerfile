# Build release-lag as a static binary and ship it on a base with nothing
# else on it. It needs no writable path, so it runs with a read-only root
# filesystem.

FROM golang:1.27 AS build
WORKDIR /src

COPY go.mod go.sum ./
COPY cmd/ cmd/
COPY internal/ internal/

# CGO off makes the result static, which is what lets it run on a base image
# with no libc. The caches are mounts rather than layers so that rebuilding
# after an edit does not start from an empty module cache.
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/release-lag ./cmd/release-lag

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /out/release-lag /release-lag
# 65532 is distroless's "nonroot"; the Deployment names the same number so the
# manifest and the image cannot disagree about who this runs as.
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/release-lag"]
