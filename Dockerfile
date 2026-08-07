# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26.5

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /src
COPY go.mod ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/

RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/batchscope ./cmd/batchscope

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/batchscope /batchscope

ENV PORT=8080 \
    BATCHSCOPE_DATA_DIR=/tmp/batchscope
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/batchscope"]
CMD ["serve"]
