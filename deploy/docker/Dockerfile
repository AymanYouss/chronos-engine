# syntax=docker/dockerfile:1

# --- build stage ---------------------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -ldflags "-s -w -X github.com/AymanYouss/chronos-engine/internal/version.Version=${VERSION}" \
      -o /out/chronos-server ./cmd/chronos-server && \
    CGO_ENABLED=0 GOOS=linux go build \
      -ldflags "-s -w -X github.com/AymanYouss/chronos-engine/internal/version.Version=${VERSION}" \
      -o /out/chronos-worker ./cmd/chronos-worker && \
    CGO_ENABLED=0 GOOS=linux go build \
      -ldflags "-s -w -X github.com/AymanYouss/chronos-engine/internal/version.Version=${VERSION}" \
      -o /out/chronosctl ./cmd/chronosctl

# --- runtime stage -------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/chronos-server /usr/local/bin/chronos-server
COPY --from=build /out/chronos-worker /usr/local/bin/chronos-worker
COPY --from=build /out/chronosctl /usr/local/bin/chronosctl

USER nonroot:nonroot
EXPOSE 7233 8080 9090
ENTRYPOINT ["/usr/local/bin/chronos-server"]
