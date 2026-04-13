FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

ARG TARGETOS TARGETARCH TARGETVARIANT
ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    GOARM=${TARGETVARIANT#v} \
    go build -ldflags="-s -w \
      -X github.com/LamGC/tailscale-metrics-discovery-agent/internal/version.Version=${VERSION} \
      -X github.com/LamGC/tailscale-metrics-discovery-agent/internal/version.Commit=${COMMIT}" \
    -o /tsd ./cmd/tsd

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /tsd /usr/bin/tsd

RUN mkdir -p /etc/tsd

ENTRYPOINT ["tsd"]
