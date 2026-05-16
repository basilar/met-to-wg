# syntax=docker/dockerfile:1.7

FROM golang:1.26-alpine AS build

RUN apk add --no-cache ca-certificates

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/met-to-wg ./cmd/met-to-wg

RUN mkdir -p /out/data && chown 65534:65534 /out/data

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/met-to-wg /met-to-wg
COPY --from=build /out/data /data

USER 65534:65534

ENTRYPOINT ["/met-to-wg"]
